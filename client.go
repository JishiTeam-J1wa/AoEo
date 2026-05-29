package aoeo

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/internal/engine"
	"github.com/JishiTeam-J1wa/AoEo/providers"
)

// Type aliases for backward compatibility.
type (
	ChatCompletionRequest    = core.ChatCompletionRequest
	ChatCompletionResponse   = core.ChatCompletionResponse
	Message                  = core.Message
	Choice                   = core.Choice
	Usage                    = core.Usage
	ModelInfo                = core.ModelInfo
	ResponseFormat           = core.ResponseFormat
	StreamChunk              = core.StreamChunk
	StreamCompletionResponse = core.StreamCompletionResponse
	DualResult               = core.DualResult
	AuditResult              = core.AuditResult
	ProviderStatus           = core.ProviderStatus
	Pricing                  = core.Pricing
	Config                   = core.Config
	ProviderConfig           = core.ProviderConfig
	RetryConfig              = core.RetryConfig
	EventEmitter             = core.EventEmitter
	NopEmitter               = core.NopEmitter
	Logger                   = core.Logger
	CallRecord               = engine.CallRecord
	ProviderStats            = engine.ProviderStats
	History                  = engine.History
	PromptTemplate           = engine.PromptTemplate
	PromptInjector           = engine.PromptInjector
	Scheduler                = engine.Scheduler
	SchedulerOption          = engine.SchedulerOption
	Provider                 = providers.Provider
)

// Convenience re-exports.
var (
	NewHistory              = engine.NewHistory
	NewPromptInjector       = engine.NewPromptInjector
	SetLogger               = core.SetLogger
	GetLogger               = core.GetLogger
	DefaultPricing          = core.DefaultPricing
	DefaultRetryConfig      = core.DefaultRetryConfig
	IsRetryableError        = core.IsRetryableError
	ValidateConfig          = core.ValidateConfig
	ExtractJSON             = engine.ExtractJSON
	ExtractField            = engine.ExtractField
	MergeChoices            = engine.MergeChoices
	Consensus               = engine.Consensus
	ParseSSE                = engine.ParseSSE
	CreateProvider          = engine.CreateProvider
	NewScheduler            = engine.NewScheduler
	NewSchedulerWithOptions = engine.NewSchedulerWithOptions
	NewDeepSeekProvider     = providers.NewDeepSeekProvider
	NewKimiProvider         = providers.NewKimiProvider
	NewGLMProvider          = providers.NewGLMProvider
	NewQwenProvider         = providers.NewQwenProvider
	NewOpenAIProvider       = providers.NewOpenAIProvider
	NewBaseProvider         = providers.NewBaseProvider
)

// WithXxx options re-exported from engine.
var (
	WithTimeout              = engine.WithTimeout
	WithHistory              = engine.WithHistory
	WithRetry                = engine.WithRetry
	WithPromptInjector       = engine.WithPromptInjector
	InjectPrompts            = engine.InjectPrompts
	WithSystemPromptInjector = engine.WithSystemPromptInjector
)

// Event constants re-exported.
const (
	EventProviderFail    = core.EventProviderFail
	EventProviderOpen    = core.EventProviderOpen
	EventProviderRecover = core.EventProviderRecover
	EventFallbackTrigger = core.EventFallbackTrigger
	EventAuditDisagree   = core.EventAuditDisagree
	EventDualComplete    = core.EventDualComplete
)

// Sentinel errors re-exported for SDK consumers.
var (
	ErrSchedulerClosed          = engine.ErrSchedulerClosed
	ErrNoAvailableProvider      = engine.ErrNoAvailableProvider
	ErrProviderNotFound         = engine.ErrProviderNotFound
	ErrAllProvidersFailed       = engine.ErrAllProvidersFailed
	ErrProviderConfigIncomplete = engine.ErrProviderConfigIncomplete
)

// Client is the high-level entry point for AoEo.
type Client struct {
	scheduler *engine.Scheduler
	emitterMu sync.RWMutex
	emitter   core.EventEmitter
}

// NewClient creates a new AoEo client from a configuration.
func NewClient(cfg core.Config, opts ...engine.SchedulerOption) (*Client, error) {
	if issues := cfg.Validate(); len(issues) > 0 {
		return nil, fmt.Errorf("config validation failed: %v", issues)
	}
	s := engine.NewSchedulerWithOptions(nil, opts...)
	if err := s.ApplyConfig(cfg); err != nil {
		return nil, fmt.Errorf("apply config: %w", err)
	}
	return &Client{
		scheduler: s,
		emitter:   core.NopEmitter{},
	}, nil
}

