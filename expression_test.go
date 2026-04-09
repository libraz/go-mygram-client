package mygram

import (
	"testing"
)

func TestParseSearchExpression(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantRequired []string
		wantExcluded []string
		wantOptional []string
		wantComplex  bool
	}{
		{
			name:         "single word",
			input:        "hello",
			wantOptional: []string{"hello"},
		},
		{
			name:         "multiple words",
			input:        "hello world",
			wantOptional: []string{"hello", "world"},
		},
		{
			name:         "required keywords",
			input:        "+golang +rust",
			wantRequired: []string{"golang", "rust"},
		},
		{
			name:         "excluded keyword",
			input:        "hello -spam",
			wantOptional: []string{"hello"},
			wantExcluded: []string{"spam"},
		},
		{
			name:         "mixed modifiers",
			input:        "+golang -deprecated tutorial",
			wantRequired: []string{"golang"},
			wantExcluded: []string{"deprecated"},
			wantOptional: []string{"tutorial"},
		},
		{
			name:         "quoted phrase",
			input:        `"hello world" test`,
			wantOptional: []string{"hello world", "test"},
		},
		{
			name:         "OR operator",
			input:        "python OR ruby",
			wantOptional: []string{"python", "ruby"},
			wantComplex:  true,
		},
		{
			name:         "grouping",
			input:        "(golang OR rust) -old",
			wantOptional: []string{"golang", "rust"},
			wantExcluded: []string{"old"},
			wantComplex:  true,
		},
		{
			name:         "Japanese keywords",
			input:        "keyword1 keyword2 -excluded",
			wantOptional: []string{"keyword1", "keyword2"},
			wantExcluded: []string{"excluded"},
		},
		{
			name:         "full-width space",
			input:        "hello\u3000world",
			wantOptional: []string{"hello", "world"},
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
			assertStringSlice(t, "OptionalTerms", expr.OptionalTerms, tt.wantOptional)

			if HasComplexExpression(expr) != tt.wantComplex {
				t.Errorf("HasComplexExpression = %v, want %v", HasComplexExpression(expr), tt.wantComplex)
			}
		})
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
}

func TestConvertSearchExpression(t *testing.T) {
	result, err := ConvertSearchExpression("+golang -old")
	if err != nil {
		t.Fatal(err)
	}
	if result != "golang NOT old" {
		t.Errorf("result = %q, want %q", result, "golang NOT old")
	}
}

func TestToQueryString(t *testing.T) {
	expr := &SearchExpression{
		RequiredTerms: []string{"golang"},
		OptionalTerms: []string{"tutorial"},
		ExcludedTerms: []string{"deprecated"},
	}
	result := ToQueryString(expr)
	expected := "golang AND tutorial NOT deprecated"
	if result != expected {
		t.Errorf("result = %q, want %q", result, expected)
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
