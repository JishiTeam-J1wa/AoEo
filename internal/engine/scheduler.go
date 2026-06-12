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

// WithRouter sets the provider selection router.
func WithRouter(r core.Router) SchedulerOption {
	return func(s *Scheduler) {
		s.router.Store(&r)
	}
}

// WithHealthCheckInterval sets the background health check interval.
// Pass 0 to disable health checks.
func WithHealthCheckInterval(d time.Duration) SchedulerOption {
	return func(s *Scheduler) {
		s.healthCheckInterval.Store(int64(d))
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

	// Optional router for provider selection strategy.
	router atomic.Pointer[core.Router]

	// Cached available providers (refreshed on access if stale).
	availCache    atomic.Pointer[availCacheEntry]
	availCacheTTL time.Duration

	// Graceful shutdown tracking.
	closed  atomic.Bool
	closeMu sync.Mutex

	// Unique request ID counter.
	reqID atomic.Uint64

	// Background health check.
	healthCheckInterval atomic.Int64 // nanoseconds, 0 = disabled
	healthCheckMu       sync.Mutex
	healthCheckStop     chan struct{}
	healthCheckWG       sync.WaitGroup
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

// Close marks the scheduler as closed, stops background health checks,
// and attempts to close all providers that implement io.Closer.
// It is safe to call multiple times (idempotent).
// If any provider close fails, the first error is returned.
func (s *Scheduler) Close() error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()

	if s.closed.Swap(true) {
		return nil
	}

	s.stopHealthCheck()

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

// StartHealthCheck starts a background goroutine that periodically health-checks
// all providers. If a health check is already running, it is stopped and restarted
// with the new interval. Pass 0 to disable.
func (s *Scheduler) StartHealthCheck(interval time.Duration) {
	s.healthCheckMu.Lock()
	defer s.healthCheckMu.Unlock()

	// Stop existing loop if any.
	s.stopHealthCheckLocked()

	if interval <= 0 {
		s.healthCheckInterval.Store(0)
		return
	}
	s.healthCheckInterval.Store(int64(interval))
	stopCh := make(chan struct{})
	s.healthCheckStop = stopCh
	s.healthCheckWG.Add(1)
	go s.healthCheckLoop(interval, stopCh)
}

func (s *Scheduler) stopHealthCheck() {
	s.healthCheckMu.Lock()
	defer s.healthCheckMu.Unlock()
	s.stopHealthCheckLocked()
}

func (s *Scheduler) stopHealthCheckLocked() {
	if s.healthCheckStop != nil {
		close(s.healthCheckStop)
		s.healthCheckStop = nil
	}
	s.healthCheckWG.Wait()
}

func (s *Scheduler) healthCheckLoop(interval time.Duration, stop <-chan struct{}) {
	defer s.healthCheckWG.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.runHealthChecks()
		case <-stop:
			return
		}
	}
}

// circuitBreaker is the subset of BaseProvider methods needed by the scheduler.
type circuitBreaker interface {
	RecordFailure()
	RecordSuccess()
}

// healthReporter is the subset of BaseProvider methods for reading runtime health.
type healthReporter interface {
	Health() core.ProviderHealth
}

func (s *Scheduler) runHealthChecks() {
	s.mu.RLock()
	provs := make([]providers.Provider, len(s.providers))
	copy(provs, s.providers)
	s.mu.RUnlock()

	for _, p := range provs {
		if s.closed.Load() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := p.HealthCheck(ctx); err != nil {
			core.GetLogger().Debug("health check failed", "provider", p.Name(), "error", err)
			if cb, ok := p.(circuitBreaker); ok {
				cb.RecordFailure()
			}
		} else {
			if cb, ok := p.(circuitBreaker); ok {
				cb.RecordSuccess()
			}
		}
		cancel()
	}
}

