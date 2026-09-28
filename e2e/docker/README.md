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
MYGRAM_E2E_SEEDED=1 MYGRAM_ADMIN_TOKEN=e2e_admin_token go test -tags=e2e -count=1 ./...
docker compose -f e2e/docker/docker-compose.yml down -v
```

The compose file declares `name: go-mygram-e2e`, so the stack keeps its own
project instead of inheriting the directory name `docker` — a name every other
stack living in a directory called `docker` claims too, which would make `up`
adopt that stack's `mysql` service and `down -v` tear it down.

## Knobs (environment variables)

| Variable | Default | Purpose |
|---|---|---|
| `MYGRAMDB_VERSION` | `1.10.2` | Server image tag (`ghcr.io/libraz/mygram-db:<tag>`; e.g. `1.10`, `latest`) |
| `DB_FLAVOR` | `mysql` | `mysql` or `mariadb`; selects the compose file and seed database |
| `MYSQL_VERSION` | `8.4` | MySQL image tag (used when `DB_FLAVOR=mysql`) |
| `MARIADB_VERSION` | `11.8` | MariaDB image tag (used when `DB_FLAVOR=mariadb`) |
| `MYGRAM_PORT` | `11016` | Host port mapped to the server's TCP API |
| `MYGRAM_HTTP_PORT` | `18080` | Host port mapped to the server's HTTP/health API |
| `MYGRAM_ADMIN_TOKEN` | `e2e_admin_token` | Administrative token; passed to the server and to the tests |
| `KEEP_UP` | `0` | When `1`, leave the stack running after tests (debugging) |

```bash
MYGRAMDB_VERSION=latest make test-e2e-docker
DB_FLAVOR=mariadb make test-e2e-docker   # against MariaDB instead of MySQL
KEEP_UP=1 make test-e2e-docker     # inspect the running stack afterwards
```

## Database version matrix

```bash
# Run the full suite once per target below, sequentially, tearing the stack
# down between each. Non-zero exit if any target fails.
make test-e2e-matrix

# Only a subset
bash e2e/docker/run-matrix.sh --only mysql:8.4,mariadb:11.8
```

Targets (mirrors the server's own `e2e/run-matrix.sh`): `mysql:8.4`, `mysql:9.7`,
`mariadb:10.11`, `mariadb:11.8`, `mariadb:12.3`. Every entry is a supported LTS;
innovation releases are superseded by the next one a quarter later, so they are
reached with `--only` rather than carried in the default list.

## What it exercises

The fixed dataset (`mysql-init/02-seed.sql`) lets the suite assert exact results.
With `MYGRAM_E2E_SEEDED=1` the `TestE2ESeeded` block in `e2e_test.go` checks,
among others:

- database-qualified identity (`testdb.articles`) resolving to the seeded rows,
  and bare/qualified names resolving identically on a single-database server
- multi-word phrase quoting and `enabled = 1` required-filter visibility
- Japanese (ngram) matching
- `SearchRaw` boolean `OR`
- `Facet` aggregation by category, its pagination, and the distinct-value total
- comparison filters (`FilterConditions`) and boolean query mode
- `SearchWithHighlights` snippet wrapping (server runs with `verify_text: all`)

The version-agnostic round-trip checks (`SearchRaw`, `SetVariable` /
`ShowVariables`, `Sync` family) also run without the seed, against any server.

Independent of the seed, the suite asserts the v1.10 protocol surface against
whatever server it reaches:

- `INFO` readiness (`DataInitialized`, `Readiness`, `Ready()`)
- the `REPLICATION STATUS` diagnostics — state, CRC errors, schema
  compatibility, last failure code and the applied-event lag, whose `-1`
  means no event has been applied rather than a lag of zero
- an unknown table rejected as a `*ServerError` carrying
  `ErrCodeTableNotFound`, with the numeric code kept out of the message
- administrative authentication, when a token is configured: ordinary search
  needs none, an administrative command without one is refused with
  `ErrCodePermissionDenied`, `Authenticate` upgrades an already-open
  connection, a wrong token fails the connect instead of half-opening it, and
  an administrative command succeeds over a pooled connection

A server the client cannot reach fails the suite here rather than skipping it:
this harness booted one and waited for it, so skipping every test would exit 0
and read as a pass.

## Administrative authentication

From v1.10 the server requires `api.admin_token` because these listeners bind to
every container interface, and it gates `DUMP`, `SYNC`, `REPLICATION`, `CONFIG`,
`CACHE`, `OPTIMIZE`, `DEBUG`, `SET` and `SHOW VARIABLES` behind `AUTH`. The
compose file passes `MYGRAM_ADMIN_TOKEN` to the server as
`MYGRAM_API_ADMIN_TOKEN` and `run-e2e.sh` hands the same value to the tests,
which set it as `ClientConfig.AdminToken`. Running the suite by hand against
this stack needs the variable exported too:

```bash
MYGRAM_E2E_SEEDED=1 MYGRAM_ADMIN_TOKEN=e2e_admin_token go test -tags=e2e -count=1 ./...
```

`network.allow_cidrs` covers loopback and the docker bridge range rather than
every address: a universal allow list is rejected outright from v1.10 when the
API does not bind to loopback, and a published port presents the bridge gateway
address rather than the host's.

## Files

- `docker-compose.yml` — MySQL + MygramDB services
- `docker-compose.mariadb.yml` — MariaDB + MygramDB services (`DB_FLAVOR=mariadb`)
- `mygramdb.yaml` — server config (replicates `testdb` from the `mysql` service)
- `mygramdb-mariadb.yaml` — same config, pointed at the `mariadb` service
- `mysql-init/01-schema.sql` — schema + replication grants
- `mysql-init/02-seed.sql` — deterministic dataset
- `mariadb-init/01-schema.sql` — same schema minus the MySQL-only FULLTEXT ngram index
- `mariadb-init/02-seed.sql` — same deterministic dataset
- `run-e2e.sh` — orchestrates up → test → down (used by `make test-e2e-docker`)
- `run-matrix.sh` — runs `run-e2e.sh` once per database version target (used by `make test-e2e-matrix`)
