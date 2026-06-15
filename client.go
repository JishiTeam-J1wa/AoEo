// Package aoeo Client 门面层，SDK 主入口。
//
// Author: JishiTeam-J1wa
// Created: 2026-05
//
// Changelog:
//   2026-06-12 - 注释体系规范化
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

// 类型别名，保持向后兼容。
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
	Interceptor              = core.Interceptor
	InterceptorChain         = core.InterceptorChain
	Router                   = core.Router
	Provider                 = providers.Provider
)

// 便捷符号重新导出，供外部直接使用。
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
	LoadConfigFromEnv       = core.LoadConfigFromEnv
	LoadConfigFromEnvWithPrefix = core.LoadConfigFromEnvWithPrefix
	EnvConfigString         = core.EnvConfigString
	RetryConfigFromEnv      = core.RetryConfigFromEnv
	SetEnvConfig            = core.SetEnvConfig
	UnsetEnvConfig          = core.UnsetEnvConfig
	SetEnvConfigWithPrefix      = core.SetEnvConfigWithPrefix
	UnsetEnvConfigWithPrefix    = core.UnsetEnvConfigWithPrefix
)

// WithXxx 系列选项函数，从 engine 包重新导出。
var (
	WithTimeout              = engine.WithTimeout
	WithHistory              = engine.WithHistory
	WithRetry                = engine.WithRetry
	WithPromptInjector       = engine.WithPromptInjector
	WithInterceptors         = engine.WithInterceptors
	WithRouter               = engine.WithRouter
	WithHealthCheckInterval  = engine.WithHealthCheckInterval
	InjectPrompts            = engine.InjectPrompts
	WithSystemPromptInjector = engine.WithSystemPromptInjector
)

// 事件常量重新导出。
const (
	EventProviderFail    = core.EventProviderFail
	EventProviderOpen    = core.EventProviderOpen
	EventProviderRecover = core.EventProviderRecover
	EventFallbackTrigger = core.EventFallbackTrigger
	EventAuditDisagree   = core.EventAuditDisagree
	EventDualComplete    = core.EventDualComplete
)

// 哨兵错误重新导出，供 SDK 使用者判断特定错误场景。
var (
	ErrSchedulerClosed          = engine.ErrSchedulerClosed
	ErrNoAvailableProvider      = engine.ErrNoAvailableProvider
	ErrProviderNotFound         = engine.ErrProviderNotFound
	ErrAllProvidersFailed       = engine.ErrAllProvidersFailed
	ErrProviderConfigIncomplete = engine.ErrProviderConfigIncomplete
)

// Client 是 AoEo SDK 的高层入口，封装了调度器与事件发射器。
//
// 所有公开方法均为并发安全，可在多个 goroutine 中共享同一个 Client 实例。
type Client struct {
	scheduler *engine.Scheduler
	emitterMu sync.RWMutex
	emitter   core.EventEmitter
}

// NewClient 根据配置创建 AoEo 客户端。
//
// Param:
//   - cfg: core.Config - 多 Provider 配置，必须通过 Validate 校验
//   - opts: ...engine.SchedulerOption - 可选的调度器选项（超时、历史记录、路由等）
//
// Return:
//   - *Client: 创建成功的客户端实例
//   - error: 配置校验失败或应用配置出错时返回
//
// Edge Cases:
//   - cfg 中 Provider 列表为空时仍可创建，但调用补全方法会返回错误
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

// NewClientWithProviders 直接从 Provider 实例创建客户端，跳过配置流程。
//
// Param:
//   - provs: ...providers.Provider - 一个或多个 Provider 实例
//
// Return:
//   - *Client: 创建成功的客户端实例（不会返回 error）
func NewClientWithProviders(provs ...providers.Provider) *Client {
	return &Client{
		scheduler: engine.NewScheduler(provs...),
		emitter:   core.NopEmitter{},
	}
}

// History 返回关联的历史记录器，未配置时返回 nil。
func (c *Client) History() *engine.History {
	return c.scheduler.History()
}

// Stats 返回按 Provider 聚合的统计信息，未配置历史记录时返回 nil。
func (c *Client) Stats() map[string]engine.ProviderStats {
	if c.scheduler.History() == nil {
		return nil
	}
	return c.scheduler.History().Stats()
}

