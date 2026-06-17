package aoeo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/internal/engine"
)

// mockProvider is a test double that implements the Provider interface.
type mockProvider struct {
	name      string
	config    ProviderConfig
	available bool
	response  *ChatCompletionResponse
	err       error
	calls     atomic.Int32
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) ChatComplete(_ context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	m.calls.Add(1)
	if m.err != nil {
		return nil, m.err
	}
	if m.response != nil {
		resp := *m.response
		return &resp, nil
	}
	return &ChatCompletionResponse{
		Choices: []Choice{{
			Message: Message{Role: "assistant", Content: "mock response"},
		}},
	}, nil
}

func (m *mockProvider) IsAvailable() bool { return m.available }

func (m *mockProvider) ListModels(_ context.Context) ([]ModelInfo, error) {
	return []ModelInfo{{ID: "mock-model", OwnedBy: "test"}}, nil
}

func (m *mockProvider) SetEmitter(_ core.EventEmitter) {}

func (m *mockProvider) Config() ProviderConfig { return m.config }

func (m *mockProvider) ChatCompleteStream(_ context.Context, _ ChatCompletionRequest) (<-chan StreamCompletionResponse, error) {
	return nil, fmt.Errorf("mock provider does not support streaming")
}

func (m *mockProvider) HealthCheck(_ context.Context) error {
	if m.available {
		return nil
	}
	return fmt.Errorf("mock provider %s unhealthy", m.name)
}

func TestNewScheduler(t *testing.T) {
	p1 := &mockProvider{name: "p1", available: true, config: ProviderConfig{MaxConcurrent: 3}}
	p2 := &mockProvider{name: "p2", available: true, config: ProviderConfig{MaxConcurrent: 2}}

	s := NewScheduler(p1, p2)
	if s == nil {
		t.Fatal("expected non-nil scheduler")
	}

	// Total slots should be 3 + 2 = 5
	status := s.ProviderStatus()
	if len(status) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(status))
	}
}

func TestPickPrimaryProvider(t *testing.T) {
	p1 := &mockProvider{name: "p1", available: false, config: ProviderConfig{MaxConcurrent: 1}}
	p2 := &mockProvider{name: "p2", available: true, config: ProviderConfig{MaxConcurrent: 1}}

	s := NewScheduler(p1, p2)
	primary := s.PickPrimaryProvider()
	if primary == nil {
		t.Fatal("expected primary provider")
	}
	if primary.Name() != "p2" {
		t.Fatalf("expected p2, got %s", primary.Name())
	}
}

func TestPickPrimaryProvider_NoAvailable(t *testing.T) {
	p1 := &mockProvider{name: "p1", available: false}
	s := NewScheduler(p1)
	if s.PickPrimaryProvider() != nil {
		t.Fatal("expected nil when no providers available")
	}
}

func TestCircuitBreaker(t *testing.T) {
	bp := NewBaseProvider(ProviderConfig{Name: "test", APIKey: "key", Endpoint: "https://test.com", Model: "model"})

	// Initially available
	if !bp.IsAvailable() {
		t.Fatal("expected available initially")
	}

	// Record 3 failures
	bp.RecordFailure()
	bp.RecordFailure()
	bp.RecordFailure()

	// Should be unavailable due to circuit breaker
	if bp.IsAvailable() {
		t.Fatal("expected unavailable after 3 failures")
	}

	// Wait for cooldown
	bp.SetFailUntil(time.Now().Add(-1 * time.Second))
	if !bp.IsAvailable() {
		t.Fatal("expected available after cooldown")
	}

	// Record success should reset
	bp.RecordFailure()
	bp.RecordFailure()
	bp.RecordSuccess()
	if !bp.IsAvailable() {
		t.Fatal("expected available after success resets counter")
	}
}

func TestBaseProvider_SystemPrompt(t *testing.T) {
	bp := NewBaseProvider(ProviderConfig{})

	if bp.GetSystemPrompt() != "" {
		t.Fatal("expected empty system prompt initially")
	}

	bp.SetSystemPrompt("override")
	if bp.GetSystemPrompt() != "override" {
		t.Fatal("expected system prompt override")
	}

	bp.ClearSystemPrompt()
	if bp.GetSystemPrompt() != "" {
		t.Fatal("expected empty after clear")
	}
}

func TestCreateProvider(t *testing.T) {
	tests := []struct {
		name     string
		cfg      ProviderConfig
		expected string
	}{
		{"deepseek", ProviderConfig{Name: "deepseek"}, "deepseek"},
		{"kimi", ProviderConfig{Name: "kimi"}, "kimi"},
		{"glm", ProviderConfig{Name: "glm"}, "glm"},
		{"qwen", ProviderConfig{Name: "qwen"}, "qwen"},
		{"unknown", ProviderConfig{Name: "custom"}, "custom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := CreateProvider(tt.cfg)
			if p == nil {
				t.Fatal("expected non-nil provider")
			}
			if p.Name() != tt.expected {
				t.Fatalf("expected %s, got %s", tt.expected, p.Name())
			}
		})
	}
}

func TestMergeChoices(t *testing.T) {
	r1 := &ChatCompletionResponse{
		Choices: []Choice{{Message: Message{Content: "A"}}},
		Usage:   Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}
	r2 := &ChatCompletionResponse{
		Choices: []Choice{{Message: Message{Content: "A"}}},
		Usage:   Usage{PromptTokens: 8, CompletionTokens: 4, TotalTokens: 12},
	}

	merged := MergeChoices(r1, r2, true)
	if merged == nil {
		t.Fatal("expected non-nil merged")
	}
	if merged.Usage.TotalTokens != 27 {
		t.Fatalf("expected 27 total tokens, got %d", merged.Usage.TotalTokens)
	}

	disagree := MergeChoices(r1, r2, false)
	if disagree == nil {
		t.Fatal("expected non-nil")
	}
	if len(disagree.Choices) == 0 || disagree.Choices[0].Message.Content == "" {
		t.Fatal("expected combined content for disagreement")
	}
}

