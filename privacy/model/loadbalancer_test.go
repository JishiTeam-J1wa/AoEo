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

// ---------------------------------------------------------------------------
// Mock helpers — return OPF-format responses
// ---------------------------------------------------------------------------

func opfDetectHandler(w http.ResponseWriter, r *http.Request, label, text string) {
	json.NewEncoder(w).Encode(opfRedactResponse{
		SchemaVersion: 1,
		Text:          text,
		RedactedText:  "[REDACTED]",
		DetectedSpans: []opfSpan{
			{Label: label, Text: text, Start: 0, End: len(text), Placeholder: "[" + label + "]"},
		},
		Summary:   map[string]any{label: 1},
		LatencyMs: 1.0,
	})
}

func opfBatchHandler(w http.ResponseWriter, r *http.Request) {
	var req opfBatchRedactRequest
	json.NewDecoder(r.Body).Decode(&req)
	results := make([]opfRedactResponse, len(req.Texts))
	for i, text := range req.Texts {
		results[i] = opfRedactResponse{
			SchemaVersion: 1,
			Text:          text,
			RedactedText:  "[REDACTED]",
			DetectedSpans: []opfSpan{
				{Label: "NAME", Text: text, Start: 0, End: len(text), Placeholder: "[NAME]"},
			},
			Summary:   map[string]any{"NAME": 1},
			LatencyMs: 1.0,
		}
	}
	json.NewEncoder(w).Encode(opfBatchRedactResponse{Results: results, TotalLatencyMs: float64(len(req.Texts))})
}

func opfHealthHandler(w http.ResponseWriter) {
	json.NewEncoder(w).Encode(opfHealthResponse{Status: "ok", ModelLoaded: true})
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestLoadBalancedClient_RoundRobin(t *testing.T) {
	var received []string
	mu := &sync.Mutex{}

	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redact" {
			opfDetectHandler(w, r, "IP_ADDRESS", "10.0.0.1")
		} else {
			opfHealthHandler(w)
		}
		mu.Lock()
		received = append(received, "s1")
		mu.Unlock()
	}))
	defer s1.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redact" {
			opfDetectHandler(w, r, "IP_ADDRESS", "10.0.0.2")
		} else {
			opfHealthHandler(w)
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
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer s1.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redact" {
			opfDetectHandler(w, r, "IP_ADDRESS", "10.0.0.1")
		} else {
			opfHealthHandler(w)
		}
	}))
	defer s2.Close()

	lb := NewLoadBalancedClient(s1.URL+","+s2.URL, RoundRobin)
	defer lb.Close()

	for _, b := range lb.backends {
		if strings.Contains(b.endpoint, s1.URL[7:]) {
			b.healthy.Store(false)
		}
	}

	spans, err := lb.Detect(context.Background(), "test")
	if err != nil {
		t.Fatalf("expected failover to s2, got error: %v", err)
	}
	if len(spans) != 1 || spans[0].Text != "10.0.0.1" {
		t.Fatalf("unexpected result: %+v", spans)
	}
	// Verify label normalization: IP_ADDRESS -> ip
	if spans[0].Label != "ip" {
		t.Errorf("expected label 'ip', got '%s'", spans[0].Label)
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
		opfHealthHandler(w)
	}))
	defer srv.Close()

	lb := NewLoadBalancedClientWithOptions(srv.URL, RoundRobin, nil, []LoadBalancedClientOption{
		WithAutoHealthCheck(false),
	})
	defer lb.Close()

	if !lb.backends[0].healthy.Load() {
		t.Fatal("expected initially healthy")
	}

	failures.Store(1)
	lb.runHealthChecks(false)
	if lb.backends[0].healthy.Load() {
		t.Fatal("expected unhealthy after forced failure")
	}

	failures.Store(0)
	lb.runHealthChecks(false)
	if !lb.backends[0].healthy.Load() {
		t.Fatal("expected recovered")
	}
}

func TestLoadBalancedClient_LeastLatency(t *testing.T) {
	var s1Calls, s2Calls atomic.Int32

	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s1Calls.Add(1)
		if r.URL.Path == "/redact" {
			opfDetectHandler(w, r, "IP_ADDRESS", "10.0.0.1")
		} else {
			opfHealthHandler(w)
		}
	}))
	defer s1.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s2Calls.Add(1)
		if r.URL.Path == "/redact" {
			time.Sleep(50 * time.Millisecond)
			opfDetectHandler(w, r, "IP_ADDRESS", "10.0.0.2")
		} else {
			opfHealthHandler(w)
		}
	}))
	defer s2.Close()

	lb := NewLoadBalancedClientWithOptions(s1.URL+","+s2.URL, LeastLatency, nil, []LoadBalancedClientOption{
		WithAutoHealthCheck(false),
	})
	defer lb.Close()

	lb.backends[0].latencyNs.Store(int64(1 * time.Millisecond))
	lb.backends[1].latencyNs.Store(int64(100 * time.Millisecond))

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
		if r.URL.Path == "/redact/batch" {
			opfBatchHandler(w, r)
			return
		}
		opfHealthHandler(w)
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
	// NAME -> person
	if len(results[0]) != 1 || results[0][0].Text != "Alice" || results[0][0].Label != "person" {
		t.Errorf("unexpected result[0]: %+v", results[0])
	}
}

