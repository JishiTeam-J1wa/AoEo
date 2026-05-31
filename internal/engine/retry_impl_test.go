package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

func TestDoRetry_Success(t *testing.T) {
	calls := 0
	fn := func() error {
		calls++
		return nil
	}

	err := DoRetry(context.Background(), core.RetryConfig{MaxRetries: 2}, fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestDoRetry_EventualSuccess(t *testing.T) {
	calls := 0
	fn := func() error {
		calls++
		if calls < 3 {
			return errors.New("transient")
		}
		return nil
	}

	cfg := core.RetryConfig{
		MaxRetries: 5,
		BaseDelay:  10 * time.Millisecond,
		MaxDelay:   50 * time.Millisecond,
		Retryable:  func(err error) bool { return true },
	}

	err := DoRetry(context.Background(), cfg, fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestDoRetry_MaxRetriesExceeded(t *testing.T) {
	calls := 0
	fn := func() error {
		calls++
		return errors.New("always fails")
	}

	cfg := core.RetryConfig{
		MaxRetries: 2,
		BaseDelay:  5 * time.Millisecond,
		Retryable:  func(err error) bool { return true },
	}

	err := DoRetry(context.Background(), cfg, fn)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 3 { // initial + 2 retries
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestDoRetry_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := core.RetryConfig{
		MaxRetries: 2,
		BaseDelay:  100 * time.Millisecond,
		Retryable:  func(err error) bool { return true },
	}

	fn := func() error { return errors.New("fail") }
	err := DoRetry(ctx, cfg, fn)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestDoRetry_NonRetryable(t *testing.T) {
	calls := 0
	fn := func() error {
		calls++
		return errors.New("fatal")
	}

	cfg := core.RetryConfig{
		MaxRetries: 5,
		Retryable:  func(err error) bool { return false },
	}

	err := DoRetry(context.Background(), cfg, fn)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("expected 1 call (non-retryable), got %d", calls)
	}
}

func TestDoRetry_Disabled(t *testing.T) {
	calls := 0
	fn := func() error {
		calls++
		return errors.New("fail")
	}

	err := DoRetry(context.Background(), core.RetryConfig{MaxRetries: 0}, fn)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("expected 1 call (retry disabled), got %d", calls)
	}
}

func TestDoRetry_NegativeMaxRetries(t *testing.T) {
	calls := 0
	fn := func() error {
		calls++
		return errors.New("fail")
	}

	err := DoRetry(context.Background(), core.RetryConfig{MaxRetries: -1}, fn)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("expected 1 call (negative treated as disabled), got %d", calls)
	}
}

func TestDoRetry_BackoffJitter(t *testing.T) {
	calls := 0
	fn := func() error {
		calls++
		return errors.New("fail")
	}

	cfg := core.RetryConfig{
		MaxRetries: 3,
		BaseDelay:  10 * time.Millisecond,
		MaxDelay:   100 * time.Millisecond,
		Multiplier: 2.0,
		Retryable:  func(err error) bool { return true },
	}

	start := time.Now()
	_ = DoRetry(context.Background(), cfg, fn)
	elapsed := time.Since(start)

	// Should take at least baseDelay + some backoff
	if elapsed < 15*time.Millisecond {
		t.Fatalf("backoff too fast: %v", elapsed)
	}
}
