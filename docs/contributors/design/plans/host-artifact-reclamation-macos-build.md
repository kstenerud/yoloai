> **ABOUTME:** macOS verification brief for the darwin-only pieces of host-artifact-reclamation: the
> `ps`-based injector reaper and the seatbelt-tmux reaper, neither authorable nor verifiable on
> Linux. Completed; results and a residual finding are recorded inline.

# Build brief (macOS agent): host-artifact reclamation — darwin injector verify + seatbelt-tmux reaper

**Status: DONE (macOS agent, 2026-07-06).** Both tasks completed and verified on
macOS 26 / Apple Silicon. Task A ✅ (darwin injector reaper reaps a real orphaned
`__inject`, spares a live one). Task B ✅ (seatbelt-tmux reaper built, unit-tested,
and verified against real leaked servers). One prerequisite fix and one residual
finding (DF77) surfaced — see **Results** at the bottom. Committed on
`host-artifact-reclamation` (fix `d75b68fe`, reaper `5f7d056a`).

**Status: START HERE (macOS agent).** Design is settled in
[host-artifact-reclamation.md](host-artifact-reclamation.md) and [D114](../../decisions/working-notes.md#d114)
— read them first for the root cause and the identity-keyed-sweep principle; **build to it, do not
re-litigate it.** The Linux-verifiable pieces (the broker injector reaper, the containerd netns
sweep, the kill-before-delete teardown) are **already committed** on branch
`host-artifact-reclamation` (commit `c2feca66`). Do **not** duplicate or rewrite them; if something
conflicts, prefer what's committed and flag it.

Branch: `host-artifact-reclamation`. Pull it, `make build`, confirm `make check` is green on the Mac
before changing anything.

## Why these are macOS-only

- The darwin injector enumeration (`platformInjectorPIDs` in
  `internal/broker/reap_darwin.go`) shells `ps -axo pid=,args=` — it typechecks under `crosscheck`
  on Linux but has never *run* on macOS.
- The seatbelt backend runs tmux as a **host** process (unlike the container backends, where tmux
  lives inside the guest and dies with it). Reaping a leaked seatbelt tmux server is inherently a
  macOS-only mechanism. This part is an **exception to the queue's "Linux is sole writer" rule** —
  like [confine-host-side-git-macos-build.md](confine-host-side-git-macos-build.md), it is authored
  on the Mac because it can't be written or tested elsewhere. Commit it on this branch.

## Prerequisites

- macOS 26+ on Apple Silicon; `seatbelt` (`sandbox-exec`, `tmux`) working; `yoloai` can create/run a
  seatbelt sandbox. A brokered agent (`--agent claude`, default brokering) for Task A.
- `make check` green before you start.

## Task A — verify the darwin injector reaper (verification only, no code)

Confirms `internal/broker/ReapOrphanInjectors` reaps a real orphaned `__inject` on macOS via the
`ps` enumeration path. Mirror the Linux validation:

1. Create + run a brokered sandbox so a real `yoloai __inject` process spawns (check
   `~/.yoloai/sandboxes/<name>/injector.json` for its PID; confirm the process is alive).
2. Simulate the DF73 leak: delete the sandbox dir out from under the running injector —
   `rm -rf ~/.yoloai/sandboxes/<name>` — so `injector.json` (the PID record) is gone but the
   detached process keeps running. Confirm the `__inject` PID is still alive.
3. `./yoloai system prune --dry-run` → must list `process __inject pid <PID>` under "Orphaned
   resources". Then `./yoloai system prune` → the process is gone (verify with `ps -p <PID>`).
4. Also confirm **no false positive**: with a *live* brokered sandbox present (its `injector.json`
   intact), `--dry-run` must **not** list that sandbox's injector.

Record ✅/❌ + the dry-run output. If the `ps` parse misbehaves (wrong PID, misses the process, or
reaps a live one), report the exact `ps -axo pid=,args=` line for the injector so the Linux session
can fix `parsePsLine`/`platformInjectorPIDs`.

## Task B — implement + verify the seatbelt-tmux reaper (Phase 1c)

The seatbelt tmux server (`tmux -S <sandboxDir>/tmux/tmux.sock`, session `main`) is a host process.
On `Stop`, `runtime/seatbelt/seatbelt.go` best-effort `kill-server`s it; if that fails and
`launch.Teardown` then deletes the sandbox dir, the server survives holding a now-deleted socket
path — unreapable by name (`killStaleKataShims` has no seatbelt analogue). `runtime/seatbelt/prune.go`
`Prune` is currently a no-op. Give it a reaper, following the **identity-keyed sweep** principle and
the shape of the two committed reapers (`internal/broker/reap*.go`, `selectOrphanNetns` in
`runtime/containerd/prune.go`):

1. **Enumerate tmux server processes** (via `ps`, since a leaked server's socket file may be
   deleted) whose `-S <socket>` argument points under this data dir's `SandboxesDir()`
   (`layout.SandboxesDir()`). The socket path encodes the sandbox — derive the sandbox name from it
   (`<sandboxesDir>/<name>/tmux/tmux.sock`; use the same socket helper the backend uses, e.g.
   `runtime.TmuxSocketFor` / the seatbelt socket path func — match it, don't hardcode).
2. **Reap** any whose sandbox is not in the passed `knownInstances` and whose metadata doesn't load
   (an orphan): `tmux -S <socket> kill-server` (and, if that fails because the socket is gone, fall
   back to killing the PID). Under `dryRun`, only report. Emit a `runtime.PruneItem{Kind: "tmux",
   Name: <sandbox-or-socket>}` per reaped server. `Prune` receives `knownInstances []string` — build
   the same lookup the other backends do; `IsOrphanCandidate` doesn't apply (no labels here).
3. **Factor the pure decision** (given `[]{pid, socketPath}` + known set + sandboxes root → which to
   reap) into a small function and **unit-test it** (no root, no real tmux) — mirror
   `selectOrphanNetns` + `netns_sweep_test.go`.
4. **Integration-verify on the Mac:** start a seatbelt sandbox (its tmux server runs), then simulate
   the leak — `rm -rf` the sandbox dir while the server is alive (or kill the metadata and stop) —
   and confirm `./yoloai system prune` reaps the orphaned tmux server (and, critically, does **not**
   reap the tmux server of a *live* seatbelt sandbox).

Scope note: the cross-data-dir over-reap caveat (DF45 sibling) applies here too — scope to this data
dir's `SandboxesDir()`; that's sufficient. `make check` (native darwin) must be green, including the
new unit test. Commit on `host-artifact-reclamation`.

## When done

- Update this brief's status and record results (✅/❌ + output) under each task.
- Flip the plan's Phase-1c line in [host-artifact-reclamation.md](host-artifact-reclamation.md) from
  "deferred" to done, and drain any DF76 residual accordingly.
- Report back so the Linux session can fold results in before merge.

## Results (2026-07-06, macOS 26 / Apple Silicon)

### Prerequisite fix (make check was NOT green on the committed branch)

`make check` failed on a real Mac at the lint stage: committed `internal/broker/reap_darwin.go`
called `exec.Command("ps", …)` directly, which the project's forbidigo linter bans (subprocesses
must go through `internal/sysexec` with an explicit, never-inherited env). golangci-lint only
analyzes host-GOOS files, so this darwin-tagged file was never linted on the Linux dev host and
`crosscheck` (`go vet`) doesn't run the linter — so it slipped through. Routed the census through
`sysexec.Command` with a minimal PATH env (commit `d75b68fe`). `make check` green afterward.
**Linux session: note this — any future darwin-only file needs a Mac lint pass, not just crosscheck.**

### Task A — darwin injector reaper ✅

Created a brokered seatbelt sandbox with a dummy `ANTHROPIC_API_KEY` (claude's default OAuth is
file-based and isn't brokered via the HTTP injector, so no `__inject` spawns — an API-key env var is
required). Injector PID recorded in `injector.json`. `rm -rf`'d the sandbox dir (DF73 leak); the
`__inject` process stayed alive. `system prune`:

```
Orphaned resources:
  process __inject pid 97370
...
Removed process __inject pid 97370
```

`ps -p 97370` → gone. A concurrently-live brokered sandbox's injector (a different PID) was **not**
listed by `--dry-run` and **survived** the real prune. The darwin `ps` parse (`parsePsLine`/
`platformInjectorPIDs`) selected the right PID and only the orphan. **No false positive.**
(Note: `system prune` requires `-y` / a TTY to confirm; a piped non-TTY invocation previews but does
not act — not a reaper bug.)

### Task B — seatbelt-tmux reaper ✅

Built in `runtime/seatbelt/prune.go` (+ `prune_darwin.go` / `prune_other.go` platform split for the
`ps` enumeration, mirroring the injector reaper). Pure `selectOrphanTmux` / `sandboxDirFromSocket` /
`parseTmuxServerLine` unit-tested in `prune_test.go` (no root, no tmux). Reaps via
`tmux -S <socket> kill-server`, falling back to SIGTERM→SIGKILL on the PID when the socket is gone
(the common leak case — verified sockets are always deleted with the dir).

Verified against real pre-existing cruft on the host (42 leaked seatbelt processes across **two**
data-dir roots, `yoloai ls` reporting zero sandboxes):

- `system prune` reaped all **9** in-scope orphan tmux servers under `~/.yoloai/library/sandboxes/`
  (including all **3** stacked servers for a repeatedly-leaked name `x`).
- It **spared** the **4** servers under the legacy root `~/.yoloai/sandboxes/` (a different data dir) —
  correct per the DF45-sibling scoping caveat.
- With a freshly-created **live** seatbelt sandbox present (`active`, in the registry), `--dry-run`
  did **not** list its server and the real prune **spared** it. **No false positive.**

### Residual — DF77 (found AND fixed in this pass)

Initial tmux-only reaping left a seatbelt sandbox's sibling `status-monitor.py` / `sandbox-setup.py`
host processes (detached, ppid=1, separate tree) running after the server was reaped. Worse, the
surviving `status-monitor.py` keeps **writing into the sandbox dir** (`agent-status.json`, `home/`,
`logs/`), so after `rm -rf` + tmux reap it **recreated** the dir and `yoloai ls` showed a phantom
`broken` sandbox — defeating prune's own dir cleanup.

Since this is a real (crash-path) user problem, it was fixed here (commit `3a860e65`): the seatbelt
sweep was generalized from tmux-only to the whole identity-keyed **host process group** — any host
process whose argv points under an orphaned sandbox dir is reaped. Verified on macOS: `system prune`
reaped 4 leaked monitors (sbcheck / wa-orphan / wa-orphan2 / wc-orphan), the resurrected dirs were
then cleaned by the broken-dir classification and stayed gone, a live sandbox's whole process group
was spared, and another data dir's processes were spared. No prevention change was needed — normal
`destroy`/`stop` already cleans the monitor (its `tmux wait-for` returns and it exits); only the
crash/SIGKILL path creates the orphan, which is exactly what this backstop reconciles.

## Task C — verify the cross-platform lint/vet on the darwin→linux direction (verification only)

`make check`'s `crosscheck` and `lint-cross` now iterate `LINT_PLATFORMS`
(`linux/amd64 darwin/arm64` in the Makefile), cross-vetting/linting every GOOS
that isn't the host. On Linux this was verified to cross-check darwin (unchanged
from before). The **darwin→linux direction has only run on Linux-hosted CI**, never
from a Mac — so confirm it here:

1. `make crosscheck` on the Mac → must print `>> go vet linux/amd64` and exit 0
   (it cross-compiles the linux-only backends — `runtime/containerd`, etc. — which
   is the untested direction; watch for cgo/toolchain errors that only a Mac shows).
2. `make lint-cross` on the Mac → must print `>> golangci-lint linux/amd64` and exit 0.
3. `make check` overall green on the Mac.

If the linux cross-vet/lint hits a cgo or cross-toolchain wall on macOS that can't
be resolved cheaply, report the exact error — we may need `CGO_ENABLED=0` on the
cross passes or to scope the linux lint. Record ✅/❌ + output.
