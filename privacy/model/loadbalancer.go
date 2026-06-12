// loadbalancer.go 实现多后端负载均衡客户端，支持轮询、随机和最低延迟策略，
// 内含健康检查、EWMA 延迟跟踪和自动故障转移。
//
// Author: JishiTeam-J1wa
// Created: 2026-06
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package model

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

// LoadBalancedClient 将 Detect 调用分发到多个 sidecar 后端。
// 实现了 model.Client 接口，支持轮询调度、健康检查和自动故障转移。
type LoadBalancedClient struct {
	backends   []*backend   // 后端实例列表
	strategy   Strategy     // 负载均衡策略
	idx        atomic.Uint64 // 轮询计数器（原子操作，并发安全）
	hcInterval time.Duration // 健康检查间隔时间
	autoHC     bool          // 是否自动启动后台健康检查
	stopHC     chan struct{}  // 停止健康检查的信号通道
	hcOnce     sync.Once     // 确保健康检查协程只启动一次
	closeOnce  sync.Once     // 确保 Close 只执行一次，防止重复关闭 panic
}

// Strategy 定义请求在后端间的分发策略。
type Strategy int

const (
	// RoundRobin 按顺序轮询后端（循环调度）。
	RoundRobin Strategy = iota
	// Random 随机选择一个后端。
	Random
	// LeastLatency 路由到 EWMA 延迟最低的后端。
	LeastLatency
)

// backend 封装单个 sidecar 实例及其状态。
type backend struct {
	client    *HTTPClient          // HTTP 客户端实例
	endpoint  string               // 后端端点地址
	healthy   atomic.Bool          // 健康状态（原子操作）
	lastErr   atomic.Pointer[string] // 最后一次错误信息（原子操作）
	lastCheck atomic.Int64         // 最后检查时间的 unix 纳秒时间戳

	// EWMA 延迟跟踪（纳秒）。每次成功的 Detect 调用后更新。
	// 使用 CAS 循环确保并发更新的原子性。
	latencyNs atomic.Int64
}

// ewmaAlpha 是 EWMA 延迟计算的平滑因子。
// 值越大，对新延迟数据的响应越灵敏；值越小，越平滑。
const ewmaAlpha = 0.3

// defaultLatencyNs 是新后端的初始 EWMA 延迟值。
// 设置为较高的值（100ms），避免新后端因为零延迟吸引所有流量（惊群效应）。
// 随着真实请求的延迟数据积累，该值会逐渐收敛到实际延迟。
const defaultLatencyNs = int64(100 * time.Millisecond)

// LoadBalancedClientOption 配置 LoadBalancedClient 的选项函数。
type LoadBalancedClientOption func(*LoadBalancedClient)

// WithHCInterval 设置健康检查间隔时间（默认 10 秒）。
func WithHCInterval(d time.Duration) LoadBalancedClientOption {
	return func(lb *LoadBalancedClient) {
		lb.hcInterval = d
	}
}

// WithAutoHealthCheck 控制是否自动启动后台健康检查（默认 true）。
// 测试中可设为 false 以手动管理健康检查。
func WithAutoHealthCheck(auto bool) LoadBalancedClientOption {
	return func(lb *LoadBalancedClient) {
		lb.autoHC = auto
	}
}

// NewLoadBalancedClient 创建一个在给定端点间负载均衡的客户端。
// 端点参数支持逗号分隔的多地址格式。
//
// Param:
//   - endpoints: string - 逗号分隔的端点地址列表
//   - strategy: Strategy - 负载均衡策略（RoundRobin、Random、LeastLatency）
//   - opts: ...HTTPClientOption - HTTP 客户端配置选项
//
// Return:
//   - *LoadBalancedClient: 初始化完成的负载均衡客户端
func NewLoadBalancedClient(endpoints string, strategy Strategy, opts ...HTTPClientOption) *LoadBalancedClient {
	return NewLoadBalancedClientWithOptions(endpoints, strategy, opts, nil)
}

