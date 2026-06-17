package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/sashabaranov/go-openai"
)

// TestChatComplete mocks the OpenAI chat completions endpoint and verifies
// that OpenAIProvider.ChatComplete correctly deserializes the response.
func TestChatComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"id": "chatcmpl-test",
			"object": "chat.completion",
			"created": 1234567890,
			"model": "test-model",
			"choices": [{
				"index": 0,
				"message": {"role": "assistant", "content": "Hello!"},
				"finish_reason": "stop"
			}],
			"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
		}`)
	}))
	defer srv.Close()

	p := NewOpenAIProvider(core.ProviderConfig{
		Name:       "test",
		APIKey:     "test-key",
		Endpoint:   srv.URL,
		Model:      "test-model",
		HTTPClient: srv.Client(),
	})

	resp, err := p.ChatComplete(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("ChatComplete error: %v", err)
	}
	if resp.ID != "chatcmpl-test" {
		t.Errorf("expected ID 'chatcmpl-test', got '%s'", resp.ID)
	}
	if resp.Model != "test-model" {
		t.Errorf("expected model 'test-model', got '%s'", resp.Model)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "Hello!" {
		t.Errorf("expected content 'Hello!', got '%s'", resp.Choices[0].Message.Content)
	}
	if resp.Choices[0].Message.Role != "assistant" {
		t.Errorf("expected role 'assistant', got '%s'", resp.Choices[0].Message.Role)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("expected finish reason 'stop', got '%s'", resp.Choices[0].FinishReason)
	}
	if resp.Usage.PromptTokens != 10 {
		t.Errorf("expected prompt tokens 10, got %d", resp.Usage.PromptTokens)
	}
	if resp.Usage.CompletionTokens != 5 {
		t.Errorf("expected completion tokens 5, got %d", resp.Usage.CompletionTokens)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("expected total tokens 15, got %d", resp.Usage.TotalTokens)
	}
	if resp.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
}

// TestChatComplete_SystemPromptOverride verifies that a system prompt override
// is prepended to the messages sent to the API.
func TestChatComplete_SystemPromptOverride(t *testing.T) {
	var receivedMessages []map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)
		if msgs, ok := reqBody["messages"].([]interface{}); ok {
			for _, m := range msgs {
				if msg, ok := m.(map[string]interface{}); ok {
					receivedMessages = append(receivedMessages, msg)
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"id": "chatcmpl-sys",
			"object": "chat.completion",
			"created": 1234567890,
			"model": "test-model",
			"choices": [{"index": 0, "message": {"role": "assistant", "content": "OK"}, "finish_reason": "stop"}],
			"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
		}`)
	}))
	defer srv.Close()

	p := NewOpenAIProvider(core.ProviderConfig{
		Name:       "test",
		APIKey:     "test-key",
		Endpoint:   srv.URL,
		Model:      "test-model",
		HTTPClient: srv.Client(),
	})
	p.SetSystemPrompt("You are a helpful assistant.")

	resp, err := p.ChatComplete(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("ChatComplete error: %v", err)
	}
	if resp.Choices[0].Message.Content != "OK" {
		t.Errorf("unexpected content: %s", resp.Choices[0].Message.Content)
	}

	// The system prompt should have been prepended to the messages.
	if len(receivedMessages) < 2 {
		t.Fatalf("expected at least 2 messages (system + user), got %d", len(receivedMessages))
	}
	if receivedMessages[0]["role"] != "system" {
		t.Errorf("expected first message role 'system', got '%v'", receivedMessages[0]["role"])
	}
	if receivedMessages[0]["content"] != "You are a helpful assistant." {
		t.Errorf("expected system prompt content, got '%v'", receivedMessages[0]["content"])
	}
	if receivedMessages[1]["role"] != "user" {
		t.Errorf("expected second message role 'user', got '%v'", receivedMessages[1]["role"])
	}
}

