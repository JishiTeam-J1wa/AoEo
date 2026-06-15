// Package privacy 实现隐私网关，提供 PII 检测、伪匿名化和还原能力。
//
// Author: JishiTeam-J1wa
// Created: 2026-06
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package privacy

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/privacy/model"
	"github.com/JishiTeam-J1wa/AoEo/privacy/store"
)

// contextKey 是自定义的上下文键类型，避免使用原始字符串作为键导致冲突。
type contextKey string

const (
	// sessionContextKey 是存储在 context 中的会话标识符的键名。
	sessionContextKey contextKey = "privacy_session_id"
)

// GatewayConfig 配置隐私网关的各项参数。
type GatewayConfig struct {
	// Store 是映射存储后端。如果为 nil，则创建 Pebble 本地进程存储。
	Store store.MappingStore

	// Generator 用于生成伪造值。如果为 nil，则使用默认生成器。
	Generator *FakeGenerator

	// ModelEndpoint 是 AI 隐私过滤 sidecar 的 URL 地址。
	// 支持单个端点或逗号分隔的多端点（用于负载均衡）。
	// 示例: "http://127.0.0.1:8080"
	// 示例: "http://sidecar-1:8080,http://sidecar-2:8080"
	// 如果为空且 Detector 也为 nil，则不执行检测。
	ModelEndpoint string

	// LBStrategy 选择多端点配置下的负载均衡策略。
	// 零值默认为 RoundRobin。
	// 可选值: RoundRobin, Random, LeastLatency。
	LBStrategy model.Strategy

	// Detector 允许直接注入检测器，用于测试或高级场景。
	// 设置后将忽略 ModelEndpoint 和 LBStrategy 配置。
	Detector Detector

	// Policy 定义检测到敏感数据后的默认操作。
	// 对于匿名化网关，通常设置为 ActionPseudonymize。
	Policy Action

	// SessionTTL 定义映射的存活时间。零值表示不自动清理。
	SessionTTL time.Duration

	// FailOpen 为 true 时，如果 sidecar 不可达或返回错误，请求将原样通过。
	// 为 false（默认值）时，错误会被向上传传播并阻止请求。
	FailOpen bool
}

// Gateway 是 AoEo 的隐私拦截器，位于用户和 AI 提供商之间。
// 它在发送请求前将敏感数据透明地替换为伪造值，
// 并在响应返回时还原为原始值。
type Gateway struct {
	pseudonymizer *Pseudonymizer
	sessionTTL    time.Duration
	endpoint      string
	failOpen      bool
	stats         Stats
	modelClient   model.Client // 底层模型客户端（用于健康检查和关闭）
}

// Stats 保存隐私网关的运行时统计信息。
// 所有字段使用 atomic.Int64 确保并发安全的原子操作。
type Stats struct {
	RequestsPseudonymized atomic.Int64 // 已完成匿名化处理的请求数
	RequestsRestored      atomic.Int64 // 已完成还原处理的请求数
	RequestsFailed        atomic.Int64 // 处理失败的请求数
	SpansDetected         atomic.Int64 // 检测到的敏感信息片段总数
}

// NewGateway 创建一个新的隐私网关实例。
// 根据配置初始化存储后端、伪造值生成器、检测器和负载均衡客户端。
//
// Param:
//   - cfg: GatewayConfig - 网关配置，包含存储、生成器、检测器、策略等参数
//
// Return:
//   - *Gateway: 初始化完成的隐私网关实例
//   - error: 存储后端打开失败或配置无效时返回错误
func NewGateway(cfg GatewayConfig) (*Gateway, error) {
	mappingStore := cfg.Store
	if mappingStore == nil {
		var err error
		mappingStore, err = store.OpenPebble("./privacy_maps")
		if err != nil {
			return nil, fmt.Errorf("open default pebble store: %w", err)
		}
	}

	gen := cfg.Generator
	if gen == nil {
		gen = NewFakeGenerator(time.Now().UnixNano())
	}

	var detector Detector
	var mc model.Client
	if cfg.Detector != nil {
		detector = cfg.Detector
	} else if cfg.ModelEndpoint != "" {
		endpoints := splitEndpoints(cfg.ModelEndpoint)
		strategy := cfg.LBStrategy
		if strategy < 0 || strategy > model.LeastLatency {
			strategy = model.RoundRobin
		}
		if len(endpoints) > 1 {
			mc = model.NewLoadBalancedClient(cfg.ModelEndpoint, strategy)
		} else {
			mc = model.NewHTTPClient(endpoints[0])
		}
		detector = newModelDetectorAdapter(mc)
	} else {
		// 未配置检测源，创建空操作检测器
		detector = &noopDetector{}
	}

	return &Gateway{
		pseudonymizer: NewPseudonymizer(mappingStore, gen, detector),
		sessionTTL:    cfg.SessionTTL,
		endpoint:      cfg.ModelEndpoint,
		failOpen:      cfg.FailOpen,
		modelClient:   mc,
	}, nil
}

