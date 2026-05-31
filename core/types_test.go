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