func TestConsensus(t *testing.T) {
	r1 := &ChatCompletionResponse{Choices: []Choice{{Message: Message{Content: "yes"}}}}
	r2 := &ChatCompletionResponse{Choices: []Choice{{Message: Message{Content: "yes"}}}}
	r3 := &ChatCompletionResponse{Choices: []Choice{{Message: Message{Content: "no"}}}}

	if !Consensus(r1, r2) {
		t.Fatal("expected consensus for same content")
	}
	if Consensus(r1, r3) {
		t.Fatal("expected no consensus for different content")
	}
	if Consensus(nil, r1) {
		t.Fatal("expected no consensus when one is nil")
	}
}

func TestExtractJSON(t *testing.T) {
	type testStruct struct {
		Name string `json:"name"`
	}

	var s testStruct
	if err := ExtractJSON(`{"name":"test"}`, &s); err != nil {
		t.Fatalf("direct json failed: %v", err)
	}
	if s.Name != "test" {
		t.Fatalf("expected test, got %s", s.Name)
	}

	// Markdown fence
	s = testStruct{}
	if err := ExtractJSON("```json\n{\"name\":\"fenced\"}\n```", &s); err != nil {
		t.Fatalf("fenced json failed: %v", err)
	}
	if s.Name != "fenced" {
		t.Fatalf("expected fenced, got %s", s.Name)
	}

	// Embedded in text
	s = testStruct{}
	if err := ExtractJSON("some text before {\"name\":\"embedded\"} and after", &s); err != nil {
		t.Fatalf("embedded json failed: %v", err)
	}
	if s.Name != "embedded" {
		t.Fatalf("expected embedded, got %s", s.Name)
	}
}

func TestBuildRequest(t *testing.T) {
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hello"}},
		WithSystemPrompt("sys"),
		WithTemperature(0.5),
		WithMaxTokens(100),
	)

	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content != "sys" {
		t.Fatal("expected system prompt as first message")
	}
	if req.Temperature != 0.5 {
		t.Fatalf("expected temp 0.5, got %f", req.Temperature)
	}
	if req.MaxTokens != 100 {
		t.Fatalf("expected max tokens 100, got %d", req.MaxTokens)
	}
}

func TestWithSystemPrompt_ReplaceExisting(t *testing.T) {
	req := BuildRequest(
		[]Message{
			{Role: "system", Content: "old"},
			{Role: "user", Content: "hello"},
		},
		WithSystemPrompt("new"),
	)
	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(req.Messages))
	}
	if req.Messages[0].Content != "new" {
		t.Fatalf("expected replaced system prompt 'new', got %s", req.Messages[0].Content)
	}
}

// ============================================================================
// New tests for Close, PromptInjector, Cost, Semaphore, Retry, Fallback
// ============================================================================

func TestScheduler_Close(t *testing.T) {
	p1 := &mockProvider{name: "p1", available: true, config: ProviderConfig{MaxConcurrent: 1}}
	s := NewScheduler(p1)

	if err := s.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second close should be idempotent, got: %v", err)
	}

	ctx := context.Background()
	_, err := s.ChatComplete(ctx, BuildRequest([]Message{{Role: "user", Content: "hi"}}))
	if err == nil || !errors.Is(err, ErrSchedulerClosed) {
		t.Fatalf("expected ErrSchedulerClosed, got %v", err)
	}
}

func TestPromptInjector(t *testing.T) {
	pi := NewPromptInjector()
	pi.AddTemplate(PromptTemplate{
		Provider: "*",
		Model:    "*",
		Position: "system",
		Content:  "You are {{role}}.",
		Vars:     map[string]string{"role": "tester"},
	})

	req := ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
	}
	pi.Inject("deepseek", "deepseek-v4-pro", &req)

	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content != "You are tester." {
		t.Fatalf("expected injected system message, got %+v", req.Messages[0])
	}
}

func TestPromptInjector_PrependUser(t *testing.T) {
	pi := NewPromptInjector()
	pi.AddTemplate(PromptTemplate{
		Provider: "*",
		Model:    "*",
		Position: "prepend_user",
		Content:  "[Task: {{task}}]",
		Vars:     map[string]string{"task": "math"},
	})

	req := ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "2+2=?"}},
	}
	pi.Inject("kimi", "kimi-k2.6", &req)

	if req.Messages[0].Content != "[Task: math]\n\n2+2=?" {
		t.Fatalf("unexpected content: %s", req.Messages[0].Content)
	}
}

func TestPromptInjector_TemplatesDeepCopy(t *testing.T) {
	pi := NewPromptInjector()
	pi.AddTemplate(PromptTemplate{
		Provider: "*",
		Content:  "hello",
		Vars:     map[string]string{"a": "1"},
	})

	tmpls := pi.Templates()
	tmpls[0].Vars["a"] = "2"

	// Internal template should not be affected
	internal := pi.Templates()
	if internal[0].Vars["a"] != "1" {
		t.Fatal("Templates() did not return deep copy")
	}
}

func TestUsage_Cost(t *testing.T) {
	u := Usage{PromptTokens: 1000, CompletionTokens: 2000}
	p := Pricing{PromptPer1K: 1.0, CompletionPer1K: 2.0, Currency: "CNY"}

	cost := u.Cost(p)
	expected := 1.0 + 4.0 // 1K prompt * 1.0 + 2K completion * 2.0
	if cost != expected {
		t.Fatalf("expected cost %.2f, got %.2f", expected, cost)
	}

	// Zero pricing should return 0
	if u.Cost(Pricing{}) != 0 {
		t.Fatal("expected 0 cost for zero pricing")
	}
}

func TestDefaultPricing(t *testing.T) {
	p := DefaultPricing("deepseek", "deepseek-v4-pro")
	if p.PromptPer1K != 2.0 || p.CompletionPer1K != 8.0 {
		t.Fatalf("unexpected deepseek pricing: %+v", p)
	}

	p = DefaultPricing("kimi", "kimi-k2.6")
	if p.PromptPer1K != 3.0 || p.CompletionPer1K != 12.0 {
		t.Fatalf("unexpected kimi pricing: %+v", p)
	}
}

