package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/JishiTeam-J1wa/AoEo"
)

// EventPrinter is a simple event emitter that prints events to stdout.
type EventPrinter struct {
	mu sync.Mutex
}

func (e *EventPrinter) Emit(topic string, data ...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	fmt.Printf("[EVENT] %s", topic)
	for _, d := range data {
		fmt.Printf(" | %v", d)
	}
	fmt.Println()
}

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

	// Attach the event emitter to receive lifecycle notifications.
	client.SetEmitter(&EventPrinter{})

	req := aoeo.BuildRequest(
		[]aoeo.Message{{Role: "user", Content: "Say hello"}},
	)

	_, err = client.ChatComplete(context.Background(), req)
	if err != nil {
		log.Fatal(err)
	}
}
