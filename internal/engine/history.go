package engine

import (
	"context"
	"sync"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

// CallRecord represents a single AI provider invocation.
type CallRecord struct {
	ID           string                       `json:"id"`
	Provider     string                       `json:"provider"`
	Model        string                       `json:"model"`
	Request      core.ChatCompletionRequest   `json:"request"`
	Response     *core.ChatCompletionResponse `json:"response,omitempty"`
	Error        string                       `json:"error,omitempty"`
	LatencyMs    int64                        `json:"latency_ms"`
	Timestamp    time.Time                    `json:"timestamp"`
	Tags         []string                     `json:"tags,omitempty"`
	FallbackFrom string                       `json:"fallback_from,omitempty"`
	Cost         float64                      `json:"cost"`
	Currency     string                       `json:"currency"`
}

// History tracks recent AI provider calls with thread-safe access.
// It uses a fixed-size ring buffer internally for hot data, and can optionally
// persist records to a core.Storage backend for long-term retention.
type History struct {
	mu      sync.RWMutex
	buf     []CallRecord // fixed-size ring buffer
	head    int          // index of the next write position (newest element is at head-1)
	count   int          // number of valid elements in buf
	maxSize int
	storage core.Storage // optional persistent backend
}

// NewHistory creates a History with the given maximum number of retained records.
func NewHistory(maxSize int) *History {
	if maxSize <= 0 {
		maxSize = 100
	}
	return &History{
		buf:     make([]CallRecord, maxSize),
		maxSize: maxSize,
	}
}

// SetStorage attaches a persistent storage backend. Call before Record.
func (h *History) SetStorage(s core.Storage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.storage = s
}

// Record appends a call record. If capacity is exceeded, the oldest record is overwritten.
// If a storage backend is configured, the record is also persisted.
func (h *History) Record(r CallRecord) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.buf[h.head] = r
	h.head = (h.head + 1) % h.maxSize
	if h.count < h.maxSize {
		h.count++
	}

	if h.storage != nil {
		// Persist asynchronously to avoid blocking the caller.
		go h.persistRecord(r)
	}
}

func (h *History) persistRecord(r CallRecord) {
	cr := core.CallRecord{
		ID:           r.ID,
		Provider:     r.Provider,
		Model:        r.Model,
		Request:      r.Request,
		Response:     r.Response,
		Error:        r.Error,
		LatencyMs:    r.LatencyMs,
		Timestamp:    r.Timestamp,
		Tags:         r.Tags,
		FallbackFrom: r.FallbackFrom,
		Cost:         r.Cost,
		Currency:     r.Currency,
	}
	// Use a background context with timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := h.storage.RecordCall(ctx, cr); err != nil {
		core.GetLogger().Error("history persist failed", "error", err)
	}
}

// at returns the element at logical index i, where 0 is the newest.
// Must be called with read lock held.
func (h *History) at(i int) CallRecord {
	// newest is at (h.head - 1), then (h.head - 2), etc.
	idx := (h.head - 1 - i + h.maxSize) % h.maxSize
	return h.buf[idx]
}

// Records returns a copy of all stored records (newest first).
// If a storage backend is configured, it queries the database for the full history.
func (h *History) Records() []CallRecord {
	h.mu.RLock()
	s := h.storage
	h.mu.RUnlock()

	if s != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		crs, err := s.GetCalls(ctx, h.maxSize)
		if err == nil {
			return h.fromCoreCalls(crs)
		}
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]CallRecord, h.count)
	for i := 0; i < h.count; i++ {
		result[i] = h.at(i)
	}
	return result
}

// RecordsByTag returns records filtered by tag (newest first).
func (h *History) RecordsByTag(tag string) []CallRecord {
	h.mu.RLock()
	s := h.storage
	h.mu.RUnlock()

	if s != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		crs, err := s.GetCallsByTag(ctx, tag, h.maxSize)
		if err == nil {
			return h.fromCoreCalls(crs)
		}
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]CallRecord, 0, h.count/4+1)
	for i := 0; i < h.count; i++ {
		r := h.at(i)
		for _, t := range r.Tags {
			if t == tag {
				result = append(result, r)
				break
			}
		}
	}
	return result
}

