// Package core 重试策略与错误分类，提供指数退避重试配置和临时性错误判定。
//
// Author: JishiTeam-J1wa
// Created: 2026-05
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package core

import (
	"time"
)

// RetryConfig 控制指数退避重试行为。
type RetryConfig struct {
	MaxRetries int           // 最大重试次数（0 = 禁用）
	BaseDelay  time.Duration // 重试间的初始延迟
	MaxDelay   time.Duration // 重试间的最大延迟
	Multiplier float64       // 指数退避乘数（默认 2.0）
	Retryable  func(error) bool
}

// Validate 校验重试配置是否合法。
//
// Return:
//   - []string: 错误信息切片，空切片表示配置有效
//
// Edge Cases:
//   - BaseDelay 不可大于 MaxDelay（当 MaxDelay > 0 时）
//   - 所有数值字段须 >= 0
func (cfg RetryConfig) Validate() []string {
	var issues []string
	if cfg.MaxRetries < 0 {
		issues = append(issues, "maxRetries must be >= 0")
	}
	if cfg.BaseDelay < 0 {
		issues = append(issues, "baseDelay must be >= 0")
	}
	if cfg.MaxDelay < 0 {
		issues = append(issues, "maxDelay must be >= 0")
	}
	if cfg.MaxDelay > 0 && cfg.BaseDelay > cfg.MaxDelay {
		issues = append(issues, "baseDelay must be <= maxDelay")
	}
	if cfg.Multiplier < 0 {
		issues = append(issues, "multiplier must be >= 0")
	}
	return issues
}

// DefaultRetryConfig 返回一组合理的默认重试配置。
//
// 默认值：最大重试 2 次，基础延迟 500ms，最大延迟 5s，退避乘数 2.0。
//
// Return:
//   - RetryConfig: 预填充的默认重试配置
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries: 2,
		BaseDelay:  500 * time.Millisecond,
		MaxDelay:   5 * time.Second,
		Multiplier: 2.0,
		Retryable:  IsRetryableError,
	}
}

// transientPatterns 定义可重试的错误关键字列表。
// 匹配到任意关键字的错误被视为临时性错误，值得重试。
var transientPatterns = []string{
	"timeout",
	"deadline exceeded",
	"connection refused",
	"no such host",
	"temporary",
	"too many requests",
	"rate limit",
	"503",
	"502",
	"504",
}

// IsRetryableError 判断错误是否为可重试的临时性错误。
//
// 通过匹配错误信息中的关键字（如 timeout、503 等）进行大小写不敏感判断。
//
// Param:
//   - err: error - 待判断的错误，nil 直接返回 false
//
// Return:
//   - bool: true 表示该错误为临时性错误，值得重试
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	for _, pattern := range transientPatterns {
		if containsIgnoreCase(errStr, pattern) {
			return true
		}
	}
	return false
}

func containsIgnoreCase(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || containsFold(s, substr))
}

func containsFold(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			if toLower(s[i+j]) != toLower(substr[j]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func toLower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}
