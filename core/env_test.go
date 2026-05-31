package core

import (
	"os"
	"testing"
	"time"
)

func TestLoadConfigFromEnv(t *testing.T) {
	// Clean any existing AOEO_ variables
	for _, k := range []string{
		"AOEO_PROVIDER_0_NAME", "AOEO_PROVIDER_0_API_KEY", "AOEO_PROVIDER_0_ENDPOINT", "AOEO_PROVIDER_0_MODEL", "AOEO_PROVIDER_0_MAX_CONCURRENT", "AOEO_PROVIDER_0_SKIP_TLS_VERIFY", "AOEO_PROVIDER_0_PROXY",
		"AOEO_PROVIDER_1_NAME", "AOEO_PROVIDER_1_API_KEY",
		"AOEO_AUDIT_ENABLED",
	} {
		os.Unsetenv(k)
	}

	os.Setenv("AOEO_PROVIDER_0_NAME", "deepseek")
	os.Setenv("AOEO_PROVIDER_0_API_KEY", "key0")
	os.Setenv("AOEO_PROVIDER_0_ENDPOINT", "https://api.deepseek.com")
	os.Setenv("AOEO_PROVIDER_0_MODEL", "deepseek-v4-pro")
	os.Setenv("AOEO_PROVIDER_0_MAX_CONCURRENT", "5")
	os.Setenv("AOEO_PROVIDER_0_SKIP_TLS_VERIFY", "true")
	os.Setenv("AOEO_PROVIDER_0_PROXY", "http://proxy.example.com:8080")

	os.Setenv("AOEO_PROVIDER_1_NAME", "kimi")
	os.Setenv("AOEO_PROVIDER_1_API_KEY", "key1")
	os.Setenv("AOEO_PROVIDER_1_ENDPOINT", "https://api.moonshot.cn/v1")
	os.Setenv("AOEO_PROVIDER_1_MODEL", "kimi-k2.6")

	os.Setenv("AOEO_AUDIT_ENABLED", "true")

	cfg := LoadConfigFromEnv()
	if len(cfg.Providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(cfg.Providers))
	}
	p0 := cfg.Providers[0]
	if p0.Name != "deepseek" || p0.APIKey != "key0" || p0.Endpoint != "https://api.deepseek.com" || p0.Model != "deepseek-v4-pro" {
		t.Fatalf("unexpected p0: %+v", p0)
	}
	if p0.MaxConcurrent != 5 {
		t.Fatalf("expected maxConcurrent 5, got %d", p0.MaxConcurrent)
	}
	if !p0.SkipTLSVerify {
		t.Fatal("expected SkipTLSVerify true")
	}
	if p0.Proxy != "http://proxy.example.com:8080" {
		t.Fatalf("expected proxy, got %s", p0.Proxy)
	}

	p1 := cfg.Providers[1]
	if p1.Name != "kimi" {
		t.Fatalf("expected kimi, got %s", p1.Name)
	}

	if !cfg.AuditEnabled {
		t.Fatal("expected audit enabled")
	}

	// Cleanup
	for _, k := range []string{
		"AOEO_PROVIDER_0_NAME", "AOEO_PROVIDER_0_API_KEY", "AOEO_PROVIDER_0_ENDPOINT", "AOEO_PROVIDER_0_MODEL", "AOEO_PROVIDER_0_MAX_CONCURRENT", "AOEO_PROVIDER_0_SKIP_TLS_VERIFY",
		"AOEO_PROVIDER_1_NAME", "AOEO_PROVIDER_1_API_KEY", "AOEO_PROVIDER_1_ENDPOINT", "AOEO_PROVIDER_1_MODEL",
		"AOEO_AUDIT_ENABLED",
	} {
		os.Unsetenv(k)
	}
}

func TestLoadConfigFromEnv_Empty(t *testing.T) {
	// Ensure no AOEO_PROVIDER_0_NAME exists
	os.Unsetenv("AOEO_PROVIDER_0_NAME")
	cfg := LoadConfigFromEnv()
	if len(cfg.Providers) != 0 {
		t.Fatalf("expected 0 providers, got %d", len(cfg.Providers))
	}
}

func TestLoadConfigFromEnvWithPrefix(t *testing.T) {
	os.Setenv("MYAPP_PROVIDER_0_NAME", "glm")
	os.Setenv("MYAPP_PROVIDER_0_API_KEY", "k")
	os.Setenv("MYAPP_AUDIT_ENABLED", "true")

	cfg := LoadConfigFromEnvWithPrefix("MYAPP")
	if len(cfg.Providers) != 1 || cfg.Providers[0].Name != "glm" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if !cfg.AuditEnabled {
		t.Fatal("expected audit enabled")
	}

	os.Unsetenv("MYAPP_PROVIDER_0_NAME")
	os.Unsetenv("MYAPP_PROVIDER_0_API_KEY")
	os.Unsetenv("MYAPP_AUDIT_ENABLED")
}

