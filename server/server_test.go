package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/internal/engine"
)

// ---------------------------------------------------------------------------
// Mock ChatClient implementation
// ---------------------------------------------------------------------------

type mockClient struct {
	chatCompleteFn             func(ctx context.Context, req core.ChatCompletionRequest) (*core.ChatCompletionResponse, error)
	chatCompleteStreamFn       func(ctx context.Context, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error)
	chatCompleteWithProviderFn func(ctx context.Context, name string, req core.ChatCompletionRequest) (*core.ChatCompletionResponse, error)
	chatCompleteStreamWithProvFn func(ctx context.Context, name string, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error)
	chatCompleteWithFallbackFn func(ctx context.Context, req core.ChatCompletionRequest) (*core.ChatCompletionResponse, error)
	listModelsFn               func(ctx context.Context, name string) ([]core.ModelInfo, error)
	providerStatusFn           func() []core.ProviderStatus
	statsFn                    func() map[string]engine.ProviderStats
	testProviderFn             func(ctx context.Context, name string) error
	closeFn                    func() error
}

func (m *mockClient) ChatComplete(ctx context.Context, req core.ChatCompletionRequest) (*core.ChatCompletionResponse, error) {
	if m.chatCompleteFn != nil {
		return m.chatCompleteFn(ctx, req)
	}
	return &core.ChatCompletionResponse{}, nil
}

func (m *mockClient) ChatCompleteStream(ctx context.Context, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error) {
	if m.chatCompleteStreamFn != nil {
		return m.chatCompleteStreamFn(ctx, req)
	}
	ch := make(chan core.StreamCompletionResponse)
	close(ch)
	return ch, nil
}

func (m *mockClient) ChatCompleteWithProvider(ctx context.Context, name string, req core.ChatCompletionRequest) (*core.ChatCompletionResponse, error) {
	if m.chatCompleteWithProviderFn != nil {
		return m.chatCompleteWithProviderFn(ctx, name, req)
	}
	return &core.ChatCompletionResponse{}, nil
}

func (m *mockClient) ChatCompleteStreamWithProvider(ctx context.Context, name string, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error) {
	if m.chatCompleteStreamWithProvFn != nil {
		return m.chatCompleteStreamWithProvFn(ctx, name, req)
	}
	ch := make(chan core.StreamCompletionResponse)
	close(ch)
	return ch, nil
}

func (m *mockClient) ChatCompleteWithFallback(ctx context.Context, req core.ChatCompletionRequest) (*core.ChatCompletionResponse, error) {
	if m.chatCompleteWithFallbackFn != nil {
		return m.chatCompleteWithFallbackFn(ctx, req)
	}
	return &core.ChatCompletionResponse{}, nil
}

func (m *mockClient) ListModels(ctx context.Context, name string) ([]core.ModelInfo, error) {
	if m.listModelsFn != nil {
		return m.listModelsFn(ctx, name)
	}
	return nil, nil
}

func (m *mockClient) ProviderStatus() []core.ProviderStatus {
	if m.providerStatusFn != nil {
		return m.providerStatusFn()
	}
	return nil
}

func (m *mockClient) Stats() map[string]engine.ProviderStats {
	if m.statsFn != nil {
		return m.statsFn()
	}
	return nil
}

func (m *mockClient) TestProvider(ctx context.Context, name string) error {
	if m.testProviderFn != nil {
		return m.testProviderFn(ctx, name)
	}
	return nil
}

func (m *mockClient) Close() error {
	if m.closeFn != nil {
		return m.closeFn()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helper to build a standard successful response
// ---------------------------------------------------------------------------

func makeSuccessResp() *core.ChatCompletionResponse {
	return &core.ChatCompletionResponse{
		ID:    "chatcmpl-test123",
		Model: "gpt-4",
		Choices: []core.Choice{
			{
				Index: 0,
				Message: core.Message{
					Role:    "assistant",
					Content: "Hello, world!",
				},
				FinishReason: "stop",
			},
		},
		Usage: core.Usage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
		},
		CreatedAt: time.Unix(1700000000, 0),
	}
}

// ===========================================================================
// Converter tests
// ===========================================================================

func TestParseOpenAIRequest_Valid(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4",
		"messages": [{"role": "user", "content": "hello"}],
		"temperature": 0.7,
		"max_tokens": 100
	}`)
	req, err := ParseOpenAIRequest(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Model != "gpt-4" {
		t.Errorf("model = %q, want %q", req.Model, "gpt-4")
	}
	if len(req.Messages) != 1 {
		t.Fatalf("messages len = %d, want 1", len(req.Messages))
	}
	if req.Messages[0].Role != "user" {
		t.Errorf("role = %q, want %q", req.Messages[0].Role, "user")
	}
	if req.Temperature != 0.7 {
		t.Errorf("temperature = %f, want 0.7", req.Temperature)
	}
	if req.MaxTokens != 100 {
		t.Errorf("max_tokens = %d, want 100", req.MaxTokens)
	}
}

func TestParseOpenAIRequest_InvalidJSON(t *testing.T) {
	_, err := ParseOpenAIRequest([]byte(`{bad json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "解析 OpenAI 请求失败") {
		t.Errorf("error = %q, want to contain parse message", err.Error())
	}
}

