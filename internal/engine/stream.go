// stream.go 实现流式聊天补全及 SSE（Server-Sent Events）流解析。
//
// Author: JishiTeam-J1wa
// Created: 2026-05
//
// Changelog:
//   2026-06-12 - 注释体系规范化

package engine

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

// ChatCompleteStream 使用主 Provider 执行流式聊天补全。
// 调用方必须持续从返回的 channel 中读取直到其关闭，或取消提供的 context，
// 否则会导致后台 goroutine 和信号量槽位泄漏。
// 通过检查 chunk.Err 可检测流式传输中的错误。
//
// Param:
//   - ctx: context.Context - 请求上下文，取消后终止流式传输并释放资源
//   - req: core.ChatCompletionRequest - 聊天补全请求
//
// Return:
//   - <-chan core.StreamCompletionResponse: 流式响应通道，读取完毕或 ctx 取消后自动关闭
//   - error: 调度器已关闭、无可用 Provider 或信号量获取失败时返回错误
//
// Edge Cases:
//   - 信号量获取失败时返回 error
//   - 无可用 Provider 时返回 ErrNoAvailableProvider
//   - Provider 流式调用失败时通过通道的 Err 字段传递错误
func (s *Scheduler) ChatCompleteStream(ctx context.Context, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	if err := s.sem.Acquire(ctx); err != nil {
		return nil, err
	}
	p := s.PickPrimaryProvider()
	if p == nil {
		s.sem.Release()
		return nil, ErrNoAvailableProvider
	}

	reqCopy := req.Clone()
	if reqCopy.Model == "" {
		reqCopy.Model = p.Config().Model
	}

	if pi := s.promptInjector.Load(); pi != nil {
		pi.Inject(p.Name(), reqCopy.Model, &reqCopy)
	}

	chain := s.interceptorChain()
	if err := chain.ApplyBefore(ctx, &reqCopy); err != nil {
		s.sem.Release()
		return nil, err
	}

	streamCtx, cancel := context.WithTimeout(ctx, time.Duration(s.timeout.Load()))
	stream, err := p.ChatCompleteStream(streamCtx, reqCopy)
	if err != nil {
		cancel()
		s.sem.Release()
		return nil, err
	}

	// 包装流通道：在消费完成后释放信号量槽位。
	// 使用固定大小缓冲区（wrappedBufSize=16）解耦生产者和消费者，
	// 防止消费者读取速度慢于生产者时导致 goroutine 泄漏。
	const wrappedBufSize = 16
	wrapped := make(chan core.StreamCompletionResponse, wrappedBufSize)
	go func() {
		defer close(wrapped)
		defer s.sem.Release()
		defer cancel()
		var finalErr error
		defer func() {
			if err := chain.ApplyAfterStreamDone(streamCtx, reqCopy, finalErr); err != nil {
				core.GetLogger().Debug("AfterStreamDone error", "error", err)
			}
		}()
		for {
			select {
			case <-streamCtx.Done():
				finalErr = streamCtx.Err()
				return
			case msg, ok := <-stream:
				if !ok {
					return
				}
				if msg.Err != nil {
					finalErr = msg.Err
					select {
					case <-streamCtx.Done():
						return
					case wrapped <- msg:
					}
					return
				}
				if err := chain.ApplyAfterStreamChunk(streamCtx, reqCopy, &msg.Chunk); err != nil {
					finalErr = err
					select {
					case <-streamCtx.Done():
						return
					case wrapped <- core.StreamCompletionResponse{Err: err}:
					}
					return
				}
				select {
				case <-streamCtx.Done():
					return
				case wrapped <- msg:
				}
			}
		}
	}()
	return wrapped, nil
}

