// history.go 实现 AI 调用历史记录，使用环形缓冲区存储热数据并支持持久化后端。
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
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

// CallRecord 表示单次 AI Provider 调用的完整记录。
type CallRecord struct {
	ID           string                       `json:"id"`
	Provider     string                       `json:"provider"`
	Model        string                       `json:"model"`
	Request      core.ChatCompletionRequest   `json:"request"`
	Response     *core.ChatCompletionResponse `json:"response,omitempty"`
	Error        string                       `json:"error,omitempty"`
	LatencyMs    int64                        `json:"latency_ms"`
	Timestamp    time.Time                    `json:"timestamp"`
	Tags         []string                     `json:"tags,omitempty"`
	FallbackFrom string                       `json:"fallback_from,omitempty"`
	Cost         float64                      `json:"cost"`
	Currency     string                       `json:"currency"`
}

// History 跟踪最近的 AI Provider 调用记录，提供线程安全的访问。
// 内部使用固定大小的环形缓冲区（ring buffer）存储热数据，
// 可选配置 core.Storage 后端实现长期持久化存储。
type History struct {
	mu      sync.RWMutex
	buf     []CallRecord // 固定大小的环形缓冲区
	head    int          // 下一个写入位置的索引（最新元素位于 head-1）
	count   int          // buf 中有效元素的数量
	maxSize int
	storage core.Storage // 可选的持久化后端
}

// NewHistory 创建一个最多保留 maxSize 条记录的 History。
//
// Param:
//   - maxSize: int - 最大保留记录数，<= 0 时默认为 100
//
// Return:
//   - *History: 新创建的 History 实例
func NewHistory(maxSize int) *History {
	if maxSize <= 0 {
		maxSize = 100
	}
	return &History{
		buf:     make([]CallRecord, maxSize),
		maxSize: maxSize,
	}
}

// SetStorage 挂载持久化存储后端，应在首次调用 Record 之前设置。
//
// Param:
//   - s: core.Storage - 持久化存储后端实例
func (h *History) SetStorage(s core.Storage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.storage = s
}

// Record 追加一条调用记录。当缓冲区已满时，最旧的记录会被覆盖（环形缓冲区语义）。
// 如果已配置持久化后端，记录会异步写入（不阻塞调用方）。
//
// Param:
//   - r: CallRecord - 待记录的调用记录
func (h *History) Record(r CallRecord) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.buf[h.head] = r
	h.head = (h.head + 1) % h.maxSize
	if h.count < h.maxSize {
		h.count++
	}

	if h.storage != nil {
		// 异步持久化，避免阻塞调用方
		go h.persistRecord(r)
	}
}

// persistRecord 将单条记录异步写入持久化后端，使用 3 秒超时。
func (h *History) persistRecord(r CallRecord) {
	cr := core.CallRecord{
		ID:           r.ID,
		Provider:     r.Provider,
		Model:        r.Model,
		Request:      r.Request,
		Response:     r.Response,
		Error:        r.Error,
		LatencyMs:    r.LatencyMs,
		Timestamp:    r.Timestamp,
		Tags:         r.Tags,
		FallbackFrom: r.FallbackFrom,
		Cost:         r.Cost,
		Currency:     r.Currency,
	}
	// 使用带超时的后台上下文，避免依赖调用方的生命周期
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := h.storage.RecordCall(ctx, cr); err != nil {
		core.GetLogger().Error("history persist failed", "error", err)
	}
}

// at 返回逻辑索引 i 处的元素，其中 0 表示最新的记录。
// 调用时必须持有读锁。
func (h *History) at(i int) CallRecord {
	// 最新元素位于 (h.head - 1)，依次递减
	idx := (h.head - 1 - i + h.maxSize) % h.maxSize
	return h.buf[idx]
}

// Records 返回所有已存储的记录（按时间降序，最新在前）。
// 如果已配置持久化后端，优先从数据库查询完整历史；
// 查询失败时回退到内存中的环形缓冲区数据。
//
// Return:
//   - []CallRecord: 记录列表（副本），修改不影响内部状态
func (h *History) Records() []CallRecord {
	h.mu.RLock()
	s := h.storage
	h.mu.RUnlock()

	if s != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		crs, err := s.GetCalls(ctx, h.maxSize)
		if err == nil {
			return h.fromCoreCalls(crs)
		}
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]CallRecord, h.count)
	for i := 0; i < h.count; i++ {
		result[i] = h.at(i)
	}
	return result
}

