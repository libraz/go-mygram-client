# go-mygram-client

[![CI](https://img.shields.io/github/actions/workflow/status/libraz/go-mygram-client/ci.yml?branch=main&label=CI)](https://github.com/libraz/go-mygram-client/actions)
[![Go Reference](https://pkg.go.dev/badge/github.com/libraz/go-mygram-client.svg)](https://pkg.go.dev/github.com/libraz/go-mygram-client)
[![Go](https://img.shields.io/badge/go-%E2%89%A51.26-00ADD8?logo=go)](https://go.dev/)
[![codecov](https://codecov.io/gh/libraz/go-mygram-client/branch/main/graph/badge.svg)](https://codecov.io/gh/libraz/go-mygram-client)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
[![golangci-lint](https://img.shields.io/badge/lint-golangci--lint-00ACD7?logo=go)](https://golangci-lint.run/)
[![Zero Dependencies](https://img.shields.io/badge/dependencies-0-brightgreen)](https://pkg.go.dev/github.com/libraz/go-mygram-client?tab=imports)

Go client library for [MygramDB](https://github.com/libraz/mygram-db/) — a high-performance in-memory full-text search engine with MySQL replication support.

Compatible with MygramDB v1.10.2 (administrative `AUTH`, numeric error codes with
code-driven retries, readiness in `INFO`, the full `CACHE STATS` surface,
separate budgets for long-running commands), v1.9 (typed comparison filters,
boolean query mode, paginated facets, primary-key ordering), v1.8 (unquoted raw boolean transport with
OR groups nested under AND, date-only filter values), v1.7 (database-qualified
tables, raw boolean search, runtime variables, on-demand sync), and v1.6 (BM25
scoring, HIGHLIGHT, FUZZY, FACET). Older servers stay supported: features the
server does not implement are simply not requested, and an uncoded `ERROR` frame
is reported without a code.

## MygramDB v1.10.2

- **One quoting rule for every wire-bound string** — search terms, filter
  values, AND/NOT terms, highlight tags and primary keys are all quoted by
  the same decision: empty, a clause keyword (case-insensitive), ASCII or
  Unicode whitespace, a control character, a quote, a backslash, or a
  parenthesis. Callers always pass raw, unquoted text; a highlight tag that
  needs quoting is quoted rather than rejected. See
  [Search Expressions](docs/en/search-expression.md#notes) for the exact rule.
- **Quoted primary keys and `GET` string values are decoded** on the way
  back, so a `SEARCH` or `GET` result whose primary key or a string field
  contains a space round-trips intact instead of being split.
- **IPv6 servers are reachable** by a literal address (`"[::1]:11016"`) or a
  hostname that resolves only to an AAAA record.
- **Multi-line `HIGHLIGHT`, `DEBUG` and `SHOW VARIABLES` responses are read
  completely**, even when a chunk boundary lands exactly at the header line
  a single-line reply would also end at.
- **`SimplifySearchExpression` rejects an `OR` or grouped expression**
  instead of silently misrepresenting it as a single main term. Use
  `ConvertSearchExpression` with `SearchRaw` for that case.

## Overview

MygramDB answers full-text queries from memory instead of an on-disk MySQL FULLTEXT index. How much that gains depends on the query and the dataset; the [published benchmarks](https://mygramdb.libraz.net/benchmarks) give the numbers together with the conditions they were measured under. This client communicates via MygramDB's TCP text protocol (memcached-style) with zero external dependencies.

| | MySQL FULLTEXT | MygramDB |
|---|---|---|
| **Search Speed** | Baseline | [Measured](https://mygramdb.libraz.net/benchmarks) |
| **Storage** | On-disk | In-memory |
| **Replication** | — | MySQL binlog |
| **Protocol** | MySQL | TCP (memcached-style) |

### Features

- **Zero Dependencies** — Standard library only
- **Context Support** — All methods have `context.Context` variants
- **Search Expression Parser** — Web-style search syntax (+required, -excluded, "phrase", OR, grouping)
- **Full Protocol Support** — All MygramDB commands (SEARCH, COUNT, GET, INFO, etc.)
- **Input Validation** — Built-in protection against control character and protocol-delimiter injection
- **Typed Errors** — Server `ERROR` frames carry a numeric `ErrorCode`; transient codes drive pool retries
- **Thread Safe** — Safe for concurrent use
- **Connection Pooling** — Bounded `Pool` with reconnection and retry for high-throughput workloads
- **Resilience** — Pool circuit breaker, observability events, TCP keep-alive, and client-level auto-reconnect
- **Administrative AUTH** — A configured token is replayed on every connection, including reconnects and pooled sockets

## Installation

```bash
go get github.com/libraz/go-mygram-client
```

## Quick Start

```go
package main

import (
	"fmt"
	"log"

	mygram "github.com/libraz/go-mygram-client"
)

func main() {
	client := mygram.NewClient("localhost:11016")
	defer client.Close()

	if err := client.Connect(); err != nil {
		log.Fatal(err)
	}

	// Search
	resp, err := client.Search("articles", "hello", mygram.SearchOptions{
		Limit:      100,
		SortColumn: "created_at",
		SortDesc:   true,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Found %d results\n", resp.TotalCount)

	// Count
	count, err := client.Count("articles", "technology", mygram.CountOptions{})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Count: %d\n", count.Count)

	// Get document by ID
	doc, err := client.Get("articles", "12345")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Doc: %s %v\n", doc.PrimaryKey, doc.Fields)
}
```

## Connection Pooling

A single `Client` wraps one connection and serializes commands, so it handles
one request at a time. For concurrent workloads use a `Pool`, which lends out
many connections up to a fixed cap and adds transparent reconnection and retry.
It exposes the same command methods as `Client`, so it is a drop-in replacement.

```go
pool := mygram.NewPool(mygram.PoolConfig{
	ClientConfig:    mygram.ClientConfig{Host: "localhost", Port: 11016},
	MaxOpenConns:    16,               // hard cap on total connections
	MaxIdleConns:    8,                // connections kept for reuse
	ConnMaxLifetime: 30 * time.Minute, // recycle old connections
})
defer pool.Close()

// Safe to call from many goroutines; each call borrows a connection.
resp, err := pool.Search("articles", "hello", mygram.SearchOptions{Limit: 100})
```

When every connection is busy, a call blocks until one is returned or its
`context` is canceled — a natural backpressure signal. See
[Connection Pooling](docs/en/advanced-usage.md#connection-pooling) for sizing,
retry semantics, and monitoring.

## Documentation

- [Getting Started](docs/en/getting-started.md) — install, configuration, and error handling
- [Search Expressions](docs/en/search-expression.md) — parse web-style search input
- [API Reference](docs/en/api-reference.md) — every method, option, and type
- [Advanced Usage](docs/en/advanced-usage.md) — connection pooling and resilience (circuit breaker, observability events, TCP keep-alive, auto-reconnect), administrative `AUTH`, error codes, and the search features added through v1.10 (BM25 `_score`, fuzzy, highlighting, paginated facets, comparison filters, boolean query mode, multi-database, runtime variables, on-demand sync)

Japanese docs live under [`docs/ja/`](docs/ja/). Package-level API docs are also
on [pkg.go.dev](https://pkg.go.dev/github.com/libraz/go-mygram-client).

## Development

```bash
make test       # Run unit tests
make test-race  # Run unit tests with race detector
make test-e2e   # Run e2e tests against a live MygramDB server
make test-e2e-docker # Spin up MySQL + MygramDB (docker compose), run e2e, tear down
make lint       # Run golangci-lint
make fmt        # Format code
make coverage   # Generate coverage report
```

### End-to-end tests

`make test-e2e` exercises every public method against a running MygramDB
instance. Tests are gated by the `e2e` build tag, so the default
`go test ./...` (and CI) never connect to a server.

```bash
# Default: connect to 127.0.0.1:11016
make test-e2e

# Override the endpoint
MYGRAM_HOST=db.example.com MYGRAM_PORT=11016 make test-e2e

# Authenticate administrative commands (mandatory from v1.10 on a
# non-loopback bind)
MYGRAM_ADMIN_TOKEN=... make test-e2e

# Skip side-effect heavy suites
MYGRAM_E2E_SKIP_DUMP=1 MYGRAM_E2E_SKIP_REPLICATION=1 make test-e2e
```

If the server is unreachable, every e2e test is skipped (not failed).
Suites that mutate global server state (replication start/stop, cache
toggle, dump round-trip) restore the original state on exit.

#### Self-contained docker-compose stack

`make test-e2e-docker` brings up a throwaway **MySQL + MygramDB** stack (the
server is pulled from `ghcr.io/libraz/mygram-db`), seeds it with a deterministic
dataset, runs the e2e suite with `MYGRAM_E2E_SEEDED=1`, then tears everything
down. No local server is required. See [`e2e/docker/`](e2e/docker/README.md) for
knobs (`MYGRAMDB_VERSION`, `MYGRAM_ADMIN_TOKEN`, `KEEP_UP`, ports). The
`TestE2ESeeded` block asserts exact result sets (database-qualified identity,
phrase + required-filter visibility, Japanese ngram matching, `SearchRaw` OR,
facet counts and pagination, comparison filters, boolean query mode).

## License

[MIT](LICENSE)