func TestParseOpenAIRequest_MissingFields(t *testing.T) {
	// Empty object should parse without error (fields are optional in JSON)
	req, err := ParseOpenAIRequest([]byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Model != "" {
		t.Errorf("model = %q, want empty", req.Model)
	}
	if len(req.Messages) != 0 {
		t.Errorf("messages len = %d, want 0", len(req.Messages))
	}
}

func TestToCoreRequest_FullConversion(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4",
		"messages": [
			{"role": "system", "content": "You are helpful."},
			{"role": "user", "content": "Hi"},
			{"role": "assistant", "content": null},
			{"role": "tool", "content": "result", "tool_call_id": "tc1"}
		],
		"temperature": 0.5,
		"max_tokens": 200,
		"top_p": 0.9,
		"presence_penalty": 0.1,
		"frequency_penalty": 0.2,
		"stop": ["END"],
		"seed": 42,
		"stream": false,
		"response_format": {"type": "json_object"},
		"parallel_tool_calls": true,
		"tools": [
			{
				"type": "function",
				"function": {
					"name": "get_weather",
					"description": "Get weather info",
					"parameters": {"type": "object", "properties": {}},
					"strict": true
				}
			}
		],
		"tool_choice": "auto"
	}`)

	openAIReq, err := ParseOpenAIRequest(body)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	coreReq := openAIReq.ToCoreRequest()

	if coreReq.Model != "gpt-4" {
		t.Errorf("model = %q, want %q", coreReq.Model, "gpt-4")
	}
	if len(coreReq.Messages) != 4 {
		t.Fatalf("messages len = %d, want 4", len(coreReq.Messages))
	}

	// System message
	if coreReq.Messages[0].Role != "system" {
		t.Errorf("msg[0].role = %q, want system", coreReq.Messages[0].Role)
	}
	if coreReq.Messages[0].Content != "You are helpful." {
		t.Errorf("msg[0].content = %q", coreReq.Messages[0].Content)
	}

	// User message
	if coreReq.Messages[1].Content != "Hi" {
		t.Errorf("msg[1].content = %q, want Hi", coreReq.Messages[1].Content)
	}

	// Assistant with null content -> empty string
	if coreReq.Messages[2].Content != "" {
		t.Errorf("msg[2].content = %q, want empty", coreReq.Messages[2].Content)
	}

	// Tool message
	if coreReq.Messages[3].ToolCallID != "tc1" {
		t.Errorf("msg[3].tool_call_id = %q, want tc1", coreReq.Messages[3].ToolCallID)
	}

	// Temperature, MaxTokens, etc.
	if coreReq.Temperature != 0.5 {
		t.Errorf("temperature = %f, want 0.5", coreReq.Temperature)
	}
	if coreReq.MaxTokens != 200 {
		t.Errorf("max_tokens = %d, want 200", coreReq.MaxTokens)
	}
	if coreReq.TopP != 0.9 {
		t.Errorf("top_p = %f, want 0.9", coreReq.TopP)
	}

	// Stop as string -> []string
	if len(coreReq.Stop) != 1 || coreReq.Stop[0] != "END" {
		t.Errorf("stop = %v, want [END]", coreReq.Stop)
	}

	// Seed
	if coreReq.Seed == nil || *coreReq.Seed != 42 {
		t.Errorf("seed = %v, want 42", coreReq.Seed)
	}

	// ResponseFormat
	if coreReq.ResponseFormat.Type != "json_object" {
		t.Errorf("response_format.type = %q, want json_object", coreReq.ResponseFormat.Type)
	}

	// ParallelToolCalls
	if !coreReq.ParallelToolCalls {
		t.Error("parallel_tool_calls should be true")
	}

	// Tools
	if len(coreReq.Tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(coreReq.Tools))
	}
	if coreReq.Tools[0].Type != "function" {
		t.Errorf("tool type = %q, want function", coreReq.Tools[0].Type)
	}
	if coreReq.Tools[0].Function == nil {
		t.Fatal("tool function is nil")
	}
	if coreReq.Tools[0].Function.Name != "get_weather" {
		t.Errorf("tool function name = %q", coreReq.Tools[0].Function.Name)
	}
	if !coreReq.Tools[0].Function.Strict {
		t.Error("tool function strict should be true")
	}

	// ToolChoice as string
	if coreReq.ToolChoice != "auto" {
		t.Errorf("tool_choice = %v, want auto", coreReq.ToolChoice)
	}
}

func TestToCoreRequest_StopAsArray(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4",
		"messages": [{"role": "user", "content": "hi"}],
		"stop": ["END", "STOP"]
	}`)
	req, err := ParseOpenAIRequest(body)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	coreReq := req.ToCoreRequest()
	if len(coreReq.Stop) != 2 {
		t.Fatalf("stop len = %d, want 2", len(coreReq.Stop))
	}
	if coreReq.Stop[0] != "END" || coreReq.Stop[1] != "STOP" {
		t.Errorf("stop = %v, want [END STOP]", coreReq.Stop)
	}
}

func TestToCoreRequest_ToolChoiceAsObject(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4",
		"messages": [{"role": "user", "content": "hi"}],
		"tool_choice": {"type": "function", "function": {"name": "get_weather"}}
	}`)
	req, err := ParseOpenAIRequest(body)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	coreReq := req.ToCoreRequest()
	obj, ok := coreReq.ToolChoice.(map[string]any)
	if !ok {
		t.Fatalf("tool_choice type = %T, want map[string]any", coreReq.ToolChoice)
	}
	if obj["type"] != "function" {
		t.Errorf("tool_choice.type = %v, want function", obj["type"])
	}
}

func TestToCoreRequest_ResponseFormatNilSafe(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4",
		"messages": [{"role": "user", "content": "hi"}]
	}`)
	req, err := ParseOpenAIRequest(body)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	coreReq := req.ToCoreRequest()
	if coreReq.ResponseFormat.Type != "" {
		t.Errorf("response_format.type = %q, want empty", coreReq.ResponseFormat.Type)
	}
}

func TestToCoreRequest_ToolWithNullFunction(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4",
		"messages": [{"role": "user", "content": "hi"}],
		"tools": [{"type": "function"}]
	}`)
	req, err := ParseOpenAIRequest(body)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	coreReq := req.ToCoreRequest()
	if len(coreReq.Tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(coreReq.Tools))
	}
	if coreReq.Tools[0].Function != nil {
		t.Error("tool function should be nil when not provided")
	}
}

func TestToCoreRequest_ToolCallsInMessage(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4",
		"messages": [
			{
				"role": "assistant",
				"content": null,
				"tool_calls": [
					{
						"id": "call_1",
						"type": "function",
						"function": {"name": "get_weather", "arguments": "{\"city\":\"NYC\"}"},
						"index": 0
					}
				]
			}
		]
	}`)
	req, err := ParseOpenAIRequest(body)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	coreReq := req.ToCoreRequest()
	msg := coreReq.Messages[0]
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "call_1" {
		t.Errorf("tc.id = %q, want call_1", tc.ID)
	}
	if tc.Function.Name != "get_weather" {
		t.Errorf("tc.function.name = %q", tc.Function.Name)
	}
	if tc.Function.Arguments != `{"city":"NYC"}` {
		t.Errorf("tc.function.arguments = %q", tc.Function.Arguments)
	}
}

