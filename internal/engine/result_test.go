package engine

import (
	"testing"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

func TestExtractJSON_Direct(t *testing.T) {
	var s struct{ Name string `json:"name"` }
	if err := ExtractJSON(`{"name":"test"}`, &s); err != nil {
		t.Fatalf("direct parse failed: %v", err)
	}
	if s.Name != "test" {
		t.Fatalf("expected 'test', got %s", s.Name)
	}
}

func TestExtractJSON_MarkdownFence(t *testing.T) {
	var s struct{ Name string `json:"name"` }
	if err := ExtractJSON("```json\n{\"name\":\"fenced\"}\n```", &s); err != nil {
		t.Fatalf("fenced parse failed: %v", err)
	}
	if s.Name != "fenced" {
		t.Fatalf("expected 'fenced', got %s", s.Name)
	}
}

func TestExtractJSON_Embedded(t *testing.T) {
	var s struct{ Name string `json:"name"` }
	if err := ExtractJSON("some text before {\"name\":\"embedded\"} and after", &s); err != nil {
		t.Fatalf("embedded parse failed: %v", err)
	}
	if s.Name != "embedded" {
		t.Fatalf("expected 'embedded', got %s", s.Name)
	}
}

func TestExtractJSON_Failure(t *testing.T) {
	var s struct{ Name string `json:"name"` }
	if err := ExtractJSON("not json at all", &s); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestExtractJSON_NestedObject(t *testing.T) {
	var s struct {
		Outer struct {
			Inner string `json:"inner"`
		} `json:"outer"`
	}
	if err := ExtractJSON(`{"outer":{"inner":"value"}}`, &s); err != nil {
		t.Fatalf("nested parse failed: %v", err)
	}
	if s.Outer.Inner != "value" {
		t.Fatalf("expected 'value', got %s", s.Outer.Inner)
	}
}

func TestFindFirstJSONObject(t *testing.T) {
	if findFirstJSONObject("no braces") != "" {
		t.Fatal("expected empty for no braces")
	}
	if findFirstJSONObject("{}") != "{}" {
		t.Fatal("expected {} for empty object")
	}
	if findFirstJSONObject(`{"a":"b"}`) != `{"a":"b"}` {
		t.Fatal("expected simple object")
	}
	if findFirstJSONObject(`text {"a":"b"} more`) != `{"a":"b"}` {
		t.Fatal("expected embedded object")
	}
	// Nested braces
	if findFirstJSONObject(`{"outer":{"inner":1}}`) != `{"outer":{"inner":1}}` {
		t.Fatal("expected nested object")
	}
	// String containing braces
	result := findFirstJSONObject(`{"key":"{value}"}`)
	if result != `{"key":"{value}"}` {
		t.Fatalf("expected string-with-braces object, got %s", result)
	}
}

func TestExtractField(t *testing.T) {
	content := `{"name":"test","age":30}`
	if v := ExtractField(content, "name"); v != "test" {
		t.Fatalf("expected 'test', got '%s'", v)
	}
	if v := ExtractField(content, "missing"); v != "" {
		t.Fatalf("expected empty, got '%s'", v)
	}
}

func TestExtractField_CaseInsensitive(t *testing.T) {
	content := `{"Name":"Test"}`
	if v := ExtractField(content, "name"); v != "Test" {
		t.Fatalf("expected case-insensitive match, got '%s'", v)
	}
}

func TestMergeChoices(t *testing.T) {
	r1 := &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "A"}}},
		Usage:   core.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}
	r2 := &core.ChatCompletionResponse{
		Choices: []core.Choice{{Message: core.Message{Content: "A"}}},
		Usage:   core.Usage{PromptTokens: 8, CompletionTokens: 4, TotalTokens: 12},
	}

	merged := MergeChoices(r1, r2, true)
	if merged == nil {
		t.Fatal("expected non-nil merged")
	}
	if merged.Usage.TotalTokens != 27 {
		t.Fatalf("expected 27 total tokens, got %d", merged.Usage.TotalTokens)
	}
	if merged.Choices[0].Message.Content != "A" {
		t.Fatal("consensus should return first choice")
	}

	disagree := MergeChoices(r1, r2, false)
	if disagree.Choices[0].Message.Content == "A" {
		t.Fatal("disagreement should merge content")
	}
}

func TestMergeChoices_Nil(t *testing.T) {
	r := &core.ChatCompletionResponse{Choices: []core.Choice{{Message: core.Message{Content: "X"}}}}
	if MergeChoices(nil, nil, true) != nil {
		t.Fatal("nil + nil should be nil")
	}
	if MergeChoices(r, nil, true) != r {
		t.Fatal("r + nil should be r")
	}
	if MergeChoices(nil, r, true) != r {
		t.Fatal("nil + r should be r")
	}
}

func TestConsensus(t *testing.T) {
	r1 := &core.ChatCompletionResponse{Choices: []core.Choice{{Message: core.Message{Content: "yes"}}}}
	r2 := &core.ChatCompletionResponse{Choices: []core.Choice{{Message: core.Message{Content: "yes"}}}}
	r3 := &core.ChatCompletionResponse{Choices: []core.Choice{{Message: core.Message{Content: "no"}}}}

	if !Consensus(r1, r2) {
		t.Fatal("expected consensus")
	}
	if Consensus(r1, r3) {
		t.Fatal("expected no consensus")
	}
	if Consensus(nil, r1) {
		t.Fatal("nil should not consensus")
	}
}

func TestNormalizeContent(t *testing.T) {
	if normalizeContent("  YES  ") != "yes" {
		t.Fatal("should lowercase and trim")
	}
	if normalizeContent("a  b\tc") != "a b c" {
		t.Fatal("should normalize whitespace")
	}
}

func TestExtractContent(t *testing.T) {
	if extractContent(nil) != "" {
		t.Fatal("nil should return empty")
	}
	if extractContent(&core.ChatCompletionResponse{}) != "" {
		t.Fatal("no choices should return empty")
	}
	r := &core.ChatCompletionResponse{Choices: []core.Choice{{Message: core.Message{Content: "hello"}}}}
	if extractContent(r) != "hello" {
		t.Fatal("expected 'hello'")
	}
}
