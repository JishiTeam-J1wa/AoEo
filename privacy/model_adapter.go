package privacy

import (
	"context"

	"github.com/JishiTeam-J1wa/AoEo/privacy/model"
)

// modelDetectorAdapter wraps a model.Client into a privacy.Detector.
type modelDetectorAdapter struct {
	client model.Client
}

// newModelDetectorAdapter creates an adapter from a model client.
func newModelDetectorAdapter(client model.Client) Detector {
	return &modelDetectorAdapter{client: client}
}

// Detect implements Detector.
func (a *modelDetectorAdapter) Detect(text string) DetectResult {
	spans, err := a.client.Detect(context.Background(), text)
	if err != nil {
		return DetectResult{}
	}
	result := make([]Span, 0, len(spans))
	for _, s := range spans {
		result = append(result, Span{
			Label:    EntityType(s.Label),
			Original: s.Text,
			Start:    s.Start,
			End:      s.End,
			Score:    s.Score,
		})
	}
	return DetectResult{Spans: result}
}

// DetectBatch implements Detector by delegating to the model client's batch API.
func (a *modelDetectorAdapter) DetectBatch(texts []string) []DetectResult {
	if len(texts) == 0 {
		return nil
	}
	results, err := a.client.DetectBatch(context.Background(), texts)
	if err != nil {
		// Fall back to individual Detect calls on error.
		out := make([]DetectResult, len(texts))
		for i, t := range texts {
			out[i] = a.Detect(t)
		}
		return out
	}
	out := make([]DetectResult, 0, len(results))
	for _, spans := range results {
		result := make([]Span, 0, len(spans))
		for _, s := range spans {
			result = append(result, Span{
				Label:    EntityType(s.Label),
				Original: s.Text,
				Start:    s.Start,
				End:      s.End,
				Score:    s.Score,
			})
		}
		out = append(out, DetectResult{Spans: result})
	}
	return out
}
