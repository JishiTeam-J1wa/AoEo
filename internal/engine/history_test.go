package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

func TestHistory_RecordAndRetrieve(t *testing.T) {
	h := NewHistory(10)
	h.Record(CallRecord{Provider: "p1", Model: "m1", LatencyMs: 100, Cost: 1.0, Currency: "CNY"})
	h.Record(CallRecord{Provider: "p2", Model: "m2", LatencyMs: 200, Cost: 2.0, Currency: "USD"})

	records := h.Records()
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0].Provider != "p2" {
		t.Fatal("expected newest first")
	}
}

func TestHistory_MaxSize(t *testing.T) {
	h := NewHistory(2)
	h.Record(CallRecord{Provider: "p1"})
	h.Record(CallRecord{Provider: "p2"})
	h.Record(CallRecord{Provider: "p3"})

	records := h.Records()
	if len(records) != 2 {
		t.Fatalf("expected 2 records after trim, got %d", len(records))
	}
	if records[0].Provider != "p3" || records[1].Provider != "p2" {
		t.Fatal("expected p3, p2 after trim")
	}
}

func TestHistory_RecordsByTag(t *testing.T) {
	h := NewHistory(10)
	h.Record(CallRecord{Provider: "p1", Tags: []string{"prod", "v1"}})
	h.Record(CallRecord{Provider: "p2", Tags: []string{"dev"}})
	h.Record(CallRecord{Provider: "p3", Tags: []string{"prod"}})

	prod := h.RecordsByTag("prod")
	if len(prod) != 2 {
		t.Fatalf("expected 2 prod records, got %d", len(prod))
	}

	empty := h.RecordsByTag("nonexistent")
	if len(empty) != 0 {
		t.Fatal("expected empty for nonexistent tag")
	}
}

func TestHistory_RecordsByProvider(t *testing.T) {
	h := NewHistory(10)
	h.Record(CallRecord{Provider: "p1"})
	h.Record(CallRecord{Provider: "p2"})
	h.Record(CallRecord{Provider: "p1"})

	p1 := h.RecordsByProvider("p1")
	if len(p1) != 2 {
		t.Fatalf("expected 2 p1 records, got %d", len(p1))
	}
}

func TestHistory_Clear(t *testing.T) {
	h := NewHistory(10)
	h.Record(CallRecord{Provider: "p1"})
	h.Clear()
	if len(h.Records()) != 0 {
		t.Fatal("expected empty after clear")
	}
}

func TestHistory_Stats(t *testing.T) {
	h := NewHistory(10)
	h.Record(CallRecord{Provider: "p1", LatencyMs: 100, Cost: 1.0, Currency: "CNY"})
	h.Record(CallRecord{Provider: "p1", LatencyMs: 200, Cost: 2.0, Currency: "CNY", Error: "fail"})
	h.Record(CallRecord{Provider: "p2", LatencyMs: 50, Cost: 0.5, Currency: "USD"})

	stats := h.Stats()
	p1 := stats["p1"]
	if p1.TotalCalls != 2 {
		t.Fatalf("expected 2 calls for p1, got %d", p1.TotalCalls)
	}
	if p1.FailedCalls != 1 {
		t.Fatalf("expected 1 failed call for p1, got %d", p1.FailedCalls)
	}
	if p1.TotalCost != 3.0 {
		t.Fatalf("expected total cost 3.0, got %.2f", p1.TotalCost)
	}
	if p1.AvgLatencyMs != 150 {
		t.Fatalf("expected avg latency 150ms, got %d", p1.AvgLatencyMs)
	}
	if p1.MaxLatencyMs != 200 {
		t.Fatalf("expected max latency 200ms, got %d", p1.MaxLatencyMs)
	}
	if p1.Currency != "CNY" {
		t.Fatalf("expected CNY, got %s", p1.Currency)
	}

	p2 := stats["p2"]
	if p2.TotalCalls != 1 {
		t.Fatalf("expected 1 call for p2, got %d", p2.TotalCalls)
	}
	if p2.Currency != "USD" {
		t.Fatalf("expected USD, got %s", p2.Currency)
	}
}

