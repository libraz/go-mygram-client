package mygram

import (
	"strings"
	"testing"
)

// Input validation and protocol escaping (validate.go).

func TestEnsureSafeValue(t *testing.T) {
	t.Run("valid values", func(t *testing.T) {
		values := []string{
			"hello",
			"hello world",
			"Japanese text test",
			"multi word query",
			"special!@#$%^&*()",
		}
		for _, v := range values {
			if err := ensureSafeValue(v, "test"); err != nil {
				t.Errorf("unexpected error for %q: %v", v, err)
			}
		}
	})

	t.Run("values with control characters", func(t *testing.T) {
		values := []string{
			"hello\x00world",
			"test\nnewline",
			"test\ttab",
			"test\rreturn",
			"test\x7fdelete",
		}
		for _, v := range values {
			err := ensureSafeValue(v, "test")
			if err == nil {
				t.Errorf("expected error for %q", v)
			}
			if _, ok := err.(*ValidationError); !ok {
				t.Errorf("expected ValidationError for %q, got %T", v, err)
			}
		}
	})
}

func TestEnsureSafeValues(t *testing.T) {
	err := ensureSafeValues([]string{"hello", "world"}, "terms")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	err = ensureSafeValues([]string{"hello", "bad\x00value"}, "terms")
	if err == nil {
		t.Error("expected error")
	}
}

func TestEnsureSafeFilters(t *testing.T) {
	err := ensureSafeFilters(map[string]string{"status": "active", "lang": "ja"})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	err = ensureSafeFilters(map[string]string{"bad\nkey": "value"})
	if err == nil {
		t.Error("expected error for bad key")
	}

	err = ensureSafeFilters(map[string]string{"key": "bad\x00value"})
	if err == nil {
		t.Error("expected error for bad value")
	}
}

func TestCalculateQueryExpressionLength(t *testing.T) {
	length := calculateQueryExpressionLength(
		"hello",
		[]string{"world", "test"},
		[]string{"spam"},
		map[string]string{"status": "1"},
		nil,
		"created_at",
	)
	// hello=5 + world=5 + test=4 + spam=4 + status=6 + 1=1 + created_at=10 → 35
	if length != 35 {
		t.Errorf("length = %d, want 35", length)
	}
}

func TestCalculateQueryExpressionLengthCountsFilterConditions(t *testing.T) {
	length := calculateQueryExpressionLength(
		"hello",
		nil,
		nil,
		nil,
		[]FilterCondition{{Key: "status", Op: FilterGreaterThan, Value: "1"}},
		"",
	)
	// hello=5 + status=6 + 1=1 → 12
	if length != 12 {
		t.Errorf("length = %d, want 12", length)
	}
}

func TestEnsureQueryLengthWithinLimit(t *testing.T) {
	// Unlimited
	err := ensureQueryLengthWithinLimit("test", nil, nil, nil, nil, "", 0)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Within limit
	err = ensureQueryLengthWithinLimit("test", nil, nil, nil, nil, "", 128)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Exceeds limit
	err = ensureQueryLengthWithinLimit("test", nil, nil, nil, nil, "", 3)
	if err == nil {
		t.Error("expected error")
	}
}

