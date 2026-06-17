package core

import "testing"

func TestNopEmitter_Emit(t *testing.T) {
	e := NopEmitter{}
	// Should not panic with no arguments.
	e.Emit("test-topic")
	// Should not panic with arguments.
	e.Emit("test-topic", "data1", 42)
}

func TestEventTopicConstants(t *testing.T) {
	// Verify event topic constants have expected values.
	if EventProviderFail != "provider:fail" {
		t.Fatalf("unexpected EventProviderFail: %s", EventProviderFail)
	}
	if EventProviderOpen != "provider:open" {
		t.Fatalf("unexpected EventProviderOpen: %s", EventProviderOpen)
	}
	if EventProviderRecover != "provider:recover" {
		t.Fatalf("unexpected EventProviderRecover: %s", EventProviderRecover)
	}
	if EventFallbackTrigger != "scheduler:fallback" {
		t.Fatalf("unexpected EventFallbackTrigger: %s", EventFallbackTrigger)
	}
	if EventAuditDisagree != "audit:disagree" {
		t.Fatalf("unexpected EventAuditDisagree: %s", EventAuditDisagree)
	}
	if EventDualComplete != "scheduler:dual" {
		t.Fatalf("unexpected EventDualComplete: %s", EventDualComplete)
	}
}