func TestCoreResponseToOpenAI_Valid(t *testing.T) {
	resp := makeSuccessResp()
	data := CoreResponseToOpenAI(resp)

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if result["id"] != "chatcmpl-test123" {
		t.Errorf("id = %v", result["id"])
	}
	if result["object"] != "chat.completion" {
		t.Errorf("object = %v", result["object"])
	}
	if result["model"] != "gpt-4" {
		t.Errorf("model = %v", result["model"])
	}

	choices, ok := result["choices"].([]any)
	if !ok || len(choices) != 1 {
		t.Fatalf("choices invalid")
	}
	choice := choices[0].(map[string]any)
	if choice["index"].(float64) != 0 {
		t.Errorf("choice index = %v", choice["index"])
	}
	if choice["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v", choice["finish_reason"])
	}
	msg := choice["message"].(map[string]any)
	if msg["role"] != "assistant" {
		t.Errorf("role = %v", msg["role"])
	}
	if msg["content"] != "Hello, world!" {
		t.Errorf("content = %v", msg["content"])
	}

	usage := result["usage"].(map[string]any)
	if usage["prompt_tokens"].(float64) != 10 {
		t.Errorf("prompt_tokens = %v", usage["prompt_tokens"])
	}
	if usage["total_tokens"].(float64) != 30 {
		t.Errorf("total_tokens = %v", usage["total_tokens"])
	}
}

func TestCoreResponseToOpenAI_WithToolCalls(t *testing.T) {
	resp := &core.ChatCompletionResponse{
		ID:    "chatcmpl-tool",
		Model: "gpt-4",
		Choices: []core.Choice{
			{
				Index: 0,
				Message: core.Message{
					Role: "assistant",
					ToolCalls: []core.ToolCall{
						{
							ID:   "call_abc",
							Type: "function",
							Function: core.FunctionCall{
								Name:      "get_weather",
								Arguments: `{"city":"NYC"}`,
							},
						},
					},
				},
				FinishReason: "tool_calls",
			},
		},
		CreatedAt: time.Unix(1700000000, 0),
	}
	data := CoreResponseToOpenAI(resp)

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	choices := result["choices"].([]any)
	choice := choices[0].(map[string]any)
	msg := choice["message"].(map[string]any)

	toolCalls, ok := msg["tool_calls"].([]any)
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("tool_calls invalid")
	}
	tc := toolCalls[0].(map[string]any)
	if tc["id"] != "call_abc" {
		t.Errorf("tc.id = %v", tc["id"])
	}
	fn := tc["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Errorf("fn.name = %v", fn["name"])
	}
}

func TestCoreStreamChunkToSSE_Regular(t *testing.T) {
	chunk := core.StreamChunk{
		Index: 0,
		Delta: core.Message{
			Role:    "assistant",
			Content: "Hello",
		},
	}
	data := CoreStreamChunkToSSE("id-1", "gpt-4", chunk, nil)
	s := string(data)

	if !strings.HasPrefix(s, "data: ") {
		t.Errorf("missing data: prefix: %q", s)
	}
	if !strings.HasSuffix(s, "\n\n") {
		t.Errorf("missing trailing newlines: %q", s)
	}

	// Parse the JSON between "data: " and "\n\n"
	jsonStr := strings.TrimPrefix(strings.TrimSuffix(s, "\n\n"), "data: ")
	var result map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if result["id"] != "id-1" {
		t.Errorf("id = %v", result["id"])
	}
	if result["object"] != "chat.completion.chunk" {
		t.Errorf("object = %v", result["object"])
	}
	if result["model"] != "gpt-4" {
		t.Errorf("model = %v", result["model"])
	}

	choices := result["choices"].([]any)
	choice := choices[0].(map[string]any)
	if choice["finish_reason"] != nil {
		t.Errorf("finish_reason should be null, got %v", choice["finish_reason"])
	}
	delta := choice["delta"].(map[string]any)
	if delta["content"] != "Hello" {
		t.Errorf("delta.content = %v", delta["content"])
	}

	// No usage when nil
	if _, exists := result["usage"]; exists {
		t.Error("usage should not be present when nil")
	}
}

func TestCoreStreamChunkToSSE_WithUsage(t *testing.T) {
	chunk := core.StreamChunk{
		Index: 0,
		Delta: core.Message{},
	}
	usage := &core.Usage{
		PromptTokens:     5,
		CompletionTokens: 10,
		TotalTokens:      15,
	}
	data := CoreStreamChunkToSSE("id-2", "gpt-4", chunk, usage)
	s := string(data)

	jsonStr := strings.TrimPrefix(strings.TrimSuffix(s, "\n\n"), "data: ")
	var result map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	usageMap, ok := result["usage"].(map[string]any)
	if !ok {
		t.Fatal("usage should be present")
	}
	if usageMap["total_tokens"].(float64) != 15 {
		t.Errorf("total_tokens = %v", usageMap["total_tokens"])
	}
}

func TestCoreStreamChunkToSSE_WithFinishReason(t *testing.T) {
	chunk := core.StreamChunk{
		Index:        0,
		Delta:        core.Message{},
		FinishReason: "stop",
	}
	data := CoreStreamChunkToSSE("id-3", "gpt-4", chunk, nil)
	s := string(data)

	jsonStr := strings.TrimPrefix(strings.TrimSuffix(s, "\n\n"), "data: ")
	var result map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	choices := result["choices"].([]any)
	choice := choices[0].(map[string]any)
	if choice["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v, want stop", choice["finish_reason"])
	}
}

func TestCoreStreamChunkToSSE_WithToolCalls(t *testing.T) {
	chunk := core.StreamChunk{
		Index: 0,
		Delta: core.Message{
			ToolCalls: []core.ToolCall{
				{
					ID:    "call_1",
					Type:  "function",
					Index: 0,
					Function: core.FunctionCall{
						Name:      "search",
						Arguments: "{}",
					},
				},
			},
		},
	}
	data := CoreStreamChunkToSSE("id-tc", "gpt-4", chunk, nil)
	s := string(data)

	jsonStr := strings.TrimPrefix(strings.TrimSuffix(s, "\n\n"), "data: ")
	var result map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	choices := result["choices"].([]any)
	choice := choices[0].(map[string]any)
	delta := choice["delta"].(map[string]any)
	toolCalls := delta["tool_calls"].([]any)
	if len(toolCalls) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(toolCalls))
	}
	tc := toolCalls[0].(map[string]any)
	if tc["id"] != "call_1" {
		t.Errorf("tc.id = %v", tc["id"])
	}
}

