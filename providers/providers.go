package providers

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/sashabaranov/go-openai"
)

// Provider is the interface that all AI provider adapters must implement.
type Provider interface {
	Name() string
	Config() core.ProviderConfig
	ChatComplete(ctx context.Context, req core.ChatCompletionRequest) (*core.ChatCompletionResponse, error)
	ChatCompleteStream(ctx context.Context, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error)
	IsAvailable() bool
	ListModels(ctx context.Context) ([]core.ModelInfo, error)
	SetEmitter(e core.EventEmitter)
	HealthCheck(ctx context.Context) error
}

// healthEntry is a single observation in the sliding window.
type healthEntry struct {
	latencyMs int64
	success   bool
	timestamp time.Time
}

// BaseProvider provides common logic for all providers (circuit breaker, system prompt override).
// It is exported for use by custom provider implementations, but most users should use
// the built-in providers or the generic OpenAIProvider.
type BaseProvider struct {
	config core.ProviderConfig

	// Circuit breaker: track consecutive failures (all atomic).
	failCount atomic.Int32
	failUntil atomic.Int64 // UnixNano, 0 means not in cooldown

	// System prompt override (atomic pointer to string).
	sysPrompt atomic.Pointer[string]

	// Optional event emitter for provider lifecycle events.
	emitter atomic.Value // stores *emitterBox

	// Runtime health metrics (sliding window of recent calls).
	healthMu     sync.RWMutex
	healthWindow [20]healthEntry
	healthHead   int
	healthCount  int
	healthLatest atomic.Pointer[core.ProviderHealth]
}

// emitterBox wraps an EventEmitter so atomic.Value stores a consistently-typed pointer.
type emitterBox struct {
	em core.EventEmitter
}

// NewBaseProvider creates a BaseProvider with the given config.
func NewBaseProvider(config core.ProviderConfig) *BaseProvider {
	bp := &BaseProvider{config: config}
	bp.emitter.Store(&emitterBox{em: core.NopEmitter{}})
	bp.healthLatest.Store(&core.ProviderHealth{})
	return bp
}

// RecordHealthCheck records the result of a health-check probe.
func (b *BaseProvider) RecordHealthCheck(latencyMs int64, success bool) {
	b.pushHealthEntry(healthEntry{latencyMs: latencyMs, success: success, timestamp: time.Now()})
}

// RecordCallResult records the result of an actual API call.
func (b *BaseProvider) RecordCallResult(latencyMs int64, err error) {
	b.pushHealthEntry(healthEntry{latencyMs: latencyMs, success: err == nil, timestamp: time.Now()})
}

// Health returns the current runtime health snapshot.
func (b *BaseProvider) Health() core.ProviderHealth {
	if h := b.healthLatest.Load(); h != nil {
		return *h
	}
	return core.ProviderHealth{}
}

