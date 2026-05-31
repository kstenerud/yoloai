<!-- ABOUTME: Visual map of yoloAI's current CLI surface — command tree, dependencies, -->
<!-- ABOUTME: and leak overlay. Companion to layering-leak-audit.md and layering-comparators.md. -->

# CLI Surface — Current State

**Date:** 2026-05-23 · **Scope:** Documents the *current* CLI command tree, what each command calls underneath, and where the audited leaks (L1–L31) sit on that tree. Forward-looking layout lives in [`docs/contributors/design/layering-greenfield.md`](../../design/layering-greenfield.md).

---

## 1. Top-level command tree

Built from `internal/cli/commands.go:27-59`. Group IDs are how Cobra renders the `--help` output.

```
yoloai
├─ Lifecycle ─────────────────────────────────────────────
│  ├── new <name> <workdir>           create a sandbox
│  ├── clone <source> <dest>          duplicate a sandbox
│  ├── start <name>                   start a stopped sandbox
│  ├── stop <name>                    stop a running sandbox
│  ├── restart <name>                 restart a sandbox
│  ├── destroy <name>                 destroy a sandbox
│  ├── reset <name>                   reset agent state
│  └── mcp                            MCP server (secondary surface!)
│       ├── serve                       MCP stdio server
│       └── proxy <name> [wd] -- cmd    MCP stdio proxy
│
├─ Workflow ──────────────────────────────────────────────
│  ├── attach <name>                  attach to running sandbox
│  ├── diff <name>                    show pending changes
│  ├── apply <name>                   apply pending changes
│  ├── baseline <name>                set new baseline
│  ├── files <name>                   list/extract files from sandbox
│  └── x <extension> [args...]        run a yoloai extension
│
├─ Sandbox Tools ─────────────────────────────────────────
│  ├── sandbox (alias: sb)            name-first dispatch
│  │    ├── list                        list sandboxes (real subcmd)
│  │    ├── <name> info                 show config + state
│  │    ├── <name> log                  show logs
│  │    ├── <name> exec <cmd>           run command in sandbox
│  │    ├── <name> prompt               show prompt
│  │    ├── <name> allow <domain>...    add allowlist domains
│  │    ├── <name> allowed              show allowed domains
│  │    ├── <name> deny <domain>...     remove allowlist domains
│  │    ├── <name> bugreport [type]     bug report for sandbox
│  │    └── <name> vscode               open in VS Code
│  ├── ls                              alias for sandbox list
│  ├── log <name>                      alias for sandbox <name> log
│  ├── exec <name> <cmd>               alias for sandbox <name> exec
│  └── vscode <name>                   alias for sandbox <name> vscode
│
└─ Admin ─────────────────────────────────────────────────
   ├── system                         system administration
   │    ├── info                        show system info
   │    ├── agents                      list available agents
   │    ├── backends                    list backends (uses knownBackends — L8)
   │    ├── build [profile]             build base or profile image
   │    ├── check                       sandbox dependency checks
   │    ├── disk                        disk usage report
   │    ├── doctor                      diagnostic checks
   │    ├── prune                       prune caches/images
   │    ├── setup                       run interactive setup
   │    ├── runtime                     Tart-only! (audit L19)
   │    │    ├── create <name>             create Apple simulator runtime
   │    │    ├── list                      list runtimes
   │    │    └── delete <name>             delete runtime
   │    └── completion <shell>          generate shell completion
   ├── profile                         profile management
   │    ├── list / show / create / edit / delete / ...
   ├── help [topic]                    embedded help topics
   ├── config                          configuration
   │    ├── get / set / reset
   └── version                         show version
```

**Dual dispatch.** Most commands work in both forms:
- `yoloai diff <name>` (verb-first)
- `yoloai sandbox <name> diff` (name-first, via `sandboxDispatch`)
- `YOLOAI_SANDBOX=<name> yoloai diff` (env-var)

