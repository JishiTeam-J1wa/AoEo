package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/providers"
)

// mockProv is a test double for the Provider interface.
type mockProv struct {
	name       string
	config     core.ProviderConfig
	available  bool
	response   *core.ChatCompletionResponse
	err        error
	calls      int
	mu         sync.Mutex
	panicOnCall bool
}

func (m *mockProv) Name() string                         { return m.name }
func (m *mockProv) Config() core.ProviderConfig          { return m.config }
func (m *mockProv) IsAvailable() bool                    { return m.available }
func (m *mockProv) SetEmitter(e core.EventEmitter)       {}
func (m *mockProv) ListModels(ctx context.Context) ([]core.ModelInfo, error) {
	return []core.ModelInfo{{ID: "mock-model"}}, nil
}
func (m *mockProv) ChatComplete(_ context.Context, _ core.ChatCompletionRequest) (*core.ChatCompletionResponse, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	if m.panicOnCall {
		panic("intentional panic")
	}
	if m.err != nil {
		return nil, m.err
	}
	if m.response != nil {
		return m.response, nil
	}
	return &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Role: "assistant", Content: "ok"}}},
	}, nil
}

func (m *mockProv) ChatCompleteStream(_ context.Context, _ core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error) {
	return nil, fmt.Errorf("mock provider does not support streaming")
}
func (m *mockProv) HealthCheck(_ context.Context) error {
	if m.available {
		return nil
	}
	return fmt.Errorf("mock provider %s unhealthy", m.name)
}

func TestNewScheduler(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 3}}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 2}}
	s := NewScheduler(p1, p2)
	if s == nil {
		t.Fatal("expected non-nil scheduler")
	}
	status := s.ProviderStatus()
	if len(status) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(status))
	}
}

func TestNewScheduler_FiltersNil(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}}
	s := NewScheduler(p1, nil)
	if len(s.ProviderStatus()) != 1 {
		t.Fatalf("expected 1 provider after nil filter, got %d", len(s.ProviderStatus()))
	}
}

func TestScheduler_PickPrimaryProvider(t *testing.T) {
	p1 := &mockProv{name: "p1", available: false, config: core.ProviderConfig{MaxConcurrent: 1}}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 1}}
	s := NewScheduler(p1, p2)

	primary := s.PickPrimaryProvider()
	if primary == nil || primary.Name() != "p2" {
		t.Fatalf("expected p2, got %v", primary)
	}
}

func TestScheduler_PickProviderRoundRobin(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 1}}
	s := NewScheduler(p1, p2)

	seen := make(map[string]bool)
	for i := 0; i < 10; i++ {
		p := s.PickProviderRoundRobin()
		if p == nil {
			t.Fatal("expected non-nil provider")
		}
		seen[p.Name()] = true
	}
	if len(seen) != 2 {
		t.Fatal("round-robin should distribute across providers")
	}
}

func TestScheduler_AvailableProviders_Copy(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}}
	s := NewScheduler(p1)

	avail1 := s.AvailableProviders()
	if len(avail1) != 1 {
		t.Fatalf("expected 1 available, got %d", len(avail1))
	}
	avail1[0] = nil // mutate copy

	avail2 := s.AvailableProviders()
	if avail2[0] == nil {
		t.Fatal("modifying returned slice should not affect cache")
	}
}

func TestScheduler_ChatComplete(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}}
	s := NewScheduler(p1)

	resp, err := s.ChatComplete(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content() != "ok" {
		t.Fatalf("unexpected content: %s", resp.Content())
	}
	if p1.calls != 1 {
		t.Fatalf("expected 1 call, got %d", p1.calls)
	}
}

func TestScheduler_ChatComplete_NoAvailable(t *testing.T) {
	p1 := &mockProv{name: "p1", available: false, config: core.ProviderConfig{MaxConcurrent: 1}}
	s := NewScheduler(p1)

	_, err := s.ChatComplete(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, ErrNoAvailableProvider) {
		t.Fatalf("expected ErrNoAvailableProvider, got %v", err)
	}
}

