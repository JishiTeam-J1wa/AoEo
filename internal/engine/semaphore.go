// semaphore.go 实现支持动态容量调整的自适应信号量，使用 CAS 快速路径 + FIFO 等待队列。
//
// Author: JishiTeam-J1wa
// Created: 2026-05
//
// Changelog:
//   2026-06-12 - 注释体系规范化

package engine

import (
	"context"
	"sync"
	"sync/atomic"
)

// waiter 表示一个正在等待 n 个槽位的协程。
// ch 用于接收唤醒信号，n 表示该协程需要的并发槽位数。
type waiter struct {
	ch chan struct{}
	n  int
}

// adaptiveSemaphore 是一个支持动态容量调整的自适应信号量。
// 它使用 FIFO 队列管理等待者，并可以在运行时增大或减小 maxConc（最大并发数）。
//
// 快速路径（无竞争时）：使用原子操作避免锁开销。
// 慢速路径（有竞争时）：通过互斥锁 + FIFO 等待队列保证公平性。
type adaptiveSemaphore struct {
	mu      sync.Mutex
	inUse   atomic.Int32  // 当前已占用的槽位数（原子变量，用于快速路径）
	maxConc atomic.Int32  // 最大并发容量（原子变量，支持动态调整）
	waiters []waiter      // FIFO 等待队列，受 mu 保护
}

// NewAdaptiveSemaphore 创建一个最大并发容量为 maxConc 的自适应信号量。
//
// Param:
//   - maxConc: int - 最大并发容量，必须大于 0
//
// Return:
//   - *adaptiveSemaphore: 新创建的信号量实例
func NewAdaptiveSemaphore(maxConc int) *adaptiveSemaphore {
	a := &adaptiveSemaphore{}
	a.maxConc.Store(int32(maxConc))
	return a
}

// Acquire 获取 1 个并发槽位，等同于 AcquireN(ctx, 1)。
//
// Param:
//   - ctx: context.Context - 控制等待超时，取消后返回 ctx.Err()
//
// Return:
//   - nil: 成功获取槽位
//   - error: 上下文取消时返回 ctx.Err()
func (a *adaptiveSemaphore) Acquire(ctx context.Context) error {
	return a.AcquireN(ctx, 1)
}