// TestChatComplete_NoChoices verifies that ChatComplete returns an error when
// the API response contains an empty choices list.
func TestChatComplete_NoChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"id": "chatcmpl-empty",
			"object": "chat.completion",
			"created": 1234567890,
			"model": "test-model",
			"choices": [],
			"usage": {"prompt_tokens": 10, "completion_tokens": 0, "total_tokens": 10}
		}`)
	}))
	defer srv.Close()

	p := NewOpenAIProvider(core.ProviderConfig{
		Name:       "test",
		APIKey:     "test-key",
		Endpoint:   srv.URL,
		Model:      "test-model",
		HTTPClient: srv.Client(),
	})

	_, err := p.ChatComplete(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
	if !strings.Contains(err.Error(), "no choices") {
		t.Fatalf("expected 'no choices' error, got: %v", err)
	}
}

// TestChatComplete_Error verifies that ChatComplete propagates API errors
// and records failure metrics.
func TestChatComplete_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error": {"message": "internal error", "type": "server_error"}}`)
	}))
	defer srv.Close()

	p := NewOpenAIProvider(core.ProviderConfig{
		Name:       "test",
		APIKey:     "test-key",
		Endpoint:   srv.URL,
		Model:      "test-model",
		HTTPClient: srv.Client(),
	})

	_, err := p.ChatComplete(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error for server error response")
	}
	// Verify failure was recorded in health metrics.
	h := p.Health()
	if h.SuccessRate != 0.0 {
		t.Errorf("expected success rate 0.0 after failure, got %f", h.SuccessRate)
	}
}

