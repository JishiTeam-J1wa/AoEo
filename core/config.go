package core

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ProviderConfig holds the configuration for a single AI provider.
type ProviderConfig struct {
	Name             string        `json:"name"`
	APIKey           string        `json:"apiKey"`
	Endpoint         string        `json:"endpoint"`
	Model            string        `json:"model"`
	MaxConcurrent    int           `json:"maxConcurrent"`
	SkipTLSVerify    bool          `json:"skipTLSVerify"`
	Pricing          Pricing       `json:"pricing"`          // Optional; falls back to DefaultPricing
	MaxFailures      int           `json:"maxFailures"`      // Circuit breaker threshold (default 3)
	CooldownDuration time.Duration `json:"cooldownDuration"` // Circuit breaker cooldown (default 60s)
}

// Config holds the full configuration including all providers and mode.
type Config struct {
	Providers    []ProviderConfig `json:"providers"`
	AuditEnabled bool             `json:"auditEnabled"`
}

// ValidateConfig checks a ProviderConfig for common misconfigurations.
// Returns a slice of error messages; empty slice means valid.
func ValidateConfig(cfg ProviderConfig) []string {
	var issues []string

	if cfg.Name == "" {
		issues = append(issues, "name is required")
	}
	if cfg.APIKey == "" {
		issues = append(issues, "apiKey is required")
	}
	if cfg.Endpoint == "" {
		issues = append(issues, "endpoint is required")
	} else {
		if !strings.HasPrefix(cfg.Endpoint, "http://") && !strings.HasPrefix(cfg.Endpoint, "https://") {
			issues = append(issues, "endpoint must start with http:// or https://")
		}
		if _, err := url.Parse(cfg.Endpoint); err != nil {
			issues = append(issues, fmt.Sprintf("endpoint is not a valid URL: %v", err))
		}
	}
	if cfg.Model == "" {
		issues = append(issues, "model is required")
	}
	if cfg.MaxConcurrent < 0 {
		issues = append(issues, "maxConcurrent must be >= 0")
	}

	return issues
}

// MarshalJSON masks sensitive fields (APIKey) so that serializing a Config does not leak credentials.
func (cfg ProviderConfig) MarshalJSON() ([]byte, error) {
	type Alias ProviderConfig
	return json.Marshal(&struct {
		*Alias
		APIKey string `json:"apiKey"`
	}{
		Alias:  (*Alias)(&cfg),
		APIKey: "***",
	})
}

// Validate checks all providers in a Config and returns a map of provider name -> issues.
func (cfg Config) Validate() map[string][]string {
	result := make(map[string][]string)
	for _, pc := range cfg.Providers {
		if issues := ValidateConfig(pc); len(issues) > 0 {
			result[pc.Name] = issues
		}
	}
	return result
}
