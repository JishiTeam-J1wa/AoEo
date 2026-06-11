package privacy

import (
	"context"
	"testing"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/privacy/store"
)

func TestGateway_BeforeRequest(t *testing.T) {
	pebbleStore, _ := store.OpenPebble(t.TempDir() + "/privacy")

	gen := NewFakeGenerator(42)
	detector := &mockDetector{
		spans: []Span{
			{Label: EntityIP, Original: "192.168.1.1", Score: 0.99},
		},
	}

	gw, err := NewGateway(GatewayConfig{
		Store:     pebbleStore,
		Generator: gen,
		Detector:  detector,
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	defer gw.Close()

	req := &core.ChatCompletionRequest{
		Messages: []core.Message{
			{Role: "user", Content: "My server IP is 192.168.1.1"},
		},
	}

	if err := gw.BeforeRequest(context.Background(), req); err != nil {
		t.Fatalf("before request: %v", err)
	}

	// Original IP should be replaced.
	if req.Messages[0].Content == "My server IP is 192.168.1.1" {
		t.Fatal("IP was not replaced")
	}
}

func TestGateway_AfterResponse(t *testing.T) {
	pebbleStore, _ := store.OpenPebble(t.TempDir() + "/privacy")

	gen := NewFakeGenerator(42)
	detector := &mockDetector{
		spans: []Span{
			{Label: EntityIP, Original: "192.168.1.1", Score: 0.99},
		},
	}

	gw, err := NewGateway(GatewayConfig{
		Store:     pebbleStore,
		Generator: gen,
		Detector:  detector,
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	defer gw.Close()

	// First pseudonymize to establish mapping.
	req := core.ChatCompletionRequest{
		Messages: []core.Message{
			{Role: "user", Content: "IP is 192.168.1.1"},
		},
	}
	gw.BeforeRequest(context.Background(), &req)

	// Find the fake IP.
	entries, _ := pebbleStore.GetSession(context.Background(), "default")
	if len(entries) == 0 {
		t.Fatal("no mappings")
	}
	fakeIP := entries[0].Fake

	// Simulate AI response with fake IP.
	resp := &core.ChatCompletionResponse{
		Choices: []core.Choice{
			{Message: core.Message{Content: "The IP is " + fakeIP}},
		},
	}

	restored, err := gw.AfterResponse(context.Background(), req, resp, nil)
	if err != nil {
		t.Fatalf("after response: %v", err)
	}

	content := restored.Choices[0].Message.Content
	if content != "The IP is 192.168.1.1" {
		t.Fatalf("expected restored IP, got: %s", content)
	}
}

func TestGateway_ToInterceptor(t *testing.T) {
	pebbleStore, _ := store.OpenPebble(t.TempDir() + "/privacy")

	gw, _ := NewGateway(GatewayConfig{Store: pebbleStore})
	defer gw.Close()

	ic := gw.ToInterceptor()
	if ic.BeforeRequest == nil {
		t.Fatal("BeforeRequest should not be nil")
	}
	if ic.AfterResponse == nil {
		t.Fatal("AfterResponse should not be nil")
	}
	if ic.AfterStreamChunk == nil {
		t.Fatal("AfterStreamChunk should not be nil")
	}
	if ic.AfterStreamDone == nil {
		t.Fatal("AfterStreamDone should not be nil")
	}
}

func TestGateway_NoDetection(t *testing.T) {
	pebbleStore, _ := store.OpenPebble(t.TempDir() + "/privacy")

	gw, _ := NewGateway(GatewayConfig{Store: pebbleStore})
	defer gw.Close()

	req := &core.ChatCompletionRequest{
		Messages: []core.Message{
			{Role: "user", Content: "Hello world, no secrets here."},
		},
	}

	if err := gw.BeforeRequest(context.Background(), req); err != nil {
		t.Fatalf("before request: %v", err)
	}

	if req.Messages[0].Content != "Hello world, no secrets here." {
		t.Fatal("request should be unchanged when no detector configured")
	}
}
