package aoeo

import "fmt"

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
