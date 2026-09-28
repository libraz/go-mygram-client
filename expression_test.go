package mygram

import (
	"strings"
	"testing"
)

// Web-syntax search expression parsing and conversion (expression.go),
// mirroring mygram-db's tests/client/search_expression_test.cpp so the two
// clients agree on the query text a given web-syntax expression produces.

func TestParseSearchExpression(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantRequired []string
		wantExcluded []string
		wantComplex  bool
	}{
		{
			name:         "single word (implicit AND)",
			input:        "hello",
			wantRequired: []string{"hello"},
		},
		{
			name:         "multiple words (implicit AND)",
			input:        "hello world",
			wantRequired: []string{"hello", "world"},
		},
		{
			name:         "required keywords",
			input:        "+golang +rust",
			wantRequired: []string{"golang", "rust"},
		},
		{
			name:         "excluded keyword",
			input:        "hello -spam",
			wantRequired: []string{"hello"},
			wantExcluded: []string{"spam"},
		},
		{
			name:         "mixed modifiers",
			input:        "+golang -deprecated tutorial",
			wantRequired: []string{"golang", "tutorial"},
			wantExcluded: []string{"deprecated"},
		},
		{
			name:         "quoted phrase (implicit AND)",
			input:        `"hello world" test`,
			wantRequired: []string{"hello world", "test"},
		},
		{
			name:        "OR operator",
			input:       "python OR ruby",
			wantComplex: true,
		},
		{
			name:         "grouping",
			input:        "(golang OR rust) -old",
			wantExcluded: []string{"old"},
			wantComplex:  true,
		},
		{
			name:         "Japanese keywords",
			input:        "keyword1 keyword2 -excluded",
			wantRequired: []string{"keyword1", "keyword2"},
			wantExcluded: []string{"excluded"},
		},
		{
			name:         "full-width space",
			input:        "hello　world",
			wantRequired: []string{"hello", "world"},
		},
		{
			name:         "apostrophe is literal text, not a quote",
			input:        "don't stop",
			wantRequired: []string{"don't", "stop"},
		},
		{
			name:        "OR recognized case-insensitively",
			input:       "python or ruby",
			wantComplex: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, err := ParseSearchExpression(tt.input)
			if err != nil {
				t.Fatal(err)
			}

			assertStringSlice(t, "RequiredTerms", expr.RequiredTerms, tt.wantRequired)
			assertStringSlice(t, "ExcludedTerms", expr.ExcludedTerms, tt.wantExcluded)
			// OptionalTerms is unused: every unprefixed term is required.
			assertStringSlice(t, "OptionalTerms", expr.OptionalTerms, nil)

			if HasComplexExpression(expr) != tt.wantComplex {
				t.Errorf("HasComplexExpression = %v, want %v", HasComplexExpression(expr), tt.wantComplex)
			}
		})
	}
}

// TestOrSubstringInsideOrdinaryWordIsNotComplex guards against detecting the
// OR operator by substring: ORANGE, ORDER, FLOOR and orchestra must parse as
// one ordinary required term, not a boolean OR.
func TestOrSubstringInsideOrdinaryWordIsNotComplex(t *testing.T) {
	for _, term := range []string{"ORANGE", "ORDER", "FLOOR", "orchestra"} {
		expr, err := ParseSearchExpression(term)
		if err != nil {
			t.Fatalf("%s: %v", term, err)
		}
		if HasComplexExpression(expr) {
			t.Errorf("%s: HasComplexExpression = true, want false", term)
		}
		if len(expr.RequiredTerms) != 1 || expr.RequiredTerms[0] != term {
			t.Errorf("%s: RequiredTerms = %v, want [%s]", term, expr.RequiredTerms, term)
		}
	}
}

func TestRequiredWithParenthesizedOr(t *testing.T) {
	expr, err := ParseSearchExpression("+golang +(tutorial OR guide)")
	if err != nil {
		t.Fatal(err)
	}
	if len(expr.RequiredTerms) != 2 || expr.RequiredTerms[0] != "golang" {
		t.Errorf("RequiredTerms = %v", expr.RequiredTerms)
	}
	if !HasComplexExpression(expr) {
		t.Error("HasComplexExpression = false, want true")
	}
}

func TestComplexExpression(t *testing.T) {
	expr, err := ParseSearchExpression("+golang +(tutorial OR guide) -old -deprecated")
	if err != nil {
		t.Fatal(err)
	}
	assertStringSlice(t, "RequiredTerms[0]", expr.RequiredTerms[:1], []string{"golang"})
	assertStringSlice(t, "ExcludedTerms", expr.ExcludedTerms, []string{"old", "deprecated"})
	if !HasComplexExpression(expr) {
		t.Error("HasComplexExpression = false, want true")
	}
}

