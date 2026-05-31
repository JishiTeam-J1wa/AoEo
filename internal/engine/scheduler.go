package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/providers"
)

// SchedulerOption configures a Scheduler.
type SchedulerOption func(*Scheduler)

// WithTimeout sets the per-provider request timeout.
func WithTimeout(d time.Duration) SchedulerOption {
	return func(s *Scheduler) {
		s.timeout.Store(int64(d))
	}
}

// WithHistory attaches a History recorder to the scheduler.
func WithHistory(h *History) SchedulerOption {
	return func(s *Scheduler) {
		s.history = h
	}
}

// WithRetry sets the retry configuration for the scheduler.
func WithRetry(cfg core.RetryConfig) SchedulerOption {
	return func(s *Scheduler) {
		s.retry = cfg
	}
}

// WithInterceptors attaches interceptors to the scheduler.
func WithInterceptors(ic ...core.Interceptor) SchedulerOption {
	return func(s *Scheduler) {
		cpy := make([]core.Interceptor, len(ic))
		copy(cpy, ic)
		s.interceptors.Store(&cpy)
	}
}

// Sentinel errors for SDK consumers to use with errors.Is().
var (
	ErrSchedulerClosed          = errors.New("scheduler is closed")
	ErrNoAvailableProvider      = errors.New("no available provider")
	ErrProviderNotFound         = errors.New("provider not found")
	ErrAllProvidersFailed       = errors.New("all providers failed")
	ErrProviderConfigIncomplete = errors.New("provider config incomplete")
)

type availCacheEntry struct {
	providers []providers.Provider
	time      time.Time
}

// Scheduler manages multiple AI providers with load balancing, circuit breaking,
// and concurrency control. It is the core of AoEo's multi-provider aggregation.
type Scheduler struct {
	mu           sync.RWMutex
	providers    []providers.Provider
	providerCfgs []core.ProviderConfig
	sem          *adaptiveSemaphore

	// Round-robin index for fallback/load balancing.
	rrIndex uint64

	// Configurable timeout (default 45s).
	timeout atomic.Int64

	// Optional history recorder.
	history *History

	// Optional retry configuration.
	retry core.RetryConfig

	// Optional prompt injector.
	promptInjector atomic.Pointer[PromptInjector]

	// Optional interceptors.
	interceptors atomic.Pointer[[]core.Interceptor]

	// Cached available providers (refreshed on access if stale).
	availCache    atomic.Pointer[availCacheEntry]
	availCacheTTL time.Duration

	// Graceful shutdown tracking.
	closed  atomic.Bool
	closeMu sync.Mutex

	// Unique request ID counter.
	reqID atomic.Uint64
}

// NewScheduler creates a new scheduler with the given providers.
// If no providers are given, call ApplyConfig later.
func NewScheduler(provs ...providers.Provider) *Scheduler {
	var validProvs []providers.Provider
	totalSlots := 0
	for _, p := range provs {
		if p == nil {
			continue
		}
		if cfg := p.Config(); cfg.Name != "" {
			slots := cfg.MaxConcurrent
			if slots <= 0 {
				slots = 2
			}
			totalSlots += slots
		}
		validProvs = append(validProvs, p)
	}
	if totalSlots == 0 {
		totalSlots = 4
	}

	s := &Scheduler{
		providers:     validProvs,
		sem:           NewAdaptiveSemaphore(totalSlots),
		availCacheTTL: 1 * time.Second,
	}
	s.timeout.Store(int64(45 * time.Second))
	return s
}

