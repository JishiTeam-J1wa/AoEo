package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPClient_DetectBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/redact/batch" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		var req opfBatchRedactRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		results := make([]opfRedactResponse, len(req.Texts))
		for i, text := range req.Texts {
			results[i] = opfRedactResponse{
				SchemaVersion: 1,
				Text:          text,
				RedactedText:  "[NAME]",
				DetectedSpans: []opfSpan{
					{Label: "NAME", Start: 0, End: len(text), Text: text, Placeholder: "[NAME]"},
				},
				Summary:   map[string]any{"NAME": 1},
				LatencyMs: 5.0,
			}
		}
		json.NewEncoder(w).Encode(opfBatchRedactResponse{
			Results:        results,
			TotalLatencyMs: 10.0,
		})
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, WithTimeout(2*time.Second), WithRetries(0))

	results, err := c.DetectBatch(context.Background(), []string{"Alice", "Bob"})
	if err != nil {
		t.Fatalf("DetectBatch failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// OPF label "NAME" should be normalized to "person"
	if len(results[0]) != 1 || results[0][0].Text != "Alice" || results[0][0].Label != "person" {
		t.Errorf("unexpected result[0]: %+v", results[0])
	}
	if results[0][0].Placeholder != "[NAME]" {
		t.Errorf("expected placeholder [NAME], got %s", results[0][0].Placeholder)
	}
	if len(results[1]) != 1 || results[1][0].Text != "Bob" || results[1][0].Label != "person" {
		t.Errorf("unexpected result[1]: %+v", results[1])
	}
}

func TestHTTPClient_DetectBatch_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("server should not be called for empty batch")
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL)
	results, err := c.DetectBatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestHTTPClient_DetectBatch_SingleFallback(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path == "/redact" {
			json.NewEncoder(w).Encode(opfRedactResponse{
				SchemaVersion: 1,
				DetectedSpans: []opfSpan{
					{Label: "NAME", Text: "Alice", Start: 0, End: 5, Placeholder: "[NAME]"},
				},
				Summary:   map[string]any{"NAME": 1},
				LatencyMs: 3.0,
			})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL)
	results, err := c.DetectBatch(context.Background(), []string{"Alice"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected server to be called")
	}
	if len(results) != 1 || len(results[0]) != 1 {
		t.Fatalf("unexpected results: %v", results)
	}
	// Verify label normalization
	if results[0][0].Label != "person" {
		t.Errorf("expected label 'person', got '%s'", results[0][0].Label)
	}
}

func TestHTTPClient_DetectBatch_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "overload", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, WithRetries(0))
	_, err := c.DetectBatch(context.Background(), []string{"test"})
	if err == nil {
		t.Fatal("expected error for 503")
	}
}

func TestHTTPClient_Detect_Single(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/redact" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		var req opfRedactRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		json.NewEncoder(w).Encode(opfRedactResponse{
			SchemaVersion: 1,
			Text:          req.Text,
			RedactedText:  "My name is [NAME] and my email is [EMAIL_ADDRESS]",
			DetectedSpans: []opfSpan{
				{Label: "NAME", Start: 11, End: 16, Text: "John", Placeholder: "[NAME]"},
				{Label: "EMAIL_ADDRESS", Start: 32, End: 48, Text: "john@example.com", Placeholder: "[EMAIL_ADDRESS]"},
			},
			Summary:   map[string]any{"NAME": 1, "EMAIL_ADDRESS": 1},
			LatencyMs: 8.5,
		})
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, WithTimeout(2*time.Second), WithRetries(0))

	spans, err := c.Detect(context.Background(), "My name is John and my email is john@example.com")
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}

	// Verify label normalization
	if spans[0].Label != "person" {
		t.Errorf("span[0] label: expected 'person', got '%s'", spans[0].Label)
	}
	if spans[0].Text != "John" {
		t.Errorf("span[0] text: expected 'John', got '%s'", spans[0].Text)
	}
	if spans[0].Placeholder != "[NAME]" {
		t.Errorf("span[0] placeholder: expected '[NAME]', got '%s'", spans[0].Placeholder)
	}

	if spans[1].Label != "email" {
		t.Errorf("span[1] label: expected 'email', got '%s'", spans[1].Label)
	}
	if spans[1].Text != "john@example.com" {
		t.Errorf("span[1] text: expected 'john@example.com', got '%s'", spans[1].Text)
	}
}

func TestHTTPClient_HealthCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(opfHealthResponse{Status: "ok", ModelLoaded: true})
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, WithTimeout(2*time.Second))

	ok, err := c.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck failed: %v", err)
	}
	if !ok {
		t.Fatal("expected healthy")
	}
}

func TestHTTPClient_HealthCheck_Unhealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(opfHealthResponse{Status: "ok", ModelLoaded: false})
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, WithTimeout(2*time.Second))

	ok, err := c.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected unhealthy (model not loaded)")
	}
}

func TestOPFLabelNormalization(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"NAME", "person"},
		{"PERSON", "person"},
		{"EMAIL_ADDRESS", "email"},
		{"PHONE_NUMBER", "phone"},
		{"IP_ADDRESS", "ip"},
		{"US_SSN", "idcard"},
		{"CREDIT_CARD", "secret"},
		{"URL", "url"},
		{"DATE_TIME", "date"},
		{"LOCATION", "address"},
		{"UNKNOWN_LABEL", "secret"}, // default
		{"", "secret"},              // empty
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeOPFLabel(tt.input)
			if got != tt.expected {
				t.Errorf("normalizeOPFLabel(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
