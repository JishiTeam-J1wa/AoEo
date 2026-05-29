package aoeo

import (
	"context"
	"fmt"
	"sync"
)

// Client is the high-level entry point for AoEo.
// It wraps a Scheduler with a fluent API and optional event emission.
type Client struct {
	scheduler *Scheduler
	emitterMu sync.RWMutex
	emitter   EventEmitter
}

// NewClient creates a new AoEo client from a configuration.
func NewClient(cfg Config, opts ...SchedulerOption) (*Client, error) {
	if issues := cfg.Validate(); len(issues) > 0 {
		return nil, fmt.Errorf("config validation failed: %v", issues)
	}
	s := NewSchedulerWithOptions(nil, opts...)
	if err := s.ApplyConfig(cfg); err != nil {
		return nil, fmt.Errorf("apply config: %w", err)
	}
	return &Client{
		scheduler: s,
		emitter:   NopEmitter{},
	}, nil
}

// NewClientWithProviders creates a client directly from provider instances.
func NewClientWithProviders(providers ...Provider) *Client {
	return &Client{
		scheduler: NewScheduler(providers...),
		emitter:   NopEmitter{},
	}
}

// History returns the attached history recorder (may be nil).
func (c *Client) History() *History {
	return c.scheduler.history
}

// Stats returns aggregate statistics from history.
func (c *Client) Stats() map[string]ProviderStats {
	if c.scheduler.history == nil {
		return nil
	}
	return c.scheduler.history.Stats()
}

// SetEmitter attaches an event emitter to the client.
func (c *Client) SetEmitter(emitter EventEmitter) {
	c.emitterMu.Lock()
	defer c.emitterMu.Unlock()
	if emitter == nil {
		c.emitter = NopEmitter{}
	} else {
		c.emitter = emitter
	}
}

func (c *Client) emit(topic string, data ...any) {
	c.emitterMu.RLock()
	em := c.emitter
	c.emitterMu.RUnlock()
	em.Emit(topic, data...)
}

// ChatComplete performs a single chat completion using the primary provider.
func (c *Client) ChatComplete(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	return c.scheduler.ChatComplete(ctx, req)
}

// ChatCompleteWithFallback tries primary provider first, then falls back.
func (c *Client) ChatCompleteWithFallback(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	available := c.scheduler.availableProviders()
	if len(available) > 0 {
		primary := available[0]
		resp, err := c.scheduler.ChatCompleteWithFallback(ctx, req)
		if err != nil {
			c.emit(EventFallbackTrigger, fmt.Sprintf("all providers failed, primary %s: %v", primary.Name(), err))
		} else if resp != nil && primary.Name() != resp.Model && len(available) > 1 {
			c.emit(EventFallbackTrigger, fmt.Sprintf("fallback activated: %s -> %s", primary.Name(), resp.Model))
		}
		return resp, err
	}
	return c.scheduler.ChatCompleteWithFallback(ctx, req)
}

// ChatCompleteDual sends the request to two different providers concurrently.
func (c *Client) ChatCompleteDual(ctx context.Context, req ChatCompletionRequest) (*DualResult, error) {
	return c.scheduler.ChatCompleteDual(ctx, req)
}

// ChatCompleteStream performs a streaming chat completion.
func (c *Client) ChatCompleteStream(ctx context.Context, req ChatCompletionRequest) (<-chan StreamCompletionResponse, error) {
	return c.scheduler.ChatCompleteStream(ctx, req)
}

// ListModels returns available models for the named provider.
func (c *Client) ListModels(ctx context.Context, providerName string) ([]ModelInfo, error) {
	return c.scheduler.ListModels(ctx, providerName)
}

// TestProvider tests connectivity to a provider.
func (c *Client) TestProvider(ctx context.Context, providerName string) error {
	return c.scheduler.TestProvider(ctx, providerName)
}

// ProviderStatus returns the current status of all providers.
func (c *Client) ProviderStatus() []ProviderStatus {
	return c.scheduler.ProviderStatus()
}

// PromptInjector returns the attached prompt injector (may be nil).
func (c *Client) PromptInjector() *PromptInjector {
	return c.scheduler.promptInjector
}

// SetPromptInjector attaches a prompt injector to the client.
func (c *Client) SetPromptInjector(pi *PromptInjector) {
	c.scheduler.promptInjector = pi
}

// Close gracefully shuts down the client, waiting for in-flight requests to complete.
func (c *Client) Close() error {
	return c.scheduler.Close()
}

// Scheduler returns the underlying scheduler for advanced use.
func (c *Client) Scheduler() *Scheduler {
	return c.scheduler
}
