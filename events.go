package mygram

// PoolEvent identifies an observability event emitted by a Pool through
// PoolConfig.OnEvent. The payload map varies by event; see each constant.
type PoolEvent string

const (
	// EventAcquire is emitted when a connection is handed out. Payload:
	// {"wait_seconds": float64} — time the borrow spent waiting (0 if it did
	// not wait).
	EventAcquire PoolEvent = "acquire"

	// EventConnectionDiscarded is emitted when a connection is dropped after
	// failing validation, expiring, or overflowing the idle limit. Empty payload.
	EventConnectionDiscarded PoolEvent = "connection_discarded"

	// EventRetry is emitted before a request retry's backoff. Payload:
	// {"attempt": int, "error": string}.
	EventRetry PoolEvent = "retry"

	// EventBreakerStateChange is emitted when the circuit breaker changes state.
	// Payload: {"state": string} — one of "closed", "open", "half_open".
	EventBreakerStateChange PoolEvent = "breaker_state_change"
)

// emit delivers an event to the configured OnEvent callback, if any. A panic in
// the callback is recovered so instrumentation can never disrupt the pool.
func (p *Pool) emit(event PoolEvent, payload map[string]any) {
	cb := p.cfg.OnEvent
	if cb == nil {
		return
	}
	defer func() { _ = recover() }()
	cb(event, payload)
}
