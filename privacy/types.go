// Package privacy provides a pseudonymization gateway for AoEo that ensures
// sensitive information (PII, internal IPs, domains, etc.) never reaches
// external AI APIs in plaintext. The gateway intercepts requests, replaces
// sensitive values with realistic fake equivalents, and restores the original
// values in the AI response before returning them to the user.
package privacy

import (
	"fmt"
	"time"
)

// EntityType classifies the kind of sensitive data detected.
type EntityType string

const (
	EntityIP      EntityType = "ip"
	EntityDomain  EntityType = "domain"
	EntityPerson  EntityType = "person"
	EntityPhone   EntityType = "phone"
	EntityIDCard  EntityType = "idcard"
	EntitySecret  EntityType = "secret"
	EntityAddress EntityType = "address"
	EntityEmail   EntityType = "email"
	EntityURL     EntityType = "url"
	EntityDate    EntityType = "date"
)

// Severity indicates how critical a detection is.
type Severity int

const (
	SeverityLow Severity = iota
	SeverityMedium
	SeverityHigh
	SeverityCritical
)

func (s Severity) String() string {
	switch s {
	case SeverityLow:
		return "low"
	case SeverityMedium:
		return "medium"
	case SeverityHigh:
		return "high"
	case SeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// Action defines how a detected sensitive value is handled.
type Action int

const (
	ActionBlock Action = iota
	ActionMask
	ActionPseudonymize
	ActionAudit
)

func (a Action) String() string {
	switch a {
	case ActionBlock:
		return "block"
	case ActionMask:
		return "mask"
	case ActionPseudonymize:
		return "pseudonymize"
	case ActionAudit:
		return "audit"
	default:
		return "unknown"
	}
}

// Span represents a sensitive segment detected by the Privacy Filter model.
type Span struct {
	Start    int
	End      int
	Label    EntityType
	Score    float64
	Original string
}

// MappingEntry stores the reversible original-to-fake mapping.
type MappingEntry struct {
	ID        int64
	SessionID string
	Original  string
	Fake      string
	Type      EntityType
	CreatedAt time.Time
}

// DetectResult aggregates detections from the AI privacy filter model.
type DetectResult struct {
	Spans []Span
}

// PrivacyViolationError is returned when sensitive data is detected and the
// configured policy is to block the request.
type PrivacyViolationError struct {
	Layer   string // "privacy_filter"
	Spans   []Span // model detections (if any)
	Message string
}

func (e *PrivacyViolationError) Error() string {
	return fmt.Sprintf("privacy violation: %s", e.Message)
}