// ChatComplete 使用主（首个可用的）Provider 执行一次聊天补全请求。
// 执行流程：
//  1. 检查调度器是否已关闭
//  2. 通过信号量获取并发槽位（限制同时请求数）
//  3. 通过路由器选择最合适的 Provider
//  4. 深拷贝请求（避免修改调用方的 Messages 切片等）
//  5. 应用 Prompt 注入（如已配置）
//  6. 执行拦截器的 BeforeRequest 钩子
//  7. 调用 Provider 的 ChatComplete（支持自动重试）
//  8. 执行拦截器的 AfterResponse 钩子并记录历史
//
// 修复 SCHED-01：始终使用 req.Clone() 进行深拷贝，避免浅拷贝导致
// Messages 等切片字段与原始请求共享底层数组，进而引发数据竞争。
func (s *Scheduler) ChatComplete(ctx context.Context, req core.ChatCompletionRequest) (resp *core.ChatCompletionResponse, err error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	if err := s.sem.Acquire(ctx); err != nil {
		return nil, err
	}
	defer s.sem.Release()

	p, err := s.pickWithRouter(ctx, req)
	if err != nil {
		return nil, err
	}

	// 始终使用深拷贝，避免修改调用方的原始请求（特别是 Messages 切片的底层数组）
	reqCopy := req.Clone()
	if reqCopy.Model == "" {
		reqCopy.Model = p.Config().Model
	}

	// 应用 Prompt 注入（如果已配置）
	if pi := s.promptInjector.Load(); pi != nil {
		pi.Inject(p.Name(), reqCopy.Model, &reqCopy)
	}

	// 执行拦截器的 BeforeRequest 钩子
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

// ChatCompleteWithFallback 尝试使用主 Provider 执行请求；如果失败，
// 自动回退到下一个可用的 Provider，直到成功或所有 Provider 都失败。
// 执行流程：
//  1. 检查调度器是否已关闭，获取可用 Provider 列表
//  2. 执行拦截器的 BeforeRequest 钩子（仅一次，在回退循环之前）
//  3. 通过路由器确定回退顺序（优先使用路由器的排序结果）
//  4. 遍历 Provider 列表，依次尝试调用：
//     - 获取信号量槽位
//     - 深拷贝请求并填充默认模型
//     - 应用 Prompt 注入
//     - 调用 Provider 的 ChatComplete（支持自动重试）
//     - 记录历史
//  5. 如果某个 Provider 成功则立即返回，否则继续下一个
//
// 修复 SCHED-02：始终使用 req.Clone() 进行深拷贝，与 SCHED-01 修复一致。
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

	// Determine fallback order via router if available.
	var order []providers.Provider
	if r := s.router.Load(); r != nil {
		status := s.ProviderStatus()
		if seq, rerr := (*r).SelectSequence(ctx, status, req); rerr == nil && len(seq) > 0 {
			s.mu.RLock()
			for _, idx := range seq {
				if idx >= 0 && idx < len(s.providers) {
					order = append(order, s.providers[idx])
				}
			}
			s.mu.RUnlock()
		}
	}
	if len(order) == 0 {
		order = available
	}

	var lastErr error
	var fallbackFrom string
	for i, p := range order {
		if err := s.sem.Acquire(ctx); err != nil {
			return nil, err
		}

		// 始终使用深拷贝，避免修改调用方的原始请求（修复 SCHED-02）
		reqCopy := req.Clone()
		if reqCopy.Model == "" {
			reqCopy.Model = p.Config().Model
		}
		// 应用 Prompt 注入（如果已配置）
		if pi := s.promptInjector.Load(); pi != nil {
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

// ChatCompleteDual 将请求同时发送给两个不同的 Provider 并发执行，
// 并返回两个结果用于对比或合并分析。
// 适用场景：模型对比评测、结果一致性校验、冗余容灾等。
// 如果只有一个可用 Provider，则降级为普通的 ChatComplete 调用。
// 该操作会占用 2 个并发信号量槽位。
func (s *Scheduler) ChatCompleteDual(ctx context.Context, req core.ChatCompletionRequest) (*core.DualResult, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	available := s.AvailableProviders()
	if len(available) == 0 {
		return nil, ErrNoAvailableProvider
	}

	// 在双路调用之前，仅执行一次拦截器的 BeforeRequest 钩子
	chain := s.interceptorChain()
	if err := chain.ApplyBefore(ctx, &req); err != nil {
		return nil, err
	}

	// 优先通过路由器选择两个不同的 Provider
	var p1, p2 providers.Provider
	if r := s.router.Load(); r != nil {
		status := s.ProviderStatus()
		if seq, rerr := (*r).SelectSequence(ctx, status, req); rerr == nil && len(seq) >= 2 {
			s.mu.RLock()
			for _, idx := range seq {
				if idx < 0 || idx >= len(s.providers) {
					continue
				}
				candidate := s.providers[idx]
				if p1 == nil {
					p1 = candidate
				} else if candidate.Name() != p1.Name() {
					p2 = candidate
					break
				}
			}
			s.mu.RUnlock()
		}
	}

	// Fallback to round-robin if router didn't yield two distinct providers.
	if p1 == nil {
		p1 = s.PickProviderRoundRobin()
	}
	if p1 == nil {
		return nil, ErrNoAvailableProvider
	}
	if p2 == nil {
		for attempt := 0; attempt < len(available)*2 && p2 == nil; attempt++ {
			candidate := s.PickProviderRoundRobin()
			if candidate != nil && candidate.Name() != p1.Name() {
				p2 = candidate
			}
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

// ProviderByName returns the provider with the given name, or nil if not found.
func (s *Scheduler) ProviderByName(name string) providers.Provider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.providers {
		if p.Name() == name {
			return p
		}
	}
	return nil
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

// PickPrimaryProvider returns the first available provider (user's designated primary).
func (s *Scheduler) PickPrimaryProvider() providers.Provider {
	available := s.AvailableProviders()
	if len(available) == 0 {
		return nil
	}
	return available[0]
}

// PickProviderRoundRobin selects the next available provider using round-robin.
func (s *Scheduler) PickProviderRoundRobin() providers.Provider {
	available := s.AvailableProviders()
	if len(available) == 0 {
		return nil
	}
	newVal := atomic.AddUint64(&s.rrIndex, 1)
	idx := (newVal - 1) % uint64(len(available))
	return available[idx]
}

// pickWithRouter applies the configured router to select a provider.
// Falls back to primary selection if no router is set.
func (s *Scheduler) pickWithRouter(ctx context.Context, req core.ChatCompletionRequest) (providers.Provider, error) {
	status := s.ProviderStatus()
	var availableIdx []int
	for i, st := range status {
		if st.Available {
			availableIdx = append(availableIdx, i)
		}
	}
	if len(availableIdx) == 0 {
		return nil, ErrNoAvailableProvider
	}

	if r := s.router.Load(); r != nil {
		idx, err := (*r).Select(ctx, status, req)
		if err == nil && idx >= 0 && idx < len(status) && status[idx].Available {
			s.mu.RLock()
			p := s.providers[idx]
			s.mu.RUnlock()
			return p, nil
		}
	}

	// Fallback to primary (first available).
	s.mu.RLock()
	p := s.providers[availableIdx[0]]
	s.mu.RUnlock()
	return p, nil
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
		st := core.ProviderStatus{
			Name:      p.Name(),
			Available: p.IsAvailable(),
			Model:     model,
		}
		if hr, ok := p.(healthReporter); ok {
			st.Health = hr.Health()
		}
		status = append(status, st)
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

// Router returns the current router (may be nil).
func (s *Scheduler) Router() core.Router {
	if r := s.router.Load(); r != nil {
		return *r
	}
	return nil
}

// SetRouter replaces the provider selection router.
func (s *Scheduler) SetRouter(r core.Router) {
	if r == nil {
		s.router.Store(nil)
	} else {
		s.router.Store(&r)
	}
}

// HealthCheckInterval returns the current health check interval.
func (s *Scheduler) HealthCheckInterval() time.Duration {
	return time.Duration(s.healthCheckInterval.Load())
}

// SetHealthCheckInterval updates the health check interval and restarts
// the background health checker. Pass 0 to disable.
func (s *Scheduler) SetHealthCheckInterval(d time.Duration) {
	s.StartHealthCheck(d)
}
