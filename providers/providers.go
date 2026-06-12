// Package providers 实现 AI 服务提供商适配层，支持 OpenAI、Anthropic 等多家 API。
//
// Author: JishiTeam-J1wa
// Created: 2026-05
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package providers

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/sashabaranov/go-openai"
)

// Provider 是所有 AI 模型提供商适配器必须实现的接口。
// 每个 Provider 封装了与特定 AI 服务（如 DeepSeek、Kimi、GLM 等）的通信逻辑，
// 支持同步/流式聊天补全、健康检查、模型列表查询等功能。
type Provider interface {
	// Name 返回 Provider 的唯一标识名称。
	Name() string
	// Config 返回 Provider 的当前配置。
	Config() core.ProviderConfig
	// ChatComplete 执行同步聊天补全请求。
	ChatComplete(ctx context.Context, req core.ChatCompletionRequest) (*core.ChatCompletionResponse, error)
	// ChatCompleteStream 执行流式聊天补全请求，返回一个接收流式响应的 channel。
	ChatCompleteStream(ctx context.Context, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error)
	// IsAvailable 返回 Provider 是否可用（配置完整且未处于熔断冷却期）。
	IsAvailable() bool
	// ListModels 查询该 Provider 支持的所有可用模型。
	ListModels(ctx context.Context) ([]core.ModelInfo, error)
	// SetEmitter 设置事件发射器，用于 Provider 生命周期事件通知。
	SetEmitter(e core.EventEmitter)
	// HealthCheck 执行轻量级的连通性检查。
	HealthCheck(ctx context.Context) error
}

// healthEntry 是滑动窗口中的单条观测记录。
type healthEntry struct {
	latencyMs int64
	success   bool
	timestamp time.Time
}

// BaseProvider 为所有 Provider 提供通用逻辑（熔断器、系统 Prompt 覆盖、健康指标追踪）。
// 它已导出，可供自定义 Provider 实现嵌入使用。
// 大多数用户应直接使用内置 Provider（如 DeepSeek、Kimi 等）或通用的 OpenAIProvider。
//
// 核心功能：
//   - 熔断器：通过原子计数器追踪连续失败次数，超过阈值后进入冷却期
//   - 系统 Prompt 覆盖：支持运行时动态设置/清除系统 Prompt
//   - 健康指标：使用滑动窗口记录最近 20 次调用的延迟和成功率
//   - 事件发射：在熔断器状态变更时发出事件通知
type BaseProvider struct {
	config core.ProviderConfig

	// 熔断器：追踪连续失败次数（全部使用原子操作，无需加锁）
	failCount atomic.Int32
	failUntil atomic.Int64 // UnixNano 格式的冷却截止时间，0 表示未处于冷却期

	// 系统 Prompt 覆盖（原子指针，指向字符串）
	sysPrompt atomic.Pointer[string]

	// 可选的事件发射器，用于 Provider 生命周期事件通知
	emitter atomic.Value // 存储 *emitterBox

	// 运行时健康指标（滑动窗口，记录最近 20 次调用）
	healthMu     sync.RWMutex
	healthWindow [20]healthEntry
	healthHead   int
	healthCount  int
	healthLatest atomic.Pointer[core.ProviderHealth]
}

// emitterBox 封装 EventEmitter，使 atomic.Value 存储类型一致的指针。
type emitterBox struct {
	em core.EventEmitter
}

// NewBaseProvider 创建一个带有指定配置的 BaseProvider。
//
// Param:
//   - config: core.ProviderConfig - Provider 配置
//
// Return:
//   - *BaseProvider: 初始化完成的 BaseProvider 实例
func NewBaseProvider(config core.ProviderConfig) *BaseProvider {
	bp := &BaseProvider{config: config}
	bp.emitter.Store(&emitterBox{em: core.NopEmitter{}})
	bp.healthLatest.Store(&core.ProviderHealth{})
	return bp
}

// RecordHealthCheck 记录一次健康检查探测的结果。
//
// Param:
//   - latencyMs: int64 - 探测延迟（毫秒）
//   - success: bool - 探测是否成功
func (b *BaseProvider) RecordHealthCheck(latencyMs int64, success bool) {
	b.pushHealthEntry(healthEntry{latencyMs: latencyMs, success: success, timestamp: time.Now()})
}

