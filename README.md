# go-mygram-client

[![CI](https://img.shields.io/github/actions/workflow/status/libraz/go-mygram-client/ci.yml?branch=main&label=CI)](https://github.com/libraz/go-mygram-client/actions)
[![Go Reference](https://pkg.go.dev/badge/github.com/libraz/go-mygram-client.svg)](https://pkg.go.dev/github.com/libraz/go-mygram-client)
[![Go](https://img.shields.io/badge/go-%E2%89%A51.23-00ADD8?logo=go)](https://go.dev/)
[![codecov](https://codecov.io/gh/libraz/go-mygram-client/branch/main/graph/badge.svg)](https://codecov.io/gh/libraz/go-mygram-client)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
[![golangci-lint](https://img.shields.io/badge/lint-golangci--lint-00ACD7?logo=go)](https://golangci-lint.run/)
[![Zero Dependencies](https://img.shields.io/badge/dependencies-0-brightgreen)](https://pkg.go.dev/github.com/libraz/go-mygram-client?tab=imports)

Go client library for [MygramDB](https://github.com/libraz/mygram-db/) — a high-performance in-memory full-text search engine with MySQL replication support.

Compatible with MygramDB v1.7 (database-qualified tables, raw boolean search,
runtime variables, on-demand sync) and v1.6 (BM25 scoring, HIGHLIGHT, FUZZY,
FACET).

## Overview

MygramDB provides **25-200x faster** full-text search than MySQL FULLTEXT. This client communicates via MygramDB's TCP text protocol (memcached-style) with zero external dependencies.

| | MySQL FULLTEXT | MygramDB |
|---|---|---|
| **Search Speed** | Baseline | 25-200x faster |
| **Storage** | On-disk | In-memory |
| **Replication** | — | MySQL binlog |
| **Protocol** | MySQL | TCP (memcached-style) |

### Features

- **Zero Dependencies** — Standard library only
- **Context Support** — All methods have `context.Context` variants
- **Search Expression Parser** — Web-style search syntax (+required, -excluded, "phrase", OR, grouping)
- **Full Protocol Support** — All MygramDB commands (SEARCH, COUNT, GET, INFO, etc.)
- **Input Validation** — Built-in protection against control character injection
- **Thread Safe** — Safe for concurrent use

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

## Search Expressions

Parse web-style search queries into structured search parameters:

```go
// Space = AND, - = NOT, "" = phrase, OR = OR, () = grouping
expr, err := mygram.SimplifySearchExpression("hello world -spam")
// expr = &SimplifiedExpression{MainTerm: "hello", AndTerms: ["world"], NotTerms: ["spam"]}

resp, err := client.Search("articles", expr.MainTerm, mygram.SearchOptions{
	AndTerms:   expr.AndTerms,
	NotTerms:   expr.NotTerms,
	Limit:      100,
	Offset:     50,
	Filters:    map[string]string{"status": "published", "lang": "en"},
	SortColumn: "created_at",
	SortDesc:   true,
})
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
// Match within 1 edit (default) or 2 edits.
resp, _ := client.Search("articles", "machne", mygram.SearchOptions{
    Fuzzy: 1,
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
    Limit:      10,
})

for _, r := range resp.Results {
    fmt.Println(r.PrimaryKey, r.Snippet)
}
```

A zero-value `&HighlightOptions{}` enables highlighting with server defaults
(`<em>`/`</em>`, 100 code points, up to 3 fragments).

### Facets

Aggregate distinct filter column values with document counts. Optionally scope
the aggregation to a search result set:

```go
// All distinct statuses:
resp, _ := client.Facet("articles", "status", mygram.FacetOptions{})

// Top categories among documents matching "machine learning":
resp, _ := client.Facet("articles", "category", mygram.FacetOptions{
    Query:   "machine learning",
    Filters: map[string]string{"status": "1"},
    Limit:   10,
})

for _, v := range resp.Results {
    fmt.Printf("%s: %d\n", v.Value, v.Count)
}
```

## MygramDB v1.7 Features

### Database-qualified table identity

A single MygramDB instance can index same-named tables from different databases.
Reference a table as `database.table` (e.g. `app_db.articles`); on a
single-database server a bare name keeps working unchanged.

```go
// Build / parse identities explicitly when needed:
id, _ := mygram.QualifyTableIdentity("articles", "app_db") // "app_db.articles"
db, table, _ := mygram.ParseTableIdentity("app_db.articles") // "app_db", "articles"

// Any table argument accepts a qualified identity:
resp, _ := client.Search("app_db.articles", "hello", mygram.SearchOptions{Limit: 10})
```

### Raw boolean search

`Search` decomposes into AND/NOT clauses, which cannot express `OR` or grouping.
`SearchRaw` sends a pre-built boolean expression as a single token so the server
parses `AND` / `OR` / `NOT` / parentheses. Pair it with `ConvertSearchExpression`:

```go
raw, _ := mygram.ConvertSearchExpression("python OR (ruby AND rails)")
resp, _ := client.SearchRaw("articles", raw, mygram.SearchRawOptions{Limit: 50})

// Snippet-returning variants:
resp, _ = client.SearchWithHighlights("articles", "golang", mygram.SearchOptions{})
resp, _ = client.SearchRawWithHighlights("articles", raw, mygram.SearchRawOptions{})
```

### Runtime variables (SET / SHOW VARIABLES)

```go
_ = client.SetVariable("logging.level", "debug")
out, _ := client.ShowVariables("logging%") // raw server table; "" lists all
```

### On-demand sync

```go
_, _ = client.Sync("articles")     // start a full reload of one table
status, _ := client.SyncStatus()   // raw SYNC_STATUS report
_, _ = client.SyncStop("articles") // stop one table's sync ("" stops all)
```

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
knobs (`MYGRAMDB_VERSION`, `KEEP_UP`, ports). The `TestE2ESeeded` block asserts
exact result sets (database-qualified identity, phrase + required-filter
visibility, Japanese ngram matching, `SearchRaw` OR, facet counts).

## License

[MIT](LICENSE)