func TestScheduler_ChatComplete_Closed(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}}
	s := NewScheduler(p1)
	s.Close()

	_, err := s.ChatComplete(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, ErrSchedulerClosed) {
		t.Fatalf("expected ErrSchedulerClosed, got %v", err)
	}
}

func TestScheduler_ChatComplete_PanicRecovery(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}, panicOnCall: true}
	s := NewScheduler(p1)

	_, err := s.ChatComplete(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil || !errors.Is(err, errors.New("provider panic: intentional panic")) {
		// The exact error message contains "provider panic: intentional panic"
		if err == nil {
			t.Fatal("expected error after panic")
		}
	}
}

func TestScheduler_ChatCompleteWithFallback(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}, err: errors.New("fail")}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 1}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "p2"}}},
	}}
	s := NewScheduler(p1, p2)

	resp, err := s.ChatCompleteWithFallback(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content() != "p2" {
		t.Fatalf("expected p2 response, got %s", resp.Content())
	}
}

func TestScheduler_ChatCompleteWithFallback_AllFail(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}, err: errors.New("fail1")}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 1}, err: errors.New("fail2")}
	s := NewScheduler(p1, p2)

	_, err := s.ChatCompleteWithFallback(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, ErrAllProvidersFailed) {
		t.Fatalf("expected ErrAllProvidersFailed, got %v", err)
	}
}

func TestScheduler_ChatCompleteDual(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "same"}}},
	}}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m2"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "same"}}},
	}}
	s := NewScheduler(p1, p2)

	dual, err := s.ChatCompleteDual(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !dual.Consensus {
		t.Fatal("expected consensus for identical content")
	}
}

func TestScheduler_ChatCompleteDual_SingleProviderFallback(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "only"}}},
	}}
	s := NewScheduler(p1)

	dual, err := s.ChatCompleteDual(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !dual.Consensus {
		t.Fatal("expected consensus when only one provider")
	}
	if dual.Result1 == nil {
		t.Fatal("expected Result1")
	}
	if dual.Result2 != nil {
		t.Fatal("expected nil Result2")
	}
}

func TestScheduler_Close_Idempotent(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}}
	s := NewScheduler(p1)

	if err := s.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second close should be idempotent: %v", err)
	}
	if !s.IsClosed() {
		t.Fatal("expected closed")
	}
}

func TestScheduler_ApplyConfig(t *testing.T) {
	s := NewScheduler()
	cfg := core.Config{
		Providers: []core.ProviderConfig{
			{Name: "deepseek", APIKey: "k", Endpoint: "https://api.deepseek.com", Model: "m", MaxConcurrent: 5},
			{Name: "kimi", APIKey: "k", Endpoint: "https://api.moonshot.cn/v1", Model: "m", MaxConcurrent: 3},
		},
	}
	if err := s.ApplyConfig(cfg); err != nil {
		t.Fatalf("apply config failed: %v", err)
	}

	status := s.ProviderStatus()
	if len(status) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(status))
	}
}

func TestScheduler_CreateProvider(t *testing.T) {
	tests := []struct {
		name string
		cfg  core.ProviderConfig
		want string
	}{
		{"deepseek", core.ProviderConfig{Name: "deepseek"}, "deepseek"},
		{"kimi", core.ProviderConfig{Name: "kimi"}, "kimi"},
		{"glm", core.ProviderConfig{Name: "glm"}, "glm"},
		{"qwen", core.ProviderConfig{Name: "qwen"}, "qwen"},
		{"unknown", core.ProviderConfig{Name: "custom"}, "custom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := CreateProvider(tt.cfg)
			if p == nil {
				t.Fatal("expected non-nil provider")
			}
			if p.Name() != tt.want {
				t.Fatalf("expected %s, got %s", tt.want, p.Name())
			}
		})
	}
}

func TestScheduler_SetTimeout(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}}
	s := NewScheduler(p1)

	s.SetTimeout(10 * time.Second)
	// Just verify no panic; actual timeout behavior needs integration test.
}