// NewSchedulerWithOptions creates a scheduler with options.
func NewSchedulerWithOptions(providers []providers.Provider, opts ...SchedulerOption) *Scheduler {
	s := NewScheduler(providers...)
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ApplyConfig applies the given configuration, creating provider instances.
func (s *Scheduler) ApplyConfig(cfg core.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var providers []providers.Provider
	var cfgs []core.ProviderConfig
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
func CreateProvider(cfg core.ProviderConfig) providers.Provider {
	switch cfg.Name {
	case "deepseek":
		return providers.NewDeepSeekProvider(cfg)
	case "glm":
		return providers.NewGLMProvider(cfg)
	case "qwen":
		return providers.NewQwenProvider(cfg)
	case "kimi":
		return providers.NewKimiProvider(cfg)
	default:
		return providers.NewOpenAIProvider(cfg)
	}
}

func (s *Scheduler) checkClosed() error {
	if s.closed.Load() {
		return ErrSchedulerClosed
	}
	return nil
}

// Close marks the scheduler as closed and attempts to close all providers
// that implement io.Closer. It is safe to call multiple times (idempotent).
// If any provider close fails, the first error is returned.
func (s *Scheduler) Close() error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()

	if s.closed.Swap(true) {
		return nil
	}

	s.mu.RLock()
	allProvs := s.providers
	s.mu.RUnlock()

	var firstErr error
	for _, p := range allProvs {
		if c, ok := p.(interface{ Close() error }); ok {
			if err := c.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// ChatComplete performs a chat completion using the primary (first available) provider.
func (s *Scheduler) ChatComplete(ctx context.Context, req core.ChatCompletionRequest) (resp *core.ChatCompletionResponse, err error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	if err := s.sem.Acquire(ctx); err != nil {
		return nil, err
	}
	defer s.sem.Release()

	p := s.PickPrimaryProvider()
	if p == nil {
		return nil, ErrNoAvailableProvider
	}

	// Fill default model from provider config if not specified.
	reqCopy := req
	if reqCopy.Model == "" {
		reqCopy.Model = p.Config().Model
	}

	// Apply prompt injection if configured.
	// Clone to avoid mutating the caller's original Messages slice.
	if pi := s.promptInjector.Load(); pi != nil {
		reqCopy = req.Clone()
		if reqCopy.Model == "" {
			reqCopy.Model = p.Config().Model
		}
		pi.Inject(p.Name(), reqCopy.Model, &reqCopy)
	}

	// Apply interceptor BeforeRequest hooks.
	chain := s.interceptorChain()
	if err := chain.ApplyBefore(ctx, &reqCopy); err != nil {
		return nil, err
	}

	providerCtx, cancel := context.WithTimeout(ctx, time.Duration(s.timeout.Load()))
	defer cancel()

	start := time.Now()

	defer func() {
		if r := recover(); r != nil {
			core.GetLogger().Error("scheduler ChatComplete panic recovered", "panic", r)
			err = fmt.Errorf("provider panic: %v", r)
		}
		if s.history != nil {
			s.history.Record(s.buildRecord(p, reqCopy, resp, start, err, req.Tags, ""))
		}
		resp, err = chain.ApplyAfter(ctx, reqCopy, resp, err)
	}()

	if s.retry.MaxRetries > 0 {
		err = DoRetry(providerCtx, s.retry, func() error {
			var innerErr error
			resp, innerErr = p.ChatComplete(providerCtx, reqCopy)
			return innerErr
		})
	} else {
		resp, err = p.ChatComplete(providerCtx, reqCopy)
	}

	return resp, err
}

// ChatCompleteWithFallback tries the primary provider first; on failure,
// it falls back to the next available provider automatically.
func (s *Scheduler) ChatCompleteWithFallback(ctx context.Context, req core.ChatCompletionRequest) (resp *core.ChatCompletionResponse, err error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	available := s.AvailableProviders()
	if len(available) == 0 {
		return nil, ErrNoAvailableProvider
	}

	// Apply interceptor BeforeRequest hooks once before the fallback loop.
	chain := s.interceptorChain()
	if err := chain.ApplyBefore(ctx, &req); err != nil {
		return nil, err
	}
	defer func() {
		resp, err = chain.ApplyAfter(ctx, req, resp, err)
	}()

	var lastErr error
	var fallbackFrom string
	for i, p := range available {
		if err := s.sem.Acquire(ctx); err != nil {
			return nil, err
		}

		reqCopy := req
		if reqCopy.Model == "" {
			reqCopy.Model = p.Config().Model
		}
		if pi := s.promptInjector.Load(); pi != nil {
			reqCopy = req.Clone()
			if reqCopy.Model == "" {
				reqCopy.Model = p.Config().Model
			}
			pi.Inject(p.Name(), reqCopy.Model, &reqCopy)
		}

		providerCtx, cancel := context.WithTimeout(ctx, time.Duration(s.timeout.Load()))
		start := time.Now()

		var resp *core.ChatCompletionResponse
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					core.GetLogger().Error("scheduler ChatCompleteWithFallback panic recovered", "provider", p.Name(), "panic", r)
					err = fmt.Errorf("provider panic: %v", r)
				}
				cancel()
				s.sem.Release()
			}()

			if s.retry.MaxRetries > 0 {
				err = DoRetry(providerCtx, s.retry, func() error {
					var innerErr error
					resp, innerErr = p.ChatComplete(providerCtx, reqCopy)
					return innerErr
				})
			} else {
				resp, err = p.ChatComplete(providerCtx, reqCopy)
			}
		}()

		if s.history != nil {
			s.history.Record(s.buildRecord(p, reqCopy, resp, start, err, req.Tags, fallbackFrom))
		}

		if err == nil {
			return resp, nil
		}
		lastErr = err
		if i == 0 {
			fallbackFrom = p.Name()
		}
	}
	return nil, fmt.Errorf("%w, last error: %w", ErrAllProvidersFailed, lastErr)
}

// ChatCompleteDual sends the request to two different providers concurrently
// and returns both results for comparison/merging.
func (s *Scheduler) ChatCompleteDual(ctx context.Context, req core.ChatCompletionRequest) (*core.DualResult, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	available := s.AvailableProviders()
	if len(available) == 0 {
		return nil, ErrNoAvailableProvider
	}

	// Apply interceptor BeforeRequest hooks once before the dual call.
	chain := s.interceptorChain()
	if err := chain.ApplyBefore(ctx, &req); err != nil {
		return nil, err
	}

	p1 := s.PickProviderRoundRobin()
	if p1 == nil {
		return nil, ErrNoAvailableProvider
	}

	var p2 providers.Provider
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
		return &core.DualResult{Result1: resp, Consensus: true}, nil
	}

	if err := s.sem.AcquireN(ctx, 2); err != nil {
		return nil, err
	}
	defer s.sem.ReleaseN(2)

	type outcome struct {
		resp *core.ChatCompletionResponse
		err  error
	}
	ch1 := make(chan outcome, 1)
	ch2 := make(chan outcome, 1)

	start := time.Now()
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				core.GetLogger().Error("dual provider panic recovered", "provider", p1.Name(), "panic", rec)
				ch1 <- outcome{nil, fmt.Errorf("provider panic: %v", rec)}
			}
		}()
		pCtx, cancel := context.WithTimeout(ctx, time.Duration(s.timeout.Load()))
		defer cancel()
		// Deep copy request to avoid race with caller modifying Messages slice.
		reqCopy := req.Clone()
		if reqCopy.Model == "" {
			reqCopy.Model = p1.Config().Model
		}
		if pi := s.promptInjector.Load(); pi != nil {
			pi.Inject(p1.Name(), reqCopy.Model, &reqCopy)
		}
		r, err := p1.ChatComplete(pCtx, reqCopy)
		ch1 <- outcome{r, err}
	}()
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				core.GetLogger().Error("dual provider panic recovered", "provider", p2.Name(), "panic", rec)
				ch2 <- outcome{nil, fmt.Errorf("provider panic: %v", rec)}
			}
		}()
		pCtx, cancel := context.WithTimeout(ctx, time.Duration(s.timeout.Load()))
		defer cancel()
		reqCopy := req.Clone()
		if reqCopy.Model == "" {
			reqCopy.Model = p2.Config().Model
		}
		if pi := s.promptInjector.Load(); pi != nil {
			pi.Inject(p2.Name(), reqCopy.Model, &reqCopy)
		}
		r, err := p2.ChatComplete(pCtx, reqCopy)
		ch2 <- outcome{r, err}
	}()

	o1 := <-ch1
	o2 := <-ch2

	// Record history for both.
	if s.history != nil {
		req1 := req.Clone()
		if req1.Model == "" {
			req1.Model = p1.Config().Model
		}
		if pi := s.promptInjector.Load(); pi != nil {
			pi.Inject(p1.Name(), req1.Model, &req1)
		}
		s.history.Record(s.buildRecord(p1, req1, o1.resp, start, o1.err, append(req.Tags, "dual"), ""))

		req2 := req.Clone()
		if req2.Model == "" {
			req2.Model = p2.Config().Model
		}
		if pi := s.promptInjector.Load(); pi != nil {
			pi.Inject(p2.Name(), req2.Model, &req2)
		}
		s.history.Record(s.buildRecord(p2, req2, o2.resp, start, o2.err, append(req.Tags, "dual"), ""))
	}

	dual := &core.DualResult{Result1: o1.resp, Result2: o2.resp}
	if dual.Result1 == nil && dual.Result2 == nil {
		return nil, fmt.Errorf("%w: %v; %v", ErrAllProvidersFailed, o1.err, o2.err)
	}
	if dual.Result1 != nil && dual.Result2 != nil &&
		len(dual.Result1.Choices) > 0 && len(dual.Result2.Choices) > 0 {
		dual.Consensus = Consensus(dual.Result1, dual.Result2)
	}
	return dual, nil
}

