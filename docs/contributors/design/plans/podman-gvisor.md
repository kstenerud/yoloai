<!-- ABOUTME: Plan to support gVisor (container-enhanced) on the Podman backend, -->
<!-- ABOUTME: with rootless podman as a first-class research track, not an afterthought. -->

# Podman + gVisor (`container-enhanced`) support

## Why this doc exists

`container-enhanced` (gVisor / `runsc`) now works on the **docker** backend on both
Linux and macOS hosts (D69, D70): the macOS host-OS block was removed and the runsc
prerequisite check follows the daemon's location instead of the host `$PATH`. The
**podman** backend was deliberately left untouched in that round because Podman Machine
wasn't tested and rootless podman carries a real, unverified gVisor blocker.

This plan captures what it takes to bring podman to parity — and treats **rootless
podman as a first-class goal**, not something we route around with "just use rootful."
Many users choose podman precisely *because* it is daemonless and rootless; a
gVisor story that only works rootful would miss most of that audience.

Nothing here is implemented. Status: **planning / research.**

## Current state (grounded)

Probed on a dev machine (Apple Silicon, `podman-machine-default`, applehv, running):

- The machine is **rootless by default** (`podman machine inspect … Rootful` → `false`).
- Default OCI runtime is `crun`; `runsc` is **not** configured.
- `podman` exposes a Docker-compat socket; yoloai's podman backend embeds
  `docker.Runtime` and talks to that socket (`runtime/podman/podman.go`).

What blocks gVisor today (`runtime/podman/`):

1. **Rootless hard-block.** `buildRootlessCheckCap` (`caps.go`) returns a **permanent**
   failure whenever the socket is rootless — "rootless Podman cannot run gVisor due to
   cgroup v2 delegation." `RequiredCapabilities` returns `[rootlessCheck, gvisorRunsc]`,
   so on a default Podman Machine this kills enhanced before anything else runs. **This
   assertion is untested in this repo** — it's an inherited assumption, and it is exactly
   the thing this plan must verify rather than enshrine.
2. **Host-`$PATH` runsc check.** `gvisorRunsc = caps.NewGVisorRunsc(runscLookPath)` checks
   the *host* `$PATH`. For a VM-backed Podman Machine the daemon runs in the VM, so the
   host `$PATH` is irrelevant — the same bug fixed for docker in D70, not yet fixed here.
3. **No "runsc registered" check.** Podman has no `daemon.json`; OCI runtimes live in
   `containers.conf` `[engine.runtimes]`. There is no podman analogue of docker's
   `gvisorRegistered` capability, so yoloai can't currently tell whether the VM's podman
   even knows about runsc.
4. **Runtime field plumbing unverified.** yoloai sets `HostConfig.Runtime = "runsc"` via
   the docker-compat socket (inherited from `docker.Runtime.Create`). Whether podman's
   compat API maps that to its `--runtime runsc` has not been confirmed end-to-end.

## Research tracks (do these first — they gate the design)

### R1. Does rootless podman + gVisor actually work, and under what conditions? (central)

This is the load-bearing question for the whole plan. The current code says "never";
we need ground truth, because the answer determines whether the default Podman Machine
experience can run gVisor at all.

Specifics to establish:

- **What cgroup controllers does `runsc` require**, and which are delegated to a rootless
  user on cgroup v2 by default? The usual sticking point is `cpuset` (frequently *not*
  delegated). Map runsc's needs against a default rootless delegation set
  (`systemd` user `Delegate=`), and against an augmented one.
- **`runsc --ignore-cgroups`.** runsc has a flag to skip cgroup configuration entirely.
  This is the most promising lead for rootless: it trades resource-limit enforcement for
  the ability to start without owning a cgroup. Determine: does it unblock rootless? What
  exactly is lost (cpu/memory caps that yoloai sets via `Resources`)? Is that acceptable
  given gVisor already bounds the workload?
- **`--cgroup-manager=cgroupfs` vs `systemd`** in rootless, and the
  `delegate-controllers` / `systemd` user-unit `Delegate=cpu cpuset io memory pids`
  approach — what's the minimal, documentable host setup that makes it work?
- **Security framing.** gVisor is itself a strong sandbox; running it under rootless
  podman is defense-in-depth (no root daemon, user-namespaced). Articulate what rootless
  *adds* on top of gVisor so the tradeoff (e.g. losing resource caps via
  `--ignore-cgroups`) can be judged honestly. This belongs in `design/security.md`.
