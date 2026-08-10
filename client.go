// Package mygram is a Go client library for the MygramDB full-text search engine.
//
// MygramDB is an in-memory full-text search engine using a memcached-style text protocol,
// providing 25-200x faster search performance compared to MySQL FULLTEXT.
//
// Basic usage:
//
//	client := mygram.NewClient("localhost:11016")
//	defer client.Close()
//
//	if err := client.Connect(); err != nil {
//	    log.Fatal(err)
//	}
//
//	resp, err := client.Search("threads", "keyword", mygram.SearchOptions{
//	    Limit:      100,
//	    SortColumn: "created_at",
//	    SortDesc:   true,
//	})
package mygram

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client is a MygramDB client.
//
// A Client wraps a single connection and serializes every command with an
// internal mutex, so it is safe for concurrent use but processes at most one
// request at a time. For concurrent workloads use a Pool, which lends out many
// Clients and adds reconnection and retry.
type Client struct {
	config ClientConfig
	conn   net.Conn
	mu     sync.Mutex

	// scratch is a reusable receive buffer, allocated once per connection to
	// avoid a per-command allocation on the hot read path. Guarded by mu.
	scratch []byte
}

// NewClient creates a new client for the given address. addr should be in
// "host:port" form; an address that does not parse as host:port is treated as a
// Unix socket path (so a bare "localhost" without a port becomes a socket path,
// not a TCP host).
//
// The returned Client owns a single connection and must be connected with
// Connect (or ConnectContext) before issuing commands. Most applications should
// use NewPool instead, which manages many connections with reconnection and
// retry; use Client directly only to own and control a single connection.
func NewClient(addr string) *Client {
	config := DefaultConfig()

	// Parse host:port format
	if host, portStr, err := net.SplitHostPort(addr); err == nil {
		config.Host = host
		if port, err := strconv.Atoi(portStr); err == nil {
			config.Port = port
		}
	} else {
		// Possibly a Unix socket path
		config.SocketPath = addr
	}

	return &Client{config: config}
}

// NewClientWithConfig creates a new client with the given configuration.
func NewClientWithConfig(config ClientConfig) *Client {
	defaults := DefaultConfig()
	if config.Host == "" {
		config.Host = defaults.Host
	}
	if config.Port == 0 {
		config.Port = defaults.Port
	}
	if config.TimeoutMs == 0 {
		config.TimeoutMs = defaults.TimeoutMs
	}
	if config.DumpSaveTimeoutMs == 0 {
		config.DumpSaveTimeoutMs = defaults.DumpSaveTimeoutMs
	}
	if config.DumpLoadTimeoutMs == 0 {
		config.DumpLoadTimeoutMs = defaults.DumpLoadTimeoutMs
	}
	if config.DumpVerifyTimeoutMs == 0 {
		config.DumpVerifyTimeoutMs = defaults.DumpVerifyTimeoutMs
	}
	if config.OptimizeTimeoutMs == 0 {
		config.OptimizeTimeoutMs = defaults.OptimizeTimeoutMs
	}
	if config.RecvBufferSize == 0 {
		config.RecvBufferSize = defaults.RecvBufferSize
	}
	if config.MaxQueryLength == 0 {
		config.MaxQueryLength = defaults.MaxQueryLength
	}
	if config.TCPKeepAliveIdle == 0 {
		config.TCPKeepAliveIdle = defaults.TCPKeepAliveIdle
	}
	return &Client{config: config}
}

// Connect establishes a connection to the server.
func (c *Client) Connect() error {
	return c.ConnectContext(context.Background())
}

// ConnectContext establishes a connection to the server with the given context.
func (c *Client) ConnectContext(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return nil
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	c.conn = conn
	return nil
}

// connectTimeout is the deadline for a dial and the AUTH exchange that follows
// it. ConnectTimeoutMs governs when set; otherwise the ordinary command timeout
// applies, which is how the client behaved before the knob existed.
func (c *Client) connectTimeout() time.Duration {
	ms := c.config.ConnectTimeoutMs
	if ms <= 0 {
		ms = c.config.TimeoutMs
	}
	return time.Duration(ms) * time.Millisecond
}

// dial opens a new connection to the configured endpoint and applies TCP
// keep-alive. It performs no locking and does not touch c.conn, so it is used
// both by ConnectContext (under the lock) and by the auto-reconnect path.
func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	timeout := c.connectTimeout()
	var d net.Dialer
	d.Timeout = timeout
	if c.config.SocketPath == "" && !c.config.DisableTCPKeepAlive {
		// Enable SO_KEEPALIVE with the configured idle period. Ignored for Unix
		// sockets; left at the dialer default when DisableTCPKeepAlive is set.
		idle := c.config.TCPKeepAliveIdle
		if idle <= 0 {
			idle = 60 * time.Second
		}
		d.KeepAlive = idle
	}

	var conn net.Conn
	var err error
	if c.config.SocketPath != "" {
		conn, err = d.DialContext(ctx, "unix", c.config.SocketPath)
	} else {
		addr := net.JoinHostPort(c.config.Host, strconv.Itoa(c.config.Port))
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			te := &TimeoutError{Message: "connection timeout", beforeSend: true}
			if ctx.Err() == context.DeadlineExceeded {
				te.Err = context.DeadlineExceeded
			}
			return nil, te
		}
		// Nothing was sent, so a retry on a fresh dial is safe for any command.
		return nil, &ConnectionError{Message: "failed to connect", Err: err, beforeSend: true}
	}

	// Administrative authorization is per-connection state on the server, so a
	// configured token must be replayed on every socket, including the ones a
	// reconnect opens. Doing it here keeps a reconnected client as privileged
	// as the original.
	if c.config.AdminToken != "" {
		if err := c.authenticateConn(conn, timeout); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	return conn, nil
}

