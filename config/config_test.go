package config

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

// ---------- helpers ----------

// validProviderYAML returns a minimal valid YAML configuration string.
func validProviderYAML() string {
	return `
server:
  addr: ":9090"
providers:
  - name: "test-provider"
    api_key: "sk-test-key"
    endpoint: "https://api.example.com/v1"
    model: "gpt-4"
`
}

// writeTempYAML creates a temporary file with the given content and returns its path.
func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

// ========================================================================
// Tests for config.go
// ========================================================================

// ---------- TestExpandEnvVars ----------

func TestExpandEnvVars(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string // env vars to set (key -> value)
		unset []string         // env vars to ensure are unset
		input string
		want  string
	}{
		{
			name:  "existing env var is replaced",
			env:   map[string]string{"AOEO_TEST_EXISTING": "hello"},
			input: "value: ${AOEO_TEST_EXISTING}",
			want:  "value: hello",
		},
		{
			name:  "missing env var becomes empty string",
			unset: []string{"AOEO_TEST_NONEXISTENT_XYZ"},
			input: "value: ${AOEO_TEST_NONEXISTENT_XYZ}",
			want:  "value: ",
		},
		{
			name:  "default with missing env var uses default",
			unset: []string{"AOEO_TEST_MISSING_DEF"},
			input: "value: ${AOEO_TEST_MISSING_DEF:-fallback}",
			want:  "value: fallback",
		},
		{
			name:  "default with existing env var uses env value",
			env:   map[string]string{"AOEO_TEST_HAS_DEF": "real_value"},
			input: "value: ${AOEO_TEST_HAS_DEF:-fallback}",
			want:  "value: real_value",
		},
		{
			name:  "no env vars in text leaves it unchanged",
			input: "value: plain text without any vars",
			want:  "value: plain text without any vars",
		},
		{
			name: "multiple env vars in same text",
			env: map[string]string{
				"AOEO_TEST_MULTI_A": "alpha",
				"AOEO_TEST_MULTI_B": "beta",
			},
			input: "${AOEO_TEST_MULTI_A} and ${AOEO_TEST_MULTI_B}",
			want:  "alpha and beta",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			for _, k := range tt.unset {
				t.Setenv(k, "")
				os.Unsetenv(k)
			}

			got := string(expandEnvVars([]byte(tt.input)))
			if got != tt.want {
				t.Errorf("expandEnvVars(%q)\n got  %q\n want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------- TestLoadConfig ----------

func TestLoadConfig(t *testing.T) {
	t.Run("valid YAML file returns success", func(t *testing.T) {
		path := writeTempYAML(t, validProviderYAML())

		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Server.Addr != ":9090" {
			t.Errorf("Server.Addr = %q, want %q", cfg.Server.Addr, ":9090")
		}
		if len(cfg.Providers) != 1 {
			t.Fatalf("len(Providers) = %d, want 1", len(cfg.Providers))
		}
		if cfg.Providers[0].Name != "test-provider" {
			t.Errorf("Provider.Name = %q, want %q", cfg.Providers[0].Name, "test-provider")
		}
		if cfg.Providers[0].APIKey != "sk-test-key" {
			t.Errorf("Provider.APIKey = %q, want %q", cfg.Providers[0].APIKey, "sk-test-key")
		}
		if cfg.Providers[0].Endpoint != "https://api.example.com/v1" {
			t.Errorf("Provider.Endpoint = %q, want %q", cfg.Providers[0].Endpoint, "https://api.example.com/v1")
		}
		if cfg.Providers[0].Model != "gpt-4" {
			t.Errorf("Provider.Model = %q, want %q", cfg.Providers[0].Model, "gpt-4")
		}
	})

	t.Run("missing file returns error", func(t *testing.T) {
		_, err := LoadConfig("/nonexistent/path/config.yaml")
		if err == nil {
			t.Fatal("expected error for missing file, got nil")
		}
		if !strings.Contains(err.Error(), "读取配置文件失败") {
			t.Errorf("error = %q, want it to contain %q", err.Error(), "读取配置文件失败")
		}
	})

	t.Run("invalid YAML returns error", func(t *testing.T) {
		path := writeTempYAML(t, ":\n  invalid: [yaml\n  broken: {{{")

		_, err := LoadConfig(path)
		if err == nil {
			t.Fatal("expected error for invalid YAML, got nil")
		}
		if !strings.Contains(err.Error(), "解析 YAML 失败") {
			t.Errorf("error = %q, want it to contain %q", err.Error(), "解析 YAML 失败")
		}
	})

	t.Run("config with env var substitution", func(t *testing.T) {
		t.Setenv("AOEO_LOAD_TEST_KEY", "sk-from-env")
		t.Setenv("AOEO_LOAD_TEST_EP", "https://env.example.com/v1")

		yamlContent := `
providers:
  - name: "env-provider"
    api_key: "${AOEO_LOAD_TEST_KEY}"
    endpoint: "${AOEO_LOAD_TEST_EP}"
    model: "gpt-4"
`
		path := writeTempYAML(t, yamlContent)

		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Providers[0].APIKey != "sk-from-env" {
			t.Errorf("APIKey = %q, want %q", cfg.Providers[0].APIKey, "sk-from-env")
		}
		if cfg.Providers[0].Endpoint != "https://env.example.com/v1" {
			t.Errorf("Endpoint = %q, want %q", cfg.Providers[0].Endpoint, "https://env.example.com/v1")
		}
	})

	t.Run("env var with default in YAML", func(t *testing.T) {
		os.Unsetenv("AOEO_LOAD_MISSING_VAR")

		yamlContent := `
providers:
  - name: "default-provider"
    api_key: "${AOEO_LOAD_MISSING_VAR:-sk-default-key}"
    endpoint: "https://api.example.com/v1"
    model: "gpt-4"
`
		path := writeTempYAML(t, yamlContent)

		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Providers[0].APIKey != "sk-default-key" {
			t.Errorf("APIKey = %q, want %q", cfg.Providers[0].APIKey, "sk-default-key")
		}
	})

	t.Run("valid YAML but validation failure returns error", func(t *testing.T) {
		// Valid YAML that parses fine but fails validation (no providers)
		yamlContent := `
server:
  addr: ":8081"
providers: []
`
		path := writeTempYAML(t, yamlContent)

		_, err := LoadConfig(path)
		if err == nil {
			t.Fatal("expected validation error, got nil")
		}
		if !strings.Contains(err.Error(), "配置校验失败") {
			t.Errorf("error = %q, want it to contain %q", err.Error(), "配置校验失败")
		}
	})
}

// ---------- TestApplyDefaults ----------

func TestApplyDefaults(t *testing.T) {
	t.Run("all zero values get defaults", func(t *testing.T) {
		cfg := &AoEoConfig{}
		cfg.ApplyDefaults()

		if cfg.Server.Addr != ":8081" {
			t.Errorf("Server.Addr = %q, want %q", cfg.Server.Addr, ":8081")
		}
		if cfg.Server.ReadTimeout != 120*time.Second {
			t.Errorf("Server.ReadTimeout = %v, want %v", cfg.Server.ReadTimeout, 120*time.Second)
		}
		if cfg.Server.WriteTimeout != 120*time.Second {
			t.Errorf("Server.WriteTimeout = %v, want %v", cfg.Server.WriteTimeout, 120*time.Second)
		}
		if cfg.HealthCheck.Interval != 30*time.Second {
			t.Errorf("HealthCheck.Interval = %v, want %v", cfg.HealthCheck.Interval, 30*time.Second)
		}
		if cfg.History.RingSize != 1000 {
			t.Errorf("History.RingSize = %d, want %d", cfg.History.RingSize, 1000)
		}
		if cfg.Retry.MaxRetries != 2 {
			t.Errorf("Retry.MaxRetries = %d, want %d", cfg.Retry.MaxRetries, 2)
		}
		if cfg.Retry.BaseDelay != 500*time.Millisecond {
			t.Errorf("Retry.BaseDelay = %v, want %v", cfg.Retry.BaseDelay, 500*time.Millisecond)
		}
		if cfg.Retry.MaxDelay != 5*time.Second {
			t.Errorf("Retry.MaxDelay = %v, want %v", cfg.Retry.MaxDelay, 5*time.Second)
		}
		if cfg.Retry.Multiplier != 2.0 {
			t.Errorf("Retry.Multiplier = %f, want %f", cfg.Retry.Multiplier, 2.0)
		}
	})

	t.Run("preset values are preserved and only zero values get defaults", func(t *testing.T) {
		cfg := &AoEoConfig{
			Server: ServerConfig{
				Addr:         ":9999",
				ReadTimeout:  30 * time.Second,
				WriteTimeout: 45 * time.Second,
			},
			HealthCheck: HealthCheckYAML{
				Interval: 10 * time.Second,
			},
			History: HistoryYAML{
				RingSize: 500,
			},
			Retry: RetryYAML{
				MaxRetries: 5,
				BaseDelay:  1 * time.Second,
				MaxDelay:   10 * time.Second,
				Multiplier: 3.0,
			},
		}
		cfg.ApplyDefaults()

		// All preset values should be preserved
		if cfg.Server.Addr != ":9999" {
			t.Errorf("Server.Addr = %q, want %q (should not be overwritten)", cfg.Server.Addr, ":9999")
		}
		if cfg.Server.ReadTimeout != 30*time.Second {
			t.Errorf("Server.ReadTimeout = %v, want %v", cfg.Server.ReadTimeout, 30*time.Second)
		}
		if cfg.Server.WriteTimeout != 45*time.Second {
			t.Errorf("Server.WriteTimeout = %v, want %v", cfg.Server.WriteTimeout, 45*time.Second)
		}
		if cfg.HealthCheck.Interval != 10*time.Second {
			t.Errorf("HealthCheck.Interval = %v, want %v", cfg.HealthCheck.Interval, 10*time.Second)
		}
		if cfg.History.RingSize != 500 {
			t.Errorf("History.RingSize = %d, want %d", cfg.History.RingSize, 500)
		}
		if cfg.Retry.MaxRetries != 5 {
			t.Errorf("Retry.MaxRetries = %d, want %d", cfg.Retry.MaxRetries, 5)
		}
		if cfg.Retry.BaseDelay != 1*time.Second {
			t.Errorf("Retry.BaseDelay = %v, want %v", cfg.Retry.BaseDelay, 1*time.Second)
		}
		if cfg.Retry.MaxDelay != 10*time.Second {
			t.Errorf("Retry.MaxDelay = %v, want %v", cfg.Retry.MaxDelay, 10*time.Second)
		}
		if cfg.Retry.Multiplier != 3.0 {
			t.Errorf("Retry.Multiplier = %f, want %f", cfg.Retry.Multiplier, 3.0)
		}
	})
}

// ---------- TestValidate ----------

func TestValidate(t *testing.T) {
	t.Run("valid config returns no error", func(t *testing.T) {
		cfg := &AoEoConfig{
			Server: ServerConfig{Addr: ":8081"},
			Providers: []ProviderYAML{
				{
					Name:     "test",
					APIKey:   "sk-key",
					Endpoint: "https://api.example.com/v1",
					Model:    "gpt-4",
				},
			},
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("empty providers returns error", func(t *testing.T) {
		cfg := &AoEoConfig{
			Server:    ServerConfig{Addr: ":8081"},
			Providers: []ProviderYAML{},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("expected error for empty providers, got nil")
		}
		if !strings.Contains(err.Error(), "至少需要配置一个 provider") {
			t.Errorf("error = %q, want it to contain %q", err.Error(), "至少需要配置一个 provider")
		}
	})

	t.Run("missing provider name returns error", func(t *testing.T) {
		cfg := &AoEoConfig{
			Server: ServerConfig{Addr: ":8081"},
			Providers: []ProviderYAML{
				{
					Name:     "",
					APIKey:   "sk-key",
					Endpoint: "https://api.example.com/v1",
					Model:    "gpt-4",
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("expected error for missing provider name, got nil")
		}
		if !strings.Contains(err.Error(), "name 不能为空") {
			t.Errorf("error = %q, want it to contain %q", err.Error(), "name 不能为空")
		}
	})

	t.Run("missing provider APIKey returns error", func(t *testing.T) {
		cfg := &AoEoConfig{
			Server: ServerConfig{Addr: ":8081"},
			Providers: []ProviderYAML{
				{
					Name:     "test",
					APIKey:   "",
					Endpoint: "https://api.example.com/v1",
					Model:    "gpt-4",
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("expected error for missing APIKey, got nil")
		}
		if !strings.Contains(err.Error(), "api_key 不能为空") {
			t.Errorf("error = %q, want it to contain %q", err.Error(), "api_key 不能为空")
		}
	})

	t.Run("missing provider endpoint returns error", func(t *testing.T) {
		cfg := &AoEoConfig{
			Server: ServerConfig{Addr: ":8081"},
			Providers: []ProviderYAML{
				{
					Name:   "test",
					APIKey: "sk-key",
					Model:  "gpt-4",
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("expected error for missing endpoint, got nil")
		}
		if !strings.Contains(err.Error(), "endpoint 不能为空") {
			t.Errorf("error = %q, want it to contain %q", err.Error(), "endpoint 不能为空")
		}
	})

	t.Run("missing provider model returns error", func(t *testing.T) {
		cfg := &AoEoConfig{
			Server: ServerConfig{Addr: ":8081"},
			Providers: []ProviderYAML{
				{
					Name:     "test",
					APIKey:   "sk-key",
					Endpoint: "https://api.example.com/v1",
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("expected error for missing model, got nil")
		}
		if !strings.Contains(err.Error(), "model 不能为空") {
			t.Errorf("error = %q, want it to contain %q", err.Error(), "model 不能为空")
		}
	})

	t.Run("invalid endpoint URL returns error", func(t *testing.T) {
		cfg := &AoEoConfig{
			Server: ServerConfig{Addr: ":8081"},
			Providers: []ProviderYAML{
				{
					Name:     "test",
					APIKey:   "sk-key",
					Endpoint: "not-a-valid-url",
					Model:    "gpt-4",
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("expected error for invalid endpoint URL, got nil")
		}
		if !strings.Contains(err.Error(), "配置错误") {
			t.Errorf("error = %q, want it to contain %q", err.Error(), "配置错误")
		}
	})

	t.Run("empty server addr returns error", func(t *testing.T) {
		cfg := &AoEoConfig{
			Server: ServerConfig{Addr: ""},
			Providers: []ProviderYAML{
				{
					Name:     "test",
					APIKey:   "sk-key",
					Endpoint: "https://api.example.com/v1",
					Model:    "gpt-4",
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("expected error for empty server addr, got nil")
		}
		if !strings.Contains(err.Error(), "server.addr 不能为空") {
			t.Errorf("error = %q, want it to contain %q", err.Error(), "server.addr 不能为空")
		}
	})
}

// ---------- TestToCoreConfig ----------

func TestToCoreConfig(t *testing.T) {
	t.Run("multiple providers are correctly mapped", func(t *testing.T) {
		cfg := &AoEoConfig{
			Providers: []ProviderYAML{
				{
					Name:          "provider-a",
					APIKey:        "sk-a",
					Endpoint:      "https://api-a.example.com/v1",
					Model:         "model-a",
					MaxConcurrent: 10,
					SkipTLSVerify: true,
					MaxFailures:   5,
					Proxy:         "http://proxy:8080",
					Pricing: PricingYAML{
						PromptPer1K:     0.01,
						CompletionPer1K: 0.02,
						Currency:        "USD",
					},
				},
				{
					Name:     "provider-b",
					APIKey:   "sk-b",
					Endpoint: "https://api-b.example.com/v1",
					Model:    "model-b",
					Pricing: PricingYAML{
						PromptPer1K:     0.03,
						CompletionPer1K: 0.04,
						Currency:        "CNY",
					},
				},
			},
		}

		coreCfg := cfg.ToCoreConfig()

		if len(coreCfg.Providers) != 2 {
			t.Fatalf("len(Providers) = %d, want 2", len(coreCfg.Providers))
		}

		// Verify first provider
		p0 := coreCfg.Providers[0]
		if p0.Name != "provider-a" {
			t.Errorf("Providers[0].Name = %q, want %q", p0.Name, "provider-a")
		}
		if p0.APIKey != "sk-a" {
			t.Errorf("Providers[0].APIKey = %q, want %q", p0.APIKey, "sk-a")
		}
		if p0.Endpoint != "https://api-a.example.com/v1" {
			t.Errorf("Providers[0].Endpoint = %q, want %q", p0.Endpoint, "https://api-a.example.com/v1")
		}
		if p0.Model != "model-a" {
			t.Errorf("Providers[0].Model = %q, want %q", p0.Model, "model-a")
		}
		if p0.MaxConcurrent != 10 {
			t.Errorf("Providers[0].MaxConcurrent = %d, want 10", p0.MaxConcurrent)
		}
		if !p0.SkipTLSVerify {
			t.Error("Providers[0].SkipTLSVerify = false, want true")
		}
		if p0.MaxFailures != 5 {
			t.Errorf("Providers[0].MaxFailures = %d, want 5", p0.MaxFailures)
		}
		if p0.Proxy != "http://proxy:8080" {
			t.Errorf("Providers[0].Proxy = %q, want %q", p0.Proxy, "http://proxy:8080")
		}

		// Verify pricing fields
		if p0.Pricing.PromptPer1K != 0.01 {
			t.Errorf("Providers[0].Pricing.PromptPer1K = %f, want 0.01", p0.Pricing.PromptPer1K)
		}
		if p0.Pricing.CompletionPer1K != 0.02 {
			t.Errorf("Providers[0].Pricing.CompletionPer1K = %f, want 0.02", p0.Pricing.CompletionPer1K)
		}
		if p0.Pricing.Currency != "USD" {
			t.Errorf("Providers[0].Pricing.Currency = %q, want %q", p0.Pricing.Currency, "USD")
		}

		// Verify second provider pricing
		p1 := coreCfg.Providers[1]
		if p1.Pricing.PromptPer1K != 0.03 {
			t.Errorf("Providers[1].Pricing.PromptPer1K = %f, want 0.03", p1.Pricing.PromptPer1K)
		}
		if p1.Pricing.CompletionPer1K != 0.04 {
			t.Errorf("Providers[1].Pricing.CompletionPer1K = %f, want 0.04", p1.Pricing.CompletionPer1K)
		}
		if p1.Pricing.Currency != "CNY" {
			t.Errorf("Providers[1].Pricing.Currency = %q, want %q", p1.Pricing.Currency, "CNY")
		}

		// Verify AuditEnabled is true
		if !coreCfg.AuditEnabled {
			t.Error("AuditEnabled = false, want true")
		}
	})
}

// ---------- TestBuildRouter ----------

func TestBuildRouter(t *testing.T) {
	tests := []struct {
		name     string
		strategy string
		wantType string
	}{
		{"round-robin strategy", "round-robin", "*core.RoundRobinRouter"},
		{"random strategy", "random", "*core.RandomRouter"},
		{"weighted strategy", "weighted", "*core.WeightedRouter"},
		{"primary strategy", "primary", "*core.PrimaryRouter"},
		{"empty strategy defaults to primary", "", "*core.PrimaryRouter"},
		{"unknown strategy defaults to primary", "unknown-strategy", "*core.PrimaryRouter"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &AoEoConfig{
				Router: RouterConfig{Strategy: tt.strategy},
			}
			router := cfg.BuildRouter()

			switch tt.wantType {
			case "*core.RoundRobinRouter":
				if _, ok := router.(*core.RoundRobinRouter); !ok {
					t.Errorf("got %T, want *core.RoundRobinRouter", router)
				}
			case "*core.RandomRouter":
				if _, ok := router.(*core.RandomRouter); !ok {
					t.Errorf("got %T, want *core.RandomRouter", router)
				}
			case "*core.WeightedRouter":
				if _, ok := router.(*core.WeightedRouter); !ok {
					t.Errorf("got %T, want *core.WeightedRouter", router)
				}
			case "*core.PrimaryRouter":
				if _, ok := router.(*core.PrimaryRouter); !ok {
					t.Errorf("got %T, want *core.PrimaryRouter", router)
				}
			}
		})
	}

	t.Run("weighted router uses configured weight strategy", func(t *testing.T) {
		cfg := &AoEoConfig{
			Router: RouterConfig{
				Strategy:       "weighted",
				WeightStrategy: "latency",
			},
		}
		router := cfg.BuildRouter()
		wr, ok := router.(*core.WeightedRouter)
		if !ok {
			t.Fatalf("got %T, want *core.WeightedRouter", router)
		}
		if wr.Strategy != core.StrategyLatency {
			t.Errorf("WeightedRouter.Strategy = %v, want %v (StrategyLatency)", wr.Strategy, core.StrategyLatency)
		}
	})
}

// ---------- TestMapWeightStrategy ----------

func TestMapWeightStrategy(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected core.WeightStrategy
	}{
		{"latency maps to StrategyLatency", "latency", core.StrategyLatency},
		{"success_rate maps to StrategySuccessRate", "success_rate", core.StrategySuccessRate},
		{"combined maps to StrategyCombined", "combined", core.StrategyCombined},
		{"empty string defaults to StrategyCombined", "", core.StrategyCombined},
		{"unknown value defaults to StrategyCombined", "unknown", core.StrategyCombined},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &AoEoConfig{
				Router: RouterConfig{WeightStrategy: tt.input},
			}
			got := cfg.mapWeightStrategy()
			if got != tt.expected {
				t.Errorf("mapWeightStrategy() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// ========================================================================
// Tests for watcher.go
// ========================================================================

// ---------- TestConfigWatcher ----------

func TestConfigWatcher(t *testing.T) {
	t.Run("detects file modification and fires callback", func(t *testing.T) {
		yamlContent := validProviderYAML()
		path := writeTempYAML(t, yamlContent)

		var mu sync.Mutex
		var receivedCfg *AoEoConfig
		done := make(chan struct{})

		watcher := NewConfigWatcher(path, 50*time.Millisecond, func(cfg *AoEoConfig) {
			mu.Lock()
			receivedCfg = cfg
			mu.Unlock()
			select {
			case <-done:
			default:
				close(done)
			}
		})

		watcher.Start()
		defer watcher.Stop()

		// Wait to ensure the watcher has recorded the initial ModTime
		time.Sleep(200 * time.Millisecond)

		// Rewrite the file to trigger a ModTime change
		if err := os.WriteFile(path, []byte(yamlContent), 0644); err != nil {
			t.Fatal(err)
		}

		// Wait for the callback to fire
		select {
		case <-done:
			mu.Lock()
			cfg := receivedCfg
			mu.Unlock()
			if cfg == nil {
				t.Fatal("callback received nil config")
			}
			if len(cfg.Providers) != 1 {
				t.Errorf("callback config has %d providers, want 1", len(cfg.Providers))
			}
			if cfg.Providers[0].Name != "test-provider" {
				t.Errorf("callback Provider.Name = %q, want %q", cfg.Providers[0].Name, "test-provider")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for config change callback")
		}
	})

	t.Run("Start is idempotent", func(t *testing.T) {
		yamlContent := validProviderYAML()
		path := writeTempYAML(t, yamlContent)

		watcher := NewConfigWatcher(path, 50*time.Millisecond, func(cfg *AoEoConfig) {})

		// Calling Start multiple times should not panic or create extra goroutines
		watcher.Start()
		watcher.Start()
		watcher.Start()
		watcher.Stop()
	})

	t.Run("Stop is idempotent", func(t *testing.T) {
		yamlContent := validProviderYAML()
		path := writeTempYAML(t, yamlContent)

		watcher := NewConfigWatcher(path, 50*time.Millisecond, func(cfg *AoEoConfig) {})

		watcher.Start()
		watcher.Stop()
		watcher.Stop()
		watcher.Stop()
	})

	t.Run("Stop on never-started watcher is safe", func(t *testing.T) {
		watcher := NewConfigWatcher("/nonexistent/file.yaml", 50*time.Millisecond, func(cfg *AoEoConfig) {})
		// Should not panic
		watcher.Stop()
	})

	t.Run("non-existent file does not crash", func(t *testing.T) {
		var callbackFired bool
		watcher := NewConfigWatcher(
			"/nonexistent/path/config.yaml",
			50*time.Millisecond,
			func(cfg *AoEoConfig) {
				callbackFired = true
			},
		)

		watcher.Start()
		// Let it poll a few times on the non-existent file
		time.Sleep(300 * time.Millisecond)
		watcher.Stop()

		if callbackFired {
			t.Error("callback should not fire for non-existent file")
		}
	})

	t.Run("default interval when zero", func(t *testing.T) {
		watcher := NewConfigWatcher("/some/path.yaml", 0, func(cfg *AoEoConfig) {})
		if watcher.interval != 5*time.Second {
			t.Errorf("interval = %v, want %v (default)", watcher.interval, 5*time.Second)
		}
	})

	t.Run("restart after stop works", func(t *testing.T) {
		yamlContent := validProviderYAML()
		path := writeTempYAML(t, yamlContent)

		done := make(chan struct{}, 1)

		watcher := NewConfigWatcher(path, 50*time.Millisecond, func(cfg *AoEoConfig) {
			select {
			case done <- struct{}{}:
			default:
			}
		})

		// First lifecycle: start and stop
		watcher.Start()
		time.Sleep(150 * time.Millisecond)
		watcher.Stop()

		// Second lifecycle: restart the watcher
		watcher.Start()
		defer watcher.Stop()

		// Wait for the watcher to record the baseline ModTime
		time.Sleep(200 * time.Millisecond)

		// Now modify the file AFTER the watcher has restarted
		if err := os.WriteFile(path, []byte(yamlContent), 0644); err != nil {
			t.Fatal(err)
		}

		// Wait for detection
		select {
		case <-done:
			// Callback fired after restart - success
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for callback after restart")
		}
	})

	t.Run("reload failure does not crash watcher", func(t *testing.T) {
		validYAML := validProviderYAML()
		path := writeTempYAML(t, validYAML)

		callbackFired := false
		watcher := NewConfigWatcher(path, 50*time.Millisecond, func(cfg *AoEoConfig) {
			callbackFired = true
		})

		watcher.Start()
		defer watcher.Stop()

		// Wait for the watcher to record the baseline ModTime
		time.Sleep(200 * time.Millisecond)

		// Rewrite with INVALID YAML - LoadConfig should fail inside checkForChanges
		time.Sleep(100 * time.Millisecond)
		if err := os.WriteFile(path, []byte(":\n  broken: [yaml\n"), 0644); err != nil {
			t.Fatal(err)
		}

		// Let the watcher detect the change and fail to reload
		time.Sleep(300 * time.Millisecond)

		// The callback should NOT fire since reload failed
		if callbackFired {
			t.Error("callback should not fire when reload fails")
		}
	})
}
