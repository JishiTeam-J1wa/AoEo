package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/sashabaranov/go-openai"
)

// mockEmitter records emitted events.
type mockEmitter struct {
	mu     sync.Mutex
	events []struct {
		topic string
		data  []any
	}
}

func (e *mockEmitter) Emit(topic string, data ...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, struct {
		topic string
		data  []any
	}{topic, data})
}

func (e *mockEmitter) Count(topic string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	count := 0
	for _, ev := range e.events {
		if ev.topic == topic {
			count++
		}
	}
	return count
}

func TestBaseProvider_IsAvailable(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{Name: "test", APIKey: "k", Endpoint: "https://t.com", Model: "m"})
	if !bp.IsAvailable() {
		t.Fatal("expected available with complete config")
	}

	bp = NewBaseProvider(core.ProviderConfig{Name: "test", APIKey: "", Endpoint: "https://t.com", Model: "m"})
	if bp.IsAvailable() {
		t.Fatal("expected unavailable without APIKey")
	}

	bp = NewBaseProvider(core.ProviderConfig{Name: "test", APIKey: "k", Endpoint: "", Model: "m"})
	if bp.IsAvailable() {
		t.Fatal("expected unavailable without Endpoint")
	}

	bp = NewBaseProvider(core.ProviderConfig{Name: "test", APIKey: "k", Endpoint: "https://t.com", Model: ""})
	if bp.IsAvailable() {
		t.Fatal("expected unavailable without Model")
	}
}

func TestBaseProvider_CircuitBreaker(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{Name: "test", APIKey: "k", Endpoint: "https://t.com", Model: "m"})

	// Initially available
	if !bp.IsAvailable() {
		t.Fatal("expected available initially")
	}

	// Record 3 failures (default threshold)
	bp.RecordFailure()
	bp.RecordFailure()
	bp.RecordFailure()

	if bp.IsAvailable() {
		t.Fatal("expected unavailable after 3 failures")
	}

	// Wait for cooldown
	bp.SetFailUntil(time.Now().Add(-time.Second))
	if !bp.IsAvailable() {
		t.Fatal("expected available after cooldown expires")
	}

	// Record 2 failures then success should reset
	bp.RecordFailure()
	bp.RecordFailure()
	bp.RecordSuccess()
	if !bp.IsAvailable() {
		t.Fatal("expected available after success resets counter")
	}
}

func TestBaseProvider_RecordSuccess_EmitsRecover(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{Name: "test", APIKey: "k", Endpoint: "https://t.com", Model: "m"})
	emit := &mockEmitter{}
	bp.SetEmitter(emit)

	bp.RecordFailure()
	bp.RecordFailure()
	bp.RecordFailure() // circuit open

	bp.RecordSuccess() // should emit recover

	if emit.Count(core.EventProviderRecover) != 1 {
		t.Fatalf("expected 1 recover event, got %d", emit.Count(core.EventProviderRecover))
	}
}

func TestBaseProvider_SetEmitter_Nil(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{})
	bp.SetEmitter(nil)
	// Should not panic and should use NopEmitter
	bp.getEmitter().Emit("test")
}

func TestBaseProvider_SystemPrompt(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{})

	if bp.GetSystemPrompt() != "" {
		t.Fatal("expected empty initially")
	}

	bp.SetSystemPrompt("override")
	if bp.GetSystemPrompt() != "override" {
		t.Fatal("expected override")
	}

	bp.ClearSystemPrompt()
	if bp.GetSystemPrompt() != "" {
		t.Fatal("expected empty after clear")
	}
}

func TestBaseProvider_Config(t *testing.T) {
	cfg := core.ProviderConfig{Name: "test", Model: "gpt-4"}
	bp := NewBaseProvider(cfg)
	if bp.Config().Name != "test" || bp.Config().Model != "gpt-4" {
		t.Fatal("Config did not return correct values")
	}
}

func TestBaseProvider_FailUntil(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{})
	if !bp.FailUntil().IsZero() {
		t.Fatal("expected zero FailUntil initially")
	}

	future := time.Now().Add(time.Hour)
	bp.SetFailUntil(future)
	if !bp.FailUntil().Equal(future) {
		t.Fatal("FailUntil not set correctly")
	}
}

func TestBaseProvider_Close(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{})
	if err := bp.Close(); err != nil {
		t.Fatalf("Close should return nil: %v", err)
	}
}

func TestOpenAIProvider_Name(t *testing.T) {
	p := NewOpenAIProvider(core.ProviderConfig{Name: "test"})
	if p.Name() != "test" {
		t.Fatalf("expected 'test', got %s", p.Name())
	}
}

func TestOpenAIProvider_DefaultEndpoint(t *testing.T) {
	p := NewOpenAIProvider(core.ProviderConfig{})
	if p.Config().Endpoint != "https://api.openai.com/v1" {
		t.Fatalf("unexpected default endpoint: %s", p.Config().Endpoint)
	}
}

func TestIsTemperatureError(t *testing.T) {
	if isTemperatureError(nil) {
		t.Fatal("nil should be false")
	}
	if !isTemperatureError(errors.New("invalid temperature value")) {
		t.Fatal("should detect temperature error")
	}
	if isTemperatureError(errors.New("bad request")) {
		t.Fatal("should not match unrelated error")
	}
}

