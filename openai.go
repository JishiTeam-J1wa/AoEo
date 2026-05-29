package aoeo

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"
)

// OpenAIProvider implements a generic OpenAI-compatible provider adapter.
// It works with any API that follows the OpenAI chat completions protocol,
// including self-hosted models (vLLM, Ollama, etc.).
type OpenAIProvider struct {
	*BaseProvider
	client *openai.Client
}

// NewOpenAIProvider creates a generic OpenAI-compatible provider.
// If endpoint is empty, it defaults to "https://api.openai.com/v1".
func NewOpenAIProvider(config ProviderConfig) *OpenAIProvider {
	if config.Endpoint == "" {
		config.Endpoint = "https://api.openai.com/v1"
	}

	oc := openai.DefaultConfig(config.APIKey)
	oc.BaseURL = config.Endpoint
	if config.SkipTLSVerify {
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		oc.HTTPClient = &http.Client{Transport: tr}
	}

	return &OpenAIProvider{
		BaseProvider: NewBaseProvider(config),
		client:       openai.NewClientWithConfig(oc),
	}
}

func (p *OpenAIProvider) Name() string { return p.Config().Name }

func (p *OpenAIProvider) ChatComplete(ctx context.Context, req ChatCompletionRequest) (result *ChatCompletionResponse, err error) {
	defer func() {
		if r := recover(); r != nil {
			GetLogger().Error("provider panic recovered",
				"provider", p.Name(),
				"panic", r)
			p.RecordFailure()
			err = fmt.Errorf("provider panic: %v", r)
			return
		}
		if err != nil {
			p.RecordFailure()
		} else {
			p.RecordSuccess()
		}
	}()

	messages := make([]openai.ChatCompletionMessage, len(req.Messages))
	for i, m := range req.Messages {
		messages[i] = openai.ChatCompletionMessage{
			Role:    m.Role,
			Content: m.Content,
		}
	}

	// Inject system prompt override if set.
	if sys := p.GetSystemPrompt(); sys != "" {
		messages = append([]openai.ChatCompletionMessage{{
			Role:    openai.ChatMessageRoleSystem,
			Content: sys,
		}}, messages...)
	}

	var respFormat *openai.ChatCompletionResponseFormat
	if req.ResponseFormat.Type != "" {
		respFormat = &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatType(req.ResponseFormat.Type),
		}
	}

	resp, err := p.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:          req.Model,
		Messages:       messages,
		Temperature:    req.Temperature,
		MaxTokens:      req.MaxTokens,
		ResponseFormat: respFormat,
	})
	if err != nil {
		// Compatibility retry: some providers (e.g. Kimi kimi-k2.6) only accept temperature=1.
		// If error mentions temperature, retry without setting it (omitted field defaults to 1).
		if isTemperatureError(err) && req.Temperature != 0 {
			resp, err = p.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
				Model:          req.Model,
				Messages:       messages,
				MaxTokens:      req.MaxTokens,
				ResponseFormat: respFormat,
			})
		}
		if err != nil {
			return nil, fmt.Errorf("%s chat complete: %w", p.Name(), err)
		}
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("%s chat complete: no choices in response", p.Name())
	}

	result = &ChatCompletionResponse{
		ID:    resp.ID,
		Model: resp.Model,
		Choices: []Choice{{
			Index: 0,
			Message: Message{
				Role:             resp.Choices[0].Message.Role,
				Content:          resp.Choices[0].Message.Content,
				// ReasoningContent is provider-specific; DeepSeek puts it in a custom field.
				// For generic OpenAI-compatible, we leave it empty here.
			},
			FinishReason: string(resp.Choices[0].FinishReason),
		}},
		Usage: Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}
	return result, nil
}

// ListModels fetches the list of available models from the provider via the
// OpenAI-compatible /models endpoint. It reuses the provider's HTTP client.
func isTemperatureError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "temperature")
}

func (p *OpenAIProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	if p.Config().APIKey == "" || p.Config().Endpoint == "" {
		return nil, fmt.Errorf("provider %s config incomplete", p.Config().Name)
	}

	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	models, err := p.client.ListModels(listCtx)
	if err != nil {
		return nil, fmt.Errorf("list models from %s: %w", p.Config().Name, err)
	}

	var result []ModelInfo
	for _, m := range models.Models {
		result = append(result, ModelInfo{ID: m.ID, OwnedBy: m.OwnedBy})
	}
	return result, nil
}
