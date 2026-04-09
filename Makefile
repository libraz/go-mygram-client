.PHONY: all test test-race lint fmt vet check coverage clean

all: check test

test:
	go test ./... -count=1

test-race:
	go test -race ./... -count=1

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