// NewLoadBalancedClientWithOptions 创建具有完整选项控制的负载均衡客户端。
// 支持 HTTP 客户端选项和负载均衡选项的独立配置。
//
// Param:
//   - endpoints: string - 逗号分隔的端点地址列表
//   - strategy: Strategy - 负载均衡策略
//   - httpOpts: []HTTPClientOption - HTTP 客户端配置选项
//   - lbOpts: []LoadBalancedClientOption - 负载均衡器配置选项
//
// Return:
//   - *LoadBalancedClient: 初始化完成的负载均衡客户端
func NewLoadBalancedClientWithOptions(endpoints string, strategy Strategy, httpOpts []HTTPClientOption, lbOpts []LoadBalancedClientOption) *LoadBalancedClient {
	parts := splitEndpoints(endpoints)
	if len(parts) == 0 {
		return &LoadBalancedClient{}
	}

	backends := make([]*backend, 0, len(parts))
	for _, ep := range parts {
		ep = strings.TrimSpace(ep)
		if ep == "" {
			continue
		}
		b := &backend{
			client:   NewHTTPClient(ep, httpOpts...),
			endpoint: ep,
		}
		b.healthy.Store(true) // 乐观假设：在首次健康检查前视为健康
		// 设置较高的初始延迟值，防止新后端因零延迟吸引所有流量（惊群效应）
		b.latencyNs.Store(defaultLatencyNs)
		backends = append(backends, b)
	}

	lb := &LoadBalancedClient{
		backends:   backends,
		strategy:   strategy,
		hcInterval: 10 * time.Second,
		autoHC:     true,
		stopHC:     make(chan struct{}),
	}
	for _, opt := range lbOpts {
		opt(lb)
	}
	if lb.autoHC {
		lb.startHealthChecks()
	}
	return lb
}

// Detect 将文本发送到健康的后端进行检测。
// 如果选中的后端失败，自动故障转移到下一个健康后端。
//
// Param:
//   - ctx: context.Context - 请求上下文，用于超时控制
//   - text: string - 待检测的文本内容
//
// Return:
//   - []Span: 检测到的敏感信息片段列表
//   - error: 所有后端均失败时返回最后一次的错误
func (lb *LoadBalancedClient) Detect(ctx context.Context, text string) ([]Span, error) {
	order := lb.pickOrder() // 根据策略决定后端的尝试顺序
	var lastErr error

	for _, b := range order {
		if !b.healthy.Load() {
			continue // 跳过不健康的后端
		}
		spans, err := lb.detectOn(b, ctx, text)
		if err == nil {
			return spans, nil
		}
		lastErr = err
		b.healthy.Store(false) // 标记为不健康
		s := err.Error()
		b.lastErr.Store(&s)
		core.GetLogger().Warn("privacy backend failed, trying next",
			"endpoint", b.endpoint,
			"error", err,
		)
	}

	return nil, fmt.Errorf("all privacy backends failed (last: %w)", lastErr)
}

// DetectBatch 将一批文本分发到后端进行批量检测。
// 为简化实现，所有文本发送到同一个后端（由策略选择）。
// 如果该后端失败，自动故障转移到下一个健康后端。
//
// Param:
//   - ctx: context.Context - 请求上下文，用于超时控制
//   - texts: []string - 待检测的文本列表
//
// Return:
//   - [][]Span: 每段文本对应的检测结果列表
//   - error: 所有后端均失败时返回最后一次的错误
func (lb *LoadBalancedClient) DetectBatch(ctx context.Context, texts []string) ([][]Span, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	order := lb.pickOrder()
	var lastErr error

	for _, b := range order {
		if !b.healthy.Load() {
			continue
		}
		start := time.Now()
		results, err := b.client.DetectBatch(ctx, texts)
		if err == nil {
			b.updateLatency(time.Since(start))
			return results, nil
		}
		lastErr = err
		b.healthy.Store(false)
		s := err.Error()
		b.lastErr.Store(&s)
		core.GetLogger().Warn("privacy backend batch failed, trying next",
			"endpoint", b.endpoint,
			"error", err,
		)
	}

	return nil, fmt.Errorf("all privacy backends failed batch (last: %w)", lastErr)
}

