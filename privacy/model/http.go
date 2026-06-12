// http.go 实现基于 HTTP/JSON 协议的 OPF sidecar 客户端，
// 支持单条检测、批量检测、健康检查和自动重试。
//
// Author: JishiTeam-J1wa
// Created: 2026-06
//
// Changelog:
//   2026-06-12 - 注释体系规范化
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

// HTTPClient 通过 HTTP/JSON 协议调用 OpenAI Privacy Filter (OPF) sidecar。
// 兼容 gh0stkey/opf-privacy-filter 服务。
type HTTPClient struct {
	baseURL string
	client  *http.Client
	timeout time.Duration
	retries int
}

// HTTPClientOption 配置 HTTPClient 的选项函数。
type HTTPClientOption func(*HTTPClient)

// WithTimeout 设置单次请求超时时间（默认 10 秒）。
// OPF 模型推理可能比简单正则检测更慢，需要适当增大超时。
func WithTimeout(d time.Duration) HTTPClientOption {
	return func(c *HTTPClient) {
		c.timeout = d
	}
}

// WithRetries 设置瞬态错误的重试次数（默认 2 次）。
func WithRetries(n int) HTTPClientOption {
	return func(c *HTTPClient) {
		c.retries = n
	}
}

// NewHTTPClient 创建 OPF 隐私过滤 sidecar 的 HTTP 客户端。
// 使用连接池传输层以支持高并发场景，并启用 HTTP/2 多路复用。
//
// Param:
//   - baseURL: string - OPF sidecar 的基础 URL 地址
//   - opts: ...HTTPClientOption - 可选的客户端配置
//
// Return:
//   - *HTTPClient: 初始化完成的 HTTP 客户端
func NewHTTPClient(baseURL string, opts ...HTTPClientOption) *HTTPClient {
	c := &HTTPClient{
		baseURL: baseURL,
		timeout: 10 * time.Second,
		retries: 2,
	}
	for _, opt := range opts {
		opt(c)
	}

	// 连接池传输层：支持高并发场景下的连接复用。
	// ForceAttemptHTTP2 确保服务端支持时启用 HTTP/2 多路复用。
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
// OPF API 请求 / 响应类型
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
// Detect（单条文本）
// ---------------------------------------------------------------------------

// Detect 发送单段文本到 OPF /redact 端点，返回检测到的 PII 片段。
// 支持自动重试，重试间隔为简单指数退避（100ms、200ms）。
//
// Param:
//   - ctx: context.Context - 请求上下文，用于超时控制
//   - text: string - 待检测的文本内容
//
// Return:
//   - []Span: 检测到的敏感信息片段列表
//   - error: 所有重试均失败时返回最后一次错误
func (c *HTTPClient) Detect(ctx context.Context, text string) ([]Span, error) {
	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		spans, err := c.detectOnce(ctx, text)
		if err == nil {
			return spans, nil
		}
		lastErr = err
		if attempt < c.retries {
			// 简单指数退避：100ms、200ms
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
// DetectBatch（多条文本）
// ---------------------------------------------------------------------------

// DetectBatch 在单次请求中发送多段文本到 OPF /redact/batch 端点。
// 单条文本时自动降级为 Detect 调用，避免不必要的批量请求开销。
//
// Param:
//   - ctx: context.Context - 请求上下文，用于超时控制
//   - texts: []string - 待检测的文本列表
//
// Return:
//   - [][]Span: 每段文本对应的检测结果列表
//   - error: 请求失败时返回错误
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
// HealthCheck（健康检查）
// ---------------------------------------------------------------------------

// HealthCheck 向 OPF /health 端点发送健康检查请求。
// 仅当状态为 "ok" 且模型已加载时返回 true。
//
// Param:
//   - ctx: context.Context - 请求上下文
//
// Return:
//   - bool: sidecar 就绪时返回 true
//   - error: 请求或解析响应失败时返回错误
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
// 辅助函数
// ---------------------------------------------------------------------------

// opfSpansToSpans 将 OPF 检测到的片段转换为项目内部的 Span 类型。
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
			Score:       1.0, // OPF 不提供逐片段置信度分数
			Placeholder: s.Placeholder,
		})
	}
	return spans
}

// normalizeOPFLabel 将 OPF 模型输出标签映射为项目内部的实体类型名称。
// OPF 使用 "NAME"、"EMAIL_ADDRESS"、"PHONE_NUMBER" 等标签，
// 统一转换为 EntityType 常量："person"、"email"、"phone" 等。
func normalizeOPFLabel(label string) string {
	if mapped, ok := opfLabelMap[label]; ok {
		return mapped
	}
	// 未知标签默认归为 "secret"（保守策略）。
	return "secret"
}

// opfLabelMap 将 OPF 模型输出标签映射为 AoEo 实体类型。
// 覆盖 OPF/Presidio 标签和旧版 sidecar 标签，保持向后兼容。
var opfLabelMap = map[string]string{
	// 人名
	"NAME":   "person",
	"PERSON": "person",
	"PER":    "person",
	// 邮箱
	"EMAIL_ADDRESS": "email",
	"EMAIL":         "email",
	// 电话
	"PHONE_NUMBER": "phone",
	"PHONE":        "phone",
	"TEL":          "phone",
	// IP 地址
	"IP_ADDRESS": "ip",
	"IP":         "ip",
	// 金融/密钥
	"CREDIT_CARD":       "secret",
	"CRYPTO":            "secret",
	"IBAN_CODE":         "secret",
	"US_BANK_NUMBER":    "secret",
	"MEDICAL_LICENSE":   "secret",
	"SECRET":            "secret",
	// 身份证/社保号
	"US_DRIVER_LICENSE": "idcard",
	"US_SSN":            "idcard",
	"SSN":               "idcard",
	"IDCARD":            "idcard",
	"ID":                "idcard",
	// URL / 域名
	"URL":    "url",
	"DOMAIN": "domain",
	// 日期
	"DATE_TIME": "date",
	"DATE":      "date",
	// 位置 / 地址
	"LOCATION": "address",
	"ADDRESS":  "address",
	"ADDR":     "address",
	// 其他
	"NRP": "secret",
}