// RecordCallResult 记录一次实际 API 调用的结果。
//
// Param:
//   - latencyMs: int64 - 调用延迟（毫秒）
//   - err: error - 调用错误（nil 表示成功）
func (b *BaseProvider) RecordCallResult(latencyMs int64, err error) {
	b.pushHealthEntry(healthEntry{latencyMs: latencyMs, success: err == nil, timestamp: time.Now()})
}

// Health 返回当前运行时健康状态快照。
//
// Return:
//   - core.ProviderHealth: 当前健康状态（含延迟、成功率等指标）
func (b *BaseProvider) Health() core.ProviderHealth {
	if h := b.healthLatest.Load(); h != nil {
		return *h
	}
	return core.ProviderHealth{}
}

// pushHealthEntry 将一条记录添加到滑动窗口并重新计算健康快照。
func (b *BaseProvider) pushHealthEntry(entry healthEntry) {
	b.healthMu.Lock()
	defer b.healthMu.Unlock()

	b.healthWindow[b.healthHead] = entry
	b.healthHead = (b.healthHead + 1) % len(b.healthWindow)
	if b.healthCount < len(b.healthWindow) {
		b.healthCount++
	}

	// 从滑动窗口重新计算聚合指标。
	var totalLatency int64
	var successCount int
	var consecutiveFails int
	var lastFailStreak int
	for i := 0; i < b.healthCount; i++ {
		idx := (b.healthHead - b.healthCount + i + len(b.healthWindow)) % len(b.healthWindow)
		e := b.healthWindow[idx]
		totalLatency += e.latencyMs
		if e.success {
			successCount++
			lastFailStreak = 0
		} else {
			lastFailStreak++
			if lastFailStreak > consecutiveFails {
				consecutiveFails = lastFailStreak
			}
		}
	}

	// 使用 Go 1.21+ 内置的 max 函数，无需自定义实现
	snapshot := &core.ProviderHealth{
		LastCheckAt:      entry.timestamp,
		LastLatencyMs:    entry.latencyMs,
		AvgLatencyMs:     totalLatency / int64(max(b.healthCount, 1)),
		SuccessRate:      float64(successCount) / float64(max(b.healthCount, 1)),
		ConsecutiveFails: consecutiveFails,
		TotalChecks:      b.healthCount,
	}
	b.healthLatest.Store(snapshot)
}

// 注意：此处不再定义自定义 max() 函数。
// Go 1.21+ 已内置泛型 max/min 函数，本项目使用 Go 1.25，直接使用内置版本。
// 修复 P-08：移除自定义 max()，避免遮蔽（shadow）内置函数。

// Config 返回 Provider 的当前配置。
func (b *BaseProvider) Config() core.ProviderConfig {
	return b.config
}

// SetEmitter 设置事件发射器。当传入 nil 时使用空操作发射器。
//
// Param:
//   - e: core.EventEmitter - 事件发射器实例，nil 表示使用空操作发射器
func (b *BaseProvider) SetEmitter(e core.EventEmitter) {
	if e == nil {
		b.emitter.Store(&emitterBox{em: core.NopEmitter{}})
	} else {
		b.emitter.Store(&emitterBox{em: e})
	}
}

func (b *BaseProvider) getEmitter() core.EventEmitter {
	if box, ok := b.emitter.Load().(*emitterBox); ok {
		return box.em
	}
	return core.NopEmitter{}
}

// RecordSuccess 在调用成功时重置失败计数器。
func (b *BaseProvider) RecordSuccess() {
	wasFailed := b.failCount.Load() > 0
	b.failCount.Store(0)
	b.failUntil.Store(0)

	if wasFailed {
		b.getEmitter().Emit(core.EventProviderRecover, b.config.Name)
	}
}

