package mygram

import "time"

// ClientConfig holds connection settings for the MygramDB server.
type ClientConfig struct {
	// Host is the server hostname or IP address. Default is "127.0.0.1".
	Host string

	// Port is the server port number. Default is 11016.
	Port int

	// SocketPath is the Unix domain socket path. When specified, it takes priority over TCP.
	SocketPath string

	// TimeoutMs is the command timeout in milliseconds. Default is 5000.
	//
	// This field is milliseconds (an int) for parity with the other language
	// clients. The pool-level limits (PoolConfig.ConnMaxLifetime and friends)
	// are time.Duration; mind the difference when configuring both.
	TimeoutMs int

	// ConnectTimeoutMs bounds the dial (and the AUTH handshake that follows it)
	// separately from a command. 0 falls back to TimeoutMs, which is the
	// historical behavior. Raise it when the server is reachable only over a
	// slow path but commands themselves must still fail fast.
	ConnectTimeoutMs int

	// DumpSaveTimeoutMs bounds DumpSaveAndWait: the whole poll loop, from the
	// DUMP SAVE request to the terminal DUMP STATUS, must finish inside it. It
	// does not apply to DumpSave, which returns the server's immediate
	// acknowledgement without waiting. 0 selects the default (300000).
	//
	// This and the three timeouts below cover the commands that rebuild or
	// traverse the whole index. They are separate from TimeoutMs because an
	// ordinary request timeout would abandon a command the server is still
	// executing; set one equal to TimeoutMs to opt out. A caller-supplied
	// context deadline still wins when it is the shorter of the two.
	DumpSaveTimeoutMs int

	// DumpLoadTimeoutMs bounds a DUMP LOAD command, which restores an entire
	// index. 0 selects the default (300000).
	DumpLoadTimeoutMs int

	// DumpVerifyTimeoutMs bounds a DUMP VERIFY command, which streams and
	// checksums every section of a dump. 0 selects the default (300000).
	DumpVerifyTimeoutMs int

	// OptimizeTimeoutMs bounds an OPTIMIZE command, which rebuilds posting
	// lists. 0 selects the default (300000).
	OptimizeTimeoutMs int

	// RecvBufferSize is the receive buffer size. Default is 65536.
	RecvBufferSize int

	// MaxQueryLength is the maximum length of a query expression. 0 means unlimited. Default is 128.
	MaxQueryLength int

	// MaxResponseSize is the maximum accepted response size in bytes. When a
	// response exceeds it, the read is aborted with a ProtocolError and the
	// connection is closed (its stream position is no longer known). 0 means
	// unlimited, which is the default for backward compatibility. A bounded
	// value (for example 64<<20) is recommended to guard against a runaway or
	// malformed response exhausting memory.
	MaxResponseSize int

	// AutoReconnect transparently re-establishes a dropped connection. When
	// enabled, SendCommandContext reconnects once and resends the command if
	// the socket is found dead *before* the request is written. A disconnect
	// that occurs *after* the request was sent is surfaced as a ConnectionError
	// without resending, since the command may already have been applied
	// server-side. Default is false (legacy behavior). A Pool always enables it
	// on the connections it owns so a transient drop is absorbed on the slot.
	AutoReconnect bool

	// DisableTCPKeepAlive turns off the client's SO_KEEPALIVE idle override on
	// TCP connections. The zero value keeps keep-alive enabled with the
	// TCPKeepAliveIdle period, so a silently dropped peer is detected without
	// waiting for the next command's read timeout. Set it to true to leave the
	// dialer's built-in keep-alive default in place instead. Ignored for
	// Unix-socket connections. Defaulting keep-alive on this way keeps the
	// behavior identical whether a client is built with NewClient or with
	// NewClientWithConfig/NewPool.
	DisableTCPKeepAlive bool

	// TCPKeepAliveIdle is the idle period before keep-alive probing begins.
	// Applies unless DisableTCPKeepAlive is set. 0 selects the default (60s).
	TCPKeepAliveIdle time.Duration

	// AdminToken authenticates administrative commands on a server that sets
	// api.admin_token (MygramDB >= v1.10). When non-empty, the client sends
	// AUTH as soon as a connection is established — including the connections
	// opened by a reconnect or by a Pool — so administrative commands are
	// authorized without a per-command handshake. A rejected token surfaces as
	// a ServerError from Connect. Empty (the default) sends no AUTH, which is
	// correct for a server with no admin token and for search-only workloads.
	AdminToken string
}

