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

func isASCIIAlnum(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
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

// next returns the next token, mirroring the server client's Tokenizer::Next.
func (t *exprTokenizer) next() exprToken {
	t.skipWhitespace()
	if t.pos >= len(t.input) {
		return exprToken{typ: exprTokEnd}
	}

	ch := t.input[t.pos]
	switch ch {
	case '"':
		return exprToken{typ: exprTokTerm, value: t.readQuotedString()}
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
	if t.pos+2 <= len(t.input) && toASCIIUpperByte(t.input[t.pos]) == 'O' && toASCIIUpperByte(t.input[t.pos+1]) == 'R' {
		isWholeWord := true
		if t.pos > 0 && isASCIIAlnum(t.input[t.pos-1]) {
			isWholeWord = false
		}
		if t.pos+2 < len(t.input) && isASCIIAlnum(t.input[t.pos+2]) {
			isWholeWord = false
		}
		if isWholeWord {
			t.pos += 2
			return exprToken{typ: exprTokOr, value: "OR"}
		}
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
// Parser::Parse.
func (p *exprParser) parse() (*SearchExpression, error) {
	expr := &SearchExpression{}

	for p.current.typ != exprTokEnd {
		switch p.current.typ {
		case exprTokPlus:
			p.advance()
			term, ok := p.parsePrefixedTerm()
			if !ok {
				return nil, &ValidationError{Message: "expected term after '+'"}
			}
			expr.RequiredTerms = append(expr.RequiredTerms, term)
		case exprTokMinus:
			p.advance()
			term, ok := p.parsePrefixedTerm()
			if !ok {
				return nil, &ValidationError{Message: "expected term after '-'"}
			}
			expr.ExcludedTerms = append(expr.ExcludedTerms, term)
		case exprTokLParen:
			paren := p.captureParenExpression()
			if paren == "" {
				return nil, &ValidationError{Message: "unbalanced parentheses"}
			}
			if expr.RawExpression != "" {
				expr.RawExpression += " "
			}
			expr.RawExpression += paren
		case exprTokTerm:
			if p.looksLikeOrExpression() {
				orExpr, err := p.captureOrExpression()
				if err != nil {
					return nil, err
				}
				if expr.RawExpression != "" {
					expr.RawExpression += " "
				}
				expr.RawExpression += orExpr
			} else {
				// Regular term (implicit AND). Stored as plain text
				// regardless of whether it was quoted -- ToQueryString
				// re-applies wire quoting exactly once, at the destination
				// the term is serialized onto.
				expr.RequiredTerms = append(expr.RequiredTerms, p.current.value)
				p.advance()
			}
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

func (p *exprParser) parsePrefixedTerm() (string, bool) {
	if p.current.typ == exprTokLParen {
		expr := p.captureParenExpression()
		if expr == "" {
			return "", false
		}
		return expr, true
	}
	if p.current.typ == exprTokTerm {
		term := p.current.value
		p.advance()
		return term, true
	}
	return "", false
}

// looksLikeOrExpression looks ahead one token to decide whether the current
// term begins an OR chain, then rewinds.
func (p *exprParser) looksLikeOrExpression() bool {
	savedPos := p.tok.pos
	savedCurrent := p.current

	p.advance()
	hasOr := p.current.typ == exprTokOr

	p.tok.pos = savedPos
	p.current = savedCurrent
	return hasOr
}

// captureOrExpression reconstructs an OR chain as AST-syntax text, applying
// the shared wire-quoting decision to every term so a plain word that
// collides with a reserved keyword (e.g. "order") is quoted here too,
// instead of reaching the server's AST tokenizer as a bare operator.
func (p *exprParser) captureOrExpression() (string, error) {
	var sb strings.Builder
	sb.WriteString(quoteWireToken(p.current.value))
	p.advance()

	for p.current.typ == exprTokOr {
		sb.WriteString(" OR ")
		p.advance()
		switch p.current.typ {
		case exprTokTerm:
			sb.WriteString(quoteWireToken(p.current.value))
			p.advance()
		case exprTokLParen:
			paren := p.captureParenExpression()
			if paren == "" {
				return "", &ValidationError{Message: "unbalanced parentheses after 'OR'"}
			}
			sb.WriteString(paren)
		default:
			return "", &ValidationError{Message: "expected term after 'OR'"}
		}
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
	// understands the literal word NOT. Defer emission until the term it
	// prefixes arrives, exactly as ExcludedTerms outside a group are
	// rendered as "NOT term" in ToQueryString.
	pendingNot := false

	for {
		switch p.current.typ {
		case exprTokLParen:
			depth++
			sb.WriteByte('(')
			previousType = exprTokLParen
		case exprTokRParen:
			depth--
			sb.WriteByte(')')
			previousType = exprTokRParen
		case exprTokTerm:
			if previousType == exprTokTerm || previousType == exprTokRParen {
				sb.WriteByte(' ')
			}
			if pendingNot {
				sb.WriteString("NOT ")
				pendingNot = false
			}
			sb.WriteString(quoteWireToken(p.current.value))
			previousType = exprTokTerm
		case exprTokOr:
			sb.WriteString(" OR ")
			previousType = exprTokOr
		case exprTokPlus:
			// No operator meaning inside a group -- adjacent terms are
			// AND'ed by adjacency already, so '+' is dropped rather than
			// glued onto the next term as a raw character the AST
			// tokenizer rejects.
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

// isParenthesizedSubexpression reports whether term is a parenthesized
// sub-expression captured by captureParenExpression (already fully
// quoted/escaped), as opposed to plain term text still needing
// quoteWireToken's decision. Detected structurally, never by scanning for an
// "OR" substring inside a token, which would misclassify ordinary words such
// as ORDER and ORANGE.
func isParenthesizedSubexpression(term string) bool {
	return len(term) >= 2 && term[0] == '(' && term[len(term)-1] == ')'
}

// SearchExpression represents a parsed search expression.
type SearchExpression struct {
	// RequiredTerms contains every term that must match: those with a '+'
	// prefix and every unprefixed term (implicit AND), in the order they
	// appeared. An entry may itself be a parenthesized sub-expression (see
	// isParenthesizedSubexpression) captured from "+(...)".
	RequiredTerms []string

	// ExcludedTerms contains keywords marked for exclusion with '-'.
	ExcludedTerms []string

	// OptionalTerms is unused; kept for source compatibility. Every
	// unprefixed term is required (implicit AND) and lives in RequiredTerms.
	OptionalTerms []string

	// RawExpression holds AST-syntax text reconstructed from any bare OR
	// chain or parenthesized group not immediately preceded by '+'/'-',
	// with the shared wire-quoting decision already applied to each of its
	// terms.
	RawExpression string
}

// ParseSearchExpression parses a web-search-style search expression.
//
// Supported syntax:
//   - +term: required keyword
//   - -term: excluded keyword
//   - "phrase": phrase search
//   - OR (case-insensitive, as a whole word): logical OR
//   - (expr): grouping
func ParseSearchExpression(expression string) (*SearchExpression, error) {
	if expression == "" {
		return nil, &ValidationError{Message: "empty search expression"}
	}
	return newExprParser(expression).parse()
}

// HasComplexExpression returns whether the expression contains OR or
// grouping: a non-empty RawExpression, or a RequiredTerms/ExcludedTerms
// entry that is itself a parenthesized sub-expression (from "+(...)"
// or "-(...)").
func HasComplexExpression(expr *SearchExpression) bool {
	if expr.RawExpression != "" {
		return true
	}
	for _, t := range expr.RequiredTerms {
		if isParenthesizedSubexpression(t) {
			return true
		}
	}
	for _, t := range expr.ExcludedTerms {
		if isParenthesizedSubexpression(t) {
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
	emit := func(term string) string {
		if isParenthesizedSubexpression(term) {
			return term
		}
		return quoteWireToken(term)
	}

	var sb strings.Builder
	for i, term := range expr.RequiredTerms {
		if i > 0 {
			sb.WriteString(" AND ")
		}
		sb.WriteString(emit(term))
	}
	for _, term := range expr.ExcludedTerms {
		if sb.Len() > 0 {
			sb.WriteString(" AND ")
		}
		sb.WriteString("NOT ")
		sb.WriteString(emit(term))
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
