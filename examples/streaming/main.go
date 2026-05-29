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

	client, err := aoeo.NewClient(aoeo.Config{
		Providers: []aoeo.ProviderConfig{
			{
				Name:     "deepseek",
				APIKey:   apiKey,
				Endpoint: "https://api.deepseek.com",
				Model:    "deepseek-v4-pro",
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	req := aoeo.ChatCompletionRequest{
		Messages: []aoeo.Message{
			{Role: "user", Content: "Write a haiku about programming."},
		},
	}

	stream, err := client.ChatCompleteStream(context.Background(), req)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Print("[Streaming] ")
	for chunk := range stream {
		if chunk.Err != nil {
			log.Printf("\n[Stream Error: %v]\n", chunk.Err)
			return
		}
		if chunk.Chunk.FinishReason != "" {
			fmt.Printf("\n[Finish: %s]\n", chunk.Chunk.FinishReason)
			if chunk.Usage.TotalTokens > 0 {
				fmt.Printf("[Usage] %d prompt, %d completion, %d total\n",
					chunk.Usage.PromptTokens, chunk.Usage.CompletionTokens, chunk.Usage.TotalTokens)
			}
			continue
		}
		fmt.Print(chunk.Chunk.Delta.Content)
	}
}
