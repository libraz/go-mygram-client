package mygram

import (
	"net"
	"strings"
	"testing"
)

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

// mockServer starts a mock server for testing and returns the address and cleanup function.
func mockServer(t *testing.T, handler func(conn net.Conn)) (addr string, cleanup func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go handler(conn)
		}
	}()

	return ln.Addr().String(), func() { _ = ln.Close() }
}

func TestClientSearch(t *testing.T) {
	addr, cleanup := mockServer(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		cmd := string(buf[:n])

		if strings.HasPrefix(cmd, "SEARCH") {
			_, _ = conn.Write([]byte("OK RESULTS 2 101 102\r\n"))
		}
	})
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()

	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	resp, err := c.Search("threads", "keyword", SearchOptions{
		Limit:      100,
		SortColumn: "created_at",
		SortDesc:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.TotalCount != 2 {
		t.Errorf("TotalCount = %d, want 2", resp.TotalCount)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("len(Results) = %d, want 2", len(resp.Results))
	}
}

func TestClientCount(t *testing.T) {
	addr, cleanup := mockServer(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		cmd := string(buf[:n])

		if strings.HasPrefix(cmd, "COUNT") {
			_, _ = conn.Write([]byte("OK COUNT 42\r\n"))
		}
	})
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()

	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	resp, err := c.Count("threads", "beauty", CountOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Count != 42 {
		t.Errorf("Count = %d, want 42", resp.Count)
	}
}

func TestClientGet(t *testing.T) {
	addr, cleanup := mockServer(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		cmd := string(buf[:n])

		if strings.HasPrefix(cmd, "GET") {
			_, _ = conn.Write([]byte("OK DOC 12345 status=1 category=tech\r\n"))
		}
	})
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()

	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	doc, err := c.Get("threads", "12345")
	if err != nil {
		t.Fatal(err)
	}
	if doc.PrimaryKey != "12345" {
		t.Errorf("PrimaryKey = %q, want %q", doc.PrimaryKey, "12345")
	}
	if doc.Fields["status"] != "1" {
		t.Errorf("Fields[status] = %q, want %q", doc.Fields["status"], "1")
	}
}