// SetEmitter 为客户端及其所有 Provider 挂载事件发射器。
//
// Param:
//   - emitter: core.EventEmitter - 事件发射器实例，传 nil 则回退为 NopEmitter
func (c *Client) SetEmitter(emitter core.EventEmitter) {
	c.emitterMu.Lock()
	if emitter == nil {
		c.emitter = core.NopEmitter{}
	} else {
		c.emitter = emitter
	}
	c.emitterMu.Unlock()
	// 将事件发射器传播到所有已注册的 Provider。
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

// ChatComplete 使用主 Provider 执行单次聊天补全请求。
//
// Param:
//   - ctx: context.Context - 请求上下文，用于超时和取消控制
//   - req: core.ChatCompletionRequest - 聊天补全请求
//
// Return:
//   - *core.ChatCompletionResponse: 补全响应
//   - error: 请求失败时返回
func (c *Client) ChatComplete(ctx context.Context, req core.ChatCompletionRequest) (*core.ChatCompletionResponse, error) {
	return c.scheduler.ChatComplete(ctx, req)
}

// ChatCompleteWithFallback 先尝试主 Provider，失败后按路由顺序降级到备用 Provider。
//
// Param:
//   - ctx: context.Context - 请求上下文
//   - req: core.ChatCompletionRequest - 聊天补全请求
//
// Return:
//   - *core.ChatCompletionResponse: 首个成功 Provider 的响应
//   - error: 所有 Provider 均失败时返回 ErrAllProvidersFailed
func (c *Client) ChatCompleteWithFallback(ctx context.Context, req core.ChatCompletionRequest) (*core.ChatCompletionResponse, error) {
	resp, err := c.scheduler.ChatCompleteWithFallback(ctx, req)
	if err != nil && errors.Is(err, engine.ErrAllProvidersFailed) {
		if available := c.scheduler.AvailableProviders(); len(available) > 0 {
			c.emit(core.EventFallbackTrigger, fmt.Sprintf("all providers failed, primary %s: %v", available[0].Name(), err))
		}
	}
	return resp, err
}

// ChatCompleteDual 同时向两个不同 Provider 发送请求，并发执行并比对结果。
//
// Param:
//   - ctx: context.Context - 请求上下文
//   - req: core.ChatCompletionRequest - 聊天补全请求
//
// Return:
//   - *core.DualResult: 包含两份响应及一致性标记
//   - error: 请求失败时返回
func (c *Client) ChatCompleteDual(ctx context.Context, req core.ChatCompletionRequest) (*core.DualResult, error) {
	result, err := c.scheduler.ChatCompleteDual(ctx, req)
	if err != nil {
		return nil, err
	}
	c.emit(core.EventDualComplete, result.Consensus)
	return result, nil
}

// ChatCompleteStream 执行流式聊天补全，通过 channel 逐步返回 SSE 数据块。
//
// Param:
//   - ctx: context.Context - 请求上下文
//   - req: core.ChatCompletionRequest - 聊天补全请求
//
// Return:
//   - <-chan core.StreamCompletionResponse: 流式数据块 channel，读完或出错时关闭
//   - error: 建立流连接失败时返回
func (c *Client) ChatCompleteStream(ctx context.Context, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error) {
	return c.scheduler.ChatCompleteStream(ctx, req)
}

// ChatCompleteWithProvider 向指定名称的 Provider 发送请求。
//
// 临时覆盖路由器以定向请求，完成后自动恢复原路由器。
//
// Param:
//   - ctx: context.Context - 请求上下文
//   - providerName: string - 目标 Provider 名称
//   - req: core.ChatCompletionRequest - 聊天补全请求
//
// Return:
//   - *core.ChatCompletionResponse: 补全响应
//   - error: 指定 Provider 不可用或请求失败时返回
func (c *Client) ChatCompleteWithProvider(ctx context.Context, providerName string, req core.ChatCompletionRequest) (*core.ChatCompletionResponse, error) {
	return c.scheduler.ChatCompleteWithRouter(ctx, &core.SingleProviderRouter{Name: providerName}, req)
}

// ChatCompleteStreamWithProvider 向指定名称的 Provider 发送流式请求。
//
// 临时覆盖路由器以定向请求，完成后自动恢复原路由器。
//
// Param:
//   - ctx: context.Context - 请求上下文
//   - providerName: string - 目标 Provider 名称
//   - req: core.ChatCompletionRequest - 聊天补全请求
//
// Return:
//   - <-chan core.StreamCompletionResponse: 流式数据块 channel
//   - error: 建立流连接失败时返回
func (c *Client) ChatCompleteStreamWithProvider(ctx context.Context, providerName string, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error) {
	return c.scheduler.ChatCompleteStreamWithRouter(ctx, &core.SingleProviderRouter{Name: providerName}, req)
}

// ListModels 返回指定 Provider 支持的模型列表。
//
// Param:
//   - ctx: context.Context - 请求上下文
//   - providerName: string - Provider 名称
//
// Return:
//   - []core.ModelInfo: 模型信息列表
//   - error: Provider 不存在或请求失败时返回
func (c *Client) ListModels(ctx context.Context, providerName string) ([]core.ModelInfo, error) {
	return c.scheduler.ListModels(ctx, providerName)
}

// TestProvider 测试与指定 Provider 的网络连通性。
//
// Param:
//   - ctx: context.Context - 请求上下文
//   - providerName: string - Provider 名称
//
// Return:
//   - error: 连接失败时返回错误信息
func (c *Client) TestProvider(ctx context.Context, providerName string) error {
	return c.scheduler.TestProvider(ctx, providerName)
}

// ProviderStatus 返回所有 Provider 的当前运行状态（可用性、健康指标等）。
func (c *Client) ProviderStatus() []core.ProviderStatus {
	return c.scheduler.ProviderStatus()
}

// PromptInjector 返回关联的 Prompt 注入器，未配置时返回 nil。
func (c *Client) PromptInjector() *engine.PromptInjector {
	return c.scheduler.PromptInjector()
}

// SetPromptInjector 为客户端挂载 Prompt 注入器。
//
// Param:
//   - pi: *engine.PromptInjector - Prompt 注入器实例
func (c *Client) SetPromptInjector(pi *engine.PromptInjector) {
	c.scheduler.SetPromptInjector(pi)
}

// Interceptors 返回当前的拦截器链，未配置时返回 nil。
func (c *Client) Interceptors() []core.Interceptor {
	return c.scheduler.Interceptors()
}

// SetInterceptors 替换拦截器链。
//
// Param:
//   - ic: []core.Interceptor - 新的拦截器切片
func (c *Client) SetInterceptors(ic []core.Interceptor) {
	c.scheduler.SetInterceptors(ic)
}

// Router 返回当前的路由选择器，未配置时返回 nil。
func (c *Client) Router() core.Router {
	return c.scheduler.Router()
}

// SetRouter 替换 Provider 路由选择器。
//
// Param:
//   - r: core.Router - 新的路由器实例
func (c *Client) SetRouter(r core.Router) {
	c.scheduler.SetRouter(r)
}

// HealthCheckInterval 返回当前后台健康检查的执行间隔。
func (c *Client) HealthCheckInterval() time.Duration {
	return c.scheduler.HealthCheckInterval()
}

// SetHealthCheckInterval 更新后台健康检查的执行间隔。
//
// Param:
//   - d: time.Duration - 检查间隔，传 0 禁用健康检查
func (c *Client) SetHealthCheckInterval(d time.Duration) {
	c.scheduler.SetHealthCheckInterval(d)
}

// Close 优雅关闭客户端，释放所有资源。多次调用安全无副作用。
func (c *Client) Close() error {
	return c.scheduler.Close()
}

// IsClosed 返回客户端是否已关闭。
func (c *Client) IsClosed() bool {
	return c.scheduler.IsClosed()
}

// SetTimeout 在运行时更新每个 Provider 的请求超时时间。
//
// Param:
//   - d: time.Duration - 新的超时时长
func (c *Client) SetTimeout(d time.Duration) {
	c.scheduler.SetTimeout(d)
}

// Scheduler 返回底层调度器实例，供高级用户直接操作。
func (c *Client) Scheduler() *engine.Scheduler {
	return c.scheduler
}

// Audit 使用另一个 Provider 执行二次补全，并与主响应进行一致性比对。
//
// Param:
//   - ctx: context.Context - 请求上下文
//   - req: core.ChatCompletionRequest - 聊天补全请求
//
// Return:
//   - *core.AuditResult: 包含主响应、审计响应、一致性标记
//   - error: 请求失败时返回
//
// Edge Cases:
//   - 当两次补全结果不一致时，会触发 EventAuditDisagree 事件
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