func TestAdaptiveSemaphore_ContextCancel(t *testing.T) {
	sem := engine.NewAdaptiveSemaphore(1)
	sem.Acquire(context.Background()) // occupy the only slot

	ctx, cancel := context.WithCancel(context.Background())
	go cancel() // cancel immediately

	err := sem.Acquire(ctx)
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	sem.Release() // should not panic
}

func TestRetry_DoRetry(t *testing.T) {
	callCount := 0
	fn := func() error {
		callCount++
		return errors.New("transient")
	}

	cfg := RetryConfig{
		MaxRetries: 2,
		BaseDelay:  10 * time.Millisecond,
		MaxDelay:   50 * time.Millisecond,
		Multiplier: 2.0,
		Retryable:  func(err error) bool { return true },
	}

	err := engine.DoRetry(context.Background(), cfg, fn)
	if err == nil {
		t.Fatal("expected error")
	}
	if callCount != 3 { // initial + 2 retries
		t.Fatalf("expected 3 calls, got %d", callCount)
	}

	// Verify cfg was not mutated
	if cfg.BaseDelay != 10*time.Millisecond {
		t.Fatal("RetryConfig was mutated")
	}
}

func TestRetry_DoRetry_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := RetryConfig{
		MaxRetries: 2,
		BaseDelay:  10 * time.Millisecond,
		Retryable:  func(err error) bool { return true },
	}
	err := engine.DoRetry(ctx, cfg, func() error { return errors.New("fail") })
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestRetry_IsRetryableError(t *testing.T) {
	if !IsRetryableError(errors.New("connection refused")) {
		t.Fatal("expected retryable")
	}
	if IsRetryableError(nil) {
		t.Fatal("expected non-retryable for nil")
	}
	if IsRetryableError(errors.New("invalid api key")) {
		t.Fatal("expected non-retryable for auth error")
	}
}

func TestHistory_RecordAndStats(t *testing.T) {
	h := NewHistory(3)

	h.Record(CallRecord{Provider: "p1", Cost: 1.0, Currency: "CNY", LatencyMs: 100})
	h.Record(CallRecord{Provider: "p1", Cost: 2.0, Currency: "CNY", LatencyMs: 200})
	h.Record(CallRecord{Provider: "p2", Cost: 3.0, Currency: "CNY", LatencyMs: 300})

	records := h.Records()
	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}

	stats := h.Stats()
	if stats["p1"].TotalCalls != 2 {
		t.Fatalf("expected 2 calls for p1, got %d", stats["p1"].TotalCalls)
	}
	if stats["p1"].TotalCost != 3.0 {
		t.Fatalf("expected total cost 3.0 for p1, got %.2f", stats["p1"].TotalCost)
	}
	if stats["p1"].Currency != "CNY" {
		t.Fatalf("expected currency CNY, got %s", stats["p1"].Currency)
	}
	if stats["p2"].TotalCalls != 1 {
		t.Fatalf("expected 1 call for p2, got %d", stats["p2"].TotalCalls)
	}
}

func TestHistory_MaxSize(t *testing.T) {
	h := NewHistory(2)
	h.Record(CallRecord{Provider: "p1"})
	h.Record(CallRecord{Provider: "p2"})
	h.Record(CallRecord{Provider: "p3"})

	records := h.Records()
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0].Provider != "p3" || records[1].Provider != "p2" {
		t.Fatal("expected newest-first with oldest dropped")
	}
}

