// Package core 请求拦截器链，提供请求/响应生命周期的钩子机制。
//
// Author: JishiTeam-J1wa
// Created: 2026-05
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package core

import "context"

// Interceptor 提供请求/响应生命周期的拦截钩子。
//
// 用于实现日志记录、指标采集、限流、请求改写、响应后处理等横切关注点。
// 所有钩子均为可选，nil 函数会被跳过。实现必须是并发安全的。
// 在钩子内执行阻塞 I/O 会阻塞调用方。
type Interceptor struct {
	// BeforeRequest 在请求发送给 Provider 之前调用。
	// 通过指针接收请求，可原地修改。返回非 nil error 将中止请求，
	// 该错误会直接返回给调用方。
	BeforeRequest func(ctx context.Context, req *ChatCompletionRequest) error

	// AfterResponse 在 Provider 返回响应或错误后调用。
	// 接收原始请求、响应（出错时为 nil）和错误（成功时为 nil），
	// 可返回修改后的响应/错误对。
	AfterResponse func(ctx context.Context, req ChatCompletionRequest, resp *ChatCompletionResponse, err error) (*ChatCompletionResponse, error)

	// AfterStreamChunk 在流式响应的每个数据块转发给消费者之前调用。
	// 通过指针接收数据块，可原地修改。返回非 nil error 将中止流，
	// 并向消费者发送错误数据块。
	AfterStreamChunk func(ctx context.Context, req ChatCompletionRequest, chunk *StreamChunk) error

	// AfterStreamDone 在流式响应完成时调用（正常结束或因错误中止）。
	// 接收原始请求和最终错误（正常完成时为 nil）。
	AfterStreamDone func(ctx context.Context, req ChatCompletionRequest, err error) error
}

// InterceptorChain 按顺序执行一组拦截器切片。
//
// 由调度器内部使用，SDK 使用者通常无需直接操作。
type InterceptorChain []Interceptor

// ApplyBefore 按顺序执行所有 BeforeRequest 钩子。
//
// 首个返回错误的钩子会短路中止后续链。
//
// Param:
//   - ctx: context.Context - 请求上下文
//   - req: *ChatCompletionRequest - 待发送的请求（可被钩子修改）
//
// Return:
//   - error: 首个钩子返回的错误，全部成功时为 nil
func (chain InterceptorChain) ApplyBefore(ctx context.Context, req *ChatCompletionRequest) error {
	for _, ic := range chain {
		if ic.BeforeRequest == nil {
			continue
		}
		if err := ic.BeforeRequest(ctx, req); err != nil {
			return err
		}
	}
	return nil
}

// ApplyAfter 按顺序执行所有 AfterResponse 钩子。
//
// 每个钩子可以变换响应和错误，变换结果传递给下一个钩子。
//
// Param:
//   - ctx: context.Context - 请求上下文
//   - req: ChatCompletionRequest - 原始请求
//   - resp: *ChatCompletionResponse - Provider 返回的响应（出错时为 nil）
//   - err: error - Provider 返回的错误（成功时为 nil）
//
// Return:
//   - *ChatCompletionResponse: 经过所有钩子变换后的最终响应
//   - error: 经过所有钩子变换后的最终错误
func (chain InterceptorChain) ApplyAfter(ctx context.Context, req ChatCompletionRequest, resp *ChatCompletionResponse, err error) (*ChatCompletionResponse, error) {
	for _, ic := range chain {
		if ic.AfterResponse == nil {
			continue
		}
		resp, err = ic.AfterResponse(ctx, req, resp, err)
	}
	return resp, err
}

// ApplyAfterStreamChunk 按顺序执行所有 AfterStreamChunk 钩子。
//
// 首个返回错误的钩子会短路中止后续链。
//
// Param:
//   - ctx: context.Context - 请求上下文
//   - req: ChatCompletionRequest - 原始请求
//   - chunk: *StreamChunk - 当前数据块（可被钩子修改）
//
// Return:
//   - error: 首个钩子返回的错误，全部成功时为 nil
func (chain InterceptorChain) ApplyAfterStreamChunk(ctx context.Context, req ChatCompletionRequest, chunk *StreamChunk) error {
	for _, ic := range chain {
		if ic.AfterStreamChunk == nil {
			continue
		}
		if err := ic.AfterStreamChunk(ctx, req, chunk); err != nil {
			return err
		}
	}
	return nil
}

// ApplyAfterStreamDone 按顺序执行所有 AfterStreamDone 钩子。
//
// 每个钩子可以变换最终错误，变换结果传递给下一个钩子。
//
// Param:
//   - ctx: context.Context - 请求上下文
//   - req: ChatCompletionRequest - 原始请求
//   - err: error - 流结束时的错误（正常完成时为 nil）
//
// Return:
//   - error: 经过所有钩子变换后的最终错误
func (chain InterceptorChain) ApplyAfterStreamDone(ctx context.Context, req ChatCompletionRequest, err error) error {
	for _, ic := range chain {
		if ic.AfterStreamDone == nil {
			continue
		}
		if e := ic.AfterStreamDone(ctx, req, err); e != nil {
			err = e
		}
	}
	return err
}
