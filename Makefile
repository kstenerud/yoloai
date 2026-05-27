BINARY := yoloai
VERSION ?= dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

GOFILES := $(shell find . -name '*.go' -not -path './vendor/*')
EMBEDFILES := $(shell find internal -type f \( -name 'Dockerfile' -o -name '*.sh' -o -name '*.py' -o -name '*.conf' -o -name '*.md' \) -not -path './vendor/*' -not -path '*/__pycache__/*' -not -path '*/tests/*')

.PHONY: build test fmt lint tidy-check govulncheck hadolint actionlint check cover integration e2e integration-podman integration-seatbelt integration-tart python-test python-typecheck setup-dev-python smoketest smoketest-full releasetest setcap clean

build: $(BINARY)

$(BINARY): $(GOFILES) $(EMBEDFILES) go.mod go.sum
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

## hadolint: lint the Dockerfile (skip when neither hadolint CLI nor Docker is available)
## Prefers a local hadolint install; falls back to Docker; skips if neither is usable.
## CI installs hadolint and treats this target as required.
hadolint:
	@if command -v hadolint >/dev/null 2>&1; then \
		hadolint internal/runtime/docker/resources/Dockerfile; \
	elif docker info >/dev/null 2>&1; then \
		docker run --rm -i hadolint/hadolint < internal/runtime/docker/resources/Dockerfile; \
	else \
		echo "hadolint: skipping (install hadolint or start Docker to enable)"; \
	fi

actionlint:
	go run github.com/rhysd/actionlint/cmd/actionlint@latest

## check: run all CI checks locally (same as PR checks)
check: lint tidy-check hadolint actionlint test python-test

## python-test: run pytest on internal/runtime/monitor/tests (skip when pytest absent)
## Detects pytest availability so fresh clones without dev deps still get a
## clean `make check`. CI installs deps via `make setup-dev-python` and
## treats this target as required.
python-test: python-typecheck
	@if python3 -m pytest --version >/dev/null 2>&1; then \
		python3 -m pytest internal/runtime/monitor/tests/ -v; \
	else \
		echo "Python tests skipped (install pytest + mypy via 'make setup-dev-python' to enable)"; \
	fi

## python-typecheck: run mypy --strict on the typed Python surface
python-typecheck:
	@if python3 -m mypy --version >/dev/null 2>&1; then \
		python3 -m mypy --strict internal/runtime/monitor/setup_helpers.py internal/runtime/monitor/tmux_io.py internal/runtime/monitor/tests/; \
	else \
		echo "Python type-check skipped (install mypy via 'make setup-dev-python' to enable)"; \
	fi

## setup-dev-python: install Python dev deps (pytest, mypy) for python-test/python-typecheck
setup-dev-python:
	python3 -m pip install -r internal/runtime/monitor/tests/requirements-dev.txt

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

integration:
	@if docker info >/dev/null 2>&1; then \
		$(MAKE) base-image && \
		go test -tags=integration -v -count=1 -timeout=10m ./internal/sandbox/ ./internal/runtime/docker/ ./internal/cli/; \
	else \
		echo "Docker unavailable — skipping Docker integration tests"; \
	fi
	@if [ "$$(uname -s)" = "Darwin" ] && [ "$$(uname -m)" = "arm64" ]; then \
		echo "Apple Silicon detected — running seatbelt and tart integration tests"; \
		$(MAKE) integration-seatbelt integration-tart; \
	fi

e2e: build
	@if ! docker info >/dev/null 2>&1; then \
		echo "e2e tests require Docker — start Docker Desktop and retry"; exit 1; \
	fi
	./$(BINARY) system build --backend docker
	go test -tags=e2e -v -count=1 -timeout=15m ./test/e2e/

## integration-podman: run Podman integration tests (requires Podman with socket)
##
## Two suites run under YOLOAI_TEST_BACKEND=podman:
##   1. ./internal/runtime/podman/                    — backend-internal tests
##   2. ./internal/cli/ launch/lifecycle subset — CLI flow against podman
##      Catches sandbox-setup.py regressions that only surface on a non-Docker
##      runtime (CI's Docker job won't notice; that's the point of the matrix).
integration-podman: build
	@echo "Building base image with Podman..."
	@./$(BINARY) system build --backend=podman
	@echo "Running Podman runtime tests..."
	@go test -tags=integration -v -count=1 -timeout=10m ./internal/runtime/podman/
	@echo "Running CLI lifecycle subset against Podman..."
	@YOLOAI_TEST_BACKEND=podman go test -tags=integration -v -count=1 -timeout=10m \
		-run '^TestCLI_(StartStop|StartAfterDone)$$' ./internal/cli/

## integration-seatbelt: run Seatbelt integration tests (requires macOS with sandbox-exec)
## On non-macOS platforms the tests skip cleanly via TestMain (exit 0).
## Pair-runs are encouraged on macOS as part of releasetest on Apple Silicon machines.
integration-seatbelt:
	go test -tags=integration -v -count=1 -timeout=5m ./internal/runtime/seatbelt/

## integration-tart: run Tart integration tests (requires macOS with Apple Silicon + tart)
## On platforms without tart the tests skip cleanly via TestMain (exit 0).
## The TestTart_FullVMLifecycle test is gated behind YOLOAI_TEST_TART_VM=1
## because it clones the base image (multi-GB, multi-minute).
integration-tart:
	go test -tags=integration -v -count=1 -timeout=10m ./internal/runtime/tart/

## smoketest: run base-tier smoke tests (docker + containerd-vm / tart)
## VM backends require root (CAP_SYS_ADMIN + write to /var/run/netns/).
## Run as root: sudo -E make smoketest
smoketest: build
	python3 scripts/smoke_test.py --debug $(SMOKE_ARGS)

## smoketest-full: run full-tier smoke tests (all backends including podman, gVisor)
## Automatically escalates to root on Linux (preserving PATH and env).
smoketest-full: build
	@if [ "$$(uname)" = "Linux" ] && [ "$$(id -u)" != "0" ]; then \
		echo "==> Escalating to root for full smoke tests..."; \
		exec sudo -E PATH="$$PATH" $(MAKE) smoketest-full SMOKE_ARGS="$(SMOKE_ARGS)"; \
	else \
		python3 scripts/smoke_test.py --full --debug $(SMOKE_ARGS); \
	fi

## releasetest: run every test tier, fastest first
## Runs: lint → unit → integration → e2e → podman integration → full smoke
## Automatically escalates to root on Linux for smoke tests.
releasetest: check integration e2e integration-podman smoketest-full

## setcap: grant capabilities needed for VM backends (must re-run after each build)
setcap: build
	sudo setcap cap_sys_admin,cap_dac_override+ep ./$(BINARY)

clean:
	rm -f $(BINARY)
