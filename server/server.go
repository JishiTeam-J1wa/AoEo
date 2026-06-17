// Package server AoEo Gateway HTTP 服务层，提供 OpenAI 兼容的 REST API。
//
// 包含聊天补全（同步/流式）、模型列表、健康检查、指标暴露和管理接口。
// 所有处理器通过 Server 结构体绑定 ChatClient 接口，与底层 SDK 解耦。
//
// Author: JishiTeam-J1wa
// Created: 2026-06
package server

import (
	"context"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/internal/engine"
)

// ChatClient AoEo SDK 客户端接口，抽象 HTTP 服务层所需的客户端方法。
//
// 定义该接口使得处理器可以与任何实现了这些方法的类型协作，
// 包括 *aoeo.Client 和测试用的 Mock 实现。
type ChatClient interface {
	// ChatComplete 使用主 Provider 执行单次聊天补全请求。
	ChatComplete(ctx context.Context, req core.ChatCompletionRequest) (*core.ChatCompletionResponse, error)

	// ChatCompleteStream 使用主 Provider 执行流式聊天补全，通过 channel 逐步返回数据块。
	ChatCompleteStream(ctx context.Context, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error)

	// ChatCompleteWithProvider 向指定名称的 Provider 发送聊天补全请求。
	ChatCompleteWithProvider(ctx context.Context, name string, req core.ChatCompletionRequest) (*core.ChatCompletionResponse, error)

	// ChatCompleteStreamWithProvider 向指定名称的 Provider 发送流式聊天补全请求。
	ChatCompleteStreamWithProvider(ctx context.Context, name string, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error)

	// ChatCompleteWithFallback 先尝试主 Provider，失败后按路由顺序降级到备用 Provider。
	ChatCompleteWithFallback(ctx context.Context, req core.ChatCompletionRequest) (*core.ChatCompletionResponse, error)

	// ListModels 返回指定 Provider 支持的模型列表。
	ListModels(ctx context.Context, providerName string) ([]core.ModelInfo, error)

	// ProviderStatus 返回所有 Provider 的当前运行状态。
	ProviderStatus() []core.ProviderStatus

	// Stats 返回按 Provider 聚合的统计信息。
	Stats() map[string]engine.ProviderStats

	// TestProvider 测试与指定 Provider 的网络连通性。
	TestProvider(ctx context.Context, name string) error

	// Close 优雅关闭客户端，释放所有资源。
	Close() error
}

// Server 持有 HTTP 服务器的依赖项，所有处理器方法均绑定到该结构体。
type Server struct {
	// Client 是 AoEo SDK 客户端实例，提供聊天补全、模型列表等核心能力。
	Client ChatClient
}

// NewServer 创建一个新的 Server 实例。
//
// Param:
//   - client: ChatClient - AoEo SDK 客户端（通常为 *aoeo.Client）
//
// Return:
//   - *Server: 新创建的服务器实例
func NewServer(client ChatClient) *Server {
	return &Server{Client: client}
}