- **Verdict shape.** The outcome is one of: (a) rootless works with a documented host
  tweak; (b) rootless works only with `--ignore-cgroups` and reduced guarantees; (c)
  rootless genuinely can't and rootful is required. The current code assumes (c) without
  evidence; R1 replaces the assumption with a tested verdict.

Write findings to `docs/contributors/design/research/podman-gvisor-rootless.md`.

### R2. Podman Machine specifics on macOS

- Is `runsc` installable/persistent in the applehv VM, and how (vs the OrbStack/Docker
  Desktop install story)?
- **`/tmp` layout.** Confirm whether Podman Machine has the OrbStack-style
  `/tmp → /private/tmp` virtiofs collision (see `backend-idiosyncrasies.md`). Likely
  *not* (applehv mounts the host home, not `/private/tmp` over `/tmp`), but verify — it
  changes whether the gVisor chroot works out of the box.
- Does `podman machine set --rootful` cleanly switch an existing machine, and what's the
  UX cost (recreate? data loss?) — relevant only if R1 lands on "rootful required."

### R3. Compat-API runtime plumbing

Confirm `HostConfig.Runtime="runsc"` over the podman compat socket actually launches the
container under runsc (not silently ignored / falling back to crun). If it doesn't,
scope a podman-specific create path. Cheap to test once runsc is registered in the VM.

## Implementation work (gated on research)

1. **Make podman `RequiredCapabilities` daemon-location-aware** (mirror docker D70): on
   macOS/Windows (VM-backed) drop the host-`$PATH` `gvisorRunsc` check; on Linux keep it.
2. **Add a podman "runsc registered" capability** — the `gvisorRegistered` analogue:
   verify runsc is configured in the VM's `containers.conf` `[engine.runtimes]` (query via
   `podman info` or read the config), replacing the host-PATH signal off-Linux.
3. **Rework `rootlessCheck` per R1's verdict** — not a blanket permanent block:
   - if rootless works (possibly with `--ignore-cgroups`): allow it, surface any required
     host setup as a *fixable* (non-permanent) capability with guidance, and wire the
     necessary runsc args;
   - if rootful is required: keep a permanent block but with an accurate reason and a
     `podman machine set --rootful` fix step (not just "use docker").
4. **Setup/install path** — document (and, if we automate any of it, implement) installing
   runsc into the Podman Machine VM and registering it in `containers.conf`, including the
   systrap platform args.
5. **Verify runtime plumbing** (R3); add a podman create path only if the compat field is
   ignored.
6. **Tests + docs:** podman caps unit tests (rootless/rootful × Linux/VM matrices),
   `GUIDE.md` podman gVisor setup section, a decision-log entry, and — if the smoke harness
   ever schedules it — an honest `ISOLATION_HOST_NOTE`/matrix treatment (likely keep it
   unscheduled like docker-cenhanced until it's turn-key).

## Sequencing

R1 first (it decides everything downstream), R2/R3 in parallel with it (cheap, once runsc
is in the VM). Then implement 1–2 (safe, mirror docker, independent of the rootless
verdict), then 3 (the rootless rework, which depends on R1), then 4–6.

## Open questions

- If rootless needs `--ignore-cgroups`, do we accept losing the `Resources` cpu/memory
  caps yoloai sets, or refuse `--resources` + rootless + enhanced together?
- Should yoloai ever auto-install runsc into the Podman Machine VM, or only detect +
  instruct? (Leaning detect-and-instruct, matching the docker story.)
- Is podman+gVisor worth shipping before the host-netns network-isolation redesign lands
  (gVisor ignores in-sandbox iptables, so `--network-isolated` + enhanced is already
  rejected regardless of backend)?

## References

- D69, D70 — `docs/contributors/decisions/working-notes.md` (the docker gate + daemon-location fix this mirrors).
- `docs/contributors/backend-idiosyncrasies.md` — OrbStack `/tmp` gVisor chroot collision; gVisor netstack ignores in-sandbox iptables.
- `runtime/podman/` — `podman.go`, `caps.go` (current rootless block + host-PATH check).
- `runtime/docker/docker.go` — `RequiredCapabilities` (the daemon-location pattern to mirror).
- `docs/GUIDE.md` — isolation modes / gVisor setup.
- `docs/contributors/design/security.md` — where the rootless-vs-gVisor security framing belongs.