// authenticateConn sends AUTH on a freshly dialed connection and verifies the
// acknowledgement. The connection is not yet published as c.conn, so failures
// are reported to the caller, which closes it.
func (c *Client) authenticateConn(conn net.Conn, timeout time.Duration) error {
	token, err := quoteCommandArgument(c.config.AdminToken, "admin token")
	if err != nil {
		return err
	}
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return &ConnectionError{Message: "failed to set write deadline for AUTH", Err: err, beforeSend: true}
	}
	if _, err := conn.Write([]byte("AUTH " + token + "\r\n")); err != nil {
		return &ConnectionError{Message: "failed to send AUTH", Err: err, beforeSend: true}
	}
	response, err := c.readFrom(conn, timeout)
	if err != nil {
		return err
	}
	return checkAuthResponse(response)
}

// authenticatedResponse is the acknowledgement the server returns for a
// successful AUTH.
const authenticatedResponse = "OK AUTHENTICATED"

// checkAuthResponse normalizes an AUTH reply and converts anything other than
// the acknowledgement into an error.
func checkAuthResponse(response string) error {
	normalized := strings.TrimSpace(strings.ReplaceAll(response, "\r\n", "\n"))
	if strings.HasPrefix(normalized, errorPrefix) {
		return parseErrorFrame(normalized)
	}
	if normalized != authenticatedResponse {
		return &ProtocolError{Message: "unexpected AUTH response: " + normalized}
	}
	return nil
}

// reconnectLocked tears down any half-open socket and dials a fresh one. The
// caller must hold c.mu. On failure c.conn is left nil.
func (c *Client) reconnectLocked(ctx context.Context) error {
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	c.conn = conn
	return nil
}

// Close closes the connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

// IsConnected returns whether the client is currently connected.
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// Authenticate authorizes the connection for administrative commands
// (MygramDB >= v1.10).
func (c *Client) Authenticate(token string) error {
	return c.AuthenticateContext(context.Background(), token)
}

// AuthenticateContext authorizes the connection for administrative commands
// with the given context.
//
// A server configured with api.admin_token gates DUMP, SYNC, REPLICATION,
// CONFIG, CACHE, OPTIMIZE, DEBUG, SET and SHOW VARIABLES behind AUTH, and the
// grant is per-connection. The token is remembered on the client so a
// reconnect re-authenticates before the next command runs; setting
// ClientConfig.AdminToken up front does the same thing without an explicit
// call, and is the only way to authenticate the connections a Pool opens.
func (c *Client) AuthenticateContext(ctx context.Context, token string) error {
	if token == "" {
		return &ValidationError{Message: "admin token must not be empty"}
	}
	quoted, err := quoteCommandArgument(token, "admin token")
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.config.AdminToken = token
	c.mu.Unlock()

	response, err := c.SendCommandContext(ctx, "AUTH "+quoted)
	if err != nil {
		return err
	}
	return checkAuthResponse(response)
}

// SendCommand sends a raw command to the server and returns the response.
func (c *Client) SendCommand(command string) (string, error) {
	return c.SendCommandContext(context.Background(), command)
}

// SendCommandContext sends a raw command to the server with the given context.
//
// The context bounds how long the call waits: its deadline, if any, caps the
// I/O timeout, and a context that is already canceled or past its deadline
// fails fast before anything is sent. Once the request is in flight,
// cancellation of a deadline-less context does not interrupt it; the command
// still completes or hits TimeoutMs.
func (c *Client) SendCommandContext(ctx context.Context, command string) (string, error) {
	return c.sendCommandWithTimeout(ctx, command, c.config.TimeoutMs)
}

// sendCommandWithTimeout is SendCommandContext with an explicit I/O timeout, so
// a command that rebuilds or traverses the whole index can outlive the ordinary
// request timeout. timeoutMs of 0 or less falls back to TimeoutMs.
func (c *Client) sendCommandWithTimeout(ctx context.Context, command string, timeoutMs int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if timeoutMs <= 0 {
		timeoutMs = c.config.TimeoutMs
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond

	// Use context deadline if it is shorter, and remember that it was the
	// binding deadline so a resulting timeout can be attributed to the context.
	ctxDeadlineBinding := false
	if deadline, ok := ctx.Deadline(); ok {
		if d := time.Until(deadline); d < timeout {
			timeout = d
			ctxDeadlineBinding = true
		}
	}

	// A failure before or during the write means the server never observed the
	// command, so with AutoReconnect it is safe to reconnect once and resend.
	// A failure during the read means the command was already delivered, so it
	// is surfaced without resending.
	reconnected := false
	for {
		if c.conn == nil {
			if c.config.AutoReconnect && !reconnected {
				reconnected = true
				if err := c.reconnectLocked(ctx); err != nil {
					return "", err
				}
			} else {
				return "", &ConnectionError{Message: "not connected to server", beforeSend: true}
			}
		}

		// Write
		if err := c.conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
			// The command has not been written, so the server never acted on it,
			// and the connection is no longer usable; close it and allow a retry.
			_ = c.conn.Close()
			c.conn = nil
			if c.config.AutoReconnect && !reconnected {
				reconnected = true
				if rerr := c.reconnectLocked(ctx); rerr != nil {
					return "", rerr
				}
				continue
			}
			return "", &ConnectionError{Message: "failed to set write deadline", Err: err, beforeSend: true}
		}

		if _, err := c.conn.Write([]byte(command + "\r\n")); err != nil {
			_ = c.conn.Close()
			c.conn = nil
			// The command was not (fully) written, so the server never acted on
			// it; mark it as safe to retry on a fresh connection.
			if c.config.AutoReconnect && !reconnected {
				reconnected = true
				if rerr := c.reconnectLocked(ctx); rerr != nil {
					return "", rerr
				}
				continue
			}
			return "", &ConnectionError{Message: "failed to send command", Err: err, beforeSend: true}
		}

		return c.finishCommand(ctx, timeout, ctxDeadlineBinding)
	}
}