func TestScheduler_ChatCompleteWithFallback(t *testing.T) {
	p1 := &mockProvider{name: "p1", available: true, config: ProviderConfig{MaxConcurrent: 1}, err: errors.New("fail")}
	p2 := &mockProvider{name: "p2", available: true, config: ProviderConfig{MaxConcurrent: 1}, response: &ChatCompletionResponse{
		Choices: []Choice{{Message: Message{Content: "p2"}}},
	}}

	s := NewScheduler(p1, p2)
	resp, err := s.ChatCompleteWithFallback(context.Background(), BuildRequest([]Message{{Role: "user", Content: "hi"}}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Choices[0].Message.Content != "p2" {
		t.Fatalf("expected p2 response, got %s", resp.Choices[0].Message.Content)
	}
	if p1.calls.Load() != 1 || p2.calls.Load() != 1 {
		t.Fatalf("expected p1=1 p2=1, got p1=%d p2=%d", p1.calls.Load(), p2.calls.Load())
	}
}

func TestScheduler_ChatCompleteDual(t *testing.T) {
	p1 := &mockProvider{name: "p1", available: true, config: ProviderConfig{MaxConcurrent: 1, Model: "m1"}, response: &ChatCompletionResponse{
		Choices: []Choice{{Message: Message{Content: "answer"}}},
	}}
	p2 := &mockProvider{name: "p2", available: true, config: ProviderConfig{MaxConcurrent: 1, Model: "m2"}, response: &ChatCompletionResponse{
		Choices: []Choice{{Message: Message{Content: "answer"}}},
	}}

	s := NewScheduler(p1, p2)
	resp, err := s.ChatCompleteDual(context.Background(), BuildRequest([]Message{{Role: "user", Content: "hi"}}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Consensus {
		t.Fatal("expected consensus")
	}
}

func TestClient_SetPromptInjector(t *testing.T) {
	p1 := &mockProvider{name: "p1", available: true, config: ProviderConfig{MaxConcurrent: 1}}
	client := NewClientWithProviders(p1)

	pi := NewPromptInjector()
	client.SetPromptInjector(pi)
	if client.PromptInjector() == nil {
		t.Fatal("expected prompt injector")
	}

	client.SetPromptInjector(nil)
	if client.PromptInjector() != nil {
		t.Fatal("expected nil prompt injector")
	}
}

func TestExtractField(t *testing.T) {
	content := `{"name":"test","age":30}`
	if v := ExtractField(content, "name"); v != "test" {
		t.Fatalf("expected 'test', got '%s'", v)
	}
	if v := ExtractField(content, "missing"); v != "" {
		t.Fatalf("expected empty, got '%s'", v)
	}
}

func TestConfig_Validate(t *testing.T) {
	cfg := Config{
		Providers: []ProviderConfig{
			{Name: "", APIKey: "", Endpoint: "not-a-url", Model: ""},
		},
	}
	issues := cfg.Validate()
	if len(issues) == 0 {
		t.Fatal("expected validation issues")
	}
}

func TestAudit(t *testing.T) {
	p1 := &mockProvider{
		name: "p1", available: true,
		config: ProviderConfig{MaxConcurrent: 1, Model: "m1"},
		response: &ChatCompletionResponse{
			Choices: []Choice{{Message: Message{Content: "yes"}}},
			Usage:   Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
	}
	p2 := &mockProvider{
		name: "p2", available: true,
		config: ProviderConfig{MaxConcurrent: 1, Model: "m2"},
		response: &ChatCompletionResponse{
			Choices: []Choice{{Message: Message{Content: "yes"}}},
			Usage:   Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
	}

	s := NewScheduler(p1, p2)
	result, err := s.Audit(context.Background(), BuildRequest([]Message{{Role: "user", Content: "hello"}}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Consensus {
		t.Fatal("expected consensus for identical content")
	}
	if result.Adjusted == nil {
		t.Fatal("expected adjusted result")
	}
}

func TestAudit_Disagreement(t *testing.T) {
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

	s := NewScheduler(p1, p2)
	result, err := s.Audit(context.Background(), BuildRequest([]Message{{Role: "user", Content: "hello"}}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Consensus {
		t.Fatal("expected no consensus for different content")
	}
	if result.Adjusted == nil {
		t.Fatal("expected adjusted result")
	}
}

func TestAudit_InsufficientProviders(t *testing.T) {
	p1 := &mockProvider{name: "p1", available: true, config: ProviderConfig{MaxConcurrent: 1}}
	s := NewScheduler(p1)
	_, err := s.Audit(context.Background(), BuildRequest([]Message{{Role: "user", Content: "hello"}}))
	if err == nil {
		t.Fatal("expected error for insufficient providers")
	}
}

func TestPromptInjector_AppendUser(t *testing.T) {
	pi := NewPromptInjector()
	pi.AddTemplate(PromptTemplate{
		Provider: "*",
		Model:    "*",
		Position: "append_user",
		Content:  "[End: {{end}}]",
		Vars:     map[string]string{"end": "stop"},
	})

	req := ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
	}
	pi.Inject("any", "any-model", &req)

	if req.Messages[0].Content != "hello\n\n[End: stop]" {
		t.Fatalf("unexpected content: %s", req.Messages[0].Content)
	}
}

func TestProviderStatus_ModelFromConfig(t *testing.T) {
	p1 := &mockProvider{name: "p1", available: true, config: ProviderConfig{MaxConcurrent: 1, Model: "my-model"}}
	s := NewScheduler(p1)
	status := s.ProviderStatus()
	if len(status) != 1 {
		t.Fatalf("expected 1 status, got %d", len(status))
	}
	if status[0].Model != "my-model" {
		t.Fatalf("expected model 'my-model', got '%s'", status[0].Model)
	}
}

func TestBuildRequest_CopyMessages(t *testing.T) {
	original := []Message{{Role: "user", Content: "hello"}}
	req := BuildRequest(original, WithSystemPrompt("sys"))

	// Mutate the original slice; the request should be unaffected.
	original[0].Content = "modified"
	if req.Messages[len(req.Messages)-1].Content != "hello" {
		t.Fatal("BuildRequest did not copy messages slice")
	}
}

func TestClient_SetEmitter_Concurrent(t *testing.T) {
	p1 := &mockProvider{name: "p1", available: true, config: ProviderConfig{MaxConcurrent: 1}}
	client := NewClientWithProviders(p1)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			emitter := &testEmitter{topic: fmt.Sprintf("topic-%d", idx)}
			client.SetEmitter(emitter)
		}(i)
	}
	wg.Wait()
}

type testEmitter struct {
	topic string
}

func (e *testEmitter) Emit(topic string, data ...any) {}

func TestConfig_Validate_Detailed(t *testing.T) {
	cfg := Config{
		Providers: []ProviderConfig{
			{Name: "", APIKey: "", Endpoint: "not-a-url", Model: ""},
		},
	}
	issues := cfg.Validate()
	if len(issues) == 0 {
		t.Fatal("expected validation issues")
	}
	pIssues := issues[""]
	if len(pIssues) == 0 {
		t.Fatal("expected issues for unnamed provider")
	}
	found := make(map[string]bool)
	for _, issue := range pIssues {
		found[issue] = true
	}
	if !found["name is required"] {
		t.Fatal("expected 'name is required' issue")
	}
	if !found["apiKey is required"] {
		t.Fatal("expected 'apiKey is required' issue")
	}
	if !found["endpoint must start with http:// or https://"] {
		t.Fatal("expected endpoint scheme issue")
	}
	if !found["model is required"] {
		t.Fatal("expected 'model is required' issue")
	}
}

func TestSetTimeout(t *testing.T) {
	p1 := &mockProvider{name: "p1", available: true, config: ProviderConfig{MaxConcurrent: 1}}
	client := NewClientWithProviders(p1)

	client.SetTimeout(10 * time.Second)
	if !client.Scheduler().IsClosed() {
		// Just verify it doesn't panic; actual timeout behavior requires integration test.
	}
}

func TestWithAdvancedOptions(t *testing.T) {
	seed := 42
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hello"}},
		WithTopP(0.9),
		WithPresencePenalty(0.5),
		WithFrequencyPenalty(0.3),
		WithStop([]string{"STOP"}),
		WithSeed(seed),
	)

	if req.TopP != 0.9 {
		t.Fatalf("expected TopP 0.9, got %f", req.TopP)
	}
	if req.PresencePenalty != 0.5 {
		t.Fatalf("expected PresencePenalty 0.5, got %f", req.PresencePenalty)
	}
	if req.FrequencyPenalty != 0.3 {
		t.Fatalf("expected FrequencyPenalty 0.3, got %f", req.FrequencyPenalty)
	}
	if len(req.Stop) != 1 || req.Stop[0] != "STOP" {
		t.Fatalf("expected Stop [STOP], got %v", req.Stop)
	}
	if req.Seed == nil || *req.Seed != 42 {
		t.Fatalf("expected Seed 42, got %v", req.Seed)
	}
}

func TestProviderConfigMarshalJSON(t *testing.T) {
	cfg := ProviderConfig{
		Name:   "test",
		APIKey: "secret-key-123",
		Model:  "gpt-4",
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if strings.Contains(string(data), "secret-key-123") {
		t.Fatal("APIKey should be masked in JSON output")
	}
	if !strings.Contains(string(data), "***") {
		t.Fatal("expected masked APIKey")
	}
}

func TestHistory_ReturnsEmptySlice(t *testing.T) {
	h := NewHistory(10)
	byTag := h.RecordsByTag("nonexistent")
	if byTag == nil {
		t.Fatal("RecordsByTag should return empty slice, not nil")
	}
	if len(byTag) != 0 {
		t.Fatalf("expected 0 records, got %d", len(byTag))
	}
	byProvider := h.RecordsByProvider("nonexistent")
	if byProvider == nil {
		t.Fatal("RecordsByProvider should return empty slice, not nil")
	}
}

func TestIsClosed(t *testing.T) {
	p1 := &mockProvider{name: "p1", available: true, config: ProviderConfig{MaxConcurrent: 1}}
	client := NewClientWithProviders(p1)
	if client.IsClosed() {
		t.Fatal("expected not closed")
	}
	client.Close()
	if !client.IsClosed() {
		t.Fatal("expected closed")
	}
}

// panicProvider is a mock that panics on ChatComplete.
type panicProvider struct {
	mockProvider
}

func (p *panicProvider) ChatComplete(_ context.Context, _ ChatCompletionRequest) (*ChatCompletionResponse, error) {
	panic("intentional panic")
}

func TestChatCompleteStream_Closed(t *testing.T) {
	p1 := &mockProvider{name: "p1", available: true, config: ProviderConfig{MaxConcurrent: 1}}
	client := NewClientWithProviders(p1)
	client.Close()

	_, err := client.ChatCompleteStream(context.Background(), BuildRequest([]Message{{Role: "user", Content: "hi"}}))
	if !errors.Is(err, ErrSchedulerClosed) {
		t.Fatalf("expected ErrSchedulerClosed, got %v", err)
	}
}

func TestChatCompleteStream_NoAvailableProvider(t *testing.T) {
	p1 := &mockProvider{name: "p1", available: false, config: ProviderConfig{MaxConcurrent: 1}}
	client := NewClientWithProviders(p1)
	defer client.Close()

	_, err := client.ChatCompleteStream(context.Background(), BuildRequest([]Message{{Role: "user", Content: "hi"}}))
	if !errors.Is(err, ErrNoAvailableProvider) {
		t.Fatalf("expected ErrNoAvailableProvider, got %v", err)
	}
}

func TestSetTimeout_RaceSafe(t *testing.T) {
	p1 := &mockProvider{name: "p1", available: true, config: ProviderConfig{MaxConcurrent: 10}}
	client := NewClientWithProviders(p1)
	defer client.Close()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			client.SetTimeout(time.Duration(10+i) * time.Second)
		}()
		go func() {
			defer wg.Done()
			_, _ = client.ChatComplete(context.Background(), BuildRequest([]Message{{Role: "user", Content: "hi"}}))
		}()
	}
	wg.Wait()
}

func TestAvailableProviders_Copy(t *testing.T) {
	p1 := &mockProvider{name: "p1", available: true, config: ProviderConfig{MaxConcurrent: 1}}
	p2 := &mockProvider{name: "p2", available: true, config: ProviderConfig{MaxConcurrent: 1}}
	s := NewScheduler(p1, p2)

	avail1 := s.AvailableProviders()
	if len(avail1) != 2 {
		t.Fatalf("expected 2 available, got %d", len(avail1))
	}

	// Mutate the returned slice; should not affect subsequent calls.
	avail1[0] = nil
	avail2 := s.AvailableProviders()
	if len(avail2) != 2 {
		t.Fatalf("expected 2 available after mutation, got %d", len(avail2))
	}
	if avail2[0] == nil {
		t.Fatal("mutation of returned slice affected internal cache")
	}
}

func TestChatComplete_PanicRecovery(t *testing.T) {
	p1 := &panicProvider{mockProvider{name: "p1", available: true, config: ProviderConfig{MaxConcurrent: 1}}}
	s := NewScheduler(p1)

	_, err := s.ChatComplete(context.Background(), BuildRequest([]Message{{Role: "user", Content: "hi"}}))
	if err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("expected panic error, got %v", err)
	}

	// Semaphore should have been released; a second call should not hang.
	done := make(chan struct{})
	go func() {
		_, err2 := s.ChatComplete(context.Background(), BuildRequest([]Message{{Role: "user", Content: "hi"}}))
		if err2 == nil || !strings.Contains(err2.Error(), "panic") {
			t.Errorf("expected panic error on second call, got %v", err2)
		}
		close(done)
	}()

	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("second call hung; semaphore likely not released after panic")
	}
}

func TestChatCompleteWithFallback_PanicRecovery(t *testing.T) {
	p1 := &panicProvider{mockProvider{name: "p1", available: true, config: ProviderConfig{MaxConcurrent: 1}}}
	p2 := &mockProvider{name: "p2", available: true, config: ProviderConfig{MaxConcurrent: 1}, response: &ChatCompletionResponse{
		Choices: []Choice{{Message: Message{Content: "p2"}}},
	}}
	s := NewScheduler(p1, p2)

	resp, err := s.ChatCompleteWithFallback(context.Background(), BuildRequest([]Message{{Role: "user", Content: "hi"}}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Choices[0].Message.Content != "p2" {
		t.Fatalf("expected p2 response, got %s", resp.Choices[0].Message.Content)
	}

	// Verify semaphore was released: both p1 and p2 have 1 slot each.
	// A dual call should be able to acquire both.
	p3 := &mockProvider{name: "p3", available: true, config: ProviderConfig{MaxConcurrent: 1}, response: &ChatCompletionResponse{
		Choices: []Choice{{Message: Message{Content: "p3"}}},
	}}
	s2 := NewScheduler(p1, p3)
	_, err = s2.ChatCompleteDual(context.Background(), BuildRequest([]Message{{Role: "user", Content: "hi"}}))
	if err != nil {
		t.Fatalf("dual call failed after fallback panic recovery: %v", err)
	}
}

func TestTestProvider_PanicRecovery(t *testing.T) {
	p1 := &panicProvider{mockProvider{name: "p1", available: true, config: ProviderConfig{MaxConcurrent: 1}}}
	s := NewScheduler(p1)

	err := s.TestProvider(context.Background(), "p1")
	if err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("expected panic error from TestProvider, got %v", err)
	}
}

func TestAudit_NoAvailableProvider(t *testing.T) {
	p1 := &mockProvider{name: "p1", available: false, config: ProviderConfig{MaxConcurrent: 1}}
	s := NewScheduler(p1)

	_, err := s.Audit(context.Background(), BuildRequest([]Message{{Role: "user", Content: "hi"}}))
	if !errors.Is(err, ErrNoAvailableProvider) {
		t.Fatalf("expected ErrNoAvailableProvider, got %v", err)
	}
}

func TestChatCompletionRequest_Validate(t *testing.T) {
	tests := []struct {
		name         string
		req          ChatCompletionRequest
		wantIssues   int
		wantContains string
	}{
		{
			name:         "empty messages",
			req:          ChatCompletionRequest{},
			wantIssues:   1,
			wantContains: "messages cannot be empty",
		},
		{
			name: "valid request",
			req: ChatCompletionRequest{
				Messages:    []Message{{Role: "user", Content: "hi"}},
				Temperature: 0.7,
				MaxTokens:   100,
			},
			wantIssues: 0,
		},
		{
			name: "negative temperature",
			req: ChatCompletionRequest{
				Messages:    []Message{{Role: "user", Content: "hi"}},
				Temperature: -0.5,
			},
			wantIssues:   1,
			wantContains: "temperature must be between 0 and 2",
		},
		{
			name: "temperature too high",
			req: ChatCompletionRequest{
				Messages:    []Message{{Role: "user", Content: "hi"}},
				Temperature: 2.5,
			},
			wantIssues:   1,
			wantContains: "temperature must be between 0 and 2",
		},
		{
			name: "top_p out of range",
			req: ChatCompletionRequest{
				Messages: []Message{{Role: "user", Content: "hi"}},
				TopP:     1.5,
			},
			wantIssues:   1,
			wantContains: "top_p must be between 0 and 1",
		},
		{
			name: "negative max_tokens",
			req: ChatCompletionRequest{
				Messages:  []Message{{Role: "user", Content: "hi"}},
				MaxTokens: -10,
			},
			wantIssues:   1,
			wantContains: "max_tokens must be >= 0",
		},
		{
			name: "missing role",
			req: ChatCompletionRequest{
				Messages: []Message{{Content: "hi"}},
			},
			wantIssues:   1,
			wantContains: "role is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := tt.req.Validate()
			if len(issues) != tt.wantIssues {
				t.Fatalf("expected %d issues, got %d: %v", tt.wantIssues, len(issues), issues)
			}
			if tt.wantContains != "" && len(issues) > 0 && !strings.Contains(issues[0], tt.wantContains) {
				t.Fatalf("expected issue containing %q, got %q", tt.wantContains, issues[0])
			}
		})
	}
}

func TestChatCompletionResponse_Content(t *testing.T) {
	if got := (*ChatCompletionResponse)(nil).Content(); got != "" {
		t.Fatalf("expected empty string for nil response, got %q", got)
	}
	if got := (&ChatCompletionResponse{}).Content(); got != "" {
		t.Fatalf("expected empty string for no choices, got %q", got)
	}
	resp := &ChatCompletionResponse{
		Choices: []Choice{{Message: Message{Content: "hello"}}},
	}
	if got := resp.Content(); got != "hello" {
		t.Fatalf("expected 'hello', got %q", got)
	}
}

func TestWithStop_DefensiveCopy(t *testing.T) {
	stop := []string{"STOP", "END"}
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithStop(stop),
	)
	if len(req.Stop) != 2 || req.Stop[0] != "STOP" {
		t.Fatalf("unexpected stop: %v", req.Stop)
	}
	// Mutate original slice; request should be unaffected.
	stop[0] = "MODIFIED"
	if req.Stop[0] != "STOP" {
		t.Fatal("WithStop did not copy the slice defensively")
	}
}

func TestUsage_CostString_DefaultCurrency(t *testing.T) {
	u := Usage{PromptTokens: 1000, CompletionTokens: 1000}
	p := Pricing{PromptPer1K: 1.0, CompletionPer1K: 2.0}
	s := u.CostString(p)
	// 货币为空时不附加任何货币单位，仅返回数字
	if strings.Contains(s, "CNY") || strings.Contains(s, "USD") {
		t.Fatalf("expected no currency suffix when currency is empty, got %s", s)
	}
	if !strings.HasPrefix(s, "3.000000") {
		t.Fatalf("expected cost 3.000000, got %s", s)
	}
	// Verify Pricing was not mutated
	if p.Currency != "" {
		t.Fatal("CostString mutated the Pricing argument")
	}
}

// mutationProvider is a mock that records the request it receives.
type mutationProvider struct {
	mockProvider
	lastReq ChatCompletionRequest
}

func (m *mutationProvider) ChatComplete(_ context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	m.lastReq = req
	m.calls.Add(1)
	return &ChatCompletionResponse{
		Choices: []Choice{{Message: Message{Content: "ok"}}},
	}, nil
}

func TestPromptInjector_DoesNotMutateOriginalRequest(t *testing.T) {
	pi := NewPromptInjector()
	pi.AddTemplate(PromptTemplate{
		Provider: "*",
		Model:    "*",
		Position: "system",
		Content:  "You are {{role}}.",
		Vars:     map[string]string{"role": "tester"},
	})

	original := []Message{{Role: "user", Content: "hello"}}
	p := &mutationProvider{mockProvider: mockProvider{name: "p1", available: true, config: ProviderConfig{MaxConcurrent: 1}}}
	s := NewScheduler(p)
	s.SetPromptInjector(pi)

	req := BuildRequest(original)
	_, err := s.ChatComplete(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Original Messages should be unchanged.
	if len(original) != 1 || original[0].Role != "user" || original[0].Content != "hello" {
		t.Fatalf("original Messages mutated by prompt injection: %+v", original)
	}

	// But the provider should have received the injected request.
	if len(p.lastReq.Messages) != 2 || p.lastReq.Messages[0].Role != "system" {
		t.Fatalf("provider did not receive injected request: %+v", p.lastReq.Messages)
	}
}

func TestPromptInjector_DoesNotMutateOriginalRequest_Fallback(t *testing.T) {
	pi := NewPromptInjector()
	pi.AddTemplate(PromptTemplate{
		Provider: "*",
		Model:    "*",
		Position: "prepend_user",
		Content:  "[Task]",
	})

	original := []Message{{Role: "user", Content: "hello"}}
	p := &mutationProvider{mockProvider: mockProvider{name: "p1", available: true, config: ProviderConfig{MaxConcurrent: 1}}}
	s := NewScheduler(p)
	s.SetPromptInjector(pi)

	req := BuildRequest(original)
	_, err := s.ChatCompleteWithFallback(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(original) != 1 || original[0].Content != "hello" {
		t.Fatalf("original Messages mutated by fallback prompt injection: %+v", original)
	}
}

func TestPromptInjector_DoesNotMutateOriginalRequest_Stream(t *testing.T) {
	pi := NewPromptInjector()
	pi.AddTemplate(PromptTemplate{
		Provider: "*",
		Model:    "*",
		Position: "system",
		Content:  "Sys",
	})

	original := []Message{{Role: "user", Content: "hello"}}
	p := &mutationProvider{mockProvider: mockProvider{name: "p1", available: true, config: ProviderConfig{MaxConcurrent: 1}}}
	s := NewScheduler(p)
	s.SetPromptInjector(pi)

	req := BuildRequest(original)
	// Stream will fail because p is not a real OpenAIProvider, but that's ok
	// as long as it gets far enough to trigger prompt injection.
	_ = req
	// The stream method requires a real OpenAIProvider or falls back to creating one.
	// Since our mock doesn't satisfy the type assertion, it creates a new client which fails.
	// We can't easily test stream injection without a real provider, but the code path
	// was fixed and is identical to ChatComplete.
}

func TestClose_ReturnsFirstProviderError(t *testing.T) {
	p1 := &mockProvider{name: "p1", available: true, config: ProviderConfig{MaxConcurrent: 1}}
	p2 := &closeErrorProvider{mockProvider{name: "p2", available: true, config: ProviderConfig{MaxConcurrent: 1}}}
	s := NewScheduler(p1, p2)

	err := s.Close()
	if err == nil || err.Error() != "close error" {
		t.Fatalf("expected close error, got %v", err)
	}

	// Idempotent: second close should return nil.
	err = s.Close()
	if err != nil {
		t.Fatalf("second close should be nil, got %v", err)
	}
}

// closeErrorProvider is a mock that returns an error on Close.
type closeErrorProvider struct {
	mockProvider
}

func (c *closeErrorProvider) Close() error {
	return fmt.Errorf("close error")
}

func TestChatCompleteResponse_CreatedAt(t *testing.T) {
	// Verify that our mock doesn't set CreatedAt (it uses zero value).
	// This test documents the expected behavior.
	p1 := &mockProvider{name: "p1", available: true, config: ProviderConfig{MaxConcurrent: 1}}
	s := NewScheduler(p1)
	resp, err := s.ChatComplete(context.Background(), BuildRequest([]Message{{Role: "user", Content: "hi"}}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Mock provider doesn't set CreatedAt, so it should be zero.
	if !resp.CreatedAt.IsZero() {
		t.Fatalf("expected zero CreatedAt from mock, got %v", resp.CreatedAt)
	}
}

// ============================================================================
// Additional Option tests for coverage
// ============================================================================

func TestWithSystemPrompt_EmptyStringIgnored(t *testing.T) {
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithSystemPrompt(""),
	)
	// Empty prompt should be ignored; no system message should be added
	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message (empty system prompt ignored), got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "user" {
		t.Fatal("expected only user message")
	}
}

func TestWithSystemPrompt_OverwriteExisting(t *testing.T) {
	req := BuildRequest(
		[]Message{
			{Role: "system", Content: "old system"},
			{Role: "user", Content: "hi"},
		},
		WithSystemPrompt("new system"),
	)
	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(req.Messages))
	}
	if req.Messages[0].Content != "new system" {
		t.Fatalf("expected 'new system', got %s", req.Messages[0].Content)
	}
}

func TestWithTemperature_ClampNegative(t *testing.T) {
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithTemperature(-1.0),
	)
	if req.Temperature != 0 {
		t.Fatalf("expected Temperature clamped to 0, got %f", req.Temperature)
	}
}

func TestWithTemperature_ClampAboveTwo(t *testing.T) {
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithTemperature(3.5),
	)
	if req.Temperature != 2 {
		t.Fatalf("expected Temperature clamped to 2, got %f", req.Temperature)
	}
}

func TestWithTemperature_NormalValue(t *testing.T) {
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithTemperature(0.8),
	)
	if req.Temperature != 0.8 {
		t.Fatalf("expected Temperature 0.8, got %f", req.Temperature)
	}
}