// pushHealthEntry adds an entry to the sliding window and recomputes the snapshot.
func (b *BaseProvider) pushHealthEntry(entry healthEntry) {
	b.healthMu.Lock()
	defer b.healthMu.Unlock()

	b.healthWindow[b.healthHead] = entry
	b.healthHead = (b.healthHead + 1) % len(b.healthWindow)
	if b.healthCount < len(b.healthWindow) {
		b.healthCount++
	}

	// Recompute aggregated metrics from the window.
	var totalLatency int64
	var successCount int
	var consecutiveFails int
	var lastFailStreak int
	for i := 0; i < b.healthCount; i++ {
		idx := (b.healthHead - b.healthCount + i + len(b.healthWindow)) % len(b.healthWindow)
		e := b.healthWindow[idx]
		totalLatency += e.latencyMs
		if e.success {
			successCount++
			lastFailStreak = 0
		} else {
			lastFailStreak++
			if lastFailStreak > consecutiveFails {
				consecutiveFails = lastFailStreak
			}
		}
	}

	snapshot := &core.ProviderHealth{
		LastCheckAt:      entry.timestamp,
		LastLatencyMs:    entry.latencyMs,
		AvgLatencyMs:     totalLatency / int64(max(b.healthCount, 1)),
		SuccessRate:      float64(successCount) / float64(max(b.healthCount, 1)),
		ConsecutiveFails: consecutiveFails,
		TotalChecks:      b.healthCount,
	}
	b.healthLatest.Store(snapshot)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Config returns the provider's configuration.
func (b *BaseProvider) Config() core.ProviderConfig {
	return b.config
}

// SetEmitter attaches an event emitter to the provider.
func (b *BaseProvider) SetEmitter(e core.EventEmitter) {
	if e == nil {
		b.emitter.Store(&emitterBox{em: core.NopEmitter{}})
	} else {
		b.emitter.Store(&emitterBox{em: e})
	}
}

func (b *BaseProvider) getEmitter() core.EventEmitter {
	if box, ok := b.emitter.Load().(*emitterBox); ok {
		return box.em
	}
	return core.NopEmitter{}
}

// RecordSuccess resets the failure counter on a successful call.
func (b *BaseProvider) RecordSuccess() {
	wasFailed := b.failCount.Load() > 0
	b.failCount.Store(0)
	b.failUntil.Store(0)

	if wasFailed {
		b.getEmitter().Emit(core.EventProviderRecover, b.config.Name)
	}
}

// RecordFailure increments the failure counter and triggers cooldown after MaxFailures consecutive failures.
func (b *BaseProvider) RecordFailure() {
	count := b.failCount.Add(1)
	maxFailures := b.config.MaxFailures
	if maxFailures <= 0 {
		maxFailures = 3
	}
	cooldown := b.config.CooldownDuration
	if cooldown <= 0 {
		cooldown = 60 * time.Second
	}
	opened := false
	if int(count) >= maxFailures {
		b.failUntil.Store(time.Now().Add(cooldown).UnixNano())
		opened = true
		core.GetLogger().Warn("circuit breaker opened",
			"provider", b.config.Name,
			"failCount", count,
			"cooldownUntil", time.Unix(0, b.failUntil.Load()).Format(time.RFC3339))
	} else {
		core.GetLogger().Warn("provider failure recorded",
			"provider", b.config.Name,
			"failCount", count)
	}

	b.getEmitter().Emit(core.EventProviderFail, b.config.Name, count)
	if opened {
		b.getEmitter().Emit(core.EventProviderOpen, b.config.Name, count)
	}
}

// IsAvailable returns true if the provider has the minimum required config
// and is not in circuit-breaker cooldown.
func (b *BaseProvider) IsAvailable() bool {
	if b.config.APIKey == "" || b.config.Endpoint == "" || b.config.Model == "" {
		return false
	}
	until := b.failUntil.Load()
	if until == 0 {
		return true
	}
	return time.Now().UnixNano() >= until
}

// HealthCheck performs a lightweight connectivity check to the provider endpoint.
// It uses a 5-second timeout and does a simple HTTP GET to the base URL.
// The latency and result are recorded in the provider's health window.
func (b *BaseProvider) HealthCheck(ctx context.Context) error {
	if b.config.APIKey == "" || b.config.Endpoint == "" {
		b.RecordHealthCheck(0, false)
		return fmt.Errorf("provider %s config incomplete", b.config.Name)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	if b.config.HTTPClient != nil {
		client = b.config.HTTPClient
	}

	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, b.config.Endpoint, nil)
	if err != nil {
		b.RecordHealthCheck(0, false)
		return fmt.Errorf("health check request for %s: %w", b.config.Name, err)
	}
	req.Header.Set("Authorization", "Bearer "+b.config.APIKey)

	start := time.Now()
	resp, err := client.Do(req)
	latencyMs := time.Since(start).Milliseconds()
	if err != nil {
		b.RecordHealthCheck(latencyMs, false)
		return fmt.Errorf("health check failed for %s: %w", b.config.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		b.RecordHealthCheck(latencyMs, false)
		return fmt.Errorf("health check for %s returned status %d", b.config.Name, resp.StatusCode)
	}
	b.RecordHealthCheck(latencyMs, true)
	return nil
}

// ListModels fetches the list of available models from the provider via the
// OpenAI-compatible /models endpoint.
// Note: this creates a temporary client; for connection reuse, use OpenAIProvider.ListModels.
func (b *BaseProvider) ListModels(ctx context.Context) ([]core.ModelInfo, error) {
	if b.config.APIKey == "" || b.config.Endpoint == "" {
		return nil, fmt.Errorf("provider %s config incomplete", b.config.Name)
	}
	oc := openai.DefaultConfig(b.config.APIKey)
	oc.BaseURL = b.config.Endpoint
	client := openai.NewClientWithConfig(oc)

	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	models, err := client.ListModels(listCtx)
	if err != nil {
		return nil, fmt.Errorf("list models from %s: %w", b.config.Name, err)
	}

	result := make([]core.ModelInfo, 0, len(models.Models))
	for _, m := range models.Models {
		result = append(result, core.ModelInfo{ID: m.ID, OwnedBy: m.OwnedBy})
	}
	return result, nil
}

// SetSystemPrompt sets an override system prompt for the next completion call.
func (b *BaseProvider) SetSystemPrompt(prompt string) {
	b.sysPrompt.Store(&prompt)
}

// ClearSystemPrompt removes the system prompt override.
func (b *BaseProvider) ClearSystemPrompt() {
	b.sysPrompt.Store(nil)
}

// GetSystemPrompt returns the override if set, otherwise empty.
func (b *BaseProvider) GetSystemPrompt() string {
	if ptr := b.sysPrompt.Load(); ptr != nil {
		return *ptr
	}
	return ""
}

// ChatCompleteStream provides a default implementation that returns an error.
// Providers that support streaming should override this method.
func (b *BaseProvider) ChatCompleteStream(ctx context.Context, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error) {
	return nil, fmt.Errorf("provider %s does not support streaming", b.config.Name)
}

// OpenAIProvider implements a generic OpenAI-compatible provider adapter.
// It works with any API that follows the OpenAI chat completions protocol,
// including self-hosted models (vLLM, Ollama, etc.).
type OpenAIProvider struct {
	*BaseProvider
	Client *openai.Client
}

// NewOpenAIProvider creates a generic OpenAI-compatible provider.
// If endpoint is empty, it defaults to "https://api.openai.com/v1".
func NewOpenAIProvider(config core.ProviderConfig) *OpenAIProvider {
	if config.Endpoint == "" {
		config.Endpoint = "https://api.openai.com/v1"
	}

	oc := openai.DefaultConfig(config.APIKey)
	oc.BaseURL = config.Endpoint

	// Build the final HTTP client with support for custom HTTPClient, Proxy, and SkipTLSVerify.
	oc.HTTPClient = buildHTTPClient(config)

	return &OpenAIProvider{
		BaseProvider: NewBaseProvider(config),
		Client:       openai.NewClientWithConfig(oc),
	}
}

// buildHTTPClient assembles an *http.Client from ProviderConfig fields.
// Priority: custom HTTPClient transport > DefaultTransport, then applies Proxy and SkipTLSVerify.
// When a custom HTTPClient is provided, its CheckRedirect and Jar are preserved.
func buildHTTPClient(config core.ProviderConfig) *http.Client {
	// Fast path: no modifications needed.
	if config.Proxy == "" && !config.SkipTLSVerify {
		if config.HTTPClient != nil {
			return config.HTTPClient
		}
		return &http.Client{Timeout: 120 * time.Second}
	}

	// Slow path: need to build a transport with Proxy and/or TLS overrides.
	var base *http.Client
	if config.HTTPClient != nil {
		base = config.HTTPClient
	}

	var timeout time.Duration
	if base != nil {
		timeout = base.Timeout
	}
	if timeout == 0 {
		timeout = 120 * time.Second
	}

	transport := deriveTransport(base)

	// Apply proxy if configured.
	if config.Proxy != "" {
		proxyURL, err := url.Parse(config.Proxy)
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}

	// Apply TLS skip verify if configured.
	if config.SkipTLSVerify {
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		}
		transport.TLSClientConfig.InsecureSkipVerify = true
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
	if base != nil {
		client.CheckRedirect = base.CheckRedirect
		client.Jar = base.Jar
	}
	return client
}

// deriveTransport returns a cloneable *http.Transport from the given client,
// falling back to http.DefaultTransport if the client's transport is not cloneable.
// The returned transport always respects HTTP_PROXY/HTTPS_PROXY/NO_PROXY env vars
// unless an explicit Proxy was already configured by the caller.
func deriveTransport(base *http.Client) *http.Transport {
	if base != nil && base.Transport != nil {
		if t, ok := base.Transport.(*http.Transport); ok {
			return t.Clone()
		}
	}
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		return t.Clone()
	}
	// Fallback: create a minimal transport that still respects standard proxy env vars.
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
	}
}