// TestChatComplete_TemperatureRetry verifies that ChatComplete automatically
// retries without temperature when the API returns a temperature-related error.
func TestChatComplete_TemperatureRetry(t *testing.T) {
	var mu sync.Mutex
	callCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		n := callCount
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			// First request: return temperature error.
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error": {"message": "invalid temperature value", "type": "invalid_request_error"}}`)
			return
		}
		// Second request (retry with temperature=0/omitted): return success.
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"id": "chatcmpl-retry",
			"object": "chat.completion",
			"created": 1234567890,
			"model": "test-model",
			"choices": [{"index": 0, "message": {"role": "assistant", "content": "Retry OK"}, "finish_reason": "stop"}],
			"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
		}`)
	}))
	defer srv.Close()

	p := NewOpenAIProvider(core.ProviderConfig{
		Name:       "test",
		APIKey:     "test-key",
		Endpoint:   srv.URL,
		Model:      "test-model",
		HTTPClient: srv.Client(),
	})

	resp, err := p.ChatComplete(context.Background(), core.ChatCompletionRequest{
		Messages:    []core.Message{{Role: "user", Content: "Hi"}},
		Temperature: 0.7,
	})
	if err != nil {
		t.Fatalf("ChatComplete error: %v", err)
	}
	if resp.Choices[0].Message.Content != "Retry OK" {
		t.Errorf("expected 'Retry OK', got '%s'", resp.Choices[0].Message.Content)
	}

	mu.Lock()
	if callCount != 2 {
		t.Errorf("expected 2 API calls (original + retry), got %d", callCount)
	}
	mu.Unlock()
}

// TestChatCompleteStream mocks an SSE streaming endpoint and verifies that
// ChatCompleteStream correctly emits stream chunks through the channel.
func TestChatCompleteStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher := w.(http.Flusher)

		// First chunk: role and partial content.
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-stream\",\"object\":\"chat.completion.chunk\",\"created\":1234567890,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hello\"},\"finish_reason\":null}]}\n\n")
		flusher.Flush()

		// Second chunk: more content.
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-stream\",\"object\":\"chat.completion.chunk\",\"created\":1234567890,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\" World\"},\"finish_reason\":null}]}\n\n")
		flusher.Flush()

		// Final chunk: finish reason.
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-stream\",\"object\":\"chat.completion.chunk\",\"created\":1234567890,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()

		// End of stream.
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	p := NewOpenAIProvider(core.ProviderConfig{
		Name:       "test",
		APIKey:     "test-key",
		Endpoint:   srv.URL,
		Model:      "test-model",
		HTTPClient: srv.Client(),
	})

	ch, err := p.ChatCompleteStream(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompleteStream error: %v", err)
	}

	var chunks []core.StreamCompletionResponse
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("stream error: %v", chunk.Err)
		}
		chunks = append(chunks, chunk)
	}

	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks, got %d", len(chunks))
	}

	// First chunk should carry the role.
	if chunks[0].Chunk.Delta.Role != "assistant" {
		t.Errorf("expected first chunk role 'assistant', got '%s'", chunks[0].Chunk.Delta.Role)
	}
	if chunks[0].Chunk.Delta.Content != "Hello" {
		t.Errorf("expected first chunk content 'Hello', got '%s'", chunks[0].Chunk.Delta.Content)
	}
	if chunks[0].ID != "chatcmpl-stream" {
		t.Errorf("expected ID 'chatcmpl-stream', got '%s'", chunks[0].ID)
	}
	if chunks[0].Model != "test-model" {
		t.Errorf("expected model 'test-model', got '%s'", chunks[0].Model)
	}

	// Second chunk should carry additional content.
	if chunks[1].Chunk.Delta.Content != " World" {
		t.Errorf("expected second chunk content ' World', got '%s'", chunks[1].Chunk.Delta.Content)
	}

	// Third chunk should have the finish reason.
	if chunks[2].Chunk.FinishReason != "stop" {
		t.Errorf("expected finish reason 'stop', got '%s'", chunks[2].Chunk.FinishReason)
	}

	// Verify Close waits for the stream goroutine to finish.
	if err := p.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}
}

// TestChatCompleteStream_ContextCancelled verifies that cancelling the context
// during an active stream causes the channel to close promptly.
func TestChatCompleteStream_ContextCancelled(t *testing.T) {
	// The server continuously sends events so that the goroutine is always
	// blocked inside stream.Recv() when we cancel the context. This avoids
	// a race where the goroutine checks ctx.Err() between events.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher := w.(http.Flusher)

		for i := 0; ; i++ {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			fmt.Fprintf(w, "data: {\"id\":\"chatcmpl-cancel\",\"object\":\"chat.completion.chunk\",\"created\":1234567890,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"E%d\"},\"finish_reason\":null}]}\n\n", i)
			flusher.Flush()
			time.Sleep(5 * time.Millisecond)
		}
	}))
	defer srv.Close()

	p := NewOpenAIProvider(core.ProviderConfig{
		Name:       "test",
		APIKey:     "test-key",
		Endpoint:   srv.URL,
		Model:      "test-model",
		HTTPClient: srv.Client(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := p.ChatCompleteStream(ctx, core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompleteStream error: %v", err)
	}

	// Read at least one chunk to confirm the stream is active.
	first := <-ch
	if first.Err != nil {
		t.Fatalf("first chunk error: %v", first.Err)
	}
	if first.Chunk.Delta.Content == "" {
		t.Error("expected non-empty content in first chunk")
	}

	// Cancel the context. The goroutine should detect this and close
	// the channel (possibly after sending a cancellation or error message).
	cancel()

	// Drain the channel. It must close within a reasonable timeout.
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()

	select {
	case <-done:
		// Channel closed as expected.
	case <-time.After(5 * time.Second):
		t.Fatal("channel did not close within 5 seconds after context cancel")
	}

	p.Close()
}

// TestBuildStreamDelta_Basic verifies buildStreamDelta with basic role and content.
func TestBuildStreamDelta_Basic(t *testing.T) {
	delta := openai.ChatCompletionStreamChoiceDelta{
		Role:    "assistant",
		Content: "Hello",
	}
	msg := buildStreamDelta(delta)
	if msg.Role != "assistant" {
		t.Errorf("expected role 'assistant', got '%s'", msg.Role)
	}
	if msg.Content != "Hello" {
		t.Errorf("expected content 'Hello', got '%s'", msg.Content)
	}
	if len(msg.ToolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(msg.ToolCalls))
	}
}

// TestBuildStreamDelta_Empty verifies buildStreamDelta with an empty delta.
func TestBuildStreamDelta_Empty(t *testing.T) {
	delta := openai.ChatCompletionStreamChoiceDelta{}
	msg := buildStreamDelta(delta)
	if msg.Role != "" {
		t.Errorf("expected empty role, got '%s'", msg.Role)
	}
	if msg.Content != "" {
		t.Errorf("expected empty content, got '%s'", msg.Content)
	}
}

// TestBuildStreamDelta_ToolCallNilIndex verifies buildStreamDelta with a tool
// call that has a nil Index pointer.
func TestBuildStreamDelta_ToolCallNilIndex(t *testing.T) {
	delta := openai.ChatCompletionStreamChoiceDelta{
		ToolCalls: []openai.ToolCall{
			{
				ID:   "call_1",
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      "get_weather",
					Arguments: `{"city":"Beijing"}`,
				},
				Index: nil,
			},
		},
	}
	msg := buildStreamDelta(delta)
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "call_1" {
		t.Errorf("expected ID 'call_1', got '%s'", tc.ID)
	}
	if tc.Type != "function" {
		t.Errorf("expected type 'function', got '%s'", tc.Type)
	}
	if tc.Function.Name != "get_weather" {
		t.Errorf("expected function name 'get_weather', got '%s'", tc.Function.Name)
	}
	if tc.Function.Arguments != `{"city":"Beijing"}` {
		t.Errorf("unexpected arguments: %s", tc.Function.Arguments)
	}
	// Nil Index should default to 0.
	if tc.Index != 0 {
		t.Errorf("expected Index 0 for nil pointer, got %d", tc.Index)
	}
}

// TestBuildStreamDelta_ToolCallWithIndex verifies buildStreamDelta with a tool
// call that has a non-nil Index pointer.
func TestBuildStreamDelta_ToolCallWithIndex(t *testing.T) {
	idx := 2
	delta := openai.ChatCompletionStreamChoiceDelta{
		ToolCalls: []openai.ToolCall{
			{
				ID:   "call_2",
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      "search",
					Arguments: `{"q":"test"}`,
				},
				Index: &idx,
			},
		},
	}
	msg := buildStreamDelta(delta)
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "call_2" {
		t.Errorf("expected ID 'call_2', got '%s'", tc.ID)
	}
	if tc.Function.Name != "search" {
		t.Errorf("expected function name 'search', got '%s'", tc.Function.Name)
	}
	if tc.Index != 2 {
		t.Errorf("expected Index 2, got %d", tc.Index)
	}
}

// TestListModels_OpenAIProvider verifies that OpenAIProvider.ListModels correctly
// queries the /models endpoint and deserializes the response.
func TestListModels_OpenAIProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"object": "list",
			"data": [
				{"id": "model-1", "object": "model", "created": 1234567890, "owned_by": "org"},
				{"id": "model-2", "object": "model", "created": 1234567890, "owned_by": "org2"}
			]
		}`)
	}))
	defer srv.Close()

	p := NewOpenAIProvider(core.ProviderConfig{
		Name:       "test",
		APIKey:     "test-key",
		Endpoint:   srv.URL,
		Model:      "test-model",
		HTTPClient: srv.Client(),
	})

	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels error: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].ID != "model-1" {
		t.Errorf("expected model-1, got %s", models[0].ID)
	}
	if models[0].OwnedBy != "org" {
		t.Errorf("expected owned_by 'org', got '%s'", models[0].OwnedBy)
	}
	if models[1].ID != "model-2" {
		t.Errorf("expected model-2, got %s", models[1].ID)
	}
	if models[1].OwnedBy != "org2" {
		t.Errorf("expected owned_by 'org2', got '%s'", models[1].OwnedBy)
	}
}