func TestSimplifySearchExpression(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantMain string
		wantAnd  []string
		wantNot  []string
	}{
		{
			name:     "single word",
			input:    "hello",
			wantMain: "hello",
		},
		{
			name:     "multiple words",
			input:    "hello world",
			wantMain: "hello",
			wantAnd:  []string{"world"},
		},
		{
			name:     "with exclusion",
			input:    "hello -spam",
			wantMain: "hello",
			wantNot:  []string{"spam"},
		},
		{
			name:     "complex expression",
			input:    "+golang +rust -deprecated tutorial",
			wantMain: "golang",
			wantAnd:  []string{"rust", "tutorial"},
			wantNot:  []string{"deprecated"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := SimplifySearchExpression(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if result.MainTerm != tt.wantMain {
				t.Errorf("MainTerm = %q, want %q", result.MainTerm, tt.wantMain)
			}
			assertStringSlice(t, "AndTerms", result.AndTerms, tt.wantAnd)
			assertStringSlice(t, "NotTerms", result.NotTerms, tt.wantNot)
		})
	}

	t.Run("no positive terms", func(t *testing.T) {
		_, err := SimplifySearchExpression("-only -negative")
		if err == nil {
			t.Error("expected error")
		}
	})

	// The legacy MainTerm/AndTerms/NotTerms shape has no way to represent
	// boolean OR: every consumer re-applies wire quoting to MainTerm, which
	// would wrap "python OR ruby" as one opaque literal-phrase term instead
	// of parsing it as boolean OR. Returning an error is correct.
	for _, tt := range []struct {
		name  string
		input string
	}{
		{"OR-only expression rejected", "python OR ruby"},
		{"parenthesized OR expression rejected", "(python OR ruby)"},
		{"required with OR sub-expression rejected", "+golang (tutorial OR guide)"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SimplifySearchExpression(tt.input)
			if err == nil {
				t.Fatal("expected error")
			}
			if _, ok := err.(*ValidationError); !ok {
				t.Errorf("err = %T, want *ValidationError", err)
			}
		})
	}
}

func TestConvertSearchExpression(t *testing.T) {
	result, err := ConvertSearchExpression("+golang -old")
	if err != nil {
		t.Fatal(err)
	}
	if result != "golang AND NOT old" {
		t.Errorf("result = %q, want %q", result, "golang AND NOT old")
	}
}

func TestToQueryString(t *testing.T) {
	expr := &SearchExpression{
		RequiredTerms: []string{"golang", "tutorial"},
		ExcludedTerms: []string{"deprecated"},
	}
	result := ToQueryString(expr)
	expected := "golang AND tutorial AND NOT deprecated"
	if result != expected {
		t.Errorf("result = %q, want %q", result, expected)
	}
}

func TestEmptyExpressionIsRejected(t *testing.T) {
	_, err := ParseSearchExpression("")
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("err = %T, want *ValidationError", err)
	}
}

func TestInvalidMissingTermAfterPlus(t *testing.T) {
	if _, err := ParseSearchExpression("+"); err == nil {
		t.Fatal("expected error")
	}
}

func TestInvalidUnbalancedParens(t *testing.T) {
	if _, err := ParseSearchExpression("(golang tutorial"); err == nil {
		t.Fatal("expected error")
	}
}

func TestWhitespaceHandling(t *testing.T) {
	expr, err := ParseSearchExpression("  +golang   -old   tutorial  ")
	if err != nil {
		t.Fatal(err)
	}
	assertStringSlice(t, "RequiredTerms", expr.RequiredTerms, []string{"golang", "tutorial"})
	assertStringSlice(t, "ExcludedTerms", expr.ExcludedTerms, []string{"old"})
}

func TestJapaneseTerms(t *testing.T) {
	expr, err := ParseSearchExpression("+日本語 -古い チュートリアル")
	if err != nil {
		t.Fatal(err)
	}
	assertStringSlice(t, "RequiredTerms", expr.RequiredTerms, []string{"日本語", "チュートリアル"})
	assertStringSlice(t, "ExcludedTerms", expr.ExcludedTerms, []string{"古い"})
}

func TestQuotedPhrase(t *testing.T) {
	expr, err := ParseSearchExpression(`"machine learning" tutorial`)
	if err != nil {
		t.Fatal(err)
	}
	// Stored as plain phrase text, not pre-quoted: ToQueryString applies the
	// shared wire-quoting decision exactly once, at its own destination.
	assertStringSlice(t, "RequiredTerms", expr.RequiredTerms, []string{"machine learning", "tutorial"})
	if got := ToQueryString(expr); got != `"machine learning" AND tutorial` {
		t.Errorf("ToQueryString = %q", got)
	}
}