func TestHistory_Stats_Empty(t *testing.T) {
	h := NewHistory(10)
	stats := h.Stats()
	if len(stats) != 0 {
		t.Fatal("expected empty stats for empty history")
	}
}

func TestHistory_ConcurrentAccess(t *testing.T) {
	h := NewHistory(100)
	for i := 0; i < 100; i++ {
		go func(idx int) {
			h.Record(CallRecord{Provider: "p", LatencyMs: int64(idx)})
		}(i)
	}
	// Give goroutines time to finish
	time.Sleep(100 * time.Millisecond)
	if len(h.Records()) != 100 {
		t.Fatalf("expected 100 records, got %d", len(h.Records()))
	}
}

func TestCallRecord_ZeroValue(t *testing.T) {
	var r CallRecord
	if r.Provider != "" || r.LatencyMs != 0 {
		t.Fatal("zero value CallRecord should be empty")
	}
}

func TestHistory_RingBuffer_WrapAround(t *testing.T) {
	h := NewHistory(3)
	h.Record(CallRecord{Provider: "p1"})
	h.Record(CallRecord{Provider: "p2"})
	h.Record(CallRecord{Provider: "p3"})
	h.Record(CallRecord{Provider: "p4"}) // overwrites p1

	records := h.Records()
	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}
	if records[0].Provider != "p4" || records[1].Provider != "p3" || records[2].Provider != "p2" {
		t.Fatalf("expected p4,p3,p2, got %v", []string{records[0].Provider, records[1].Provider, records[2].Provider})
	}
}

func TestHistory_RingBuffer_MaxSizeOne(t *testing.T) {
	h := NewHistory(1)
	h.Record(CallRecord{Provider: "p1"})
	h.Record(CallRecord{Provider: "p2"})

	records := h.Records()
	if len(records) != 1 || records[0].Provider != "p2" {
		t.Fatalf("expected single p2, got %v", records)
	}
}

func TestHistory_RingBuffer_ClearAfterWrap(t *testing.T) {
	h := NewHistory(2)
	h.Record(CallRecord{Provider: "p1"})
	h.Record(CallRecord{Provider: "p2"})
	h.Record(CallRecord{Provider: "p3"})
	h.Clear()

	if len(h.Records()) != 0 {
		t.Fatal("expected empty after clear")
	}
	// Verify we can write again after clear
	h.Record(CallRecord{Provider: "p4"})
	records := h.Records()
	if len(records) != 1 || records[0].Provider != "p4" {
		t.Fatalf("expected p4 after re-write, got %v", records)
	}
}

func TestHistory_RingBuffer_RecordsByTagAfterWrap(t *testing.T) {
	h := NewHistory(2)
	h.Record(CallRecord{Provider: "p1", Tags: []string{"a"}})
	h.Record(CallRecord{Provider: "p2", Tags: []string{"a"}})
	h.Record(CallRecord{Provider: "p3", Tags: []string{"b"}}) // overwrites p1

	 tagged := h.RecordsByTag("a")
	if len(tagged) != 1 || tagged[0].Provider != "p2" {
		t.Fatalf("expected only p2 tagged a, got %v", tagged)
	}
}

func TestHistory_RingBuffer_StatsAfterWrap(t *testing.T) {
	h := NewHistory(2)
	h.Record(CallRecord{Provider: "p1", LatencyMs: 100, Cost: 1.0})
	h.Record(CallRecord{Provider: "p2", LatencyMs: 200, Cost: 2.0})
	h.Record(CallRecord{Provider: "p3", LatencyMs: 300, Cost: 3.0}) // overwrites p1

	stats := h.Stats()
	if _, ok := stats["p1"]; ok {
		t.Fatal("p1 should have been overwritten")
	}
	if stats["p3"].TotalCalls != 1 || stats["p3"].TotalCost != 3.0 {
		t.Fatalf("unexpected p3 stats: %+v", stats["p3"])
	}
}

// ---------------------------------------------------------------------------
// mockStorage implements core.Storage for testing
// ---------------------------------------------------------------------------

type mockStorage struct {
	mu           sync.Mutex
	calls        []core.CallRecord
	getCallsErr  error
	getByTagErr  error
	getByProvErr error
	getStatsErr  error
	recordErr    error
	stats        map[string]core.ProviderStats
}

