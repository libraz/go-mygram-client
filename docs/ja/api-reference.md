# API リファレンス

`go-mygram-client` の完全なリファレンスです。パッケージは `mygram` として
インポートします。

```go
import mygram "github.com/libraz/go-mygram-client"
```

各コマンドメソッドには、第 1 引数に `context.Context` を取る `Context` 版が
あります（例: `Search` / `SearchContext`）。context はその 1 回の呼び出しを
制限し、キャンセルできます。以下では素の形式のみを掲載しています。各メソッドの
`Context` 版は、先頭に `ctx context.Context` を付けたシグネチャを共有します。

`Client` と `Pool` はどちらも同じコマンド群を公開するため、リクエスト処理
コードでは `Pool` を `Client` の差し替えとしてそのまま使えます。プールについては
[高度な使い方](advanced-usage.md#コネクションプール) を参照してください。

## Client

### コンストラクタ

```go
func NewClient(addr string) *Client
func NewClientWithConfig(config ClientConfig) *Client
```

`NewClient` は `addr` を `host:port` として解析し、その形式でなければ Unix
ソケットのパスとして扱います。`NewClientWithConfig` は
[`ClientConfig`](#clientconfig) のゼロ値フィールドをそれぞれのデフォルト値で
補完します。どちらも I/O は行いません。

### 接続

```go
func (c *Client) Connect() error
func (c *Client) Close() error
func (c *Client) IsConnected() bool
```

`Connect` はサーバーに接続し（`TimeoutMs` を尊重）、`Close` は接続を切断します。
`Close` は defer しても安全です。`Client` は 1 本の接続を持ってコマンドを直列化
するため、一度に 1 リクエストを処理します。複数のゴルーチンから呼び出しても
安全ですが、並行には実行されません。

### 検索メソッド

```go
func (c *Client) Search(table, query string, opts SearchOptions) (*SearchResponse, error)
func (c *Client) SearchWithHighlights(table, query string, opts SearchOptions) (*SearchResponse, error)
func (c *Client) SearchRaw(table, rawQuery string, opts SearchRawOptions) (*SearchResponse, error)
func (c *Client) SearchRawWithHighlights(table, rawQuery string, opts SearchRawOptions) (*SearchResponse, error)
func (c *Client) Count(table, query string, opts CountOptions) (*CountResponse, error)
func (c *Client) Facet(table, column string, opts FacetOptions) (*FacetResponse, error)
```

#### Search

`table` の中から `query` に一致するドキュメントを検索します。`table` は素の名前
でも、MygramDB v1.7 以降のマルチデータベースサーバーでは `database.table` の
形式でも指定できます。クエリとオプションは
[検索式](search-expression.md#search-コマンドの組み立て方) で説明する
`SEARCH` コマンドにコンパイルされます。

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

`SearchWithHighlights` は `HIGHLIGHT` 句を有効にした同じ呼び出しで、
`SearchResult.Snippet` にスニペットを返します。

#### SearchRaw

構築済みのブール式をそのまま（引用符なしで）送り（MygramDB v1.8 以降）、
サーバーにトークン化させて `AND` / `OR` / `NOT` / 括弧を解釈させます（`AND` の
下にネストした `OR` グループを含む）。`Search` の AND/NOT 分解では表現できない
`OR` やグループ化を保つため、
[`ConvertSearchExpression`](#検索式の関数) と組み合わせて使います。
`SearchRawWithHighlights` は `HIGHLIGHT` 句を追加します。

```go
raw, _ := mygram.ConvertSearchExpression("python OR (ruby AND rails)")
resp, _ := client.SearchRaw("articles", raw, mygram.SearchRawOptions{Limit: 50})
```

#### Count

一致するドキュメントを、その ID を返さずに数えます。

```go
cnt, _ := client.Count("articles", "machine learning", mygram.CountOptions{
    Filters: map[string]string{"status": "1"},
})
fmt.Println(cnt.Count)
```

#### Facet

フィルタ列の distinct な値を、値ごとのドキュメント数とともに集計します。任意で
クエリにスコープを絞れます（MygramDB v1.6 以降）。
[高度な使い方](advanced-usage.md#ファセット) を参照してください。

### ドキュメントメソッド

```go
func (c *Client) Get(table, primaryKey string) (*Document, error)
```

主キーで単一のドキュメントを取得します。ドキュメントが存在しない場合は
`*ProtocolError`（または `*ServerError`）を返します。

### サーバー情報

```go
func (c *Client) Info() (*ServerInfo, error)
func (c *Client) GetConfig() (string, error)
```

`Info` はバージョン、稼働時間、ドキュメント数、テーブル一覧を返します。
`GetConfig` はサーバー設定を生の YAML 文字列として返します。

### レプリケーション

```go
func (c *Client) GetReplicationStatus() (*ReplicationStatus, error)
func (c *Client) StartReplication() error
func (c *Client) StopReplication() error
```

MySQL binlog レプリケーションを問い合わせ・制御します。

### デバッグ

```go
func (c *Client) EnableDebug() error
func (c *Client) DisableDebug() error
```

デバッグモードを有効にすると、検索とカウントの応答に、クエリごとの所要時間と
候補数を格納した `Debug *DebugInfo` フィールドが付与されます。

### ランタイム変数（v1.7 以降）

```go
func (c *Client) SetVariable(name, value string) error
func (c *Client) ShowVariables(likePattern string) (string, error)
```

`SetVariable` は MySQL 互換の `SET` を発行します。空白を含む値は自動的に
引用符で囲まれます。`ShowVariables` は生の `SHOW VARIABLES [LIKE …]` テーブルを
返します（`""` を渡すとすべてを一覧します）。

### オンデマンド同期（v1.7 以降）

```go
func (c *Client) Sync(table string) (string, error)
func (c *Client) SyncStatus() (string, error)
func (c *Client) SyncStop(table string) (string, error)
```

`Sync` は 1 つのテーブル（素の名前または `database.table`）の完全な再読み込みを
開始します。`SyncStatus` は生の `SYNC_STATUS` レポートを返します。`SyncStop` は
1 つのテーブルの同期を停止し、`table` が `""` のときは実行中のすべての同期を
停止します。

### 永続化とメンテナンス

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

インデックスのスナップショット取得・復元（`Dump*`）、テーブルのインデックス
再構築（`Optimize`）、クエリキャッシュの検査・制御（`Cache*`）を行います。

### 生コマンド

```go
func (c *Client) SendCommand(command string) (string, error)
```

任意のプロトコルコマンドを送り、生のサーバー応答を返します。型付き API が
カバーしないコマンド向けの避難口です。

## Pool

`Pool` は一定の上限まで多数の接続を貸し出し、透過的な再接続とリトライを加え
ます。`Client` が持つすべてのコマンドメソッドを同一のシグネチャで公開し、加えて
次を提供します。

```go
func NewPool(cfg PoolConfig) *Pool
func (p *Pool) Close() error
func (p *Pool) Stats() PoolStats
func (p *Pool) WithConn(ctx context.Context, fn func(c *Client) error) error
```

`WithConn` は `fn` の実行中だけ 1 本の接続を借り受けます。同一の接続で一連の
コマンドを実行したいとき、あるいはリトライの意味論を自分で制御したいときに
使ってください（プールは `fn` の周りでリトライを行いません）。設定とサイジングに
ついては [高度な使い方](advanced-usage.md#コネクションプール) を参照してください。

## 型

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

`AutoReconnect`、`DisableTCPKeepAlive`、`TCPKeepAliveIdle` については
[高度な使い方](advanced-usage.md#tcp-キープアライブ) を参照してください。`Pool` は
自身が所有する接続では常に `AutoReconnect` を有効にします。

### CircuitBreakerConfig

```go
type CircuitBreakerConfig struct {
    FailureThreshold int           // consecutive network failures that open the breaker (default 5)
    ResetTimeout     time.Duration // how long the breaker stays open before a trial (default 10s)
}
```

プールのコマンド API にサーキットブレーカーを付けるには、
`PoolConfig.CircuitBreaker` に `*CircuitBreakerConfig` を設定します。nil の
ポインタは無効を意味します。ブレーカーを開かせるのは `*ConnectionError` と
`*TimeoutError` だけです。[高度な使い方](advanced-usage.md#サーキットブレーカー)
を参照してください。

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

`PoolConfig.OnEvent func(event PoolEvent, payload map[string]any)` に配信され
ます。コールバックはプールのホットパス上で同期的に呼ばれるためブロックしては
いけません。内部で panic しても回復され、無視されます。
[高度な使い方](advanced-usage.md#プールイベント可観測性) を参照してください。

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

ゼロ値の `&HighlightOptions{}` は、サーバーのデフォルトでハイライトを有効にします。

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

### 応答の型

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

`DumpStatus` と `CacheStats` は、それぞれ `DUMP STATUS` と `CACHE STATS` を
解析したフィールドを保持します。フィールドの完全な一覧はそれぞれの godoc を
参照してください。

## エラー型

すべてのコマンドメソッドは、`errors.As` でマッチできる以下の具象型のいずれかを
返します。

| Type | 意味 |
|------|---------|
| `*ConnectionError` | 接続に失敗した、または送受信中に接続レベルの障害が発生した |
| `*ProtocolError` | サーバーが不正または予期しない応答を返した |
| `*TimeoutError` | コマンドが締め切りを超過した。`net.Error` を満たす（`Timeout()` が `true`）。呼び出し側の context の締切が原因の場合は `context.DeadlineExceeded` を wrap する |
| `*ValidationError` | クライアント側の入力検証に失敗した（制御文字、長さ超過のクエリ） |
| `*ServerError` | サーバーが明示的な `ERROR` を返した |
| `*CircuitOpenError` | プールのサーキットブレーカーが開いている（または試行が実行中のハーフオープン）。ネットワークに触れず送出され、呼び出し側は素早く失敗する。ネットワーク障害には分類されないため、これ自体がブレーカーを駆動することはない |

```go
type ConnectionError struct {
    Message string
    Err     error // unwrappable with errors.Unwrap / errors.Is
}
```

`ConnectionError` は `Unwrap` を実装しているため、`errors.Is(err, io.EOF)` の
ような検査が根本原因まで到達します。

## 検索式の関数

```go
func SimplifySearchExpression(expression string) (*SimplifiedExpression, error)
func ConvertSearchExpression(expression string) (string, error)
func ParseSearchExpression(expression string) (*SearchExpression, error)
func HasComplexExpression(expr *SearchExpression) bool
func ToQueryString(expr *SearchExpression) string
```

詳細と例は [検索式](search-expression.md) を参照してください。

## テーブル識別子のヘルパー（v1.7 以降）

```go
func QualifyTableIdentity(table, database string) (string, error)
func ParseTableIdentity(identity string) (database, table string, err error)
```

`QualifyTableIdentity` は `database.table` の識別子を組み立てます（`database` が
空のときは素のテーブル名を返します）。`ParseTableIdentity` はそれを各部分に
分割し直します。どちらも識別子を検証し、空白と制御文字を拒否します。

```go
id, _ := mygram.QualifyTableIdentity("articles", "app_db") // "app_db.articles"
db, tbl, _ := mygram.ParseTableIdentity("app_db.articles") // "app_db", "articles"
```
