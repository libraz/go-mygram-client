package mygram

import (
	"net"
	"strings"
	"sync"
	"testing"
)

// Tests for MygramDB v1.6 features: FUZZY, HIGHLIGHT, FACET, and SORT _score.

// --- parseSearchResponse: HIGHLIGHT format -----------------------------------

func TestParseSearchResponseHighlight(t *testing.T) {
	t.Run("two highlighted results", func(t *testing.T) {
		response := "OK RESULTS 2\n" +
			"101\tmachine <em>learning</em> basics\n" +
			"102\tintro to <em>learning</em>\n"
		resp, err := parseSearchResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if resp.TotalCount != 2 {
			t.Errorf("TotalCount = %d, want 2", resp.TotalCount)
		}
		if len(resp.Results) != 2 {
			t.Fatalf("len(Results) = %d, want 2", len(resp.Results))
		}
		if resp.Results[0].PrimaryKey != "101" {
			t.Errorf("Results[0].PrimaryKey = %q, want 101", resp.Results[0].PrimaryKey)
		}
		if resp.Results[0].Snippet != "machine <em>learning</em> basics" {
			t.Errorf("Results[0].Snippet = %q", resp.Results[0].Snippet)
		}
		if resp.Results[1].PrimaryKey != "102" {
			t.Errorf("Results[1].PrimaryKey = %q, want 102", resp.Results[1].PrimaryKey)
		}
		if resp.Results[1].Snippet != "intro to <em>learning</em>" {
			t.Errorf("Results[1].Snippet = %q", resp.Results[1].Snippet)
		}
	})

	t.Run("highlight with custom tag and debug", func(t *testing.T) {
		response := "OK RESULTS 1\n" +
			"42\t<b>golang</b> rocks\n" +
			"# DEBUG\n" +
			"query_time: 0.42\n" +
			"index_time: 0.1\n" +
			"terms: 1\n" +
			"ngrams: 2\n" +
			"final: 1\n" +
			"highlight: on\n" +
			"cache: miss\n"
		resp, err := parseSearchResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Results) != 1 {
			t.Fatalf("len(Results) = %d, want 1", len(resp.Results))
		}
		if resp.Results[0].Snippet != "<b>golang</b> rocks" {
			t.Errorf("Results[0].Snippet = %q", resp.Results[0].Snippet)
		}
		if resp.Debug == nil {
			t.Fatal("Debug should not be nil")
		}
		if resp.Debug.QueryTimeMs != 0.42 {
			t.Errorf("QueryTimeMs = %f, want 0.42", resp.Debug.QueryTimeMs)
		}
		if resp.Debug.Cache != "miss" {
			t.Errorf("Cache = %q, want miss", resp.Debug.Cache)
		}
	})

	t.Run("highlight with zero results", func(t *testing.T) {
		// When total=0, no snippet lines follow; behaves like classic format.
		resp, err := parseSearchResponse("OK RESULTS 0")
		if err != nil {
			t.Fatal(err)
		}
		if resp.TotalCount != 0 {
			t.Errorf("TotalCount = %d, want 0", resp.TotalCount)
		}
		if len(resp.Results) != 0 {
			t.Errorf("len(Results) = %d, want 0", len(resp.Results))
		}
	})

	t.Run("snippet containing spaces", func(t *testing.T) {
		// Make sure we split on the first tab only; spaces inside snippets
		// must be preserved verbatim.
		response := "OK RESULTS 1\nfoo-1\ta b c d e <em>kw</em> f g h\n"
		resp, err := parseSearchResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Results[0].PrimaryKey != "foo-1" {
			t.Errorf("PrimaryKey = %q, want foo-1", resp.Results[0].PrimaryKey)
		}
		if resp.Results[0].Snippet != "a b c d e <em>kw</em> f g h" {
			t.Errorf("Snippet = %q", resp.Results[0].Snippet)
		}
	})
}

// --- parseFacetResponse ------------------------------------------------------

