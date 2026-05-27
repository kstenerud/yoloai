# Critique Follow-up — Decisions

Decisions made during the 2026-05-27 triage of `docs/dev/CRITIQUE.md`. Each
entry below has been ratified by the project owner; this file is the
canonical decision record while the work lands. As findings close, mark
them **CLOSED**. When this file is empty, the critique round is done.

## Triaged findings (decisions ratified)

### F1 — Curated public Options + advanced escape hatch

Promote a deliberately small public Options surface (Run-style 8-ish
fields). Provide a typed escape hatch (`RunRaw(ctx, opts *AdvancedOptions)`
or similar) for embedders who need every CLI flag. Two-tier surface;
documented boundary between basic and advanced.

### F2 — Sub-handle grouping: judge per-method, present for owner approval

Walk each of the 18 per-sandbox Client methods. For each, propose a
target home (Sandbox root vs `Sandbox(name).Workdir()` vs `…Files()` vs
`…Network()` vs `…Logs()` vs `…Exec()`). Present the full mapping to
the owner before implementation. Owner may overrule any individual
choice or accept `api_surface.go`'s design verbatim.

### F3 — Run stays as sugar over Create

Run remains the convenience entry (curated 8-field surface). Internally
materializes into `Create(ctx, opts.Materialize())`. F1's escape-hatch
path becomes the deep entry.

### F4 — Hard error on `Backend == ""`; broader: forbid `""` unless demonstrably beneficial

`Options.Backend == ""` returns `*UsageError` at construction. Matches
Q-W.5's treatment of DataDir.

**Broader principle (new):** empty string is not a valid value for
typed-name / config / identity fields unless there's a demonstrated
benefit to allowing it. Implicit behavior tends to become evil. To be
added to `docs/dev/principles/development-principles.md` as part of
this work.

### F5 — Full sandbox/ god-package carve

Carve `internal/sandbox/` into:
- `internal/sandbox/create/` — orchestration, sandboxState, buildContainerConfig
- `internal/sandbox/lifecycle/` — Start/Stop/Destroy/Reset
- `internal/sandbox/mounts/` — the eight `buildXMounts` functions
- `internal/sandbox/manager/` — façade holding runtime+layout+logger

Shared `sandboxState` exposed deliberately. Mirrors the W-L13 CLI
carve. Multi-week effort.

### F8 — Refactor to structured results

Manager methods return per-method `<Op>Result` types carrying a
`Notices []Notice` field (each Notice has `Level`, `Code`, `Args`).
CLI gains a `RenderNotice` helper. Roughly 30 message sites converted
across `internal/sandbox/*`. The embedded `m.output io.Writer` goes
away. Multi-week.

### F18 — Move all five to optional interfaces

`Logs`, `DiagHint`, `TmuxSocket`, `PrepareAgentCommand`, `GitExec` all
move to optional interfaces (`LogTailer`, `DiagHinter`,
`TmuxSocketResolver`, `AgentCommandPreparer`, `GitExecer`). Backends
drop trivial impls. Callers use the existing helper-function pattern
(`runtime.LogsFor(rt, ...)` etc.). Strict "core = universal" bar.

### F22 — Strict `Sandbox(name)` validation

`Client.Sandbox(name)` becomes `(*Sandbox, error)`. Loads meta + checks
sandbox-dir existence upfront. Surfaces `ErrSandboxNotFound` where the
caller typed the name. Matches `api_surface.go`'s Q-G design + §4
parse-don't-validate.

### F23 — Migrate all four cross-backend ops into SystemClient

Add `SystemClient.ListAcrossBackends`, `SystemClient.Doctor`,
`SystemClient.Info`, `SystemClient.AllowDomain`. Drop the four
allowlist exceptions in `.golangci.yml`. CLI becomes a thin shell
again. Each op is roughly a week.

### F30 — `RunOptions.PollInterval`

Add `PollInterval time.Duration` to RunOptions. Default 5s. CLI keeps
the default; embedders pick. ~10 lines, no new deps.