func copyProviders(src []providers.Provider) []providers.Provider {
	dst := make([]providers.Provider, len(src))
	copy(dst, src)
	return dst
}

// AvailableProviders returns the currently available providers.
// It uses a short-lived cache to avoid repeated scans under high load.
// The returned slice is a copy; modifying it does not affect internal state.
func (s *Scheduler) AvailableProviders() []providers.Provider {
	// Try cache first.
	if cached := s.availCache.Load(); cached != nil {
		if time.Since(cached.time) < s.availCacheTTL {
			return copyProviders(cached.providers)
		}
	}

	s.mu.RLock()
	allProvs := s.providers
	s.mu.RUnlock()

	var available []providers.Provider
	for _, p := range allProvs {
		if p.IsAvailable() {
			available = append(available, p)
		}
	}

	// Update cache.
	s.availCache.Store(&availCacheEntry{
		providers: available,
		time:      time.Now(),
	})
	return copyProviders(available)
}

// pickPrimaryProvider returns the first available provider (user's designated primary).
func (s *Scheduler) PickPrimaryProvider() providers.Provider {
	available := s.AvailableProviders()
	if len(available) == 0 {
		return nil
	}
	return available[0]
}

// pickProviderRoundRobin selects the next available provider using round-robin.
func (s *Scheduler) PickProviderRoundRobin() providers.Provider {
	available := s.AvailableProviders()
	if len(available) == 0 {
		return nil
	}
	newVal := atomic.AddUint64(&s.rrIndex, 1)
	idx := (newVal - 1) % uint64(len(available))
	return available[idx]
}