func TestFactoryFunctions(t *testing.T) {
	tests := []struct {
		name         string
		fn           func(core.ProviderConfig) Provider
		wantEndpoint string
		wantModel    string
		wantName     string
	}{
		{"deepseek", NewDeepSeekProvider, "https://api.deepseek.com", "deepseek-v4-pro", "deepseek"},
		{"kimi", NewKimiProvider, "https://api.moonshot.cn/v1", "kimi-k2.6", "kimi"},
		{"glm", NewGLMProvider, "https://open.bigmodel.cn/api/paas/v4", "glm-5.1", "glm"},
		{"qwen", NewQwenProvider, "https://dashscope.aliyuncs.com/compatible-mode/v1", "qwen3.7-max", "qwen"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.fn(core.ProviderConfig{})
			if p.Config().Endpoint != tt.wantEndpoint {
				t.Fatalf("endpoint: want %s, got %s", tt.wantEndpoint, p.Config().Endpoint)
			}
			if p.Config().Model != tt.wantModel {
				t.Fatalf("model: want %s, got %s", tt.wantModel, p.Config().Model)
			}
			if p.Config().Name != tt.wantName {
				t.Fatalf("name: want %s, got %s", tt.wantName, p.Config().Name)
			}
		})
	}
}

func TestFactoryFunctions_PreserveExplicitValues(t *testing.T) {
	p := NewDeepSeekProvider(core.ProviderConfig{
		Endpoint: "https://custom.com",
		Model:    "custom-model",
		Name:     "custom-name",
	})
	if p.Config().Endpoint != "https://custom.com" {
		t.Fatal("explicit endpoint should be preserved")
	}
	if p.Config().Model != "custom-model" {
		t.Fatal("explicit model should be preserved")
	}
	if p.Config().Name != "custom-name" {
		t.Fatal("explicit name should be preserved")
	}
}

func TestOpenAIProvider_CustomHTTPClient(t *testing.T) {
	custom := &http.Client{
		Transport: &mockRoundTripper{fn: func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
		}},
	}
	p := NewOpenAIProvider(core.ProviderConfig{
		Name:       "test",
		APIKey:     "key",
		HTTPClient: custom,
	})
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
	// Provider stores config but doesn't expose the internal client directly.
	// We verify by checking the config is preserved.
	if p.Config().HTTPClient != custom {
		t.Fatal("custom HTTPClient should be preserved in config")
	}
}

func TestOpenAIProvider_SkipTLSVerify_WithCustomClient(t *testing.T) {
	custom := &http.Client{
		Transport: &http.Transport{MaxIdleConns: 42},
		Timeout:   30 * time.Second,
	}
	p := NewOpenAIProvider(core.ProviderConfig{
		Name:          "test",
		APIKey:        "key",
		HTTPClient:    custom,
		SkipTLSVerify: true,
	})
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
	// Smoke test: no panic when combining custom client + SkipTLSVerify.
}

func TestOpenAIProvider_SkipTLSVerify_NoCustomClient(t *testing.T) {
	p := NewOpenAIProvider(core.ProviderConfig{
		Name:          "test",
		APIKey:        "key",
		SkipTLSVerify: true,
	})
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestOpenAIProvider_Proxy(t *testing.T) {
	p := NewOpenAIProvider(core.ProviderConfig{
		Name:   "test",
		APIKey: "key",
		Proxy:  "http://proxy.example.com:8080",
	})
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
	// Transport is not directly exposed; smoke test verifies no panic.
}

func TestOpenAIProvider_ProxyAndSkipTLSVerify(t *testing.T) {
	p := NewOpenAIProvider(core.ProviderConfig{
		Name:          "test",
		APIKey:        "key",
		Proxy:         "http://proxy.example.com:8080",
		SkipTLSVerify: true,
	})
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestOpenAIProvider_ProxyWithCustomHTTPClient(t *testing.T) {
	custom := &http.Client{
		Transport: &http.Transport{MaxIdleConns: 42},
		Timeout:   30 * time.Second,
	}
	p := NewOpenAIProvider(core.ProviderConfig{
		Name:       "test",
		APIKey:     "key",
		HTTPClient: custom,
		Proxy:      "http://proxy.example.com:8080",
	})
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestOpenAIProvider_InvalidProxy(t *testing.T) {
	// Invalid proxy URL should be ignored, not panic.
	p := NewOpenAIProvider(core.ProviderConfig{
		Name:   "test",
		APIKey: "key",
		Proxy:  "://invalid-url",
	})
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestBuildHTTPClient_PreservesCustomClient(t *testing.T) {
	custom := &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
	}
	hc := buildHTTPClient(core.ProviderConfig{
		HTTPClient: custom,
	})
	if hc != custom {
		t.Fatal("expected the same custom HTTPClient to be used when no Proxy/SkipTLSVerify")
	}
}

func TestBuildHTTPClient_PreservesCheckRedirectAndJar(t *testing.T) {
	custom := &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     &http.Transport{MaxIdleConns: 42},
	}
	hc := buildHTTPClient(core.ProviderConfig{
		HTTPClient:    custom,
		SkipTLSVerify: true,
	})
	if hc == custom {
		t.Fatal("expected a new client to be created when SkipTLSVerify is set")
	}
	if hc.Timeout != 30*time.Second {
		t.Fatalf("expected timeout preserved, got %v", hc.Timeout)
	}
	if hc.CheckRedirect == nil {
		t.Fatal("expected CheckRedirect preserved")
	}
	if tr, ok := hc.Transport.(*http.Transport); !ok || tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("expected InsecureSkipVerify applied to transport")
	}
	if tr, ok := hc.Transport.(*http.Transport); !ok || tr.MaxIdleConns != 42 {
		t.Fatal("expected MaxIdleConns preserved from custom transport")
	}
}

func TestBuildHTTPClient_ProxyAppliedToTransport(t *testing.T) {
	hc := buildHTTPClient(core.ProviderConfig{
		Proxy: "http://proxy.example.com:8080",
	})
	tr, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport")
	}
	if tr.Proxy == nil {
		t.Fatal("expected Proxy to be set on transport")
	}
	// Verify proxy resolves the configured URL
	req, _ := http.NewRequest("GET", "https://api.example.com", nil)
	proxyURL, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("proxy func error: %v", err)
	}
	if proxyURL == nil || proxyURL.String() != "http://proxy.example.com:8080" {
		t.Fatalf("unexpected proxy URL: %v", proxyURL)
	}
}

