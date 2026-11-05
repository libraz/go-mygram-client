package mygram

import (
	"errors"
	"net"
	"strings"
	"testing"
)

// Wire form and response handling of the query commands SEARCH, COUNT, FACET
// and GET (client.go).

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

// TestSearchUnicodeWhitespaceTermQuoted guards the wire-quoting whitespace
// set: the Unicode White_Space code points the server's tokenizer splits on
// (spec/tcp-commands.md §2). U+200B (zero width space) is not one of them and
// travels bare.
func TestSearchUnicodeWhitespaceTermQuoted(t *testing.T) {
	cases := []struct {
		name string
		term string
	}{
		{"NBSP", "hello\u00a0world"},
		{"next line", "hello\u0085world"},
		{"en quad", "hello\u2000world"},
		{"hair space", "hello\u200aworld"},
		{"ideographic space", "hello\u3000world"},
		{"line separator", "hello\u2028world"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := captureSearchCommand(t, tc.term, SearchOptions{Limit: 10})
			want := `SEARCH threads "` + tc.term + `"`
			if !strings.Contains(cmd, want) {
				t.Errorf("command %q missing quoted term, want to contain %q", cmd, want)
			}
		})
	}
}

// TestSearchReservedKeywordTermsQuoted guards every wire destination
// EscapeQueryString reaches: a term, filter value, or AND/NOT term equal to
// a clause keyword must be quoted, case-insensitively, or the server's
// tokenizer reads it as the operator instead of data.
func TestSearchReservedKeywordTermsQuoted(t *testing.T) {
	cmd := captureSearchCommand(t, "and", SearchOptions{
		Limit:    10,
		AndTerms: []string{"NOT"},
		NotTerms: []string{"or"},
		Filters:  map[string]string{"status": "FILTER"},
	})
	for _, want := range []string{
		`SEARCH threads "and"`,
		`AND "NOT"`,
		`NOT "or"`,
		`FILTER status = "FILTER"`,
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command %q missing %q", cmd, want)
		}
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

func TestSearchCommandFuzzy(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK RESULTS 0\r\n")
	defer cleanup()

	c := NewClient(addr)
	c.config.MaxQueryLength = 0
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	if _, err := c.Search("articles", "machne", SearchOptions{Fuzzy: 2}); err != nil {
		t.Fatal(err)
	}

	cmd := srv.lastCommand()
	if !strings.Contains(cmd, "FUZZY 2") {
		t.Errorf("command %q missing FUZZY 2", cmd)
	}
	if !strings.Contains(cmd, "SEARCH articles machne") {
		t.Errorf("command %q missing SEARCH header", cmd)
	}
}

func TestSearchCommandFuzzyInvalid(t *testing.T) {
	c := NewClient("127.0.0.1:1")
	_, err := c.Search("articles", "x", SearchOptions{Fuzzy: 3})
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("expected ValidationError, got %T", err)
	}
}

func TestSearchCommandFuzzyDistance1(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK RESULTS 0\r\n")
	defer cleanup()

	c := NewClient(addr)
	c.config.MaxQueryLength = 0
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	if _, err := c.Search("articles", "databse", SearchOptions{Fuzzy: 1}); err != nil {
		t.Fatal(err)
	}
	cmd := srv.lastCommand()
	if !strings.Contains(cmd, "FUZZY 1") {
		t.Errorf("command %q missing FUZZY 1", cmd)
	}
}