// finishCommand reads and normalizes the response after the command has been
// written. A read-phase failure is never resent: the command was delivered, so
// its outcome is unknown.
func (c *Client) finishCommand(ctx context.Context, timeout time.Duration, ctxDeadlineBinding bool) (string, error) {
	// Read
	response, err := c.readResponse(timeout)
	if err != nil {
		// If the read timed out because the caller's context deadline was the
		// binding one (the effective deadline is min(ctx, TimeoutMs)), attribute
		// the cause so errors.Is(err, context.DeadlineExceeded) matches. Decide
		// from which deadline was shorter rather than polling ctx.Err(), whose
		// timer may not have fired yet at the instant the read deadline elapses.
		if te, ok := err.(*TimeoutError); ok && te.Err == nil &&
			(ctxDeadlineBinding || ctx.Err() == context.DeadlineExceeded) {
			te.Err = context.DeadlineExceeded
		}
		return "", err
	}

	// Normalize CRLF and trim
	response = strings.ReplaceAll(response, "\r\n", "\n")
	response = strings.TrimSpace(response)

	// Check for server error
	if strings.HasPrefix(response, errorPrefix) {
		return "", parseErrorFrame(response)
	}

	return response, nil
}

// readResponse reads a complete response on the client's current connection.
// A failure other than a timeout leaves the connection unusable, so it is
// closed and cleared here; a timeout leaves the socket intact for the caller
// to classify.
func (c *Client) readResponse(timeout time.Duration) (string, error) {
	response, err := c.readFrom(c.conn, timeout)
	if err != nil {
		if _, isTimeout := err.(*TimeoutError); !isTimeout {
			_ = c.conn.Close()
			c.conn = nil
		}
		return "", err
	}
	return response, nil
}

// readFrom reads one complete response frame from conn. It has no side effects
// on the client's connection state, so it also serves the connection handshake,
// which runs before the socket is published as c.conn.
//
// The scratch buffer it reuses is guarded by c.mu, which every caller holds.
func (c *Client) readFrom(conn net.Conn, timeout time.Duration) (string, error) {
	if c.scratch == nil {
		c.scratch = make([]byte, c.config.RecvBufferSize)
	}
	var sb strings.Builder
	deadline := time.Now().Add(timeout)

	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			// The command was already written, so its outcome is unknown; the
			// connection cannot be reused (beforeSend stays false).
			return "", &ConnectionError{Message: "failed to set read deadline", Err: err}
		}

		n, err := conn.Read(c.scratch)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				return "", &TimeoutError{Message: "command timeout"}
			}
			return "", &ConnectionError{Message: "failed to read response", Err: err}
		}

		sb.Write(c.scratch[:n])

		if c.config.MaxResponseSize > 0 && sb.Len() > c.config.MaxResponseSize {
			// The stream position is now unknown, so the connection cannot be
			// reused; report a protocol violation.
			return "", &ProtocolError{Message: "response exceeds MaxResponseSize"}
		}

		// strings.Builder.String() does not copy, so the only cost per read is
		// the completeness scan. responseMayBeComplete is an O(1) necessary
		// condition that lets us skip that scan until a terminator could
		// plausibly be present, avoiding an O(n^2) blowup on large responses.
		data := sb.String()
		if responseMayBeComplete(data) && isResponseComplete(data) {
			return data, nil
		}
	}
}

// responseMayBeComplete is a cheap necessary condition for isResponseComplete:
// every complete response recognized by isResponseComplete ends with a newline
// or with a bare "END" marker. When this returns false the full check is
// guaranteed to return false, so it is safe to skip.
func responseMayBeComplete(data string) bool {
	return strings.HasSuffix(data, "\n") || strings.HasSuffix(data, "END")
}

// endTerminatedPrefix describes a multi-line response prefix that is
// terminated by "END\r\n" (or "END\n"). When exactMatch is true the first
// line must equal the prefix exactly; otherwise the prefix may be followed
// by " <payload>" on the same line (e.g. "OK DUMP_INFO /path/to/file").
type endTerminatedPrefix struct {
	prefix     string
	exactMatch bool
}

// endTerminatedPrefixes lists every multi-line response prefix that the
// server terminates with an explicit "END" marker. Matched against the
// first line of the buffer only, to avoid false positives when the prefix
// appears later as payload (e.g. as a primary key).
var endTerminatedPrefixes = []endTerminatedPrefix{
	{"OK INFO", true},
	{"OK REPLICATION", true},
	{"OK CACHE_STATS", true},
	{"OK DUMP_STATUS", true},
	{"OK SYNC_STATUS", true},
	{"OK DUMP_INFO", false},
}

// isResponseComplete determines whether the response has been fully received.
func isResponseComplete(data string) bool {
	endsWithBlank := strings.HasSuffix(data, "\n\n") ||
		strings.HasSuffix(data, "\r\n\r\n") ||
		strings.HasSuffix(data, "\n\r\n")

	endsWithEndMarker := strings.HasSuffix(data, "\nEND\n") ||
		strings.HasSuffix(data, "\nEND\r\n") ||
		strings.HasSuffix(data, "\nEND") ||
		strings.HasSuffix(data, "\r\nEND")

	// First-line prefix detection for END-terminated multi-line responses.
	// Accept either an explicit END marker (the canonical termination per the
	// MygramDB v1.6 protocol) or a trailing blank line, since the Go test
	// fixtures and earlier server builds use blank-line termination.
	if firstLine, ok := extractFirstLine(data); ok {
		for _, p := range endTerminatedPrefixes {
			matched := false
			if p.exactMatch {
				matched = firstLine == p.prefix
			} else {
				matched = firstLine == p.prefix || strings.HasPrefix(firstLine, p.prefix+" ")
			}
			if matched {
				return endsWithEndMarker || endsWithBlank
			}
		}
	}

	// CONFIG: "+OK\n..." terminated by blank line.
	if strings.HasPrefix(data, "+OK\n") || strings.HasPrefix(data, "+OK\r\n") || data == "+OK" {
		return endsWithBlank
	}

	// Multi-line OK CONFIG.
	if strings.HasPrefix(data, "OK CONFIG\n") || strings.HasPrefix(data, "OK CONFIG\r\n") {
		return endsWithBlank
	}

	// FACET response: first line starts with "OK FACET <n>", followed by
	// <value>\t<count> lines and optional debug comments. Terminated by the
	// server-appended trailing \r\n (i.e. ends with \r\n\r\n).
	if strings.HasPrefix(data, "OK FACET") {
		return endsWithBlank
	}

	// Generic multi-line terminator (e.g. SEARCH HIGHLIGHT).
	if endsWithBlank {
		return true
	}

	// DEBUG response.
	if strings.Contains(data, "# DEBUG") {
		return endsWithBlank
	}

	// Single-line response with trailing newline.
	lines := strings.Split(data, "\n")
	if len(lines) >= 2 && lines[len(lines)-1] == "" {
		return true
	}
	if strings.HasSuffix(data, "\r\n") && !strings.Contains(data, "\n\n") {
		// Single line with CRLF termination
		first := strings.TrimRight(data, "\r\n")
		if !strings.Contains(first, "\n") {
			return true
		}
	}

	return false
}

