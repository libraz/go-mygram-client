package mygram

import (
	"sync"
	"time"
)

// Default circuit-breaker trip settings.
const (
	defaultFailureThreshold = 5
	defaultResetTimeout     = 10 * time.Second
)

// CircuitBreakerConfig configures a Pool's circuit breaker. Consecutive
// connect/timeout failures up to FailureThreshold open the breaker; it stays
// open for ResetTimeout, then admits a single trial (half-open) before closing
// on success or re-opening on failure. Set PoolConfig.CircuitBreaker to enable
// it; a nil pointer disables it.
type CircuitBreakerConfig struct {
	// FailureThreshold is the number of consecutive network failures that open
	// the breaker. 0 or negative selects the default (5).
	FailureThreshold int

	// ResetTimeout is how long the breaker stays open before admitting a trial.
	// 0 or negative selects the default (10s).
	ResetTimeout time.Duration
}

// isNetworkFailure reports whether err indicates the server is unreachable and
// should therefore count toward tripping the breaker. Pool-control failures
// (pool closed, breaker open) and server-side rejections (ServerError,
// ValidationError, ProtocolError) do not count: the server is still reachable.
func isNetworkFailure(err error) bool {
	if err == nil {
		return false
	}
	if err == errPoolClosedSentinel {
		return false
	}
	switch err.(type) {
	case *ConnectionError, *TimeoutError:
		return true
	default:
		return false
	}
}

// circuitBreaker is a three-state breaker (closed → open → half-open →
// closed/open). It is safe for concurrent use.
type circuitBreaker struct {
	failureThreshold int
	resetTimeout     time.Duration
	onStateChange    func(state string)

	mu               sync.Mutex
	state            string // "closed", "open", or "half_open"
	failures         int
	openedAt         time.Time
	halfOpenInFlight bool
}

// newCircuitBreaker builds a breaker from cfg, applying defaults for zero
// fields. onStateChange is invoked (outside the lock) on every state
// transition, and may be nil.
func newCircuitBreaker(cfg CircuitBreakerConfig, onStateChange func(state string)) *circuitBreaker {
	threshold := cfg.FailureThreshold
	if threshold <= 0 {
		threshold = defaultFailureThreshold
	}
	reset := cfg.ResetTimeout
	if reset <= 0 {
		reset = defaultResetTimeout
	}
	return &circuitBreaker{
		failureThreshold: threshold,
		resetTimeout:     reset,
		onStateChange:    onStateChange,
		state:            "closed",
	}
}

// beforeRequest reports whether a request may proceed. It returns a
// CircuitOpenError when the breaker forbids the request. On a permitted
// half-open trial it marks a trial in flight, which the matching record clears.
func (b *circuitBreaker) beforeRequest() error {
	b.mu.Lock()
	transition := ""
	defer func() {
		b.mu.Unlock()
		if transition != "" && b.onStateChange != nil {
			b.onStateChange(transition)
		}
	}()

	switch b.state {
	case "open":
		if time.Since(b.openedAt) >= b.resetTimeout {
			b.state = "half_open"
			b.halfOpenInFlight = true
			transition = "half_open"
			return nil
		}
		return &CircuitOpenError{Message: "breaker is open"}
	case "half_open":
		if b.halfOpenInFlight {
			return &CircuitOpenError{Message: "breaker is half-open; a trial is in progress"}
		}
		b.halfOpenInFlight = true
	}
	// closed: allow through.
	return nil
}

// record updates breaker state from the outcome of a permitted request. A
// network failure in half-open re-opens the breaker; a success closes it. In
// closed state consecutive network failures trip it at FailureThreshold, and
// any success resets the counter.
func (b *circuitBreaker) record(err error) {
	failure := isNetworkFailure(err)

	b.mu.Lock()
	transition := ""
	defer func() {
		b.mu.Unlock()
		if transition != "" && b.onStateChange != nil {
			b.onStateChange(transition)
		}
	}()

	if b.state == "half_open" {
		b.halfOpenInFlight = false
		if failure {
			b.openedAt = time.Now()
			b.state = "open"
			transition = "open"
		} else {
			b.failures = 0
			b.state = "closed"
			transition = "closed"
		}
		return
	}

	if failure {
		b.failures++
		if b.failures >= b.failureThreshold {
			b.openedAt = time.Now()
			b.state = "open"
			transition = "open"
		}
	} else {
		b.failures = 0
	}
}
