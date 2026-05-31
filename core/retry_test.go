package core

import (
	"errors"
	"testing"
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