// Close 释放网关持有的所有资源。
// 包括停止后台健康检查协程和关闭存储后端。
//
// Return:
//   - error: 关闭存储后端失败时返回错误
func (g *Gateway) Close() error {
	// 如果使用 LoadBalancedClient，停止后台健康检查协程
	if g.modelClient != nil {
		if lb, ok := g.modelClient.(*model.LoadBalancedClient); ok {
			lb.Close()
		}
	}
	if g.pseudonymizer != nil && g.pseudonymizer.store != nil {
		return g.pseudonymizer.store.Close()
	}
	return nil
}

// HealthCheck 向 AI sidecar 发送健康检查请求以验证其可达性。
// 如果至少有一个后端返回 HTTP 200，则返回 true。
//
// Param:
//   - ctx: context.Context - 请求上下文，用于控制超时
//
// Return:
//   - bool: 至少一个后端可达时返回 true
func (g *Gateway) HealthCheck(ctx context.Context) bool {
	if g.modelClient != nil {
		ok, err := g.modelClient.HealthCheck(ctx)
		if err != nil {
			core.GetLogger().Warn("privacy sidecar health check failed",
				"endpoint", g.endpoint,
				"error", err,
			)
			return false
		}
		return ok
	}
	if g.endpoint == "" {
		return false
	}
	mc := model.NewHTTPClient(g.endpoint)
	ok, err := mc.HealthCheck(ctx)
	if err != nil {
		core.GetLogger().Warn("privacy sidecar health check failed",
			"endpoint", g.endpoint,
			"error", err,
		)
		return false
	}
	return ok
}

// Stats 返回隐私网关的运行时统计信息快照（指针引用，避免 atomic 值拷贝）。
//
// Return:
//   - *Stats: 统计信息结构指针，包含请求数、检测片段数等原子计数器
func (g *Gateway) Stats() *Stats {
	return &g.stats
}

// BeforeRequest 实现 core.Interceptor 接口。
// 在请求发出之前，将请求中的敏感值替换为伪造的等价值。
// 本次请求创建的映射会通过请求元数据传递给 AfterResponse。
//
// Param:
//   - ctx: context.Context - 请求上下文，可携带会话标识符
//   - req: *core.ChatCompletionRequest - 待处理的聊天请求（会被原地替换）
//
// Return:
//   - error: 处理失败时返回错误；若 FailOpen 为 true 则返回 nil 并记录警告
func (g *Gateway) BeforeRequest(ctx context.Context, req *core.ChatCompletionRequest) error {
	sessionID := extractSessionID(ctx, req)

	newReq, mappings, err := g.pseudonymizer.PseudonymizeRequest(ctx, sessionID, req)
	if err != nil {
		g.stats.RequestsFailed.Add(1)
		core.GetLogger().Error("privacy_before_request failed",
			"session", sessionID,
			"error", err,
		)
		if g.failOpen {
			core.GetLogger().Warn("privacy_fail_open: passing request through despite error", "session", sessionID)
			return nil
		}
		return fmt.Errorf("privacy gateway: %w", err)
	}

	// 将本次请求创建的映射通过请求元数据传递给 AfterResponse，
	// 确保只还原本次请求的映射，而非历史映射。
	if len(mappings) > 0 {
		if newReq.Metadata == nil {
			newReq.Metadata = make(map[string]any)
		}
		newReq.Metadata["privacy_mappings"] = mappings
		g.stats.RequestsPseudonymized.Add(1)
		g.stats.SpansDetected.Add(int64(len(mappings)))
	}

	*req = *newReq
	return nil
}

