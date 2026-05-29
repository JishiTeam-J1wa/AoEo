package aoeo

// NewKimiProvider creates a Kimi (Moonshot AI) provider with sensible defaults.
// Default endpoint: https://api.moonshot.cn/v1
// Default model:    kimi-k2.6
func NewKimiProvider(config ProviderConfig) Provider {
	if config.Endpoint == "" {
		config.Endpoint = "https://api.moonshot.cn/v1"
	}
	if config.Model == "" {
		config.Model = "kimi-k2.6"
	}
	if config.Name == "" {
		config.Name = "kimi"
	}
	return NewOpenAIProvider(config)
}
