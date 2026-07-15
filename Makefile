BINARY := yoloai
VERSION ?= dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

# Under `sudo make ...`, PATH is reset to the sudoers secure_path, which drops
# tools installed outside it: the Go toolchain (/usr/local/go/bin) and user-local
# tools like uv (~/.local/bin). Without recovery, `sudo make releasetest` dies
# with "go: No such file" or "uv is required". When invoked under sudo (SUDO_USER
# set), append the Go toolchain dirs plus the INVOKING user's local bins (resolved
# via SUDO_USER's passwd home, since HOME is /root under sudo) so those tools
# resolve. Appended, not prepended, so a tool already in secure_path still wins; a
# non-sudo build is untouched. A tool installed somewhere exotic still needs the
# explicit `sudo -E PATH="$$PATH" make ...`.
ifneq ($(SUDO_USER),)
SUDO_USER_HOME := $(shell getent passwd "$(SUDO_USER)" 2>/dev/null | cut -d: -f6)
export PATH := $(PATH):/usr/local/go/bin:/usr/lib/go/bin:/opt/go/bin:$(SUDO_USER_HOME)/.local/bin:$(SUDO_USER_HOME)/.cargo/bin:$(SUDO_USER_HOME)/go/bin
endif

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
integration e2e base-image smoketest smoketest-quick: export DOCKER_HOST = $(DOCKER_HOST_RESOLVED)

.PHONY: build test fmt lint lint-cross vet-tagged crosscheck tidy-check govulncheck hadolint shellcheck actionlint lint-commits check cover integration e2e integration-podman integration-containerd integration-apple integration-seatbelt integration-tart python-test python-typecheck ensure-python-venv setup-dev-python smoketest smoketest-quick releasetest setcap clean clean-testtmp

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

# LINT_PLATFORMS: every GOOS/GOARCH whose build-tagged code must pass `go vet` +
# golangci-lint. The host platform is covered by `lint` + `test`; `crosscheck` and
# `lint-cross` additionally vet/lint each OTHER GOOS in this list (host-native
# tooling, GOOS/GOARCH pointed at the target), so an issue in a //go:build <goos>
# file is caught whichever OS runs `make check` — not just on a Linux host. Add a
# platform here once the tree builds for it and the cross targets pick it up.
#
# windows/amd64 is intentionally omitted: the tree still calls syscall.Kill /
# Setsid / Setpgid unconditionally (internal/broker, runtime/tart, runtime/seatbelt),
# so it does not compile for Windows. Guarding those behind build tags with Windows
# stubs is the prerequisite for Windows sandbox support; add windows/amd64 here once
# that lands and the cross vet/lint will start enforcing it.
LINT_PLATFORMS := linux/amd64 darwin/arm64

lint:
	@UNFMT=$$(gofmt -l .); \
	if [ -n "$$UNFMT" ]; then \
		echo "gofmt needed:"; echo "$$UNFMT"; exit 1; \
	fi
	GOTOOLCHAIN=$(shell PATH="$(PATH)" go env GOVERSION 2>/dev/null) go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.3 run ./...

## lint-cross: run golangci-lint against each non-host GOOS in LINT_PLATFORMS, so
## lint rules (forbidigo, etc.) on //go:build <goos> files are ENFORCED regardless
## of which OS runs the gate — golangci-lint honours build constraints, so the
## host-GOOS `lint` above skips other platforms' files (a forbidigo violation in a
## *_darwin.go file once passed `make check` on Linux and failed only on a native-mac
## run — DF78; the mirror hole hid linux-only issues on a Mac). `go run` under a cross
## GOOS would cross-build the linter itself (exec format error), so install it
## host-native to a temp GOBIN and point only its ANALYSIS at the target GOOS/GOARCH.
lint-cross:
	@hostos="$$(go env GOOS)"; \
	tmp="$$(mktemp -d)"; \
	GOBIN="$$tmp" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.3 || { rm -rf "$$tmp"; exit 1; }; \
	rc=0; \
	for plat in $(LINT_PLATFORMS); do \
		goos="$${plat%/*}"; goarch="$${plat#*/}"; \
		[ "$$goos" = "$$hostos" ] && continue; \
		echo ">> golangci-lint $$goos/$$goarch"; \
		GOOS="$$goos" GOARCH="$$goarch" "$$tmp/golangci-lint" run ./... || rc=1; \
	done; \
	rm -rf "$$tmp"; exit $$rc

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

