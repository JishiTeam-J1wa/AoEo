// Package model provides AI model-based PII detection clients.
// It supports both HTTP/JSON and gRPC transports to a privacy filter sidecar.
package model

import "context"

// Span is the detection result from the AI model.
type Span struct {
	Label string  `json:"label"` // e.g. "person", "phone", "email", "secret"
	Text  string  `json:"text"`  // detected original text
	Start int     `json:"start"` // start index in original text
	End   int     `json:"end"`   // end index in original text
	Score float64 `json:"score"` // confidence 0.0~1.0
}

// Client is the interface for AI privacy filter sidecars.
type Client interface {
	// Detect sends text to the model and returns detected spans.
	Detect(ctx context.Context, text string) ([]Span, error)

	// DetectBatch sends multiple texts in a single request and returns
	// detected spans for each text. This reduces round-trips for multi-message
	// requests.
	DetectBatch(ctx context.Context, texts []string) ([][]Span, error)

	// HealthCheck returns true if the sidecar is ready.
	HealthCheck(ctx context.Context) (bool, error)
}
