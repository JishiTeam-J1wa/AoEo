package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/providers"
)

func TestAudit_Closed(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 1}}
	s := NewScheduler(p1, p2)
	s.Close()

	_, err := s.Audit(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, ErrSchedulerClosed) {
		t.Fatalf("expected ErrSchedulerClosed, got %v", err)
	}
}

func TestAudit_NoAvailableProviders(t *testing.T) {
	p1 := &mockProv{name: "p1", available: false, config: core.ProviderConfig{MaxConcurrent: 1}}
	p2 := &mockProv{name: "p2", available: false, config: core.ProviderConfig{MaxConcurrent: 1}}
	s := NewScheduler(p1, p2)

	_, err := s.Audit(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, ErrNoAvailableProvider) {
		t.Fatalf("expected ErrNoAvailableProvider, got %v", err)
	}
}

func TestAudit_OnlyOneProvider(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}}
	p2 := &mockProv{name: "p2", available: false, config: core.ProviderConfig{MaxConcurrent: 1}}
	s := NewScheduler(p1, p2)

	_, err := s.Audit(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for insufficient providers")
	}
	if !strings.Contains(err.Error(), "at least 2") {
		t.Fatalf("expected 'at least 2' error, got: %v", err)
	}
}

func TestAudit_PrimaryFails(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}, err: errors.New("primary fail")}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m2"}}
	s := NewScheduler(p1, p2)

	_, err := s.Audit(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error when primary provider fails")
	}
	if !strings.Contains(err.Error(), "primary completion failed") {
		t.Fatalf("expected 'primary completion failed' error, got: %v", err)
	}
}

func TestAudit_AuditProviderFails(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "primary-result"}}},
	}}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m2"}, err: errors.New("audit fail")}
	s := NewScheduler(p1, p2)

	result, err := s.Audit(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("audit should succeed even when audit provider fails, got: %v", err)
	}
	if result.Primary == nil {
		t.Fatal("expected primary result")
	}
	if result.Primary.Content() != "primary-result" {
		t.Fatalf("expected primary-result, got %s", result.Primary.Content())
	}
	// Audit provider failed, so Audit response should be nil
	if result.Audit != nil {
		t.Fatal("expected nil audit response when audit provider fails")
	}
	// When audit fails, consensus is assumed true
	if !result.Consensus {
		t.Fatal("expected consensus=true when audit provider fails")
	}
	// Adjusted should be the primary
	if result.Adjusted == nil || result.Adjusted.Content() != "primary-result" {
		t.Fatal("expected adjusted to be primary result")
	}
}

func TestAudit_Consensus(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "same answer"}}},
	}}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m2"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "same answer"}}},
	}}
	s := NewScheduler(p1, p2)

	result, err := s.Audit(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Consensus {
		t.Fatal("expected consensus for identical responses")
	}
	if result.Audit == nil {
		t.Fatal("expected audit result")
	}
}

func TestAudit_Disagreement(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}, response: &core.ChatCompletionResponse{
		Model:   "m1",
		Choices: []core.Choice{{Message: core.Message{Content: "answer A"}}},
	}}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m2"}, response: &core.ChatCompletionResponse{
		Model:   "m2",
		Choices: []core.Choice{{Message: core.Message{Content: "answer B"}}},
	}}
	s := NewScheduler(p1, p2)

	result, err := s.Audit(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Consensus {
		t.Fatal("expected disagreement for different responses")
	}
	if result.Adjusted == nil {
		t.Fatal("expected adjusted result even on disagreement")
	}
}

func TestAudit_WithHistory(t *testing.T) {
	h := NewHistory(10)
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "same"}}},
	}}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m2"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "same"}}},
	}}
	s := NewSchedulerWithOptions([]providers.Provider{p1, p2}, WithHistory(h))

	_, err := s.Audit(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
		Tags:     []string{"audit-test"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	records := h.Records()
	if len(records) < 2 {
		t.Fatalf("expected at least 2 history records (primary + audit), got %d", len(records))
	}

	// Verify audit tag is present
	foundTag := false
	for _, r := range records {
		for _, tag := range r.Tags {
			if tag == "audit" {
				foundTag = true
				break
			}
		}
	}
	if !foundTag {
		t.Fatal("expected 'audit' tag in history records")
	}
}

func TestAudit_WithPromptInjector(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "ok"}}},
	}}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m2"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "ok"}}},
	}}
	s := NewScheduler(p1, p2)

	pi := NewPromptInjector()
	pi.AddTemplate(PromptTemplate{
		Provider: "*",
		Model:    "*",
		Position: "system",
		Content:  "You are a helpful assistant.",
	})
	s.SetPromptInjector(pi)

	result, err := s.Audit(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Primary == nil {
		t.Fatal("expected primary result")
	}
}

func TestAudit_DefaultModelFill(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "default-m1"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "ok"}}},
	}}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "default-m2"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "ok"}}},
	}}
	s := NewScheduler(p1, p2)

	// Model is empty, should be filled from provider config
	result, err := s.Audit(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Primary == nil {
		t.Fatal("expected primary result")
	}
}

func TestAudit_PrimaryPanic(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}, panicOnCall: true}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m2"}}
	s := NewScheduler(p1, p2)

	_, err := s.Audit(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error when primary panics")
	}
	if !strings.Contains(err.Error(), "primary completion failed") {
		t.Fatalf("expected 'primary completion failed', got: %v", err)
	}
}

func TestAudit_ThreeProviders(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "answer"}}},
	}}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m2"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "answer"}}},
	}}
	p3 := &mockProv{name: "p3", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m3"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "answer"}}},
	}}
	s := NewScheduler(p1, p2, p3)

	result, err := s.Audit(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Consensus {
		t.Fatal("expected consensus with 3 providers returning same answer")
	}
}
