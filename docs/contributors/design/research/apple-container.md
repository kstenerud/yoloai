# Apple `container` as a yoloAI Backend

Research date: 2026-06-10
Tool version probed: `container` CLI 1.0.0 (build release, commit ee848e3), macOS 26, Apple Silicon.

Hands-on spike against a locally installed `container`. Companion to
[linux-vm-backends.md](linux-vm-backends.md) §8 (which flagged Apple
Containerization as "evaluate when macOS 26 reaches adoption"); this doc
is the verified deep-dive that resolves whether it fits as a backend.

## What it is

Apple's `container` (source: `github.com/apple/container`, Swift; built on
the `apple/containerization` framework) runs **each OCI container in its own
lightweight Virtualization.framework VM**, with its own Linux guest kernel
(Kata's `kata-static` kernel, 6.18.x, pulled on first `system start`). It is
OCI-native (`container image pull alpine:3.22` works) but exposes **no
Docker-compatible API socket** — the CLI is bespoke and talks to a per-user
`container-apiserver` launchd agent over an **XPC mach service**.

## The filesystem-exposure scare: `machine` ≠ `container`

The initial concern ("it exposes the entire host filesystem") came from
`container machine run`. That is a *different* subcommand from `container run`:

- **`container machine …`** boots a *persistent* Linux VM and, by default,
  virtiofs-mounts the **entire home directory read-write at the same path**.
  The default is `MachineConfig.defaultHomeMount = .rw`; the `home-mount`
  option (`none`/`ro`/`rw`) exists **only** under `Sources/ContainerCommands/Machine/`.
  It is a cross-development convenience ("switch between macOS and Linux on my
  own project"), not an isolation primitive.
- **`container run`** (regular containers) shares **only** what you pass via
  `-v` / `--mount`. No automatic home mount. This is the path a yoloAI backend
  uses, and its isolation model is exactly what we want.

So the exposure is opt-in to the `machine` workflow and irrelevant to the
backend. yoloAI would never invoke `container machine`.

## Spike results (verified)

All four open questions resolved positively against `container` 1.0.0.

| # | Question | Result |
|---|----------|--------|
| Q1 | `:rw` live bind mount | **Yes.** `-v host:guest` / `--mount type=virtiofs,source=,target=` is a true live virtiofs bind mount, default rw. Host→guest and guest→host writes propagate **live** (verified both directions on a running container). |
| Q1b | `:ro` | **Yes.** `--mount type=virtiofs,source=,target=,readonly` mounts read-only; guest writes fail with `Read-only file system`. |
| Q2 | `:overlay` | **Yes, same cap requirement as Docker.** `overlay` is in the guest kernel (`/proc/filesystems`). Mount fails under default caps (no `CAP_SYS_ADMIN`; guest `CapEff=0x…a80425fb`), succeeds with `--cap-add CAP_SYS_ADMIN`; upper layer captures writes correctly. |
| Q3 | Network allowlist via in-guest iptables | **Yes, cleanly.** With `--cap-add CAP_NET_ADMIN` (+`CAP_NET_RAW`), `iptables -A OUTPUT -d <ip> -j DROP` is enforced inside the guest (real per-VM kernel). None of gVisor's userspace-netstack problem (cf. `IsolationEnforcesInSandboxIptables`). |
| Q4 | CLI/JSON surface | **Clean.** `list --format json\|yaml\|toml`, `inspect` emits pretty JSON (mounts as `type/source/destination`, per-container `status.networks[].ipv4Address`). `exec` propagates exit codes (verified `exit 42` → 42). Missing-container `inspect` → exit 1. |

### Flag surface maps directly onto `InstanceConfig`

`container run` exposes: `--mount type=,source=,target=,readonly`, `-v`,
`--read-only` (rootfs), `--tmpfs`, `--cap-add`/`--cap-drop` (incl. `ALL`),
`--user name|uid[:gid]`, `-w/--workdir`, `-m/--memory`, `--cidfile`. `exec`
supports `-i/--interactive`, `-t/--tty`, `-u/--user`, `-w/--workdir` →
satisfies `InteractiveExec` (TTY + workDir + user).

### Simpler than Tart: no path remapping

Mounts land at the **literal target** (`-v /tmp/x:/mnt/work` → `/mnt/work` in
guest). No host↔guest path translation — unlike Tart's `/home/yoloai`→
`/Users/admin` remap. Each container also gets its own IP/gateway/MAC
(`192.168.64.x/24` in the spike), i.e. real per-VM networking.

## Environment variables it reads

Found by grepping `ProcessInfo.processInfo.environment[…]` / `getenv` across
`Sources/`. This is the input for a curated-env (`HostEnv`) keyset — see the
backend plan for how to bucket these.

| Var | Read at | Purpose / default |
|-----|---------|-------------------|
| `CONTAINER_APP_ROOT` | `ContainerPlugin/ApplicationRoot.swift` | State/data root. Default `~/Library/Application Support/com.apple.container`. **Resolved from `HOME`** when unset. |
| `CONTAINER_INSTALL_ROOT` | `ContainerPlugin/InstallRoot.swift` | Install root for binaries/plugins. Default `/usr/local`. |
| `CONTAINER_LOG_ROOT` | `ContainerPlugin/LogRoot.swift` | Log directory. |
| `CONTAINER_DEFAULT_PLATFORM` | `ContainerAPIService/Client/DefaultPlatform.swift` | Default `os/arch` for image ops. Precedence: `--platform` > `--os/--arch` > this var. |
| `CONTAINER_REGISTRY_HOST` / `CONTAINER_REGISTRY_USER` / `CONTAINER_REGISTRY_TOKEN` | `ContainerImagesService/Server/ImagesService.swift` | Registry auth for pulls (private images). |
| `CONTAINER_DEBUG` | `Application.swift`, `MachineAPIServer+Start.swift` | Debug logging toggle. |
| `CONTAINER_DEBUG_LAUNCHD_LABEL` | `ContainerPlugin/LaunchPlist.swift` | Debug launchd target (dev only). |
| `SSH_AUTH_SOCK` | `ContainerRun.swift`, `ContainerStart.swift`, `MachineHelpers.swift`, `BuilderStart.swift` | Forwarded into the container/machine for an SSH agent. **We likely do NOT want to forward the host agent into a sandbox.** |
| `BUILDKIT_COLORS`, `NO_COLOR` | `BuilderStart.swift` | Build-output coloring. |
| (arbitrary) | `ContainerAPIService/Client/Parser.swift` | `${VAR}` interpolation in config/value substitution — user-controlled, not a fixed passthrough. |

### The key curation insight: no socket env var

The CLI connects to its daemon via an **XPC mach service**
(`xpc_connection_create_mach_service` in `Sources/ContainerXPC/`), a per-user
launchd agent — **not** a socket path from the environment. There is **no
`CONTAINER_HOST`/`DOCKER_HOST` analogue.** Consequences:

- `BackendDescriptor.Probe(ctx, env)`'s `env` map is **not needed for daemon
  discovery** here (Docker/Podman use it for `DOCKER_HOST`/`CONTAINER_HOST`).
  Probe = `LookPath("container")` + `container system status` reachable.
- The apiserver is **shared per macOS user** across all principals. Per-principal
  state isolation, if ever needed, is via `CONTAINER_APP_ROOT` (and the daemon
  would have to honor a per-call root, which the XPC-server model may not — open
  question for principal-isolation work, not for the single-principal backend).
- Minimum always-pass set for the CLI to function: `PATH` (locate binary +
  plugins under the install root) and `HOME` (default `CONTAINER_APP_ROOT`
  resolution).

## Capability conclusion

Model the backend's `BackendCaps` on **Docker at full parity**, not the reduced
Tart profile:

- `OverlayDirs: true` (with `--cap-add CAP_SYS_ADMIN`, same as Docker)
- `:rw` / `:ro` live mounts supported (virtiofs)
- `NetworkIsolation: true` (in-guest iptables works)
- `CapAdd: true`
- `HostFilesystem: false`
- Base isolation mode `IsolationModeVM` (per-container VM)

…plus **stronger isolation than Docker** (per-container VM, own kernel) and a
**simpler mount model than Tart** (no path remap), reusing the **existing
Dockerfile/profile OCI images unchanged**.

## Costs / risks (none are blockers)

- **v1, days old.** Bespoke CLI with no stable API contract; a shell-out
  backend parses output that may churn release-to-release. Mitigated by stable
  `--format json` + sane exit codes, but pin behavior to the probed version and
  re-verify on upgrades.
- **macOS 26 Tahoe + Apple Silicon only.** Narrower than Tart (which runs on
  older macOS). Fine as an *additional* backend; doesn't broaden reach.
- **Memory isn't released back to the host** (virtio balloon allocates but
  doesn't shrink; noted in the WWDC 2026 session 389 HN thread). Minor for
  ephemeral sandboxes.
- **No Docker API socket** → zero reuse of the Docker SDK path; a fresh
  shell-out backend (Tart-style mechanism, Docker-style capabilities).
- **Shared per-user apiserver** — see principal-isolation note above.

## Recommendation

Viable and attractive: "Docker's images + workflow, with VM-grade isolation per
sandbox, native on macOS, no Docker Desktop." Closes the weakest link in the
current macOS isolation story (Docker Desktop's shared-kernel VM) without
changing the image/profile workflow. Proceed to a backend design plan; the main
engineering judgment is risk appetite for a v1 bespoke CLI. See
[plans/apple-container-backend.md](../plans/apple-container-backend.md).

## Reproduction (spike commands)

```
container image pull alpine:3.22
container run -d --name spike -v /tmp/work:/mnt/work alpine:3.22 sleep 3600
container exec spike cat /mnt/work/from-host.txt        # Q1 host→guest
container exec spike sh -c 'echo x > /mnt/work/g.txt'   # Q1 guest→host (visible on host live)
container run --rm --cap-add CAP_SYS_ADMIN alpine:3.22 sh -c 'mount -t overlay …'   # Q2
container run --rm --cap-add CAP_NET_ADMIN --cap-add CAP_NET_RAW alpine:3.22 \
  sh -c 'apk add iptables; iptables -A OUTPUT -d 1.1.1.1 -j DROP; wget …'           # Q3
container inspect spike            # Q4 JSON
container list --format json       # Q4 JSON
```
