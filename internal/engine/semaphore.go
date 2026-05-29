package engine

import (
	"context"
	"sync"
	"sync/atomic"
)

// waiter represents a goroutine waiting for n slots.
type waiter struct {
	ch chan struct{}
	n  int
}

// adaptiveSemaphore is a semaphore that supports dynamic capacity adjustment.
// It uses a FIFO queue of waiters and can increase/decrease maxConc at runtime.
//
// Fast path (no contention): uses atomic operations to avoid lock overhead.
type adaptiveSemaphore struct {
	mu      sync.Mutex
	inUse   atomic.Int32
	maxConc atomic.Int32
	waiters []waiter
}

func NewAdaptiveSemaphore(maxConc int) *adaptiveSemaphore {
	a := &adaptiveSemaphore{}
	a.maxConc.Store(int32(maxConc))
	return a
}

func (a *adaptiveSemaphore) Acquire(ctx context.Context) error {
	return a.AcquireN(ctx, 1)
}

func (a *adaptiveSemaphore) AcquireN(ctx context.Context, n int) error {
	// Fast path: try atomic CAS to avoid locking.
	for {
		current := a.inUse.Load()
		maxC := a.maxConc.Load()
		if current+int32(n) > maxC {
			break // Fall through to slow path.
		}
		if a.inUse.CompareAndSwap(current, current+int32(n)) {
			return nil // Fast path succeeded.
		}
		// CAS failed, retry.
	}

	// Slow path: queue as waiter.
	a.mu.Lock()
	ch := make(chan struct{}, 1)
	a.waiters = append(a.waiters, waiter{ch: ch, n: n})
	a.mu.Unlock()

	select {
	case <-ctx.Done():
		// Remove ourselves from the waiters list.
		a.mu.Lock()
		found := false
		for i, w := range a.waiters {
			if w.ch == ch {
				a.waiters = append(a.waiters[:i], a.waiters[i+1:]...)
				found = true
				break
			}
		}
		a.mu.Unlock()
		if !found {
			// Already awakened by a release; slot was reserved but we won't use it.
			a.inUse.Add(-int32(n))
		}
		return ctx.Err()
	case <-ch:
		return nil
	}
}

func (a *adaptiveSemaphore) Release() {
	a.ReleaseN(1)
}

func (a *adaptiveSemaphore) ReleaseN(n int) {
	a.inUse.Add(-int32(n))

	a.mu.Lock()
	for len(a.waiters) > 0 {
		w := a.waiters[0]
		if a.inUse.Load()+int32(w.n) > a.maxConc.Load() {
			break
		}
		a.waiters = a.waiters[1:]
		// Atomically reserve slots for the waiter.
		a.inUse.Add(int32(w.n))
		a.mu.Unlock()
		w.ch <- struct{}{}
		a.mu.Lock()
	}
	a.mu.Unlock()
}

// setMaxConc updates the capacity and attempts to satisfy pending waiters.
func (a *adaptiveSemaphore) setMaxConc(n int) {
	a.maxConc.Store(int32(n))

	a.mu.Lock()
	for len(a.waiters) > 0 {
		w := a.waiters[0]
		if a.inUse.Load()+int32(w.n) > a.maxConc.Load() {
			break
		}
		a.waiters = a.waiters[1:]
		a.inUse.Add(int32(w.n))
		a.mu.Unlock()
		w.ch <- struct{}{}
		a.mu.Lock()
	}
	a.mu.Unlock()
}
