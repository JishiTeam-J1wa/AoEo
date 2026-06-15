package engine

import (
	"testing"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

func TestMatchWildcard(t *testing.T) {
	if !matchWildcard("*", "anything") {
		t.Fatal("* should match anything")
	}
	if !matchWildcard("", "anything") {
		t.Fatal("empty pattern should match anything")
	}
	if !matchWildcard("deepseek", "deepseek") {
		t.Fatal("exact match should work")
	}
	if matchWildcard("deepseek", "kimi") {
		t.Fatal("mismatch should return false")
	}
}

func TestReplaceVars(t *testing.T) {
	if replaceVars("hello", nil) != "hello" {
		t.Fatal("nil vars should return template as-is")
	}
	if replaceVars("hello", map[string]string{}) != "hello" {
		t.Fatal("empty vars should return template as-is")
	}
	result := replaceVars("Hello {{name}}!", map[string]string{"name": "World"})
	if result != "Hello World!" {
		t.Fatalf("unexpected result: %s", result)
	}
	result = replaceVars("{{a}} and {{b}}", map[string]string{"a": "1", "b": "2"})
	if result != "1 and 2" {
		t.Fatalf("unexpected result: %s", result)
	}
}

func TestInjectSystem(t *testing.T) {
	req := core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	}
	injectSystem(&req, "You are a bot.")
	if len(req.Messages) != 2 || req.Messages[0].Role != "system" {
		t.Fatalf("expected system message prepended, got %+v", req.Messages)
	}
	if req.Messages[0].Content != "You are a bot." {
		t.Fatalf("unexpected content: %s", req.Messages[0].Content)
	}

	// Replace existing system message
	req2 := core.ChatCompletionRequest{
		Messages: []core.Message{
			{Role: "system", Content: "old"},
			{Role: "user", Content: "hi"},
		},
	}
	injectSystem(&req2, "new")
	if len(req2.Messages) != 2 || req2.Messages[0].Content != "new\n\nold" {
		t.Fatal("expected system message prepended")
	}
}

func TestInjectPrependUser(t *testing.T) {
	req := core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	}
	injectPrependUser(&req, "[Task]")
	if req.Messages[0].Content != "[Task]\n\nhi" {
		t.Fatalf("unexpected content: %s", req.Messages[0].Content)
	}

	// No user message: append new user message
	req2 := core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "system", Content: "sys"}},
	}
	injectPrependUser(&req2, "[Task]")
	if len(req2.Messages) != 2 || req2.Messages[1].Role != "user" {
		t.Fatal("expected user message appended")
	}
}

func TestInjectAppendUser(t *testing.T) {
	req := core.ChatCompletionRequest{
		Messages: []core.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
		},
	}
	injectAppendUser(&req, "[End]")
	if req.Messages[0].Content != "hi\n\n[End]" {
		t.Fatalf("unexpected content: %s", req.Messages[0].Content)
	}

	// No user message: append new user message
	req2 := core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "system", Content: "sys"}},
	}
	injectAppendUser(&req2, "[End]")
	if len(req2.Messages) != 2 || req2.Messages[1].Role != "user" {
		t.Fatal("expected user message appended")
	}
}

func TestPromptInjector_AddAndGet(t *testing.T) {
	pi := NewPromptInjector()
	pi.AddTemplate(PromptTemplate{Provider: "*", Content: "hello"})
	if len(pi.Templates()) != 1 {
		t.Fatal("expected 1 template")
	}
}

func TestPromptInjector_SetTemplates(t *testing.T) {
	pi := NewPromptInjector()
	pi.AddTemplate(PromptTemplate{Provider: "a", Content: "A"})
	pi.SetTemplates([]PromptTemplate{{Provider: "b", Content: "B"}})
	if len(pi.Templates()) != 1 {
		t.Fatal("expected 1 template after SetTemplates")
	}
	if pi.Templates()[0].Provider != "b" {
		t.Fatal("expected template b")
	}
}

func TestPromptInjector_Clear(t *testing.T) {
	pi := NewPromptInjector()
	pi.AddTemplate(PromptTemplate{Provider: "*", Content: "hello"})
	pi.Clear()
	if len(pi.Templates()) != 0 {
		t.Fatal("expected 0 templates after clear")
	}
}

func TestPromptInjector_TemplatesDeepCopy(t *testing.T) {
	pi := NewPromptInjector()
	pi.AddTemplate(PromptTemplate{Provider: "*", Content: "hello", Vars: map[string]string{"a": "1"}})

	tmpls := pi.Templates()
	tmpls[0].Vars["a"] = "2"

	internal := pi.Templates()
	if internal[0].Vars["a"] != "1" {
		t.Fatal("Templates() should return deep copy")
	}
}

func TestPromptInjector_Inject(t *testing.T) {
	pi := NewPromptInjector()
	pi.AddTemplate(PromptTemplate{
		Provider: "deepseek",
		Model:    "*",
		Position: "system",
		Content:  "You are {{role}}.",
		Vars:     map[string]string{"role": "expert"},
	})

	req := core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	}
	pi.Inject("deepseek", "deepseek-v4-pro", &req)

	if len(req.Messages) != 2 || req.Messages[0].Content != "You are expert." {
		t.Fatalf("unexpected injection result: %+v", req.Messages)
	}
}

func TestPromptInjector_Inject_NoMatch(t *testing.T) {
	pi := NewPromptInjector()
	pi.AddTemplate(PromptTemplate{
		Provider: "deepseek",
		Position: "system",
		Content:  "sys",
	})

	req := core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "hi"}},
	}
	pi.Inject("kimi", "kimi-k2.6", &req)

	if len(req.Messages) != 1 {
		t.Fatal("no match should not modify messages")
	}
}
