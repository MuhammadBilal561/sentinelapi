# Optional convenience wrapper. GNU Make is NOT required: every target below
# is a plain Go command that you can run directly (see README.md).

BINARY := bin/sentinelapi

.PHONY: all fmt fmt-check vet test race build run check

all: check

fmt:
	gofmt -w .

fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "files need gofmt:"; gofmt -l .; exit 1)

vet:
	go vet ./...

test:
	go test ./...

# Needs cgo and a C compiler (on Windows, for example, MinGW-w64 gcc).
race:
	go test -race ./...

build:
	go build -o $(BINARY) ./cmd/sentinelapi

run:
	go run ./cmd/sentinelapi

check: fmt-check vet test build