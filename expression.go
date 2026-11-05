package mygram

import "strings"

// isFullWidthSpaceAt reports whether input[pos:] begins with the UTF-8
// encoding of U+3000 (0xE3 0x80 0x80).
func isFullWidthSpaceAt(input string, pos int) bool {
	return pos+2 < len(input) && input[pos] == 0xE3 && input[pos+1] == 0x80 && input[pos+2] == 0x80
}

// isASCIISpace mirrors C's isspace for a single byte.
func isASCIISpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	}
	return false
}

func toASCIIUpperByte(b byte) byte {
	if b >= 'a' && b <= 'z' {
		return b - ('a' - 'A')
	}
	return b
}

// exprTokenType is a lexical token kind for the web-syntax search expression
// parser.
type exprTokenType int

const (
	exprTokTerm exprTokenType = iota
	exprTokQuotedTerm
	exprTokPlus
	exprTokMinus
	exprTokOr
	exprTokLParen
	exprTokRParen
	exprTokEnd
)

type exprToken struct {
	typ   exprTokenType
	value string
}

// exprTokenizer is a byte-oriented lexer for web-syntax search expressions,
// mirroring the server client's Tokenizer
// (src/client/search_expression.cpp) byte-for-byte so the two agree on
// where a term starts and ends.
type exprTokenizer struct {
	input string
	pos   int
}

func (t *exprTokenizer) skipWhitespace() {
	for t.pos < len(t.input) {
		if isFullWidthSpaceAt(t.input, t.pos) {
			t.pos += 3
			continue
		}
		if isASCIISpace(t.input[t.pos]) {
			t.pos++
			continue
		}
		break
	}
}

// readTerm reads a bare (unquoted) term: everything up to the next
// whitespace, '(', ')' or '"'. '+' and '-' are unary operators only when
// next() sees them at a token boundary; once a term has started they are
// ordinary text, as in "COVID-19", "e-mail" and "C++". An apostrophe is
// likewise ordinary text -- only '"' opens a quoted phrase.
func (t *exprTokenizer) readTerm() string {
	start := t.pos
	for t.pos < len(t.input) {
		if isFullWidthSpaceAt(t.input, t.pos) {
			break
		}
		ch := t.input[t.pos]
		if isASCIISpace(ch) || ch == '(' || ch == ')' || ch == '"' {
			break
		}
		t.pos++
	}
	return t.input[start:t.pos]
}

func (t *exprTokenizer) readQuotedString() string {
	if t.pos >= len(t.input) || t.input[t.pos] != '"' {
		return ""
	}
	t.pos++
	var sb strings.Builder
	for t.pos < len(t.input) {
		ch := t.input[t.pos]
		if ch == '"' {
			t.pos++
			return sb.String()
		}
		if ch == '\\' && t.pos+1 < len(t.input) {
			t.pos++
			sb.WriteByte(t.input[t.pos])
			t.pos++
		} else {
			sb.WriteByte(ch)
			t.pos++
		}
	}
	// Unclosed quote - return what we have.
	return sb.String()
}

// isOperatorBoundaryAfter reports whether an operator word may end at pos:
// end of input, ASCII or U+3000 whitespace, a parenthesis or a double quote.
// Anything else continues the word.
func (t *exprTokenizer) isOperatorBoundaryAfter(pos int) bool {
	if pos >= len(t.input) {
		return true
	}
	ch := t.input[pos]
	return isASCIISpace(ch) || ch == '(' || ch == ')' || ch == '"' || isFullWidthSpaceAt(t.input, pos)
}

// isOperatorBoundaryBefore reports whether an operator word may start at pos
// (same delimiters as isOperatorBoundaryAfter).
func (t *exprTokenizer) isOperatorBoundaryBefore(pos int) bool {
	if pos == 0 {
		return true
	}
	prev := t.input[pos-1]
	return isASCIISpace(prev) || prev == '(' || prev == ')' || prev == '"' ||
		(pos >= 3 && isFullWidthSpaceAt(t.input, pos-3))
}

