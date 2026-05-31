package engine

import (
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
// It uses a fixed-size ring buffer internally to avoid repeated allocations.
type History struct {
	mu      sync.RWMutex
	buf     []CallRecord // fixed-size ring buffer
	head    int          // index of the next write position (newest element is at head-1)
	count   int          // number of valid elements in buf
	maxSize int
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

// Record appends a call record. If capacity is exceeded, the oldest record is overwritten.
func (h *History) Record(r CallRecord) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.buf[h.head] = r
	h.head = (h.head + 1) % h.maxSize
	if h.count < h.maxSize {
		h.count++
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
func (h *History) Records() []CallRecord {
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
	defer h.mu.RUnlock()

	// Pre-allocate with a reasonable capacity to reduce reallocations.
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
func (h *History) Stats() map[string]ProviderStats {
	h.mu.RLock()
	defer h.mu.RUnlock()

	stats := make(map[string]ProviderStats)
	for i := 0; i < h.count; i++ {
		r := h.at(i)
		s := stats[r.Provider]
		s.Provider = r.Provider
		s.TotalCalls++
		s.TotalLatencyMs += r.LatencyMs
		if r.Error != "" {
			s.FailedCalls++
		}
		if r.LatencyMs > s.MaxLatencyMs {
			s.MaxLatencyMs = r.LatencyMs
		}
		s.TotalCost += r.Cost
		if s.Currency == "" && r.Currency != "" {
			s.Currency = r.Currency
		}
		stats[r.Provider] = s
	}

	for name, s := range stats {
		if s.TotalCalls > 0 {
			s.AvgLatencyMs = s.TotalLatencyMs / int64(s.TotalCalls)
		}
		stats[name] = s
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
