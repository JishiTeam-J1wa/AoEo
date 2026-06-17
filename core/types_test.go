package core

import (
	"testing"
)

func TestChatCompletionRequest_Clone(t *testing.T) {
	seed := 42
	original := ChatCompletionRequest{
		Model:       "gpt-4",
		Messages:    []Message{{Role: "user", Content: "hello"}},
		Temperature: 0.7,
		Tags:        []string{"tag1", "tag2"},
		Stop:        []string{"STOP"},
		Seed:        &seed,
	}

	cloned := original.Clone()

	// Verify fields copied
	if cloned.Model != original.Model {
		t.Fatal("model not copied")
	}
	if cloned.Temperature != original.Temperature {
		t.Fatal("temperature not copied")
	}

	// Verify Messages deep copied
	if len(cloned.Messages) != len(original.Messages) {
		t.Fatal("messages length mismatch")
	}
	cloned.Messages[0].Content = "modified"
	if original.Messages[0].Content != "hello" {
		t.Fatal("clone mutated original Messages")
	}

	// Verify Tags deep copied
	cloned.Tags[0] = "modified"
	if original.Tags[0] != "tag1" {
		t.Fatal("clone mutated original Tags")
	}

	// Verify Stop NOT deep copied (known behavior of Clone)
	// Stop is expected to be handled by BuildRequest/WithStop

	// Verify Seed pointer shared (expected, it's a pointer to int)
	*cloned.Seed = 99
	if *original.Seed != 99 {
		t.Fatal("Seed pointer not shared (unexpected)")
	}
}

func TestChatCompletionRequest_Clone_Empty(t *testing.T) {
	original := ChatCompletionRequest{}
	cloned := original.Clone()
	if len(cloned.Messages) != 0 || len(cloned.Tags) != 0 {
		t.Fatal("empty request clone should have empty slices")
	}
}

func TestChatCompletionRequest_Validate(t *testing.T) {
	tests := []struct {
		name       string
		req        ChatCompletionRequest
		wantIssues int
	}{
		{"valid", ChatCompletionRequest{Messages: []Message{{Role: "user", Content: "hi"}}, Temperature: 0.5, TopP: 0.9, MaxTokens: 100}, 0},
		{"empty messages", ChatCompletionRequest{}, 1},
		{"negative temperature", ChatCompletionRequest{Messages: []Message{{Role: "user", Content: "hi"}}, Temperature: -1}, 1},
		{"temperature too high", ChatCompletionRequest{Messages: []Message{{Role: "user", Content: "hi"}}, Temperature: 2.1}, 1},
		{"top_p negative", ChatCompletionRequest{Messages: []Message{{Role: "user", Content: "hi"}}, TopP: -0.1}, 1},
		{"top_p over 1", ChatCompletionRequest{Messages: []Message{{Role: "user", Content: "hi"}}, TopP: 1.1}, 1},
		{"negative max_tokens", ChatCompletionRequest{Messages: []Message{{Role: "user", Content: "hi"}}, MaxTokens: -10}, 1},
		{"missing role", ChatCompletionRequest{Messages: []Message{{Content: "hi"}}}, 1},
		{"multiple issues", ChatCompletionRequest{Messages: []Message{}, Temperature: -1, TopP: 2}, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := tt.req.Validate()
			if len(issues) != tt.wantIssues {
				t.Fatalf("expected %d issues, got %d: %v", tt.wantIssues, len(issues), issues)
			}
		})
	}
}

func TestChatCompletionResponse_Content(t *testing.T) {
	if (*ChatCompletionResponse)(nil).Content() != "" {
		t.Fatal("nil response should return empty")
	}
	if (&ChatCompletionResponse{}).Content() != "" {
		t.Fatal("empty choices should return empty")
	}
	resp := &ChatCompletionResponse{Choices: []Choice{{Message: Message{Content: "hello"}}}}
	if resp.Content() != "hello" {
		t.Fatalf("expected 'hello', got %s", resp.Content())
	}
}

func TestMessage_ZeroValue(t *testing.T) {
	var m Message
	if m.Role != "" || m.Content != "" {
		t.Fatal("zero value Message should be empty")
	}
}

