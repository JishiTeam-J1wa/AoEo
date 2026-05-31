package core

import (
	"time"
)

// RetryConfig controls the exponential backoff retry behavior.
type RetryConfig struct {
	MaxRetries int           // Maximum number of retries (0 = disabled)
	BaseDelay  time.Duration // Initial delay between retries
	MaxDelay   time.Duration // Maximum delay between retries
	Multiplier float64       // Exponential multiplier (default 2.0)
	Retryable  func(error) bool
}

// Validate checks the retry configuration for invalid values.
// Returns a slice of error messages; empty slice means valid.
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