func TestSearchCommandHighlightDefault(t *testing.T) {
	// A HIGHLIGHT reply always carries the trailing CRLF
	// FormatSearchResponseWithHighlights appends, even with zero results.
	srv, addr, cleanup := newRecordingServer(t, "OK RESULTS 0\r\n\r\n")
	defer cleanup()

	c := NewClient(addr)
	c.config.MaxQueryLength = 0
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	if _, err := c.Search("articles", "kw", SearchOptions{Highlight: &HighlightOptions{}}); err != nil {
		t.Fatal(err)
	}

	cmd := srv.lastCommand()
	if !strings.Contains(cmd, " HIGHLIGHT ") && !strings.HasSuffix(cmd, " HIGHLIGHT") {
		t.Errorf("command %q missing HIGHLIGHT keyword", cmd)
	}
	// Defaults -> no TAG/SNIPPET_LEN/MAX_FRAGMENTS tokens.
	if strings.Contains(cmd, " TAG ") {
		t.Errorf("unexpected TAG in default highlight: %q", cmd)
	}
	if strings.Contains(cmd, " SNIPPET_LEN ") {
		t.Errorf("unexpected SNIPPET_LEN in default highlight: %q", cmd)
	}
	if strings.Contains(cmd, " MAX_FRAGMENTS ") {
		t.Errorf("unexpected MAX_FRAGMENTS in default highlight: %q", cmd)
	}
}

func TestSearchCommandHighlightCustom(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK RESULTS 0\r\n\r\n")
	defer cleanup()

	c := NewClient(addr)
	c.config.MaxQueryLength = 0
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	_, err := c.Search("articles", "kw", SearchOptions{
		Highlight: &HighlightOptions{
			OpenTag:      "<b>",
			CloseTag:     "</b>",
			SnippetLen:   200,
			MaxFragments: 5,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	cmd := srv.lastCommand()
	for _, want := range []string{
		"HIGHLIGHT",
		"TAG <b> </b>",
		"SNIPPET_LEN 200",
		"MAX_FRAGMENTS 5",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command %q missing %q", cmd, want)
		}
	}
}

func TestSearchCommandHighlightInvalid(t *testing.T) {
	c := NewClient("127.0.0.1:1")

	// Mismatched tag pair.
	_, err := c.Search("t", "q", SearchOptions{Highlight: &HighlightOptions{OpenTag: "<b>"}})
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("mismatched tags: want ValidationError, got %T: %v", err, err)
	}

	// SnippetLen out of range.
	_, err = c.Search("t", "q", SearchOptions{Highlight: &HighlightOptions{SnippetLen: 99999}})
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("snippet_len oob: want ValidationError, got %T: %v", err, err)
	}
}

