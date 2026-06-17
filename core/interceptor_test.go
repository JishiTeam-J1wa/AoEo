package core

import (
	"context"
	"errors"
	"testing"
)

func TestInterceptorChain_ApplyBefore(t *testing.T) {
	var calls []string
	chain := InterceptorChain{
		Interceptor{
			BeforeRequest: func(ctx context.Context, req *ChatCompletionRequest) error {
				calls = append(calls, "first")
				req.Model = "modified"
				return nil
			},
		},
		Interceptor{
			BeforeRequest: func(ctx context.Context, req *ChatCompletionRequest) error {
				calls = append(calls, "second")
				req.MaxTokens = 100
				return nil
			},
		},
	}

	req := ChatCompletionRequest{Model: "original"}
	if err := chain.ApplyBefore(context.Background(), &req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 2 || calls[0] != "first" || calls[1] != "second" {
		t.Fatalf("expected ordered calls, got %v", calls)
	}
	if req.Model != "modified" || req.MaxTokens != 100 {
		t.Fatalf("request not modified: %+v", req)
	}
}

func TestInterceptorChain_ApplyBefore_ShortCircuit(t *testing.T) {
	chain := InterceptorChain{
		Interceptor{
			BeforeRequest: func(ctx context.Context, req *ChatCompletionRequest) error {
				return errors.New("blocked")
			},
		},
		Interceptor{
			BeforeRequest: func(ctx context.Context, req *ChatCompletionRequest) error {
				t.Fatal("should not be called after short-circuit")
				return nil
			},
		},
	}

	req := ChatCompletionRequest{}
	err := chain.ApplyBefore(context.Background(), &req)
	if err == nil || err.Error() != "blocked" {
		t.Fatalf("expected blocked error, got %v", err)
	}
}

func TestInterceptorChain_ApplyBefore_NilHook(t *testing.T) {
	chain := InterceptorChain{
		Interceptor{}, // nil BeforeRequest
	}
	req := ChatCompletionRequest{}
	if err := chain.ApplyBefore(context.Background(), &req); err != nil {
		t.Fatalf("nil hook should not error: %v", err)
	}
}

func TestInterceptorChain_ApplyAfter(t *testing.T) {
	var calls []string
	chain := InterceptorChain{
		Interceptor{
			AfterResponse: func(ctx context.Context, req ChatCompletionRequest, resp *ChatCompletionResponse, err error) (*ChatCompletionResponse, error) {
				calls = append(calls, "first")
				resp.Choices[0].Message.Content = "modified"
				return resp, nil
			},
		},
		Interceptor{
			AfterResponse: func(ctx context.Context, req ChatCompletionRequest, resp *ChatCompletionResponse, err error) (*ChatCompletionResponse, error) {
				calls = append(calls, "second")
				return resp, nil
			},
		},
	}

	req := ChatCompletionRequest{}
	resp := &ChatCompletionResponse{Choices: []Choice{{Message: Message{Content: "original"}}}}
	result, err := chain.ApplyAfter(context.Background(), req, resp, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 2 || calls[0] != "first" || calls[1] != "second" {
		t.Fatalf("expected ordered calls, got %v", calls)
	}
	if result.Choices[0].Message.Content != "modified" {
		t.Fatalf("response not modified: %s", result.Choices[0].Message.Content)
	}
}

func TestInterceptorChain_ApplyAfter_TransformError(t *testing.T) {
	chain := InterceptorChain{
		Interceptor{
			AfterResponse: func(ctx context.Context, req ChatCompletionRequest, resp *ChatCompletionResponse, err error) (*ChatCompletionResponse, error) {
				// Transform error into a successful response
				if err != nil {
					return &ChatCompletionResponse{Choices: []Choice{{Message: Message{Content: "recovered"}}}}, nil
				}
				return resp, err
			},
		},
	}

	req := ChatCompletionRequest{}
	result, err := chain.ApplyAfter(context.Background(), req, nil, errors.New("original"))
	if err != nil {
		t.Fatalf("expected error transformed away: %v", err)
	}
	if result.Choices[0].Message.Content != "recovered" {
		t.Fatalf("unexpected content: %s", result.Choices[0].Message.Content)
	}
}

func TestInterceptorChain_ApplyAfter_NilHook(t *testing.T) {
	chain := InterceptorChain{
		Interceptor{}, // nil AfterResponse
	}
	req := ChatCompletionRequest{}
	resp := &ChatCompletionResponse{}
	result, err := chain.ApplyAfter(context.Background(), req, resp, nil)
	if err != nil {
		t.Fatalf("nil hook should not error: %v", err)
	}
	if result != resp {
		t.Fatal("nil hook should pass through")
	}
}

func TestInterceptorChain_Empty(t *testing.T) {
	var chain InterceptorChain
	req := ChatCompletionRequest{Model: "m"}
	if err := chain.ApplyBefore(context.Background(), &req); err != nil {
		t.Fatalf("empty chain should not error: %v", err)
	}
	resp := &ChatCompletionResponse{}
	result, err := chain.ApplyAfter(context.Background(), req, resp, nil)
	if err != nil {
		t.Fatalf("empty chain should not error: %v", err)
	}
	if result != resp {
		t.Fatal("empty chain should pass through")
	}
}

// ========== Additional tests for coverage ==========

func TestInterceptorChain_ApplyBefore_PanicRecovery(t *testing.T) {
	chain := InterceptorChain{
		Interceptor{
			BeforeRequest: func(ctx context.Context, req *ChatCompletionRequest) error {
				panic("before panic")
			},
		},
		Interceptor{
			BeforeRequest: func(ctx context.Context, req *ChatCompletionRequest) error {
				t.Fatal("should not be called after panic")
				return nil
			},
		},
	}

	req := ChatCompletionRequest{}
	err := chain.ApplyBefore(context.Background(), &req)
	if err == nil {
		t.Fatal("expected error from panic recovery")
	}
	if err.Error() != "interceptor BeforeRequest panic: before panic" {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestInterceptorChain_ApplyBefore_EmptyChain(t *testing.T) {
	chain := InterceptorChain{}
	req := ChatCompletionRequest{Model: "test"}
	err := chain.ApplyBefore(context.Background(), &req)
	if err != nil {
		t.Fatalf("empty chain should not error: %v", err)
	}
	if req.Model != "test" {
		t.Fatal("request should not be modified by empty chain")
	}
}

func TestInterceptorChain_ApplyAfter_EmptyChain(t *testing.T) {
	chain := InterceptorChain{}
	req := ChatCompletionRequest{}
	resp := &ChatCompletionResponse{Choices: []Choice{{Message: Message{Content: "test"}}}}
	result, err := chain.ApplyAfter(context.Background(), req, resp, nil)
	if err != nil {
		t.Fatalf("empty chain should not error: %v", err)
	}
	if result != resp {
		t.Fatal("empty chain should pass through resp")
	}
}

func TestInterceptorChain_ApplyAfter_PanicRecovery(t *testing.T) {
	chain := InterceptorChain{
		Interceptor{
			AfterResponse: func(ctx context.Context, req ChatCompletionRequest, resp *ChatCompletionResponse, err error) (*ChatCompletionResponse, error) {
				panic("after panic")
			},
		},
	}

	req := ChatCompletionRequest{}
	resp := &ChatCompletionResponse{}
	result, err := chain.ApplyAfter(context.Background(), req, resp, nil)
	if err == nil {
		t.Fatal("expected error from panic recovery")
	}
	if err.Error() != "interceptor AfterResponse panic: after panic" {
		t.Fatalf("unexpected error message: %v", err)
	}
	if result != nil {
		t.Fatal("resp should be nil after panic")
	}
}

func TestInterceptorChain_ApplyAfter_PanicRecoveryContinues(t *testing.T) {
	var secondCalled bool
	chain := InterceptorChain{
		Interceptor{
			AfterResponse: func(ctx context.Context, req ChatCompletionRequest, resp *ChatCompletionResponse, err error) (*ChatCompletionResponse, error) {
				panic("first panic")
			},
		},
		Interceptor{
			AfterResponse: func(ctx context.Context, req ChatCompletionRequest, resp *ChatCompletionResponse, err error) (*ChatCompletionResponse, error) {
				secondCalled = true
				// err should be the panic error from the first hook
				if err == nil {
					t.Fatal("expected panic error to be passed to second hook")
				}
				return &ChatCompletionResponse{Choices: []Choice{{Message: Message{Content: "recovered"}}}}, nil
			},
		},
	}

	req := ChatCompletionRequest{}
	result, err := chain.ApplyAfter(context.Background(), req, nil, nil)
	if !secondCalled {
		t.Fatal("second hook should be called after first panic")
	}
	if err != nil {
		t.Fatalf("expected error to be cleared by second hook, got: %v", err)
	}
	if result.Choices[0].Message.Content != "recovered" {
		t.Fatal("expected recovered response")
	}
}

func TestInterceptorChain_ApplyAfterStreamChunk_NormalExecution(t *testing.T) {
	var calls []string
	chain := InterceptorChain{
		Interceptor{
			AfterStreamChunk: func(ctx context.Context, req ChatCompletionRequest, chunk *StreamChunk) error {
				calls = append(calls, "first")
				chunk.Delta.Content = "modified"
				return nil
			},
		},
		Interceptor{
			AfterStreamChunk: func(ctx context.Context, req ChatCompletionRequest, chunk *StreamChunk) error {
				calls = append(calls, "second")
				return nil
			},
		},
	}

	req := ChatCompletionRequest{}
	chunk := &StreamChunk{Delta: Message{Content: "original"}}
	err := chain.ApplyAfterStreamChunk(context.Background(), req, chunk)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 2 || calls[0] != "first" || calls[1] != "second" {
		t.Fatalf("expected ordered calls, got %v", calls)
	}
	if chunk.Delta.Content != "modified" {
		t.Fatalf("chunk not modified: %s", chunk.Delta.Content)
	}
}

func TestInterceptorChain_ApplyAfterStreamChunk_NilHook(t *testing.T) {
	chain := InterceptorChain{
		Interceptor{}, // nil AfterStreamChunk
	}
	req := ChatCompletionRequest{}
	chunk := &StreamChunk{}
	err := chain.ApplyAfterStreamChunk(context.Background(), req, chunk)
	if err != nil {
		t.Fatalf("nil hook should not error: %v", err)
	}
}

func TestInterceptorChain_ApplyAfterStreamChunk_ErrorShortCircuit(t *testing.T) {
	chain := InterceptorChain{
		Interceptor{
			AfterStreamChunk: func(ctx context.Context, req ChatCompletionRequest, chunk *StreamChunk) error {
				return errors.New("chunk error")
			},
		},
		Interceptor{
			AfterStreamChunk: func(ctx context.Context, req ChatCompletionRequest, chunk *StreamChunk) error {
				t.Fatal("should not be called after short-circuit")
				return nil
			},
		},
	}

	req := ChatCompletionRequest{}
	chunk := &StreamChunk{}
	err := chain.ApplyAfterStreamChunk(context.Background(), req, chunk)
	if err == nil || err.Error() != "chunk error" {
		t.Fatalf("expected chunk_error, got %v", err)
	}
}

func TestInterceptorChain_ApplyAfterStreamChunk_PanicRecovery(t *testing.T) {
	chain := InterceptorChain{
		Interceptor{
			AfterStreamChunk: func(ctx context.Context, req ChatCompletionRequest, chunk *StreamChunk) error {
				panic("chunk panic")
			},
		},
	}

	req := ChatCompletionRequest{}
	chunk := &StreamChunk{}
	err := chain.ApplyAfterStreamChunk(context.Background(), req, chunk)
	if err == nil {
		t.Fatal("expected error from panic recovery")
	}
	if err.Error() != "interceptor AfterStreamChunk panic: chunk panic" {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestInterceptorChain_ApplyAfterStreamChunk_EmptyChain(t *testing.T) {
	chain := InterceptorChain{}
	req := ChatCompletionRequest{}
	chunk := &StreamChunk{Delta: Message{Content: "test"}}
	err := chain.ApplyAfterStreamChunk(context.Background(), req, chunk)
	if err != nil {
		t.Fatalf("empty chain should not error: %v", err)
	}
}

func TestInterceptorChain_ApplyAfterStreamDone_NormalExecution(t *testing.T) {
	var calls []string
	chain := InterceptorChain{
		Interceptor{
			AfterStreamDone: func(ctx context.Context, req ChatCompletionRequest, err error) error {
				calls = append(calls, "first")
				return nil
			},
		},
		Interceptor{
			AfterStreamDone: func(ctx context.Context, req ChatCompletionRequest, err error) error {
				calls = append(calls, "second")
				return nil
			},
		},
	}

	req := ChatCompletionRequest{}
	err := chain.ApplyAfterStreamDone(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 2 || calls[0] != "first" || calls[1] != "second" {
		t.Fatalf("expected ordered calls, got %v", calls)
	}
}

func TestInterceptorChain_ApplyAfterStreamDone_NilHook(t *testing.T) {
	chain := InterceptorChain{
		Interceptor{}, // nil AfterStreamDone
	}
	req := ChatCompletionRequest{}
	err := chain.ApplyAfterStreamDone(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("nil hook should not error: %v", err)
	}
}

func TestInterceptorChain_ApplyAfterStreamDone_ErrorOverride(t *testing.T) {
	chain := InterceptorChain{
		Interceptor{
			AfterStreamDone: func(ctx context.Context, req ChatCompletionRequest, err error) error {
				return errors.New("overridden error")
			},
		},
		Interceptor{
			AfterStreamDone: func(ctx context.Context, req ChatCompletionRequest, err error) error {
				// Should receive the overridden error from the first hook
				if err == nil || err.Error() != "overridden error" {
					t.Fatalf("expected 'overridden error', got %v", err)
				}
				return errors.New("final error")
			},
		},
	}

	req := ChatCompletionRequest{}
	err := chain.ApplyAfterStreamDone(context.Background(), req, nil)
	if err == nil || err.Error() != "final error" {
		t.Fatalf("expected 'final error', got %v", err)
	}
}

func TestInterceptorChain_ApplyAfterStreamDone_NilReturnPreservesError(t *testing.T) {
	originalErr := errors.New("original")
	chain := InterceptorChain{
		Interceptor{
			AfterStreamDone: func(ctx context.Context, req ChatCompletionRequest, err error) error {
				// Return nil — should NOT clear the error; only non-nil returns override
				return nil
			},
		},
	}

	req := ChatCompletionRequest{}
	err := chain.ApplyAfterStreamDone(context.Background(), req, originalErr)
	if err != originalErr {
		t.Fatalf("expected original error to be preserved, got %v", err)
	}
}

func TestInterceptorChain_ApplyAfterStreamDone_PanicRecovery(t *testing.T) {
	chain := InterceptorChain{
		Interceptor{
			AfterStreamDone: func(ctx context.Context, req ChatCompletionRequest, err error) error {
				panic("done panic")
			},
		},
	}

	req := ChatCompletionRequest{}
	err := chain.ApplyAfterStreamDone(context.Background(), req, nil)
	if err == nil {
		t.Fatal("expected error from panic recovery")
	}
	if err.Error() != "interceptor AfterStreamDone panic: done panic" {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestInterceptorChain_ApplyAfterStreamDone_EmptyChain(t *testing.T) {
	chain := InterceptorChain{}
	req := ChatCompletionRequest{}
	err := chain.ApplyAfterStreamDone(context.Background(), req, errors.New("passthrough"))
	if err == nil || err.Error() != "passthrough" {
		t.Fatalf("expected passthrough error, got %v", err)
	}
}

func TestInterceptorChain_ApplyAfterStreamDone_EmptyChainNilError(t *testing.T) {
	chain := InterceptorChain{}
	req := ChatCompletionRequest{}
	err := chain.ApplyAfterStreamDone(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("empty chain with nil error should return nil, got %v", err)
	}
}
