BINARY := yoloai
VERSION ?= dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

# Python dev tooling lives in a uv-managed venv so mypy/pytest run at
# lockfile-pinned versions (requirements-dev.lock), decoupled from whatever
# ambient `python3 -m mypy` happens to be installed. See setup-dev-python.
VENV := .venv
MYPY := $(VENV)/bin/mypy
PYTEST := $(VENV)/bin/pytest
PY_REQ_LOCK := runtime/monitor/tests/requirements-dev.lock

# Resolve the Docker endpoint from the active docker context when DOCKER_HOST is
# not already set. `docker context use` retargets the docker CLI, but the Go SDK
# and the HOME-isolating integration harness only honor DOCKER_HOST — so a stale
# /var/run/docker.sock symlink (e.g. after switching OrbStack <-> Docker Desktop)
# would otherwise break the docker test tiers. Empty (docker absent / no context)
# degrades harmlessly to the SDK default socket. Exported only for the targets
# that actually talk to Docker, so `build`/`check`/`integration-podman` are
# unaffected and pay no `docker` invocation cost. Recursive `=` defers the shell
# call until one of these targets runs.
# DOCKER_HOST_ENV snapshots any caller-provided DOCKER_HOST at parse time (`:=`)
# so DOCKER_HOST_RESOLVED can reference it without the target-specific DOCKER_HOST
# referencing itself. Recursive `=` on RESOLVED defers the `docker` call until a
# listed target actually runs.
DOCKER_HOST_ENV := $(DOCKER_HOST)
DOCKER_HOST_RESOLVED = $(if $(DOCKER_HOST_ENV),$(DOCKER_HOST_ENV),$(shell docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null))
integration e2e base-image smoketest smoketest-full: export DOCKER_HOST = $(DOCKER_HOST_RESOLVED)

.PHONY: build test fmt lint vet-tagged crosscheck tidy-check govulncheck hadolint actionlint check cover integration e2e integration-podman integration-containerd integration-apple integration-seatbelt integration-tart python-test python-typecheck ensure-python-venv setup-dev-python smoketest smoketest-full releasetest setcap clean

# Always invoke `go build` and let it decide whether to relink. `go build` does
# complete, authoritative dependency tracking — crucially including //go:embed'd
# files. A Make prerequisite list can't safely replicate that: it previously
# globbed embed files by extension (Dockerfile/*.sh/*.py/*.conf/*.md) and
# silently missed new embed types (e.g. a *.js plugin), serving a stale binary.
# go's build cache makes a no-op build effectively instant, so always running it
# costs nothing and is never stale.
build:
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

## govulncheck: scan for known vulnerabilities, applying a self-policing
## allowlist of unfixable-today findings that auto-fails once a fix ships
## (see scripts/govulncheck.py).
govulncheck:
	python3 scripts/govulncheck.py

## hadolint: lint the Dockerfile (skip when neither hadolint CLI nor Docker is available)
## Prefers a local hadolint install; falls back to Docker; skips if neither is usable.
## CI installs hadolint and treats this target as required.
hadolint:
	@if command -v hadolint >/dev/null 2>&1; then \
		hadolint runtime/docker/resources/Dockerfile; \
	elif docker info >/dev/null 2>&1; then \
		docker run --rm -i hadolint/hadolint < runtime/docker/resources/Dockerfile; \
	else \
		echo "hadolint: skipping (install hadolint or start Docker to enable)"; \
	fi

actionlint:
	go run github.com/rhysd/actionlint/cmd/actionlint@latest

## vet-tagged: typecheck every _test.go behind the integration & e2e build tags
## WITHOUT running them. The default build (and `go build -tags ...`) skips
## tagged test files, so a broken reference in an integration/e2e test — e.g. a
## deleted helper — used to surface only in `make releasetest` (which spins up
## daemons/VMs). `go vet` compiles the tagged test binaries but never executes
## them, so this stays hermetic and fast while catching those build breaks early.
vet-tagged:
	go vet -tags 'integration e2e' ./...

## crosscheck: cross-compile + typecheck the whole tree (incl. the cmd/yoloai
## binary AND every _test.go) for macOS — the non-host platform. `make check`
## otherwise only builds for the host (linux), so a darwin-only break — a backend
## whose Go deps build only on linux, an unconditional import of one, a //go:build
## typo — used to surface only when someone ran `make releasetest` on a Mac.
## `go vet` cross-compiles every package and test file for darwin without
## executing them (a darwin binary can't run on linux), so it stays hermetic and
## fast. Windows is not a target (the project runs under WSL = linux; docker/tart
## are //go:build !windows), so only darwin is cross-checked.
crosscheck:
	GOOS=darwin GOARCH=arm64 go vet ./...

