package main

import (
	"context"
	"fmt"
	"log"

	aoeo "github.com/JishiTeam-J1wa/AoEo"
	"github.com/JishiTeam-J1wa/AoEo/privacy"
)

func main() {
	// One-line privacy enablement via environment variables:
	//   AOEO_PRIVACY_ENABLED=true
	//   AOEO_PRIVACY_ENDPOINT=http://localhost:8080
	//
	// Or use privacy.WithPrivacyModel(endpoint) for explicit configuration.
	//
	// Multi-instance load balancing with LeastLatency:
	//   AOEO_PRIVACY_ENDPOINT=http://sidecar-1:8080,http://sidecar-2:8080,http://sidecar-3:8080
	client, err := aoeo.NewClient(
		aoeo.Config{
			Providers: []aoeo.ProviderConfig{
				{
					Name:     "deepseek",
					APIKey:   "sk-xxx",
					Endpoint: "https://api.deepseek.com",
					Model:    "deepseek-v4-pro",
				},
			},
		},
		privacy.WithPrivacyFilter(),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	// User sends a message containing sensitive data.
	// The AI only sees fake values; originals are restored in the response.
	resp, err := client.ChatComplete(context.Background(),
		aoeo.BuildRequest([]aoeo.Message{
			{Role: "user", Content: "我叫张三，服务器IP是192.168.1.100，电话13800138000"},
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(resp.Content())
}
