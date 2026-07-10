package mygram

import "fmt"

// ConnectionError is an error returned when a connection fails.
type ConnectionError struct {
	Message string
	Err     error

	// beforeSend reports whether the failure occurred before the command
	// could reach the server (not connected, or a write-phase failure). When
	// true the server never observed the command, so a retry on a fresh
	// connection is safe even for non-idempotent commands. Set within the
	// package and consumed by the connection pool's retry logic.
	beforeSend bool
}

func (e *ConnectionError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("connection error: %s: %v", e.Message, e.Err)
	}
	return fmt.Sprintf("connection error: %s", e.Message)
}

func (e *ConnectionError) Unwrap() error { return e.Err }

// ProtocolError is an error returned when a protocol violation occurs.
type ProtocolError struct {
	Message string
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("protocol error: %s", e.Message)
}

// TimeoutError is an error returned when an operation times out.
//
// It satisfies net.Error (Timeout reports true), so existing code that checks
// for timeouts via net.Error keeps working. When the timeout was caused by the
// caller's context deadline, Err is context.DeadlineExceeded and is exposed via
// Unwrap, so errors.Is(err, context.DeadlineExceeded) matches.
type TimeoutError struct {
	Message string

	// Err is the underlying cause, if any (for example context.DeadlineExceeded
	// when the caller's context deadline elapsed). It is nil for a timeout
	// driven purely by the configured TimeoutMs.
	Err error

	// beforeSend reports whether the timeout occurred before the command was
	// written to the socket (a connect-phase dial timeout). Such a failure sent
	// nothing, so a fresh dial is safe to retry for any command; a post-send
	// read timeout leaves this false because the server may have acted on it.
	beforeSend bool
}

func (e *TimeoutError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("timeout: %s: %v", e.Message, e.Err)
	}
	return fmt.Sprintf("timeout: %s", e.Message)
}

func (e *TimeoutError) Unwrap() error { return e.Err }

// Timeout reports that this error represents a timeout, satisfying net.Error.
func (e *TimeoutError) Timeout() bool { return true }

// Temporary satisfies net.Error. A command timeout is not treated as a
// transient condition (matching os.ErrDeadlineExceeded), so it returns false.
func (e *TimeoutError) Temporary() bool { return false }

// ValidationError is an error returned when input validation fails.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error: %s", e.Message)
}

// ServerError is an error returned when the server responds with ERROR.
type ServerError struct {
	Message string
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("server error: %s", e.Message)
}

// CircuitOpenError is returned by a Pool whose circuit breaker is open (or
// half-open with a trial already in flight). It is raised without touching the
// network so a caller fails fast instead of piling requests onto a server that
// is already unreachable. It is not classified as a network failure, so it does
// not itself drive the breaker.
type CircuitOpenError struct {
	Message string
}

func (e *CircuitOpenError) Error() string {
	return fmt.Sprintf("circuit breaker open: %s", e.Message)
}
