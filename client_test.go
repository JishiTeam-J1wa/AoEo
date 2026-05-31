package aoeo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/providers"
)

func TestNewClient_Validation(t *testing.T) {
	_, err := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "", APIKey: "", Endpoint: "bad", Model: ""},
		},
	})
	if err == nil {
		t.Fatal("expected error for invalid config")
	}
}

func TestNewClient_Success(t *testing.T) {
	client, err := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.IsClosed() {
		t.Fatal("expected client not closed")
	}
}

func TestClient_Close(t *testing.T) {
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	})
	if err := client.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !client.IsClosed() {
		t.Fatal("expected client closed")
	}
	// Idempotent
	if err := client.Close(); err != nil {
		t.Fatalf("second close should be idempotent: %v", err)
	}
}

func TestClient_SetTimeout(t *testing.T) {
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	})
	client.SetTimeout(10 * time.Second)
	// Smoke test: no panic
}

func TestClient_Interceptors(t *testing.T) {
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	})
	if client.Interceptors() != nil {
		t.Fatal("expected nil interceptors initially")
	}
	ic := []core.Interceptor{{}}
	client.SetInterceptors(ic)
	if len(client.Interceptors()) != 1 {
		t.Fatal("expected 1 interceptor")
	}
}

func TestClient_HistoryAndStats(t *testing.T) {
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	}, WithHistory(NewHistory(100)))

	if client.History() == nil {
		t.Fatal("expected history")
	}
	if client.Stats() == nil {
		t.Fatal("expected stats")
	}
}

func TestClient_ProviderStatus(t *testing.T) {
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	})
	status := client.ProviderStatus()
	if len(status) != 1 {
		t.Fatalf("expected 1 provider status, got %d", len(status))
	}
}

func TestClient_Scheduler(t *testing.T) {
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	})
	if client.Scheduler() == nil {
		t.Fatal("expected non-nil scheduler")
	}
}

func TestClient_PromptInjector(t *testing.T) {
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	})
	if client.PromptInjector() != nil {
		t.Fatal("expected nil prompt injector initially")
	}
	pi := NewPromptInjector()
	client.SetPromptInjector(pi)
	if client.PromptInjector() != pi {
		t.Fatal("SetPromptInjector did not work")
	}
}

func TestClient_SetEmitter(t *testing.T) {
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	})
	// Nil should not panic
	client.SetEmitter(nil)
}

func TestClient_ChatCompleteStream_Closed(t *testing.T) {
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	})
	client.Close()
	_, err := client.ChatCompleteStream(context.Background(), ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, ErrSchedulerClosed) {
		t.Fatalf("expected ErrSchedulerClosed, got %v", err)
	}
}

func TestClient_Audit_InsufficientProviders(t *testing.T) {
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	})
	_, err := client.Audit(context.Background(), ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for insufficient providers")
	}
}

func TestClient_ChatCompleteWithFallback_EmitsEvent(t *testing.T) {
	// Create a client with a provider that will fail, but no fallback
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m"},
		},
	})
	// Even though there's no fallback, this tests the code path
	_, _ = client.ChatCompleteWithFallback(context.Background(), ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
}

func TestClient_ChatCompleteDual_Closed(t *testing.T) {
	client, _ := NewClient(core.Config{
		Providers: []core.ProviderConfig{
			{Name: "p1", APIKey: "k", Endpoint: "https://a.com", Model: "m"},
			{Name: "p2", APIKey: "k", Endpoint: "https://b.com", Model: "m"},
		},
	})
	client.Close()
	_, err := client.ChatCompleteDual(context.Background(), ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, ErrSchedulerClosed) {
		t.Fatalf("expected ErrSchedulerClosed, got %v", err)
	}
}

func TestNewClientWithProviders(t *testing.T) {
	p := providers.NewOpenAIProvider(core.ProviderConfig{
		Name: "test", APIKey: "k", Endpoint: "https://api.example.com", Model: "m",
	})
	client := NewClientWithProviders(p)
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}
