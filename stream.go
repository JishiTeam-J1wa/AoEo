package aoeo

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/sashabaranov/go-openai"
)

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

// ChatCompleteStream performs a streaming chat completion using the primary provider.
// The caller should read from the returned channel until it is closed.
// Check chunk.Err to detect stream errors.
func (s *Scheduler) ChatCompleteStream(ctx context.Context, req ChatCompletionRequest) (<-chan StreamCompletionResponse, error) {
	if err := s.sem.acquire(ctx); err != nil {
		return nil, err
	}
	p := s.PickPrimaryProvider()
	if p == nil {
		s.sem.release()
		return nil, fmt.Errorf("no available provider")
	}

	reqCopy := req
	if reqCopy.Model == "" {
		reqCopy.Model = p.Config().Model
	}

	// Apply prompt injection if configured.
	if s.promptInjector != nil {
		s.promptInjector.Inject(p.Name(), reqCopy.Model, &reqCopy)
	}

	stream, err := chatCompleteStreamWithProvider(ctx, p, reqCopy)
	if err != nil {
		s.sem.release()
		return nil, err
	}

	// Wrap the stream channel so we release the semaphore when consumption finishes.
	wrapped := make(chan StreamCompletionResponse, cap(stream))
	go func() {
		defer close(wrapped)
		defer s.sem.release()
		for msg := range stream {
			select {
			case <-ctx.Done():
				return
			case wrapped <- msg:
			}
		}
	}()
	return wrapped, nil
}

func chatCompleteStreamWithProvider(ctx context.Context, p Provider, req ChatCompletionRequest) (<-chan StreamCompletionResponse, error) {
	cfg := p.Config()

	var client *openai.Client
	if op, ok := p.(*OpenAIProvider); ok {
		client = op.client
	} else {
		oc := openai.DefaultConfig(cfg.APIKey)
		oc.BaseURL = cfg.Endpoint
		client = openai.NewClientWithConfig(oc)
	}

	messages := make([]openai.ChatCompletionMessage, len(req.Messages))
	for i, m := range req.Messages {
		messages[i] = openai.ChatCompletionMessage{
			Role:    m.Role,
			Content: m.Content,
		}
	}

	var respFormat *openai.ChatCompletionResponseFormat
	if req.ResponseFormat.Type != "" {
		respFormat = &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatType(req.ResponseFormat.Type),
		}
	}

	streamReq := openai.ChatCompletionRequest{
		Model:          req.Model,
		Messages:       messages,
		Temperature:    req.Temperature,
		MaxTokens:      req.MaxTokens,
		ResponseFormat: respFormat,
		Stream:         true,
	}
	if streamReq.Model == "" {
		streamReq.Model = cfg.Model
	}

	stream, err := client.CreateChatCompletionStream(ctx, streamReq)
	if err != nil {
		return nil, fmt.Errorf("%s stream: %w", p.Name(), err)
	}

	ch := make(chan StreamCompletionResponse, 16)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(ch)
		defer stream.Close()

		for {
			select {
			case <-ctx.Done():
				ch <- StreamCompletionResponse{
					Model: cfg.Model,
					Chunk: StreamChunk{FinishReason: "cancelled"},
					Err:   ctx.Err(),
				}
				return
			default:
			}

			response, err := stream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				ch <- StreamCompletionResponse{
					Model: cfg.Model,
					Chunk: StreamChunk{
						FinishReason: "error",
					},
					Err: fmt.Errorf("%s stream recv: %w", p.Name(), err),
				}
				return
			}

			for _, choice := range response.Choices {
				select {
				case <-ctx.Done():
					return
				case ch <- StreamCompletionResponse{
					ID:    response.ID,
					Model: response.Model,
					Chunk: StreamChunk{
						Index: choice.Index,
						Delta: Message{
							Role:    choice.Delta.Role,
							Content: choice.Delta.Content,
						},
						FinishReason: string(choice.FinishReason),
					},
				}:
				}
			}
		}
	}()

	return ch, nil
}

// ParseSSE parses a raw Server-Sent Events stream into chunks.
// Useful for debugging or proxying streams.
func ParseSSE(r io.Reader) <-chan StreamChunk {
	ch := make(chan StreamChunk, 8)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				return
			}
			// Minimal parse - users can extend with json.Unmarshal.
			ch <- StreamChunk{
				Delta: Message{Content: data},
			}
		}
		if err := scanner.Err(); err != nil {
			ch <- StreamChunk{
				Delta: Message{Content: fmt.Sprintf("[SSE parse error: %v]", err)},
			}
		}
	}()
	return ch
}
