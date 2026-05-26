# `internal/cli/` conventions

Patterns Cobra command handlers in this package follow. These are written
after each pattern earned its place — read this before writing a new
command, but don't apply patterns where they don't fit.

## Construction: `withClient` vs `withRuntime`

Two helpers in `helpers.go` open a backend connection, defer the close, and
hand control to a callback. Pick the smallest one that does the job.

- **`withClient(cmd, backend, fn)`** — opens a `yoloai.Client`. Use for
  command handlers that only need orchestration-level operations: `Run`,
  `Stop`, `Destroy`, `List`, `Inspect`, `Diff`, `Apply`. The Client wraps
  a `runtime.Runtime` plus a `sandbox.Manager` with a §12-clean Layout
  derived from `cliLayout()`. This is the default for new commands.
- **`withRuntime(ctx, backend, fn)`** — exposes the raw `runtime.Runtime`.
  Use only when the handler needs operations not on `yoloai.Client`: image
  probing, raw `Exec`, container inspect/logs, tmux attach helpers,
  per-backend availability checks, multi-backend enumeration (e.g.
  `sandbox list` walks every backend, not one).

The migration to `withClient` is incremental. A handler that calls
`withRuntime + sandbox.NewManager` is the old shape; reach for `withClient`
when touching that handler for any other reason.

### Attach: `Client.Attach` is now the canonical path

W-L8d added `yoloai.Client.Attach(ctx, name, IOStreams) error` and moved
the readiness polling (`waitForTmux`) into `sandbox.WaitForAttachReady`.
Every attach flow should ultimately go through `c.Attach`:

- The standalone `yoloai attach` command uses `withClient + c.Attach`
  directly (see `attach.go`).
- Lifecycle commands with an `--attach` branch (`clone` today;
  `restart`/`reset`/`start`/`new` to be migrated) call the shared
  `attachToSandboxByName(cmd, name)` helper in `helpers.go`, which
  itself opens a Client and calls `c.Attach`.
- The terminal-title machinery (`setTerminalTitle`) stays in the CLI —
  it's UI, not orchestration. `Client.Attach` is library code and
  doesn't touch the terminal beyond the PTY.

**Limitation to know.** `IOStreams.In/Out/Err` are accepted on the
public API but not yet fully plumbed: the underlying
`runtime.Runtime.InteractiveExec` hardcodes `os.Stdin/Stdout/Stderr`,
so non-CLI embedders (HTTP server, MCP) passing custom streams will
still see the calling process's stdio. Fully wiring IOStreams through
requires extending the runtime interface (one method per backend);
tracked as future work. CLI use is unaffected — terminal stdio IS
the calling process's stdio.

### Legacy raw-runtime attach — RETIRED

`new.go`, `start.go`, `restart.go`, `reset.go` previously held their own
`attachAfter<Verb>` helpers; all have been migrated to `c.Attach`. The
`attachToSandbox` and `waitForTmux` CLI shims in `attach.go` are gone.
The library `sandbox.WaitForAttachReady` is the single readiness
implementation; `Client.Attach` is the single attach entry point. Do
NOT add a `Client.Runtime()` escape hatch; it would defeat the
layering.

Still on raw runtime: `exec.go` (interactive non-attach exec — needs
PTY-aware Exec on the runtime interface).

`list.go` does NOT need migration — it calls the library helper
`sandbox.ListSandboxesMultiBackend` directly, which is the correct
layered shape for multi-backend ops (single-backend Client by design;
multi-backend dispatch is the embedder's concern).

`apply.go` does NOT need migration — operates entirely on disk, no
runtime in the picture.

## Path resolution: `cliLayout()`

Every path under `~/.yoloai/` comes from `cliLayout()` (defined in
`layout_bridge.go`) — that is the ONE place in the CLI that reads `$HOME`.
A handler that constructs a `sandbox.NewManager` directly must pass
`sandbox.WithLayout(cliLayout())`; otherwise the Manager panics at
construction. `withClient` handles this automatically.

Never call `config.YoloaiDir()`, `config.SandboxesDir()`, etc. — those
helpers were deleted in Q-W.6. Use the Layout methods instead
(`cliLayout().SandboxesDir()`, `cliLayout().ProfileDir(name)`).

## Backend selection

Resolution priority for the `--backend` flag, in order:

1. `--backend` flag (if set; not present on all commands).
2. For lifecycle commands operating on a named sandbox
   (`stop`/`start`/`destroy`/etc.): `resolveBackendForSandbox(name)`
   reads the backend from the sandbox's `meta.json`.
3. `resolveContainerBackendConfig()` for the config default.
4. `runtime.SelectContainerBackend(ctx, cfg)` picks a platform default.

Pattern: resolve the backend BEFORE calling `withClient`/`withRuntime` —
the helpers take the resolved name as a string. The `stop.go` handler is
the canonical example.

## Name resolution: arg, env, `--all`

Lifecycle commands accept one or more sandbox names with these
precedences (every command supports the same set; share the helpers in
`envname.go`):

1. Positional args (validate each with `store.ValidateName`).
2. `$YOLOAI_SANDBOX` (the env-name fallback for `cd`-style workflows).
3. `--all` (mutually exclusive with positional args; returns a typed
   `UsageError`).

`resolveStopNames` in `stop.go` is the canonical name-resolution
function. Other commands have parallel `resolve<Verb>Names`.

## JSON output

Every user-facing command supports `--json`. Pattern:

```go
if jsonEnabled(cmd) {
    return writeJSON(cmd.OutOrStdout(), payload)
}
// human-readable output
```

JSON output is for scripting and integration; human output is the
default. Empty results print "No <thing> to <verb>" in human mode and an
empty array in JSON mode. JSON-incompatible flags (e.g. `--attach`)
return a `UsageError` early.

## Logging

`slog.Info("verb sandbox", "event", "sandbox.verb", "sandbox", name)`
is the standard structured-log shape. Pair every action log with a
completion log: `"sandbox.verb.complete"`. Tests assert on these
event keys; new ones should match `sandbox.<verb>(.<phase>)?`.

## Errors

- Validation/usage errors → `sandbox.NewUsageError(...)`. Maps to exit
  code 2 in `errorExitCode`.
- Wrapped runtime errors → `fmt.Errorf("connect to runtime: %w", err)`
  pattern. Preserve sentinel errors with `%w` so `errors.Is/As` keeps
  working at the call site.
- For lifecycle commands, run errors through `sandboxErrorHint(name, err)`
  to add the standard "did you mean..." hint.
