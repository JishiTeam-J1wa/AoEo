// Package core 存储抽象接口，定义 AoEo 持久化层的数据模型和操作契约。
// 实现包括 SQLite、MySQL、PostgreSQL 等后端。
//
// Author: JishiTeam-J1wa
// Created: 2026-05
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package core

import (
	"context"
	"time"
)

// Storage 是 AoEo 的持久化存储抽象接口。
//
// 负责调用历史记录、费用统计、审计日志和隐私映射的存储操作。
type Storage interface {
	// ------------------------------------------------------------------
	// 调用历史
	// ------------------------------------------------------------------

	// RecordCall 持久化单条调用记录。
	RecordCall(ctx context.Context, r CallRecord) error

	// GetCalls 返回最近的调用记录，最多 limit 条。
	GetCalls(ctx context.Context, limit int) ([]CallRecord, error)

	// GetCallsByTag 按标签过滤返回调用记录，最多 limit 条。
	GetCallsByTag(ctx context.Context, tag string, limit int) ([]CallRecord, error)

	// GetCallsByProvider 按 Provider 名称过滤返回调用记录，最多 limit 条。
	GetCallsByProvider(ctx context.Context, provider string, limit int) ([]CallRecord, error)

	// GetProviderStats 按 Provider 聚合费用统计信息。
	GetProviderStats(ctx context.Context) (map[string]ProviderStats, error)

	// ------------------------------------------------------------------
	// 审计日志
	// ------------------------------------------------------------------

	// RecordAudit 持久化一条审计事件。
	RecordAudit(ctx context.Context, e AuditEvent) error

	// GetAudits 返回最近的审计事件，最多 limit 条。
	GetAudits(ctx context.Context, limit int) ([]AuditEvent, error)

	// ------------------------------------------------------------------
	// 隐私映射
	// ------------------------------------------------------------------

	// CreateMapping 存储一条原始值到脱敏值的映射关系。
	CreateMapping(ctx context.Context, m PrivacyMapping) error

	// FindFake 在指定会话中查找原始值对应的脱敏值。
	FindFake(ctx context.Context, sessionID, original string) (string, bool, error)

	// FindOriginal 在指定会话中查找脱敏值对应的原始值。
	FindOriginal(ctx context.Context, sessionID, fake string) (string, bool, error)

	// GetMappings 返回指定会话的所有映射关系，按创建时间倒序排列。
	GetMappings(ctx context.Context, sessionID string) ([]PrivacyMapping, error)

	// DeleteMappingsBySession 删除指定会话的所有映射关系。
	DeleteMappingsBySession(ctx context.Context, sessionID string) error

	// CleanupMappings 清除早于指定时间的过期映射关系。
	CleanupMappings(ctx context.Context, before time.Time) error

	// ------------------------------------------------------------------
	// 生命周期
	// ------------------------------------------------------------------

	// Close 释放存储后端持有的资源。
	Close() error
}

// CallRecord 表示单次 AI Provider 调用的完整记录，包含请求、响应、耗时和费用等元数据。
type CallRecord struct {
	ID           string                `json:"id"`
	Provider     string                `json:"provider"`
	Model        string                `json:"model"`
	Request      ChatCompletionRequest `json:"request"`
	Response     *ChatCompletionResponse `json:"response,omitempty"`
	Error        string                `json:"error,omitempty"`
	LatencyMs    int64                 `json:"latency_ms"`
	Timestamp    time.Time             `json:"timestamp"`
	Tags         []string              `json:"tags,omitempty"`
	FallbackFrom string                `json:"fallback_from,omitempty"`
	Cost         float64               `json:"cost"`
	Currency     string                `json:"currency"`
}

// ProviderStats 聚合单个 Provider 的统计信息，包括调用次数、费用、延迟和错误数。
type ProviderStats struct {
	Provider    string  `json:"provider"`
	TotalCalls  int     `json:"total_calls"`
	TotalCost   float64 `json:"total_cost"`
	Currency    string  `json:"currency"`
	AvgLatency  int64   `json:"avg_latency_ms"`
	ErrorCount  int     `json:"error_count"`
}

// AuditEvent 表示一条隐私或安全审计条目。
type AuditEvent struct {
	ID             string    `json:"id"`
	Timestamp      time.Time `json:"timestamp"`
	Stage          string    `json:"stage"`     // 审计阶段："before_request" | "after_stream"
	Type           string    `json:"type"`      // 审计类型："rule_hit" | "pii_detected"
	HitsJSON       string    `json:"hits_json,omitempty"`
	SpansJSON      string    `json:"spans_json,omitempty"`
	Action         string    `json:"action"`
	Provider       string    `json:"provider"`
	Model          string    `json:"model"`
	ContentHash    string    `json:"content_hash"`
	ContentPreview string    `json:"content_preview"`
}

// PrivacyMapping 存储原始值与脱敏值之间的可逆映射关系。
type PrivacyMapping struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"session_id"`
	Original  string    `json:"original"`
	Fake      string    `json:"fake"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
}