// TestListModels_OpenAIProvider_Error verifies that ListModels propagates errors
// from the /models endpoint.
func TestListModels_OpenAIProvider_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error": {"message": "internal error", "type": "server_error"}}`)
	}))
	defer srv.Close()

	p := NewOpenAIProvider(core.ProviderConfig{
		Name:       "test",
		APIKey:     "test-key",
		Endpoint:   srv.URL,
		Model:      "test-model",
		HTTPClient: srv.Client(),
	})

	_, err := p.ListModels(context.Background())
	if err == nil {
		t.Fatal("expected error for server error response")
	}
	if !strings.Contains(err.Error(), "list models") {
		t.Fatalf("expected 'list models' in error, got: %v", err)
	}
}

// TestZBaseProvider_ListModels verifies that BaseProvider.ListModels (which
// creates a temporary openai.Client internally) correctly queries the /models
// endpoint. This test is named with a "Z" prefix to ensure it runs after
// TestBuildHTTPClient_ProxyFromEnvironment, because BaseProvider.ListModels
// uses http.DefaultTransport which populates the http.ProxyFromEnvironment
// sync.Once cache.
func TestZBaseProvider_ListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"object": "list",
			"data": [
				{"id": "base-model-1", "object": "model", "created": 1234567890, "owned_by": "test-org"}
			]
		}`)
	}))
	defer srv.Close()

	bp := NewBaseProvider(core.ProviderConfig{
		Name:     "test",
		APIKey:   "test-key",
		Endpoint: srv.URL,
		Model:    "m",
	})

	models, err := bp.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels error: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].ID != "base-model-1" {
		t.Errorf("expected base-model-1, got %s", models[0].ID)
	}
	if models[0].OwnedBy != "test-org" {
		t.Errorf("expected owned_by 'test-org', got '%s'", models[0].OwnedBy)
	}
}

// TestDeriveTransport_NilClient verifies that deriveTransport falls back to
// http.DefaultTransport when given a nil client.
func TestDeriveTransport_NilClient(t *testing.T) {
	tr := deriveTransport(nil)
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
	// Should be a clone of http.DefaultTransport, so Proxy should be set.
	if tr.Proxy == nil {
		t.Error("expected Proxy to be set (from DefaultTransport)")
	}
}

// TestDeriveTransport_ClientWithNilTransport verifies that deriveTransport falls
// back to http.DefaultTransport when the client has a nil Transport.
func TestDeriveTransport_ClientWithNilTransport(t *testing.T) {
	client := &http.Client{}
	tr := deriveTransport(client)
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
}

// TestDeriveTransport_CustomRoundTripper verifies that deriveTransport falls back
// to http.DefaultTransport when the client's Transport is not an *http.Transport.
func TestDeriveTransport_CustomRoundTripper(t *testing.T) {
	client := &http.Client{
		Transport: &apiTestCustomRT{},
	}
	tr := deriveTransport(client)
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
	// The returned transport should come from the DefaultTransport fallback.
}

// TestDeriveTransport_WithHTTPTransport verifies that deriveTransport clones
// the client's *http.Transport.
func TestDeriveTransport_WithHTTPTransport(t *testing.T) {
	original := &http.Transport{MaxIdleConns: 42}
	client := &http.Client{Transport: original}

	tr := deriveTransport(client)
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
	if tr.MaxIdleConns != 42 {
		t.Errorf("expected MaxIdleConns 42, got %d", tr.MaxIdleConns)
	}
	// Should be a clone, not the same pointer.
	if tr == original {
		t.Error("expected a clone, got the same pointer")
	}
}

// TestDeriveTransport_NonDefaultTransport verifies the minimal transport fallback
// when http.DefaultTransport is replaced with a non-*http.Transport.
func TestDeriveTransport_NonDefaultTransport(t *testing.T) {
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()

	http.DefaultTransport = &apiTestCustomRT{}

	tr := deriveTransport(nil)
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
	// Verify the proxy function is set (use the raw httpproxy to avoid
	// polluting the http.ProxyFromEnvironment cache used by other tests).
	if tr.Proxy == nil {
		t.Error("expected a proxy function on the minimal transport")
	}
}

// TestOpenAIProvider_Close_WaitsForStreams verifies that Close blocks until
// all stream goroutines have completed.
func TestOpenAIProvider_Close_WaitsForStreams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher := w.(http.Flusher)

		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-close\",\"object\":\"chat.completion.chunk\",\"created\":1234567890,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"A\"},\"finish_reason\":null}]}\n\n")
		flusher.Flush()

		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	p := NewOpenAIProvider(core.ProviderConfig{
		Name:       "test",
		APIKey:     "test-key",
		Endpoint:   srv.URL,
		Model:      "test-model",
		HTTPClient: srv.Client(),
	})

	ch, err := p.ChatCompleteStream(context.Background(), core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompleteStream error: %v", err)
	}

	// Drain the channel so the goroutine can finish.
	for range ch {
	}

	// Close should return quickly now that the goroutine is done.
	done := make(chan error, 1)
	go func() {
		done <- p.Close()
	}()

	select {
	case closeErr := <-done:
		if closeErr != nil {
			t.Fatalf("Close error: %v", closeErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return within 5 seconds")
	}
}

// apiTestCustomRT is a minimal http.RoundTripper for testing deriveTransport
// with a non-*http.Transport.
type apiTestCustomRT struct{}

func (c *apiTestCustomRT) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("not implemented")
}