## check: run all CI checks locally (same as PR checks)
check: lint vet-tagged crosscheck tidy-check hadolint actionlint test python-test

## ensure-python-venv: provision the uv-managed venv on demand (idempotent).
## When uv is present this syncs the lockfile-pinned tools into .venv; when uv
## is absent it does nothing, so the python-* targets below skip gracefully and
## a fresh clone without uv still gets a green `make check`. uv pip sync is
## near-instant once the venv already matches the lock, so running this on every
## `make check` is cheap and removes any need to call setup-dev-python by hand.
ensure-python-venv:
	@if command -v uv >/dev/null 2>&1; then \
		[ -x $(VENV)/bin/python ] || uv venv --quiet $(VENV); \
		uv pip sync --quiet --python $(VENV)/bin/python $(PY_REQ_LOCK); \
	fi

## python-test: run pytest from the uv-managed venv (skip when venv absent)
python-test: python-typecheck
	@if [ -x $(PYTEST) ]; then \
		$(PYTEST) runtime/monitor/tests/ -v; \
		$(PYTEST) scripts/tests/ -v; \
	else \
		echo "Python tests skipped (install uv to enable: https://docs.astral.sh/uv/)"; \
	fi

## python-typecheck: run mypy --strict from the uv-managed venv on the typed surface
## Two invocations: the monitor surface and the smoke harness each have their
## own tests/conftest.py, which mypy would otherwise reject as a duplicate
## top-level "conftest" module if checked in one pass.
python-typecheck: ensure-python-venv
	@if [ -x $(MYPY) ]; then \
		$(MYPY) --strict runtime/monitor/setup_helpers.py runtime/monitor/tmux_io.py runtime/monitor/tests/; \
		$(MYPY) --strict scripts/smoke_test.py scripts/govulncheck.py scripts/tests/; \
	else \
		echo "Python type-check skipped (install uv to enable: https://docs.astral.sh/uv/)"; \
	fi

## setup-dev-python: explicitly provision the uv-managed venv with lockfile-pinned
## dev tools. Optional for local dev (the python-* targets self-provision via
## ensure-python-venv); kept for CI and as an explicit "set it up now" entry that
## fails loudly if uv is missing. Regenerate the lock after editing
## requirements-dev.txt: uv pip compile --generate-hashes requirements-dev.txt -o requirements-dev.lock
setup-dev-python:
	@command -v uv >/dev/null 2>&1 || { echo "uv is required: install from https://docs.astral.sh/uv/"; exit 1; }
	uv venv --clear $(VENV)
	uv pip sync --python $(VENV)/bin/python $(PY_REQ_LOCK)

## cover: show test coverage per package and total
cover:
	@go test -coverprofile=coverage.out ./... 2>&1 | grep -E '^ok|no test' | \
		sed 's/.*yoloai\//  /; s/[[:space:]]*coverage: / /; s/ of statements//' | \
		sort -t' ' -k2 -n; \
	echo ""; \
	go tool cover -func=coverage.out | tail -1; \
	rm -f coverage.out

base-image: build
	./$(BINARY) system build --backend docker

## integration: run every backend's integration suite. Each backend self-skips
## when its daemon/host isn't available (TestMain gate / skipIfNotAvailable), so
## the same target runs on any host and exercises whatever that host supports —
## the host-aware contract. Docker additionally drives the cross-cutting sandbox/
## and cli/ suites, which require a real container daemon.
integration:
	@if docker info >/dev/null 2>&1; then \
		$(MAKE) base-image && \
		go test -tags=integration -v -count=1 -timeout=10m ./internal/orchestrator/ ./runtime/docker/ ./internal/cli/; \
	else \
		echo "Docker unavailable — skipping Docker integration tests"; \
	fi
	@$(MAKE) integration-containerd integration-apple integration-seatbelt integration-tart

e2e: build
	@if ! docker info >/dev/null 2>&1; then \
		echo "e2e tests require Docker — start Docker Desktop and retry"; exit 1; \
	fi
	./$(BINARY) system build --backend docker
	go test -tags=e2e -v -count=1 -timeout=15m ./test/e2e/

## integration-podman: run Podman integration tests (requires Podman with socket)
##
## Two suites run under YOLOAI_TEST_BACKEND=podman:
##   1. ./runtime/podman/                    — backend-internal tests
##   2. ./internal/cli/ launch/lifecycle subset — CLI flow against podman
##      Catches sandbox-setup.py regressions that only surface on a non-Docker
##      runtime (CI's Docker job won't notice; that's the point of the matrix).
integration-podman: build
	@echo "Building base image with Podman..."
	@./$(BINARY) system build --backend=podman
	@echo "Running Podman runtime tests..."
	@go test -tags=integration -v -count=1 -timeout=10m ./runtime/podman/
	@echo "Running CLI lifecycle subset against Podman..."
	@YOLOAI_TEST_BACKEND=podman go test -tags=integration -v -count=1 -timeout=10m \
		-run '^TestCLI_(StartStop|StartAfterDone)$$' ./internal/cli/

