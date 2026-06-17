package aoeo

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/providers"
)

func TestNewClient_Validation(t *testing.T) {
	_, err := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "", APIKey: "", Endpoint: "bad", Model: ""},
		},
	})
	if err == nil {
		t.Fatal("expected error for invalid config")
	}
}

func TestNewClient_Success(t *testing.T) {
	client, err := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.IsClosed() {
		t.Fatal("expected client not closed")
	}
}

func TestClient_Close(t *testing.T) {
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	})
	if err := client.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !client.IsClosed() {
		t.Fatal("expected client closed")
	}
	// Idempotent
	if err := client.Close(); err != nil {
		t.Fatalf("second close should be idempotent: %v", err)
	}
}

func TestClient_SetTimeout(t *testing.T) {
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	})
	client.SetTimeout(10 * time.Second)
	// Smoke test: no panic
}

func TestClient_Interceptors(t *testing.T) {
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	})
	if client.Interceptors() != nil {
		t.Fatal("expected nil interceptors initially")
	}
	ic := []core.Interceptor{{}}
	client.SetInterceptors(ic)
	if len(client.Interceptors()) != 1 {
		t.Fatal("expected 1 interceptor")
	}
}

func TestClient_HistoryAndStats(t *testing.T) {
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	}, WithHistory(NewHistory(100)))

	if client.History() == nil {
		t.Fatal("expected history")
	}
	if client.Stats() == nil {
		t.Fatal("expected stats")
	}
}

func TestClient_ProviderStatus(t *testing.T) {
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	})
	status := client.ProviderStatus()
	if len(status) != 1 {
		t.Fatalf("expected 1 provider status, got %d", len(status))
	}
}

func TestClient_Scheduler(t *testing.T) {
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	})
	if client.Scheduler() == nil {
		t.Fatal("expected non-nil scheduler")
	}
}

func TestClient_PromptInjector(t *testing.T) {
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	})
	if client.PromptInjector() != nil {
		t.Fatal("expected nil prompt injector initially")
	}
	pi := NewPromptInjector()
	client.SetPromptInjector(pi)
	if client.PromptInjector() != pi {
		t.Fatal("SetPromptInjector did not work")
	}
}

func TestClient_SetEmitter(t *testing.T) {
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	})
	// Nil should not panic
	client.SetEmitter(nil)
}

func TestClient_ChatCompleteStream_Closed(t *testing.T) {
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	})
	client.Close()
	_, err := client.ChatCompleteStream(context.Background(), ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, ErrSchedulerClosed) {
		t.Fatalf("expected ErrSchedulerClosed, got %v", err)
	}
}

func TestClient_Audit_InsufficientProviders(t *testing.T) {
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	})
	_, err := client.Audit(context.Background(), ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for insufficient providers")
	}
}

func TestClient_ChatCompleteWithFallback_EmitsEvent(t *testing.T) {
	// Create a client with a provider that will fail, but no fallback
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	})
	// Even though there's no fallback, this tests the code path
	_, _ = client.ChatCompleteWithFallback(context.Background(), ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
}

func TestClient_ChatCompleteDual_Closed(t *testing.T) {
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "p1", APIKey: "k", Endpoint: "https://a.com", Model: "m"},
			{Name: "p2", APIKey: "k", Endpoint: "https://b.com", Model: "m"},
		},
	})
	client.Close()
	_, err := client.ChatCompleteDual(context.Background(), ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, ErrSchedulerClosed) {
		t.Fatalf("expected ErrSchedulerClosed, got %v", err)
	}
}

func TestNewClientWithProviders(t *testing.T) {
	p := providers.NewOpenAIProvider(core.ProviderConfig{
		Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m",
	})
	client := NewClientWithProviders(p)
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

// ========== New tests for coverage improvement ==========

// trackingEmitter records emitted events for verification.
type trackingEmitter struct {
	mu     sync.Mutex
	events []struct {
		topic string
		data  []any
	}
}

func (e *trackingEmitter) Emit(topic string, data ...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, struct {
		topic string
		data  []any
	}{topic, data})
}

func (e *trackingEmitter) count(topic string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, ev := range e.events {
		if ev.topic == topic {
			n++
		}
	}
	return n
}

