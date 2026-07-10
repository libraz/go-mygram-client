package mygram

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

func clientFor(t *testing.T, addr string, cfg ClientConfig) *Client {
	t.Helper()
	cfg.Host, cfg.Port = splitHostPort(t, addr)
	cfg.MaxQueryLength = 0
	c := NewClientWithConfig(cfg)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestClientAutoReconnectResendsWhenDeadBeforeSend(t *testing.T) {
	addr, accepts, cleanup := poolMockServer(t, echoSearchServer)
	defer cleanup()

	c := clientFor(t, addr, ClientConfig{TimeoutMs: 500, AutoReconnect: true})
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	// Simulate a socket that died between commands: the next send finds no
	// connection and must transparently reconnect and resend.
	_ = c.Close()

	resp, err := c.SendCommand("SEARCH threads k LIMIT 1")
	if err != nil {
		t.Fatalf("auto-reconnect did not resend: %v", err)
	}
	if !strings.Contains(resp, "OK RESULTS") {
		t.Errorf("response = %q, want an OK RESULTS reply", resp)
	}
	if got := accepts(); got != 2 {
		t.Errorf("accepted %d connections, want 2 (initial + reconnect)", got)
	}
}

func TestClientWithoutAutoReconnectFailsWhenDead(t *testing.T) {
	addr, accepts, cleanup := poolMockServer(t, echoSearchServer)
	defer cleanup()

	c := clientFor(t, addr, ClientConfig{TimeoutMs: 500}) // AutoReconnect defaults to false
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()

	_, err := c.SendCommand("SEARCH threads k LIMIT 1")
	if _, ok := err.(*ConnectionError); !ok {
		t.Fatalf("err = %T (%v), want *ConnectionError", err, err)
	}
	if got := accepts(); got != 1 {
		t.Errorf("accepted %d connections, want 1 (no reconnect without AutoReconnect)", got)
	}
}

// holdServer reads one request and then holds the connection open without
// replying, so the client's read deadline elapses (a read-phase failure).
func holdServer(idx int, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)
	if _, err := br.ReadString('\n'); err != nil {
		return
	}
	time.Sleep(time.Second) // outlast the client's command timeout
}

func TestClientAutoReconnectDoesNotResendAfterSend(t *testing.T) {
	addr, accepts, cleanup := poolMockServer(t, holdServer)
	defer cleanup()

	c := clientFor(t, addr, ClientConfig{TimeoutMs: 150, AutoReconnect: true})
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	// The request was written and then timed out waiting for a reply. Because
	// the server may have acted on it, it must NOT be resent.
	_, err := c.SendCommand("SEARCH threads k LIMIT 1")
	if _, ok := err.(*TimeoutError); !ok {
		t.Fatalf("err = %T (%v), want *TimeoutError", err, err)
	}
	if got := accepts(); got != 1 {
		t.Errorf("accepted %d connections, want 1 (a post-send failure must not resend)", got)
	}
}
