package aoeo

// NewQwenProvider creates a Qwen (Alibaba Tongyi) provider with sensible defaults.
// Default endpoint: https://dashscope.aliyuncs.com/compatible-mode/v1
// Default model:    qwen3.7-max
func NewQwenProvider(config ProviderConfig) Provider {
	if config.Endpoint == "" {
		config.Endpoint = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	}
	if config.Model == "" {
		config.Model = "qwen3.7-max"
	}
	if config.Name == "" {
		config.Name = "qwen"
	}
	return NewOpenAIProvider(config)
}
