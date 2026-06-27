# microvm backend — Linux/KVM QEMU-microvm isolation (E1)

**Status:** Planned, greenfield (nothing built). Chosen as the first post-merge
workstream (2026-06-27). Source-audited research: [[reference_pve_microvm]]
(https://github.com/rcarmo/pve-microvm), clone at `~/.cache/yoloai-research/pve-microvm`.
Roadmap context: [post-merge-roadmap.md](post-merge-roadmap.md) §E1.

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

## Decisions — SETTLED 2026-06-27 (with the user)

1. **Isolation-mode name** → **new `microvm`** (`--isolation microvm`). Distinct
   prereqs/UX from Kata; no ambiguous auto-selection.
2. **Kernel strategy** → **bundle/download a pinned `vmlinuz` at `yoloai setup`.**
   The spike (below) pins the exact requirement: a **stock Firecracker CI kernel is
   insufficient** — it must be a custom build with `CONFIG_VIRTIO_MMIO_CMDLINE_DEVICES=y`
   **and** `CONFIG_FUSE_FS=y` + `CONFIG_VIRTIO_FS=y` (for the chosen virtiofs workdir).
   The pve `build-kernel.sh` (Firecracker base + pve overlay, 6.12) produces exactly this.
3. **Rootfs toolchain** → **skopeo/umoci as setup-time prereqs** (checked by `IsReady`),
   matching how kata is a containerd setup dep. Revisit the `umoci` Go-lib path if dep
   friction shows up.
4. **Workdir sharing** → **virtiofs now** (not 9p). `LocalityHostSide`, host git works,
   needs `virtiofsd` + a shared-memory guest backend. Forces decision 2's kernel to carry
   `FUSE_FS`/`VIRTIO_FS`.
5. **TAP/bridge ownership** → **yoloAI-managed `yoloai0`**, gated on a
   CAP_NET_ADMIN/setuid-helper answer; ship **phase 1 with no networking isolation**
   (`BackendCaps.NetworkIsolation=false`).

## Spike results (2026-06-27, this Linux box — `~/.cache/yoloai-research/microvm-spike/`)

**STATUS: fully validated — boot + QGA exec + virtiofs all green. Backend de-risked;
ready to write Go.** Harness in the spike dir (`build-rootfs.sh`, `boot.sh`,
`qga-drive.py`, `microvm-init`, `Dockerfile`). Validated end-to-end:
- ✅ **Boot:** pve 6.12 kernel boots via `-M microvm,acpi=off` (PVH entry), mounts the
  ext4 root on `/dev/vda`, runs `/sbin/init`. ~3.6s to init in the spike.
- ✅ **QGA exec (the #1 unknown):** host drove `guest-sync → guest-ping → guest-exec →
  guest-exec-status` over the chardev unix socket with raw JSON; got `exitcode 0` +
  base64 stdout. **Confirmed: a Go `net.Dial`+`encoding/json` client is all that's
  needed — no QGA library.** Guest port enumerated as `/dev/vport1p1`.
- ✅ **virtiofs workdir (decision 4):** `virtiofsd` + `-object memory-backend-memfd,
  share=on -machine memory-backend=mem` + `vhost-user-fs-device,tag=workdir`; guest
  `mount -t virtiofs workdir /mnt/workdir` succeeds; **bidirectional** read/write
  confirmed (host→guest marker read, guest→host file written and visible on host).
  **Caveat:** files mapped to the uid virtiofsd runs as (1000 in the spike) — the Go
  backend must run `virtiofsd` as / map to the sandbox user for correct ownership.

Toolchain confirmed: qemu 8.2.2 (`-M microvm` +
`virtio-blk-device`/`virtio-serial-device`/`virtserialport`/`vhost-user-fs-device`),
`/dev/kvm`, skopeo/umoci, `virtiofsd` at `/usr/libexec/virtiofsd`, e2fsprogs 1.47.

- **Rootfs build works** via the real toolchain: `docker build` → `skopeo copy
  docker-daemon:… oci:…` → `umoci unpack` → `mkfs.ext4 -F -d <rootfs> disk.raw` (no
  loop mount). `qemu-guest-agent` baked into the image via `apk add`. **`umoci unpack`
  needs root (or a userns) for ownership** — rootless gives a uid-mapped tree; the
  spike used `sudo` for a faithful root-owned rootfs. The Go builder must decide
  rootless-userns vs a privileged unpack step.
- **Kernel — the load-bearing finding.** The stock Firecracker `vmlinux` (ELF,
  v1.10/6.1.102) **cannot** drive this backend on two counts, both confirmed by
  extracting its embedded `ikconfig`:
  1. `CONFIG_VIRTIO_MMIO_CMDLINE_DEVICES` is **not set** → with `-M microvm,acpi=off`
     (the qboot/PVH fast path that boots an ELF `vmlinux`), the kernel ignores the
     `virtio_mmio.device=` cmdline entries QEMU injects, so **virtio-blk never binds**
     → `VFS: Cannot open root device "vda"` panic. Switching to `-M microvm,acpi=on`
     doesn't help: ACPI-on routes boot through **SeaBIOS `linuxboot`, which needs a
     bzImage**, but the Firecracker kernel is an uncompressed ELF (PVH-only). The
     winning shape (validated below) is a **bzImage with `CONFIG_PVH=y` +
     `CMDLINE_DEVICES=y`** — it carries a PVH entry note so `-M microvm,acpi=off`
     boots it via the qboot/PVH fast path *and* the cmdline `virtio_mmio.device=`
     entries enumerate virtio-blk. The pve `build-kernel.sh` produces exactly this
     (an 11 MB bzImage, 6.12.22).
  2. `CONFIG_FUSE_FS` is **not set** (and no `VIRTIO_FS`) → **virtiofs is impossible**
     on the stock kernel, which kills the decision-4 workdir choice outright.
  → **Required bundled-kernel config:** `PVH=y`, `VIRTIO_MMIO=y`,
  `VIRTIO_MMIO_CMDLINE_DEVICES=y`, `VIRTIO_BLK=y`, `VIRTIO_CONSOLE=y`, `FUSE_FS=y`,
  `VIRTIO_FS=y`. The pve `pve-microvm-6.12.config` already sets all of these; building
  it is the spike's in-progress step.
- **QEMU boot incantation (PVH fast path):** `-M microvm,acpi=off
  -global virtio-mmio.force-legacy=false` (modern virtio 1.0 — the Firecracker/pve
  kernels are modern-only) `-kernel vmlinuz -append "console=ttyS0 root=/dev/vda rw
  init=/sbin/init reboot=t panic=-1" -drive …,if=none,id=root -device
  virtio-blk-device,drive=root -nodefaults -no-reboot -nographic`.
- **QGA channel:** `-chardev socket,id=qga,path=…,server=on,wait=off -device
  virtio-serial-device -device virtserialport,chardev=qga,name=org.qemu.guest_agent.0`.
  Guest init starts `qemu-ga -m virtio-serial -p /dev/vport*p*`. **Host drives QGA with
  raw JSON over the unix socket** (guest-sync → guest-ping → guest-exec → poll
  guest-exec-status; out/err are base64). This is a plain `net.Dial` + `encoding/json`
  in Go — **no QGA bindings needed**, de-risking the #1 unknown. (End-to-end exec
  validation completes once the pve kernel finishes building.)
- **virtiofs (decision 4):** needs the FUSE/VIRTIO_FS kernel above **plus** a
  shared-memory guest backend on the QEMU side (`-object memory-backend-memfd,share=on`
  + `-machine …,memory-backend=mem`) and a `virtiofsd --socket-path --shared-dir` daemon
  per VM. Validated against the rebuilt kernel in spike Phase B.

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
