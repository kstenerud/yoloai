> **ABOUTME:** Design for an opt-in `yoloai system setup-gvisor` (macOS) to install and register
> runsc in the Docker VM so `container-enhanced` works there. Captures the motivation, the
> Docker-Desktop-vs-OrbStack provider tradeoffs, and a phased implementation plan.

# `yoloai system setup-gvisor` (macOS)

- **Status:** PLANNED — paused 2026-06-06. Phase 0 (the R-DD spike) ran and came back negative on
  both macOS providers — see [Phase 0 verdict](#phase-0-verdict-gvisor-on-macos-is-not-turn-key--pause-recommended).
  gVisor on macOS is not turn-key; the recommendation is to keep `container-enhanced`
  Linux-primary and defer the macOS command. This doc is retained as the record + revival plan.
- **Depends on:** —

## Why this doc exists

`container-enhanced` (gVisor) is allowed on macOS hosts (D69/D70) once `runsc` is installed
and registered in the Docker daemon's Linux VM. But neither Docker Desktop nor OrbStack
ships runsc, so on a fresh Mac `--isolation container-enhanced` fails — there is no install
path. This doc designs an **opt-in** `yoloai system setup-gvisor` command that does that
setup explicitly, captures the motivation (is gVisor on macOS even worth it?), and lays out
a phased implementation plan.

## Motivation — is gVisor on macOS worth it?

Researched 2026-06-05 (sources at the bottom). Two distinct value props, pitched honestly:

1. **Production parity (primary reason, macOS-neutral).** Google Cloud Run, App Engine,
   Cloud Functions, and GKE Sandbox run workloads **under gVisor in production**. gVisor has
   real syscall-compatibility quirks (some syscalls unimplemented or behave differently), so
   "works on my Docker, breaks under gVisor in prod" is a real failure class. Running locally
   under `container-enhanced` catches those *before* deploy. This value is identical on macOS
   or Linux and does not depend on the security argument.

2. **Defense-in-depth (secondary on macOS).** gVisor's Sentry intercepts the container's
   syscalls in userspace, so the workload talks to the gVisor kernel, not the host Linux
   kernel — an attacker must break out of gVisor before reaching the kernel. That boundary is
   intact even when gVisor runs *nested in a VM* (gVisor's own docs only flag VM nesting as a
   *performance* concern — use `systrap`, slower than KVM — not a security one). The honest
   caveat: on **bare Linux** gVisor is the primary kernel-isolation wall; on **macOS** the
   workload is already behind Docker Desktop's hardware VM, so gVisor is a *second* layer
   (workload → gVisor → VM kernel → VM boundary → macOS). Real and additive, but lower
   marginal host-protection than on Linux. For yoloAI — which runs untrusted / AI-generated
   code — the extra layer is on-mission, just not the main wall on macOS.

**Real-world precedent:** Dangerzone (Freedom of the Press Foundation) runs gVisor *inside
Docker Desktop on macOS and Windows* to sandbox untrusted document rendering — a
security-serious product depending on exactly this configuration. So the config is viable and
used in the wild, not theoretical.

**Verdict:** worth supporting as an **opt-in** mode, framed **parity-first, security-second**
on macOS. Not a default (setup cost, systrap perf hit, syscall-compat surprises).

## The calculus shift (from the 2026-06-05 dind/provider work)

This session's docker-storage-driver research (`../research/dind-storage-drivers.md`) and
context/provider work changed two things in our favor:

- **Provider detection is solved.** `docker info --format '{{.OperatingSystem}}'` cleanly
  distinguishes `Docker Desktop` / `OrbStack` / Podman, and `docker context` resolution is
  wired through the Go client. The command's "detect the VM-backed daemon" step is no longer
  speculative — reuse this.
- **The provider quirks are complementary, and they make Docker Desktop the *favorable*
  gVisor target** (the reverse of dind):
  - **Docker Desktop** (LinuxKit): can't exec fuse-overlayfs (so dind needed a volume) **but
    has a normal `/tmp`** → **no gVisor chroot collision**. Favorable for gVisor.
  - **OrbStack**: dinds fine, **but `/tmp → /private/tmp` (virtiofs) breaks runsc's hardcoded
    `/tmp` chroot**. Blocked for gVisor without a workaround.

So the make-or-break collapses to **R-DD**: if runsc can be installed into Docker Desktop's
VM, gVisor works there cleanly with **no `/tmp` hack**. OrbStack becomes the second-class
path that needs the `/tmp` tradeoff.

## What the command does

`yoloai system setup-gvisor` (macOS, docker backend) — idempotent, reversible, opt-in.
yoloAI never mutates the user's Docker VM on a normal `new`.

1. **Detect** the VM-backed daemon (OrbStack vs Docker Desktop via `docker info`
   OperatingSystem) and the VM architecture (arm64/amd64).
2. **Install runsc into the VM** (not the macOS host): download the matching-arch binary,
   verify its checksum, place it where the daemon can exec it.
   - *Docker Desktop (primary):* LinuxKit rootfs is read-only — installing into a persistent,
     exec-able location is **R-DD** (Phase 0).
   - *OrbStack (secondary):* writable VM rootfs; install via a privileged `--pid=host` helper
     that `nsenter`s the VM mount namespace (the manual path already verified this session).
3. **Register** runsc as a daemon runtime (`daemon.json` `runtimes`, `--platform=systrap` so
   no nested virtualization is needed) and reload the daemon.
4. **Verify**: `docker run --runtime=runsc … echo ok`; report success/failure with the real
   reason.

`--check` (dry-run/diagnose, no mutation) and `--remove` (inverse) round it out.

## The blocking constraint: OrbStack `/tmp` (secondary path only)

runsc hard-codes its sandbox chroot at `/tmp` and runs a mount-safety check that the resolved
path matches. OrbStack symlinks the VM's `/tmp → /private/tmp` (the macOS host over
virtiofs), so the check fails: `expected to open /tmp, but found /private/tmp` (surfaces as
`cannot read client sync file: EOF`). See `backend-idiosyncrasies.md`. **There is no clean
per-process workaround** (TMPDIR doesn't move the hardcoded chroot; bind-mounts don't remove
the symlink indirection; a container `/var/lib/docker`-style volume is the wrong layer —
this is the VM's own `/tmp`).

The only ways to run gVisor on OrbStack, both unattractive as a default:

- **(a) Replace the VM's `/tmp` symlink with a real directory** (global) — works, but breaks
  OrbStack's macOS-`/tmp` sharing.
- **(b) `--TESTONLY-unsafe-nonroot`** in the runtime args — skips the chroot but disables a
  gVisor security boundary. Unacceptable for a "secure isolation" mode.

**Decision (deferred to Phase 2):** because Docker Desktop is now the favorable target with
*no* `/tmp` issue, OrbStack gVisor can start as **(b-equivalent) refuse + steer to Docker
Desktop**, and only later offer (a) behind an explicit confirmation + `--remove` restore if
there's demand. We are not blocked on this decision to ship the Docker Desktop path.

## Implementation plan

### Phase 0 — R-DD spike (decisive; do before building anything)

Resolve whether runsc can be installed **persistently** and **exec-ably** in Docker
Desktop's read-only LinuxKit VM, and registered as a runtime. Concretely: get the runsc
arm64 binary into a writable, exec-able VM path the daemon can see; add it to the VM's
`daemon.json` `runtimes` (with `--platform=systrap`); restart the daemon; run
`docker run --runtime=runsc --rm hello-world`. Then confirm it **survives a Docker Desktop
restart** (the VM is recreated on update/restart). Candidate persistence mechanisms to try:
a `/var/lib`-backed path, the Docker Desktop settings-managed `daemon.json`, or a Docker
Desktop extension. **Output: go/no-go on Docker Desktop as the primary target.** If no-go,
re-scope to OrbStack-with-`/tmp`-tradeoff as primary.

#### Phase 0 result (2026-06-06): naive registration breaks the engine ❌

First attempt (the dev.to recipe): runsc downloaded + checksum-verified into a Docker
**volume** (`/var/lib/docker/volumes/runsc-runtime-binaries/_data/runsc`; it ran —
`runsc version release-20260601.0`), registered in `~/.docker/daemon.json` as
`runtimes.runsc.path` with `runtimeArgs: ["--platform=systrap"]`, then quit+restart Docker
Desktop. **Docker Desktop's engine failed to start** — "running engine: service failed",
stuck on diagnostics, had to force-quit. Reverting `daemon.json` + restart restored it
cleanly (verified byte-identical to the pre-spike backup; containers run). The **runsc binary
is fine**; the **registration** takes the engine down. Unlike bare-Linux dockerd (which logs a
bad runtime and continues), Docker Desktop's supervisor treats the daemon error as a hard
failure.

Root cause not yet captured (would need the engine log at
`~/Library/Containers/com.docker.docker/Data/log/`). Hypotheses: dockerd probes the runtime at
startup (e.g. `runsc features`) and that exec fails in the early init context; or the
volume-backed path isn't valid/exec-able when dockerd validates `runtimes`.

**Implication:** the Docker-Desktop "favorable target" advantage (normal `/tmp`) is undercut if
we can't register runsc without killing the engine. Combined with OrbStack (runsc installs +
registers fine, but the `/tmp` chroot blocks *execution*), **neither macOS provider currently
has a clean path** — Docker Desktop breaks at *registration*, OrbStack at *run*. This
materially weakens the build-it-now case (decision point).

