// Package store 为隐私映射（original <-> fake）提供高性能键值存储。
// 支持 Pebble、Redis 和 SQL 等多种后端实现。
//
// Author: JishiTeam-J1wa
// Created: 2026-06
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package store

import (
	"context"
	"time"
)

// Entry 存储单条可逆的 original-to-fake 映射记录。
type Entry struct {
	SessionID string
	Original  string
	Fake      string
	Type      string // 实体类型的字符串表示，避免循环导入
	CreatedAt time.Time
}

// MappingStore 是隐私映射持久化的抽象接口。
type MappingStore interface {
	// Set 存储双向映射：fake -> original 和 original -> fake。
	Set(ctx context.Context, sessionID, fake, original string, typ string) error

	// GetOriginal 通过伪造值查找对应的原始值。
	GetOriginal(ctx context.Context, sessionID, fake string) (string, bool, error)

	// GetFake 通过原始值查找对应的伪造值。
	GetFake(ctx context.Context, sessionID, original string) (string, bool, error)

	// GetSession 返回指定会话的全部映射记录。
	GetSession(ctx context.Context, sessionID string) ([]Entry, error)

	// DeleteSession 删除指定会话的全部映射记录。
	DeleteSession(ctx context.Context, sessionID string) error

	// Cleanup 删除创建时间早于指定时刻的过期映射。
	Cleanup(ctx context.Context, before time.Time) error

	// Close 关闭底层存储引擎，释放文件句柄、内存缓存和连接池等资源。
	Close() error
}
