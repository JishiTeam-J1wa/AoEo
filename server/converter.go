// Package server 实现 AoEo 的 HTTP 服务端，包括 OpenAI 兼容 API 网关、
// 健康检查、指标导出和中间件链。
//
// converter.go 负责 OpenAI 兼容 JSON 格式与 core 内部类型之间的双向转换。
//
// Author: JishiTeam-J1wa
// Created: 2026-06
package server

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

// OpenAIRequest 是 OpenAI 兼容的 JSON 请求体。
// 字段定义与标准 OpenAI Chat Completion API 保持一致。
type OpenAIRequest struct {
	Model             string          `json:"model"`
	Messages          []OpenAIMessage `json:"messages"`
	Temperature       float32         `json:"temperature,omitempty"`
	MaxTokens         int             `json:"max_tokens,omitempty"`
	TopP              float32         `json:"top_p,omitempty"`
	PresencePenalty   float32         `json:"presence_penalty,omitempty"`
	FrequencyPenalty  float32         `json:"frequency_penalty,omitempty"`
	Stop              json.RawMessage `json:"stop,omitempty"` // string 或 []string
	Seed              *int            `json:"seed,omitempty"`
	ResponseFormat    *OpenAIRespFmt  `json:"response_format,omitempty"`
	Stream            bool            `json:"stream,omitempty"`
	StreamOptions     *StreamOptions  `json:"stream_options,omitempty"`
	Tools             []OpenAITool    `json:"tools,omitempty"`
	ToolChoice        json.RawMessage `json:"tool_choice,omitempty"` // string 或 object
	ParallelToolCalls bool            `json:"parallel_tool_calls,omitempty"`
}

// OpenAIMessage 表示 OpenAI 格式的单条聊天消息。
// Content 可以是字符串或 null（工具调用场景下 assistant 消息的 content 为 null）。
type OpenAIMessage struct {
	Role       string           `json:"role"`
	Content    json.RawMessage  `json:"content"` // string 或 null
	Name       string           `json:"name,omitempty"`
	ToolCalls  []OpenAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

// OpenAIRespFmt 控制模型的输出格式（例如 text 或 json_object）。
type OpenAIRespFmt struct {
	Type string `json:"type"`
}

// StreamOptions 控制流式传输的附加选项。
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// OpenAITool 描述模型可使用的工具定义（目前仅支持 function 类型）。
type OpenAITool struct {
	Type     string             `json:"type"`
	Function *OpenAIFunctionDef `json:"function,omitempty"`
}

// OpenAIFunctionDef 描述函数的名称、说明和参数 Schema。
type OpenAIFunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      bool            `json:"strict,omitempty"`
}

// OpenAIToolCall 表示模型生成的工具调用。
type OpenAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function OpenAIFunctionCall `json:"function"`
	Index    int                `json:"index,omitempty"`
}

// OpenAIFunctionCall 保存模型生成的函数名和参数 JSON 字符串。
type OpenAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ParseOpenAIRequest 将原始 JSON 字节解析为 OpenAIRequest。
//
// Parameters:
//   - body: 原始 JSON 请求体字节
//
// Return:
//   - *OpenAIRequest: 解析后的请求指针
//   - error: JSON 解析错误（如有）
func ParseOpenAIRequest(body []byte) (*OpenAIRequest, error) {
	var req OpenAIRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("解析 OpenAI 请求失败: %w", err)
	}
	return &req, nil
}

