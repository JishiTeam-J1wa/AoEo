package core

import (
	"context"
	"errors"
	"sync/atomic"
)

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
