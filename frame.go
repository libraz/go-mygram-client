package mygram

import (
	"strings"
	"unicode/utf8"
)

// The TCP protocol has no length prefix, and several replies carry blank
// lines inside their bodies, so whether the bytes received so far form the
// whole frame depends on which command was sent. beginResponseFrame records
// that context once per request (command verb, a SEARCH HIGHLIGHT clause, the
// connection's DEBUG flag); isResponseComplete then walks the reply line by
// line and stops exactly at the frame's last CRLF. The grammar follows
// spec/tcp-commands.md §8, §10 and §12, with the transport CRLF the server
// appends to every response body (§1) included, and mirrors the C++ client's
// protocol_detection.h.

// requestToken is one request token as the server tokenizer splits it
// (spec/tcp-commands.md §2.1): text holds the raw bytes without the
// surrounding quotes and with escapes left undecoded.
type requestToken struct {
	text   string
	quoted bool
}

// tokenizeRequest splits a request line the way the server tokenizer does:
// on ASCII and Unicode whitespace outside quotes, where '"' and a single quote
// open a quoted region that a backslash escape cannot close and opening a quote
// flushes any pending unquoted token. Only token boundaries and the quoted
// flag are recovered; escapes are not decoded.
func tokenizeRequest(command string) []requestToken {
	var tokens []requestToken
	start := -1
	flush := func(end int) {
		if start >= 0 {
			tokens = append(tokens, requestToken{text: command[start:end]})
			start = -1
		}
	}

	pos := 0
	for pos < len(command) {
		if width := wireWhitespaceWidthAt(command, pos); width > 0 {
			flush(pos)
			pos += width
			continue
		}
		if quote := command[pos]; quote == '"' || quote == '\'' {
			flush(pos)
			pos++
			open := pos
			for pos < len(command) && command[pos] != quote {
				if command[pos] == '\\' {
					pos += 2
				} else {
					pos++
				}
			}
			closeAt := min(pos, len(command))
			tokens = append(tokens, requestToken{text: command[open:closeAt], quoted: true})
			pos = closeAt + 1
			continue
		}
		if start < 0 {
			start = pos
		}
		pos++
	}
	flush(len(command))
	return tokens
}

// equalsASCIIFold reports ASCII case-insensitive equality, matching the
// server's keyword comparison (spec/tcp-commands.md §3). Unlike
// strings.EqualFold it does not fold non-ASCII code points onto ASCII ones.
func equalsASCIIFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if toASCIIUpperByte(a[i]) != toASCIIUpperByte(b[i]) {
			return false
		}
	}
	return true
}

// frameCommand names the commands whose reply grammar cannot be told from the
// reply's first line alone.
type frameCommand int

const (
	frameCommandOther      frameCommand = iota
	frameCommandSearch                  // "OK RESULTS" may carry highlight rows and/or a DEBUG block
	frameCommandCount                   // "OK COUNT" may carry a DEBUG block
	frameCommandConfigHelp              // "+OK" body carries exactly one internal blank line
)

// responseFrame is the per-request framing context plus the parse position
// inside the reply. Create one with beginResponseFrame for every request and
// pass it to each isResponseComplete call for that request's reply.
type responseFrame struct {
	command   frameCommand
	debug     bool // the connection had DEBUG ON when the command was sent
	highlight bool // a SEARCH carried an unquoted HIGHLIGHT clause

	lineStart  int  // offset of the first line not yet consumed
	scanOffset int  // where the CRLF search resumes inside an unfinished line
	frameStart int  // offset of the status line; blank lines before it are a prior reply's leftover
	headerSeen bool // the status line has been consumed

	bodyLinePending bool // the next line is body even if empty (bare "+OK")
	untilEndLine    bool // lines are body until one reads exactly "END"
	blankLinesLeft  int  // empty lines still to come; the last one ends the frame
}

