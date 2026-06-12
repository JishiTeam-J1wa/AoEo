// Package engine 实现 AoEo 请求调度引擎，提供多 Provider 聚合调度、负载均衡、熔断与并发控制能力。
//
// Author: JishiTeam-J1wa
// Created: 2026-05
//
// Changelog:
//   2026-06-12 - 注释体系规范化

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

// SchedulerOption 调度器配置选项函数类型。
type SchedulerOption func(*Scheduler)

// WithTimeout 设置每个 Provider 的请求超时时间。
//
// Param:
//   - d: time.Duration - 超时时长
//
// Return:
//   - SchedulerOption: 调度器配置选项
func WithTimeout(d time.Duration) SchedulerOption {
	return func(s *Scheduler) {
		s.timeout.Store(int64(d))
	}
}

// WithHistory 挂载历史记录器到调度器。
//
// Param:
//   - h: *History - 历史记录器实例
//
// Return:
//   - SchedulerOption: 调度器配置选项
func WithHistory(h *History) SchedulerOption {
	return func(s *Scheduler) {
		s.history = h
	}
}

// WithRetry 设置调度器的重试配置。
//
// Param:
//   - cfg: core.RetryConfig - 重试策略配置
//
// Return:
//   - SchedulerOption: 调度器配置选项
func WithRetry(cfg core.RetryConfig) SchedulerOption {
	return func(s *Scheduler) {
		s.retry = cfg
	}
}

// WithInterceptors 挂载拦截器链到调度器（内部会创建副本，调用方后续修改不影响调度器）。
//
// Param:
//   - ic: ...core.Interceptor - 拦截器列表
//
// Return:
//   - SchedulerOption: 调度器配置选项
func WithInterceptors(ic ...core.Interceptor) SchedulerOption {
	return func(s *Scheduler) {
		cpy := make([]core.Interceptor, len(ic))
		copy(cpy, ic)
		s.interceptors.Store(&cpy)
	}
}

// WithRouter 设置 Provider 选择路由器。
//
// Param:
//   - r: core.Router - 路由器实例
//
// Return:
//   - SchedulerOption: 调度器配置选项
func WithRouter(r core.Router) SchedulerOption {
	return func(s *Scheduler) {
		s.router.Store(&r)
	}
}

// WithHealthCheckInterval 设置后台健康检查间隔。传入 0 禁用健康检查。
//
// Param:
//   - d: time.Duration - 检查间隔，传入 0 禁用
//
// Return:
//   - SchedulerOption: 调度器配置选项
func WithHealthCheckInterval(d time.Duration) SchedulerOption {
	return func(s *Scheduler) {
		s.healthCheckInterval.Store(int64(d))
	}
}

// 哨兵错误，供 SDK 使用者通过 errors.Is() 进行判断。
var (
	ErrSchedulerClosed          = errors.New("scheduler is closed")
	ErrNoAvailableProvider      = errors.New("no available provider")
	ErrProviderNotFound         = errors.New("provider not found")
	ErrAllProvidersFailed       = errors.New("all providers failed")
	ErrProviderConfigIncomplete = errors.New("provider config incomplete")
)

// availCacheEntry 可用 Provider 缓存条目，用于减少高负载下的重复扫描。
type availCacheEntry struct {
	providers []providers.Provider
	time      time.Time
}

// Scheduler 管理多个 AI Provider，提供负载均衡、熔断和并发控制能力。
// 它是 AoEo 多 Provider 聚合调度的核心组件。
type Scheduler struct {
	mu           sync.RWMutex
	providers    []providers.Provider
	providerCfgs []core.ProviderConfig
	sem          *adaptiveSemaphore

	// Round-Robin 索引，用于回退/负载均衡
	rrIndex uint64

	// 可配置的超时时间（默认 45 秒）
	timeout atomic.Int64

	// 可选的历史记录器
	history *History

	// 可选的重试配置
	retry core.RetryConfig

	// 可选的 Prompt 注入器
	promptInjector atomic.Pointer[PromptInjector]

	// 可选的拦截器链
	interceptors atomic.Pointer[[]core.Interceptor]

	// 可选的路由器，用于 Provider 选择策略
	router atomic.Pointer[core.Router]

	// 可用 Provider 缓存（过期后在下次访问时刷新）
	availCache    atomic.Pointer[availCacheEntry]
	availCacheTTL time.Duration

	// 优雅关闭状态跟踪
	closed  atomic.Bool
	closeMu sync.Mutex

	// 唯一请求 ID 计数器
	reqID atomic.Uint64

	// 后台健康检查
	healthCheckInterval atomic.Int64 // 纳秒，0 = 禁用
	healthCheckMu       sync.Mutex
	healthCheckStop     chan struct{}
	healthCheckWG       sync.WaitGroup
}

