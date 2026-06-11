package privacy

import (
	"net"
	"regexp"
)

// Detector is the interface for PII / sensitive data detection.
type Detector interface {
	Detect(text string) DetectResult

	// DetectBatch detects sensitive spans in multiple texts at once.
	// The returned slice has the same length as the input texts.
	DetectBatch(texts []string) []DetectResult
}

// ---------------------------------------------------------------------------
// Text extraction helpers.
// ---------------------------------------------------------------------------

var (
	ipRegex     = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	domainRegex = regexp.MustCompile(`(?:https?://)?(?:[a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}(?:/[^\s]*)?`)
)

// extractIPAddresses finds all IPv4 addresses in text.
func extractIPAddresses(text string) []string {
	matches := ipRegex.FindAllString(text, -1)
	var valid []string
	for _, m := range matches {
		if net.ParseIP(m) != nil {
			valid = append(valid, m)
		}
	}
	return valid
}

// extractDomains finds all domain-like strings in text.
func extractDomains(text string) []string {
	return domainRegex.FindAllString(text, -1)
}


