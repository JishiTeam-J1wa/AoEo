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

	req := aoeo.BuildRequest(
		[]aoeo.Message{{Role: "user", Content: "What is 2+2?"}},
	)

	result, err := client.Audit(context.Background(), req)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Consensus: %v\n", result.Consensus)
	if result.Primary != nil {
		fmt.Println("Primary:", result.Primary.Content())
	}
	if result.Audit != nil {
		fmt.Println("Audit:  ", result.Audit.Content())
	}
	if result.Adjusted != nil {
		fmt.Println("Adjusted:", result.Adjusted.Content())
	}
}