// ===========================================================================
// Middleware tests
// ===========================================================================

func TestAPIKeyAuth_ValidKey(t *testing.T) {
	handler := APIKeyAuth("my-secret")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer my-secret")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if rr.Body.String() != "ok" {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestAPIKeyAuth_MissingKey(t *testing.T) {
	handler := APIKeyAuth("my-secret")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "missing Authorization") {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestAPIKeyAuth_WrongKey(t *testing.T) {
	handler := APIKeyAuth("my-secret")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid API key") {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestAPIKeyAuth_InvalidFormat(t *testing.T) {
	handler := APIKeyAuth("my-secret")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Basic abc123")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid Authorization format") {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestAPIKeyAuth_EmptyKeyDevMode(t *testing.T) {
	handler := APIKeyAuth("")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("dev-mode"))
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if rr.Body.String() != "dev-mode" {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestCORS_HeadersSet(t *testing.T) {
	handler := CORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("CORS origin = %q", rr.Header().Get("Access-Control-Allow-Origin"))
	}
	if rr.Header().Get("Access-Control-Allow-Methods") != "GET, POST, PUT, DELETE, OPTIONS" {
		t.Errorf("CORS methods = %q", rr.Header().Get("Access-Control-Allow-Methods"))
	}
	if rr.Header().Get("Access-Control-Allow-Headers") != "Content-Type, Authorization" {
		t.Errorf("CORS headers = %q", rr.Header().Get("Access-Control-Allow-Headers"))
	}
}

func TestCORS_OptionsReturns204(t *testing.T) {
	handler := CORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("should not reach"))
	}))

	req := httptest.NewRequest("OPTIONS", "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "should not reach") {
		t.Error("next handler should not be called for OPTIONS")
	}
}

func TestRequestLogger_CapturesStatusCode(t *testing.T) {
	handler := RequestLogger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestChain_FullMiddlewareChain(t *testing.T) {
	innerCalled := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("chained"))
	})

	handler := Chain(inner, "test-key")

	// Valid request with correct API key
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if !innerCalled {
		t.Error("inner handler was not called")
	}
	if rr.Body.String() != "chained" {
		t.Errorf("body = %q", rr.Body.String())
	}
	// Verify CORS headers are set by chain
	if rr.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("CORS headers not set by chain")
	}
}

func TestChain_RejectsInvalidKey(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := Chain(inner, "test-key")

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

// ===========================================================================
// Handler tests
// ===========================================================================

func newTestServer(client ChatClient) *Server {
	return NewServer(client)
}

func TestChatHandler_ValidRequest(t *testing.T) {
	client := &mockClient{
		chatCompleteFn: func(ctx context.Context, req core.ChatCompletionRequest) (*core.ChatCompletionResponse, error) {
			if req.Model != "gpt-4" {
				t.Errorf("model = %q, want gpt-4", req.Model)
			}
			return makeSuccessResp(), nil
		},
	}
	srv := newTestServer(client)

	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv.ChatHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp["id"] != "chatcmpl-test123" {
		t.Errorf("id = %v", resp["id"])
	}
}

func TestChatHandler_InvalidJSON(t *testing.T) {
	srv := newTestServer(&mockClient{})

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{bad"))
	rr := httptest.NewRecorder()

	srv.ChatHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
	var resp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &resp)
	errObj := resp["error"].(map[string]any)
	if errObj["type"] != "invalid_request_error" {
		t.Errorf("error type = %v", errObj["type"])
	}
}

func TestChatHandler_ClientError(t *testing.T) {
	client := &mockClient{
		chatCompleteFn: func(ctx context.Context, req core.ChatCompletionRequest) (*core.ChatCompletionResponse, error) {
			return nil, errors.New("provider unavailable")
		},
	}
	srv := newTestServer(client)

	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	rr := httptest.NewRecorder()

	srv.ChatHandler(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	var resp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &resp)
	errObj := resp["error"].(map[string]any)
	if !strings.Contains(errObj["message"].(string), "provider unavailable") {
		t.Errorf("error message = %v", errObj["message"])
	}
}

func TestChatHandler_Stream(t *testing.T) {
	// When stream=true, ChatHandler should delegate to StreamHandler
	client := &mockClient{
		chatCompleteStreamFn: func(ctx context.Context, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error) {
			ch := make(chan core.StreamCompletionResponse, 2)
			ch <- core.StreamCompletionResponse{
				ID:    "stream-1",
				Model: "gpt-4",
				Chunk: core.StreamChunk{
					Index: 0,
					Delta: core.Message{Role: "assistant", Content: "Hi"},
				},
			}
			ch <- core.StreamCompletionResponse{
				ID:    "stream-1",
				Model: "gpt-4",
				Chunk: core.StreamChunk{
					Index:        0,
					Delta:        core.Message{},
					FinishReason: "stop",
				},
				Usage: core.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
			}
			close(ch)
			return ch, nil
		},
	}
	srv := newTestServer(client)

	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hello"}],"stream":true}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	rr := httptest.NewRecorder()

	srv.ChatHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content-type = %q, want text/event-stream", ct)
	}
	bodyStr := rr.Body.String()
	if !strings.Contains(bodyStr, "data: ") {
		t.Error("missing SSE data prefix")
	}
	if !strings.Contains(bodyStr, "data: [DONE]") {
		t.Error("missing [DONE] marker")
	}
}

