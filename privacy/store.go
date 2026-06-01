package privacy

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// MappingStore persists original-to-fake mappings in a local SQLite database.
// It is safe for concurrent use.
type MappingStore struct {
	db *sql.DB
	mu sync.RWMutex
}

// OpenMappingStore opens (or creates) a SQLite database at the given path.
// If path is ":memory:", an in-memory database is used (useful for tests).
func OpenMappingStore(path string) (*MappingStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open mapping store: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping mapping store: %w", err)
	}
	store := &MappingStore{db: db}
	if err := store.migrate(); err != nil {
		return nil, fmt.Errorf("migrate mapping store: %w", err)
	}
	return store, nil
}

// Close closes the underlying database.
func (s *MappingStore) Close() error {
	return s.db.Close()
}

func (s *MappingStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS mappings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			original TEXT NOT NULL,
			fake TEXT NOT NULL,
			type TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_session_original ON mappings(session_id, original);
		CREATE INDEX IF NOT EXISTS idx_session_fake ON mappings(session_id, fake);
	`)
	return err
}

// FindFake looks up the fake value for a given original within a session.
func (s *MappingStore) FindFake(sessionID, original string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var fake string
	err := s.db.QueryRow(
		"SELECT fake FROM mappings WHERE session_id = ? AND original = ?",
		sessionID, original,
	).Scan(&fake)
	if err != nil {
		return "", false
	}
	return fake, true
}

// FindOriginal looks up the original value for a given fake within a session.
// This is used during response restoration.
func (s *MappingStore) FindOriginal(sessionID, fake string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var original string
	err := s.db.QueryRow(
		"SELECT original FROM mappings WHERE session_id = ? AND fake = ?",
		sessionID, fake,
	).Scan(&original)
	if err != nil {
		return "", false
	}
	return original, true
}

// ExistsFake reports whether a fake value already exists in a session.
func (s *MappingStore) ExistsFake(sessionID, fake string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM mappings WHERE session_id = ? AND fake = ?",
		sessionID, fake,
	).Scan(&count)
	return err == nil && count > 0
}

// Create inserts a new mapping entry.
func (s *MappingStore) Create(sessionID, original, fake string, typ EntityType) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(
		"INSERT INTO mappings (session_id, original, fake, type, created_at) VALUES (?, ?, ?, ?, ?)",
		sessionID, original, fake, string(typ), time.Now().Unix(),
	)
	return err
}

// GetSessionMappings returns all mappings for a session, ordered by length
// descending (longest first). This ordering is important for restoration to
// avoid partial replacements.
func (s *MappingStore) GetSessionMappings(sessionID string) ([]MappingEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		"SELECT id, session_id, original, fake, type, created_at FROM mappings WHERE session_id = ? ORDER BY LENGTH(fake) DESC",
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []MappingEntry
	for rows.Next() {
		var e MappingEntry
		var createdUnix int64
		var typeStr string
		if err := rows.Scan(&e.ID, &e.SessionID, &e.Original, &e.Fake, &typeStr, &createdUnix); err != nil {
			return nil, err
		}
		e.Type = EntityType(typeStr)
		e.CreatedAt = time.Unix(createdUnix, 0)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// Cleanup removes mappings older than the given time.
func (s *MappingStore) Cleanup(before time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(
		"DELETE FROM mappings WHERE created_at < ?",
		before.Unix(),
	)
	return err
}
