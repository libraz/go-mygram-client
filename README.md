# go-mygram-client

[![CI](https://img.shields.io/github/actions/workflow/status/libraz/go-mygram-client/ci.yml?branch=main&label=CI)](https://github.com/libraz/go-mygram-client/actions)
[![Go Reference](https://pkg.go.dev/badge/github.com/libraz/go-mygram-client.svg)](https://pkg.go.dev/github.com/libraz/go-mygram-client)
[![Go](https://img.shields.io/badge/go-%E2%89%A51.23-00ADD8?logo=go)](https://go.dev/)
[![codecov](https://codecov.io/gh/libraz/go-mygram-client/branch/main/graph/badge.svg)](https://codecov.io/gh/libraz/go-mygram-client)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
[![golangci-lint](https://img.shields.io/badge/lint-golangci--lint-00ACD7?logo=go)](https://golangci-lint.run/)
[![Zero Dependencies](https://img.shields.io/badge/dependencies-0-brightgreen)](https://pkg.go.dev/github.com/libraz/go-mygram-client?tab=imports)

Go client library for [MygramDB](https://github.com/libraz/mygram-db/) — a high-performance in-memory full-text search engine with MySQL replication support.

Compatible with MygramDB v1.6 (BM25 scoring, HIGHLIGHT, FUZZY, FACET).

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

## Development

```bash
make test       # Run tests
make test-race  # Run tests with race detector
make lint       # Run golangci-lint
make fmt        # Format code
make coverage   # Generate coverage report
```

## License

[MIT](LICENSE)