func TestEnvConfigString(t *testing.T) {
	os.Setenv("AOEO_TEST_CFG", "deepseek|key|https://api.deepseek.com|model|3|http://proxy:8080")
	cfg := EnvConfigString("AOEO_TEST_CFG")
	if cfg.Name != "deepseek" || cfg.APIKey != "key" || cfg.Endpoint != "https://api.deepseek.com" || cfg.Model != "model" || cfg.MaxConcurrent != 3 || cfg.Proxy != "http://proxy:8080" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	os.Unsetenv("AOEO_TEST_CFG")
}

func TestEnvConfigString_Empty(t *testing.T) {
	os.Unsetenv("AOEO_NONEXISTENT")
	cfg := EnvConfigString("AOEO_NONEXISTENT")
	if cfg.Name != "" {
		t.Fatal("expected empty config for missing env var")
	}
}

func TestSetEnvConfig(t *testing.T) {
	cfg := Config{
		Providers: []ProviderConfig{
			{Name: "qwen", APIKey: "k", Endpoint: "https://e.com", Model: "m", MaxConcurrent: 2, SkipTLSVerify: true, Proxy: "http://proxy:8080"},
		},
		AuditEnabled: true,
	}
	SetEnvConfig(cfg)

	if os.Getenv("AOEO_PROVIDER_0_NAME") != "qwen" {
		t.Fatal("SetEnvConfig did not set NAME")
	}
	if os.Getenv("AOEO_PROVIDER_0_SKIP_TLS_VERIFY") != "true" {
		t.Fatal("SetEnvConfig did not set SKIP_TLS_VERIFY")
	}
	if os.Getenv("AOEO_PROVIDER_0_PROXY") != "http://proxy:8080" {
		t.Fatal("SetEnvConfig did not set PROXY")
	}
	if os.Getenv("AOEO_AUDIT_ENABLED") != "true" {
		t.Fatal("SetEnvConfig did not set AUDIT_ENABLED")
	}

	UnsetEnvConfig(cfg)
	if os.Getenv("AOEO_PROVIDER_0_NAME") != "" {
		t.Fatal("UnsetEnvConfig did not clear NAME")
	}
}

func TestRetryConfigFromEnv(t *testing.T) {
	os.Setenv("AOEO_RETRY_MAX_RETRIES", "3")
	os.Setenv("AOEO_RETRY_BASE_DELAY", "500ms")
	os.Setenv("AOEO_RETRY_MAX_DELAY", "10s")
	os.Setenv("AOEO_RETRY_MULTIPLIER", "1.5")

	cfg := RetryConfigFromEnv()
	if cfg.MaxRetries != 3 {
		t.Fatalf("expected 3 retries, got %d", cfg.MaxRetries)
	}
	if cfg.BaseDelay != 500*time.Millisecond {
		t.Fatalf("expected 500ms base delay, got %v", cfg.BaseDelay)
	}
	if cfg.MaxDelay != 10*time.Second {
		t.Fatalf("expected 10s max delay, got %v", cfg.MaxDelay)
	}
	if cfg.Multiplier != 1.5 {
		t.Fatalf("expected 1.5 multiplier, got %f", cfg.Multiplier)
	}

	os.Unsetenv("AOEO_RETRY_MAX_RETRIES")
	os.Unsetenv("AOEO_RETRY_BASE_DELAY")
	os.Unsetenv("AOEO_RETRY_MAX_DELAY")
	os.Unsetenv("AOEO_RETRY_MULTIPLIER")
}

func TestRetryConfigFromEnv_Defaults(t *testing.T) {
	os.Unsetenv("AOEO_RETRY_MAX_RETRIES")
	os.Unsetenv("AOEO_RETRY_BASE_DELAY")
	os.Unsetenv("AOEO_RETRY_MAX_DELAY")
	os.Unsetenv("AOEO_RETRY_MULTIPLIER")

	cfg := RetryConfigFromEnv()
	if cfg.MaxRetries != 0 {
		t.Fatalf("expected 0 retries by default, got %d", cfg.MaxRetries)
	}
	if cfg.BaseDelay != 1*time.Second {
		t.Fatalf("expected 1s base delay by default, got %v", cfg.BaseDelay)
	}
	if cfg.MaxDelay != 30*time.Second {
		t.Fatalf("expected 30s max delay by default, got %v", cfg.MaxDelay)
	}
	if cfg.Multiplier != 0 {
		t.Fatalf("expected 0 multiplier by default, got %f", cfg.Multiplier)
	}
}
