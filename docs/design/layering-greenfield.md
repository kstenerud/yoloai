<!-- ABOUTME: The package + command layout we would build if starting yoloAI clean today, -->
<!-- ABOUTME: given the audit + comparator findings. Target state for the layering refactor. -->

> **Design documents:** [README](README.md) | [Layering Architecture](layering.md) | [Commands](commands.md)
> **Backing research:** [CLI Surface (current)](../dev/research/layering-cli-surface.md) | [Leak Audit](../dev/research/layering-leak-audit.md) | [Comparators](../dev/research/layering-comparators.md)

# Greenfield Layout

If we were starting yoloAI clean today, knowing what we know now, this is the structure we would build. Not a refactor target in itself — the [phased plan](../dev/plans/layering-refactor.md) lands the *architecture* defined in [`layering.md`](layering.md); the greenfield layout shows the destination it converges toward.

**Date:** 2026-05-23.

---

## 1. Public surface

Exactly **two** packages outside `internal/`:

```
yoloai/                         repo root
├── yoloai.go                   (and supporting files) — public Client API
│   └── package yoloai
│         Client, New, RunOptions, ApplyOptions, DiffOptions, ...
│         List, Inspect, Run, Apply, Diff, Destroy, Attach, Exec, ...
│         BackendDescriptor, SandboxInfo, DiffResult, ApplyResult, ...
│
└── cmd/
    └── yoloai/
        └── main.go             — entry point only; sets up signals, calls internal/cli
```

