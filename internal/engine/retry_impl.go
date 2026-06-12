// retry_impl.go 实现带指数退避的自动重试机制。
//
// Author: JishiTeam-J1wa
// Created: 2026-05
//
// Changelog:
//   2026-06-12 - 注释体系规范化

package engine

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

// DoRetry 使用指数退避策略重复执行 fn，直到成功、达到最大重试次数或上下文取消。
//
// 退避策略：
//   初始延迟为 baseDelay，每次重试后延迟时间按 multiplier 倍数增长，
//   并叠加 30% 的随机抖动（jitter）以避免多个请求同时重试引发的惊群效应（thundering herd）。
//   延迟时间上限为 maxDelay。
//
// 幂等性要求：
//   fn 应当是幂等的或可安全重试的操作。非幂等操作（如支付扣款）不应使用本函数，
//   否则可能导致重复执行产生副作用。
//
// 上下文取消行为：
//   如果在退避等待期间 ctx 被取消，函数会立即停止等待并返回带 ctx.Err() 的错误，
//   不会再发起后续重试。
//
// Param:
//   - ctx: context.Context - 控制重试生命周期，取消后立即停止
//   - cfg: core.RetryConfig - 重试配置（最大次数、基础延迟、最大延迟、乘数、可重试判断）
//   - fn: func() error - 待执行的操作，返回 nil 表示成功
//
// Return:
//   - nil: fn 执行成功
//   - error: 所有重试耗尽后的最后一次错误（wrapped），或不可重试错误，或上下文取消错误
//
// Edge Cases:
//   - maxRetries <= 0 时仅执行一次，不进行重试
//   - cfg 中的延迟/乘数使用零值时自动填充默认值（500ms / 5s / 2.0）
//   - retryable 未配置时默认使用 core.IsRetryableError 判断
func DoRetry(ctx context.Context, cfg core.RetryConfig, fn func() error) error {
	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		return fn()
	}

	baseDelay := cfg.BaseDelay
	maxDelay := cfg.MaxDelay
	multiplier := cfg.Multiplier
	retryable := cfg.Retryable

	if baseDelay <= 0 {
		baseDelay = 500 * time.Millisecond
	}
	if maxDelay <= 0 {
		maxDelay = 5 * time.Second
	}
	if multiplier <= 1 {
		multiplier = 2.0
	}
	if retryable == nil {
		retryable = core.IsRetryableError
	}

	var lastErr error
	delay := baseDelay

	for attempt := 0; attempt <= maxRetries; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err

		if attempt == maxRetries {
			break
		}
		if !retryable(err) {
			return err
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("retry cancelled: %w", ctx.Err())
		case <-timer.C:
		}

		// 指数退避叠加 30% 随机抖动，避免惊群效应
		jitter := 1.0 + rand.Float64()*0.3
		delay = time.Duration(float64(delay) * multiplier * jitter)
		if delay > maxDelay {
			delay = maxDelay
		}
	}

	return fmt.Errorf("failed after %d retries, last error: %w", maxRetries, lastErr)
}