func TestParseFacetResponse(t *testing.T) {
	t.Run("three values", func(t *testing.T) {
		response := "OK FACET 3\n" +
			"tech\t42\n" +
			"science\t18\n" +
			"art\t7\n"
		resp, err := parseFacetResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Results) != 3 {
			t.Fatalf("len(Results) = %d, want 3", len(resp.Results))
		}
		want := []FacetValue{
			{Value: "tech", Count: 42},
			{Value: "science", Count: 18},
			{Value: "art", Count: 7},
		}
		for i, w := range want {
			if resp.Results[i] != w {
				t.Errorf("Results[%d] = %+v, want %+v", i, resp.Results[i], w)
			}
		}
	})

	t.Run("empty result set", func(t *testing.T) {
		resp, err := parseFacetResponse("OK FACET 0\n")
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Results) != 0 {
			t.Errorf("len(Results) = %d, want 0", len(resp.Results))
		}
	})

	t.Run("with debug comments", func(t *testing.T) {
		response := "OK FACET 1\n" +
			"published\t100\n" +
			"# query_time_ms: 1.23\n" +
			"# total_docs_searched: 100\n"
		resp, err := parseFacetResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Results) != 1 {
			t.Fatalf("len(Results) = %d, want 1", len(resp.Results))
		}
		if resp.Results[0].Value != "published" || resp.Results[0].Count != 100 {
			t.Errorf("Results[0] = %+v", resp.Results[0])
		}
	})

	t.Run("value starting with hash is kept", func(t *testing.T) {
		// v1.8: a facet value may legitimately start with '#'. Such a row has a
		// tab and must not be dropped as a comment, while a tab-less '#' line
		// (e.g. a debug comment) is still skipped.
		response := "OK FACET 2\n" +
			"#hashtag\t9\n" +
			"#golang\t4\n" +
			"# query_time_ms: 0.5\n"
		resp, err := parseFacetResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		want := []FacetValue{
			{Value: "#hashtag", Count: 9},
			{Value: "#golang", Count: 4},
		}
		if len(resp.Results) != len(want) {
			t.Fatalf("len(Results) = %d, want %d (%+v)", len(resp.Results), len(want), resp.Results)
		}
		for i, w := range want {
			if resp.Results[i] != w {
				t.Errorf("Results[%d] = %+v, want %+v", i, resp.Results[i], w)
			}
		}
	})

	t.Run("value with spaces", func(t *testing.T) {
		// Facet values are whitespace-tolerant; only tab separates the count.
		response := "OK FACET 1\nmachine learning\t5\n"
		resp, err := parseFacetResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Results[0].Value != "machine learning" {
			t.Errorf("Value = %q, want %q", resp.Results[0].Value, "machine learning")
		}
		if resp.Results[0].Count != 5 {
			t.Errorf("Count = %d, want 5", resp.Results[0].Count)
		}
	})

	t.Run("invalid prefix", func(t *testing.T) {
		_, err := parseFacetResponse("NOT OK")
		if err == nil {
			t.Fatal("expected error")
		}
		if _, ok := err.(*ProtocolError); !ok {
			t.Errorf("expected ProtocolError, got %T", err)
		}
	})

	t.Run("malformed row (no tab)", func(t *testing.T) {
		_, err := parseFacetResponse("OK FACET 1\nbroken\n")
		if err == nil {
			t.Fatal("expected error for missing tab")
		}
	})

	t.Run("malformed count", func(t *testing.T) {
		_, err := parseFacetResponse("OK FACET 1\ntech\tNaN\n")
		if err == nil {
			t.Fatal("expected error for non-numeric count")
		}
	})
}

// --- validateFuzzy / validateHighlight / validateFacetColumn -----------------

func TestValidateFuzzy(t *testing.T) {
	cases := []struct {
		distance int
		wantErr  bool
	}{
		{0, false},
		{1, false},
		{2, false},
		{-1, true},
		{3, true},
		{100, true},
	}
	for _, tc := range cases {
		err := validateFuzzy(tc.distance)
		if (err != nil) != tc.wantErr {
			t.Errorf("validateFuzzy(%d) err=%v, wantErr=%v", tc.distance, err, tc.wantErr)
		}
	}
}

