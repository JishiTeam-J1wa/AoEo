package aoeo

// NewDeepSeekProvider creates a DeepSeek provider with sensible defaults.
// Default endpoint: https://api.deepseek.com
// Default model:    deepseek-v4-pro
func NewDeepSeekProvider(config ProviderConfig) Provider {
	if config.Endpoint == "" {
		config.Endpoint = "https://api.deepseek.com"
	}
	if config.Model == "" {
		config.Model = "deepseek-v4-pro"
	}
	if config.Name == "" {
		config.Name = "deepseek"
	}
	return NewOpenAIProvider(config)
}