func TestBuildHTTPClient_ProxyWithNonHTTPTransport(t *testing.T) {
	// When custom Transport is not *http.Transport, deriveTransport falls back
	// to DefaultTransport. Proxy should still be applied.
	custom := &http.Client{
		Transport: &mockRoundTripper{},
	}
	hc := buildHTTPClient(core.ProviderConfig{
		HTTPClient: custom,
		Proxy:      "http://proxy.example.com:8080",
	})
	tr, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected fallback to *http.Transport when custom transport is not cloneable")
	}
	if tr.Proxy == nil {
		t.Fatal("expected Proxy to be set on fallback transport")
	}
	// CheckRedirect should be preserved even when transport falls back.
	if hc.CheckRedirect != nil {
		t.Fatal("expected nil CheckRedirect since original was nil")
	}
}

func TestBuildHTTPClient_DefaultTimeout(t *testing.T) {
	hc := buildHTTPClient(core.ProviderConfig{})
	if hc.Timeout != 120*time.Second {
		t.Fatalf("expected default 120s timeout, got %v", hc.Timeout)
	}
}

func TestBuildHTTPClient_ProxyFromEnvironment(t *testing.T) {
	// When Proxy is not explicitly set, the transport should respect HTTP_PROXY.
	t.Setenv("HTTP_PROXY", "http://env-proxy.example.com:3128")
	defer os.Unsetenv("HTTP_PROXY")

	hc := buildHTTPClient(core.ProviderConfig{})
	// When no custom transport is set, Transport is nil and http.DefaultTransport
	// is used at runtime, which respects HTTP_PROXY/HTTPS_PROXY/NO_PROXY.
	var proxy func(*http.Request) (*url.URL, error)
	if hc.Transport == nil {
		proxy = http.ProxyFromEnvironment
	} else if tr, ok := hc.Transport.(*http.Transport); ok {
		proxy = tr.Proxy
	} else {
		t.Fatal("unexpected transport type")
	}

	req, _ := http.NewRequest("GET", "http://target.example.com", nil)
	proxyURL, err := proxy(req)
	if err != nil {
		t.Fatalf("proxy func error: %v", err)
	}
	if proxyURL == nil || proxyURL.String() != "http://env-proxy.example.com:3128" {
		t.Fatalf("expected env proxy, got %v", proxyURL)
	}
}

func TestBuildHTTPClient_ExplicitProxyOverridesEnv(t *testing.T) {
	// Explicit Proxy config takes precedence over HTTP_PROXY env var.
	t.Setenv("HTTP_PROXY", "http://env-proxy.example.com:3128")

	hc := buildHTTPClient(core.ProviderConfig{Proxy: "http://explicit-proxy.example.com:8080"})
	tr, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport")
	}
	req, _ := http.NewRequest("GET", "http://target.example.com", nil)
	proxyURL, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("proxy func error: %v", err)
	}
	if proxyURL == nil || proxyURL.String() != "http://explicit-proxy.example.com:8080" {
		t.Fatalf("expected explicit proxy to override env, got %v", proxyURL)
	}
}

func TestBuildHTTPClient_SOCKS5ProxyURL(t *testing.T) {
	// SOCKS5 URLs are parsed and stored. Actual dialing requires a custom
	// HTTPClient with golang.org/x/net/proxy Dialer.
	hc := buildHTTPClient(core.ProviderConfig{Proxy: "socks5://127.0.0.1:1080"})
	tr, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport")
	}
	if tr.Proxy == nil {
		t.Fatal("expected Proxy to be set")
	}
	req, _ := http.NewRequest("GET", "http://target.example.com", nil)
	proxyURL, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("proxy func error: %v", err)
	}
	if proxyURL == nil || proxyURL.Scheme != "socks5" {
		t.Fatalf("expected socks5 scheme, got %v", proxyURL)
	}
}

type mockRoundTripper struct {
	fn func(*http.Request) (*http.Response, error)
}

func (m *mockRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return m.fn(r)
}


// ========== Health tracking tests ==========