// decodeRequestToken decodes a request token's escapes the way the server
// tokenizer does. Inside quotes \n, \t, \r, \\, \" , \' and \xHH decode, \x
// without two hex digits is 'x', and any other escaped byte stands for
// itself. An unquoted token is taken verbatim, backslashes included.
func decodeRequestToken(token requestToken) string {
	text := token.text
	if !token.quoted {
		return text
	}
	var sb strings.Builder
	sb.Grow(len(text))
	for i := 0; i < len(text); i++ {
		if text[i] != '\\' || i+1 >= len(text) {
			sb.WriteByte(text[i])
			continue
		}
		i++
		switch escaped := text[i]; escaped {
		case 'n':
			sb.WriteByte('\n')
		case 't':
			sb.WriteByte('\t')
		case 'r':
			sb.WriteByte('\r')
		case 'x':
			high, low := -1, -1
			if i+2 < len(text) {
				high, low = hexDigitValue(text[i+1]), hexDigitValue(text[i+2])
			}
			if high >= 0 && low >= 0 {
				sb.WriteByte(byte(high<<4 | low))
				i += 2
			} else {
				sb.WriteByte('x')
			}
		default:
			sb.WriteByte(escaped)
		}
	}
	return sb.String()
}

// isNonExpressionClauseKeyword reports FILTER, SORT, LIMIT, OFFSET, HIGHLIGHT,
// FUZZY or FACET, which end even a Boolean search text.
func isNonExpressionClauseKeyword(token string) bool {
	for _, keyword := range [...]string{"FILTER", "SORT", "LIMIT", "OFFSET", "HIGHLIGHT", "FUZZY", "FACET"} {
		if equalsASCIIFold(token, keyword) {
			return true
		}
	}
	return false
}

// isClauseKeyword reports a non-expression clause keyword, or AND, OR or NOT.
func isClauseKeyword(token string) bool {
	return isNonExpressionClauseKeyword(token) ||
		equalsASCIIFold(token, "AND") || equalsASCIIFold(token, "OR") || equalsASCIIFold(token, "NOT")
}

// skipSearchText finds where the server ends a SEARCH's search text and
// returns the index of the first token after it. It mirrors
// ParseSearchTextTokens: the text is a Boolean expression when it opens with
// NOT, or when a top-level OR, or a parenthesized group after a top-level
// AND/NOT, comes before the first clause keyword. A Boolean expression ends
// only at a top-level non-expression clause keyword; a flat one also at AND,
// OR or NOT. Quoted tokens are never keywords and their parentheses never
// count.
func skipSearchText(tokens []requestToken, start int) int {
	parenDelta := func(text string) int {
		return strings.Count(text, "(") - strings.Count(text, ")")
	}

	booleanExpression := start < len(tokens) && !tokens[start].quoted && equalsASCIIFold(tokens[start].text, "NOT")
	depth := 0
	seenOperator := false
	for i := start; i < len(tokens) && !booleanExpression; i++ {
		text := tokens[i].text
		if tokens[i].quoted {
			continue
		}
		if depth == 0 && seenOperator && strings.Contains(text, "(") {
			booleanExpression = true
		}
		depth += parenDelta(text)
		if depth == 0 && (isNonExpressionClauseKeyword(text) || equalsASCIIFold(text, "ORDER")) {
			break
		}
		if depth == 0 && equalsASCIIFold(text, "OR") {
			booleanExpression = true
		}
		if depth == 0 && (equalsASCIIFold(text, "AND") || equalsASCIIFold(text, "NOT")) {
			seenOperator = true
		}
	}

	depth = 0
	pos := start
	for ; pos < len(tokens); pos++ {
		text := tokens[pos].text
		if tokens[pos].quoted {
			continue
		}
		depth += parenDelta(text)
		if depth == 0 && (isNonExpressionClauseKeyword(text) || (!booleanExpression && isClauseKeyword(text))) {
			break
		}
	}
	return pos
}

// skipFilterOperands skips the operands of a FILTER clause the way
// ParseFilterArguments consumes them. A token holding the operator
// ("col=value", or "col=" followed by the value) is one or two tokens;
// otherwise the clause takes "col op value". pos is the index just after
// FILTER.
func skipFilterOperands(tokens []requestToken, pos int) int {
	const separateOperands = 3
	startsWithOperator := func(value string) bool {
		return value != "" && (value[0] == '=' || value[0] == '<' || value[0] == '>' || value[0] == '!')
	}
	if pos >= len(tokens) {
		return pos
	}
	first := decodeRequestToken(tokens[pos])
	for _, op := range [...]string{">=", "<=", "!=", "<>", "=", ">", "<"} {
		at := strings.Index(first, op)
		if at < 0 {
			continue
		}
		value := first[at+len(op):]
		if at > 0 && value != "" && !startsWithOperator(value) {
			return pos + 1
		}
		if at > 0 && value == "" && pos+1 < len(tokens) && !startsWithOperator(decodeRequestToken(tokens[pos+1])) {
			return pos + 2
		}
		break
	}
	return pos + separateOperands
}