func TestLoadBalancedClient_DetectBatch_Failover(t *testing.T) {
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redact/batch" {
			http.Error(w, "overload", http.StatusServiceUnavailable)
			return
		}
		opfHealthHandler(w)
	}))
	defer s1.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redact/batch" {
			opfBatchHandler(w, r)
			return
		}
		if r.URL.Path == "/redact" {
			opfDetectHandler(w, r, "NAME", "Alice")
			return
		}
		opfHealthHandler(w)
	}))
	defer s2.Close()

	lb := NewLoadBalancedClientWithOptions(s1.URL+","+s2.URL, RoundRobin, nil, []LoadBalancedClientOption{
		WithAutoHealthCheck(false),
	})
	defer lb.Close()

	lb.backends[0].healthy.Store(false)

	results, err := lb.DetectBatch(context.Background(), []string{"Alice"})
	if err != nil {
		t.Fatalf("expected failover to s2, got error: %v", err)
	}
	if len(results) != 1 || len(results[0]) != 1 || results[0][0].Text != "Alice" {
		t.Fatalf("unexpected result: %+v", results)
	}
}

func TestLoadBalancedClient_StatsLatency(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redact" {
			opfDetectHandler(w, r, "IP_ADDRESS", "10.0.0.1")
			return
		}
		opfHealthHandler(w)
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

func TestWithHCInterval(t *testing.T) {
	lb := NewLoadBalancedClientWithOptions("http://localhost:8080", RoundRobin, nil, []LoadBalancedClientOption{
		WithHCInterval(30 * time.Second),
		WithAutoHealthCheck(false),
	})
	defer lb.Close()
	if lb.hcInterval != 30*time.Second {
		t.Fatalf("expected hcInterval 30s, got %v", lb.hcInterval)
	}
}

func TestLoadBalancedClient_HealthCheck(t *testing.T) {
	// Create a mock health endpoint.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	lb := NewLoadBalancedClientWithOptions(srv.URL, RoundRobin, nil, []LoadBalancedClientOption{
		WithAutoHealthCheck(false),
	})
	defer lb.Close()

	// Initially all backends are healthy.
	ok, err := lb.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck error: %v", err)
	}
	if !ok {
		t.Fatal("expected at least one healthy backend")
	}

	// Mark backend as unhealthy.
	lb.backends[0].healthy.Store(false)
	ok, err = lb.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck error: %v", err)
	}
	if ok {
		t.Fatal("expected no healthy backends")
	}
}

func TestLoadBalancedClient_EmptyEndpoints(t *testing.T) {
	lb := NewLoadBalancedClientWithOptions("", RoundRobin, nil, []LoadBalancedClientOption{
		WithAutoHealthCheck(false),
	})
	// Don't call Close - empty client has nil stopHC channel.
	if len(lb.backends) != 0 {
		t.Fatalf("expected 0 backends, got %d", len(lb.backends))
	}
	// pickOrder should return nil for empty backends.
	order := lb.pickOrder()
	if order != nil {
		t.Fatalf("expected nil order, got %v", order)
	}
	// HealthCheck should return false.
	ok, _ := lb.HealthCheck(context.Background())
	if ok {
		t.Fatal("expected no healthy backends for empty client")
	}
	// Detect should fail.
	_, err := lb.Detect(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error for empty client")
	}
}

func TestLoadBalancedClient_RandomStrategy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opfDetectHandler(w, r, "NAME", "test")
	}))
	defer srv.Close()

	lb := NewLoadBalancedClientWithOptions(srv.URL+","+srv.URL, Random, nil, []LoadBalancedClientOption{
		WithAutoHealthCheck(false),
	})
	defer lb.Close()

	order := lb.pickOrder()
	if len(order) != 2 {
		t.Fatalf("expected 2 backends, got %d", len(order))
	}
}

func TestLoadBalancedClient_LeastLatencyStrategy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opfDetectHandler(w, r, "NAME", "test")
	}))
	defer srv.Close()

	lb := NewLoadBalancedClientWithOptions(srv.URL+","+srv.URL, LeastLatency, nil, []LoadBalancedClientOption{
		WithAutoHealthCheck(false),
	})
	defer lb.Close()

	// Set different latencies.
	lb.backends[0].latencyNs.Store(int64(200 * time.Millisecond))
	lb.backends[1].latencyNs.Store(int64(50 * time.Millisecond))

	order := lb.pickOrder()
	if len(order) != 2 {
		t.Fatalf("expected 2 backends, got %d", len(order))
	}
	// Least latency backend should be first.
	if order[0].latencyNs.Load() > order[1].latencyNs.Load() {
		t.Fatal("expected least latency backend first")
	}
}

func TestLoadBalancedClient_LeastLatency_AllUnhealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opfDetectHandler(w, r, "NAME", "test")
	}))
	defer srv.Close()

	lb := NewLoadBalancedClientWithOptions(srv.URL+","+srv.URL, LeastLatency, nil, []LoadBalancedClientOption{
		WithAutoHealthCheck(false),
	})
	defer lb.Close()

	// Mark all as unhealthy.
	for _, b := range lb.backends {
		b.healthy.Store(false)
	}

	// When all unhealthy, pickOrder returns all backends as fallback.
	order := lb.pickOrder()
	if len(order) != 2 {
		t.Fatalf("expected 2 backends fallback, got %d", len(order))
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
