package privacy

import (
	"context"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	spans, err := a.client.Detect(ctx, text)
	if err != nil {
		core.GetLogger().Warn("privacy model detect failed", "error", err)
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	results, err := a.client.DetectBatch(ctx, texts)
	if err != nil {
		core.GetLogger().Warn("privacy model batch detect failed, falling back to individual calls", "error", err)
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
