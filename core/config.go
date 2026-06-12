// Package core 配置结构定义与验证，包含 Provider 配置和全局配置的校验逻辑。
//
// Author: JishiTeam-J1wa
// Created: 2026-05
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ProviderConfig 保存单个 AI Provider 的配置信息。
type ProviderConfig struct {
	Name             string        `json:"name"`
	APIKey           string        `json:"apiKey"`
	Endpoint         string        `json:"endpoint"`
	Model            string        `json:"model"`
	MaxConcurrent    int           `json:"maxConcurrent"`
	SkipTLSVerify    bool          `json:"skipTLSVerify"`
	Pricing          Pricing       `json:"pricing"`          // 可选，缺省回退到 DefaultPricing
	MaxFailures      int           `json:"maxFailures"`      // 熔断器阈值（默认 3）
	CooldownDuration time.Duration `json:"cooldownDuration"` // 熔断器冷却时间（默认 60s）
	HTTPClient       *http.Client  `json:"-"`                // 可选自定义 HTTP 客户端（追踪、代理、Mock）
	Proxy            string        `json:"proxy,omitempty"`  // HTTP/SOCKS5 代理 URL（如 http://proxy:8080）
}

// Config 保存完整的全局配置，包括所有 Provider 列表和审计模式开关。
type Config struct {
	Providers    []ProviderConfig `json:"providers"`
	AuditEnabled bool             `json:"auditEnabled"`
}

// ValidateConfig 校验 ProviderConfig 中的常见配置错误。
//
// Param:
//   - cfg: ProviderConfig - 待校验的 Provider 配置
//
// Return:
//   - []string: 错误信息切片，空切片表示配置有效
//
// Edge Cases:
//   - Endpoint 必须以 http:// 或 https:// 开头
//   - Name、APIKey、Endpoint、Model 均为必填字段
func ValidateConfig(cfg ProviderConfig) []string {
	var issues []string

	if cfg.Name == "" {
		issues = append(issues, "name is required")
	}
	if cfg.APIKey == "" {
		issues = append(issues, "apiKey is required")
	}
	if cfg.Endpoint == "" {
		issues = append(issues, "endpoint is required")
	} else {
		if !strings.HasPrefix(cfg.Endpoint, "http://") && !strings.HasPrefix(cfg.Endpoint, "https://") {
			issues = append(issues, "endpoint must start with http:// or https://")
		}
		if _, err := url.Parse(cfg.Endpoint); err != nil {
			issues = append(issues, fmt.Sprintf("endpoint is not a valid URL: %v", err))
		}
	}
	if cfg.Model == "" {
		issues = append(issues, "model is required")
	}
	if cfg.MaxConcurrent < 0 {
		issues = append(issues, "maxConcurrent must be >= 0")
	}

	return issues
}

// MarshalJSON 对敏感字段（APIKey）做脱敏处理，序列化时不泄露凭据。
//
// Return:
//   - []byte: 脱敏后的 JSON 字节切片
//   - error: 序列化失败时返回
func (cfg ProviderConfig) MarshalJSON() ([]byte, error) {
	type Alias ProviderConfig
	return json.Marshal(&struct {
		*Alias
		APIKey string `json:"apiKey"`
	}{
		Alias:  (*Alias)(&cfg),
		APIKey: "***",
	})
}

// Validate 校验 Config 中所有 Provider 的配置，按 Provider 名称聚合错误信息。
//
// Return:
//   - map[string][]string: Provider 名称到错误列表的映射，空 map 表示全部有效
func (cfg Config) Validate() map[string][]string {
	result := make(map[string][]string)
	for _, pc := range cfg.Providers {
		if issues := ValidateConfig(pc); len(issues) > 0 {
			result[pc.Name] = issues
		}
	}
	return result
}
