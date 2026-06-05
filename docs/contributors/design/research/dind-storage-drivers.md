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

> **Status:** **Complete.** Linux empirics verified 2026-06-05 (host kernel 6.8.0-117, Ubuntu
> 24.04.4, outer driver overlayfs). macOS empirics verified 2026-06-05 across all three providers
> (Docker Desktop, OrbStack, Podman Machine) on Apple Silicon — see
> [Verified macOS results](#verified-macos-results). **O3 holds on every platform**, so the
> recommended strategy (overlay2 on a real-fs `/var/lib/docker`) is confirmed cross-platform.

## The three candidate drivers

| driver | how it stores layers | nests on overlay rootfs? | disk | speed |
|---|---|---|---|---|
| **overlay2** | native kernel overlayfs | **No** — refuses on an overlay backing | low (shared layers) | fast |
| **fuse-overlayfs** | userspace FUSE overlay | Yes | low (shared layers) | fast on Linux / OrbStack / Podman Machine; **exec EINVAL on Docker Desktop only** (its LinuxKit kernel) |
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

## Verified macOS results

All on one Apple Silicon Mac (2026-06-05), `yoloai-base` arm64, switching the active provider
between runs. Each provider tested on **both** backings: the container's default **overlay** rootfs,
and a **named volume** (real fs in the provider's VM — fstype shown). Outer launcher was `docker` for
Docker Desktop/OrbStack and `podman run --privileged` (rootless machine) for Podman Machine.

| provider (VM kernel) | driver | backing | starts? | nested exec? |
|---|---|---|---|---|
| **Docker Desktop** (LinuxKit 6.10.14) | overlay2 | overlay (default) | **NO** — `driver not supported` | — |
| | **overlay2** | **ext4 (volume, O3)** | **YES** | **YES** |
| | fuse-overlayfs | overlay | YES | **NO — EINVAL** |
| | fuse-overlayfs | ext4 (volume) | YES | **NO — EINVAL** |
| | vfs | either | YES | YES |
| **OrbStack** (7.0.5-orbstack) | overlay2 | overlay (default) | **NO** — `driver not supported` | — |
| | **overlay2** | **btrfs (volume, O3)** | **YES** | **YES** |
| | fuse-overlayfs | overlay / btrfs | YES | **YES** |
| | vfs | either | YES | YES |
| **Podman Machine** (Fedora 43, 6.18.10, rootless) | overlay2 | overlay (default) | **NO** — `driver not supported` | — |
| | **overlay2** | **xfs (volume, O3)** | **YES** | **YES** |
| | fuse-overlayfs | overlay / xfs | YES | **YES** |
| | vfs | either | YES | YES |

Takeaways:

- **O3 holds on all three macOS providers.** overlay2 refuses on the overlay rootfs everywhere
  (same `driver not supported: overlay2` as Linux), but on a **named volume** (ext4 / btrfs / xfs —
  all real, non-overlay) it starts and execs cleanly on every provider. Combined with the Linux
  result, **overlay2 + real-fs `/var/lib/docker` is now verified on Linux + all three Mac VMs.**
- **The fuse-overlayfs exec EINVAL is Docker-Desktop-only**, not a general macOS-VM trait. OrbStack
  (kernel 7.0.5) and Podman Machine (Fedora 6.18) both exec fine off fuse-overlayfs; only Docker
  Desktop's **LinuxKit 6.10** kernel returns EINVAL — and it does so **regardless of backing** (fails
  on both overlay and ext4), confirming it's the FUSE-exec path, not the graph store's backing fs.
- **`--storage-driver` flag vs daemon.json pin:** every run cleared `/etc/docker/daemon.json` to
  `{}` first; leaving the fuse pin in place while also passing `--storage-driver` makes dockerd refuse
  to start ("specified both as a flag and in the configuration file"). Implementations must not set
  both.

### Reconciling the smoke `dind/podman-priv` failure

yoloAI's smoke `dind/podman-priv` tier **failed** with the EINVAL signature, yet plain
`podman run --privileged yoloai-base` + nested fuse-overlayfs on the **same** Podman Machine execs
fine here. So Podman Machine's kernel is *not* the cause — the smoke failure comes from **how yoloAI
launches the podman-privileged sandbox** (almost certainly the rootless `--userns=keep-id` mapping
interacting with FUSE exec; see the keep-id/dind entry in `backend-idiosyncrasies.md`), not the
storage driver. Action: when implementing the overlay2+volume strategy, re-test podman-priv dind —
overlay2 (no FUSE) is expected to sidestep this too, but confirm, and if keep-id is the culprit
track it as a separate finding.

### O1 — why fuse-overlayfs exec = EINVAL (root cause, now moot)

Narrowed to **Docker Desktop's LinuxKit kernel (6.10.14) specifically**: the two other Mac VMs
(OrbStack 7.0.5, Podman Fedora 6.18) and native Linux 6.8 all exec binaries off a fuse-overlayfs
mount without error; only LinuxKit returns EINVAL, on both overlay and real-fs backings. The precise
mechanism (LinuxKit FUSE config / a missing `exec` capability on the FUSE mount) was **not** drilled
further because **O3 routes around it** — overlay2 uses no FUSE, so the strategy never depends on
fixing LinuxKit. Marked deferred-moot; revisit only if a future direction reintroduces a FUSE driver
on Docker Desktop.

### Podman id-mapped-volume caveat — checked

The open caveat (rootless Podman keeping a chowned per-userns copy of named-volume content) did **not**
block here: a **fresh, empty** named volume mounted as xfs and overlay2 initialized on it immediately,
no observable id-map copy delay. That's the case that matters for yoloAI (a new volume per sandbox
starts empty). A *populated* volume re-entered under a different mapping could still incur the copy;
not profiled, but not on yoloAI's path.

