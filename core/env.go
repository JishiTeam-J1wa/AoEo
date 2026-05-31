package core

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// LoadConfigFromEnv builds a Config from environment variables.
//
// Supported variables (prefix all with AOEO_):
//
//	PROVIDER_{N}_NAME           - Provider name (deepseek, kimi, glm, qwen, or custom)
//	PROVIDER_{N}_API_KEY        - API key for the provider
//	PROVIDER_{N}_ENDPOINT       - Base URL endpoint
//	PROVIDER_{N}_MODEL          - Default model ID
//	PROVIDER_{N}_MAX_CONCURRENT - Max concurrent requests (default 2)
//	PROVIDER_{N}_SKIP_TLS_VERIFY- Set to "true" to skip TLS verification
//	AUDIT_ENABLED               - Set to "true" to enable audit mode
//
// {N} is a zero-based index. Gaps in numbering terminate the scan.
// Returns an empty Config if no provider variables are found.
func LoadConfigFromEnv() Config {
	var providers []ProviderConfig
	for i := 0; ; i++ {
		prefix := fmt.Sprintf("AOEO_PROVIDER_%d_", i)
		name := os.Getenv(prefix + "NAME")
		if name == "" {
			break // gap terminates scan
		}
		cfg := ProviderConfig{
			Name:     name,
			APIKey:   os.Getenv(prefix + "API_KEY"),
			Endpoint: os.Getenv(prefix + "ENDPOINT"),
			Model:    os.Getenv(prefix + "MODEL"),
		}
		if v := os.Getenv(prefix + "MAX_CONCURRENT"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				cfg.MaxConcurrent = n
			}
		}
		if strings.EqualFold(os.Getenv(prefix+"SKIP_TLS_VERIFY"), "true") {
			cfg.SkipTLSVerify = true
		}
		cfg.Proxy = os.Getenv(prefix + "PROXY")
		providers = append(providers, cfg)
	}

	cfg := Config{Providers: providers}
	if strings.EqualFold(os.Getenv("AOEO_AUDIT_ENABLED"), "true") {
		cfg.AuditEnabled = true
	}
	return cfg
}

// LoadConfigFromEnvWithPrefix builds a Config using a custom environment variable prefix.
// For prefix "MYAPP", variables are read as MYAPP_PROVIDER_0_NAME, etc.
func LoadConfigFromEnvWithPrefix(prefix string) Config {
	oldPrefix := "AOEO_"
	var providers []ProviderConfig
	for i := 0; ; i++ {
		p := fmt.Sprintf("%s_PROVIDER_%d_", prefix, i)
		name := os.Getenv(p + "NAME")
		if name == "" {
			break
		}
		cfg := ProviderConfig{
			Name:     name,
			APIKey:   os.Getenv(p + "API_KEY"),
			Endpoint: os.Getenv(p + "ENDPOINT"),
			Model:    os.Getenv(p + "MODEL"),
		}
		if v := os.Getenv(p + "MAX_CONCURRENT"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				cfg.MaxConcurrent = n
			}
		}
		if strings.EqualFold(os.Getenv(p+"SKIP_TLS_VERIFY"), "true") {
			cfg.SkipTLSVerify = true
		}
		cfg.Proxy = os.Getenv(p + "PROXY")
		providers = append(providers, cfg)
	}

	cfg := Config{Providers: providers}
	if strings.EqualFold(os.Getenv(prefix+"_AUDIT_ENABLED"), "true") {
		cfg.AuditEnabled = true
	}
	_ = oldPrefix // unused but kept for documentation
	return cfg
}

// EnvConfigString returns a single provider config string in the form
// "name|apiKey|endpoint|model|maxConcurrent|proxy" used by some simplified deployments.
// If the variable is not set, an empty ProviderConfig is returned.
func EnvConfigString(envVar string) ProviderConfig {
	s := os.Getenv(envVar)
	if s == "" {
		return ProviderConfig{}
	}
	parts := strings.Split(s, "|")
	cfg := ProviderConfig{}
	if len(parts) > 0 {
		cfg.Name = parts[0]
	}
	if len(parts) > 1 {
		cfg.APIKey = parts[1]
	}
	if len(parts) > 2 {
		cfg.Endpoint = parts[2]
	}
	if len(parts) > 3 {
		cfg.Model = parts[3]
	}
	if len(parts) > 4 {
		if n, err := strconv.Atoi(parts[4]); err == nil {
			cfg.MaxConcurrent = n
		}
	}
	if len(parts) > 5 {
		cfg.Proxy = parts[5]
	}
	return cfg
}

// SetEnvConfig writes a Config back to environment variables.
// Primarily used for testing and tooling; not recommended for production secrets.
func SetEnvConfig(cfg Config) {
	for i, pc := range cfg.Providers {
		prefix := fmt.Sprintf("AOEO_PROVIDER_%d_", i)
		os.Setenv(prefix+"NAME", pc.Name)
		os.Setenv(prefix+"API_KEY", pc.APIKey)
		os.Setenv(prefix+"ENDPOINT", pc.Endpoint)
		os.Setenv(prefix+"MODEL", pc.Model)
		if pc.MaxConcurrent > 0 {
			os.Setenv(prefix+"MAX_CONCURRENT", strconv.Itoa(pc.MaxConcurrent))
		}
		if pc.SkipTLSVerify {
			os.Setenv(prefix+"SKIP_TLS_VERIFY", "true")
		}
		if pc.Proxy != "" {
			os.Setenv(prefix+"PROXY", pc.Proxy)
		}
	}
	if cfg.AuditEnabled {
		os.Setenv("AOEO_AUDIT_ENABLED", "true")
	}
}

// UnsetEnvConfig removes all AOEO_ environment variables set by SetEnvConfig.
func UnsetEnvConfig(cfg Config) {
	for i := range cfg.Providers {
		prefix := fmt.Sprintf("AOEO_PROVIDER_%d_", i)
		os.Unsetenv(prefix + "NAME")
		os.Unsetenv(prefix + "API_KEY")
		os.Unsetenv(prefix + "ENDPOINT")
		os.Unsetenv(prefix + "MODEL")
		os.Unsetenv(prefix + "MAX_CONCURRENT")
		os.Unsetenv(prefix + "SKIP_TLS_VERIFY")
		os.Unsetenv(prefix + "PROXY")
	}
	os.Unsetenv("AOEO_AUDIT_ENABLED")
}

// RetryConfigFromEnv loads retry settings from environment.
//
//	AOEO_RETRY_MAX_RETRIES  - default 0 (disabled)
//	AOEO_RETRY_BASE_DELAY   - default 1s
//	AOEO_RETRY_MAX_DELAY    - default 30s
//	AOEO_RETRY_MULTIPLIER   - default 2.0
func RetryConfigFromEnv() RetryConfig {
	cfg := RetryConfig{}
	if v := os.Getenv("AOEO_RETRY_MAX_RETRIES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxRetries = n
		}
	}
	cfg.BaseDelay = parseDurationEnv("AOEO_RETRY_BASE_DELAY", 1*time.Second)
	cfg.MaxDelay = parseDurationEnv("AOEO_RETRY_MAX_DELAY", 30*time.Second)
	if v := os.Getenv("AOEO_RETRY_MULTIPLIER"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Multiplier = f
		}
	}
	return cfg
}

func parseDurationEnv(key string, defaultVal time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return defaultVal
	}
	return d
}
