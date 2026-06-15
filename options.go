// Package aoeo 客户端选项函数，提供函数式配置 API 构建聊天补全请求。
//
// Author: JishiTeam-J1wa
// Created: 2026-05
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package aoeo

import "github.com/JishiTeam-J1wa/AoEo/core"

// Option 是聊天补全请求的函数式选项类型。
//
// 通过闭包修改 ChatCompletionRequest 的字段，支持链式组合使用。
type Option func(*core.ChatCompletionRequest)

// WithSystemPrompt 设置或替换请求中的系统提示词。
//
// 若已有 system 消息则原地替换内容，否则在消息列表头部插入一条新的 system 消息。
//
// Param:
//   - prompt: string - 系统提示词内容，不可为空
func WithSystemPrompt(prompt string) Option {
	return func(req *core.ChatCompletionRequest) {
		if prompt == "" {
			return // 忽略空 system prompt
		}
		for i := range req.Messages {
			if req.Messages[i].Role == "system" {
				req.Messages[i].Content = prompt
				return
			}
		}
		req.Messages = append([]core.Message{{Role: "system", Content: prompt}}, req.Messages...)
	}
}

// WithTemperature 设置采样温度，控制生成文本的随机性。
//
// Param:
//   - t: float32 - 温度值，有效范围 0~2；值越高生成结果越随机，值越低越确定
func WithTemperature(t float32) Option {
	return func(req *core.ChatCompletionRequest) {
		if t < 0 {
			t = 0
		}
		if t > 2 {
			t = 2
		}
		req.Temperature = t
	}
}

// WithMaxTokens 设置生成补全的最大 Token 数量。
//
// Param:
//   - n: int - 最大 Token 数，须 >= 0；为 0 时使用 Provider 默认值
func WithMaxTokens(n int) Option {
	return func(req *core.ChatCompletionRequest) {
		req.MaxTokens = n
	}
}

// WithJSONResponse 请求模型以 JSON 对象格式输出响应。
//
// 设置 ResponseFormat.Type 为 "json_object"，需确保系统提示词中引导模型输出 JSON。
func WithJSONResponse() Option {
	return func(req *core.ChatCompletionRequest) {
		req.ResponseFormat = core.ResponseFormat{Type: "json_object"}
	}
}

// WithModel 设置请求的目标模型名称。
//
// Param:
//   - model: string - 模型标识符（如 "deepseek-v4-pro"），须与 Provider 支持的模型匹配
func WithModel(model string) Option {
	return func(req *core.ChatCompletionRequest) {
		req.Model = model
	}
}

// WithTopP 设置核采样（nucleus sampling）参数。
//
// Param:
//   - p: float32 - TopP 值，有效范围 0~1；仅从累积概率达到 p 的最小 Token 集合中采样
func WithTopP(p float32) Option {
	return func(req *core.ChatCompletionRequest) {
		if p < 0 {
			p = 0
		}
		if p > 1 {
			p = 1
		}
		req.TopP = p
	}
}

// WithPresencePenalty 设置存在惩罚系数，鼓励模型讨论新话题。
//
// Param:
//   - p: float32 - 惩罚系数，有效范围 -2.0~2.0；正值惩罚已出现的 Token，负值鼓励重复
func WithPresencePenalty(p float32) Option {
	return func(req *core.ChatCompletionRequest) {
		if p < -2 {
			p = -2
		}
		if p > 2 {
			p = 2
		}
		req.PresencePenalty = p
	}
}

// WithFrequencyPenalty 设置频率惩罚系数，降低模型重复用词倾向。
//
// Param:
//   - p: float32 - 惩罚系数，有效范围 -2.0~2.0；正值按出现频率惩罚，负值鼓励高频词
func WithFrequencyPenalty(p float32) Option {
	return func(req *core.ChatCompletionRequest) {
		if p < -2 {
			p = -2
		}
		if p > 2 {
			p = 2
		}
		req.FrequencyPenalty = p
	}
}

// WithStop 设置停止序列，模型生成到任意停止序列时将终止输出。
//
// Param:
//   - stop: []string - 停止序列列表，空切片时不生效；内部会做防御性拷贝
func WithStop(stop []string) Option {
	return func(req *core.ChatCompletionRequest) {
		if len(stop) > 0 {
			copied := make([]string, len(stop))
			copy(copied, stop)
			req.Stop = copied
		}
	}
}

// WithSeed 设置随机种子，用于实现确定性采样（相同输入产生相同输出）。
//
// Param:
//   - seed: int - 种子值，具体行为取决于 Provider 实现
func WithSeed(seed int) Option {
	return func(req *core.ChatCompletionRequest) {
		req.Seed = &seed
	}
}

// WithTools 挂载模型可调用的工具（函数）列表。
//
// Param:
//   - tools: []core.Tool - 工具定义列表，空切片时不生效；内部会做防御性拷贝
func WithTools(tools []core.Tool) Option {
	return func(req *core.ChatCompletionRequest) {
		if len(tools) > 0 {
			copied := make([]core.Tool, len(tools))
			copy(copied, tools)
			req.Tools = copied
		}
	}
}

// WithToolChoice 设置模型使用工具的策略。
//
// Param:
//   - choice: any - 可选值为 "none"、"auto"、"required" 或 ToolChoice 结构体
func WithToolChoice(choice any) Option {
	return func(req *core.ChatCompletionRequest) {
		req.ToolChoice = choice
	}
}

// WithParallelToolCalls 启用或禁用并行工具调用。
//
// Param:
//   - v: bool - true 允许模型在一次响应中并发调用多个工具，false 强制串行调用
func WithParallelToolCalls(v bool) Option {
	return func(req *core.ChatCompletionRequest) {
		req.ParallelToolCalls = v
	}
}

// BuildRequest 从消息列表和选项列表构建聊天补全请求。
//
// 对输入消息切片做防御性拷贝，然后依次应用所有 Option。
//
// Param:
//   - messages: []core.Message - 聊天消息列表，不可为空
//   - opts: ...Option - 可选的请求配置选项
//
// Return:
//   - core.ChatCompletionRequest: 构建完成的请求对象
func BuildRequest(messages []core.Message, opts ...Option) core.ChatCompletionRequest {
	copied := make([]core.Message, len(messages))
	copy(copied, messages)
	req := core.ChatCompletionRequest{
		Messages: copied,
	}
	for _, opt := range opts {
		opt(&req)
	}
	return req
}
