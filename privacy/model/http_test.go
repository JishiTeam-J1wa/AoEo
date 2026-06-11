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
		if r.URL.Path != "/detect/batch" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		var req batchDetectRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		results := make([]batchDetectResult, len(req.Texts))
		for i, text := range req.Texts {
			results[i] = batchDetectResult{
				Spans: []Span{
					{Label: "person", Text: text, Start: 0, End: len(text), Score: 0.95},
				},
			}
		}
		json.NewEncoder(w).Encode(batchDetectResponse{Results: results})
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
	if len(results[0]) != 1 || results[0][0].Text != "Alice" {
		t.Errorf("unexpected result[0]: %v", results[0])
	}
	if len(results[1]) != 1 || results[1][0].Text != "Bob" {
		t.Errorf("unexpected result[1]: %v", results[1])
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
		if r.URL.Path == "/detect" {
			json.NewEncoder(w).Encode(detectResponse{
				Spans: []Span{{Label: "person", Text: "Alice", Score: 0.9}},
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