// QueryMode selects how the search text of a SEARCH or COUNT is interpreted
// (MygramDB >= v1.9).
type QueryMode uint8

const (
	// QueryLiteral treats the text as literal user input: it is quoted on the
	// wire, so reserved words such as AND or LIMIT stay data. This is the zero
	// value, so search semantics do not change unless a caller opts in.
	QueryLiteral QueryMode = iota

	// QueryBoolean sends the text verbatim so the server's AST parser reads
	// its AND / OR / NOT operators and parentheses. Use it to combine an
	// expression such as `alpha AND (beta OR gamma)` with typed filters,
	// sorting, fuzzy matching or highlights; SearchRaw remains the compact
	// expression-only entry point.
	QueryBoolean
)

// FilterOp is the comparison operator of a typed filter condition
// (MygramDB >= v1.9).
type FilterOp uint8

const (
	// FilterEqual is the zero value, so a FilterCondition without an explicit
	// operator behaves like the equality entries of the Filters map.
	FilterEqual FilterOp = iota
	FilterNotEqual
	FilterGreaterThan
	FilterGreaterThanOrEqual
	FilterLessThan
	FilterLessThanOrEqual
)

// wire returns the operator's protocol token.
func (o FilterOp) wire() string {
	switch o {
	case FilterNotEqual:
		return "!="
	case FilterGreaterThan:
		return ">"
	case FilterGreaterThanOrEqual:
		return ">="
	case FilterLessThan:
		return "<"
	case FilterLessThanOrEqual:
		return "<="
	default:
		return "="
	}
}

// valid reports whether the operator is one of the defined constants.
func (o FilterOp) valid() bool {
	return o <= FilterLessThanOrEqual
}

// FilterCondition is a filter with an explicit comparison operator
// (MygramDB >= v1.9). Use it where the Filters map cannot express the
// predicate; the two forms may be combined on one request.
type FilterCondition struct {
	// Key is the filter column name. It is sent unquoted, so it must be a
	// valid identifier.
	Key string

	// Op is the comparison operator. The zero value is FilterEqual.
	Op FilterOp

	// Value is the compared value. It is quoted on the wire as needed.
	Value string
}

// PrimaryKeySort selects an explicit primary-key ordering for a search that
// names no sort column. The protocol's shorthand `SORT ASC` / `SORT DESC`
// orders by primary key; omitting the clause entirely leaves the server on its
// own default, which is descending.
type PrimaryKeySort uint8

const (
	// PrimaryKeySortDefault omits the SORT clause, leaving the ordering to the
	// server (primary key, descending). It is the zero value, so the wire form
	// of an existing request is unchanged.
	PrimaryKeySortDefault PrimaryKeySort = iota

	// PrimaryKeySortAsc requests primary key ascending. This ordering is not
	// reachable any other way: SortDesc only chooses the direction of an
	// explicit SortColumn.
	PrimaryKeySortAsc

	// PrimaryKeySortDesc requests primary key descending explicitly. It selects
	// the same ordering as PrimaryKeySortDefault but states it on the wire.
	PrimaryKeySortDesc
)

// wire returns the direction token of the SORT shorthand.
func (s PrimaryKeySort) wire() string {
	if s == PrimaryKeySortAsc {
		return "ASC"
	}
	return "DESC"
}

// valid reports whether the value is one of the defined constants.
func (s PrimaryKeySort) valid() bool {
	return s <= PrimaryKeySortDesc
}

// defaultLongOperationTimeoutMs is the fallback command timeout for the
// operations that rebuild or traverse the whole index. They routinely outlast
// the ordinary request timeout, so applying TimeoutMs to them would abandon a
// command the server is still executing.
const defaultLongOperationTimeoutMs = 300000

// DefaultConfig returns the default configuration.
func DefaultConfig() ClientConfig {
	return ClientConfig{
		Host:                "127.0.0.1",
		Port:                11016,
		TimeoutMs:           5000,
		DumpSaveTimeoutMs:   defaultLongOperationTimeoutMs,
		DumpLoadTimeoutMs:   defaultLongOperationTimeoutMs,
		DumpVerifyTimeoutMs: defaultLongOperationTimeoutMs,
		OptimizeTimeoutMs:   defaultLongOperationTimeoutMs,
		RecvBufferSize:      65536,
		MaxQueryLength:      128,
		TCPKeepAliveIdle:    60 * time.Second,
	}
}

