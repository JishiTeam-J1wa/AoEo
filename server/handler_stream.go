// handler_stream.go 实现 SSE（Server-Sent Events）流式聊天补全处理器。
//
// 当 POST /v1/chat/completions 请求中 stream=true 时，由 ChatHandler 内部调用。
// 遵循 OpenAI Streaming API 协议，逐块推送 JSON 数据并以 "data: [DONE]" 结束。
//
// Author: JishiTeam-J1wa
// Created: 2026-06
package server

import (
	"fmt"
	"net/http"
)

// StreamHandler 处理 SSE 流式聊天补全请求。
//
// 由 ChatHandler 在 stream=true 时内部调用，不直接注册为路由。
//
// SSE 协议：
//  1. 设置 Content-Type: text/event-stream 等 SSE 必要响应头
//  2. 每个数据块以 "data: {json}\n\n" 格式推送
//  3. 流结束时推送 "data: [DONE]\n\n"
//  4. 流式过程中出错时推送错误事件并关闭连接
//
// 使用 CoreStreamChunkToSSE（converter.go）将核心流式数据块转换为 OpenAI 兼容 JSON。
//
// Param:
//   - w: http.ResponseWriter - HTTP 响应写入器
//   - r: *http.Request - 原始 HTTP 请求
//   - openAIReq: *OpenAIRequest - 已解析的 OpenAI 格式请求
func (s *Server) StreamHandler(w http.ResponseWriter, r *http.Request, openAIReq *OpenAIRequest) {
	// 转换为 core 请求
	coreReq := openAIReq.ToCoreRequest()

	// 调用流式补全
	stream, err := s.Client.ChatCompleteStream(r.Context(), coreReq)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stream error: "+err.Error())
		return
	}

	// 设置 SSE 响应头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // 禁用 Nginx 缓冲
	w.WriteHeader(http.StatusOK)

	// 获取 Flusher 用于实时推送
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// 逐块推送流式数据
	for chunk := range stream {
		// 检查流式错误
		if chunk.Err != nil {
			errMsg := fmt.Sprintf(`{"error": {"message": %q, "type": "server_error"}}`, chunk.Err.Error())
			fmt.Fprintf(w, "data: %s\n\n", errMsg)
			flusher.Flush()
			return
		}

		// 使用 converter 将核心数据块转换为 SSE JSON
		// CoreStreamChunkToSSE 返回已包含 "data: {...}\n\n" 格式的字节切片
		sseData := CoreStreamChunkToSSE(chunk.ID, chunk.Model, chunk.Chunk, &chunk.Usage)

		// 写入 SSE 帧（数据已包含完整 SSE 格式）
		w.Write(sseData)
		flusher.Flush()
	}

	// 流结束标记
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}
