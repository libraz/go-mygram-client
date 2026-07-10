package mygram

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

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

// silentServer accepts a connection and reads the command but never replies,
// so the client's read blocks until its deadline fires.
func silentServer(t *testing.T) string {
	t.Helper()
	addr, cleanup := mockServer(t, func(conn net.Conn) {
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)
		// Hold the connection open without responding.
		time.Sleep(2 * time.Second)
		_ = conn.Close()
	})
	t.Cleanup(cleanup)
	return addr
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
