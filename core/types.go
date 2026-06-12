package core

import (
	"fmt"
	"time"
)

// ModelInfo 保存可用模型的基本信息。
type ModelInfo struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by"`
}

// Usage 追踪单次补全请求的 Token 消耗情况。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// FunctionDefinition 描述模型可调用的函数定义。
type FunctionDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"` // json.RawMessage or a struct
	Strict      bool   `json:"strict,omitempty"`
}

// Tool 表示模型可以使用的工具（目前仅支持函数类型）。
type Tool struct {
	Type     string              `json:"type"` // "function"
	Function *FunctionDefinition `json:"function,omitempty"`
}

// ToolChoice 控制模型如何使用工具。
// 可选值："none" | "auto" | "required" | "function"。
type ToolChoice struct {
	Type     string `json:"type"` // "none" | "auto" | "required" | "function"
	Function struct {
		Name string `json:"name"`
	} `json:"function,omitempty"`
}

// FunctionCall 表示模型生成的函数调用，包含函数名和参数（JSON 字符串）。
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ToolCall 表示模型生成的工具调用（目前为函数调用）。
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
	Index    int          `json:"index,omitempty"`
}

// Message 表示聊天补全请求/响应中的单条消息。
// 支持 system/user/assistant/tool 等角色，以及工具调用和推理内容。
type Message struct {
	Role             string     `json:"role"`
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	Name             string     `json:"name,omitempty"`         // function name for tool results
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`   // assistant messages with tool calls
	ToolCallID       string     `json:"tool_call_id,omitempty"` // tool result messages
}

// ChatCompletionRequest 是所有 Provider 统一使用的聊天补全请求类型。
// 涵盖模型参数（温度、TopP 等）、工具调用、标签和拦截器元数据等字段。
type ChatCompletionRequest struct {
	Model             string         `json:"model"`
	Messages          []Message      `json:"messages"`
	Temperature       float32        `json:"temperature,omitempty"`
	MaxTokens         int            `json:"max_tokens,omitempty"`
	TopP              float32        `json:"top_p,omitempty"`
	PresencePenalty   float32        `json:"presence_penalty,omitempty"`
	FrequencyPenalty  float32        `json:"frequency_penalty,omitempty"`
	Stop              []string       `json:"stop,omitempty"`
	Seed              *int           `json:"seed,omitempty"`
	ResponseFormat    ResponseFormat `json:"response_format,omitempty"`
	Stream            bool           `json:"stream,omitempty"`
	Tags              []string       `json:"tags,omitempty"` // 可选标签，用于在历史记录中按标签过滤/分类
	Tools             []Tool         `json:"tools,omitempty"`
	ToolChoice        any            `json:"tool_choice,omitempty"` // "none" | "auto" | "required" 或 ToolChoice
	ParallelToolCalls bool           `json:"parallel_tool_calls,omitempty"`

	// Metadata 是内存中的映射，供拦截器在 BeforeRequest 和 AfterResponse
	// 钩子之间传递数据。该字段不会被序列化为 JSON。
	Metadata map[string]any `json:"-"`
}

// Validate 校验请求中的常见配置错误。
// 返回错误信息切片，空切片表示请求有效。
func (req ChatCompletionRequest) Validate() []string {
	var issues []string
	if len(req.Messages) == 0 {
		issues = append(issues, "messages cannot be empty")
	}
	for i, m := range req.Messages {
		if m.Role == "" {
			issues = append(issues, fmt.Sprintf("message[%d]: role is required", i))
		}
		if m.Content == "" && m.Role != "assistant" && m.Role != "tool" {
			// assistant 消息可以为空（例如工具调用），tool 消息使用 tool_call_id
			issues = append(issues, fmt.Sprintf("message[%d]: content is required", i))
		}
	}
	if req.Temperature < 0 || req.Temperature > 2 {
		issues = append(issues, "temperature must be between 0 and 2")
	}
	if req.TopP < 0 || req.TopP > 1 {
		issues = append(issues, "top_p must be between 0 and 1")
	}
	if req.MaxTokens < 0 {
		issues = append(issues, "max_tokens must be >= 0")
	}
	return issues
}

// ResponseFormat 控制模型的输出格式（例如 JSON 对象）。
type ResponseFormat struct {
	Type string `json:"type"`
}

// ChatCompletionResponse 是所有 Provider 统一使用的聊天补全响应类型。
// 包含响应 ID、模型名称、补全结果列表、Token 用量和时间戳。
type ChatCompletionResponse struct {
	ID        string    `json:"id"`
	Model     string    `json:"model"`
	Choices   []Choice  `json:"choices"`
	Usage     Usage     `json:"usage"`
	CreatedAt time.Time `json:"created_at"`
}

// Content 返回第一个补全结果的文本内容。
// 这是一个安全的便捷访问器，避免索引越界 panic。
func (r *ChatCompletionResponse) Content() string {
	if r == nil || len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].Message.Content
}

// Choice 表示一个补全结果选项，包含消息内容、索引和结束原因。
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// ProviderHealth 表示 Provider 的运行时健康指标。
type ProviderHealth struct {
	LastCheckAt      time.Time `json:"last_check_at"`
	LastLatencyMs    int64     `json:"last_latency_ms"`
	AvgLatencyMs     int64     `json:"avg_latency_ms"`
	SuccessRate      float64   `json:"success_rate"`       // 0.0~1.0，近期窗口
	ConsecutiveFails int       `json:"consecutive_fails"`
	TotalChecks      int       `json:"total_checks"`
}

// ProviderStatus 表示 Provider 的运行时状态，包括名称、可用性、模型和健康指标。
type ProviderStatus struct {
	Name      string         `json:"name"`
	Available bool           `json:"available"`
	Model     string         `json:"model"`
	Health    ProviderHealth `json:"health,omitempty"`
}

// StreamChunk 表示 SSE 流式传输中的单个数据块。
type StreamChunk struct {
	Index        int     `json:"index"`
	Delta        Message `json:"delta"`
	FinishReason string  `json:"finish_reason,omitempty"`
}

// StreamCompletionResponse 在流式传输过程中为每个数据块生成。
type StreamCompletionResponse struct {
	ID    string      `json:"id"`
	Model string      `json:"model"`
	Chunk StreamChunk `json:"chunk"`
	// Usage 仅在最后一个数据块中设置（当 Provider 支持时，
	// 例如 OpenAI 的 stream_options: {"include_usage": true}）。
	Usage Usage `json:"usage,omitempty"`
	// Err 在流式传输遇到非 EOF 错误时设置。
	// 当 Err 非空时，channel 将立即关闭。
	Err error `json:"-"`
}

// DualResult 保存双 Provider 补全的结果，包括两份响应和一致性标记。
type DualResult struct {
	Result1   *ChatCompletionResponse `json:"result1"`
	Result2   *ChatCompletionResponse `json:"result2"`
	Consensus bool                    `json:"consensus"`
}

// AuditResult 保存审计流程的结果，包括主响应、审计响应、一致性标记和调整后的响应。
type AuditResult struct {
	Primary   *ChatCompletionResponse `json:"primary"`
	Audit     *ChatCompletionResponse `json:"audit"`
	Consensus bool                    `json:"consensus"`
	Adjusted  *ChatCompletionResponse `json:"adjusted"`
}

// Clone 创建请求的深拷贝，包括 Messages、Tags、Tools 和 Metadata。
// Function 指针会被独立拷贝，确保克隆体与原始对象互不影响。
func (req ChatCompletionRequest) Clone() ChatCompletionRequest {
	cloned := req
	if len(req.Messages) > 0 {
		cloned.Messages = make([]Message, len(req.Messages))
		for i, m := range req.Messages {
			cloned.Messages[i] = m
			if len(m.ToolCalls) > 0 {
				cloned.Messages[i].ToolCalls = make([]ToolCall, len(m.ToolCalls))
				copy(cloned.Messages[i].ToolCalls, m.ToolCalls)
			}
		}
	}
	if len(req.Tags) > 0 {
		cloned.Tags = make([]string, len(req.Tags))
		copy(cloned.Tags, req.Tags)
	}
	if len(req.Tools) > 0 {
		cloned.Tools = make([]Tool, len(req.Tools))
		for i, t := range req.Tools {
			cloned.Tools[i] = t
			// 深拷贝 Function 指针，避免克隆体与原始对象共享同一份定义
			if t.Function != nil {
				f := *t.Function
				cloned.Tools[i].Function = &f
			}
		}
	}
	if len(req.Metadata) > 0 {
		cloned.Metadata = make(map[string]any, len(req.Metadata))
		for k, v := range req.Metadata {
			cloned.Metadata[k] = v
		}
	}
	return cloned
}
