package main

import (
	"context"
	"fmt"
	"log"
	"time"

	aoeo "github.com/JishiTeam-J1wa/AoEo"
	"github.com/JishiTeam-J1wa/AoEo/privacy"
)

func main() {
	// 1. Load local privacy rules.
	rules, err := privacy.LoadRuleDatabase("privacy_rules.yaml")
	if err != nil {
		log.Fatalf("load rules: %v", err)
	}

	// 2. Create the privacy gateway.
	// In production, you would also configure the OpenAI Privacy Filter model.
	gateway, err := privacy.NewGateway(privacy.GatewayConfig{
		Rules:     privacy.NewRuleEngine(rules),
		Policy:    privacy.ActionPseudonymize,
		SessionTTL: 7 * 24 * time.Hour,
	})
	if err != nil {
		log.Fatalf("new gateway: %v", err)
	}
	defer gateway.Close()

	// 3. Create AoEo client with privacy interceptor.
	client, err := aoeo.NewClient(aoeo.Config{
		Providers: []aoeo.ProviderConfig{
			{
				Name:     "deepseek",
				APIKey:   "sk-xxx",
				Endpoint: "https://api.deepseek.com",
				Model:    "deepseek-v4-pro",
			},
		},
	}, aoeo.WithInterceptors(gateway.ToInterceptor()))
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	// 4. User sends a message containing sensitive data.
	// The user sees and types the original values.
	resp, err := client.ChatComplete(context.Background(),
		aoeo.BuildRequest([]aoeo.Message{
			{Role: "user", Content: "我叫张三，服务器IP是192.168.1.100，电话13800138000，域名www.x1.com"},
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 5. User receives the response with original values restored.
	// The AI never saw the real IP, phone, or domain.
	fmt.Println(resp.Content())
}