func TestBaseProvider_RecordHealthCheck(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{Name: "test", APIKey: "k", Endpoint: "https://t.com", Model: "m"})

	// Record a successful health check.
	bp.RecordHealthCheck(100, true)
	h := bp.Health()
	if h.LastLatencyMs != 100 {
		t.Errorf("expected last latency 100, got %d", h.LastLatencyMs)
	}
	if h.SuccessRate != 1.0 {
		t.Errorf("expected success rate 1.0, got %f", h.SuccessRate)
	}
	if h.ConsecutiveFails != 0 {
		t.Errorf("expected consecutive fails 0, got %d", h.ConsecutiveFails)
	}

	// Record a failed health check.
	bp.RecordHealthCheck(200, false)
	h = bp.Health()
	if h.LastLatencyMs != 200 {
		t.Errorf("expected last latency 200, got %d", h.LastLatencyMs)
	}
	if h.SuccessRate != 0.5 {
		t.Errorf("expected success rate 0.5, got %f", h.SuccessRate)
	}
	if h.ConsecutiveFails != 1 {
		t.Errorf("expected consecutive fails 1, got %d", h.ConsecutiveFails)
	}
}

func TestBaseProvider_RecordCallResult(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{Name: "test", APIKey: "k", Endpoint: "https://t.com", Model: "m"})

	bp.RecordCallResult(50, nil)
	bp.RecordCallResult(150, errors.New("fail"))

	h := bp.Health()
	if h.AvgLatencyMs != 100 {
		t.Errorf("expected avg latency 100, got %d", h.AvgLatencyMs)
	}
	if h.SuccessRate != 0.5 {
		t.Errorf("expected success rate 0.5, got %f", h.SuccessRate)
	}
	if h.ConsecutiveFails != 1 {
		t.Errorf("expected consecutive fails 1, got %d", h.ConsecutiveFails)
	}
}

func TestBaseProvider_HealthSlidingWindow(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{Name: "test", APIKey: "k", Endpoint: "https://t.com", Model: "m"})

	// Fill the window with 20 entries: 10 success (latency 100) + 10 failure (latency 200).
	for i := 0; i < 10; i++ {
		bp.RecordHealthCheck(100, true)
	}
	for i := 0; i < 10; i++ {
		bp.RecordHealthCheck(200, false)
	}

	h := bp.Health()
	if h.TotalChecks != 20 {
		t.Errorf("expected total checks 20, got %d", h.TotalChecks)
	}
	if h.SuccessRate != 0.5 {
		t.Errorf("expected success rate 0.5, got %f", h.SuccessRate)
	}
	if h.AvgLatencyMs != 150 {
		t.Errorf("expected avg latency 150, got %d", h.AvgLatencyMs)
	}
	if h.ConsecutiveFails != 10 {
		t.Errorf("expected consecutive fails 10, got %d", h.ConsecutiveFails)
	}

	// Push one more entry (success) — oldest should be evicted.
	bp.RecordHealthCheck(100, true)
	h = bp.Health()
	if h.TotalChecks != 20 {
		t.Errorf("expected total checks still 20 after overflow, got %d", h.TotalChecks)
	}
	// Window had 10 success + 10 failure. The oldest success is evicted.
	// Result: 9 success + 10 failure + 1 new success = 10 success out of 20.
	expectedRate := 10.0 / 20.0
	if h.SuccessRate != expectedRate {
		t.Errorf("expected success rate %f after overflow, got %f", expectedRate, h.SuccessRate)
	}
}

func TestBaseProvider_HealthConsecutiveFails(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{Name: "test", APIKey: "k", Endpoint: "https://t.com", Model: "m"})

	// Pattern: fail, fail, success, fail, success, success
	bp.RecordHealthCheck(10, false)
	bp.RecordHealthCheck(10, false)
	bp.RecordHealthCheck(10, true)
	bp.RecordHealthCheck(10, false)
	bp.RecordHealthCheck(10, true)
	bp.RecordHealthCheck(10, true)

	h := bp.Health()
	// Max consecutive fails in window: 2 (the first two entries).
	if h.ConsecutiveFails != 2 {
		t.Errorf("expected consecutive fails 2, got %d", h.ConsecutiveFails)
	}
	// 3 success out of 6.
	if h.SuccessRate != 3.0/6.0 {
		t.Errorf("expected success rate %f, got %f", 3.0/6.0, h.SuccessRate)
	}
}

func TestBaseProvider_HealthConcurrent(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{Name: "test", APIKey: "k", Endpoint: "https://t.com", Model: "m"})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(success bool) {
			defer wg.Done()
			bp.RecordHealthCheck(10, success)
		}(i%2 == 0)
	}
	wg.Wait()

	h := bp.Health()
	if h.TotalChecks != 20 {
		t.Errorf("expected total checks 20 (window size), got %d", h.TotalChecks)
	}
	// Success rate should be close to 0.5 but may vary due to race.
	if h.SuccessRate < 0.3 || h.SuccessRate > 0.7 {
		t.Errorf("success rate %f out of reasonable range for concurrent writes", h.SuccessRate)
	}
}


// ========== Function Calling mapping tests ==========

func TestBuildOpenAIMessages_WithToolCalls(t *testing.T) {
	coreMsgs := []core.Message{
		{Role: "user", Content: "What's the weather?"},
		{Role: "assistant", ToolCalls: []core.ToolCall{
			{ID: "call_1", Type: "function", Function: core.FunctionCall{Name: "get_weather", Arguments: `{"city":"Beijing"}`}},
		}},
		{Role: "tool", ToolCallID: "call_1", Content: "Sunny, 25C"},
	}

	openaiMsgs := buildOpenAIMessages(coreMsgs)
	if len(openaiMsgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(openaiMsgs))
	}
	if len(openaiMsgs[1].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call in assistant message, got %d", len(openaiMsgs[1].ToolCalls))
	}
	if openaiMsgs[1].ToolCalls[0].ID != "call_1" {
		t.Fatalf("expected tool call ID call_1, got %s", openaiMsgs[1].ToolCalls[0].ID)
	}
	if openaiMsgs[2].ToolCallID != "call_1" {
		t.Fatalf("expected tool call ID call_1 in tool message, got %s", openaiMsgs[2].ToolCallID)
	}
}

