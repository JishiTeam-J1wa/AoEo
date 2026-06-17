package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
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

func TestScheduler_ChatCompleteStream_AfterStreamChunk(t *testing.T) {
	ch := make(chan core.StreamCompletionResponse, 4)
	ch <- core.StreamCompletionResponse{Chunk: core.StreamChunk{Delta: core.Message{Content: "hello"}}}
	ch <- core.StreamCompletionResponse{Chunk: core.StreamChunk{Delta: core.Message{Content: " world"}, FinishReason: "stop"}}
	close(ch)

	var chunks []string
	ic := core.Interceptor{
		AfterStreamChunk: func(_ context.Context, _ core.ChatCompletionRequest, chunk *core.StreamChunk) error {
			chunks = append(chunks, chunk.Delta.Content)
			chunk.Delta.Content = "[" + chunk.Delta.Content + "]"
			return nil
		},
	}

	p := &mockStreamProv{mockProv: mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}}, streamCh: ch}
	s := NewSchedulerWithOptions([]providers.Provider{p}, WithInterceptors(ic))

	result, err := s.ChatCompleteStream(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var contents []string
	for msg := range result {
		if msg.Err != nil {
			t.Fatalf("unexpected stream error: %v", msg.Err)
		}
		contents = append(contents, msg.Chunk.Delta.Content)
	}

	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks intercepted, got %d", len(chunks))
	}
	if strings.Join(contents, "") != "[hello][ world]" {
		t.Fatalf("unexpected wrapped content: %v", contents)
	}
}

func TestScheduler_ChatCompleteStream_AfterStreamChunkAbort(t *testing.T) {
	ch := make(chan core.StreamCompletionResponse, 4)
	ch <- core.StreamCompletionResponse{Chunk: core.StreamChunk{Delta: core.Message{Content: "hello"}}}
	ch <- core.StreamCompletionResponse{Chunk: core.StreamChunk{Delta: core.Message{Content: " world"}, FinishReason: "stop"}}
	close(ch)

	ic := core.Interceptor{
		AfterStreamChunk: func(_ context.Context, _ core.ChatCompletionRequest, chunk *core.StreamChunk) error {
			if chunk.Delta.Content == "hello" {
				return fmt.Errorf("blocked by interceptor")
			}
			return nil
		},
	}

	p := &mockStreamProv{mockProv: mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}}, streamCh: ch}
	s := NewSchedulerWithOptions([]providers.Provider{p}, WithInterceptors(ic))

	result, err := s.ChatCompleteStream(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var gotErr bool
	for msg := range result {
		if msg.Err != nil {
			gotErr = true
			if msg.Err.Error() != "blocked by interceptor" {
				t.Fatalf("unexpected error: %v", msg.Err)
			}
		}
	}
	if !gotErr {
		t.Fatal("expected stream to abort with interceptor error")
	}
}

func TestScheduler_ChatCompleteStream_AfterStreamDone(t *testing.T) {
	ch := make(chan core.StreamCompletionResponse, 4)
	ch <- core.StreamCompletionResponse{Chunk: core.StreamChunk{Delta: core.Message{Content: "done"}, FinishReason: "stop"}}
	close(ch)

	var doneCalled bool
	var doneErr error
	ic := core.Interceptor{
		AfterStreamDone: func(_ context.Context, _ core.ChatCompletionRequest, err error) error {
			doneCalled = true
			doneErr = err
			return nil
		},
	}

	p := &mockStreamProv{mockProv: mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}}, streamCh: ch}
	s := NewSchedulerWithOptions([]providers.Provider{p}, WithInterceptors(ic))

	result, err := s.ChatCompleteStream(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for range result {
	}

	if !doneCalled {
		t.Fatal("expected AfterStreamDone to be called")
	}
	if doneErr != nil {
		t.Fatalf("expected nil final error, got %v", doneErr)
	}
}

