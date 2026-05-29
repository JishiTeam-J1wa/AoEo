package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/JishiTeam-J1wa/AoEo"
)

func main() {
	deepseekKey := os.Getenv("DEEPSEEK_API_KEY")
	kimiKey := os.Getenv("KIMI_API_KEY")
	if deepseekKey == "" || kimiKey == "" {
		log.Fatal("DEEPSEEK_API_KEY and KIMI_API_KEY must be set")
	}

	cfg := aoeo.Config{
		Providers: []aoeo.ProviderConfig{
			{
				Name:          "deepseek",
				APIKey:        deepseekKey,
				Endpoint:      "https://api.deepseek.com",
				Model:         "deepseek-v4-pro",
				MaxConcurrent: 2,
				Pricing:       aoeo.DefaultPricing("deepseek", "deepseek-v4-pro"),
			},
			{
				Name:          "kimi",
				APIKey:        kimiKey,
				Endpoint:      "https://api.moonshot.cn/v1",
				Model:         "kimi-k2.6",
				MaxConcurrent: 2,
				Pricing:       aoeo.DefaultPricing("kimi", "kimi-k2.6"),
			},
		},
	}

	client, err := aoeo.NewClient(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	// Check provider status
	for _, s := range client.ProviderStatus() {
		fmt.Printf("Provider: %s | Available: %v | Model: %s\n", s.Name, s.Available, s.Model)
	}

	req := aoeo.BuildRequest(
		[]aoeo.Message{{Role: "user", Content: "Explain quantum computing in one sentence."}},
	)

	// 1. Single completion (uses primary provider)
	resp, err := client.ChatComplete(context.Background(), req)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\n[Primary]", resp.Content())
	fmt.Printf("  Cost: %.4f %s\n", resp.Usage.Cost(cfg.Providers[0].Pricing), cfg.Providers[0].Pricing.Currency)

	// 2. With fallback (tries primary, then falls back to next)
	resp, err = client.ChatCompleteWithFallback(context.Background(), req)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\n[With Fallback]", resp.Content())

	// 3. Dual mode (queries two different providers concurrently)
	dual, err := client.ChatCompleteDual(context.Background(), req)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n[Dual] Consensus: %v\n", dual.Consensus)
	if dual.Result1 != nil && len(dual.Result1.Choices) > 0 {
		fmt.Println("Provider 1:", dual.Result1.Content())
	}
	if dual.Result2 != nil && len(dual.Result2.Choices) > 0 {
		fmt.Println("Provider 2:", dual.Result2.Content())
	}

	// 4. Show aggregated stats
	fmt.Println("\n=== Stats ===")
	for name, s := range client.Stats() {
		fmt.Printf("  %s: %d calls, %.4f %s total, %.1fms avg\n",
			name, s.TotalCalls, s.TotalCost, s.Currency, float64(s.AvgLatencyMs))
	}
}
