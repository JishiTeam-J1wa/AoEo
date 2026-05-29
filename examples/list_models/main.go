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

	client, err := aoeo.NewClient(aoeo.Config{
		Providers: []aoeo.ProviderConfig{
			{
				Name:     "deepseek",
				APIKey:   deepseekKey,
				Endpoint: "https://api.deepseek.com",
				Model:    "deepseek-v4-pro",
			},
			{
				Name:     "kimi",
				APIKey:   kimiKey,
				Endpoint: "https://api.moonshot.cn/v1",
				Model:    "kimi-k2.6",
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	ctx := context.Background()

	for _, name := range []string{"deepseek", "kimi"} {
		models, err := client.ListModels(ctx, name)
		if err != nil {
			log.Printf("[%s] list models failed: %v", name, err)
			continue
		}
		fmt.Printf("\n=== %s (%d models) ===\n", name, len(models))
		for _, m := range models {
			fmt.Printf("  - %s (owned_by: %s)\n", m.ID, m.OwnedBy)
		}
	}

	// Test connectivity
	if err := client.TestProvider(ctx, "deepseek"); err != nil {
		log.Printf("[deepseek] test failed: %v", err)
	} else {
		fmt.Println("\n[deepseek] connectivity OK")
	}
}
