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
func (c *Client) Authenticate(token string) error
```

`Connect` はサーバーに接続し（`TimeoutMs` を尊重）、`Close` は接続を切断します。
`Close` は defer しても安全です。`Client` は 1 本の接続を持ってコマンドを直列化
するため、一度に 1 リクエストを処理します。複数のゴルーチンから呼び出しても
安全ですが、並行には実行されません。

`Authenticate` は `AUTH` を送り、トークンを記憶します。そのため再接続時には
次のコマンドの前に再認可されます。[`ClientConfig.AdminToken`](#clientconfig) を
設定すれば明示的な呼び出しなしで同じことができ、`Pool` の接続を認証する手段は
これだけです。[高度な使い方](advanced-usage.md#管理コマンドの認証) を参照して
ください。

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

`Filters` が表現できるのは等価比較だけです。比較演算子を使う場合は
`FilterConditions` を、`SearchRaw` が受け付けない型付きオプションと式を
組み合わせる場合は `QueryMode: mygram.QueryBoolean` を指定します
（MygramDB v1.9 以降）。

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
クエリにスコープを絞れます（MygramDB v1.6 以降）。`Limit` と `Offset` で値を
ページングでき、`FacetResponse.TotalCount` はページング前の distinct 値の総数を
返します（MygramDB v1.9 以降）。
[高度な使い方](advanced-usage.md#ファセット) を参照してください。

### ドキュメントメソッド

```go
func (c *Client) Get(table, primaryKey string) (*Document, error)
```

主キーで単一のドキュメントを取得します。主キーは可逆的にエスケープされるため、
空白や引用符を含むものも含めて、サーバーが返せる主キーはそのまま送り返せます。
ドキュメントが存在しない場合は `*ProtocolError`（または `*ServerError`）を
返します。

### サーバー情報

```go
func (c *Client) Info() (*ServerInfo, error)
func (c *Client) GetConfig() (string, error)
```

`Info` はバージョン、稼働時間、ドキュメント数、テーブル一覧を返し、MygramDB
v1.10 以降ではレディネスの判定（`ServerInfo.DataInitialized`、
`ServerInfo.Readiness`、`ServerInfo.Ready()`）も返します。`GetConfig` は
サーバー設定を生の YAML 文字列として返します。

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

インデックスのスナップショット取得・復元（`Dump*`）、テーブルのインデックス
再構築（`Optimize`）、クエリキャッシュの検査・制御（`Cache*`）を行います。

非同期実行されるほど大きいダンプは `OK DUMP_STARTED <path>` で受理され、
書き出しはバックグラウンドで続きます。`DumpSave` はこの受理応答をそのまま返し、
完了の監視は呼び出し側に委ねます。`DumpSaveAndWait` は `DUMP STATUS` を
ポーリングし、完了時は実際に書き出されたパスを、失敗時は `*ServerError` を
返します。待ち時間の上限は `ClientConfig.DumpSaveTimeoutMs` です。同期的に
完了する小さいダンプは `OK SAVED <path>` で受理され、どちらも即座に返ります。

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
    AdminToken          string        // AUTH token replayed on every connection (v1.10+; default none)
}
```

`AutoReconnect`、`DisableTCPKeepAlive`、`TCPKeepAliveIdle` については
[高度な使い方](advanced-usage.md#tcp-キープアライブ) を参照してください。`Pool` は
自身が所有する接続では常に `AutoReconnect` を有効にします。

`DumpLoadTimeoutMs`、`DumpVerifyTimeoutMs`、`OptimizeTimeoutMs` は、インデックス
全体を再構築または走査するコマンドに適用されます。これらがないと、5 秒の
`TimeoutMs` によって、サーバーがまだ実行中のコマンドを打ち切ってしまいます。
`TimeoutMs` と同じ値を設定すれば無効化できます。`*Context` 版に渡した
コンテキストの期限は、そちらが短い場合には引き続き優先されます。

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
    SortDesc   bool              // descending order; applies to SortColumn
    Fuzzy      int               // Levenshtein distance 0/1/2 (v1.6+)
    Highlight  *HighlightOptions // enable HIGHLIGHT when non-nil (v1.6+)

    FilterConditions []FilterCondition // filters with a comparison operator (v1.9+)
    QueryMode        QueryMode         // QueryLiteral (default) or QueryBoolean (v1.9+)
    SortPrimaryKey   PrimaryKeySort    // primary-key ordering with no sort column
}
```

`Filters` のエントリはキーのソート順で、続いて `FilterConditions` がスライスの
順で送られます。したがって同じオプションからは常に同じコマンドが生成されます。

### PrimaryKeySort

```go
type PrimaryKeySort uint8

const (
    PrimaryKeySortDefault PrimaryKeySort = iota // omit SORT; server orders by primary key descending
    PrimaryKeySortAsc                           // SORT ASC  — primary key ascending
    PrimaryKeySortDesc                          // SORT DESC — primary key descending, stated explicitly
)
```

`SortDesc` は明示的な `SortColumn` の並び順を選ぶだけなので、主キーの**昇順**は
`SortPrimaryKey` からしか指定できません。`SortColumn` と既定値以外の
`SortPrimaryKey` を同時に設定すると `*ValidationError` になります。1 つの
リクエストが持てる `SORT` 句は 1 つだけだからです。

```go
resp, err := client.Search("articles", "golang", mygram.SearchOptions{
    SortPrimaryKey: mygram.PrimaryKeySortAsc,
    Limit:          20,
})
```

### QueryMode と FilterCondition

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
    AndTerms         []string
    NotTerms         []string
    Filters          map[string]string
    FilterConditions []FilterCondition // comparison filters (v1.9+)
    QueryMode        QueryMode         // QueryLiteral (default) or QueryBoolean (v1.9+)
}
```

