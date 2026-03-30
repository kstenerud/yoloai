BINARY := yoloai
VERSION ?= dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

GOFILES := $(shell find . -name '*.go' -not -path './vendor/*')

.PHONY: build test fmt lint tidy-check govulncheck hadolint actionlint check cover integration e2e integration-podman smoketest smoketest-full setcap clean

build: $(BINARY)

$(BINARY): $(GOFILES) go.mod go.sum
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/yoloai

test:
	go test ./...

fmt:
	gofmt -w .

lint:
	@UNFMT=$$(gofmt -l .); \
	if [ -n "$$UNFMT" ]; then \
		echo "gofmt needed:"; echo "$$UNFMT"; exit 1; \
	fi
	GOTOOLCHAIN=$(shell go env GOVERSION) go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.3 run ./...

tidy-check:
	@cp go.mod go.mod.bak && cp go.sum go.sum.bak
	go mod tidy
	@if ! diff -q go.mod go.mod.bak >/dev/null 2>&1 || ! diff -q go.sum go.sum.bak >/dev/null 2>&1; then \
		mv go.mod.bak go.mod; mv go.sum.bak go.sum; \
		echo "go mod tidy produced changes; run 'go mod tidy' and commit the result"; exit 1; \
	fi
	@rm -f go.mod.bak go.sum.bak

govulncheck:
	GOTOOLCHAIN=$(shell go env GOVERSION) go run golang.org/x/vuln/cmd/govulncheck@latest ./...

hadolint:
	docker run --rm -i hadolint/hadolint < runtime/docker/resources/Dockerfile

actionlint:
	go run github.com/rhysd/actionlint/cmd/actionlint@latest

## check: run all CI checks locally (same as PR checks)
check: lint tidy-check govulncheck hadolint actionlint test base-image integration e2e

## cover: show test coverage per package and total
cover:
	@go test -coverprofile=coverage.out ./... 2>&1 | grep -E '^ok|no test' | \
		sed 's/.*yoloai\//  /; s/[[:space:]]*coverage: / /; s/ of statements//' | \
		sort -t' ' -k2 -n; \
	echo ""; \
	go tool cover -func=coverage.out | tail -1; \
	rm -f coverage.out

base-image: build
	./$(BINARY) system build

integration: base-image
	go test -tags=integration -v -count=1 -timeout=10m ./sandbox/ ./runtime/docker/ ./internal/cli/

e2e: base-image
	go test -tags=e2e -v -count=1 -timeout=15m ./test/e2e/

## integration-podman: run Podman integration tests (requires Podman with socket)
integration-podman: build
	@echo "Building base image with Podman..."
	@./$(BINARY) system build --backend=podman
	@echo "Running Podman integration tests..."
	@go test -tags=integration -v -count=1 -timeout=10m ./runtime/podman/

## smoketest: run end-to-end smoke tests, skipping tests for unavailable backends
## VM backends require root (CAP_SYS_ADMIN + write to /var/run/netns/).
## Run as root: sudo -E make smoketest
smoketest: build
	python3 scripts/smoke_test.py --limited --debug

## smoketest-full: run smoke tests, failing if any optional backend is unavailable
## Run as root: sudo -E make smoketest-full
smoketest-full: build
	python3 scripts/smoke_test.py

## setcap: grant capabilities needed for VM backends (must re-run after each build)
setcap: build
	sudo setcap cap_sys_admin,cap_dac_override+ep ./$(BINARY)

clean:
	rm -f $(BINARY)