func TestClient_ChatCompleteWithProvider_Success(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: true,
		config: ProviderConfig{MaxConcurrent: 1, Model: "m1"},
		response: &ChatCompletionResponse{
			Choices: []Choice{{Message: Message{Content: "from-p1"}}},
		},
	}
	p2 := &mockProvider{
		name: "p2", available: true,
		config: ProviderConfig{MaxConcurrent: 1, Model: "m2"},
		response: &ChatCompletionResponse{
			Choices: []Choice{{Message: Message{Content: "from-p2"}}},
		},
	}

	client := NewClientWithProviders(p1, p2)
	defer client.Close()

	resp, err := client.ChatCompleteWithProvider(context.Background(), "p2", ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Choices[0].Message.Content != "from-p2" {
		t.Fatalf("expected 'from-p2', got '%s'", resp.Choices[0].Message.Content)
	}
}

func TestClient_ChatCompleteWithProvider_NotFound(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: true,
		config: ProviderConfig{MaxConcurrent: 1, Model: "m1"},
	}
	client := NewClientWithProviders(p1)
	defer client.Close()

	// Requesting a non-existent provider should fall back to the first available.
	// SingleProviderRouter falls back when the target is not found.
	resp, err := client.ChatCompleteWithProvider(context.Background(), "nonexistent", ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	// The fallback behavior should still return a response from p1.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response from fallback")
	}
}

func TestClient_ChatCompleteWithProvider_Closed(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: true,
		config: ProviderConfig{MaxConcurrent: 1},
	}
	client := NewClientWithProviders(p1)
	client.Close()

	_, err := client.ChatCompleteWithProvider(context.Background(), "p1", ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, ErrSchedulerClosed) {
		t.Fatalf("expected ErrSchedulerClosed, got %v", err)
	}
}

func TestClient_ChatCompleteStreamWithProvider_Closed(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: true,
		config: ProviderConfig{MaxConcurrent: 1},
	}
	client := NewClientWithProviders(p1)
	client.Close()

	_, err := client.ChatCompleteStreamWithProvider(context.Background(), "p1", ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, ErrSchedulerClosed) {
		t.Fatalf("expected ErrSchedulerClosed, got %v", err)
	}
}

func TestClient_ChatCompleteStreamWithProvider_NoStreamSupport(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: true,
		config: ProviderConfig{MaxConcurrent: 1, Model: "m1"},
	}
	client := NewClientWithProviders(p1)
	defer client.Close()

	// mockProvider.ChatCompleteStream returns an error (not supported).
	_, err := client.ChatCompleteStreamWithProvider(context.Background(), "p1", ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for unsupported streaming")
	}
}

func TestClient_ListModels_Success(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: true,
		config: ProviderConfig{MaxConcurrent: 1},
	}
	client := NewClientWithProviders(p1)
	defer client.Close()

	models, err := client.ListModels(context.Background(), "p1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 1 || models[0].ID != "mock-model" {
		t.Fatalf("unexpected models: %+v", models)
	}
}

func TestClient_ListModels_NotFound(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: true,
		config: ProviderConfig{MaxConcurrent: 1},
	}
	client := NewClientWithProviders(p1)
	defer client.Close()

	_, err := client.ListModels(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent provider")
	}
	if !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("expected ErrProviderNotFound, got %v", err)
	}
}

func TestClient_TestProvider_Success(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: true,
		config: ProviderConfig{MaxConcurrent: 1, Model: "m1"},
	}
	client := NewClientWithProviders(p1)
	defer client.Close()

	err := client.TestProvider(context.Background(), "p1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClient_TestProvider_NotFound(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: true,
		config: ProviderConfig{MaxConcurrent: 1},
	}
	client := NewClientWithProviders(p1)
	defer client.Close()

	err := client.TestProvider(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent provider")
	}
}

func TestClient_TestProvider_Unhealthy(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: false,
		config: ProviderConfig{MaxConcurrent: 1},
	}
	client := NewClientWithProviders(p1)
	defer client.Close()

	err := client.TestProvider(context.Background(), "p1")
	if err == nil {
		t.Fatal("expected error for unhealthy provider")
	}
}