func TestBuildOpenAITools(t *testing.T) {
	tools := []core.Tool{
		{Type: "function", Function: &core.FunctionDefinition{
			Name:        "get_weather",
			Description: "Get weather for a city",
			Parameters:  map[string]any{"type": "object"},
			Strict:      true,
		}},
	}
	openaiTools := buildOpenAITools(tools)
	if len(openaiTools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(openaiTools))
	}
	if openaiTools[0].Function.Name != "get_weather" {
		t.Fatalf("expected function name get_weather, got %s", openaiTools[0].Function.Name)
	}
	if !openaiTools[0].Function.Strict {
		t.Fatal("expected Strict=true")
	}
}

func TestBuildOpenAIToolChoice(t *testing.T) {
	// String choice
	if v := buildOpenAIToolChoice("auto"); v != "auto" {
		t.Fatalf("expected auto, got %v", v)
	}
	// ToolChoice struct
	choice := core.ToolChoice{Type: "function"}
	choice.Function.Name = "get_weather"
	v := buildOpenAIToolChoice(choice)
	_ = v // just verify no panic

	// Nil
	if v := buildOpenAIToolChoice(nil); v != nil {
		t.Fatalf("expected nil, got %v", v)
	}
}

// ========== New tests for coverage improvement ==========

func TestBaseProvider_HealthCheck_Success(t *testing.T) {
	srv := newMockHTTPServer(t, http.StatusOK, `{"status":"ok"}`)
	defer srv.Close()

	bp := NewBaseProvider(core.ProviderConfig{
		Name:       "test",
		APIKey:     "test-key",
		Endpoint:   srv.URL,
		Model:      "m",
		HTTPClient: srv.Client(),
	})

	err := bp.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("expected successful health check, got: %v", err)
	}

	h := bp.Health()
	if h.LastLatencyMs < 0 {
		t.Errorf("expected non-negative latency, got %d", h.LastLatencyMs)
	}
	if h.SuccessRate != 1.0 {
		t.Errorf("expected success rate 1.0, got %f", h.SuccessRate)
	}
}

func TestBaseProvider_HealthCheck_ServerError(t *testing.T) {
	srv := newMockHTTPServer(t, http.StatusInternalServerError, "server error")
	defer srv.Close()

	bp := NewBaseProvider(core.ProviderConfig{
		Name:       "test",
		APIKey:     "test-key",
		Endpoint:   srv.URL,
		Model:      "m",
		HTTPClient: srv.Client(),
	})

	err := bp.HealthCheck(context.Background())
	if err == nil {
		t.Fatal("expected error for 500 status")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected error to mention 500, got: %v", err)
	}

	h := bp.Health()
	if h.SuccessRate != 0.0 {
		t.Errorf("expected success rate 0.0, got %f", h.SuccessRate)
	}
}

func TestBaseProvider_HealthCheck_ConfigIncomplete(t *testing.T) {
	// Missing APIKey.
	bp := NewBaseProvider(core.ProviderConfig{Name: "test", Endpoint: "https://t.com"})
	err := bp.HealthCheck(context.Background())
	if err == nil {
		t.Fatal("expected error for incomplete config")
	}
	if !strings.Contains(err.Error(), "config incomplete") {
		t.Fatalf("expected 'config incomplete' error, got: %v", err)
	}

	// Missing Endpoint.
	bp = NewBaseProvider(core.ProviderConfig{Name: "test", APIKey: "k"})
	err = bp.HealthCheck(context.Background())
	if err == nil {
		t.Fatal("expected error for missing endpoint")
	}
}

func TestBaseProvider_HealthCheck_ConnectionRefused(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{
		Name:     "test",
		APIKey:   "test-key",
		Endpoint: "http://127.0.0.1:1", // unlikely to be listening
		Model:    "m",
	})

	err := bp.HealthCheck(context.Background())
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
}

func TestBaseProvider_HealthCheck_4xxIsSuccess(t *testing.T) {
	// 4xx errors are not treated as health check failures (only >= 500 fails).
	srv := newMockHTTPServer(t, http.StatusUnauthorized, `{"error":"unauthorized"}`)
	defer srv.Close()

	bp := NewBaseProvider(core.ProviderConfig{
		Name:       "test",
		APIKey:     "test-key",
		Endpoint:   srv.URL,
		Model:      "m",
		HTTPClient: srv.Client(),
	})

	err := bp.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("expected 401 to pass health check, got: %v", err)
	}
}

func TestBaseProvider_IsAvailable_CircuitBreakerCooldown(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{
		Name: "test", APIKey: "k", Endpoint: "https://t.com", Model: "m",
		MaxFailures: 2, CooldownDuration: 100 * time.Millisecond,
	})

	// Trigger circuit breaker.
	bp.RecordFailure()
	bp.RecordFailure()

	if bp.IsAvailable() {
		t.Fatal("expected unavailable after circuit breaker opens")
	}

	// Wait for cooldown to expire.
	time.Sleep(150 * time.Millisecond)

	if !bp.IsAvailable() {
		t.Fatal("expected available after cooldown expires")
	}
}

