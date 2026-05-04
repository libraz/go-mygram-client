package mygram

import (
	"strings"
	"unicode"
)

// tokenType represents the kind of a token.
type tokenType int

const (
	tokenWord   tokenType = iota // Regular word
	tokenQuoted                  // Quoted string
	tokenPlus                    // + operator
	tokenMinus                   // - operator
	tokenOR                      // OR operator
	tokenLParen                  // (
	tokenRParen                  // )
	tokenEOF                     // End of input
)

// token represents an output token from the tokenizer.
type token struct {
	typ   tokenType
	value string
}

// tokenize splits a search expression into a sequence of tokens.
func tokenize(input string) ([]token, error) {
	// Normalize full-width spaces to half-width
	input = strings.ReplaceAll(input, "\u3000", " ")

	var tokens []token
	runes := []rune(input)
	i := 0

	for i < len(runes) {
		r := runes[i]

		// Skip whitespace
		if unicode.IsSpace(r) {
			i++
			continue
		}

		switch r {
		case '+':
			tokens = append(tokens, token{typ: tokenPlus, value: "+"})
			i++
		case '-':
			tokens = append(tokens, token{typ: tokenMinus, value: "-"})
			i++
		case '(':
			tokens = append(tokens, token{typ: tokenLParen, value: "("})
			i++
		case ')':
			tokens = append(tokens, token{typ: tokenRParen, value: ")"})
			i++
		case '"', '\'':
			// Quoted string
			quote := r
			i++
			var sb strings.Builder
			for i < len(runes) && runes[i] != quote {
				if runes[i] == '\\' && i+1 < len(runes) {
					i++
					switch runes[i] {
					case 'n':
						sb.WriteRune('\n')
					case 't':
						sb.WriteRune('\t')
					case 'r':
						sb.WriteRune('\r')
					case '\\':
						sb.WriteRune('\\')
					case '"':
						sb.WriteRune('"')
					case '\'':
						sb.WriteRune('\'')
					default:
						sb.WriteRune('\\')
						sb.WriteRune(runes[i])
					}
				} else {
					sb.WriteRune(runes[i])
				}
				i++
			}
			if i < len(runes) {
				i++ // Skip closing quote
			}
			tokens = append(tokens, token{typ: tokenQuoted, value: sb.String()})
		default:
			// Word
			var sb strings.Builder
			for i < len(runes) && !unicode.IsSpace(runes[i]) &&
				runes[i] != '+' && runes[i] != '-' && runes[i] != '(' && runes[i] != ')' &&
				runes[i] != '"' && runes[i] != '\'' {
				sb.WriteRune(runes[i])
				i++
			}
			word := sb.String()
			if word == "OR" {
				tokens = append(tokens, token{typ: tokenOR, value: "OR"})
			} else {
				tokens = append(tokens, token{typ: tokenWord, value: word})
			}
		}
	}

	tokens = append(tokens, token{typ: tokenEOF})
	return tokens, nil
}

// SearchExpression represents a parsed search expression.
type SearchExpression struct {
	// RequiredTerms contains keywords marked as required with +.
	RequiredTerms []string

	// ExcludedTerms contains keywords marked for exclusion with -.
	ExcludedTerms []string

	// OptionalTerms contains keywords without any modifier.
	OptionalTerms []string

	// RawExpression holds the original expression when it contains OR/grouping (complex expressions).
	RawExpression string
}

// ParseSearchExpression parses a web-search-style search expression.
//
// Supported syntax:
//   - +term: required keyword
//   - -term: excluded keyword
//   - "phrase": phrase search
//   - OR: logical OR
//   - (expr): grouping
func ParseSearchExpression(expression string) (*SearchExpression, error) {
	tokens, err := tokenize(expression)
	if err != nil {
		return nil, err
	}

	result := &SearchExpression{}
	hasComplex := false
	i := 0

	for i < len(tokens) && tokens[i].typ != tokenEOF {
		t := tokens[i]

		switch t.typ {
		case tokenPlus:
			i++
			if i < len(tokens) && (tokens[i].typ == tokenWord || tokens[i].typ == tokenQuoted) {
				result.RequiredTerms = append(result.RequiredTerms, tokens[i].value)
				i++
			}
		case tokenMinus:
			i++
			if i < len(tokens) && (tokens[i].typ == tokenWord || tokens[i].typ == tokenQuoted) {
				result.ExcludedTerms = append(result.ExcludedTerms, tokens[i].value)
				i++
			}
		case tokenOR:
			hasComplex = true
			i++
		case tokenLParen, tokenRParen:
			hasComplex = true
			i++
		case tokenWord, tokenQuoted:
			result.OptionalTerms = append(result.OptionalTerms, t.value)
			i++
		default:
			i++
		}
	}

	if hasComplex {
		result.RawExpression = expression
	}

	return result, nil
}

