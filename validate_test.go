package mygram

import (
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
}
