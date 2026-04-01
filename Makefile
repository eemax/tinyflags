.PHONY: build install run test tidy clean vet fmt lint check

BINARY ?= tinyflags
CMD_PKG := ./cmd/tinyflags

VERSION_PKG := github.com/eemax/tinyflags/internal/version
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE        ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -ldflags "-X $(VERSION_PKG).Version=$(VERSION) -X $(VERSION_PKG).Commit=$(COMMIT) -X $(VERSION_PKG).Date=$(DATE)"

build:
	go build $(LDFLAGS) -o $(BINARY) $(CMD_PKG)

install:
	go install $(LDFLAGS) $(CMD_PKG)

run:
	go run $(CMD_PKG) $(ARGS)

test:
	go vet ./...
	go test ./...

tidy:
	go mod tidy

clean:
	rm -f $(BINARY)
	rm -f cover.out

vet:
	go vet ./...

fmt:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)

lint:
	@which staticcheck > /dev/null 2>&1 && staticcheck ./... || echo "staticcheck not installed, skipping"

check: vet fmt test
