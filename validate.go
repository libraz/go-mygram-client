package mygram

import (
	"fmt"
	"unicode"
)

// controlCharNames maps control characters to their display names.
var controlCharNames = map[rune]string{
	0x00: "NULL (0x00)",
	0x01: "SOH (0x01)",
	0x02: "STX (0x02)",
	0x03: "ETX (0x03)",
	0x04: "EOT (0x04)",
	0x05: "ENQ (0x05)",
	0x06: "ACK (0x06)",
	0x07: "BEL (0x07)",
	0x08: "BS (0x08)",
	0x09: "TAB (0x09)",
	0x0A: "LF (0x0a)",
	0x0B: "VT (0x0b)",
	0x0C: "FF (0x0c)",
	0x0D: "CR (0x0d)",
	0x0E: "SO (0x0e)",
	0x0F: "SI (0x0f)",
	0x10: "DLE (0x10)",
	0x11: "DC1 (0x11)",
	0x12: "DC2 (0x12)",
	0x13: "DC3 (0x13)",
	0x14: "DC4 (0x14)",
	0x15: "NAK (0x15)",
	0x16: "SYN (0x16)",
	0x17: "ETB (0x17)",
	0x18: "CAN (0x18)",
	0x19: "EM (0x19)",
	0x1A: "SUB (0x1a)",
	0x1B: "ESC (0x1b)",
	0x1C: "FS (0x1c)",
	0x1D: "GS (0x1d)",
	0x1E: "RS (0x1e)",
	0x1F: "US (0x1f)",
	0x7F: "DEL (0x7f)",
}

// isControlChar checks whether a rune is a control character, compatible with C++ std::iscntrl.
func isControlChar(r rune) bool {
	return (r >= 0x00 && r <= 0x1f) || r == 0x7f
}

// ensureSafeValue verifies that a value does not contain control characters.
func ensureSafeValue(value, fieldName string) error {
	for _, r := range value {
		if isControlChar(r) {
			name, ok := controlCharNames[r]
			if !ok {
				name = fmt.Sprintf("U+%04X", r)
			}
			return &ValidationError{
				Message: fmt.Sprintf("input for %s contains %s, which is not allowed", fieldName, name),
			}
		}
	}
	return nil
}

// ensureSafeValues validates each value in the slice.
func ensureSafeValues(values []string, fieldName string) error {
	for i, v := range values {
		if err := ensureSafeValue(v, fmt.Sprintf("%s[%d]", fieldName, i)); err != nil {
			return err
		}
	}
	return nil
}

// ensureSafeFilters validates the keys and values of filters.
func ensureSafeFilters(filters map[string]string) error {
	for k, v := range filters {
		if err := ensureSafeValue(k, "filter key"); err != nil {
			return err
		}
		if err := ensureSafeValue(v, fmt.Sprintf("filter[%s]", k)); err != nil {
			return err
		}
	}
	return nil
}

// calculateQueryExpressionLength calculates the total character count of a query expression.
func calculateQueryExpressionLength(query string, andTerms, notTerms []string, filters map[string]string, sortColumn string) int {
	length := len([]rune(query))
	for _, t := range andTerms {
		length += len([]rune(t))
	}
	for _, t := range notTerms {
		length += len([]rune(t))
	}
	for k, v := range filters {
		length += len([]rune(k)) + len([]rune(v))
	}
	if sortColumn != "" {
		length += len([]rune(sortColumn))
	}
	return length
}

// ensureQueryLengthWithinLimit verifies that the query expression length does not exceed the limit.
func ensureQueryLengthWithinLimit(query string, andTerms, notTerms []string, filters map[string]string, sortColumn string, maxLength int) error {
	if maxLength <= 0 {
		return nil
	}
	length := calculateQueryExpressionLength(query, andTerms, notTerms, filters, sortColumn)
	if length > maxLength {
		return &ValidationError{
			Message: fmt.Sprintf("query expression length (%d) exceeds maximum allowed length of %d characters", length, maxLength),
		}
	}
	return nil
}

// validateTableName validates a table name.
func validateTableName(table string) error {
	if table == "" {
		return &ValidationError{Message: "table name must not be empty"}
	}
	for _, r := range table {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return &ValidationError{Message: fmt.Sprintf("table name contains invalid character: %q", r)}
		}
	}
	return nil
}

// validatePrimaryKey validates a primary key.
func validatePrimaryKey(pk string) error {
	if pk == "" {
		return &ValidationError{Message: "primary key must not be empty"}
	}
	return ensureSafeValue(pk, "primary_key")
}

// validateFuzzy validates a fuzzy edit distance. The server accepts 1 or 2;
// 0 means the clause is disabled.
func validateFuzzy(distance int) error {
	if distance == 0 || distance == 1 || distance == 2 {
		return nil
	}
	return &ValidationError{
		Message: fmt.Sprintf("invalid fuzzy distance %d: must be 0, 1, or 2", distance),
	}
}

// validateHighlight validates HIGHLIGHT clause options. OpenTag and CloseTag
// must both be empty or both set; SnippetLen must be within 1..10000 when set;
// MaxFragments must be within 1..100 when set. Tags must not contain control
// characters or spaces (the protocol is whitespace-delimited).
func validateHighlight(h *HighlightOptions) error {
	if h == nil {
		return nil
	}
	if (h.OpenTag == "") != (h.CloseTag == "") {
		return &ValidationError{Message: "highlight open_tag and close_tag must be set together"}
	}
	for _, tag := range []struct {
		name, value string
	}{
		{"highlight.open_tag", h.OpenTag},
		{"highlight.close_tag", h.CloseTag},
	} {
		if tag.value == "" {
			continue
		}
		if err := ensureSafeValue(tag.value, tag.name); err != nil {
			return err
		}
		for _, r := range tag.value {
			if unicode.IsSpace(r) {
				return &ValidationError{
					Message: fmt.Sprintf("%s must not contain whitespace: %q", tag.name, tag.value),
				}
			}
		}
	}
	if h.SnippetLen < 0 || h.SnippetLen > 10000 {
		return &ValidationError{
			Message: fmt.Sprintf("highlight.snippet_len out of range (0..10000): %d", h.SnippetLen),
		}
	}
	if h.MaxFragments < 0 || h.MaxFragments > 100 {
		return &ValidationError{
			Message: fmt.Sprintf("highlight.max_fragments out of range (0..100): %d", h.MaxFragments),
		}
	}
	return nil
}

// validateFacetColumn validates a FACET column name. Same rules as table names:
// must be non-empty and contain no control or whitespace characters.
func validateFacetColumn(column string) error {
	if column == "" {
		return &ValidationError{Message: "facet column must not be empty"}
	}
	for _, r := range column {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return &ValidationError{Message: fmt.Sprintf("facet column contains invalid character: %q", r)}
		}
	}
	return nil
}
