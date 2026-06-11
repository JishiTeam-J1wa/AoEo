package privacy

import (
	"context"
	"strings"
	"testing"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/privacy/store"
)

func TestRestore_FuzzyPunctuation(t *testing.T) {
	pebbleStore, _ := store.OpenPebble(t.TempDir() + "/privacy")
	defer pebbleStore.Close()

	gen := NewFakeGenerator(42)
	detector := &mockDetector{
		spans: []Span{{Label: EntityIP, Original: "192.168.1.1", Score: 0.99}},
	}

	ps := NewPseudonymizer(pebbleStore, gen, detector)
	ctx := context.Background()

	req := &core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "IP: 192.168.1.1"}},
	}
	ps.PseudonymizeRequest(ctx, "sess-fuzzy", req)

	entries, _ := pebbleStore.GetSession(ctx, "sess-fuzzy")
	fakeIP := entries[0].Fake

	// AI adds punctuation after the fake value.
	resp := &core.ChatCompletionResponse{
		Choices: []core.Choice{
			{Message: core.Message{Content: "服务器地址是 " + fakeIP + "。请确认。"}},
		},
	}

	restored, err := ps.RestoreResponse(ctx, "sess-fuzzy", resp)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	content := restored.Choices[0].Message.Content
	want := "服务器地址是 192.168.1.1。请确认。"
	if content != want {
		t.Fatalf("fuzzy restore failed:\n  got:  %s\n  want: %s", content, want)
	}
}

func TestRestore_LeakDetection(t *testing.T) {
	pebbleStore, _ := store.OpenPebble(t.TempDir() + "/privacy")
	defer pebbleStore.Close()

	gen := NewFakeGenerator(42)
	detector := &mockDetector{
		spans: []Span{{Label: EntityIP, Original: "192.168.1.1", Score: 0.99}},
	}

	ps := NewPseudonymizer(pebbleStore, gen, detector)
	ctx := context.Background()

	req := &core.ChatCompletionRequest{
		Messages: []core.Message{{Role: "user", Content: "IP: 192.168.1.1"}},
	}
	ps.PseudonymizeRequest(ctx, "sess-leak", req)

	entries, _ := pebbleStore.GetSession(ctx, "sess-leak")
	fakeIP := entries[0].Fake

	// AI wraps fake in characters not handled by fuzzy replace.
	resp := &core.ChatCompletionResponse{
		Choices: []core.Choice{
			{Message: core.Message{Content: "地址是 ~" + fakeIP + "~"}},
		},
	}

	restored, _ := ps.RestoreResponse(ctx, "sess-leak", resp)
	content := restored.Choices[0].Message.Content

	// Exact replace still works because fake is a substring between ~ chars.
	if !strings.Contains(content, "192.168.1.1") {
		t.Fatalf("expected restore through exact match, got: %s", content)
	}
	// No leaks because exact ReplaceAll found it.
	leaks := ps.detectLeaks(restored, entries)
	if len(leaks) != 0 {
		t.Fatalf("expected no leaks, got: %v", leaks)
	}
}
