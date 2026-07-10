# Search Expressions

The client ships a parser that turns web-style search input (the familiar
`+required -excluded "phrase" OR (grouping)` syntax) into the terms and query
strings MygramDB understands. It is pure Go and has no server dependency, so you
can parse untrusted user input before deciding how to query.

## Syntax

| Syntax | Meaning | Example |
|--------|---------|---------|
| `+term` | Required term (must appear) | `+golang` |
| `-term` | Excluded term (must not appear) | `-deprecated` |
| `term1 term2` | Multiple terms (implicit AND) | `golang tutorial` |
| `"phrase"` | Quoted phrase (exact match) | `"hello world"` |
| `(expr)` | Grouping | `(python OR ruby)` |
| `term1 OR term2` | Logical OR | `golang OR rust` |

Expressions may combine these, for example:

```
+golang +(tutorial OR guide) -deprecated "best practices"
```

## Functions

### SimplifySearchExpression

```go
func SimplifySearchExpression(expression string) (*SimplifiedExpression, error)
```

Decomposes an expression into a main term plus AND/NOT term lists that map
directly onto `SearchOptions`. Use it for simple (non-`OR`, non-grouped) input.

```go
expr, err := mygram.SimplifySearchExpression("+golang tutorial -old -deprecated")
// expr.MainTerm == "golang"
// expr.AndTerms == []string{"tutorial"}
// expr.NotTerms == []string{"old", "deprecated"}

resp, err := client.Search("articles", expr.MainTerm, mygram.SearchOptions{
    AndTerms: expr.AndTerms,
    NotTerms: expr.NotTerms,
})
```

`SimplifiedExpression` has three fields:

```go
type SimplifiedExpression struct {
    MainTerm string   // primary keyword
    AndTerms []string // additional required keywords
    NotTerms []string // excluded keywords
}
```

For an `OR`-only or parenthesized expression, the whole sub-expression is
surfaced as a single parenthesized `MainTerm` so the `OR` semantics survive when
the caller AND-composes the result.

### ConvertSearchExpression

```go
func ConvertSearchExpression(expression string) (string, error)
```

Converts an expression into a MygramDB query string. Simple expressions are
flattened into `AND`/`NOT` form; complex ones (containing `OR` or grouping) are
returned verbatim so the server's boolean parser can interpret them.

```go
mygram.ConvertSearchExpression("+golang +tutorial") // "golang AND tutorial"
mygram.ConvertSearchExpression("+golang -old")      // "golang NOT old"
mygram.ConvertSearchExpression("python OR ruby")    // "python OR ruby"
```

Pass the result to [`SearchRaw`](api-reference.md#searchraw) to run a boolean
query that `Search`'s AND/NOT decomposition cannot express:

```go
raw, _ := mygram.ConvertSearchExpression("python OR (ruby AND rails)")
resp, _ := client.SearchRaw("articles", raw, mygram.SearchRawOptions{Limit: 50})
```

### ParseSearchExpression

```go
func ParseSearchExpression(expression string) (*SearchExpression, error)
```

Returns the fully structured parse tree. Use it when you need to inspect the
classified terms directly.

```go
type SearchExpression struct {
    RequiredTerms []string // terms marked with +
    ExcludedTerms []string // terms marked with -
    OptionalTerms []string // terms without a modifier
    RawExpression string   // original expression when it contains OR/grouping
}
```

### HasComplexExpression

```go
func HasComplexExpression(expr *SearchExpression) bool
```

Reports whether a parsed expression contains `OR` or grouping (i.e. cannot be
simplified into plain AND/NOT terms).

### ToQueryString

```go
func ToQueryString(expr *SearchExpression) string
```

Renders a parsed `SearchExpression` back into a MygramDB query string. Complex
expressions return their original raw form.

## Choosing the Right Path

A small helper decides between the simple and boolean paths automatically:

```go
func smartSearch(client *mygram.Client, table, input string, opts mygram.SearchOptions) (*mygram.SearchResponse, error) {
    parsed, err := mygram.ParseSearchExpression(input)
    if err != nil {
        return nil, err
    }

    if mygram.HasComplexExpression(parsed) {
        raw, err := mygram.ConvertSearchExpression(input)
        if err != nil {
            return nil, err
        }
        return client.SearchRaw(table, raw, mygram.SearchRawOptions{
            Limit:  opts.Limit,
            Offset: opts.Offset,
        })
    }

    simple, err := mygram.SimplifySearchExpression(input)
    if err != nil {
        return nil, err
    }
    opts.AndTerms = append(opts.AndTerms, simple.AndTerms...)
    opts.NotTerms = append(opts.NotTerms, simple.NotTerms...)
    return client.Search(table, simple.MainTerm, opts)
}
```

## How the SEARCH Command Is Built

The options on `SearchOptions` map onto MygramDB clauses as follows.

### Filters

Each entry in `Filters` becomes an independent `FILTER key = value` clause — the
filters are combined by the server, not joined with `AND`:

```go
client.Search("articles", "golang", mygram.SearchOptions{
    Filters: map[string]string{"status": "published", "lang": "en"},
})
// SEARCH articles golang FILTER status = published FILTER lang = en LIMIT 1000
```

`Filters` is a `map`, so when several filters are supplied the relative order of
the `FILTER` clauses on the wire is unspecified.

### Pagination

`Limit` and `Offset` compile to MySQL-style `LIMIT`:

- `LIMIT count` when `Limit` > 0 and `Offset` is 0
- `LIMIT offset,count` when both `Limit` and `Offset` are set
- `OFFSET offset` (no `LIMIT`) when only `Offset` is set
- `LIMIT 1000` (the historical default) when both `Limit` and `Offset` are 0

### Sorting

`SortColumn` and `SortDesc` compile to a `SORT` clause (`SORT column ASC|DESC`).
`SortDesc` defaults to ascending order for a zero value; set it to `true` for
descending. Leave `SortColumn` empty to sort by primary key, or set it to
`"_score"` to sort by BM25 relevance (see
[Advanced Usage](advanced-usage.md#bm25-relevance-scoring)).

### Combined Example

```go
resp, _ := client.Search("articles", "hello world", mygram.SearchOptions{
    AndTerms:   []string{"programming"},
    NotTerms:   []string{"deprecated"},
    Filters:    map[string]string{"status": "published", "lang": "en"},
    SortColumn: "created_at",
    SortDesc:   true,
    Limit:      20,
    Offset:     40,
})
// SEARCH articles "hello world" AND programming NOT deprecated
// FILTER status = published FILTER lang = en SORT created_at DESC LIMIT 40,20
```

## Notes

- A quoted phrase such as `"hello world"` is parsed into a single term (the
  surrounding quotes are stripped). When the client builds the wire command it
  re-quotes any term containing whitespace, so the server still receives it as
  one exact phrase.
- The parser rejects unbalanced parentheses and unclosed quotes with an error;
  wrap parsing of untrusted input accordingly.
- Input sent through the client is validated for control characters and length
  before it reaches the server (see the `MaxQueryLength` config option).
