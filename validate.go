package mygram

import (
	"fmt"
	"strings"
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
func calculateQueryExpressionLength(query string, andTerms, notTerms []string, filters map[string]string, conditions []FilterCondition, sortColumn string) int {
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
	// Typed conditions occupy the same expression budget as the equality map;
	// leaving them out would let a request built entirely from them bypass the
	// limit the server enforces on the assembled command.
	for _, c := range conditions {
		length += len([]rune(c.Key)) + len([]rune(c.Value))
	}
	if sortColumn != "" {
		length += len([]rune(sortColumn))
	}
	return length
}

// ensureQueryLengthWithinLimit verifies that the query expression length does not exceed the limit.
func ensureQueryLengthWithinLimit(query string, andTerms, notTerms []string, filters map[string]string, conditions []FilterCondition, sortColumn string, maxLength int) error {
	if maxLength <= 0 {
		return nil
	}
	length := calculateQueryExpressionLength(query, andTerms, notTerms, filters, conditions, sortColumn)
	if length > maxLength {
		return &ValidationError{
			Message: fmt.Sprintf("query expression length (%d) exceeds maximum allowed length of %d characters", length, maxLength),
		}
	}
	return nil
}

// validateIdentifier validates an unquoted identifier such as a table name,
// sort column, facet column or filter key. Identifiers are sent on the wire
// without quoting, so embedded whitespace would split the value into multiple
// tokens and a quote or backslash would open a quoted literal — either way the
// rest of the command would be reinterpreted. Mirrors the C++ client's
// ValidateIdentifier.
func validateIdentifier(value, fieldName string) error {
	if value == "" {
		return &ValidationError{Message: fmt.Sprintf("input for %s is empty", fieldName)}
	}
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
		if isWireWhitespace(r) {
			return &ValidationError{
				Message: fmt.Sprintf("input for %s contains whitespace, which is not allowed in identifiers", fieldName),
			}
		}
		if r == '"' || r == '\'' || r == '\\' {
			return &ValidationError{
				Message: fmt.Sprintf("input for %s contains protocol delimiter %q, which is not allowed in identifiers", fieldName, r),
			}
		}
	}
	return nil
}

// validateTableName validates a table name, which is an identifier.
func validateTableName(table string) error {
	if table == "" {
		return &ValidationError{Message: "table name must not be empty"}
	}
	return validateIdentifier(table, "table name")
}

// validateFilters validates both filter forms of a request: the equality map
// and the typed comparison conditions.
func validateFilters(filters map[string]string, conditions []FilterCondition) error {
	if err := ensureSafeFilters(filters); err != nil {
		return err
	}
	for k := range filters {
		if err := validateIdentifier(k, "filter key"); err != nil {
			return err
		}
	}
	for i, cond := range conditions {
		if err := validateIdentifier(cond.Key, fmt.Sprintf("filterConditions[%d].key", i)); err != nil {
			return err
		}
		if !cond.Op.valid() {
			return &ValidationError{
				Message: fmt.Sprintf("invalid filter operator %d in filterConditions[%d]", cond.Op, i),
			}
		}
		if err := ensureSafeValue(cond.Value, fmt.Sprintf("filterConditions[%d].value", i)); err != nil {
			return err
		}
	}
	return nil
}

// validateSort validates the two ways a search can request an ordering. A
// command carries exactly one SORT clause, so naming a column and asking for a
// primary-key ordering at the same time is a caller error rather than a
// silently discarded option.
func validateSort(sortColumn string, primaryKey PrimaryKeySort) error {
	if !primaryKey.valid() {
		return &ValidationError{Message: fmt.Sprintf("invalid primary key sort %d", primaryKey)}
	}
	if sortColumn == "" {
		return nil
	}
	if primaryKey != PrimaryKeySortDefault {
		return &ValidationError{
			Message: "sortColumn and sortPrimaryKey are mutually exclusive: a request carries a single SORT clause",
		}
	}
	return validateIdentifier(sortColumn, "sort column")
}