func (p *OpenAIProvider) Name() string { return p.Config().Name }

// buildOpenAIMessages converts core.Message slice to go-openai messages,
// preserving tool calls, tool call IDs, and function names.
func buildOpenAIMessages(messages []core.Message) []openai.ChatCompletionMessage {
	result := make([]openai.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		result[i] = openai.ChatCompletionMessage{
			Role:       m.Role,
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		if len(m.ToolCalls) > 0 {
			result[i].ToolCalls = make([]openai.ToolCall, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				result[i].ToolCalls[j] = openai.ToolCall{
					ID:   tc.ID,
					Type: openai.ToolType(tc.Type),
					Function: openai.FunctionCall{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
				if tc.Index > 0 {
					idx := tc.Index
					result[i].ToolCalls[j].Index = &idx
				}
			}
		}
	}
	return result
}

// buildOpenAITools converts core.Tool slice to go-openai tools.
func buildOpenAITools(tools []core.Tool) []openai.Tool {
	if len(tools) == 0 {
		return nil
	}
	result := make([]openai.Tool, len(tools))
	for i, t := range tools {
		result[i] = openai.Tool{Type: openai.ToolType(t.Type)}
		if t.Function != nil {
			result[i].Function = &openai.FunctionDefinition{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
				Strict:      t.Function.Strict,
			}
		}
	}
	return result
}

// buildOpenAIToolChoice converts a core tool choice value to go-openai format.
func buildOpenAIToolChoice(choice any) any {
	if choice == nil {
		return nil
	}
	if s, ok := choice.(string); ok {
		return s
	}
	if tc, ok := choice.(core.ToolChoice); ok {
		return openai.ToolChoice{
			Type: openai.ToolType(tc.Type),
			Function: openai.ToolFunction{
				Name: tc.Function.Name,
			},
		}
	}
	return choice
}

// buildCoreChoice converts a go-openai choice to core.Choice, preserving tool calls.
func buildCoreChoice(choice openai.ChatCompletionChoice) core.Choice {
	msg := core.Message{
		Role:    choice.Message.Role,
		Content: choice.Message.Content,
		Name:    choice.Message.Name,
	}
	if choice.Message.ToolCallID != "" {
		msg.ToolCallID = choice.Message.ToolCallID
	}
	if len(choice.Message.ToolCalls) > 0 {
		msg.ToolCalls = make([]core.ToolCall, len(choice.Message.ToolCalls))
		for i, tc := range choice.Message.ToolCalls {
			msg.ToolCalls[i] = core.ToolCall{
				ID:   tc.ID,
				Type: string(tc.Type),
				Function: core.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			}
			if tc.Index != nil {
				msg.ToolCalls[i].Index = *tc.Index
			}
		}
	}
	return core.Choice{
		Index:        choice.Index,
		Message:      msg,
		FinishReason: string(choice.FinishReason),
	}
}

// buildStreamDelta converts a go-openai stream delta to core.Message, preserving tool calls.
func buildStreamDelta(delta openai.ChatCompletionStreamChoiceDelta) core.Message {
	msg := core.Message{
		Role:    delta.Role,
		Content: delta.Content,
	}
	if len(delta.ToolCalls) > 0 {
		msg.ToolCalls = make([]core.ToolCall, len(delta.ToolCalls))
		for i, tc := range delta.ToolCalls {
			msg.ToolCalls[i] = core.ToolCall{
				ID:   tc.ID,
				Type: string(tc.Type),
				Function: core.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			}
			if tc.Index != nil {
				msg.ToolCalls[i].Index = *tc.Index
			}
		}
	}
	return msg
}

// ChatCompleteStream performs a streaming chat completion.
func (p *OpenAIProvider) ChatCompleteStream(ctx context.Context, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error) {
	messages := buildOpenAIMessages(req.Messages)

	// Inject system prompt override if set.
	if sys := p.GetSystemPrompt(); sys != "" {
		messages = append([]openai.ChatCompletionMessage{{
			Role:    openai.ChatMessageRoleSystem,
			Content: sys,
		}}, messages...)
	}

	var respFormat *openai.ChatCompletionResponseFormat
	if req.ResponseFormat.Type != "" {
		respFormat = &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatType(req.ResponseFormat.Type),
		}
	}

	streamReq := openai.ChatCompletionRequest{
		Model:             req.Model,
		Messages:          messages,
		Temperature:       req.Temperature,
		MaxTokens:         req.MaxTokens,
		TopP:              req.TopP,
		PresencePenalty:   req.PresencePenalty,
		FrequencyPenalty:  req.FrequencyPenalty,
		Stop:              req.Stop,
		Seed:              req.Seed,
		ResponseFormat:    respFormat,
		Stream:            true,
		Tools:             buildOpenAITools(req.Tools),
		ToolChoice:        buildOpenAIToolChoice(req.ToolChoice),
		ParallelToolCalls: req.ParallelToolCalls,
	}
	cfg := p.Config()
	if streamReq.Model == "" {
		streamReq.Model = cfg.Model
	}

	stream, err := p.Client.CreateChatCompletionStream(ctx, streamReq)
	if err != nil {
		return nil, fmt.Errorf("%s stream: %w", p.Name(), err)
	}

	ch := make(chan core.StreamCompletionResponse, 16)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(ch)
		defer stream.Close()

		for {
			select {
			case <-ctx.Done():
				ch <- core.StreamCompletionResponse{
					Model: cfg.Model,
					Chunk: core.StreamChunk{FinishReason: "cancelled"},
					Err:   ctx.Err(),
				}
				return
			default:
			}

			response, err := stream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				ch <- core.StreamCompletionResponse{
					Model: cfg.Model,
					Chunk: core.StreamChunk{
						FinishReason: "error",
					},
					Err: fmt.Errorf("%s stream recv: %w", p.Name(), err),
				}
				return
			}

			var usage core.Usage
			if response.Usage != nil {
				usage = core.Usage{
					PromptTokens:     response.Usage.PromptTokens,
					CompletionTokens: response.Usage.CompletionTokens,
					TotalTokens:      response.Usage.TotalTokens,
				}
			}

			for _, choice := range response.Choices {
				select {
				case <-ctx.Done():
					return
				case ch <- core.StreamCompletionResponse{
					ID:    response.ID,
					Model: response.Model,
					Chunk: core.StreamChunk{
						Index: choice.Index,
						Delta: buildStreamDelta(choice.Delta),
						FinishReason: string(choice.FinishReason),
					},
					Usage: usage,
				}:
				}
			}
		}
	}()

	return ch, nil
}