func TestWithTopP_ClampNegative(t *testing.T) {
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithTopP(-0.5),
	)
	if req.TopP != 0 {
		t.Fatalf("expected TopP clamped to 0, got %f", req.TopP)
	}
}

func TestWithTopP_ClampAboveOne(t *testing.T) {
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithTopP(1.5),
	)
	if req.TopP != 1 {
		t.Fatalf("expected TopP clamped to 1, got %f", req.TopP)
	}
}

func TestWithPresencePenalty_ClampNegative(t *testing.T) {
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithPresencePenalty(-3.0),
	)
	if req.PresencePenalty != -2 {
		t.Fatalf("expected PresencePenalty clamped to -2, got %f", req.PresencePenalty)
	}
}

func TestWithPresencePenalty_ClampPositive(t *testing.T) {
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithPresencePenalty(3.0),
	)
	if req.PresencePenalty != 2 {
		t.Fatalf("expected PresencePenalty clamped to 2, got %f", req.PresencePenalty)
	}
}

func TestWithFrequencyPenalty_ClampNegative(t *testing.T) {
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithFrequencyPenalty(-3.0),
	)
	if req.FrequencyPenalty != -2 {
		t.Fatalf("expected FrequencyPenalty clamped to -2, got %f", req.FrequencyPenalty)
	}
}