// ToCoreRequest 将 OpenAI 兼容请求转换为 core 内部类型。
//
// 转换规则：
//   - Stop: JSON 中可以是 string 或 []string，统一转为 []string
//   - Content: JSON 中可以是 string 或 null，null 时转为空字符串
//   - ToolChoice: JSON 中可以是 string 或 object，直接传递为 any
//   - ResponseFormat: 做 nil 安全处理
//
// Return:
//   - core.ChatCompletionRequest: 转换后的内部请求类型
func (r *OpenAIRequest) ToCoreRequest() core.ChatCompletionRequest {
	// 转换消息列表
	messages := make([]core.Message, len(r.Messages))
	for i, m := range r.Messages {
		messages[i] = convertMessage(m)
	}

	// 转换 Stop 字段：支持 string 或 []string
	stop := convertStop(r.Stop)

	// 转换 ToolChoice 字段：支持 string 或 object
	toolChoice := convertToolChoice(r.ToolChoice)

	// 转换工具定义
	tools := make([]core.Tool, len(r.Tools))
	for i, t := range r.Tools {
		tools[i] = convertTool(t)
	}

	// 转换 ResponseFormat（nil 安全）
	var respFmt core.ResponseFormat
	if r.ResponseFormat != nil {
		respFmt.Type = r.ResponseFormat.Type
	}

	return core.ChatCompletionRequest{
		Model:             r.Model,
		Messages:          messages,
		Temperature:       r.Temperature,
		MaxTokens:         r.MaxTokens,
		TopP:              r.TopP,
		PresencePenalty:   r.PresencePenalty,
		FrequencyPenalty:  r.FrequencyPenalty,
		Stop:              stop,
		Seed:              r.Seed,
		ResponseFormat:    respFmt,
		Stream:            r.Stream,
		Tools:             tools,
		ToolChoice:        toolChoice,
		ParallelToolCalls: r.ParallelToolCalls,
	}
}

// convertMessage 将 OpenAI 消息转换为 core.Message。
// Content 字段在 JSON 中可以是 string 或 null，这里统一处理。
func convertMessage(m OpenAIMessage) core.Message {
	// 解析 Content：可以是 string 或 null
	var content string
	if m.Content != nil && string(m.Content) != "null" {
		// 尝试解析为字符串
		var s string
		if err := json.Unmarshal(m.Content, &s); err == nil {
			content = s
		}
	}

	// 转换工具调用列表
	var toolCalls []core.ToolCall
	for _, tc := range m.ToolCalls {
		toolCalls = append(toolCalls, core.ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: core.FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
			Index: tc.Index,
		})
	}

	return core.Message{
		Role:       m.Role,
		Content:    content,
		Name:       m.Name,
		ToolCalls:  toolCalls,
		ToolCallID: m.ToolCallID,
	}
}

// convertStop 将 JSON 中的 Stop 字段（string 或 []string）统一转为 []string。
func convertStop(raw json.RawMessage) []string {
	if raw == nil || string(raw) == "null" {
		return nil
	}

	// 尝试解析为单个字符串
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}
	}

	// 尝试解析为字符串数组
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}

	return nil
}

// convertToolChoice 将 JSON 中的 ToolChoice 字段（string 或 object）转为 any。
// OpenAI 规范中 ToolChoice 可以是 "none"、"auto"、"required" 或具体的函数对象。
func convertToolChoice(raw json.RawMessage) any {
	if raw == nil || string(raw) == "null" {
		return nil
	}

	// 尝试解析为字符串（"none" / "auto" / "required"）
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	// 尝试解析为对象（{"type": "function", "function": {"name": "..."}}）
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj
	}

	return nil
}

// convertTool 将 OpenAI 工具定义转换为 core.Tool。
func convertTool(t OpenAITool) core.Tool {
	var fn *core.FunctionDefinition
	if t.Function != nil {
		// Parameters 在 JSON 中是 RawMessage，在 core 中是 any
		// 尝试解析为 map[string]any 以便后续使用
		var params any
		if t.Function.Parameters != nil {
			var p map[string]any
			if err := json.Unmarshal(t.Function.Parameters, &p); err == nil {
				params = p
			}
		}

		fn = &core.FunctionDefinition{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  params,
			Strict:      t.Function.Strict,
		}
	}

	return core.Tool{
		Type:     t.Type,
		Function: fn,
	}
}