// detectOn 在指定后端上执行 Detect 调用，并附带 EWMA 延迟跟踪。
func (lb *LoadBalancedClient) detectOn(b *backend, ctx context.Context, text string) ([]Span, error) {
	start := time.Now()
	spans, err := b.client.Detect(ctx, text)
	if err == nil {
		b.updateLatency(time.Since(start))
	}
	return spans, err
}

// updateLatency 使用 CAS 循环更新后端的 EWMA 延迟值。
// CAS（Compare-And-Swap）循环确保在并发环境下多个协程不会互相覆盖延迟更新：
// 每次更新时先读取旧值，计算新值，然后原子地比较并交换。
// 如果交换失败（说明其他协程已更新），则重新计算。
func (b *backend) updateLatency(d time.Duration) {
	for {
		old := b.latencyNs.Load()
		newNs := int64(float64(old)*(1-ewmaAlpha) + float64(d.Nanoseconds())*ewmaAlpha)
		if b.latencyNs.CompareAndSwap(old, newNs) {
			break
		}
		// CAS 失败说明其他协程已更新延迟值，重新读取并重试
	}
}

// HealthCheck 检查是否至少有一个后端处于健康状态。
//
// Param:
//   - ctx: context.Context - 请求上下文（当前实现未使用，保留以满足接口约定）
//
// Return:
//   - bool: 至少有一个后端健康时返回 true
//   - error: 当前始终返回 nil
func (lb *LoadBalancedClient) HealthCheck(ctx context.Context) (bool, error) {
	for _, b := range lb.backends {
		if b.healthy.Load() {
			return true, nil
		}
	}
	return false, nil
}

// Close 停止后台健康检查协程。
// 使用 sync.Once 确保多次调用不会 panic（防止重复关闭 channel）。
func (lb *LoadBalancedClient) Close() {
	lb.closeOnce.Do(func() {
		close(lb.stopHC)
	})
}

// Stats 返回每个后端的运行状态和健康信息。
//
// Return:
//   - []BackendStats: 每个后端的健康状态快照
func (lb *LoadBalancedClient) Stats() []BackendStats {
	out := make([]BackendStats, 0, len(lb.backends))
	for _, b := range lb.backends {
		st := BackendStats{
			Endpoint:  b.endpoint,
			Healthy:   b.healthy.Load(),
			LastCheck: time.Unix(0, b.lastCheck.Load()),
			LatencyMs: time.Duration(b.latencyNs.Load()).Milliseconds(),
		}
		if p := b.lastErr.Load(); p != nil {
			st.LastError = *p
		}
		out = append(out, st)
	}
	return out
}

// BackendStats 描述单个后端的健康状态信息。
type BackendStats struct {
	Endpoint  string    // 后端端点地址
	Healthy   bool      // 是否健康
	LastCheck time.Time // 最后一次健康检查时间
	LastError string    // 最后一次错误信息
	LatencyMs int64     // EWMA 延迟（毫秒）
}

// ---------------------------------------------------------------------------
// 内部实现
// ---------------------------------------------------------------------------