// RecordFailure 递增失败计数器，当连续失败次数达到 MaxFailures 时触发熔断冷却。
func (b *BaseProvider) RecordFailure() {
	count := b.failCount.Add(1)
	maxFailures := b.config.MaxFailures
	if maxFailures <= 0 {
		maxFailures = 3
	}
	cooldown := b.config.CooldownDuration
	if cooldown <= 0 {
		cooldown = 60 * time.Second
	}
	opened := false
	if int(count) >= maxFailures {
		b.failUntil.Store(time.Now().Add(cooldown).UnixNano())
		opened = true
		core.GetLogger().Warn("circuit breaker opened",
			"provider", b.config.Name,
			"failCount", count,
			"cooldownUntil", time.Unix(0, b.failUntil.Load()).Format(time.RFC3339))
	} else {
		core.GetLogger().Warn("provider failure recorded",
			"provider", b.config.Name,
			"failCount", count)
	}

	b.getEmitter().Emit(core.EventProviderFail, b.config.Name, count)
	if opened {
		b.getEmitter().Emit(core.EventProviderOpen, b.config.Name, count)
	}
}

// IsAvailable 返回 Provider 是否具备最低配置要求且未处于熔断冷却期。
//
// Return:
//   - bool: true 表示可用，false 表示配置不完整或处于冷却期
func (b *BaseProvider) IsAvailable() bool {
	if b.config.APIKey == "" || b.config.Endpoint == "" || b.config.Model == "" {
		return false
	}
	until := b.failUntil.Load()
	if until == 0 {
		return true
	}
	return time.Now().UnixNano() >= until
}

// HealthCheck 执行轻量级的连通性检查。
// 使用 5 秒超时，对 Provider 端点的基础 URL 发起 HTTP GET 请求。
// 延迟和结果会记录到 Provider 的健康窗口中。
//
// Param:
//   - ctx: context.Context - 调用上下文
//
// Return:
//   - error: 检查失败或配置不完整时返回错误
func (b *BaseProvider) HealthCheck(ctx context.Context) error {
	if b.config.APIKey == "" || b.config.Endpoint == "" {
		b.RecordHealthCheck(0, false)
		return fmt.Errorf("provider %s config incomplete", b.config.Name)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	if b.config.HTTPClient != nil {
		client = b.config.HTTPClient
	}

	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, b.config.Endpoint, nil)
	if err != nil {
		b.RecordHealthCheck(0, false)
		return fmt.Errorf("health check request for %s: %w", b.config.Name, err)
	}
	req.Header.Set("Authorization", "Bearer "+b.config.APIKey)

	start := time.Now()
	resp, err := client.Do(req)
	latencyMs := time.Since(start).Milliseconds()
	if err != nil {
		b.RecordHealthCheck(latencyMs, false)
		return fmt.Errorf("health check failed for %s: %w", b.config.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		b.RecordHealthCheck(latencyMs, false)
		return fmt.Errorf("health check for %s returned status %d", b.config.Name, resp.StatusCode)
	}
	b.RecordHealthCheck(latencyMs, true)
	return nil
}

// ListModels 通过 OpenAI 兼容的 /models 端点查询该 Provider 支持的可用模型列表。
// 注意：此方法会创建临时客户端；如需连接复用，请使用 OpenAIProvider.ListModels。
//
// Param:
//   - ctx: context.Context - 调用上下文
//
// Return:
//   - []core.ModelInfo: 可用模型信息列表
//   - error: 查询失败或配置不完整时返回错误
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

	result := make([]core.ModelInfo, 0, len(models.Models))
	for _, m := range models.Models {
		result = append(result, core.ModelInfo{ID: m.ID, OwnedBy: m.OwnedBy})
	}
	return result, nil
}

// SetSystemPrompt 设置覆盖系统 Prompt，将在下次补全调用时生效。
//
// Param:
//   - prompt: string - 系统 Prompt 内容
func (b *BaseProvider) SetSystemPrompt(prompt string) {
	b.sysPrompt.Store(&prompt)
}

// ClearSystemPrompt 移除系统 Prompt 覆盖。
func (b *BaseProvider) ClearSystemPrompt() {
	b.sysPrompt.Store(nil)
}

// GetSystemPrompt 返回已设置的系统 Prompt 覆盖，未设置时返回空字符串。
//
// Return:
//   - string: 系统 Prompt 覆盖内容，未设置时返回 ""
func (b *BaseProvider) GetSystemPrompt() string {
	if ptr := b.sysPrompt.Load(); ptr != nil {
		return *ptr
	}
	return ""
}

// ChatCompleteStream 提供默认实现，返回不支持流式的错误。
// 支持流式的 Provider 应覆盖此方法。
func (b *BaseProvider) ChatCompleteStream(ctx context.Context, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error) {
	return nil, fmt.Errorf("provider %s does not support streaming", b.config.Name)
}

