package aoeo

import (
	"context"
	"fmt"
	"time"
)

// RetryConfig controls the exponential backoff retry behavior.
type RetryConfig struct {
	MaxRetries  int           // Maximum number of retries (0 = disabled)
	BaseDelay   time.Duration // Initial delay between retries
	MaxDelay    time.Duration // Maximum delay between retries
	Multiplier  float64       // Exponential multiplier (default 2.0)
	Retryable   func(error) bool
}

// DefaultRetryConfig returns a sensible default retry configuration.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries: 2,
		BaseDelay:  500 * time.Millisecond,
		MaxDelay:   5 * time.Second,
		Multiplier: 2.0,
		Retryable:  IsRetryableError,
	}
}

// IsRetryableError returns true for errors that are typically transient.
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	// Common transient error patterns.
	transient := []string{
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
	for _, pattern := range transient {
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

// doRetry executes fn with exponential backoff retries.
func doRetry(ctx context.Context, cfg RetryConfig, fn func() error) error {
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
		retryable = IsRetryableError
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