// NewClientWithProviders creates a client directly from provider instances.
func NewClientWithProviders(provs ...providers.Provider) *Client {
	return &Client{
		scheduler: engine.NewScheduler(provs...),
		emitter:   core.NopEmitter{},
	}
}

// History returns the attached history recorder (may be nil).
func (c *Client) History() *engine.History {
	return c.scheduler.History()
}

// Stats returns aggregate statistics from history.
func (c *Client) Stats() map[string]engine.ProviderStats {
	if c.scheduler.History() == nil {
		return nil
	}
	return c.scheduler.History().Stats()
}

// SetEmitter attaches an event emitter to the client.
func (c *Client) SetEmitter(emitter core.EventEmitter) {
	c.emitterMu.Lock()
	defer c.emitterMu.Unlock()
	if emitter == nil {
		c.emitter = core.NopEmitter{}
	} else {
		c.emitter = emitter
	}
	// Propagate to providers.
	for _, p := range c.scheduler.AvailableProviders() {
		p.SetEmitter(emitter)
	}
}

func (c *Client) emit(topic string, data ...any) {
	c.emitterMu.RLock()
	em := c.emitter
	c.emitterMu.RUnlock()
	em.Emit(topic, data...)
}

// ChatComplete performs a single chat completion using the primary provider.
func (c *Client) ChatComplete(ctx context.Context, req core.ChatCompletionRequest) (*core.ChatCompletionResponse, error) {
	return c.scheduler.ChatComplete(ctx, req)
}

// ChatCompleteWithFallback tries primary provider first, then falls back.
func (c *Client) ChatCompleteWithFallback(ctx context.Context, req core.ChatCompletionRequest) (*core.ChatCompletionResponse, error) {
	resp, err := c.scheduler.ChatCompleteWithFallback(ctx, req)
	if err != nil && errors.Is(err, engine.ErrAllProvidersFailed) {
		if available := c.scheduler.AvailableProviders(); len(available) > 0 {
			c.emit(core.EventFallbackTrigger, fmt.Sprintf("all providers failed, primary %s: %v", available[0].Name(), err))
		}
	}
	return resp, err
}

// ChatCompleteDual sends the request to two different providers concurrently.
func (c *Client) ChatCompleteDual(ctx context.Context, req core.ChatCompletionRequest) (*core.DualResult, error) {
	result, err := c.scheduler.ChatCompleteDual(ctx, req)
	if err != nil {
		return nil, err
	}
	c.emit(core.EventDualComplete, result.Consensus)
	return result, nil
}

// ChatCompleteStream performs a streaming chat completion.
func (c *Client) ChatCompleteStream(ctx context.Context, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error) {
	return c.scheduler.ChatCompleteStream(ctx, req)
}

// ListModels returns available models for the named provider.
func (c *Client) ListModels(ctx context.Context, providerName string) ([]core.ModelInfo, error) {
	return c.scheduler.ListModels(ctx, providerName)
}

// TestProvider tests connectivity to a provider.
func (c *Client) TestProvider(ctx context.Context, providerName string) error {
	return c.scheduler.TestProvider(ctx, providerName)
}

// ProviderStatus returns the current status of all providers.
func (c *Client) ProviderStatus() []core.ProviderStatus {
	return c.scheduler.ProviderStatus()
}

// PromptInjector returns the attached prompt injector (may be nil).
func (c *Client) PromptInjector() *engine.PromptInjector {
	return c.scheduler.PromptInjector()
}

// SetPromptInjector attaches a prompt injector to the client.
func (c *Client) SetPromptInjector(pi *engine.PromptInjector) {
	c.scheduler.SetPromptInjector(pi)
}

// Close gracefully shuts down the client. It is safe to call multiple times.
func (c *Client) Close() error {
	return c.scheduler.Close()
}

// IsClosed reports whether the client has been closed.
func (c *Client) IsClosed() bool {
	return c.scheduler.IsClosed()
}

// SetTimeout updates the per-provider request timeout at runtime.
func (c *Client) SetTimeout(d time.Duration) {
	c.scheduler.SetTimeout(d)
}

// Scheduler returns the underlying scheduler for advanced use.
func (c *Client) Scheduler() *engine.Scheduler {
	return c.scheduler
}

// Audit performs a secondary completion using a different provider and compares results.
func (c *Client) Audit(ctx context.Context, req core.ChatCompletionRequest) (*core.AuditResult, error) {
	result, err := c.scheduler.Audit(ctx, req)
	if err != nil {
		return nil, err
	}
	if !result.Consensus {
		c.emit(core.EventAuditDisagree, result.Primary.Model, result.Audit.Model)
	}
	return result, nil
}