// OpenAIProvider 实现了通用的 OpenAI 兼容协议 Provider 适配器。
// 它适用于所有遵循 OpenAI Chat Completions 协议的 API 服务，
// 包括自托管模型（如 vLLM、Ollama 等）以及各商业 AI 服务商的兼容接口。
//
// 核心特性：
//   - 同步聊天补全（ChatComplete）：带自动重试、熔断器、健康指标追踪
//   - 流式聊天补全（ChatCompleteStream）：通过 channel 逐块返回响应
//   - 系统 Prompt 覆盖：支持运行时动态切换系统 Prompt
//   - Temperature 兼容处理：自动检测并兼容不支持自定义 temperature 的模型
type OpenAIProvider struct {
	*BaseProvider                   // 嵌入 BaseProvider，继承熔断器、健康指标等通用功能
	Client       *openai.Client     // OpenAI 兼容协议的客户端实例
}

// NewOpenAIProvider 创建一个通用的 OpenAI 兼容协议 Provider。
// 如果 endpoint 为空，默认使用 "https://api.openai.com/v1"。
// 自动根据配置构建 HTTP 客户端（支持自定义 Transport、代理、跳过 TLS 验证等）。
func NewOpenAIProvider(config core.ProviderConfig) *OpenAIProvider {
	if config.Endpoint == "" {
		config.Endpoint = "https://api.openai.com/v1"
	}

	oc := openai.DefaultConfig(config.APIKey)
	oc.BaseURL = config.Endpoint

	// 构建最终的 HTTP 客户端，支持自定义 HTTPClient、代理和跳过 TLS 验证。
	oc.HTTPClient = buildHTTPClient(config)

	return &OpenAIProvider{
		BaseProvider: NewBaseProvider(config),
		Client:       openai.NewClientWithConfig(oc),
	}
}

// buildHTTPClient 根据 ProviderConfig 字段组装 *http.Client。
// 优先级：自定义 HTTPClient transport > DefaultTransport，然后应用 Proxy 和 SkipTLSVerify。
// 当提供了自定义 HTTPClient 时，保留其 CheckRedirect 和 Jar。
func buildHTTPClient(config core.ProviderConfig) *http.Client {
	// 快速路径：无需修改。
	if config.Proxy == "" && !config.SkipTLSVerify {
		if config.HTTPClient != nil {
			return config.HTTPClient
		}
		return &http.Client{Timeout: 120 * time.Second}
	}

	// 慢速路径：需要构建带有 Proxy 和/或 TLS 覆盖的 transport。
	var base *http.Client
	if config.HTTPClient != nil {
		base = config.HTTPClient
	}

	var timeout time.Duration
	if base != nil {
		timeout = base.Timeout
	}
	if timeout == 0 {
		timeout = 120 * time.Second
	}

	transport := deriveTransport(base)

	// 如果配置了代理，则应用代理设置。
	if config.Proxy != "" {
		proxyURL, err := url.Parse(config.Proxy)
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}

	// 如果配置了跳过 TLS 验证，则应用 TLS 设置。
	if config.SkipTLSVerify {
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		}
		transport.TLSClientConfig.InsecureSkipVerify = true
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
	if base != nil {
		client.CheckRedirect = base.CheckRedirect
		client.Jar = base.Jar
	}
	return client
}

// deriveTransport 从给定的客户端返回可克隆的 *http.Transport，
// 如果客户端的 transport 不可克隆则回退到 http.DefaultTransport。
// 返回的 transport 始终遵循 HTTP_PROXY/HTTPS_PROXY/NO_PROXY 环境变量，
// 除非调用方已显式配置了 Proxy。
func deriveTransport(base *http.Client) *http.Transport {
	if base != nil && base.Transport != nil {
		if t, ok := base.Transport.(*http.Transport); ok {
			return t.Clone()
		}
	}
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		return t.Clone()
	}
	// 回退：创建最小化的 transport，仍然遵循标准代理环境变量。
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
	}
}

func (p *OpenAIProvider) Name() string { return p.Config().Name }