func (p *OpenAIProvider) ChatComplete(ctx context.Context, req core.ChatCompletionRequest) (result *core.ChatCompletionResponse, err error) {
	start := time.Now()
	defer func() {
		latencyMs := time.Since(start).Milliseconds()
		if r := recover(); r != nil {
			core.GetLogger().Error("provider panic recovered",
				"provider", p.Name(),
				"panic", r)
			p.RecordFailure()
			p.RecordCallResult(latencyMs, fmt.Errorf("panic: %v", r))
			err = fmt.Errorf("provider panic: %v", r)
			return
		}
		if err != nil {
			p.RecordFailure()
			p.RecordCallResult(latencyMs, err)
		} else {
			p.RecordSuccess()
			p.RecordCallResult(latencyMs, nil)
		}
	}()

	messages := buildOpenAIMessages(req.Messages)

	// Inject system prompt override if set.
	if sys := p.GetSystemPrompt(); sys != "" {
		messages = append([]openai.ChatCompletionMessage{{
			Role:    openai.ChatMessageRoleSystem,
			Content: sys,
		}}, messages...)
	}

	var respFormat *openai.ChatCompletionResponseFormat
	if req.ResponseFormat.Type != "" {
		respFormat = &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatType(req.ResponseFormat.Type),
		}
	}

	streamReq := openai.ChatCompletionRequest{
		Model:             req.Model,
		Messages:          messages,
		Temperature:       req.Temperature,
		MaxTokens:         req.MaxTokens,
		TopP:              req.TopP,
		PresencePenalty:   req.PresencePenalty,
		FrequencyPenalty:  req.FrequencyPenalty,
		Stop:              req.Stop,
		Seed:              req.Seed,
		ResponseFormat:    respFormat,
		Stream:            req.Stream,
		Tools:             buildOpenAITools(req.Tools),
		ToolChoice:        buildOpenAIToolChoice(req.ToolChoice),
		ParallelToolCalls: req.ParallelToolCalls,
	}
	resp, err := p.Client.CreateChatCompletion(ctx, streamReq)
	if err != nil {
		// Compatibility retry: some providers (e.g. Kimi kimi-k2.6) only accept temperature=1.
		// If error mentions temperature, retry without setting it (omitted field defaults to 1).
		if isTemperatureError(err) && req.Temperature != 1 {
			streamReq.Temperature = 0 // omitempty will drop it
			resp, err = p.Client.CreateChatCompletion(ctx, streamReq)
		}
		if err != nil {
			return nil, fmt.Errorf("%s chat complete: %w", p.Name(), err)
		}
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("%s chat complete: no choices in response", p.Name())
	}

	result = &core.ChatCompletionResponse{
		ID:      resp.ID,
		Model:   resp.Model,
		Choices: []core.Choice{buildCoreChoice(resp.Choices[0])},
		Usage: core.Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}
	if resp.Created > 0 {
		result.CreatedAt = time.Unix(resp.Created, 0)
	}
	return result, nil
}

