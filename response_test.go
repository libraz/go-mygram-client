package mygram

import (
	"net"
	"strings"
	"testing"
)

// TestResponseMayBeCompleteIsNecessary asserts the core invariant that lets
// readResponse skip the completeness scan: whenever isResponseComplete reports
// a complete response, responseMayBeComplete must also be true. If this held
// false, readResponse could hang on a genuinely complete response.
func TestResponseMayBeCompleteIsNecessary(t *testing.T) {
	corpus := []string{
		"OK RESULTS 0\r\n",
		"OK RESULTS 0\n",
		"OK RESULTS 1 42\r\n",
		"OK COUNT 42\r\n",
		"OK DOC 12345 status=1 category=tech\r\n",
		"OK INFO\nversion: 1.0\n\n",
		"OK INFO\r\nversion: 1.0\r\n\r\n",
		"OK REPLICATION\nstatus: running\nEND\n",
		"OK REPLICATION\nstatus: running\nEND",
		"OK REPLICATION\nstatus: running\r\nEND",
		"OK CACHE_STATS\r\n\r\n# Cache\r\nenabled: true\r\nEND\r\n",
		"OK DUMP_INFO /backup/dump.dmp\r\nversion: 2\r\nEND\r\n",
		"OK DUMP_INFO /backup/dump.dmp\r\nversion: 2\r\n\r\n",
		"+OK\nkey: value\n\n",
		"OK CONFIG\nkey: value\n\n",
		"OK FACET 2\nvalue\t10\n\n",
		"ERROR Table not found\r\n",
		"# DEBUG\nquery_time_ms: 1.0\n\n",
		// Partial fragments that must not be considered complete.
		"OK RESULTS ",
		"OK CACHE_STATS\r\nenabled: true",
		"OK DUMP_INFO /backup/dump.dmp\r\nversion: 2\r\n",
		"OK IN",
		"",
	}

	for _, s := range corpus {
		complete := isResponseComplete(s)
		gate := responseMayBeComplete(s)
		if complete && !gate {
			t.Errorf("invariant broken: isResponseComplete(%q)=true but responseMayBeComplete=false", s)
		}
	}
}

// TestResponseGateAgainstTruncations feeds every prefix of a set of complete
// responses through the gate and the oracle, and checks that the gate never
// hides a completion the oracle would report.
func TestResponseGateAgainstTruncations(t *testing.T) {
	complete := []string{
		"OK RESULTS 3 1 2 3\r\n",
		"OK INFO\nversion: 2.0.0\nuptime_seconds: 100\n\n",
		"OK REPLICATION\nstatus: running\nEND\n",
		"OK CACHE_STATS\r\n\r\n# Cache\r\nenabled: true\r\nEND\r\n",
	}
	for _, full := range complete {
		for i := 1; i <= len(full); i++ {
			prefix := full[:i]
			if isResponseComplete(prefix) && !responseMayBeComplete(prefix) {
				t.Fatalf("gate hid a completion at prefix %q of %q", prefix, full)
			}
		}
	}
}

func TestMaxResponseSizeExceeded(t *testing.T) {
	// The server replies with an oversized, unterminated payload.
	big := strings.Repeat("a", 5000)
	addr, cleanup := mockServer(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte(big))
	})
	defer cleanup()

	c := NewClient(addr)
	c.config.MaxQueryLength = 0
	c.config.MaxResponseSize = 1024
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	_, err := c.Search("threads", "k", SearchOptions{Limit: 1})
	if _, ok := err.(*ProtocolError); !ok {
		t.Fatalf("err = %T (%v), want *ProtocolError", err, err)
	}
	// The connection must have been closed after the violation.
	if c.IsConnected() {
		t.Error("connection should be closed after MaxResponseSize violation")
	}
}

func TestMaxResponseSizeUnlimitedByDefault(t *testing.T) {
	// A response larger than the historical buffer must still be accepted when
	// MaxResponseSize is 0.
	body := "OK FACET 1\n" + strings.Repeat("v\t1\n", 5000) + "\n"
	addr, cleanup := mockServer(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte(body))
	})
	defer cleanup()

	c := NewClient(addr)
	c.config.MaxQueryLength = 0
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	resp, err := c.Facet("threads", "cat", FacetOptions{})
	if err != nil {
		t.Fatalf("facet failed: %v", err)
	}
	if len(resp.Results) != 5000 {
		t.Errorf("got %d facet values, want 5000", len(resp.Results))
	}
}
