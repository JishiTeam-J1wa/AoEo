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
