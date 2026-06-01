package privacy

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

// Pseudonymizer is the core reversible privacy gateway.
// It detects sensitive values, replaces them with realistic fakes,
// and can restore the original values from AI responses.
type Pseudonymizer struct {
	store     *MappingStore
	generator *FakeGenerator
	detector  Detector
}

// NewPseudonymizer creates a new pseudonymizer.
func NewPseudonymizer(store *MappingStore, generator *FakeGenerator, detector Detector) *Pseudonymizer {
	return &Pseudonymizer{
		store:     store,
		generator: generator,
		detector:  detector,
	}
}

// PseudonymizeRequest processes a request before it leaves for the AI provider.
// It returns a new request with sensitive values replaced, and the list of
// mappings created during this operation.
func (p *Pseudonymizer) PseudonymizeRequest(sessionID string, req *core.ChatCompletionRequest) (*core.ChatCompletionRequest, []MappingEntry, error) {
	// Aggregate all message contents.
	parts := make([]string, len(req.Messages))
	for i, m := range req.Messages {
		parts[i] = m.Content
	}
	fullText := strings.Join(parts, "\n")

	// 1. Detect all sensitive spans.
	detected := p.detector.Detect(fullText)
	spans := mergeSpans(detected.Spans, detected.RuleHits)

	if len(spans) == 0 {
		return req, nil, nil
	}

	// 2. Sort by length descending to avoid short-string interference.
	sort.Slice(spans, func(i, j int) bool {
		return len(spans[i].Original) > len(spans[j].Original)
	})

	// 3. Build replacements, reusing existing mappings when possible.
	replacements := make(map[string]string) // original -> fake
	var mappings []MappingEntry

	for _, span := range spans {
		original := span.Original
		if _, ok := replacements[original]; ok {
			continue
		}

		// Check if we already have a mapping for this value.
		if fake, ok := p.store.FindFake(sessionID, original); ok {
			replacements[original] = fake
			continue
		}

		// Generate a new fake value.
		fake := p.generator.Generate(span.Label, original)

		// Ensure no collision with existing fakes in this session.
		for p.store.ExistsFake(sessionID, fake) {
			fake = p.generator.Generate(span.Label, original)
		}

		// Persist the mapping.
		if err := p.store.Create(sessionID, original, fake, span.Label); err != nil {
			return nil, nil, fmt.Errorf("create mapping: %w", err)
		}

		replacements[original] = fake
		mappings = append(mappings, MappingEntry{
			SessionID: sessionID,
			Original:  original,
			Fake:      fake,
			Type:      span.Label,
		})
	}

	// 4. Apply replacements to each message individually.
	cloned := req.Clone()
	for i := range cloned.Messages {
		for orig, fake := range replacements {
			cloned.Messages[i].Content = strings.ReplaceAll(cloned.Messages[i].Content, orig, fake)
		}
	}

	return &cloned, mappings, nil
}

// RestoreResponse processes an AI response, restoring fake values back to
// their originals.
func (p *Pseudonymizer) RestoreResponse(sessionID string, resp *core.ChatCompletionResponse) (*core.ChatCompletionResponse, error) {
	if resp == nil {
		return nil, nil
	}

	mappings, err := p.store.GetSessionMappings(sessionID)
	if err != nil {
		return nil, fmt.Errorf("load mappings: %w", err)
	}
	if len(mappings) == 0 {
		return resp, nil
	}

	// Sort by fake value length descending to avoid partial replacements.
	sort.Slice(mappings, func(i, j int) bool {
		return len(mappings[i].Fake) > len(mappings[j].Fake)
	})

	for i := range resp.Choices {
		text := resp.Choices[i].Message.Content
		for _, m := range mappings {
			text = strings.ReplaceAll(text, m.Fake, m.Original)
		}
		resp.Choices[i].Message.Content = text
	}

	return resp, nil
}

// RestoreStreamChunk restores fake values in a streaming chunk.
func (p *Pseudonymizer) RestoreStreamChunk(sessionID string, chunk *core.StreamCompletionResponse) {
	if chunk == nil || chunk.Err != nil {
		return
	}

	mappings, err := p.store.GetSessionMappings(sessionID)
	if err != nil || len(mappings) == 0 {
		return
	}

	sort.Slice(mappings, func(i, j int) bool {
		return len(mappings[i].Fake) > len(mappings[j].Fake)
	})

	for _, m := range mappings {
		chunk.Chunk.Delta.Content = strings.ReplaceAll(chunk.Chunk.Delta.Content, m.Fake, m.Original)
	}
}