// next returns the next token, mirroring the server client's Tokenizer::Next.
func (t *exprTokenizer) next() exprToken {
	t.skipWhitespace()
	if t.pos >= len(t.input) {
		return exprToken{typ: exprTokEnd}
	}

	ch := t.input[t.pos]
	switch ch {
	case '"':
		return exprToken{typ: exprTokQuotedTerm, value: t.readQuotedString()}
	case '+':
		t.pos++
		return exprToken{typ: exprTokPlus}
	case '-':
		t.pos++
		return exprToken{typ: exprTokMinus}
	case '(':
		t.pos++
		return exprToken{typ: exprTokLParen}
	case ')':
		t.pos++
		return exprToken{typ: exprTokRParen}
	}

	// OR operator, recognized case-insensitively as a whole word: the
	// server does the same on both the top-level clause scanner and the
	// boolean AST tokenizer, so "or"/"Or" must be caught here too, or the
	// client sends it as data while the server re-reads it as the operator.
	// A word that merely starts with "or" ("OR-tools", "OR回路") is a term.
	if t.pos+2 <= len(t.input) && equalsASCIIFold(t.input[t.pos:t.pos+2], "OR") &&
		t.isOperatorBoundaryBefore(t.pos) && t.isOperatorBoundaryAfter(t.pos+2) {
		t.pos += 2
		return exprToken{typ: exprTokOr, value: "OR"}
	}

	return exprToken{typ: exprTokTerm, value: t.readTerm()}
}

// exprParser is a recursive-descent parser for web-syntax search
// expressions, mirroring the server client's Parser
// (src/client/search_expression.cpp).
type exprParser struct {
	tok     exprTokenizer
	current exprToken
}

func newExprParser(input string) *exprParser {
	p := &exprParser{tok: exprTokenizer{input: input}}
	p.advance()
	return p
}

func (p *exprParser) advance() {
	p.current = p.tok.next()
}

// parse parses the whole expression, mirroring the server client's
// Parser::Parse. An expression with a top-level OR becomes one group: implicit
// AND binds tighter than OR, so "a OR b c" is "a OR (b c)", exactly what the
// parenthesized form "(a OR b c)" means to the server grammar.
func (p *exprParser) parse() (*SearchExpression, error) {
	expr := &SearchExpression{}

	if p.hasTopLevelOr() {
		group, err := p.captureTopLevelOrGroup()
		if err != nil {
			return nil, err
		}
		expr.RawExpression = group
		return expr, nil
	}

	for p.current.typ != exprTokEnd {
		switch p.current.typ {
		case exprTokPlus:
			p.advance()
			operand, ok := p.parsePrefixedTerm()
			if !ok {
				return nil, &ValidationError{Message: "expected term after '+'"}
			}
			expr.RequiredTerms = append(expr.RequiredTerms, operand.text)
			expr.RequiredIsGroup = append(expr.RequiredIsGroup, operand.isGroup)
		case exprTokMinus:
			p.advance()
			operand, ok := p.parsePrefixedTerm()
			if !ok {
				return nil, &ValidationError{Message: "expected term after '-'"}
			}
			expr.ExcludedTerms = append(expr.ExcludedTerms, operand.text)
			expr.ExcludedIsGroup = append(expr.ExcludedIsGroup, operand.isGroup)
		case exprTokLParen:
			paren := p.captureParenExpression()
			if paren == "" {
				return nil, &ValidationError{Message: "unbalanced parentheses"}
			}
			if expr.RawExpression != "" {
				expr.RawExpression += " "
			}
			expr.RawExpression += paren
		case exprTokTerm, exprTokQuotedTerm:
			// Implicit AND. Stored as plain text regardless of whether it was
			// quoted -- ToQueryString re-applies wire quoting exactly once, at
			// the destination the term is serialized onto.
			expr.RequiredTerms = append(expr.RequiredTerms, p.current.value)
			expr.RequiredIsGroup = append(expr.RequiredIsGroup, false)
			p.advance()
		case exprTokOr:
			return nil, &ValidationError{Message: "unexpected 'OR' operator"}
		case exprTokRParen:
			return nil, &ValidationError{Message: "unexpected ')'"}
		default:
			p.advance()
		}
	}

	return expr, nil
}