func TestClientInfo(t *testing.T) {
	addr, cleanup := mockServer(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)

		response := "OK INFO\n\n# Server\nversion: 2.0.0\nuptime_seconds: 100\n\n# Index\ntotal_documents: 5000\n\n# Tables\ntables: threads,images\n\n"
		_, _ = conn.Write([]byte(response))
	})
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()

	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	info, err := c.Info()
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "2.0.0" {
		t.Errorf("Version = %q, want %q", info.Version, "2.0.0")
	}
	if info.DocCount != 5000 {
		t.Errorf("DocCount = %d, want 5000", info.DocCount)
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

func TestClientSearchCommandFormat(t *testing.T) {
	var receivedCmd string
	addr, cleanup := mockServer(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		receivedCmd = strings.TrimRight(string(buf[:n]), "\r\n")
		_, _ = conn.Write([]byte("OK RESULTS 0\r\n"))
	})
	defer cleanup()

	c := NewClient(addr)
	c.config.MaxQueryLength = 0 // Unlimited
	defer func() { _ = c.Close() }()

	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	_, err := c.Search("threads", "keyword", SearchOptions{
		Limit:      100,
		Offset:     50,
		AndTerms:   []string{"beauty"},
		NotTerms:   []string{"amateur"},
		Filters:    map[string]string{"status": "1"},
		SortColumn: "created_at",
		SortDesc:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify the command contains all required elements
	expectedParts := []string{
		"SEARCH threads keyword",
		"AND beauty",
		"NOT amateur",
		"FILTER status = 1",
		"SORT created_at DESC",
		"LIMIT 50,100",
	}
	for _, part := range expectedParts {
		if !strings.Contains(receivedCmd, part) {
			t.Errorf("command %q does not contain %q", receivedCmd, part)
		}
	}
}

func TestClientValidation(t *testing.T) {
	c := NewClient("localhost:11016")

	t.Run("empty table name", func(t *testing.T) {
		_, err := c.Search("", "test", SearchOptions{})
		if err == nil {
			t.Error("expected error")
		}
		if _, ok := err.(*ValidationError); !ok {
			t.Errorf("expected ValidationError, got %T", err)
		}
	})

	t.Run("query with control characters", func(t *testing.T) {
		_, err := c.Search("threads", "bad\x00query", SearchOptions{})
		if err == nil {
			t.Error("expected error")
		}
	})

	t.Run("query length exceeded", func(t *testing.T) {
		longQuery := strings.Repeat("a", 200)
		_, err := c.Search("threads", longQuery, SearchOptions{})
		if err == nil {
			t.Error("expected error")
		}
		if _, ok := err.(*ValidationError); !ok {
			t.Errorf("expected ValidationError, got %T", err)
		}
	})
}

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

// captureSearchCommand connects to a mock server, runs Search with the given
// query+opts, and returns the wire-form command the server received.
func captureSearchCommand(t *testing.T, query string, opts SearchOptions) string {
	t.Helper()
	var receivedCmd string
	addr, cleanup := mockServer(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		receivedCmd = string(buf[:n])
		_, _ = conn.Write([]byte("OK RESULTS 0\r\n"))
	})
	defer cleanup()

	c := NewClient(addr)
	c.config.MaxQueryLength = 0
	defer func() { _ = c.Close() }()

	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	if _, err := c.Search("threads", query, opts); err != nil {
		t.Fatal(err)
	}
	return receivedCmd
}

func TestSearchOffsetOnlyEmitsBareOffset(t *testing.T) {
	cmd := captureSearchCommand(t, "hello", SearchOptions{Offset: 42})
	if !strings.Contains(cmd, "OFFSET 42") {
		t.Errorf("expected bare OFFSET 42 in command, got %q", cmd)
	}
	if strings.Contains(cmd, "LIMIT") {
		t.Errorf("expected no LIMIT clause, got %q", cmd)
	}
}

func TestSearchOffsetAndLimitEmitsCombinedForm(t *testing.T) {
	cmd := captureSearchCommand(t, "hello", SearchOptions{Limit: 10, Offset: 20})
	if !strings.Contains(cmd, "LIMIT 20,10") {
		t.Errorf("expected LIMIT 20,10 in command, got %q", cmd)
	}
}

func TestSearchBothZeroEmitsDefaultLimit(t *testing.T) {
	cmd := captureSearchCommand(t, "hello", SearchOptions{})
	if !strings.Contains(cmd, "LIMIT 1000") {
		t.Errorf("expected default LIMIT 1000, got %q", cmd)
	}
	if strings.Contains(cmd, "OFFSET") {
		t.Errorf("expected no OFFSET, got %q", cmd)
	}
}

func TestSearchEmptyQueryQuotedOnWire(t *testing.T) {
	cmd := captureSearchCommand(t, "", SearchOptions{Limit: 10})
	// The query token between table and the next clause must be the explicit ""
	if !strings.Contains(cmd, `SEARCH threads ""`) {
		t.Errorf("expected SEARCH threads \"\" in command, got %q", cmd)
	}
}

func TestSearchQueryWithSpaceQuoted(t *testing.T) {
	cmd := captureSearchCommand(t, "hello world", SearchOptions{Limit: 10})
	if !strings.Contains(cmd, `SEARCH threads "hello world"`) {
		t.Errorf("expected quoted multi-word query, got %q", cmd)
	}
}

func TestSearchFilterValueWithSpaceQuoted(t *testing.T) {
	cmd := captureSearchCommand(t, "hi", SearchOptions{
		Limit:   10,
		Filters: map[string]string{"title": "hello world"},
	})
	if !strings.Contains(cmd, `FILTER title = "hello world"`) {
		t.Errorf("expected quoted filter value, got %q", cmd)
	}
}

func TestSearchAndTermWithSpaceQuoted(t *testing.T) {
	cmd := captureSearchCommand(t, "hi", SearchOptions{
		Limit:    10,
		AndTerms: []string{"machine learning"},
	})
	if !strings.Contains(cmd, `AND "machine learning"`) {
		t.Errorf("expected quoted AND term, got %q", cmd)
	}
}

func TestSearchRejectsFilterKeyWithWhitespace(t *testing.T) {
	c := NewClient("127.0.0.1:1") // unused; validation runs before connect
	_, err := c.Search("threads", "hi", SearchOptions{
		Limit:   10,
		Filters: map[string]string{"bad key": "v"},
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T", err)
	}
}

func TestSearchRejectsSortColumnWithWhitespace(t *testing.T) {
	c := NewClient("127.0.0.1:1")
	_, err := c.Search("threads", "hi", SearchOptions{
		Limit:      10,
		SortColumn: "bad column",
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T", err)
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
