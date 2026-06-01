// Package storage provides SQL-backed implementations of core.Storage.
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

// sqlStorage is the common SQL implementation shared by SQLite, MySQL and Postgres.
type sqlStorage struct {
	db          *sql.DB
	placeholder func(n int) string
}

// ---------------------------------------------------------------------------
// Schema creation (dialect-specific via callbacks)
// ---------------------------------------------------------------------------

func (s *sqlStorage) createSchema(autoIncrement string) error {
	callsSQL := `
		CREATE TABLE IF NOT EXISTS calls (
			id TEXT PRIMARY KEY,
			provider TEXT,
			model TEXT,
			request_json TEXT,
			response_json TEXT,
			error TEXT,
			latency_ms INTEGER,
			timestamp INTEGER,
			tags_json TEXT,
			fallback_from TEXT,
			cost REAL,
			currency TEXT
		);`

	auditsSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS audits (
			id %s,
			timestamp INTEGER,
			stage TEXT,
			type TEXT,
			hits_json TEXT,
			spans_json TEXT,
			action TEXT,
			provider TEXT,
			model TEXT,
			content_hash TEXT,
			content_preview TEXT
		);`, autoIncrement)

	mappingsSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS privacy_mappings (
			id %s,
			session_id TEXT NOT NULL,
			original TEXT NOT NULL,
			fake TEXT NOT NULL,
			type TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);`, autoIncrement)

	for _, stmt := range []string{callsSQL, auditsSQL, mappingsSQL} {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("create schema: %w", err)
		}
	}

	// Create indexes.
	idx := []string{
		`CREATE INDEX IF NOT EXISTS idx_calls_provider ON calls(provider);`,
		`CREATE INDEX IF NOT EXISTS idx_calls_timestamp ON calls(timestamp);`,
		`CREATE INDEX IF NOT EXISTS idx_audits_timestamp ON audits(timestamp);`,
		`CREATE INDEX IF NOT EXISTS idx_mappings_session ON privacy_mappings(session_id);`,
		`CREATE INDEX IF NOT EXISTS idx_mappings_session_original ON privacy_mappings(session_id, original);`,
		`CREATE INDEX IF NOT EXISTS idx_mappings_session_fake ON privacy_mappings(session_id, fake);`,
	}
	for _, stmt := range idx {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Call History
// ---------------------------------------------------------------------------

func (s *sqlStorage) RecordCall(ctx context.Context, r core.CallRecord) error {
	reqJSON, _ := json.Marshal(r.Request)
	respJSON, _ := json.Marshal(r.Response)
	tagsJSON, _ := json.Marshal(r.Tags)

	_, err := s.db.ExecContext(ctx,
		"INSERT INTO calls (id, provider, model, request_json, response_json, error, latency_ms, timestamp, tags_json, fallback_from, cost, currency) VALUES ("+
			s.placeholders(12)+
			")",
		r.ID, r.Provider, r.Model, string(reqJSON), string(respJSON), r.Error, r.LatencyMs, r.Timestamp.Unix(), string(tagsJSON), r.FallbackFrom, r.Cost, r.Currency,
	)
	return err
}

func (s *sqlStorage) GetCalls(ctx context.Context, limit int) ([]core.CallRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, provider, model, request_json, response_json, error, latency_ms, timestamp, tags_json, fallback_from, cost, currency FROM calls ORDER BY timestamp DESC LIMIT "+s.ph(1),
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanCalls(rows)
}

func (s *sqlStorage) GetCallsByTag(ctx context.Context, tag string, limit int) ([]core.CallRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	// SQLite/MySQL use LIKE with JSON string match; Postgres could use JSON operators.
	// For simplicity, we search tags_json as text.
	pattern := "%" + tag + "%"
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, provider, model, request_json, response_json, error, latency_ms, timestamp, tags_json, fallback_from, cost, currency FROM calls WHERE tags_json LIKE "+s.ph(1)+" ORDER BY timestamp DESC LIMIT "+s.ph(2),
		pattern, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanCalls(rows)
}

func (s *sqlStorage) GetCallsByProvider(ctx context.Context, provider string, limit int) ([]core.CallRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, provider, model, request_json, response_json, error, latency_ms, timestamp, tags_json, fallback_from, cost, currency FROM calls WHERE provider = "+s.ph(1)+" ORDER BY timestamp DESC LIMIT "+s.ph(2),
		provider, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanCalls(rows)
}

func (s *sqlStorage) GetProviderStats(ctx context.Context) (map[string]core.ProviderStats, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT provider, COUNT(*), SUM(cost), currency, AVG(latency_ms), SUM(CASE WHEN error != '' THEN 1 ELSE 0 END) FROM calls GROUP BY provider, currency",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := make(map[string]core.ProviderStats)
	for rows.Next() {
		var p core.ProviderStats
		var currency sql.NullString
		var avgLatency sql.NullFloat64
		if err := rows.Scan(&p.Provider, &p.TotalCalls, &p.TotalCost, &currency, &avgLatency, &p.ErrorCount); err != nil {
			return nil, err
		}
		p.Currency = currency.String
		p.AvgLatency = int64(avgLatency.Float64)
		stats[p.Provider] = p
	}
	return stats, rows.Err()
}

func (s *sqlStorage) scanCalls(rows *sql.Rows) ([]core.CallRecord, error) {
	var result []core.CallRecord
	for rows.Next() {
		var r core.CallRecord
		var reqJSON, respJSON, tagsJSON string
		var tsUnix int64
		if err := rows.Scan(&r.ID, &r.Provider, &r.Model, &reqJSON, &respJSON, &r.Error, &r.LatencyMs, &tsUnix, &tagsJSON, &r.FallbackFrom, &r.Cost, &r.Currency); err != nil {
			return nil, err
		}
		r.Timestamp = time.Unix(tsUnix, 0)
		json.Unmarshal([]byte(reqJSON), &r.Request)
		json.Unmarshal([]byte(respJSON), &r.Response)
		json.Unmarshal([]byte(tagsJSON), &r.Tags)
		result = append(result, r)
	}
	return result, rows.Err()
}

// ---------------------------------------------------------------------------
// Audit Log
// ---------------------------------------------------------------------------

func (s *sqlStorage) RecordAudit(ctx context.Context, e core.AuditEvent) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO audits (timestamp, stage, type, hits_json, spans_json, action, provider, model, content_hash, content_preview) VALUES ("+s.placeholders(10)+")",
		e.Timestamp.Unix(), e.Stage, e.Type, e.HitsJSON, e.SpansJSON, e.Action, e.Provider, e.Model, e.ContentHash, e.ContentPreview,
	)
	return err
}