// buildOpenAIMessages 将 core.Message 切片转换为 go-openai 消息格式，
// 保留工具调用、工具调用 ID 和函数名称。
func buildOpenAIMessages(messages []core.Message) []openai.ChatCompletionMessage {
	result := make([]openai.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		result[i] = openai.ChatCompletionMessage{
			Role:       m.Role,
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		if len(m.ToolCalls) > 0 {
			result[i].ToolCalls = make([]openai.ToolCall, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				result[i].ToolCalls[j] = openai.ToolCall{
					ID:   tc.ID,
					Type: openai.ToolType(tc.Type),
					Function: openai.FunctionCall{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
				if tc.Index > 0 {
					idx := tc.Index
					result[i].ToolCalls[j].Index = &idx
				}
			}
		}
	}
	return result
}

// buildOpenAITools 将 core.Tool 切片转换为 go-openai 工具格式。
func buildOpenAITools(tools []core.Tool) []openai.Tool {
	if len(tools) == 0 {
		return nil
	}
	result := make([]openai.Tool, len(tools))
	for i, t := range tools {
		result[i] = openai.Tool{Type: openai.ToolType(t.Type)}
		if t.Function != nil {
			result[i].Function = &openai.FunctionDefinition{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
				Strict:      t.Function.Strict,
			}
		}
	}
	return result
}

// buildOpenAIToolChoice 将 core 工具选择值转换为 go-openai 格式。
func buildOpenAIToolChoice(choice any) any {
	if choice == nil {
		return nil
	}
	if s, ok := choice.(string); ok {
		return s
	}
	if tc, ok := choice.(core.ToolChoice); ok {
		return openai.ToolChoice{
			Type: openai.ToolType(tc.Type),
			Function: openai.ToolFunction{
				Name: tc.Function.Name,
			},
		}
	}
	return choice
}

// buildCoreChoice 将 go-openai 选择转换为 core.Choice，保留工具调用信息。
func buildCoreChoice(choice openai.ChatCompletionChoice) core.Choice {
	msg := core.Message{
		Role:    choice.Message.Role,
		Content: choice.Message.Content,
		Name:    choice.Message.Name,
	}
	if choice.Message.ToolCallID != "" {
		msg.ToolCallID = choice.Message.ToolCallID
	}
	if len(choice.Message.ToolCalls) > 0 {
		msg.ToolCalls = make([]core.ToolCall, len(choice.Message.ToolCalls))
		for i, tc := range choice.Message.ToolCalls {
			msg.ToolCalls[i] = core.ToolCall{
				ID:   tc.ID,
				Type: string(tc.Type),
				Function: core.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			}
			if tc.Index != nil {
				msg.ToolCalls[i].Index = *tc.Index
			}
		}
	}
	return core.Choice{
		Index:        choice.Index,
		Message:      msg,
		FinishReason: string(choice.FinishReason),
	}
}

// buildStreamDelta 将 go-openai 流式增量转换为 core.Message，保留工具调用信息。
func buildStreamDelta(delta openai.ChatCompletionStreamChoiceDelta) core.Message {
	msg := core.Message{
		Role:    delta.Role,
		Content: delta.Content,
	}
	if len(delta.ToolCalls) > 0 {
		msg.ToolCalls = make([]core.ToolCall, len(delta.ToolCalls))
		for i, tc := range delta.ToolCalls {
			msg.ToolCalls[i] = core.ToolCall{
				ID:   tc.ID,
				Type: string(tc.Type),
				Function: core.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			}
			if tc.Index != nil {
				msg.ToolCalls[i].Index = *tc.Index
			}
		}
	}
	return msg
}

// ChatCompleteStream 执行流式聊天补全请求。
// 通过 channel 逐块返回响应，支持上下文取消。
//
// Param:
//   - ctx: context.Context - 用于超时控制和取消
//   - req: core.ChatCompletionRequest - 聊天补全请求
//
// Return:
//   - <-chan core.StreamCompletionResponse: 接收流式响应的 channel
//   - error: 请求失败时返回错误
func (p *OpenAIProvider) ChatCompleteStream(ctx context.Context, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error) {
	messages := buildOpenAIMessages(req.Messages)

	// 如果设置了系统 Prompt 覆盖，则注入到消息列表头部。
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

	streamReq := openai.ChatCompletionRequest{
		Model:             req.Model,
		Messages:          messages,
		Temperature:       req.Temperature,
		MaxTokens:         req.MaxTokens,
		TopP:              req.TopP,
		PresencePenalty:   req.PresencePenalty,
		FrequencyPenalty:  req.FrequencyPenalty,
		Stop:              req.Stop,
		Seed:              req.Seed,
		ResponseFormat:    respFormat,
		Stream:            true,
		Tools:             buildOpenAITools(req.Tools),
		ToolChoice:        buildOpenAIToolChoice(req.ToolChoice),
		ParallelToolCalls: req.ParallelToolCalls,
	}
	cfg := p.Config()
	if streamReq.Model == "" {
		streamReq.Model = cfg.Model
	}

	stream, err := p.Client.CreateChatCompletionStream(ctx, streamReq)
	if err != nil {
		return nil, fmt.Errorf("%s stream: %w", p.Name(), err)
	}

	ch := make(chan core.StreamCompletionResponse, 16)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(ch)
		defer stream.Close()

		for {
			select {
			case <-ctx.Done():
				ch <- core.StreamCompletionResponse{
					Model: cfg.Model,
					Chunk: core.StreamChunk{FinishReason: "cancelled"},
					Err:   ctx.Err(),
				}
				return
			default:
			}

			response, err := stream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				ch <- core.StreamCompletionResponse{
					Model: cfg.Model,
					Chunk: core.StreamChunk{
						FinishReason: "error",
					},
					Err: fmt.Errorf("%s stream recv: %w", p.Name(), err),
				}
				return
			}

			var usage core.Usage
			if response.Usage != nil {
				usage = core.Usage{
					PromptTokens:     response.Usage.PromptTokens,
					CompletionTokens: response.Usage.CompletionTokens,
					TotalTokens:      response.Usage.TotalTokens,
				}
			}

			for _, choice := range response.Choices {
				select {
				case <-ctx.Done():
					return
				case ch <- core.StreamCompletionResponse{
					ID:    response.ID,
					Model: response.Model,
					Chunk: core.StreamChunk{
						Index: choice.Index,
						Delta: buildStreamDelta(choice.Delta),
						FinishReason: string(choice.FinishReason),
					},
					Usage: usage,
				}:
				}
			}
		}
	}()

	return ch, nil
}

// ChatComplete 执行同步聊天补全请求，带 panic 恢复、熔断器管理和健康指标追踪。
//
// 执行流程：
//  1. 记录开始时间，设置 defer 函数处理 panic 恢复和指标记录
//  2. 将 core.Message 转换为 go-openai 消息格式
//  3. 注入系统 Prompt 覆盖（如果已设置）
//  4. 构建 OpenAI 请求参数
//  5. 调用 OpenAI API
//  6. 如遇 temperature 兼容错误则自动重试（部分模型仅接受 temperature=1）
//  7. 将 go-openai 响应转换为 core.ChatCompletionResponse
//
// Param:
//   - ctx: context.Context - 用于超时控制和取消
//   - req: core.ChatCompletionRequest - 聊天补全请求
//
// Return:
//   - *core.ChatCompletionResponse: 聊天补全响应
//   - error: 请求失败、响应无选项或发生 panic 时返回错误
func (p *OpenAIProvider) ChatComplete(ctx context.Context, req core.ChatCompletionRequest) (result *core.ChatCompletionResponse, err error) {
	start := time.Now()
	defer func() {
		latencyMs := time.Since(start).Milliseconds()
		if r := recover(); r != nil {
			core.GetLogger().Error("provider panic recovered",
				"provider", p.Name(),
				"panic", r)
			p.RecordFailure()
			p.RecordCallResult(latencyMs, fmt.Errorf("panic: %v", r))
			err = fmt.Errorf("provider panic: %v", r)
			return
		}
		if err != nil {
			p.RecordFailure()
			p.RecordCallResult(latencyMs, err)
		} else {
			p.RecordSuccess()
			p.RecordCallResult(latencyMs, nil)
		}
	}()

	messages := buildOpenAIMessages(req.Messages)

	// 如果设置了系统 Prompt 覆盖，则注入到消息列表头部。
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

	streamReq := openai.ChatCompletionRequest{
		Model:             req.Model,
		Messages:          messages,
		Temperature:       req.Temperature,
		MaxTokens:         req.MaxTokens,
		TopP:              req.TopP,
		PresencePenalty:   req.PresencePenalty,
		FrequencyPenalty:  req.FrequencyPenalty,
		Stop:              req.Stop,
		Seed:              req.Seed,
		ResponseFormat:    respFormat,
		Stream:            req.Stream,
		Tools:             buildOpenAITools(req.Tools),
		ToolChoice:        buildOpenAIToolChoice(req.ToolChoice),
		ParallelToolCalls: req.ParallelToolCalls,
	}
	resp, err := p.Client.CreateChatCompletion(ctx, streamReq)
	if err != nil {
		// 兼容性重试：部分提供商（如 Kimi kimi-k2.6）仅接受 temperature=1。
		// 如果错误消息包含 temperature 关键字，则在不设置 temperature 的情况下重试（omitempty 会将其忽略）。
		if isTemperatureError(err) && req.Temperature != 1 {
			streamReq.Temperature = 0 // omitempty will drop it
			resp, err = p.Client.CreateChatCompletion(ctx, streamReq)
		}
		if err != nil {
			return nil, fmt.Errorf("%s chat complete: %w", p.Name(), err)
		}
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("%s chat complete: no choices in response", p.Name())
	}

	result = &core.ChatCompletionResponse{
		ID:      resp.ID,
		Model:   resp.Model,
		Choices: []core.Choice{buildCoreChoice(resp.Choices[0])},
		Usage: core.Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}
	if resp.Created > 0 {
		result.CreatedAt = time.Unix(resp.Created, 0)
	}
	return result, nil
}

// isTemperatureError 检查错误消息中是否包含 "temperature" 关键字。
// 部分模型提供商（如 Kimi kimi-k2.6）仅接受 temperature=1，
// 当返回 temperature 相关错误时，调度器会自动重试并将 temperature 设为 0（omitempty 会将其忽略）。
//
// Param:
//   - err: error - 待检查的错误
//
// Return:
//   - bool: true 表示错误与 temperature 兼容性相关
func isTemperatureError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "temperature")
}

