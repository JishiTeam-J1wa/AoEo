package engine

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/providers"
	"github.com/sashabaranov/go-openai"
)



// ChatCompleteStream performs a streaming chat completion using the primary provider.
// The caller should read from the returned channel until it is closed.
// Check chunk.Err to detect stream errors.
func (s *Scheduler) ChatCompleteStream(ctx context.Context, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error) {
	if err := s.sem.Acquire(ctx); err != nil {
		return nil, err
	}
	p := s.PickPrimaryProvider()
	if p == nil {
		s.sem.Release()
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
		s.sem.Release()
		return nil, err
	}

	// Wrap the stream channel so we release the semaphore when consumption finishes.
	wrapped := make(chan core.StreamCompletionResponse, cap(stream))
	go func() {
		defer close(wrapped)
		defer s.sem.Release()
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

func chatCompleteStreamWithProvider(ctx context.Context, p providers.Provider, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error) {
	cfg := p.Config()

	var client *openai.Client
	if op, ok := p.(*providers.OpenAIProvider); ok {
		client = op.Client
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

	ch := make(chan core.StreamCompletionResponse, 16)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(ch)
		defer stream.Close()

		for {
			select {
			case <-ctx.Done():
				ch <- core.StreamCompletionResponse{
					Model: cfg.Model,
					Chunk: core.StreamChunk{FinishReason: "cancelled"},
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
				ch <- core.StreamCompletionResponse{
					Model: cfg.Model,
					Chunk: core.StreamChunk{
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
				case ch <- core.StreamCompletionResponse{
					ID:    response.ID,
					Model: response.Model,
					Chunk: core.StreamChunk{
						Index: choice.Index,
						Delta: core.Message{
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
func ParseSSE(r io.Reader) <-chan core.StreamChunk {
	ch := make(chan core.StreamChunk, 8)
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
			ch <- core.StreamChunk{
				Delta: core.Message{Content: data},
			}
		}
		if err := scanner.Err(); err != nil {
			ch <- core.StreamChunk{
				Delta: core.Message{Content: fmt.Sprintf("[SSE parse error: %v]", err)},
			}
		}
	}()
	return ch
}
