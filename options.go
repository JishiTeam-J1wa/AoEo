package aoeo

// Option is a functional option for configuring chat completion requests.
type Option func(*ChatCompletionRequest)

// WithSystemPrompt sets or replaces the system prompt for the request.
// If a system message already exists, it is replaced; otherwise one is prepended.
func WithSystemPrompt(prompt string) Option {
	return func(req *ChatCompletionRequest) {
		// Find existing system message.
		for i := range req.Messages {
			if req.Messages[i].Role == "system" {
				req.Messages[i].Content = prompt
				return
			}
		}
		// Prepend a new system message.
		req.Messages = append([]Message{{Role: "system", Content: prompt}}, req.Messages...)
	}
}

// WithTemperature sets the sampling temperature.
func WithTemperature(t float32) Option {
	return func(req *ChatCompletionRequest) {
		req.Temperature = t
	}
}

// WithMaxTokens sets the maximum number of tokens to generate.
func WithMaxTokens(n int) Option {
	return func(req *ChatCompletionRequest) {
		req.MaxTokens = n
	}
}

// WithJSONResponse requests JSON output format.
func WithJSONResponse() Option {
	return func(req *ChatCompletionRequest) {
		req.ResponseFormat = ResponseFormat{Type: "json_object"}
	}
}

// WithModel sets the target model for the request.
func WithModel(model string) Option {
	return func(req *ChatCompletionRequest) {
		req.Model = model
	}
}

// BuildRequest creates a ChatCompletionRequest from messages and options.
func BuildRequest(messages []Message, opts ...Option) ChatCompletionRequest {
	req := ChatCompletionRequest{
		Messages: messages,
	}
	for _, opt := range opts {
		opt(&req)
	}
	return req
}
