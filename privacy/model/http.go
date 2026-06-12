package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

// HTTPClient calls an OpenAI Privacy Filter (OPF) sidecar via HTTP/JSON.
// It is compatible with the gh0stkey/opf-privacy-filter service.
type HTTPClient struct {
	baseURL string
	client  *http.Client
	timeout time.Duration
	retries int
}

// HTTPClientOption configures an HTTPClient.
type HTTPClientOption func(*HTTPClient)

// WithTimeout sets the per-request timeout (default 10s).
// OPF inference may be slower than simple regex detection.
func WithTimeout(d time.Duration) HTTPClientOption {
	return func(c *HTTPClient) {
		c.timeout = d
	}
}

// WithRetries sets the number of retries on transient errors (default 2).
func WithRetries(n int) HTTPClientOption {
	return func(c *HTTPClient) {
		c.retries = n
	}
}

// NewHTTPClient creates an HTTP client for the OPF privacy filter sidecar.
func NewHTTPClient(baseURL string, opts ...HTTPClientOption) *HTTPClient {
	c := &HTTPClient{
		baseURL: baseURL,
		timeout: 10 * time.Second,
		retries: 2,
	}
	for _, opt := range opts {
		opt(c)
	}

	// Connection-pooled transport for high-concurrency scenarios.
	// ForceAttemptHTTP2 ensures HTTP/2 multiplexing when the server supports it.
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   true,
		DialContext: (&net.Dialer{
			Timeout:   3 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}

	c.client = &http.Client{
		Transport: transport,
		Timeout:   c.timeout,
	}
	return c
}

// ---------------------------------------------------------------------------
// OPF API request / response types
// ---------------------------------------------------------------------------

type opfRedactRequest struct {
	Text string `json:"text"`
}

type opfSpan struct {
	Label       string `json:"label"`
	Start       int    `json:"start"`
	End         int    `json:"end"`
	Text        string `json:"text"`
	Placeholder string `json:"placeholder"`
}

type opfRedactResponse struct {
	SchemaVersion int       `json:"schema_version"`
	Text          string    `json:"text"`
	RedactedText  string    `json:"redacted_text"`
	DetectedSpans []opfSpan `json:"detected_spans"`
	Summary       map[string]any `json:"summary"`
	Warning       *string   `json:"warning"`
	LatencyMs     float64   `json:"latency_ms"`
}

type opfBatchRedactRequest struct {
	Texts []string `json:"texts"`
}

type opfBatchRedactResponse struct {
	Results        []opfRedactResponse `json:"results"`
	TotalLatencyMs float64             `json:"total_latency_ms"`
}

type opfHealthResponse struct {
	Status      string `json:"status"`
	ModelLoaded bool   `json:"model_loaded"`
}

// ---------------------------------------------------------------------------
// Detect (single text)
// ---------------------------------------------------------------------------

// Detect implements Client. Sends a single text to the OPF /redact endpoint
// and returns the detected PII spans.
func (c *HTTPClient) Detect(ctx context.Context, text string) ([]Span, error) {
	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		spans, err := c.detectOnce(ctx, text)
		if err == nil {
			return spans, nil
		}
		lastErr = err
		if attempt < c.retries {
			// Simple exponential backoff: 100ms, 200ms
			select {
			case <-time.After(time.Duration(attempt+1) * 100 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	return nil, fmt.Errorf("opf privacy filter detect (retried %d): %w", c.retries, lastErr)
}

func (c *HTTPClient) detectOnce(ctx context.Context, text string) ([]Span, error) {
	start := time.Now()
	body, err := json.Marshal(opfRedactRequest{Text: text})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/redact", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		core.GetLogger().Error("opf sidecar request failed",
			"url", c.baseURL+"/redact",
			"error", err,
			"latency_ms", time.Since(start).Milliseconds(),
		)
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		core.GetLogger().Error("opf sidecar error response",
			"url", c.baseURL+"/redact",
			"status", resp.StatusCode,
			"body", string(respBody),
			"latency_ms", time.Since(start).Milliseconds(),
		)
		return nil, fmt.Errorf("status %d, body %s", resp.StatusCode, string(respBody))
	}

	var or opfRedactResponse
	if err := json.Unmarshal(respBody, &or); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	spans := opfSpansToSpans(or.DetectedSpans)

	core.GetLogger().Info("opf sidecar detect",
		"spans", len(spans),
		"opf_latency_ms", or.LatencyMs,
		"total_latency_ms", time.Since(start).Milliseconds(),
	)
	return spans, nil
}

// ---------------------------------------------------------------------------
// DetectBatch (multiple texts)
// ---------------------------------------------------------------------------

// DetectBatch implements Client by sending multiple texts to the OPF
// /redact/batch endpoint in a single request.
func (c *HTTPClient) DetectBatch(ctx context.Context, texts []string) ([][]Span, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if len(texts) == 1 {
		spans, err := c.Detect(ctx, texts[0])
		if err != nil {
			return nil, err
		}
		return [][]Span{spans}, nil
	}

	start := time.Now()
	body, err := json.Marshal(opfBatchRedactRequest{Texts: texts})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/redact/batch", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		core.GetLogger().Error("opf sidecar batch request failed",
			"url", c.baseURL+"/redact/batch",
			"error", err,
			"latency_ms", time.Since(start).Milliseconds(),
		)
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d, body %s", resp.StatusCode, string(respBody))
	}

	var br opfBatchRedactResponse
	if err := json.Unmarshal(respBody, &br); err != nil {
		return nil, fmt.Errorf("decode batch: %w", err)
	}

	result := make([][]Span, 0, len(br.Results))
	for _, rs := range br.Results {
		spans := opfSpansToSpans(rs.DetectedSpans)
		result = append(result, spans)
	}

	core.GetLogger().Info("opf sidecar batch detect",
		"texts", len(texts),
		"opf_total_latency_ms", br.TotalLatencyMs,
		"total_latency_ms", time.Since(start).Milliseconds(),
	)
	return result, nil
}

