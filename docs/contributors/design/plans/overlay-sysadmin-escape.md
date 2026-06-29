# Plan: eliminate the `:overlay` CAP_SYS_ADMIN host escape (audit H2)

ABOUTME: Design for removing the host-escape surface of `:overlay` mode on Docker
ABOUTME: rootful — fuse-overlayfs vs userns vs documented-opt-in, with a recommendation.

Status: **design only, not yet implemented.** Surfaced by the 2026-06-29
escape/exfil security audit (finding H2). The interim for v0.6.0 is to treat
`:overlay` as an explicit, documented dangerous opt-in (see "v0.6.0 interim").

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

### A. fuse-overlayfs (recommended proper fix)

Switch the overlay mount from kernel overlayfs to **fuse-overlayfs**, which runs in
userspace and does **not** require `CAP_SYS_ADMIN` (it needs `/dev/fuse` and
`CAP_SYS_ADMIN` is *not* required for an unprivileged FUSE mount in a user
namespace; in the container it works without the cap given `/dev/fuse` access). The
`fuse-overlayfs` binary is **already installed** in the base image
(`runtime/docker/resources/Dockerfile`) but currently unused for the mount.

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

## Recommendation

**Adopt A (fuse-overlayfs).** It removes both the cap and the AppArmor downgrade,
needs no daemon config, and the binary already ships. Gate the change behind
real-Docker verification of performance + xattr + nested-overlay behavior (the
existing kernel path's known sharp edges). Keep B as an opportunistic add-on only.

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
