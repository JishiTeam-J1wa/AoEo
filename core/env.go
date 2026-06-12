package core

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// LoadConfigFromEnv 从 AOEO_PROVIDER_N_* 环境变量加载多 Provider 配置。
// 索引从 0 开始，遇到空位（NAME 为空）终止扫描。
// 同时加载 AOEO_AUDIT_ENABLED 和 AOEO_RETRY_* 配置。
func LoadConfigFromEnv() Config {
	return LoadConfigFromEnvWithPrefix("AOEO")
}

// LoadConfigFromEnvWithPrefix 使用自定义前缀从环境变量构建 Config。
// 例如前缀为 "MYAPP" 时，将读取 MYAPP_PROVIDER_0_NAME 等变量。
// 扫描逻辑与 LoadConfigFromEnv 相同：从索引 0 开始，遇到 NAME 为空时终止。
func LoadConfigFromEnvWithPrefix(prefix string) Config {
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
	return cfg
}

// EnvConfigString 从单个环境变量中解析 Provider 配置。
// 环境变量值的格式为 "name|apiKey|endpoint|model|maxConcurrent|proxy"，
// 用于简化部署场景。如果变量未设置，返回空的 ProviderConfig。
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

// SetEnvConfig 将 Config 写入环境变量。
// 主要用于测试和工具链，不建议在生产环境中用于存储敏感密钥。
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

// UnsetEnvConfig 清除由 SetEnvConfig 设置的所有 AOEO_ 环境变量。
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

// RetryConfigFromEnv 从环境变量加载重试配置。
//
//	AOEO_RETRY_MAX_RETRIES  - 最大重试次数，默认 0（禁用）
//	AOEO_RETRY_BASE_DELAY   - 基础延迟时间，默认 1s
//	AOEO_RETRY_MAX_DELAY    - 最大延迟时间，默认 30s
//	AOEO_RETRY_MULTIPLIER   - 指数退避乘数，默认 2.0
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
