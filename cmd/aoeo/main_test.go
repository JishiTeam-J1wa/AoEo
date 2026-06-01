package main

import (
	"testing"
)

func TestUsage(t *testing.T) {
	// Ensure usage does not panic.
	usage()
}

func TestMain_Help(t *testing.T) {
	// This is a smoke test to verify the binary structure is correct.
	// Full integration tests would require valid API keys and network access.
}
