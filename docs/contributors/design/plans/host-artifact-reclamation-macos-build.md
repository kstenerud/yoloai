# Build brief (macOS agent): host-artifact reclamation — darwin injector verify + seatbelt-tmux reaper

<!-- ABOUTME: Actionable macOS brief for the parts of host-artifact-reclamation that can't be -->
<!-- ABOUTME: authored/verified on Linux: the darwin `ps` injector path and the seatbelt-tmux reaper. -->

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
2. Simulate the DF71 leak: delete the sandbox dir out from under the running injector —
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
  "deferred" to done, and drain any DF74 residual accordingly.
- Report back so the Linux session can fold results in before merge.
