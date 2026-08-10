package mygram

import (
	"strings"
	"testing"
)

// Auto-reconnect (client.go): a command is resent only when the socket was
// found dead before anything was written.

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
	waitAccepts(t, accepts, 2) // initial + reconnect
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
	waitAccepts(t, accepts, 1) // no reconnect without AutoReconnect
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
	waitAccepts(t, accepts, 1) // a post-send failure must not resend
}
