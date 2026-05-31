package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/providers"
)

// mockStreamProv is a provider that supports streaming for tests.
type mockStreamProv struct {
	mockProv
	streamCh   chan core.StreamCompletionResponse
	streamErr  error
	streamReq  *core.ChatCompletionRequest
}

func (m *mockStreamProv) ChatCompleteStream(_ context.Context, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error) {
	m.mu.Lock()
	m.calls++
	if m.streamReq != nil {
		*m.streamReq = req
	}
	m.mu.Unlock()
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	if m.streamCh != nil {
		return m.streamCh, nil
	}
	ch := make(chan core.StreamCompletionResponse, 4)
	ch <- core.StreamCompletionResponse{Chunk: core.StreamChunk{Delta: core.Message{Content: "hello"}}}
	ch <- core.StreamCompletionResponse{Chunk: core.StreamChunk{Delta: core.Message{Content: " world"}, FinishReason: "stop"}}
	close(ch)
	return ch, nil
}

func TestScheduler_ChatCompleteStream(t *testing.T) {
	p := &mockStreamProv{mockProv: mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}}}
	s := NewScheduler(p)

	ch, err := s.ChatCompleteStream(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var msgs []string
	for msg := range ch {
		if msg.Err != nil {
			t.Fatalf("unexpected stream error: %v", msg.Err)
		}
		msgs = append(msgs, msg.Chunk.Delta.Content)
	}
	if strings.Join(msgs, "") != "hello world" {
		t.Fatalf("unexpected content: %v", msgs)
	}
}

func TestScheduler_ChatCompleteStream_NoAvailable(t *testing.T) {
	p := &mockStreamProv{mockProv: mockProv{name: "p1", available: false, config: core.ProviderConfig{MaxConcurrent: 1}}}
	s := NewScheduler(p)

	_, err := s.ChatCompleteStream(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, ErrNoAvailableProvider) {
		t.Fatalf("expected ErrNoAvailableProvider, got %v", err)
	}
}

func TestScheduler_ChatCompleteStream_Closed(t *testing.T) {
	p := &mockStreamProv{mockProv: mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}}}
	s := NewScheduler(p)
	s.Close()

	_, err := s.ChatCompleteStream(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, ErrSchedulerClosed) {
		t.Fatalf("expected ErrSchedulerClosed, got %v", err)
	}
}

func TestScheduler_ChatCompleteStream_ProviderError(t *testing.T) {
	p := &mockStreamProv{mockProv: mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}}, streamErr: errors.New("stream fail")}
	s := NewScheduler(p)

	_, err := s.ChatCompleteStream(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestScheduler_ChatCompleteStream_ContextCancel(t *testing.T) {
	p := &mockStreamProv{mockProv: mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}}}
	s := NewScheduler(p)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := s.ChatCompleteStream(ctx, core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Cancel immediately
	cancel()

	// Drain the channel to avoid goroutine leak
	for range ch {
	}
	// Give goroutine time to clean up
	time.Sleep(50 * time.Millisecond)
}

func TestScheduler_ChatCompleteStream_Interceptor(t *testing.T) {
	p := &mockStreamProv{mockProv: mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}}}

	var beforeCalled bool
	ic := core.Interceptor{
		BeforeRequest: func(ctx context.Context, req *core.ChatCompletionRequest) error {
			beforeCalled = true
			req.Tags = append(req.Tags, "stream-tag")
			return nil
		},
	}

	s := NewSchedulerWithOptions([]providers.Provider{p}, WithInterceptors(ic))
	ch, err := s.ChatCompleteStream(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for range ch {
	}
	if !beforeCalled {
		t.Fatal("expected BeforeRequest interceptor to be called")
	}
}

func TestScheduler_ChatCompleteStream_InterceptorBlock(t *testing.T) {
	p := &mockStreamProv{mockProv: mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}}}

	ic := core.Interceptor{
		BeforeRequest: func(ctx context.Context, req *core.ChatCompletionRequest) error {
			return errors.New("blocked")
		},
	}

	s := NewSchedulerWithOptions([]providers.Provider{p}, WithInterceptors(ic))
	_, err := s.ChatCompleteStream(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil || err.Error() != "blocked" {
		t.Fatalf("expected blocked error, got %v", err)
	}
}

func TestScheduler_ChatCompleteStream_SlowConsumer(t *testing.T) {
	// Producer sends faster than consumer; verify buffered channel decouples them.
	ch := make(chan core.StreamCompletionResponse, 20)
	for i := 0; i < 10; i++ {
		ch <- core.StreamCompletionResponse{Chunk: core.StreamChunk{Delta: core.Message{Content: "x"}}}
	}
	close(ch)

	p := &mockStreamProv{mockProv: mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}}, streamCh: ch}
	s := NewScheduler(p)

	result, err := s.ChatCompleteStream(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	count := 0
	for range result {
		count++
		// Simulate slow consumer
		time.Sleep(5 * time.Millisecond)
	}
	if count != 10 {
		t.Fatalf("expected 10 messages, got %d", count)
	}
}