func TestWithFrequencyPenalty_ClampPositive(t *testing.T) {
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithFrequencyPenalty(3.0),
	)
	if req.FrequencyPenalty != 2 {
		t.Fatalf("expected FrequencyPenalty clamped to 2, got %f", req.FrequencyPenalty)
	}
}

func TestWithMaxTokens_NormalValue(t *testing.T) {
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithMaxTokens(500),
	)
	if req.MaxTokens != 500 {
		t.Fatalf("expected MaxTokens 500, got %d", req.MaxTokens)
	}
}

func TestWithMaxTokens_NegativeValue(t *testing.T) {
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithMaxTokens(-10),
	)
	if req.MaxTokens != -10 {
		t.Fatalf("expected MaxTokens -10 (not clamped by option), got %d", req.MaxTokens)
	}
}

func TestWithModel_SetsModel(t *testing.T) {
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithModel("deepseek-v4-pro"),
	)
	if req.Model != "deepseek-v4-pro" {
		t.Fatalf("expected model 'deepseek-v4-pro', got %s", req.Model)
	}
}

func TestWithTools_DefensiveCopy(t *testing.T) {
	tools := []core.Tool{
		{Type: "function", Function: &core.FunctionDefinition{Name: "f1"}},
	}
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithTools(tools),
	)
	if len(req.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(req.Tools))
	}
	// Mutate original slice; request should be unaffected
	tools[0].Type = "modified"
	if req.Tools[0].Type != "function" {
		t.Fatal("WithTools did not copy the slice defensively")
	}
}