func TestQuotedPhraseWithExclusion(t *testing.T) {
	expr, err := ParseSearchExpression(`"deep learning" -tensorflow`)
	if err != nil {
		t.Fatal(err)
	}
	assertStringSlice(t, "RequiredTerms", expr.RequiredTerms, []string{"deep learning"})
	assertStringSlice(t, "ExcludedTerms", expr.ExcludedTerms, []string{"tensorflow"})
	if got := ToQueryString(expr); got != `"deep learning" AND NOT tensorflow` {
		t.Errorf("ToQueryString = %q", got)
	}
}

func TestQuotedPhraseWithOr(t *testing.T) {
	expr, err := ParseSearchExpression(`"machine learning" OR "deep learning"`)
	if err != nil {
		t.Fatal(err)
	}
	if !HasComplexExpression(expr) {
		t.Fatal("HasComplexExpression = false, want true")
	}
	if !containsAll(expr.RawExpression, `"machine learning"`, `"deep learning"`) {
		t.Errorf("RawExpression = %q, want both quoted phrases", expr.RawExpression)
	}
}

func TestFullWidthSpace(t *testing.T) {
	expr, err := ParseSearchExpression("golang　tutorial")
	if err != nil {
		t.Fatal(err)
	}
	assertStringSlice(t, "RequiredTerms", expr.RequiredTerms, []string{"golang", "tutorial"})
}

func TestMixedSpaces(t *testing.T) {
	expr, err := ParseSearchExpression("golang tutorial　日本語")
	if err != nil {
		t.Fatal(err)
	}
	assertStringSlice(t, "RequiredTerms", expr.RequiredTerms, []string{"golang", "tutorial", "日本語"})
}

func TestQuotedPhraseToQueryString(t *testing.T) {
	result, err := ConvertSearchExpression(`"machine learning" tutorial`)
	if err != nil {
		t.Fatal(err)
	}
	if result != `"machine learning" AND tutorial` {
		t.Errorf("result = %q", result)
	}
}

func TestEmojiInExpression(t *testing.T) {
	expr, err := ParseSearchExpression("\U0001F600 tutorial")
	if err != nil {
		t.Fatal(err)
	}
	assertStringSlice(t, "RequiredTerms", expr.RequiredTerms, []string{"\U0001F600", "tutorial"})
}

func TestEmojiWithPrefixOperators(t *testing.T) {
	expr, err := ParseSearchExpression("+\U0001F600 -\U0001F389")
	if err != nil {
		t.Fatal(err)
	}
	assertStringSlice(t, "RequiredTerms", expr.RequiredTerms, []string{"\U0001F600"})
	assertStringSlice(t, "ExcludedTerms", expr.ExcludedTerms, []string{"\U0001F389"})
}

func TestEmojiInQuotedPhrase(t *testing.T) {
	expr, err := ParseSearchExpression("\"Hello \U0001F600 World\"")
	if err != nil {
		t.Fatal(err)
	}
	assertStringSlice(t, "RequiredTerms", expr.RequiredTerms, []string{"Hello \U0001F600 World"})
	if got := ToQueryString(expr); got != "\"Hello \U0001F600 World\"" {
		t.Errorf("ToQueryString = %q", got)
	}
}

func TestEmojiWithOr(t *testing.T) {
	expr, err := ParseSearchExpression("\U0001F600 OR \U0001F389")
	if err != nil {
		t.Fatal(err)
	}
	if !HasComplexExpression(expr) {
		t.Fatal("HasComplexExpression = false, want true")
	}
	if !containsAll(expr.RawExpression, "\U0001F600", "\U0001F389") {
		t.Errorf("RawExpression = %q", expr.RawExpression)
	}
}

func TestEmojiToQueryString(t *testing.T) {
	result, err := ConvertSearchExpression("\U0001F600 tutorial -\U0001F389")
	if err != nil {
		t.Fatal(err)
	}
	if !containsAll(result, "\U0001F600", "tutorial", "NOT \U0001F389") {
		t.Errorf("result = %q", result)
	}
}

func TestParenthesizedImplicitAndPreservesWhitespace(t *testing.T) {
	got, err := ConvertSearchExpression("+(machine learning)")
	if err != nil {
		t.Fatal(err)
	}
	if got != "(machine learning)" {
		t.Errorf("result = %q, want %q", got, "(machine learning)")
	}

	got, err = ConvertSearchExpression(`+(a "quoted phrase" b)`)
	if err != nil {
		t.Fatal(err)
	}
	if got != `(a "quoted phrase" b)` {
		t.Errorf("result = %q, want %q", got, `(a "quoted phrase" b)`)
	}
}