// extractFirstLine returns the first line of the buffer (stripped of CR/LF)
// and whether a complete first line was found.
func extractFirstLine(data string) (string, bool) {
	idxCRLF := strings.Index(data, "\r\n")
	idxLF := strings.Index(data, "\n")
	switch {
	case idxCRLF == -1 && idxLF == -1:
		return "", false
	case idxCRLF != -1 && (idxLF == -1 || idxCRLF <= idxLF):
		return data[:idxCRLF], true
	default:
		return data[:idxLF], true
	}
}

// Search performs a full-text search.
func (c *Client) Search(table, query string, opts SearchOptions) (*SearchResponse, error) {
	return c.SearchContext(context.Background(), table, query, opts)
}

// SearchContext performs a full-text search with the given context.
func (c *Client) SearchContext(ctx context.Context, table, query string, opts SearchOptions) (*SearchResponse, error) {
	if err := validateTableName(table); err != nil {
		return nil, err
	}
	if err := ensureSafeValue(query, "query"); err != nil {
		return nil, err
	}
	if err := ensureSafeValues(opts.AndTerms, "andTerms"); err != nil {
		return nil, err
	}
	if err := ensureSafeValues(opts.NotTerms, "notTerms"); err != nil {
		return nil, err
	}
	if err := validateFilters(opts.Filters, opts.FilterConditions); err != nil {
		return nil, err
	}
	if err := validateQueryMode(opts.QueryMode); err != nil {
		return nil, err
	}
	if err := validateSort(opts.SortColumn, opts.SortPrimaryKey); err != nil {
		return nil, err
	}
	if err := ensureQueryLengthWithinLimit(query, opts.AndTerms, opts.NotTerms, opts.Filters, opts.FilterConditions, opts.SortColumn, c.config.MaxQueryLength); err != nil {
		return nil, err
	}
	if err := validateFuzzy(opts.Fuzzy); err != nil {
		return nil, err
	}
	if err := validateHighlight(opts.Highlight); err != nil {
		return nil, err
	}

	// A boolean query is the caller's own expression and goes on the wire
	// verbatim so the server's AST parser sees its AND / OR / NOT operators and
	// parentheses; literal text is quoted so the same words stay data.
	searchText := escapeQueryString(query)
	if opts.QueryMode == QueryBoolean {
		searchText = query
	}
	parts := []string{"SEARCH", table, searchText}

	for _, term := range opts.AndTerms {
		parts = append(parts, "AND", escapeQueryString(term))
	}
	for _, term := range opts.NotTerms {
		parts = append(parts, "NOT", escapeQueryString(term))
	}
	appendFilters(&parts, opts.Filters, opts.FilterConditions)
	switch {
	case opts.SortColumn != "":
		dir := "ASC"
		if opts.SortDesc {
			dir = "DESC"
		}
		parts = append(parts, "SORT", opts.SortColumn, dir)
	case opts.SortPrimaryKey != PrimaryKeySortDefault:
		// The column-less shorthand orders by primary key. Omitting the clause
		// selects the same descending default, so only an explicit request
		// reaches the wire.
		parts = append(parts, "SORT", opts.SortPrimaryKey.wire())
	}

	if opts.Fuzzy > 0 {
		parts = append(parts, "FUZZY", strconv.Itoa(opts.Fuzzy))
	}

	appendHighlightClause(&parts, opts.Highlight)

	if opts.Limit == 0 && opts.Offset == 0 {
		// Both zero: preserve historical default of LIMIT 1000.
		parts = append(parts, "LIMIT", "1000")
	} else {
		appendLimitOffset(&parts, opts.Limit, opts.Offset)
	}

	response, err := c.SendCommandContext(ctx, strings.Join(parts, " "))
	if err != nil {
		return nil, err
	}
	return parseSearchResponse(response)
}

// SearchWithHighlights is a Search variant that requests highlighted snippets.
// Any highlight options passed in opts are preserved; otherwise server
// defaults are used. Snippets are returned in SearchResult.Snippet. Requires
// MygramDB >= v1.6.
func (c *Client) SearchWithHighlights(table, query string, opts SearchOptions) (*SearchResponse, error) {
	return c.SearchWithHighlightsContext(context.Background(), table, query, opts)
}

// SearchWithHighlightsContext is the context-aware variant of SearchWithHighlights.
func (c *Client) SearchWithHighlightsContext(ctx context.Context, table, query string, opts SearchOptions) (*SearchResponse, error) {
	if opts.Highlight == nil {
		opts.Highlight = &HighlightOptions{}
	}
	return c.SearchContext(ctx, table, query, opts)
}