func TestBaseProvider_IsAvailable_CooldownNotExpired(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{
		Name: "test", APIKey: "k", Endpoint: "https://t.com", Model: "m",
		MaxFailures: 2, CooldownDuration: 10 * time.Second,
	})

	bp.RecordFailure()
	bp.RecordFailure()

	if bp.IsAvailable() {
		t.Fatal("expected unavailable during cooldown")
	}
}

func TestBaseProvider_Health_InitialState(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{})
	h := bp.Health()
	if h.TotalChecks != 0 {
		t.Errorf("expected 0 total checks initially, got %d", h.TotalChecks)
	}
	if h.SuccessRate != 0 {
		t.Errorf("expected 0 success rate initially, got %f", h.SuccessRate)
	}
}

func TestBaseProvider_RecordHealthCheck_AvgLatency(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{Name: "test", APIKey: "k", Endpoint: "https://t.com", Model: "m"})

	bp.RecordHealthCheck(100, true)
	bp.RecordHealthCheck(200, true)
	bp.RecordHealthCheck(300, true)

	h := bp.Health()
	if h.AvgLatencyMs != 200 {
		t.Errorf("expected avg latency 200, got %d", h.AvgLatencyMs)
	}
	if h.TotalChecks != 3 {
		t.Errorf("expected 3 total checks, got %d", h.TotalChecks)
	}
}

func TestBaseProvider_RecordCallResult_Success(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{Name: "test", APIKey: "k", Endpoint: "https://t.com", Model: "m"})

	bp.RecordCallResult(50, nil)
	h := bp.Health()
	if h.SuccessRate != 1.0 {
		t.Errorf("expected success rate 1.0, got %f", h.SuccessRate)
	}
	if h.ConsecutiveFails != 0 {
		t.Errorf("expected 0 consecutive fails, got %d", h.ConsecutiveFails)
	}
}

func TestBaseProvider_RecordCallResult_Failure(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{Name: "test", APIKey: "k", Endpoint: "https://t.com", Model: "m"})

	bp.RecordCallResult(100, errors.New("timeout"))
	h := bp.Health()
	if h.SuccessRate != 0.0 {
		t.Errorf("expected success rate 0.0, got %f", h.SuccessRate)
	}
	if h.ConsecutiveFails != 1 {
		t.Errorf("expected 1 consecutive fail, got %d", h.ConsecutiveFails)
	}
}

func TestBaseProvider_RecordSuccess_NoPriorFailure(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{Name: "test", APIKey: "k", Endpoint: "https://t.com", Model: "m"})
	emit := &mockEmitter{}
	bp.SetEmitter(emit)

	// RecordSuccess when no prior failures should NOT emit recover event.
	bp.RecordSuccess()
	if emit.Count(core.EventProviderRecover) != 0 {
		t.Fatalf("expected no recover event, got %d", emit.Count(core.EventProviderRecover))
	}
}

func TestBaseProvider_RecordFailure_EmitsEvents(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{
		Name: "test", APIKey: "k", Endpoint: "https://t.com", Model: "m",
		MaxFailures: 2,
	})
	emit := &mockEmitter{}
	bp.SetEmitter(emit)

	bp.RecordFailure() // count=1, below threshold
	if emit.Count(core.EventProviderFail) != 1 {
		t.Fatalf("expected 1 fail event, got %d", emit.Count(core.EventProviderFail))
	}
	if emit.Count(core.EventProviderOpen) != 0 {
		t.Fatalf("expected 0 open events before threshold, got %d", emit.Count(core.EventProviderOpen))
	}

	bp.RecordFailure() // count=2, reaches threshold => circuit opens
	if emit.Count(core.EventProviderFail) != 2 {
		t.Fatalf("expected 2 fail events, got %d", emit.Count(core.EventProviderFail))
	}
	if emit.Count(core.EventProviderOpen) != 1 {
		t.Fatalf("expected 1 open event after threshold, got %d", emit.Count(core.EventProviderOpen))
	}
}

func TestBaseProvider_RecordFailure_DefaultMaxFailures(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{
		Name: "test", APIKey: "k", Endpoint: "https://t.com", Model: "m",
		// MaxFailures=0 => defaults to 3
	})

	bp.RecordFailure()
	bp.RecordFailure()
	if !bp.IsAvailable() {
		t.Fatal("should still be available after 2 failures (default max is 3)")
	}

	bp.RecordFailure()
	if bp.IsAvailable() {
		t.Fatal("should be unavailable after 3 failures")
	}
}

func TestBaseProvider_SetEmitter_NilFallsBackToNop(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{Name: "test"})
	bp.SetEmitter(nil)

	// Should not panic when emitting events.
	em := bp.getEmitter()
	if em == nil {
		t.Fatal("expected non-nil emitter after SetEmitter(nil)")
	}
	em.Emit("test-topic", "data")
}

func TestBaseProvider_FailUntil_ZeroValue(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{})

	if !bp.FailUntil().IsZero() {
		t.Fatal("expected zero FailUntil initially")
	}

	// Set to zero explicitly.
	bp.SetFailUntil(time.Time{})
	if !bp.FailUntil().IsZero() {
		t.Fatal("expected zero FailUntil after SetFailUntil(zero)")
	}
}