// RecordsByProvider returns records for a specific provider (newest first).
func (h *History) RecordsByProvider(name string) []CallRecord {
	h.mu.RLock()
	s := h.storage
	h.mu.RUnlock()

	if s != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		crs, err := s.GetCallsByProvider(ctx, name, h.maxSize)
		if err == nil {
			return h.fromCoreCalls(crs)
		}
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]CallRecord, 0, h.count/4+1)
	for i := 0; i < h.count; i++ {
		r := h.at(i)
		if r.Provider == name {
			result = append(result, r)
		}
	}
	return result
}

func (h *History) fromCoreCalls(crs []core.CallRecord) []CallRecord {
	result := make([]CallRecord, len(crs))
	for i, cr := range crs {
		result[i] = CallRecord{
			ID:           cr.ID,
			Provider:     cr.Provider,
			Model:        cr.Model,
			Request:      cr.Request,
			Response:     cr.Response,
			Error:        cr.Error,
			LatencyMs:    cr.LatencyMs,
			Timestamp:    cr.Timestamp,
			Tags:         cr.Tags,
			FallbackFrom: cr.FallbackFrom,
			Cost:         cr.Cost,
			Currency:     cr.Currency,
		}
	}
	return result
}

// Clear removes all records.
func (h *History) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.head = 0
	h.count = 0
	// Zero out references to help GC.
	for i := range h.buf {
		h.buf[i] = CallRecord{}
	}
}

// Stats returns aggregate statistics per provider.
// If a storage backend is configured, it queries the database for aggregated stats.
func (h *History) Stats() map[string]ProviderStats {
	h.mu.RLock()
	s := h.storage
	h.mu.RUnlock()

	if s != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		coreStats, err := s.GetProviderStats(ctx)
		if err == nil {
			return h.fromCoreStats(coreStats)
		}
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	stats := make(map[string]ProviderStats)
	for i := 0; i < h.count; i++ {
		r := h.at(i)
		st := stats[r.Provider]
		st.Provider = r.Provider
		st.TotalCalls++
		st.TotalLatencyMs += r.LatencyMs
		if r.Error != "" {
			st.FailedCalls++
		}
		if r.LatencyMs > st.MaxLatencyMs {
			st.MaxLatencyMs = r.LatencyMs
		}
		st.TotalCost += r.Cost
		if st.Currency == "" && r.Currency != "" {
			st.Currency = r.Currency
		}
		stats[r.Provider] = st
	}

	for name, st := range stats {
		if st.TotalCalls > 0 {
			st.AvgLatencyMs = st.TotalLatencyMs / int64(st.TotalCalls)
		}
		stats[name] = st
	}
	return stats
}

func (h *History) fromCoreStats(css map[string]core.ProviderStats) map[string]ProviderStats {
	stats := make(map[string]ProviderStats)
	for name, cs := range css {
		stats[name] = ProviderStats{
			Provider:     cs.Provider,
			TotalCalls:   cs.TotalCalls,
			TotalCost:    cs.TotalCost,
			Currency:     cs.Currency,
			AvgLatencyMs: cs.AvgLatency,
			FailedCalls:  cs.ErrorCount,
		}
	}
	return stats
}

// ProviderStats holds aggregated statistics for a single provider.
type ProviderStats struct {
	Provider       string  `json:"provider"`
	TotalCalls     int     `json:"total_calls"`
	FailedCalls    int     `json:"failed_calls"`
	TotalLatencyMs int64   `json:"total_latency_ms"`
	AvgLatencyMs   int64   `json:"avg_latency_ms"`
	MaxLatencyMs   int64   `json:"max_latency_ms"`
	TotalCost      float64 `json:"total_cost"`
	Currency       string  `json:"currency"`
}
