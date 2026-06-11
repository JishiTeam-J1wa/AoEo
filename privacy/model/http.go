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

// HTTPClient calls a privacy filter sidecar via HTTP/JSON.
// Compatible with Luwu's resources/privacy-server/server.py.
type HTTPClient struct {
	baseURL string
	client  *http.Client
	timeout time.Duration
	retries int
}

// HTTPClientOption configures an HTTPClient.
type HTTPClientOption func(*HTTPClient)

// WithTimeout sets the per-request timeout (default 5s).
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

// NewHTTPClient creates an HTTP client for the privacy filter sidecar.
func NewHTTPClient(baseURL string, opts ...HTTPClientOption) *HTTPClient {
	c := &HTTPClient{
		baseURL: baseURL,
		timeout: 5 * time.Second,
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
			Timeout:   2 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}

	c.client = &http.Client{
		Transport: transport,
		Timeout:   c.timeout,
	}
	return c
}

type detectRequest struct {
	Text string `json:"text"`
}

type detectResponse struct {
	Text  string `json:"text"`
	Spans []Span `json:"spans"`
	Error string `json:"error,omitempty"`
}

type batchDetectRequest struct {
	Texts []string `json:"texts"`
}

type batchDetectResult struct {
	Spans []Span `json:"spans"`
}

type batchDetectResponse struct {
	Results []batchDetectResult `json:"results"`
}

type healthResponse struct {
	Status string `json:"status"`
}

// Detect implements Client.
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
	return nil, fmt.Errorf("privacy filter detect (retried %d): %w", c.retries, lastErr)
}

func (c *HTTPClient) detectOnce(ctx context.Context, text string) ([]Span, error) {
	start := time.Now()
	body, err := json.Marshal(detectRequest{Text: text})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/detect", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		core.GetLogger().Error("privacy sidecar request failed",
			"url", c.baseURL+"/detect",
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
		core.GetLogger().Error("privacy sidecar error response",
			"url", c.baseURL+"/detect",
			"status", resp.StatusCode,
			"body", string(respBody),
			"latency_ms", time.Since(start).Milliseconds(),
		)
		return nil, fmt.Errorf("status %d, body %s", resp.StatusCode, string(respBody))
	}

	var dr detectResponse
	if err := json.Unmarshal(respBody, &dr); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if dr.Error != "" {
		return nil, fmt.Errorf("sidecar error: %s", dr.Error)
	}

	core.GetLogger().Info("privacy sidecar detect",
		"spans", len(dr.Spans),
		"latency_ms", time.Since(start).Milliseconds(),
	)
	return dr.Spans, nil
}

// DetectBatch implements Client by sending multiple texts in a single request.
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
	body, err := json.Marshal(batchDetectRequest{Texts: texts})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/detect/batch", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		core.GetLogger().Error("privacy sidecar batch request failed",
			"url", c.baseURL+"/detect/batch",
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

	var br batchDetectResponse
	if err := json.Unmarshal(respBody, &br); err != nil {
		return nil, fmt.Errorf("decode batch: %w", err)
	}

	result := make([][]Span, 0, len(br.Results))
	for _, rs := range br.Results {
		spans := make([]Span, 0, len(rs.Spans))
		for _, s := range rs.Spans {
			spans = append(spans, Span{
				Label: s.Label,
				Text:  s.Text,
				Start: s.Start,
				End:   s.End,
				Score: s.Score,
			})
		}
		result = append(result, spans)
	}

	core.GetLogger().Info("privacy sidecar batch detect",
		"texts", len(texts),
		"latency_ms", time.Since(start).Milliseconds(),
	)
	return result, nil
}

// HealthCheck implements Client.
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

	var hr healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&hr); err != nil {
		return false, err
	}
	return hr.Status == "ok", nil
}
