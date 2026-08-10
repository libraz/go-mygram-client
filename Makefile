.PHONY: all test test-race test-e2e test-e2e-docker lint fmt format vet check coverage clean

all: check test

test:
	go test ./... -count=1

test-race:
	go test -race ./... -count=1

# Run e2e tests against a live MygramDB server.
# Override the endpoint via MYGRAM_HOST / MYGRAM_PORT (defaults: 127.0.0.1:11016).
# A server with api.admin_token set (mandatory from v1.10 on a non-loopback
# bind) needs MYGRAM_ADMIN_TOKEN so administrative commands can AUTH.
# To skip the dump or replication round-trip, set
#   MYGRAM_E2E_SKIP_DUMP=1 / MYGRAM_E2E_SKIP_REPLICATION=1
test-e2e:
	go test -tags=e2e -count=1 -v ./...

# Spin up a throwaway MySQL + MygramDB stack (docker compose), run the e2e
# suite against it with the deterministic seed, then tear it down.
# Knobs: MYGRAMDB_VERSION, MYSQL_VERSION, MYGRAM_PORT, MYGRAM_HTTP_PORT,
#        MYGRAM_ADMIN_TOKEN, KEEP_UP.
test-e2e-docker:
	bash e2e/docker/run-e2e.sh

lint:
	golangci-lint run ./...

# Resolve goimports from PATH, then GOPATH/bin, falling back to `go run`
# so `make fmt` works regardless of the developer's PATH setup.
GOIMPORTS ?= $(shell command -v goimports 2>/dev/null || ( [ -x "$$(go env GOPATH)/bin/goimports" ] && echo "$$(go env GOPATH)/bin/goimports" ) || echo "go run golang.org/x/tools/cmd/goimports@latest")

fmt:
	gofmt -w .
	$(GOIMPORTS) -w -local github.com/libraz/go-mygram-client .

format: fmt

vet:
	go vet ./...

check: vet lint

coverage:
	go test -coverprofile=coverage.txt -covermode=atomic ./...
	go tool cover -html=coverage.txt -o coverage.html

clean:
	rm -f coverage.txt coverage.html
