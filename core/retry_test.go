package core

import (
	"errors"
	"testing"
	"time"
)

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		err      string
		expected bool
	}{
		{"timeout", true},
		{"deadline exceeded", true},
		{"connection refused", true},
		{"no such host", true},
		{"temporary error", true},
		{"too many requests", true},
		{"rate limit exceeded", true},
		{"503 service unavailable", true},
		{"502 bad gateway", true},
		{"504 gateway timeout", true},
		{"invalid api key", false},
		{"bad request", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.err, func(t *testing.T) {
			var err error
			if tt.err != "" {
				err = errors.New(tt.err)
			}
			got := IsRetryableError(err)
			if got != tt.expected {
				t.Fatalf("IsRetryableError(%q) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}

func TestIsRetryableError_Nil(t *testing.T) {
	if IsRetryableError(nil) {
		t.Fatal("nil error should not be retryable")
	}
}

func TestContainsIgnoreCase(t *testing.T) {
	if !containsIgnoreCase("Timeout occurred", "timeout") {
		t.Fatal("should match case-insensitively")
	}
	if !containsIgnoreCase("TIMEOUT", "timeout") {
		t.Fatal("should match exact case")
	}
	if containsIgnoreCase("short", "longer substring") {
		t.Fatal("should not match when s is shorter than substr")
	}
}

func TestContainsFold(t *testing.T) {
	if !containsFold("Hello World", "world") {
		t.Fatal("should match case-insensitively")
	}
	if containsFold("hello", "world") {
		t.Fatal("should not match unrelated strings")
	}
}

func TestToLower(t *testing.T) {
	if toLower('A') != 'a' || toLower('Z') != 'z' || toLower('a') != 'a' || toLower('1') != '1' {
		t.Fatal("toLower incorrect")
	}
}

func TestRetryConfig_Validate(t *testing.T) {
	tests := []struct {
		name       string
		cfg        RetryConfig
		wantIssues int
	}{
		{"valid", RetryConfig{MaxRetries: 2, BaseDelay: time.Second, MaxDelay: 5 * time.Second, Multiplier: 2.0}, 0},
		{"negative maxRetries", RetryConfig{MaxRetries: -1}, 1},
		{"negative baseDelay", RetryConfig{BaseDelay: -1 * time.Second}, 1},
		{"negative maxDelay", RetryConfig{MaxDelay: -1 * time.Second}, 1},
		{"baseDelay > maxDelay", RetryConfig{BaseDelay: 10 * time.Second, MaxDelay: 5 * time.Second}, 1},
		{"negative multiplier", RetryConfig{Multiplier: -1}, 1},
		{"multiple issues", RetryConfig{MaxRetries: -1, BaseDelay: -1 * time.Second, Multiplier: -1}, 3},
		{"zero values ok", RetryConfig{}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := tt.cfg.Validate()
			if len(issues) != tt.wantIssues {
				t.Fatalf("expected %d issues, got %d: %v", tt.wantIssues, len(issues), issues)
			}
		})
	}
}