## integration-containerd: run containerd (Kata VM) integration tests.
## Linux-only: the real tests are `//go:build integration && linux`. On non-Linux
## hosts a stub TestMain (integration_main_test.go) exits 0 so this target runs
## everywhere. On Linux, skipIfNotAvailable skips cleanly when the containerd
## socket is absent/unconnectable or the host can't create network namespaces.
integration-containerd:
	go test -tags=integration -v -count=1 -timeout=10m ./runtime/containerd/

## integration-apple: run Apple `container` backend integration tests (requires
## macOS 26 + Apple Silicon + the `container` CLI). On any other host the suite
## skips cleanly via TestMain. The full base-image build is gated behind
## YOLOAI_TEST_APPLE_BASE=1 (slow); the conformance + lifecycle tests use a tiny
## alpine sleep image and run whenever the backend is available.
integration-apple:
	go test -tags=integration -v -count=1 -timeout=15m ./runtime/apple/

## integration-seatbelt: run Seatbelt integration tests (requires macOS with sandbox-exec)
## On non-macOS platforms the tests skip cleanly via TestMain (exit 0).
## Pair-runs are encouraged on macOS as part of releasetest on Apple Silicon machines.
integration-seatbelt:
	go test -tags=integration -v -count=1 -timeout=5m ./runtime/seatbelt/

## integration-tart: run Tart integration tests (requires macOS with Apple Silicon + tart).
## On platforms without tart the tests skip cleanly via TestMain (exit 0).
## The heavyweight TestTartConformance suite clones a multi-GB macOS VM per subtest,
## so it is gated behind YOLOAI_TEST_TART_VM=1 (skipped for a quick `make
## integration-tart`). `make releasetest` sets it, so the release gate runs the
## full suite — building the tart base first so a missing base fails loudly rather
## than silently skipping. The base build only runs on macOS + Apple Silicon (where
## tart can actually run); on any other host it is skipped and the go test self-skips
## via TestMain, keeping this target runnable everywhere like the other backends.
integration-tart:
	@if [ "$$YOLOAI_TEST_TART_VM" = "1" ] && [ "$$(uname)" = "Darwin" ] && [ "$$(uname -m)" = "arm64" ]; then \
		$(MAKE) build && \
		echo "Building tart base image for the conformance suite..." && \
		./$(BINARY) system build --backend tart; \
	fi
	go test -tags=integration -v -count=1 -timeout=40m ./runtime/tart/

## smoketest: run base-tier smoke tests (docker + containerd-vm / tart)
## VM backends require root (CAP_SYS_ADMIN + write to /var/run/netns/).
## Run as root: sudo -E make smoketest
smoketest: build
	python3 scripts/smoke_test.py --debug $(SMOKE_ARGS)

## smoketest-full: run full-tier smoke tests (all backends including podman, gVisor)
## across every installed docker provider (macOS: OrbStack + Docker Desktop;
## errors if an installed provider isn't running). Single-provider hosts (Linux)
## run once. Automatically escalates to root on Linux (preserving PATH and env).
smoketest-full: build
	@if [ "$$(uname)" = "Linux" ] && [ "$$(id -u)" != "0" ]; then \
		echo "==> Escalating to root for full smoke tests..."; \
		exec sudo -E PATH="$$PATH" $(MAKE) smoketest-full SMOKE_ARGS="$(SMOKE_ARGS)"; \
	else \
		python3 scripts/smoke_test.py --full --debug --all-docker-providers $(SMOKE_ARGS); \
	fi

## releasetest: run every test tier, fastest first
## Runs: lint → unit → integration → e2e → podman integration → full smoke
## Automatically escalates to root on Linux for smoke tests.
## The release gate exercises everything the host supports: these exports flip on
## the heavyweight macOS-VM paths (tart conformance clones a real base VM per
## subtest; apple builds its real yoloai-base instead of a sleep image). They
## propagate through the recursive sub-makes; on a host without tart/apple the
## suites still skip cleanly via TestMain.
releasetest: export YOLOAI_TEST_TART_VM := 1
releasetest: export YOLOAI_TEST_APPLE_BASE := 1
releasetest: check integration e2e integration-podman smoketest-full

## setcap: grant capabilities needed for VM backends (must re-run after each build)
setcap: build
	sudo setcap cap_sys_admin,cap_dac_override+ep ./$(BINARY)

clean:
	rm -f $(BINARY)
