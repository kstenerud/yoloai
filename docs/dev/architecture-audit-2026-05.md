# Architecture Audit — 2026-05

Fundamental audit of the yoloAI codebase. Findings ordered by impact. The companion remediation plan lives at [`plans/architecture-remediation.md`](plans/architecture-remediation.md).

## 1. Context

- **Go 1.26.1**, single-binary CLI with a library API (`yoloai.go`)
- **~36k lines of Go** across 14 packages + **~2k lines of embedded Python** (`runtime/monitor/*.py`)
- 5 backends (Docker, Podman, Tart, Seatbelt, containerd+Kata), 5 isolation modes
- Cobra + log/slog + testify + go:embed + Docker SDK + containerd v2 client + go-cni
- Solid architectural intent: `ARCHITECTURE.md`, design docs, breaking-changes log, CRITIQUE.md cycle, gocognit gate, `make check` gate via Stop hook

## 2. What the codebase does well

Worth saying explicitly so the critique below stays in proportion.

- **Backend registry via `init()` + blank imports** — idiomatic, lets each backend self-register only where supported (`runtime_imports_linux.go` for containerd). Adding a backend is mechanical.
- **`runtime/caps/`** is a clean second-class subsystem: probes the host, returns structured `BackendReport`s, formats for `system doctor` and `system check`. Capability detection is rightly separated from lifecycle.
- **`Meta` (immutable) vs `SandboxState` (mutable) split** is principled and correct.
- **Error discipline is excellent.** 783 `fmt.Errorf` calls, 608 wrap with `%w` (78%); none use `%v`/`%s` on errors; custom typed errors (`UsageError`, `ConfigError`, `DependencyError`, `PermissionError`) carry exit codes via `errors.As` — clean alternative to sentinels. Only 3 `panic()`s, all init-time invariants.
- **Test pyramid is real** — `unit` / `integration` / `e2e` build tags, `internal/testutil` shared helpers, `TestMain` caches per-package image build. Recent gocognit refactor brought all 28 over-complex functions under 20.
- **`internal/`** is used appropriately for non-API code (`internal/cli`, `internal/mcpsrv`, `internal/testutil`, `internal/fileutil`).

## 3. Highest-impact findings

### F1. The Go ⇄ Python boundary is the largest unmanaged risk

- `runtime/monitor/sandbox-setup.py`: **1303 lines, 29 functions/classes, zero unit tests**
- `runtime/monitor/status-monitor.py`: **685 lines, 22 functions/classes, zero unit tests**

This Python runs inside every container/VM, owns: tmux orchestration, agent launch, secrets reading, lifecycle command execution, status detection, prompt delivery. The `TestCLI_StartAfterDone` race fixed in `5a060b9` lived in this Python and could only be caught by an end-to-end run that boots Docker, builds the base image, creates a sandbox, and times out at 60s. The Podman CI variant doesn't even exercise it (`make integration-podman` only runs `./runtime/podman/`).

The Python boundary also produced two of the entries kept in `backend-idiosyncrasies.md` ("node@24 in .zprofile breaks agent launch after restart" and "swift-wrapper not sourced on restart") — both are about Go's `lifecycle.go` (relaunch path) silently disagreeing with Python's `prepare_launch_command()` (initial-launch path). Two sources of truth, drift inevitable.

**Impact:** every change inside the container is a latent bug waiting for an integration test you might or might not have. Cross-backend testing is effectively unrun for non-Docker setups.

**Underlying problems this finding represents** (each separately addressable — see the remediation plan):

1. No unit tests for Python — pure-function logic (config parsing, command building, retry loops, lifecycle preamble) is unreachable from `make check`.
2. Two sources of truth for "what wraps the agent launch command": Go's `Runtime.PrepareAgentCommand()` and Python's `prepare_launch_command()`. They drift.
3. No cross-backend end-to-end coverage: only Docker exercises the Python orchestration path end-to-end in CI.
4. No schema for the Go↔Python data contract (`runtime-config.json`). Schema drift is invisible until runtime.
5. Race-prone code (tmux orchestration, threading.Event coordination) has no unit-level tests — bugs land at integration time with 60s feedback loops.

### F2. `runtime.Runtime` is a 24-method fat interface that mixes 5 concerns

`runtime/runtime.go:114-236` declares `Runtime` with 24 methods. They fall into five categories:

| Category | Methods |
|---|---|
| **Lifecycle** (real behavior) | Create, Start, Stop, Remove, Inspect, Exec, GitExec, InteractiveExec, Prune, Close |
| **Diagnostics** | Logs, DiagHint |
| **Setup** | Setup, IsReady |
| **Backend descriptor** (constants) | Name, BaseModeName, Capabilities, AgentProvisionedByBackend, SupportedIsolationModes |
| **Adapters** (some return ""/nil for most backends) | ResolveCopyMount, TmuxSocket, AttachCommand, RequiredCapabilities, PrepareAgentCommand |

