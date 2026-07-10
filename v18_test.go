package mygram

import (
	"testing"
	"time"
)

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

// TestV18SearchRawVerbatim confirms the v1.8 wire change: a boolean expression
// is sent unquoted so the server parses AND/OR/NOT and grouping.
func TestV18SearchRawVerbatim(t *testing.T) {
	c, srv := connectRecording(t, "OK RESULTS 1 42\r\n")
	if _, err := c.SearchRaw("articles", "python OR (ruby AND rails)", SearchRawOptions{Limit: 50}); err != nil {
		t.Fatal(err)
	}
	want := "SEARCH articles python OR (ruby AND rails) LIMIT 50"
	if got := srv.lastCommand(); got != want {
		t.Errorf("wire command = %q, want %q (verbatim, unquoted)", got, want)
	}
}

// TestV18FacetKeepsHashValue confirms the v1.8 FACET fix: a value that starts
// with '#' but carries a tab is data, while a tab-less '#' line is a comment.
func TestV18FacetKeepsHashValue(t *testing.T) {
	resp := "OK FACET 2\r\n#machinelearning\t9\r\n# query_time_ms: 1.2\r\n#ai\t4\r\n"
	parsed, err := parseFacetResponse(resp)
	if err != nil {
		t.Fatalf("parseFacetResponse: %v", err)
	}
	want := []FacetValue{{Value: "#machinelearning", Count: 9}, {Value: "#ai", Count: 4}}
	if len(parsed.Results) != len(want) {
		t.Fatalf("results = %+v, want %+v", parsed.Results, want)
	}
	for i := range want {
		if parsed.Results[i] != want[i] {
			t.Fatalf("results = %+v, want %+v", parsed.Results, want)
		}
	}
}
