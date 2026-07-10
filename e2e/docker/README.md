# Closed-loop E2E (docker-compose)

A self-contained end-to-end stack: **MySQL** (seeded with a fixed dataset) plus a
**MygramDB** server that replicates from it. It lets `go test -tags=e2e` run
against a real server with deterministic data — nothing outside this directory
is needed, because the server is pulled as a published image.

## Run

```bash
# One shot: bring the stack up, run the e2e suite, tear it down.
make test-e2e-docker
```

Equivalent manual steps:

```bash
docker compose -f e2e/docker/docker-compose.yml up -d --wait
MYGRAM_E2E_SEEDED=1 go test -tags=e2e -count=1 ./...
docker compose -f e2e/docker/docker-compose.yml down -v
```

## Knobs (environment variables)

| Variable | Default | Purpose |
|---|---|---|
| `MYGRAMDB_VERSION` | `1.8.0` | Server image tag (`ghcr.io/libraz/mygram-db:<tag>`; e.g. `1.8`, `latest`) |
| `MYSQL_VERSION` | `8.4` | MySQL image tag |
| `MYGRAM_PORT` | `11016` | Host port mapped to the server's TCP API |
| `MYGRAM_HTTP_PORT` | `18080` | Host port mapped to the server's HTTP/health API |
| `KEEP_UP` | `0` | When `1`, leave the stack running after tests (debugging) |

```bash
MYGRAMDB_VERSION=latest make test-e2e-docker
KEEP_UP=1 make test-e2e-docker     # inspect the running stack afterwards
```

## What it exercises

The fixed dataset (`mysql-init/02-seed.sql`) lets the suite assert exact results.
With `MYGRAM_E2E_SEEDED=1` the `TestE2ESeeded` block in `e2e_test.go` checks,
among others:

- database-qualified identity (`testdb.articles`) resolving to the seeded rows,
  and bare/qualified names resolving identically on a single-database server
- multi-word phrase quoting and `enabled = 1` required-filter visibility
- Japanese (ngram) matching
- `SearchRaw` boolean `OR`
- `Facet` aggregation by category
- `SearchWithHighlights` snippet wrapping (server runs with `verify_text: all`)

The version-agnostic v1.7 round-trip checks (`SearchRaw`, `SetVariable` /
`ShowVariables`, `Sync` family) also run without the seed, against any server.

## Files

- `docker-compose.yml` — MySQL + MygramDB services
- `mygramdb.yaml` — server config (replicates `testdb` from the `mysql` service)
- `mysql-init/01-schema.sql` — schema + replication grants
- `mysql-init/02-seed.sql` — deterministic dataset
- `run-e2e.sh` — orchestrates up → test → down (used by `make test-e2e-docker`)
