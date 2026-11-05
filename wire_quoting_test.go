package mygram

import "testing"

func TestIsUnicodeWhitespaceFollowsWhiteSpaceProperty(t *testing.T) {
	for _, r := range []rune{0x0085, 0x00A0, 0x1680, 0x2000, 0x2005, 0x200A, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000} {
		if !isUnicodeWhitespace(r) {
			t.Errorf("isUnicodeWhitespace(U+%04X) = false, want true", r)
		}
	}
	for _, r := range []rune{0x200B, 0x200C, 0xFEFF, 0x180E, 'a', 0x00E0, 0x3001} {
		if isUnicodeWhitespace(r) {
			t.Errorf("isUnicodeWhitespace(U+%04X) = true, want false", r)
		}
	}
}

func TestNeedsWireQuoting(t *testing.T) {
	quoted := []string{"", "and", "Or", "NOT", "order", "Highlight", "a b", "a\u0085b", "a\u3000b", `a"b`, `it's`, `a\b`, "(x", "x)", "a\tb", "a\x7fb"}
	for _, v := range quoted {
		if !needsWireQuoting(v) {
			t.Errorf("needsWireQuoting(%q) = false, want true", v)
		}
	}
	bare := []string{"plain", "café", "Ångström", "a\u200bb", "ſort", "ORDERS", "日本語", "OR-tools"}
	for _, v := range bare {
		if needsWireQuoting(v) {
			t.Errorf("needsWireQuoting(%q) = true, want false", v)
		}
	}
}

func TestQuoteWireTokenRoundTrip(t *testing.T) {
	for _, v := range []string{"", "and", "a b", "a\u0085b", `q"uote`, `back\slash`, "tab\there", "ctl\x01x", "日本 語"} {
		quoted := quoteWireToken(v)
		got, pos, ok := parseWireToken(quoted, 0)
		if !ok || pos != len(quoted) || got != v {
			t.Errorf("round trip of %q via %q = %q (ok=%v)", v, quoted, got, ok)
		}
	}
}

func TestParseWireTokenDoesNotSplitInsideUTF8(t *testing.T) {
	// U+00E0 ends in byte 0xA0 and U+00C5 in 0x85; neither is whitespace on its own.
	input := "café next"
	got, pos, ok := parseWireToken(input, 0)
	if !ok || got != "café" {
		t.Fatalf("first token = %q (ok=%v)", got, ok)
	}
	got, _, ok = parseWireToken(input, pos)
	if !ok || got != "next" {
		t.Fatalf("second token = %q (ok=%v)", got, ok)
	}
}

func TestValidateIdentifierRejectsUnicodeWhitespace(t *testing.T) {
	for _, name := range []string{"a\u0085b", "a\u00a0b", "a\u3000b", "a b"} {
		if err := validateIdentifier(name, "table name"); err == nil {
			t.Errorf("validateIdentifier(%q): expected error", name)
		}
		if err := validateTableName(name); err == nil {
			t.Errorf("validateTableName(%q): expected error", name)
		}
	}
	if err := validateIdentifier("a\u200bb", "table name"); err != nil {
		t.Errorf("zero width space is not whitespace to the server: %v", err)
	}
}
