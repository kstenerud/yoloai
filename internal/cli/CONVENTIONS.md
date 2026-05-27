# `internal/cli/` conventions

Patterns Cobra command handlers in this package follow. These are written
after each pattern earned its place — read this before writing a new
command, but don't apply patterns where they don't fit.

## Construction: `withClient`, `systemClient`, and `newRuntime`

Two helpers in `helpers.go` give a command handler an orchestration
entry point. Pick the one that matches the command's scope.

- **`withClient(cmd, backend, fn)`** — opens a `yoloai.Client` for one
  backend, defers close. The canonical path for sandbox-scoped command
  handlers: `Run`, `Stop`, `Destroy`, `List`, `Inspect`, `Diff`, `Apply`,
  `Exec`, `Attach`, plus the MCP server's tool handlers. Use this for
  every new command that operates on a single sandbox / single backend.
- **`systemClient()`** — returns a `*yoloai.SystemClient` (no runtime
  yet, no close needed). The canonical path for `yoloai system …`
  handlers that aren't tied to a specific backend: `DiskUsage`, `Prune`,
  `Build`, `Check`, `Setup`. SystemClient spins up runtimes per backend
  internally for cross-backend operations.

`withRuntime` and `withManager` have been removed (W-L10) — every
command goes through `yoloai.Client` / `yoloai.SystemClient`. The
underlying `newRuntime(ctx, backend)` factory still exists for the
handful of commands that walk every registered backend for
enumeration (`yoloai ls`, `yoloai sandbox <name> allow`,
`yoloai system doctor`, `yoloai system info`) and for the
backend-scoped `system tart` subtree. Don't add new direct calls —
prefer Client/SystemClient for any new orchestration.

### Interactive wizards: prompts live in the CLI

Q-F (W-L8b) — library Client/SystemClient methods never do interactive
IO. `yoloai system setup` follows the established pattern:

1. Call `SystemClient.SetupStatus(ctx)` to inspect the host (classify
   `~/.tmux.conf`, enumerate available backends/agents).
2. Render prompts and read user input in the CLI (`system_setup.go`'s
   `wizardTmuxConf` / `wizardChoice`).
3. Pass the resulting `SetupOptions` to `SystemClient.Setup(ctx, opts)`
   for a pure config write.

If a new command needs interactive prompts, follow the same shape:
library provides "what to ask" (status + available options) and "how
to apply" (a non-interactive setter); CLI owns the conversation.

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

`IOStreams.In/Out/Err` are wired all the way through the runtime
interface as of `runtime/iostreams.go`. Non-CLI embedders (HTTP, MCP,
test harnesses) can pass their own streams to `Client.Attach` and
have them reach the backend faithfully. For TTY=true the streams
must be terminals (e.g. `*os.File` with a PTY fd); for TTY=false
plain pipes work and stderr stays separate.

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
