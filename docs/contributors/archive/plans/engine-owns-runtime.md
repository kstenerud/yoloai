<!-- ABOUTME: Plan to move lazy-runtime ownership from Client into the Engine so
ABOUTME: per-sandbox sub-handles stop reaching two/three levels deep. Refines D67. -->

# Plan: Engine owns the lazy runtime (refine D67)

**Status (2026-06-07):** Complete. Stage 1 landed — C1 (`9f67d28`), C2
(`45aab36`), C3 (`8479258`); Stage 2 landed — C4a (Workdir →
`engine_workdir.go`), C4b (Files → `engine_files.go`), C4c (Network →
`engine_network.go`) — all on `main`, `make check` green. The Workdir/Network/
Files sub-handles no longer thread `layout`/`runtime` into the patch/files/
network free functions. See [D74].

[D74]: ../../decisions/working-notes.md

## The problem

The A2/A3 collapse (D67) put **one lazy `Client`** in place of the old
eager-Client / lazy-`SystemClient` split: `NewWithOptions` no longer opens the
backend eagerly, `Options.Backend` is optional, and a backend-bound op on a
backend-less `Client` returns `ErrBackendRequired`. That was the right surface
decision. But it parked the *laziness machinery* on the wrong object.

Today the lazy-backend state and verbs live on `Client`:

```go
type Client struct {
    layout  config.Layout
    backend runtime.BackendType
    logger  …; version …; output …; input …
    mutex   sync.Mutex      // ┐
    opened  bool            // │ lazy-runtime machinery
    runtime runtime.Runtime // │ — but the Engine is the
    engine  *sandbox.Engine // ┘   thing that USES a runtime
}
```

`ensure(ctx)` opens the runtime once and *then builds the Engine*
(`sandbox.NewEngine(rt, …)`). So the Engine is a child the Client constructs
lazily, even though the Engine already owns `layout`, `input`, `logger`, and
`runtime` and is the object every sandbox operation actually runs through.

The fallout shows up at every sub-handle. `Agent`, `Workdir`, `Network`, and
`Files` each hold a `*Client` and reach two or three levels deep to do their job:

```go
// agent.go
return a.client.engine.SendInput(ctx, a.name, text)   // 3 levels: client→engine→verb
plain, ansi, err := a.client.engine.CaptureTerminal(…) // and again
// workdir.go
w.client.tryEnsure(ctx)
patch.GenerateDiff(ctx, patch.DiffOptions{Layout: w.client.layout,
    Runtime: w.client.runtime, …})                    // reaches two sibling fields
// network.go
n.client.runtime.Exec(ctx, store.InstanceName(n.client.layout.Principal, n.name), …)
```

This is a Demeter / reach-depth smell, and it is a *symptom*, not the disease.
The handle reaches through `Client` to get at `engine`/`runtime`/`layout`
because the ownership is inverted: the laziness sits one layer too high.

## The fix (Stage 1 — ownership core)

Move lazy-runtime ownership **into the Engine**. Build the Engine eagerly from
layout-only state (rt nil); open the runtime lazily *inside* the Engine on the
first backend-bound method. Per-sandbox sub-handles then hold a
`*sandbox.Engine` + name and call `engine.Verb(name, …)` — one level, no
field-reaching.

### Engine becomes the lazy owner

```go
type Engine struct {
    backend runtime.BackendType
    layout  config.Layout
    logger  …; input io.Reader; progress …

    mutex   sync.Mutex      // moved from Client
    opened  bool            // moved from Client (latches on success only)
    runtime runtime.Runtime // nil until ensure() opens it
}
```

- **`NewEngine(layout, logger, input, opts…)`** builds eagerly from layout-only
  state, `runtime` nil. (Today's `NewEngine(rt, …)` derives `backend` from
  `rt.Descriptor().Type`; the new primary constructor takes `backend` explicitly
  from options/layout.)
- **`ensure(ctx) (runtime.Runtime, error)`** — mutex-guarded; if `backend == ""`
  return `ErrBackendRequired`; else open once via `runtime.New(ctx, backend,
  layout)`, cache on success, return. A failed open is **not** latched (retryable).
  This is exactly today's `Client.ensure` body, relocated.
- **`tryEnsure(ctx)`** — best-effort; on failure `runtime` stays nil so the
  host-only fallback paths (copy-mode diff, ContainerLogs, Network read) keep
  working. Relocated from `Client`.
- **`deps(ctx) (state.Deps, error)`** becomes Engine-internal: `ensure` then
  `state.Deps{Runtime: rt, Layout: e.layout, Input: e.input}`.
- **`Close()`** closes `runtime` only if `opened`. Relocated from `Client`.
- **Injected-runtime path preserved.** Keep a `NewEngineWithRuntime(rt, …)` (or
  an option) that seeds `runtime` + sets `opened = true`, for the ~12 test sites
  that hand the Engine a mock runtime and for `destroyForOverwrite`'s ephemeral
  open. These must not regress.

`ErrBackendRequired` moves to the `sandbox` package (where `ensure` now lives);
the root package re-exports it as a typed alias so the public name is unchanged.

### Engine grows the lifecycle wrappers

The lifecycle/create verbs are free functions (`lifecycle.Start`, `…Stop`,
`…Restart`, `…Reset`, `…Destroy`, `…NeedsConfirmation`, `create.Create`) taking
`state.Deps`. Promote them to first-class Engine methods so callers stop
assembling `deps()` themselves:

```go
func (e *Engine) Start(ctx, name, opts) (…, error) {
    deps, err := e.deps(ctx); if err != nil { return …, err }
    return lifecycle.Start(ctx, deps, name, opts)
}
// …Stop / Restart / Reset / Destroy / Create likewise.
// NeedsConfirmation uses tryEnsure (best-effort, host-readable).
```

