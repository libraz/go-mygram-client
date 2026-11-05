package mygram

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// Deadline handling (client.go): a connection whose deadline cannot be set is
// unusable, so it is closed and the failure is classified by whether the
// command had already been written.

// deadlineFailConn wraps a net.Conn and forces SetWriteDeadline and/or
// SetReadDeadline to fail, to exercise the deadline-failure paths that a real
// socket only hits when it is already closed or reset.
type deadlineFailConn struct {
	net.Conn
	failWrite bool
	failRead  bool
	closed    bool
}

var errDeadline = errors.New("simulated deadline failure")

func (d *deadlineFailConn) SetWriteDeadline(t time.Time) error {
	if d.failWrite {
		return errDeadline
	}
	return d.Conn.SetWriteDeadline(t)
}

func (d *deadlineFailConn) SetReadDeadline(t time.Time) error {
	if d.failRead {
		return errDeadline
	}
	return d.Conn.SetReadDeadline(t)
}

func (d *deadlineFailConn) Close() error {
	d.closed = true
	return d.Conn.Close()
}

// newConnectedClient dials a mock server that echoes a fixed response and
// returns the client with its connection swapped for wrap(conn).
func newConnectedClient(t *testing.T, wrap func(net.Conn) net.Conn) *Client {
	t.Helper()
	addr, cleanup := mockServer(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte("OK RESULTS 0\r\n"))
	})
	t.Cleanup(cleanup)

	c := NewClient(addr)
	c.config.MaxQueryLength = 0
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if wrap != nil {
		c.conn = wrap(c.conn)
	}
	return c
}

func TestWriteDeadlineFailureClosesConnAndMarksBeforeSend(t *testing.T) {
	var wrapped *deadlineFailConn
	c := newConnectedClient(t, func(conn net.Conn) net.Conn {
		wrapped = &deadlineFailConn{Conn: conn, failWrite: true}
		return wrapped
	})

	_, err := c.SendCommand("INFO")
	ce, ok := err.(*ConnectionError)
	if !ok {
		t.Fatalf("err = %T (%v), want *ConnectionError", err, err)
	}
	if !ce.beforeSend {
		t.Error("write-deadline failure must set beforeSend=true")
	}
	if !wrapped.closed {
		t.Error("connection must be closed on write-deadline failure")
	}
	if c.IsConnected() {
		t.Error("client must not report connected after write-deadline failure")
	}
}

func TestReadDeadlineFailureClosesConn(t *testing.T) {
	var wrapped *deadlineFailConn
	c := newConnectedClient(t, func(conn net.Conn) net.Conn {
		wrapped = &deadlineFailConn{Conn: conn, failRead: true}
		return wrapped
	})

	_, err := c.SendCommand("INFO")
	ce, ok := err.(*ConnectionError)
	if !ok {
		t.Fatalf("err = %T (%v), want *ConnectionError", err, err)
	}
	if ce.beforeSend {
		t.Error("read-deadline failure must keep beforeSend=false (command was sent)")
	}
	if !wrapped.closed {
		t.Error("connection must be closed on read-deadline failure")
	}
	if c.IsConnected() {
		t.Error("client must not report connected after read-deadline failure")
	}
}

func TestSendCommandCanceledContextFailsFast(t *testing.T) {
	c := newConnectedClient(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.SendCommandContext(ctx, "INFO")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	// A fail-fast on a canceled context must not have touched the connection.
	if !c.IsConnected() {
		t.Error("connection should remain usable after a fail-fast cancellation")
	}
}

func TestTimeoutForCommand(t *testing.T) {
	c := NewClientWithConfig(ClientConfig{
		TimeoutMs:           100,
		OptimizeTimeoutMs:   200,
		DumpLoadTimeoutMs:   300,
		DumpVerifyTimeoutMs: 400,
	})
	cases := []struct {
		command string
		want    int
	}{
		{"OPTIMIZE", 200},
		{"optimize", 200},
		{"Optimize articles", 200},
		{"OPTIMIZE　articles", 200},
		{"DUMP LOAD /tmp/x", 300},
		{"dump   load", 300},
		{"Dump\tVerify f", 400},
		{"dump verify f", 400},
		{"DUMP SAVE", 100},
		{"DUMP INFO f", 100},
		{"DUMP", 100},
		{`SEARCH t "optimize"`, 100},
		{"SEARCH t optimize", 100},
		{"OPTIMIZED", 100},
		{"", 100},
	}
	for _, tc := range cases {
		if got := c.timeoutForCommand(tc.command); got != tc.want {
			t.Errorf("timeoutForCommand(%q) = %d, want %d", tc.command, got, tc.want)
		}
	}
}