### FacetOptions

```go
type FacetOptions struct {
    Query            string            // scope aggregation to matching documents
    AndTerms         []string
    NotTerms         []string
    Filters          map[string]string
    FilterConditions []FilterCondition // comparison filters (v1.9+)
    Limit            int               // bound the number of (value, count) pairs (0 = no limit)
    Offset           int               // distinct values to skip (v1.9+)
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
    Results    []FacetValue // ordered by count descending; one page when Limit/Offset are set
    TotalCount int64        // distinct values before OFFSET and LIMIT (v1.9+)
}

type ServerInfo struct {
    Version           string
    UptimeSeconds     int
    TotalRequests     int
    ActiveConnections int
    IndexSizeBytes    int
    DocCount          int
    Tables            []string
    DataInitialized   bool   // initial index state built (v1.10+)
    Readiness         string // "ready" or "not_ready" (v1.10+); empty on older servers
}

func (i *ServerInfo) Ready() bool // Readiness == "ready"

type ReplicationStatus struct {
    Running                 bool
    GTID                    string
    StatusStr               string
    ProcessedEvents         int
    QueueSize               int
    State                   string    // "running" | "stopped" | "failed" | "not_configured" (v1.10+)
    CRCErrors               int       // チェックサム検証に失敗した binlog イベント数 (v1.10+)
    SchemaIncompatible      bool      // MySQL のスキーマが設定と一致せず停止 (v1.10+)
    LastErrorCode           ErrorCode // 直近の失敗コード。失敗がなければ ErrCodeNone (v1.10+)
    LastError               string    // LastErrorCode に対応するメッセージ (v1.10+)
    LastAppliedUnixtime     int64     // 最後にイベントを適用した Unix 時刻 (v1.10+)
    SecondsSinceLastApplied int64     // レプリケーション遅延。未適用の間は -1 (v1.10+)
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
参照してください。`CacheStats` は v1.10 サーバーが報告するすべてのキーを
カバーします。ヒット数のカウンタ、ペイロード・無効化インデックス・無効化キュー
に分けたメモリ内訳、理由別の登録拒否数、無効化の総数です。古いサーバーが
出力しないキーはゼロ値のままになります。`AvgHitTimeMs` と `AvgMissTimeMs` が
`*float64` なのは、対応するカウンタが 0 の間サーバーがこれらを出力しないため
です。単なる `0` では両者を区別できません。

## エラー型

すべてのコマンドメソッドは、`errors.As` でマッチできる以下の具象型のいずれかを
返します。

| Type | 意味 |
|------|---------|
| `*ConnectionError` | 接続に失敗した、または送受信中に接続レベルの障害が発生した |
| `*ProtocolError` | サーバーが不正または予期しない応答を返した |
| `*TimeoutError` | コマンドが締め切りを超過した。`net.Error` を満たす（`Timeout()` が `true`）。呼び出し側の context の締切が原因の場合は `context.DeadlineExceeded` を wrap する |
| `*ValidationError` | クライアント側の入力検証に失敗した（制御文字、識別子中のプロトコル区切り文字、長さ超過のクエリ） |
| `*ServerError` | サーバーが明示的な `ERROR` を返した。MygramDB v1.10 以降では数値の `Code` を伴う |
| `*CircuitOpenError` | プールのサーキットブレーカーが開いている（または試行が実行中のハーフオープン）。ネットワークに触れず送出され、呼び出し側は素早く失敗する。ネットワーク障害には分類されないため、これ自体がブレーカーを駆動することはない |

```go
type ConnectionError struct {
    Message string
    Err     error // unwrappable with errors.Unwrap / errors.Is
}
```

`ConnectionError` は `Unwrap` を実装しているため、`errors.Is(err, io.EOF)` の
ような検査が根本原因まで到達します。

### サーバーのエラーコード

MygramDB v1.10 以降、すべての `ERROR` フレームは `ERROR <code> <message>` の形
を取り、コードは `ServerError.Code` に解析されます。それより古いサーバーは
コードなしのフレームを送り、その場合は `ErrCodeNone` として報告されます。
つまり「コードなし」と実際のコードは、メッセージ本文から推測せずに区別できます。

```go
type ErrorCode uint16

type ServerError struct {
    Message string    // the frame's message, without the code prefix
    Code    ErrorCode // ErrCodeNone on a pre-v1.10 server
}

func (e *ServerError) Retryable() bool
func (c ErrorCode) Retryable() bool
func ServerErrorCode(err error) (ErrorCode, bool)
```

`Retryable` が真になるのは一過性のコード、すなわち `ErrCodeServerLoading`
（6028）、`ErrCodeServerNotReady`（6029）、`ErrCodeServerBusy`（6030）だけです。
これらはコマンドを実行する**前**にサーバーが返すため、状態を変えるコマンドで
あっても再送は安全です。`Pool` は通常のバックオフのもとでこれらだけを再試行し、
他のコードは決定的な失敗としてそのまま返します。

クライアントが対処しうるコードには名前付き定数があります
（`ErrCodeTableNotFound`、`ErrCodePermissionDenied`、`ErrCodeQuerySyntaxError`
など）。ワイヤ上には任意の `uint16` が現れうるため、未知の値は不透明な
サーバーエラーとして扱ってください。

```go
if code, ok := mygram.ServerErrorCode(err); ok && code == mygram.ErrCodeTableNotFound {
    // テーブルがサーバーのカタログに存在しない
}
```

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