// skipSortOperands skips the operands of a SORT clause: "[BY] col [ASC|DESC]"
// or "[BY] ASC|DESC". pos is the index just after SORT.
func skipSortOperands(tokens []requestToken, pos int) int {
	tokenIs := func(index int, word string) bool {
		return index < len(tokens) && equalsASCIIFold(decodeRequestToken(tokens[index]), word)
	}
	isOrder := func(index int) bool { return tokenIs(index, "ASC") || tokenIs(index, "DESC") }
	if tokenIs(pos, "BY") {
		pos++
	}
	if isOrder(pos) {
		return pos + 1
	}
	pos++
	if isOrder(pos) {
		return pos + 1
	}
	return pos
}

// searchRequestsHighlight reports whether a SEARCH carries a HIGHLIGHT
// clause. It mirrors QueryParser::ParseSearch: after the search text each
// clause consumes its operands (AND and NOT one term, AND NOT two, FILTER and
// SORT as above, LIMIT and OFFSET one token, FUZZY an optional distance), and
// only an unquoted HIGHLIGHT in clause position counts. Requests the server
// rejects reply with ERROR, which ends the frame whatever this returns.
func searchRequestsHighlight(tokens []requestToken) bool {
	const searchTextStart = 2
	keywordAt := func(index int, keyword string) bool {
		return index < len(tokens) && !tokens[index].quoted && equalsASCIIFold(tokens[index].text, keyword)
	}

	pos := skipSearchText(tokens, searchTextStart)
	for pos < len(tokens) {
		clause := pos
		pos++
		switch {
		case keywordAt(clause, "HIGHLIGHT"):
			return true
		case keywordAt(clause, "AND"):
			if keywordAt(pos, "NOT") {
				pos += 2
			} else {
				pos++
			}
		case keywordAt(clause, "NOT"), keywordAt(clause, "LIMIT"), keywordAt(clause, "OFFSET"):
			pos++
		case keywordAt(clause, "FILTER"):
			pos = skipFilterOperands(tokens, pos)
		case keywordAt(clause, "SORT"):
			pos = skipSortOperands(tokens, pos)
		case keywordAt(clause, "FUZZY"):
			if pos < len(tokens) && !isClauseKeyword(decodeRequestToken(tokens[pos])) {
				pos++
			}
		default:
			return false
		}
	}
	return false
}

// beginResponseFrame records the framing context of a request before its
// reply is read. command is the request line as sent, without its CRLF;
// debugMode is whether the connection has DEBUG ON (spec §10.7).
func beginResponseFrame(command string, debugMode bool) *responseFrame {
	frame := &responseFrame{debug: debugMode}
	tokens := tokenizeRequest(command)
	if len(tokens) == 0 {
		return frame
	}
	switch verb := tokens[0].text; {
	case equalsASCIIFold(verb, "SEARCH"):
		frame.command = frameCommandSearch
		frame.highlight = searchRequestsHighlight(tokens)
	case equalsASCIIFold(verb, "COUNT"):
		frame.command = frameCommandCount
	case equalsASCIIFold(verb, "CONFIG") && len(tokens) > 1 && equalsASCIIFold(tokens[1].text, "HELP"):
		frame.command = frameCommandConfigHelp
	}
	return frame
}

