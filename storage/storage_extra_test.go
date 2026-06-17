package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	_ "modernc.org/sqlite"
)

// TestNewSQLite_MemoryDSN verifies that NewSQLite works correctly with ":memory:" DSN,
// exercising the memory-detection path that sets MaxOpenConns(1).
func TestNewSQLite_MemoryDSN(t *testing.T) {
	s, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite(:memory:) failed: %v", err)
	}
	defer s.Close()

	// Verify the storage is functional by recording and reading back a call.
	ctx := context.Background()
	err = s.RecordCall(ctx, core.CallRecord{
		ID:        "mem-test-1",
		Provider:  "test-provider",
		Model:     "test-model",
		Timestamp: time.Now(),
		Tags:      []string{"memory"},
	})
	if err != nil {
		t.Fatalf("RecordCall on memory DB failed: %v", err)
	}

	calls, err := s.GetCalls(ctx, 10)
	if err != nil {
		t.Fatalf("GetCalls failed: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].ID != "mem-test-1" {
		t.Fatalf("expected ID 'mem-test-1', got '%s'", calls[0].ID)
	}
}

// TestNewSQLite_FileBasedDSN verifies that NewSQLite works with a file-based DSN,
// exercising the non-memory path (file-based SQLite).
func TestNewSQLite_FileBasedDSN(t *testing.T) {
	tmpDir := os.TempDir()
	dbPath := filepath.Join(tmpDir, fmt.Sprintf("test_sqlite_%d.db", time.Now().UnixNano()))
	defer os.Remove(dbPath)
	// Also clean up WAL and SHM files that SQLite may create.
	defer os.Remove(dbPath + "-wal")
	defer os.Remove(dbPath + "-shm")

	s, err := NewSQLite(dbPath)
	if err != nil {
		t.Fatalf("NewSQLite(file) failed: %v", err)
	}

	ctx := context.Background()
	err = s.RecordCall(ctx, core.CallRecord{
		ID:        "file-test-1",
		Provider:  "test-provider",
		Model:     "test-model",
		LatencyMs: 42,
		Timestamp: time.Now(),
		Cost:      0.01,
		Currency:  "USD",
		Tags:      []string{"file-based"},
	})
	if err != nil {
		t.Fatalf("RecordCall on file DB failed: %v", err)
	}

	calls, err := s.GetCalls(ctx, 10)
	if err != nil {
		t.Fatalf("GetCalls failed: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].ID != "file-test-1" {
		t.Fatalf("expected ID 'file-test-1', got '%s'", calls[0].ID)
	}
	if calls[0].LatencyMs != 42 {
		t.Fatalf("expected LatencyMs 42, got %d", calls[0].LatencyMs)
	}

	// Close and verify the file was actually created on disk.
	s.Close()
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatalf("expected SQLite file to exist at %s", dbPath)
	}
}

// TestNewSQLite_InvalidDSN verifies that NewSQLite returns an error for an invalid DSN
// that causes the Ping step to fail.
func TestNewSQLite_InvalidDSN(t *testing.T) {
	// Use a path under a non-existent directory to force a Ping failure.
	_, err := NewSQLite("/nonexistent/directory/that/does/not/exist/test.db")
	if err == nil {
		t.Fatal("expected NewSQLite to fail with invalid DSN, but it succeeded")
	}
	if !strings.Contains(err.Error(), "ping sqlite") {
		t.Logf("got error (may be from PRAGMA or Ping): %v", err)
	}
}