// SearchRaw searches using a pre-built boolean expression (MygramDB >= v1.8).
//
// The expression is sent verbatim (unquoted) so the server tokenizes it and its
// AST parser can interpret AND / OR / NOT / parentheses, including OR groups
// nested under AND such as "alpha AND (beta OR gamma)". A quoted expression
// would be treated by a v1.8 server as a single literal phrase, so quoting is
// intentionally not applied. Pair this with ConvertSearchExpression to preserve
// OR / grouping semantics that Search's AND/NOT decomposition cannot express.
func (c *Client) SearchRaw(table, rawQuery string, opts SearchRawOptions) (*SearchResponse, error) {
	return c.SearchRawContext(context.Background(), table, rawQuery, opts)
}

// SearchRawContext is the context-aware variant of SearchRaw.
func (c *Client) SearchRawContext(ctx context.Context, table, rawQuery string, opts SearchRawOptions) (*SearchResponse, error) {
	if err := validateTableName(table); err != nil {
		return nil, err
	}
	if rawQuery == "" {
		return nil, &ValidationError{Message: "rawQuery must not be empty"}
	}
	if err := ensureSafeValue(rawQuery, "rawQuery"); err != nil {
		return nil, err
	}
	if err := validateHighlight(opts.Highlight); err != nil {
		return nil, err
	}

	// The boolean expression is sent verbatim, not as a quoted token: a v1.8
	// server treats a quoted phrase embedding AND/OR/NOT as a literal phrase,
	// so quoting would defeat boolean parsing. ensureSafeValue above has already
	// rejected control characters, so the expression cannot break the framing.
	parts := []string{"SEARCH", table, rawQuery}
	appendHighlightClause(&parts, opts.Highlight)
	appendLimitOffset(&parts, opts.Limit, opts.Offset)

	response, err := c.SendCommandContext(ctx, strings.Join(parts, " "))
	if err != nil {
		return nil, err
	}
	return parseSearchResponse(response)
}

// SearchRawWithHighlights is a SearchRaw variant that requests highlighted
// snippets. Any highlight options passed in opts are preserved; otherwise
// server defaults are used.
func (c *Client) SearchRawWithHighlights(table, rawQuery string, opts SearchRawOptions) (*SearchResponse, error) {
	return c.SearchRawWithHighlightsContext(context.Background(), table, rawQuery, opts)
}

// SearchRawWithHighlightsContext is the context-aware variant of SearchRawWithHighlights.
func (c *Client) SearchRawWithHighlightsContext(ctx context.Context, table, rawQuery string, opts SearchRawOptions) (*SearchResponse, error) {
	if opts.Highlight == nil {
		opts.Highlight = &HighlightOptions{}
	}
	return c.SearchRawContext(ctx, table, rawQuery, opts)
}

// appendHighlightClause appends the HIGHLIGHT clause (and its TAG /
// SNIPPET_LEN / MAX_FRAGMENTS sub-options) when highlight options are present.
func appendHighlightClause(parts *[]string, h *HighlightOptions) {
	if h == nil {
		return
	}
	*parts = append(*parts, "HIGHLIGHT")
	if h.OpenTag != "" || h.CloseTag != "" {
		*parts = append(*parts, "TAG", h.OpenTag, h.CloseTag)
	}
	if h.SnippetLen > 0 {
		*parts = append(*parts, "SNIPPET_LEN", strconv.Itoa(h.SnippetLen))
	}
	if h.MaxFragments > 0 {
		*parts = append(*parts, "MAX_FRAGMENTS", strconv.Itoa(h.MaxFragments))
	}
}

// appendFilters appends the FILTER clauses for both filter forms: the
// equality-only map first, then the typed comparison conditions in the order
// the caller gave them.
//
// Map iteration order is randomized in Go, so the keys are sorted to keep one
// set of options producing one byte-identical command — which matters because
// the server's query cache keys on the command text.
func appendFilters(parts *[]string, filters map[string]string, conditions []FilterCondition) {
	if len(filters) > 0 {
		keys := make([]string, 0, len(filters))
		for k := range filters {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			*parts = append(*parts, "FILTER", k, "=", escapeQueryString(filters[k]))
		}
	}
	for _, cond := range conditions {
		*parts = append(*parts, "FILTER", cond.Key, cond.Op.wire(), escapeQueryString(cond.Value))
	}
}

// appendLimitOffset appends the LIMIT / OFFSET clause, matching the C++
// client's AppendLimitOffset:
//
//   - limit > 0 and offset > 0 emits the atomic "LIMIT <offset>,<limit>".
//   - limit > 0 and offset == 0 emits "LIMIT <limit>".
//   - limit == 0 and offset > 0 emits a bare "OFFSET <offset>" so the server
//     still skips the first <offset> results.
func appendLimitOffset(parts *[]string, limit, offset int) {
	switch {
	case limit > 0 && offset > 0:
		*parts = append(*parts, "LIMIT", fmt.Sprintf("%d,%d", offset, limit))
	case limit > 0:
		*parts = append(*parts, "LIMIT", strconv.Itoa(limit))
	case offset > 0:
		*parts = append(*parts, "OFFSET", strconv.Itoa(offset))
	}
}

// Facet aggregates distinct values of a filter column with their document
// counts. Optionally scoped to a search result set via opts.Query and
// related options. Available on MygramDB >= v1.6.
func (c *Client) Facet(table, column string, opts FacetOptions) (*FacetResponse, error) {
	return c.FacetContext(context.Background(), table, column, opts)
}

