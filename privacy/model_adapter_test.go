package privacy

import (
	"context"
	"errors"
	"testing"

	"github.com/JishiTeam-J1wa/AoEo/privacy/model"
)

// adapterMockClient is a mock model.Client for testing modelDetectorAdapter.
type adapterMockClient struct {
	detectFn      func(ctx context.Context, text string) ([]model.Span, error)
	detectBatchFn func(ctx context.Context, texts []string) ([][]model.Span, error)
}

func (m *adapterMockClient) Detect(ctx context.Context, text string) ([]model.Span, error) {
	if m.detectFn != nil {
		return m.detectFn(ctx, text)
	}
	return nil, nil
}

func (m *adapterMockClient) DetectBatch(ctx context.Context, texts []string) ([][]model.Span, error) {
	if m.detectBatchFn != nil {
		return m.detectBatchFn(ctx, texts)
	}
	return nil, nil
}

func (m *adapterMockClient) HealthCheck(ctx context.Context) (bool, error) {
	return true, nil
}

// ---------------------------------------------------------------------------
// newModelDetectorAdapter
// ---------------------------------------------------------------------------

func TestNewModelDetectorAdapter(t *testing.T) {
	mc := &adapterMockClient{}
	d := newModelDetectorAdapter(mc)
	if d == nil {
		t.Fatal("adapter should not be nil")
	}
	adapter, ok := d.(*modelDetectorAdapter)
	if !ok {
		t.Fatal("expected *modelDetectorAdapter type")
	}
	if adapter.client != mc {
		t.Fatal("client should match")
	}
}

// ---------------------------------------------------------------------------
// Detect
// ---------------------------------------------------------------------------

func TestModelDetectorAdapter_Detect_Success(t *testing.T) {
	mc := &adapterMockClient{
		detectFn: func(ctx context.Context, text string) ([]model.Span, error) {
			return []model.Span{
				{Label: "person", Text: "Alice", Start: 0, End: 5, Score: 0.95},
				{Label: "phone", Text: "1380000", Start: 10, End: 17, Score: 0.88},
			}, nil
		},
	}
	adapter := newModelDetectorAdapter(mc)
	result := adapter.Detect("Alice and 1380000")

	if len(result.Spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(result.Spans))
	}

	if result.Spans[0].Label != EntityPerson {
		t.Fatalf("expected person label, got %s", result.Spans[0].Label)
	}
	if result.Spans[0].Original != "Alice" {
		t.Fatalf("expected original 'Alice', got %s", result.Spans[0].Original)
	}
	if result.Spans[0].Start != 0 || result.Spans[0].End != 5 {
		t.Fatalf("unexpected start/end: %d/%d", result.Spans[0].Start, result.Spans[0].End)
	}
	if result.Spans[0].Score != 0.95 {
		t.Fatalf("expected score 0.95, got %f", result.Spans[0].Score)
	}

	if result.Spans[1].Label != EntityPhone {
		t.Fatalf("expected phone label, got %s", result.Spans[1].Label)
	}
}

func TestModelDetectorAdapter_Detect_EmptyResult(t *testing.T) {
	mc := &adapterMockClient{
		detectFn: func(ctx context.Context, text string) ([]model.Span, error) {
			return nil, nil
		},
	}
	adapter := newModelDetectorAdapter(mc)
	result := adapter.Detect("no sensitive data")

	if len(result.Spans) != 0 {
		t.Fatalf("expected 0 spans, got %d", len(result.Spans))
	}
}

func TestModelDetectorAdapter_Detect_ClientError(t *testing.T) {
	mc := &adapterMockClient{
		detectFn: func(ctx context.Context, text string) ([]model.Span, error) {
			return nil, errors.New("connection refused")
		},
	}
	adapter := newModelDetectorAdapter(mc)
	result := adapter.Detect("some text")

	// Error path: should return empty DetectResult (no panic).
	if len(result.Spans) != 0 {
		t.Fatalf("expected 0 spans on error, got %d", len(result.Spans))
	}
}

func TestModelDetectorAdapter_Detect_ContextCancelled(t *testing.T) {
	mc := &adapterMockClient{
		detectFn: func(ctx context.Context, text string) ([]model.Span, error) {
			// Simulate context cancellation.
			return nil, ctx.Err()
		},
	}
	adapter := newModelDetectorAdapter(mc)
	result := adapter.Detect("test")
	if len(result.Spans) != 0 {
		t.Fatalf("expected empty result on context error, got %d spans", len(result.Spans))
	}
}

// ---------------------------------------------------------------------------
// DetectBatch
// ---------------------------------------------------------------------------

