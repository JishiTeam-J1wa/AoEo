// handler_chat.go 实现 POST /v1/chat/completions 同步聊天补全处理器。
//
// 遵循 OpenAI Chat Completions API 协议，接收 JSON 请求并返回兼容格式的响应。
// 当请求中 "stream": true 时，自动委托给 StreamHandler 处理 SSE 流式传输。
//
// Author: JishiTeam-J1wa
// Created: 2026-06
package server

import (
	"encoding/json"
	"io"
	"net/http"
)

// ChatHandler 处理 POST /v1/chat/completions 请求。
//
// 执行流程：
//  1. 读取请求体并通过 ParseOpenAIRequest 解析为 OpenAIRequest
//  2. 若 stream=true，委托给 StreamHandler 处理 SSE 流式传输
//  3. 转换为 core.ChatCompletionRequest 并调用 ChatClient.ChatComplete
//  4. 将响应转换为 OpenAI 兼容 JSON 格式返回
//
// 错误响应遵循 OpenAI 格式：
//
//	{"error": {"message": "...", "type": "server_error", "code": 500}}
func (s *Server) ChatHandler(w http.ResponseWriter, r *http.Request) {
	// 读取请求体
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	defer r.Body.Close()

	// 解析 OpenAI 格式请求
	openAIReq, err := ParseOpenAIRequest(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request JSON: "+err.Error())
		return
	}

	// 流式请求委托给 StreamHandler
	if openAIReq.Stream {
		s.StreamHandler(w, r, openAIReq)
		return
	}

	// 转换为 core 请求并执行补全
	coreReq := openAIReq.ToCoreRequest()
	resp, err := s.Client.ChatComplete(r.Context(), coreReq)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 转换为 OpenAI 兼容响应并写入
	respBytes := CoreResponseToOpenAI(resp)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(respBytes)
}

// writeError 写入 OpenAI 兼容格式的错误响应。
//
// 输出格式：
//
//	{"error": {"message": "...", "type": "server_error", "code": <status>}}
//
// Param:
//   - w: http.ResponseWriter - HTTP 响应写入器
//   - status: int - HTTP 状态码
//   - message: string - 错误信息
func writeError(w http.ResponseWriter, status int, message string) {
	errResp := map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    errorTypeFromStatus(status),
			"code":    status,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(errResp)
}

// errorTypeFromStatus 根据 HTTP 状态码返回 OpenAI 错误类型字符串。
//
// Param:
//   - status: int - HTTP 状态码
//
// Return:
//   - string: 对应的错误类型
func errorTypeFromStatus(status int) string {
	switch {
	case status == http.StatusBadRequest:
		return "invalid_request_error"
	case status == http.StatusUnauthorized:
		return "authentication_error"
	case status == http.StatusNotFound:
		return "not_found_error"
	case status == http.StatusTooManyRequests:
		return "rate_limit_error"
	case status >= 500:
		return "server_error"
	default:
		return "api_error"
	}
}