// prefixedOperand is the operand of a unary +/-: plain term text, or a
// formatted group.
type prefixedOperand struct {
	text    string
	isGroup bool
}

func (p *exprParser) parsePrefixedTerm() (prefixedOperand, bool) {
	switch p.current.typ {
	case exprTokLParen:
		expr := p.captureParenExpression()
		if expr == "" {
			return prefixedOperand{}, false
		}
		return prefixedOperand{text: expr, isGroup: true}, true
	case exprTokTerm, exprTokQuotedTerm:
		operand := prefixedOperand{text: p.current.value}
		p.advance()
		return operand, true
	}
	return prefixedOperand{}, false
}

// hasTopLevelOr reports whether an OR operator appears outside every
// parenthesized group, then rewinds.
func (p *exprParser) hasTopLevelOr() bool {
	savedPos := p.tok.pos
	savedCurrent := p.current

	found := false
	depth := 0
	for p.current.typ != exprTokEnd && !found {
		switch p.current.typ {
		case exprTokLParen:
			depth++
		case exprTokRParen:
			depth--
		case exprTokOr:
			found = depth <= 0
		}
		p.advance()
	}

	p.tok.pos = savedPos
	p.current = savedCurrent
	return found
}

// isOperandEnd reports whether a token ends a term, quoted term or group, so
// that a following operand needs a separating space.
func isOperandEnd(typ exprTokenType) bool {
	return typ == exprTokTerm || typ == exprTokQuotedTerm || typ == exprTokRParen
}

// captureTopLevelOrGroup captures the whole expression as one group of
// AST-syntax text, applying the shared wire-quoting decision to every term so
// a plain word that collides with a reserved keyword (e.g. "order") is quoted
// instead of reaching the server's AST tokenizer as a bare operator. A '-'
// becomes NOT on the operand it prefixes; a '+' is dropped, since adjacency
// is already AND.
func (p *exprParser) captureTopLevelOrGroup() (string, error) {
	var sb strings.Builder
	previous := exprTokEnd
	pendingNot := false

	for p.current.typ != exprTokEnd {
		switch p.current.typ {
		case exprTokOr:
			if previous == exprTokEnd || previous == exprTokOr {
				return "", &ValidationError{Message: "unexpected 'OR' operator"}
			}
			pendingNot = false
			sb.WriteString(" OR ")
			previous = exprTokOr
			p.advance()
		case exprTokPlus, exprTokMinus:
			if previous == exprTokEnd || previous == exprTokOr {
				return "", &ValidationError{Message: "expected term next to 'OR'"}
			}
			pendingNot = p.current.typ == exprTokMinus
			p.advance()
		case exprTokRParen:
			return "", &ValidationError{Message: "unexpected ')'"}
		default:
			// A term, quoted term or parenthesized group.
			isGroup := p.current.typ == exprTokLParen
			operandType := p.current.typ
			var operand string
			if isGroup {
				operand = p.captureParenExpression()
				if operand == "" {
					message := "unbalanced parentheses"
					if previous == exprTokOr {
						message = "unbalanced parentheses after 'OR'"
					}
					return "", &ValidationError{Message: message}
				}
			} else {
				operand = quoteWireToken(p.current.value)
				p.advance()
			}
			if isOperandEnd(previous) {
				sb.WriteByte(' ')
			}
			if pendingNot {
				sb.WriteString("NOT ")
				pendingNot = false
			}
			sb.WriteString(operand)
			if isGroup {
				previous = exprTokRParen
			} else {
				previous = operandType
			}
		}
	}

	if previous == exprTokOr {
		return "", &ValidationError{Message: "expected term after 'OR'"}
	}
	return sb.String(), nil
}