## hadolint: lint the Dockerfile. The Dockerfile ships in the binary and builds
## every sandbox image, so this is REQUIRED, not optional (D112) — its absence
## FAILS loudly rather than skipping. It used to `echo "skipping"` and exit 0
## while claiming CI treated it as required; no CI install step had ever existed,
## and it only ran at all because GitHub's ubuntu-latest happens to ship Docker.
## A gate that can silently not run is not a gate (D117).
## Prefers a local hadolint; falls back to Docker, which needs no install.
hadolint:
	@if command -v hadolint >/dev/null 2>&1; then \
		hadolint runtime/docker/resources/Dockerfile; \
	elif docker info >/dev/null 2>&1; then \
		docker run --rm -i hadolint/hadolint < runtime/docker/resources/Dockerfile; \
	else \
		echo "ERROR: hadolint is required (the Dockerfile ships in the binary; D112)."; \
		echo "       Install hadolint (https://github.com/hadolint/hadolint) or start Docker."; \
		exit 1; \
	fi

## shellcheck: lint every tracked shell script. Required, not optional (D112) —
## runtime/monitor/*.sh and the docker entrypoint ship inside the binary, and
## shell.md asserts these pass clean. Before D117 that assertion was ungated
## prose: true when written, with nothing to keep it true. Prefers a local
## shellcheck; falls back to Docker.
shellcheck:
	@scripts_list="$$(git ls-files '*.sh')"; \
	if command -v shellcheck >/dev/null 2>&1; then \
		shellcheck $$scripts_list; \
	elif docker info >/dev/null 2>&1; then \
		docker run --rm -v "$(CURDIR)":/mnt -w /mnt koalaman/shellcheck:stable $$scripts_list; \
	else \
		echo "ERROR: shellcheck is required (shell scripts ship in the binary; D112)."; \
		echo "       Install shellcheck (https://www.shellcheck.net/) or start Docker."; \
		exit 1; \
	fi

## lint-commits: check this branch's commit messages against AGENTS.md.
## Deliberately NOT a `check` prerequisite: it needs a base ref to diff against,
## which `make check` has no notion of (on main the range is empty and the check
## is vacuous). CI runs it on every PR; this target is for checking before you
## push. Override the base with `make lint-commits BASE=upstream/main`.
BASE ?= origin/main
lint-commits:
	python3 scripts/lint_commits.py --base $(BASE) --head HEAD

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

## crosscheck: `go vet` (cross-compile + typecheck the whole tree, incl. the
## cmd/yoloai binary AND every _test.go) for each non-host GOOS in LINT_PLATFORMS.
## `make check` otherwise builds only for the host, so a break confined to another
## platform — a backend whose Go deps build only on one OS, an unconditional import
## of one, a //go:build typo — would surface only when someone ran the gate on that
## OS. `go vet` cross-compiles without executing (a foreign binary can't run here),
## so it stays hermetic and fast. See LINT_PLATFORMS for the platform set (and why
## Windows isn't in it yet).
crosscheck:
	@hostos="$$(go env GOOS)"; \
	for plat in $(LINT_PLATFORMS); do \
		goos="$${plat%/*}"; goarch="$${plat#*/}"; \
		[ "$$goos" = "$$hostos" ] && continue; \
		echo ">> go vet $$goos/$$goarch"; \
		GOOS="$$goos" GOARCH="$$goarch" go vet ./... || exit 1; \
	done

## check: the local gate. NOT all of CI — CI also runs the integration and
## integration-podman jobs, and `test` here is a bare `go test ./...` which skips
## every build-tagged file. See docs/contributors/procedures/pull-requests.md.
check: lint lint-cross vet-tagged crosscheck tidy-check hadolint shellcheck actionlint test python-test