// HasComplexExpression returns whether the expression contains OR or grouping.
func HasComplexExpression(expr *SearchExpression) bool {
	return expr.RawExpression != ""
}

// ToQueryString converts a SearchExpression to a MygramDB query string.
func ToQueryString(expr *SearchExpression) string {
	if expr.RawExpression != "" {
		return expr.RawExpression
	}

	var parts []string

	// When required terms exist
	if len(expr.RequiredTerms) > 0 {
		parts = append(parts, strings.Join(expr.RequiredTerms, " AND "))
		if len(expr.OptionalTerms) > 0 {
			parts = append(parts, strings.Join(expr.OptionalTerms, " AND "))
		}
	} else if len(expr.OptionalTerms) > 0 {
		parts = append(parts, strings.Join(expr.OptionalTerms, " AND "))
	}

	query := strings.Join(parts, " AND ")

	for _, term := range expr.ExcludedTerms {
		query += " NOT " + term
	}

	return query
}

// ConvertSearchExpression converts a search expression to a MygramDB query string.
func ConvertSearchExpression(expression string) (string, error) {
	expr, err := ParseSearchExpression(expression)
	if err != nil {
		return "", err
	}
	if HasComplexExpression(expr) {
		return expression, nil
	}
	return ToQueryString(expr), nil
}

// SimplifySearchExpression decomposes a search expression into MainTerm/AndTerms/NotTerms.
// The result maps directly to the MygramDB SEARCH command format.
//
// OR-only or parenthesized sub-expressions (e.g. "python OR ruby" or
// "(a OR b)") are surfaced as a single parenthesized MainTerm so the OR
// semantics survive when the caller AND-composes the result. Mixed
// expressions that combine required terms with an OR sub-expression keep
// the required terms; the OR sub-expression is currently not surfaced.
func SimplifySearchExpression(expression string) (*SimplifiedExpression, error) {
	expr, err := ParseSearchExpression(expression)
	if err != nil {
		return nil, err
	}

	// Complex expression (OR / parenthesized): the OptionalTerms list
	// contains the inside of OR sub-expressions, which would lose meaning
	// if flattened into AND. Use the raw expression as MainTerm.
	if HasComplexExpression(expr) {
		if len(expr.RequiredTerms) > 0 {
			result := &SimplifiedExpression{MainTerm: expr.RequiredTerms[0]}
			if len(expr.RequiredTerms) > 1 {
				result.AndTerms = append([]string(nil), expr.RequiredTerms[1:]...)
			}
			if len(expr.ExcludedTerms) > 0 {
				result.NotTerms = append([]string(nil), expr.ExcludedTerms...)
			}
			return result, nil
		}

		raw := strings.TrimSpace(expr.RawExpression)
		var mainTerm string
		if strings.HasPrefix(raw, "(") && strings.HasSuffix(raw, ")") {
			mainTerm = raw
		} else {
			mainTerm = "(" + raw + ")"
		}
		result := &SimplifiedExpression{MainTerm: mainTerm}
		if len(expr.ExcludedTerms) > 0 {
			result.NotTerms = append([]string(nil), expr.ExcludedTerms...)
		}
		return result, nil
	}

	// Simple expression: combine required + optional (the implicit AND case).
	var positiveTerms []string
	positiveTerms = append(positiveTerms, expr.RequiredTerms...)
	positiveTerms = append(positiveTerms, expr.OptionalTerms...)

	if len(positiveTerms) == 0 {
		return nil, &ValidationError{Message: "search expression must contain at least one positive term"}
	}

	result := &SimplifiedExpression{
		MainTerm: positiveTerms[0],
	}
	if len(positiveTerms) > 1 {
		result.AndTerms = positiveTerms[1:]
	}
	if len(expr.ExcludedTerms) > 0 {
		result.NotTerms = expr.ExcludedTerms
	}

	return result, nil
}
