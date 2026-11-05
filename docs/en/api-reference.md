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
An IPv6 literal host is written bracketed, e.g. `"[::1]:11016"` for `NewClient`
or `Host: "::1"` for `NewClientWithConfig` — `dial` re-brackets it with
`net.JoinHostPort` before connecting either way.

### Connection

```go
func (c *Client) Connect() error
func (c *Client) Close() error
func (c *Client) IsConnected() bool
func (c *Client) Authenticate(token string) error
```

`Connect` dials the server (honoring `TimeoutMs`); `Close` shuts the connection
down and is safe to defer. A `Client` owns one connection and serializes
commands, so it handles one request at a time; it is safe to call from multiple
goroutines but they will not run concurrently.

`Authenticate` sends `AUTH` and remembers the token, so a reconnect
re-authorizes before the next command. Setting
[`ClientConfig.AdminToken`](#clientconfig) does the same without an explicit
call and is the only way to authenticate a `Pool`'s connections. See
[Advanced Usage](advanced-usage.md#administrative-authentication).

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

Searches `table` for documents matching `query`. `table` may be a bare name
or, on a multi-database server (MygramDB 1.7 or later), a `database.table`
identity. The query and options compile to a `SEARCH` command as described in
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

`Filters` expresses equality only. For a comparison, use `FilterConditions`,
and set `QueryMode: mygram.QueryBoolean` to combine an expression with typed
options that `SearchRaw` does not accept (MygramDB 1.9 or later):

```go
resp, err := client.Search("articles", "alpha AND (beta OR gamma)", mygram.SearchOptions{
    QueryMode: mygram.QueryBoolean,
    FilterConditions: []mygram.FilterCondition{
        {Key: "views", Op: mygram.FilterGreaterThanOrEqual, Value: "100"},
        {Key: "status", Op: mygram.FilterNotEqual, Value: "3"},
    },
    SortColumn: "created_at",
    SortDesc:   true,
})
```

#### SearchRaw

Sends a pre-built boolean expression verbatim, unquoted (MygramDB 1.8 or
later), so the server tokenizes it and its parser interprets `AND` / `OR` /
`NOT` / parentheses, including `OR` groups nested under `AND`. Pair it with
[`ConvertSearchExpression`](#search-expression-functions) to preserve `OR` and
grouping that `Search`'s AND/NOT decomposition cannot express.
`SearchRawWithHighlights` adds the `HIGHLIGHT` clause.

```go
raw, _ := mygram.ConvertSearchExpression("python OR (ruby rails)")
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
optionally scoped to a query. `Limit` and `Offset` page through the values,
and `FacetResponse.TotalCount` reports how many distinct values exist before
paging (MygramDB 1.9 or later). See
[Advanced Usage](advanced-usage.md#facets).

### Document Methods

```go
func (c *Client) Get(table, primaryKey string) (*Document, error)
```

Retrieves a single document by primary key. The key is escaped reversibly, so
any key the server can return can be sent back to it — including one holding
spaces or quotes. `Document.PrimaryKey`, `Document.Fields`' string-typed
values, and `SearchResult.PrimaryKey` are decoded the same way on the way
back in, so a key or field value containing a space survives a `Search`/`Get`
round trip intact. Returns a `*ProtocolError` (or `*ServerError`) when the
document does not exist.

### Server Information

```go
func (c *Client) Info() (*ServerInfo, error)
func (c *Client) GetConfig() (string, error)
```

`Info` returns version, uptime, document count, the table list, and — on a
server running MygramDB 1.10 or later — the readiness verdict
(`ServerInfo.DataInitialized`, `ServerInfo.Readiness`, `ServerInfo.Ready()`).
`GetConfig` returns the server configuration as a raw YAML string.

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
`Debug *DebugInfo` field with per-query timing and candidate counts. The client
follows the server's `OK DEBUG_ON` / `OK DEBUG_OFF` acknowledgement, so a
`DEBUG ON` sent through `SendCommand` is tracked the same way, and the flag is
cleared when the connection is replaced.

### Runtime Variables

Requires MygramDB 1.7 or later.

```go
func (c *Client) SetVariable(name, value string) error
func (c *Client) ShowVariables(likePattern string) (string, error)
```

`SetVariable` issues a MySQL-compatible `SET`; values containing whitespace are
quoted automatically. `ShowVariables` returns the raw `SHOW VARIABLES [LIKE …]`
table (pass `""` to list everything).

### On-demand Sync

Requires MygramDB 1.7 or later.

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
func (c *Client) DumpSaveAndWait(filepath string) (string, error)
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

A dump large enough to run asynchronously is acknowledged with
`OK DUMP_STARTED <path>`, and the write continues in the background.
`DumpSave` returns that acknowledgement as-is, leaving the polling to the
caller. `DumpSaveAndWait` polls `DUMP STATUS` until the dump completes and
returns the path the server actually wrote, or the failure as a `*ServerError`;
its budget is `ClientConfig.DumpSaveTimeoutMs`. A dump small enough to finish
inline is acknowledged with `OK SAVED <path>` and both calls return at once.

### Raw Commands

```go
func (c *Client) SendCommand(command string) (string, error)
```

Sends an arbitrary protocol command and returns the raw server response — an
escape hatch for commands the typed API does not cover. `OPTIMIZE`,
`DUMP LOAD` and `DUMP VERIFY` get their long-operation timeouts here too,
whatever their case or spacing.

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

    ConnectTimeoutMs    int // dial + AUTH timeout in ms (0 = use TimeoutMs)
    DumpSaveTimeoutMs   int // DumpSaveAndWait poll budget in ms (default 300000)
    DumpLoadTimeoutMs   int // DUMP LOAD timeout in ms (default 300000)
    DumpVerifyTimeoutMs int // DUMP VERIFY timeout in ms (default 300000)
    OptimizeTimeoutMs   int // OPTIMIZE timeout in ms (default 300000)

    RecvBufferSize  int    // receive buffer size in bytes (default 65536)
    MaxQueryLength  int    // max query expression length (default 128; 0 = unlimited)
    MaxResponseSize int    // max response size in bytes (default 0 = unlimited)

    AutoReconnect       bool          // reconnect + resend once on a pre-write drop (default false)
    DisableTCPKeepAlive bool          // turn off the SO_KEEPALIVE idle override (default off; keep-alive on)
    TCPKeepAliveIdle    time.Duration // idle before keep-alive probing begins (default 60s)
    AdminToken          string        // AUTH token replayed on every connection (MygramDB 1.10+; default none)
}
```

`AutoReconnect`, `DisableTCPKeepAlive`, and `TCPKeepAliveIdle` are covered in
[Advanced Usage](advanced-usage.md#tcp-keep-alive). A `Pool` always enables
`AutoReconnect` on the connections it owns.

`DumpLoadTimeoutMs`, `DumpVerifyTimeoutMs` and `OptimizeTimeoutMs` apply to
commands that rebuild or traverse the whole index; without them the 5 second
`TimeoutMs` would abandon a command the server is still executing. Set one equal
to `TimeoutMs` to opt out. A context deadline passed to the `*Context` variant
still wins when it is the shorter of the two.

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
    SortDesc   bool              // descending order; applies to SortColumn
    Fuzzy      int               // Levenshtein distance 0/1/2
    Highlight  *HighlightOptions // enable HIGHLIGHT when non-nil

    FilterConditions []FilterCondition // filters with a comparison operator (MygramDB 1.9+)
    QueryMode        QueryMode         // QueryLiteral (default) or QueryBoolean (MygramDB 1.9+)
    SortPrimaryKey   PrimaryKeySort    // primary-key ordering with no sort column
}
```

`Filters` entries are sent in sorted key order, then `FilterConditions` in
slice order, so one set of options always produces the same command.

### PrimaryKeySort

```go
type PrimaryKeySort uint8

const (
    PrimaryKeySortDefault PrimaryKeySort = iota // omit SORT; server orders by primary key descending
    PrimaryKeySortAsc                           // SORT ASC  — primary key ascending
    PrimaryKeySortDesc                          // SORT DESC — primary key descending, stated explicitly
)
```

`SortDesc` only chooses the direction of an explicit `SortColumn`, so primary-key
*ascending* order is reachable only through `SortPrimaryKey`. Setting both
`SortColumn` and a non-default `SortPrimaryKey` is a `*ValidationError`: a
request carries a single `SORT` clause.

```go
resp, err := client.Search("articles", "golang", mygram.SearchOptions{
    SortPrimaryKey: mygram.PrimaryKeySortAsc,
    Limit:          20,
})
```

### QueryMode and FilterCondition

```go
type QueryMode uint8

const (
    QueryLiteral QueryMode = iota // quote the text; reserved words stay data (default)
    QueryBoolean                  // send verbatim; AND / OR / NOT and parentheses are operators
)

type FilterOp uint8

const (
    FilterEqual FilterOp = iota // "="  (zero value)
    FilterNotEqual              // "!="
    FilterGreaterThan           // ">"
    FilterGreaterThanOrEqual    // ">="
    FilterLessThan              // "<"
    FilterLessThanOrEqual       // "<="
)

type FilterCondition struct {
    Key   string   // filter column; sent unquoted, so it must be an identifier
    Op    FilterOp // default FilterEqual
    Value string
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
`OpenTag` and `CloseTag` are quoted on the wire automatically (the same
decision `Search` applies to terms and filter values), so a tag containing
whitespace or matching a clause keyword still reaches the server as the
literal tag it was configured as.

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
    AndTerms         []string
    NotTerms         []string
    Filters          map[string]string
    FilterConditions []FilterCondition // comparison filters (MygramDB 1.9+)
    QueryMode        QueryMode         // QueryLiteral (default) or QueryBoolean (MygramDB 1.9+)
}
```

### FacetOptions

```go
type FacetOptions struct {
    Query            string            // scope aggregation to matching documents
    AndTerms         []string
    NotTerms         []string
    Filters          map[string]string
    FilterConditions []FilterCondition // comparison filters (MygramDB 1.9+)
    Limit            int               // bound the number of (value, count) pairs (0 = no limit)
    Offset           int               // distinct values to skip (MygramDB 1.9+)
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
    Results    []FacetValue // ordered by count descending; one page when Limit/Offset are set
    TotalCount int64        // distinct values before OFFSET and LIMIT (MygramDB 1.9+)
}

type ServerInfo struct {
    Version           string
    UptimeSeconds     int
    TotalRequests     int
    ActiveConnections int
    IndexSizeBytes    int
    DocCount          int
    Tables            []string
    DataInitialized   bool   // initial index state built (MygramDB 1.10+)
    Readiness         string // "ready" or "not_ready" (MygramDB 1.10+); empty on older servers
}

func (i *ServerInfo) Ready() bool // Readiness == "ready"

type ReplicationStatus struct {
    Running                 bool
    GTID                    string
    StatusStr               string
    ProcessedEvents         int
    QueueSize               int
    State                   string    // "running" | "stopped" | "failed" | "not_configured" (MygramDB 1.10+)
    CRCErrors               int       // binlog events whose checksum did not verify (MygramDB 1.10+)
    SchemaIncompatible      bool      // stopped because MySQL no longer matches the config (MygramDB 1.10+)
    LastErrorCode           ErrorCode // last failure, ErrCodeNone when none (MygramDB 1.10+)
    LastError               string    // message for LastErrorCode (MygramDB 1.10+)
    LastAppliedUnixtime     int64     // when the last event was applied (MygramDB 1.10+)
    SecondsSinceLastApplied int64     // replication lag; -1 until an event is applied (MygramDB 1.10+)
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
`CacheStats` covers every key a MygramDB 1.10 or later server reports — hit
counters, the memory split between payloads, the invalidation index and the
invalidation queue, admission rejections by reason, and invalidation totals. A
key an older server omits keeps its zero value. `AvgHitTimeMs` and
`AvgMissTimeMs` are `*float64` because the server omits them entirely while
the matching counter is zero, which a plain `0` could not distinguish.

## Error Types

All command methods return one of these concrete types, matchable with
`errors.As`:

| Type | Meaning |
|------|---------|
| `*ConnectionError` | Failed to connect, or a connection-level failure while sending/receiving |
| `*ProtocolError` | Server returned a malformed or unexpected response |
| `*TimeoutError` | The command exceeded its deadline. Satisfies `net.Error` (`Timeout()` is `true`); wraps `context.DeadlineExceeded` when the caller's context deadline was the cause |
| `*ValidationError` | Client-side input validation failed (control characters, protocol delimiters in an identifier, over-length query) |
| `*ServerError` | Server replied with an explicit `ERROR`, carrying a numeric `Code` on MygramDB 1.10 or later |
| `*CircuitOpenError` | The pool's circuit breaker is open (or half-open with a trial in flight); raised without touching the network so the caller fails fast. Not classified as a network failure, so it does not itself drive the breaker |

```go
type ConnectionError struct {
    Message string
    Err     error // unwrappable with errors.Unwrap / errors.Is
}
```

`ConnectionError` implements `Unwrap`, so `errors.Is(err, io.EOF)` and similar
checks reach the underlying cause.

### Server error codes

On MygramDB 1.10 or later, every `ERROR` frame is `ERROR <code> <message>`,
and the code is parsed into `ServerError.Code`. An older server sends an
uncoded frame, which is reported as `ErrCodeNone` — so "no code" is
distinguishable from a real code rather than guessed at from the message text.

```go
type ErrorCode uint16

type ServerError struct {
    Message string    // the frame's message, without the code prefix
    Code    ErrorCode // ErrCodeNone on a server older than MygramDB 1.10
}

func (e *ServerError) Retryable() bool
func (c ErrorCode) Retryable() bool
func ServerErrorCode(err error) (ErrorCode, bool)
```

`Retryable` is true only for the transient codes — `ErrCodeServerLoading`
(6028), `ErrCodeServerNotReady` (6029) and `ErrCodeServerBusy` (6030) — which
the server returns *before* executing the command, so re-issuing is safe even
for a state-changing one. A `Pool` retries exactly these under its normal
backoff; every other code is deterministic and is surfaced immediately.

Named constants cover the codes a client can act on (`ErrCodeTableNotFound`,
`ErrCodePermissionDenied`, `ErrCodeQuerySyntaxError`, …). Any `uint16` may
appear on the wire, so treat an unrecognized value as an opaque server error.

```go
if code, ok := mygram.ServerErrorCode(err); ok && code == mygram.ErrCodeTableNotFound {
    // the table is not in the server's catalog
}
```

## Search Expression Functions

```go
func SimplifySearchExpression(expression string) (*SimplifiedExpression, error)
func ConvertSearchExpression(expression string) (string, error)
func ParseSearchExpression(expression string) (*SearchExpression, error)
func HasComplexExpression(expr *SearchExpression) bool
func ToQueryString(expr *SearchExpression) string
```

See [Search Expressions](search-expression.md) for details and examples.

## Table Identity Helpers

Requires MygramDB 1.7 or later.

```go
func QualifyTableIdentity(table, database string) (string, error)
func ParseTableIdentity(identity string) (database, table string, err error)
```

`QualifyTableIdentity` builds a `database.table` identity (or returns the bare
table when `database` is empty); `ParseTableIdentity` splits one back into its
parts. Both validate the identifiers and reject whitespace (ASCII and
Unicode `White_Space`) and control characters. Table, column, filter-key and
`SetVariable` names are validated the same way.

```go
id, _ := mygram.QualifyTableIdentity("articles", "app_db") // "app_db.articles"
db, tbl, _ := mygram.ParseTableIdentity("app_db.articles") // "app_db", "articles"
```