## ensure-python-venv: provision the uv-managed venv on demand (idempotent).
## The Python surface is part of the app (contributors can modify it), so it is
## REQUIRED, not optional (D112): uv is mandatory and its absence FAILS loudly —
## `uv`/`python3` install everywhere including CI, so they get no carve-out. uv pip
## sync is near-instant once the venv already matches the lock, so running this on
## every `make check` is cheap and removes any need to call setup-dev-python by hand.
ensure-python-venv:
	@command -v uv >/dev/null 2>&1 || { echo "ERROR: uv is required (Python is part of the app; D112). Install: https://docs.astral.sh/uv/"; exit 1; }
	@[ -x $(VENV)/bin/python ] || uv venv --quiet $(VENV)
	@uv pip sync --quiet --python $(VENV)/bin/python $(PY_REQ_LOCK)

## python-test: run pytest from the uv-managed venv (uv required; see D112)
python-test: python-typecheck
	$(PYTEST) runtime/monitor/tests/ -v
	$(PYTEST) scripts/tests/ -v

## python-typecheck: run mypy --strict from the uv-managed venv over EVERY tracked
## .py under runtime/ and scripts/.
##
## The file list is DERIVED, never hand-maintained. A hand-maintained list is
## precisely how five //go:embed'ed scripts — entrypoint.py, firewall.py,
## install-firewall.py, sandbox-setup.py, status-monitor.py — shipped inside every
## sandbox for months with no typecheck, no test, and not even a syntax check,
## while python.md claimed the opposite (D117). Anything added under runtime/ or
## scripts/ is now covered the moment it is tracked.
##
## Scope note: docs/**/*.py is excluded on purpose — the only one is a research
## spike (design/research/egress-broker-spike/mock-anthropic.py) that no code
## references and nothing ships.
##
## Two invocations: the monitor surface and the smoke harness each have their
## own tests/conftest.py, which mypy would otherwise reject as a duplicate
## top-level "conftest" module if checked in one pass.
## Lazy (`=`, not `:=`) so the git call happens only when this target runs,
## rather than on every `make` invocation including `make build`.
PY_RUNTIME = $(shell git ls-files '*.py' | grep '^runtime/')
PY_SCRIPTS = $(shell git ls-files '*.py' | grep '^scripts/')

python-typecheck: ensure-python-venv
	$(MYPY) --strict $(PY_RUNTIME)
	$(MYPY) --strict scripts/smoke_test.py scripts/govulncheck.py scripts/tests/

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

# require-root-for-containerd: fail fast, upfront, when a target runs the
# containerd/Kata integration suite but isn't root. Kata needs CAP_SYS_ADMIN to
# create the network namespace, so a non-root run can only end in an opaque
# "operation not permitted" deep in the suite — stop now with an actionable
# message instead (the maintainer's rule: a sudo-requiring target must say so, not
# run-to-inevitable-failure and not silently skip). Linux-only: containerd is
# Linux-only (the macOS stub TestMain exits 0), and non-Linux hosts need no root
# here. Skipped when containerd is carved out via YOLOAI_TEST_UNCONTROLLED_BACKENDS
# — then the backend won't run at all, so root isn't required. $@ is the invoking
# target, so the suggested commands are exact.
define require-root-for-containerd
	@if [ "$$(uname)" = "Linux" ] && [ "$$(id -u)" != "0" ] && \
	    ! printf '%s' "$$YOLOAI_TEST_UNCONTROLLED_BACKENDS" | tr ',' '\n' | grep -qx containerd; then \
		echo "ERROR: '$@' includes the containerd/Kata integration suite, which needs root"; \
		echo "       (CAP_SYS_ADMIN — to create a network namespace). Refusing to run it as a"; \
		echo "       non-root user, which could only fail deep in the suite."; \
		echo "  Re-run under sudo, preserving env (credentials + PATH for the Go toolchain):"; \
		echo "      sudo -E make $@"; \
		echo "  Or, only on a machine that genuinely cannot provide containerd, carve it out:"; \
		echo "      YOLOAI_TEST_UNCONTROLLED_BACKENDS=containerd make $@"; \
		exit 1; \
	fi
endef