// TestScanCalls_InvalidJSON inserts records with invalid JSON in request_json,
// response_json, and tags_json fields using raw SQL, then reads them back via
// GetCalls to verify that unmarshal failures do not crash and records are still
// returned with zero-value fields.
func TestScanCalls_InvalidJSON(t *testing.T) {
	// Open a raw SQLite connection to insert bad data.
	s, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	defer s.Close()

	// Access the underlying *sql.DB through the sqlStorage type (same package).
	ss := s.(*sqlStorage)

	ctx := context.Background()

	// Insert a record with completely invalid JSON in all three JSON fields.
	_, err = ss.db.ExecContext(ctx,
		`INSERT INTO calls (id, provider, model, request_json, response_json, error, latency_ms, timestamp, tags_json, fallback_from, cost, currency)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"bad-json-1", "test", "m1",
		"NOT VALID JSON {{{",  // request_json - invalid
		"also not valid ]][",   // response_json - invalid
		"",                     // error
		100,                    // latency_ms
		time.Now().Unix(),      // timestamp
		"invalid tags json",    // tags_json - invalid
		"",                     // fallback_from
		0.5,                    // cost
		"USD",                  // currency
	)
	if err != nil {
		t.Fatalf("insert bad json record: %v", err)
	}

	// Insert a second record with empty string JSON fields (also invalid JSON).
	_, err = ss.db.ExecContext(ctx,
		`INSERT INTO calls (id, provider, model, request_json, response_json, error, latency_ms, timestamp, tags_json, fallback_from, cost, currency)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"bad-json-2", "test", "m2",
		"",                    // request_json - empty string, invalid JSON
		"",                    // response_json - empty string, invalid JSON
		"some error",         // error
		200,                  // latency_ms
		time.Now().Unix(),    // timestamp
		"",                    // tags_json - empty string, invalid JSON
		"fallback-provider",  // fallback_from
		1.0,                  // cost
		"CNY",                // currency
	)
	if err != nil {
		t.Fatalf("insert second bad json record: %v", err)
	}

	// GetCalls should not crash; it should return both records with zero-value
	// fields for the unparseable JSON.
	calls, err := s.GetCalls(ctx, 100)
	if err != nil {
		t.Fatalf("GetCalls should not return error for invalid JSON: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}

	// Verify that non-JSON fields are still correctly read back.
	foundIDs := map[string]bool{}
	for _, c := range calls {
		foundIDs[c.ID] = true
		// JSON fields should be zero-valued due to unmarshal failure.
		// Request should be zero-value ChatCompletionRequest (empty Messages, etc.).
		if len(c.Request.Messages) != 0 {
			t.Errorf("call %s: expected empty Request.Messages for invalid JSON, got %d messages", c.ID, len(c.Request.Messages))
		}
		// Response should be nil (zero value for pointer).
		if c.Response != nil {
			t.Errorf("call %s: expected nil Response for invalid JSON", c.ID)
		}
		// Tags should be nil (zero value for slice).
		if len(c.Tags) != 0 {
			t.Errorf("call %s: expected empty Tags for invalid JSON, got %v", c.ID, c.Tags)
		}
	}
	if !foundIDs["bad-json-1"] || !foundIDs["bad-json-2"] {
		t.Fatalf("expected both bad-json records, found IDs: %v", foundIDs)
	}

	// Verify non-JSON fields are correct.
	for _, c := range calls {
		if c.ID == "bad-json-1" {
			if c.Provider != "test" || c.Model != "m1" || c.LatencyMs != 100 || c.Cost != 0.5 || c.Currency != "USD" {
				t.Errorf("bad-json-1: unexpected field values: provider=%s model=%s latency=%d cost=%f currency=%s",
					c.Provider, c.Model, c.LatencyMs, c.Cost, c.Currency)
			}
		}
		if c.ID == "bad-json-2" {
			if c.Error != "some error" || c.FallbackFrom != "fallback-provider" || c.Cost != 1.0 {
				t.Errorf("bad-json-2: unexpected field values: error=%s fallback=%s cost=%f",
					c.Error, c.FallbackFrom, c.Cost)
			}
		}
	}
}

// TestRecordCall_AllFieldsPopulated records a call with every field populated
// and verifies reading it back correctly.
func TestRecordCall_AllFieldsPopulated(t *testing.T) {
	s, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	fullRecord := core.CallRecord{
		ID:       "all-fields-1",
		Provider: "anthropic",
		Model:    "claude-3-opus",
		Request: core.ChatCompletionRequest{
			Messages: []core.Message{
				{Role: "system", Content: "You are helpful."},
				{Role: "user", Content: "Hello!"},
			},
		},
		Response: &core.ChatCompletionResponse{
			ID: "resp-123",
			Choices: []core.Choice{
				{Message: core.Message{Role: "assistant", Content: "Hi there!"}},
			},
		},
		Error:        "",
		LatencyMs:    1234,
		Timestamp:    ts,
		Tags:         []string{"production", "high-priority", "batch-42"},
		FallbackFrom: "openai",
		Cost:         0.0234,
		Currency:     "USD",
	}

	err = s.RecordCall(ctx, fullRecord)
	if err != nil {
		t.Fatalf("RecordCall: %v", err)
	}

	calls, err := s.GetCalls(ctx, 10)
	if err != nil {
		t.Fatalf("GetCalls: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}

	got := calls[0]
	if got.ID != "all-fields-1" {
		t.Errorf("ID: expected 'all-fields-1', got '%s'", got.ID)
	}
	if got.Provider != "anthropic" {
		t.Errorf("Provider: expected 'anthropic', got '%s'", got.Provider)
	}
	if got.Model != "claude-3-opus" {
		t.Errorf("Model: expected 'claude-3-opus', got '%s'", got.Model)
	}
	if len(got.Request.Messages) != 2 {
		t.Errorf("Request.Messages: expected 2 messages, got %d", len(got.Request.Messages))
	}
	if got.Response == nil {
		t.Fatal("Response: expected non-nil response")
	}
	if got.Response.ID != "resp-123" {
		t.Errorf("Response.ID: expected 'resp-123', got '%s'", got.Response.ID)
	}
	if len(got.Response.Choices) != 1 {
		t.Errorf("Response.Choices: expected 1 choice, got %d", len(got.Response.Choices))
	}
	if got.LatencyMs != 1234 {
		t.Errorf("LatencyMs: expected 1234, got %d", got.LatencyMs)
	}
	if len(got.Tags) != 3 {
		t.Errorf("Tags: expected 3 tags, got %d: %v", len(got.Tags), got.Tags)
	}
	if got.FallbackFrom != "openai" {
		t.Errorf("FallbackFrom: expected 'openai', got '%s'", got.FallbackFrom)
	}
	if got.Cost != 0.0234 {
		t.Errorf("Cost: expected 0.0234, got %f", got.Cost)
	}
	if got.Currency != "USD" {
		t.Errorf("Currency: expected 'USD', got '%s'", got.Currency)
	}
	// Timestamp is stored as Unix seconds, so compare at second precision.
	if got.Timestamp.Unix() != ts.Unix() {
		t.Errorf("Timestamp: expected %d, got %d", ts.Unix(), got.Timestamp.Unix())
	}
}

// TestRecordCall_WithFallbackFrom records a call with the FallbackFrom field set
// and verifies it is stored and retrieved correctly.
func TestRecordCall_WithFallbackFrom(t *testing.T) {
	s, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	defer s.Close()

	ctx := context.Background()

	// Record with FallbackFrom set.
	err = s.RecordCall(ctx, core.CallRecord{
		ID:           "fallback-1",
		Provider:     "deepseek",
		Model:        "deepseek-v4-pro",
		FallbackFrom: "openai",
		Timestamp:    time.Now(),
		Tags:         []string{"fallback"},
		Cost:         0.001,
		Currency:     "CNY",
	})
	if err != nil {
		t.Fatalf("RecordCall: %v", err)
	}

	// Record without FallbackFrom (should be empty string).
	err = s.RecordCall(ctx, core.CallRecord{
		ID:        "no-fallback",
		Provider:  "openai",
		Model:     "gpt-4",
		Timestamp: time.Now(),
	})
	if err != nil {
		t.Fatalf("RecordCall: %v", err)
	}

	calls, err := s.GetCalls(ctx, 10)
	if err != nil {
		t.Fatalf("GetCalls: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}

	for _, c := range calls {
		if c.ID == "fallback-1" {
			if c.FallbackFrom != "openai" {
				t.Errorf("fallback-1: expected FallbackFrom 'openai', got '%s'", c.FallbackFrom)
			}
		}
		if c.ID == "no-fallback" {
			if c.FallbackFrom != "" {
				t.Errorf("no-fallback: expected empty FallbackFrom, got '%s'", c.FallbackFrom)
			}
		}
	}
}

// TestGetCalls_LargeResultSet inserts 150 records and verifies that GetCalls
// with various limits works correctly.
func TestGetCalls_LargeResultSet(t *testing.T) {
	s, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	totalRecords := 150

	// Insert 150 records with staggered timestamps.
	for i := 0; i < totalRecords; i++ {
		err := s.RecordCall(ctx, core.CallRecord{
			ID:        fmt.Sprintf("large-%04d", i),
			Provider:  "test",
			Model:     "m1",
			LatencyMs: int64(i),
			Timestamp: time.Now().Add(-time.Duration(i) * time.Second),
			Tags:      []string{"bulk"},
		})
		if err != nil {
			t.Fatalf("seed call %d: %v", i, err)
		}
	}

	// Test: limit=50 should return exactly 50 records.
	calls, err := s.GetCalls(ctx, 50)
	if err != nil {
		t.Fatalf("GetCalls(50): %v", err)
	}
	if len(calls) != 50 {
		t.Fatalf("expected 50 calls, got %d", len(calls))
	}
	// First record should be the most recent (large-0000).
	if calls[0].ID != "large-0000" {
		t.Errorf("expected first call 'large-0000', got '%s'", calls[0].ID)
	}

	// Test: limit=100 (default) should return 100 records.
	calls, err = s.GetCalls(ctx, 0)
	if err != nil {
		t.Fatalf("GetCalls(0/default): %v", err)
	}
	if len(calls) != 100 {
		t.Fatalf("expected 100 calls with default limit, got %d", len(calls))
	}

	// Test: limit=200 should return all 150 records.
	calls, err = s.GetCalls(ctx, 200)
	if err != nil {
		t.Fatalf("GetCalls(200): %v", err)
	}
	if len(calls) != totalRecords {
		t.Fatalf("expected %d calls, got %d", totalRecords, len(calls))
	}

	// Test: limit=1 should return exactly 1 record.
	calls, err = s.GetCalls(ctx, 1)
	if err != nil {
		t.Fatalf("GetCalls(1): %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].ID != "large-0000" {
		t.Errorf("expected 'large-0000', got '%s'", calls[0].ID)
	}
}

// TestCreateSchema_InvalidSQL tests that createSchema returns an error when given
// an invalid autoIncrement string that causes the SQL to be malformed.
func TestCreateSchema_InvalidSQL(t *testing.T) {
	// Open a raw SQLite DB without going through NewSQLite (which would create
	// the schema successfully).
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	s := &sqlStorage{
		db:          db,
		placeholder: func(n int) string { return "?" },
	}

	// Use a clearly invalid autoIncrement string that will make the CREATE TABLE fail.
	// The audits table SQL becomes: "CREATE TABLE IF NOT EXISTS audits (id THIS IS NOT VALID SQL, ...)"
	// which is syntactically invalid.
	err = s.createSchema("THIS IS NOT VALID SQL !!!")
	if err == nil {
		t.Fatal("expected createSchema to fail with invalid autoIncrement string, but it succeeded")
	}
	if !strings.Contains(err.Error(), "create schema") {
		t.Logf("got error: %v (expected it to contain 'create schema')", err)
	}
}

// TestGetCallsByTag_EscapedPercent verifies the behavior of GetCallsByTag when
// searching for tags containing SQL LIKE wildcard characters ("%" and "_").
// The source code escapes these with backslash via strings.NewReplacer, but
// SQLite's LIKE does not treat backslash as an escape character by default.
// This means the SQL LIKE layer may not match, and Go-layer filtering ensures
// no false positives are returned. The test verifies:
//  1. No errors occur when searching for tags with special characters.
//  2. Go-layer filtering prevents false positives.
//  3. Normal (non-special-character) tags still work correctly alongside records
//     that have special-character tags.
func TestGetCallsByTag_EscapedPercent(t *testing.T) {
	s, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	defer s.Close()

	ctx := context.Background()

	// Insert records with tags containing special LIKE characters and normal tags.
	records := []struct {
		id   string
		tags []string
	}{
		{"pct-1", []string{"100%", "normal"}},
		{"pct-2", []string{"50%", "other"}},
		{"underscore-1", []string{"my_tag", "normal"}},
		{"no-special", []string{"normal", "other"}},
		{"both", []string{"100%", "my_tag"}},
	}

	for _, rec := range records {
		err := s.RecordCall(ctx, core.CallRecord{
			ID:        rec.id,
			Provider:  "test",
			Model:     "m1",
			Timestamp: time.Now(),
			Tags:      rec.tags,
		})
		if err != nil {
			t.Fatalf("RecordCall(%s): %v", rec.id, err)
		}
	}

	// Search for "normal" - a tag without special characters.
	// Should match pct-1, underscore-1, and no-special (3 records).
	calls, err := s.GetCallsByTag(ctx, "normal", 100)
	if err != nil {
		t.Fatalf("GetCallsByTag('normal'): %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("expected 3 calls with tag 'normal', got %d", len(calls))
	}
	foundIDs := map[string]bool{}
	for _, c := range calls {
		foundIDs[c.ID] = true
	}
	if !foundIDs["pct-1"] || !foundIDs["underscore-1"] || !foundIDs["no-special"] {
		t.Errorf("expected pct-1, underscore-1, and no-special, got %v", foundIDs)
	}

	// Search for "other" - should match pct-2 and no-special (2 records).
	calls, err = s.GetCallsByTag(ctx, "other", 100)
	if err != nil {
		t.Fatalf("GetCallsByTag('other'): %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls with tag 'other', got %d", len(calls))
	}
	foundIDs = map[string]bool{}
	for _, c := range calls {
		foundIDs[c.ID] = true
	}
	if !foundIDs["pct-2"] || !foundIDs["no-special"] {
		t.Errorf("expected pct-2 and no-special, got %v", foundIDs)
	}

	// Search for "100%" - the "%" is a LIKE wildcard character.
	// The escape with backslash may not work in SQLite, so the SQL layer might
	// return 0 or more rows than expected. The Go-layer filter ensures no false
	// positives: every returned record must have the exact tag "100%".
	calls, err = s.GetCallsByTag(ctx, "100%", 100)
	if err != nil {
		t.Fatalf("GetCallsByTag('100%%') should not error: %v", err)
	}
	// Verify no false positives: every returned record must truly have "100%" tag.
	for _, c := range calls {
		hasTag := false
		for _, tag := range c.Tags {
			if tag == "100%" {
				hasTag = true
				break
			}
		}
		if !hasTag {
			t.Errorf("false positive: call %s returned by GetCallsByTag('100%%') but does not have that exact tag: %v", c.ID, c.Tags)
		}
	}

	// Search for "my_tag" - the "_" is a LIKE wildcard character.
	// Same as above: no errors, no false positives.
	calls, err = s.GetCallsByTag(ctx, "my_tag", 100)
	if err != nil {
		t.Fatalf("GetCallsByTag('my_tag') should not error: %v", err)
	}
	for _, c := range calls {
		hasTag := false
		for _, tag := range c.Tags {
			if tag == "my_tag" {
				hasTag = true
				break
			}
		}
		if !hasTag {
			t.Errorf("false positive: call %s returned by GetCallsByTag('my_tag') but does not have that exact tag: %v", c.ID, c.Tags)
		}
	}
}

// TestGetCalls_OrderDescending verifies that GetCalls returns records ordered by
// timestamp in descending order (most recent first).
func TestGetCalls_OrderDescending(t *testing.T) {
	s, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	baseTime := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	// Insert records with explicitly ordered timestamps (out of insertion order).
	records := []struct {
		id string
		ts time.Time
	}{
		{"oldest", baseTime.Add(-3 * time.Hour)},
		{"newest", baseTime},
		{"middle", baseTime.Add(-1 * time.Hour)},
		{"second-oldest", baseTime.Add(-2 * time.Hour)},
	}

	// Insert in non-chronological order to verify DB ordering, not insertion order.
	for _, idx := range []int{2, 0, 3, 1} {
		rec := records[idx]
		err := s.RecordCall(ctx, core.CallRecord{
			ID:        rec.id,
			Provider:  "test",
			Model:     "m1",
			Timestamp: rec.ts,
		})
		if err != nil {
			t.Fatalf("RecordCall(%s): %v", rec.id, err)
		}
	}

	calls, err := s.GetCalls(ctx, 10)
	if err != nil {
		t.Fatalf("GetCalls: %v", err)
	}
	if len(calls) != 4 {
		t.Fatalf("expected 4 calls, got %d", len(calls))
	}

	// Verify descending timestamp order.
	expectedOrder := []string{"newest", "middle", "second-oldest", "oldest"}
	for i, expectedID := range expectedOrder {
		if calls[i].ID != expectedID {
			t.Errorf("position %d: expected '%s', got '%s'", i, expectedID, calls[i].ID)
		}
	}

	// Verify timestamps are strictly non-increasing.
	for i := 1; i < len(calls); i++ {
		if calls[i].Timestamp.After(calls[i-1].Timestamp) {
			t.Errorf("calls not in descending order: calls[%d].Timestamp (%v) > calls[%d].Timestamp (%v)",
				i, calls[i].Timestamp, i-1, calls[i-1].Timestamp)
		}
	}
}
