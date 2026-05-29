package aoeo

// NewGLMProvider creates a GLM (Zhipu AI) provider with sensible defaults.
// Default endpoint: https://open.bigmodel.cn/api/paas/v4
// Default model:    glm-5.1
func NewGLMProvider(config ProviderConfig) Provider {
	if config.Endpoint == "" {
		config.Endpoint = "https://open.bigmodel.cn/api/paas/v4"
	}
	if config.Model == "" {
		config.Model = "glm-5.1"
	}
	if config.Name == "" {
		config.Name = "glm"
	}
	return NewOpenAIProvider(config)
}
