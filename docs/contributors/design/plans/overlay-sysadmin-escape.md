# Plan: eliminate the `:overlay` CAP_SYS_ADMIN host escape (audit H2)

ABOUTME: Design for removing the host-escape surface of `:overlay` mode on Docker
ABOUTME: rootful — fuse-overlayfs vs userns vs documented-opt-in, with a recommendation.

Status: **design audited 2026-06-29 — the recommended fix (Option A,
fuse-overlayfs) is EMPIRICALLY REFUTED; see "## Audit" below.** Surfaced by the
2026-06-29 escape/exfil security audit (finding H2). The interim for v0.6.0 —
treat `:overlay` as an explicit, documented dangerous opt-in (see "v0.6.0
interim") — stands, and is now the *recommended* near-term posture, not just a
stopgap.

> ⚠️ **Read the Audit section before acting on this plan.** The original
> recommendation (swap kernel overlayfs → fuse-overlayfs to drop `CAP_SYS_ADMIN`)
> does **not** work in yoloAI's deployment (rootful Docker, no userns-remap):
> fuse-overlayfs there needs the *same* `CAP_SYS_ADMIN`, so it removes nothing.
> Proven on real Docker. The body below is kept for the record with Option A
> struck through.

## The vulnerability

`:overlay` dirs are mounted with Linux **kernel** overlayfs inside the container
(`runtime/docker/resources/entrypoint.py` — `mount -t overlay`). Kernel overlayfs
requires `CAP_SYS_ADMIN`, and Docker's default AppArmor profile blocks `mount(2)`
even with `SYS_ADMIN`, so the launch path grants both:

- `internal/orchestrator/launch/launch.go:843` — adds `SYS_ADMIN` when any dir is `:overlay`.
- `runtime/docker/docker.go:547-550` — sets `apparmor=unconfined` when `SYS_ADMIN` is present (non-privileged).

On **Docker rootful** the container's root maps to **host uid 0** (Docker is not a
`runtime.UsernsProvider` — only Podman is; `resolveUsernsMode`/`create.go:679`
returns `""` for Docker). The base image grants the agent passwordless sudo
(`Dockerfile` — `yoloai ALL=(ALL) NOPASSWD:ALL`), so the agent becomes root with
`CAP_SYS_ADMIN` in the **init** user namespace. That is a classic container escape:

- `mount -t cgroup` a cgroup-v1 controller, write `release_agent` + a `notify_on_release` task → host code execution; or
- remount `/proc/sys` rw and write `/proc/sys/kernel/core_pattern` to `|/path/to/host/handler` → host code execution on the next core dump.

Reachable whenever the user selects `:overlay` (a normal, documented mode). It is
**not** reachable for the default `:copy`/read-only sandbox (no `SYS_ADMIN`).
Podman rootless is unaffected (always in a user namespace; `SYS_ADMIN` is namespaced).

## Options

### A. fuse-overlayfs ~~(recommended proper fix)~~ — REFUTED, see Audit

> **This option does not work in yoloAI's deployment. Kept for the record.** The
> claim below — "fuse-overlayfs … does not require `CAP_SYS_ADMIN`" — is true only
> when the mount happens **inside a user namespace** (rootless Podman, buildah).
> yoloAI's container runs rootful with **no userns-remap** (the init user
> namespace), where the FUSE mount needs real `CAP_SYS_ADMIN` just like kernel
> overlayfs. Empirically confirmed in the Audit section. This is a textbook GEN §14
> error: the design leaned on fuse-overlayfs's *reputation* as "the unprivileged
> overlay" (an incidental property of the rootless contexts it ships in) rather
> than its *contract* in our rootful, init-namespace container.