func TestStreamHandler_Success(t *testing.T) {
	client := &mockClient{
		chatCompleteStreamFn: func(ctx context.Context, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error) {
			ch := make(chan core.StreamCompletionResponse, 1)
			ch <- core.StreamCompletionResponse{
				ID:    "stream-2",
				Model: "gpt-4",
				Chunk: core.StreamChunk{
					Index:        0,
					Delta:        core.Message{Role: "assistant", Content: "World"},
					FinishReason: "stop",
				},
				Usage: core.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
			}
			close(ch)
			return ch, nil
		},
	}
	srv := newTestServer(client)

	openAIReq := &OpenAIRequest{
		Model:    "gpt-4",
		Messages: []OpenAIMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
		Stream:   true,
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	rr := httptest.NewRecorder()

	srv.StreamHandler(rr, req, openAIReq)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if rr.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("content-type = %q", rr.Header().Get("Content-Type"))
	}
	if rr.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("cache-control = %q", rr.Header().Get("Cache-Control"))
	}
	if rr.Header().Get("Connection") != "keep-alive" {
		t.Errorf("connection = %q", rr.Header().Get("Connection"))
	}

	bodyStr := rr.Body.String()
	lines := strings.Split(strings.TrimSpace(bodyStr), "\n")
	// Should have at least one data line and the [DONE] line
	if len(lines) < 2 {
		t.Fatalf("too few lines: %d", len(lines))
	}
	if !strings.Contains(bodyStr, "data: [DONE]") {
		t.Error("missing [DONE]")
	}
}

func TestStreamHandler_ClientError(t *testing.T) {
	client := &mockClient{
		chatCompleteStreamFn: func(ctx context.Context, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error) {
			return nil, errors.New("stream init failed")
		},
	}
	srv := newTestServer(client)

	openAIReq := &OpenAIRequest{
		Model:    "gpt-4",
		Messages: []OpenAIMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
		Stream:   true,
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	rr := httptest.NewRecorder()

	srv.StreamHandler(rr, req, openAIReq)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "stream init failed") {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestStreamHandler_StreamError(t *testing.T) {
	client := &mockClient{
		chatCompleteStreamFn: func(ctx context.Context, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error) {
			ch := make(chan core.StreamCompletionResponse, 1)
			ch <- core.StreamCompletionResponse{
				Err: errors.New("upstream timeout"),
			}
			close(ch)
			return ch, nil
		},
	}
	srv := newTestServer(client)

	openAIReq := &OpenAIRequest{
		Model:    "gpt-4",
		Messages: []OpenAIMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
		Stream:   true,
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	rr := httptest.NewRecorder()

	srv.StreamHandler(rr, req, openAIReq)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (SSE error sent after headers)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "upstream timeout") {
		t.Errorf("body should contain error message: %q", rr.Body.String())
	}
}

func TestModelsHandler_ReturnsModels(t *testing.T) {
	client := &mockClient{
		providerStatusFn: func() []core.ProviderStatus {
			return []core.ProviderStatus{
				{Name: "deepseek", Available: true},
				{Name: "openai", Available: true},
			}
		},
		listModelsFn: func(ctx context.Context, name string) ([]core.ModelInfo, error) {
			switch name {
			case "deepseek":
				return []core.ModelInfo{
					{ID: "deepseek-chat", OwnedBy: "deepseek"},
				}, nil
			case "openai":
				return []core.ModelInfo{
					{ID: "gpt-4", OwnedBy: "openai"},
					{ID: "gpt-3.5-turbo", OwnedBy: "openai"},
				}, nil
			}
			return nil, nil
		},
	}
	srv := newTestServer(client)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	rr := httptest.NewRecorder()

	srv.ModelsHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}

	var resp modelListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.Object != "list" {
		t.Errorf("object = %q", resp.Object)
	}
	if len(resp.Data) != 3 {
		t.Errorf("data len = %d, want 3", len(resp.Data))
	}
}

func TestModelsHandler_HandlesProviderFailure(t *testing.T) {
	client := &mockClient{
		providerStatusFn: func() []core.ProviderStatus {
			return []core.ProviderStatus{
				{Name: "good", Available: true},
				{Name: "bad", Available: true},
				{Name: "unavailable", Available: false},
			}
		},
		listModelsFn: func(ctx context.Context, name string) ([]core.ModelInfo, error) {
			if name == "bad" {
				return nil, errors.New("connection refused")
			}
			if name == "good" {
				return []core.ModelInfo{{ID: "model-a", OwnedBy: "good"}}, nil
			}
			return nil, nil
		},
	}
	srv := newTestServer(client)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	rr := httptest.NewRecorder()

	srv.ModelsHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}

	var resp modelListResponse
	json.Unmarshal(rr.Body.Bytes(), &resp)
	// Only "good" provider should contribute
	if len(resp.Data) != 1 {
		t.Errorf("data len = %d, want 1", len(resp.Data))
	}
}

func TestModelsHandler_Deduplicates(t *testing.T) {
	client := &mockClient{
		providerStatusFn: func() []core.ProviderStatus {
			return []core.ProviderStatus{
				{Name: "p1", Available: true},
				{Name: "p2", Available: true},
			}
		},
		listModelsFn: func(ctx context.Context, name string) ([]core.ModelInfo, error) {
			return []core.ModelInfo{{ID: "shared-model", OwnedBy: "both"}}, nil
		},
	}
	srv := newTestServer(client)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	rr := httptest.NewRecorder()

	srv.ModelsHandler(rr, req)

	var resp modelListResponse
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Data) != 1 {
		t.Errorf("data len = %d, want 1 (deduplicated)", len(resp.Data))
	}
}

func TestModelsHandler_EmptyWhenNoProviders(t *testing.T) {
	client := &mockClient{
		providerStatusFn: func() []core.ProviderStatus {
			return nil
		},
	}
	srv := newTestServer(client)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	rr := httptest.NewRecorder()

	srv.ModelsHandler(rr, req)

	var resp modelListResponse
	json.Unmarshal(rr.Body.Bytes(), &resp)
	// Data should be [] not null
	if resp.Data == nil {
		t.Error("data should be empty array, not null")
	}
}

func TestProviderStatusHandler_ReturnsStatus(t *testing.T) {
	client := &mockClient{
		providerStatusFn: func() []core.ProviderStatus {
			return []core.ProviderStatus{
				{Name: "deepseek", Available: true, Model: "deepseek-chat"},
				{Name: "openai", Available: false, Model: "gpt-4"},
			}
		},
	}
	srv := newTestServer(client)

	req := httptest.NewRequest("GET", "/admin/providers", nil)
	rr := httptest.NewRecorder()

	srv.ProviderStatusHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}

	var statuses []core.ProviderStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &statuses); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("len = %d, want 2", len(statuses))
	}
	if statuses[0].Name != "deepseek" {
		t.Errorf("name = %q", statuses[0].Name)
	}
	if statuses[1].Available {
		t.Error("openai should not be available")
	}
}