// core.ProviderStatus returns the runtime status of each configured provider.
func (s *Scheduler) ProviderStatus() []core.ProviderStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := make([]core.ProviderStatus, 0, len(s.providers))
	for i, p := range s.providers {
		model := ""
		if i < len(s.providerCfgs) {
			model = s.providerCfgs[i].Model
		} else {
			model = p.Config().Model
		}
		status = append(status, core.ProviderStatus{
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
	allProvs := s.providers
	s.mu.RUnlock()

	for _, p := range allProvs {
		if p.Name() == providerName {
			if !p.IsAvailable() {
				return fmt.Errorf("provider %s config incomplete", providerName)
			}
			testCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()

			_, err := func() (result *core.ChatCompletionResponse, err error) {
				defer func() {
					if r := recover(); r != nil {
						core.GetLogger().Error("TestProvider panic recovered", "provider", providerName, "panic", r)
						err = fmt.Errorf("provider panic: %v", r)
					}
				}()
				result, err = p.ChatComplete(testCtx, core.ChatCompletionRequest{
					Messages: []core.Message{
						{Role: "user", Content: "Hi"},
					},
				})
				return
			}()

			if err != nil {
				return fmt.Errorf("provider %s test failed: %w", providerName, err)
			}
			return nil
		}
	}
	return fmt.Errorf("provider not found: %s", providerName)
}

// ListModels returns the list of available models for a specific provider.
func (s *Scheduler) ListModels(ctx context.Context, providerName string) ([]core.ModelInfo, error) {
	s.mu.RLock()
	allProvs := s.providers
	s.mu.RUnlock()

	for _, p := range allProvs {
		if p.Name() == providerName {
			return p.ListModels(ctx)
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, providerName)
}

// ListModelsWithConfig queries model list using a temporary config,
// without requiring the provider to be saved in the scheduler.
func (s *Scheduler) ListModelsWithConfig(ctx context.Context, cfg core.ProviderConfig) ([]core.ModelInfo, error) {
	p := CreateProvider(cfg)
	if p == nil {
		return nil, fmt.Errorf("%w: %s", ErrProviderConfigIncomplete, cfg.Name)
	}
	return p.ListModels(ctx)
}

// Acquire reserves a slot in the concurrency semaphore.
func (s *Scheduler) Acquire() error { return s.sem.Acquire(context.Background()) }

// Release frees a slot in the concurrency semaphore.
func (s *Scheduler) Release() { s.sem.Release() }

// SetSystemPrompt sets the system prompt override on all providers.
func (s *Scheduler) SetSystemPrompt(sp string) {
	s.mu.RLock()
	allProvs := s.providers
	s.mu.RUnlock()

	for _, p := range allProvs {
		if bp, ok := p.(interface{ SetSystemPrompt(string) }); ok {
			bp.SetSystemPrompt(sp)
		}
	}
}

// ClearSystemPrompt removes the system prompt override from all providers.
func (s *Scheduler) ClearSystemPrompt() {
	s.mu.RLock()
	allProvs := s.providers
	s.mu.RUnlock()

	for _, p := range allProvs {
		if bp, ok := p.(interface{ ClearSystemPrompt() }); ok {
			bp.ClearSystemPrompt()
		}
	}
}

// buildRecord creates a CallRecord with pricing/cost calculation.
func (s *Scheduler) buildRecord(p providers.Provider, req core.ChatCompletionRequest, resp *core.ChatCompletionResponse, start time.Time, err error, tags []string, fallbackFrom string) CallRecord {
	record := CallRecord{
		ID:           fmt.Sprintf("%s-%d-%d", p.Name(), start.UnixNano(), s.reqID.Add(1)),
		Provider:     p.Name(),
		Model:        req.Model,
		Request:      req,
		Response:     resp,
		LatencyMs:    time.Since(start).Milliseconds(),
		Timestamp:    start,
		Tags:         tags,
		FallbackFrom: fallbackFrom,
	}
	if err != nil {
		record.Error = err.Error()
	}
	if resp != nil {
		pricing := p.Config().Pricing
		if pricing.Currency == "" && pricing.PromptPer1K == 0 && pricing.CompletionPer1K == 0 {
			pricing = core.DefaultPricing(p.Name(), req.Model)
		}
		record.Cost = resp.Usage.Cost(pricing)
		record.Currency = pricing.Currency
	}
	return record
}

// SetTimeout updates the per-provider request timeout at runtime.
func (s *Scheduler) SetTimeout(d time.Duration) {
	if d > 0 {
		s.timeout.Store(int64(d))
	}
}

// IsClosed reports whether the scheduler has been closed.
func (s *Scheduler) IsClosed() bool {
	return s.closed.Load()
}

// History returns the attached history recorder (may be nil).
func (s *Scheduler) History() *History {
	return s.history
}

// PromptInjector returns the attached prompt injector (may be nil).
func (s *Scheduler) PromptInjector() *PromptInjector {
	return s.promptInjector.Load()
}

// SetPromptInjector attaches a prompt injector to the scheduler.
func (s *Scheduler) SetPromptInjector(pi *PromptInjector) {
	s.promptInjector.Store(pi)
}

// Interceptors returns the current interceptor slice (may be nil).
func (s *Scheduler) Interceptors() []core.Interceptor {
	if ptr := s.interceptors.Load(); ptr != nil {
		return *ptr
	}
	return nil
}

// SetInterceptors replaces the interceptor chain.
func (s *Scheduler) SetInterceptors(ic []core.Interceptor) {
	cpy := make([]core.Interceptor, len(ic))
	copy(cpy, ic)
	s.interceptors.Store(&cpy)
}

func (s *Scheduler) interceptorChain() core.InterceptorChain {
	if ptr := s.interceptors.Load(); ptr != nil {
		return core.InterceptorChain(*ptr)
	}
	return nil
}
