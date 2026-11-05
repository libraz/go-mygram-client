package mygram

import (
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Response reading (client.go): the completeness gate that decides when a
// frame has fully arrived, and the MaxResponseSize bound.

// frameComplete reports whether data is exactly one complete reply to command.
func frameComplete(command string, debug bool, data string) bool {
	return isResponseComplete(data, beginResponseFrame(command, debug))
}

func TestIsResponseComplete(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		debug    bool
		data     string
		complete bool
	}{
		{"single line", "COUNT t x", false, "OK COUNT 0\r\n", true},
		{"search header only", "SEARCH t x", false, "OK RESULTS 0\r\n", true},
		{"incomplete line", "SEARCH t x", false, "OK RESULTS ", false},
		{"error ends any frame", "SEARCH t x", true, "ERROR 4007 Table not found: t\r\n", true},
		{"INFO", "INFO", false, "OK INFO\r\n\r\n# Server\r\nversion: 1.0\r\n\r\nEND\r\n", true},
		{"INFO without END", "INFO", false, "OK INFO\r\n\r\n# Server\r\nversion: 1.0\r\n\r\n", false},
		{"REPLICATION", "REPLICATION STATUS", false, "OK REPLICATION\r\nstatus: running\r\nEND\r\n", true},
		{"REPLICATION before END", "REPLICATION STATUS", false, "OK REPLICATION\r\nstatus: running\r\n", false},
		{"CACHE_STATS", "CACHE STATS", false, "OK CACHE_STATS\r\n\r\n# Cache\r\nenabled: true\r\nEND\r\n", true},
		{"CACHE_STATS incomplete", "CACHE STATS", false, "OK CACHE_STATS\r\nenabled: true", false},
		{"DUMP_STATUS", "DUMP STATUS", false, "OK DUMP_STATUS\r\nstatus: IDLE\r\nEND\r\n", true},
		{"DUMP_INFO with path", "DUMP INFO f", false, "OK DUMP_INFO /backup/dump.dmp\r\nversion: 2\r\nEND\r\n", true},
		{"DUMP_INFO without END", "DUMP INFO f", false, "OK DUMP_INFO /backup/dump.dmp\r\nversion: 2\r\n", false},
		{"OK RESULTS with INFO-looking key", "SEARCH t x", false, "OK RESULTS 1 OK_DUMP_STATUS_pk\r\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := frameComplete(tt.command, tt.debug, tt.data); got != tt.complete {
				t.Errorf("complete(%q, %q) = %v, want %v", tt.command, tt.data, got, tt.complete)
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
		{"with total, terminated", "OK FACET 1 5\r\nfoo\t1\r\n\r\n\r\n", true},
		{"with total, one blank line", "OK FACET 1 5\r\nfoo\t1\r\n\r\n", false},
		{"empty with total", "OK FACET 0 0\r\n\r\n\r\n", true},
		{"without total ends at first blank line", "OK FACET 1\r\nfoo\t1\r\n\r\n", true},
		{"without total, rows only", "OK FACET 1\r\nfoo\t1\r\n", false},
		{"header only", "OK FACET 1 1\r\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := frameComplete("FACET t c", false, tc.data); got != tc.complete {
				t.Errorf("complete(%q) = %v, want %v", tc.data, got, tc.complete)
			}
		})
	}
}

func TestIsResponseCompleteSyncStatus(t *testing.T) {
	// The body ends in "END\r\n" and the transport CRLF adds one more.
	if !frameComplete("SYNC STATUS", false, "OK SYNC_STATUS\r\nstatus=IDLE\r\nEND\r\n\r\n") {
		t.Error("SYNC_STATUS with its transport CRLF not recognized as complete")
	}
	if frameComplete("SYNC STATUS", false, "OK SYNC_STATUS\r\nstatus=IDLE\r\nEND\r\n") {
		t.Error("SYNC_STATUS without its transport CRLF wrongly recognized as complete")
	}
	if frameComplete("SYNC STATUS", false, "OK SYNC_STATUS\r\n") {
		t.Error("header-only SYNC_STATUS wrongly recognized as complete")
	}
}

