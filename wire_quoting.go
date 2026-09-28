package mygram

import "strings"

// wireReservedKeywords lists the tokens the server's query grammar consumes
// as clause keywords, case-insensitively, on both the top-level command
// scanner and the boolean sub-expression tokenizer. A literal term equal to
// one of them must be quoted, or the server reads it as the start of a
// clause instead of as data. Mirrors the C++ client's IsReservedKeyword
// (src/client/wire_quoting.h).
var wireReservedKeywords = map[string]bool{
	"AND":       true,
	"OR":        true,
	"NOT":       true,
	"FILTER":    true,
	"SORT":      true,
	"LIMIT":     true,
	"OFFSET":    true,
	"HIGHLIGHT": true,
	"FUZZY":     true,
	"FACET":     true,
	"ORDER":     true,
}

// isUnicodeWhitespace reports whether r is one of the non-ASCII code points
// the server's tokenizer splits on outside quotes: U+00A0, U+1680,
// U+2000-U+200B, U+2028, U+2029, U+202F, U+205F, U+3000
// (spec/tcp-commands.md §2). Deliberately not unicode.IsSpace, which
// disagrees with this set — notably it does not treat U+200B (zero width
// space) as whitespace, while the server's tokenizer does.
func isUnicodeWhitespace(r rune) bool {
	switch r {
	case 0x00A0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000:
		return true
	}
	return r >= 0x2000 && r <= 0x200B
}

// isWireWhitespace reports whether r is a token separator by the server's
// own rule: C isspace for ASCII (space, \t, \n, \v, \f, \r) plus
// isUnicodeWhitespace.
func isWireWhitespace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	}
	return isUnicodeWhitespace(r)
}

// isASCIISpaceByte mirrors C's isspace for a single byte, used to find token
// boundaries while decoding a response the server already framed on the
// wire (parseWireToken). Unlike isWireWhitespace it is byte-, not
// rune-, oriented: decoding only needs to know where an unquoted token
// ends, and every byte of a multi-byte UTF-8 sequence is >= 0x80, so this
// never splits inside one.
func isASCIISpaceByte(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	}
	return false
}

// needsWireQuoting reports whether value must be quoted to survive the wire
// tokenizer intact: it is empty, a reserved keyword (case-insensitive), or
// contains ASCII/Unicode whitespace, a control character, a quote,
// a backslash, or a parenthesis. Mirrors the C++ client's NeedsWireQuoting
// (src/client/wire_quoting.h).
func needsWireQuoting(value string) bool {
	if value == "" {
		return true
	}
	if wireReservedKeywords[strings.ToUpper(value)] {
		return true
	}
	for _, r := range value {
		if isWireWhitespace(r) || isControlChar(r) || r == '"' || r == '\'' || r == '\\' || r == '(' || r == ')' {
			return true
		}
	}
	return false
}

// quoteWireToken quotes and escapes value for the wire protocol, only if
// needsWireQuoting requires it. The encoding is reversible -- '\\' and '"'
// are backslash-escaped, CR/LF/TAB as \r/\n/\t, and any other control byte
// as \xHH (uppercase hex) -- the inverse of parseWireToken.
//
// This is the single encoder used by every string that reaches the wire:
// search terms, filter values, AND/NOT terms, highlight tags, primary keys,
// and command arguments. Mirrors the C++ client's QuoteWireToken
// (src/client/wire_quoting.h).
func quoteWireToken(value string) string {
	if !needsWireQuoting(value) {
		return value
	}

	const hexDigits = "0123456789ABCDEF"
	var sb strings.Builder
	sb.Grow(len(value) + 2)
	sb.WriteByte('"')
	for i := 0; i < len(value); i++ {
		ch := value[i]
		switch {
		case ch == '\\':
			sb.WriteString(`\\`)
		case ch == '"':
			sb.WriteString(`\"`)
		case ch == '\r':
			sb.WriteString(`\r`)
		case ch == '\n':
			sb.WriteString(`\n`)
		case ch == '\t':
			sb.WriteString(`\t`)
		case ch < 0x20 || ch == 0x7f:
			sb.WriteByte('\\')
			sb.WriteByte('x')
			sb.WriteByte(hexDigits[ch>>4])
			sb.WriteByte(hexDigits[ch&0x0F])
		default:
			sb.WriteByte(ch)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// hexDigitValue decodes one hex digit; -1 if ch is not [0-9a-fA-F].
func hexDigitValue(ch byte) int {
	switch {
	case ch >= '0' && ch <= '9':
		return int(ch - '0')
	case ch >= 'a' && ch <= 'f':
		return int(ch-'a') + 10
	case ch >= 'A' && ch <= 'F':
		return int(ch-'A') + 10
	}
	return -1
}

// parseWireToken reads one whitespace-delimited or quoted token starting at
// pos, the inverse of quoteWireToken: it skips leading whitespace, then
// reads either a bare token (up to the next whitespace) or, if it opens
// with '"', a quoted token, unescaping \\, \", \r, \n, \t and \xHH back to
// their literal bytes.
//
// Used to decode a value the server quoted per spec/tcp-commands.md §11.1
// (primary keys, header fields). Returns the decoded value and the position
// just past what was consumed; ok is false on an unterminated quote, a
// truncated \x escape, or when nothing is left to read.
func parseWireToken(input string, pos int) (value string, newPos int, ok bool) {
	for pos < len(input) && isASCIISpaceByte(input[pos]) {
		pos++
	}
	if pos >= len(input) {
		return "", pos, false
	}

	if input[pos] != '"' {
		start := pos
		for pos < len(input) && !isASCIISpaceByte(input[pos]) {
			pos++
		}
		return input[start:pos], pos, true
	}

	pos++
	var sb strings.Builder
	for pos < len(input) {
		ch := input[pos]
		pos++
		if ch == '"' {
			return sb.String(), pos, true
		}
		if ch != '\\' {
			sb.WriteByte(ch)
			continue
		}
		if pos >= len(input) {
			return "", pos, false
		}

		escaped := input[pos]
		pos++
		switch escaped {
		case 'n':
			sb.WriteByte('\n')
		case 'r':
			sb.WriteByte('\r')
		case 't':
			sb.WriteByte('\t')
		case '\\', '"':
			sb.WriteByte(escaped)
		case 'x':
			if pos+1 >= len(input) {
				return "", pos, false
			}
			high := hexDigitValue(input[pos])
			low := hexDigitValue(input[pos+1])
			if high < 0 || low < 0 {
				return "", pos, false
			}
			sb.WriteByte(byte(high<<4 | low))
			pos += 2
		default:
			sb.WriteByte(escaped)
		}
	}

	return "", pos, false
}
