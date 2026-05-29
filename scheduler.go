package aoeo

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// SchedulerOption configures a Scheduler.
type SchedulerOption func(*Scheduler)

// WithTimeout sets the per-provider request timeout.
func WithTimeout(d time.Duration) SchedulerOption {
	return func(s *Scheduler) {
		s.timeout = d
	}
}

// WithHistory attaches a History recorder to the scheduler.
func WithHistory(h *History) SchedulerOption {
	return func(s *Scheduler) {
		s.history = h
	}
}

// WithRetry sets the retry configuration for the scheduler.
func WithRetry(cfg RetryConfig) SchedulerOption {
	return func(s *Scheduler) {
		s.retry = cfg
	}
}

type availCacheEntry struct {
	providers []Provider
	time      time.Time
}

// Scheduler manages multiple AI providers with load balancing, circuit breaking,
// and concurrency control. It is the core of AoEo's multi-provider aggregation.
type Scheduler struct {
	mu           sync.RWMutex
	providers    []Provider
	providerCfgs []ProviderConfig
	sem          *adaptiveSemaphore

	// Round-robin index for fallback/load balancing.
	rrIndex uint64

	// Configurable timeout (default 45s).
	timeout time.Duration

	// Optional history recorder.
	history *History

	// Optional retry configuration.
	retry RetryConfig

	// Optional prompt injector.
	promptInjector *PromptInjector

	// Cached available providers (refreshed on access if stale).
	availCache    atomic.Pointer[availCacheEntry]
	availCacheTTL time.Duration

	// Graceful shutdown tracking.
	closed   atomic.Bool
	closeMu  sync.Mutex
	closeCh  chan struct{}
}

// NewScheduler creates a new scheduler with the given providers.
// If no providers are given, call ApplyConfig later.
func NewScheduler(providers ...Provider) *Scheduler {
	totalSlots := 0
	for _, p := range providers {
		if cfg := p.Config(); cfg.Name != "" {
			slots := cfg.MaxConcurrent
			if slots <= 0 {
				slots = 2
			}
			totalSlots += slots
		}
	}
	if totalSlots == 0 {
		totalSlots = 4
	}

	s := &Scheduler{
		providers:     providers,
		sem:           newAdaptiveSemaphore(totalSlots),
		timeout:       45 * time.Second,
		availCacheTTL: 1 * time.Second,
		closeCh:       make(chan struct{}),
	}
	return s
}

// NewSchedulerWithOptions creates a scheduler with options.
func NewSchedulerWithOptions(providers []Provider, opts ...SchedulerOption) *Scheduler {
	s := NewScheduler(providers...)
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ApplyConfig applies the given configuration, creating provider instances.
func (s *Scheduler) ApplyConfig(cfg Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var providers []Provider
	var cfgs []ProviderConfig
	totalSlots := 0
	for _, pc := range cfg.Providers {
		p := CreateProvider(pc)
		if p != nil {
			providers = append(providers, p)
			cfgs = append(cfgs, pc)
			slots := pc.MaxConcurrent
			if slots <= 0 {
				slots = 2
			}
			totalSlots += slots
		}
	}
	s.providers = providers
	s.providerCfgs = cfgs
	s.availCache.Store(nil) // Invalidate cache.

	if totalSlots > 0 {
		s.sem.setMaxConc(totalSlots)
	} else {
		s.sem.setMaxConc(4)
	}

	return nil
}

// CreateProvider creates the appropriate provider instance based on config name.
func CreateProvider(cfg ProviderConfig) Provider {
	switch cfg.Name {
	case "deepseek":
		return NewDeepSeekProvider(cfg)
	case "glm":
		return NewGLMProvider(cfg)
	case "qwen":
		return NewQwenProvider(cfg)
	case "kimi":
		return NewKimiProvider(cfg)
	default:
		return NewOpenAIProvider(cfg)
	}
}

func (s *Scheduler) checkClosed() error {
	if s.closed.Load() {
		return fmt.Errorf("scheduler is closed")
	}
	return nil
}

// Close gracefully shuts down the scheduler, waiting for in-flight requests to complete.
func (s *Scheduler) Close() error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()

	if s.closed.Swap(true) {
		return fmt.Errorf("scheduler already closed")
	}
	close(s.closeCh)
	return nil
}

