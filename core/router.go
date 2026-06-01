package core

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
)

// WeightStrategy determines how the WeightedRouter scores providers.
type WeightStrategy int

const (
	StrategyLatency WeightStrategy = iota     // lower latency = higher weight
	StrategySuccessRate                       // higher success rate = higher weight
	StrategyCombined                          // composite: latency 50% + success rate 50%
)

// SingleProviderRouter forces selection of a specific provider by name.
// It is useful for CLI tools and debugging that need to target one provider.
type SingleProviderRouter struct {
	Name string
}

func (r *SingleProviderRouter) Select(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) (int, error) {
	for i, c := range candidates {
		if c.Name == r.Name && c.Available {
			return i, nil
		}
	}
	return -1, errors.New("provider not available")
}

func (r *SingleProviderRouter) SelectSequence(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) ([]int, error) {
	idx, err := r.Select(ctx, candidates, req)
	if err != nil {
		return nil, err
	}
	return []int{idx}, nil
}

// Router decides which provider(s) to use for a given request.
// Implementations must be safe for concurrent use.
type Router interface {
	// Select picks a single provider from the candidate list.
	// Returns the index into candidates, or an error if none are suitable.
	Select(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) (int, error)

	// SelectSequence returns an ordered list of candidate indices to try
	// for fallback-style requests. The scheduler tries each in order
	// until one succeeds.
	SelectSequence(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) ([]int, error)
}

// PrimaryRouter always selects the first available provider.
type PrimaryRouter struct{}

func (r *PrimaryRouter) Select(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) (int, error) {
	for i, c := range candidates {
		if c.Available {
			return i, nil
		}
	}
	return -1, errors.New("no available provider")
}

func (r *PrimaryRouter) SelectSequence(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) ([]int, error) {
	var seq []int
	for i, c := range candidates {
		if c.Available {
			seq = append(seq, i)
		}
	}
	if len(seq) == 0 {
		return nil, errors.New("no available provider")
	}
	return seq, nil
}

// RoundRobinRouter distributes requests evenly across available providers.
type RoundRobinRouter struct {
	idx atomic.Uint64
}

func (r *RoundRobinRouter) Select(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) (int, error) {
	avail := make([]int, 0, len(candidates))
	for i, c := range candidates {
		if c.Available {
			avail = append(avail, i)
		}
	}
	if len(avail) == 0 {
		return -1, errors.New("no available provider")
	}
	next := int(r.idx.Add(1)-1) % len(avail)
	return avail[next], nil
}

func (r *RoundRobinRouter) SelectSequence(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) ([]int, error) {
	var seq []int
	for i, c := range candidates {
		if c.Available {
			seq = append(seq, i)
		}
	}
	if len(seq) == 0 {
		return nil, errors.New("no available provider")
	}
	// Rotate starting point for variety, but keep full sequence for fallback
	if len(seq) > 1 {
		start := int(r.idx.Add(1)-1) % len(seq)
		rotated := make([]int, len(seq))
		for i := range seq {
			rotated[i] = seq[(start+i)%len(seq)]
		}
		return rotated, nil
	}
	return seq, nil
}

// RandomRouter selects a random available provider using an internal counter.
// The selection is deterministic per call order, not per request content.
type RandomRouter struct {
	counter atomic.Uint64
}

func (r *RandomRouter) Select(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) (int, error) {
	var avail []int
	for i, c := range candidates {
		if c.Available {
			avail = append(avail, i)
		}
	}
	if len(avail) == 0 {
		return -1, errors.New("no available provider")
	}
	h := r.counter.Add(1)
	return avail[h%uint64(len(avail))], nil
}

func (r *RandomRouter) SelectSequence(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) ([]int, error) {
	var seq []int
	for i, c := range candidates {
		if c.Available {
			seq = append(seq, i)
		}
	}
	if len(seq) == 0 {
		return nil, errors.New("no available provider")
	}
	// Shuffle sequence using a counter-based PRNG
	shuffled := make([]int, len(seq))
	copy(shuffled, seq)
	for i := len(shuffled) - 1; i > 0; i-- {
		h := r.counter.Add(1)
		j := int(h % uint64(i+1))
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}
	return shuffled, nil
}

// WeightedRouter selects providers using weighted scoring based on runtime health metrics.
// Higher score = more likely to be selected. All selections are deterministic (no math/rand).
type WeightedRouter struct {
	Strategy WeightStrategy
	counter  atomic.Uint64
}

// score computes a weight score for a provider based on the configured strategy.
// Score is always in range [0, 1].
func (r *WeightedRouter) score(status ProviderStatus) float64 {
	h := status.Health
	switch r.Strategy {
	case StrategyLatency:
		// Lower latency = higher score. Use sigmoid-like decay.
		// 0ms -> 1.0, 1000ms -> ~0.5, 5000ms -> ~0.17
		return 1.0 / (1.0 + float64(h.AvgLatencyMs)/1000.0)
	case StrategySuccessRate:
		return h.SuccessRate
	case StrategyCombined:
		latencyScore := 1.0 / (1.0 + float64(h.AvgLatencyMs)/1000.0)
		return latencyScore*0.5 + h.SuccessRate*0.5
	default:
		return 1.0
	}
}

func (r *WeightedRouter) Select(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) (int, error) {
	type scored struct {
		idx    int
		score  float64
		weight float64
	}
	var scoredList []scored
	var totalWeight float64
	for i, c := range candidates {
		if c.Available {
			s := r.score(c)
			if s > 0 {
				scoredList = append(scoredList, scored{idx: i, score: s, weight: s})
				totalWeight += s
			}
		}
	}
	if len(scoredList) == 0 {
		return -1, errors.New("no available provider")
	}
	if len(scoredList) == 1 {
		return scoredList[0].idx, nil
	}

	// Deterministic weighted random using counter.
	// Use a larger step to get meaningful variation even with small counters.
	h := r.counter.Add(1)
	step := uint64(0x9E3779B97F4A7C15) // golden ratio constant for better distribution
	target := float64(h*step%math.MaxUint64) / float64(math.MaxUint64) * totalWeight
	var accum float64
	for _, s := range scoredList {
		accum += s.weight
		if accum >= target {
			return s.idx, nil
		}
	}
	return scoredList[len(scoredList)-1].idx, nil
}

func (r *WeightedRouter) SelectSequence(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) ([]int, error) {
	type scored struct {
		idx   int
		score float64
	}
	var scoredList []scored
	for i, c := range candidates {
		if c.Available {
			s := r.score(c)
			scoredList = append(scoredList, scored{idx: i, score: s})
		}
	}
	if len(scoredList) == 0 {
		return nil, errors.New("no available provider")
	}

	// Sort by score descending (simple bubble for small N, deterministic).
	for i := 0; i < len(scoredList)-1; i++ {
		for j := i + 1; j < len(scoredList); j++ {
			if scoredList[j].score > scoredList[i].score {
				scoredList[i], scoredList[j] = scoredList[j], scoredList[i]
			}
		}
	}

	seq := make([]int, len(scoredList))
	for i, s := range scoredList {
		seq[i] = s.idx
	}
	return seq, nil
}