func TestValidateHighlight(t *testing.T) {
	if err := validateHighlight(nil); err != nil {
		t.Errorf("nil options: err=%v", err)
	}
	if err := validateHighlight(&HighlightOptions{}); err != nil {
		t.Errorf("zero options: err=%v", err)
	}
	if err := validateHighlight(&HighlightOptions{OpenTag: "<b>", CloseTag: "</b>"}); err != nil {
		t.Errorf("both tags set: err=%v", err)
	}
	if err := validateHighlight(&HighlightOptions{SnippetLen: 200, MaxFragments: 5}); err != nil {
		t.Errorf("limits in range: err=%v", err)
	}

	bad := []HighlightOptions{
		{OpenTag: "<b>"},   // missing close
		{CloseTag: "</b>"}, // missing open
		{OpenTag: "<em class='x'>", CloseTag: "</em>"}, // whitespace in tag
		{SnippetLen: -1},
		{SnippetLen: 10001},
		{MaxFragments: -1},
		{MaxFragments: 101},
	}
	for i, b := range bad {
		if err := validateHighlight(&b); err == nil {
			t.Errorf("case %d (%+v): expected error", i, b)
		} else if _, ok := err.(*ValidationError); !ok {
			t.Errorf("case %d: expected ValidationError, got %T", i, err)
		}
	}
}

func TestValidateFacetColumn(t *testing.T) {
	if err := validateFacetColumn("status"); err != nil {
		t.Errorf("valid column: err=%v", err)
	}
	if err := validateFacetColumn(""); err == nil {
		t.Error("empty column: expected error")
	}
	if err := validateFacetColumn("col with space"); err == nil {
		t.Error("spaced column: expected error")
	}
	if err := validateFacetColumn("col\x00name"); err == nil {
		t.Error("control char column: expected error")
	}
}

// --- Client.Search: FUZZY / HIGHLIGHT command formatting ---------------------

// recordingServer captures the last command received from the client. Since
// clients reuse the same connection and the server goroutine may outlive the
// call, guard access with a mutex.
type recordingServer struct {
	mu       sync.Mutex
	lastCmd  string
	response string
}

func (s *recordingServer) lastCommand() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastCmd
}

// newRecordingServer spins up a mock TCP server that records the command it
// receives and replies with the caller-supplied response.
func newRecordingServer(t *testing.T, initialResponse string) (*recordingServer, string, func()) {
	t.Helper()
	s := &recordingServer{response: initialResponse}

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
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				buf := make([]byte, 4096)
				n, readErr := c.Read(buf)
				if readErr != nil {
					return
				}
				cmd := strings.TrimRight(string(buf[:n]), "\r\n")
				s.mu.Lock()
				s.lastCmd = cmd
				resp := s.response
				s.mu.Unlock()
				_, _ = c.Write([]byte(resp))
			}(conn)
		}
	}()

	return s, ln.Addr().String(), func() { _ = ln.Close() }
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

func TestSearchCommandHighlightDefault(t *testing.T) {
	srv, addr, cleanup := newRecordingServer(t, "OK RESULTS 0\r\n")
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
	srv, addr, cleanup := newRecordingServer(t, "OK RESULTS 0\r\n")
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

// --- End-to-end HIGHLIGHT response parsing through Client.Search -------------

func TestSearchWithHighlightResponse(t *testing.T) {
	response := "OK RESULTS 2\r\n" +
		"10\t<em>foo</em> bar\r\n" +
		"11\tqux <em>foo</em>\r\n"
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

// --- Client.Facet ------------------------------------------------------------

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

// --- SORT _score --------------------------------------------------------------

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

// --- Additional coverage: combined clauses & negative paths ------------------

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
	srv, addr, cleanup := newRecordingServer(t, "OK RESULTS 0\r\n")
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

// validateHighlight: tag with control character must be rejected by
// ensureSafeValue (the inner loop in the tag validation block).
func TestValidateHighlightControlCharInTag(t *testing.T) {
	err := validateHighlight(&HighlightOptions{
		OpenTag:  "<b\x01>",
		CloseTag: "</b>",
	})
	if err == nil {
		t.Fatal("expected error for control char in tag")
	}
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("expected ValidationError, got %T: %v", err, err)
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

// parseSearchResponse: defensive handling of malformed first lines.
func TestParseSearchResponseMalformed(t *testing.T) {
	cases := []string{
		"OK RESULTS",     // missing count
		"OK RESULTS abc", // non-numeric count
		"OK RESULTS\n",   // count missing with newline
	}
	for _, input := range cases {
		if _, err := parseSearchResponse(input); err == nil {
			t.Errorf("expected error for %q", input)
		}
	}
}

// --- isResponseComplete: FACET framing ---------------------------------------

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