func TestStatsHandler_ReturnsStats(t *testing.T) {
	client := &mockClient{
		statsFn: func() map[string]engine.ProviderStats {
			return map[string]engine.ProviderStats{
				"deepseek": {
					Provider:   "deepseek",
					TotalCalls: 100,
					FailedCalls: 2,
				},
			}
		},
	}
	srv := newTestServer(client)

	req := httptest.NewRequest("GET", "/admin/stats", nil)
	rr := httptest.NewRecorder()

	srv.StatsHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}

	var stats map[string]engine.ProviderStats
	if err := json.Unmarshal(rr.Body.Bytes(), &stats); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	ds, ok := stats["deepseek"]
	if !ok {
		t.Fatal("missing deepseek stats")
	}
	if ds.TotalCalls != 100 {
		t.Errorf("total_calls = %d, want 100", ds.TotalCalls)
	}
}

func TestStatsHandler_NilStats(t *testing.T) {
	client := &mockClient{
		statsFn: func() map[string]engine.ProviderStats {
			return nil
		},
	}
	srv := newTestServer(client)

	req := httptest.NewRequest("GET", "/admin/stats", nil)
	rr := httptest.NewRecorder()

	srv.StatsHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}

	var stats map[string]any
	json.Unmarshal(rr.Body.Bytes(), &stats)
	if len(stats) != 0 {
		t.Errorf("expected empty stats, got %v", stats)
	}
}

func TestReloadHandler_Success(t *testing.T) {
	reloadFn := func() error { return nil }
	handler := ReloadHandler(reloadFn)

	req := httptest.NewRequest("PUT", "/admin/config/reload", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}

	var resp map[string]string
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %q", resp["status"])
	}
}

func TestReloadHandler_Failure(t *testing.T) {
	reloadFn := func() error { return errors.New("bad config") }
	handler := ReloadHandler(reloadFn)

	req := httptest.NewRequest("PUT", "/admin/config/reload", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}

	var resp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &resp)
	errObj := resp["error"].(map[string]any)
	if !strings.Contains(errObj["message"].(string), "bad config") {
		t.Errorf("error = %v", errObj["message"])
	}
}

func TestTestProviderHandler_Success(t *testing.T) {
	client := &mockClient{
		testProviderFn: func(ctx context.Context, name string) error {
			if name != "deepseek" {
				t.Errorf("name = %q, want deepseek", name)
			}
			return nil
		},
	}
	srv := newTestServer(client)

	req := httptest.NewRequest("POST", "/admin/providers/deepseek/test", nil)
	rr := httptest.NewRecorder()

	srv.TestProviderHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}

	var resp map[string]string
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %q", resp["status"])
	}
	if resp["provider"] != "deepseek" {
		t.Errorf("provider = %q", resp["provider"])
	}
}