// pickOrder 根据负载均衡策略决定后端的尝试顺序。
// 返回排序后的后端列表，供 Detect/DetectBatch 按顺序尝试。
func (lb *LoadBalancedClient) pickOrder() []*backend {
	n := len(lb.backends)
	if n == 0 {
		return nil
	}

	switch lb.strategy {
	case Random:
		// 随机打乱后端顺序（Fisher-Yates 洗牌算法）
		shuffled := make([]*backend, n)
		copy(shuffled, lb.backends)
		for i := n - 1; i > 0; i-- {
			j := rand.Intn(i + 1)
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		}
		return shuffled

	case LeastLatency:
		// 按 EWMA 延迟升序排列，优先选择延迟最低的后端
		// 先过滤出健康后端，避免将请求发送到故障节点
		healthy := make([]*backend, 0, n)
		for _, b := range lb.backends {
			if b.healthy.Load() {
				healthy = append(healthy, b)
			}
		}
		if len(healthy) == 0 {
			return lb.backends // 回退：所有后端不健康时返回全部
		}
		sortBackendsByLatency(healthy)
		return healthy

	default: // RoundRobin（轮询）
		// 使用原子计数器确保并发安全，从下一个位置开始轮询
		start := int(lb.idx.Add(1)) % n
		if start < 0 {
			start = 0
		}
		ordered := make([]*backend, 0, n)
		ordered = append(ordered, lb.backends[start:]...)
		ordered = append(ordered, lb.backends[:start]...)
		return ordered
	}
}

// sortBackendsByLatency 按 EWMA 延迟升序排列后端（简单选择排序）。
func sortBackendsByLatency(bes []*backend) {
	for i := 0; i < len(bes); i++ {
		for j := i + 1; j < len(bes); j++ {
			if bes[i].latencyNs.Load() > bes[j].latencyNs.Load() {
				bes[i], bes[j] = bes[j], bes[i]
			}
		}
	}
}

// startHealthChecks 启动后台健康检查协程（仅启动一次）。
func (lb *LoadBalancedClient) startHealthChecks() {
	lb.hcOnce.Do(func() {
		go lb.healthCheckLoop()
	})
}

// healthCheckLoop 后台健康检查主循环。
// 启动时立即执行一次检查（含预热），之后按设定间隔周期性检查。
func (lb *LoadBalancedClient) healthCheckLoop() {
	// 启动时立即执行一次检查（含连接预热）
	lb.runHealthChecks(true)

	ticker := time.NewTicker(lb.hcInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			lb.runHealthChecks(false)
		case <-lb.stopHC:
			return
		}
	}
}

// runHealthChecks 对所有后端执行健康检查。
// warm 参数为 true 时，会对健康后端发送预热请求以建立 TCP/HTTP2 连接。
func (lb *LoadBalancedClient) runHealthChecks(warm bool) {
	for _, b := range lb.backends {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		ok, err := b.client.HealthCheck(ctx)
		cancel()

		b.lastCheck.Store(time.Now().UnixNano())
		wasHealthy := b.healthy.Load()
		b.healthy.Store(ok)

		if !ok && err != nil {
			s := err.Error()
			b.lastErr.Store(&s)
		}

		// 记录状态变化日志：从健康变为不健康、从不健康恢复为健康
		if wasHealthy && !ok {
			core.GetLogger().Error("privacy backend unhealthy",
				"endpoint", b.endpoint,
				"error", err,
			)
		} else if !wasHealthy && ok {
			core.GetLogger().Info("privacy backend recovered", "endpoint", b.endpoint)
			b.lastErr.Store(nil)
		}

		// 预热阶段：对健康后端发送简单请求以建立连接
		if warm && ok {
			go func(bk *backend) {
				wCtx, wCancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer wCancel()
				// 发送空文本进行连接预热（sidecar 应返回空 spans）
				start := time.Now()
				_, _ = bk.client.Detect(wCtx, "privacy_warmup")
				// 不关心结果，仅确保连接已建立
				core.GetLogger().Debug("privacy backend warmed up",
					"endpoint", bk.endpoint,
					"latency_ms", time.Since(start).Milliseconds(),
				)
			}(b)
		}
	}
}

// splitEndpoints 将逗号分隔的端点字符串拆分为端点列表。
// 会过滤掉空字符串，防止配置中的尾逗号或多余逗号导致无效端点进入负载均衡池。
func splitEndpoints(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	// 过滤空字符串，避免无效端点
	filtered := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}
