package engine

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

// doRetry executes fn with exponential backoff retries.
func DoRetry(ctx context.Context, cfg core.RetryConfig, fn func() error) error {
	if cfg.MaxRetries <= 0 {
		return fn()
	}
	// Guard against negative MaxRetries.
	maxRetries := cfg.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
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

		// Exponential backoff with 30% jitter to avoid thundering herd.
		jitter := 1.0 + rand.Float64()*0.3
		delay = time.Duration(float64(delay) * multiplier * jitter)
		if delay > maxDelay {
			delay = maxDelay
		}
	}

	return fmt.Errorf("failed after %d retries, last error: %w", maxRetries, lastErr)
}