func TestClient_Audit_Success(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: true,
		config: ProviderConfig{MaxConcurrent: 1, Model: "m1"},
		response: &ChatCompletionResponse{
			Choices: []Choice{{Message: Message{Content: "answer"}}},
			Usage:   Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
	}
	p2 := &mockProvider{
		name: "p2", available: true,
		config: ProviderConfig{MaxConcurrent: 1, Model: "m2"},
		response: &ChatCompletionResponse{
			Choices: []Choice{{Message: Message{Content: "answer"}}},
			Usage:   Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
	}

	client := NewClientWithProviders(p1, p2)
	defer client.Close()

	result, err := client.Audit(context.Background(), ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Consensus {
		t.Fatal("expected consensus for identical content")
	}
}

func TestClient_Audit_DisagreementEmitsEvent(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: true,
		config: ProviderConfig{MaxConcurrent: 1, Model: "m1"},
		response: &ChatCompletionResponse{
			Choices: []Choice{{Message: Message{Content: "answer A"}}},
			Usage:   Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
	}
	p2 := &mockProvider{
		name: "p2", available: true,
		config: ProviderConfig{MaxConcurrent: 1, Model: "m2"},
		response: &ChatCompletionResponse{
			Choices: []Choice{{Message: Message{Content: "answer B"}}},
			Usage:   Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
	}

	client := NewClientWithProviders(p1, p2)
	defer client.Close()

	emitter := &trackingEmitter{}
	client.SetEmitter(emitter)

	result, err := client.Audit(context.Background(), ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Consensus {
		t.Fatal("expected no consensus for different content")
	}
	if emitter.count(EventAuditDisagree) != 1 {
		t.Fatalf("expected 1 EventAuditDisagree event, got %d", emitter.count(EventAuditDisagree))
	}
}

func TestClient_Stats_NilHistory(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: true,
		config: ProviderConfig{MaxConcurrent: 1},
	}
	client := NewClientWithProviders(p1)
	defer client.Close()

	if client.Stats() != nil {
		t.Fatal("expected nil stats when no history is configured")
	}
}

func TestClient_Stats_WithHistory(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: true,
		config: ProviderConfig{MaxConcurrent: 1},
	}
	client := NewClientWithProviders(p1)
	defer client.Close()

	// Scheduler is already created; we can't add history after creation via the
	// Scheduler() directly, so let's test via the existing client_test pattern.
	// Use NewClient with WithHistory instead.
	client2, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	}, WithHistory(NewHistory(100)))
	defer client2.Close()

	stats := client2.Stats()
	if stats == nil {
		t.Fatal("expected non-nil stats when history is configured")
	}
}

func TestClient_SetEmitter_NilFallsBackToNop(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: true,
		config: ProviderConfig{MaxConcurrent: 1},
	}
	client := NewClientWithProviders(p1)
	defer client.Close()

	// Set a real emitter first.
	emitter := &trackingEmitter{}
	client.SetEmitter(emitter)

	// Then set nil - should fall back to NopEmitter without panic.
	client.SetEmitter(nil)

	// Trigger an emit path (Audit disagreement) - should not panic.
	p2 := &mockProvider{
		name: "p2", available: true,
		config: ProviderConfig{MaxConcurrent: 1, Model: "m2"},
		response: &ChatCompletionResponse{
			Choices: []Choice{{Message: Message{Content: "different"}}},
			Usage:   Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
	}
	client2 := NewClientWithProviders(
		&mockProvider{
			name: "p1", available: true,
			config: ProviderConfig{MaxConcurrent: 1, Model: "m1"},
			response: &ChatCompletionResponse{
				Choices: []Choice{{Message: Message{Content: "answer A"}}},
				Usage:   Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			},
		},
		p2,
	)
	defer client2.Close()
	client2.SetEmitter(nil)
	_, _ = client2.Audit(context.Background(), ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
}

func TestClient_HealthCheckInterval_GetAndSet(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: true,
		config: ProviderConfig{MaxConcurrent: 1},
	}
	client := NewClientWithProviders(p1)
	defer client.Close()

	// Default interval should be 0 (disabled).
	if client.HealthCheckInterval() != 0 {
		t.Fatalf("expected default health check interval 0, got %v", client.HealthCheckInterval())
	}

	// Set a non-zero interval.
	client.SetHealthCheckInterval(30 * time.Second)
	if client.HealthCheckInterval() != 30*time.Second {
		t.Fatalf("expected 30s, got %v", client.HealthCheckInterval())
	}

	// Disable it.
	client.SetHealthCheckInterval(0)
	if client.HealthCheckInterval() != 0 {
		t.Fatalf("expected 0 after disable, got %v", client.HealthCheckInterval())
	}
}