// SearchOptions holds options for a search request.
type SearchOptions struct {
	// Limit is the maximum number of results to return. Default is 1000.
	Limit int

	// Offset is the number of results to skip. Default is 0.
	Offset int

	// AndTerms is a list of additional keywords combined with AND.
	AndTerms []string

	// NotTerms is a list of keywords to exclude with NOT.
	NotTerms []string

	// Filters is a map of column filter conditions (key=value). Entries are
	// sent in sorted key order so one set of options always produces the same
	// command.
	Filters map[string]string

	// FilterConditions holds filters with an explicit comparison operator
	// (MygramDB >= v1.9). They are appended after the Filters entries, in
	// order.
	FilterConditions []FilterCondition

	// QueryMode selects literal (default) or boolean interpretation of the
	// search text. Requires MygramDB >= v1.9 for a boolean query combined with
	// filters, sorting, fuzzy matching or highlights.
	QueryMode QueryMode

	// SortColumn is the column name to sort by.
	//
	// Use the special value "_score" to sort by BM25 relevance score
	// (requires verify_text=ascii|all on the server, MygramDB >= v1.6).
	SortColumn string

	// SortDesc indicates descending sort order when true. It applies to
	// SortColumn; with no sort column, use SortPrimaryKey instead.
	SortDesc bool

	// SortPrimaryKey orders by primary key without naming a column. It is only
	// meaningful when SortColumn is empty — setting both is rejected, since the
	// protocol carries a single SORT clause. The zero value omits the clause,
	// which leaves the server on its default primary-key descending order.
	SortPrimaryKey PrimaryKeySort

	// Fuzzy enables fuzzy (Levenshtein) matching with the given edit
	// distance. Valid values are 0 (disabled), 1 and 2. Requires
	// MygramDB >= v1.6.
	Fuzzy int

	// Highlight, when non-nil, enables the HIGHLIGHT clause so that
	// matching snippets are returned alongside each primary key.
	// Requires verify_text=ascii|all on the server and MygramDB >= v1.6.
	Highlight *HighlightOptions
}

// HighlightOptions configures the HIGHLIGHT clause introduced in
// MygramDB v1.6. A zero-value HighlightOptions requests default
// highlighting ("<em>"/"</em>" tags, 100 code point snippets, up to
// 3 fragments).
type HighlightOptions struct {
	// OpenTag is the tag inserted before each matched term. When empty
	// the server default ("<em>") is used.
	OpenTag string

	// CloseTag is the tag inserted after each matched term. When empty
	// the server default ("</em>") is used.
	//
	// OpenTag and CloseTag must be set (or unset) together.
	CloseTag string

	// SnippetLen is the maximum number of code points per snippet
	// fragment. Valid range is 1..10000. 0 means "use server default"
	// (100).
	SnippetLen int

	// MaxFragments is the maximum number of snippet fragments joined by
	// ellipsis. Valid range is 1..100. 0 means "use server default" (3).
	MaxFragments int
}

// SearchRawOptions holds options for a raw search request that sends a
// pre-built boolean expression. Introduced in MygramDB v1.7; since v1.8 the
// expression is sent verbatim (unquoted) so the server's AST parser
// interprets AND/OR/NOT and grouping (see Client.SearchRaw). Unlike
// SearchOptions, a raw search exposes only pagination and highlight
// controls — AND/NOT/FILTER refinements belong inside the expression itself.
type SearchRawOptions struct {
	// Limit is the maximum number of results to return. 0 means the server
	// default.
	Limit int

	// Offset is the number of results to skip. Default is 0.
	Offset int

	// Highlight, when non-nil, enables the HIGHLIGHT clause so that matching
	// snippets are returned alongside each primary key.
	Highlight *HighlightOptions
}

