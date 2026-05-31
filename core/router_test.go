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