Second cycle (deeper, 2026-06-06) — narrowed but not fully root-caused:

- **Vanilla nested dockerd starts fine with runsc registered.** Running a plain `dockerd` in a
  privileged container on Docker Desktop's *same VM kernel*, with the identical runsc runtime
  in its `daemon.json`, started cleanly. So it is **not** "dockerd can't load runsc" — the
  failure is specific to **Docker Desktop's engine/supervisor config-apply**, not dockerd or
  runsc.
- **Docker Desktop did read the staged binary** — the VM console log shows it enumerating the
  volume and finding `runsc` (`-rwxr-xr-x`, correct size). So the volume-path mechanism works;
  the engine still "service failed" at startup. The precise cause stays **opaque** (not clearly
  logged in `dockerd.log`; would need detailed `com.docker.backend.log` spelunking around the
  failure timestamp — and the likely culprit is Docker Desktop regenerating/validating its
  Settings-managed engine config, which hand-editing `~/.docker/daemon.json` conflicts with).
- **Even when runsc *runs* (nested), it hits a cgroup-v2 error:**
  `cgroup.subtree_control: device or resource busy` — a second, separate blocker (at least
  under nesting). gVisor's `--ignore-cgroups` is the known lever but trades resource limits.

### Phase 0 verdict: gVisor on macOS is NOT turn-key — pause recommended