// AcquireN 获取 n 个并发槽位。
//
// 快速路径（CAS）：
//
//	在循环中尝试通过 CompareAndSwap（CAS）原子地增加 inUse。
//	如果当前 inUse + n <= maxConc 且 CAS 成功，则直接返回，无需加锁。
//	如果 CAS 失败（说明有其他协程同时修改了 inUse），则重试。
//
// 慢速路径（FIFO 等待队列）：
//
//	当 inUse + n > maxConc 时，说明当前没有足够的空闲槽位，
//	协程需要加入 FIFO 等待队列并阻塞，直到被 Release 唤醒或上下文取消。
//
// 修复 SEM-01：在 CAS 失败到获取锁之间，可能有其他协程释放了槽位。
// 因此获取锁后必须重新检查是否有空闲槽位，避免丢失唤醒信号导致永久阻塞。
//
// Param:
//   - ctx: context.Context - 控制等待超时，取消后尝试从等待队列移除自身
//   - n: int - 需要获取的并发槽位数，必须大于 0
//
// Return:
//   - nil: 成功获取 n 个槽位
//   - error: 上下文取消时返回 ctx.Err()
//
// Edge Cases:
//   - 上下文在获取槽位后立即取消时，会归还已占用的槽位
//   - 上下文取消时若已被唤醒（槽位已预留），会归还预留的槽位
func (a *adaptiveSemaphore) AcquireN(ctx context.Context, n int) error {
	// ---- 快速路径：尝试原子 CAS 操作避免加锁 ----
	for {
		current := a.inUse.Load()
		maxC := a.maxConc.Load()
		if current+int32(n) > maxC {
			break // 槽位不足，落入慢速路径
		}
		if a.inUse.CompareAndSwap(current, current+int32(n)) {
			return nil // CAS 成功，快速路径获取槽位成功
		}
		// CAS 失败（其他协程同时修改了 inUse），重试快速路径
	}

	// ---- 慢速路径：加锁后加入 FIFO 等待队列 ----
	a.mu.Lock()

	// 修复 SEM-01：重新检查槽位是否可用。
	// 在 CAS 失败到获取锁的这段时间窗口内，可能有其他协程已经释放了槽位，
	// 如果不重新检查就直接入队等待，可能会因为错过唤醒信号而永久阻塞。
	if a.inUse.Load()+int32(n) <= a.maxConc.Load() {
		// 槽位已可用，直接占用并返回
		a.inUse.Add(int32(n))
		a.mu.Unlock()
		// 即使获取到了槽位，仍需检查上下文是否已取消
		if ctx.Err() != nil {
			// 上下文已取消，归还刚占用的槽位
			a.inUse.Add(-int32(n))
			return ctx.Err()
		}
		return nil
	}

	// 槽位仍然不足，创建 waiter 加入 FIFO 等待队列
	ch := make(chan struct{}, 1)
	a.waiters = append(a.waiters, waiter{ch: ch, n: n})
	a.mu.Unlock()

	// 阻塞等待，直到被唤醒或上下文取消
	select {
	case <-ctx.Done():
		// 上下文已取消，尝试从等待队列中移除自己
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
			// 已被 Release 唤醒（槽位已预留），但我们不会使用它，需要归还
			a.inUse.Add(-int32(n))
		}
		return ctx.Err()
	case <-ch:
		// 被 Release/setMaxConc 唤醒，槽位已被预留，直接返回
		return nil
	}
}

// Release 释放 1 个并发槽位，等同于 ReleaseN(1)。
func (a *adaptiveSemaphore) Release() {
	a.ReleaseN(1)
}

// ReleaseN 释放 n 个并发槽位，并尝试唤醒 FIFO 队列头部的等待者。
// 唤醒逻辑：从队列头部开始，依次检查是否有足够的空闲槽位来满足等待者，
// 如果有则为该等待者预留槽位（原子增加 inUse）并发送唤醒信号。
// 每次唤醒后重新获取锁以检查下一个等待者，保证在持锁状态下修改 inUse 的原子性。
//
// Param:
//   - n: int - 要释放的并发槽位数，必须大于 0
func (a *adaptiveSemaphore) ReleaseN(n int) {
	// 先原子地减少 inUse（不使用锁，保证快速归还）
	a.inUse.Add(-int32(n))

	// 尝试唤醒等待队列中可以被满足的协程
	a.mu.Lock()
	for len(a.waiters) > 0 {
		w := a.waiters[0]
		// 检查剩余容量是否足以满足队头等待者的需求
		if a.inUse.Load()+int32(w.n) > a.maxConc.Load() {
			break // 容量不足，停止唤醒
		}
		a.waiters = a.waiters[1:]
		// 原子地为该等待者预留槽位（在持锁状态下执行，保证与检查的原子性）
		a.inUse.Add(int32(w.n))
		a.mu.Unlock()
		// 发送唤醒信号（channel 有 1 个缓冲区，不会阻塞）
		w.ch <- struct{}{}
		a.mu.Lock()
	}
	a.mu.Unlock()
}

// setMaxConc 更新最大并发容量，并尝试唤醒因容量不足而阻塞的等待者。
// 当管理员动态调大并发上限时，原先因容量不足而等待的协程可能现在可以被满足。
//
// Param:
//   - n: int - 新的最大并发容量
func (a *adaptiveSemaphore) setMaxConc(n int) {
	a.maxConc.Store(int32(n))

	// 容量变更后，尝试唤醒等待队列中可以被满足的协程（逻辑同 ReleaseN）
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
