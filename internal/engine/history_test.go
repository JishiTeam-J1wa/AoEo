package engine

import (
	"testing"
	"time"
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
