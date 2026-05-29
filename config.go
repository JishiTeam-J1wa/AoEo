package aoeo

import (
	"fmt"
	"net/url"
	"strings"
)

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