// chatCompleteStreamWithRouter 使用指定路由器执行流式补全，不修改全局路由状态。
func (s *Scheduler) chatCompleteStreamWithRouter(ctx context.Context, r core.Router, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	if err := s.sem.Acquire(ctx); err != nil {
		return nil, err
	}
	p, err := s.pickWithSpecificRouter(ctx, r, req)
	if err != nil {
		s.sem.Release()
		return nil, err
	}

	reqCopy := req.Clone()
	if reqCopy.Model == "" {
		reqCopy.Model = p.Config().Model
	}

	if pi := s.promptInjector.Load(); pi != nil {
		pi.Inject(p.Name(), reqCopy.Model, &reqCopy)
	}

	chain := s.interceptorChain()
	if err := chain.ApplyBefore(ctx, &reqCopy); err != nil {
		s.sem.Release()
		return nil, err
	}

	streamCtx, cancel := context.WithTimeout(ctx, time.Duration(s.timeout.Load()))
	stream, err := p.ChatCompleteStream(streamCtx, reqCopy)
	if err != nil {
		cancel()
		s.sem.Release()
		return nil, err
	}

	const wrappedBufSize = 16
	wrapped := make(chan core.StreamCompletionResponse, wrappedBufSize)
	go func() {
		defer close(wrapped)
		defer s.sem.Release()
		defer cancel()
		var finalErr error
		defer func() {
			if err := chain.ApplyAfterStreamDone(streamCtx, reqCopy, finalErr); err != nil {
				core.GetLogger().Debug("AfterStreamDone error", "error", err)
			}
		}()
		for {
			select {
			case <-streamCtx.Done():
				finalErr = streamCtx.Err()
				return
			case msg, ok := <-stream:
				if !ok {
					return
				}
				if msg.Err != nil {
					finalErr = msg.Err
					select {
					case <-streamCtx.Done():
						return
					case wrapped <- msg:
					}
					return
				}
				if err := chain.ApplyAfterStreamChunk(streamCtx, reqCopy, &msg.Chunk); err != nil {
					finalErr = err
					select {
					case <-streamCtx.Done():
						return
					case wrapped <- core.StreamCompletionResponse{Err: err}:
					}
					return
				}
				select {
				case <-streamCtx.Done():
					return
				case wrapped <- msg:
				}
			}
		}
	}()
	return wrapped, nil
}

// ChatCompleteStreamWithRouter 使用指定路由器执行流式补全，线程安全。
func (s *Scheduler) ChatCompleteStreamWithRouter(ctx context.Context, r core.Router, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error) {
	return s.chatCompleteStreamWithRouter(ctx, r, req)
}

// ParseSSE 解析原始 SSE（Server-Sent Events）数据流，将其转换为 StreamChunk 通道。
// 适用于调试或代理 SSE 流的场景。
//
// Param:
//   - r: io.Reader - SSE 数据流（通常为 HTTP 响应体）
//
// Return:
//   - <-chan core.StreamChunk: 解析后的数据块通道，流结束或出错后自动关闭
//
// Edge Cases:
//   - 遇到 "[DONE]" 标记时正常关闭通道
//   - 读取流发生错误时发送包含错误信息的 StreamChunk 后关闭
func ParseSSE(r io.Reader) <-chan core.StreamChunk {
	return ParseSSEWithContext(context.Background(), r)
}

// ParseSSEWithContext 解析原始 SSE（Server-Sent Events）数据流，支持通过 context 取消解析。
// 适用于调试或代理 SSE 流的场景。
//
// Param:
//   - ctx: context.Context - 控制解析生命周期，取消后停止解析并关闭通道
//   - r: io.Reader - SSE 数据流（通常为 HTTP 响应体）
//
// Return:
//   - <-chan core.StreamChunk: 解析后的数据块通道，流结束、出错或 ctx 取消后自动关闭
//
// Edge Cases:
//   - 遇到 "[DONE]" 标记时正常关闭通道
//   - 读取流发生错误时发送包含错误信息的 StreamChunk 后关闭
//   - ctx 取消时立即停止解析并关闭通道
func ParseSSEWithContext(ctx context.Context, r io.Reader) <-chan core.StreamChunk {
	ch := make(chan core.StreamChunk, 8)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			if ctx.Err() != nil {
				return
			}
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				return
			}
			// 最简解析，用户可自行通过 json.Unmarshal 扩展
			select {
			case <-ctx.Done():
				return
			case ch <- core.StreamChunk{
				Delta: core.Message{Content: data},
			}:
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case <-ctx.Done():
			case ch <- core.StreamChunk{
				Delta: core.Message{Content: fmt.Sprintf("[SSE parse error: %v]", err)},
			}:
			}
		}
	}()
	return ch
}