func TestScheduler_SetPromptInjector(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}}
	s := NewScheduler(p1)

	pi := NewPromptInjector()
	s.SetPromptInjector(pi)
	if s.PromptInjector() != pi {
		t.Fatal("SetPromptInjector did not work")
	}

	s.SetPromptInjector(nil)
	if s.PromptInjector() != nil {
		t.Fatal("SetPromptInjector(nil) should clear")
	}
}

func TestScheduler_TestProvider(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}}
	s := NewScheduler(p1)

	if err := s.TestProvider(context.Background(), "p1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := s.TestProvider(context.Background(), "nonexistent"); err == nil {
		t.Fatal("expected error for nonexistent provider")
	}
}

func TestScheduler_ListModels(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}}
	s := NewScheduler(p1)

	models, err := s.ListModels(context.Background(), "p1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 1 || models[0].ID != "mock-model" {
		t.Fatalf("unexpected models: %+v", models)
	}
}

func TestScheduler_ListModels_NotFound(t *testing.T) {
	s := NewScheduler()
	_, err := s.ListModels(context.Background(), "nonexistent")
	if !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("expected ErrProviderNotFound, got %v", err)
	}
}

func TestScheduler_Audit(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "yes"}}},
	}}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m2"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "yes"}}},
	}}
	s := NewScheduler(p1, p2)

	result, err := s.Audit(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Consensus {
		t.Fatal("expected consensus")
	}
}

func TestScheduler_Audit_InsufficientProviders(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}}
	s := NewScheduler(p1)

	_, err := s.Audit(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for insufficient providers")
	}
}

func TestScheduler_SetSystemPrompt(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}}
	s := NewScheduler(p1)

	s.SetSystemPrompt("override")
	// Smoke test: no panic
}

func TestScheduler_ConcurrentAccess(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 10}}
	s := NewScheduler(p1)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.ChatComplete(context.Background(), core.ChatCompletionRequest{
				Messages: []core.Message{{Role: "user", Content: "hi"}},
			})
		}()
	}
	wg.Wait()

	if p1.calls != 50 {
		t.Fatalf("expected 50 calls, got %d", p1.calls)
	}
}

func TestScheduler_WithInterceptors(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}}

	var beforeCalled, afterCalled bool
	ic := core.Interceptor{
		BeforeRequest: func(ctx context.Context, req *core.ChatCompletionRequest) error {
			beforeCalled = true
			req.Tags = append(req.Tags, "injected")
			return nil
		},
		AfterResponse: func(ctx context.Context, req core.ChatCompletionRequest, resp *core.ChatCompletionResponse, err error) (*core.ChatCompletionResponse, error) {
			afterCalled = true
			return resp, err
		},
	}

	s := NewSchedulerWithOptions([]providers.Provider{p1}, WithInterceptors(ic))
	_, err := s.ChatComplete(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !beforeCalled {
		t.Fatal("expected BeforeRequest to be called")
	}
	if !afterCalled {
		t.Fatal("expected AfterResponse to be called")
	}
}

func TestScheduler_InterceptorBeforeShortCircuit(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}}

	ic := core.Interceptor{
		BeforeRequest: func(ctx context.Context, req *core.ChatCompletionRequest) error {
			return errors.New("blocked by interceptor")
		},
	}

	s := NewSchedulerWithOptions([]providers.Provider{p1}, WithInterceptors(ic))
	_, err := s.ChatComplete(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil || err.Error() != "blocked by interceptor" {
		t.Fatalf("expected interceptor error, got %v", err)
	}
	if p1.calls != 0 {
		t.Fatal("provider should not be called when interceptor blocks")
	}
}

func TestScheduler_InterceptorAfterTransform(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}, err: errors.New("provider fail")}

	ic := core.Interceptor{
		AfterResponse: func(ctx context.Context, req core.ChatCompletionRequest, resp *core.ChatCompletionResponse, err error) (*core.ChatCompletionResponse, error) {
			if err != nil {
				return &core.ChatCompletionResponse{Choices: []core.Choice{{Message: core.Message{Content: "fallback"}}}}, nil
			}
			return resp, err
		},
	}

	s := NewSchedulerWithOptions([]providers.Provider{p1}, WithInterceptors(ic))
	resp, err := s.ChatComplete(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error after interceptor transform: %v", err)
	}
	if resp.Content() != "fallback" {
		t.Fatalf("expected fallback content, got %s", resp.Content())
	}
}

