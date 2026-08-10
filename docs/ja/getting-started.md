# はじめに

このガイドでは、MygramDB サーバーへの接続と、`go-mygram-client` を使った
最初のクエリの実行までを順に説明します。

## 前提条件

- Go 1.26 以降
- 稼働中の MygramDB サーバー（v1.10 を推奨。v1.6 〜 v1.9 も利用可）

## インストール

```bash
go get github.com/libraz/go-mygram-client
```

このクライアントが依存するのは Go 標準ライブラリのみです。

## 基本セットアップ

### 1. パッケージのインポート

このモジュールはパッケージ名 `mygram` としてインポートします。

```go
import mygram "github.com/libraz/go-mygram-client"
```

### 2. クライアントの作成

`NewClient` は `host:port` 形式のアドレス（または Unix ソケットのパス）を受け取ります。

```go
client := mygram.NewClient("localhost:11016")
```

> **Client と Pool の使い分け。** `Client` は単一の接続を保持してコマンドを直列化
> するため、同時に1リクエストしか扱えず、事前に明示的な接続が必要です（下記）。
> 多くのアプリケーションでは [`Pool`](advanced-usage.md#コネクションプール) を使う
> のがおすすめです — 複数接続を再接続・リトライ付きで管理し、遅延接続で、同じ
> コマンドメソッドを公開します。`Client` を直接使うのは、単一接続を自分で握って
> 制御したい場合に限ります。
>
> `host:port` として解釈できないアドレス（例: ポートのない `localhost`）は Unix
> ソケットのパスとして扱われます。

より細かく制御したい場合は、`ClientConfig` を `NewClientWithConfig` に渡します。

```go
client := mygram.NewClientWithConfig(mygram.ClientConfig{
    Host:      "192.168.1.100",
    Port:      11016,
    TimeoutMs: 10000,
})
```

### 3. 接続

```go
if err := client.Connect(); err != nil {
    log.Fatal(err)
}
defer client.Close()
```

すべてのコマンドには `Context` 版（例: `ConnectContext`）もあり、
`context.Context` を受け取って呼び出しごとの締め切りやキャンセルを指定できます。

### 4. コマンドの実行

```go
// Search
resp, err := client.Search("articles", "hello world", mygram.SearchOptions{Limit: 10})

// Count matches without fetching IDs
cnt, err := client.Count("articles", "technology", mygram.CountOptions{})

// Fetch a document by primary key
doc, err := client.Get("articles", "12345")
```

## 完全な例

```go
package main

import (
    "fmt"
    "log"

    mygram "github.com/libraz/go-mygram-client"
)

func main() {
    client := mygram.NewClient("localhost:11016")

    if err := client.Connect(); err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    resp, err := client.Search("articles", "golang tutorial", mygram.SearchOptions{
        Limit:      10,
        SortColumn: "created_at",
        SortDesc:   true,
    })
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Found %d results\n", resp.TotalCount)
    for _, r := range resp.Results {
        fmt.Printf("- %s\n", r.PrimaryKey)
    }
}
```

## 設定オプション

`ClientConfig`（`NewClientWithConfig` が使用）は以下のフィールドを受け取ります。
`DefaultConfig()` がデフォルト値を返し、ゼロ値のフィールドはそれぞれの
デフォルト値で補完されます。

| Field | Type | Default | 説明 |
|-------|------|---------|-------------|
| `Host` | string | `"127.0.0.1"` | サーバーのホスト名または IP アドレス |
| `Port` | int | `11016` | サーバーのポート番号 |
| `SocketPath` | string | — | Unix ドメインソケットのパス。設定時は TCP より優先される |
| `TimeoutMs` | int | `5000` | コマンドのタイムアウト（ミリ秒） |
| `RecvBufferSize` | int | `65536` | 受信バッファのサイズ（バイト） |
| `MaxQueryLength` | int | `128` | 検証で失敗となるクエリ式の最大長。`0` でチェックを無効化 |
| `MaxResponseSize` | int | `0` | 受け付ける応答の最大サイズ（バイト）。`0` は無制限。有限値（例: `64<<20`）を指定すると、暴走した応答がメモリを食い潰すのを防げる |

`ClientConfig` には耐障害性向けのフィールド（`AutoReconnect`、
`DisableTCPKeepAlive`、`TCPKeepAliveIdle`）もあります。詳細は
[高度な使い方](advanced-usage.md#tcp-キープアライブ) と
[API リファレンス](api-reference.md#clientconfig) を参照してください。

## エラーハンドリング

各コマンドは `errors.As` でマッチできる具象エラー型を返します。

```go
import "errors"

resp, err := client.Search("articles", "test", mygram.SearchOptions{})
if err != nil {
    var connErr *mygram.ConnectionError
    var protoErr *mygram.ProtocolError
    var timeoutErr *mygram.TimeoutError
    var valErr *mygram.ValidationError
    var srvErr *mygram.ServerError

    switch {
    case errors.As(err, &connErr):
        log.Println("connection failed:", connErr)
    case errors.As(err, &timeoutErr):
        log.Println("request timed out:", timeoutErr)
    case errors.As(err, &valErr):
        log.Println("invalid input:", valErr)
    case errors.As(err, &srvErr):
        // srvErr.Code は MygramDB v1.10 以降でのサーバーの数値エラーコード
        // （それより古いサーバーでは ErrCodeNone）。
        log.Println("server returned an error:", srvErr)
    case errors.As(err, &protoErr):
        log.Println("protocol error:", protoErr)
    default:
        log.Println("unexpected error:", err)
    }
}
```

エラー型の一覧は [API リファレンス](api-reference.md#エラー型) を、MygramDB
v1.10 以降がすべての `ERROR` フレームに付与する数値コードについては
[サーバーのエラーコード](api-reference.md#サーバーのエラーコード) を参照して
ください。

`TimeoutError` は `net.Error` も満たし（`Timeout()` が `true` を返す）、`context`
の締切超過が原因でコマンドがタイムアウトした場合は `context.DeadlineExceeded` を
wrap します。そのため次のどちらも一致します。

```go
var ne net.Error
if errors.As(err, &ne) && ne.Timeout() { /* あらゆるタイムアウト */ }
if errors.Is(err, context.DeadlineExceeded) { /* ctx の締切が原因 */ }
```

## 次のステップ

- [API リファレンス](api-reference.md) — すべてのメソッド、オプション、型
- [検索式](search-expression.md) — Web スタイルの検索入力を解析する
- [高度な使い方](advanced-usage.md) — コネクションプール、耐障害性機能
  （サーキットブレーカー、可観測性、TCP キープアライブ、自動再接続）、管理コマンド
  の `AUTH`、エラーコード、v1.6 〜 v1.10 の検索機能、本番環境向けのパターン

## トラブルシューティング

### Connection refused（接続拒否）

- MygramDB サーバーが稼働していて到達可能か確認してください。
- ホストとポート（または `SocketPath`）が正しいか確認してください。
- ファイアウォールが接続を遮断していないか確認してください。

### タイムアウトエラー

- `ClientConfig` の `TimeoutMs` を増やしてください。
- サーバー負荷とネットワーク遅延を確認してください。
- 呼び出しごとに制御したい場合は、明示的な締め切りを設定した `Context` 版を使ってください。

### 検証エラー

`*ValidationError` は、送信前にクライアントが入力を拒否したことを意味します。
多くの場合、テーブル名・クエリ・フィルタ値に含まれる制御文字（CR/LF）か、
結合した式の長さが `MaxQueryLength` を超えたクエリが原因です。
入力を修正するか、より長い式が正当に必要であれば `MaxQueryLength`
（およびサーバー側の `api.max_query_length`）を引き上げてください。