Everything else lives under `internal/`, enforcing the boundary mechanically (Go's `internal/` rule, not just convention).

**Stability discipline:** `yoloai.Client` carries a documented stability promise (e.g., declared in `docs/api-stability.md`). Versioning follows the project's chosen scheme; breaking changes go through `BREAKING-CHANGES.md`. See [`layering.md` §6](layering.md#6-public-api-stabilitydecoupled) — in a true greenfield, the stability decision is made *with* the architecture, not deferred.

---

## 2. Package tree

```
yoloai/                                repo root
├── yoloai.go                          public Client API
├── client_internal.go                 (split if yoloai.go grows; same package)
├── go.mod
├── cmd/
│   └── yoloai/
│       └── main.go
├── internal/
│   ├── cli/                           ◀── PRESENTATION layer
│   │   ├── root.go                       Cobra root command, group definitions
│   │   ├── commands.go                   AddCommand registration
│   │   ├── streams.go                    IOStreams (gh CLI pattern; stdin/out/err, TTY, color)
│   │   ├── format/                       output formatters
│   │   │   ├── format.go                   Formatter interface
│   │   │   ├── text.go                     human-readable
│   │   │   └── json.go                     machine-readable
│   │   ├── help/                         embedded help topics (md files)
│   │   ├── commands/                     generic CLI commands; each file: parse → call → format
│   │   │   ├── new.go
│   │   │   ├── attach.go
│   │   │   ├── diff.go
│   │   │   ├── apply.go
│   │   │   ├── destroy.go
│   │   │   ├── list.go
│   │   │   ├── inspect.go
│   │   │   ├── reset.go
│   │   │   ├── baseline.go
│   │   │   ├── exec.go
│   │   │   ├── log.go
│   │   │   ├── files.go
│   │   │   ├── clone.go
│   │   │   └── ...
│   │   ├── system/                       admin subtree
│   │   │   ├── system.go                   `yoloai system` parent
│   │   │   ├── info.go, agents.go, backends.go
│   │   │   ├── build.go, check.go, disk.go, doctor.go, prune.go, setup.go
│   │   │   └── tart/                       ◀── BACKEND-SCOPED subcommand group
│   │   │       ├── tart.go                   `yoloai system tart` parent
│   │   │       ├── runtime.go                Apple simulator runtimes (was system_runtime.go)
│   │   │       └── base.go                   base VM management
│   │   │       (this is the ONLY directory in internal/cli/ that imports runtime/tart)
│   │   ├── profile/                      profile management subtree
│   │   ├── config/                       config management subtree
│   │   ├── mcp/                          MCP server commands — calls yoloai.Client like CLI does
│   │   └── x/                            extensions (`yoloai x ...`)
│   │
│   ├── orchestration/                  ◀── ORCHESTRATION layer (currently `sandbox/`)
│   │   ├── manager.go                    sandbox.Manager and state machine
│   │   ├── lifecycle.go                  Create, Start, Stop, Restart, Destroy
│   │   ├── review.go                     Diff, Apply, Baseline policies
│   │   ├── attach.go                     interactive attach orchestration
│   │   ├── exec.go                       interactive exec orchestration
│   │   ├── overlay.go                    overlay-mode handling
│   │   ├── format_patch.go               format-patch apply path
│   │   ├── store/                        sandbox metadata persistence
│   │   ├── archetype/                    environment archetypes
│   │   └── errors.go                     orchestration-level typed errors
│   │
│   ├── runtime/                        ◀── BACKEND layer
│   │   ├── runtime.go                    Runtime interface
│   │   ├── descriptor.go                 BackendDescriptor (Name, Description, Probe,
│   │   │                                  VersionString, CleanupHint, HostFromContainer,
│   │   │                                  Platforms, Requires, Notes, ...)
│   │   ├── capabilities.go               BackendCaps + optional interfaces:
│   │   │                                  CopyMountResolver, UsernsProvider, StdioExecer,
│   │   │                                  CachePruner, AppleSimulatorRuntimes, ...
│   │   ├── isolation.go                  Isolation modes: IsContainer, EnforcesIptables,
│   │   │                                  SupportsOverlayDirs, IsolationAvailability(...)
│   │   ├── registry.go                   Register, Registered, IsAvailable
│   │   ├── docker/
│   │   ├── podman/
│   │   ├── tart/
│   │   ├── seatbelt/
│   │   └── containerd/
│   │       (each backend package: Runtime impl + descriptor + Probe + version)
│   │
│   ├── config/                         user config (yaml files, profile chains)
│   ├── agent/                          agent definitions (claude, codex, gemini, etc.)
│   ├── workspace/                      workdir parsing, dirty-repo detection
│   ├── network/                        iptables/ipset allowlist policy
│   ├── credentials/                    keychain / file-based credential mount
│   ├── resources/                      embedded resources NOT owned by a backend
│   │   ├── tmux/                         tmux.conf (currently lives in runtime/docker!)
│   │   └── scripts/                      shared shell snippets
│   └── tmux/                           if tmux session management gets its own home
│
├── docs/                              (as today)
└── runtime/monitor/                   the in-container Python helper (orthogonal layer)
```

**Two principles enforced by structure:**

1. **`internal/orchestration/` knows nothing about specific backends.** It consumes `runtime.Runtime` and queries `BackendDescriptor`. Optional interfaces (`AppleSimulatorRuntimes`, etc.) let it react to backend-specific capabilities without importing concrete packages.

2. **`internal/cli/commands/` knows nothing about `internal/orchestration/` or `internal/runtime/`.** It imports only `yoloai` (the Client API), `internal/cli/streams`, and `internal/cli/format`. The single exception is `internal/cli/system/tart/`, which imports `internal/runtime/tart` directly — and its command path declares that scope (Pattern B).

---

## 3. Layered dependency graph

```
   ┌────────────────────────────────────────────────────────────┐
   │  cmd/yoloai/main.go                                        │
   └─────────────────────┬──────────────────────────────────────┘
                         │ calls
   ┌─────────────────────▼──────────────────────────────────────┐
   │  internal/cli/   (Cobra commands, formatters, IOStreams)   │
   │                                                            │
   │  generic commands ──────────────┐                          │
   │                                 │                          │
   │  system/* generic ──────────────┤                          │
   │                                 │ calls yoloai.Client      │
   │  mcp/                ───────────┤                          │
   │                                 │                          │
   │  system/tart/        ─────┐     │                          │
   └───────────────────────────┼─────┼──────────────────────────┘
                               │     │
              imports          │     ▼
              (explicit        │  ┌─────────────────────────────┐
              scope)           │  │  yoloai (Client API)         │
                               │  │  package yoloai              │
                               │  └────────────┬─────────────────┘
                               │               │
                               │               ▼
                               │  ┌─────────────────────────────┐
                               │  │  internal/orchestration/    │
                               │  │  (state machine, archetypes,│
                               │  │   diff/apply, attach, exec) │
                               │  └────────────┬─────────────────┘
                               │               │
                               │               ▼
                               │  ┌─────────────────────────────┐
                               └─▶│  internal/runtime/           │
                                  │  Runtime interface + caps    │
                                  │  Descriptor (Probe, version, │
                                  │  CleanupHint, ...)           │
                                  └────────────┬─────────────────┘
                                               │
                                               ▼
                            ┌──────────────────────────────────┐
                            │  runtime/docker, podman, tart,   │
                            │  seatbelt, containerd            │
                            │  (each: Runtime impl + Descriptor)│
                            └──────────────────────────────────┘
```

**Strict downward-only.** No layer ever imports up. The only horizontal lateral is `internal/cli/system/tart/` reaching to `internal/runtime/tart/` — explicitly named in the command path.

---

## 4. Command tree (greenfield naming)

```
yoloai
├─ Lifecycle ─────────────────────────────────
│  ├── new <name> <workdir>
│  ├── clone <source> <dest>
│  ├── start <name>
│  ├── stop <name>
│  ├── restart <name>
│  ├── destroy <name>
│  └── reset <name>
│
├─ Workflow ──────────────────────────────────
│  ├── attach <name>
│  ├── diff <name>
│  ├── apply <name>
│  ├── baseline <name>
│  └── files <name>
│
├─ Sandbox Tools ─────────────────────────────
│  ├── sandbox (alias: sb)         name-first dispatch (kept; see OPEN_QUESTIONS #100)
│  │    └── list, info, log, exec, prompt, allow, allowed, deny, bugreport, vscode
│  ├── ls / log / exec / vscode    (top-level aliases — same as today)
│  └── x <extension>               extensions
│
└─ Admin ─────────────────────────────────────
   ├── system                      system administration
   │    ├── info / agents / backends
   │    ├── build [profile]
   │    ├── check / disk / doctor / prune
   │    ├── setup                    interactive setup
   │    └── tart                     ◀── RENAMED from `system runtime` (D1)
   │         ├── runtime ...           Apple simulator runtimes
   │         └── base ...              base VM management
   │       (hypothetical future: `system kata`, `system docker`, etc.)
   ├── mcp                         MCP server — calls yoloai.Client
   │    ├── serve
   │    └── proxy <name>
   ├── profile ...
   ├── config ...
   ├── help [topic]
   ├── completion <shell>
   └── version
```

**Changes from current:**

| Current | Greenfield | Why |
|---|---|---|
| `yoloai system runtime ...` | `yoloai system tart ...` | Current name reads generic but is Tart-only (audit L19, D1). |
| `yoloai system completion` | `yoloai completion` | Shell completion is presentation, not system admin. Conventional location. |
| `mcp` under Lifecycle group | `mcp` under Admin group | MCP server is administrative surface, not sandbox lifecycle. |
| (none) | `yoloai system <backend>` convention reserved | Future backend-specific surfaces (kata, docker-specific) follow the same pattern. |

The rest of the command tree is unchanged from current — the *naming* didn't need fixing, just the structure.

---

## 5. Differences from current, summarized

| Concern | Current | Greenfield |
|---|---|---|
| Public packages | `yoloai`, `sandbox/`, `runtime/`, `config/`, `agent/`, ... — multiple | Only `yoloai` + `cmd/yoloai`. Everything else `internal/`. |
| Orchestration package name | `sandbox/` | `internal/orchestration/` |
| Embedded resources owned by backends | `runtime/docker/` owns `tmux.conf` (audit L27) | `internal/resources/tmux/` — neutral location |
| Backend metadata source | Three parallel tables (`knownBackends`, `availableBackends`, version-query map) | One: `runtime.Registered()` + `BackendDescriptor` |
| Backend selection logic | Duplicated in `helpers.go` + `yoloai.go` | One function, in `yoloai.Client` |
| Backend availability detection | `dockerAvailable()` (hard-codes socket) + `podmanrt.SocketExists()` named import | Each backend's `Descriptor.Probe(ctx)` method |
| CLI orchestration | Built directly against `sandbox/` + `runtime/` | Calls `yoloai.Client` exclusively |
| Backend-specific commands | `system_runtime.go` (`*tart.Runtime` cast, lives in main CLI directory) | `internal/cli/system/tart/` — directory enforces scope |
| `--isolation` validation | 3× repeated error tables in `validateIsolationOSCombo` | One `runtime.IsolationAvailability(mode, hostOS)` query |
| `--security` flag | Stale help (audit L22–L24) | `--isolation` only; old name doesn't exist |
| MCP server | Bypasses `yoloai.Client` (parallel orchestration) | Calls `yoloai.Client` — same surface as CLI |
| Public API stability | Undeclared | Documented promise with `BREAKING-CHANGES.md` policy |

---

## 6. Why this layout

- **`internal/` for everything-but-Client** mechanically enforces the public surface. No accidental external dependency on `sandbox/`, `runtime/`, etc. The current root-level package layout depends on convention; the greenfield depends on the Go toolchain.

- **Three layers, one direction.** Presentation → orchestration → backend. No cycles, no horizontal reach-around. The dependency graph is literally a tree.

- **Backend-scoped commands live in backend-named directories.** `internal/cli/system/tart/` *is* the scope declaration. A linter rule can enforce "files under `cli/system/tart/` may import `runtime/tart`; files elsewhere in `cli/` may not." Structural, not stylistic.

- **The Client is the only surface every other interface (CLI, MCP, future HTTP, future library use) shares.** Adding a new interface means adding `internal/<surface>/` and consuming `yoloai.Client`. Adding a new CLI command means adding a method to Client. **There is no "but the CLI needs to do X" escape hatch.**

- **Embedded resources have a neutral home.** `internal/resources/` ends the historical accident where the tmux config lives in `runtime/docker/` because Docker was the first backend to use it. Future shared resources go here.

- **Public API stability is a designed property, not an emergent one.** In a true greenfield, the stability discipline is baked in — `yoloai.Client` is the surface from day one, with its evolution rules documented and enforced.

---

## 7. What the greenfield does NOT change

These remain the same as today and would be the same if starting over:

- `runtime.Runtime` interface and `BackendCaps` shape (already good; capability-driven).
- Optional capability interfaces pattern (`UsernsProvider`, `StdioExecer`, etc.) — idiomatic Go.
- Dual command dispatch (`yoloai diff <name>` vs `yoloai sandbox <name> diff`) — separate decision tracked in [OPEN_QUESTIONS #100](../dev/OPEN_QUESTIONS.md).
- Cobra as the CLI framework.
- The copy/diff/apply workflow itself — orthogonal to layering.
- Network isolation architecture (`internal/network/` is just a rename, not a redesign).
- The agent abstraction — `internal/agent/` is just a rename.

---

## 8. Migration delta vs. starting fresh

The phased plan in [`layering-refactor.md`](../dev/plans/layering-refactor.md) gets us close to this layout. The remaining differences after the plan completes:

- **Package paths.** The plan does not propose moving `sandbox/` → `internal/orchestration/` or `runtime/` → `internal/runtime/`. That move is mechanical but disruptive (every import path changes). Defer until the layering refactor is otherwise complete, then do as one large rename PR.
- **`internal/cli/` reorganization into `commands/` and `system/`.** The plan leaves CLI files flat; the greenfield groups them by command tree. Low-priority cosmetic; can be done anytime after W-L8e.
- **`internal/resources/`.** W-L1 moves the tmux config; the broader resources/ directory is forward-looking and only gets populated as new shared resources appear.
- **`yoloai.Client` declared externally stable.** Deferred until a real consumer materializes ([`layering.md` §6](layering.md#6-public-api-stabilitydecoupled)). The greenfield declares from day one.

These are decisions that can be made independently of the main refactor.

---

## 9. References

- [Layering Architecture (the decisions)](layering.md)
- [CLI Surface — Current State](../dev/research/layering-cli-surface.md)
- [Leak Audit](../dev/research/layering-leak-audit.md)
- [Comparator Research](../dev/research/layering-comparators.md)
- [Implementation plan](../dev/plans/layering-refactor.md)