// FacetContext is the context-aware variant of Facet.
func (c *Client) FacetContext(ctx context.Context, table, column string, opts FacetOptions) (*FacetResponse, error) {
	if err := validateTableName(table); err != nil {
		return nil, err
	}
	if err := validateFacetColumn(column); err != nil {
		return nil, err
	}
	if err := ensureSafeValue(opts.Query, "query"); err != nil {
		return nil, err
	}
	if err := ensureSafeValues(opts.AndTerms, "andTerms"); err != nil {
		return nil, err
	}
	if err := ensureSafeValues(opts.NotTerms, "notTerms"); err != nil {
		return nil, err
	}
	if err := validateFilters(opts.Filters, opts.FilterConditions); err != nil {
		return nil, err
	}
	if err := ensureQueryLengthWithinLimit(opts.Query, opts.AndTerms, opts.NotTerms, opts.Filters, opts.FilterConditions, "", c.config.MaxQueryLength); err != nil {
		return nil, err
	}

	parts := []string{"FACET", table, column}
	if opts.Query != "" {
		parts = append(parts, escapeQueryString(opts.Query))
	}
	for _, term := range opts.AndTerms {
		parts = append(parts, "AND", escapeQueryString(term))
	}
	for _, term := range opts.NotTerms {
		parts = append(parts, "NOT", escapeQueryString(term))
	}
	appendFilters(&parts, opts.Filters, opts.FilterConditions)
	// FACET pages with a separate OFFSET clause rather than the "offset,count"
	// LIMIT form that SEARCH uses; the server accepts them in either order but
	// this matches the reference client's wire output.
	if opts.Offset > 0 {
		parts = append(parts, "OFFSET", strconv.Itoa(opts.Offset))
	}
	if opts.Limit > 0 {
		parts = append(parts, "LIMIT", strconv.Itoa(opts.Limit))
	}

	response, err := c.SendCommandContext(ctx, strings.Join(parts, " "))
	if err != nil {
		return nil, err
	}
	return parseFacetResponse(response)
}

// Count returns the number of matching documents.
func (c *Client) Count(table, query string, opts CountOptions) (*CountResponse, error) {
	return c.CountContext(context.Background(), table, query, opts)
}

// CountContext performs a count query with the given context.
func (c *Client) CountContext(ctx context.Context, table, query string, opts CountOptions) (*CountResponse, error) {
	if err := validateTableName(table); err != nil {
		return nil, err
	}
	if err := ensureSafeValue(query, "query"); err != nil {
		return nil, err
	}
	if err := ensureSafeValues(opts.AndTerms, "andTerms"); err != nil {
		return nil, err
	}
	if err := ensureSafeValues(opts.NotTerms, "notTerms"); err != nil {
		return nil, err
	}
	if err := validateFilters(opts.Filters, opts.FilterConditions); err != nil {
		return nil, err
	}
	if err := validateQueryMode(opts.QueryMode); err != nil {
		return nil, err
	}

	searchText := escapeQueryString(query)
	if opts.QueryMode == QueryBoolean {
		searchText = query
	}
	parts := []string{"COUNT", table, searchText}
	for _, term := range opts.AndTerms {
		parts = append(parts, "AND", escapeQueryString(term))
	}
	for _, term := range opts.NotTerms {
		parts = append(parts, "NOT", escapeQueryString(term))
	}
	appendFilters(&parts, opts.Filters, opts.FilterConditions)

	response, err := c.SendCommandContext(ctx, strings.Join(parts, " "))
	if err != nil {
		return nil, err
	}
	return parseCountResponse(response)
}

// Get retrieves a document by its primary key.
func (c *Client) Get(table, primaryKey string) (*Document, error) {
	return c.GetContext(context.Background(), table, primaryKey)
}

// GetContext retrieves a document by its primary key with the given context.
func (c *Client) GetContext(ctx context.Context, table, primaryKey string) (*Document, error) {
	if err := validateTableName(table); err != nil {
		return nil, err
	}
	if primaryKey == "" {
		return nil, &ValidationError{Message: "primary key must not be empty"}
	}

	// A primary key is data, not an identifier: it is escaped reversibly so any
	// key the server can return is also a key the server can be asked for.
	response, err := c.SendCommandContext(ctx, fmt.Sprintf("GET %s %s", table, escapeProtocolToken(primaryKey)))
	if err != nil {
		return nil, err
	}
	return parseGetResponse(response)
}

// Info retrieves server information.
func (c *Client) Info() (*ServerInfo, error) {
	return c.InfoContext(context.Background())
}

// InfoContext retrieves server information with the given context.
func (c *Client) InfoContext(ctx context.Context) (*ServerInfo, error) {
	response, err := c.SendCommandContext(ctx, "INFO")
	if err != nil {
		return nil, err
	}
	return parseInfoResponse(response)
}

// GetConfig retrieves server configuration in YAML format.
func (c *Client) GetConfig() (string, error) {
	return c.GetConfigContext(context.Background())
}

// GetConfigContext retrieves server configuration with the given context.
func (c *Client) GetConfigContext(ctx context.Context) (string, error) {
	return c.SendCommandContext(ctx, "CONFIG")
}

// GetReplicationStatus retrieves the replication status.
func (c *Client) GetReplicationStatus() (*ReplicationStatus, error) {
	return c.GetReplicationStatusContext(context.Background())
}

// GetReplicationStatusContext retrieves the replication status with the given context.
func (c *Client) GetReplicationStatusContext(ctx context.Context) (*ReplicationStatus, error) {
	response, err := c.SendCommandContext(ctx, "REPLICATION STATUS")
	if err != nil {
		return nil, err
	}
	return parseReplicationStatusResponse(response)
}

// StartReplication starts replication.
func (c *Client) StartReplication() error {
	return c.StartReplicationContext(context.Background())
}

// StartReplicationContext starts replication with the given context.
func (c *Client) StartReplicationContext(ctx context.Context) error {
	_, err := c.SendCommandContext(ctx, "REPLICATION START")
	return err
}

// StopReplication stops replication.
func (c *Client) StopReplication() error {
	return c.StopReplicationContext(context.Background())
}

// StopReplicationContext stops replication with the given context.
func (c *Client) StopReplicationContext(ctx context.Context) error {
	_, err := c.SendCommandContext(ctx, "REPLICATION STOP")
	return err
}

// EnableDebug enables debug mode.
func (c *Client) EnableDebug() error {
	return c.EnableDebugContext(context.Background())
}

