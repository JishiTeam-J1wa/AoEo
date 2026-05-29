package aoeo

import (
	"fmt"
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
// Pricing holds per-1K-token pricing for a provider.
type Pricing struct {
	PromptPer1K     float64 `json:"promptPer1K"`     // Cost per 1K prompt tokens
	CompletionPer1K float64 `json:"completionPer1K"` // Cost per 1K completion tokens
	Currency        string  `json:"currency"`        // e.g. "CNY", "USD"
}

// Cost calculates the monetary cost of a Usage given pricing.
func (u Usage) Cost(p Pricing) float64 {
	if p.PromptPer1K == 0 && p.CompletionPer1K == 0 {
		return 0
	}
	promptCost := float64(u.PromptTokens) / 1000.0 * p.PromptPer1K
	completionCost := float64(u.CompletionTokens) / 1000.0 * p.CompletionPer1K
	return promptCost + completionCost
}

// CostString returns a human-readable cost string.
func (u Usage) CostString(p Pricing) string {
	if p.Currency == "" {
		p.Currency = "CNY"
	}
	return fmt.Sprintf("%.6f %s", u.Cost(p), p.Currency)
}

// DefaultPricing returns built-in pricing for known providers/models.
// Prices are approximations; override via ProviderConfig for accuracy.
func DefaultPricing(name, model string) Pricing {
	switch name {
	case "deepseek":
		if model == "deepseek-v4-pro" {
			return Pricing{PromptPer1K: 2.0, CompletionPer1K: 8.0, Currency: "CNY"}
		}
		// deepseek-v4-flash and others
		return Pricing{PromptPer1K: 1.0, CompletionPer1K: 2.0, Currency: "CNY"}
	case "kimi":
		return Pricing{PromptPer1K: 3.0, CompletionPer1K: 12.0, Currency: "CNY"}
	case "glm":
		return Pricing{PromptPer1K: 5.0, CompletionPer1K: 5.0, Currency: "CNY"}
	case "qwen":
		return Pricing{PromptPer1K: 5.0, CompletionPer1K: 10.0, Currency: "CNY"}
	default:
		return Pricing{Currency: "CNY"}
	}
}
