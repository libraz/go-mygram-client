package mygram

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// Error types and their interoperation with the standard library (errors.go).

func TestTimeoutErrorSatisfiesNetError(t *testing.T) {
	var err error = &TimeoutError{Message: "command timeout"}

	var ne net.Error
	if !errors.As(err, &ne) {
		t.Fatal("TimeoutError should satisfy net.Error")
	}
	if !ne.Timeout() {
		t.Error("net.Error.Timeout() should be true for a TimeoutError")
	}
	// Check Temporary via the concrete type: net.Error.Temporary is deprecated,
	// so calling it through the interface would trip staticcheck, but the method
	// must still exist for net.Error to be satisfied.
	if (&TimeoutError{}).Temporary() {
		t.Error("Temporary() should be false for a command timeout")
	}
}

func TestTimeoutErrorWrapsContextDeadline(t *testing.T) {
	err := error(&TimeoutError{Message: "command timeout", Err: context.DeadlineExceeded})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Error("errors.Is(err, context.DeadlineExceeded) should match when Err is set")
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Error("a context-caused timeout should still present as a net.Error timeout")
	}
}

func TestTimeoutErrorWithoutCauseDoesNotMatchDeadline(t *testing.T) {
	err := error(&TimeoutError{Message: "command timeout"})
	if errors.Is(err, context.DeadlineExceeded) {
		t.Error("a config-driven timeout must not report context.DeadlineExceeded")
	}
}

func TestCommandTimeoutFromContextWrapsDeadline(t *testing.T) {
	c := NewClient(silentServer(t))
	c.config.MaxQueryLength = 0
	c.config.TimeoutMs = 5000 // larger than the context deadline below
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	_, err := c.SearchContext(ctx, "threads", "k", SearchOptions{Limit: 1})

	var te *TimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("err = %T (%v), want *TimeoutError", err, err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Error("a context-deadline command timeout should match context.DeadlineExceeded")
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Error("command timeout should present as a net.Error timeout")
	}
}

func TestCommandTimeoutFromConfigDoesNotWrapDeadline(t *testing.T) {
	c := NewClient(silentServer(t))
	c.config.MaxQueryLength = 0
	c.config.TimeoutMs = 60 // fires before any context deadline
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })

	// No deadline on the context: the config TimeoutMs must drive the timeout.
	_, err := c.SearchContext(context.Background(), "threads", "k", SearchOptions{Limit: 1})

	var te *TimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("err = %T (%v), want *TimeoutError", err, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Error("a config-driven timeout must not report context.DeadlineExceeded")
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Error("config timeout should still present as a net.Error timeout")
	}
}

func TestServerErrorSurfacesCode(t *testing.T) {
	c, _ := connectRecording(t, "ERROR 4007 Table not found: ghosts\r\n")

	_, err := c.Search("ghosts", "x", SearchOptions{})
	se, ok := err.(*ServerError)
	if !ok {
		t.Fatalf("expected *ServerError, got %T (%v)", err, err)
	}
	if se.Code != ErrCodeTableNotFound {
		t.Errorf("Code = %d, want %d", se.Code, ErrCodeTableNotFound)
	}
	if se.Message != "Table not found: ghosts" {
		t.Errorf("Message = %q", se.Message)
	}
	if !strings.Contains(se.Error(), "code 4007") {
		t.Errorf("Error() = %q, want it to name the code", se.Error())
	}

	code, ok := ServerErrorCode(err)
	if !ok || code != ErrCodeTableNotFound {
		t.Errorf("ServerErrorCode = (%d, %v)", code, ok)
	}
}