func TestSearchWithHighlightResponse(t *testing.T) {
	response := "OK RESULTS 2\r\n" +
		"10\t<em>foo</em> bar\r\n" +
		"11\tqux <em>foo</em>\r\n" +
		"\r\n"
	_, addr, cleanup := newRecordingServer(t, response)
	defer cleanup()

	c := NewClient(addr)
	c.config.MaxQueryLength = 0
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	resp, err := c.Search("articles", "foo", SearchOptions{
		Highlight: &HighlightOptions{},
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
	if resp.Results[0].PrimaryKey != "10" || resp.Results[0].Snippet != "<em>foo</em> bar" {
		t.Errorf("Results[0] = %+v", resp.Results[0])
	}
	if resp.Results[1].PrimaryKey != "11" || resp.Results[1].Snippet != "qux <em>foo</em>" {
		t.Errorf("Results[1] = %+v", resp.Results[1])
	}
}

func TestSearchCommandSortByScore(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK RESULTS 0\r\n")
	defer cleanup()

	c := NewClient(addr)
	c.config.MaxQueryLength = 0
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	_, err := c.Search("articles", "kw", SearchOptions{
		SortColumn: "_score",
		SortDesc:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	cmd := srv.lastCommand()
	if !strings.Contains(cmd, "SORT _score DESC") {
		t.Errorf("command %q missing SORT _score DESC", cmd)
	}
}

// Regression guard: with Highlight=nil and Fuzzy=0, neither keyword must appear.
func TestSearchCommandNoV16Clauses(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK RESULTS 0\r\n")
	defer cleanup()

	c := NewClient(addr)
	c.config.MaxQueryLength = 0
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	if _, err := c.Search("articles", "kw", SearchOptions{Limit: 5}); err != nil {
		t.Fatal(err)
	}
	cmd := srv.lastCommand()
	for _, forbidden := range []string{"HIGHLIGHT", "FUZZY", "TAG", "SNIPPET_LEN", "MAX_FRAGMENTS"} {
		if strings.Contains(cmd, forbidden) {
			t.Errorf("command %q unexpectedly contains %q", cmd, forbidden)
		}
	}
}

// Combined clauses: the documented v1.6 canonical example. Verifies both that
// every clause lands in the payload and that clauses appear in the order the
// server's query parser expects (SORT before FUZZY before HIGHLIGHT before
// LIMIT).
func TestSearchCommandCombinedV16Clauses(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK RESULTS 0\r\n\r\n")
	defer cleanup()

	c := NewClient(addr)
	c.config.MaxQueryLength = 0
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	_, err := c.Search("articles", "machine", SearchOptions{
		AndTerms:   []string{"learning"},
		NotTerms:   []string{"beginner"},
		Filters:    map[string]string{"status": "1"},
		SortColumn: "_score",
		SortDesc:   true,
		Fuzzy:      1,
		Highlight: &HighlightOptions{
			OpenTag:      "<b>",
			CloseTag:     "</b>",
			SnippetLen:   200,
			MaxFragments: 3,
		},
		Limit:  10,
		Offset: 20,
	})
	if err != nil {
		t.Fatal(err)
	}

	cmd := srv.lastCommand()
	for _, want := range []string{
		"SEARCH articles machine",
		"AND learning",
		"NOT beginner",
		"FILTER status = 1",
		"SORT _score DESC",
		"FUZZY 1",
		"HIGHLIGHT",
		"TAG <b> </b>",
		"SNIPPET_LEN 200",
		"MAX_FRAGMENTS 3",
		"LIMIT 20,10",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command %q missing %q", cmd, want)
		}
	}

	// Ordering: SORT -> FUZZY -> HIGHLIGHT -> LIMIT.
	idxSort := strings.Index(cmd, "SORT ")
	idxFuzzy := strings.Index(cmd, "FUZZY ")
	idxHighlight := strings.Index(cmd, "HIGHLIGHT")
	idxLimit := strings.Index(cmd, "LIMIT ")
	if idxSort >= idxFuzzy || idxFuzzy >= idxHighlight || idxHighlight >= idxLimit {
		t.Errorf("clause order wrong: SORT=%d FUZZY=%d HIGHLIGHT=%d LIMIT=%d in %q",
			idxSort, idxFuzzy, idxHighlight, idxLimit, cmd)
	}
}

func TestSearchQualifiedIdentity(t *testing.T) {
	c, srv := connectRecording(t, "OK RESULTS 0\r\n")
	if _, err := c.Search("app_db.articles", "go", SearchOptions{}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if cmd := srv.lastCommand(); !strings.HasPrefix(cmd, "SEARCH app_db.articles go") {
		t.Errorf("command %q missing qualified identity", cmd)
	}
}

// TestQualifiedIdentityPassthrough verifies that a database.table identity
// reaches COUNT / GET / FACET unchanged (MygramDB v1.7 multi-database).
func TestQualifiedIdentityPassthrough(t *testing.T) {
	t.Run("count", func(t *testing.T) {
		c, srv := connectRecording(t, "OK COUNT 0\r\n")
		if _, err := c.Count("app_db.articles", "x", CountOptions{}); err != nil {
			t.Fatalf("Count: %v", err)
		}
		if cmd := srv.lastCommand(); !strings.HasPrefix(cmd, "COUNT app_db.articles x") {
			t.Errorf("command %q missing qualified identity", cmd)
		}
	})
	t.Run("get", func(t *testing.T) {
		c, srv := connectRecording(t, "OK DOC pk1\r\n")
		if _, err := c.Get("app_db.articles", "pk1"); err != nil {
			t.Fatalf("Get: %v", err)
		}
		if cmd := srv.lastCommand(); cmd != "GET app_db.articles pk1" {
			t.Errorf("command = %q, want %q", cmd, "GET app_db.articles pk1")
		}
	})
	t.Run("facet", func(t *testing.T) {
		c, srv := connectRecording(t, "OK FACET 0\r\n\r\n")
		if _, err := c.Facet("app_db.articles", "category", FacetOptions{}); err != nil {
			t.Fatalf("Facet: %v", err)
		}
		if cmd := srv.lastCommand(); !strings.HasPrefix(cmd, "FACET app_db.articles category") {
			t.Errorf("command %q missing qualified identity", cmd)
		}
	})
}

func TestSearchRawCommand(t *testing.T) {
	c, srv := connectRecording(t, "OK RESULTS 2 101 102\r\n")
	resp, err := c.SearchRaw("articles", "python OR (ruby AND rails)", SearchRawOptions{Limit: 50})
	if err != nil {
		t.Fatalf("SearchRaw: %v", err)
	}
	if resp.TotalCount != 2 {
		t.Errorf("TotalCount = %d, want 2", resp.TotalCount)
	}
	cmd := srv.lastCommand()
	// v1.8: the boolean expression is sent verbatim (unquoted) so the server's
	// AST parser interprets AND/OR/grouping. A quoted expression would be taken
	// as a literal phrase.
	want := `SEARCH articles python OR (ruby AND rails) LIMIT 50`
	if cmd != want {
		t.Errorf("command = %q, want %q", cmd, want)
	}
}

func TestSearchRawNestedBooleanUnquoted(t *testing.T) {
	c, srv := connectRecording(t, "OK RESULTS 1 2\r\n")
	// OR nested under AND is the case v1.8 fixed; it must reach the server
	// whole and unquoted.
	if _, err := c.SearchRaw("articles", "programming AND (ruby OR python)", SearchRawOptions{Limit: 10}); err != nil {
		t.Fatalf("SearchRaw: %v", err)
	}
	want := `SEARCH articles programming AND (ruby OR python) LIMIT 10`
	if cmd := srv.lastCommand(); cmd != want {
		t.Errorf("command = %q, want %q", cmd, want)
	}
}

func TestSearchRawRejectsControlChars(t *testing.T) {
	// Because the expression is now unquoted, a newline could break framing;
	// validation must reject it before it reaches the wire.
	c := NewClient("127.0.0.1:1")
	if _, err := c.SearchRaw("articles", "a OR b\nDROP", SearchRawOptions{}); err == nil {
		t.Fatal("expected validation error for control character in rawQuery")
	} else if _, ok := err.(*ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T", err)
	}
}

func TestSearchRawEmptyQuery(t *testing.T) {
	c := NewClient("127.0.0.1:1")
	if _, err := c.SearchRaw("articles", "", SearchRawOptions{}); err == nil {
		t.Fatal("expected error for empty rawQuery")
	}
}

func TestSearchRawOffsetOnly(t *testing.T) {
	c, srv := connectRecording(t, "OK RESULTS 0\r\n")
	if _, err := c.SearchRaw("articles", "go", SearchRawOptions{Offset: 20}); err != nil {
		t.Fatalf("SearchRaw: %v", err)
	}
	if cmd := srv.lastCommand(); !strings.HasSuffix(cmd, "OFFSET 20") {
		t.Errorf("command %q missing bare OFFSET", cmd)
	}
}

func TestSearchRawWithHighlights(t *testing.T) {
	c, srv := connectRecording(t, "OK RESULTS 0\r\n\r\n")
	if _, err := c.SearchRawWithHighlights("articles", "go", SearchRawOptions{}); err != nil {
		t.Fatalf("SearchRawWithHighlights: %v", err)
	}
	if cmd := srv.lastCommand(); !strings.Contains(cmd, "HIGHLIGHT") {
		t.Errorf("command %q missing HIGHLIGHT", cmd)
	}
}

func TestSearchWithHighlights(t *testing.T) {
	c, srv := connectRecording(t, "OK RESULTS 0\r\n\r\n")
	if _, err := c.SearchWithHighlights("articles", "go", SearchOptions{}); err != nil {
		t.Fatalf("SearchWithHighlights: %v", err)
	}
	if cmd := srv.lastCommand(); !strings.Contains(cmd, "HIGHLIGHT") {
		t.Errorf("command %q missing HIGHLIGHT", cmd)
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

func TestSearchCommandFilterConditions(t *testing.T) {
	c, srv := connectRecording(t, "OK RESULTS 0\r\n")

	_, err := c.Search("articles", "golang", SearchOptions{
		FilterConditions: []FilterCondition{
			{Key: "views", Op: FilterGreaterThanOrEqual, Value: "100"},
			{Key: "status", Op: FilterNotEqual, Value: "draft"},
			{Key: "created_at", Op: FilterLessThan, Value: "2026-01-01"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	cmd := srv.lastCommand()
	for _, want := range []string{
		"FILTER views >= 100",
		"FILTER status != draft",
		"FILTER created_at < 2026-01-01",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command %q missing %q", cmd, want)
		}
	}
}

func TestSearchRejectsInvalidFilterOp(t *testing.T) {
	c := NewClient("127.0.0.1:1")
	_, err := c.Search("articles", "x", SearchOptions{
		FilterConditions: []FilterCondition{{Key: "views", Op: FilterOp(99), Value: "1"}},
	})
	if _, ok := err.(*ValidationError); !ok {
		t.Fatalf("expected ValidationError, got %T (%v)", err, err)
	}
}

func TestSearchRejectsFilterConditionKeyWithQuote(t *testing.T) {
	c := NewClient("127.0.0.1:1")
	_, err := c.Search("articles", "x", SearchOptions{
		FilterConditions: []FilterCondition{{Key: `views"`, Value: "1"}},
	})
	if _, ok := err.(*ValidationError); !ok {
		t.Fatalf("expected ValidationError, got %T (%v)", err, err)
	}
}

// Map iteration order is randomized, so an unsorted implementation would only
// fail intermittently; several keys make one run enough to catch it.
func TestFilterMapEmittedInSortedKeyOrder(t *testing.T) {
	c, srv := connectRecording(t, "OK RESULTS 0\r\n")

	_, err := c.Search("articles", "golang", SearchOptions{
		Filters: map[string]string{"zeta": "1", "alpha": "2", "mu": "3", "beta": "4"},
	})
	if err != nil {
		t.Fatal(err)
	}

	cmd := srv.lastCommand()
	want := "FILTER alpha = 2 FILTER beta = 4 FILTER mu = 3 FILTER zeta = 1"
	if !strings.Contains(cmd, want) {
		t.Errorf("command %q does not contain filters in sorted order %q", cmd, want)
	}
}

func TestSearchBooleanModeSendsExpressionVerbatim(t *testing.T) {
	c, srv := connectRecording(t, "OK RESULTS 0\r\n")

	_, err := c.Search("articles", "alpha AND (beta OR gamma)", SearchOptions{
		QueryMode:  QueryBoolean,
		SortColumn: "created_at",
		SortDesc:   true,
		Limit:      10,
	})
	if err != nil {
		t.Fatal(err)
	}

	cmd := srv.lastCommand()
	if !strings.HasPrefix(cmd, "SEARCH articles alpha AND (beta OR gamma) ") {
		t.Errorf("command %q did not send the expression verbatim", cmd)
	}
	if !strings.Contains(cmd, "SORT created_at DESC") {
		t.Errorf("command %q lost the SORT clause", cmd)
	}
}

func TestSearchLiteralModeQuotesExpression(t *testing.T) {
	c, srv := connectRecording(t, "OK RESULTS 0\r\n")

	if _, err := c.Search("articles", "alpha AND beta", SearchOptions{}); err != nil {
		t.Fatal(err)
	}
	if cmd := srv.lastCommand(); !strings.Contains(cmd, `"alpha AND beta"`) {
		t.Errorf("command %q did not quote the literal text", cmd)
	}
}

func TestSearchRejectsUnknownQueryMode(t *testing.T) {
	c := NewClient("127.0.0.1:1")
	_, err := c.Search("articles", "x", SearchOptions{QueryMode: QueryMode(7)})
	if _, ok := err.(*ValidationError); !ok {
		t.Fatalf("expected ValidationError, got %T (%v)", err, err)
	}
}

func TestSearchPrimaryKeySortShorthand(t *testing.T) {
	cases := []struct {
		name string
		opts SearchOptions
		want string
	}{
		{
			name: "default omits the clause",
			opts: SearchOptions{},
			want: "SEARCH articles go LIMIT 1000",
		},
		{
			name: "ascending uses the column-less shorthand",
			opts: SearchOptions{SortPrimaryKey: PrimaryKeySortAsc},
			want: "SEARCH articles go SORT ASC LIMIT 1000",
		},
		{
			name: "descending is stated explicitly",
			opts: SearchOptions{SortPrimaryKey: PrimaryKeySortDesc},
			want: "SEARCH articles go SORT DESC LIMIT 1000",
		},
		{
			name: "a sort column still carries its direction",
			opts: SearchOptions{SortColumn: "created_at", SortDesc: true},
			want: "SEARCH articles go SORT created_at DESC LIMIT 1000",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, srv := connectRecording(t, "OK RESULTS 0\r\n")
			if _, err := c.Search("articles", "go", tc.opts); err != nil {
				t.Fatalf("Search: %v", err)
			}
			if got := srv.lastCommand(); got != tc.want {
				t.Errorf("command = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSearchRejectsConflictingSortOptions(t *testing.T) {
	c := NewClient("127.0.0.1:1")
	_, err := c.Search("articles", "go", SearchOptions{
		SortColumn:     "created_at",
		SortPrimaryKey: PrimaryKeySortAsc,
	})
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("error = %v (%T), want *ValidationError", err, err)
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error should explain the conflict, got: %v", err)
	}
}

func TestSearchRejectsUndefinedPrimaryKeySort(t *testing.T) {
	c := NewClient("127.0.0.1:1")
	_, err := c.Search("articles", "go", SearchOptions{SortPrimaryKey: PrimaryKeySort(9)})
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("error = %v (%T), want *ValidationError", err, err)
	}
}

func TestCountCommandFilterConditions(t *testing.T) {
	c, srv := connectRecording(t, "OK COUNT 7\r\n")

	if _, err := c.Count("articles", "golang", CountOptions{
		FilterConditions: []FilterCondition{{Key: "views", Op: FilterGreaterThan, Value: "10"}},
	}); err != nil {
		t.Fatal(err)
	}
	if cmd := srv.lastCommand(); !strings.Contains(cmd, "FILTER views > 10") {
		t.Errorf("command %q missing comparison filter", cmd)
	}
}

func TestCountBooleanModeSendsExpressionVerbatim(t *testing.T) {
	c, srv := connectRecording(t, "OK COUNT 3\r\n")

	if _, err := c.Count("articles", "alpha AND NOT beta", CountOptions{QueryMode: QueryBoolean}); err != nil {
		t.Fatal(err)
	}
	if cmd := srv.lastCommand(); cmd != "COUNT articles alpha AND NOT beta" {
		t.Errorf("command = %q, want the expression verbatim", cmd)
	}
}

func TestFacetCommandFormat(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK FACET 0\r\n\r\n")
	defer cleanup()

	c := NewClient(addr)
	c.config.MaxQueryLength = 0
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	_, err := c.Facet("articles", "category", FacetOptions{
		Query:    "machine",
		AndTerms: []string{"learning"},
		NotTerms: []string{"beginner"},
		Filters:  map[string]string{"status": "1"},
		Limit:    10,
	})
	if err != nil {
		t.Fatal(err)
	}

	cmd := srv.lastCommand()
	for _, want := range []string{
		"FACET articles category",
		" machine",
		"AND learning",
		"NOT beginner",
		"FILTER status = 1",
		"LIMIT 10",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command %q missing %q", cmd, want)
		}
	}
}

func TestFacetCommandAllValues(t *testing.T) {
	// No query: "FACET articles status" with nothing else.
	srv, addr, cleanup := newRecordingServer(t, "OK FACET 2\r\n1\t100\r\n0\t50\r\n\r\n")
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	resp, err := c.Facet("articles", "status", FacetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("len(Results) = %d, want 2", len(resp.Results))
	}
	if resp.Results[0].Value != "1" || resp.Results[0].Count != 100 {
		t.Errorf("Results[0] = %+v", resp.Results[0])
	}

	cmd := srv.lastCommand()
	if strings.TrimSpace(cmd) != "FACET articles status" {
		t.Errorf("unexpected command %q", cmd)
	}
}

func TestFacetCommandOffset(t *testing.T) {
	c, srv := connectRecording(t, "OK FACET 0 42\r\n\r\n\r\n")

	if _, err := c.Facet("articles", "category", FacetOptions{Limit: 10, Offset: 20}); err != nil {
		t.Fatal(err)
	}
	if cmd := srv.lastCommand(); cmd != "FACET articles category OFFSET 20 LIMIT 10" {
		t.Errorf("command = %q, want OFFSET before LIMIT", cmd)
	}
}

func TestFacetValidation(t *testing.T) {
	c := NewClient("127.0.0.1:1")

	if _, err := c.Facet("", "status", FacetOptions{}); err == nil {
		t.Error("empty table: expected error")
	}
	if _, err := c.Facet("articles", "", FacetOptions{}); err == nil {
		t.Error("empty column: expected error")
	}
	if _, err := c.Facet("articles", "status", FacetOptions{Query: "bad\x00"}); err == nil {
		t.Error("query with NUL: expected error")
	}
	if _, err := c.Facet("articles", "status", FacetOptions{Filters: map[string]string{"k": "bad\x01"}}); err == nil {
		t.Error("filter with control char: expected error")
	}
}

func TestFacetServerError(t *testing.T) {
	_, addr, cleanup := newRecordingServer(t, "ERROR Facet column not found: foo\r\n")
	defer cleanup()

	c := NewClient(addr)
	defer func() { _ = c.Close() }()
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}

	_, err := c.Facet("articles", "foo", FacetOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := err.(*ServerError); !ok {
		t.Errorf("expected ServerError, got %T: %v", err, err)
	}
}

// Query length is checked across table/query/terms/filters/sort. Facet reuses
// the same validator; verify it actually trips for oversize facet queries.
func TestFacetQueryLengthExceeded(t *testing.T) {
	c := NewClient("127.0.0.1:1") // MaxQueryLength = 128 (default)
	longQuery := strings.Repeat("a", 200)
	_, err := c.Facet("articles", "status", FacetOptions{Query: longQuery})
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("expected ValidationError, got %T", err)
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

func TestGetEscapesPrimaryKey(t *testing.T) {
	c, srv := connectRecording(t, "OK DOC \"a b\"\r\n")

	if _, err := c.Get("articles", "a b"); err != nil {
		t.Fatal(err)
	}
	if cmd := srv.lastCommand(); cmd != `GET articles "a b"` {
		t.Errorf("command = %q, want the primary key quoted", cmd)
	}
}

func TestGetRejectsEmptyPrimaryKey(t *testing.T) {
	c := NewClient("127.0.0.1:1")
	if _, err := c.Get("articles", ""); err == nil {
		t.Fatal("expected an error for an empty primary key")
	}
}
