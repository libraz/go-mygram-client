package mygram

import "fmt"

// ConnectionError is an error returned when a connection fails.
type ConnectionError struct {
	Message string
	Err     error
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
type TimeoutError struct {
	Message string
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("timeout: %s", e.Message)
}

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
