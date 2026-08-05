> **ARCHIVED — not maintained, not swept, not a live reference.** Everything below was
> true when written and has not been checked since; the code it describes has moved. It is
> **not a specification** — do not build from it or cite it as the current answer. Good for
> archaeology only: see [`../README.md`](../README.md).

# microvm backend — Linux/KVM QEMU-microvm isolation (E1) — RETIRED

> **RETIRED 2026-06-28 — see [D104](../../../decisions/working-notes.md#d104--retire-the-hand-rolled-qemu--m-microvm-backend-libkrun-is-the-tech-if-a-light-vm-tier-is-ever-added-e1).**
> The QEMU `-M microvm` approach is abandoned: it cannot boot a stock distro
> kernel (the `6.12.94` bump broke bzImage load + modular-virtio-mmio enumeration;
> only a *custom-built* kernel works — the maintenance burden this plan tried to
> avoid), and a lighter microVM adds no isolation over the existing Kata `vm`
> backend and no boot benefit for long interactive sessions. If a light VM tier is
> ever added it will be **libkrun** (bundled Red-Hat kernel, virtio-fs, OCI-native,
> macOS HVF), not QEMU-microvm — but not now (see D104 for triggers). The
> implementation spike was on the unmerged `microvm-backend` branch (`73cfe338`);
> that branch was **deleted 2026-08-05** and the spike no longer exists anywhere.
> D104's six sourced facts are what it established, and a revival would start from
> libkrun rather than this code. The text below is the original plan, kept for history.

**Status:** RETIRED (was: Planned, greenfield). Original research:
[[reference_pve_microvm]] (https://github.com/rcarmo/pve-microvm).

## What & why

A new `runtime/microvm/` backend that boots OCI-profile images as lightweight
QEMU `-M microvm` VMs **directly** — no containerd, no Kata shim, no CNI/nerdctl.
The profile image is converted to a bootable ext4 rootfs on the host at Setup
(`skopeo` + `umoci` + `mkfs.ext4 -d`, the `pve-oci-import` recipe), then launched
with `qemu-system-x86_64 -M microvm -kernel … -drive …`. Dependencies: only QEMU +
`/dev/kvm` + skopeo/umoci (setup-time) + virtiofsd. Avoids the
[[project_kata_nerdctl]] networking gotcha; the ext4 is Firecracker-compatible.

Provides a `vm`-class isolation on **Linux + KVM only** (`//go:build linux` — the
`crosscheck` darwin target excludes it automatically, like `runtime/containerd`).

## Decisions to settle first (with recommendations)

1. **Isolation-mode name** — reuse `--isolation vm` (SelectBackend prefers microvm
   when installed) vs a new `--isolation microvm` / `vm-microvm`. **Rec: new name**
   (`microvm`) — distinct prereqs/UX from Kata; avoids ambiguous auto-selection.
2. **Kernel strategy** (widest downstream impact) — (a) ship/download a pinned
   `vmlinuz` at `yoloai setup`, (b) use host `/boot/vmlinuz-*`, (c) build in the
   profile Dockerfile. **Rec: (a)** (the pve-microvm approach) — predictable virtio
   support, ~10 MB setup cost.
3. **Rootfs toolchain** — skopeo/umoci as setup-time prereqs (checked by `IsReady`)
   vs the `github.com/opencontainers/umoci` Go lib + a Go OCI pull. **Rec: start
   with setup-time CLI prereqs** (matches how kata is a containerd setup dep); revisit
   the Go-lib path if dep friction shows up.
4. **Workdir sharing** — virtiofs (`LocalityHostSide`, host git works, needs
   `virtiofsd`) vs 9p (QEMU built-in, no daemon, slower) vs copy-into-disk
   (`LocalitySandboxSide` + `GitExecer`, like Tart). **Rec: 9p for the MVP**
   (zero extra dep, simplest), **virtiofs as the follow-up** for performance + clean
   host-git locality.
5. **TAP/bridge ownership** — yoloAI creates/owns a `yoloai0` bridge vs user-configured.
   **Rec: yoloAI-managed**, gated on a CAP_NET_ADMIN/ setuid-helper answer; ship
   **phase 1 with no networking isolation** (`BackendCaps.NetworkIsolation=false`).

## Build sub-steps (sizes from the scope)

- **(a) Rootfs builder — `Setup()`/`IsReady()` (M):** `skopeo copy docker://<profile> oci:…`
  → `umoci unpack` → inject a **neutral keepalive init** (NOT pve's login-respawn —
  a minimal reaper for the D88 model) → `mkfs.ext4 -F -d <rootfs> disk.raw` (no
  loop-mount, rootless-safe) → optional `qemu-img convert` to qcow2. Greenfield Go
  wrapping the CLI tools.
- **(b) QEMU lifecycle — Create/Start/Stop/Remove/Inspect/Prune (L):** per-instance
  config in `~/.yoloai/sandboxes/<name>/`; `Start` execs the daemonized
  `qemu-system-x86_64 -M microvm -kernel … -append "console=ttyS0 root=/dev/vda rw …"
  -nodefaults -nographic -drive …,if=none,id=d0 -device virtio-blk-device …
  -device virtio-net-device,netdev=n0 -netdev tap,…`, PID to instance dir; Stop =
  SIGTERM→SIGKILL escalation (reuse the `runtime/docker` pattern); Inspect = PID-alive
  check. TAP add/cleanup in Start/Remove. **Biggest chunk.**
- **(c) Exec / session (M):** **guest-agent (QGA)** over virtio-serial UNIX socket
  for non-interactive `Exec` + the `ProcessLauncher.Launch` headless path (JSON
  `{out-data,err-data,exitcode}` → `runtime.ExecResult`); **serial console** on a
  UNIX socket (`-serial unix:…,server,nowait`) for `InteractiveExec`/attach, bridged
  via `runtime.IOStreams`. Backend declares `KeepAliveGuestOSInit` (guest init is the
  reaper, like containerd/tart) + `AgentFreeLaunch=true`. `AttachCommand` returns a
  `socat`/`minicom` to the serial socket (Tart-`ssh`-analog).
- **(d) Networking (M):** TAP-per-VM on a Linux bridge; host-side iptables/nftables on
  the TAP for allowlist (the D90 egress-proxy topology — TAP-per-VM is *cleaner* than
  shared-netns). Ship with `NetworkIsolation=false`; flip true when D (egress proxy)
  lands.
- **(e)+(f) Interface surface + registration (S):** implement the ~13 core
  `runtime.Backend` methods + optionals `ProcessLauncher`, `InteractiveSession`,
  `IsolationCapabilityProvider` (declare `/dev/kvm`, qemu, skopeo/umoci, virtiofsd);
  `GitExecer` only if copy-into-disk (avoid via 9p/virtiofs). `init()` →
  `runtime.Register` with `BackendType="microvm"`, `KeepAliveModel=KeepAliveGuestOSInit`,
  `FilesystemLocality=LocalityHostSide` (9p/virtiofs) or SandboxSide (copy),
  `IsolationTargetOnly=true`, `Platforms=["linux"]`, `AgentFreeLaunch=true`. New arm in
  `runtime/probe.go SelectBackend`. Pattern fully established by containerd/tart/docker.

## Spikes (do these first — they de-risk the unknowns)

1. **Boot-recipe spike (highest value):** on this Linux box (check `/dev/kvm` first),
   manually run the pve-microvm recipe end-to-end with a small test image — build an
   ext4 rootfs from an OCI image, boot `-M microvm` with a kernel, confirm it boots to
   the init, and that **QGA exec works** (the #1 unknown — the QGA Go-protocol has no
   Go bindings; validate the wire protocol or the `qemu-guest-agent` CLI path before
   committing to it). Fallback if QGA is painful: serial-console exec or a
   virtiofs-dropped-script. This spike answers decisions 2 & 4 empirically.
2. **`mkfs.ext4 -d` availability** (`e2fsprogs` ≥ 1.43) + skopeo/umoci presence on the
   target.

## Reuse / pattern pointers
`runtime/containerd/` (VM lifecycle + KeepAliveGuestOSInit + the network shape),
`runtime/tart/` (in-guest session + serial/ssh attach + LocalitySandboxSide+GitExecer),
`runtime/docker/` (stop SIGTERM→SIGKILL escalation), `runtime/caps/` (host capability
declarations), `runtime.InstanceConfig/MountSpec/ExecResult/ProcSpec` (unchanged).

## First implementation step
Settle decisions 1–5 (above) with the user, then run **spike #1** (the boot-recipe
+ QGA validation) on the Linux box. The spike result shapes the rootfs-builder and
exec paths before any Go backend code is written. Then build (a)→(b)→(c)→(e/f),
with (d) networking deferred behind the egress proxy.