// consumeResponseHeader classifies the status line and sets up the rest of
// the frame. It reports whether the status line is the whole frame.
func (f *responseFrame) consumeResponseHeader(line string) bool {
	const (
		okInfo         = "OK INFO"
		okReplication  = "OK REPLICATION"
		okCacheStats   = "OK CACHE_STATS"
		okDumpStatus   = "OK DUMP_STATUS"
		okDumpInfo     = "OK DUMP_INFO"
		okSyncStatus   = "OK SYNC_STATUS"
		okFacet        = "OK FACET"
		okResults      = "OK RESULTS "
		okCount        = "OK COUNT "
		plusOK         = "+OK"
		tableBorder    = "+-"
		facetWithSpace = okFacet + " "
	)

	switch {
	case strings.HasPrefix(line, errorPrefix):
		return true
	case line == okInfo || line == okReplication || line == okCacheStats || line == okDumpStatus ||
		strings.HasPrefix(line, okDumpInfo):
		f.untilEndLine = true
	case line == okSyncStatus:
		// The body already ends in "END\r\n", so the transport CRLF adds a blank line.
		f.untilEndLine = true
		f.blankLinesLeft = 1
	case strings.HasPrefix(line, okFacet):
		// Rows, a blank line closing the body, then the transport CRLF. Without
		// the distinct-value total the frame ends at the first blank line; a CRLF
		// after it is skipped as the next reply's leading blank line.
		hasTotal := len(line) >= len(facetWithSpace) && strings.Contains(line[len(facetWithSpace):], " ")
		if hasTotal {
			f.blankLinesLeft = 2
		} else {
			f.blankLinesLeft = 1
		}
	case strings.HasPrefix(line, tableBorder):
		// Non-empty SHOW VARIABLES: a bare table with no status prefix (spec §10.10).
		f.blankLinesLeft = 1
	case line == plusOK:
		// CONFIG body: at least one line, which may be empty when CONFIG SHOW
		// renders an empty value.
		f.bodyLinePending = true
		if f.command == frameCommandConfigHelp {
			f.blankLinesLeft = 2
		} else {
			f.blankLinesLeft = 1
		}
	case strings.HasPrefix(line, plusOK):
		f.blankLinesLeft = 1
	case f.command == frameCommandSearch && strings.HasPrefix(line, okResults):
		// A highlight header carries only the count; primary keys on it mean a plain reply.
		afterCount := line[len(okResults):]
		highlightRows := f.highlight && strings.Trim(afterCount, "0123456789") == ""
		f.blankLinesLeft = 0
		if highlightRows {
			f.blankLinesLeft++
		}
		if f.debug {
			f.blankLinesLeft += 2
		}
	case f.command == frameCommandCount && strings.HasPrefix(line, okCount):
		if f.debug {
			f.blankLinesLeft = 2
		}
	}
	return !f.untilEndLine && !f.bodyLinePending && f.blankLinesLeft == 0
}

// isResponseComplete reports whether the accumulated reply bytes are one
// complete frame. Call it after every read with all bytes received so far for
// the request the frame was begun for; already-consumed lines are not
// rescanned. It reports true when response ends exactly at the end of the
// frame.
func isResponseComplete(response string, f *responseFrame) bool {
	const (
		crlf    = "\r\n"
		endLine = "END"
	)
	for {
		from := max(f.lineStart, f.scanOffset)
		rel := strings.Index(response[min(from, len(response)):], crlf)
		if rel < 0 {
			f.scanOffset = 0
			if response != "" {
				f.scanOffset = len(response) - 1
			}
			return false
		}
		lineEnd := from + rel
		line := response[f.lineStart:lineEnd]
		f.lineStart = lineEnd + len(crlf)

		if !f.headerSeen {
			// No reply starts with a blank line, so one here is left over from the previous reply.
			if line == "" {
				f.frameStart = f.lineStart
				continue
			}
			f.headerSeen = true
			if f.consumeResponseHeader(line) {
				return true
			}
			continue
		}
		if f.bodyLinePending {
			f.bodyLinePending = false
			continue
		}
		if f.untilEndLine {
			if line == endLine {
				f.untilEndLine = false
				if f.blankLinesLeft == 0 {
					return true
				}
			}
			continue
		}
		if line == "" {
			f.blankLinesLeft--
			if f.blankLinesLeft == 0 {
				return true
			}
		}
	}
}

// wireWhitespaceWidthAt returns the byte width of the whitespace code point
// starting at text[pos] by the server's own rule (see isWireWhitespace), or 0
// when none starts there. It is byte-oriented, so a lone 0xA0 continuation
// byte inside a longer UTF-8 sequence is never mistaken for whitespace.
func wireWhitespaceWidthAt(text string, pos int) int {
	r, size := utf8.DecodeRuneInString(text[pos:])
	if r == utf8.RuneError && size <= 1 {
		return 0
	}
	if isWireWhitespace(r) {
		return size
	}
	return 0
}