// NewScheduler 使用给定的 Provider 列表创建一个新的调度器。
// 如果未提供 Provider，可稍后调用 ApplyConfig 进行配置。
//
// Param:
//   - provs: ...providers.Provider - Provider 列表（nil 元素会被自动跳过）
//
// Return:
//   - *Scheduler: 新创建的调度器实例（默认超时 45 秒）
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

// NewSchedulerWithOptions 使用 Provider 列表和配置选项创建调度器。
//
// Param:
//   - providers: []providers.Provider - Provider 列表
//   - opts: ...SchedulerOption - 配置选项
//
// Return:
//   - *Scheduler: 新创建的调度器实例
func NewSchedulerWithOptions(providers []providers.Provider, opts ...SchedulerOption) *Scheduler {
	s := NewScheduler(providers...)
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ApplyConfig 应用配置，根据配置创建 Provider 实例并更新调度器状态。
// 已有配置会被完全替换。
//
// Param:
//   - cfg: core.Config - 全局配置（包含 Provider 列表）
//
// Return:
//   - error: 当前始终返回 nil
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
	s.availCache.Store(nil) // 配置变更后使缓存失效

	if totalSlots > 0 {
		s.sem.setMaxConc(totalSlots)
	} else {
		s.sem.setMaxConc(4)
	}

	return nil
}

// CreateProvider 根据配置中的名称创建对应的 Provider 实例。
//
// Param:
//   - cfg: core.ProviderConfig - Provider 配置
//
// Return:
//   - providers.Provider: 对应的 Provider 实例（未识别的名称默认使用 OpenAI 兼容协议）
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

// checkClosed 检查调度器是否已关闭，已关闭时返回 ErrSchedulerClosed。
func (s *Scheduler) checkClosed() error {
	if s.closed.Load() {
		return ErrSchedulerClosed
	}
	return nil
}

// Close 将调度器标记为已关闭，停止后台健康检查，
// 并尝试关闭所有实现了 io.Closer 的 Provider。
// 可安全多次调用（幂等）。如果某个 Provider 关闭失败，返回第一个错误。
//
// Return:
//   - error: 第一个 Provider 关闭错误，或 nil
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