// CountOptions holds options for a count request.
type CountOptions struct {
	// AndTerms is a list of additional keywords combined with AND.
	AndTerms []string

	// NotTerms is a list of keywords to exclude with NOT.
	NotTerms []string

	// Filters is a map of column filter conditions (key=value). Entries are
	// sent in sorted key order.
	Filters map[string]string

	// FilterConditions holds filters with an explicit comparison operator
	// (MygramDB >= v1.9), appended after the Filters entries.
	FilterConditions []FilterCondition

	// QueryMode selects literal (default) or boolean interpretation of the
	// count text.
	QueryMode QueryMode
}

// SearchResult represents a single search result.
type SearchResult struct {
	// PrimaryKey is the document's primary key.
	PrimaryKey string

	// Snippet contains the highlighted text fragment for this result,
	// populated only when SearchOptions.Highlight was set (MygramDB
	// >= v1.6). Empty otherwise.
	Snippet string
}

// SearchResponse represents a search response.
type SearchResponse struct {
	// Results is the list of search results.
	Results []SearchResult

	// TotalCount is the total number of matches after filtering.
	TotalCount int

	// Debug contains debug information (only available when debug mode is enabled).
	Debug *DebugInfo
}

// CountResponse represents a count response.
type CountResponse struct {
	// Count is the number of matched documents.
	Count int

	// Debug contains debug information (only available when debug mode is enabled).
	Debug *DebugInfo
}

// Document represents a document retrieved by GET.
type Document struct {
	// PrimaryKey is the document's primary key.
	PrimaryKey string

	// Fields is a map of filter column key-value pairs.
	Fields map[string]string
}

// ServerInfo represents server information.
type ServerInfo struct {
	// Version is the server version.
	Version string

	// UptimeSeconds is the number of seconds since server startup.
	UptimeSeconds int

	// TotalRequests is the total number of processed requests.
	TotalRequests int

	// ActiveConnections is the current number of client connections.
	ActiveConnections int

	// IndexSizeBytes is the memory used by the index in bytes.
	IndexSizeBytes int

	// DocCount is the total number of documents in the index.
	DocCount int

	// Tables is the list of table names.
	Tables []string

	// DataInitialized reports whether the server has finished building its
	// initial index state (MygramDB >= v1.10). It is false on older servers,
	// which do not report the field.
	DataInitialized bool

	// Readiness is the server's readiness verdict, "ready" or "not_ready"
	// (MygramDB >= v1.10), evaluated from the same inputs as the HTTP
	// readiness endpoint. It is empty on older servers.
	Readiness string
}

// Ready reports whether the server declared itself ready to serve requests.
// It is false on a server that predates the readiness field, so use it to gate
// traffic only when the deployment is known to run MygramDB >= v1.10.
func (i *ServerInfo) Ready() bool { return i.Readiness == "ready" }

// ReplicationStatus represents the replication state.
type ReplicationStatus struct {
	// Running is true when replication is active.
	Running bool

	// GTID is the current GTID position.
	GTID string

	// StatusStr is the raw response string.
	StatusStr string

	// ProcessedEvents is the total number of binlog events processed since
	// startup. Populated only by the multi-line REPLICATION STATUS response.
	ProcessedEvents int

	// QueueSize is the number of events currently pending in the
	// replication apply queue. Populated only by the multi-line response.
	QueueSize int
}

// DebugInfo holds query metrics from debug mode.
type DebugInfo struct {
	QueryTimeMs       float64
	IndexTimeMs       float64
	FilterTimeMs      float64
	Terms             int
	Ngrams            int
	Candidates        int
	AfterIntersection int
	AfterNot          int
	AfterFilters      int
	Final             int
	Optimization      string
	Sort              string
	Cache             string
	CacheAgeMs        float64
	CacheSavedMs      float64
	Limit             int
	Offset            int
}

// DumpStatus represents the status of a dump operation.
type DumpStatus struct {
	SaveInProgress  bool
	LoadInProgress  bool
	Status          string
	Filepath        string
	TablesProcessed int
	TablesTotal     int
	CurrentTable    string
	ElapsedSeconds  float64
	ResultFilepath  string
	Error           string
}

