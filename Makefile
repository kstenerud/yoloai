BINARY := yoloai
VERSION ?= dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build test fmt lint tidy-check check integration clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/yoloai

test:
	go test ./...

fmt:
	gofmt -w .

lint:
	@UNFMT=$$(gofmt -l .); \
	if [ -n "$$UNFMT" ]; then \
		echo "gofmt needed:"; echo "$$UNFMT"; exit 1; \
	fi
	golangci-lint run ./...

tidy-check:
	@cp go.mod go.mod.bak && cp go.sum go.sum.bak
	go mod tidy
	@if ! diff -q go.mod go.mod.bak >/dev/null 2>&1 || ! diff -q go.sum go.sum.bak >/dev/null 2>&1; then \
		mv go.mod.bak go.mod; mv go.sum.bak go.sum; \
		echo "go mod tidy produced changes; run 'go mod tidy' and commit the result"; exit 1; \
	fi
	@rm -f go.mod.bak go.sum.bak

## check: run all CI checks locally (same as PR checks)
check: lint tidy-check test

integration:
	go test -tags=integration -v -count=1 ./internal/sandbox/ ./internal/docker/

clean:
	rm -f $(BINARY)
