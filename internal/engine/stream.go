package engine

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

// ChatCompleteStream performs a streaming chat completion using the primary provider.
// The caller MUST read from the returned channel until it is closed, or cancel the
// provided context, to avoid leaking the background goroutine and the semaphore slot.
// Check chunk.Err to detect stream errors.
func (s *Scheduler) ChatCompleteStream(ctx context.Context, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	if err := s.sem.Acquire(ctx); err != nil {
		return nil, err
	}
	p := s.PickPrimaryProvider()
	if p == nil {
		s.sem.Release()
		return nil, ErrNoAvailableProvider
	}

	reqCopy := req
	if reqCopy.Model == "" {
		reqCopy.Model = p.Config().Model
	}

	// Apply prompt injection if configured.
	if pi := s.promptInjector.Load(); pi != nil {
		reqCopy = req.Clone()
		if reqCopy.Model == "" {
			reqCopy.Model = p.Config().Model
		}
		pi.Inject(p.Name(), reqCopy.Model, &reqCopy)
	}

	// Apply interceptor BeforeRequest hooks.
	chain := s.interceptorChain()
	if err := chain.ApplyBefore(ctx, &reqCopy); err != nil {
		s.sem.Release()
		return nil, err
	}

	streamCtx, cancel := context.WithTimeout(ctx, time.Duration(s.timeout.Load()))
	stream, err := p.ChatCompleteStream(streamCtx, reqCopy)
	if err != nil {
		cancel()
		s.sem.Release()
		return nil, err
	}

	// Wrap the stream channel so we release the semaphore when consumption finishes.
	// Use a fixed buffer to decouple producer and consumer, preventing goroutine leaks
	// when the consumer reads slower than the producer.
	const wrappedBufSize = 16
	wrapped := make(chan core.StreamCompletionResponse, wrappedBufSize)
	go func() {
		defer close(wrapped)
		defer s.sem.Release()
		defer cancel()
		var finalErr error
		defer func() {
			if err := chain.ApplyAfterStreamDone(streamCtx, reqCopy, finalErr); err != nil {
				core.GetLogger().Debug("AfterStreamDone error", "error", err)
			}
		}()
		for {
			select {
			case <-streamCtx.Done():
				finalErr = streamCtx.Err()
				return
			case msg, ok := <-stream:
				if !ok {
					return
				}
				if msg.Err != nil {
					finalErr = msg.Err
					select {
					case <-streamCtx.Done():
						return
					case wrapped <- msg:
					}
					return
				}
				if err := chain.ApplyAfterStreamChunk(streamCtx, reqCopy, &msg.Chunk); err != nil {
					finalErr = err
					select {
					case <-streamCtx.Done():
						return
					case wrapped <- core.StreamCompletionResponse{Err: err}:
					}
					return
				}
				select {
				case <-streamCtx.Done():
					return
				case wrapped <- msg:
				}
			}
		}
	}()
	return wrapped, nil
}

// ParseSSE parses a raw Server-Sent Events stream into chunks.
// Useful for debugging or proxying streams.
func ParseSSE(r io.Reader) <-chan core.StreamChunk {
	ch := make(chan core.StreamChunk, 8)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
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