~~Switch the overlay mount from kernel overlayfs to **fuse-overlayfs**, which runs in
userspace and does **not** require `CAP_SYS_ADMIN` (it needs `/dev/fuse` and
`CAP_SYS_ADMIN` is *not* required for an unprivileged FUSE mount in a user
namespace; in the container it works without the cap given `/dev/fuse` access).~~ The
`fuse-overlayfs` binary is already installed in the base image
(`runtime/docker/resources/Dockerfile`); `fusermount3` is present and setuid-root.

- Removes the `SYS_ADMIN` grant **and** the `apparmor=unconfined` downgrade for overlay → the escape primitives disappear.
- `entrypoint.py` calls `fuse-overlayfs -o lowerdir=…,upperdir=…,workdir=… <merged>` instead of `mount -t overlay`.
- Requires `/dev/fuse` exposed to the container (`--device /dev/fuse`) — a narrow, non-escalating device, not a cap.
- **Risks to verify on real Docker:** (a) performance vs kernel overlayfs; (b) xattr / `trusted.*` behavior (the existing kernel path already special-cases this on Docker Desktop, `entrypoint.py:232`); (c) nested-overlay-on-overlay (the host rootfs is usually overlay2 — `entrypoint.py:242` already notes this); (d) whether `/dev/fuse` is available on all target hosts (Docker Desktop VM, OrbStack, native).
- macOS backends (apple/tart/seatbelt) don't use this path; containerd-vm contains `SYS_ADMIN` behind the guest kernel already.

### B. user-namespace remapping (Docker)

Map container root to a non-host uid so `SYS_ADMIN` is namespaced and
`core_pattern`/`release_agent` become inaccessible. Podman already does this. The
problem: Docker per-container userns remap requires the **daemon** to be configured
with `--userns-remap` (host-wide, `/etc/docker/daemon.json`) — yoloAI cannot enable
it unilaterally per container. yoloAI could *detect* a userns-remapped daemon and
prefer it, but cannot guarantee it. Rejected as the primary fix (not self-contained);
viable only as an opportunistic hardening if the daemon already has it.

### C. fine-grained AppArmor profile

Keep `SYS_ADMIN` but replace `apparmor=unconfined` with a custom profile that
`allow mount fstype=overlay` and denies other mounts / `/proc/sys` remounts.
AppArmor mount rules *can* filter by fstype (seccomp cannot — it can't deref the
fstype string). Rejected as primary: shipping + loading a custom profile via
`apparmor_parser` is host/distro-fragile (AppArmor may be absent or be SELinux),
and it still leaves `SYS_ADMIN` in the init namespace (other vectors).

## Audit (2026-06-29) — Option A empirically refuted

Tested on real rootful Docker (this dev host, no userns-remap — the exact H2
deployment), `yoloai-base:latest`, default cap set:

| Config | Result |
|---|---|
| `fuse-overlayfs`, `--device /dev/fuse`, **no `SYS_ADMIN`**, default seccomp | ❌ `fusermount3: mount failed: Operation not permitted` |
| same, **no `SYS_ADMIN`**, `seccomp=unconfined` | ❌ `fuse: mount failed: Permission denied` |
| **no `SYS_ADMIN`**, self-create a userns first (`unshare -U -m -r`) | ❌ `unshare … Operation not permitted` (default seccomp blocks `unshare(CLONE_NEWUSER)`; with seccomp off, the mount-ns step still EPERMs) |
| `fuse-overlayfs`, **`--cap-add SYS_ADMIN` + `apparmor=unconfined`** | ✅ mounts, writes land in upperdir, handles overlay-on-overlay |

**Conclusions.**

