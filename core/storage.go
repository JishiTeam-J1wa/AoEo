// Package core defines the Storage interface for AoEo's persistent layer.
// Implementations include SQLite, MySQL, and PostgreSQL backends.
package core

import (
	"context"
	"time"
)

// Storage is the persistent storage abstraction for AoEo.
// It handles call history, cost statistics, audit logs, and privacy mappings.
type Storage interface {
	// ------------------------------------------------------------------
	// Call History
	// ------------------------------------------------------------------

	// RecordCall persists a single call record.
	RecordCall(ctx context.Context, r CallRecord) error

	// GetCalls returns the most recent call records up to limit.
	GetCalls(ctx context.Context, limit int) ([]CallRecord, error)

	// GetCallsByTag returns call records filtered by tag.
	GetCallsByTag(ctx context.Context, tag string, limit int) ([]CallRecord, error)

	// GetCallsByProvider returns call records filtered by provider name.
	GetCallsByProvider(ctx context.Context, provider string, limit int) ([]CallRecord, error)

	// GetProviderStats aggregates cost statistics per provider.
	GetProviderStats(ctx context.Context) (map[string]ProviderStats, error)

	// ------------------------------------------------------------------
	// Audit Log
	// ------------------------------------------------------------------

	// RecordAudit persists an audit event.
	RecordAudit(ctx context.Context, e AuditEvent) error

	// GetAudits returns the most recent audit events up to limit.
	GetAudits(ctx context.Context, limit int) ([]AuditEvent, error)

	// ------------------------------------------------------------------
	// Privacy Mappings
	// ------------------------------------------------------------------

	// CreateMapping stores an original-to-fake mapping.
	CreateMapping(ctx context.Context, m PrivacyMapping) error

	// FindFake looks up the fake value for an original within a session.
	FindFake(ctx context.Context, sessionID, original string) (string, bool, error)

	// FindOriginal looks up the original value for a fake within a session.
	FindOriginal(ctx context.Context, sessionID, fake string) (string, bool, error)

	// GetMappings returns all mappings for a session, longest first.
	GetMappings(ctx context.Context, sessionID string) ([]PrivacyMapping, error)

	// DeleteMappingsBySession removes all mappings for a session.
	DeleteMappingsBySession(ctx context.Context, sessionID string) error

	// CleanupMappings removes mappings older than the given time.
	CleanupMappings(ctx context.Context, before time.Time) error

	// ------------------------------------------------------------------
	// Lifecycle
	// ------------------------------------------------------------------

	// Close releases any resources held by the storage backend.
	Close() error
}

// CallRecord represents a single AI provider call with full metadata.
type CallRecord struct {
	ID           string                `json:"id"`
	Provider     string                `json:"provider"`
	Model        string                `json:"model"`
	Request      ChatCompletionRequest `json:"request"`
	Response     *ChatCompletionResponse `json:"response,omitempty"`
	Error        string                `json:"error,omitempty"`
	LatencyMs    int64                 `json:"latency_ms"`
	Timestamp    time.Time             `json:"timestamp"`
	Tags         []string              `json:"tags,omitempty"`
	FallbackFrom string                `json:"fallback_from,omitempty"`
	Cost         float64               `json:"cost"`
	Currency     string                `json:"currency"`
}

// ProviderStats aggregates statistics for a single provider.
type ProviderStats struct {
	Provider    string  `json:"provider"`
	TotalCalls  int     `json:"total_calls"`
	TotalCost   float64 `json:"total_cost"`
	Currency    string  `json:"currency"`
	AvgLatency  int64   `json:"avg_latency_ms"`
	ErrorCount  int     `json:"error_count"`
}

// AuditEvent represents a privacy or security audit entry.
type AuditEvent struct {
	ID             string    `json:"id"`
	Timestamp      time.Time `json:"timestamp"`
	Stage          string    `json:"stage"`     // "before_request" | "after_stream"
	Type           string    `json:"type"`      // "rule_hit" | "pii_detected"
	HitsJSON       string    `json:"hits_json,omitempty"`
	SpansJSON      string    `json:"spans_json,omitempty"`
	Action         string    `json:"action"`
	Provider       string    `json:"provider"`
	Model          string    `json:"model"`
	ContentHash    string    `json:"content_hash"`
	ContentPreview string    `json:"content_preview"`
}

// PrivacyMapping stores the reversible original-to-fake relationship.
type PrivacyMapping struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"session_id"`
	Original  string    `json:"original"`
	Fake      string    `json:"fake"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
}
