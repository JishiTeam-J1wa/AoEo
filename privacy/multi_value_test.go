package privacy

import (
	"context"
	"strings"
	"testing"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/privacy/store"
)

// TestMultiValue_RoundTrip verifies that multiple different sensitive values
// in a single request are correctly replaced and then correctly restored.
func TestMultiValue_RoundTrip(t *testing.T) {
	pebbleStore, _ := store.OpenPebble(t.TempDir() + "/privacy")
	defer pebbleStore.Close()

	gen := NewFakeGenerator(42)
	detector := &mockDetector{
		spans: []Span{
			{Label: EntityPerson, Original: "张三", Score: 0.99},
			{Label: EntityIP, Original: "192.168.1.1", Score: 0.99},
			{Label: EntityPhone, Original: "13800138000", Score: 0.99},
		},
	}

	ps := NewPseudonymizer(pebbleStore, gen, detector)
	ctx := context.Background()

	// 1. Pseudonymize
	req := &core.ChatCompletionRequest{
		Messages: []core.Message{{
			Role:    "user",
			Content: "我叫张三，服务器IP是192.168.1.1，电话13800138000",
		}},
	}
	newReq, mappings, err := ps.PseudonymizeRequest(ctx, "sess-multi", req)
	if err != nil {
		t.Fatalf("pseudonymize: %v", err)
	}
	if len(mappings) != 3 {
		t.Fatalf("expected 3 mappings, got %d", len(mappings))
	}

	// Verify originals are gone from the request.
	content := newReq.Messages[0].Content
	if strings.Contains(content, "张三") || strings.Contains(content, "192.168.1.1") || strings.Contains(content, "13800138000") {
		t.Fatalf("originals still present: %s", content)
	}

	// 2. Build fake-to-original map.
	fakeToOrig := make(map[string]string, len(mappings))
	for _, m := range mappings {
		fakeToOrig[m.Fake] = m.Original
	}

	// 3. Simulate AI response using the fake values.
	// Build by original label so order doesn't depend on mappings slice order.
	var personFake, ipFake, phoneFake string
	for _, m := range mappings {
		switch m.Type {
		case string(EntityPerson):
			personFake = m.Fake
		case string(EntityIP):
			ipFake = m.Fake
		case string(EntityPhone):
			phoneFake = m.Fake
		}
	}
	resp := &core.ChatCompletionResponse{
		Choices: []core.Choice{{
			Message: core.Message{
				Content: "好的" + personFake + "，服务器" + ipFake + "已配置，手机" + phoneFake + "已记录",
			},
		}},
	}

	// 4. Restore using only this request's mappings.
	restored, err := ps.RestoreResponseWithMappings(ctx, "sess-multi", resp, mappings)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	want := "好的张三，服务器192.168.1.1已配置，手机13800138000已记录"
	got := restored.Choices[0].Message.Content
	if got != want {
		t.Fatalf("restore mismatch:\n  got:  %s\n  want: %s", got, want)
	}
}

// TestMultiValue_NoHistoricalPollution verifies that a second request does not
// accidentally restore fake values from the first request.
func TestMultiValue_NoHistoricalPollution(t *testing.T) {
	pebbleStore, _ := store.OpenPebble(t.TempDir() + "/privacy")
	defer pebbleStore.Close()

	gen := NewFakeGenerator(42)
	detector := &mockDetector{}

	ps := NewPseudonymizer(pebbleStore, gen, detector)
	ctx := context.Background()

	// Request 1: person
	detector.spans = []Span{{Label: EntityPerson, Original: "张三", Score: 0.99}}
	req1 := &core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "我叫张三"}},
	}
	_, mappings1, _ := ps.PseudonymizeRequest(ctx, "sess-pollute", req1)
	fake1 := mappings1[0].Fake

	// Request 2: ip (different entity, same session)
	detector.spans = []Span{{Label: EntityIP, Original: "10.0.0.1", Score: 0.99}}
	req2 := &core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "IP是10.0.0.1"}},
	}
	_, mappings2, _ := ps.PseudonymizeRequest(ctx, "sess-pollute", req2)

	// AI response for request 2 mentions the fake IP AND coincidentally
	// also mentions the fake name from request 1 (simulating AI hallucination).
	resp2 := &core.ChatCompletionResponse{
		Choices: []core.Choice{{
			Message: core.Message{
				Content: fake1 + "的服务器IP是" + mappings2[0].Fake,
			},
		}},
	}

	// Restore request 2 using ONLY request 2's mappings.
	restored, _ := ps.RestoreResponseWithMappings(ctx, "sess-pollute", resp2, mappings2)
	content := restored.Choices[0].Message.Content

	// The fake IP from request 2 should be restored.
	if !strings.Contains(content, "10.0.0.1") {
		t.Fatalf("request 2 IP not restored: %s", content)
	}
	// The fake name from request 1 should NOT be restored (it was not in mappings2).
	if strings.Contains(content, "张三") {
		t.Fatalf("request 1 name was incorrectly restored: %s", content)
	}
	// The fake name should still be present (it's the AI's own text).
	if !strings.Contains(content, fake1) {
		t.Fatalf("request 1 fake name should remain as AI-generated text: %s", content)
	}
}
