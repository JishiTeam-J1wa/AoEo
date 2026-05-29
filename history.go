package aoeo

import (
	"sync"
	"time"
)

// CallRecord represents a single AI provider invocation.
type CallRecord struct {
	ID           string                  `json:"id"`
	Provider     string                  `json:"provider"`
	Model        string                  `json:"model"`
	Request      ChatCompletionRequest   `json:"request"`
	Response     *ChatCompletionResponse `json:"response,omitempty"`
	Error        string                  `json:"error,omitempty"`
	LatencyMs    int64                   `json:"latency_ms"`
	Timestamp    time.Time               `json:"timestamp"`
	Tags         []string                `json:"tags,omitempty"`
	FallbackFrom string                  `json:"fallback_from,omitempty"` // If this was a fallback, which provider failed first
	Cost         float64                 `json:"cost"`                    // Monetary cost of this call
	Currency     string                  `json:"currency"`                // Currency unit for cost
}

// History tracks recent AI provider calls with thread-safe access.
// It is intended for debugging, auditing, and building UIs that show call history.
type History struct {
	mu      sync.RWMutex
	records []CallRecord
	maxSize int
}

// NewHistory creates a History with the given maximum number of retained records.
func NewHistory(maxSize int) *History {
	if maxSize <= 0 {
		maxSize = 100
	}
	return &History{maxSize: maxSize}
}

// Record appends a call record. If capacity is exceeded, oldest records are dropped.
func (h *History) Record(r CallRecord) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.records = append(h.records, r)
	if len(h.records) > h.maxSize {
		h.records = h.records[len(h.records)-h.maxSize:]
	}
}

// Records returns a copy of all stored records (newest first).
func (h *History) Records() []CallRecord {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make([]CallRecord, len(h.records))
	// Reverse copy (newest first).
	for i := range h.records {
		result[i] = h.records[len(h.records)-1-i]
	}
	return result
}

// RecordsByTag returns records filtered by tag.
func (h *History) RecordsByTag(tag string) []CallRecord {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var result []CallRecord
	for i := len(h.records) - 1; i >= 0; i-- {
		for _, t := range h.records[i].Tags {
			if t == tag {
				result = append(result, h.records[i])
				break
			}
		}
	}
	return result
}

// RecordsByProvider returns records for a specific provider.
func (h *History) RecordsByProvider(name string) []CallRecord {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var result []CallRecord
	for i := len(h.records) - 1; i >= 0; i-- {
		if h.records[i].Provider == name {
			result = append(result, h.records[i])
		}
	}
	return result
}

// Clear removes all records.
func (h *History) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = h.records[:0]
}

// Stats returns aggregate statistics per provider.
func (h *History) Stats() map[string]ProviderStats {
	h.mu.RLock()
	defer h.mu.RUnlock()

	stats := make(map[string]ProviderStats)
	for _, r := range h.records {
		s := stats[r.Provider]
		s.TotalCalls++
		s.TotalLatencyMs += r.LatencyMs
		if r.Error != "" {
			s.FailedCalls++
		}
		if r.LatencyMs > s.MaxLatencyMs {
			s.MaxLatencyMs = r.LatencyMs
		}
		s.TotalCost += r.Cost
		stats[r.Provider] = s
	}

	for name, s := range stats {
		if s.TotalCalls > 0 {
			s.AvgLatencyMs = s.TotalLatencyMs / int64(s.TotalCalls)
		}
		// Derive currency from the most recent record with cost for this provider.
		for i := len(h.records) - 1; i >= 0; i-- {
			if h.records[i].Provider == name && h.records[i].Currency != "" {
				s.Currency = h.records[i].Currency
				break
			}
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
