// Package store provides high-performance key-value storage for privacy
// mappings (original ↔ fake). It abstracts over Pebble, Redis, and SQL backends.
package store

import (
	"context"
	"time"
)

// Entry stores a single reversible original-to-fake mapping.
type Entry struct {
	SessionID string
	Original  string
	Fake      string
	Type      string // EntityType as string to avoid import cycle
	CreatedAt time.Time
}

// MappingStore is the abstraction for privacy mapping persistence.
type MappingStore interface {
	// Set stores a mapping: fake → original and original → fake.
	Set(ctx context.Context, sessionID, fake, original string, typ string) error

	// GetOriginal looks up the original value from a fake value.
	GetOriginal(ctx context.Context, sessionID, fake string) (string, bool, error)

	// GetFake looks up the fake value from an original value.
	GetFake(ctx context.Context, sessionID, original string) (string, bool, error)

	// GetSession returns all mappings for a session.
	GetSession(ctx context.Context, sessionID string) ([]Entry, error)

	// DeleteSession removes all mappings for a session.
	DeleteSession(ctx context.Context, sessionID string) error

	// Cleanup removes mappings created before the given time.
	Cleanup(ctx context.Context, before time.Time) error

	// Close releases resources.
	Close() error
}
