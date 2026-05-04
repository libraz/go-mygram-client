package mygram

import (
	"strings"
	"testing"
)

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
		"created_at",
	)
	// hello=5 + world=5 + test=4 + spam=4 + status=6 + 1=1 + created_at=10 → 35
	if length != 35 {
		t.Errorf("length = %d, want 35", length)
	}
}

func TestEnsureQueryLengthWithinLimit(t *testing.T) {
	// Unlimited
	err := ensureQueryLengthWithinLimit("test", nil, nil, nil, "", 0)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Within limit
	err = ensureQueryLengthWithinLimit("test", nil, nil, nil, "", 128)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Exceeds limit
	err = ensureQueryLengthWithinLimit("test", nil, nil, nil, "", 3)
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

func TestValidatePrimaryKey(t *testing.T) {
	if err := validatePrimaryKey("12345"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := validatePrimaryKey(""); err == nil {
		t.Error("expected error for empty key")
	}
	if err := validatePrimaryKey("bad\x00key"); err == nil {
		t.Error("expected error for key with null")
	}
	if err := validatePrimaryKey("bad key"); err == nil {
		t.Error("expected error for primary key with whitespace")
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
		{"control chars dropped when quoted", "a\x00b c", `"ab c"`},
		{"tab triggers quoting", "a\tb", `"ab"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := escapeQueryString(tt.in); got != tt.want {
				t.Errorf("escapeQueryString(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
