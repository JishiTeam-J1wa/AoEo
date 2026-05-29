package core

import (
	"log/slog"
	"os"
	"sync/atomic"
)

// Logger is the structured logging interface used by AoEo.
// It defaults to slog with JSON output. Override via SetLogger.
type Logger interface {
	Debug(msg string, attrs ...any)
	Info(msg string, attrs ...any)
	Warn(msg string, attrs ...any)
	Error(msg string, attrs ...any)
}

// defaultLogger wraps slog for AoEo's Logger interface.
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

// SetLogger replaces the default logger with a custom implementation.
func SetLogger(l Logger) {
	if l == nil {
		return
	}
	aoLogger.Store(&l)
}

// GetLogger returns the current logger.
func GetLogger() Logger {
	return *aoLogger.Load()
}