func TestStreamCompletionResponse_ZeroUsage(t *testing.T) {
	var s StreamCompletionResponse
	if s.Usage.TotalTokens != 0 {
		t.Fatal("zero value StreamCompletionResponse should have zero usage")
	}
}


// ========== Function Calling type tests ==========

func TestMessage_ToolCalls(t *testing.T) {
	m := Message{
		Role: "assistant",
		ToolCalls: []ToolCall{
			{ID: "call_1", Type: "function", Function: FunctionCall{Name: "get_weather", Arguments: `{"city":"Beijing"}`}},
		},
	}
	if len(m.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(m.ToolCalls))
	}
	if m.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("expected function name get_weather, got %s", m.ToolCalls[0].Function.Name)
	}
}

func TestChatCompletionRequest_Validate_ToolMessage(t *testing.T) {
	// Tool result messages can have empty content.
	req := ChatCompletionRequest{
		Model: "gpt-4",
		Messages: []Message{
			{Role: "user", Content: "What's the weather?"},
			{Role: "assistant", Content: "", ToolCalls: []ToolCall{
				{ID: "call_1", Type: "function", Function: FunctionCall{Name: "get_weather", Arguments: `{}`}},
			}},
			{Role: "tool", Content: "Sunny, 25C", ToolCallID: "call_1"},
		},
		Tools: []Tool{
			{Type: "function", Function: &FunctionDefinition{Name: "get_weather"}},
		},
	}
	issues := req.Validate()
	if len(issues) > 0 {
		t.Fatalf("expected no validation issues for tool messages, got: %v", issues)
	}
}

func TestChatCompletionRequest_Clone_WithTools(t *testing.T) {
	req := ChatCompletionRequest{
		Model: "gpt-4",
		Messages: []Message{
			{Role: "assistant", ToolCalls: []ToolCall{
				{ID: "call_1", Function: FunctionCall{Name: "f1"}},
			}},
		},
		Tools: []Tool{
			{Type: "function", Function: &FunctionDefinition{Name: "f1"}},
		},
	}
	cloned := req.Clone()

	// Mutate original tool calls
	req.Messages[0].ToolCalls[0].ID = "modified"
	req.Tools[0].Type = "modified"

	if cloned.Messages[0].ToolCalls[0].ID == "modified" {
		t.Fatal("Clone did not deep-copy ToolCalls")
	}
	if cloned.Tools[0].Type == "modified" {
		t.Fatal("Clone did not deep-copy Tools")
	}
}

// ========== Additional tests for coverage ==========

func TestChatCompletionRequest_Clone_Metadata(t *testing.T) {
	original := ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Metadata: map[string]any{"key": "value", "count": 42},
	}

	cloned := original.Clone()

	// Verify Metadata was copied
	if cloned.Metadata["key"] != "value" {
		t.Fatal("Metadata not copied")
	}

	// Mutate clone; original should be unaffected
	cloned.Metadata["key"] = "modified"
	cloned.Metadata["new_key"] = "new_value"
	if original.Metadata["key"] != "value" {
		t.Fatal("Clone mutated original Metadata")
	}
	if _, ok := original.Metadata["new_key"]; ok {
		t.Fatal("Clone added key to original Metadata")
	}
}

func TestChatCompletionRequest_Clone_Stop(t *testing.T) {
	original := ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Stop:     []string{"STOP", "END"},
	}

	cloned := original.Clone()

	// Verify Stop was deep copied
	if len(cloned.Stop) != 2 || cloned.Stop[0] != "STOP" {
		t.Fatal("Stop not copied correctly")
	}

	// Mutate clone; original should be unaffected
	cloned.Stop[0] = "modified"
	if original.Stop[0] != "STOP" {
		t.Fatal("Clone mutated original Stop")
	}
}

func TestChatCompletionRequest_Clone_FunctionPointerIndependence(t *testing.T) {
	original := ChatCompletionRequest{
		Tools: []Tool{
			{Type: "function", Function: &FunctionDefinition{Name: "f1", Description: "original"}},
		},
	}

	cloned := original.Clone()

	// Mutate the Function through the original
	original.Tools[0].Function.Name = "modified"
	original.Tools[0].Function.Description = "changed"

	// Cloned Function should be independent
	if cloned.Tools[0].Function.Name != "f1" {
		t.Fatalf("Clone Function Name should be 'f1', got %s", cloned.Tools[0].Function.Name)
	}
	if cloned.Tools[0].Function.Description != "original" {
		t.Fatalf("Clone Function Description should be 'original', got %s", cloned.Tools[0].Function.Description)
	}
}