func TestTestProviderHandler_Failure(t *testing.T) {
	client := &mockClient{
		testProviderFn: func(ctx context.Context, name string) error {
			return errors.New("connection timeout")
		},
	}
	srv := newTestServer(client)

	req := httptest.NewRequest("POST", "/admin/providers/deepseek/test", nil)
	rr := httptest.NewRecorder()

	srv.TestProviderHandler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

func TestTestProviderHandler_MissingName(t *testing.T) {
	client := &mockClient{}
	srv := newTestServer(client)

	req := httptest.NewRequest("POST", "/admin/providers//test", nil)
	rr := httptest.NewRecorder()

	srv.TestProviderHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestExtractProviderName_VariousFormats(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/admin/providers/deepseek/test", "deepseek"},
		{"/admin/providers/openai/test", "openai"},
		{"/admin/providers/my-provider/test", "my-provider"},
		{"/admin/providers//test", ""},             // empty name
		{"/wrong/prefix/deepseek/test", ""},        // wrong prefix
		{"/admin/providers/deepseek", ""},          // missing suffix
		{"/admin/providers/deepseek/wrong", ""},    // wrong suffix
		{"/admin/providers/a/b/test", ""},          // name contains /
		{"/other", ""},                              // completely different path
	}

	for _, tt := range tests {
		got := extractProviderName(tt.path)
		if got != tt.want {
			t.Errorf("extractProviderName(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestHealthHandler_Returns200(t *testing.T) {
	handler := HealthHandler()

	req := httptest.NewRequest("GET", "/healthz", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}

	var resp map[string]string
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %q", resp["status"])
	}
	if rr.Header().Get("Content-Type") != "application/json" {
		t.Errorf("content-type = %q", rr.Header().Get("Content-Type"))
	}
}

func TestReadyHandler_AvailableProvider(t *testing.T) {
	handler := ReadyHandler(func() []core.ProviderStatus {
		return []core.ProviderStatus{
			{Name: "deepseek", Available: true},
		}
	})

	req := httptest.NewRequest("GET", "/readyz", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}

	var resp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["status"] != "ready" {
		t.Errorf("status = %v", resp["status"])
	}
}

func TestReadyHandler_NoProviders(t *testing.T) {
	handler := ReadyHandler(func() []core.ProviderStatus {
		return []core.ProviderStatus{
			{Name: "deepseek", Available: false},
		}
	})

	req := httptest.NewRequest("GET", "/readyz", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}

	var resp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["status"] != "not_ready" {
		t.Errorf("status = %v", resp["status"])
	}
	if resp["reason"] != "no available providers" {
		t.Errorf("reason = %v", resp["reason"])
	}
}

func TestReadyHandler_EmptyProviders(t *testing.T) {
	handler := ReadyHandler(func() []core.ProviderStatus {
		return nil
	})

	req := httptest.NewRequest("GET", "/readyz", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

func TestMetricsHandler_ReturnsPrometheusFormat(t *testing.T) {
	handler := MetricsHandler(func() []core.ProviderStatus {
		return []core.ProviderStatus{
			{
				Name:      "deepseek",
				Available: true,
				Health: core.ProviderHealth{
					SuccessRate:      0.95,
					AvgLatencyMs:     200,
					ConsecutiveFails: 0,
				},
			},
			{
				Name:      "openai",
				Available: false,
				Health: core.ProviderHealth{
					SuccessRate:      0.5,
					AvgLatencyMs:     500,
					ConsecutiveFails: 3,
				},
			},
		}
	})

	req := httptest.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type = %q, want text/plain", ct)
	}

	body := rr.Body.String()

	// Check for version info metric
	if !strings.Contains(body, `aoeo_info{version="0.1.0"} 1`) {
		t.Error("missing aoeo_info metric")
	}

	// Check for provider available metrics
	if !strings.Contains(body, `aoeo_provider_available{name="deepseek"} 1`) {
		t.Error("missing deepseek available metric")
	}
	if !strings.Contains(body, `aoeo_provider_available{name="openai"} 0`) {
		t.Error("missing openai available metric")
	}

	// Check for health metrics
	if !strings.Contains(body, `aoeo_provider_health_success_rate{name="deepseek"} 0.95`) {
		t.Error("missing deepseek success_rate")
	}
	if !strings.Contains(body, `aoeo_provider_health_avg_latency_ms{name="deepseek"} 200`) {
		t.Error("missing deepseek avg_latency_ms")
	}
	if !strings.Contains(body, `aoeo_provider_health_consecutive_fails{name="openai"} 3`) {
		t.Error("missing openai consecutive_fails")
	}

	// Check for HELP and TYPE comments
	if !strings.Contains(body, "# HELP aoeo_info") {
		t.Error("missing HELP for aoeo_info")
	}
	if !strings.Contains(body, "# TYPE aoeo_provider_available gauge") {
		t.Error("missing TYPE for aoeo_provider_available")
	}
}

func TestMetricsHandler_EmptyProviders(t *testing.T) {
	handler := MetricsHandler(func() []core.ProviderStatus {
		return nil
	})

	req := httptest.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}

	body := rr.Body.String()
	// Should still contain version info
	if !strings.Contains(body, `aoeo_info{version="0.1.0"} 1`) {
		t.Error("missing aoeo_info metric")
	}
}

func TestWriteError_DifferentStatusCodes(t *testing.T) {
	tests := []struct {
		status   int
		errType  string
	}{
		{http.StatusBadRequest, "invalid_request_error"},
		{http.StatusUnauthorized, "authentication_error"},
		{http.StatusNotFound, "not_found_error"},
		{http.StatusTooManyRequests, "rate_limit_error"},
		{http.StatusInternalServerError, "server_error"},
		{http.StatusBadGateway, "server_error"},
		{http.StatusForbidden, "api_error"},
	}

	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			rr := httptest.NewRecorder()
			writeError(rr, tt.status, "test error")

			if rr.Code != tt.status {
				t.Errorf("status = %d, want %d", rr.Code, tt.status)
			}

			ct := rr.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("content-type = %q", ct)
			}

			var resp map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}

			errObj := resp["error"].(map[string]any)
			if errObj["message"] != "test error" {
				t.Errorf("message = %v", errObj["message"])
			}
			if errObj["type"] != tt.errType {
				t.Errorf("type = %v, want %v", errObj["type"], tt.errType)
			}
			if errObj["code"].(float64) != float64(tt.status) {
				t.Errorf("code = %v, want %d", errObj["code"], tt.status)
			}
		})
	}
}

// ===========================================================================
// Additional edge case tests for completeness
// ===========================================================================