Several methods return constant facts. `Name()` is the registered name — already known by the registry. `BaseModeName()` is presentation only (used in `system doctor`). `Capabilities()` returns a struct of bools. `AgentProvisionedByBackend()` is a single bool. `SupportedIsolationModes()` is a string slice. These are not behavior — they're metadata that could live alongside the `Factory` in the registry.

Several methods are no-ops for most backends. `TmuxSocket()` returns `""` for all backends except seatbelt. `PrepareAgentCommand()` returns `cmd` unchanged for most.

This violates **interface segregation** and has knock-on effects: tests that need to mock `Runtime` must implement 24 methods even if they only exercise `Create`/`Start`/`Stop`.

### F3. `sandbox/` is overpopulated and `sandbox/create.go` is a 2000-line god-file

`sandbox/` has **33+ Go files** doing diff/apply, lifecycle, profile resolution, archetype detection, devcontainer parsing, VS Code workspace injection, keychain integration, file exchange, archetype-specific seeding, agent-files copying, profile image building, sandbox state, sandbox meta, locking, archetype detection, `.yoloai.yaml` loading, context generation. The package is "everything that orchestrates a sandbox."

Concrete size hotspots:
- `sandbox/create.go` — **2023 lines, 69 functions**. Imports 6 internal packages including `runtime/docker` and `runtime/tart` directly.
- `sandbox/lifecycle.go` — 1395 lines, 46 functions
- `sandbox/create_prepare.go` — 1095 lines, 46 functions

The package has cohesion-fracture lines visible to the eye:
- **archetype/devcontainer** (`archetype.go`, `devcontainer.go`, `yoloaiyaml.go`, `vscode.go`) is project-detection logic
- **diff/apply** is patch generation/application
- **lifecycle** (`create*.go`, `lifecycle.go`, `clone.go`, `inspect.go`) is the actual sandbox orchestrator
- **paths/meta/state** (`paths.go`, `meta.go`, `sandbox_state.go`) is storage layout

Existing `docs/dev/plans/soc-refactor.md` already addresses a subset of this (the `create.go` god file, backend-specific mappings in `sandbox/`).

### F4. Confirmed backend-name leaks outside `runtime/`

Three places where backend names appear as decision points in non-runtime code:

1. **`internal/mcpsrv/proxy.go:225`** hardcodes `exec.Command("docker", ...)`. If the sandbox uses Podman or anything else, the proxy doesn't work.
2. **`sandbox/create.go:522`** checks `m.backend != "tart"` to decide flag validity. The check should be expressed as a `Capabilities()` field or moved into the runtime.
3. **`internal/cli/sandbox_bugreport.go:213-222`** has a `switch backend { case "docker": ... case "podman": ... }` for fetching container logs. This is exactly what `Runtime.Logs()` is for — but the bugreport bypasses the interface.

The Runtime abstraction's *purpose* is to keep these decisions inside backend packages. Three confirmed escapes is small but not zero, and each is a future bug magnet.

### F5. `runtime/*` imports `config/` — small architectural inversion

`runtime/docker`, `runtime/podman`, `runtime/seatbelt`, `runtime/tart` all import `config/` — specifically for `config.NewDependencyError`, `config.NewPermissionError`, etc. (the typed error constructors).

`config/` is supposed to be a leaf (or near-leaf). Having every runtime backend depend on it inverts the implied direction — runtime is "lower" than config in any sensible layering.

## 4. Medium-impact findings

### F6. The CLI has its own 690-line `commands.go` plus a 1068-line `apply.go`

`internal/cli/commands.go` (690 lines) is a kitchen-sink: it registers all subcommands AND defines several of them inline (`newNewCmd`, `newLsAliasCmd`, `newLogAliasCmd`, `newExecAliasCmd`, `newCompletionCmd`, `newVersionCmd`, plus the `attachToSandbox`/`waitForTmux` helpers). Most commands moved to their own files; these stayed for unclear reasons. Continued accretion here is a smell.

`internal/cli/apply.go` (1068 lines) holds two distinct workflows (squash and selective-commit) plus the `--export` path. Could be split.

### F7. Dual command dispatch creates cognitive load

The CLI supports both verb-first (`yoloai diff <name>`) and name-first (`yoloai sandbox <name> diff`). Aliases (`ls`, `log`, `exec`) and `YOLOAI_SANDBOX` env-var resolution add a third axis. This is implemented via custom argument parsing in `commands.go` and `envname.go` — Cobra doesn't natively do it.

