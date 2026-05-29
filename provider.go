package aoeo

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sashabaranov/go-openai"
)

// Provider is the interface that all AI provider adapters must implement.
type Provider interface {
	Name() string
	Config() ProviderConfig
	ChatComplete(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error)
	IsAvailable() bool
	ListModels(ctx context.Context) ([]ModelInfo, error)
}

// BaseProvider provides common logic for all providers (circuit breaker, system prompt override).
// It is exported for use by custom provider implementations, but most users should use
// the built-in providers or the generic OpenAIProvider.
type BaseProvider struct {
	config ProviderConfig

	// Circuit breaker: track consecutive failures.
	failMu    sync.Mutex
	failCount int
	failUntil time.Time

	// System prompt override.
	sysMu                sync.RWMutex
	systemPromptOverride string

	// Optional event emitter for provider lifecycle events.
	emitterMu sync.RWMutex
	emitter   EventEmitter
}

// NewBaseProvider creates a BaseProvider with the given config.
func NewBaseProvider(config ProviderConfig) *BaseProvider {
	return &BaseProvider{
		config:  config,
		emitter: NopEmitter{},
	}
}

// Config returns the provider's configuration.
func (b *BaseProvider) Config() ProviderConfig {
	return b.config
}

// SetEmitter attaches an event emitter to the provider.
func (b *BaseProvider) SetEmitter(e EventEmitter) {
	b.emitterMu.Lock()
	defer b.emitterMu.Unlock()
	if e == nil {
		b.emitter = NopEmitter{}
	} else {
		b.emitter = e
	}
}

func (b *BaseProvider) getEmitter() EventEmitter {
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
		b.getEmitter().Emit(EventProviderRecover, b.config.Name)
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
		GetLogger().Warn("circuit breaker opened",
			"provider", b.config.Name,
			"failCount", b.failCount,
			"cooldownUntil", b.failUntil.Format(time.RFC3339))
	} else {
		GetLogger().Warn("provider failure recorded",
			"provider", b.config.Name,
			"failCount", b.failCount)
	}
	b.failMu.Unlock()

	b.getEmitter().Emit(EventProviderFail, b.config.Name, b.failCount)
	if opened {
		b.getEmitter().Emit(EventProviderOpen, b.config.Name, b.failCount)
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
func (b *BaseProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
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

	var result []ModelInfo
	for _, m := range models.Models {
		result = append(result, ModelInfo{ID: m.ID, OwnedBy: m.OwnedBy})
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
