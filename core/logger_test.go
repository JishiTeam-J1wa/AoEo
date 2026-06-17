package core

import (
	"testing"
)

type testLogger struct {
	lastMsg   string
	lastAttrs []any
}

func (l *testLogger) Debug(msg string, attrs ...any) { l.lastMsg = msg; l.lastAttrs = attrs }
func (l *testLogger) Info(msg string, attrs ...any)  { l.lastMsg = msg; l.lastAttrs = attrs }
func (l *testLogger) Warn(msg string, attrs ...any)  { l.lastMsg = msg; l.lastAttrs = attrs }
func (l *testLogger) Error(msg string, attrs ...any) { l.lastMsg = msg; l.lastAttrs = attrs }

func TestSetLogger(t *testing.T) {
	l := &testLogger{}
	SetLogger(l)

	if GetLogger() != l {
		t.Fatal("GetLogger should return the set logger")
	}

	GetLogger().Info("test message", "key", "value")
	if l.lastMsg != "test message" {
		t.Fatalf("expected 'test message', got %s", l.lastMsg)
	}
}

func TestSetLogger_Nil(t *testing.T) {
	original := GetLogger()
	SetLogger(nil)
	if GetLogger() != original {
		t.Fatal("SetLogger(nil) should be a no-op")
	}
}

// ========== Additional tests for coverage ==========

func TestGetLogger_ReturnsCurrent(t *testing.T) {
	l1 := &testLogger{}
	SetLogger(l1)
	if GetLogger() != l1 {
		t.Fatal("GetLogger should return l1")
	}

	l2 := &testLogger{}
	SetLogger(l2)
	if GetLogger() != l2 {
		t.Fatal("GetLogger should return l2 after SetLogger")
	}
	if GetLogger() == l1 {
		t.Fatal("GetLogger should no longer return l1")
	}
}

func TestNewDebugLogger(t *testing.T) {
	l := NewDebugLogger()
	if l == nil {
		t.Fatal("NewDebugLogger should return non-nil logger")
	}
	// Verify it implements the Logger interface by calling all methods
	// (should not panic)
	l.Debug("debug msg", "key", "value")
	l.Info("info msg", "key", "value")
	l.Warn("warn msg", "key", "value")
	l.Error("error msg", "key", "value")
}

func TestNewDebugLogger_IsDistinctFromDefault(t *testing.T) {
	debugL := NewDebugLogger()
	SetLogger(debugL)
	if GetLogger() != debugL {
		t.Fatal("GetLogger should return the debug logger after SetLogger")
	}
}

func TestDefaultLogger_Debug(t *testing.T) {
	l := NewDebugLogger()
	// Should not panic
	l.Debug("test debug message")
}

func TestDefaultLogger_Info(t *testing.T) {
	l := NewDebugLogger()
	// Should not panic
	l.Info("test info message")
}

func TestDefaultLogger_Warn(t *testing.T) {
	l := NewDebugLogger()
	// Should not panic
	l.Warn("test warn message")
}

func TestDefaultLogger_Error(t *testing.T) {
	l := NewDebugLogger()
	// Should not panic
	l.Error("test error message")
}

func TestDefaultLogger_WithAttrs(t *testing.T) {
	l := NewDebugLogger()
	// Should not panic with various attribute patterns
	l.Debug("msg", "k1", "v1", "k2", 42)
	l.Info("msg", "k1", "v1")
	l.Warn("msg", "k1", true, "k2", 3.14)
	l.Error("msg", "err", "some error")
}

func TestSetLogger_Overwrite(t *testing.T) {
	l1 := &testLogger{}
	l2 := &testLogger{}

	SetLogger(l1)
	GetLogger().Info("to l1")
	if l1.lastMsg != "to l1" {
		t.Fatalf("expected 'to l1', got %s", l1.lastMsg)
	}

	SetLogger(l2)
	GetLogger().Info("to l2")
	if l2.lastMsg != "to l2" {
		t.Fatalf("expected 'to l2', got %s", l2.lastMsg)
	}
	// l1 should not have received the second message
	if l1.lastMsg != "to l1" {
		t.Fatal("l1 should not have received the second message")
	}
}

func TestTestLogger_AllMethods(t *testing.T) {
	l := &testLogger{}
	l.Debug("d", "a", 1)
	if l.lastMsg != "d" {
		t.Fatalf("Debug: expected 'd', got %s", l.lastMsg)
	}
	l.Info("i", "b", 2)
	if l.lastMsg != "i" {
		t.Fatalf("Info: expected 'i', got %s", l.lastMsg)
	}
	l.Warn("w", "c", 3)
	if l.lastMsg != "w" {
		t.Fatalf("Warn: expected 'w', got %s", l.lastMsg)
	}
	l.Error("e", "d", 4)
	if l.lastMsg != "e" {
		t.Fatalf("Error: expected 'e', got %s", l.lastMsg)
	}
}