No import cycle: `internal/sandbox` already imports and re-exports
`lifecycle`/`create`/`state` (see `aliases.go`), and those leaf packages do not
import the parent `sandbox` package. Verified.

### Handles hold the Engine

`Sandbox`, `Agent`, `Workdir`, `Network`, `Files` change their backing field
from `*Client` to `*sandbox.Engine` (plus `name`). Sub-handle construction stays
pure namespace expansion (no IO, no error). Reach depth drops to one:

```go
func (a *Agent) SendInput(ctx, text) error { return a.engine.SendInput(ctx, a.name, text) }
func (a *Agent) Prompt() (…) { return sandbox.ReadStoredPrompt(a.engine.Layout(), a.name) }
```

Path-only reads use a single `engine.Layout()` accessor (already present) instead
of reaching `client.layout`.

### Client shrinks to a factory

`Client` keeps its construction-time config and **one** `*sandbox.Engine` built
eagerly in `NewWithOptions` (layout-only — no backend open). Its job narrows to:
validate options, build the Engine, hand out handles (`Sandbox(name)`,
`System()`), and own process-level concerns (`Close()` delegates to
`engine.Close()`). `Client` retains a `layout` field for the ~35 test
`c.layout.SandboxDir(…)` reaches (test-compat; cheap, no behavior).

The public surface (`NewWithOptions`, `Options`, `Client.Sandbox`,
`Sandbox.Agent()/Workdir()/…`, `ErrBackendRequired`) is **unchanged**. This stage
is an internal ownership move — no breaking change required. The CLI uses only
the public API, so CLI churn is zero.

## The fix (Stage 2 — operations-on-Engine purity, optional / separately scoped)

Stage 1 fixes the *ownership* smell. A residual smell remains in `Workdir` and
`Network`: they still thread `layout` + `runtime` into ~15 patch/sandbox
**free functions** (`patch.GenerateDiff/Apply/…`, `sandbox.FilesDir/Import…`,
the network-rule helpers). Even holding `*Engine`, they'd call
`engine.Layout()`/`engine.Runtime()` and pass those outward.

Stage 2 promotes those free-function calls to first-class Engine methods —
`engine.GenerateDiff(name, opts)`, `engine.ApplyDiff(name, opts)`,
`engine.ListExchangeFiles(name)`, `engine.NetworkRules(name)`, etc. (~15
methods) — so the handles call `engine.Verb(name, …)` and never see `layout` or
`runtime`. This is the same push-down already applied to exec/attach
(`internal/sandbox/exec.go`, `attach.go`), generalized to the diff/files/network
verbs.

Stage 2 is larger and lower-urgency. It is presented for completeness (the
end-state is "handles never touch layout/runtime") but can ship as its own
follow-up so Stage 1 lands clean. **No scope is being cut** — Stage 2 is the
documented remainder, sequenced, not dropped.

## Feasibility (verified)

- **No import cycle.** `lifecycle`/`create`/`state` don't import parent
  `sandbox`; `sandbox` already imports + re-exports them (`aliases.go`).
- **Engine may open the runtime.** `internal/sandbox` already imports
  `internal/runtime`; `runtime.New` is callable there. Backend *registration*
  stays in the root package via the existing blank imports (`_ ".../docker"`),
  unaffected.
- **Injected-runtime construction survives** for tests (8 mock-runtime + 4
  real-rt sites) and `destroyForOverwrite`'s ephemeral open via
  `NewEngineWithRuntime`.
- **Client keeps `layout`** → the ~35 test `c.layout` reaches don't churn.
- **CLI is public-API-only** → zero CLI churn for Stage 1.

## Commit plan (each compiles + `make check` green)

- **C1:** relocate the lazy core (mutex/opened/runtime/ensure/tryEnsure/deps/
  Close) from `Client` to `Engine`; add the layout-only `NewEngine` +
  `NewEngineWithRuntime`; move `ErrBackendRequired` to `sandbox`, re-export from
  root. `Client.ensure`/`tryEnsure` become thin `c.engine.ensure` delegations (or
  are inlined at handle sites in C3). Tests: opens once under concurrency;
  backend-free path never opens; empty backend → `ErrBackendRequired`; `Close()`
  on unopened is a no-op.
- **C2:** promote lifecycle/create verbs to Engine methods (`Start`/`Stop`/
  `Restart`/`Reset`/`Destroy`/`NeedsConfirmation`/`Create`); `Sandbox`/`Client`
  call `engine.Verb` instead of assembling `deps()`.
- **C3:** repoint sub-handles (`Sandbox`/`Agent`/`Workdir`/`Network`/`Files`)
  from `*Client` to `*sandbox.Engine`; collapse the reach-depth call sites; add
  `engine.Layout()` reads for path getters.
- **C4 (Stage 2, optional/separate):** push the patch/files/network free-function
  calls down into Engine methods; strip `layout`/`runtime` threading from
  `Workdir`/`Network`/`Files`.
- **C5:** docs — drain to D74 in `working-notes.md` (refines D67), update
  `architecture/README.md` ownership description, this plan's status, memory.

## Verification

1. `go build ./... && go test ./...` after each commit; `make check` green
   (gofmt, golangci-lint incl. gocognit≤20, mod tidy, Go + Python).
2. New unit tests as in C1 above (open-once, backend-free no-open,
   ErrBackendRequired, Close no-op) now asserted against the **Engine**.
3. Manual single-handle regression: `yoloai new --agent test box`; run the
   backend-free half (`info`/`prompt`/`agent-log`/`diff` copy-mode) with no
   backend open, then a backend-bound op (`attach`/`exec`/`start`) and confirm the
   runtime opens lazily exactly once, at first use.