// CoreResponseToOpenAI 将 core 内部响应转换为 OpenAI 兼容的 JSON 字节。
//
// 输出格式示例：
//
//	{
//	  "id": "chatcmpl-xxx",
//	  "object": "chat.completion",
//	  "created": 1234567890,
//	  "model": "gpt-4",
//	  "choices": [{"index": 0, "message": {"role": "assistant", "content": "..."}, "finish_reason": "stop"}],
//	  "usage": {"prompt_tokens": 10, "completion_tokens": 20, "total_tokens": 30}
//	}
//
// Parameters:
//   - resp: core 内部响应指针
//
// Return:
//   - []byte: OpenAI 兼容的 JSON 字节
func CoreResponseToOpenAI(resp *core.ChatCompletionResponse) []byte {
	// 构建 choices 数组
	choices := make([]map[string]any, len(resp.Choices))
	for i, c := range resp.Choices {
		msg := map[string]any{
			"role":    c.Message.Role,
			"content": c.Message.Content,
		}
		// 如果有工具调用，添加到消息中
		if len(c.Message.ToolCalls) > 0 {
			toolCalls := make([]map[string]any, len(c.Message.ToolCalls))
			for j, tc := range c.Message.ToolCalls {
				toolCalls[j] = map[string]any{
					"id":   tc.ID,
					"type": tc.Type,
					"function": map[string]any{
						"name":      tc.Function.Name,
						"arguments": tc.Function.Arguments,
					},
				}
			}
			msg["tool_calls"] = toolCalls
		}

		choices[i] = map[string]any{
			"index":         c.Index,
			"message":       msg,
			"finish_reason": c.FinishReason,
		}
	}

	// 构建完整的响应对象
	result := map[string]any{
		"id":      resp.ID,
		"object":  "chat.completion",
		"created": resp.CreatedAt.Unix(),
		"model":   resp.Model,
		"choices": choices,
		"usage": map[string]any{
			"prompt_tokens":     resp.Usage.PromptTokens,
			"completion_tokens": resp.Usage.CompletionTokens,
			"total_tokens":      resp.Usage.TotalTokens,
		},
	}

	// 序列化为 JSON（忽略错误，因为结构是可控的）
	data, _ := json.Marshal(result)
	return data
}

// CoreStreamChunkToSSE 将 core 流式块转换为 OpenAI 兼容的 SSE 格式。
//
// 输出格式示例：
//
//	data: {"id":"...","object":"chat.completion.chunk","created":1234567890,"model":"...","choices":[{"index":0,"delta":{"content":"..."},"finish_reason":null}]}
//
// 当 usage 非空时（最后一个块），会包含 usage 字段。
//
// Parameters:
//   - id: 请求 ID
//   - model: 模型名称
//   - chunk: 流式数据块
//   - usage: Token 用量（仅在最后一个块中非空）
//
// Return:
//   - []byte: SSE 格式的字节（包含 "data: " 前缀和双换行后缀）
func CoreStreamChunkToSSE(id, model string, chunk core.StreamChunk, usage *core.Usage) []byte {
	// 构建 delta 对象
	delta := map[string]any{
		"role":    chunk.Delta.Role,
		"content": chunk.Delta.Content,
	}

	// 如果有工具调用，添加到 delta 中
	if len(chunk.Delta.ToolCalls) > 0 {
		toolCalls := make([]map[string]any, len(chunk.Delta.ToolCalls))
		for i, tc := range chunk.Delta.ToolCalls {
			toolCalls[i] = map[string]any{
				"id":    tc.ID,
				"type":  tc.Type,
				"index": tc.Index,
				"function": map[string]any{
					"name":      tc.Function.Name,
					"arguments": tc.Function.Arguments,
				},
			}
		}
		delta["tool_calls"] = toolCalls
	}

	// finish_reason 在流式传输中通常为 null，仅在最后一个块中为 "stop" 等值
	finishReason := any(nil)
	if chunk.FinishReason != "" {
		finishReason = chunk.FinishReason
	}

	// 构建 choices 数组
	choices := []map[string]any{
		{
			"index":         chunk.Index,
			"delta":         delta,
			"finish_reason": finishReason,
		},
	}

	// 构建完整的块对象
	result := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": choices,
	}

	// 如果是最后一个块且包含 usage，添加到响应中
	if usage != nil {
		result["usage"] = map[string]any{
			"prompt_tokens":     usage.PromptTokens,
			"completion_tokens": usage.CompletionTokens,
			"total_tokens":      usage.TotalTokens,
		}
	}

	// 序列化为 JSON
	data, _ := json.Marshal(result)

	// 格式化为 SSE: "data: {...}\n\n"
	return []byte(fmt.Sprintf("data: %s\n\n", data))
}
