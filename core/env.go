// Package core 环境变量配置加载，支持从系统环境变量读取多 Provider 配置和重试策略。
//
// Author: JishiTeam-J1wa
// Created: 2026-05
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package core

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// LoadConfigFromEnv 从 AOEO_PROVIDER_N_* 环境变量加载多 Provider 配置。
//
// 索引从 0 开始，遇到空位（NAME 为空）终止扫描。
// 同时加载 AOEO_AUDIT_ENABLED 和 AOEO_RETRY_* 配置。
//
// Return:
//   - Config: 从环境变量解析得到的配置对象
func LoadConfigFromEnv() Config {
	return LoadConfigFromEnvWithPrefix("AOEO")
}

// LoadConfigFromEnvWithPrefix 使用自定义前缀从环境变量构建 Config。
//
// 例如前缀为 "MYAPP" 时，将读取 MYAPP_PROVIDER_0_NAME 等变量。
// 扫描逻辑与 LoadConfigFromEnv 相同：从索引 0 开始，遇到 NAME 为空时终止。
//
// Param:
//   - prefix: string - 环境变量前缀，如 "AOEO"、"MYAPP"
//
// Return:
//   - Config: 从环境变量解析得到的配置对象
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
//
// 环境变量值的格式为 "name|apiKey|endpoint|model|maxConcurrent|proxy"，
// 用于简化部署场景。如果变量未设置，返回空的 ProviderConfig。
//
// Param:
//   - envVar: string - 环境变量名称
//
// Return:
//   - ProviderConfig: 解析得到的 Provider 配置，变量未设置时为零值
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
//
// 主要用于测试和工具链，不建议在生产环境中用于存储敏感密钥。
//
// Param:
//   - cfg: Config - 待写入的配置对象
func SetEnvConfig(cfg Config) {
	SetEnvConfigWithPrefix("AOEO", cfg)
}

// SetEnvConfigWithPrefix 使用自定义前缀将 Config 写入环境变量。
// 写入前会先清理同前缀的残留环境变量，防止先前配置遗留。
//
// Param:
//   - prefix: string - 环境变量前缀，如 "AOEO"、"MYAPP"
//   - cfg: Config - 待写入的配置对象
func SetEnvConfigWithPrefix(prefix string, cfg Config) {
	// 先清理同前缀的残留环境变量，防止先前配置遗留
	UnsetEnvConfigWithPrefix(prefix)
	for i, pc := range cfg.Providers {
		p := fmt.Sprintf("%s_PROVIDER_%d_", prefix, i)
		os.Setenv(p+"NAME", pc.Name)
		os.Setenv(p+"API_KEY", pc.APIKey)
		os.Setenv(p+"ENDPOINT", pc.Endpoint)
		os.Setenv(p+"MODEL", pc.Model)
		if pc.MaxConcurrent > 0 {
			os.Setenv(p+"MAX_CONCURRENT", strconv.Itoa(pc.MaxConcurrent))
		}
		if pc.SkipTLSVerify {
			os.Setenv(p+"SKIP_TLS_VERIFY", "true")
		}
		if pc.Proxy != "" {
			os.Setenv(p+"PROXY", pc.Proxy)
		}
	}
	if cfg.AuditEnabled {
		os.Setenv(prefix+"_AUDIT_ENABLED", "true")
	}
}

// UnsetEnvConfig 清除由 SetEnvConfig 设置的所有 AOEO_ 环境变量。
//
// Param:
//   - cfg: Config - 用于确定需清除的 Provider 索引范围（保留向后兼容，实际按环境变量扫描清除）
func UnsetEnvConfig(cfg Config) {
	UnsetEnvConfigWithPrefix("AOEO")
}

// UnsetEnvConfigWithPrefix 清除指定前缀的所有 AoEo 环境变量。
// 通过扫描环境变量实现，无需知晓 Provider 数量。
//
// Param:
//   - prefix: string - 环境变量前缀
func UnsetEnvConfigWithPrefix(prefix string) {
	providerPrefix := prefix + "_PROVIDER_"
	auditKey := prefix + "_AUDIT_ENABLED"
	for _, env := range os.Environ() {
		kv := strings.SplitN(env, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := kv[0]
		if strings.HasPrefix(key, providerPrefix) || key == auditKey {
			os.Unsetenv(key)
		}
	}
}

// RetryConfigFromEnv 从环境变量加载重试配置。
//
// 支持的环境变量：
//
//	AOEO_RETRY_MAX_RETRIES  - 最大重试次数，默认 0（禁用）
//	AOEO_RETRY_BASE_DELAY   - 基础延迟时间，默认 1s
//	AOEO_RETRY_MAX_DELAY    - 最大延迟时间，默认 30s
//	AOEO_RETRY_MULTIPLIER   - 指数退避乘数，默认 2.0
//
// Return:
//   - RetryConfig: 从环境变量解析得到的重试配置
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
		GetLogger().Warn("环境变量解析失败，使用默认值", "key", key, "value", v, "default", defaultVal, "error", err)
		return defaultVal
	}
	return d
}