The ergonomics are nice but the cost is: every command author must think about both dispatch paths, the help text has to make both make sense, and the test surface doubles. Worth occasionally asking: are users actually using both?

### F8. Five string-match-on-error-text fragility points

Brittle matches:
- `sandbox/apply.go:385` — `strings.Contains(err.Error(), "exec exited with code 1")`
- `runtime/containerd/caps.go:179` — `strings.Contains(openErr.Error(), "permission denied")`
- `runtime/containerd/lifecycle.go:442` — `strings.Contains(strings.ToLower(createTaskErr.Error()), "address in use")`
- `runtime/containerd/containerd.go:82` — `strings.Contains(err.Error(), "permission denied")`
- `runtime/docker/docker.go:80` — `strings.Contains(err.Error(), "permission denied")`

Each one breaks the moment an upstream library localizes its error or restructures it. The "permission denied" ones should be `errors.Is(err, fs.ErrPermission)` or `errors.Is(err, syscall.EACCES)`.

### F9. slog conventions slipped in `runtime/tart` and `sandbox/create_prepare.go`

The codebase mostly uses `slog.Info("...", "event", "x.y", ...attrs)` as the first attribute. `runtime/tart/tart.go` and a couple of `sandbox/create_prepare.go` call sites omit `event`. The attribute key for errors mixes `"error"` (most places) and `"err"` (none yet, but inconsistency emerging). `sloglint` is already enabled — just needs configuration to enforce required keys.

### F10. No JSON schema versioning on `environment.json` / `sandbox-state.json`

`Meta` is what every sandbox persists. There's a "legacy: meta.json" comment indicating one rename has already happened. No `schema_version` field in the JSON to gate compatible reads. As the field set grows, this will eventually bite — either through silent breakage on downgrade or via brittle add-fields-but-never-remove growth.

## 5. Smaller things worth fixing

- **Public Go API `yoloai.Client`.** ARCHITECTURE.md calls it the "high-level public Go API for library consumers." Is there a known external consumer? If not, removing it shrinks the API-stability surface. If yes, document who.
- **`PrepareAgentCommand` duplicates the Python `prepare_launch_command`.** Documented as F1 issue 2.
- **`sandbox/setup.go` has a 632-line test file.** Tests longer than the production code can mean the unit under test is doing too much. Worth a quick look.
- **Generic `internal/testutil/wait.go`.** The codebase uses `WaitForActive`/`WaitForStopped`. Generics let `Wait[T any](t, get func() (T, error), pred func(T) bool, timeout time.Duration)` — cleaner than two helpers.
- **`go fix` and `gopls modernize` (Go 1.26 feature).** Codebase is on Go 1.26.1; the modernizer can apply analyzer-driven idiom updates (`for range n`, `min`/`max`, `slices.Concat`) safely. Worth a one-time pass.

## 6. What's *not* a problem (despite first impressions)

- **5 backends.** Each has its own package, each registers via init(). The abstraction is paying for itself.
- **Embedded resources.** `//go:embed` for Dockerfile, tmux.conf, monitor scripts is exactly what go:embed is for. Single-binary distribution stays clean.
- **CLI file count.** Many small files in `internal/cli/`. That's fine — each command being its own file is good navigability.
- **Sentinel-error patterns.** Codebase uses typed errors with constructors; standard `errors.Is`/`errors.As` works against them. Modern Go pattern.
- **Cobra command-builder pattern with options structs.** Established by the gocognit refactor. Consistent across files now.

## 7. Bottom line

The architecture is **mostly sound and unusually well-documented for a one-person project at this maturity**. The error handling, dependency direction, and testing scaffolding are above-average for a Go codebase this size. The two material risks are (a) the **uncovered Python boundary** running ~2k lines of orchestration in every sandbox with no unit tests, and (b) a **`Runtime` interface that's accreted past comfortable** — both fixable without breaking changes. The leaks identified by the dependency audit are small in number and easy to remedy.

## Sources

- [Go Project Structure 2026: Clean Architecture and Best Practices](https://reintech.io/blog/go-project-structure-2026-clean-architecture-best-practices)
- [Standard Go Project Layout (golang-standards)](https://github.com/golang-standards/project-layout)
- [Eleven Tips for Structuring Your Go Projects — Alex Edwards](https://www.alexedwards.net/blog/11-tips-for-structuring-your-go-projects)
- [Go 1.26 — Small Changes, Big Impact for Real-World Go Systems](https://medium.com/@anand.hv123/go-1-26-is-around-the-corner-small-changes-big-impact-for-real-world-go-systems-b7e5bd271f51)
- [Using go fix to modernize Go code — The Go Programming Language](https://go.dev/blog/gofix)
