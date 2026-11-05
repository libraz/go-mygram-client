# 高度な使い方

本番環境向けのパターン、コネクションプール、検索機能、管理コマンドについて
説明します。

## コネクションプール

単一の `Client` は 1 本のソケットを持ち、すべてのコマンドを直列化するため、
一度に 1 リクエストを処理します。並行ワークロードには `Pool` を使ってください。
一定の上限まで多数の接続を貸し出し、透過的な再接続とリトライを加えます。
`Client` と同じコマンドメソッドを公開するため、差し替えとしてそのまま使えます。

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

`NewPool` は I/O を行いません。接続は初回使用時に遅延的に確立されるため、構築
時点でサーバーがまだ到達不能でもプールは許容します。

### PoolConfig

| Field | Default | 説明 |
|-------|---------|-------------|
| `ClientConfig` | — | プール内の各接続に適用される埋め込み設定 |
| `MaxOpenConns` | `10` | 接続総数（アイドル + 使用中）の上限。これを超える借用はブロックする |
| `MaxIdleConns` | = `MaxOpenConns` | 再利用のために保持するアイドル接続数。`MaxOpenConns` に丸められる |
| `ConnMaxLifetime` | `0`（無制限） | 接続の最大寿命。借用時とバックグラウンド回収の両方で適用 |
| `ConnMaxIdleTime` | `0`（無制限） | 接続の最大アイドル時間。借用時とバックグラウンド回収の両方で適用 |
| `Retry` | 下記参照 | リトライ安全な失敗に対するリトライ方針 |
| `CircuitBreaker` | `nil`（無効） | コマンド API を守る任意の `*CircuitBreakerConfig`（[サーキットブレーカー](#サーキットブレーカー) を参照） |
| `OnEvent` | `nil` | 任意の可観測性コールバック（[プールイベント／可観測性](#プールイベント可観測性) を参照） |

`MaxOpenConns` に「無制限」の選択肢は意図的に用意していません。明示的な上限を
設けることこそが要点です。

### バックプレッシャー

すべての接続がビジーのとき、借用は接続が返却されるか呼び出しの `context` が
キャンセルされるまでブロックします。これは自然なバックプレッシャーの信号です。
レイテンシに敏感な呼び出しには締め切りを与え、飽和したプールが際限なくキュー
に積むのではなく負荷を切り捨てるようにしてください。

```go
ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
defer cancel()

resp, err := pool.SearchContext(ctx, "articles", "hello", mygram.SearchOptions{Limit: 100})
if errors.Is(err, context.DeadlineExceeded) {
    // Pool saturated — shed this request (e.g. respond 503 with Retry-After).
}
```

### リトライの意味論

プールは、リトライしても安全な失敗のみをリトライします。

- **冪等なコマンド**（`Search`、`Count`、`Get`、`Facet`、`Info` など）は、
  どの段階で接続レベルの障害が起きても再試行されます。
- **状態を変えるコマンド**（`DumpSave`、`Optimize`、`Sync`、`SetVariable` など）
  は、書き込みがサーバーに届かなかった場合のみ再試行され、コマンドが二重に
  実行されないようにします。
- **接続確立の失敗**（サーバーが一時的に到達不能な場合）は、まだ何も送信して
  いないため、どのコマンドでも再試行されます。
- **一過性のサーバー拒否** — `ErrCodeServerLoading`（6028）、
  `ErrCodeServerNotReady`（6029）、`ErrCodeServerBusy`（6030）（MygramDB 1.10
  以降）— は、どのコマンドでも再試行されます。サーバーは実行**前**にこれらを
  返すため、再送しても二重に適用されることはありません。判定はメッセージ本文の
  照合ではなく、数値コードに基づきます。
- それ以外のサーバーエラー、検証エラー、タイムアウトは決してリトライされません。

リトライ方針は設定可能です。

```go
Retry: mygram.RetryPolicy{
    MaxRetries:  2,                     // additional attempts after the first (default 1)
    BaseBackoff: 100 * time.Millisecond, // base for exponential + full-jitter backoff (default 50ms)
},
```

リトライを完全に無効化するには `MaxRetries` に負値を指定します（ゼロ値は既定の1が選ばれるため無効化にはなりません）。

```go
Retry: mygram.RetryPolicy{MaxRetries: -1}, // コマンドを再送しない
```

時間ベースの上限（`ConnMaxLifetime` または `ConnMaxIdleTime`）を設定すると、バックグラウンド回収がアイドル接続を掃除するため、バースト後に静かになったプールが次の借用まで期限切れソケットを保持し続けることはありません。プールを閉じると回収も停止します。

### モニタリング

`Pool.Stats()` はライブのスナップショットを返します。

```go
s := pool.Stats()
fmt.Printf("open=%d idle=%d in-use=%d waits=%d waitTime=%s discarded=%d retries=%d\n",
    s.Open, s.Idle, s.InUse, s.WaitCount, s.WaitDuration, s.Discarded, s.Retries)
```

### プールのサイジング

各接続はたかだか 1 つの実行中コマンドしか扱わないため、`MaxOpenConns` が実効的な
最大並行数になります。リトルの法則でサイズを決めます。

```
MaxOpenConns ≈ targetThroughput(req/s) × p95RTT(s)
```

そこに RTT のばらつきとスパイクに備えた余裕（およそ 3 倍）を加えます。5 ms の
p95 RTT を持つ LAN 上で 500 req/s なら `500 × 0.005 = 2.5` となるので、
**8〜16** 本の接続で負荷を余裕をもって吸収できます。数値を決める前に、実際の
p95 RTT を測定してください。

### 単一接続での管理コマンド

一連のコマンドを 1 本の借用接続で実行するには `WithConn` を使います。

```go
err := pool.WithConn(ctx, func(c *mygram.Client) error {
    if err := c.CacheDisable(); err != nil {
        return err
    }
    return c.Optimize("articles")
})
```

`WithConn` はエスケープハッチであり、リトライポリシーとサーキットブレーカーの
どちらも経由しません（リトライ・ブレーカーで保護されたコマンドパスを通らず、
接続を直接借用します）。プールは `fn` の周囲でリトライを行わず、ブレーカーが
open でも `WithConn` の呼び出しを即時失敗させません。借用した接続は実行後に
プールへ返却され、`fn` が接続レベルの失敗を報告した場合は破棄されます。

### 管理コマンドの認証

`api.admin_token` を設定した MygramDB サーバー（1.10 以降。TCP リスナーが
ループバック以外にバインドする場合は必須）は、`DUMP`、`SYNC`、`REPLICATION`、
`CONFIG`、`CACHE`、`OPTIMIZE`、`DEBUG`、`SET`、`SHOW VARIABLES` を `AUTH` の
背後に置きます。この許可は接続ごとなので、すべてのソケットで送り直す必要が
あります。

`ClientConfig.AdminToken` を設定すると、接続が確立した直後に `AUTH` が送られ
ます。自動再接続や `Pool` が開いた接続でも同様です。

```go
pool := mygram.NewPool(mygram.PoolConfig{
    ClientConfig: mygram.ClientConfig{
        Host:       "localhost",
        Port:       11016,
        AdminToken: os.Getenv("MYGRAM_ADMIN_TOKEN"),
    },
})
```

トークンが拒否されると `Connect`（およびプールの借用）が
`ErrCodePermissionDenied` を持つ `*ServerError` で失敗します。設定ミスは最初の
管理コマンドではなく、起動時に表面化します。

接続後にしかトークンが分からない場合は `Client.Authenticate(token)` を使います。
トークンを記憶するため、後の再接続でも再認可されます。管理トークンを設定して
いないサーバーに対する検索専用のワークロードでは、どちらも不要です。

### サーキットブレーカー

障害が続いている状況で全リクエストを再試行すると、すでにダウンしている
サーバーへ接続試行とタイムアウトを積み上げるだけです。代わりに素早く失敗
させるには、`PoolConfig.CircuitBreaker` に `*CircuitBreakerConfig` を設定
します。`FailureThreshold`（デフォルト 5）回連続で接続／タイムアウト障害が
起きるとブレーカーが開き、以降の借用はネットワークに触れず `*CircuitOpenError`
を返します。`ResetTimeout`（デフォルト 10 秒）が経過するとブレーカーは 1 回の
試行を許可し（ハーフオープン）、成功すれば閉じ、失敗すれば再び開きます。

ブレーカーはリトライ方針の外側に位置するため、開いている間はリトライも抑制
されます。ブレーカーを開かせるのは接続とタイムアウトの障害だけです。
`*ServerError` や `*ValidationError` はサーバーが依然として到達可能なので
ブレーカーを閉じたままにします。

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

`CircuitBreaker` が nil（デフォルト）のときはブレーカーは無効です。

### プールイベント／可観測性

プールの動作を任意のメトリクスバックエンドに流し込むには、
`PoolConfig.OnEvent` を設定します。コールバックはプールのホットパス上で同期的に
呼ばれるため、ブロックしてはいけません。コールバック内の panic は回復され、
無視されます。発行される `PoolEvent` は次の 4 つです。

| Event | ペイロード | 発行タイミング |
|-------|---------|------|
| `EventAcquire` | `{"wait_seconds": float64}` | 接続が貸し出されたとき。値は待機した時間（待機しなければ 0） |
| `EventRetry` | `{"attempt": int, "error": string}` | リトライのバックオフ前 |
| `EventConnectionDiscarded` | （空） | 検証失敗・期限切れ・アイドル上限超過により接続が破棄されたとき |
| `EventBreakerStateChange` | `{"state": string}` | サーキットブレーカーの状態が変化したとき。`state` は `"closed"`、`"open"`、`"half_open"` のいずれか |

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

`Pool.Stats()` は加えて `PoolStats` のスナップショットを返します
（[モニタリング](#モニタリング) を参照）。

### TCP キープアライブ

TCP 接続では `SO_KEEPALIVE` がデフォルトで有効になり、次のコマンドの読み取り
タイムアウトを待つことなく、静かに切断されたピアを検出します。
`ClientConfig.TCPKeepAliveIdle`（デフォルト 60 秒）はプローブを開始するまでの
アイドル時間を設定し、`ClientConfig.DisableTCPKeepAlive` はこの上書きを無効化
します（dialer 組み込みのデフォルトに委ねます）。どちらも Unix ソケット接続
では無視されます。

```go
client := mygram.NewClientWithConfig(mygram.ClientConfig{
    Host:             "localhost",
    Port:             11016,
    TCPKeepAliveIdle: 30 * time.Second,
})
```

### 自動再接続

`ClientConfig.AutoReconnect`（デフォルト `false`）は、切断された接続を `Client`
に透過的に張り直させます。有効にすると、リクエストが書き込まれる*前*にソケット
が死んでいると分かった場合に限り、コマンドは 1 回だけ再接続して再送します。
リクエストの送信*後*に発生した切断は、コマンドがサーバー側ですでに適用されて
いるかもしれないため、再送せず `*ConnectionError` として表面化します。

```go
client := mygram.NewClientWithConfig(mygram.ClientConfig{
    Host:          "localhost",
    Port:          11016,
    AutoReconnect: true,
})
```

`Pool` は自身が所有する接続では常に自動再接続を有効にするため、一時的な切断は
同じスロット上で吸収されます。プール接続に対して自分で設定する必要はありません。

## 並行性

プールは実際の並行数を `MaxOpenConns` に制限します。好きなだけゴルーチンを起動
してよく、余った呼び出しは借用でブロックします。

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

## ヘルスチェック

`Info` と `GetReplicationStatus` を組み合わせて監視プローブを作れます。

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

## 検索機能

### BM25 関連度スコアリング

特別なソート列 `_score` を使って関連度順にソートします（サーバー側で
`verify_text: ascii|all` が必要です）。

```go
resp, _ := client.Search("articles", "machine learning", mygram.SearchOptions{
    SortColumn: "_score",
    SortDesc:   true,
    Limit:      10,
})
```

### ファジー検索（レーベンシュタイン）

```go
resp, _ := client.Search("articles", "machne", mygram.SearchOptions{
    Fuzzy: 1, // match within 1 edit (or 2)
    Limit: 10,
})
```

### ハイライト

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

ゼロ値の `&mygram.HighlightOptions{}` は、サーバーのデフォルト（`<em>`/`</em>`、
100 コードポイント、最大 3 フラグメント）でハイライトを有効にします。
`SearchWithHighlights` は、任意の `SearchOptions` でハイライトを有効にする
短縮形です。

### ファセット

フィルタ列の distinct な値を、ドキュメント数とともに集計します。任意で検索結果
セットにスコープを絞れます。

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

`Limit` と `Offset` でファセットをページングでき、`TotalCount` がページング前の
distinct 値の総数を返します（MygramDB 1.9 以降）。ページネーションのサイズを
決めるにはこれが必要です。

```go
page, _ := client.Facet("articles", "category", mygram.FacetOptions{
    Limit:  20,
    Offset: 40,
})
fmt.Printf("showing %d of %d categories\n", len(page.Results), page.TotalCount)
```

それより古いサーバーはページのサイズしか返さないため、そこでは `TotalCount` は
`len(Results)` と等しくなります。

### 比較フィルタ

`Filters` が表現できるのは等価比較だけです。`FilterConditions` はサーバーが
受け付ける比較演算子（`=`、`!=`、`>`、`>=`、`<`、`<=`）を追加し、両方の形式を
1 つのリクエストで併用できます（MygramDB 1.9 以降）。マップのエントリはキーの
ソート順、条件はスライスの順で送られるため、同じオプションからは常に同じ
コマンドが生成されます（サーバーのクエリキャッシュにとって重要です）。

```go
resp, _ := client.Search("articles", "golang", mygram.SearchOptions{
    Filters: map[string]string{"status": "1"},
    FilterConditions: []mygram.FilterCondition{
        {Key: "views", Op: mygram.FilterGreaterThanOrEqual, Value: "100"},
        {Key: "created_at", Op: mygram.FilterLessThan, Value: "2026-01-01"},
    },
})
```

`FilterConditions` は `SearchOptions`、`CountOptions`、`FacetOptions` で使え
ます。

### ブールクエリモード

`Search` はテキストをリテラルなユーザー入力として扱い、トークンを引用符で
囲みます。そのため `AND` や `LIMIT` を検索すると、句の開始ではなくその語その
ものが検索されます。`QueryMode: mygram.QueryBoolean` を指定すると、テキストは
そのまま送られ（MygramDB 1.9 以降）、サーバーの式パーサーが `AND` / `OR` /
`NOT` と括弧を演算子として解釈します。しかも `SearchRaw` では使えない型付き
オプションを併用できます。

```go
resp, _ := client.Search("articles", "alpha AND (beta OR gamma)", mygram.SearchOptions{
    QueryMode:  mygram.QueryBoolean,
    SortColumn: "created_at",
    SortDesc:   true,
    Highlight:  &mygram.HighlightOptions{},
    Limit:      50,
})
```

`SearchRaw` は式だけを扱う簡潔な入口として残っています。フィルタ、ソート、
ハイライトが絡まない場合はこちらを使ってください。

### 主キーによる並び替え

`SortColumn` を指定しない場合、クライアントは `SORT` 句を省略し、サーバーは
主キーの降順で並べます。`SortDesc` は明示的なソート列の向きを選ぶだけなので、
主キーの**昇順**には `SortPrimaryKey` が必要です。これはプロトコルの列名なし
`SORT ASC` / `SORT DESC` 短縮形を送出します。

```go
resp, _ := client.Search("articles", "golang", mygram.SearchOptions{
    SortPrimaryKey: mygram.PrimaryKeySortAsc,
})
```

ソート列と主キー順を同時に指定した場合はクライアント側で拒否されます。1 つの
リクエストが持てる `SORT` 句は 1 つだけだからです。

### 生ブール検索

`Search` は AND/NOT 句に分解しますが、これでは `OR` やグループ化を表現できません。
`SearchRaw` は構築済みのブール式をそのまま（引用符なしで）送り、サーバーに
トークン化させて `AND` / `OR` / `NOT` / 括弧を解析させます。`AND` の下に
ネストした `OR` グループも解釈されます（MygramDB 1.8 以降）。
`ConvertSearchExpression` と組み合わせて使います。

```go
raw, _ := mygram.ConvertSearchExpression("python OR (ruby rails)")
resp, _ := client.SearchRaw("articles", raw, mygram.SearchRawOptions{Limit: 50})

// Snippet-returning variant:
resp, _ = client.SearchRawWithHighlights("articles", raw, mygram.SearchRawOptions{})
```

## マルチデータベーステーブル

単一の MygramDB インスタンスは、異なるデータベースにある同名のテーブルを
インデックス化できます（MygramDB 1.7 以降）。テーブルは `database.table`
（例: `app_db.articles`）として参照します。単一データベースのサーバーでは
素の名前も引き続き機能します。

```go
resp, _ := client.Search("app_db.articles", "hello", mygram.SearchOptions{Limit: 10})

// Build / parse identities explicitly when needed:
id, _ := mygram.QualifyTableIdentity("articles", "app_db")  // "app_db.articles"
db, tbl, _ := mygram.ParseTableIdentity("app_db.articles")  // "app_db", "articles"
```

## ランタイム変数

MygramDB 1.7 以降が必要です。

```go
_ = client.SetVariable("logging.level", "debug")
out, _ := client.ShowVariables("logging%") // raw server table; "" lists all
```

## オンデマンド同期

MygramDB 1.7 以降が必要です。

```go
_, _ = client.Sync("articles")     // start a full reload of one table
status, _ := client.SyncStatus()   // raw SYNC_STATUS report
_, _ = client.SyncStop("articles") // stop one table's sync ("" stops all)
```

## レディネス

`INFO` は、HTTP のレディネスエンドポイントと同じ入力から評価した、サーバーの
状態構築が完了しているかどうかを返します（MygramDB 1.10 以降）。

```go
info, _ := client.Info()
if !info.Ready() {
    // info.Readiness は "not_ready"。初期インデックスが構築済みかどうかは
    // info.DataInitialized が示す
}
```

どちらのフィールドも古いサーバーには存在せず、その場合 `Readiness` は空、
`Ready()` は false になります。したがって、MygramDB 1.10 以降の配備だと
分かっている場合にのみ、これでトラフィックを制御してください。

## エラーコード

すべての `ERROR` フレームは数値コードを伴い、`ServerError.Code` として公開され
ます（MygramDB 1.10 以降）。

```go
if _, err := client.Search("ghosts", "x", mygram.SearchOptions{}); err != nil {
    if code, ok := mygram.ServerErrorCode(err); ok && code == mygram.ErrCodeTableNotFound {
        // テーブルがサーバーのカタログに存在しない
    }
}
```

プールも同じコードで何を再試行するかを決めます（[リトライの意味論](#リトライの意味論)
を参照）。MygramDB 1.10 より前のサーバーからのコードなしフレームは `ErrCodeNone`
として報告され、メッセージ本文は信頼できる手がかりではないため、決して再試行
されません。

## 永続化とキャッシュの管理

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

これらのコマンドはサーバーの状態を変えるため、`Pool` の下ではリクエストが
サーバーに届かなかった場合のみリトライされます（[リトライの意味論](#リトライの意味論)
を参照）。

### 長時間かかるコマンド

`DUMP LOAD`、`DUMP VERIFY`、`OPTIMIZE` はインデックス全体を走査または再構築
するため、通常のリクエストよりはるかに長くかかります。これらは 5 秒の
`TimeoutMs` ではなく、それぞれ専用の上限（`DumpLoadTimeoutMs`、
`DumpVerifyTimeoutMs`、`OptimizeTimeoutMs`。いずれも既定 300 秒）を使います。
`TimeoutMs` のままでは、サーバーがまだ実行中のコマンドを打ち切ってしまうから
です。`TimeoutMs` と同じ値を設定すれば無効化できます。より短いコンテキストの
期限は引き続き優先されます。上限はコマンド文字列で決まるため、
`SendCommand("optimize")` や `SendCommand("dump   load")` にも同じ値が適用されます。

非同期実行されるほど大きいダンプは `OK DUMP_STARTED <path>` で受理され、
書き出しはバックグラウンドで続きます。`DumpSave` はこの応答をそのまま返すため、
`DumpStatus` のポーリングは呼び出し側の仕事になります。`DumpSaveAndWait` は
そのポーリングを行い、サーバーが実際に書き出したパスを返します。

```go
path, err := client.DumpSaveAndWait("/var/lib/mygram/snapshot.dump")
```

待ち時間の上限は `DumpSaveTimeoutMs`（既定 300 秒）か、より短いコンテキストの
期限です。タイムアウトしてもダンプは止まりません。サーバーは書き出しを続け、
`DumpStatus` で結果を確認できます。
