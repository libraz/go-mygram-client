# API Reference

Complete reference for `go-mygram-client`. The package is imported as `mygram`:

```go
import mygram "github.com/libraz/go-mygram-client"
```

Every command method has a `Context` variant that takes a `context.Context` as
its first argument (for example `Search` / `SearchContext`). The context bounds
that single call and can cancel it. Only the plain forms are listed below; the
`Context` variant of each shares its signature with `ctx context.Context`
prepended.

Both `Client` and `Pool` expose the same command surface, so a `Pool` is a
drop-in replacement for a `Client` in request-handling code. See
[Advanced Usage](advanced-usage.md#connection-pooling) for the pool.

## Client

### Constructors

```go
func NewClient(addr string) *Client
func NewClientWithConfig(config ClientConfig) *Client
```

`NewClient` parses `addr` as `host:port`, or treats it as a Unix socket path if
it is not in that form. `NewClientWithConfig` fills any zero-valued
[`ClientConfig`](#clientconfig) field with its default. Neither performs I/O.

### Connection

```go
func (c *Client) Connect() error
func (c *Client) Close() error
func (c *Client) IsConnected() bool
```

`Connect` dials the server (honoring `TimeoutMs`); `Close` shuts the connection
down and is safe to defer. A `Client` owns one connection and serializes
commands, so it handles one request at a time; it is safe to call from multiple
goroutines but they will not run concurrently.

### Search Methods

```go
func (c *Client) Search(table, query string, opts SearchOptions) (*SearchResponse, error)
func (c *Client) SearchWithHighlights(table, query string, opts SearchOptions) (*SearchResponse, error)
func (c *Client) SearchRaw(table, rawQuery string, opts SearchRawOptions) (*SearchResponse, error)
func (c *Client) SearchRawWithHighlights(table, rawQuery string, opts SearchRawOptions) (*SearchResponse, error)
func (c *Client) Count(table, query string, opts CountOptions) (*CountResponse, error)
func (c *Client) Facet(table, column string, opts FacetOptions) (*FacetResponse, error)
```

#### Search

Searches `table` for documents matching `query`. `table` may be a bare name or,
on a MygramDB v1.7+ multi-database server, a `database.table` identity. The
query and options compile to a `SEARCH` command as described in
[Search Expressions](search-expression.md#how-the-search-command-is-built).

```go
resp, err := client.Search("articles", "golang tutorial", mygram.SearchOptions{
    Limit:      50,
    AndTerms:   []string{"beginner"},
    NotTerms:   []string{"advanced"},
    Filters:    map[string]string{"status": "1"},
    SortColumn: "created_at",
    SortDesc:   true,
})
```

`SearchWithHighlights` is the same call with the `HIGHLIGHT` clause enabled,
returning snippets in `SearchResult.Snippet`.

#### SearchRaw

Sends a pre-built boolean expression verbatim, unquoted (MygramDB v1.8+), so the
server tokenizes it and its parser interprets `AND` / `OR` / `NOT` / parentheses,
including `OR` groups nested under `AND`. Pair it with
[`ConvertSearchExpression`](#search-expression-functions) to preserve `OR` and
grouping that `Search`'s AND/NOT decomposition cannot express.
`SearchRawWithHighlights` adds the `HIGHLIGHT` clause.

```go
raw, _ := mygram.ConvertSearchExpression("python OR (ruby AND rails)")
resp, _ := client.SearchRaw("articles", raw, mygram.SearchRawOptions{Limit: 50})
```

#### Count

Counts matching documents without returning their IDs.

```go
cnt, _ := client.Count("articles", "machine learning", mygram.CountOptions{
    Filters: map[string]string{"status": "1"},
})
fmt.Println(cnt.Count)
```

#### Facet

Aggregates distinct values of a filter column with per-value document counts,
optionally scoped to a query (MygramDB v1.6+). See
[Advanced Usage](advanced-usage.md#facets).

### Document Methods

```go
func (c *Client) Get(table, primaryKey string) (*Document, error)
```

Retrieves a single document by primary key. Returns a `*ProtocolError` (or
`*ServerError`) when the document does not exist.

### Server Information

```go
func (c *Client) Info() (*ServerInfo, error)
func (c *Client) GetConfig() (string, error)
```

`Info` returns version, uptime, document count, and the table list. `GetConfig`
returns the server configuration as a raw YAML string.

### Replication

```go
func (c *Client) GetReplicationStatus() (*ReplicationStatus, error)
func (c *Client) StartReplication() error
func (c *Client) StopReplication() error
```

Query and control MySQL binlog replication.

### Debug

```go
func (c *Client) EnableDebug() error
func (c *Client) DisableDebug() error
```

When debug mode is enabled, search and count responses carry a populated
`Debug *DebugInfo` field with per-query timing and candidate counts.

### Runtime Variables (v1.7+)

```go
func (c *Client) SetVariable(name, value string) error
func (c *Client) ShowVariables(likePattern string) (string, error)
```

`SetVariable` issues a MySQL-compatible `SET`; values containing whitespace are
quoted automatically. `ShowVariables` returns the raw `SHOW VARIABLES [LIKE …]`
table (pass `""` to list everything).

### On-demand Sync (v1.7+)

```go
func (c *Client) Sync(table string) (string, error)
func (c *Client) SyncStatus() (string, error)
func (c *Client) SyncStop(table string) (string, error)
```

`Sync` starts a full reload of one table (bare or `database.table`).
`SyncStatus` returns the raw `SYNC_STATUS` report. `SyncStop` stops one table's
sync, or every in-flight sync when `table` is `""`.

### Persistence & Maintenance

```go
func (c *Client) DumpSave(filepath string) (string, error)
func (c *Client) DumpLoad(filepath string) error
func (c *Client) DumpStatus() (*DumpStatus, error)
func (c *Client) DumpVerify(filepath string) (string, error)
func (c *Client) DumpInfo(filepath string) (string, error)
func (c *Client) Optimize(table string) error

func (c *Client) CacheStats() (*CacheStats, error)
func (c *Client) CacheClear(table string) error
func (c *Client) CacheEnable() error
func (c *Client) CacheDisable() error
```

Snapshot/restore the index (`Dump*`), rebuild a table's index (`Optimize`), and
inspect or control the query cache (`Cache*`).

### Raw Commands

```go
func (c *Client) SendCommand(command string) (string, error)
```

Sends an arbitrary protocol command and returns the raw server response — an
escape hatch for commands the typed API does not cover.

## Pool

A `Pool` lends out many connections up to a fixed cap and adds transparent
reconnection and retry. It exposes every command method that `Client` does with
identical signatures, plus:

```go
func NewPool(cfg PoolConfig) *Pool
func (p *Pool) Close() error
func (p *Pool) Stats() PoolStats
func (p *Pool) WithConn(ctx context.Context, fn func(c *Client) error) error
```

`WithConn` borrows one connection for the duration of `fn` — use it to run a
sequence of commands on the same connection, or to control retry semantics
yourself (the pool applies no retries around `fn`). See
[Advanced Usage](advanced-usage.md#connection-pooling) for configuration and
sizing.

## Types

### ClientConfig

```go
type ClientConfig struct {
    Host            string // default "127.0.0.1"
    Port            int    // default 11016
    SocketPath      string // Unix socket path; takes priority over TCP
    TimeoutMs       int    // command timeout in ms (default 5000)
    RecvBufferSize  int    // receive buffer size in bytes (default 65536)
    MaxQueryLength  int    // max query expression length (default 128; 0 = unlimited)
    MaxResponseSize int    // max response size in bytes (default 0 = unlimited)

    AutoReconnect       bool          // reconnect + resend once on a pre-write drop (default false)
    DisableTCPKeepAlive bool          // turn off the SO_KEEPALIVE idle override (default off; keep-alive on)
    TCPKeepAliveIdle    time.Duration // idle before keep-alive probing begins (default 60s)
}
```

`AutoReconnect`, `DisableTCPKeepAlive`, and `TCPKeepAliveIdle` are covered in
[Advanced Usage](advanced-usage.md#tcp-keep-alive). A `Pool` always enables
`AutoReconnect` on the connections it owns.

### CircuitBreakerConfig

```go
type CircuitBreakerConfig struct {
    FailureThreshold int           // consecutive network failures that open the breaker (default 5)
    ResetTimeout     time.Duration // how long the breaker stays open before a trial (default 10s)
}
```

Set `PoolConfig.CircuitBreaker` to a `*CircuitBreakerConfig` to enable a circuit
breaker around the pool's command API; a nil pointer disables it. Only
`*ConnectionError` and `*TimeoutError` count toward tripping it. See
[Advanced Usage](advanced-usage.md#circuit-breaker).

### PoolEvent

```go
type PoolEvent string

const (
    EventAcquire             PoolEvent = "acquire"              // payload {"wait_seconds": float64}
    EventConnectionDiscarded PoolEvent = "connection_discarded" // empty payload
    EventRetry               PoolEvent = "retry"               // payload {"attempt": int, "error": string}
    EventBreakerStateChange  PoolEvent = "breaker_state_change" // payload {"state": string}
)
```

Delivered to `PoolConfig.OnEvent func(event PoolEvent, payload map[string]any)`.
The callback runs synchronously on the pool's hot path and must not block; a
panic in it is recovered and ignored. See
[Advanced Usage](advanced-usage.md#pool-events--observability).

### SearchOptions

```go
type SearchOptions struct {
    Limit      int               // max results (default 1000)
    Offset     int               // pagination offset (default 0)
    AndTerms   []string          // additional required terms
    NotTerms   []string          // excluded terms
    Filters    map[string]string // column filter conditions
    SortColumn string            // sort column; "_score" for BM25 (default primary key)
    SortDesc   bool              // descending order
    Fuzzy      int               // Levenshtein distance 0/1/2 (v1.6+)
    Highlight  *HighlightOptions // enable HIGHLIGHT when non-nil (v1.6+)
}
```

### HighlightOptions

```go
type HighlightOptions struct {
    OpenTag      string // tag before each match (default "<em>")
    CloseTag     string // tag after each match (default "</em>"); set with OpenTag
    SnippetLen   int    // code points per fragment, 1..10000 (0 = server default 100)
    MaxFragments int    // fragments joined by ellipsis, 1..100 (0 = server default 3)
}
```

A zero-value `&HighlightOptions{}` enables highlighting with server defaults.

### SearchRawOptions

```go
type SearchRawOptions struct {
    Limit     int               // max results (0 = server default)
    Offset    int               // pagination offset
    Highlight *HighlightOptions // enable HIGHLIGHT when non-nil
}
```

### CountOptions

```go
type CountOptions struct {
    AndTerms []string
    NotTerms []string
    Filters  map[string]string
}
```

### FacetOptions

```go
type FacetOptions struct {
    Query    string            // scope aggregation to matching documents
    AndTerms []string
    NotTerms []string
    Filters  map[string]string
    Limit    int               // bound the number of (value, count) pairs (0 = no limit)
}
```

### Response Types

```go
type SearchResult struct {
    PrimaryKey string
    Snippet    string // populated only when Highlight was set
}

type SearchResponse struct {
    Results    []SearchResult
    TotalCount int
    Debug      *DebugInfo // populated only in debug mode
}

type CountResponse struct {
    Count int
    Debug *DebugInfo
}

type Document struct {
    PrimaryKey string
    Fields     map[string]string
}

type FacetValue struct {
    Value string
    Count int64
}

type FacetResponse struct {
    Results []FacetValue // ordered by count descending
}

type ServerInfo struct {
    Version           string
    UptimeSeconds     int
    TotalRequests     int
    ActiveConnections int
    IndexSizeBytes    int
    DocCount          int
    Tables            []string
}

type ReplicationStatus struct {
    Running         bool
    GTID            string
    StatusStr       string
    ProcessedEvents int
    QueueSize       int
}

type DebugInfo struct {
    QueryTimeMs  float64
    IndexTimeMs  float64
    FilterTimeMs float64
    Terms        int
    Ngrams       int
    Candidates   int
    Final        int
    // ... additional counters (intersection/not/filter stages, cache, sort)
}
```

`DumpStatus` and `CacheStats` carry the parsed fields of `DUMP STATUS` and
`CACHE STATS` respectively; see their godoc for the full field list.

## Error Types

All command methods return one of these concrete types, matchable with
`errors.As`:

| Type | Meaning |
|------|---------|
| `*ConnectionError` | Failed to connect, or a connection-level failure while sending/receiving |
| `*ProtocolError` | Server returned a malformed or unexpected response |
| `*TimeoutError` | The command exceeded its deadline. Satisfies `net.Error` (`Timeout()` is `true`); wraps `context.DeadlineExceeded` when the caller's context deadline was the cause |
| `*ValidationError` | Client-side input validation failed (control characters, over-length query) |
| `*ServerError` | Server replied with an explicit `ERROR` |
| `*CircuitOpenError` | The pool's circuit breaker is open (or half-open with a trial in flight); raised without touching the network so the caller fails fast. Not classified as a network failure, so it does not itself drive the breaker |

```go
type ConnectionError struct {
    Message string
    Err     error // unwrappable with errors.Unwrap / errors.Is
}
```

`ConnectionError` implements `Unwrap`, so `errors.Is(err, io.EOF)` and similar
checks reach the underlying cause.

## Search Expression Functions

```go
func SimplifySearchExpression(expression string) (*SimplifiedExpression, error)
func ConvertSearchExpression(expression string) (string, error)
func ParseSearchExpression(expression string) (*SearchExpression, error)
func HasComplexExpression(expr *SearchExpression) bool
func ToQueryString(expr *SearchExpression) string
```

See [Search Expressions](search-expression.md) for details and examples.

## Table Identity Helpers (v1.7+)

```go
func QualifyTableIdentity(table, database string) (string, error)
func ParseTableIdentity(identity string) (database, table string, err error)
```

`QualifyTableIdentity` builds a `database.table` identity (or returns the bare
table when `database` is empty); `ParseTableIdentity` splits one back into its
parts. Both validate the identifiers and reject whitespace and control
characters.

```go
id, _ := mygram.QualifyTableIdentity("articles", "app_db") // "app_db.articles"
db, tbl, _ := mygram.ParseTableIdentity("app_db.articles") // "app_db", "articles"
```
