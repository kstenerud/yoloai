# F2 — Sub-handle mapping (proposal for owner review)

Per `critique-followup.md` F2: walk every per-sandbox `yoloai.Client` method and
propose a target home. **This is a proposal — nothing is implemented.** Approve
as-is, or overrule any individual row. Implementation (the re-rooting PR) only
starts after sign-off.

## The target shape (from `api_surface.go`, Q-G / Shape B)

- **`Client` root** keeps only operations that aren't scoped to one existing
  sandbox: creation (`Run`, `Create`, `Clone`), enumeration (`List`), and
  Client/system lifecycle (`Close`, `EnsureSetup`).
- **`Sandbox(name)`** is the canonical entry for per-sandbox ops. Direct methods:
  lifecycle + interaction (`Inspect`, `Status`, `Start`, `Stop`, `Restart`,
  `Destroy`, `Reset`, `Wait`, `Attach`, `Exec`, `Logs`, `ProxyMCP`, `Unlock`,
  `CaptureTerminal`).
- **Three sub-handles only** — `Sandbox(name).Workdir()`, `.Files()`,
  `.Network()`. (`api_surface.go` does **not** design `Logs()`/`Exec()`
  sub-handles — those are direct `Sandbox` methods; the plan listed them only as
  options to weigh. Recommendation: don't add them.)

The headline isn't just re-rooting — it's **collapse**. Several root methods are
variants that `api_surface.go`'s option structs already absorb (the whole `Diff*`
family → one `Workdir().Diff(DiffOptions)`), and the overlay-explicit methods
**disappear** because the design resolves copy-vs-overlay internally from
`meta.Workdir.Mode`.

## Mapping

### Stays at `Client` root (not per-sandbox)

| Current method | Home | Note |
|---|---|---|
| `Run` | root | creation; F3 makes it sugar over `Create` |
| `Create` | root | creation (advanced entry; see F1) |
| `Clone` | root | spans Source→Dest; creation-family |
| `List` | root | all sandboxes |
| `EnsureSetup` | root | system setup (candidate for `SystemClient` later — out of F2 scope) |
| `Close` | root | Client lifecycle |

### Move to `Sandbox(name)` direct methods (1:1)

| Current method | Proposed | Note |
|---|---|---|
| `Inspect(name)` | `Sandbox(name).Inspect()` | |
| `Stop(name)` | `Sandbox(name).Stop()` | |
| `Start(name, opts)` | `Sandbox(name).Start(opts)` | |
| `Destroy(name, force)` | `Sandbox(name).Destroy(opts)` | `force` → `DestroyOptions` |
| `Reset(opts{Name})` | `Sandbox(name).Reset(opts)` | name moves from opts to handle |
| `Attach(name, io)` | `Sandbox(name).Attach(io)` | |
| `SendInput(name, text)` | `Sandbox(name).SendInput(text)` | |
| `CaptureTerminal` | `Sandbox(name).CaptureTerminal()` | **already done** |

### Move to `Sandbox(name)` — with a judgment call (⚑)

| Current method | Proposed | ⚑ Judgment |
|---|---|---|
| `Exec(name, cmd, io)` | `Sandbox(name).Exec(ExecOptions, io)` | api_surface has one `Exec` taking `ExecOptions` |
| `StdioExec(name, cmd, stdin, out, err)` | **fold into** `Sandbox(name).Exec` | ⚑ Add a non-PTY/stdio mode to `ExecOptions` rather than a second method. MCP proxy is the only caller. Alt: keep a distinct `Sandbox.StdioExec`. |
| `ContainerLogs(name, tail)` | `Sandbox(name).ContainerLogs(tail)` | ⚑ Distinct from the designed `Sandbox.Logs` (structured agent/jsonl stream). This is raw backend container stdout/stderr for diagnostics — keep it as its own method, not merged into `Logs`. |
| `NeedsConfirmation(name)` | `Sandbox(name).NeedsConfirmation()` | ⚑ Or fold into `Destroy`'s flow (CLI does the prompt). Recommend keeping as a handle query — it's a pre-flight the CLI reads before prompting. |
| `SandboxDir(name)` | `Sandbox(name).Dir()` | ⚑ Path accessor. api_surface puts the *exchange* dir on `*Info.HostExchangeDir`; this is the *state* dir. Recommend a `Dir()` accessor on the handle. |

### Move to `Sandbox(name).Workdir()` — diff / apply / baseline / commits

