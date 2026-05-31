package core

import "context"

// Interceptor provides hooks into the request/response lifecycle.
// Use Interceptors to implement cross-cutting concerns such as logging,
// metrics, rate limiting, request mutation, or response post-processing.
//
// All hooks are optional; nil functions are skipped. Implementations must be
// safe for concurrent use. Blocking I/O inside hooks will block the caller.
type Interceptor struct {
	// BeforeRequest is called before a request is sent to a provider.
	// It receives the request by pointer and may modify it in place.
	// Returning a non-nil error aborts the request and the error is
	// returned to the caller.
	BeforeRequest func(ctx context.Context, req *ChatCompletionRequest) error

	// AfterResponse is called after a provider returns a response or error.
	// It receives the original request, the response (may be nil on error),
	// and the error (may be nil on success). It may return a modified
	// response/error pair.
	AfterResponse func(ctx context.Context, req ChatCompletionRequest, resp *ChatCompletionResponse, err error) (*ChatCompletionResponse, error)

	// AfterStreamChunk is called for each chunk in a streaming response
	// before it is forwarded to the consumer. It receives the chunk by
	// pointer and may modify it in place. Returning a non-nil error aborts
	// the stream and sends an error chunk to the consumer.
	AfterStreamChunk func(ctx context.Context, req ChatCompletionRequest, chunk *StreamChunk) error

	// AfterStreamDone is called when a streaming response completes,
	// either normally or due to an error. It receives the original request
	// and any final error (nil on normal completion).
	AfterStreamDone func(ctx context.Context, req ChatCompletionRequest, err error) error
}

// InterceptorChain executes a slice of interceptors in order.
// It is used internally by the scheduler; consumers do not need to use it directly.
type InterceptorChain []Interceptor

// ApplyBefore runs all BeforeRequest hooks in order.
// The first error short-circuits the chain.
func (chain InterceptorChain) ApplyBefore(ctx context.Context, req *ChatCompletionRequest) error {
	for _, ic := range chain {
		if ic.BeforeRequest == nil {
			continue
		}
		if err := ic.BeforeRequest(ctx, req); err != nil {
			return err
		}
	}
	return nil
}

// ApplyAfter runs all AfterResponse hooks in order.
// Each hook may transform the response/error.
func (chain InterceptorChain) ApplyAfter(ctx context.Context, req ChatCompletionRequest, resp *ChatCompletionResponse, err error) (*ChatCompletionResponse, error) {
	for _, ic := range chain {
		if ic.AfterResponse == nil {
			continue
		}
		resp, err = ic.AfterResponse(ctx, req, resp, err)
	}
	return resp, err
}

// ApplyAfterStreamChunk runs all AfterStreamChunk hooks in order.
// The first error short-circuits the chain and is returned.
func (chain InterceptorChain) ApplyAfterStreamChunk(ctx context.Context, req ChatCompletionRequest, chunk *StreamChunk) error {
	for _, ic := range chain {
		if ic.AfterStreamChunk == nil {
			continue
		}
		if err := ic.AfterStreamChunk(ctx, req, chunk); err != nil {
			return err
		}
	}
	return nil
}

// ApplyAfterStreamDone runs all AfterStreamDone hooks in order.
// Each hook may transform the final error.
func (chain InterceptorChain) ApplyAfterStreamDone(ctx context.Context, req ChatCompletionRequest, err error) error {
	for _, ic := range chain {
		if ic.AfterStreamDone == nil {
			continue
		}
		if e := ic.AfterStreamDone(ctx, req, err); e != nil {
			err = e
		}
	}
	return err
}