// AfterResponse 实现 core.Interceptor 接口。
// 将 AI 响应中的伪造值还原为原始值。
// 优先使用 BeforeRequest 阶段传递的精确映射，回退到全量会话映射。
//
// Param:
//   - ctx: context.Context - 请求上下文，可携带会话标识符
//   - req: core.ChatCompletionRequest - 原始聊天请求（含元数据中的映射信息）
//   - resp: *core.ChatCompletionResponse - AI 返回的响应
//   - err: error - 上游处理过程中产生的错误
//
// Return:
//   - *core.ChatCompletionResponse: 还原后的响应
//   - error: 还原失败时返回错误；若 FailOpen 为 true 则返回原始响应
func (g *Gateway) AfterResponse(ctx context.Context, req core.ChatCompletionRequest, resp *core.ChatCompletionResponse, err error) (*core.ChatCompletionResponse, error) {
	if err != nil || resp == nil {
		return resp, err
	}

	sessionID := extractSessionID(ctx, &req)

	// 优先使用 BeforeRequest 阶段通过元数据传递的映射。
	// 这可以防止误还原历史会话中遗留的伪造值。
	if raw, ok := req.Metadata["privacy_mappings"]; ok {
		if mappings, ok := raw.([]core.PrivacyMapping); ok && len(mappings) > 0 {
			restored, rerr := g.pseudonymizer.RestoreResponseWithMappings(ctx, sessionID, resp, mappings)
			if rerr != nil {
				core.GetLogger().Error("privacy_after_response failed",
					"session", sessionID,
					"error", rerr,
				)
				if g.failOpen {
					return resp, nil
				}
				return nil, fmt.Errorf("privacy restore: %w", rerr)
			}
			g.stats.RequestsRestored.Add(1)
			return restored, nil
		}
	}

	// 回退路径：元数据中无映射信息（正常流程中不应发生）
	restored, rerr := g.pseudonymizer.RestoreResponse(ctx, sessionID, resp)
	if rerr != nil {
		core.GetLogger().Error("privacy_after_response failed",
			"session", sessionID,
			"error", rerr,
		)
		if g.failOpen {
			return resp, nil
		}
		return nil, fmt.Errorf("privacy restore: %w", rerr)
	}
	return restored, nil
}

// AfterStreamChunk 实现 core.Interceptor 接口。
// 在流式响应传输过程中实时还原伪造值。
//
// Param:
//   - ctx: context.Context - 请求上下文，可携带会话标识符
//   - req: core.ChatCompletionRequest - 原始聊天请求
//   - chunk: *core.StreamChunk - 当前流式数据块（会被原地替换）
//
// Return:
//   - error: 当前始终返回 nil
func (g *Gateway) AfterStreamChunk(ctx context.Context, req core.ChatCompletionRequest, chunk *core.StreamChunk) error {
	sessionID := extractSessionID(ctx, &req)

	// 将 StreamChunk 包装为 StreamCompletionResponse 以复用匿名化处理器的还原逻辑
	wrapped := &core.StreamCompletionResponse{Chunk: *chunk}
	g.pseudonymizer.RestoreStreamChunk(ctx, sessionID, wrapped)
	*chunk = wrapped.Chunk

	return nil
}

// AfterStreamDone 实现 core.Interceptor 接口。
// 在流式会话结束后执行清理操作（预留接口，未来可用于审计日志、会话清理等）。
//
// Param:
//   - ctx: context.Context - 请求上下文
//   - req: core.ChatCompletionRequest - 原始聊天请求
//   - err: error - 流式传输过程中产生的错误
//
// Return:
//   - error: 当前始终返回 nil
func (g *Gateway) AfterStreamDone(ctx context.Context, req core.ChatCompletionRequest, err error) error {
	// TODO(JishiTeam-J1wa, 2026-06): 实现审计日志记录和会话清理
	return nil
}

// ToInterceptor 将网关转换为 core.Interceptor，用于 AoEo 调度器的选项配置。
//
// Return:
//   - core.Interceptor: 包含所有拦截器回调的聚合结构体
func (g *Gateway) ToInterceptor() core.Interceptor {
	return core.Interceptor{
		BeforeRequest:    g.BeforeRequest,
		AfterResponse:    g.AfterResponse,
		AfterStreamChunk: g.AfterStreamChunk,
		AfterStreamDone:  g.AfterStreamDone,
	}
}

// ---------------------------------------------------------------------------
// 内部辅助函数
// ---------------------------------------------------------------------------

// noopDetector 是空操作检测器，不执行任何敏感信息检测。
// 用于未配置检测源时的默认回退。
type noopDetector struct{}

func (n *noopDetector) Detect(text string) DetectResult {
	return DetectResult{}
}

func (n *noopDetector) DetectBatch(texts []string) []DetectResult {
	return make([]DetectResult, len(texts))
}

// splitEndpoints 将逗号分隔的端点字符串拆分为端点列表。
// 会过滤掉空字符串和仅含空白字符的条目，防止配置中的尾逗号导致无效端点。
func splitEndpoints(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	// 过滤空字符串，避免无效端点进入负载均衡池
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

// extractSessionID 从上下文或请求中提取会话标识符。
// 查找优先级：
//  1. 使用类型安全的 contextKey 从上下文中读取
//  2. 从请求标签中查找 "session:" 前缀的标签
//  3. 回退到 "default"（适用于单用户部署场景）
func extractSessionID(ctx context.Context, req *core.ChatCompletionRequest) string {
	// 优先从上下文中读取（使用类型安全的 contextKey 避免键冲突）
	if v := ctx.Value(sessionContextKey); v != nil {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}

	// 回退到请求标签中查找
	for _, tag := range req.Tags {
		if strings.HasPrefix(tag, "session:") {
			return strings.TrimPrefix(tag, "session:")
		}
	}

	// 默认：使用共享映射的空会话（适用于单用户部署）
	return "default"
}