## integration: run every backend's integration suite. A platform-possible
## backend that's absent FAILS loudly (D112) — never a silent skip; the only
## downgrade is the YOLOAI_TEST_UNCONTROLLED_BACKENDS carve-out (CI steps we don't
## own), which each backend's TestMain honors itself. Docker is mandatory here: it
## drives the cross-cutting sandbox/ and cli/ suites AND builds the base image, so
## `make base-image` fails loudly if the daemon is absent. On Linux this needs root
## for the containerd/Kata suite (netns); it stops upfront with an actionable error
## if run non-root (use `sudo -E make integration`, or carve out containerd).
integration:
	$(require-root-for-containerd)
	$(MAKE) base-image
	go test -tags=integration -v -count=1 -timeout=10m ./internal/orchestrator/ ./runtime/docker/ ./internal/cli/
	@$(MAKE) integration-containerd integration-apple integration-seatbelt integration-tart

e2e: build
	@if ! docker info >/dev/null 2>&1; then \
		echo "e2e tests require Docker — start Docker Desktop and retry"; exit 1; \
	fi
	./$(BINARY) system build --backend docker
	go test -tags=e2e -v -count=1 -timeout=15m ./test/e2e/

## integration-podman: run Podman integration tests (requires Podman with socket)
##
## Three suites run under YOLOAI_TEST_BACKEND=podman:
##   1. ./runtime/podman/                    — backend-internal tests
##   2. ./internal/cli/ launch/lifecycle subset — CLI flow against podman
##      Catches sandbox-setup.py regressions that only surface on a non-Docker
##      runtime (CI's Docker job won't notice; that's the point of the matrix).
##   3. ./internal/orchestrator/ podman tests — rootless-podman brokering + C1
##      containment. These require provisioned rootless podman (slirp host-loopback,
##      keep-id), so they are scoped OUT of the docker `integration` job and run
##      only here, where podman is the owned+provisioned backend.
integration-podman: build
	@if [ "$$(id -u)" = "0" ]; then \
		echo "ERROR: integration-podman exercises ROOTLESS podman (keep-id, slirp host-loopback),"; \
		echo "       which is bound to your login session (systemd --user, DBUS, the podman user"; \
		echo "       socket) — it cannot run as root, and a sudo de-escalation doesn't reproduce"; \
		echo "       the session (volume mounts fail). Run it directly in your own shell:"; \
		echo "           make integration-podman        (NOT via sudo / sudo make releasetest)"; \
		exit 1; \
	fi
	@echo "Building base image with Podman..."
	@./$(BINARY) system build --backend=podman
	@echo "Running Podman runtime tests..."
	@go test -tags=integration -v -count=1 -timeout=10m ./runtime/podman/
	@echo "Running CLI lifecycle subset against Podman..."
	@YOLOAI_TEST_BACKEND=podman go test -tags=integration -v -count=1 -timeout=10m \
		-run '^TestCLI_(StartStop|StartAfterDone)$$' ./internal/cli/
	@echo "Running orchestrator Podman brokering + containment tests..."
	@YOLOAI_TEST_BACKEND=podman go test -tags=integration -v -count=1 -timeout=10m \
		-run '^TestIntegration_(CredentialBroker_Podman|CopyModeMaliciousFilterNoHostExec_Podman|Podman_DirectDeliveryOnMacOS)$$' ./internal/orchestrator/

## integration-containerd: run containerd (Kata VM) integration tests.
## Linux-only: the real tests are `//go:build integration && linux`. On non-Linux
## hosts a stub TestMain (integration_main_test.go) exits 0 so this target runs
## everywhere. On Linux, skipIfNotAvailable skips cleanly when the containerd
## socket is absent/unconnectable or the host can't create network namespaces.
integration-containerd:
	$(require-root-for-containerd)
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

## smoketest: run the full smoke matrix — every backend this host can exercise,
## across every installed docker provider (macOS: OrbStack + Docker Desktop; errors
## if an installed provider isn't running). Single-provider hosts (Linux) run once.
## STRICT (D112): a scheduled backend that's absent FAILS unless carved out via
## YOLOAI_TEST_UNCONTROLLED_BACKENDS. VM backends require root (CAP_SYS_ADMIN +
## /var/run/netns/), so this auto-escalates to root on Linux (preserving PATH/env).
smoketest: build
	@if [ "$$(uname)" = "Linux" ] && [ "$$(id -u)" != "0" ]; then \
		echo "==> Escalating to root for the full smoke matrix..."; \
		exec sudo -E PATH="$$PATH" $(MAKE) smoketest SMOKE_ARGS="$(SMOKE_ARGS)"; \
	else \
		python3 scripts/smoke_test.py --debug --all-docker-providers $(SMOKE_ARGS); \
	fi