// EnableDebugContext enables debug mode with the given context.
func (c *Client) EnableDebugContext(ctx context.Context) error {
	_, err := c.SendCommandContext(ctx, "DEBUG ON")
	return err
}

// DisableDebug disables debug mode.
func (c *Client) DisableDebug() error {
	return c.DisableDebugContext(context.Background())
}

// DisableDebugContext disables debug mode with the given context.
func (c *Client) DisableDebugContext(ctx context.Context) error {
	_, err := c.SendCommandContext(ctx, "DEBUG OFF")
	return err
}

// DumpSave saves the index to a file.
func (c *Client) DumpSave(filepath string) (string, error) {
	return c.DumpSaveContext(context.Background(), filepath)
}

// DumpSaveContext saves the index to a file with the given context.
func (c *Client) DumpSaveContext(ctx context.Context, filepath string) (string, error) {
	cmd := "DUMP SAVE"
	if filepath != "" {
		safe, err := quoteCommandArgument(filepath, "filepath")
		if err != nil {
			return "", err
		}
		cmd += " " + safe
	}
	return c.SendCommandContext(ctx, cmd)
}

// dumpStatusPollInterval is how often DumpSaveAndWait re-reads DUMP STATUS
// while an asynchronous dump is running.
const dumpStatusPollInterval = 200 * time.Millisecond

// DumpSaveAndWait saves the index and blocks until the dump has finished.
func (c *Client) DumpSaveAndWait(filepath string) (string, error) {
	return c.DumpSaveAndWaitContext(context.Background(), filepath)
}

// DumpSaveAndWaitContext saves the index and blocks until the dump has
// finished, returning the path the server actually wrote.
//
// A large dump is executed asynchronously: the server acknowledges with
// "OK DUMP_STARTED <path>" and reports progress through DUMP STATUS. DumpSave
// returns that acknowledgement as-is; this variant polls DUMP STATUS until the
// operation reaches COMPLETED (returning the path) or FAILED (returning the
// server's message as a ServerError). A dump small enough to finish inline is
// acknowledged with "OK SAVED <path>" and returns without polling.
//
// The wait is bounded by ClientConfig.DumpSaveTimeoutMs, or by ctx if its
// deadline is sooner. Timing out does not stop the dump — the server keeps
// writing, and DumpStatus still reports the outcome.
func (c *Client) DumpSaveAndWaitContext(ctx context.Context, filepath string) (string, error) {
	response, err := c.DumpSaveContext(ctx, filepath)
	if err != nil {
		return "", err
	}

	const (
		savedPrefix   = "OK SAVED "
		startedPrefix = "OK DUMP_STARTED "
	)
	switch {
	case strings.HasPrefix(response, savedPrefix):
		return strings.TrimSpace(response[len(savedPrefix):]), nil
	case strings.HasPrefix(response, startedPrefix):
		started := strings.TrimSpace(response[len(startedPrefix):])
		return c.waitForDumpSave(ctx, started)
	default:
		return "", &ProtocolError{Message: "unexpected DUMP SAVE response: " + response}
	}
}

// waitForDumpSave polls DUMP STATUS until the asynchronous dump identified by
// startedPath reaches a terminal state.
func (c *Client) waitForDumpSave(ctx context.Context, startedPath string) (string, error) {
	timeoutMs := c.config.DumpSaveTimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = c.config.TimeoutMs
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	ticker := time.NewTicker(dumpStatusPollInterval)
	defer ticker.Stop()
	for {
		status, err := c.DumpStatusContext(waitCtx)
		if err != nil {
			return "", err
		}
		switch status.Status {
		case "COMPLETED":
			if status.ResultFilepath != "" {
				return status.ResultFilepath, nil
			}
			if status.Filepath != "" {
				return status.Filepath, nil
			}
			return startedPath, nil
		case "FAILED":
			message := status.Error
			if message == "" {
				message = "DUMP SAVE failed"
			}
			return "", &ServerError{Message: message}
		}

		select {
		case <-ticker.C:
		case <-waitCtx.Done():
			// Distinguish the wait budget running out from the caller giving up:
			// only the former is a timeout of this client's own making.
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", &TimeoutError{
				Message: fmt.Sprintf("timed out after %dms waiting for DUMP SAVE to complete", timeoutMs),
			}
		}
	}
}

// DumpLoad loads the index from a file.
func (c *Client) DumpLoad(filepath string) error {
	return c.DumpLoadContext(context.Background(), filepath)
}

// DumpLoadContext loads the index from a file with the given context.
func (c *Client) DumpLoadContext(ctx context.Context, filepath string) error {
	cmd := "DUMP LOAD"
	if filepath != "" {
		safe, err := quoteCommandArgument(filepath, "filepath")
		if err != nil {
			return err
		}
		cmd += " " + safe
	}
	_, err := c.sendCommandWithTimeout(ctx, cmd, c.config.DumpLoadTimeoutMs)
	return err
}

// DumpStatus retrieves the status of a dump operation.
func (c *Client) DumpStatus() (*DumpStatus, error) {
	return c.DumpStatusContext(context.Background())
}

// DumpStatusContext retrieves the status of a dump operation with the given context.
func (c *Client) DumpStatusContext(ctx context.Context) (*DumpStatus, error) {
	response, err := c.SendCommandContext(ctx, "DUMP STATUS")
	if err != nil {
		return nil, err
	}
	return parseDumpStatusResponse(response)
}

// DumpVerify verifies the integrity of a dump file.
func (c *Client) DumpVerify(filepath string) (string, error) {
	return c.DumpVerifyContext(context.Background(), filepath)
}

// DumpVerifyContext verifies the integrity of a dump file with the given context.
func (c *Client) DumpVerifyContext(ctx context.Context, filepath string) (string, error) {
	safe, err := quoteCommandArgument(filepath, "filepath")
	if err != nil {
		return "", err
	}
	return c.sendCommandWithTimeout(ctx, "DUMP VERIFY "+safe, c.config.DumpVerifyTimeoutMs)
}

