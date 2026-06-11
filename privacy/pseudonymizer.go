package privacy

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/privacy/store"
)

var privacyLog = core.GetLogger()

// Pseudonymizer is the core reversible privacy gateway.
// It detects sensitive values, replaces them with realistic fakes,
// and can restore the original values from AI responses.
type Pseudonymizer struct {
	store     store.MappingStore
	generator *FakeGenerator
	detector  Detector
}

// NewPseudonymizer creates a new pseudonymizer.
func NewPseudonymizer(store store.MappingStore, generator *FakeGenerator, detector Detector) *Pseudonymizer {
	return &Pseudonymizer{
		store:     store,
		generator: generator,
		detector:  detector,
	}
}

// PseudonymizeRequest processes a request before it leaves for the AI provider.
// It returns a new request with sensitive values replaced, and the list of
// mappings created during this operation.
func (p *Pseudonymizer) PseudonymizeRequest(ctx context.Context, sessionID string, req *core.ChatCompletionRequest) (*core.ChatCompletionRequest, []core.PrivacyMapping, error) {
	// 1. Detect all sensitive spans using batch API.
	parts := make([]string, len(req.Messages))
	for i, m := range req.Messages {
		parts[i] = m.Content
	}
	batchResults := p.detector.DetectBatch(parts)

	// Merge spans from all messages (offset stays per-message).
	var spans []Span
	for _, dr := range batchResults {
		spans = append(spans, dr.Spans...)
	}

	// Compute total text length for logging.
	totalLen := 0
	for _, p := range parts {
		totalLen += len(p)
	}
	privacyLog.Info("privacy_detect",
		"session", sessionID,
		"spans_found", len(spans),
		"text_len", totalLen,
	)

	if len(spans) == 0 {
		return req, nil, nil
	}

	// 2. Sort by length descending to avoid short-string interference.
	sort.Slice(spans, func(i, j int) bool {
		return len(spans[i].Original) > len(spans[j].Original)
	})

	// 3. Build replacements, reusing existing mappings when possible.
	replacements := make(map[string]string) // original -> fake
	var mappings []core.PrivacyMapping

	for _, span := range spans {
		original := span.Original
		if _, ok := replacements[original]; ok {
			continue
		}

		// Check if we already have a mapping for this value.
		if fake, ok, _ := p.store.GetFake(ctx, sessionID, original); ok {
			replacements[original] = fake
			continue
		}

		// Generate a new fake value.
		fake := p.generator.Generate(span.Label, original)

		// Ensure no collision with existing fakes in this session.
		for {
			_, exists, _ := p.store.GetOriginal(ctx, sessionID, fake)
			if !exists {
				break
			}
			fake = p.generator.Generate(span.Label, original)
		}

		// Persist the mapping.
		if err := p.store.Set(ctx, sessionID, fake, original, string(span.Label)); err != nil {
			return nil, nil, fmt.Errorf("create mapping: %w", err)
		}
		m := core.PrivacyMapping{
			SessionID: sessionID,
			Original:  original,
			Fake:      fake,
			Type:      string(span.Label),
			CreatedAt: time.Now(),
		}

		replacements[original] = fake
		mappings = append(mappings, m)

		privacyLog.Info("privacy_replace",
			"session", sessionID,
			"type", span.Label,
			"original", original,
			"fake", fake,
		)
	}

	// 4. Apply replacements to each message individually.
	cloned := req.Clone()
	for i := range cloned.Messages {
		for orig, fake := range replacements {
			cloned.Messages[i].Content = strings.ReplaceAll(cloned.Messages[i].Content, orig, fake)
		}
	}

	privacyLog.Info("privacy_pseudonymized",
		"session", sessionID,
		"replacements", len(replacements),
	)

	return &cloned, mappings, nil
}

// RestoreResponse processes an AI response, restoring fake values back to
// their originals using all session mappings.
func (p *Pseudonymizer) RestoreResponse(ctx context.Context, sessionID string, resp *core.ChatCompletionResponse) (*core.ChatCompletionResponse, error) {
	if resp == nil {
		return nil, nil
	}

	entries, err := p.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load mappings: %w", err)
	}
	return p.restoreWithEntries(ctx, sessionID, resp, entries)
}

