package mygram

import (
	"strings"
	"testing"
)

// Response parsing (parser.go).

func TestParseSearchResponse(t *testing.T) {
	t.Run("basic response", func(t *testing.T) {
		resp, err := parseSearchResponse("OK RESULTS 150 101 102 103")
		if err != nil {
			t.Fatal(err)
		}
		if resp.TotalCount != 150 {
			t.Errorf("TotalCount = %d, want 150", resp.TotalCount)
		}
		if len(resp.Results) != 3 {
			t.Fatalf("len(Results) = %d, want 3", len(resp.Results))
		}
		if resp.Results[0].PrimaryKey != "101" {
			t.Errorf("Results[0].PrimaryKey = %q, want %q", resp.Results[0].PrimaryKey, "101")
		}
		if resp.Results[2].PrimaryKey != "103" {
			t.Errorf("Results[2].PrimaryKey = %q, want %q", resp.Results[2].PrimaryKey, "103")
		}
		if resp.Debug != nil {
			t.Error("Debug should be nil")
		}
	})

	t.Run("zero results", func(t *testing.T) {
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

	t.Run("with debug info", func(t *testing.T) {
		response := "OK RESULTS 50 1 2 3\n# DEBUG\nquery_time: 1.5\nindex_time: 0.8\nterms: 3\ncache: hit\n"
		resp, err := parseSearchResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if resp.TotalCount != 50 {
			t.Errorf("TotalCount = %d, want 50", resp.TotalCount)
		}
		if resp.Debug == nil {
			t.Fatal("Debug should not be nil")
		}
		if resp.Debug.QueryTimeMs != 1.5 {
			t.Errorf("QueryTimeMs = %f, want 1.5", resp.Debug.QueryTimeMs)
		}
		if resp.Debug.Terms != 3 {
			t.Errorf("Terms = %d, want 3", resp.Debug.Terms)
		}
		if resp.Debug.Cache != "hit" {
			t.Errorf("Cache = %q, want %q", resp.Debug.Cache, "hit")
		}
	})

	t.Run("invalid response", func(t *testing.T) {
		_, err := parseSearchResponse("INVALID RESPONSE")
		if err == nil {
			t.Error("expected error")
		}
		if _, ok := err.(*ProtocolError); !ok {
			t.Errorf("expected ProtocolError, got %T", err)
		}
	})
}

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

// parseSearchResponse: primary keys are reversibly escaped per
// spec/tcp-commands.md §11.1 when they contain whitespace, so both formats
// must decode a quoted key rather than splitting on raw whitespace/tab.
func TestParseSearchResponseQuotedPrimaryKey(t *testing.T) {
	t.Run("classic mode, quoted key with a space", func(t *testing.T) {
		resp, err := parseSearchResponse(`OK RESULTS 2 "id one" id2`)
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Results) != 2 {
			t.Fatalf("len(Results) = %d, want 2", len(resp.Results))
		}
		if resp.Results[0].PrimaryKey != "id one" {
			t.Errorf("Results[0].PrimaryKey = %q, want %q", resp.Results[0].PrimaryKey, "id one")
		}
		if resp.Results[1].PrimaryKey != "id2" {
			t.Errorf("Results[1].PrimaryKey = %q, want %q", resp.Results[1].PrimaryKey, "id2")
		}
	})

	t.Run("highlight mode, quoted key with a space", func(t *testing.T) {
		response := "OK RESULTS 1\n" + `"id one"` + "\tsnippet text\n"
		resp, err := parseSearchResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Results) != 1 {
			t.Fatalf("len(Results) = %d, want 1", len(resp.Results))
		}
		if resp.Results[0].PrimaryKey != "id one" {
			t.Errorf("PrimaryKey = %q, want %q", resp.Results[0].PrimaryKey, "id one")
		}
		if resp.Results[0].Snippet != "snippet text" {
			t.Errorf("Snippet = %q, want %q", resp.Results[0].Snippet, "snippet text")
		}
	})

	t.Run("highlight mode, malformed quoted key is a protocol error", func(t *testing.T) {
		response := "OK RESULTS 1\n" + `"unterminated` + "\tsnippet\n"
		if _, err := parseSearchResponse(response); err == nil {
			t.Error("expected error for unterminated quoted primary key")
		}
	})
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

func TestParseCountResponse(t *testing.T) {
	t.Run("basic response", func(t *testing.T) {
		resp, err := parseCountResponse("OK COUNT 1523")
		if err != nil {
			t.Fatal(err)
		}
		if resp.Count != 1523 {
			t.Errorf("Count = %d, want 1523", resp.Count)
		}
	})

	t.Run("with debug info", func(t *testing.T) {
		response := "OK COUNT 100\n# DEBUG\nquery_time: 0.5\nindex_time: 0.3\n"
		resp, err := parseCountResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Count != 100 {
			t.Errorf("Count = %d, want 100", resp.Count)
		}
		if resp.Debug == nil {
			t.Fatal("Debug should not be nil")
		}
		if resp.Debug.QueryTimeMs != 0.5 {
			t.Errorf("QueryTimeMs = %f, want 0.5", resp.Debug.QueryTimeMs)
		}
	})
}

func TestParseCountResponseRejectsTrailingTokens(t *testing.T) {
	_, err := parseCountResponse("OK COUNT 12 34")
	if _, ok := err.(*ProtocolError); !ok {
		t.Fatalf("expected ProtocolError, got %T (%v)", err, err)
	}
}

func TestParseCountResponseRejectsNegativeCount(t *testing.T) {
	_, err := parseCountResponse("OK COUNT -1")
	if _, ok := err.(*ProtocolError); !ok {
		t.Fatalf("expected ProtocolError, got %T (%v)", err, err)
	}
}

func TestParseGetResponse(t *testing.T) {
	t.Run("document with fields", func(t *testing.T) {
		resp, err := parseGetResponse("OK DOC 12345 status=1 category=tech created_at=1705312200")
		if err != nil {
			t.Fatal(err)
		}
		if resp.PrimaryKey != "12345" {
			t.Errorf("PrimaryKey = %q, want %q", resp.PrimaryKey, "12345")
		}
		if resp.Fields["status"] != "1" {
			t.Errorf("Fields[status] = %q, want %q", resp.Fields["status"], "1")
		}
		if resp.Fields["category"] != "tech" {
			t.Errorf("Fields[category] = %q, want %q", resp.Fields["category"], "tech")
		}
	})

	t.Run("document without fields", func(t *testing.T) {
		resp, err := parseGetResponse("OK DOC 99999")
		if err != nil {
			t.Fatal(err)
		}
		if resp.PrimaryKey != "99999" {
			t.Errorf("PrimaryKey = %q, want %q", resp.PrimaryKey, "99999")
		}
		if len(resp.Fields) != 0 {
			t.Errorf("len(Fields) = %d, want 0", len(resp.Fields))
		}
	})

	t.Run("quoted primary key with a space", func(t *testing.T) {
		resp, err := parseGetResponse(`OK DOC "id one" status=1`)
		if err != nil {
			t.Fatal(err)
		}
		if resp.PrimaryKey != "id one" {
			t.Errorf("PrimaryKey = %q, want %q", resp.PrimaryKey, "id one")
		}
		if resp.Fields["status"] != "1" {
			t.Errorf("Fields[status] = %q, want %q", resp.Fields["status"], "1")
		}
	})

	t.Run("quoted string field value with a space", func(t *testing.T) {
		resp, err := parseGetResponse(`OK DOC 12345 title="hello world" status=1 category=tech`)
		if err != nil {
			t.Fatal(err)
		}
		if resp.PrimaryKey != "12345" {
			t.Errorf("PrimaryKey = %q, want %q", resp.PrimaryKey, "12345")
		}
		if resp.Fields["title"] != "hello world" {
			t.Errorf("Fields[title] = %q, want %q", resp.Fields["title"], "hello world")
		}
		if resp.Fields["status"] != "1" {
			t.Errorf("Fields[status] = %q, want %q", resp.Fields["status"], "1")
		}
		if resp.Fields["category"] != "tech" {
			t.Errorf("Fields[category] = %q, want %q", resp.Fields["category"], "tech")
		}
	})

	t.Run("field value with escaped characters", func(t *testing.T) {
		resp, err := parseGetResponse(`OK DOC 1 note="say \"hi\"\ttab"`)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Fields["note"] != "say \"hi\"\ttab" {
			t.Errorf("Fields[note] = %q", resp.Fields["note"])
		}
	})
}

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

func TestParseFacetResponseTotalCount(t *testing.T) {
	resp, err := parseFacetResponse("OK FACET 2 42\r\nnews\t10\r\nblog\t7\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(resp.Results))
	}
	if resp.TotalCount != 42 {
		t.Errorf("TotalCount = %d, want 42", resp.TotalCount)
	}
}

// A server that predates paginated facets sends only the page size, so the
// best available total is that same number.
func TestParseFacetResponseLegacyHeaderFallsBackToPageSize(t *testing.T) {
	resp, err := parseFacetResponse("OK FACET 2\r\nnews\t10\r\nblog\t7\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if resp.TotalCount != 2 {
		t.Errorf("TotalCount = %d, want 2", resp.TotalCount)
	}
}

func TestParseFacetResponseRejectsRowCountMismatch(t *testing.T) {
	_, err := parseFacetResponse("OK FACET 3 3\r\nnews\t10\r\n\r\n")
	if _, ok := err.(*ProtocolError); !ok {
		t.Fatalf("expected ProtocolError, got %T (%v)", err, err)
	}
}

func TestParseFacetResponseRejectsTotalBelowPageSize(t *testing.T) {
	_, err := parseFacetResponse("OK FACET 2 1\r\nnews\t10\r\nblog\t7\r\n\r\n")
	if _, ok := err.(*ProtocolError); !ok {
		t.Fatalf("expected ProtocolError, got %T (%v)", err, err)
	}
}

func TestParseInfoResponse(t *testing.T) {
	response := `OK INFO

# Server
version: 2.1.0
uptime_seconds: 3600

# Stats
total_requests: 50000

# Clients
connected_clients: 5

# Memory
used_memory_bytes: 1048576

# Index
total_documents: 100000

# Tables
tables: threads,images,keywords
`

	info, err := parseInfoResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "2.1.0" {
		t.Errorf("Version = %q, want %q", info.Version, "2.1.0")
	}
	if info.UptimeSeconds != 3600 {
		t.Errorf("UptimeSeconds = %d, want 3600", info.UptimeSeconds)
	}
	if info.TotalRequests != 50000 {
		t.Errorf("TotalRequests = %d, want 50000", info.TotalRequests)
	}
	if info.ActiveConnections != 5 {
		t.Errorf("ActiveConnections = %d, want 5", info.ActiveConnections)
	}
	if info.DocCount != 100000 {
		t.Errorf("DocCount = %d, want 100000", info.DocCount)
	}
	if len(info.Tables) != 3 {
		t.Fatalf("len(Tables) = %d, want 3", len(info.Tables))
	}
	if info.Tables[0] != "threads" {
		t.Errorf("Tables[0] = %q, want %q", info.Tables[0], "threads")
	}
}

func TestParseInfoResponseReadiness(t *testing.T) {
	response := strings.Join([]string{
		"OK INFO",
		"",
		"# Server",
		"version: 1.10.0",
		"uptime_seconds: 120",
		"data_initialized: true",
		"readiness: ready",
		"",
		"END",
	}, "\n")

	info, err := parseInfoResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	if !info.DataInitialized {
		t.Error("DataInitialized = false, want true")
	}
	if info.Readiness != "ready" {
		t.Errorf("Readiness = %q, want %q", info.Readiness, "ready")
	}
	if !info.Ready() {
		t.Error("Ready() = false, want true")
	}
}

func TestParseInfoResponseNotReady(t *testing.T) {
	response := "OK INFO\n\n# Server\nversion: 1.10.0\ndata_initialized: false\nreadiness: not_ready\n\nEND"

	info, err := parseInfoResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	if info.DataInitialized {
		t.Error("DataInitialized = true, want false")
	}
	if info.Ready() {
		t.Error("Ready() = true, want false")
	}
}

// A pre-v1.10 server omits both fields, so readiness is simply unknown.
func TestParseInfoResponseLegacyServerHasNoReadiness(t *testing.T) {
	info, err := parseInfoResponse("OK INFO\n\n# Server\nversion: 1.8.0\n\nEND")
	if err != nil {
		t.Fatal(err)
	}
	if info.Readiness != "" || info.DataInitialized || info.Ready() {
		t.Errorf("legacy INFO reported readiness: %+v", info)
	}
}

func TestParseReplicationStatusResponse(t *testing.T) {
	t.Run("multi-line format", func(t *testing.T) {
		response := "OK REPLICATION\nstatus: running\ncurrent_gtid: abc-123\nprocessed_events: 100\nEND"
		status, err := parseReplicationStatusResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if !status.Running {
			t.Error("Running should be true")
		}
		if status.GTID != "abc-123" {
			t.Errorf("GTID = %q, want %q", status.GTID, "abc-123")
		}
	})

	t.Run("single-line format", func(t *testing.T) {
		response := "OK REPLICATION status=stopped gtid=xyz-789"
		status, err := parseReplicationStatusResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if status.Running {
			t.Error("Running should be false")
		}
		if status.GTID != "xyz-789" {
			t.Errorf("GTID = %q, want %q", status.GTID, "xyz-789")
		}
	})

	t.Run("multi-line with processed_events and queue_size", func(t *testing.T) {
		response := "OK REPLICATION\nstatus: running\ncurrent_gtid: abc-123\nprocessed_events: 1234\nqueue_size: 5\nEND"
		status, err := parseReplicationStatusResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if status.ProcessedEvents != 1234 {
			t.Errorf("ProcessedEvents = %d, want 1234", status.ProcessedEvents)
		}
		if status.QueueSize != 5 {
			t.Errorf("QueueSize = %d, want 5", status.QueueSize)
		}
	})

	t.Run("processed_events defaults to zero", func(t *testing.T) {
		response := "OK REPLICATION\nstatus: running\ncurrent_gtid: abc\nEND"
		status, err := parseReplicationStatusResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if status.ProcessedEvents != 0 {
			t.Errorf("ProcessedEvents = %d, want 0", status.ProcessedEvents)
		}
		if status.QueueSize != 0 {
			t.Errorf("QueueSize = %d, want 0", status.QueueSize)
		}
	})

	t.Run("gtid key alias accepted", func(t *testing.T) {
		// Some server builds emit `gtid:` instead of `current_gtid:`.
		response := "OK REPLICATION\nstatus: stopped\ngtid: aaa-bbb\nEND"
		status, err := parseReplicationStatusResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if status.GTID != "aaa-bbb" {
			t.Errorf("GTID = %q, want %q", status.GTID, "aaa-bbb")
		}
		if status.Running {
			t.Error("Running should be false")
		}
	})

	t.Run("diagnostics fields", func(t *testing.T) {
		response := "OK REPLICATION\n" +
			"status: failed\n" +
			"current_gtid: abc-123\n" +
			"crc_errors: 3\n" +
			"schema_incompatible: true\n" +
			"last_error_code: 2017\n" +
			"last_error: undecodable binlog event\n" +
			"last_applied_unixtime: 1786368383\n" +
			"seconds_since_last_applied: 42\n" +
			"END"
		status, err := parseReplicationStatusResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if status.Running {
			t.Error("Running should be false for state 'failed'")
		}
		if status.State != "failed" {
			t.Errorf("State = %q, want %q", status.State, "failed")
		}
		if status.CRCErrors != 3 {
			t.Errorf("CRCErrors = %d, want 3", status.CRCErrors)
		}
		if !status.SchemaIncompatible {
			t.Error("SchemaIncompatible should be true")
		}
		if status.LastErrorCode != ErrorCode(2017) {
			t.Errorf("LastErrorCode = %d, want 2017", status.LastErrorCode)
		}
		if status.LastError != "undecodable binlog event" {
			t.Errorf("LastError = %q", status.LastError)
		}
		if status.LastAppliedUnixtime != 1786368383 {
			t.Errorf("LastAppliedUnixtime = %d, want 1786368383", status.LastAppliedUnixtime)
		}
		if status.SecondsSinceLastApplied != 42 {
			t.Errorf("SecondsSinceLastApplied = %d, want 42", status.SecondsSinceLastApplied)
		}
	})

	t.Run("state distinguishes a stop from a failure", func(t *testing.T) {
		// Running is false either way, so State is the only field that says
		// whether the reader was asked to stop or died.
		for _, state := range []string{"stopped", "failed", "not_configured"} {
			status, err := parseReplicationStatusResponse("OK REPLICATION\nstatus: " + state + "\nEND")
			if err != nil {
				t.Fatal(err)
			}
			if status.Running {
				t.Errorf("Running should be false for state %q", state)
			}
			if status.State != state {
				t.Errorf("State = %q, want %q", status.State, state)
			}
		}
	})

	t.Run("lag sentinel is passed through", func(t *testing.T) {
		// A server that has applied nothing yet reports -1 rather than 0, so a
		// caller can tell "no data" from "fully caught up".
		response := "OK REPLICATION\nstatus: running\nlast_applied_unixtime: 0\nseconds_since_last_applied: -1\nEND"
		status, err := parseReplicationStatusResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		if status.LastAppliedUnixtime != 0 {
			t.Errorf("LastAppliedUnixtime = %d, want 0", status.LastAppliedUnixtime)
		}
		if status.SecondsSinceLastApplied != -1 {
			t.Errorf("SecondsSinceLastApplied = %d, want -1", status.SecondsSinceLastApplied)
		}
	})

	t.Run("diagnostics absent on an older server", func(t *testing.T) {
		status, err := parseReplicationStatusResponse("OK REPLICATION\nstatus: running\ncurrent_gtid: abc\nEND")
		if err != nil {
			t.Fatal(err)
		}
		if status.CRCErrors != 0 || status.SchemaIncompatible ||
			status.LastErrorCode != ErrCodeNone || status.LastError != "" ||
			status.LastAppliedUnixtime != 0 || status.SecondsSinceLastApplied != 0 {
			t.Errorf("diagnostics should stay zero-valued; got %+v", status)
		}
	})
}

func TestParseDumpStatusResponse(t *testing.T) {
	response := `OK DUMP_STATUS
save_in_progress: false
load_in_progress: false
status: IDLE
filepath: /tmp/dump.bin
tables_processed: 3
tables_total: 5
elapsed_seconds: 1.5
END
`
	ds, err := parseDumpStatusResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	if ds.SaveInProgress {
		t.Error("SaveInProgress should be false")
	}
	if ds.Status != "IDLE" {
		t.Errorf("Status = %q, want %q", ds.Status, "IDLE")
	}
	if ds.TablesProcessed != 3 {
		t.Errorf("TablesProcessed = %d, want 3", ds.TablesProcessed)
	}
	if ds.ElapsedSeconds != 1.5 {
		t.Errorf("ElapsedSeconds = %f, want 1.5", ds.ElapsedSeconds)
	}
}

func TestParseCacheStatsFullSurface(t *testing.T) {
	response := strings.Join([]string{
		"OK CACHE_STATS",
		"",
		"# Cache",
		"enabled: true",
		"total_queries: 100",
		"cache_hits: 70",
		"cache_misses: 30",
		"hit_rate: 0.7000",
		"current_entries: 12",
		"current_memory_bytes: 4096",
		"invalidation_index_memory_bytes: 512",
		"invalidation_queue_memory_bytes: 256",
		"accounted_memory_bytes: 4864",
		"evictions: 3",
		"ttl_expirations: 2",
		"rejection_count: 6",
		"rejection_oversize: 1",
		"rejection_memory_budget: 2",
		"rejection_duplicate: 3",
		"stale_entry_removals: 4",
		"decompression_failures: 5",
		"stale_lru_entries: 7",
		"invalidations_immediate: 8",
		"invalidations_deferred: 9",
		"invalidations_batches: 10",
		"avg_cache_hit_time_ms: 0.250",
		"avg_cache_miss_time_ms: 3.500",
		"total_time_saved_ms: 227.500",
		"",
		"END",
	}, "\n")

	cs, err := parseCacheStatsResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	if !cs.Enabled || cs.TotalQueries != 100 || cs.Hits != 70 || cs.Misses != 30 {
		t.Errorf("counters = %+v", cs)
	}
	if cs.InvalidationIndexMemoryBytes != 512 || cs.InvalidationQueueMemoryBytes != 256 || cs.AccountedMemoryBytes != 4864 {
		t.Errorf("memory accounting = %+v", cs)
	}
	if cs.TTLExpirations != 2 || cs.RejectionCount != 6 || cs.RejectionOversize != 1 ||
		cs.RejectionMemoryBudget != 2 || cs.RejectionDuplicate != 3 {
		t.Errorf("rejection counters = %+v", cs)
	}
	if cs.StaleEntryRemovals != 4 || cs.DecompressionFailures != 5 || cs.StaleLRUEntries != 7 {
		t.Errorf("health counters = %+v", cs)
	}
	if cs.InvalidationsImmediate != 8 || cs.InvalidationsDeferred != 9 || cs.InvalidationsBatches != 10 {
		t.Errorf("invalidation counters = %+v", cs)
	}
	if cs.AvgHitTimeMs == nil || *cs.AvgHitTimeMs != 0.25 {
		t.Errorf("AvgHitTimeMs = %v, want 0.25", cs.AvgHitTimeMs)
	}
	if cs.AvgMissTimeMs == nil || *cs.AvgMissTimeMs != 3.5 {
		t.Errorf("AvgMissTimeMs = %v, want 3.5", cs.AvgMissTimeMs)
	}
	if cs.TotalTimeSavedMs != 227.5 {
		t.Errorf("TotalTimeSavedMs = %v, want 227.5", cs.TotalTimeSavedMs)
	}
}

func TestParseCacheStatsOmittedAveragesStayNil(t *testing.T) {
	// The server omits the averages while the matching counter is zero, which
	// must stay distinguishable from a measured 0.
	cs, err := parseCacheStatsResponse("OK CACHE_STATS\n\nenabled: false\ncache_hits: 0\n\nEND")
	if err != nil {
		t.Fatal(err)
	}
	if cs.AvgHitTimeMs != nil || cs.AvgMissTimeMs != nil {
		t.Errorf("averages = %v / %v, want nil", cs.AvgHitTimeMs, cs.AvgMissTimeMs)
	}
}

func TestParseCacheStatsResponseInvalid(t *testing.T) {
	if _, err := parseCacheStatsResponse("NOPE"); err == nil {
		t.Error("expected error for bad prefix")
	}
}

func TestParseDebugInfo(t *testing.T) {
	lines := []string{
		"query_time: 45.23",
		"index_time: 12.34",
		"filter_time: 2.10",
		"terms: 3",
		"ngrams: 10",
		"candidates: 1000",
		"after_intersection: 500",
		"after_not: 450",
		"after_filters: 100",
		"final: 50",
		"optimization: fast_path",
		"cache: miss",
		"limit: 100 (default)",
		"offset: 0 (default)",
	}

	debug := parseDebugInfo(lines)
	if debug.QueryTimeMs != 45.23 {
		t.Errorf("QueryTimeMs = %f, want 45.23", debug.QueryTimeMs)
	}
	if debug.IndexTimeMs != 12.34 {
		t.Errorf("IndexTimeMs = %f, want 12.34", debug.IndexTimeMs)
	}
	if debug.Terms != 3 {
		t.Errorf("Terms = %d, want 3", debug.Terms)
	}
	if debug.Candidates != 1000 {
		t.Errorf("Candidates = %d, want 1000", debug.Candidates)
	}
	if debug.Final != 50 {
		t.Errorf("Final = %d, want 50", debug.Final)
	}
	if debug.Optimization != "fast_path" {
		t.Errorf("Optimization = %q, want %q", debug.Optimization, "fast_path")
	}
	if debug.Cache != "miss" {
		t.Errorf("Cache = %q, want %q", debug.Cache, "miss")
	}
}