func TestValidateTableName(t *testing.T) {
	if err := validateTableName("threads"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := validateTableName(""); err == nil {
		t.Error("expected error for empty table")
	}
	if err := validateTableName("bad table"); err == nil {
		t.Error("expected error for table with space")
	}
}

func TestValidateIdentifier(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		valid := []string{"threads", "记事", "user_id", "a1"}
		for _, v := range valid {
			if err := validateIdentifier(v, "field"); err != nil {
				t.Errorf("validateIdentifier(%q) returned error: %v", v, err)
			}
		}
	})

	t.Run("empty rejected", func(t *testing.T) {
		err := validateIdentifier("", "table name")
		if err == nil {
			t.Fatal("expected error for empty value")
		}
		if !strings.Contains(err.Error(), "table name") {
			t.Errorf("error should mention field name, got: %v", err)
		}
	})

	t.Run("whitespace rejected", func(t *testing.T) {
		cases := []string{"a b", " a", "a ", "a　b"}
		for _, v := range cases {
			err := validateIdentifier(v, "field")
			if err == nil {
				t.Errorf("expected error for whitespace in %q", v)
				continue
			}
			// Tab is also a control char so it can hit either path.
		}
	})

	t.Run("control character rejected", func(t *testing.T) {
		cases := []string{"a\x00b", "a\nb", "a\rb", "a\tb", "a\x7fb"}
		for _, v := range cases {
			if err := validateIdentifier(v, "field"); err == nil {
				t.Errorf("expected error for control char in %q", v)
			}
		}
	})

	t.Run("field name in error", func(t *testing.T) {
		err := validateIdentifier("a b", "filter key")
		if err == nil || !strings.Contains(err.Error(), "filter key") {
			t.Errorf("error should mention 'filter key', got: %v", err)
		}
	})
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
	// Whitespace in a tag is no longer rejected here: appendHighlightClause
	// quotes each tag through the shared wire-quoting encoder before it
	// reaches the wire (wire_quoting.go), matching the C++ client's
	// validation (ValidateNoControlCharacters only).
	if err := validateHighlight(&HighlightOptions{OpenTag: "<em class='x'>", CloseTag: "</em>"}); err != nil {
		t.Errorf("whitespace in tag: err=%v", err)
	}

	bad := []HighlightOptions{
		{OpenTag: "<b>"},   // missing close
		{CloseTag: "</b>"}, // missing open
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

func TestEscapeQueryString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", `""`},
		{"simple word", "hello", "hello"},
		{"unicode passthrough", "こんにちは", "こんにちは"},
		{"with space", "hello world", `"hello world"`},
		{"with single quote", "it's", `"it's"`},
		{"with double quote", `say "hi"`, `"say \"hi\""`},
		{"with backslash", `a\b c`, `"a\\b c"`},
		{"control chars escaped reversibly when quoted", "a\x00b c", `"a\x00b c"`},
		{"tab triggers quoting and is escaped reversibly", "a\tb", `"a\tb"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := escapeQueryString(tt.in); got != tt.want {
				t.Errorf("escapeQueryString(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestEscapeQueryStringQuotesBackslash(t *testing.T) {
	// A backslash forces quoting and is escaped inside the quotes, so the
	// server's tokenizer decodes it back to the original value.
	if got := escapeQueryString(`a\b`); got != `"a\\b"` {
		t.Errorf("escapeQueryString = %q, want %q", got, `"a\\b"`)
	}
}

func TestEscapeQueryStringQuotesReservedWords(t *testing.T) {
	for _, word := range []string{"AND", "or", "Not", "FILTER", "sort", "LIMIT", "offset", "HIGHLIGHT", "FUZZY", "FACET", "ORDER"} {
		got := escapeQueryString(word)
		if !strings.HasPrefix(got, `"`) {
			t.Errorf("escapeQueryString(%q) = %q, want a quoted token", word, got)
		}
	}
}

func TestEscapeQueryStringQuotesParentheses(t *testing.T) {
	if got := escapeQueryString("(golang)"); got != `"(golang)"` {
		t.Errorf("escapeQueryString = %q, want %q", got, `"(golang)"`)
	}
}

func TestEscapeQueryStringLeavesOrdinaryTermsVerbatim(t *testing.T) {
	for _, term := range []string{"golang", "android", "記事", "v1.9.0"} {
		if got := escapeQueryString(term); got != term {
			t.Errorf("escapeQueryString(%q) = %q, want it unquoted", term, got)
		}
	}
}

func TestEscapeProtocolToken(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{"plain value sent verbatim", "12345", "12345"},
		{"multibyte value sent verbatim", "記事-1", "記事-1"},
		{"empty value quoted", "", `""`},
		{"whitespace quoted", "a b", `"a b"`},
		{"quote escaped", `a"b`, `"a\"b"`},
		{"backslash escaped", `a\b`, `"a\\b"`},
		{"single quote quoted", "a'b", `"a'b"`},
		{"newline escaped reversibly", "a\nb", `"a\nb"`},
		{"tab escaped reversibly", "a\tb", `"a\tb"`},
		{"null escaped as hex", "a\x00b", `"a\x00b"`},
		{"delete escaped as hex", "a\x7fb", `"a\x7Fb"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := escapeProtocolToken(tc.value); got != tc.want {
				t.Errorf("escapeProtocolToken(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

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
