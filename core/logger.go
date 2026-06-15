// Package core 日志抽象层，提供结构化日志接口及默认 slog 实现。
//
// Author: JishiTeam-J1wa
// Created: 2026-05
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package core

import (
	"log/slog"
	"os"
	"sync/atomic"
)

// Logger 是 AoEo 使用的结构化日志接口。
//
// 默认实现为 slog JSON 输出，可通过 SetLogger 替换为自定义实现。
type Logger interface {
	Debug(msg string, attrs ...any)
	Info(msg string, attrs ...any)
	Warn(msg string, attrs ...any)
	Error(msg string, attrs ...any)
}

// defaultLogger 将 slog.Logger 适配为 AoEo 的 Logger 接口。
type defaultLogger struct {
	inner *slog.Logger
}

func (l *defaultLogger) Debug(msg string, attrs ...any) { l.inner.Debug(msg, attrs...) }
func (l *defaultLogger) Info(msg string, attrs ...any)  { l.inner.Info(msg, attrs...) }
func (l *defaultLogger) Warn(msg string, attrs ...any)  { l.inner.Warn(msg, attrs...) }
func (l *defaultLogger) Error(msg string, attrs ...any) { l.inner.Error(msg, attrs...) }

var aoLogger atomic.Pointer[Logger]

func init() {
	var l Logger = &defaultLogger{
		inner: slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
	aoLogger.Store(&l)
}

// SetLogger 将默认日志替换为自定义实现。
//
// Param:
//   - l: Logger - 自定义日志实例，传 nil 时忽略
func SetLogger(l Logger) {
	if l == nil {
		return
	}
	aoLogger.Store(&l)
}

// GetLogger 返回当前使用的日志实例。
func GetLogger() Logger {
	return *aoLogger.Load()
}

// NewDebugLogger 创建一个输出到 Stderr 的 Debug 级别日志器，适合开发调试。
//
// Return:
//   - Logger: Debug 级别的 JSON 格式日志实例
func NewDebugLogger() Logger {
	return &defaultLogger{
		inner: slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}