func (s *sqlStorage) GetAudits(ctx context.Context, limit int) ([]core.AuditEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, timestamp, stage, type, hits_json, spans_json, action, provider, model, content_hash, content_preview FROM audits ORDER BY timestamp DESC LIMIT "+s.ph(1),
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []core.AuditEvent
	for rows.Next() {
		var e core.AuditEvent
		var tsUnix int64
		if err := rows.Scan(&e.ID, &tsUnix, &e.Stage, &e.Type, &e.HitsJSON, &e.SpansJSON, &e.Action, &e.Provider, &e.Model, &e.ContentHash, &e.ContentPreview); err != nil {
			return nil, err
		}
		e.Timestamp = time.Unix(tsUnix, 0)
		result = append(result, e)
	}
	return result, rows.Err()
}

// ---------------------------------------------------------------------------
// Privacy Mappings
// ---------------------------------------------------------------------------

func (s *sqlStorage) CreateMapping(ctx context.Context, m core.PrivacyMapping) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO privacy_mappings (session_id, original, fake, type, created_at) VALUES ("+s.placeholders(5)+")",
		m.SessionID, m.Original, m.Fake, m.Type, m.CreatedAt.Unix(),
	)
	return err
}

func (s *sqlStorage) FindFake(ctx context.Context, sessionID, original string) (string, bool, error) {
	var fake string
	err := s.db.QueryRowContext(ctx,
		"SELECT fake FROM privacy_mappings WHERE session_id = "+s.ph(1)+" AND original = "+s.ph(2),
		sessionID, original,
	).Scan(&fake)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return fake, true, nil
}

func (s *sqlStorage) FindOriginal(ctx context.Context, sessionID, fake string) (string, bool, error) {
	var original string
	err := s.db.QueryRowContext(ctx,
		"SELECT original FROM privacy_mappings WHERE session_id = "+s.ph(1)+" AND fake = "+s.ph(2),
		sessionID, fake,
	).Scan(&original)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return original, true, nil
}

func (s *sqlStorage) GetMappings(ctx context.Context, sessionID string) ([]core.PrivacyMapping, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, session_id, original, fake, type, created_at FROM privacy_mappings WHERE session_id = "+s.ph(1)+" ORDER BY LENGTH(fake) DESC",
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []core.PrivacyMapping
	for rows.Next() {
		var m core.PrivacyMapping
		var tsUnix int64
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Original, &m.Fake, &m.Type, &tsUnix); err != nil {
			return nil, err
		}
		m.CreatedAt = time.Unix(tsUnix, 0)
		result = append(result, m)
	}
	return result, rows.Err()
}

func (s *sqlStorage) DeleteMappingsBySession(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM privacy_mappings WHERE session_id = "+s.ph(1),
		sessionID,
	)
	return err
}

func (s *sqlStorage) CleanupMappings(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM privacy_mappings WHERE created_at < "+s.ph(1),
		before.Unix(),
	)
	return err
}

func (s *sqlStorage) Close() error {
	return s.db.Close()
}

// ph returns the n-th positional placeholder for the configured dialect.
func (s *sqlStorage) ph(n int) string {
	if s.placeholder == nil {
		return "?"
	}
	return s.placeholder(n)
}

// placeholders returns a comma-separated list of n placeholders for the configured dialect.
func (s *sqlStorage) placeholders(n int) string {
	if s.placeholder == nil {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = "?"
		}
		return strings.Join(parts, ", ")
	}
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		parts[i] = s.placeholder(i + 1)
	}
	return strings.Join(parts, ", ")
}
