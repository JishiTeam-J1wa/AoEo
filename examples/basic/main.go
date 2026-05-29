package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/JishiTeam-J1wa/AoEo"
)

func main() {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		log.Fatal("DEEPSEEK_API_KEY environment variable not set")
	}

	cfg := aoeo.Config{
		Providers: []aoeo.ProviderConfig{
			{
				Name:     "deepseek",
				APIKey:   apiKey,
				Endpoint: "https://api.deepseek.com",
				Model:    "deepseek-v4-pro",
			},
		},
	}

	client, err := aoeo.NewClient(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	req := aoeo.BuildRequest(
		[]aoeo.Message{
			{Role: "user", Content: "What is the capital of France?"},
		},
		aoeo.WithTemperature(0.7),
	)

	resp, err := client.ChatComplete(context.Background(), req)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(resp.Choices[0].Message.Content)
	fmt.Printf("Tokens: %d prompt, %d completion\n",
		resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
	fmt.Printf("Cost: %.4f CNY\n",
		resp.Usage.Cost(aoeo.DefaultPricing("deepseek", "deepseek-v4-pro")))
}