// CacheStats represents cache statistics.
//
// A field the server does not report keeps its zero value, so the struct also
// describes an older server: the counters below the memory block were added
// over the v1.9 and v1.10 lines.
type CacheStats struct {
	// Enabled reports whether the query cache is currently serving.
	Enabled bool

	// TotalQueries is the number of cache lookups, so Hits+Misses.
	TotalQueries int

	Hits    int
	Misses  int
	HitRate float64

	// CurrentEntries is the number of cached results held right now.
	CurrentEntries int

	// MemoryBytes is the cached payload size (the server's
	// current_memory_bytes).
	MemoryBytes int

	// InvalidationIndexMemoryBytes and InvalidationQueueMemoryBytes are the
	// reverse indexes that map a row event to the entries it invalidates, and
	// the pending-invalidation queue. Both are charged to the cache budget.
	InvalidationIndexMemoryBytes int
	InvalidationQueueMemoryBytes int

	// AccountedMemoryBytes is the total charged against the cache memory
	// budget: the payloads plus the invalidation structures.
	AccountedMemoryBytes int

	// Evictions and TTLExpirations count entries dropped to stay within the
	// capacity budget and entries dropped for age.
	Evictions      int
	TTLExpirations int

	// The rejection counters are four independent reasons a result was not
	// admitted, not a total and its parts: RejectionCount is specifically the
	// count of queries cheaper than the server's min_query_cost_ms threshold,
	// which is why a healthy cache reports it while the other three stay zero.
	RejectionCount        int // below the min_query_cost_ms threshold
	RejectionOversize     int // the entry alone exceeds max_memory_bytes
	RejectionMemoryBudget int // the shared budget could not retain it
	RejectionDuplicate    int // the cache key was already present

	// StaleEntryRemovals counts entries discarded because a staleness check
	// failed on lookup; DecompressionFailures counts entries dropped because
	// their payload could not be restored; StaleLRUEntries counts LRU nodes
	// found without a live entry behind them.
	StaleEntryRemovals    int
	DecompressionFailures int
	StaleLRUEntries       int

	// InvalidationsImmediate and InvalidationsDeferred split invalidations by
	// whether the row event applied them inline or queued them;
	// InvalidationsBatches counts the queue drains.
	InvalidationsImmediate int
	InvalidationsDeferred  int
	InvalidationsBatches   int

	// AvgHitTimeMs and AvgMissTimeMs are nil when the server omitted them,
	// which it does while the corresponding counter is still zero. A pointer
	// keeps "not reported" distinct from a measured 0.
	AvgHitTimeMs  *float64
	AvgMissTimeMs *float64

	// TotalTimeSavedMs is the cumulative query time avoided by cache hits.
	TotalTimeSavedMs float64
}

// FacetOptions holds options for a FACET request. Introduced in
// MygramDB v1.6.
type FacetOptions struct {
	// Query, when non-empty, scopes the facet aggregation to documents
	// matching the given search text (same semantics as SEARCH).
	Query string

	// AndTerms is a list of additional keywords combined with AND.
	AndTerms []string

	// NotTerms is a list of keywords to exclude with NOT.
	NotTerms []string

	// Filters is a map of column filter conditions (key=value). Entries are
	// sent in sorted key order.
	Filters map[string]string

	// FilterConditions holds filters with an explicit comparison operator
	// (MygramDB >= v1.9), appended after the Filters entries.
	FilterConditions []FilterCondition

	// Limit bounds the number of returned (value, count) pairs.
	// 0 means "no limit".
	Limit int

	// Offset is the number of distinct values to skip, so Limit and Offset
	// together page through a facet (MygramDB >= v1.9). Use
	// FacetResponse.TotalCount to size the pagination.
	Offset int
}

// FacetValue is a single aggregated (value, count) pair returned by
// the FACET command.
type FacetValue struct {
	// Value is the facet column's distinct value as a string.
	Value string

	// Count is the number of documents that have this value.
	Count int64
}

// FacetResponse is the response to a FACET command.
type FacetResponse struct {
	// Results is the list of (value, count) pairs ordered by count
	// descending. With FacetOptions.Offset or Limit set, it is one page.
	Results []FacetValue

	// TotalCount is the number of distinct values before OFFSET and LIMIT
	// (MygramDB >= v1.9). A server that predates paginated facets reports only
	// the page size, so this falls back to len(Results).
	TotalCount int64
}

// SimplifiedExpression represents a simplified search expression.
type SimplifiedExpression struct {
	// MainTerm is the main search keyword.
	MainTerm string

	// AndTerms is a list of additional keywords combined with AND.
	AndTerms []string

	// NotTerms is a list of keywords to exclude with NOT.
	NotTerms []string
}
