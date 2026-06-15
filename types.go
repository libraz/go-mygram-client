package mygram

// ClientConfig holds connection settings for the MygramDB server.
type ClientConfig struct {
	// Host is the server hostname or IP address. Default is "127.0.0.1".
	Host string

	// Port is the server port number. Default is 11016.
	Port int

	// SocketPath is the Unix domain socket path. When specified, it takes priority over TCP.
	SocketPath string

	// TimeoutMs is the command timeout in milliseconds. Default is 5000.
	TimeoutMs int

	// RecvBufferSize is the receive buffer size. Default is 65536.
	RecvBufferSize int

	// MaxQueryLength is the maximum length of a query expression. 0 means unlimited. Default is 128.
	MaxQueryLength int
}

// DefaultConfig returns the default configuration.
func DefaultConfig() ClientConfig {
	return ClientConfig{
		Host:           "127.0.0.1",
		Port:           11016,
		TimeoutMs:      5000,
		RecvBufferSize: 65536,
		MaxQueryLength: 128,
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

	// Filters is a map of column filter conditions (key=value).
	Filters map[string]string

	// SortColumn is the column name to sort by.
	//
	// Use the special value "_score" to sort by BM25 relevance score
	// (requires verify_text=ascii|all on the server, MygramDB >= v1.6).
	SortColumn string

	// SortDesc indicates descending sort order when true.
	SortDesc bool

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
// pre-built boolean expression as a single token. Introduced in MygramDB
// v1.7. Unlike SearchOptions, a raw search exposes only pagination and
// highlight controls — AND/NOT/FILTER refinements belong inside the
// expression itself.
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

	// Filters is a map of column filter conditions (key=value).
	Filters map[string]string
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
}

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
type CacheStats struct {
	Enabled        bool
	Hits           int
	Misses         int
	HitRate        float64
	CurrentEntries int
	MemoryBytes    int
	Evictions      int
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

	// Filters is a map of column filter conditions (key=value).
	Filters map[string]string

	// Limit bounds the number of returned (value, count) pairs.
	// 0 means "no limit".
	Limit int
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
	// descending.
	Results []FacetValue
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