func TestModelDetectorAdapter_DetectBatch_Success(t *testing.T) {
	mc := &adapterMockClient{
		detectBatchFn: func(ctx context.Context, texts []string) ([][]model.Span, error) {
			return [][]model.Span{
				{{Label: "email", Text: "a@b.com", Start: 0, End: 7, Score: 0.9}},
				{{Label: "ip", Text: "10.0.0.1", Start: 5, End: 13, Score: 0.85}},
			}, nil
		},
	}
	adapter := newModelDetectorAdapter(mc)
	results := adapter.DetectBatch([]string{"email a@b.com", "server 10.0.0.1"})

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if len(results[0].Spans) != 1 {
		t.Fatalf("expected 1 span in first result, got %d", len(results[0].Spans))
	}
	if results[0].Spans[0].Label != EntityEmail {
		t.Fatalf("expected email label, got %s", results[0].Spans[0].Label)
	}
	if results[0].Spans[0].Original != "a@b.com" {
		t.Fatalf("expected 'a@b.com', got %s", results[0].Spans[0].Original)
	}

	if len(results[1].Spans) != 1 {
		t.Fatalf("expected 1 span in second result, got %d", len(results[1].Spans))
	}
	if results[1].Spans[0].Label != EntityIP {
		t.Fatalf("expected ip label, got %s", results[1].Spans[0].Label)
	}
}

func TestModelDetectorAdapter_DetectBatch_EmptyInput(t *testing.T) {
	mc := &adapterMockClient{}
	adapter := newModelDetectorAdapter(mc)
	results := adapter.DetectBatch(nil)
	if results != nil {
		t.Fatalf("expected nil for empty input, got %v", results)
	}
}

func TestModelDetectorAdapter_DetectBatch_EmptySlice(t *testing.T) {
	mc := &adapterMockClient{}
	adapter := newModelDetectorAdapter(mc)
	results := adapter.DetectBatch([]string{})
	if results != nil {
		t.Fatalf("expected nil for empty slice, got %v", results)
	}
}

func TestModelDetectorAdapter_DetectBatch_ErrorFallbackToDetect(t *testing.T) {
	callCount := 0
	mc := &adapterMockClient{
		detectFn: func(ctx context.Context, text string) ([]model.Span, error) {
			callCount++
			return []model.Span{
				{Label: "person", Text: text, Start: 0, End: len(text), Score: 0.5},
			}, nil
		},
		detectBatchFn: func(ctx context.Context, texts []string) ([][]model.Span, error) {
			return nil, errors.New("batch endpoint not available")
		},
	}
	adapter := newModelDetectorAdapter(mc)
	results := adapter.DetectBatch([]string{"Alice", "Bob"})

	// Batch failed, should fall back to individual Detect calls.
	if len(results) != 2 {
		t.Fatalf("expected 2 results from fallback, got %d", len(results))
	}
	if callCount != 2 {
		t.Fatalf("expected 2 individual detect calls, got %d", callCount)
	}
	if len(results[0].Spans) != 1 {
		t.Fatalf("expected 1 span in first fallback result, got %d", len(results[0].Spans))
	}
	if results[0].Spans[0].Label != EntityPerson {
		t.Fatalf("expected person label, got %s", results[0].Spans[0].Label)
	}
	if results[1].Spans[0].Original != "Bob" {
		t.Fatalf("expected 'Bob', got %s", results[1].Spans[0].Original)
	}
}

func TestModelDetectorAdapter_DetectBatch_MultipleSpansPerText(t *testing.T) {
	mc := &adapterMockClient{
		detectBatchFn: func(ctx context.Context, texts []string) ([][]model.Span, error) {
			return [][]model.Span{
				{
					{Label: "person", Text: "Alice", Start: 0, End: 5, Score: 0.9},
					{Label: "phone", Text: "555-1234", Start: 10, End: 18, Score: 0.8},
				},
			}, nil
		},
	}
	adapter := newModelDetectorAdapter(mc)
	results := adapter.DetectBatch([]string{"Alice and 555-1234"})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if len(results[0].Spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(results[0].Spans))
	}
}

func TestModelDetectorAdapter_DetectBatch_FallbackDetectAlsoFails(t *testing.T) {
	mc := &adapterMockClient{
		detectFn: func(ctx context.Context, text string) ([]model.Span, error) {
			return nil, errors.New("individual detect also failed")
		},
		detectBatchFn: func(ctx context.Context, texts []string) ([][]model.Span, error) {
			return nil, errors.New("batch failed")
		},
	}
	adapter := newModelDetectorAdapter(mc)
	results := adapter.DetectBatch([]string{"text1", "text2"})

	// Both batch and individual detect fail; should return empty results.
	if len(results) != 2 {
		t.Fatalf("expected 2 results (empty), got %d", len(results))
	}
	for i, r := range results {
		if len(r.Spans) != 0 {
			t.Fatalf("result %d should have 0 spans, got %d", i, len(r.Spans))
		}
	}
}
