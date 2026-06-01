package core

import (
	"context"
	"testing"
)

func TestPrimaryRouter_Select(t *testing.T) {
	r := &PrimaryRouter{}
	candidates := []ProviderStatus{
		{Name: "p1", Available: false},
		{Name: "p2", Available: true},
		{Name: "p3", Available: true},
	}
	idx, err := r.Select(context.Background(), candidates, ChatCompletionRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx != 1 {
		t.Fatalf("expected index 1, got %d", idx)
	}
}

func TestPrimaryRouter_Select_NoAvailable(t *testing.T) {
	r := &PrimaryRouter{}
	candidates := []ProviderStatus{{Name: "p1", Available: false}}
	_, err := r.Select(context.Background(), candidates, ChatCompletionRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPrimaryRouter_SelectSequence(t *testing.T) {
	r := &PrimaryRouter{}
	candidates := []ProviderStatus{
		{Name: "p1", Available: true},
		{Name: "p2", Available: false},
		{Name: "p3", Available: true},
	}
	seq, err := r.SelectSequence(context.Background(), candidates, ChatCompletionRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(seq) != 2 || seq[0] != 0 || seq[1] != 2 {
		t.Fatalf("expected [0, 2], got %v", seq)
	}
}

func TestRoundRobinRouter_Select(t *testing.T) {
	r := &RoundRobinRouter{}
	candidates := []ProviderStatus{
		{Name: "p1", Available: true},
		{Name: "p2", Available: true},
	}

	seen := make(map[int]bool)
	for i := 0; i < 10; i++ {
		idx, err := r.Select(context.Background(), candidates, ChatCompletionRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		seen[idx] = true
	}
	if len(seen) != 2 {
		t.Fatal("round-robin should distribute across providers")
	}
}

func TestRoundRobinRouter_SelectSequence(t *testing.T) {
	r := &RoundRobinRouter{}
	candidates := []ProviderStatus{
		{Name: "p1", Available: true},
		{Name: "p2", Available: true},
	}

	seq1, _ := r.SelectSequence(context.Background(), candidates, ChatCompletionRequest{})
	seq2, _ := r.SelectSequence(context.Background(), candidates, ChatCompletionRequest{})

	// Sequences should be rotated
	if seq1[0] == seq2[0] {
		t.Fatal("round-robin sequence should rotate starting point")
	}
}

func TestRandomRouter_Select(t *testing.T) {
	r := &RandomRouter{}
	candidates := []ProviderStatus{
		{Name: "p1", Available: true},
		{Name: "p2", Available: true},
		{Name: "p3", Available: true},
	}

	seen := make(map[int]bool)
	for i := 0; i < 30; i++ {
		idx, err := r.Select(context.Background(), candidates, ChatCompletionRequest{Model: "test"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		seen[idx] = true
	}
	if len(seen) < 2 {
		t.Fatal("random router should distribute across providers")
	}
}

func TestRandomRouter_SelectSequence(t *testing.T) {
	r := &RandomRouter{}
	candidates := []ProviderStatus{
		{Name: "p1", Available: true},
		{Name: "p2", Available: true},
		{Name: "p3", Available: true},
	}

	seq, err := r.SelectSequence(context.Background(), candidates, ChatCompletionRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(seq) != 3 {
		t.Fatalf("expected 3 items, got %d", len(seq))
	}
	// Verify all providers are in the sequence
	seen := make(map[int]bool)
	for _, idx := range seq {
		seen[idx] = true
	}
	if len(seen) != 3 {
		t.Fatal("sequence should contain all providers")
	}
}


// ========== WeightedRouter tests ==========

func TestWeightedRouter_Select_Latency(t *testing.T) {
	r := &WeightedRouter{Strategy: StrategyLatency}
	candidates := []ProviderStatus{
		{Name: "fast", Available: true, Health: ProviderHealth{AvgLatencyMs: 50}},
		{Name: "slow", Available: true, Health: ProviderHealth{AvgLatencyMs: 5000}},
	}

	// Fast provider should be selected more often.
	fastCount := 0
	for i := 0; i < 100; i++ {
		idx, err := r.Select(context.Background(), candidates, ChatCompletionRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if candidates[idx].Name == "fast" {
			fastCount++
		}
	}
	if fastCount < 50 {
		t.Fatalf("expected fast provider to be selected majority of time, got %d/100", fastCount)
	}
}

func TestWeightedRouter_Select_SuccessRate(t *testing.T) {
	r := &WeightedRouter{Strategy: StrategySuccessRate}
	candidates := []ProviderStatus{
		{Name: "good", Available: true, Health: ProviderHealth{SuccessRate: 1.0}},
		{Name: "bad", Available: true, Health: ProviderHealth{SuccessRate: 0.0}},
	}

	// Good provider should always be selected (score 1.0 vs 0.0).
	for i := 0; i < 20; i++ {
		idx, err := r.Select(context.Background(), candidates, ChatCompletionRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if candidates[idx].Name != "good" {
			t.Fatalf("expected good provider at iteration %d, got %s", i, candidates[idx].Name)
		}
	}
}

func TestWeightedRouter_Select_Combined(t *testing.T) {
	r := &WeightedRouter{Strategy: StrategyCombined}
	candidates := []ProviderStatus{
		{Name: "a", Available: true, Health: ProviderHealth{AvgLatencyMs: 100, SuccessRate: 0.5}},
		{Name: "b", Available: true, Health: ProviderHealth{AvgLatencyMs: 100, SuccessRate: 0.5}},
	}

	// Equal scores: verify both can be selected (counter grows enough over many calls).
	seenA, seenB := false, false
	for i := 0; i < 10000; i++ {
		idx, err := r.Select(context.Background(), candidates, ChatCompletionRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if candidates[idx].Name == "a" {
			seenA = true
		} else {
			seenB = true
		}
		if seenA && seenB {
			break
		}
	}
	if !seenA || !seenB {
		t.Fatal("expected both providers to be selected with equal scores")
	}
}

func TestWeightedRouter_SelectSequence(t *testing.T) {
	r := &WeightedRouter{Strategy: StrategyLatency}
	candidates := []ProviderStatus{
		{Name: "slow", Available: true, Health: ProviderHealth{AvgLatencyMs: 5000}},
		{Name: "fast", Available: true, Health: ProviderHealth{AvgLatencyMs: 50}},
		{Name: "medium", Available: true, Health: ProviderHealth{AvgLatencyMs: 1000}},
	}

	seq, err := r.SelectSequence(context.Background(), candidates, ChatCompletionRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(seq) != 3 {
		t.Fatalf("expected 3 in sequence, got %d", len(seq))
	}
	// Should be sorted by score descending: fast, medium, slow
	if candidates[seq[0]].Name != "fast" {
		t.Fatalf("expected fast first, got %s", candidates[seq[0]].Name)
	}
	if candidates[seq[2]].Name != "slow" {
		t.Fatalf("expected slow last, got %s", candidates[seq[2]].Name)
	}
}

func TestWeightedRouter_NoAvailable(t *testing.T) {
	r := &WeightedRouter{Strategy: StrategyLatency}
	candidates := []ProviderStatus{{Name: "p1", Available: false}}
	_, err := r.Select(context.Background(), candidates, ChatCompletionRequest{})
	if err == nil {
		t.Fatal("expected error for no available providers")
	}
}

// ========== SingleProviderRouter tests ==========

func TestSingleProviderRouter_Select(t *testing.T) {
	r := &SingleProviderRouter{Name: "target"}
	candidates := []ProviderStatus{
		{Name: "other", Available: true},
		{Name: "target", Available: true},
	}
	idx, err := r.Select(context.Background(), candidates, ChatCompletionRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx != 1 {
		t.Fatalf("expected index 1, got %d", idx)
	}
}

func TestSingleProviderRouter_Select_NotAvailable(t *testing.T) {
	r := &SingleProviderRouter{Name: "target"}
	candidates := []ProviderStatus{
		{Name: "target", Available: false},
	}
	_, err := r.Select(context.Background(), candidates, ChatCompletionRequest{})
	if err == nil {
		t.Fatal("expected error when target not available")
	}
}

func TestSingleProviderRouter_SelectSequence(t *testing.T) {
	r := &SingleProviderRouter{Name: "target"}
	candidates := []ProviderStatus{
		{Name: "other", Available: true},
		{Name: "target", Available: true},
	}
	seq, err := r.SelectSequence(context.Background(), candidates, ChatCompletionRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(seq) != 1 || seq[0] != 1 {
		t.Fatalf("expected [1], got %v", seq)
	}
}