// ChatComplete performs a chat completion using the primary (first available) provider.
func (s *Scheduler) ChatComplete(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	if err := s.sem.acquire(ctx); err != nil {
		return nil, err
	}
	defer s.sem.release()

	p := s.PickPrimaryProvider()
	if p == nil {
		return nil, fmt.Errorf("no available provider")
	}

	// Fill default model from provider config if not specified.
	reqCopy := req
	if reqCopy.Model == "" {
		reqCopy.Model = p.Config().Model
	}

	// Apply prompt injection if configured.
	if s.promptInjector != nil {
		s.promptInjector.Inject(p.Name(), reqCopy.Model, &reqCopy)
	}

	providerCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	start := time.Now()
	var resp *ChatCompletionResponse
	var err error

	if s.retry.MaxRetries > 0 {
		err = doRetry(providerCtx, s.retry, func() error {
			var innerErr error
			resp, innerErr = p.ChatComplete(providerCtx, reqCopy)
			return innerErr
		})
	} else {
		resp, err = p.ChatComplete(providerCtx, reqCopy)
	}

	if s.history != nil {
		record := CallRecord{
			ID:        fmt.Sprintf("%s-%d", p.Name(), start.UnixNano()),
			Provider:  p.Name(),
			Model:     reqCopy.Model,
			Request:   reqCopy,
			Response:  resp,
			LatencyMs: time.Since(start).Milliseconds(),
			Timestamp: start,
			Tags:      req.Tags,
		}
		if err != nil {
			record.Error = err.Error()
		}
		if resp != nil {
			pricing := p.Config().Pricing
			if pricing.Currency == "" && pricing.PromptPer1K == 0 && pricing.CompletionPer1K == 0 {
				pricing = DefaultPricing(p.Name(), reqCopy.Model)
			}
			record.Cost = resp.Usage.Cost(pricing)
			record.Currency = pricing.Currency
		}
		s.history.Record(record)
	}

	return resp, err
}

// ChatCompleteWithFallback tries the primary provider first; on failure,
// it falls back to the next available provider automatically.
func (s *Scheduler) ChatCompleteWithFallback(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	available := s.availableProviders()
	if len(available) == 0 {
		return nil, fmt.Errorf("no available provider")
	}

	var lastErr error
	var fallbackFrom string
	for i, p := range available {
		if err := s.sem.acquire(ctx); err != nil {
			return nil, err
		}
		// Fill default model if needed (use copy to avoid polluting the original req).
		reqCopy := req
		if reqCopy.Model == "" {
			reqCopy.Model = p.Config().Model
		}
		if s.promptInjector != nil {
			s.promptInjector.Inject(p.Name(), reqCopy.Model, &reqCopy)
		}
		providerCtx, cancel := context.WithTimeout(ctx, s.timeout)
		start := time.Now()
		resp, err := p.ChatComplete(providerCtx, reqCopy)
		cancel()
		s.sem.release()

		// Record history.
		if s.history != nil {
			record := CallRecord{
				ID:           fmt.Sprintf("%s-%d", p.Name(), start.UnixNano()),
				Provider:     p.Name(),
				Model:        reqCopy.Model,
				Request:      reqCopy,
				Response:     resp,
				LatencyMs:    time.Since(start).Milliseconds(),
				Timestamp:    start,
				Tags:         req.Tags,
				FallbackFrom: fallbackFrom,
			}
			if err != nil {
				record.Error = err.Error()
			}
			if resp != nil {
				pricing := p.Config().Pricing
				if pricing.Currency == "" && pricing.PromptPer1K == 0 && pricing.CompletionPer1K == 0 {
					pricing = DefaultPricing(p.Name(), reqCopy.Model)
				}
				record.Cost = resp.Usage.Cost(pricing)
				record.Currency = pricing.Currency
			}
			s.history.Record(record)
		}

		if err == nil {
			return resp, nil
		}
		lastErr = err
		if i == 0 {
			fallbackFrom = p.Name()
		}
	}
	return nil, fmt.Errorf("all providers failed, last error: %w", lastErr)
}