func TestScheduler_ChatCompleteStream_AfterStreamDoneWithError(t *testing.T) {
	ch := make(chan core.StreamCompletionResponse, 4)
	ch <- core.StreamCompletionResponse{Err: fmt.Errorf("stream error")}
	close(ch)

	var doneCalled bool
	var doneErr error
	ic := core.Interceptor{
		AfterStreamDone: func(_ context.Context, _ core.ChatCompletionRequest, err error) error {
			doneCalled = true
			doneErr = err
			return nil
		},
	}

	p := &mockStreamProv{mockProv: mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}}, streamCh: ch}
	s := NewSchedulerWithOptions([]providers.Provider{p}, WithInterceptors(ic))

	result, err := s.ChatCompleteStream(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for range result {
	}

	if !doneCalled {
		t.Fatal("expected AfterStreamDone to be called")
	}
	if doneErr == nil || doneErr.Error() != "stream error" {
		t.Fatalf("expected stream error, got %v", doneErr)
	}
}

// ---------------------------------------------------------------------------
// ParseSSE tests
// ---------------------------------------------------------------------------

func TestParseSSE_Normal(t *testing.T) {
	input := "data: hello\ndata: world\ndata: [DONE]\n"
	r := strings.NewReader(input)
	ch := ParseSSE(r)

	var chunks []string
	for chunk := range ch {
		chunks = append(chunks, chunk.Delta.Content)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d: %v", len(chunks), chunks)
	}
	if chunks[0] != "hello" || chunks[1] != "world" {
		t.Fatalf("unexpected chunks: %v", chunks)
	}
}

func TestParseSSE_EmptyInput(t *testing.T) {
	r := strings.NewReader("")
	ch := ParseSSE(r)

	var chunks []core.StreamChunk
	for chunk := range ch {
		chunks = append(chunks, chunk)
	}
	if len(chunks) != 0 {
		t.Fatalf("expected 0 chunks for empty input, got %d", len(chunks))
	}
}

func TestParseSSE_InvalidFormat(t *testing.T) {
	// Lines without "data: " prefix should be skipped
	input := "event: message\nid: 1\nretry: 1000\n:comment\ndata: valid\n\n"
	r := strings.NewReader(input)
	ch := ParseSSE(r)

	var chunks []string
	for chunk := range ch {
		chunks = append(chunks, chunk.Delta.Content)
	}
	if len(chunks) != 1 || chunks[0] != "valid" {
		t.Fatalf("expected only 'valid' chunk, got %v", chunks)
	}
}

func TestParseSSE_DoneMarkerClosesChannel(t *testing.T) {
	input := "data: first\ndata: [DONE]\ndata: should-not-appear\n"
	r := strings.NewReader(input)
	ch := ParseSSE(r)

	var chunks []string
	for chunk := range ch {
		chunks = append(chunks, chunk.Delta.Content)
	}
	if len(chunks) != 1 || chunks[0] != "first" {
		t.Fatalf("expected only 'first' chunk before [DONE], got %v", chunks)
	}
}

func TestParseSSE_ReadError(t *testing.T) {
	r := &errReader{err: fmt.Errorf("read failed")}
	ch := ParseSSE(r)

	var gotErr bool
	for chunk := range ch {
		if strings.Contains(chunk.Delta.Content, "SSE parse error") {
			gotErr = true
		}
	}
	if !gotErr {
		t.Fatal("expected SSE parse error chunk for read error")
	}
}

// errReader is an io.Reader that always returns an error.
type errReader struct {
	err error
}

func (e *errReader) Read(_ []byte) (int, error) {
	return 0, e.err
}

// ---------------------------------------------------------------------------
// ParseSSEWithContext tests
// ---------------------------------------------------------------------------

func TestParseSSEWithContext_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Create a reader that blocks until cancelled
	pr, pw := io.Pipe()

	ch := ParseSSEWithContext(ctx, pr)

	// Cancel the context immediately
	cancel()

	// Close the writer so the goroutine can exit
	pw.Close()

	// Drain channel; should close quickly after cancel
	timeout := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // channel closed as expected
			}
		case <-timeout:
			t.Fatal("timed out waiting for ParseSSEWithContext channel to close after cancel")
		}
	}
}

func TestParseSSEWithContext_ContextCancelMidStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Write some data then keep the pipe open
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte("data: chunk1\n"))
		// Give time for chunk to be read, then cancel
		time.Sleep(50 * time.Millisecond)
		cancel()
		pw.Close()
	}()

	ch := ParseSSEWithContext(ctx, pr)

	var chunks []string
	for chunk := range ch {
		chunks = append(chunks, chunk.Delta.Content)
	}
	// Should have at most 1 chunk before cancellation
	if len(chunks) > 1 {
		t.Fatalf("expected at most 1 chunk before cancel, got %d", len(chunks))
	}
}

