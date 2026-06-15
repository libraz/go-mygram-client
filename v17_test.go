package mygram

import (
	"strings"
	"testing"
)

// connectRecording wires a client to a recording mock server and connects it.
func connectRecording(t *testing.T, response string) (*Client, *recordingServer) {
	t.Helper()
	srv, addr, cleanup := newRecordingServer(t, response)
	t.Cleanup(cleanup)
	c := NewClient(addr)
	c.config.MaxQueryLength = 0
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return c, srv
}

// -----------------------------------------------------------------------------
// database-qualified table identity helpers
// -----------------------------------------------------------------------------

func TestQualifyTableIdentity(t *testing.T) {
	tests := []struct {
		name     string
		table    string
		database string
		want     string
		wantErr  bool
	}{
		{"bare", "articles", "", "articles", false},
		{"qualified", "articles", "app_db", "app_db.articles", false},
		{"empty table", "", "app_db", "", true},
		{"whitespace table", "a b", "app_db", "", true},
		{"dotted database", "articles", "a.b", "", true},
		{"dotted table with db", "a.b", "app_db", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := QualifyTableIdentity(tt.table, tt.database)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("QualifyTableIdentity = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseTableIdentity(t *testing.T) {
	tests := []struct {
		name     string
		identity string
		wantDB   string
		wantTbl  string
		wantErr  bool
	}{
		{"bare", "articles", "", "articles", false},
		{"qualified", "app_db.articles", "app_db", "articles", false},
		{"first dot only", "a.b.c", "a", "b.c", false},
		{"empty", "", "", "", true},
		{"empty database", ".articles", "", "", true},
		{"empty table", "app_db.", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, tbl, err := ParseTableIdentity(tt.identity)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got (%q, %q)", db, tbl)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if db != tt.wantDB || tbl != tt.wantTbl {
				t.Errorf("ParseTableIdentity = (%q, %q), want (%q, %q)", db, tbl, tt.wantDB, tt.wantTbl)
			}
		})
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

// -----------------------------------------------------------------------------
// quoting (escapeQueryString / quoteCommandArgument)
// -----------------------------------------------------------------------------

func TestQuoteCommandArgument(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"empty", "", `""`},
		{"plain", "info", "info"},
		{"with space", "two words", `"two words"`},
		{"with quote", `a"b`, `"a\"b"`},
		{"with backslash", `a\b`, `"a\\b"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := quoteCommandArgument(tt.value, "value")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("quoteCommandArgument(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestQuoteCommandArgumentRejectsControlChar(t *testing.T) {
	if _, err := quoteCommandArgument("a\x01b", "value"); err == nil {
		t.Fatal("expected error for control character")
	}
}

func TestEscapeQueryStringBackslashNotQuoted(t *testing.T) {
	// A lone backslash does not force quoting for query strings (EscapeQueryString).
	if got := escapeQueryString(`a\b`); got != `a\b` {
		t.Errorf("escapeQueryString = %q, want %q", got, `a\b`)
	}
}

// -----------------------------------------------------------------------------
// SearchRaw
// -----------------------------------------------------------------------------

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
	want := `SEARCH articles "python OR (ruby AND rails)" LIMIT 50`
	if cmd != want {
		t.Errorf("command = %q, want %q", cmd, want)
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
	c, srv := connectRecording(t, "OK RESULTS 0\r\n")
	if _, err := c.SearchRawWithHighlights("articles", "go", SearchRawOptions{}); err != nil {
		t.Fatalf("SearchRawWithHighlights: %v", err)
	}
	if cmd := srv.lastCommand(); !strings.Contains(cmd, "HIGHLIGHT") {
		t.Errorf("command %q missing HIGHLIGHT", cmd)
	}
}

func TestSearchWithHighlights(t *testing.T) {
	c, srv := connectRecording(t, "OK RESULTS 0\r\n")
	if _, err := c.SearchWithHighlights("articles", "go", SearchOptions{}); err != nil {
		t.Fatalf("SearchWithHighlights: %v", err)
	}
	if cmd := srv.lastCommand(); !strings.Contains(cmd, "HIGHLIGHT") {
		t.Errorf("command %q missing HIGHLIGHT", cmd)
	}
}

// -----------------------------------------------------------------------------
// SET / SHOW VARIABLES
// -----------------------------------------------------------------------------

func TestSetVariableCommand(t *testing.T) {
	c, srv := connectRecording(t, "+OK\r\n\r\n")
	if err := c.SetVariable("logging.level", "debug value"); err != nil {
		t.Fatalf("SetVariable: %v", err)
	}
	cmd := srv.lastCommand()
	want := `SET logging.level = "debug value"`
	if cmd != want {
		t.Errorf("command = %q, want %q", cmd, want)
	}
}

func TestSetVariableInvalidName(t *testing.T) {
	c := NewClient("127.0.0.1:1")
	if err := c.SetVariable("bad name", "x"); err == nil {
		t.Fatal("expected error for whitespace in variable name")
	}
}

func TestShowVariablesNoPattern(t *testing.T) {
	c, srv := connectRecording(t, "+OK\r\n\r\n")
	if _, err := c.ShowVariables(""); err != nil {
		t.Fatalf("ShowVariables: %v", err)
	}
	if cmd := srv.lastCommand(); cmd != "SHOW VARIABLES" {
		t.Errorf("command = %q, want %q", cmd, "SHOW VARIABLES")
	}
}

func TestShowVariablesWithPattern(t *testing.T) {
	c, srv := connectRecording(t, "+OK\r\n\r\n")
	if _, err := c.ShowVariables("logging%"); err != nil {
		t.Fatalf("ShowVariables: %v", err)
	}
	if cmd := srv.lastCommand(); cmd != "SHOW VARIABLES LIKE logging%" {
		t.Errorf("command = %q, want %q", cmd, "SHOW VARIABLES LIKE logging%")
	}
}

// -----------------------------------------------------------------------------
// SYNC family
// -----------------------------------------------------------------------------

func TestSyncCommand(t *testing.T) {
	c, srv := connectRecording(t, "OK SYNC STARTED articles\r\n")
	if _, err := c.Sync("articles"); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if cmd := srv.lastCommand(); cmd != "SYNC articles" {
		t.Errorf("command = %q, want %q", cmd, "SYNC articles")
	}
}

func TestSyncStopAll(t *testing.T) {
	c, srv := connectRecording(t, "OK\r\n")
	if _, err := c.SyncStop(""); err != nil {
		t.Fatalf("SyncStop: %v", err)
	}
	if cmd := srv.lastCommand(); cmd != "SYNC STOP" {
		t.Errorf("command = %q, want %q", cmd, "SYNC STOP")
	}
}

func TestSyncStopTable(t *testing.T) {
	c, srv := connectRecording(t, "OK\r\n")
	if _, err := c.SyncStop("articles"); err != nil {
		t.Fatalf("SyncStop: %v", err)
	}
	if cmd := srv.lastCommand(); cmd != "SYNC STOP articles" {
		t.Errorf("command = %q, want %q", cmd, "SYNC STOP articles")
	}
}

func TestSyncStatusCommand(t *testing.T) {
	// SYNC STATUS is a multi-line, END-terminated response that ends with an
	// extra blank line (END\r\n\r\n); the framing must capture it whole.
	resp := "OK SYNC_STATUS\r\narticles running 50%\r\nEND\r\n\r\n"
	c, srv := connectRecording(t, resp)
	got, err := c.SyncStatus()
	if err != nil {
		t.Fatalf("SyncStatus: %v", err)
	}
	if cmd := srv.lastCommand(); cmd != "SYNC STATUS" {
		t.Errorf("command = %q, want %q", cmd, "SYNC STATUS")
	}
	if !strings.Contains(got, "articles running 50%") {
		t.Errorf("response missing status line; got %q", got)
	}
}

// -----------------------------------------------------------------------------
// response framing
// -----------------------------------------------------------------------------

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

// -----------------------------------------------------------------------------
// DUMP filepath quoting
// -----------------------------------------------------------------------------

func TestDumpSaveQuotesFilepathWithSpace(t *testing.T) {
	c, srv := connectRecording(t, "OK DUMP_STARTED /tmp/my dump.bin\r\n")
	if _, err := c.DumpSave("/tmp/my dump.bin"); err != nil {
		t.Fatalf("DumpSave: %v", err)
	}
	cmd := srv.lastCommand()
	want := `DUMP SAVE "/tmp/my dump.bin"`
	if cmd != want {
		t.Errorf("command = %q, want %q", cmd, want)
	}
}

func TestDumpSavePlainFilepathUnquoted(t *testing.T) {
	c, srv := connectRecording(t, "OK DUMP_STARTED /tmp/dump.bin\r\n")
	if _, err := c.DumpSave("/tmp/dump.bin"); err != nil {
		t.Fatalf("DumpSave: %v", err)
	}
	if cmd := srv.lastCommand(); cmd != "DUMP SAVE /tmp/dump.bin" {
		t.Errorf("command = %q, want %q", cmd, "DUMP SAVE /tmp/dump.bin")
	}
}