func TestBaseProvider_FailUntil_SetAndClear(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{})

	future := time.Now().Add(time.Hour)
	bp.SetFailUntil(future)
	if bp.FailUntil().IsZero() {
		t.Fatal("expected non-zero FailUntil after setting")
	}

	// Clear it.
	bp.SetFailUntil(time.Time{})
	if !bp.FailUntil().IsZero() {
		t.Fatal("expected zero FailUntil after clearing")
	}
}

func TestBaseProvider_SystemPrompt_Lifecycle(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{})

	// Initial state: empty.
	if bp.GetSystemPrompt() != "" {
		t.Fatal("expected empty system prompt initially")
	}

	// Set.
	bp.SetSystemPrompt("You are a helpful assistant.")
	if bp.GetSystemPrompt() != "You are a helpful assistant." {
		t.Fatalf("unexpected system prompt: %s", bp.GetSystemPrompt())
	}

	// Override.
	bp.SetSystemPrompt("New prompt.")
	if bp.GetSystemPrompt() != "New prompt." {
		t.Fatalf("expected overridden prompt, got: %s", bp.GetSystemPrompt())
	}

	// Clear.
	bp.ClearSystemPrompt()
	if bp.GetSystemPrompt() != "" {
		t.Fatal("expected empty after clear")
	}
}

func TestBaseProvider_Close_ReturnsNil(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{})
	if err := bp.Close(); err != nil {
		t.Fatalf("BaseProvider.Close should return nil, got: %v", err)
	}
}

func TestOpenAIProvider_Close_NoStreams(t *testing.T) {
	p := NewOpenAIProvider(core.ProviderConfig{
		Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m",
	})
	// Close without any active streams should return nil immediately.
	if err := p.Close(); err != nil {
		t.Fatalf("Close should return nil: %v", err)
	}
}

func TestBuildOpenAIMessages_AllRoles(t *testing.T) {
	msgs := []core.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hello", Name: "user1"},
		{Role: "assistant", Content: "Hi there"},
		{Role: "tool", ToolCallID: "tc_1", Content: "result"},
	}

	result := buildOpenAIMessages(msgs)
	if len(result) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(result))
	}
	if result[0].Role != "system" || result[0].Content != "You are helpful." {
		t.Fatalf("unexpected system message: %+v", result[0])
	}
	if result[1].Name != "user1" {
		t.Fatalf("expected Name 'user1', got '%s'", result[1].Name)
	}
	if result[3].ToolCallID != "tc_1" {
		t.Fatalf("expected ToolCallID 'tc_1', got '%s'", result[3].ToolCallID)
	}
}

func TestBuildOpenAIMessages_ToolCallsWithIndex(t *testing.T) {
	msgs := []core.Message{
		{Role: "assistant", ToolCalls: []core.ToolCall{
			{ID: "call_1", Type: "function", Index: 0, Function: core.FunctionCall{Name: "fn1", Arguments: "{}"}},
			{ID: "call_2", Type: "function", Index: 1, Function: core.FunctionCall{Name: "fn2", Arguments: `{"x":1}`}},
		}},
	}

	result := buildOpenAIMessages(msgs)
	if len(result[0].ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(result[0].ToolCalls))
	}
	if result[0].ToolCalls[0].Function.Name != "fn1" {
		t.Fatalf("expected fn1, got %s", result[0].ToolCalls[0].Function.Name)
	}
	if result[0].ToolCalls[1].Function.Name != "fn2" {
		t.Fatalf("expected fn2, got %s", result[0].ToolCalls[1].Function.Name)
	}
	if *result[0].ToolCalls[0].Index != 0 {
		t.Fatalf("expected Index 0, got %d", *result[0].ToolCalls[0].Index)
	}
	if *result[0].ToolCalls[1].Index != 1 {
		t.Fatalf("expected Index 1, got %d", *result[0].ToolCalls[1].Index)
	}
}

func TestBuildOpenAIMessages_EmptyMessages(t *testing.T) {
	result := buildOpenAIMessages(nil)
	if len(result) != 0 {
		t.Fatalf("expected empty result for nil input, got %d", len(result))
	}

	result = buildOpenAIMessages([]core.Message{})
	if len(result) != 0 {
		t.Fatalf("expected empty result for empty input, got %d", len(result))
	}
}

func TestBuildOpenAIMessages_NoToolCalls(t *testing.T) {
	msgs := []core.Message{
		{Role: "user", Content: "Hello"},
	}
	result := buildOpenAIMessages(msgs)
	if len(result[0].ToolCalls) != 0 {
		t.Fatalf("expected no tool calls, got %d", len(result[0].ToolCalls))
	}
}

func TestBuildOpenAITools_Empty(t *testing.T) {
	if result := buildOpenAITools(nil); result != nil {
		t.Fatalf("expected nil for nil input, got %v", result)
	}
	if result := buildOpenAITools([]core.Tool{}); result != nil {
		t.Fatalf("expected nil for empty input, got %v", result)
	}
}

func TestBuildOpenAITools_NilFunction(t *testing.T) {
	tools := []core.Tool{
		{Type: "function", Function: nil},
	}
	result := buildOpenAITools(tools)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result))
	}
	if result[0].Function != nil {
		t.Fatal("expected nil Function in output")
	}
}

func TestBuildOpenAIToolChoice_UnknownType(t *testing.T) {
	// Non-string, non-ToolChoice value should pass through.
	v := buildOpenAIToolChoice(42)
	if v != 42 {
		t.Fatalf("expected 42, got %v", v)
	}
}