1. **A removes nothing.** fuse-overlayfs in a rootful, non-userns container needs
   the *same* `CAP_SYS_ADMIN` kernel overlayfs needs. The setuid `fusermount3`
   cannot regain the cap because Docker drops it from the **bounding set**
   (`CapBnd` has no `sys_admin`), so the `mount(2)` is EPERM. The headline benefit
   ("removes the `SYS_ADMIN` grant and the AppArmor downgrade → the escape
   primitives disappear") is false.
2. **It is the capability, not seccomp.** Disabling seccomp does not help, so a
   custom seccomp profile cannot rescue A either.
3. **The container cannot self-host a userns.** Default Docker seccomp blocks
   `unshare(CLONE_NEWUSER)` without `CAP_SYS_ADMIN`, and even with seccomp off the
   private-mount-propagation step fails — so "run fuse-overlayfs inside a userns
   the container makes itself" is also closed on rootful Docker.
4. **Scope gap regardless of A.** The entrypoint's VirtioFS fallback uses
   `mount -t tmpfs`, `mount --make-shared /`, and a nested kernel overlay
   (`entrypoint.py` `apply_overlays`) — all `CAP_SYS_ADMIN` operations A never
   touches. And "macOS backends don't use this path" is imprecise: **Docker
   Desktop on macOS is the docker backend** and hits exactly this fallback.

The only configuration in which an overlay mount gets a *namespaced* (safe)
`CAP_SYS_ADMIN` is when the **whole container** is in a user namespace — i.e.
rootless Podman (already safe) or a **userns-remapped Docker daemon** (Option B).
There is no self-contained per-container way to get there on Docker.

## Recommendation (revised after the audit)

**Do not pursue A.** The honest options that actually close the hole are narrower
than first thought:

1. **Gate `:overlay` by daemon posture (reframed B).** Permit `:overlay` only when
   the runtime puts the container in a user namespace — Podman rootless, or a
   Docker daemon configured with `--userns-remap`. **Refuse `:overlay` on rootful,
   non-userns-remapped Docker** with a clear message (use `:copy`, or remap the
   daemon, or use Podman). This is the only posture that makes the cap namespaced
   and the escape primitives inert. yoloAI *can* detect userns-remap
   (`docker info` `SecurityOptions` lists `name=userns`), so the gate is
   implementable and self-contained even though enabling remap is the operator's
   job. Whether fuse-overlayfs *or* kernel overlayfs is used inside that userns is
   then a functionality choice, not a security one (fuse-overlayfs is the better
   pick — it handles overlay-on-overlay, which kernel overlayfs choked on in
   TEST 7).
2. **Reconsider whether `:overlay` needs an in-container kernel mount at all.**
   `:copy` already gets diff/apply with no privileged mount. If `:overlay`'s only
   real win is "instant setup, no copy," a host-side or fuse-without-mount approach
   (e.g. a userspace diff over the upper dir) may deliver the UX without any
   `CAP_SYS_ADMIN`. Bigger rethink; out of scope here but the strategically right
   question.
3. **Keep the v0.6.0 interim as the standing posture** (below) until 1 or 2 lands:
   `:overlay` is a documented dangerous opt-in, like `container-privileged`.

Option C (custom AppArmor mount-fstype filter) is also weaker than first written:
it still leaves real `CAP_SYS_ADMIN` in the init namespace (other vectors), and the
audit shows the blocker is the capability itself, not the LSM.

## v0.6.0 interim (chosen 2026-06-29)

Until A lands, treat `:overlay` like `container-privileged`: an **explicit,
documented dangerous opt-in**. Emit a loud warning at use that `:overlay` grants
`CAP_SYS_ADMIN` (a host-escape surface on Docker rootful) and is safe only for
trusted workloads, and document the tradeoff in the user docs next to the
`:overlay` description. The escape requires the user to actively choose `:overlay`,
so this is a defensible ship for the cut while A is built as a focused follow-up.

## Pointers

- `runtime/docker/resources/entrypoint.py` (overlay mount), `runtime/docker/docker.go:547`
  (apparmor), `internal/orchestrator/launch/launch.go:843` (SYS_ADMIN grant),
  `internal/orchestrator/create/create.go:679` (`resolveUsernsMode`),
  `runtime/podman/podman.go:316` (the Podman userns reference).
- Audit finding H2 + the mount audit F1 (both 2026-06-29).