// ListModels fetches the list of available models from the provider via the
// OpenAI-compatible /models endpoint. It reuses the provider's HTTP client.
func isTemperatureError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "temperature")
}

func (p *OpenAIProvider) ListModels(ctx context.Context) ([]core.ModelInfo, error) {
	if p.Config().APIKey == "" || p.Config().Endpoint == "" {
		return nil, fmt.Errorf("provider %s config incomplete", p.Config().Name)
	}

	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	models, err := p.Client.ListModels(listCtx)
	if err != nil {
		return nil, fmt.Errorf("list models from %s: %w", p.Config().Name, err)
	}

	var result []core.ModelInfo
	for _, m := range models.Models {
		result = append(result, core.ModelInfo{ID: m.ID, OwnedBy: m.OwnedBy})
	}
	return result, nil
}

// NewDeepSeekProvider creates a DeepSeek provider with sensible defaults.
func NewDeepSeekProvider(config core.ProviderConfig) Provider {
	if config.Endpoint == "" {
		config.Endpoint = "https://api.deepseek.com"
	}
	if config.Model == "" {
		config.Model = "deepseek-v4-pro"
	}
	if config.Name == "" {
		config.Name = "deepseek"
	}
	return NewOpenAIProvider(config)
}

// NewKimiProvider creates a Kimi (Moonshot AI) provider with sensible defaults.
func NewKimiProvider(config core.ProviderConfig) Provider {
	if config.Endpoint == "" {
		config.Endpoint = "https://api.moonshot.cn/v1"
	}
	if config.Model == "" {
		config.Model = "kimi-k2.6"
	}
	if config.Name == "" {
		config.Name = "kimi"
	}
	return NewOpenAIProvider(config)
}