Across both cycles: the **runsc binary works**, but **Docker Desktop breaks at registration**
(opaque supervisor failure, twice; reverting always restores), **OrbStack breaks at run**
(`/tmp` chroot), and there's a **cgroup-v2 hazard** on top. A robust `setup-gvisor` would be a
significant, fragile, Docker-Desktop-internal-dependent effort fighting multiple independent
blockers. Meanwhile the **primary value (production parity) is Linux-anchored and already
works on Linux**, and on macOS gVisor is only defense-in-depth atop the existing VM.

**Recommendation: pause gVisor-on-macOS.** Keep `container-enhanced` **Linux-primary**; treat
macOS as not-currently-supported for gVisor (manual setup only, unsupported). Revisit if (a)
there's real user demand, or (b) Docker Desktop gains a sanctioned runtime-install path
(extension) that doesn't fight the Settings-managed engine config. The gate (D70) can stay
permissive, but `new`/`system check` should say "gVisor on macOS needs manual runsc setup and
is unsupported" rather than imply it works out of the box — a small honesty fix, separate from
building the command.

Deferred next options if revived (each needs another controlled DD break + backend-log
capture): read `com.docker.backend.log` to pin the supervisor failure; register via Docker
Desktop's Settings-store instead of `~/.docker/daemon.json`; test runsc on the *real* engine
(single-nested) for the cgroup behavior; add `--ignore-cgroups`.

### Phase 1 — the command, Docker Desktop first

Build `yoloai system setup-gvisor` (`internal/cli/system/`): detect → download+checksum-verify
the arch-matched runsc → install into the VM (Phase-0 mechanism) → register in the VM
`daemon.json` (systrap) → reload → verify. Implement `--check` and `--remove`. Keep the pure
bits (provider classification, runsc version/URL/checksum selection, daemon.json runtime
merge) as testable functions; isolate the impure VM-mutation behind a thin seam.

### Phase 2 — OrbStack support

Install is easy (writable rootfs + nsenter helper). Settle the `/tmp` decision: ship
refuse-and-steer first; optionally add the global `/tmp` replacement behind explicit confirm
+ `--remove` restore if users want it.

### Phase 3 — diagnostics + wiring

`yoloai system check --isolation container-enhanced` already checks daemon registration
(`gvisorRegistered`, daemon-location-aware). Point its Fix step and `new`'s failure hint at
`yoloai system setup-gvisor`. Surface a friendly error when runsc is unregistered.

### Phase 4 — tests + docs

Unit-test the pure helpers; add an integration/smoke path if feasible (likely keep
`container-enhanced` unscheduled in the smoke matrix until setup is turn-key, or schedule it
on Docker Desktop once `setup-gvisor` exists). Update `docs/GUIDE.md` (macOS gVisor setup), a
D-entry, and `backend-idiosyncrasies.md`.

## Open research

- **R-DD (Phase 0):** persistent, exec-able runsc install in Docker Desktop's LinuxKit VM.
- **Persistence across VM restarts/updates:** does an install survive a Docker Desktop / VM
  restart, and should `system check` detect a drifted/removed binary and say so?

## References

- D69, D70 — `../../decisions/working-notes.md` (enhanced allowed on macOS; daemon-location runsc check).
- `../research/dind-storage-drivers.md` — provider detection + the complementary-quirks finding.
- `../../backend-idiosyncrasies.md` — the OrbStack `/tmp` gVisor chroot collision.
- `../../../GUIDE.md` — gVisor setup (Linux + macOS).
- [podman-gvisor.md](podman-gvisor.md) — the sibling backend; a Podman Machine setup story would parallel this.
- Research sources (2026-06-05): gVisor "Safe Ride into the Dangerzone" (Dangerzone on macOS/Windows via Docker Desktop); gVisor Production guide (systrap-in-VM guidance); Releasing Systrap; gVisor production users (Cloud Run / App Engine / Cloud Functions / GKE Sandbox).