func TestBuildCoreChoice_BasicMessage(t *testing.T) {
	choice := openai.ChatCompletionChoice{
		Index: 0,
		Message: openai.ChatCompletionMessage{
			Role:    "assistant",
			Content: "Hello",
		},
		FinishReason: "stop",
	}

	result := buildCoreChoice(choice)
	if result.Index != 0 {
		t.Fatalf("expected index 0, got %d", result.Index)
	}
	if result.Message.Role != "assistant" {
		t.Fatalf("expected role 'assistant', got '%s'", result.Message.Role)
	}
	if result.Message.Content != "Hello" {
		t.Fatalf("expected content 'Hello', got '%s'", result.Message.Content)
	}
	if result.FinishReason != "stop" {
		t.Fatalf("expected finish reason 'stop', got '%s'", result.FinishReason)
	}
}

func TestBuildCoreChoice_WithToolCalls(t *testing.T) {
	idx0 := 0
	idx1 := 1
	choice := openai.ChatCompletionChoice{
		Index: 1,
		Message: openai.ChatCompletionMessage{
			Role:       "assistant",
			ToolCallID: "tc_1",
			ToolCalls: []openai.ToolCall{
				{ID: "call_1", Type: "function", Index: &idx0, Function: openai.FunctionCall{Name: "fn1", Arguments: "{}"}},
				{ID: "call_2", Type: "function", Index: &idx1, Function: openai.FunctionCall{Name: "fn2", Arguments: `{"x":1}`}},
			},
		},
		FinishReason: "tool_calls",
	}

	result := buildCoreChoice(choice)
	if result.Message.ToolCallID != "tc_1" {
		t.Fatalf("expected ToolCallID 'tc_1', got '%s'", result.Message.ToolCallID)
	}
	if len(result.Message.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(result.Message.ToolCalls))
	}
	if result.Message.ToolCalls[0].ID != "call_1" {
		t.Fatalf("expected call_1, got %s", result.Message.ToolCalls[0].ID)
	}
	if result.Message.ToolCalls[0].Index != 0 {
		t.Fatalf("expected Index 0, got %d", result.Message.ToolCalls[0].Index)
	}
	if result.Message.ToolCalls[1].Index != 1 {
		t.Fatalf("expected Index 1, got %d", result.Message.ToolCalls[1].Index)
	}
	if result.FinishReason != "tool_calls" {
		t.Fatalf("expected finish reason 'tool_calls', got '%s'", result.FinishReason)
	}
}

func TestBuildCoreChoice_NilToolCallIndex(t *testing.T) {
	choice := openai.ChatCompletionChoice{
		Index: 0,
		Message: openai.ChatCompletionMessage{
			Role: "assistant",
			ToolCalls: []openai.ToolCall{
				{ID: "call_1", Type: "function", Index: nil, Function: openai.FunctionCall{Name: "fn1"}},
			},
		},
	}

	result := buildCoreChoice(choice)
	if len(result.Message.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.Message.ToolCalls))
	}
	// Index should be zero value when nil.
	if result.Message.ToolCalls[0].Index != 0 {
		t.Fatalf("expected Index 0 for nil pointer, got %d", result.Message.ToolCalls[0].Index)
	}
}

func TestIsTemperatureError_CaseInsensitive(t *testing.T) {
	if !isTemperatureError(errors.New("TEMPERATURE must be 1")) {
		t.Fatal("should match uppercase TEMPERATURE")
	}
	if !isTemperatureError(errors.New("Invalid Temperature Value")) {
		t.Fatal("should match mixed case")
	}
}

func TestOpenAIProvider_ChatCompleteStream_NotSupported(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{Name: "test"})
	// BaseProvider.ChatCompleteStream returns "not supported" error.
	_, err := bp.ChatCompleteStream(context.Background(), core.ChatCompletionRequest{})
	if err == nil {
		t.Fatal("expected error for unsupported streaming")
	}
	if !strings.Contains(err.Error(), "does not support streaming") {
		t.Fatalf("expected 'does not support streaming' error, got: %v", err)
	}
}

func TestOpenAIProvider_ListModels_ConfigIncomplete(t *testing.T) {
	// No APIKey, default endpoint is set. ListModels should fail.
	p := NewOpenAIProvider(core.ProviderConfig{Name: "test"})
	_, err := p.ListModels(context.Background())
	if err == nil {
		t.Fatal("expected error for incomplete config")
	}
}

func TestBaseProvider_ListModels_ConfigIncomplete(t *testing.T) {
	bp := NewBaseProvider(core.ProviderConfig{Name: "test"})
	_, err := bp.ListModels(context.Background())
	if err == nil {
		t.Fatal("expected error for incomplete config")
	}
	if !strings.Contains(err.Error(), "config incomplete") {
		t.Fatalf("expected 'config incomplete' error, got: %v", err)
	}
}

// newMockHTTPServer creates a test HTTP server that responds with the given status and body.
func newMockHTTPServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	return srv
}

func TestBaseProvider_HealthCheck_ContextCanceled(t *testing.T) {
	// Use a server that blocks until context is canceled.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	bp := NewBaseProvider(core.ProviderConfig{
		Name:       "test",
		APIKey:     "test-key",
		Endpoint:   srv.URL,
		Model:      "m",
		HTTPClient: srv.Client(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	err := bp.HealthCheck(ctx)
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}
