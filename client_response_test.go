package mygram

import (
	"net"
	"strings"
	"testing"
	"time"
)

// Response reading (client.go): the completeness gate that decides when a
// frame has fully arrived, and the MaxResponseSize bound.

func TestIsResponseComplete(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		complete bool
	}{
		{"single line CRLF", "OK RESULTS 0\r\n", true},
		{"single line LF", "OK RESULTS 0\n", true},
		{"double LF", "OK INFO\nversion: 1.0\n\n", true},
		{"double CRLF", "OK INFO\r\nversion: 1.0\r\n\r\n", true},
		{"incomplete", "OK RESULTS ", false},
		{"REPLICATION with END", "OK REPLICATION\nstatus: running\nEND\n", true},
		{"REPLICATION with END and newline", "OK REPLICATION\nstatus: running\nEND\n\n", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isResponseComplete(tt.data)
			if result != tt.complete {
				t.Errorf("isResponseComplete(%q) = %v, want %v", tt.data, result, tt.complete)
			}
		})
	}
}

func TestIsResponseCompleteFirstLinePrefixOnly(t *testing.T) {
	// First-line prefix matching: a response whose first line is OK RESULTS
	// must not be treated as DUMP_STATUS just because the substring appears
	// later in the payload.
	tests := []struct {
		name string
		data string
		want bool
	}{
		{
			"DUMP_INFO with filepath END-terminated complete",
			"OK DUMP_INFO /backup/dump.dmp\r\nversion: 2\r\nEND\r\n",
			true,
		},
		{
			"DUMP_INFO incomplete without END or blank line",
			"OK DUMP_INFO /backup/dump.dmp\r\nversion: 2\r\n",
			false,
		},
		{
			"DUMP_INFO with blank-line termination",
			"OK DUMP_INFO /backup/dump.dmp\r\nversion: 2\r\n\r\n",
			true,
		},
		{
			"OK RESULTS with DUMP_STATUS-looking PK is single-line complete",
			"OK RESULTS 1 OK_DUMP_STATUS_pk\r\n",
			true,
		},
		{
			"OK CACHE_STATS with END",
			"OK CACHE_STATS\r\n\r\n# Cache\r\nenabled: true\r\nEND\r\n",
			true,
		},
		{
			"OK CACHE_STATS incomplete",
			"OK CACHE_STATS\r\nenabled: true",
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isResponseComplete(tt.data); got != tt.want {
				t.Errorf("isResponseComplete(%q) = %v, want %v", tt.data, got, tt.want)
			}
		})
	}
}

func TestIsResponseCompleteFacet(t *testing.T) {
	cases := []struct {
		name     string
		data     string
		complete bool
	}{
		{"empty facet terminated", "OK FACET 0\r\n\r\n", true},
		{"facet with rows terminated", "OK FACET 1\r\nfoo\t1\r\n\r\n", true},
		{"facet with rows not yet terminated", "OK FACET 1\r\nfoo\t1\r\n", false},
		{"facet header only", "OK FACET 1\r\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isResponseComplete(tc.data)
			if got != tc.complete {
				t.Errorf("isResponseComplete(%q) = %v, want %v", tc.data, got, tc.complete)
			}
		})
	}
}

func TestIsResponseCompleteSyncStatus(t *testing.T) {
	// END-terminated with a single trailing CRLF (no blank line).
	single := "OK SYNC_STATUS\r\narticles idle\r\nEND\r\n"
	if !isResponseComplete(single) {
		t.Error("single-CRLF SYNC_STATUS not recognized as complete")
	}
	// END-terminated with an extra blank line.
	doubled := "OK SYNC_STATUS\r\narticles idle\r\nEND\r\n\r\n"
	if !isResponseComplete(doubled) {
		t.Error("blank-line-terminated SYNC_STATUS not recognized as complete")
	}
	// Incomplete: header only.
	if isResponseComplete("OK SYNC_STATUS\r\n") {
		t.Error("header-only SYNC_STATUS wrongly recognized as complete")
	}
}

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
	body := "OK FACET 5000\n" + strings.Repeat("v\t1\n", 5000) + "\n"
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

// TestSearchHighlightResponseChunked delivers a SEARCH...HIGHLIGHT reply in
// two writes, with the first ending exactly at the header line's "\r\n" --
// the chunk boundary that used to make the client return a highlight-less
// response, since a SEARCH/COUNT header reads identically whether or not a
// highlight/debug body follows it (client.go isResponseCompleteFor).
func TestSearchHighlightResponseChunked(t *testing.T) {
	addr, cleanup := mockServer(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4096)
		if _, err := conn.Read(buf); err != nil {
			return
		}
		if _, err := conn.Write([]byte("OK RESULTS 1\r\n")); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
		_, _ = conn.Write([]byte("42\t<em>kw</em>\r\n\r\n"))
	})
	defer cleanup()

	c := NewClient(addr)
	c.config.MaxQueryLength = 0
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	resp, err := c.Search("threads", "kw", SearchOptions{Highlight: &HighlightOptions{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("len(Results) = %d, want 1", len(resp.Results))
	}
	if resp.Results[0].PrimaryKey != "42" || resp.Results[0].Snippet != "<em>kw</em>" {
		t.Errorf("Results[0] = %+v", resp.Results[0])
	}
}

// TestSearchDebugResponseChunked delivers a SEARCH reply under DEBUG ON in
// two writes, split so the second write opens with the DEBUG block's own
// "\r\n\r\n# DEBUG\r\n" marker -- itself a blank line, which the client must
// not mistake for the frame's terminator before the marker has fully
// arrived (client.go isResponseCompleteFor's expectDebugMarker gate).
func TestSearchDebugResponseChunked(t *testing.T) {
	addr, cleanup := mockServer(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4096)

		if _, err := conn.Read(buf); err != nil { // DEBUG ON
			return
		}
		if _, err := conn.Write([]byte("OK\r\n")); err != nil {
			return
		}

		if _, err := conn.Read(buf); err != nil { // SEARCH
			return
		}
		if _, err := conn.Write([]byte("OK RESULTS 1 42\r\n")); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
		_, _ = conn.Write([]byte("\r\n# DEBUG\r\nquery_time: 1.0\r\n\r\n"))
	})
	defer cleanup()

	c := NewClient(addr)
	c.config.MaxQueryLength = 0
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	if err := c.EnableDebug(); err != nil {
		t.Fatal(err)
	}

	resp, err := c.Search("threads", "kw", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 || resp.Results[0].PrimaryKey != "42" {
		t.Fatalf("Results = %+v", resp.Results)
	}
	if resp.Debug == nil || resp.Debug.QueryTimeMs != 1.0 {
		t.Errorf("Debug = %+v", resp.Debug)
	}
}

// TestConnectIPv6Loopback connects to a server listening on the IPv6
// loopback address, exercising net.JoinHostPort's bracketing of a literal
// IPv6 host (client.go dial) rather than a hostname that happens to resolve
// to one.
func TestConnectIPv6Loopback(t *testing.T) {
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				buf := make([]byte, 4096)
				if _, readErr := c.Read(buf); readErr != nil {
					return
				}
				_, _ = c.Write([]byte("OK RESULTS 1 42\r\n"))
			}(conn)
		}
	}()

	host, port := splitHostPort(t, ln.Addr().String())
	c := NewClientWithConfig(ClientConfig{Host: host, Port: port, MaxQueryLength: 0})
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatalf("connect over IPv6: %v", err)
	}

	resp, err := c.Search("threads", "kw", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 || resp.Results[0].PrimaryKey != "42" {
		t.Fatalf("Results = %+v", resp.Results)
	}
}
