package mygram

import (
	"context"
	"net"
	"testing"
	"time"
)

// Construction, connection lifecycle, response framing and timeout plumbing
// (client.go). Command construction lives in the client_*_test.go files
// grouped by command family.

func TestNewClient(t *testing.T) {
	t.Run("host:port format", func(t *testing.T) {
		c := NewClient("localhost:11016")
		if c.config.Host != "localhost" {
			t.Errorf("Host = %q, want %q", c.config.Host, "localhost")
		}
		if c.config.Port != 11016 {
			t.Errorf("Port = %d, want %d", c.config.Port, 11016)
		}
	})

	t.Run("default config", func(t *testing.T) {
		c := NewClient("127.0.0.1:11016")
		if c.config.TimeoutMs != 5000 {
			t.Errorf("TimeoutMs = %d, want 5000", c.config.TimeoutMs)
		}
		if c.config.RecvBufferSize != 65536 {
			t.Errorf("RecvBufferSize = %d, want 65536", c.config.RecvBufferSize)
		}
		if c.config.MaxQueryLength != 128 {
			t.Errorf("MaxQueryLength = %d, want 128", c.config.MaxQueryLength)
		}
	})
}

func TestNewClientWithConfig(t *testing.T) {
	c := NewClientWithConfig(ClientConfig{
		Host:      "mygramdb",
		Port:      12345,
		TimeoutMs: 10000,
	})
	if c.config.Host != "mygramdb" {
		t.Errorf("Host = %q, want %q", c.config.Host, "mygramdb")
	}
	if c.config.Port != 12345 {
		t.Errorf("Port = %d, want 12345", c.config.Port)
	}
	if c.config.TimeoutMs != 10000 {
		t.Errorf("TimeoutMs = %d, want 10000", c.config.TimeoutMs)
	}
	// Default values should be applied
	if c.config.RecvBufferSize != 65536 {
		t.Errorf("RecvBufferSize = %d, want 65536", c.config.RecvBufferSize)
	}
}

func TestIsConnected(t *testing.T) {
	c := NewClient("localhost:11016")
	if c.IsConnected() {
		t.Error("should not be connected")
	}
}

func TestSendCommandNotConnected(t *testing.T) {
	c := NewClient("localhost:11016")
	_, err := c.SendCommand("INFO")
	if err == nil {
		t.Error("expected error")
	}
	if _, ok := err.(*ConnectionError); !ok {
		t.Errorf("expected ConnectionError, got %T", err)
	}
}

func TestConnectToNonexistentServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	c := NewClient(addr)
	c.config.TimeoutMs = 500
	err = c.Connect()
	if err == nil {
		t.Error("expected error")
	}
}

func TestClientServerError(t *testing.T) {
	addr, cleanup := mockServer(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)

		_, _ = conn.Write([]byte("ERROR Table not found: nonexistent\r\n"))
	})
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()

	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	_, err := c.Search("nonexistent", "test", SearchOptions{})
	if err == nil {
		t.Error("expected error")
	}
	serverErr, ok := err.(*ServerError)
	if !ok {
		t.Fatalf("expected ServerError, got %T: %v", err, err)
	}
	if serverErr.Message != "Table not found: nonexistent" {
		t.Errorf("Message = %q, want %q", serverErr.Message, "Table not found: nonexistent")
	}
}

// TestV18DefaultKeepAlive documents the v1.8 client-alignment defaults: TCP
// keep-alive is on by default with a 60s idle period, uniformly for NewClient
// and NewClientWithConfig (the zero value of DisableTCPKeepAlive leaves it on).
func TestV18DefaultKeepAlive(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.DisableTCPKeepAlive {
		t.Error("DefaultConfig should leave keep-alive enabled")
	}
	if cfg.TCPKeepAliveIdle != 60*time.Second {
		t.Errorf("TCPKeepAliveIdle = %v, want 60s", cfg.TCPKeepAliveIdle)
	}
	// NewClientWithConfig keeps keep-alive on and fills a zero idle from the
	// default, so a config-built client matches a NewClient one.
	c := NewClientWithConfig(ClientConfig{Host: "127.0.0.1", Port: 11016})
	if c.config.DisableTCPKeepAlive {
		t.Error("NewClientWithConfig should keep keep-alive enabled by default")
	}
	if c.config.TCPKeepAliveIdle != 60*time.Second {
		t.Errorf("TCPKeepAliveIdle = %v, want 60s default", c.config.TCPKeepAliveIdle)
	}
}

