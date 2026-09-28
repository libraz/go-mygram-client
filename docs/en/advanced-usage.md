# Advanced Usage

Production patterns, connection pooling, and the MygramDB v1.6/v1.7 feature
surface.

## Connection Pooling

A single `Client` owns one socket and serializes every command, so it handles
one request at a time. For concurrent workloads use a `Pool`, which lends out
many connections up to a fixed cap and adds transparent reconnection and retry.
It exposes the same command methods as `Client`, so it is a drop-in replacement.

```go
pool := mygram.NewPool(mygram.PoolConfig{
    ClientConfig:    mygram.ClientConfig{Host: "localhost", Port: 11016},
    MaxOpenConns:    16,               // hard cap on total connections
    MaxIdleConns:    8,                // connections kept for reuse
    ConnMaxLifetime: 30 * time.Minute, // recycle connections past this age
})
defer pool.Close()

// Safe to call from many goroutines; each call borrows a connection.
resp, err := pool.Search("articles", "hello", mygram.SearchOptions{Limit: 100})
```

`NewPool` performs no I/O — connections are dialed lazily on first use, so the
pool tolerates a server that is not yet reachable at construction time.

### PoolConfig

| Field | Default | Description |
|-------|---------|-------------|
| `ClientConfig` | — | Embedded config applied to every pooled connection |
| `MaxOpenConns` | `10` | Hard cap on total connections (idle + in use); a borrow that would exceed it blocks |
| `MaxIdleConns` | = `MaxOpenConns` | Idle connections retained for reuse; clamped to `MaxOpenConns` |
| `ConnMaxLifetime` | `0` (no limit) | Max age of a connection; enforced on borrow and by a background reaper |
| `ConnMaxIdleTime` | `0` (no limit) | Max idle time before a connection is discarded; enforced on borrow and by a background reaper |
| `Retry` | see below | Retry policy for safe-to-retry failures |
| `CircuitBreaker` | `nil` (disabled) | Optional `*CircuitBreakerConfig` guarding the command API (see [Circuit Breaker](#circuit-breaker)) |
| `OnEvent` | `nil` | Optional observability callback (see [Pool Events / Observability](#pool-events--observability)) |

There is deliberately no "unlimited" option for `MaxOpenConns`: an explicit cap
is the point.

### Backpressure

When every connection is busy, a borrow blocks until one is returned or the
call's `context` is canceled — a natural backpressure signal. Give latency-
sensitive calls a deadline so a saturated pool sheds load instead of queuing
without bound:

```go
ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
defer cancel()

resp, err := pool.SearchContext(ctx, "articles", "hello", mygram.SearchOptions{Limit: 100})
if errors.Is(err, context.DeadlineExceeded) {
    // Pool saturated — shed this request (e.g. respond 503 with Retry-After).
}
```

### Retry Semantics

The pool retries only failures that are safe to retry:

- **Idempotent commands** (`Search`, `Count`, `Get`, `Facet`, `Info`, …) are
  retried after a connection-level failure at any phase.
- **State-changing commands** (`DumpSave`, `Optimize`, `Sync`, `SetVariable`, …)
  are retried only when the write never reached the server, so the command
  cannot execute twice.
- **Connection establishment failures** (a briefly unavailable server) are
  retried for any command — nothing has been sent yet.
- **Transient server rejections** — `ErrCodeServerLoading` (6028),
  `ErrCodeServerNotReady` (6029) and `ErrCodeServerBusy` (6030) on MygramDB
  v1.10+ — are retried for any command. The server returns these *before*
  executing, so re-issuing cannot apply anything twice. The decision comes from
  the numeric code, never from matching the message text.
- Every other server error, validation errors, and timeouts are never retried.

The retry policy is configurable:

```go
Retry: mygram.RetryPolicy{
    MaxRetries:  2,                     // additional attempts after the first (default 1)
    BaseBackoff: 100 * time.Millisecond, // base for exponential + full-jitter backoff (default 50ms)
},
```

To disable retries entirely, set a negative `MaxRetries` (the zero value selects
the default of 1, so it does not turn retries off):

```go
Retry: mygram.RetryPolicy{MaxRetries: -1}, // never re-issue a command
```

When a time-based limit (`ConnMaxLifetime` or `ConnMaxIdleTime`) is set, a
background reaper sweeps idle connections so a pool that goes quiet after a burst
does not hold expired sockets open until the next borrow. It stops when the pool
is closed.

### Monitoring

`Pool.Stats()` returns a live snapshot:

```go
s := pool.Stats()
fmt.Printf("open=%d idle=%d in-use=%d waits=%d waitTime=%s discarded=%d retries=%d\n",
    s.Open, s.Idle, s.InUse, s.WaitCount, s.WaitDuration, s.Discarded, s.Retries)
```

### Sizing the Pool

Each connection handles at most one in-flight command, so `MaxOpenConns` is the
effective maximum concurrency. Size it with Little's law:

```
MaxOpenConns ≈ targetThroughput(req/s) × p95RTT(s)
```

Then add headroom (about 3×) for RTT variance and spikes. For 500 req/s over a
LAN with a 5 ms p95 RTT, `500 × 0.005 = 2.5`, so **8–16** connections comfortably
absorb the load. Measure your real p95 RTT before committing to a number.

### Administrative Commands on One Connection

Use `WithConn` to run a sequence of commands on a single borrowed connection:

```go
err := pool.WithConn(ctx, func(c *mygram.Client) error {
    if err := c.CacheDisable(); err != nil {
        return err
    }
    return c.Optimize("articles")
})
```

`WithConn` is an escape hatch: it bypasses both the retry policy and the circuit
breaker (it borrows a connection directly rather than going through the retrying,
breaker-guarded command path). The pool applies no retries around `fn`, and an
open breaker does not fast-fail a `WithConn` call. The borrowed connection is
returned to the pool afterwards, or discarded if `fn` reports a connection-level
failure.

### Administrative Authentication

A MygramDB v1.10+ server that sets `api.admin_token` — mandatory when its TCP
listener does not bind to loopback — gates `DUMP`, `SYNC`, `REPLICATION`,
`CONFIG`, `CACHE`, `OPTIMIZE`, `DEBUG`, `SET` and `SHOW VARIABLES` behind
`AUTH`. The grant is per-connection, so it has to be replayed on every socket.

Set `ClientConfig.AdminToken` and the client sends `AUTH` as soon as a
connection is established, including the ones opened by an auto-reconnect and by
a `Pool`:

```go
pool := mygram.NewPool(mygram.PoolConfig{
    ClientConfig: mygram.ClientConfig{
        Host:       "localhost",
        Port:       11016,
        AdminToken: os.Getenv("MYGRAM_ADMIN_TOKEN"),
    },
})
```

A rejected token fails `Connect` (and a pool borrow) with a `*ServerError`
carrying `ErrCodePermissionDenied`, so a misconfigured token surfaces at startup
rather than on the first administrative command.

`Client.Authenticate(token)` covers the case where the token is only known after
connecting. It remembers the token, so a later reconnect re-authorizes too.
Search-only workloads against a server with no admin token need neither.

### Circuit Breaker

Under a sustained outage, retrying every request only piles connect attempts and
timeouts onto a server that is already down. Set `PoolConfig.CircuitBreaker` to a
`*CircuitBreakerConfig` to fail fast instead. After `FailureThreshold` (default
5) consecutive connect/timeout failures the breaker opens; borrows then return a
`*CircuitOpenError` without touching the network. After `ResetTimeout` (default
10s) the breaker admits a single trial (half-open) and closes on success or
re-opens on failure.

The breaker sits outside the retry policy, so an open breaker suppresses retries
too. Only connect and timeout failures count toward tripping it; a `*ServerError`
or `*ValidationError` leaves it closed, since the server is still reachable.

```go
pool := mygram.NewPool(mygram.PoolConfig{
    ClientConfig: mygram.ClientConfig{Host: "localhost", Port: 11016},
    CircuitBreaker: &mygram.CircuitBreakerConfig{
        FailureThreshold: 5,                // consecutive network failures to open (default 5)
        ResetTimeout:     10 * time.Second, // stay open this long before a trial (default 10s)
    },
})
defer pool.Close()

resp, err := pool.Search("articles", "hello", mygram.SearchOptions{Limit: 100})
var open *mygram.CircuitOpenError
if errors.As(err, &open) {
    // Breaker is open — serve a degraded response instead of hammering a downed server.
}
```

A nil `CircuitBreaker` (the default) disables the breaker.

### Pool Events / Observability

Set `PoolConfig.OnEvent` to feed pool activity into any metrics backend. The
callback runs synchronously on the pool's hot path, so it must not block; a panic
in the callback is recovered and ignored. Four `PoolEvent` values are emitted:

| Event | Payload | When |
|-------|---------|------|
| `EventAcquire` | `{"wait_seconds": float64}` | A connection is handed out; the value is time spent waiting (0 if it did not wait) |
| `EventRetry` | `{"attempt": int, "error": string}` | Before a retry's backoff |
| `EventConnectionDiscarded` | (empty) | A connection is dropped after failing validation, expiring, or overflowing the idle limit |
| `EventBreakerStateChange` | `{"state": string}` | The circuit breaker changes state; `state` is one of `"closed"`, `"open"`, `"half_open"` |

```go
pool := mygram.NewPool(mygram.PoolConfig{
    ClientConfig: mygram.ClientConfig{Host: "localhost", Port: 11016},
    OnEvent: func(event mygram.PoolEvent, payload map[string]any) {
        switch event {
        case mygram.EventAcquire:
            recordWait(payload["wait_seconds"].(float64))
        case mygram.EventRetry:
            countRetry(payload["attempt"].(int))
        case mygram.EventBreakerStateChange:
            logBreaker(payload["state"].(string))
        }
    },
})
defer pool.Close()
```

`Pool.Stats()` additionally returns a `PoolStats` snapshot (see
[Monitoring](#monitoring)).

### TCP Keep-Alive

`SO_KEEPALIVE` is enabled on TCP connections by default, so a silently dropped
peer is detected without waiting for the next command's read timeout.
`ClientConfig.TCPKeepAliveIdle` (default 60s) sets the idle period before probing
begins, and `ClientConfig.DisableTCPKeepAlive` turns the override off (leaving the
dialer's built-in default in place). Both are ignored for Unix-socket connections.

```go
client := mygram.NewClientWithConfig(mygram.ClientConfig{
    Host:             "localhost",
    Port:             11016,
    TCPKeepAliveIdle: 30 * time.Second,
})
```

### Auto-Reconnect

`ClientConfig.AutoReconnect` (default `false`) makes a `Client` re-establish a
dropped connection transparently. When enabled, a command reconnects once and
resends only when the socket is found dead *before* the request is written. A
disconnect that occurs *after* the request was sent surfaces as a
`*ConnectionError` without resending, since the command may already have been
applied server-side.

```go
client := mygram.NewClientWithConfig(mygram.ClientConfig{
    Host:          "localhost",
    Port:          11016,
    AutoReconnect: true,
})
```

A `Pool` always enables auto-reconnect on the connections it owns, so a transient
drop is absorbed on the same slot; you do not set it for pooled connections.

## Concurrency

The pool bounds real concurrency to `MaxOpenConns`; fire as many goroutines as
you like and surplus calls block on borrow:

```go
var wg sync.WaitGroup
for _, q := range queries {
    wg.Add(1)
    go func(query string) {
        defer wg.Done()
        resp, err := pool.Search("articles", query, mygram.SearchOptions{Limit: 20})
        // handle resp, err
    }(q)
}
wg.Wait()
```

## Health Checking

Combine `Info` and `GetReplicationStatus` for a monitoring probe:

```go
func healthy(ctx context.Context, c *mygram.Client) (bool, error) {
    info, err := c.InfoContext(ctx)
    if err != nil {
        return false, err
    }
    status, err := c.GetReplicationStatusContext(ctx)
    if err != nil {
        return false, err
    }
    log.Printf("version=%s docs=%d replication=%v", info.Version, info.DocCount, status.Running)
    return true, nil
}
```

## MygramDB v1.6 Features

### BM25 Relevance Scoring

Sort by relevance using the special `_score` sort column (requires
`verify_text: ascii|all` on the server):

```go
resp, _ := client.Search("articles", "machine learning", mygram.SearchOptions{
    SortColumn: "_score",
    SortDesc:   true,
    Limit:      10,
})
```

### Fuzzy Search (Levenshtein)

```go
resp, _ := client.Search("articles", "machne", mygram.SearchOptions{
    Fuzzy: 1, // match within 1 edit (or 2)
    Limit: 10,
})
```

### Highlighting

```go
resp, _ := client.Search("articles", "golang", mygram.SearchOptions{
    Highlight: &mygram.HighlightOptions{
        OpenTag:      "<strong>",
        CloseTag:     "</strong>",
        SnippetLen:   200,
        MaxFragments: 3,
    },
    SortColumn: "_score",
    SortDesc:   true,
})
for _, r := range resp.Results {
    fmt.Println(r.PrimaryKey, r.Snippet)
}
```

A zero-value `&mygram.HighlightOptions{}` enables highlighting with server
defaults (`<em>`/`</em>`, 100 code points, up to 3 fragments).
`SearchWithHighlights` is a shorthand that enables highlighting on any
`SearchOptions`.

### Facets

Aggregate distinct filter-column values with document counts, optionally scoped
to a search result set:

```go
// All distinct statuses:
resp, _ := client.Facet("articles", "status", mygram.FacetOptions{})

// Top categories among documents matching "machine learning":
resp, _ = client.Facet("articles", "category", mygram.FacetOptions{
    Query:   "machine learning",
    Filters: map[string]string{"status": "1"},
    Limit:   10,
})
for _, v := range resp.Results {
    fmt.Printf("%s: %d\n", v.Value, v.Count)
}
```

On MygramDB v1.9+ a facet pages with `Limit` and `Offset`, and `TotalCount`
reports how many distinct values exist before paging — which is what you need to
size the pagination:

```go
page, _ := client.Facet("articles", "category", mygram.FacetOptions{
    Limit:  20,
    Offset: 40,
})
fmt.Printf("showing %d of %d categories\n", len(page.Results), page.TotalCount)
```

An older server reports only the page size, so `TotalCount` equals
`len(Results)` there.

## MygramDB v1.7 Features

### Database-qualified Table Identity

A single MygramDB instance can index same-named tables from different databases.
Reference a table as `database.table` (e.g. `app_db.articles`); a bare name
keeps working on a single-database server.

```go
resp, _ := client.Search("app_db.articles", "hello", mygram.SearchOptions{Limit: 10})

// Build / parse identities explicitly when needed:
id, _ := mygram.QualifyTableIdentity("articles", "app_db")  // "app_db.articles"
db, tbl, _ := mygram.ParseTableIdentity("app_db.articles")  // "app_db", "articles"
```

### Raw Boolean Search

`Search` decomposes into AND/NOT clauses, which cannot express `OR` or grouping.
`SearchRaw` sends a pre-built boolean expression verbatim (unquoted) so the
server tokenizes it and parses `AND` / `OR` / `NOT` / parentheses, including
`OR` groups nested under `AND` (MygramDB v1.8+). Pair it with
`ConvertSearchExpression`:

```go
raw, _ := mygram.ConvertSearchExpression("python OR (ruby rails)")
resp, _ := client.SearchRaw("articles", raw, mygram.SearchRawOptions{Limit: 50})

// Snippet-returning variant:
resp, _ = client.SearchRawWithHighlights("articles", raw, mygram.SearchRawOptions{})
```

### Runtime Variables

```go
_ = client.SetVariable("logging.level", "debug")
out, _ := client.ShowVariables("logging%") // raw server table; "" lists all
```

### On-demand Sync

```go
_, _ = client.Sync("articles")     // start a full reload of one table
status, _ := client.SyncStatus()   // raw SYNC_STATUS report
_, _ = client.SyncStop("articles") // stop one table's sync ("" stops all)
```

## MygramDB v1.9 Features

### Comparison Filters

`Filters` expresses equality only. `FilterConditions` adds the comparison
operators the server accepts — `=`, `!=`, `>`, `>=`, `<`, `<=` — and both forms
may be combined on one request. Map entries are sent in sorted key order and
the conditions in slice order, so identical options always produce an identical
command (which matters for the server's query cache).

```go
resp, _ := client.Search("articles", "golang", mygram.SearchOptions{
    Filters: map[string]string{"status": "1"},
    FilterConditions: []mygram.FilterCondition{
        {Key: "views", Op: mygram.FilterGreaterThanOrEqual, Value: "100"},
        {Key: "created_at", Op: mygram.FilterLessThan, Value: "2026-01-01"},
    },
})
```

`FilterConditions` is available on `SearchOptions`, `CountOptions` and
`FacetOptions`.

### Boolean Query Mode

`Search` treats its text as literal user input: it quotes the token, so a search
for `AND` or `LIMIT` looks for those words rather than starting a clause. Set
`QueryMode: mygram.QueryBoolean` to send the text verbatim instead, so the
server's expression parser reads `AND` / `OR` / `NOT` and parentheses as
operators — while keeping the typed options that `SearchRaw` does not accept:

```go
resp, _ := client.Search("articles", "alpha AND (beta OR gamma)", mygram.SearchOptions{
    QueryMode:  mygram.QueryBoolean,
    SortColumn: "created_at",
    SortDesc:   true,
    Highlight:  &mygram.HighlightOptions{},
    Limit:      50,
})
```

`SearchRaw` remains the compact expression-only entry point; use it when no
filters, sorting or highlighting are involved.

### Primary-key Ordering

With no `SortColumn` the client omits the `SORT` clause and the server orders by
primary key descending. `SortDesc` only chooses the direction of an explicit
sort column, so primary-key *ascending* order needs `SortPrimaryKey`, which
emits the protocol's column-less `SORT ASC` / `SORT DESC` shorthand:

```go
resp, _ := client.Search("articles", "golang", mygram.SearchOptions{
    SortPrimaryKey: mygram.PrimaryKeySortAsc,
})
```

Naming both a sort column and a primary-key ordering is rejected client-side: a
request carries a single `SORT` clause.

## MygramDB v1.10 Features

### Readiness

`INFO` reports whether the server has finished building its state, evaluated
from the same inputs as the HTTP readiness endpoint:

```go
info, _ := client.Info()
if !info.Ready() {
    // info.Readiness is "not_ready"; info.DataInitialized says whether the
    // initial index is built
}
```

Both fields are absent on older servers, where `Readiness` is empty and
`Ready()` is false — so gate traffic on it only where MygramDB v1.10+ is known
to be deployed.

### Error Codes

Every `ERROR` frame carries a numeric code, exposed as `ServerError.Code`:

```go
if _, err := client.Search("ghosts", "x", mygram.SearchOptions{}); err != nil {
    if code, ok := mygram.ServerErrorCode(err); ok && code == mygram.ErrCodeTableNotFound {
        // the table is not in the server's catalog
    }
}
```

The pool uses the same codes to decide what to retry (see
[Retry Semantics](#retry-semantics)). An uncoded frame from a pre-v1.10 server
reports `ErrCodeNone` and is never retried, because its message text is not a
reliable signal.

## Persistence and Cache Administration

```go
// Snapshot the index to disk and restore it later.
result, _ := client.DumpSave("/var/lib/mygram/snapshot.dump")
_ = client.DumpLoad("/var/lib/mygram/snapshot.dump")

// Inspect an in-progress dump.
st, _ := client.DumpStatus()

// Rebuild a table's index.
_ = client.Optimize("articles")

// Query cache controls.
stats, _ := client.CacheStats()
_ = client.CacheClear("articles")
_ = client.CacheDisable()
_ = client.CacheEnable()
```

These commands change server state, so under a `Pool` they are retried only when
the request never reached the server (see [Retry Semantics](#retry-semantics)).

### Long-running Commands

`DUMP LOAD`, `DUMP VERIFY` and `OPTIMIZE` traverse or rebuild the whole index
and routinely outlast an ordinary request. They use their own budgets —
`DumpLoadTimeoutMs`, `DumpVerifyTimeoutMs` and `OptimizeTimeoutMs`, each 300 s
by default — instead of the 5 s `TimeoutMs`, which would abandon a command the
server is still executing. Set one equal to `TimeoutMs` to opt out; a shorter
context deadline still wins.

A dump big enough to run asynchronously is acknowledged with
`OK DUMP_STARTED <path>` while the write continues in the background.
`DumpSave` returns that acknowledgement verbatim, so polling `DumpStatus` is the
caller's job. `DumpSaveAndWait` does the polling and returns the path the server
actually wrote:

```go
path, err := client.DumpSaveAndWait("/var/lib/mygram/snapshot.dump")
```

The wait is bounded by `DumpSaveTimeoutMs` (300 s by default) or by a shorter
context deadline. Timing out does not stop the dump — the server keeps writing,
and `DumpStatus` still reports the outcome.