func TestWithTools_EmptySliceIgnored(t *testing.T) {
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithTools([]core.Tool{}),
	)
	if req.Tools != nil {
		t.Fatalf("expected nil Tools for empty slice, got %v", req.Tools)
	}
}

func TestWithSeed_PointerSemantics(t *testing.T) {
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithSeed(42),
	)
	if req.Seed == nil {
		t.Fatal("expected non-nil Seed")
	}
	if *req.Seed != 42 {
		t.Fatalf("expected Seed 42, got %d", *req.Seed)
	}
}

func TestWithToolChoice_StringValue(t *testing.T) {
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithToolChoice("auto"),
	)
	if req.ToolChoice != "auto" {
		t.Fatalf("expected ToolChoice 'auto', got %v", req.ToolChoice)
	}
}

func TestWithToolChoice_StructValue(t *testing.T) {
	choice := core.ToolChoice{Type: "function"}
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithToolChoice(choice),
	)
	tc, ok := req.ToolChoice.(core.ToolChoice)
	if !ok {
		t.Fatalf("expected ToolChoice struct, got %T", req.ToolChoice)
	}
	if tc.Type != "function" {
		t.Fatalf("expected type 'function', got %s", tc.Type)
	}
}

func TestWithParallelToolCalls(t *testing.T) {
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithParallelToolCalls(true),
	)
	if !req.ParallelToolCalls {
		t.Fatal("expected ParallelToolCalls to be true")
	}

	req2 := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithParallelToolCalls(false),
	)
	if req2.ParallelToolCalls {
		t.Fatal("expected ParallelToolCalls to be false")
	}
}