// TestMinusInsideGroupBecomesNot: a bare '-' has no meaning the server's AST
// parser recognizes; only the literal word NOT does. A minus sign inside a
// parenthesized group must translate to "NOT term", not pass through as a
// raw '-' character.
func TestMinusInsideGroupBecomesNot(t *testing.T) {
	got, err := ConvertSearchExpression("+(tutorial -old)")
	if err != nil {
		t.Fatal(err)
	}
	if got != "(tutorial NOT old)" {
		t.Errorf("result = %q, want %q", got, "(tutorial NOT old)")
	}
}

func TestMalformedOrChainsAreRejected(t *testing.T) {
	for _, expression := range []string{"a OR -b", "a b OR", "foo OR"} {
		if _, err := ConvertSearchExpression(expression); err == nil {
			t.Errorf("%q: expected error", expression)
		}
	}
}

func TestPlusAndMinusInsideTermsAreLiteralText(t *testing.T) {
	for _, term := range []string{"COVID-19", "e-mail", "UTF-8", "C++"} {
		expr, err := ParseSearchExpression(term)
		if err != nil {
			t.Fatalf("%s: %v", term, err)
		}
		assertStringSlice(t, term+": RequiredTerms", expr.RequiredTerms, []string{term})
		if len(expr.ExcludedTerms) != 0 {
			t.Errorf("%s: ExcludedTerms = %v, want empty", term, expr.ExcludedTerms)
		}

		converted, err := ConvertSearchExpression(term)
		if err != nil {
			t.Fatalf("%s: %v", term, err)
		}
		if converted != term {
			t.Errorf("ConvertSearchExpression(%q) = %q, want %q", term, converted, term)
		}
	}
}

func TestLeadingPlusAndMinusRemainUnaryOperators(t *testing.T) {
	expr, err := ParseSearchExpression("+COVID-19 -e-mail C++")
	if err != nil {
		t.Fatal(err)
	}
	assertStringSlice(t, "RequiredTerms", expr.RequiredTerms, []string{"COVID-19", "C++"})
	assertStringSlice(t, "ExcludedTerms", expr.ExcludedTerms, []string{"e-mail"})
}

func TestToQueryStringWrapsOrInParens(t *testing.T) {
	result, err := ConvertSearchExpression("python OR ruby")
	if err != nil {
		t.Fatal(err)
	}
	if result != "(python OR ruby)" {
		t.Errorf("result = %q, want %q", result, "(python OR ruby)")
	}
}

// TestOrCaseInsensitive: the server recognizes OR case-insensitively on both
// the top-level clause scanner and the boolean AST tokenizer, so "or"/"Or"
// must be recognized as the operator, not sent as a literal term.
func TestOrCaseInsensitive(t *testing.T) {
	for _, input := range []string{"python or ruby", "python Or ruby", "python OR ruby"} {
		result, err := ConvertSearchExpression(input)
		if err != nil {
			t.Fatalf("%q: %v", input, err)
		}
		if result != "(python OR ruby)" {
			t.Errorf("ConvertSearchExpression(%q) = %q, want %q", input, result, "(python OR ruby)")
		}
	}
}

// TestApostropheIsLiteralText guards the tokenizer against treating a bare
// apostrophe as an alternate quote character: only '"' opens a phrase, so
// "don't" must survive as one term rather than starting an unterminated
// quoted region.
func TestApostropheIsLiteralText(t *testing.T) {
	result, err := ConvertSearchExpression("don't stop")
	if err != nil {
		t.Fatal(err)
	}
	if result != `"don't" AND stop` {
		t.Errorf("result = %q, want %q", result, `"don't" AND stop`)
	}
}

// TestToQueryStringQuotesLiteralTermsCollidingWithKeywords: a literal term
// that collides with a server clause keyword (AND, OR, NOT, FILTER, SORT,
// LIMIT, OFFSET, HIGHLIGHT, FUZZY, FACET, ORDER) must be quoted, or the
// server's clause scanner and AST tokenizer both re-read it as the operator
// instead of search text.
func TestToQueryStringQuotesLiteralTermsCollidingWithKeywords(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"golang not", `golang AND "not"`},
		{"filter", `"filter"`},
		{"sort", `"sort"`},
		{"NOT", `"NOT"`},
		{"golang NOT old", `golang AND "NOT" AND old`},
		{"+not", `"not"`},
	}
	for _, tc := range cases {
		got, err := ConvertSearchExpression(tc.input)
		if err != nil {
			t.Fatalf("%q: %v", tc.input, err)
		}
		if got != tc.want {
			t.Errorf("ConvertSearchExpression(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func assertStringSlice(t *testing.T, name string, got, want []string) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if len(got) != len(want) {
		t.Errorf("%s: len = %d, want %d (got %v, want %v)", name, len(got), len(want), got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s[%d] = %q, want %q", name, i, got[i], want[i])
		}
	}
}

func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