func (m *mockStorage) RecordCall(_ context.Context, r core.CallRecord) error {
	if m.recordErr != nil {
		return m.recordErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, r)
	return nil
}

func (m *mockStorage) GetCalls(_ context.Context, limit int) ([]core.CallRecord, error) {
	if m.getCallsErr != nil {
		return nil, m.getCallsErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) > limit {
		return m.calls[:limit], nil
	}
	return m.calls, nil
}

func (m *mockStorage) GetCallsByTag(_ context.Context, tag string, limit int) ([]core.CallRecord, error) {
	if m.getByTagErr != nil {
		return nil, m.getByTagErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []core.CallRecord
	for _, c := range m.calls {
		for _, t := range c.Tags {
			if t == tag {
				result = append(result, c)
				break
			}
		}
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (m *mockStorage) GetCallsByProvider(_ context.Context, provider string, limit int) ([]core.CallRecord, error) {
	if m.getByProvErr != nil {
		return nil, m.getByProvErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []core.CallRecord
	for _, c := range m.calls {
		if c.Provider == provider {
			result = append(result, c)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *mockStorage) GetCallsPaged(_ context.Context, offset, limit int) ([]core.CallRecord, error) {
	return nil, nil
}

func (m *mockStorage) GetCallsByTagPaged(_ context.Context, tag string, offset, limit int) ([]core.CallRecord, error) {
	return nil, nil
}

func (m *mockStorage) GetCallsByProviderPaged(_ context.Context, provider string, offset, limit int) ([]core.CallRecord, error) {
	return nil, nil
}

func (m *mockStorage) GetProviderStats(_ context.Context) (map[string]core.ProviderStats, error) {
	if m.getStatsErr != nil {
		return nil, m.getStatsErr
	}
	if m.stats != nil {
		return m.stats, nil
	}
	// Compute from calls
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := make(map[string]core.ProviderStats)
	for _, c := range m.calls {
		st := stats[c.Provider]
		st.Provider = c.Provider
		st.TotalCalls++
		st.TotalCost += c.Cost
		if c.Error != "" {
			st.ErrorCount++
		}
		if st.Currency == "" && c.Currency != "" {
			st.Currency = c.Currency
		}
		stats[c.Provider] = st
	}
	return stats, nil
}

func (m *mockStorage) RecordAudit(_ context.Context, _ core.AuditEvent) error { return nil }
func (m *mockStorage) GetAudits(_ context.Context, _ int) ([]core.AuditEvent, error) { return nil, nil }
func (m *mockStorage) CreateMapping(_ context.Context, _ core.PrivacyMapping) error { return nil }
func (m *mockStorage) FindFake(_ context.Context, _, _ string) (string, bool, error) { return "", false, nil }
func (m *mockStorage) FindOriginal(_ context.Context, _, _ string) (string, bool, error) { return "", false, nil }
func (m *mockStorage) GetMappings(_ context.Context, _ string) ([]core.PrivacyMapping, error) { return nil, nil }
func (m *mockStorage) DeleteMappingsBySession(_ context.Context, _ string) error { return nil }
func (m *mockStorage) CleanupMappings(_ context.Context, _ time.Time) error { return nil }
func (m *mockStorage) Close() error { return nil }

func (m *mockStorage) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

// ---------------------------------------------------------------------------
// History tests with Storage backend
// ---------------------------------------------------------------------------

func TestHistory_Records_WithStorage(t *testing.T) {
	st := &mockStorage{
		calls: []core.CallRecord{
			{ID: "1", Provider: "sp1", Model: "sm1", Cost: 1.0, Currency: "CNY"},
			{ID: "2", Provider: "sp2", Model: "sm2", Cost: 2.0, Currency: "USD"},
		},
	}
	h := NewHistory(10)
	h.SetStorage(st)

	records := h.Records()
	if len(records) != 2 {
		t.Fatalf("expected 2 records from storage, got %d", len(records))
	}
	if records[0].Provider != "sp1" {
		t.Fatalf("expected first record provider sp1, got %s", records[0].Provider)
	}
}

func TestHistory_Records_StorageError_FallbackToMemory(t *testing.T) {
	st := &mockStorage{getCallsErr: errors.New("db error")}
	h := NewHistory(10)
	h.SetStorage(st)

	// Add records to memory buffer
	h.Record(CallRecord{Provider: "mem1"})
	h.Record(CallRecord{Provider: "mem2"})

	records := h.Records()
	if len(records) != 2 {
		t.Fatalf("expected 2 records from memory fallback, got %d", len(records))
	}
	if records[0].Provider != "mem2" {
		t.Fatalf("expected newest first, got %s", records[0].Provider)
	}
}

func TestHistory_RecordsByTag_WithStorage(t *testing.T) {
	st := &mockStorage{
		calls: []core.CallRecord{
			{Provider: "p1", Tags: []string{"prod"}},
			{Provider: "p2", Tags: []string{"dev"}},
			{Provider: "p3", Tags: []string{"prod"}},
		},
	}
	h := NewHistory(10)
	h.SetStorage(st)

	tagged := h.RecordsByTag("prod")
	if len(tagged) != 2 {
		t.Fatalf("expected 2 prod records from storage, got %d", len(tagged))
	}
}

func TestHistory_RecordsByTag_StorageError_FallbackToMemory(t *testing.T) {
	st := &mockStorage{getByTagErr: errors.New("db error")}
	h := NewHistory(10)
	h.SetStorage(st)

	h.Record(CallRecord{Provider: "p1", Tags: []string{"a"}})
	h.Record(CallRecord{Provider: "p2", Tags: []string{"b"}})

	tagged := h.RecordsByTag("a")
	if len(tagged) != 1 || tagged[0].Provider != "p1" {
		t.Fatalf("expected 1 record tagged 'a' from memory, got %v", tagged)
	}
}

func TestHistory_RecordsByProvider_WithStorage(t *testing.T) {
	st := &mockStorage{
		calls: []core.CallRecord{
			{Provider: "p1"},
			{Provider: "p2"},
			{Provider: "p1"},
		},
	}
	h := NewHistory(10)
	h.SetStorage(st)

	p1Records := h.RecordsByProvider("p1")
	if len(p1Records) != 2 {
		t.Fatalf("expected 2 p1 records from storage, got %d", len(p1Records))
	}
}

func TestHistory_RecordsByProvider_StorageError_FallbackToMemory(t *testing.T) {
	st := &mockStorage{getByProvErr: errors.New("db error")}
	h := NewHistory(10)
	h.SetStorage(st)

	h.Record(CallRecord{Provider: "p1"})
	h.Record(CallRecord{Provider: "p2"})
	h.Record(CallRecord{Provider: "p1"})

	p1Records := h.RecordsByProvider("p1")
	if len(p1Records) != 2 {
		t.Fatalf("expected 2 p1 records from memory, got %d", len(p1Records))
	}
}

func TestHistory_Stats_WithStorage(t *testing.T) {
	st := &mockStorage{
		stats: map[string]core.ProviderStats{
			"sp1": {Provider: "sp1", TotalCalls: 10, TotalCost: 5.0, Currency: "CNY", AvgLatency: 100, ErrorCount: 1},
		},
	}
	h := NewHistory(10)
	h.SetStorage(st)

	stats := h.Stats()
	sp1, ok := stats["sp1"]
	if !ok {
		t.Fatal("expected sp1 in stats from storage")
	}
	if sp1.TotalCalls != 10 {
		t.Fatalf("expected 10 total calls, got %d", sp1.TotalCalls)
	}
	if sp1.TotalCost != 5.0 {
		t.Fatalf("expected total cost 5.0, got %.2f", sp1.TotalCost)
	}
	if sp1.Currency != "CNY" {
		t.Fatalf("expected CNY, got %s", sp1.Currency)
	}
	if sp1.AvgLatencyMs != 100 {
		t.Fatalf("expected avg latency 100, got %d", sp1.AvgLatencyMs)
	}
	if sp1.FailedCalls != 1 {
		t.Fatalf("expected 1 failed call, got %d", sp1.FailedCalls)
	}
}

func TestHistory_Stats_StorageError_FallbackToMemory(t *testing.T) {
	st := &mockStorage{getStatsErr: errors.New("db error")}
	h := NewHistory(10)
	h.SetStorage(st)

	h.Record(CallRecord{Provider: "p1", LatencyMs: 100, Cost: 1.0})

	stats := h.Stats()
	p1, ok := stats["p1"]
	if !ok {
		t.Fatal("expected p1 in stats from memory fallback")
	}
	if p1.TotalCalls != 1 {
		t.Fatalf("expected 1 total call, got %d", p1.TotalCalls)
	}
}

func TestHistory_PersistRecord_Async(t *testing.T) {
	st := &mockStorage{}
	h := NewHistory(10)
	h.SetStorage(st)

	h.Record(CallRecord{ID: "r1", Provider: "p1", Cost: 1.0})
	h.Record(CallRecord{ID: "r2", Provider: "p2", Cost: 2.0})

	// Wait for async persistence
	time.Sleep(200 * time.Millisecond)

	if st.callCount() != 2 {
		t.Fatalf("expected 2 persisted calls, got %d", st.callCount())
	}
}

func TestHistory_PersistRecord_Error(t *testing.T) {
	st := &mockStorage{recordErr: errors.New("persist failed")}
	h := NewHistory(10)
	h.SetStorage(st)

	// Should not panic even when persist fails
	h.Record(CallRecord{ID: "r1", Provider: "p1"})

	// Wait for async persistence attempt
	time.Sleep(200 * time.Millisecond)
	// No panic means success
}

func TestHistory_Clear_NilsOutBuffer(t *testing.T) {
	h := NewHistory(5)
	h.Record(CallRecord{Provider: "p1", Model: "m1"})
	h.Record(CallRecord{Provider: "p2", Model: "m2"})

	h.Clear()

	// Verify internal buffer is zeroed
	h.mu.RLock()
	for i, r := range h.buf {
		if r.Provider != "" || r.Model != "" {
			t.Fatalf("buf[%d] not zeroed after clear: %+v", i, r)
		}
	}
	if h.head != 0 {
		t.Fatalf("expected head=0 after clear, got %d", h.head)
	}
	if h.count != 0 {
		t.Fatalf("expected count=0 after clear, got %d", h.count)
	}
	h.mu.RUnlock()
}

func TestHistory_At_IndexCalculation(t *testing.T) {
	h := NewHistory(5)
	for i := 0; i < 5; i++ {
		h.Record(CallRecord{Provider: "p" + string(rune('0'+i))})
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	// at(0) should be the newest (p4), at(4) the oldest (p0)
	if r := h.at(0); r.Provider != "p4" {
		t.Fatalf("at(0) expected p4, got %s", r.Provider)
	}
	if r := h.at(4); r.Provider != "p0" {
		t.Fatalf("at(4) expected p0, got %s", r.Provider)
	}
}

func TestHistory_At_AfterWrap(t *testing.T) {
	h := NewHistory(3)
	h.Record(CallRecord{Provider: "p1"})
	h.Record(CallRecord{Provider: "p2"})
	h.Record(CallRecord{Provider: "p3"})
	h.Record(CallRecord{Provider: "p4"}) // overwrites p1

	h.mu.RLock()
	defer h.mu.RUnlock()

	// at(0) = p4, at(1) = p3, at(2) = p2
	if r := h.at(0); r.Provider != "p4" {
		t.Fatalf("at(0) expected p4, got %s", r.Provider)
	}
	if r := h.at(1); r.Provider != "p3" {
		t.Fatalf("at(1) expected p3, got %s", r.Provider)
	}
	if r := h.at(2); r.Provider != "p2" {
		t.Fatalf("at(2) expected p2, got %s", r.Provider)
	}
}

func TestHistory_FromCoreCalls(t *testing.T) {
	h := NewHistory(10)
	coreCalls := []core.CallRecord{
		{ID: "1", Provider: "p1", Model: "m1", Cost: 1.0, Currency: "CNY"},
		{ID: "2", Provider: "p2", Model: "m2", Cost: 2.0, Currency: "USD"},
	}

	result := h.fromCoreCalls(coreCalls)
	if len(result) != 2 {
		t.Fatalf("expected 2 records, got %d", len(result))
	}
	if result[0].Provider != "p1" || result[0].Cost != 1.0 {
		t.Fatalf("unexpected first record: %+v", result[0])
	}
	if result[1].Currency != "USD" {
		t.Fatalf("expected USD, got %s", result[1].Currency)
	}
}

func TestHistory_FromCoreStats(t *testing.T) {
	h := NewHistory(10)
	coreStats := map[string]core.ProviderStats{
		"p1": {Provider: "p1", TotalCalls: 5, TotalCost: 10.0, Currency: "CNY", AvgLatency: 200, ErrorCount: 1},
	}

	result := h.fromCoreStats(coreStats)
	p1, ok := result["p1"]
	if !ok {
		t.Fatal("expected p1 in converted stats")
	}
	if p1.TotalCalls != 5 {
		t.Fatalf("expected 5 total calls, got %d", p1.TotalCalls)
	}
	if p1.TotalCost != 10.0 {
		t.Fatalf("expected total cost 10.0, got %.2f", p1.TotalCost)
	}
	if p1.AvgLatencyMs != 200 {
		t.Fatalf("expected avg latency 200, got %d", p1.AvgLatencyMs)
	}
	if p1.FailedCalls != 1 {
		t.Fatalf("expected 1 failed call, got %d", p1.FailedCalls)
	}
}

func TestHistory_Close_Idempotent(t *testing.T) {
	h := NewHistory(10)
	h.Close()
	h.Close() // should not panic
}

func TestHistory_SetStorage(t *testing.T) {
	h := NewHistory(10)
	st := &mockStorage{}
	h.SetStorage(st)

	h.mu.RLock()
	s := h.storage
	h.mu.RUnlock()

	if s == nil {
		t.Fatal("expected storage to be set")
	}
}

func TestHistory_NewHistory_DefaultMaxSize(t *testing.T) {
	h := NewHistory(0)
	if h.maxSize != 100 {
		t.Fatalf("expected default maxSize 100, got %d", h.maxSize)
	}

	h2 := NewHistory(-5)
	if h2.maxSize != 100 {
		t.Fatalf("expected default maxSize 100, got %d", h2.maxSize)
	}
}

func TestHistory_RecordsByTag_NoMatch(t *testing.T) {
	h := NewHistory(10)
	h.Record(CallRecord{Provider: "p1", Tags: []string{"a"}})
	h.Record(CallRecord{Provider: "p2", Tags: []string{"b"}})

	result := h.RecordsByTag("nonexistent")
	if len(result) != 0 {
		t.Fatalf("expected empty result for nonexistent tag, got %d", len(result))
	}
}

func TestHistory_RecordsByProvider_NoMatch(t *testing.T) {
	h := NewHistory(10)
	h.Record(CallRecord{Provider: "p1"})
	h.Record(CallRecord{Provider: "p2"})

	result := h.RecordsByProvider("nonexistent")
	if len(result) != 0 {
		t.Fatalf("expected empty result for nonexistent provider, got %d", len(result))
	}
}

func TestHistory_Stats_MaxLatency(t *testing.T) {
	h := NewHistory(10)
	h.Record(CallRecord{Provider: "p1", LatencyMs: 50})
	h.Record(CallRecord{Provider: "p1", LatencyMs: 300})
	h.Record(CallRecord{Provider: "p1", LatencyMs: 100})

	stats := h.Stats()
	p1 := stats["p1"]
	if p1.MaxLatencyMs != 300 {
		t.Fatalf("expected max latency 300, got %d", p1.MaxLatencyMs)
	}
}

func TestHistory_Stats_CurrencyFromFirstRecord(t *testing.T) {
	h := NewHistory(10)
	h.Record(CallRecord{Provider: "p1", Currency: "", Cost: 1.0})
	h.Record(CallRecord{Provider: "p1", Currency: "CNY", Cost: 2.0})

	stats := h.Stats()
	p1 := stats["p1"]
	if p1.Currency != "CNY" {
		t.Fatalf("expected CNY (first non-empty), got %s", p1.Currency)
	}
}