func TestChatCompletionRequest_Clone_NilFunctionPointer(t *testing.T) {
	original := ChatCompletionRequest{
		Tools: []Tool{
			{Type: "function", Function: nil},
		},
	}

	cloned := original.Clone()
	if cloned.Tools[0].Function != nil {
		t.Fatal("nil Function pointer should remain nil in clone")
	}
}

func TestChatCompletionRequest_Validate_SystemMessageNotFirst(t *testing.T) {
	req := ChatCompletionRequest{
		Messages: []Message{
			{Role: "user", Content: "hi"},
			{Role: "system", Content: "sys"},
		},
	}
	issues := req.Validate()
	found := false
	for _, issue := range issues {
		if issue == "message[1]: system message should be the first message" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected system message position issue, got: %v", issues)
	}
}

func TestChatCompletionRequest_Validate_MultipleSystemMessages(t *testing.T) {
	req := ChatCompletionRequest{
		Messages: []Message{
			{Role: "system", Content: "sys1"},
			{Role: "system", Content: "sys2"},
		},
	}
	issues := req.Validate()
	found := false
	for _, issue := range issues {
		if issue == "found 2 system messages, at most 1 is allowed" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected multiple system messages issue, got: %v", issues)
	}
}

func TestChatCompletionRequest_Validate_EmptyContent(t *testing.T) {
	req := ChatCompletionRequest{
		Messages: []Message{
			{Role: "user", Content: ""},
		},
	}
	issues := req.Validate()
	found := false
	for _, issue := range issues {
		if issue == "message[0]: content is required" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected empty content issue, got: %v", issues)
	}
}

func TestChatCompletionRequest_Validate_EmptyRole(t *testing.T) {
	req := ChatCompletionRequest{
		Messages: []Message{
			{Role: "", Content: "hi"},
		},
	}
	issues := req.Validate()
	found := false
	for _, issue := range issues {
		if issue == "message[0]: role is required" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected empty role issue, got: %v", issues)
	}
}

func TestChatCompletionRequest_Validate_AssistantEmptyContentAllowed(t *testing.T) {
	req := ChatCompletionRequest{
		Messages: []Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: ""},
		},
	}
	issues := req.Validate()
	for _, issue := range issues {
		if issue == "message[1]: content is required" {
			t.Fatal("assistant messages should be allowed to have empty content")
		}
	}
}

func TestChatCompletionRequest_Validate_ToolEmptyContentAllowed(t *testing.T) {
	req := ChatCompletionRequest{
		Messages: []Message{
			{Role: "user", Content: "hi"},
			{Role: "tool", Content: ""},
		},
	}
	issues := req.Validate()
	for _, issue := range issues {
		if issue == "message[1]: content is required" {
			t.Fatal("tool messages should be allowed to have empty content")
		}
	}
}

func TestChatCompletionRequest_Validate_MultipleErrors(t *testing.T) {
	req := ChatCompletionRequest{
		Messages: []Message{
			{Role: "", Content: ""},
			{Role: "system", Content: "sys"},
		},
		Temperature: -1,
		TopP:        2.0,
		MaxTokens:   -5,
	}
	issues := req.Validate()
	// Expected issues:
	// 1. message[0]: role is required
	// 2. message[0]: content is required
	// 3. message[1]: system message should be the first message
	// 4. found 1 system messages (not >1, so no issue for count)
	// 5. temperature must be between 0 and 2
	// 6. top_p must be between 0 and 1
	// 7. max_tokens must be >= 0
	if len(issues) < 5 {
		t.Fatalf("expected at least 5 issues, got %d: %v", len(issues), issues)
	}
}

func TestChatCompletionResponse_Content_MultipleChoices(t *testing.T) {
	resp := &ChatCompletionResponse{
		Choices: []Choice{
			{Message: Message{Content: "first"}},
			{Message: Message{Content: "second"}},
		},
	}
	if resp.Content() != "first" {
		t.Fatalf("expected 'first', got %s", resp.Content())
	}
}
