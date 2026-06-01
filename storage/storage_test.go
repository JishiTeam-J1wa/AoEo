package storage

import (
	"context"
	"testing"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

func TestSQLiteStorage_CallHistory(t *testing.T) {
	s, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("new sqlite: %v", err)
	}
	defer s.Close()

	ctx := context.Background()

	// Record calls
	for i := 0; i < 5; i++ {
		if err := s.RecordCall(ctx, core.CallRecord{
			ID:        "call-" + string(rune('0'+i)),
			Provider:  "deepseek",
			Model:     "deepseek-v4-pro",
			LatencyMs: int64(100 + i*10),
			Timestamp: time.Now().Add(-time.Duration(i) * time.Minute),
			Cost:      0.1 * float64(i+1),
			Currency:  "CNY",
			Tags:      []string{"test", "batch"},
		}); err != nil {
			t.Fatalf("record call: %v", err)
		}
	}

	// Get calls
	calls, err := s.GetCalls(ctx, 10)
	if err != nil {
		t.Fatalf("get calls: %v", err)
	}
	if len(calls) != 5 {
		t.Fatalf("expected 5 calls, got %d", len(calls))
	}

	// Get by provider
	calls, err = s.GetCallsByProvider(ctx, "deepseek", 10)
	if err != nil {
		t.Fatalf("get by provider: %v", err)
	}
	if len(calls) != 5 {
		t.Fatalf("expected 5 calls by provider, got %d", len(calls))
	}

	// Stats
	stats, err := s.GetProviderStats(ctx)
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("expected 1 provider stat, got %d", len(stats))
	}
	st := stats["deepseek"]
	if st.TotalCalls != 5 {
		t.Fatalf("expected 5 total calls, got %d", st.TotalCalls)
	}
}

func TestSQLiteStorage_AuditLog(t *testing.T) {
	s, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("new sqlite: %v", err)
	}
	defer s.Close()

	ctx := context.Background()

	if err := s.RecordAudit(ctx, core.AuditEvent{
		Timestamp: time.Now(),
		Stage:     "before_request",
		Type:      "pii_detected",
		Action:    "mask",
		Provider:  "deepseek",
	}); err != nil {
		t.Fatalf("record audit: %v", err)
	}

	audits, err := s.GetAudits(ctx, 10)
	if err != nil {
		t.Fatalf("get audits: %v", err)
	}
	if len(audits) != 1 {
		t.Fatalf("expected 1 audit, got %d", len(audits))
	}
	if audits[0].Stage != "before_request" {
		t.Fatalf("unexpected stage: %s", audits[0].Stage)
	}
}

func TestSQLiteStorage_PrivacyMappings(t *testing.T) {
	s, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("new sqlite: %v", err)
	}
	defer s.Close()

	ctx := context.Background()

	// Create mapping
	m := core.PrivacyMapping{
		SessionID: "sess-1",
		Original:  "192.168.1.1",
		Fake:      "10.0.0.1",
		Type:      "ip",
		CreatedAt: time.Now(),
	}
	if err := s.CreateMapping(ctx, m); err != nil {
		t.Fatalf("create mapping: %v", err)
	}

	// Find fake
	fake, ok, err := s.FindFake(ctx, "sess-1", "192.168.1.1")
	if err != nil {
		t.Fatalf("find fake: %v", err)
	}
	if !ok || fake != "10.0.0.1" {
		t.Fatalf("expected fake 10.0.0.1, got %s (ok=%v)", fake, ok)
	}

	// Find original
	orig, ok, err := s.FindOriginal(ctx, "sess-1", "10.0.0.1")
	if err != nil {
		t.Fatalf("find original: %v", err)
	}
	if !ok || orig != "192.168.1.1" {
		t.Fatalf("expected original 192.168.1.1, got %s (ok=%v)", orig, ok)
	}

	// Get mappings
	mappings, err := s.GetMappings(ctx, "sess-1")
	if err != nil {
		t.Fatalf("get mappings: %v", err)
	}
	if len(mappings) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(mappings))
	}

	// Cleanup
	if err := s.CleanupMappings(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	_, ok, _ = s.FindFake(ctx, "sess-1", "192.168.1.1")
	if ok {
		t.Fatal("expected mapping to be cleaned up")
	}
}

func TestSQLiteStorage_SessionIsolation(t *testing.T) {
	s, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("new sqlite: %v", err)
	}
	defer s.Close()

	ctx := context.Background()

	s.CreateMapping(ctx, core.PrivacyMapping{SessionID: "sess-a", Original: "192.168.1.1", Fake: "10.0.0.1", Type: "ip", CreatedAt: time.Now()})
	s.CreateMapping(ctx, core.PrivacyMapping{SessionID: "sess-b", Original: "192.168.1.1", Fake: "10.0.0.2", Type: "ip", CreatedAt: time.Now()})

	fakeA, _, _ := s.FindFake(ctx, "sess-a", "192.168.1.1")
	fakeB, _, _ := s.FindFake(ctx, "sess-b", "192.168.1.1")

	if fakeA == fakeB {
		t.Fatal("sessions should have independent mappings")
	}
}