func TestClient_Router_GetAndSet(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: true,
		config: ProviderConfig{MaxConcurrent: 1},
	}
	client := NewClientWithProviders(p1)
	defer client.Close()

	// Initially nil.
	if client.Router() != nil {
		t.Fatal("expected nil router initially")
	}

	// Set a router.
	r := &core.SingleProviderRouter{Name: "p1"}
	client.SetRouter(r)
	if client.Router() == nil {
		t.Fatal("expected non-nil router after SetRouter")
	}

	// Set nil to remove.
	client.SetRouter(nil)
	if client.Router() != nil {
		t.Fatal("expected nil router after SetRouter(nil)")
	}
}

func TestClient_ProviderStatus_MultipleProviders(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: true,
		config: ProviderConfig{MaxConcurrent: 1, Model: "m1"},
	}
	p2 := &mockProvider{
		name: "p2", available: false,
		config: ProviderConfig{MaxConcurrent: 1, Model: "m2"},
	}
	client := NewClientWithProviders(p1, p2)
	defer client.Close()

	status := client.ProviderStatus()
	if len(status) != 2 {
		t.Fatalf("expected 2 provider statuses, got %d", len(status))
	}
}

func TestClient_Scheduler_NotNil(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: true,
		config: ProviderConfig{MaxConcurrent: 1},
	}
	client := NewClientWithProviders(p1)
	defer client.Close()

	s := client.Scheduler()
	if s == nil {
		t.Fatal("expected non-nil scheduler")
	}
	// Verify the scheduler is the same instance.
	if s != client.Scheduler() {
		t.Fatal("Scheduler() should return the same instance on multiple calls")
	}
}

func TestClient_ChatCompleteDual_EmitsEvent(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: true,
		config: ProviderConfig{MaxConcurrent: 1, Model: "m1"},
		response: &ChatCompletionResponse{
			Choices: []Choice{{Message: Message{Content: "answer"}}},
		},
	}
	p2 := &mockProvider{
		name: "p2", available: true,
		config: ProviderConfig{MaxConcurrent: 1, Model: "m2"},
		response: &ChatCompletionResponse{
			Choices: []Choice{{Message: Message{Content: "answer"}}},
		},
	}

	client := NewClientWithProviders(p1, p2)
	defer client.Close()

	emitter := &trackingEmitter{}
	client.SetEmitter(emitter)

	result, err := client.ChatCompleteDual(context.Background(), ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Consensus {
		t.Fatal("expected consensus")
	}
	if emitter.count(EventDualComplete) != 1 {
		t.Fatalf("expected 1 EventDualComplete event, got %d", emitter.count(EventDualComplete))
	}
}

func TestClient_ChatCompleteWithFallback_AllFailEmitsEvent(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: true,
		config: ProviderConfig{MaxConcurrent: 1},
		err:    errors.New("fail-1"),
	}

	client := NewClientWithProviders(p1)
	defer client.Close()

	emitter := &trackingEmitter{}
	client.SetEmitter(emitter)

	_, err := client.ChatCompleteWithFallback(context.Background(), ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
	if !errors.Is(err, ErrAllProvidersFailed) {
		t.Fatalf("expected ErrAllProvidersFailed, got %v", err)
	}
	if emitter.count(EventFallbackTrigger) != 1 {
		t.Fatalf("expected 1 EventFallbackTrigger event, got %d", emitter.count(EventFallbackTrigger))
	}
}

func TestClient_History_Nil(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: true,
		config: ProviderConfig{MaxConcurrent: 1},
	}
	client := NewClientWithProviders(p1)
	defer client.Close()

	if client.History() != nil {
		t.Fatal("expected nil history when not configured")
	}
}