| Current method | Proposed | ⚑ Judgment |
|---|---|---|
| `Diff(name)` | `Workdir().Diff(DiffOptions{})` | |
| `DiffWithOptions(name, paths, stat, nameOnly)` | **fold into** `Workdir().Diff(DiffOptions{...})` | the bools/paths are `DiffOptions` fields |
| `DiffRef(name, ref, stat)` | **fold into** `Workdir().Diff(DiffOptions{Ref, Stat})` | |
| `GenerateWIPDiff(name, paths)` | **fold into** `Workdir().Diff(DiffOptions{Paths, IncludeUncommitted})` | ⚑ "WIP" = include uncommitted; an option, not a method |
| `DiffOverlay(name, stat, nameOnly)` | **disappears** → `Workdir().Diff` | ⚑ Overlay-vs-copy resolved internally from `meta.Workdir.Mode`. Confirm we want the overlay-explicit method gone. |
| `Apply(name)` | `Workdir().Apply(ApplyOptions{})` | |
| `ApplyWithOptions(name, opts)` | **fold into** `Workdir().Apply(opts)` | |
| `GeneratePatch(name, paths, includeWIP)` | `Workdir().Apply(ApplyOptions{DryRun:true, …})` returns the patch | ⚑ Or a distinct `Workdir().Patch(opts)`. api_surface routes "what would apply" through `Apply` + `DryRun` (ApplyStatusDryRun). Recommend the DryRun path. |
| `GenerateFormatPatch(name, paths)` | `Workdir().Apply(ApplyOptions{Mode: ApplyExport, ExportDir})` | ⚑ api_surface models export as an `ApplyMode`. Alt: a distinct `Workdir().FormatPatch`. Recommend the ApplyExport path. |
| `GenerateFormatPatchForRefs(name, shas, paths)` | same, `+ Refs` | ⚑ as above |
| `OverlayPatch(name, paths)` | **disappears** → `Workdir().Apply`/patch | ⚑ Overlay internal; folds into the mode-agnostic patch path |
| `AdvanceBaseline(name)` | `Workdir().AdvanceBaseline()` | matches api_surface |
| `UpdateOverlayBaseline(name, hostPath)` | **disappears** → `Workdir().AdvanceBaseline`/`SetBaseline` | ⚑ Overlay-explicit baseline update; folds into the mode-agnostic baseline ops |
| `ListCommits(name)` | `Workdir().Commits(CommitOptions{})` | ⚑ New `Commits` query, or reuse `BaselineLog`. api_surface has `Workdir.BaselineLog` (inception→HEAD, baseline marked). Recommend one `Commits`/`BaselineLog` method with options. |
| `ListCommitsWithStats(name)` | **fold into** `Workdir().Commits(CommitOptions{WithStats:true})` | ⚑ stat is an option |
| `ListCommitsOverlay(name)` | **disappears** → `Workdir().Commits` | ⚑ overlay internal |
| `ResolveCommitRefs(name, refs)` | `Workdir().ResolveRefs(refs)` | ⚑ Or `Commits(CommitOptions{Refs})`. Recommend a small dedicated method. |
| `HasUncommittedChanges(name)` | `Workdir().HasUncommittedChanges()` | ⚑ Or expose via a `Workdir().Status()`. Recommend the boolean method. |

### `Files()` / `Network()`

- **`Files()`** — no Client-root methods to re-home today; `Put/Get/Ls/Rm` are
  currently CLI-only (`internal/cli/workflow/files.go` reads the host dir
  directly). Wiring them onto the Client as `Sandbox(name).Files().*` is a
  *separate* gap (new surface), not part of F2's re-rooting. Flag for a later
  finding if we want them on the Client.
- **`Network()`** — **already done** (`network.go`: `Allow`/`Deny`/`Allowed`).

## Net effect

~33 public `Client` methods → **6 root** (`Run`/`Create`/`Clone`/`List`/
`EnsureSetup`/`Close`) + a `Sandbox(name)` handle with ~12 direct methods +
`Workdir()` (≈7 methods after collapse) + `Files()`/`Network()`. The four
overlay-explicit methods leave the public surface entirely.

## Decisions needed from the owner

1. **Overlay-explicit methods disappear** (DiffOverlay, ListCommitsOverlay,
   OverlayPatch, UpdateOverlayBaseline) — confirm the surface should be
   mode-agnostic (copy/overlay resolved internally), per api_surface.
2. **`Diff*` collapse to one `Workdir().Diff(DiffOptions)`** and **`GeneratePatch`/
   `FormatPatch` route through `Apply` (DryRun / ApplyExport)** vs. keeping
   distinct `Workdir().Patch` / `.FormatPatch` methods.
3. **`StdioExec` folds into `Exec`** (ExecOptions mode) vs. stays a distinct method.
4. **`Commits` family** shape — one `Commits(opts)` vs. separate methods +
   reuse of `BaselineLog`.
5. **`ContainerLogs`, `NeedsConfirmation`, `SandboxDir`** stay as distinct
   `Sandbox` methods (recommended) vs. folded/renamed.
6. **`Files()` Client wiring** — in scope as a follow-up finding, or leave
   exchange-dir ops CLI-only.