func TestConvertStop_Nil(t *testing.T) {
	result := convertStop(nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestConvertStop_NullString(t *testing.T) {
	result := convertStop(json.RawMessage(`null`))
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestConvertToolChoice_Nil(t *testing.T) {
	result := convertToolChoice(nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestConvertToolChoice_NullString(t *testing.T) {
	result := convertToolChoice(json.RawMessage(`null`))
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestConvertMessage_ContentAsString(t *testing.T) {
	msg := OpenAIMessage{
		Role:    "user",
		Content: json.RawMessage(`"hello world"`),
	}
	result := convertMessage(msg)
	if result.Content != "hello world" {
		t.Errorf("content = %q, want 'hello world'", result.Content)
	}
}

func TestConvertMessage_ContentAsNull(t *testing.T) {
	msg := OpenAIMessage{
		Role:    "assistant",
		Content: json.RawMessage(`null`),
	}
	result := convertMessage(msg)
	if result.Content != "" {
		t.Errorf("content = %q, want empty", result.Content)
	}
}

func TestConvertMessage_ContentAsNil(t *testing.T) {
	msg := OpenAIMessage{
		Role:    "assistant",
		Content: nil,
	}
	result := convertMessage(msg)
	if result.Content != "" {
		t.Errorf("content = %q, want empty", result.Content)
	}
}

func TestNewServer(t *testing.T) {
	client := &mockClient{}
	srv := NewServer(client)
	if srv == nil {
		t.Fatal("NewServer returned nil")
	}
	if srv.Client == nil {
		t.Fatal("Server.Client is nil")
	}
}

func TestMockClient_Defaults(t *testing.T) {
	client := &mockClient{}
	ctx := context.Background()

	// ChatComplete default
	resp, err := client.ChatComplete(ctx, core.ChatCompletionRequest{})
	if err != nil || resp == nil {
		t.Error("ChatComplete default failed")
	}

	// ChatCompleteStream default
	ch, err := client.ChatCompleteStream(ctx, core.ChatCompletionRequest{})
	if err != nil || ch == nil {
		t.Error("ChatCompleteStream default failed")
	}

	// ChatCompleteWithProvider default
	resp, err = client.ChatCompleteWithProvider(ctx, "test", core.ChatCompletionRequest{})
	if err != nil || resp == nil {
		t.Error("ChatCompleteWithProvider default failed")
	}

	// ChatCompleteStreamWithProvider default
	ch, err = client.ChatCompleteStreamWithProvider(ctx, "test", core.ChatCompletionRequest{})
	if err != nil || ch == nil {
		t.Error("ChatCompleteStreamWithProvider default failed")
	}

	// ChatCompleteWithFallback default
	resp, err = client.ChatCompleteWithFallback(ctx, core.ChatCompletionRequest{})
	if err != nil || resp == nil {
		t.Error("ChatCompleteWithFallback default failed")
	}

	// ListModels default
	models, err := client.ListModels(ctx, "test")
	if err != nil {
		t.Errorf("ListModels error: %v", err)
	}
	if models != nil {
		t.Errorf("ListModels = %v, want nil", models)
	}

	// ProviderStatus default
	statuses := client.ProviderStatus()
	if statuses != nil {
		t.Errorf("ProviderStatus = %v, want nil", statuses)
	}

	// Stats default
	stats := client.Stats()
	if stats != nil {
		t.Errorf("Stats = %v, want nil", stats)
	}

	// TestProvider default
	if err := client.TestProvider(ctx, "test"); err != nil {
		t.Errorf("TestProvider error: %v", err)
	}

	// Close default
	if err := client.Close(); err != nil {
		t.Errorf("Close error: %v", err)
	}
}

func TestChatHandler_ReadBodyError(t *testing.T) {
	srv := newTestServer(&mockClient{})

	// Use a reader that returns an error
	req := httptest.NewRequest("POST", "/v1/chat/completions", &errorReader{})
	rr := httptest.NewRecorder()

	srv.ChatHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

// errorReader is an io.Reader that always returns an error.
type errorReader struct{}

func (e *errorReader) Read(p []byte) (int, error) {
	return 0, errors.New("forced read error")
}

func (e *errorReader) Close() error {
	return nil
}

// Ensure the body is fully consumed and test io.ReadAll error path
func TestChatHandler_BodyReadError(t *testing.T) {
	srv := newTestServer(&mockClient{})

	// Create a request with an already-closed body
	req := httptest.NewRequest("POST", "/v1/chat/completions", io.NopCloser(&errorReader{}))
	rr := httptest.NewRecorder()

	srv.ChatHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestConvertTool_WithParameters(t *testing.T) {
	tool := OpenAITool{
		Type: "function",
		Function: &OpenAIFunctionDef{
			Name:        "search",
			Description: "Search the web",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`),
			Strict:      false,
		},
	}
	result := convertTool(tool)
	if result.Type != "function" {
		t.Errorf("type = %q", result.Type)
	}
	if result.Function == nil {
		t.Fatal("function is nil")
	}
	if result.Function.Name != "search" {
		t.Errorf("name = %q", result.Function.Name)
	}
	params, ok := result.Function.Parameters.(map[string]any)
	if !ok {
		t.Fatalf("params type = %T", result.Function.Parameters)
	}
	if params["type"] != "object" {
		t.Errorf("params.type = %v", params["type"])
	}
}

func TestConvertTool_NilParameters(t *testing.T) {
	tool := OpenAITool{
		Type: "function",
		Function: &OpenAIFunctionDef{
			Name:       "ping",
			Parameters: nil,
		},
	}
	result := convertTool(tool)
	if result.Function.Parameters != nil {
		t.Errorf("params = %v, want nil", result.Function.Parameters)
	}
}

func TestResponseWriter_WriteHeader(t *testing.T) {
	rr := httptest.NewRecorder()
	rw := &responseWriter{
		ResponseWriter: rr,
		statusCode:     http.StatusOK,
	}

	rw.WriteHeader(http.StatusNotFound)

	if rw.statusCode != http.StatusNotFound {
		t.Errorf("statusCode = %d, want 404", rw.statusCode)
	}
	if rr.Code != http.StatusNotFound {
		t.Errorf("recorder code = %d, want 404", rr.Code)
	}
}

func TestErrorTypeFromStatus(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{400, "invalid_request_error"},
		{401, "authentication_error"},
		{404, "not_found_error"},
		{429, "rate_limit_error"},
		{500, "server_error"},
		{502, "server_error"},
		{503, "server_error"},
		{403, "api_error"},
		{409, "api_error"},
	}
	for _, tt := range tests {
		got := errorTypeFromStatus(tt.status)
		if got != tt.want {
			t.Errorf("errorTypeFromStatus(%d) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestModelsHandler_BodyContent(t *testing.T) {
	// Verify JSON encoding with empty model list returns data:[] not data:null
	client := &mockClient{
		providerStatusFn: func() []core.ProviderStatus {
			return nil
		},
	}
	srv := newTestServer(client)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	rr := httptest.NewRecorder()
	srv.ModelsHandler(rr, req)

	body := rr.Body.String()
	// json.Encoder adds a newline, so trim it
	var parsed map[string]any
	json.Unmarshal([]byte(body), &parsed)
	data, ok := parsed["data"].([]any)
	if !ok {
		t.Fatalf("data should be array, got %T", parsed["data"])
	}
	if len(data) != 0 {
		t.Errorf("data len = %d, want 0", len(data))
	}
}

func TestCoreResponseToOpenAI_EmptyChoices(t *testing.T) {
	resp := &core.ChatCompletionResponse{
		ID:        "empty",
		Model:     "gpt-4",
		Choices:   []core.Choice{},
		CreatedAt: time.Unix(1700000000, 0),
	}
	data := CoreResponseToOpenAI(resp)

	var result map[string]any
	json.Unmarshal(data, &result)

	choices := result["choices"].([]any)
	if len(choices) != 0 {
		t.Errorf("choices len = %d, want 0", len(choices))
	}
}

func TestStreamHandler_FlusherNotSupported(t *testing.T) {
	// httptest.ResponseRecorder implements http.Flusher, so to test the
	// "streaming not supported" path we would need a custom writer.
	// We test the normal flusher path in other tests.
	// This test verifies the stream handler works with a standard recorder.
	client := &mockClient{
		chatCompleteStreamFn: func(ctx context.Context, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error) {
			ch := make(chan core.StreamCompletionResponse)
			close(ch)
			return ch, nil
		},
	}
	srv := newTestServer(client)

	openAIReq := &OpenAIRequest{
		Model:    "gpt-4",
		Messages: []OpenAIMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
		Stream:   true,
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	rr := httptest.NewRecorder()

	srv.StreamHandler(rr, req, openAIReq)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "[DONE]") {
		t.Error("missing [DONE]")
	}
}
