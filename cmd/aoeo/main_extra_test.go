package main

import (
	"context"
	"os"
	"testing"
)

func TestLoadClient_NoProviders(t *testing.T) {
	// Clear any existing provider env vars to ensure no providers are configured.
	for _, env := range os.Environ() {
		for i, c := range env {
			if c == '=' {
				key := env[:i]
				if len(key) >= 14 && key[:14] == "AOEO_PROVIDER_" {
					os.Unsetenv(key)
				}
				break
			}
		}
	}

	_, err := loadClient()
	if err == nil {
		t.Fatal("expected error when no providers configured")
	}
}

func TestCmdPrivacy_Disabled(t *testing.T) {
	// When privacy is disabled, cmdPrivacy should just print status and return.
	os.Setenv("AOEO_PRIVACY_ENABLED", "false")
	os.Setenv("AOEO_PRIVACY_ENDPOINT", "http://localhost:8080")
	os.Setenv("AOEO_PRIVACY_POLICY", "strict")
	os.Setenv("AOEO_PRIVACY_FAILOPEN", "true")
	defer func() {
		os.Unsetenv("AOEO_PRIVACY_ENABLED")
		os.Unsetenv("AOEO_PRIVACY_ENDPOINT")
		os.Unsetenv("AOEO_PRIVACY_POLICY")
		os.Unsetenv("AOEO_PRIVACY_FAILOPEN")
	}()

	// Should not panic when disabled.
	cmdPrivacy(nil)
}

func TestCmdPrivacy_EnabledNoSidecar(t *testing.T) {
	// When enabled but no sidecar is running, it should handle gracefully.
	os.Setenv("AOEO_PRIVACY_ENABLED", "true")
	os.Setenv("AOEO_PRIVACY_ENDPOINT", "http://127.0.0.1:1") // unlikely to be listening
	defer func() {
		os.Unsetenv("AOEO_PRIVACY_ENABLED")
		os.Unsetenv("AOEO_PRIVACY_ENDPOINT")
	}()

	// Initialize signalCtx for cmdPrivacy to use.
	if signalCtx == nil {
		var cancel func()
		signalCtx, cancel = context.WithCancel(context.Background())
		defer cancel()
	}

	// Should not panic even when sidecar is unreachable.
	cmdPrivacy(nil)
}