// ---------------------------------------------------------------------------
// ChatCompleteStreamWithRouter tests
// ---------------------------------------------------------------------------

func TestScheduler_ChatCompleteStreamWithRouter(t *testing.T) {
	p := &mockStreamProv{mockProv: mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}}}
	s := NewScheduler(p)

	router := &core.PrimaryRouter{}
	ch, err := s.ChatCompleteStreamWithRouter(context.Background(), router, core.ChatCompletionRequest{
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

func TestScheduler_ChatCompleteStreamWithRouter_Closed(t *testing.T) {
	p := &mockStreamProv{mockProv: mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}}}
	s := NewScheduler(p)
	s.Close()

	router := &core.PrimaryRouter{}
	_, err := s.ChatCompleteStreamWithRouter(context.Background(), router, core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, ErrSchedulerClosed) {
		t.Fatalf("expected ErrSchedulerClosed, got %v", err)
	}
}

func TestScheduler_ChatCompleteStreamWithRouter_NoAvailable(t *testing.T) {
	p := &mockStreamProv{mockProv: mockProv{name: "p1", available: false, config: core.ProviderConfig{MaxConcurrent: 1}}}
	s := NewScheduler(p)

	router := &core.PrimaryRouter{}
	_, err := s.ChatCompleteStreamWithRouter(context.Background(), router, core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, ErrNoAvailableProvider) {
		t.Fatalf("expected ErrNoAvailableProvider, got %v", err)
	}
}

func TestScheduler_ChatCompleteStream_WithPromptInjector(t *testing.T) {
	var capturedReq core.ChatCompletionRequest
	p := &mockStreamProv{
		mockProv:  mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}},
		streamReq: &capturedReq,
	}
	s := NewScheduler(p)

	pi := NewPromptInjector()
	pi.AddTemplate(PromptTemplate{
		Provider: "p1",
		Model:    "m1",
		Position: "system",
		Content:  "You are a test bot.",
	})
	s.SetPromptInjector(pi)

	ch, err := s.ChatCompleteStream(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for range ch {
	}

	// Verify prompt was injected: system message should be prepended
	found := false
	for _, m := range capturedReq.Messages {
		if m.Role == "system" && strings.Contains(m.Content, "test bot") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected system prompt to be injected, got messages: %+v", capturedReq.Messages)
	}
}

func TestScheduler_ChatCompleteStream_SemaphoreReleaseOnError(t *testing.T) {
	p := &mockStreamProv{
		mockProv:  mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1}},
		streamErr: errors.New("stream fail"),
	}
	s := NewScheduler(p)

	// First call should fail with stream error
	_, err := s.ChatCompleteStream(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}

	// Semaphore should be released, so a second call should also succeed (or fail with same error, not semaphore error)
	_, err = s.ChatCompleteStream(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error on second call too")
	}
	if err.Error() != "stream fail" {
		t.Fatalf("expected 'stream fail' error, got: %v", err)
	}
}

func TestScheduler_ChatCompleteStream_SemaphoreReleaseAfterConsume(t *testing.T) {
	p := &mockStreamProv{mockProv: mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "m1"}}}
	s := NewScheduler(p)

	// First stream: consume fully
	ch, err := s.ChatCompleteStream(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for range ch {
	}

	// Give goroutine time to release semaphore
	time.Sleep(50 * time.Millisecond)

	// Second stream should succeed (semaphore released)
	ch2, err := s.ChatCompleteStream(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("expected second stream to succeed after semaphore release, got: %v", err)
	}
	for range ch2 {
	}
}

func TestScheduler_ChatCompleteStream_DefaultModelFill(t *testing.T) {
	var capturedReq core.ChatCompletionRequest
	p := &mockStreamProv{
		mockProv:  mockProv{name: "p1", available: true, config: core.ProviderConfig{MaxConcurrent: 1, Model: "default-model"}},
		streamReq: &capturedReq,
	}
	s := NewScheduler(p)

	ch, err := s.ChatCompleteStream(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
		// Model is empty, should be filled from provider config
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for range ch {
	}

	if capturedReq.Model != "default-model" {
		t.Fatalf("expected model to be filled with 'default-model', got '%s'", capturedReq.Model)
	}
}