### Note on the test run

A few nested image pulls (some `vfs` `alpine` cells, a couple `hello-world` cells) timed out on a bad
network / laptop-lid-close, not on any driver fault — `vfs` is confirmed working (its `hello-world`
cells that did complete printed `Hello from Docker!` on every provider). The starts/exec verdicts
above are taken only from cells where the pull completed.

## Recommended strategy

**Give the nested daemon a real-filesystem `/var/lib/docker` and run overlay2; fall back to
fuse-overlayfs / vfs only when the backing is still overlay.** This is the fastest, lowest-disk
config and — now verified — the single config that works unmodified on Linux, Docker Desktop,
OrbStack, and Podman Machine.

Selection at daemon start, by probing the backing fstype (`findmnt -no FSTYPE /var/lib/docker`):

| `/var/lib/docker` backing | provider | driver |
|---|---|---|
| non-overlay (ext4/btrfs/xfs) | any | **overlay2** |
| overlay | Linux / OrbStack / Podman Machine | fuse-overlayfs (exec works on these kernels) |
| overlay | Docker Desktop | **vfs** (the only kernel where fuse-overlayfs exec is broken; + keep the `DindAdvisory` heads-up) |

If yoloAI always provides the real-fs volume, the backing is always non-overlay and overlay2 always
wins on every platform — the fallback rows only matter if the volume can't be created. (Corrected from
the pre-Mac-data version: fuse-overlayfs is fine on Podman Machine; Docker Desktop is the lone
overlay-backed case that needs vfs.)

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

### Open caveat — resolved

A volume at `/var/lib/docker` interacts with rootless Podman's userns id-mapping (Podman keeps a
chowned per-userns copy of mapped content). **Checked on a Podman Machine:** a fresh empty named
volume incurs no observable id-map copy and overlay2 inits on it immediately (details under
[Verified macOS results](#verified-macos-results)). yoloAI uses a fresh volume per sandbox, so this
is not on the hot path. A *populated* volume re-entered under a different mapping could still copy —
unprofiled but irrelevant here.

## macOS test protocol (executed 2026-06-05)

Results are folded into [Verified macOS results](#verified-macos-results) above; the protocol is kept
here for reproducibility. **Key gotcha:** on macOS every provider runs the daemon inside a **Linux
VM**. To give the nested daemon a *real-fs* `/var/lib/docker`, use a **named volume** — it lives on
the VM's real fs (ext4 on Docker Desktop, btrfs on OrbStack, xfs on Podman Machine). Do **not**
bind-mount a macOS host directory (`-v /Users/...:/var/lib/docker`): that path is the VirtioFS /
gRPC-FUSE share and is the *wrong* filesystem for an overlay2 backing. Also note **Podman Machine
shares only `$HOME`** into its VM, not `/tmp` — put the test script under `$HOME` when mounting it
into a podman container.

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

Actual (verified): `fuse-overlayfs` STARTS=YES + `EXEC … invalid argument` (EINVAL) **only on Docker
Desktop**; STARTS=YES + clean exec on OrbStack **and Podman Machine**. `overlay2` STARTS=NO everywhere
(overlay backing). `vfs` works everywhere.

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

### Deliverables — done

1. ✅ Three Mac provider columns filled — see [Verified macOS results](#verified-macos-results).
2. ✅ O3 marked per provider: **GO on all three** (Docker Desktop/ext4, OrbStack/btrfs,
   Podman Machine/xfs). overlay2 + real-fs volume is the confirmed single cross-platform default.
3. ✅ O1 narrowed to Docker Desktop's LinuxKit 6.10 kernel and marked **deferred-moot** (O3 avoids
   FUSE entirely). The precise LinuxKit FUSE mechanism wasn't drilled — not needed for the decision.
4. ✅ Podman id-map caveat checked — no copy on a fresh empty volume; not on yoloAI's path.

Residual follow-up (implementation-time, not research): reconcile yoloAI's smoke `dind/podman-priv`
EINVAL — it's a podman launch-config (userns/keep-id) interaction, not a storage-driver issue, since
plain `podman run --privileged` execs fuse-overlayfs fine on the same machine. See
[Reconciling the smoke dind/podman-priv failure](#reconciling-the-smoke-dindpodman-priv-failure).