// RestoreResponseWithMappings restores only the provided mappings.
// This is the preferred path when the caller knows exactly which fake values
// were created for the current request, avoiding false restoration of
// historical fake values from earlier turns.
func (p *Pseudonymizer) RestoreResponseWithMappings(ctx context.Context, sessionID string, resp *core.ChatCompletionResponse, mappings []core.PrivacyMapping) (*core.ChatCompletionResponse, error) {
	if resp == nil {
		return nil, nil
	}
	if len(mappings) == 0 {
		return resp, nil
	}

	entries := make([]store.Entry, len(mappings))
	for i, m := range mappings {
		entries[i] = store.Entry{
			SessionID: m.SessionID,
			Original:  m.Original,
			Fake:      m.Fake,
		}
	}
	return p.restoreWithEntries(ctx, sessionID, resp, entries)
}

func (p *Pseudonymizer) restoreWithEntries(ctx context.Context, sessionID string, resp *core.ChatCompletionResponse, entries []store.Entry) (*core.ChatCompletionResponse, error) {
	if len(entries) == 0 {
		return resp, nil
	}

	// Sort by fake value length descending to avoid partial replacements.
	sort.Slice(entries, func(i, j int) bool {
		return len(entries[i].Fake) > len(entries[j].Fake)
	})

	restoredCount := 0
	for i := range resp.Choices {
		text := resp.Choices[i].Message.Content
		for _, e := range entries {
			if strings.Contains(text, e.Fake) {
				restoredCount++
			}
			text = replaceFake(text, e.Fake, e.Original)
		}
		resp.Choices[i].Message.Content = text
	}

	// Leak detection: scan for any remaining fake values.
	leaks := p.detectLeaks(resp, entries)
	if len(leaks) > 0 {
		privacyLog.Warn("privacy_restore_leak",
			"session", sessionID,
			"leaks", leaks,
		)
	}

	privacyLog.Info("privacy_restore",
		"session", sessionID,
		"mappings_loaded", len(entries),
		"restored_count", restoredCount,
		"leaks", len(leaks),
	)

	return resp, nil
}

// RestoreStreamChunk restores fake values in a streaming chunk.
func (p *Pseudonymizer) RestoreStreamChunk(ctx context.Context, sessionID string, chunk *core.StreamCompletionResponse) {
	if chunk == nil || chunk.Err != nil {
		return
	}

	entries, err := p.store.GetSession(ctx, sessionID)
	if err != nil || len(entries) == 0 {
		return
	}

	sort.Slice(entries, func(i, j int) bool {
		return len(entries[i].Fake) > len(entries[j].Fake)
	})

	restored := false
	for _, e := range entries {
		before := chunk.Chunk.Delta.Content
		after := replaceFake(before, e.Fake, e.Original)
		if after != before {
			restored = true
		}
		chunk.Chunk.Delta.Content = after
	}

	if restored {
		privacyLog.Debug("privacy_restore_stream", "session", sessionID)
	}
}

// ---------------------------------------------------------------------------
// Restoration helpers
// ---------------------------------------------------------------------------

// replaceFake replaces fake with original in text. It first tries exact match,
// then tries common punctuation boundaries (AI often adds punctuation after
// generated values).
func replaceFake(text, fake, original string) string {
	// Exact replacement.
	text = strings.ReplaceAll(text, fake, original)

	// Fuzzy: fake followed by common punctuation.
	// If the fake ends with a digit or letter, AI may append . , ! ? ; :
	puncts := []string{".", ",", "!", "?", ";", ":", ")", "]", "}"}
	for _, p := range puncts {
		text = strings.ReplaceAll(text, fake+p, original+p)
	}
	// Fuzzy: fake preceded by opening punctuation.
	opens := []string{"(", "[", "{"}
	for _, p := range opens {
		text = strings.ReplaceAll(text, p+fake, p+original)
	}

	return text
}

// detectLeaks scans all response choices for any remaining fake values.
func (p *Pseudonymizer) detectLeaks(resp *core.ChatCompletionResponse, entries []store.Entry) []string {
	var leaks []string
	seen := make(map[string]bool)
	for i := range resp.Choices {
		text := resp.Choices[i].Message.Content
		for _, e := range entries {
			if strings.Contains(text, e.Fake) && !seen[e.Fake] {
				seen[e.Fake] = true
				leaks = append(leaks, e.Fake)
			}
		}
	}
	return leaks
}
