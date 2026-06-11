package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoadBalancedClient_RoundRobin(t *testing.T) {
	var received []string
	mu := &sync.Mutex{} // actually not needed for this simple test but ok

	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/detect" {
			json.NewEncoder(w).Encode(detectResponse{Spans: []Span{{Label: "ip", Text: "10.0.0.1"}}})
		} else {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
		}
		mu.Lock()
		received = append(received, "s1")
		mu.Unlock()
	}))
	defer s1.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/detect" {
			json.NewEncoder(w).Encode(detectResponse{Spans: []Span{{Label: "ip", Text: "10.0.0.2"}}})
		} else {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
		}
		mu.Lock()
		received = append(received, "s2")
		mu.Unlock()
	}))
	defer s2.Close()

	lb := NewLoadBalancedClient(s1.URL+","+s2.URL, RoundRobin)
	defer lb.Close()

	// Give health checks a moment to mark backends healthy.
	time.Sleep(100 * time.Millisecond)

	// Two calls should hit both servers.
	for i := 0; i < 2; i++ {
		_, err := lb.Detect(context.Background(), "test")
		if err != nil {
			t.Fatalf("detect %d: %v", i, err)
		}
	}

	mu.Lock()
	if len(received) < 2 {
		t.Fatalf("expected at least 2 requests, got %d", len(received))
	}
	mu.Unlock()
}

func TestLoadBalancedClient_Failover(t *testing.T) {
	// s1 always fails.
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer s1.Close()

	// s2 succeeds.
	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/detect" {
			json.NewEncoder(w).Encode(detectResponse{Spans: []Span{{Label: "ip", Text: "10.0.0.1"}}})
		} else {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
		}
	}))
	defer s2.Close()

	lb := NewLoadBalancedClient(s1.URL+","+s2.URL, RoundRobin)
	defer lb.Close()

	// s1 is marked unhealthy by health check after first failure,
	// but we force it here for deterministic test.
	for _, b := range lb.backends {
		if strings.Contains(b.endpoint, s1.URL[7:]) { // strip http://
			b.healthy.Store(false)
		}
	}

	spans, err := lb.Detect(context.Background(), "test")
	if err != nil {
		t.Fatalf("expected failover to s2, got error: %v", err)
	}
	if len(spans) != 1 || spans[0].Text != "10.0.0.1" {
		t.Fatalf("unexpected result: %v", spans)
	}
}

func TestLoadBalancedClient_AllDown(t *testing.T) {
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer s1.Close()

	lb := NewLoadBalancedClient(s1.URL, RoundRobin)
	defer lb.Close()

	for _, b := range lb.backends {
		b.healthy.Store(false)
	}

	_, err := lb.Detect(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error when all backends down")
	}
	if !strings.Contains(err.Error(), "all privacy backends failed") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestLoadBalancedClient_HealthCheckRecovery(t *testing.T) {
	var failures atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" && failures.Load() > 0 {
			failures.Add(-1)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
	}))
	defer srv.Close()

	// Disable auto health checks so we control timing exactly.
	lb := NewLoadBalancedClientWithOptions(srv.URL, RoundRobin, nil, []LoadBalancedClientOption{
		WithAutoHealthCheck(false),
	})
	defer lb.Close()

	// Initial check (triggered by startHealthChecks) should pass.
	if !lb.backends[0].healthy.Load() {
		t.Fatal("expected initially healthy")
	}

	// Force failure.
	failures.Store(1)
	lb.runHealthChecks(false)
	if lb.backends[0].healthy.Load() {
		t.Fatal("expected unhealthy after forced failure")
	}

	// Recover.
	failures.Store(0)
	lb.runHealthChecks(false)
	if !lb.backends[0].healthy.Load() {
		t.Fatal("expected recovered")
	}
}

