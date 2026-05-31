package engine

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

func TestBuildRecord_Basic(t *testing.T) {
	p := &mockProv{name: "deepseek", config: core.ProviderConfig{Model: "deepseek-v4-pro"}}
	s := NewScheduler(p)

	start := time.Now()
	req := core.ChatCompletionRequest{Model: "deepseek-v4-pro", Messages: []core.Message{{Role: "user", Content: "hi"}}}
	resp := &core.ChatCompletionResponse{
		Usage: core.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}

	record := s.buildRecord(p, req, resp, start, nil, []string{"prod"}, "kimi")

	if record.Provider != "deepseek" {
		t.Fatalf("expected provider deepseek, got %s", record.Provider)
	}
	if record.Model != "deepseek-v4-pro" {
		t.Fatalf("expected model deepseek-v4-pro, got %s", record.Model)
	}
	if record.Error != "" {
		t.Fatalf("expected no error, got %s", record.Error)
	}
	if len(record.Tags) != 1 || record.Tags[0] != "prod" {
		t.Fatalf("unexpected tags: %v", record.Tags)
	}
	if record.FallbackFrom != "kimi" {
		t.Fatalf("unexpected fallbackFrom: %s", record.FallbackFrom)
	}
	if record.LatencyMs < 0 {
		t.Fatal("expected non-negative latency")
	}
	if !strings.HasPrefix(record.ID, "deepseek-") {
		t.Fatalf("expected ID prefix deepseek-, got %s", record.ID)
	}
}

func TestBuildRecord_WithError(t *testing.T) {
	p := &mockProv{name: "kimi", config: core.ProviderConfig{Model: "kimi-k2.6"}}
	s := NewScheduler(p)

	start := time.Now()
	req := core.ChatCompletionRequest{Model: "kimi-k2.6"}
	record := s.buildRecord(p, req, nil, start, errors.New("timeout"), nil, "")

	if record.Error != "timeout" {
		t.Fatalf("expected error timeout, got %s", record.Error)
	}
	if record.Response != nil {
		t.Fatal("expected nil response on error")
	}
	if record.Cost != 0 {
		t.Fatalf("expected zero cost on error, got %.4f", record.Cost)
	}
}

func TestBuildRecord_CostCalculation(t *testing.T) {
	p := &mockProv{name: "deepseek", config: core.ProviderConfig{
		Model:   "deepseek-v4-pro",
		Pricing: core.Pricing{PromptPer1K: 1.0, CompletionPer1K: 2.0, Currency: "CNY"},
	}}
	s := NewScheduler(p)

	start := time.Now()
	req := core.ChatCompletionRequest{Model: "deepseek-v4-pro"}
	resp := &core.ChatCompletionResponse{
		Usage: core.Usage{PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500},
	}

	record := s.buildRecord(p, req, resp, start, nil, nil, "")

	// Cost = 1000/1000 * 1.0 + 500/1000 * 2.0 = 1.0 + 1.0 = 2.0
	expectedCost := 2.0
	if record.Cost != expectedCost {
		t.Fatalf("expected cost %.2f, got %.4f", expectedCost, record.Cost)
	}
	if record.Currency != "CNY" {
		t.Fatalf("expected currency CNY, got %s", record.Currency)
	}
}

func TestBuildRecord_DefaultPricingFallback(t *testing.T) {
	p := &mockProv{name: "deepseek", config: core.ProviderConfig{
		Model:   "deepseek-v4-pro",
		Pricing: core.Pricing{}, // empty pricing triggers fallback
	}}
	s := NewScheduler(p)

	start := time.Now()
	req := core.ChatCompletionRequest{Model: "deepseek-v4-pro"}
	resp := &core.ChatCompletionResponse{
		Usage: core.Usage{PromptTokens: 1000, CompletionTokens: 500},
	}

	record := s.buildRecord(p, req, resp, start, nil, nil, "")

	// DefaultPricing("deepseek", "deepseek-v4-pro") should return known pricing
	if record.Cost == 0 {
		t.Fatal("expected non-zero cost with default pricing fallback")
	}
	if record.Currency == "" {
		t.Fatal("expected non-empty currency with default pricing fallback")
	}
}

func TestBuildRecord_ReqIDIncrement(t *testing.T) {
	p := &mockProv{name: "deepseek", config: core.ProviderConfig{Model: "m1"}}
	s := NewScheduler(p)

	start := time.Now()
	req := core.ChatCompletionRequest{Model: "m1"}

	record1 := s.buildRecord(p, req, nil, start, nil, nil, "")
	record2 := s.buildRecord(p, req, nil, start, nil, nil, "")

	if record1.ID == record2.ID {
		t.Fatal("expected unique IDs")
	}
}
