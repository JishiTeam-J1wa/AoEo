package privacy

// Detector is the interface for PII / sensitive data detection.
// Implementations include the modelDetectorAdapter (backed by OpenAI Privacy Filter)
// and noopDetector (no-op fallback).
type Detector interface {
	Detect(text string) DetectResult

	// DetectBatch detects sensitive spans in multiple texts at once.
	// The returned slice has the same length as the input texts.
	DetectBatch(texts []string) []DetectResult
}
