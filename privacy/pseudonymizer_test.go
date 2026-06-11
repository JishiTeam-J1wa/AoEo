package privacy

import (
	"context"
	"strings"
	"testing"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/privacy/store"
)

// mockDetector simulates the Privacy Filter model for testing.
type mockDetector struct {
	spans []Span
}

func (m *mockDetector) Detect(text string) DetectResult {
	return DetectResult{Spans: m.spans}
}

func (m *mockDetector) DetectBatch(texts []string) []DetectResult {
	result := make([]DetectResult, len(texts))
	for i := range texts {
		result[i] = m.Detect(texts[i])
	}
	return result
}

// modelDetectAdapter adapts mockDetector to ModelDetector interface.
type modelDetectAdapter struct {
	md *mockDetector
}

func (a *modelDetectAdapter) Detect(text string) []Span {
	return a.md.Detect(text).Spans
}

func TestPseudonymizer_BasicPseudonymize(t *testing.T) {
	pebbleStore, _ := store.OpenPebble(t.TempDir() + "/privacy")
	defer pebbleStore.Close()

	gen := NewFakeGenerator(42)
	detector := &mockDetector{
		spans: []Span{
			{Label: EntityIP, Original: "192.168.1.100", Score: 0.99},
			{Label: EntityPerson, Original: "张三", Score: 0.95},
			{Label: EntityPhone, Original: "13800138000", Score: 0.98},
		},
	}

	ps := NewPseudonymizer(pebbleStore, gen, detector)

	req := &core.ChatCompletionRequest{
		Messages: []core.Message{
			{Role: "user", Content: "我叫张三，服务器IP是192.168.1.100，电话13800138000"},
		},
	}

	ctx := context.Background()
	newReq, mappings, err := ps.PseudonymizeRequest(ctx, "sess-1", req)
	if err != nil {
		t.Fatalf("pseudonymize: %v", err)
	}
	if len(mappings) != 3 {
		t.Fatalf("expected 3 mappings, got %d", len(mappings))
	}

	// Verify originals are gone from the request.
	content := newReq.Messages[0].Content
	if strings.Contains(content, "张三") {
		t.Fatal("original name still in request")
	}
	if strings.Contains(content, "192.168.1.100") {
		t.Fatal("original IP still in request")
	}
	if strings.Contains(content, "13800138000") {
		t.Fatal("original phone still in request")
	}

	// Verify fake values are present (RFC1918 private IP).
	if !strings.Contains(content, "10.") && !strings.Contains(content, "172.") && !strings.Contains(content, "192.168.") {
		t.Fatalf("fake IP not found in: %s", content)
	}
}

func TestPseudonymizer_ConsistentMapping(t *testing.T) {
	pebbleStore, _ := store.OpenPebble(t.TempDir() + "/privacy")
	defer pebbleStore.Close()

	gen := NewFakeGenerator(42)
	detector := &mockDetector{
		spans: []Span{
			{Label: EntityIP, Original: "192.168.1.1", Score: 0.99},
		},
	}

	ps := NewPseudonymizer(pebbleStore, gen, detector)
	ctx := context.Background()

	req1 := &core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "IP: 192.168.1.1"}},
	}
	newReq1, _, _ := ps.PseudonymizeRequest(ctx, "sess-1", req1)
	fake1 := newReq1.Messages[0].Content

	// Second call with same original should reuse the same fake.
	req2 := &core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "Again IP: 192.168.1.1"}},
	}
	newReq2, _, _ := ps.PseudonymizeRequest(ctx, "sess-1", req2)
	fake2 := newReq2.Messages[0].Content

	// Both should contain the same fake IP.
	if !strings.Contains(fake1, "192.168.1.1") && !strings.Contains(fake2, "192.168.1.1") {
		// Good, both replaced
	} else {
		t.Fatal("expected both to be replaced")
	}

	// Extract the fake IP from both and compare.
	// The fake IP should be identical because we reused the mapping.
	entries, _ := pebbleStore.GetSession(ctx, "sess-1")
	if len(entries) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(entries))
	}
}

func TestPseudonymizer_RestoreResponse(t *testing.T) {
	pebbleStore, _ := store.OpenPebble(t.TempDir() + "/privacy")
	defer pebbleStore.Close()

	gen := NewFakeGenerator(42)
	detector := &mockDetector{
		spans: []Span{
			{Label: EntityIP, Original: "192.168.1.1", Score: 0.99},
		},
	}

	ps := NewPseudonymizer(pebbleStore, gen, detector)
	ctx := context.Background()

	// First pseudonymize to create the mapping.
	req := &core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "IP: 192.168.1.1"}},
	}
	ps.PseudonymizeRequest(ctx, "sess-1", req)

	// Find the fake IP.
	entries, _ := pebbleStore.GetSession(ctx, "sess-1")
	if len(entries) == 0 {
		t.Fatal("no mappings")
	}
	fakeIP := entries[0].Fake

	// Simulate AI response with fake IP.
	resp := &core.ChatCompletionResponse{
		Choices: []core.Choice{
			{Message: core.Message{Content: "服务器地址是 " + fakeIP}},
		},
	}

	restored, err := ps.RestoreResponse(ctx, "sess-1", resp)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	content := restored.Choices[0].Message.Content
	if !strings.Contains(content, "192.168.1.1") {
		t.Fatalf("expected original IP restored, got: %s", content)
	}
	if strings.Contains(content, fakeIP) {
		t.Fatalf("fake IP should not be present after restore: %s", content)
	}
}

func TestPseudonymizer_NoSensitiveData(t *testing.T) {
	pebbleStore, _ := store.OpenPebble(t.TempDir() + "/privacy")
	defer pebbleStore.Close()

	gen := NewFakeGenerator(42)
	detector := &mockDetector{spans: nil}

	ps := NewPseudonymizer(pebbleStore, gen, detector)
	ctx := context.Background()

	req := &core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "Hello world"}},
	}
	newReq, mappings, err := ps.PseudonymizeRequest(ctx, "sess-1", req)
	if err != nil {
		t.Fatalf("pseudonymize: %v", err)
	}
	if len(mappings) != 0 {
		t.Fatalf("expected 0 mappings, got %d", len(mappings))
	}
	if newReq.Messages[0].Content != "Hello world" {
		t.Fatal("request should be unchanged")
	}
}
