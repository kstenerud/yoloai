# Docker-in-Docker Storage Drivers

Making nested `dockerd` (dind) "just work" under `--isolation container-privileged` across the
three container providers yoloAI targets: native **Linux**, **Docker Desktop** (macOS VM),
**OrbStack** (macOS VM), and **Podman Machine** (macOS VM). The question is which storage driver the
*nested* daemon should use, and how to select it portably instead of hardcoding one.

Today yoloAI pins `fuse-overlayfs` in two places — `internal/runtime/docker/resources/Dockerfile`
(`/etc/docker/daemon.json`) and `internal/runtime/monitor/sandbox-setup.py`
(`start_dockerd()` passes `--storage-driver=fuse-overlayfs`). That pin works on Linux and OrbStack
but **breaks on Docker Desktop and Podman Machine**, where every nested `execve` off the
fuse-overlayfs mount returns `EINVAL`.

> **Status:** Linux empirics are **verified** (2026-06-05, host kernel 6.8.0-117, Ubuntu 24.04.4,
> outer driver overlayfs). The macOS columns and the O1 EINVAL root-cause are **hypotheses pending a
> Mac host** — see [Completing the research on macOS](#completing-the-research-on-macos).

## The three candidate drivers

| driver | how it stores layers | nests on overlay rootfs? | disk | speed |
|---|---|---|---|---|
| **overlay2** | native kernel overlayfs | **No** — refuses on an overlay backing | low (shared layers) | fast |
| **fuse-overlayfs** | userspace FUSE overlay | Yes | low (shared layers) | fast on Linux/OrbStack; **exec EINVAL on Docker Desktop / Podman Machine** |
| **vfs** | full per-layer directory copy | Yes (always) | **high** (no sharing) | slow, scales badly with layer depth |

## Verified Linux results

All runs inside a `--privileged yoloai-base` container. Two `/var/lib/docker` backings tested: the
container's default **overlay** rootfs, and a **real ext4** filesystem bind-mounted in.

| driver | `/var/lib/docker` backing | starts? | nested exec? | disk (alpine + python:3.12-slim) | `docker load` 44M tar |
|---|---|---|---|---|---|
| overlay2 | overlay (default) | **NO** — `driver not supported: overlay2` | — | — | — |
| **overlay2** | **ext4 (O3)** | **YES** | **YES** | **137M** | 1.58s |
| fuse-overlayfs | overlay (default) | YES | YES | 137M | 1.54s |
| fuse-overlayfs | ext4 | YES | YES | 137M | — |
| vfs | overlay / ext4 | YES | YES | **427M** (3.1×) | 1.88s |

Takeaways:
- **overlay2 refuses to start on an overlay backing.** The failure is `driver not supported:
  overlay2` — it is *not* a privilege or userns problem. overlay cannot stack on overlay.
- **overlay2 on a real-fs backing nests perfectly** (the O3 result) — same 137M disk and speed as
  fuse-overlayfs, with none of the FUSE-exec fragility.
- **vfs is the correctness-over-speed fallback.** 3.1× disk on a *shallow* two-image set; the gap
  widens sharply with real dev images because vfs full-copies every shared layer instead of
  referencing it. Unpack time is only ~20% worse on a single small image, but that understates the
  real-world cost (no layer reuse across pulls/builds).

### Root cause: why yoloAI pins fuse-overlayfs

The Dockerfile comment says overlay2 "requires an unprivileged user namespace mount that is blocked
by the outer Docker layer (EPERM)". The O3 result shows the real reason is narrower: **overlay2
can't initialize because the default `/var/lib/docker` sits on the container's overlay rootfs.** Give
the nested daemon a non-overlay backing and overlay2 works under the same privileges. The fuse pin is
papering over a *backing-filesystem* problem, not an overlay2 capability gap.

### O3 — the important finding

**On Linux, overlay2 nests cleanly when `/var/lib/docker` is a real (ext4) filesystem.** This is the
ideal answer: fast, low-disk, and — because overlay2 uses no FUSE — it should also sidestep the
Docker Desktop / Podman Machine `execve` EINVAL, which is specifically a FUSE-exec limitation. That
last clause is the untested-on-Mac hypothesis.

## Recommended strategy

**Give the nested daemon a real-filesystem `/var/lib/docker` and run overlay2; fall back to
fuse-overlayfs / vfs only when the backing is still overlay.** This is the fastest, lowest-disk
config and the single most likely one to also un-break the Mac VMs.

Selection at daemon start, by probing the backing fstype (`findmnt -no FSTYPE /var/lib/docker`):

| `/var/lib/docker` backing | provider | driver |
|---|---|---|
| non-overlay (ext4/xfs) | any | **overlay2** |
| overlay | Linux / OrbStack | fuse-overlayfs |
| overlay | Docker Desktop / Podman Machine | **vfs** (+ keep the `DindAdvisory` heads-up) |

If yoloAI always provides the real-fs volume, the backing is always non-overlay and overlay2 always
wins — the provider matrix collapses to a fallback that only matters if the volume can't be created.

### Code-placement sketch

Runtime selection over a baked-image constant:

- `internal/runtime/docker/resources/Dockerfile` (~L156-160) — drop the static
  `{"storage-driver":"fuse-overlayfs"}` `daemon.json` pin.
- yoloAI container-config for privileged sandboxes — mount a named/anonymous **volume** (real-fs in
  the VM) at `/var/lib/docker` so the nested graph isn't on the overlay rootfs.
- `internal/runtime/monitor/sandbox-setup.py` `start_dockerd()` (~L1062-1088) — replace
  `--storage-driver=fuse-overlayfs` with the fstype-probe selection above.
- `internal/runtime/docker/caps.go` `dindAdvisory` — narrow to the residual overlay-backed Mac-VM
  fallback. The current Linux comment ("native overlay2 nests; dind works") is **misleading**:
  overlay2 does *not* nest on the default overlay rootfs, only on a real-fs backing.

### Open caveat (verify before implementing)

A volume at `/var/lib/docker` interacts with rootless Podman's userns id-mapping. Rootless Podman
keeps a separate chowned copy of mapped content per userns mapping (see
`backend-idiosyncrasies.md`), so a privileged-podman volume may get id-map-copied too, costing disk
and time. Check this on a Podman Machine before committing to the volume approach there.

## Completing the research on macOS

The Linux half is done. A teammate (or agent) on a Mac must fill the three VM columns and resolve
O1. **Key gotcha:** on macOS every provider runs the daemon inside a **Linux VM**. To give the
nested daemon a *real-fs* `/var/lib/docker`, use a **Docker named volume** — it lives on the VM's
real ext4. Do **not** bind-mount a macOS host directory (`-v /Users/...:/var/lib/docker`): that path
is the VirtioFS / gRPC-FUSE share and is the *wrong* filesystem for an overlay2 backing.

### Setup (per provider)

Run each block on the same Mac, switching the active provider between runs (Docker Desktop context,
`orb` / OrbStack context, and `podman machine` with `DOCKER_HOST` pointed at its socket). Confirm
which provider is active first:

```sh
docker info --format '{{.OperatingSystem}}'   # "Docker Desktop" | "OrbStack" | a Linux distro
# Podman Machine reports its guest distro, not "Podman Machine" — detect via `podman` binary.
uname -a                                       # run INSIDE the container below = the VM kernel
```

Build/refresh `yoloai-base` for the Mac's architecture before testing.

### Test 1 — driver matrix on the default (overlay) backing

Reproduces the EINVAL and confirms the OrbStack-vs-Desktop split. Script (`drv.sh`):

```bash
#!/usr/bin/env bash
set +e
echo "### fuse-overlayfs version:"; fuse-overlayfs --version 2>&1 | head -1
echo "### /var/lib/docker backing fstype:"; findmnt -no FSTYPE /var/lib/docker 2>/dev/null || stat -f -c '%T' /var/lib/docker
test_driver() {
  drv="$1"; echo "=================== DRIVER: $drv ==================="
  pkill dockerd 2>/dev/null; sleep 1; rm -rf /var/lib/docker/* 2>/dev/null
  echo '{}' | sudo tee /etc/docker/daemon.json >/dev/null
  sudo dockerd --storage-driver="$drv" >/tmp/d-$drv.log 2>&1 &
  for i in $(seq 1 25); do sudo docker info >/dev/null 2>&1 && break; sleep 1; done
  if ! sudo docker info >/dev/null 2>&1; then
    echo "[$drv] STARTS=NO"; grep -i "graphdriver\|not supported\|error" /tmp/d-$drv.log | tail -3; return; fi
  echo "[$drv] STARTS=YES active=$(sudo docker info --format '{{.Driver}}')"
  echo "[$drv] EXEC alpine: $(sudo docker run --rm alpine echo HELLO 2>&1 | tail -1)"
  echo "[$drv] EXEC hello-world: $(sudo docker run --rm hello-world 2>&1 | grep -i 'Hello from Docker\|invalid argument\|error' | head -1)"
  pkill dockerd 2>/dev/null; sleep 2
}
test_driver overlay2; test_driver fuse-overlayfs; test_driver vfs
```

```sh
docker run --rm --privileged --entrypoint bash -v /path/to/drv.sh:/drv.sh:ro yoloai-base /drv.sh
```

Expected: `fuse-overlayfs` STARTS=YES but `EXEC … invalid argument` (EINVAL) on Docker Desktop and
Podman Machine; STARTS=YES + clean exec on OrbStack. `overlay2` STARTS=NO everywhere (overlay
backing). `vfs` works everywhere.

### Test 2 — O3 on a real-fs VM volume (the decisive test)

Does overlay2 nest on the Mac VM kernels when `/var/lib/docker` is real ext4? Use a **named volume**,
not a host bind:

```sh
docker volume create dindvld
docker run --rm --privileged --entrypoint bash \
  --mount source=dindvld,target=/var/lib/docker \
  -v /path/to/drv.sh:/drv.sh:ro yoloai-base /drv.sh
docker volume rm dindvld
```

The first line of output prints the backing fstype — confirm it is **not** `overlay` (should be
ext4/xfs). Then the decisive cell: does `[overlay2] STARTS=YES` and `EXEC … Hello from Docker!`? If
yes on all three providers, the recommended strategy is confirmed cross-platform and the provider
matrix collapses. If overlay2 still fails on a Mac VM, capture the dockerd log (`/tmp/d-overlay2.log`
inside the container) and fall back stays per-provider.

For Podman Machine, run the same with `podman` and note whether the named volume triggers an
id-mapped copy (watch disk and startup time) — this is the open caveat above.

### Test 3 — O1 root cause (why fuse-overlayfs exec = EINVAL)

Only needed for the writeup, not for the decision (O3 routes around it). On a Docker Desktop / Podman
Machine VM, at the moment of the EINVAL:

- Capture the **exact mount options** of the nested fuse-overlayfs mount: `findmnt /var/lib/docker`
  and `mount | grep fuse` inside the nested daemon's view.
- Capture the **VM kernel version** (`uname -r` inside the container) and compare against OrbStack's
  (the working case) and native Linux 6.8.
- Capture `dmesg` / dockerd debug log around the failed `execve`.
- Hypothesis to confirm or refute: the VM kernel's FUSE does not permit executing binaries off the
  FUSE mount (no `FUSE_ALLOW_EXEC` / mount lacks `exec`, or a binfmt/`MNT_NOEXEC` interaction),
  whereas OrbStack's and native Linux's kernels do.

### Deliverables to fold back here

1. Fill the three Mac provider columns into the results matrix (starts / nested exec / disk per
   driver on both the default and named-volume backings).
2. Mark the O3 cell for each Mac provider — this is the go/no-go for "overlay2 + real-fs volume" as
   the single cross-platform default.
3. Record the O1 EINVAL root cause (or mark it deferred if the strategy makes it moot).
4. Confirm or refute the Podman-Machine id-mapped-volume-copy caveat.