func TestLoadBalancedClient_LeastLatency(t *testing.T) {
	// s1 is fast, s2 is slow.
	var s1Calls, s2Calls atomic.Int32

	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s1Calls.Add(1)
		if r.URL.Path == "/detect" {
			json.NewEncoder(w).Encode(detectResponse{Spans: []Span{{Label: "ip", Text: "10.0.0.1"}}})
		} else {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
		}
	}))
	defer s1.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s2Calls.Add(1)
		if r.URL.Path == "/detect" {
			time.Sleep(50 * time.Millisecond)
			json.NewEncoder(w).Encode(detectResponse{Spans: []Span{{Label: "ip", Text: "10.0.0.2"}}})
		} else {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
		}
	}))
	defer s2.Close()

	lb := NewLoadBalancedClientWithOptions(s1.URL+","+s2.URL, LeastLatency, nil, []LoadBalancedClientOption{
		WithAutoHealthCheck(false),
	})
	defer lb.Close()

	// Seed EWMA: s1 gets low latency, s2 gets high latency.
	lb.backends[0].latencyNs.Store(int64(1 * time.Millisecond))
	lb.backends[1].latencyNs.Store(int64(100 * time.Millisecond))

	// 5 calls should all go to s1 (lowest latency).
	for i := 0; i < 5; i++ {
		_, err := lb.Detect(context.Background(), "test")
		if err != nil {
			t.Fatalf("detect %d: %v", i, err)
		}
	}

	if s1Calls.Load() != 5 {
		t.Fatalf("expected 5 calls to s1, got %d", s1Calls.Load())
	}
	if s2Calls.Load() != 0 {
		t.Fatalf("expected 0 calls to s2, got %d", s2Calls.Load())
	}
}

func TestLoadBalancedClient_DetectBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/detect/batch" {
			var req batchDetectRequest
			json.NewDecoder(r.Body).Decode(&req)
			results := make([]batchDetectResult, len(req.Texts))
			for i := range req.Texts {
				results[i] = batchDetectResult{Spans: []Span{{Label: "person", Text: req.Texts[i]}}}
			}
			json.NewEncoder(w).Encode(batchDetectResponse{Results: results})
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
	}))
	defer srv.Close()

	lb := NewLoadBalancedClientWithOptions(srv.URL, RoundRobin, nil, []LoadBalancedClientOption{
		WithAutoHealthCheck(false),
	})
	defer lb.Close()

	results, err := lb.DetectBatch(context.Background(), []string{"Alice", "Bob"})
	if err != nil {
		t.Fatalf("DetectBatch failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if len(results[0]) != 1 || results[0][0].Text != "Alice" {
		t.Errorf("unexpected result[0]: %v", results[0])
	}
}

func TestLoadBalancedClient_DetectBatch_Failover(t *testing.T) {
	// s1 fails batch, s2 succeeds.
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/detect/batch" {
			http.Error(w, "overload", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
	}))
	defer s1.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/detect/batch" {
			var req batchDetectRequest
			json.NewDecoder(r.Body).Decode(&req)
			results := make([]batchDetectResult, len(req.Texts))
			for i := range req.Texts {
				results[i] = batchDetectResult{Spans: []Span{{Label: "person", Text: req.Texts[i]}}}
			}
			json.NewEncoder(w).Encode(batchDetectResponse{Results: results})
			return
		}
		if r.URL.Path == "/detect" {
			json.NewEncoder(w).Encode(detectResponse{Spans: []Span{{Label: "person", Text: "Alice"}}})
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
	}))
	defer s2.Close()

	lb := NewLoadBalancedClientWithOptions(s1.URL+","+s2.URL, RoundRobin, nil, []LoadBalancedClientOption{
		WithAutoHealthCheck(false),
	})
	defer lb.Close()

	// Mark s1 unhealthy so s2 is chosen first.
	lb.backends[0].healthy.Store(false)

	results, err := lb.DetectBatch(context.Background(), []string{"Alice"})
	if err != nil {
		t.Fatalf("expected failover to s2, got error: %v", err)
	}
	if len(results) != 1 || len(results[0]) != 1 || results[0][0].Text != "Alice" {
		t.Fatalf("unexpected result: %v", results)
	}
}

func TestLoadBalancedClient_StatsLatency(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/detect" {
			json.NewEncoder(w).Encode(detectResponse{Spans: []Span{{Label: "ip", Text: "10.0.0.1"}}})
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
	}))
	defer srv.Close()

	lb := NewLoadBalancedClientWithOptions(srv.URL, RoundRobin, nil, []LoadBalancedClientOption{
		WithAutoHealthCheck(false),
	})
	defer lb.Close()

	_, err := lb.Detect(context.Background(), "test")
	if err != nil {
		t.Fatalf("detect failed: %v", err)
	}

	stats := lb.Stats()
	if len(stats) != 1 {
		t.Fatalf("expected 1 stat, got %d", len(stats))
	}
	if stats[0].LatencyMs < 0 {
		t.Fatalf("expected non-negative latency, got %d", stats[0].LatencyMs)
	}
}

func TestSplitEndpoints(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"http://a:8080", 1},
		{"http://a:8080,http://b:8080", 2},
		{" http://a:8080 , http://b:8080 ", 2},
	}
	for _, tt := range tests {
		got := splitEndpoints(tt.input)
		if len(got) != tt.want {
			t.Errorf("splitEndpoints(%q) = %d, want %d", tt.input, len(got), tt.want)
		}
	}
}