func TestIsResponseCompleteConfig(t *testing.T) {
	cases := []struct {
		name     string
		command  string
		data     string
		complete bool
	}{
		{"CONFIG SHOW", "CONFIG SHOW api", "+OK\r\nbind: \"127.0.0.1\"\r\n\r\n", true},
		{"CONFIG SHOW incomplete", "CONFIG SHOW api", "+OK\r\nbind: \"127.0.0.1\"\r\n", false},
		{"CONFIG SHOW empty value", "CONFIG SHOW x", "+OK\r\n\r\n\r\n", true},
		{"CONFIG HELP body continues past its inner blank line", "CONFIG HELP api.x", "+OK\r\napi.x\r\n\r\nType: integer\r\n", false},
		{"CONFIG HELP full text", "config help api.x", "+OK\r\napi.x\r\n\r\nType: integer\r\nDescription: d\r\n\r\n", true},
		{"CONFIG HELP error", "CONFIG HELP nope", "ERROR 8 Configuration path not found: nope\r\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := frameComplete(tc.command, false, tc.data); got != tc.complete {
				t.Errorf("complete(%q, %q) = %v, want %v", tc.command, tc.data, got, tc.complete)
			}
		})
	}
}

func TestIsResponseCompleteShowVariables(t *testing.T) {
	table := "+---+---+\r\n| a | b |\r\n+---+---+\r\n\r\n"
	if !frameComplete("SHOW VARIABLES", false, table) {
		t.Error("SHOW VARIABLES table not recognized as complete")
	}
	if frameComplete("SHOW VARIABLES", false, "+---+---+\r\n| a | b |\r\n") {
		t.Error("partial SHOW VARIABLES table wrongly recognized as complete")
	}
}

func TestIsResponseCompleteSearchTail(t *testing.T) {
	cases := []struct {
		name     string
		command  string
		debug    bool
		data     string
		complete bool
	}{
		{"highlight header only", "SEARCH t kw HIGHLIGHT", false, "OK RESULTS 1\r\n", false},
		{"highlight rows", "SEARCH t kw HIGHLIGHT", false, "OK RESULTS 1\r\n42\tx\r\n\r\n", true},
		{"highlight with quoted keyword only is plain", "SEARCH t \"highlight\"", false, "OK RESULTS 1 42\r\n", true},
		{"highlight keyword inside quoted AND term is plain", "SEARCH t kw AND \"a highlight b\"", false, "OK RESULTS 1 42\r\n", true},
		{"lowercase highlight", "search t kw highlight", false, "OK RESULTS 1\r\n42\tx\r\n\r\n", true},
		{"highlight with primary keys on header is plain", "SEARCH t kw HIGHLIGHT", false, "OK RESULTS 1 42\r\n", true},
		{"highlight error returns at once", "SEARCH t kw HIGHLIGHT", false, "ERROR 4 HIGHLIGHT requires normalized text\r\n", true},
		{"debug header only", "SEARCH t kw", true, "OK RESULTS 1 42\r\n", false},
		{"debug marker boundary", "SEARCH t kw", true, "OK RESULTS 1 42\r\n\r\n", false},
		{"debug block", "SEARCH t kw", true, "OK RESULTS 1 42\r\n\r\n# DEBUG\r\nquery_time: 1.0\r\n\r\n", true},
		{"debug error returns at once", "SEARCH t kw", true, "ERROR 4007 Table not found: t\r\n", true},
		{"debug count header only", "COUNT t kw", true, "OK COUNT 3\r\n", false},
		{"debug count block", "COUNT t kw", true, "OK COUNT 3\r\n\r\n# DEBUG\r\nquery_time: 1.0\r\n\r\n", true},
		{"debug does not affect other commands", "GET t 1", true, "OK DOC 1 a=b\r\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := frameComplete(tc.command, tc.debug, tc.data); got != tc.complete {
				t.Errorf("complete(%q, debug=%v, %q) = %v, want %v", tc.command, tc.debug, tc.data, got, tc.complete)
			}
		})
	}
}

