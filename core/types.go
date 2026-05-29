package core

import "time"

// ModelInfo holds basic info about an available model.
type ModelInfo struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by"`
}

// Usage tracks token consumption for a single completion.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Message represents a single message in the chat completion request/response.
type Message struct {
	Role             string `json:"role"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

// ChatCompletionRequest is the unified request type for all providers.
type ChatCompletionRequest struct {
	Model          string         `json:"model"`
	Messages       []Message      `json:"messages"`
	Temperature    float32        `json:"temperature,omitempty"`
	MaxTokens      int            `json:"max_tokens,omitempty"`
	ResponseFormat ResponseFormat `json:"response_format,omitempty"`
	Stream         bool           `json:"stream,omitempty"`
	Tags           []string       `json:"tags,omitempty"` // Optional tags for filtering/categorizing calls in history
}

// ResponseFormat controls the output format (e.g. JSON object).
type ResponseFormat struct {
	Type string `json:"type"`
}

// ChatCompletionResponse is the unified response type for all providers.
type ChatCompletionResponse struct {
	ID        string    `json:"id"`
	Model     string    `json:"model"`
	Choices   []Choice  `json:"choices"`
	Usage     Usage     `json:"usage"`
	CreatedAt time.Time `json:"created_at"`
}

// Choice represents a single completion choice.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// ProviderStatus represents the runtime status of a provider.
type ProviderStatus struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Model     string `json:"model"`
}

// StreamChunk represents a single chunk from an SSE stream.
type StreamChunk struct {
	Index        int     `json:"index"`
	Delta        Message `json:"delta"`
	FinishReason string  `json:"finish_reason,omitempty"`
}

// StreamCompletionResponse is yielded for each chunk during streaming.
type StreamCompletionResponse struct {
	ID    string      `json:"id"`
	Model string      `json:"model"`
	Chunk StreamChunk `json:"chunk"`
	// Err is set when the stream encounters a non-EOF error.
	// When Err is non-nil, the channel will be closed immediately after.
	Err error `json:"-"`
}

// DualResult holds results from dual-provider completion.
type DualResult struct {
	Result1   *ChatCompletionResponse `json:"result1"`
	Result2   *ChatCompletionResponse `json:"result2"`
	Consensus bool                    `json:"consensus"`
}

// AuditResult holds the outcome of an audit pass.
type AuditResult struct {
	Primary   *ChatCompletionResponse `json:"primary"`
	Audit     *ChatCompletionResponse `json:"audit"`
	Consensus bool                    `json:"consensus"`
	Adjusted  *ChatCompletionResponse `json:"adjusted"`
}

// Clone creates a deep copy of the request, including Messages.
func (req ChatCompletionRequest) Clone() ChatCompletionRequest {
	if len(req.Messages) == 0 {
		return req
	}
	cloned := req
	cloned.Messages = make([]Message, len(req.Messages))
	copy(cloned.Messages, req.Messages)
	return cloned
}