// RecordsByTag 返回按标签过滤的记录（按时间降序，最新在前）。
// 优先查询持久化后端，失败时回退到内存缓冲区。
//
// Param:
//   - tag: string - 要匹配的标签名
//
// Return:
//   - []CallRecord: 匹配的记录列表
func (h *History) RecordsByTag(tag string) []CallRecord {
	h.mu.RLock()
	s := h.storage
	h.mu.RUnlock()

	if s != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		crs, err := s.GetCallsByTag(ctx, tag, h.maxSize)
		if err == nil {
			return h.fromCoreCalls(crs)
		}
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]CallRecord, 0, h.count/4+1)
	for i := 0; i < h.count; i++ {
		r := h.at(i)
		for _, t := range r.Tags {
			if t == tag {
				result = append(result, r)
				break
			}
		}
	}
	return result
}

// RecordsByProvider 返回指定 Provider 的调用记录（按时间降序，最新在前）。
// 优先查询持久化后端，失败时回退到内存缓冲区。
//
// Param:
//   - name: string - Provider 名称
//
// Return:
//   - []CallRecord: 匹配的记录列表
func (h *History) RecordsByProvider(name string) []CallRecord {
	h.mu.RLock()
	s := h.storage
	h.mu.RUnlock()

	if s != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		crs, err := s.GetCallsByProvider(ctx, name, h.maxSize)
		if err == nil {
			return h.fromCoreCalls(crs)
		}
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]CallRecord, 0, h.count/4+1)
	for i := 0; i < h.count; i++ {
		r := h.at(i)
		if r.Provider == name {
			result = append(result, r)
		}
	}
	return result
}

// fromCoreCalls 将 core.CallRecord 切片转换为 engine.CallRecord 切片。
func (h *History) fromCoreCalls(crs []core.CallRecord) []CallRecord {
	result := make([]CallRecord, len(crs))
	for i, cr := range crs {
		result[i] = CallRecord{
			ID:           cr.ID,
			Provider:     cr.Provider,
			Model:        cr.Model,
			Request:      cr.Request,
			Response:     cr.Response,
			Error:        cr.Error,
			LatencyMs:    cr.LatencyMs,
			Timestamp:    cr.Timestamp,
			Tags:         cr.Tags,
			FallbackFrom: cr.FallbackFrom,
			Cost:         cr.Cost,
			Currency:     cr.Currency,
		}
	}
	return result
}

// Clear 清空所有内存中的记录，同时置零引用以辅助 GC 回收。
// 不会清除持久化后端中的数据。
func (h *History) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.head = 0
	h.count = 0
	// 置零引用，辅助 GC 回收
	for i := range h.buf {
		h.buf[i] = CallRecord{}
	}
}

// Stats 返回按 Provider 聚合的统计信息。
// 如果已配置持久化后端，优先从数据库查询聚合统计；
// 查询失败时回退到内存缓冲区中的数据。
//
// Return:
//   - map[string]ProviderStats: 以 Provider 名称为键的统计数据
func (h *History) Stats() map[string]ProviderStats {
	h.mu.RLock()
	s := h.storage
	h.mu.RUnlock()

	if s != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		coreStats, err := s.GetProviderStats(ctx)
		if err == nil {
			return h.fromCoreStats(coreStats)
		}
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	stats := make(map[string]ProviderStats)
	for i := 0; i < h.count; i++ {
		r := h.at(i)
		st := stats[r.Provider]
		st.Provider = r.Provider
		st.TotalCalls++
		st.TotalLatencyMs += r.LatencyMs
		if r.Error != "" {
			st.FailedCalls++
		}
		if r.LatencyMs > st.MaxLatencyMs {
			st.MaxLatencyMs = r.LatencyMs
		}
		st.TotalCost += r.Cost
		if st.Currency == "" && r.Currency != "" {
			st.Currency = r.Currency
		}
		stats[r.Provider] = st
	}

	for name, st := range stats {
		if st.TotalCalls > 0 {
			st.AvgLatencyMs = st.TotalLatencyMs / int64(st.TotalCalls)
		}
		stats[name] = st
	}
	return stats
}

// fromCoreStats 将 core.ProviderStats 映射转换为 engine.ProviderStats。
func (h *History) fromCoreStats(css map[string]core.ProviderStats) map[string]ProviderStats {
	stats := make(map[string]ProviderStats)
	for name, cs := range css {
		stats[name] = ProviderStats{
			Provider:     cs.Provider,
			TotalCalls:   cs.TotalCalls,
			TotalCost:    cs.TotalCost,
			Currency:     cs.Currency,
			AvgLatencyMs: cs.AvgLatency,
			FailedCalls:  cs.ErrorCount,
		}
	}
	return stats
}

// ProviderStats 存储单个 Provider 的聚合统计数据。
type ProviderStats struct {
	Provider       string  `json:"provider"`
	TotalCalls     int     `json:"total_calls"`
	FailedCalls    int     `json:"failed_calls"`
	TotalLatencyMs int64   `json:"total_latency_ms"`
	AvgLatencyMs   int64   `json:"avg_latency_ms"`
	MaxLatencyMs   int64   `json:"max_latency_ms"`
	TotalCost      float64 `json:"total_cost"`
	Currency       string  `json:"currency"`
}
