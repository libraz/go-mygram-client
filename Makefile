.PHONY: all test test-race test-e2e lint fmt vet check coverage clean

all: check test

test:
	go test ./... -count=1

test-race:
	go test -race ./... -count=1

# Run e2e tests against a live MygramDB server.
# Override the endpoint via MYGRAM_HOST / MYGRAM_PORT (defaults: 127.0.0.1:11016).
# To skip the dump or replication round-trip, set
#   MYGRAM_E2E_SKIP_DUMP=1 / MYGRAM_E2E_SKIP_REPLICATION=1
test-e2e:
	go test -tags=e2e -count=1 -v ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .
	goimports -w -local github.com/libraz/go-mygram-client .

vet:
	go vet ./...

check: vet lint

coverage:
	go test -coverprofile=coverage.txt -covermode=atomic ./...
	go tool cover -html=coverage.txt -o coverage.html

clean:
	rm -f coverage.txt coverage.html