// validateQueryMode rejects a mode outside the defined constants, so an
// uninitialized or miscast value cannot silently fall back to literal search.
func validateQueryMode(mode QueryMode) error {
	if mode != QueryLiteral && mode != QueryBoolean {
		return &ValidationError{Message: fmt.Sprintf("invalid query mode %d", mode)}
	}
	return nil
}

// escapeQueryString quotes and escapes a free-form query value for use as a
// wire token: a search term, filter value, or AND/NOT term. Delegates to
// quoteWireToken, the single quote/escape decision shared by every string
// that reaches the wire (wire_quoting.go), which also covers the empty-string
// case — emitted as the explicit "" token so an unquoted empty arg cannot
// collapse into surrounding whitespace and produce a malformed command (e.g.
// "SEARCH table  AND foo"). Mirrors the C++ client's EscapeQueryString.
func escapeQueryString(value string) string {
	return quoteWireToken(value)
}

// escapeProtocolToken quotes a data token — a primary key, say — for the
// wire. Delegates to quoteWireToken, whose encoding is already reversible
// (control characters are escaped, not dropped), so any value the server can
// return can also be sent back to it. Mirrors the C++ client's
// EscapeProtocolToken.
func escapeProtocolToken(value string) string {
	return quoteWireToken(value)
}

// quoteCommandArgument quotes a free-form command argument (e.g. a SET value,
// a SHOW VARIABLES LIKE pattern, or a DUMP filepath). Mirrors the C++
// client's QuoteCommandArgumentIfNeeded, which delegates to the same shared
// QuoteWireToken.
//
// Unlike escapeQueryString, the value is validated for control characters
// first, returning an error rather than sending one reversibly encoded; an
// empty value is allowed and surfaced as the explicit empty token "".
func quoteCommandArgument(value, fieldName string) (string, error) {
	if value == "" {
		return `""`, nil
	}
	if err := ensureSafeValue(value, fieldName); err != nil {
		return "", err
	}
	return quoteWireToken(value), nil
}

// QualifyTableIdentity builds a database-qualified table identity
// ("database.table") for MygramDB v1.7+ multi-database deployments.
//
// A single-database deployment continues to accept a bare table name, so an
// empty database returns just the validated table name. When a database is
// supplied, both parts are validated as identifiers and must not themselves
// contain a '.' separator; they are then joined as "database.table".
func QualifyTableIdentity(table, database string) (string, error) {
	if err := validateIdentifier(table, "table"); err != nil {
		return "", err
	}
	if database == "" {
		return table, nil
	}
	if err := validateIdentifier(database, "database"); err != nil {
		return "", err
	}
	if strings.Contains(database, ".") {
		return "", &ValidationError{Message: "input for database must not contain a '.' separator"}
	}
	if strings.Contains(table, ".") {
		return "", &ValidationError{Message: "input for table must not contain a '.' when a database is supplied separately"}
	}
	return database + "." + table, nil
}

// ParseTableIdentity splits a (possibly database-qualified) table identity
// into its parts.
//
// Bare names return an empty database and the table name; qualified names are
// split on the first '.' so "app_db.articles" yields ("app_db", "articles").
// The identity is validated as a protocol identifier first.
func ParseTableIdentity(identity string) (database, table string, err error) {
	if err := validateIdentifier(identity, "table"); err != nil {
		return "", "", err
	}
	dot := strings.IndexByte(identity, '.')
	if dot == -1 {
		return "", identity, nil
	}
	database = identity[:dot]
	table = identity[dot+1:]
	if database == "" || table == "" {
		return "", "", &ValidationError{
			Message: fmt.Sprintf("invalid table identity %q: expected <database>.<table>", identity),
		}
	}
	return database, table, nil
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
// characters; whitespace is allowed since appendHighlightClause sends each tag
// through the shared wire-quoting encoder (wire_quoting.go), matching the C++
// client's validation (ValidateNoControlCharacters only).
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

// validateFacetColumn validates a FACET column name, which is an identifier.
func validateFacetColumn(column string) error {
	if column == "" {
		return &ValidationError{Message: "facet column must not be empty"}
	}
	return validateIdentifier(column, "facet column")
}