// ListModels 通过 OpenAI 兼容的 /models 端点查询该 Provider 支持的可用模型列表。
// 复用 OpenAIProvider 内部的 Client 实例以实现连接复用。
//
// Param:
//   - ctx: context.Context - 调用上下文
//
// Return:
//   - []core.ModelInfo: 可用模型信息列表
//   - error: 查询失败或配置不完整时返回错误
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

// NewDeepSeekProvider 创建 DeepSeek（深度求索）Provider，并设置合理的默认配置。
// 默认端点：https://api.deepseek.com
// 默认模型：deepseek-v4-pro
// 默认名称：deepseek
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

// NewKimiProvider 创建 Kimi（月之暗面 Moonshot AI）Provider，并设置合理的默认配置。
// 默认端点：https://api.moonshot.cn/v1
// 默认模型：kimi-k2.6
// 默认名称：kimi
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

// NewGLMProvider 创建 GLM（智谱 AI）Provider，并设置合理的默认配置。
// 默认端点：https://open.bigmodel.cn/api/paas/v4
// 默认模型：glm-5.1
// 默认名称：glm
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

// NewQwenProvider 创建 Qwen（阿里巴巴通义千问）Provider，并设置合理的默认配置。
// 默认端点：https://dashscope.aliyuncs.com/compatible-mode/v1
// 默认模型：qwen3.7-max
// 默认名称：qwen
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

// Close 释放 Provider 持有的资源。
// 默认实现为空操作；具体 Provider 可按需覆盖。
//
// Return:
//   - error: 释放资源失败时返回错误
func (b *BaseProvider) Close() error { return nil }

// FailUntil 返回熔断器冷却截止时间。
//
// Return:
//   - time.Time: 冷却截止时间，零值表示未处于冷却期
func (b *BaseProvider) FailUntil() time.Time {
	if until := b.failUntil.Load(); until != 0 {
		return time.Unix(0, until)
	}
	return time.Time{}
}

// SetFailUntil 设置熔断器冷却截止时间（主要用于测试）。
//
// Param:
//   - t: time.Time - 冷却截止时间，零值表示清除冷却期
func (b *BaseProvider) SetFailUntil(t time.Time) {
	if t.IsZero() {
		b.failUntil.Store(0)
	} else {
		b.failUntil.Store(t.UnixNano())
	}
}
