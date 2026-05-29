package providers

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/sashabaranov/go-openai"
)

// Provider is the interface that all AI provider adapters must implement.
type Provider interface {
	Name() string
	Config() core.ProviderConfig
	ChatComplete(ctx context.Context, req core.ChatCompletionRequest) (*core.ChatCompletionResponse, error)
	IsAvailable() bool
	ListModels(ctx context.Context) ([]core.ModelInfo, error)
	SetEmitter(e core.EventEmitter)
}

// BaseProvider provides common logic for all providers (circuit breaker, system prompt override).
// It is exported for use by custom provider implementations, but most users should use
// the built-in providers or the generic OpenAIProvider.
type BaseProvider struct {
	config core.ProviderConfig

	// Circuit breaker: track consecutive failures.
	failMu    sync.Mutex
	failCount int
	failUntil time.Time

	// System prompt override.
	sysMu                sync.RWMutex
	systemPromptOverride string

	// Optional event emitter for provider lifecycle events.
	emitterMu sync.RWMutex
	emitter   core.EventEmitter
}

// NewBaseProvider creates a BaseProvider with the given config.
func NewBaseProvider(config core.ProviderConfig) *BaseProvider {
	return &BaseProvider{
		config:  config,
		emitter: core.NopEmitter{},
	}
}

// Config returns the provider's configuration.
func (b *BaseProvider) Config() core.ProviderConfig {
	return b.config
}

// SetEmitter attaches an event emitter to the provider.
func (b *BaseProvider) SetEmitter(e core.EventEmitter) {
	b.emitterMu.Lock()
	defer b.emitterMu.Unlock()
	if e == nil {
		b.emitter = core.NopEmitter{}
	} else {
		b.emitter = e
	}
}

func (b *BaseProvider) getEmitter() core.EventEmitter {
	b.emitterMu.RLock()
	defer b.emitterMu.RUnlock()
	return b.emitter
}

// RecordSuccess resets the failure counter on a successful call.
func (b *BaseProvider) RecordSuccess() {
	b.failMu.Lock()
	wasFailed := b.failCount > 0
	b.failCount = 0
	b.failUntil = time.Time{}
	b.failMu.Unlock()

	if wasFailed {
		b.getEmitter().Emit(core.EventProviderRecover, b.config.Name)
	}
}

// RecordFailure increments the failure counter and triggers cooldown after 3 consecutive failures.
func (b *BaseProvider) RecordFailure() {
	b.failMu.Lock()
	b.failCount++
	opened := false
	if b.failCount >= 3 {
		b.failUntil = time.Now().Add(60 * time.Second)
		opened = true
		core.GetLogger().Warn("circuit breaker opened",
			"provider", b.config.Name,
			"failCount", b.failCount,
			"cooldownUntil", b.failUntil.Format(time.RFC3339))
	} else {
		core.GetLogger().Warn("provider failure recorded",
			"provider", b.config.Name,
			"failCount", b.failCount)
	}
	b.failMu.Unlock()

	b.getEmitter().Emit(core.EventProviderFail, b.config.Name, b.failCount)
	if opened {
		b.getEmitter().Emit(core.EventProviderOpen, b.config.Name, b.failCount)
	}
}

// IsAvailable returns true if the provider has the minimum required config
// and is not in circuit-breaker cooldown.
func (b *BaseProvider) IsAvailable() bool {
	if b.config.APIKey == "" || b.config.Endpoint == "" || b.config.Model == "" {
		return false
	}
	b.failMu.Lock()
	cooldown := b.failUntil.After(time.Now())
	b.failMu.Unlock()
	if cooldown {
		return false
	}
	return true
}

// ListModels fetches the list of available models from the provider via the
// OpenAI-compatible /models endpoint.
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

	var result []core.ModelInfo
	for _, m := range models.Models {
		result = append(result, core.ModelInfo{ID: m.ID, OwnedBy: m.OwnedBy})
	}
	return result, nil
}

// SetSystemPrompt sets an override system prompt for the next completion call.
func (b *BaseProvider) SetSystemPrompt(prompt string) {
	b.sysMu.Lock()
	b.systemPromptOverride = prompt
	b.sysMu.Unlock()
}

// ClearSystemPrompt removes the system prompt override.
func (b *BaseProvider) ClearSystemPrompt() {
	b.sysMu.Lock()
	b.systemPromptOverride = ""
	b.sysMu.Unlock()
}

// GetSystemPrompt returns the override if set, otherwise empty.
func (b *BaseProvider) GetSystemPrompt() string {
	b.sysMu.RLock()
	override := b.systemPromptOverride
	b.sysMu.RUnlock()
	return override
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
	if config.SkipTLSVerify {
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		oc.HTTPClient = &http.Client{Transport: tr}
	}

	return &OpenAIProvider{
		BaseProvider: NewBaseProvider(config),
		Client:       openai.NewClientWithConfig(oc),
	}
}

func (p *OpenAIProvider) Name() string { return p.Config().Name }

func (p *OpenAIProvider) ChatComplete(ctx context.Context, req core.ChatCompletionRequest) (result *core.ChatCompletionResponse, err error) {
	defer func() {
		if r := recover(); r != nil {
			core.GetLogger().Error("provider panic recovered",
				"provider", p.Name(),
				"panic", r)
			p.RecordFailure()
			err = fmt.Errorf("provider panic: %v", r)
			return
		}
		if err != nil {
			p.RecordFailure()
		} else {
			p.RecordSuccess()
		}
	}()

	messages := make([]openai.ChatCompletionMessage, len(req.Messages))
	for i, m := range req.Messages {
		messages[i] = openai.ChatCompletionMessage{
			Role:    m.Role,
			Content: m.Content,
		}
	}

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

	resp, err := p.Client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:          req.Model,
		Messages:       messages,
		Temperature:    req.Temperature,
		MaxTokens:      req.MaxTokens,
		ResponseFormat: respFormat,
	})
	if err != nil {
		// Compatibility retry: some providers (e.g. Kimi kimi-k2.6) only accept temperature=1.
		// If error mentions temperature, retry without setting it (omitted field defaults to 1).
		if isTemperatureError(err) && req.Temperature != 0 {
			resp, err = p.Client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
				Model:          req.Model,
				Messages:       messages,
				MaxTokens:      req.MaxTokens,
				ResponseFormat: respFormat,
			})
		}
		if err != nil {
			return nil, fmt.Errorf("%s chat complete: %w", p.Name(), err)
		}
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("%s chat complete: no choices in response", p.Name())
	}

	result = &core.ChatCompletionResponse{
		ID:    resp.ID,
		Model: resp.Model,
		Choices: []core.Choice{{
			Index: 0,
			Message: core.Message{
				Role:    resp.Choices[0].Message.Role,
				Content: resp.Choices[0].Message.Content,
			},
			FinishReason: string(resp.Choices[0].FinishReason),
		}},
		Usage: core.Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
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

// FailUntil returns the circuit breaker cooldown deadline (zero if not active).
func (b *BaseProvider) FailUntil() time.Time {
	b.failMu.Lock()
	defer b.failMu.Unlock()
	return b.failUntil
}

// SetFailUntil sets the circuit breaker cooldown deadline (for testing).
func (b *BaseProvider) SetFailUntil(t time.Time) {
	b.failMu.Lock()
	defer b.failMu.Unlock()
	b.failUntil = t
}
