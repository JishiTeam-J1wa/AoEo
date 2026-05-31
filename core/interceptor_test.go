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
