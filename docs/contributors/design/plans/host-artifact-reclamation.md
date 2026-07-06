<!-- ABOUTME: Plan to make yoloAI reap the host-side artifacts (broker processes, -->
<!-- ABOUTME: netns/IPAM, seatbelt tmux) it orphans on the non-happy path. D114. -->

# Host-artifact reclamation

**Status:** Phase 1 (injector + netns + **seatbelt host process group, done**)
and Phase 2 built + unit-tested on branch `host-artifact-reclamation`. The
seatbelt reaper (Phase 1c) is built and macOS-verified — see the
[macOS build brief](host-artifact-reclamation-macos-build.md) for results. It
surfaced a residual (DF75, orphaned monitor procs resurrecting deleted dirs)
which was **fixed in the same pass** by generalizing the sweep from tmux-only to
the whole host process group. Phase 3 is a manual step.
**Decision:** [D114](../../decisions/working-notes.md#d114). **Findings:** DF71–DF75.

## Problem

After a round of v0.7.0 backend testing the dev host held, with `yoloai ls`
reporting **no sandboxes**:

- a **4-day-old `yoloai __inject` root process** holding a listening socket on
  the bridge gateway (`10.89.0.1:33371`), its `exe` already `(deleted)` (binary
  rebuilt under it);
- two leaked **CNI network namespaces** `yoloai-yoloai-x` / `yoloai-yoloai-kreach`
  (each with a `tap0_kata` device) from containerd/Kata runs;
- **5.5 GB** of retired-microvm library files (`library/microvm/rootfs.ext4` …).

None of these was reclaimable by `yoloai system prune`, and `yoloai doctor`
reported the machine healthy.

## Root cause

yoloAI tears down host-side artifacts **only on the happy path, keyed off
per-sandbox state files** — `injector.json` (broker PID), `cni-state.json`
(netns), and the sandbox directory itself. Those state files can vanish
**independently** of the artifact they describe (a crash between artifact
creation and state-write; a SIGKILL/timeout; the `create`-replace path deleting
the sandbox dir *before* killing the process). When the state file is gone,
nothing enumerates the artifact **directly**, so it orphans permanently — and
neither reconciler can find it:

- **`system prune`** is scoped to **backend containers/VMs matched by
  `com.yoloai.*` labels** (`runtime.IsOrphanCandidate`), plus images/volumes/
  caches, `.lock` files, and `yoloai-*` temp dirs. It enumerates **no** host
  processes, netns, bridges, or tmux servers.
- **`doctor`** runs **backend/tool health checks** plus a dry-run of that same
  container-scoped prune. Its only host-process reconciliation is the tart
  VM-slot census (macOS, report-only). Nothing diffs host netns / broker
  processes / tmux against the sandbox registry.

The unifying fix: **host artifacts whose identity is encoded in a name or path
must be reconcilable by a sweep that enumerates them directly and diffs against
the sandbox registry (`SandboxesDir()`), not one that trusts a per-sandbox state
file to still exist.** yoloAI already does exactly this for stale Kata shims
(`killStaleKataShims` walks `/proc`) and tart VM processes — the pattern is
proven; it was never generalized to the broker, netns, or library debris.

## Design

### Phase 1 — host-orphan sweep (new capability)

A reconciliation pass wired into `system prune`, so `doctor`'s dry-run surfaces
it automatically (doctor calls `Prune(DryRun:true)`). The registry it diffs
against is the existing "enumerate `SandboxesDir()` + `LoadEnvironment`" set
(the `classifySandboxes` `known` set). Each artifact class enumerates itself by
its identity-encoding name/path:

1. **Broker processes** (Linux + macOS). Collect the set of live-sandbox broker
   PIDs from every known sandbox's `injector.json`. Walk `/proc` for processes
   whose argv is `<our-binary> __inject` (exe basename `yoloai`, or `(deleted)`).
   Any such process **not** in the live set → `SidecarHost`-style
   SIGTERM→SIGKILL. Reaps the observed 4-day orphan.
2. **netns + IPAM leases** (containerd/Linux). Enumerate
   `/var/run/netns/yoloai-yoloai-*`; the name fully encodes the sandbox
   (`netnsNameFor(name) = "yoloai-"+name`, container name `"yoloai-"+sandbox`).
   For any netns whose sandbox is not in the registry → `deleteNetNS` + clean the
   matching `/var/lib/cni/networks/yoloai/<ip>` lease. Reaps both observed netns.
   The shared `yoloai0` bridge is **left alone** (intentionally persistent —
   `reach.go`).
3. **Seatbelt host process group** (darwin) — **DONE** (macOS build brief
   [host-artifact-reclamation-macos-build.md](host-artifact-reclamation-macos-build.md),
   Task B; `runtime/seatbelt/prune.go`). Enumerates via `ps` every host process
   whose argv points under this data dir's sandboxes but whose sandbox is not in
   the known set — the tmux server AND the detached `sandbox-setup.py` /
   `status-monitor.py` (DF75), the latter of which otherwise keeps writing into
   and resurrecting the deleted dir. tmux servers die via `kill-server` (fallback
   SIGTERM→SIGKILL on the PID when the socket is gone, the common case);
   everything else by PID (ESRCH-tolerant). Pure decision unit-tested;
   macOS-verified against real leaked processes. The injector reaper also runs on
   macOS via the `ps` path (brief Task A, verified).

Each reaped artifact is reported (name + kind) and counted into the prune
result; `doctor` lists them under "Reclaimable now". Dry-run enumerates without
killing.

**Scoping caveat (documented, deferred — sibling of DF45).** A leaked
`__inject` process carries no data-dir in its argv (config arrives on stdin), so
the broker sweep is correct for the **single-data-dir** default but could
over-reap a broker belonging to a *different* data dir sharing the host. Ships
scoped to the current data dir with the limitation noted; precise per-principal
scoping is deferred to the D62 multi-principal daemon (add a data-dir marker to
the broker's identity then).

### Phase 2 — kill-before-delete ordering (stop creating unreapable orphans)

Phase 1 is the backstop; Phase 2 stops the orphans being created in the first
place, so the state file that keys teardown never predeceases the artifact:

- **`create`-replace path** (`create.go`): call `stopInjector` (and CNI
  teardown) **before** `launch.Teardown` deletes the sandbox dir — mirroring
  what `lifecycle.destroy` already does (`lifecycle.go`). This is the exact hole
  that produced the observed orphan (dir + `injector.json` deleted, PID never
  killed).
- **containerd teardown**: run `deleteNetNS` **before** `forceRemoveAll(sandboxDir)`
  so a failed netns delete doesn't first lose `cni-state.json`, the only key that
  lets teardown re-find the netns.

### Phase 3 — retired-microvm debris

`library/microvm/` was **never part of a released library schema** — it is
debris from the unmerged `microvm-backend` spike (D104), so it cannot exist on
any real user's install. Therefore:

- **No speculative product code.** A general "sweep unknown `library/`
  subdirs" pass is rejected (YAGNI + it risks deleting future legit dirs).
- **The general pattern already exists and shipped:** a *released* retired
  backend gets a **library-schema migrator** (exactly how `:overlay` retirement
  removed its on-disk state via `migrate_overlay.go`). If microvm had ever
  shipped, a v3→v4 migrator would remove it. Nothing to build.
- **On this dev host:** the `library/microvm/` tree is a one-off manual
  `rm -rf` (operator step; recorded in `reference_disk_reclaim_recipes`).

## Out of scope (follow-up)

- **Signal-handler teardown** — `main.go` only cancels the context on
  SIGINT/SIGTERM with no deferred injector/netns kill. Wiring a best-effort kill
  on SIGTERM would stop the common Ctrl-C-during-test leak at the source. SIGKILL
  is unavoidable (that is what Phase 1 is for). Separate subsystem, separate
  change — proposed as a follow-up, not part of this plan.

## Acceptance criteria

- `yoloai system prune` reaps: a leaked `__inject` process with no live sandbox;
  a `/var/run/netns/yoloai-yoloai-<x>` with no owning sandbox (+ its IPAM lease);
  (macOS) a leaked seatbelt tmux server. Each is reported.
- `yoloai doctor` (dry-run) lists the above as reclaimable when present, and
  reports clean when absent.
- The `create`-replace path and containerd teardown no longer orphan a broker
  process / netns when the sandbox dir is removed.
- Unit tests: `/proc`-walk broker matcher (owned vs orphan), netns name↔sandbox
  mapping + registry diff, ordering-fix teardown sequence. Integration: real
  Docker/containerd where the daemon is available; smoke-tier coverage where the
  sweep is exercisable.
- `make check` green.