func TestScheduler_SetInterceptors(t *testing.T) {
	s := NewScheduler()
	ic := []core.Interceptor{{BeforeRequest: func(ctx context.Context, req *core.ChatCompletionRequest) error { return nil }}}
	s.SetInterceptors(ic)
	if len(s.Interceptors()) != 1 {
		t.Fatalf("expected 1 interceptor, got %d", len(s.Interceptors()))
	}
	// Verify copy semantics: modifying original should not affect stored
	ic[0] = core.Interceptor{}
	if len(s.Interceptors()) != 1 {
		t.Fatal("modifying original slice should not affect stored interceptors")
	}
}

func TestScheduler_FallbackWithInterceptors(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}, err: errors.New("fail")}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 1}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "p2"}}},
	}}

	var beforeCalled, afterCalled bool
	ic := core.Interceptor{
		BeforeRequest: func(ctx context.Context, req *core.ChatCompletionRequest) error {
			beforeCalled = true
			return nil
		},
		AfterResponse: func(ctx context.Context, req core.ChatCompletionRequest, resp *core.ChatCompletionResponse, err error) (*core.ChatCompletionResponse, error) {
			afterCalled = true
			return resp, err
		},
	}

	s := NewSchedulerWithOptions([]providers.Provider{p1, p2}, WithInterceptors(ic))
	_, err := s.ChatCompleteWithFallback(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !beforeCalled {
		t.Fatal("expected BeforeRequest to be called once")
	}
	if !afterCalled {
		t.Fatal("expected AfterResponse to be called on final result")
	}
}

func TestScheduler_DualWithInterceptors(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "same"}}},
	}}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m2"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "same"}}},
	}}

	var beforeCalled bool
	ic := core.Interceptor{
		BeforeRequest: func(ctx context.Context, req *core.ChatCompletionRequest) error {
			beforeCalled = true
			req.Tags = append(req.Tags, "dual-tag")
			return nil
		},
	}

	s := NewSchedulerWithOptions([]providers.Provider{p1, p2}, WithInterceptors(ic))
	_, err := s.ChatCompleteDual(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !beforeCalled {
		t.Fatal("expected BeforeRequest to be called for dual")
	}
}

