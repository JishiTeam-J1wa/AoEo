package core

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name       string
		cfg        ProviderConfig
		wantIssues int
	}{
		{"complete", ProviderConfig{Name: "test", APIKey: "key", Endpoint: "https://api.example.com", Model: "gpt-4"}, 0},
		{"empty", ProviderConfig{}, 4}, // name, apiKey, endpoint, model
		{"missing name", ProviderConfig{APIKey: "key", Endpoint: "https://api.example.com", Model: "gpt-4"}, 1},
		{"missing apiKey", ProviderConfig{Name: "test", Endpoint: "https://api.example.com", Model: "gpt-4"}, 1},
		{"missing endpoint", ProviderConfig{Name: "test", APIKey: "key", Model: "gpt-4"}, 1},
		{"invalid endpoint scheme", ProviderConfig{Name: "test", APIKey: "key", Endpoint: "ftp://example.com", Model: "gpt-4"}, 1},
		{"negative maxConcurrent", ProviderConfig{Name: "test", APIKey: "key", Endpoint: "https://api.example.com", Model: "gpt-4", MaxConcurrent: -1}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := ValidateConfig(tt.cfg)
			if len(issues) != tt.wantIssues {
				t.Fatalf("expected %d issues, got %d: %v", tt.wantIssues, len(issues), issues)
			}
		})
	}
}

func TestConfig_Validate(t *testing.T) {
	cfg := Config{
		Providers: []ProviderConfig{
			{Name: "p1", APIKey: "key", Endpoint: "https://a.com", Model: "m1"},
			{Name: "", APIKey: "", Endpoint: "bad", Model: ""},
		},
	}
	issues := cfg.Validate()
	if len(issues) != 1 {
		t.Fatalf("expected 1 provider with issues, got %d", len(issues))
	}
	if _, ok := issues[""]; !ok {
		t.Fatal("expected unnamed provider in issues map")
	}
}

func TestProviderConfig_MarshalJSON(t *testing.T) {
	cfg := ProviderConfig{
		Name:       "test",
		APIKey:     "super-secret-key",
		Model:      "gpt-4",
		HTTPClient: &http.Client{},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if strings.Contains(string(data), "super-secret-key") {
		t.Fatal("APIKey should be masked in JSON")
	}
	if !strings.Contains(string(data), "***") {
		t.Fatal("expected masked APIKey")
	}
	if !strings.Contains(string(data), `"model":"gpt-4"`) {
		t.Fatal("expected model field in JSON")
	}
	if strings.Contains(string(data), "HTTPClient") {
		t.Fatal("HTTPClient should be excluded from JSON")
	}
}

func TestProviderConfig_HTTPClientRoundTrip(t *testing.T) {
	called := false
	custom := &http.Client{
		Transport: &mockRoundTripper{fn: func(r *http.Request) (*http.Response, error) {
			called = true
			return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
		}},
	}
	cfg := ProviderConfig{HTTPClient: custom}
	if cfg.HTTPClient == nil {
		t.Fatal("HTTPClient should be stored")
	}
	_, _ = cfg.HTTPClient.Get("http://example.com")
	if !called {
		t.Fatal("custom HTTPClient should be used")
	}
}

type mockRoundTripper struct {
	fn func(*http.Request) (*http.Response, error)
}

func (m *mockRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return m.fn(r)
}