// ChatCompleteDual sends the request to two different providers concurrently
// and returns both results for comparison/merging.
func (s *Scheduler) ChatCompleteDual(ctx context.Context, req ChatCompletionRequest) (*DualResult, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	available := s.availableProviders()
	if len(available) == 0 {
		return nil, fmt.Errorf("no available provider")
	}

	p1 := s.PickProviderRoundRobin()
	if p1 == nil {
		return nil, fmt.Errorf("no available provider")
	}

	var p2 Provider
	for attempt := 0; attempt < len(available)*2 && p2 == nil; attempt++ {
		candidate := s.PickProviderRoundRobin()
		if candidate != nil && candidate.Name() != p1.Name() {
			p2 = candidate
		}
	}

	if p2 == nil {
		resp, err := s.ChatComplete(ctx, req)
		if err != nil {
			return nil, err
		}
		return &DualResult{Result1: resp, Consensus: true}, nil
	}

	if err := s.sem.acquireN(ctx, 2); err != nil {
		return nil, err
	}
	defer s.sem.releaseN(2)

	type outcome struct {
		resp *ChatCompletionResponse
		err  error
	}
	ch1 := make(chan outcome, 1)
	ch2 := make(chan outcome, 1)

	start := time.Now()
	go func() {
		pCtx, cancel := context.WithTimeout(ctx, s.timeout)
		defer cancel()
		// Deep copy request to avoid race with caller modifying Messages slice.
		reqCopy := req.Clone()
		if reqCopy.Model == "" {
			reqCopy.Model = p1.Config().Model
		}
		if s.promptInjector != nil {
			s.promptInjector.Inject(p1.Name(), reqCopy.Model, &reqCopy)
		}
		r, err := p1.ChatComplete(pCtx, reqCopy)
		ch1 <- outcome{r, err}
	}()
	go func() {
		pCtx, cancel := context.WithTimeout(ctx, s.timeout)
		defer cancel()
		reqCopy := req.Clone()
		if reqCopy.Model == "" {
			reqCopy.Model = p2.Config().Model
		}
		if s.promptInjector != nil {
			s.promptInjector.Inject(p2.Name(), reqCopy.Model, &reqCopy)
		}
		r, err := p2.ChatComplete(pCtx, reqCopy)
		ch2 <- outcome{r, err}
	}()

	o1 := <-ch1
	o2 := <-ch2

	// Record history for both.
	if s.history != nil {
		record1 := CallRecord{
			ID:        fmt.Sprintf("%s-%d", p1.Name(), start.UnixNano()),
			Provider:  p1.Name(),
			Model:     req.Model,
			Request:   req,
			Response:  o1.resp,
			LatencyMs: time.Since(start).Milliseconds(),
			Timestamp: start,
			Tags:      append(req.Tags, "dual"),
			Error:     errString(o1.err),
		}
		if o1.resp != nil {
			pricing := p1.Config().Pricing
			if pricing.Currency == "" && pricing.PromptPer1K == 0 && pricing.CompletionPer1K == 0 {
				pricing = DefaultPricing(p1.Name(), p1.Config().Model)
			}
			record1.Cost = o1.resp.Usage.Cost(pricing)
			record1.Currency = pricing.Currency
		}
		s.history.Record(record1)

		record2 := CallRecord{
			ID:        fmt.Sprintf("%s-%d", p2.Name(), start.UnixNano()),
			Provider:  p2.Name(),
			Model:     req.Model,
			Request:   req,
			Response:  o2.resp,
			LatencyMs: time.Since(start).Milliseconds(),
			Timestamp: start,
			Tags:      append(req.Tags, "dual"),
			Error:     errString(o2.err),
		}
		if o2.resp != nil {
			pricing := p2.Config().Pricing
			if pricing.Currency == "" && pricing.PromptPer1K == 0 && pricing.CompletionPer1K == 0 {
				pricing = DefaultPricing(p2.Name(), p2.Config().Model)
			}
			record2.Cost = o2.resp.Usage.Cost(pricing)
			record2.Currency = pricing.Currency
		}
		s.history.Record(record2)
	}

	dual := &DualResult{Result1: o1.resp, Result2: o2.resp}
	if dual.Result1 == nil && dual.Result2 == nil {
		return nil, fmt.Errorf("dual completion failed: %w; %w", o1.err, o2.err)
	}
	if dual.Result1 != nil && dual.Result2 != nil &&
		len(dual.Result1.Choices) > 0 && len(dual.Result2.Choices) > 0 {
		dual.Consensus = Consensus(dual.Result1, dual.Result2)
	}
	return dual, nil
}

