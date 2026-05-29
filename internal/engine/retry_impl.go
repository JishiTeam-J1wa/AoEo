package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

// doRetry executes fn with exponential backoff retries.
func DoRetry(ctx context.Context, cfg core.RetryConfig, fn func() error) error {
	if cfg.MaxRetries <= 0 {
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

	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err

		if attempt == cfg.MaxRetries {
			break
		}
		if !retryable(err) {
			return err
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("retry cancelled: %w", ctx.Err())
		case <-time.After(delay):
		}

		delay = time.Duration(float64(delay) * multiplier)
		if delay > maxDelay {
			delay = maxDelay
		}
	}

	return fmt.Errorf("failed after %d retries, last error: %w", cfg.MaxRetries, lastErr)
}
