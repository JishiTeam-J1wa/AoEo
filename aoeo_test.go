package aoeo

import (
	"context"
	"errors"
	"testing"
	"time"
)

// mockProvider is a test double that implements the Provider interface.
type mockProvider struct {
	name      string
	config    ProviderConfig
	available bool
	response  *ChatCompletionResponse
	err       error
	calls     int
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) ChatComplete(_ context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	m.calls++
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

func (m *mockProvider) Config() ProviderConfig { return m.config }

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
	bp.failUntil = time.Now().Add(-1 * time.Second)
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
	if err := s.Close(); err == nil {
		t.Fatal("second close should fail")
	}

	ctx := context.Background()
	_, err := s.ChatComplete(ctx, BuildRequest([]Message{{Role: "user", Content: "hi"}}))
	if err == nil || err.Error() != "scheduler is closed" {
		t.Fatalf("expected scheduler is closed, got %v", err)
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
	sem := newAdaptiveSemaphore(1)
	sem.acquire(context.Background()) // occupy the only slot

	ctx, cancel := context.WithCancel(context.Background())
	go cancel() // cancel immediately

	err := sem.acquire(ctx)
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	sem.release() // should not panic
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

	err := doRetry(context.Background(), cfg, fn)
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
	err := doRetry(ctx, cfg, func() error { return errors.New("fail") })
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
	if p1.calls != 1 || p2.calls != 1 {
		t.Fatalf("expected p1=1 p2=1, got p1=%d p2=%d", p1.calls, p2.calls)
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