// availableProviders returns the currently available providers.
// It uses a short-lived cache to avoid repeated scans under high load.
func (s *Scheduler) availableProviders() []Provider {
	// Try cache first.
	if cached := s.availCache.Load(); cached != nil {
		if time.Since(cached.time) < s.availCacheTTL {
			return cached.providers
		}
	}

	s.mu.RLock()
	providers := s.providers
	s.mu.RUnlock()

	var available []Provider
	for _, p := range providers {
		if p.IsAvailable() {
			available = append(available, p)
		}
	}

	// Update cache.
	s.availCache.Store(&availCacheEntry{
		providers: available,
		time:      time.Now(),
	})
	return available
}

// pickPrimaryProvider returns the first available provider (user's designated primary).
func (s *Scheduler) PickPrimaryProvider() Provider {
	available := s.availableProviders()
	if len(available) == 0 {
		return nil
	}
	return available[0]
}

// pickProviderRoundRobin selects the next available provider using round-robin.
func (s *Scheduler) PickProviderRoundRobin() Provider {
	available := s.availableProviders()
	if len(available) == 0 {
		return nil
	}
	newVal := atomic.AddUint64(&s.rrIndex, 1)
	idx := (newVal - 1) % uint64(len(available))
	return available[idx]
}

// ProviderStatus returns the runtime status of each configured provider.
func (s *Scheduler) ProviderStatus() []ProviderStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := make([]ProviderStatus, 0, len(s.providers))
	for i, p := range s.providers {
		model := ""
		if i < len(s.providerCfgs) {
			model = s.providerCfgs[i].Model
		}
		status = append(status, ProviderStatus{
			Name:      p.Name(),
			Available: p.IsAvailable(),
			Model:     model,
		})
	}
	return status
}

// TestProvider tests connectivity to a specific provider by name.
func (s *Scheduler) TestProvider(ctx context.Context, providerName string) error {
	s.mu.RLock()
	providers := s.providers
	s.mu.RUnlock()

	for _, p := range providers {
		if p.Name() == providerName {
			if !p.IsAvailable() {
				return fmt.Errorf("provider %s config incomplete", providerName)
			}
			testCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			_, err := p.ChatComplete(testCtx, ChatCompletionRequest{
				Messages: []Message{
					{Role: "user", Content: "Hi"},
				},
			})
			cancel()
			if err != nil {
				return fmt.Errorf("provider %s test failed: %w", providerName, err)
			}
			return nil
		}
	}
	return fmt.Errorf("provider not found: %s", providerName)
}

// ListModels returns the list of available models for a specific provider.
func (s *Scheduler) ListModels(ctx context.Context, providerName string) ([]ModelInfo, error) {
	s.mu.RLock()
	providers := s.providers
	s.mu.RUnlock()

	for _, p := range providers {
		if p.Name() == providerName {
			return p.ListModels(ctx)
		}
	}
	return nil, fmt.Errorf("provider not found: %s", providerName)
}

// ListModelsWithConfig queries model list using a temporary config,
// without requiring the provider to be saved in the scheduler.
func (s *Scheduler) ListModelsWithConfig(ctx context.Context, cfg ProviderConfig) ([]ModelInfo, error) {
	p := CreateProvider(cfg)
	if p == nil {
		return nil, fmt.Errorf("cannot create provider: %s", cfg.Name)
	}
	return p.ListModels(ctx)
}

// Acquire reserves a slot in the concurrency semaphore.
func (s *Scheduler) Acquire() error { return s.sem.acquire(context.Background()) }

// Release frees a slot in the concurrency semaphore.
func (s *Scheduler) Release() { s.sem.release() }

// SetSystemPrompt sets the system prompt override on all providers.
func (s *Scheduler) SetSystemPrompt(sp string) {
	s.mu.RLock()
	providers := s.providers
	s.mu.RUnlock()

	for _, p := range providers {
		if bp, ok := p.(interface{ SetSystemPrompt(string) }); ok {
			bp.SetSystemPrompt(sp)
		}
	}
}

// ClearSystemPrompt removes the system prompt override from all providers.
func (s *Scheduler) ClearSystemPrompt() {
	s.mu.RLock()
	providers := s.providers
	s.mu.RUnlock()

	for _, p := range providers {
		if bp, ok := p.(interface{ ClearSystemPrompt() }); ok {
			bp.ClearSystemPrompt()
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