// captureParenExpression reconstructs a parenthesized sub-expression as
// AST-syntax text. Returns "" if the current token is not '(' or the group
// is unbalanced (ends at exprTokEnd before its matching ')').
func (p *exprParser) captureParenExpression() string {
	if p.current.typ != exprTokLParen {
		return ""
	}

	var sb strings.Builder
	depth := 0
	// Tracks the last *content* token (term/paren/OR) so the spacing
	// decision below is unaffected by a +/- prefix in between -- otherwise
	// "tutorial -video" would see previousType==exprTokMinus and skip the
	// space it needs before the "NOT" it is about to emit.
	previousType := exprTokEnd
	// A bare '-' has no keyword the AST parser recognizes; the server only
	// understands the literal word NOT. Defer emission to the very next
	// token: a term or a nested group takes the NOT, anything else drops it,
	// so it never leaks onto a later term.
	pendingNot := false

	for {
		switch p.current.typ {
		case exprTokLParen:
			if isOperandEnd(previousType) {
				sb.WriteByte(' ')
			}
			if pendingNot {
				sb.WriteString("NOT ")
				pendingNot = false
			}
			depth++
			sb.WriteByte('(')
			previousType = exprTokLParen
		case exprTokRParen:
			pendingNot = false
			depth--
			sb.WriteByte(')')
			previousType = exprTokRParen
		case exprTokTerm, exprTokQuotedTerm:
			if isOperandEnd(previousType) {
				sb.WriteByte(' ')
			}
			if pendingNot {
				sb.WriteString("NOT ")
				pendingNot = false
			}
			sb.WriteString(quoteWireToken(p.current.value))
			previousType = p.current.typ
		case exprTokOr:
			pendingNot = false
			sb.WriteString(" OR ")
			previousType = exprTokOr
		case exprTokPlus:
			// No operator meaning inside a group -- adjacent terms are
			// AND'ed by adjacency already, so '+' is dropped rather than
			// glued onto the next term as a raw character the AST
			// tokenizer rejects.
			pendingNot = false
		case exprTokMinus:
			pendingNot = true
		case exprTokEnd:
			return "" // unbalanced
		}

		if depth <= 0 {
			break
		}
		p.advance()
	}

	p.advance() // skip closing paren
	return sb.String()
}

// SearchExpression represents a parsed search expression.
type SearchExpression struct {
	// RequiredTerms contains every term that must match: those with a '+'
	// prefix and every unprefixed term (implicit AND), in the order they
	// appeared. An entry flagged in RequiredIsGroup is a formatted
	// parenthesized sub-expression captured from "+(...)", already quoted.
	RequiredTerms []string

	// ExcludedTerms contains keywords marked for exclusion with '-'. An entry
	// flagged in ExcludedIsGroup is a formatted "-(...)" group.
	ExcludedTerms []string

	// RawExpression holds AST-syntax text reconstructed from any expression
	// with a top-level OR, or any parenthesized group not immediately
	// preceded by '+'/'-', with the shared wire-quoting decision already
	// applied to each of its terms.
	RawExpression string

	// RequiredIsGroup runs parallel to RequiredTerms: true where the entry is
	// a formatted (...) group rather than plain term text. A quoted phrase
	// shaped like a group stays plain text.
	RequiredIsGroup []bool

	// ExcludedIsGroup runs parallel to ExcludedTerms with the same meaning.
	ExcludedIsGroup []bool
}

// isGroupEntry reports whether entry index of a term list is a formatted
// group; a missing flag means plain text.
func isGroupEntry(isGroup []bool, index int) bool {
	return index < len(isGroup) && isGroup[index]
}

