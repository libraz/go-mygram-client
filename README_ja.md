# go-mygram-client

[![CI](https://img.shields.io/github/actions/workflow/status/libraz/go-mygram-client/ci.yml?branch=main&label=CI)](https://github.com/libraz/go-mygram-client/actions)
[![Go Reference](https://pkg.go.dev/badge/github.com/libraz/go-mygram-client.svg)](https://pkg.go.dev/github.com/libraz/go-mygram-client)
[![Go](https://img.shields.io/badge/go-%E2%89%A51.26-00ADD8?logo=go)](https://go.dev/)
[![codecov](https://codecov.io/gh/libraz/go-mygram-client/branch/main/graph/badge.svg)](https://codecov.io/gh/libraz/go-mygram-client)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
[![golangci-lint](https://img.shields.io/badge/lint-golangci--lint-00ACD7?logo=go)](https://golangci-lint.run/)
[![Zero Dependencies](https://img.shields.io/badge/dependencies-0-brightgreen)](https://pkg.go.dev/github.com/libraz/go-mygram-client?tab=imports)

[MygramDB](https://github.com/libraz/mygram-db/)（MySQL レプリケーション対応の
高性能インメモリ全文検索エンジン）向けの Go クライアントライブラリです。

MygramDB v1.10（管理コマンドの `AUTH`、数値エラーコードとそれに基づくリトライ判定、
`INFO` のレディネス、`CACHE STATS` の全項目、長時間コマンド専用のタイムアウト）、
v1.9（型付き比較フィルタ、ブールクエリモード、ページング可能なファセット、主キー順
の指定）、v1.8（引用符なしの生ブール転送、AND の下にネストした OR グループ、
日付のみのフィルタ値）、v1.7（データベース修飾テーブル、生ブール検索、ランタイム
変数、オンデマンド同期）、v1.6（BM25 スコアリング、HIGHLIGHT、FUZZY、FACET）に
対応しています。古いサーバーも引き続き利用できます。サーバーが実装していない機能は
そもそも要求せず、コードなしの `ERROR` フレームはコードなしのまま報告します。

## 概要

MygramDB は MySQL FULLTEXT より **25〜200 倍高速** な全文検索を提供します。
本クライアントは MygramDB の TCP テキストプロトコル（memcached 形式）を通じて
通信し、外部依存はゼロです。

| | MySQL FULLTEXT | MygramDB |
|---|---|---|
| **検索速度** | 基準 | 25〜200 倍高速 |
| **ストレージ** | ディスク | インメモリ |
| **レプリケーション** | — | MySQL binlog |
| **プロトコル** | MySQL | TCP（memcached 形式） |

### 機能

- **依存ゼロ** — 標準ライブラリのみ
- **Context 対応** — すべてのメソッドに `context.Context` 版がある
- **検索式パーサー** — Web 風の検索構文（+必須、-除外、"フレーズ"、OR、グループ化）
- **完全なプロトコル対応** — MygramDB の全コマンド（SEARCH、COUNT、GET、INFO など）
- **入力検証** — 制御文字およびプロトコル区切り文字のインジェクションに対する組み込みの保護
- **型付きエラー** — サーバーの `ERROR` フレームは数値の `ErrorCode` を伴い、一過性のコードはプールのリトライを駆動する
- **スレッドセーフ** — 並行利用に対して安全
- **コネクションプール** — 高スループット向けの、再接続とリトライを備えた上限つき `Pool`
- **レジリエンス** — プールのサーキットブレーカー、可観測性イベント、TCP キープアライブ、クライアントレベルの自動再接続
- **管理コマンドの AUTH** — 設定したトークンは、再接続やプールのソケットを含むすべての接続で再送される

## インストール

```bash
go get github.com/libraz/go-mygram-client
```

## クイックスタート

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

## コネクションプール

単一の `Client` は 1 本の接続を持ち、すべてのコマンドを直列化するため、一度に
1 リクエストを処理します。並行ワークロードには `Pool` を使ってください。一定の
上限まで多数の接続を貸し出し、透過的な再接続とリトライを加えます。`Client` と
同じコマンドメソッドを公開するため、差し替えとしてそのまま使えます。

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

すべての接続がビジーのとき、呼び出しは接続が返却されるか `context` が
キャンセルされるまでブロックします。これは自然なバックプレッシャーの信号です。
サイジング、リトライの意味論、モニタリングについては
[コネクションプール](docs/ja/advanced-usage.md#コネクションプール) を参照して
ください。

## ドキュメント

- [はじめに](docs/ja/getting-started.md) — インストール、設定、エラー処理
- [検索式](docs/ja/search-expression.md) — Web 風の検索入力の解析
- [API リファレンス](docs/ja/api-reference.md) — すべてのメソッド、オプション、型
- [高度な使い方](docs/ja/advanced-usage.md) — コネクションプールとレジリエンス機能（サーキットブレーカー、可観測性イベント、TCP キープアライブ、自動再接続）、管理コマンドの `AUTH`、エラーコード、および v1.10 までに追加された検索機能（BM25 `_score`、ファジー、ハイライト、ページング可能なファセット、比較フィルタ、ブールクエリモード、マルチデータベース、ランタイム変数、オンデマンド同期）

パッケージレベルの API ドキュメントは
[pkg.go.dev](https://pkg.go.dev/github.com/libraz/go-mygram-client) にもあります。

## 開発

```bash
make test       # Run unit tests
make test-race  # Run unit tests with race detector
make test-e2e   # Run e2e tests against a live MygramDB server
make test-e2e-docker # Spin up MySQL + MygramDB (docker compose), run e2e, tear down
make lint       # Run golangci-lint
make fmt        # Format code
make coverage   # Generate coverage report
```

### エンドツーエンドテスト

`make test-e2e` は、稼働中の MygramDB インスタンスに対してすべての公開メソッドを
検証します。テストは `e2e` ビルドタグでゲートされているため、デフォルトの
`go test ./...`（および CI）はサーバーに接続しません。

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

サーバーに到達できない場合、すべての e2e テストはスキップされます（失敗では
ありません）。グローバルなサーバー状態を変更するスイート（レプリケーションの
開始／停止、キャッシュの切り替え、ダンプの往復）は、終了時に元の状態を復元
します。

#### 自己完結型の docker-compose スタック

`make test-e2e-docker` は使い捨ての **MySQL + MygramDB** スタック（サーバーは
`ghcr.io/libraz/mygram-db` から取得）を起動し、決定論的なデータセットを投入し、
`MYGRAM_E2E_SEEDED=1` で e2e スイートを実行してから、すべてを撤去します。ローカル
サーバーは不要です。ノブ（`MYGRAMDB_VERSION`、`MYGRAM_ADMIN_TOKEN`、`KEEP_UP`、
ポート）については [`e2e/docker/`](e2e/docker/README.md) を参照してください。
`TestE2ESeeded` ブロックは正確な結果セット（データベース修飾識別子、フレーズ +
必須フィルタの可視性、日本語 ngram マッチング、`SearchRaw` の OR、ファセット数と
ページング、比較フィルタ、ブールクエリモード）を検証します。

## ライセンス

[MIT](LICENSE)
