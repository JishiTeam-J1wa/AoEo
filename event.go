package aoeo

// EventEmitter is an optional interface for receiving scheduler events.
// Implementations can be attached to a Client to receive progress notifications.
type EventEmitter interface {
	// Emit sends an event with the given topic and data payload.
	// Implementations should be safe for concurrent use.
	Emit(topic string, data ...any)
}

// NopEmitter is a no-op EventEmitter for use in tests or headless environments.
type NopEmitter struct{}

func (NopEmitter) Emit(string, ...any) {}

// Event topics used by the scheduler.
const (
	EventProviderFail    = "provider:fail"
	EventProviderOpen    = "provider:open"
	EventProviderRecover = "provider:recover"
	EventFallbackTrigger = "scheduler:fallback"
	EventAuditDisagree   = "audit:disagree"
)