// ParseSearchExpression parses a web-search-style search expression.
//
// Supported syntax:
//   - +term: required keyword
//   - -term: excluded keyword
//   - "phrase": phrase search
//   - OR (case-insensitive, as a whole word): logical OR
//   - (expr): grouping
//
// Implicit AND binds tighter than OR, so an expression with a top-level OR is
// one group: "a OR b c" is "(a OR b c)". A '-' before a group excludes the
// whole group, and a quoted phrase is always a phrase, never a group.
func ParseSearchExpression(expression string) (*SearchExpression, error) {
	if expression == "" {
		return nil, &ValidationError{Message: "empty search expression"}
	}
	return newExprParser(expression).parse()
}

// HasComplexExpression returns whether the expression contains OR or
// grouping: a non-empty RawExpression, or a RequiredTerms/ExcludedTerms
// entry flagged as a group (from "+(...)" or "-(...)").
func HasComplexExpression(expr *SearchExpression) bool {
	if expr.RawExpression != "" {
		return true
	}
	for _, isGroup := range expr.RequiredIsGroup {
		if isGroup {
			return true
		}
	}
	for _, isGroup := range expr.ExcludedIsGroup {
		if isGroup {
			return true
		}
	}
	return false
}

// ToQueryString converts a SearchExpression to a MygramDB query string,
// applying the shared wire-quoting decision (wire_quoting.go) to every plain
// term so a literal term colliding with a reserved keyword, or containing
// whitespace, reaches the server as the search text it was written as.
func ToQueryString(expr *SearchExpression) string {
	emit := func(term string, isGroup bool) string {
		if isGroup {
			return term
		}
		return quoteWireToken(term)
	}

	var sb strings.Builder
	for i, term := range expr.RequiredTerms {
		if i > 0 {
			sb.WriteString(" AND ")
		}
		sb.WriteString(emit(term, isGroupEntry(expr.RequiredIsGroup, i)))
	}
	for i, term := range expr.ExcludedTerms {
		if sb.Len() > 0 {
			sb.WriteString(" AND ")
		}
		sb.WriteString("NOT ")
		sb.WriteString(emit(term, isGroupEntry(expr.ExcludedIsGroup, i)))
	}
	if expr.RawExpression != "" {
		if sb.Len() > 0 {
			sb.WriteString(" AND ")
		}
		sb.WriteString("(")
		sb.WriteString(expr.RawExpression)
		sb.WriteString(")")
	}

	return sb.String()
}

// ConvertSearchExpression converts a search expression to a MygramDB query string.
func ConvertSearchExpression(expression string) (string, error) {
	expr, err := ParseSearchExpression(expression)
	if err != nil {
		return "", err
	}
	return ToQueryString(expr), nil
}

// SimplifySearchExpression decomposes a search expression into
// MainTerm/AndTerms/NotTerms. The result maps directly to the MygramDB
// SEARCH command's AND/NOT term list.
//
// An expression containing OR or grouping is refused: the legacy
// MainTerm/AndTerms/NotTerms shape has no way to represent it, and every
// consumer of those fields re-applies wire quoting to each one, which would
// wrap an already-formatted "(a OR b)" as one opaque literal-phrase term
// instead of parsing it as boolean OR. Use ConvertSearchExpression with
// Client.SearchRaw for an expression containing OR or grouping.
func SimplifySearchExpression(expression string) (*SimplifiedExpression, error) {
	expr, err := ParseSearchExpression(expression)
	if err != nil {
		return nil, err
	}
	if HasComplexExpression(expr) {
		return nil, &ValidationError{Message: "expression cannot be represented by the simplified client API"}
	}
	if len(expr.RequiredTerms) == 0 {
		return nil, &ValidationError{Message: "no search terms found"}
	}

	result := &SimplifiedExpression{MainTerm: expr.RequiredTerms[0]}
	if len(expr.RequiredTerms) > 1 {
		result.AndTerms = append([]string(nil), expr.RequiredTerms[1:]...)
	}
	if len(expr.ExcludedTerms) > 0 {
		result.NotTerms = append([]string(nil), expr.ExcludedTerms...)
	}
	return result, nil
}