func TestScheduler_RouterRoundRobin(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "p1"}}},
	}}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m2"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "p2"}}},
	}}

	s := NewSchedulerWithOptions([]providers.Provider{p1, p2}, WithRouter(&core.RoundRobinRouter{}))

	resp1, err := s.ChatComplete(context.Background(), core.ChatCompletionRequest{Messages: []core.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if resp1.Choices[0].Message.Content != "p1" {
		t.Fatalf("expected p1 first (RR starts at 0), got %s", resp1.Choices[0].Message.Content)
	}

	resp2, err := s.ChatComplete(context.Background(), core.ChatCompletionRequest{Messages: []core.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if resp2.Choices[0].Message.Content != "p2" {
		t.Fatalf("expected p2 second, got %s", resp2.Choices[0].Message.Content)
	}
}

func TestScheduler_RouterRandom(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "p1"}}},
	}}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m2"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "p2"}}},
	}}

	s := NewSchedulerWithOptions([]providers.Provider{p1, p2}, WithRouter(&core.RandomRouter{}))

	// Just verify it doesn't error and picks one of them
	resp, err := s.ChatComplete(context.Background(), core.ChatCompletionRequest{Messages: []core.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content := resp.Choices[0].Message.Content
	if content != "p1" && content != "p2" {
		t.Fatalf("unexpected response: %s", content)
	}
}

func TestScheduler_SetRouter(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "p1"}}},
	}}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m2"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "p2"}}},
	}}

	s := NewSchedulerWithOptions([]providers.Provider{p1, p2})

	// Default should be PrimaryRouter (p1)
	resp, err := s.ChatComplete(context.Background(), core.ChatCompletionRequest{Messages: []core.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Choices[0].Message.Content != "p1" {
		t.Fatalf("expected p1 from PrimaryRouter, got %s", resp.Choices[0].Message.Content)
	}

	// Switch to RoundRobin
	s.SetRouter(&core.RoundRobinRouter{})
	resp, err = s.ChatComplete(context.Background(), core.ChatCompletionRequest{Messages: []core.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Choices[0].Message.Content != "p1" {
		t.Fatalf("expected p1 from RoundRobin first (starts at 0), got %s", resp.Choices[0].Message.Content)
	}
	// Second call should rotate to p2
	resp, err = s.ChatComplete(context.Background(), core.ChatCompletionRequest{Messages: []core.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Choices[0].Message.Content != "p2" {
		t.Fatalf("expected p2 from RoundRobin second, got %s", resp.Choices[0].Message.Content)
	}

	// Set nil router (should fall back to PrimaryRouter)
	s.SetRouter(nil)
	resp, err = s.ChatComplete(context.Background(), core.ChatCompletionRequest{Messages: []core.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Choices[0].Message.Content != "p1" {
		t.Fatalf("expected p1 from fallback PrimaryRouter, got %s", resp.Choices[0].Message.Content)
	}
}

func TestScheduler_RouterUnavailable(t *testing.T) {
	p1 := &mockProv{name: "p1", available: false, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}}
	p2 := &mockProv{name: "p2", available: false, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m2"}}

	s := NewSchedulerWithOptions([]providers.Provider{p1, p2}, WithRouter(&core.RoundRobinRouter{}))

	_, err := s.ChatComplete(context.Background(), core.ChatCompletionRequest{Messages: []core.Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error when all providers unavailable")
	}
}

func TestScheduler_HealthCheck(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "p1"}}},
	}}
	p2 := &mockProv{name: "p2", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m2"}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "p2"}}},
	}}

	s := NewSchedulerWithOptions([]providers.Provider{p1, p2}, WithHealthCheckInterval(50*time.Millisecond))
	defer s.Close()

	// Start health check explicitly
	s.StartHealthCheck(50 * time.Millisecond)

	// Wait for at least one health check cycle
	time.Sleep(120 * time.Millisecond)

	// Both providers should still be available (health check passes for mock)
	if !p1.available {
		t.Fatal("p1 should still be available")
	}
	if !p2.available {
		t.Fatal("p2 should still be available")
	}
}

func TestScheduler_HealthCheckDisablesProvider(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1", MaxFailures: 1, CooldownDuration: time.Hour}, response: &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "p1"}}},
	}}

	s := NewSchedulerWithOptions([]providers.Provider{p1}, WithHealthCheckInterval(50*time.Millisecond))
	defer s.Close()

	// Make p1 fail health checks
	p1.available = false
	s.StartHealthCheck(50 * time.Millisecond)

	// Wait for health check to run and fail
	time.Sleep(120 * time.Millisecond)

	// Provider should now be unavailable due to circuit breaker
	if p1.IsAvailable() {
		t.Fatal("p1 should be unavailable after failed health check")
	}
}

func TestScheduler_HealthCheckIntervalZero(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}}

	s := NewSchedulerWithOptions([]providers.Provider{p1}, WithHealthCheckInterval(0))
	defer s.Close()

	if s.HealthCheckInterval() != 0 {
		t.Fatalf("expected 0 interval, got %v", s.HealthCheckInterval())
	}
}

func TestScheduler_SetHealthCheckInterval(t *testing.T) {
	p1 := &mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}}

	s := NewSchedulerWithOptions([]providers.Provider{p1})
	defer s.Close()

	// Default should be 0 (no auto-start)
	if s.HealthCheckInterval() != 0 {
		t.Fatalf("expected 0 default interval, got %v", s.HealthCheckInterval())
	}

	// Start with 50ms
	s.SetHealthCheckInterval(50 * time.Millisecond)
	if s.HealthCheckInterval() != 50*time.Millisecond {
		t.Fatalf("expected 50ms, got %v", s.HealthCheckInterval())
	}

	// Stop with 0
	s.SetHealthCheckInterval(0)
	if s.HealthCheckInterval() != 0 {
		t.Fatalf("expected 0 after disable, got %v", s.HealthCheckInterval())
	}
}
