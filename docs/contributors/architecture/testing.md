# Testing

Three tiers of tests, each requiring progressively more infrastructure:

| Tier | Build tag | Requires | Run with |
|------|-----------|----------|----------|
| Unit | *(none)* | Nothing | `make test` / `go test ./...` |
| Integration | `integration` | Docker daemon + `yoloai-base` image | `make integration` |
| E2E | `e2e` | Docker daemon + compiled binary | `make e2e` |

**Run all checks (preferred — gofmt, lint, tidy, unit tests):**
```
make check
```

**Run unit tests:**
```
go test ./...
```

**Run integration tests (requires Docker):**
```
make integration
# or: go test -tags=integration -v -count=1 -timeout=10m ./orchestrator/ ./runtime/docker/ ./internal/cli/
```

**Run E2E tests (requires Docker, compiles binary first):**
```
make e2e
# or: go test -tags=e2e -v -count=1 -timeout=15m ./test/e2e/
```

## Backend conformance & per-host coverage

Every backend is verified the same way: a shared, backend-agnostic behavioral
suite in `internal/runtime/runtimetest`, run by each backend's integration
package, with a per-host smoke matrix on top. Each tier is **host-aware** — it
runs what the current host supports and skips the rest with a reason, so the same
target works on a Linux box or an Apple Silicon Mac.

- **`RunInterfaceConformance`** (`conformance_iface.go`) — the universal contract
  through interface methods only: lifecycle, exec exit-codes, the exec-on-stopped
  error path, idempotency, `IsReady`, and capability-gated `Mounts`/`Stdio`
  sections (a backend declares a section it can't honor and the suite reports a
  clean SKIP+reason). The one backend-specific seam is *how to start a
  long-running instance* (a `Sleeper`): entrypoint override for docker/podman, a
  sleep image for apple/containerd.
- **`RunConformance`** (`conformance.go`) — wraps the above for docker-API
  backends and adds the assertions that need the docker SDK `Client()` (resource
  limits, port bindings). docker/podman run both.

| Backend | Unit | `RunInterfaceConformance` | Integration target | Smoke matrix | Live run needs |
|---------|:----:|:-------------------------:|--------------------|:------------:|----------------|
| docker | ✓ | ✓ (+ SDK extras) | `make integration` | linux + mac | docker daemon |
| podman | ✓ | ✓ (+ SDK extras) | `make integration-podman` | linux + mac | podman + socket |
| containerd (Kata) | ✓ | ✓ | `make integration-containerd` | linux | Linux + containerd + Kata/KVM |
| apple | ✓ | ✓ | `make integration-apple` | mac | macOS 26 + Apple Silicon + `container` |
| tart | ✓ | ✓ (Mounts skipped — path model, DF29) | `make integration-tart` | mac | macOS + Apple Silicon + tart |
| seatbelt | ✓ | ✓ (Mounts skipped — path model) | `make integration-seatbelt` | mac | macOS (sandbox-exec) |

**Gating** — each integration package self-skips when its daemon/host is absent
(`TestMain` for docker/podman/apple/seatbelt/tart, `skipIfNotAvailable` for
containerd — which probes a real socket connection, not just the socket file). So
`make integration` runs every backend target and exercises only what the host
supports. The slow apple base-image build is gated behind `YOLOAI_TEST_APPLE_BASE=1`;
the slow tart VM clone behind `YOLOAI_TEST_TART_VM=1`.

**Tart conformance** (`TestTartConformance`, gated `YOLOAI_TEST_TART_VM=1`) — tart
is a participant: each subtest clones+boots a real macOS VM as the sleeper. The
runtime-level suite works because tart's `Start` now does **P1 only** (boot +
mounts) when no sandbox `runtime-config.json` is present, skipping the **P2**
`sandbox-setup.py` monitor — mirroring how every other backend runs conformance
against a bare idle instance. Lifecycle/exec/interactive pass; `Mounts` is skipped
for the **same reason as seatbelt** — the conformance mounts at `/mnt/test`, and
the macOS guest's `/mnt` isn't writable without root (no passwordless sudo), so the
symlink can't be created (the *conformance's container-path assumption*, not a
VirtioFS gap). The failing `ln` used to be misread as "instance not found" because
`mapTartError` misclassified inner-command stderr — fixed in DF30 (exec stderr now
surfaced verbatim); see DF29 (resolved) and DF30. Real mounts run in the smoke matrix.

**Seatbelt conformance** (`TestSeatbeltConformance`) — also a participant, via the
same P1/P2 split: `Start` launches a bare keep-alive process under the SBPL
profile (P1) when no `runtime-config.json` is present, instead of the
`sandbox-setup.py` monitor (P2). Each instance is a host `sandbox-exec` process,
so it's fast (no VM/image). Lifecycle / exec / interactive / idempotency all pass;
`Mounts` is skipped because the conformance mounts at `/mnt/test` and seatbelt is
host-side, where `/mnt` isn't writable without root — the *conformance's
container-path assumption*, not a seatbelt mount-capability gap (the SBPL RW/RO
grants are unit-tested in `TestGenerateProfile_{ReadOnly,ReadWrite}Mount`, and
real mounts run in the smoke matrix). *(The earlier "no separately-startable
instance" claim was wrong — seatbelt has a real startable, exec-able instance.)*

**Smoke tiers** (`scripts/smoke_test.py`, `HOST_MATRICES`) — one matrix per host
OS, every backend that OS can run. The tier flag changes only how *missing
infrastructure* is treated: `make smoketest` SKIPS it (quick dev check),
`make smoketest-full` FAILS loudly (release gate). A full run is per-OS, so the
gate is **not** complete until `make releasetest` has passed on every supported
host OS — the harness prints a reminder naming the others.

**Run tests for a specific package:**
```
go test ./orchestrator/
go test ./runtime/docker/
go test ./agent/
```

**Build:**
```
go build -o yoloai ./cmd/yoloai/
```

**Lint:**
```
golangci-lint run
```

## Test infrastructure

**`internal/testutil/`** — shared non-test package importable by all test files:
- `git.go`: `InitGitRepo`, `GitAdd`, `GitCommit`, `GitRevParse`, `RunGit`, `WriteFile`
- `fixtures.go`: `GoProject(t)` (git-initialized Go project), `AuxDir(t, name)`, `MultiFileProject(t)`
- `home.go`: `IsolatedHome(t)` — sets `HOME` to `t.TempDir()`
- `wait.go`: `WaitForActive`, `WaitForStopped` — poll `rt.Inspect` instead of sleeping

**`TestMain` pattern** — each integration package has `integration_main_test.go` that connects to Docker and calls `EnsureSetup` once before any tests run. Per-test `integrationSetup(t)` still uses `IsolatedHome(t)` for sandbox isolation, but subsequent `EnsureImage` calls hit the image cache.

Integration test files:
- `orchestrator/integration_test.go` — full sandbox lifecycle; includes `TestIntegration_AgentStubWorkflow` (agent runs in container → diff → apply)
- `runtime/docker/docker_integration_test.go` — Docker runtime operations
- `internal/cli/integration_test.go` — CLI commands via Cobra

E2E test files (`test/e2e/`):
- `helpers_test.go` — `TestMain` (builds binary), `runYoloai`, `e2eSetup`
- `workflow_test.go` — `new` / `ls` / `destroy` lifecycle
- `json_test.go` — `--json` output shape contracts
- `error_test.go` — exit codes and error messages
- `bugreport_test.go` — bug report generation