// StartHealthCheck 启动后台协程定期健康检查所有 Provider。
// 如果已有健康检查在运行，会先停止再以新间隔重启。传入 0 禁用。
//
// Param:
//   - interval: time.Duration - 检查间隔，传入 0 禁用
func (s *Scheduler) StartHealthCheck(interval time.Duration) {
	s.healthCheckMu.Lock()
	defer s.healthCheckMu.Unlock()

	// 停止已有的健康检查协程
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

// circuitBreaker 熔断器接口，BaseProvider 方法的子集，用于记录成功/失败。
type circuitBreaker interface {
	RecordFailure()
	RecordSuccess()
}

// healthReporter 健康状态报告接口，BaseProvider 方法的子集，用于读取运行时健康信息。
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
//
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
//
// Param:
//   - ctx: context.Context - 请求上下文，控制超时与取消
//   - req: core.ChatCompletionRequest - 聊天补全请求
//
// Return:
//   - *core.ChatCompletionResponse: 成功时返回 Provider 的响应
//   - error: 调度器已关闭、无可用 Provider、信号量获取失败或 Provider 调用失败时返回错误
//
// Edge Cases:
//   - Provider panic 会被 recover 并转为 error 返回
//   - 配置了重试时自动重试可恢复的错误
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

	if pi := s.promptInjector.Load(); pi != nil {
		pi.Inject(p.Name(), reqCopy.Model, &reqCopy)
	}

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
//
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
//
// Param:
//   - ctx: context.Context - 请求上下文，控制超时与取消
//   - req: core.ChatCompletionRequest - 聊天补全请求
//
// Return:
//   - *core.ChatCompletionResponse: 首个成功 Provider 的响应
//   - error: 所有 Provider 均失败时返回包装了 ErrAllProvidersFailed 的错误
//
// Edge Cases:
//   - 无可用 Provider 时返回 ErrNoAvailableProvider
//   - 路由器可用时按其排序结果确定回退顺序，否则使用默认可用列表
func (s *Scheduler) ChatCompleteWithFallback(ctx context.Context, req core.ChatCompletionRequest) (resp *core.ChatCompletionResponse, err error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	available := s.AvailableProviders()
	if len(available) == 0 {
		return nil, ErrNoAvailableProvider
	}

	// 在回退循环之前仅执行一次拦截器的 BeforeRequest 钩子
	chain := s.interceptorChain()
	if err := chain.ApplyBefore(ctx, &req); err != nil {
		return nil, err
	}
	defer func() {
		resp, err = chain.ApplyAfter(ctx, req, resp, err)
	}()

	// 通过路由器确定回退顺序（如果可用）
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
//
// Param:
//   - ctx: context.Context - 请求上下文，控制超时与取消
//   - req: core.ChatCompletionRequest - 聊天补全请求
//
// Return:
//   - *core.DualResult: 双路结果，包含两个 Provider 的响应及一致性判断
//   - error: 调度器已关闭、无可用 Provider 或信号量获取失败时返回错误
//
// Edge Cases:
//   - 仅一个可用 Provider 时降级为单路 ChatComplete
//   - 两个 Provider 均失败时返回包装了 ErrAllProvidersFailed 的错误
//   - 路由器可用时优先选择两个不同的 Provider，否则回退到 Round-Robin
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

	// 路由器未选出两个不同 Provider 时，回退到 Round-Robin 策略
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
		// 深拷贝请求，避免并发修改 Messages 切片引发数据竞争
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

// ProviderByName 返回指定名称的 Provider，未找到时返回 nil。
//
// Param:
//   - name: string - Provider 名称
//
// Return:
//   - providers.Provider: 匹配的 Provider 或 nil
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

// AvailableProviders 返回当前可用的 Provider 列表。
// 使用短时缓存（TTL=1s）避免高负载下重复扫描。
// 返回的切片是副本，修改不影响内部状态。
//
// Return:
//   - []providers.Provider: 可用 Provider 列表的副本
func (s *Scheduler) AvailableProviders() []providers.Provider {
	// 优先检查缓存
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

	// 刷新缓存
	s.availCache.Store(&availCacheEntry{
		providers: available,
		time:      time.Now(),
	})
	return copyProviders(available)
}

// PickPrimaryProvider 返回第一个可用的 Provider（用户指定的主 Provider）。
//
// Return:
//   - providers.Provider: 首个可用 Provider，无可用时返回 nil
func (s *Scheduler) PickPrimaryProvider() providers.Provider {
	available := s.AvailableProviders()
	if len(available) == 0 {
		return nil
	}
	return available[0]
}

// PickProviderRoundRobin 使用 Round-Robin 轮询策略选择下一个可用的 Provider。
//
// Return:
//   - providers.Provider: 轮询选中的 Provider，无可用时返回 nil
func (s *Scheduler) PickProviderRoundRobin() providers.Provider {
	available := s.AvailableProviders()
	if len(available) == 0 {
		return nil
	}
	newVal := atomic.AddUint64(&s.rrIndex, 1)
	idx := (newVal - 1) % uint64(len(available))
	return available[idx]
}

// pickWithRouter 使用配置的路由器选择 Provider，未配置路由器时回退到首个可用 Provider。
//
// Param:
//   - ctx: context.Context - 请求上下文
//   - req: core.ChatCompletionRequest - 聊天补全请求（供路由器决策使用）
//
// Return:
//   - providers.Provider: 选中的 Provider
//   - error: 无可用 Provider 时返回 ErrNoAvailableProvider
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

	// 回退到主 Provider（首个可用的）
	s.mu.RLock()
	p := s.providers[availableIdx[0]]
	s.mu.RUnlock()
	return p, nil
}

// ProviderStatus 返回每个已配置 Provider 的运行时状态。
//
// Return:
//   - []core.ProviderStatus: 所有 Provider 的状态列表
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

// TestProvider 测试指定名称的 Provider 的连通性（发送简单请求验证）。
//
// Param:
//   - ctx: context.Context - 请求上下文
//   - providerName: string - 要测试的 Provider 名称
//
// Return:
//   - error: Provider 不存在、配置不完整或连通性测试失败时返回错误
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

// ListModels 返回指定 Provider 的可用模型列表。
//
// Param:
//   - ctx: context.Context - 请求上下文
//   - providerName: string - Provider 名称
//
// Return:
//   - []core.ModelInfo: 可用模型列表
//   - error: Provider 未找到时返回包装了 ErrProviderNotFound 的错误
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

// ListModelsWithConfig 使用临时配置查询模型列表，无需 Provider 已保存在调度器中。
//
// Param:
//   - ctx: context.Context - 请求上下文
//   - cfg: core.ProviderConfig - 临时 Provider 配置
//
// Return:
//   - []core.ModelInfo: 可用模型列表
//   - error: 配置不完整时返回包装了 ErrProviderConfigIncomplete 的错误
func (s *Scheduler) ListModelsWithConfig(ctx context.Context, cfg core.ProviderConfig) ([]core.ModelInfo, error) {
	p := CreateProvider(cfg)
	if p == nil {
		return nil, fmt.Errorf("%w: %s", ErrProviderConfigIncomplete, cfg.Name)
	}
	return p.ListModels(ctx)
}

// Acquire 预留一个并发信号量槽位。
//
// Return:
//   - error: 信号量满或上下文取消时返回错误
func (s *Scheduler) Acquire() error { return s.sem.Acquire(context.Background()) }

// Release 释放一个并发信号量槽位。
func (s *Scheduler) Release() { s.sem.Release() }

// SetSystemPrompt 在所有 Provider 上设置系统 Prompt 覆盖值。
//
// Param:
//   - sp: string - 系统 Prompt 内容
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

// ClearSystemPrompt 移除所有 Provider 上的系统 Prompt 覆盖。
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

// buildRecord 构建一条包含定价/费用计算的调用记录。
//
// Param:
//   - p: providers.Provider - 执行调用的 Provider
//   - req: core.ChatCompletionRequest - 请求内容
//   - resp: *core.ChatCompletionResponse - 响应内容（可能为 nil）
//   - start: time.Time - 调用开始时间
//   - err: error - 调用错误（可能为 nil）
//   - tags: []string - 记录标签
//   - fallbackFrom: string - 回退来源 Provider 名称（非回退场景为空字符串）
//
// Return:
//   - CallRecord: 构建完成的调用记录
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

// SetTimeout 在运行时更新每个 Provider 的请求超时时间。
//
// Param:
//   - d: time.Duration - 新的超时时长，<= 0 时忽略
func (s *Scheduler) SetTimeout(d time.Duration) {
	if d > 0 {
		s.timeout.Store(int64(d))
	}
}

// IsClosed 报告调度器是否已关闭。
//
// Return:
//   - bool: 已关闭返回 true
func (s *Scheduler) IsClosed() bool {
	return s.closed.Load()
}

// History 返回已挂载的历史记录器（可能为 nil）。
//
// Return:
//   - *History: 历史记录器实例或 nil
func (s *Scheduler) History() *History {
	return s.history
}

// PromptInjector 返回已挂载的 Prompt 注入器（可能为 nil）。
//
// Return:
//   - *PromptInjector: 注入器实例或 nil
func (s *Scheduler) PromptInjector() *PromptInjector {
	return s.promptInjector.Load()
}

// SetPromptInjector 挂载 Prompt 注入器到调度器。
//
// Param:
//   - pi: *PromptInjector - 要挂载的注入器实例
func (s *Scheduler) SetPromptInjector(pi *PromptInjector) {
	s.promptInjector.Store(pi)
}

// Interceptors 返回当前的拦截器切片（可能为 nil）。
//
// Return:
//   - []core.Interceptor: 拦截器列表或 nil
func (s *Scheduler) Interceptors() []core.Interceptor {
	if ptr := s.interceptors.Load(); ptr != nil {
		return *ptr
	}
	return nil
}

// SetInterceptors 替换拦截器链（内部会创建副本）。
//
// Param:
//   - ic: []core.Interceptor - 新的拦截器列表
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

// Router 返回当前的路由器（可能为 nil）。
//
// Return:
//   - core.Router: 路由器实例或 nil
func (s *Scheduler) Router() core.Router {
	if r := s.router.Load(); r != nil {
		return *r
	}
	return nil
}

// SetRouter 替换 Provider 选择路由器。
//
// Param:
//   - r: core.Router - 新的路由器实例（传入 nil 可移除路由器）
func (s *Scheduler) SetRouter(r core.Router) {
	if r == nil {
		s.router.Store(nil)
	} else {
		s.router.Store(&r)
	}
}

// HealthCheckInterval 返回当前的健康检查间隔。
//
// Return:
//   - time.Duration: 健康检查间隔（0 表示已禁用）
func (s *Scheduler) HealthCheckInterval() time.Duration {
	return time.Duration(s.healthCheckInterval.Load())
}

// SetHealthCheckInterval 更新健康检查间隔并重启后台健康检查协程。传入 0 禁用。
//
// Param:
//   - d: time.Duration - 新的检查间隔，传入 0 禁用
func (s *Scheduler) SetHealthCheckInterval(d time.Duration) {
	s.StartHealthCheck(d)
}