See [OPEN_QUESTIONS #100](../OPEN_QUESTIONS.md) — separately tracked.

---

## 2. Where each command calls underneath

Three orchestration packages: `runtime/` (backend interface), `sandbox/` (state machine, copy/diff/apply, archetypes), `yoloai.Client` (intended-but-bypassed public API).

| Command | Calls `sandbox/` | Calls `runtime/` | Calls `yoloai.Client` | Notes |
|---|:---:|:---:|:---:|---|
| `new` | ✅ | ✅ | ❌ | `executeNewCreate` builds orchestration directly |
| `start` / `stop` / `restart` | ✅ | ✅ | ❌ | |
| `destroy` | ✅ | ✅ | ❌ | duplicates `Client.Destroy` (which exists) |
| `reset` | ✅ | ✅ | ❌ | |
| `attach` | ✅ | ✅ | ❌ | `waitForTmux` orchestration not in Client |
| `diff` | ✅ | ✅ | ❌ | overlay/format-patch paths not in Client |
| `apply` (+ overlay, format-patch, selective, squash, export) | ✅ | ✅ | ❌ | major coverage gap — Client only has basic Apply |
| `baseline` | ✅ | ❌ | ❌ | |
| `files` | ✅ | ❌ | ❌ | |
| `list` | ✅ | ❌ | ❌ | duplicates `Client.List` |
| `inspect` (`sandbox info`) | ✅ | ❌ | ❌ | duplicates `Client.Inspect` |
| `log` | ✅ | ❌ | ❌ | |
| `exec` | ✅ | ✅ | ❌ | |
| `clone` | ✅ | ✅ | ❌ | |
| `mcp serve` / `mcp proxy` | ✅ | ✅ | ❌ | **secondary surface bypasses Client too** |
| `system build` | ✅ | ✅ | ❌ | also reads `knownBackends` (L8) |
| `system check` / `disk` / `doctor` | ✅ | ✅ | ❌ | |
| `system setup` | ✅ | ❌ | ❌ | also reads `availableBackends` (L26) |
| `system runtime *` | ❌ | ✅ (tart) | ❌ | **type-asserts to `*tart.Runtime`** (L19) |
| `system backends` | ❌ | ❌ | ❌ | reads `knownBackends` directly (L8) |
| `system prune` | ✅ | ✅ | ❌ | |
| `profile *` | ❌ | ❌ | ❌ | calls `config/` directly |
| `config *` | ❌ | ❌ | ❌ | calls `config/` directly |
| `bugreport` | ✅ | ✅ | ❌ | per-backend version table (L16) |
| `x` (extensions) | ❌ | ❌ | ❌ | shells out to user scripts |

**`yoloai.Client` usage count: 0.** The public API is not consumed by the CLI.

---

## 3. Dependency overview (current)

Arrows = imports / direct calls. Layers blur because the CLI reaches past `sandbox/` into `runtime/` for some operations and into `config/` for others.

```
   ┌─────────────────────────────────────────────────────────────┐
   │  cmd/yoloai/main.go                                          │
   │      └─> internal/cli.Execute                                │
   └────────────────────┬─────────────────────────────────────────┘
                        │
   ┌────────────────────▼─────────────────────────────────────────┐
   │  internal/cli/  (Cobra commands, 16k lines)                  │
   │                                                              │
   │  ┌────────────────────┐   ┌──────────────────────────────┐  │
   │  │ generic commands   │   │ system_runtime.go            │  │
   │  │ (new, diff, apply, │   │  (Tart-scoped, type-asserts  │  │
   │  │  attach, ...)      │   │   to *tart.Runtime — L19)    │  │
   │  └─────────┬──────────┘   └──────┬────────────┬──────────┘  │
   │            │                     │            │             │
   └────────────┼─────────────────────┼────────────┼─────────────┘
                │                     │            │
                ▼                     ▼            ▼
       ┌──────────────┐      ┌──────────────┐  ┌──────────────────┐
       │  sandbox/    │      │  runtime/    │  │ runtime/tart/    │
       │  (state mach,│      │  (Runtime    │  │ (concrete imp,   │
       │   archetypes,│◀────▶│   interface, │  │  named import &  │
       │   diff/apply)│      │   registry)  │  │  type assert)    │
       └──────┬───────┘      └──────┬───────┘  └──────────────────┘
              │                     │
              ▼                     ▼
       ┌──────────────┐     ┌────────────────────────────────┐
       │  config/     │     │ runtime/{docker,podman,tart,   │
       │              │     │  seatbelt,containerd}/         │
       └──────────────┘     └────────────────────────────────┘

         ┌────────────────────────────────────────────────┐
         │  yoloai.go (Client API — 331 lines)            │
         │  PARALLEL implementation; never called by CLI  │
         │  also imports sandbox/, runtime/, config/      │
         └────────────────────────────────────────────────┘
```

---

## 4. Leak overlay (audit findings on the surface)

Each audit finding mapped to its command-tree location and severity. `🔴` = HIGH, `🟡` = MEDIUM, `🟢` = LOW.

```
yoloai
├─ Lifecycle
│  ├── new                       🔴 L5 (--isolation flag) · 🔴 L6 (3× error tables)
│  │                              🟡 L7 (gVisor in error) · 🟡 L29 (in sandbox/)
│  ├── clone                     (no specific findings)
│  ├── destroy                   🟢 L12 (legacy backend fallback)
│  └── reset                     (no specific findings)
│
├─ Workflow
│  ├── attach                    🟢 L11 (comments name docker/gvisor)
│  ├── diff                      🟡 L9 ("overlay sandbox" jargon)
│  │                              🟢 L10 (":overlay" — KEEP)
│  └── apply (+ variants)        (massive orchestration overlap — §4 of audit)
│
├─ Sandbox Tools
│  ├── sandbox list              🟢 L12 (legacy backend fallback)
│  ├── help                      🟡 L15 (host.docker.internal hard-coded)
│  └── (others)                   (config/profile leaks)
│
└─ Admin
   ├── system
   │  ├── backends              🔴 L8 (knownBackends — duplicate registry)
   │  ├── setup                  🟡 L26 (availableBackends — third registry)
   │                              🟡 L27 (imports runtime/docker for tmux)
   │  ├── runtime               🔴 L19 (whole subtree — *tart.Runtime assertion)
   │                              🟡 L20 (3× Tart guard) · 🟡 L21 (shells to tart)
   │  └── build                  🟢 L18 (--backend in error — KEEP)
   ├── profile
   │  ├── delete                 🟡 L14 (always-Docker cleanup hint)
   │  ├── scaffold               🟢 L13 (legitimate examples — KEEP)
   ├── config                    🟢 L17 (tart.image example — KEEP)
   │                              🟡 L24 (FIXED — security key)
   ├── help (embedded topics)
   │  ├── flags.md               🔴 L22 (FIXED — stale --security)
   │  ├── security.md            🔴 L23 (FIXED — stale --security)
   │  ├── config.md              🟡 L24 (FIXED — same)
   │  └── workdirs.md            🟢 L25 ("Docker only" — should be container backends)
   └── bugreport                 🟡 L16 (per-backend version table)

(Public API)
yoloai.go (Client)               🔴 L1 (doc comment lists 3/5 backends)
                                  🔴 L2 (duplicated routing + "docker" literal)
helpers.go (routing)              🔴 L3 (resolveBackend + named podmanrt import)
                                  🟡 L4 (dockerAvailable hard-codes socket)

sandbox/                         🟡 L26, L27, L28 (concrete runtime imports)
                                  🟡 L29 ("container-enhanced" string check)
                                  🟢 L30 (host.docker.internal in hint)
                                  🟢 L31 (archetype bullets name backends)
```

**Concentration patterns:**

- **`new` and `system runtime`** are the densest leak clusters (5 HIGH findings between them).
- **Three parallel backend registries** (L8 + L26 + L16) drive the bulk of MEDIUM findings and have already diverged in practice (`containerd` missing from setup; missing from bug report).
- **Embedded help files** are the largest user-visible correctness issue (3 HIGH findings, fixed in this pass).
- **`yoloai.Client` + `helpers.go`** are the routing duplication.

---

## 5. `yoloai.Client` coverage gap

Per audit §4. `❌` = no Client method exists; CLI orchestrates directly. `⚠️` = method exists but is incomplete vs CLI surface.

| CLI capability | `yoloai.Client` method | Status |
|---|---|---|
| Create sandbox (full options) | `Run(ctx, RunOptions)` | ⚠️ exists; misses overlay-specific paths |
| Start / Stop / Restart | `Stop(ctx, name)` only | ⚠️ no Start, no Restart |
| Destroy | `Destroy(ctx, name)` | ✅ exists; CLI duplicates `--force` policy |
| List | `List(ctx)` | ✅ exists; CLI duplicates |
| Inspect | `Inspect(ctx, name)` | ✅ exists; CLI duplicates |
| Diff (commit-ref) | `Diff(ctx, name, opts)` | ⚠️ no overlay path, no format-patch |
| Apply (commits) | `Apply(ctx, name, opts)` | ⚠️ no overlay, format-patch, selective, squash, export |
| Baseline | — | ❌ |
| Reset | — | ❌ |
| Attach (interactive) | — | ❌ |
| Exec (interactive) | — | ❌ |
| Log (streaming) | — | ❌ |
| Files (list/extract) | — | ❌ |
| Clone | — | ❌ |
| MCP serve / proxy | — | ❌ — and MCP server bypasses Client too |
| Backends / agents / profiles | — | ❌ |
| Bug report | — | ❌ |
| Config get/set/reset | — | ❌ |
| System build / check / prune / disk / doctor | — | ❌ |
| Setup | — | ❌ |
| `system tart` (renamed from `system runtime`) | — | N/A — backend-scoped, by design |

**Summary:** 5 of ~25 CLI surfaces have a Client method, and 3 of those 5 are incomplete. The MCP server (a *secondary surface* that ought to consume Client by design) also bypasses it. Both CLI and MCP build orchestration directly against `sandbox/` and `runtime/`.

This is the structural fact that makes the layering refactor (Phase 3, [W-L8](../plans/layering-refactor.md#w-l8--yoloaiclient-becomes-the-clis-spine-pattern-c)) the largest piece of work.

---

## 6. Cross-references

- [Leak audit (L1–L31, recommendations, open Qs)](layering-leak-audit.md)
- [Comparator research (Docker, kubectl, Terraform, etc.)](layering-comparators.md)
- [Design — Layering Architecture](../../design/layering.md)
- [Design — Greenfield Layout](../../design/layering-greenfield.md)
- [Implementation plan](../plans/layering-refactor.md)