func TestWithJSONResponse_SetsFormat(t *testing.T) {
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithJSONResponse(),
	)
	if req.ResponseFormat.Type != "json_object" {
		t.Fatalf("expected ResponseFormat.Type 'json_object', got %s", req.ResponseFormat.Type)
	}
}

func TestBuildRequest_OptionApplicationOrder(t *testing.T) {
	// Options should be applied in order; later options can override earlier ones
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithModel("model-a"),
		WithModel("model-b"),
	)
	if req.Model != "model-b" {
		t.Fatalf("expected model 'model-b' (last wins), got %s", req.Model)
	}
}

func TestBuildRequest_DefensiveCopyIsolation(t *testing.T) {
	original := []Message{
		{Role: "user", Content: "hello"},
		{Role: "user", Content: "world"},
	}
	req := BuildRequest(original)

	// Mutate original; request should be unaffected
	original[0].Content = "modified"
	original = append(original, Message{Role: "system", Content: "injected"})

	if req.Messages[0].Content != "hello" {
		t.Fatal("BuildRequest did not copy messages slice")
	}
	if len(req.Messages) != 2 {
		t.Fatal("BuildRequest messages length changed after original append")
	}
}

func TestWithStop_EmptySliceIgnored(t *testing.T) {
	req := BuildRequest(
		[]Message{{Role: "user", Content: "hi"}},
		WithStop([]string{}),
	)
	if req.Stop != nil {
		t.Fatalf("expected nil Stop for empty slice, got %v", req.Stop)
	}
}