// TestIsResponseCompleteIncremental feeds every prefix of complete replies
// through one frame and checks that completion is first reported exactly at
// the final byte, wherever the chunk boundaries fall.
func TestIsResponseCompleteIncremental(t *testing.T) {
	cases := []struct {
		command string
		debug   bool
		full    string
	}{
		{"SEARCH t k", false, "OK RESULTS 3 1 2 3\r\n"},
		{"SEARCH t k HIGHLIGHT", true, "OK RESULTS 1\r\n42\tx\r\n\r\n\r\n# DEBUG\r\nquery_time: 1.0\r\n\r\n"},
		{"INFO", false, "OK INFO\r\n\r\n# Server\r\nversion: 2.0.0\r\n\r\nEND\r\n"},
		{"FACET t c", false, "OK FACET 1 3\r\nv\t1\r\n\r\n\r\n"},
		{"SYNC STATUS", false, "OK SYNC_STATUS\r\nstatus=IDLE\r\nEND\r\n\r\n"},
		{"CONFIG HELP a.b", false, "+OK\r\na.b\r\n\r\nType: integer\r\n\r\n"},
	}
	for _, tc := range cases {
		frame := beginResponseFrame(tc.command, tc.debug)
		for i := 1; i <= len(tc.full); i++ {
			got := isResponseComplete(tc.full[:i], frame)
			if want := i == len(tc.full); got != want {
				t.Fatalf("%q: complete at %d/%d bytes = %v, want %v", tc.command, i, len(tc.full), got, want)
			}
		}
	}
}

// TestIsResponseCompleteSkipsLeadingBlankLines covers the CRLF a FACET reply
// without a distinct-value total leaves behind: it leads the next reply, which
// must still be framed from its own status line.
func TestIsResponseCompleteSkipsLeadingBlankLines(t *testing.T) {
	frame := beginResponseFrame("COUNT t k", false)
	data := "\r\nOK COUNT 7\r\n"
	if !isResponseComplete(data, frame) {
		t.Fatal("reply after a leftover blank line not recognized as complete")
	}
	if got := data[frame.frameStart:]; got != "OK COUNT 7\r\n" {
		t.Errorf("frame = %q, want the status line only", got)
	}
}