### F31 — `Layout.HostUID` / `Layout.HostGID`; library never calls `os.Getuid()`

Add `HostUID, HostGID int` to `config.Layout`. CLI startup reads
SUDO_UID/SUDO_GID once with `os.Getuid()` fallback; sets the Layout
fields. Library uses `layout.HostUID` everywhere. Forbidigo rule:
`os\.Getuid|os\.Getgid` banned outside `internal/cli/`.

Folds naturally into F13's `HomeDir` field — Layout becomes the
operator-identity contract, not just paths.

## Mechanical findings (no decision needed; land as-is per CRITIQUE.md)

- **F6** — Move `AcquireLock` + `ExecInContainer` out of `internal/sandbox` so `patch/` doesn't import parent
- **F7** — `rm -rf internal/cli/profiles/` (empty directory leftover)
- **F9** — Delete `DirArg`; `ParseDirArg` returns `DirSpec`
- **F10** — `WorkdirMeta.Mode` / `DirMeta.Mode` → `DirMode` typed enum
- **F11** — `meta.Isolation` → `IsolationMode` typed enum; cascade through `BackendDescriptor.SupportedIsolationModes`
- **F12** — Drop "(was X)" comments from `store/paths.go` constants
- **F13** — Add `HomeDir` to `config.Layout`; ban `filepath.Dir(.*\.DataDir)` via forbidigo
- **F14** — `NewLayout` rejects empty DataDir; `panic` in Manager becomes a self-evident invariant
- **F15** — No code change; possibly inline `runtime/errs.go` into `runtime/runtime.go` (2 funcs, no sibling file needed)
- **F16** — `ExitCoder` interface; collapse `errorExitCode` cascade
- **F17** — Re-export sentinels at yoloai root (`ErrSandboxNotFound`, `ErrContainerNotRunning`, `ErrMissingAPIKey`)
- **F19** — Add `Architectures []string` to `BackendDescriptor`; Tart declares `["arm64"]`; drop hardcoded check in `setup.go`
- **F20** — Folds into F11 (typed IsolationMode → `exhaustive` checks the 4 string-switches)
- **F21** — Move isolation/OS routing into `runtime.SelectContainerBackend`; CLI calls with all three preferences
- **F24** — Continue W3 pattern: incremental Python helper carves; no single landing
- **F25** — Single source of truth for `runtime-config.json` schema version (likely `go:generate` writing a Python const file)
- **F26** — Calibration; no change
- **F27** — Calibration; no change
- **F28** — Calibration; no change
- **F29** — Delete `type SecurityPerms = IsolationPerms`

## Suggested execution order

Phases assume each is its own commit / small PR. Multi-week items can
land in pieces.

1. **Mechanical batch** (1-2 sessions): F6, F7, F9, F12, F14, F15, F17, F19, F20, F25, F26, F27, F28, F29 — all small, no inter-dependency
2. **Typed surface extension** (1-2 sessions): F10, F11, F31 (Layout HostUID/HostGID), F13 (Layout HomeDir), plus the §12 "forbid empty string" principle landing
3. **Cleanup with light surface change** (1 session): F16 (ExitCoder), F21 (SelectContainerBackend signature)
4. **F2 review** (1 session): produce the per-method sub-handle mapping for owner review
5. **F1 + F3 + F4** (multi-session): public Options surface; Run as sugar; hard-error Backend
6. **F22** (1 session, post-F1): strict Sandbox(name)
7. **F18** (1-2 sessions): move 5 methods to optional interfaces
8. **F23** (multi-session): cross-backend ops into SystemClient
9. **F8** (multi-session): structured Results
10. **F5** (multi-week): sandbox/ god-package carve
11. **F24** (ongoing): Python helper carves

## Out-of-band items

- The `public_api_test.go` fence (committed `ae0ccd0`) tracks F1 progress
  automatically via `f1KnownLeaks`. Removing entries from that map is
  the test-driven definition of F1 closure.
- The §12 "forbid empty string unless demonstrably beneficial" principle
  needs to land in `docs/dev/principles/development-principles.md`
  before or alongside F4.
