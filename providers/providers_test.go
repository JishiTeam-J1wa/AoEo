package providers

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
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