## smoketest-quick: run only the primary machinery — the docker/container core path
## (create -> agent -> sentinel -> diff/apply). A fast, narrow but honest dev signal;
## STILL STRICT — Docker is primary machinery, so its absence FAILS. No root needed.
smoketest-quick: build
	python3 scripts/smoke_test.py --quick --debug $(SMOKE_ARGS)

## releasetest: run every test tier, fastest first. Run as YOUR user (NOT sudo):
##   make releasetest
## The tiers have mixed privilege needs, so releasetest runs each in its correct
## one. Everything runs as you EXCEPT, on Linux only, integration (containerd/Kata
## netns) and smoketest (VM/netns) escalate to root internally (sudo -E). On macOS
## nothing escalates: every backend — Docker Desktop, apple, seatbelt, tart,
## podman-machine — is a per-user daemon (containerd is Linux-only, compiled out),
## and running them as root breaks bind-mounts of your user-owned temp dirs. The
## rootless-podman tier (integration-podman) always needs your login session
## (keep-id/slirp), which is why the whole gate must NOT be run under `sudo` —
## it escalates only the specific Linux tiers that need it.
## The exports flip on the heavyweight macOS-VM paths (tart conformance clones a
## real base VM per subtest; apple builds its real yoloai-base instead of a sleep
## image); they propagate through the sub-makes (and across sudo -E). On a host
## without tart/apple the suites still skip cleanly via TestMain.
releasetest: export YOLOAI_TEST_TART_VM := 1
releasetest: export YOLOAI_TEST_APPLE_BASE := 1
releasetest:
	@if [ "$$(id -u)" = "0" ]; then \
		echo "ERROR: run 'make releasetest' as your normal user, NOT via sudo — the"; \
		echo "       integration-podman tier needs your login session (rootless podman) and"; \
		echo "       cannot run as root. The root-only tiers escalate themselves."; \
		exit 1; \
	fi
	$(MAKE) check
	@if [ "$$(uname)" = "Linux" ]; then \
		echo "==> integration needs root on Linux (containerd/Kata netns) — escalating for this tier..."; \
		sudo -E PATH="$(PATH)" $(MAKE) integration; \
	else \
		$(MAKE) integration; \
	fi
	$(MAKE) e2e
	$(MAKE) integration-podman
	$(MAKE) smoketest

## setcap: grant capabilities needed for VM backends (must re-run after each build)
setcap: build
	sudo setcap cap_sys_admin,cap_dac_override+ep ./$(BINARY)

clean: clean-testtmp
	rm -f $(BINARY)

## clean-testtmp: remove integration/e2e bootstrap HOMEs left in TMPDIR by a
## test run that was killed before its cleanup ran (SIGKILL / -timeout / OOM —
## none run deferred cleanup). Normal runs clean up after themselves; this is the
## deterministic sweep for leftovers. Safe: only yoloai-prefixed temp dirs.
##
## A cli HOME may contain a rootless-podman store whose files are owned by
## uid-remapped (userns) ids, so a plain `rm` hits "Permission denied"; remove
## those via `podman unshare` (which enters the user namespace) when podman is
## available, then a plain rm for the rest. All best-effort (|| true).
clean-testtmp:
	@rm -rf "$${TMPDIR:-/tmp}"/yoloai-setup-* "$${TMPDIR:-/tmp}"/yoloai-e2e-* 2>/dev/null || true
	@if command -v podman >/dev/null 2>&1; then \
		podman unshare rm -rf "$${TMPDIR:-/tmp}"/yoloai-cli-setup-* 2>/dev/null || true; \
	fi
	@rm -rf "$${TMPDIR:-/tmp}"/yoloai-cli-setup-* 2>/dev/null || true
