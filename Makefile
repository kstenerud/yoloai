BINARY := yoloai
VERSION ?= dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build test lint integration clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/yoloai

test:
	go test ./...

lint:
	golangci-lint run ./...

integration:
	go test -tags=integration -v -count=1 ./internal/sandbox/ ./internal/docker/

clean:
	rm -f $(BINARY)