// DumpInfo retrieves metadata of a dump file.
func (c *Client) DumpInfo(filepath string) (string, error) {
	return c.DumpInfoContext(context.Background(), filepath)
}

// DumpInfoContext retrieves metadata of a dump file with the given context.
func (c *Client) DumpInfoContext(ctx context.Context, filepath string) (string, error) {
	safe, err := quoteCommandArgument(filepath, "filepath")
	if err != nil {
		return "", err
	}
	return c.SendCommandContext(ctx, "DUMP INFO "+safe)
}

// CacheStats retrieves cache statistics.
func (c *Client) CacheStats() (*CacheStats, error) {
	return c.CacheStatsContext(context.Background())
}

// CacheStatsContext retrieves cache statistics with the given context.
func (c *Client) CacheStatsContext(ctx context.Context) (*CacheStats, error) {
	response, err := c.SendCommandContext(ctx, "CACHE STATS")
	if err != nil {
		return nil, err
	}
	return parseCacheStatsResponse(response)
}

// CacheClear clears the cache. If table is specified, only that table's cache is cleared.
func (c *Client) CacheClear(table string) error {
	return c.CacheClearContext(context.Background(), table)
}

// CacheClearContext clears the cache with the given context.
func (c *Client) CacheClearContext(ctx context.Context, table string) error {
	cmd := "CACHE CLEAR"
	if table != "" {
		if err := validateTableName(table); err != nil {
			return err
		}
		cmd += " " + table
	}
	_, err := c.SendCommandContext(ctx, cmd)
	return err
}

// CacheEnable enables the cache.
func (c *Client) CacheEnable() error {
	return c.CacheEnableContext(context.Background())
}

// CacheEnableContext enables the cache with the given context.
func (c *Client) CacheEnableContext(ctx context.Context) error {
	_, err := c.SendCommandContext(ctx, "CACHE ENABLE")
	return err
}

// CacheDisable disables the cache.
func (c *Client) CacheDisable() error {
	return c.CacheDisableContext(context.Background())
}

// CacheDisableContext disables the cache with the given context.
func (c *Client) CacheDisableContext(ctx context.Context) error {
	_, err := c.SendCommandContext(ctx, "CACHE DISABLE")
	return err
}

// Optimize optimizes the index. If table is specified, only that table's index is optimized.
func (c *Client) Optimize(table string) error {
	return c.OptimizeContext(context.Background(), table)
}

// OptimizeContext optimizes the index with the given context.
func (c *Client) OptimizeContext(ctx context.Context, table string) error {
	cmd := "OPTIMIZE"
	if table != "" {
		if err := validateTableName(table); err != nil {
			return err
		}
		cmd += " " + table
	}
	_, err := c.sendCommandWithTimeout(ctx, cmd, c.config.OptimizeTimeoutMs)
	return err
}

// SetVariable sets a runtime variable (MygramDB >= v1.7, MySQL-compatible SET).
func (c *Client) SetVariable(name, value string) error {
	return c.SetVariableContext(context.Background(), name, value)
}

// SetVariableContext sets a runtime variable with the given context.
func (c *Client) SetVariableContext(ctx context.Context, name, value string) error {
	if err := validateIdentifier(name, "name"); err != nil {
		return err
	}
	safeValue, err := quoteCommandArgument(value, "value")
	if err != nil {
		return err
	}
	_, err = c.SendCommandContext(ctx, fmt.Sprintf("SET %s = %s", name, safeValue))
	return err
}

// ShowVariables shows runtime variables (MygramDB >= v1.7, MySQL-compatible
// SHOW VARIABLES). An empty likePattern lists every variable; a non-empty
// pattern is matched MySQL-style (e.g. "logging%"). The raw server response is
// returned unparsed.
func (c *Client) ShowVariables(likePattern string) (string, error) {
	return c.ShowVariablesContext(context.Background(), likePattern)
}

// ShowVariablesContext is the context-aware variant of ShowVariables.
func (c *Client) ShowVariablesContext(ctx context.Context, likePattern string) (string, error) {
	if likePattern == "" {
		return c.SendCommandContext(ctx, "SHOW VARIABLES")
	}
	safe, err := quoteCommandArgument(likePattern, "likePattern")
	if err != nil {
		return "", err
	}
	return c.SendCommandContext(ctx, "SHOW VARIABLES LIKE "+safe)
}

// Sync starts an on-demand sync (full reload) of a table (MygramDB >= v1.7).
// The raw server acknowledgement is returned.
func (c *Client) Sync(table string) (string, error) {
	return c.SyncContext(context.Background(), table)
}

// SyncContext is the context-aware variant of Sync.
func (c *Client) SyncContext(ctx context.Context, table string) (string, error) {
	if err := validateTableName(table); err != nil {
		return "", err
	}
	return c.SendCommandContext(ctx, "SYNC "+table)
}

// SyncStatus retrieves the status of in-flight and recent sync operations
// (MygramDB >= v1.7). The raw SYNC_STATUS report is returned unparsed.
func (c *Client) SyncStatus() (string, error) {
	return c.SyncStatusContext(context.Background())
}

// SyncStatusContext is the context-aware variant of SyncStatus.
func (c *Client) SyncStatusContext(ctx context.Context) (string, error) {
	return c.SendCommandContext(ctx, "SYNC STATUS")
}

// SyncStop stops a running sync (MygramDB >= v1.7). With an empty table, every
// in-flight sync is stopped; with a table, only that table's sync is stopped.
func (c *Client) SyncStop(table string) (string, error) {
	return c.SyncStopContext(context.Background(), table)
}

// SyncStopContext is the context-aware variant of SyncStop.
func (c *Client) SyncStopContext(ctx context.Context, table string) (string, error) {
	cmd := "SYNC STOP"
	if table != "" {
		if err := validateTableName(table); err != nil {
			return "", err
		}
		cmd += " " + table
	}
	return c.SendCommandContext(ctx, cmd)
}