// NewGLMProvider creates a GLM (Zhipu AI) provider with sensible defaults.
func NewGLMProvider(config core.ProviderConfig) Provider {
	if config.Endpoint == "" {
		config.Endpoint = "https://open.bigmodel.cn/api/paas/v4"
	}
	if config.Model == "" {
		config.Model = "glm-5.1"
	}
	if config.Name == "" {
		config.Name = "glm"
	}
	return NewOpenAIProvider(config)
}

// NewQwenProvider creates a Qwen (Alibaba Tongyi) provider with sensible defaults.
func NewQwenProvider(config core.ProviderConfig) Provider {
	if config.Endpoint == "" {
		config.Endpoint = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	}
	if config.Model == "" {
		config.Model = "qwen3.7-max"
	}
	if config.Name == "" {
		config.Name = "qwen"
	}
	return NewOpenAIProvider(config)
}

// Close releases any resources held by the provider.
// The default implementation is a no-op; override in concrete providers if needed.
func (b *BaseProvider) Close() error { return nil }

// FailUntil returns the circuit breaker cooldown deadline (zero if not active).
func (b *BaseProvider) FailUntil() time.Time {
	if until := b.failUntil.Load(); until != 0 {
		return time.Unix(0, until)
	}
	return time.Time{}
}

// SetFailUntil sets the circuit breaker cooldown deadline (for testing).
func (b *BaseProvider) SetFailUntil(t time.Time) {
	if t.IsZero() {
		b.failUntil.Store(0)
	} else {
		b.failUntil.Store(t.UnixNano())
	}
}