// ---------------------------------------------------------------------------
// HealthCheck
// ---------------------------------------------------------------------------

// HealthCheck implements Client. It pings the OPF /health endpoint.
func (c *HTTPClient) HealthCheck(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return false, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, nil
	}

	var hr opfHealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&hr); err != nil {
		return false, err
	}
	return hr.Status == "ok" && hr.ModelLoaded, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// opfSpansToSpans converts OPF detected spans to our Span type.
func opfSpansToSpans(opfSpans []opfSpan) []Span {
	if len(opfSpans) == 0 {
		return nil
	}
	spans := make([]Span, 0, len(opfSpans))
	for _, s := range opfSpans {
		spans = append(spans, Span{
			Label:       normalizeOPFLabel(s.Label),
			Text:        s.Text,
			Start:       s.Start,
			End:         s.End,
			Score:       1.0, // OPF does not provide per-span confidence scores
			Placeholder: s.Placeholder,
		})
	}
	return spans
}

// normalizeOPFLabel maps OPF model output labels to our entity type names.
// OPF uses labels like "NAME", "EMAIL_ADDRESS", "PHONE_NUMBER", etc.
// We normalize them to our EntityType constants: "person", "email", "phone", etc.
func normalizeOPFLabel(label string) string {
	if mapped, ok := opfLabelMap[label]; ok {
		return mapped
	}
	// Unknown labels default to "secret" (conservative strategy).
	return "secret"
}

// opfLabelMap maps OPF model output labels to AoEo entity types.
// Covers OPF/Presidio labels and legacy sidecar labels for backward compatibility.
var opfLabelMap = map[string]string{
	// Person / Name
	"NAME":   "person",
	"PERSON": "person",
	"PER":    "person",
	// Email
	"EMAIL_ADDRESS": "email",
	"EMAIL":         "email",
	// Phone
	"PHONE_NUMBER": "phone",
	"PHONE":        "phone",
	"TEL":          "phone",
	// IP Address
	"IP_ADDRESS": "ip",
	"IP":         "ip",
	// Financial / Secret
	"CREDIT_CARD":       "secret",
	"CRYPTO":            "secret",
	"IBAN_CODE":         "secret",
	"US_BANK_NUMBER":    "secret",
	"MEDICAL_LICENSE":   "secret",
	"SECRET":            "secret",
	// ID Card / SSN
	"US_DRIVER_LICENSE": "idcard",
	"US_SSN":            "idcard",
	"SSN":               "idcard",
	"IDCARD":            "idcard",
	"ID":                "idcard",
	// URL / Domain
	"URL":    "url",
	"DOMAIN": "domain",
	// Date
	"DATE_TIME": "date",
	"DATE":      "date",
	// Location / Address
	"LOCATION": "address",
	"ADDRESS":  "address",
	"ADDR":     "address",
	// Other
	"NRP": "secret",
}
