package privacy

import (
	"net"
	"regexp"
)

// Detector is the interface for PII / sensitive data detection.
// The default implementation combines a local rule engine with a
// Privacy Filter model. For testing or environments without the model,
// a rule-only detector can be used.
type Detector interface {
	Detect(text string) DetectResult
}

// DefaultDetector combines RuleEngine + PrivacyFilter model detections.
type DefaultDetector struct {
	rules  *RuleEngine
	model  ModelDetector // optional; may be nil if model is not available
}

// NewDefaultDetector creates a detector with the given rule engine and model.
func NewDefaultDetector(rules *RuleEngine, model ModelDetector) *DefaultDetector {
	return &DefaultDetector{rules: rules, model: model}
}

// Detect runs both rule-based and model-based detection and merges the results.
func (d *DefaultDetector) Detect(text string) DetectResult {
	var result DetectResult

	if d.rules != nil {
		result.RuleHits = d.rules.Scan(text).Hits
	}

	if d.model != nil {
		result.Spans = d.model.Detect(text)
	}

	return result
}

// ModelDetector is the interface for the Privacy Filter model.
type ModelDetector interface {
	Detect(text string) []Span
}

// ---------------------------------------------------------------------------
// Text extraction helpers used by both RuleEngine and ModelDetector wrappers.
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

// mergeSpans combines rule hits and model spans into a unified span list,
// deduplicating overlapping segments (longer span wins).
func mergeSpans(modelSpans []Span, ruleHits []RuleHit) []Span {
	// Convert rule hits to spans.
	var all []Span
	all = append(all, modelSpans...)
	for _, hit := range ruleHits {
		all = append(all, Span{
			Label:    entityTypeFromRuleType(hit.Type),
			Original: hit.Matched,
			Score:    1.0,
		})
	}

	// Deduplicate by exact original text (simplest and safest).
	seen := make(map[string]Span)
	for _, s := range all {
		if existing, ok := seen[s.Original]; !ok || len(s.Original) > len(existing.Original) {
			seen[s.Original] = s
		}
	}

	var out []Span
	for _, s := range seen {
		out = append(out, s)
	}
	return out
}

func entityTypeFromRuleType(ruleType string) EntityType {
	switch ruleType {
	case "ip":
		return EntityIP
	case "domain":
		return EntityDomain
	case "keyword", "regex":
		return EntitySecret // fallback
	default:
		return EntitySecret
	}
}
