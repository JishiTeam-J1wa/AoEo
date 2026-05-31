package engine

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestAdaptiveSemaphore_AcquireRelease(t *testing.T) {
	sem := NewAdaptiveSemaphore(2)

	if err := sem.Acquire(context.Background()); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if err := sem.Acquire(context.Background()); err != nil {
		t.Fatalf("second acquire failed: %v", err)
	}

	// Third acquire should block
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := sem.Acquire(ctx); err != context.DeadlineExceeded {
		t.Fatalf("expected timeout, got %v", err)
	}

	sem.Release()
	if err := sem.Acquire(context.Background()); err != nil {
		t.Fatalf("acquire after release failed: %v", err)
	}
}

func TestAdaptiveSemaphore_ContextCancel(t *testing.T) {
	sem := NewAdaptiveSemaphore(1)
	sem.Acquire(context.Background()) // occupy

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := sem.Acquire(ctx); err != context.Canceled {
		t.Fatalf("expected canceled, got %v", err)
	}

	sem.Release()
}

func TestAdaptiveSemaphore_AcquireN(t *testing.T) {
	sem := NewAdaptiveSemaphore(3)
	if err := sem.AcquireN(context.Background(), 2); err != nil {
		t.Fatalf("acquire 2 failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := sem.AcquireN(ctx, 2); err != context.DeadlineExceeded {
		t.Fatalf("expected timeout, got %v", err)
	}
	sem.ReleaseN(2)
}

func TestAdaptiveSemaphore_SetMaxConc(t *testing.T) {
	sem := NewAdaptiveSemaphore(1)
	sem.Acquire(context.Background()) // occupy

	// Increase capacity
	sem.setMaxConc(2)
	if err := sem.Acquire(context.Background()); err != nil {
		t.Fatalf("acquire after increase failed: %v", err)
	}
}

func TestAdaptiveSemaphore_Concurrent(t *testing.T) {
	sem := NewAdaptiveSemaphore(10)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sem.Acquire(context.Background()); err != nil {
				t.Errorf("acquire failed: %v", err)
			}
			time.Sleep(time.Millisecond)
			sem.Release()
		}()
	}
	wg.Wait()
}
