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
	// When currency is empty, CostString should output only the number without currency suffix
	if strings.Contains(s, "CNY") {
		t.Fatalf("expected no default currency, got %s", s)
	}
	if !strings.Contains(s, "2.000000") {
		t.Fatalf("expected cost value, got %s", s)
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

func TestUsage_Cost_NegativePricing(t *testing.T) {
	u := Usage{PromptTokens: 1000, CompletionTokens: 500}
	// Negative pricing should be treated as 0.
	p := Pricing{PromptPer1K: -1.0, CompletionPer1K: -2.0, Currency: "USD"}
	cost := u.Cost(p)
	if cost != 0 {
		t.Fatalf("expected 0 cost for negative pricing, got %f", cost)
	}
}

func TestUsage_Cost_OneSideZero(t *testing.T) {
	u := Usage{PromptTokens: 2000, CompletionTokens: 1000}
	// Only prompt pricing is set.
	p := Pricing{PromptPer1K: 3.0, CompletionPer1K: 0, Currency: "USD"}
	cost := u.Cost(p)
	expected := 6.0 // 2K * 3.0 / 1K
	if cost != expected {
		t.Fatalf("expected %.2f, got %.6f", expected, cost)
	}

	// Only completion pricing is set.
	p2 := Pricing{PromptPer1K: 0, CompletionPer1K: 4.0, Currency: "USD"}
	cost2 := u.Cost(p2)
	expected2 := 4.0 // 1K * 4.0 / 1K
	if cost2 != expected2 {
		t.Fatalf("expected %.2f, got %.6f", expected2, cost2)
	}
}

func TestUsage_Cost_LargeTokens(t *testing.T) {
	u := Usage{PromptTokens: 100000, CompletionTokens: 50000}
	p := Pricing{PromptPer1K: 1.0, CompletionPer1K: 2.0, Currency: "USD"}
	cost := u.Cost(p)
	expected := 100.0 + 100.0 // 100K * 1.0 + 50K * 2.0
	if cost != expected {
		t.Fatalf("expected %.2f, got %.6f", expected, cost)
	}
}

func TestUsage_CostString_WithCurrency(t *testing.T) {
	u := Usage{PromptTokens: 1000, CompletionTokens: 500}
	p := Pricing{PromptPer1K: 1.0, CompletionPer1K: 2.0, Currency: "CNY"}
	s := u.CostString(p)
	if !strings.Contains(s, "CNY") {
		t.Fatalf("expected CNY in output, got %s", s)
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
