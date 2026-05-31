package core

import (
	"strings"
	"testing"
)

func TestUsage_Cost(t *testing.T) {
	u := Usage{PromptTokens: 2000, CompletionTokens: 1000}
	p := Pricing{PromptPer1K: 1.5, CompletionPer1K: 3.0, Currency: "USD"}

	cost := u.Cost(p)
	expected := 3.0 + 3.0 // 2K * 1.5 + 1K * 3.0
	if cost != expected {
		t.Fatalf("expected %.2f, got %.2f", expected, cost)
	}
}

func TestUsage_Cost_ZeroPricing(t *testing.T) {
	u := Usage{PromptTokens: 1000, CompletionTokens: 500}
	if u.Cost(Pricing{}) != 0 {
		t.Fatal("expected 0 cost for zero pricing")
	}
}

func TestUsage_CostString(t *testing.T) {
	u := Usage{PromptTokens: 1000, CompletionTokens: 500}
	p := Pricing{PromptPer1K: 1.0, CompletionPer1K: 2.0, Currency: "USD"}

	s := u.CostString(p)
	if !strings.Contains(s, "USD") {
		t.Fatalf("expected currency in output, got %s", s)
	}
	if !strings.Contains(s, "2.000000") {
		t.Fatalf("expected cost in output, got %s", s)
	}
}

func TestUsage_CostString_DefaultCurrency(t *testing.T) {
	u := Usage{PromptTokens: 1000, CompletionTokens: 500}
	p := Pricing{PromptPer1K: 1.0, CompletionPer1K: 2.0} // no currency

	s := u.CostString(p)
	if !strings.Contains(s, "CNY") {
		t.Fatalf("expected default CNY currency, got %s", s)
	}
}

func TestDefaultPricing(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		wantCur  string
	}{
		{"deepseek-pro", "deepseek", "deepseek-v4-pro", "CNY"},
		{"deepseek-flash", "deepseek", "deepseek-v4-flash", "CNY"},
		{"kimi", "kimi", "kimi-k2.6", "CNY"},
		{"glm", "glm", "glm-5.1", "CNY"},
		{"qwen", "qwen", "qwen3.7-max", "CNY"},
		{"unknown", "unknown", "any", "CNY"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := DefaultPricing(tt.provider, tt.model)
			if p.Currency != tt.wantCur {
				t.Fatalf("expected currency %s, got %s", tt.wantCur, p.Currency)
			}
		})
	}
}

func TestDefaultPricing_KnownModels(t *testing.T) {
	p := DefaultPricing("deepseek", "deepseek-v4-pro")
	if p.PromptPer1K != 2.0 || p.CompletionPer1K != 8.0 {
		t.Fatalf("unexpected deepseek pro pricing: %+v", p)
	}

	p = DefaultPricing("deepseek", "deepseek-v4-flash")
	if p.PromptPer1K != 1.0 || p.CompletionPer1K != 2.0 {
		t.Fatalf("unexpected deepseek flash pricing: %+v", p)
	}
}
