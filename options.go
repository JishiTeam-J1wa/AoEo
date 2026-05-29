package aoeo

import "github.com/JishiTeam-J1wa/AoEo/core"

// Option is a functional option for configuring chat completion requests.
type Option func(*core.ChatCompletionRequest)

// WithSystemPrompt sets or replaces the system prompt for the request.
func WithSystemPrompt(prompt string) Option {
	return func(req *core.ChatCompletionRequest) {
		for i := range req.Messages {
			if req.Messages[i].Role == "system" {
				req.Messages[i].Content = prompt
				return
			}
		}
		req.Messages = append([]core.Message{{Role: "system", Content: prompt}}, req.Messages...)
	}
}

// WithTemperature sets the sampling temperature.
func WithTemperature(t float32) Option {
	return func(req *core.ChatCompletionRequest) {
		req.Temperature = t
	}
}

// WithMaxTokens sets the maximum number of tokens to generate.
func WithMaxTokens(n int) Option {
	return func(req *core.ChatCompletionRequest) {
		req.MaxTokens = n
	}
}

// WithJSONResponse requests JSON output format.
func WithJSONResponse() Option {
	return func(req *core.ChatCompletionRequest) {
		req.ResponseFormat = core.ResponseFormat{Type: "json_object"}
	}
}

// WithModel sets the target model for the request.
func WithModel(model string) Option {
	return func(req *core.ChatCompletionRequest) {
		req.Model = model
	}
}

// WithTopP sets the nucleus sampling parameter.
func WithTopP(p float32) Option {
	return func(req *core.ChatCompletionRequest) {
		req.TopP = p
	}
}

// WithPresencePenalty sets the presence penalty.
func WithPresencePenalty(p float32) Option {
	return func(req *core.ChatCompletionRequest) {
		req.PresencePenalty = p
	}
}

// WithFrequencyPenalty sets the frequency penalty.
func WithFrequencyPenalty(p float32) Option {
	return func(req *core.ChatCompletionRequest) {
		req.FrequencyPenalty = p
	}
}

// WithStop sets the stop sequences.
func WithStop(stop []string) Option {
	return func(req *core.ChatCompletionRequest) {
		if len(stop) > 0 {
			copied := make([]string, len(stop))
			copy(copied, stop)
			req.Stop = copied
		}
	}
}

// WithSeed sets the seed for deterministic sampling.
func WithSeed(seed int) Option {
	return func(req *core.ChatCompletionRequest) {
		req.Seed = &seed
	}
}

// BuildRequest creates a ChatCompletionRequest from messages and options.
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