// TestV18KeepAliveDialSucceeds verifies the keep-alive dial path connects
// normally against a live server.
func TestV18KeepAliveDialSucceeds(t *testing.T) {
	addr, _, cleanup := poolMockServer(t, echoSearchServer)
	defer cleanup()

	c := clientFor(t, addr, ClientConfig{TimeoutMs: 500, TCPKeepAliveIdle: 30 * time.Second})
	if err := c.Connect(); err != nil {
		t.Fatalf("keep-alive dial failed: %v", err)
	}
	if _, err := c.SendCommand("SEARCH threads k LIMIT 1"); err != nil {
		t.Fatalf("command after keep-alive dial failed: %v", err)
	}
}

// TestV18KeepAliveDisabledDialSucceeds verifies the opt-out branch: with
// DisableTCPKeepAlive set, the dialer skips the keep-alive override and still
// connects normally.
func TestV18KeepAliveDisabledDialSucceeds(t *testing.T) {
	addr, _, cleanup := poolMockServer(t, echoSearchServer)
	defer cleanup()

	c := clientFor(t, addr, ClientConfig{TimeoutMs: 500, DisableTCPKeepAlive: true})
	if err := c.Connect(); err != nil {
		t.Fatalf("dial with keep-alive disabled failed: %v", err)
	}
	if _, err := c.SendCommand("SEARCH threads k LIMIT 1"); err != nil {
		t.Fatalf("command after dial failed: %v", err)
	}
}

func TestConnectTimeoutIsSeparateFromCommandTimeout(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TimeoutMs = 5000
	cfg.ConnectTimeoutMs = 40
	cfg.Host, cfg.Port = "192.0.2.1", 11016 // TEST-NET-1: routed nowhere

	c := NewClientWithConfig(cfg)
	t.Cleanup(func() { _ = c.Close() })

	start := time.Now()
	err := c.ConnectContext(context.Background())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected the dial to fail")
	}
	if elapsed > 2*time.Second {
		t.Errorf("dial took %v; ConnectTimeoutMs was not applied", elapsed)
	}
}

func TestLongOperationsOutliveTheCommandTimeout(t *testing.T) {
	// A server that answers only after the ordinary command timeout has passed:
	// the long-operation budget is what keeps these commands alive.
	const serverDelay = 250 * time.Millisecond
	cases := []struct {
		name  string
		reply string
		call  func(*Client) error
	}{
		{"DUMP LOAD", "OK LOADED /tmp/dump.bin\r\n", func(c *Client) error {
			return c.DumpLoadContext(context.Background(), "/tmp/dump.bin")
		}},
		{"DUMP VERIFY", "OK VERIFIED /tmp/dump.bin\r\n", func(c *Client) error {
			_, err := c.DumpVerifyContext(context.Background(), "/tmp/dump.bin")
			return err
		}},
		{"OPTIMIZE", "OK OPTIMIZED\r\n", func(c *Client) error {
			return c.OptimizeContext(context.Background(), "articles")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ln.Close() })
			go func() {
				conn, acceptErr := ln.Accept()
				if acceptErr != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				buf := make([]byte, 4096)
				if _, readErr := conn.Read(buf); readErr != nil {
					return
				}
				time.Sleep(serverDelay)
				_, _ = conn.Write([]byte(tc.reply))
			}()

			cfg := DefaultConfig()
			cfg.TimeoutMs = 50 // far below serverDelay
			c := connectScripted(t, cfg, ln.Addr().String())
			if err := tc.call(c); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
		})
	}
}