func TestTokenizeRequest(t *testing.T) {
	cases := []struct {
		command string
		want    []requestToken
	}{
		{"SEARCH  t   x", []requestToken{{text: "SEARCH"}, {text: "t"}, {text: "x"}}},
		{"SEARCH\u3000t\u00a0x\u0085y", []requestToken{{text: "SEARCH"}, {text: "t"}, {text: "x"}, {text: "y"}}},
		{"a\u200bb", []requestToken{{text: "a\u200bb"}}},
		{`a "b c" d`, []requestToken{{text: "a"}, {text: "b c", quoted: true}, {text: "d"}}},
		{`a"b c"d`, []requestToken{{text: "a"}, {text: "b c", quoted: true}, {text: "d"}}},
		{`'x y' "p\"q"`, []requestToken{{text: "x y", quoted: true}, {text: `p\"q`, quoted: true}}},
		{`"open`, []requestToken{{text: "open", quoted: true}}},
		// A lone 0xA0 continuation byte inside a UTF-8 sequence is not whitespace.
		{"\u00e0x", []requestToken{{text: "\u00e0x"}}},
		{"\xe3\x80\xa0x", []requestToken{{text: "\xe3\x80\xa0x"}}},
	}
	for _, tc := range cases {
		got := tokenizeRequest(tc.command)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("tokenizeRequest(%q) = %+v, want %+v", tc.command, got, tc.want)
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
	body := "OK FACET 5000\r\n" + strings.Repeat("v\t1\r\n", 5000) + "\r\n"
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
// highlight/debug body follows it (frame.go isResponseComplete).
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
// arrived (frame.go isResponseComplete counts the DEBUG block's blank lines).
func TestSearchDebugResponseChunked(t *testing.T) {
	addr, cleanup := mockServer(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4096)

		if _, err := conn.Read(buf); err != nil { // DEBUG ON
			return
		}
		if _, err := conn.Write([]byte("OK DEBUG_ON\r\n")); err != nil {
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

// TestDebugFlagFollowsAcknowledgementOfRawCommand covers DEBUG ON/OFF sent
// through SendCommand rather than EnableDebug: the next SEARCH must still be
// framed with its trailing debug block.
func TestDebugFlagFollowsAcknowledgementOfRawCommand(t *testing.T) {
	_, addr := newScriptedServer(t, "",
		"OK DEBUG_ON\r\n",
		"OK RESULTS 1 42\r\n\r\n# DEBUG\r\nquery_time: 1.0\r\n\r\n",
		"OK DEBUG_OFF\r\n",
		"OK RESULTS 1 42\r\n",
	)
	cfg := DefaultConfig()
	cfg.MaxQueryLength = 0
	cfg.TimeoutMs = 1000
	c := connectScripted(t, cfg, addr)

	if _, err := c.SendCommand("debug on"); err != nil {
		t.Fatal(err)
	}
	resp, err := c.Search("threads", "kw", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Debug == nil || resp.Debug.QueryTimeMs != 1.0 {
		t.Errorf("Debug = %+v", resp.Debug)
	}

	if _, err := c.SendCommand("DEBUG OFF"); err != nil {
		t.Fatal(err)
	}
	resp, err = c.Search("threads", "kw", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Debug != nil || len(resp.Results) != 1 {
		t.Errorf("response = %+v", resp)
	}
}

// TestFailingSearchReturnsItsErrorAtOnce covers a HIGHLIGHT or DEBUG search
// the server rejects: the ERROR line ends the frame, so the client must not
// wait for a body that never follows.
func TestFailingSearchReturnsItsErrorAtOnce(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxQueryLength = 0
	cfg.TimeoutMs = 1000

	t.Run("highlight", func(t *testing.T) {
		_, addr := newScriptedServer(t, "ERROR 4 HIGHLIGHT requires normalized text in the document store\r\n")
		c := connectScripted(t, cfg, addr)
		start := time.Now()
		_, err := c.Search("threads", "kw", SearchOptions{Highlight: &HighlightOptions{}})
		if code, ok := ServerErrorCode(err); !ok || code != ErrCodeNotImplemented {
			t.Fatalf("err = %v, want server error 4", err)
		}
		if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
			t.Errorf("error took %v to surface", elapsed)
		}
	})

	t.Run("debug", func(t *testing.T) {
		_, addr := newScriptedServer(t, "", "OK DEBUG_ON\r\n", "ERROR 4007 Table not found: threads\r\n")
		c := connectScripted(t, cfg, addr)
		if err := c.EnableDebug(); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		_, err := c.Search("threads", "kw", SearchOptions{})
		if code, ok := ServerErrorCode(err); !ok || code != ErrCodeTableNotFound {
			t.Fatalf("err = %v, want server error 4007", err)
		}
		if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
			t.Errorf("error took %v to surface", elapsed)
		}
	})
}

// TestQuotedHighlightTermIsNotAClause covers a term that merely contains the
// word "highlight": the reply is a plain header and must not be awaited as
// highlight rows.
func TestQuotedHighlightTermIsNotAClause(t *testing.T) {
	_, addr := newScriptedServer(t, "OK RESULTS 1 42\r\n")
	cfg := DefaultConfig()
	cfg.MaxQueryLength = 0
	cfg.TimeoutMs = 1000
	c := connectScripted(t, cfg, addr)

	for _, query := range []string{"highlight", "a highlight b"} {
		resp, err := c.Search("threads", query, SearchOptions{})
		if err != nil {
			t.Fatalf("%q: %v", query, err)
		}
		if len(resp.Results) != 1 {
			t.Errorf("%q: Results = %+v", query, resp.Results)
		}
	}
}

func TestConfigHelpReturnsFullText(t *testing.T) {
	text := "+OK\r\napi.default_limit\r\n\r\nType: integer\r\nDefault: 100\r\n\r\n"
	_, addr := newScriptedServer(t, text)
	cfg := DefaultConfig()
	cfg.TimeoutMs = 1000
	c := connectScripted(t, cfg, addr)

	got, err := c.SendCommand("CONFIG HELP api.default_limit")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Default: 100") {
		t.Errorf("response truncated at the inner blank line: %q", got)
	}
}

// TestFacetWithoutTotalLeavesALeadingBlankLine covers the CRLF that follows a
// FACET reply without a distinct-value total: it arrives ahead of the next
// reply, which must be framed from its own status line.
func TestFacetWithoutTotalLeavesALeadingBlankLine(t *testing.T) {
	_, addr := newScriptedServer(t, "", "OK FACET 1\r\nfoo\t2\r\n\r\n\r\n", "OK COUNT 7\r\n")
	cfg := DefaultConfig()
	cfg.MaxQueryLength = 0
	cfg.TimeoutMs = 1000
	c := connectScripted(t, cfg, addr)

	if _, err := c.Facet("threads", "cat", FacetOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := c.Count("threads", "kw", CountOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Count != 7 {
		t.Errorf("Count = %d, want 7", got.Count)
	}
}
