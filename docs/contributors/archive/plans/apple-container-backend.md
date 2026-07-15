> **ABOUTME:** Plan for an Apple `container` runtime backend — Linux OCI in per-container VMs on
> macOS — plus the resulting macOS backend priority and setup-wizard rework needed once several
> container systems can coexist there.

# Apple `container` backend

- **Status:** IMPLEMENTED — the backend and the macOS priority/wizard rework shipped on `main`
  (skeleton + two-tier probe + lifecycle/exec/mounts `b85a11ad`, `5bd44b91`, `c2f90c7f`; network
  isolation; container-system selector `4a90d1a9`; curated env keyset `4651b645`; wizard presets
  `cff6aebd`; docs `66f596c1`), with the macOS default-isolation shift recorded in
  `BREAKING-CHANGES.md`. **AC10 closed 2026-07-15** by the live end-to-end run it always needed:
  `--network-isolated` on a real apple sandbox resolves DNS through the vmnet gateway
  (`/etc/resolv.conf` = `192.168.64.1`, as this plan predicted), the default-deny OUTPUT chain
  ACCEPTs that gateway on udp+tcp `:53` with no host-DNS assumption hard-coded, an allowlisted
  domain returns HTTP 200, and a non-allowlisted one is REJECTed and fails fast. The run also
  surfaced DF104 — the allowlist is IPv4-only on every backend, latent because vmnet hands out a
  non-routable ULA — which is the shared firewall's gap, not apple's, and is filed rather than
  fixed here. Work-copy git confinement for apple + seatbelt followed as its own plan
  ([confine-host-side-git-macos-build.md](../../archive/plans/confine-host-side-git-macos-build.md), D113).
- **Depends on:** —

## Why this doc exists

Apple's `container` (CLI 1.0.0, macOS 26, Apple Silicon) runs Linux OCI
containers, each in its own lightweight Virtualization.framework VM. A hands-on
spike ([research/apple-container.md](../research/apple-container.md)) resolved
the open questions positively: live `:rw`/`:ro` virtiofs mounts, in-guest
overlayfs (with `CAP_SYS_ADMIN`, same as Docker), in-guest iptables for the
network allowlist, and a clean `--format json` CLI with exit-code propagation.
Conclusion: it's a **Docker-class backend with stronger isolation** (per-container
VM, own kernel), reusing the **existing Dockerfile/profile images unchanged**,
native on macOS, no Docker Desktop.

This plan covers (1) the new backend and (2) a related change the user asked
for: a defined **macOS backend priority** now that four Docker-like systems can
coexist.

## Naming decision (resolve first)

The binary is literally `container`; that word collides with the domain noun
throughout the codebase. Proposed `BackendType` key: **`apple`** (user-facing
`--backend apple`, description "Apple container — Linux OCI in per-container
VMs"). Alternative: `apple-container`. Avoid `container`. Pick one and add the
const to `runtime/names.go`. (This doc uses `apple`.)

## Backend shape

Mechanism mirrors **Tart** (shell out to a bespoke CLI, parse output);
capabilities mirror **Docker** (full mount-mode parity). Package
`runtime/apple/`. Register via `init()` → `runtime.Register(factory,
descriptor)`, guarded to `darwin`/`arm64`.

### Descriptor

```
Type:                    BackendApple          // new const in names.go
Description:              "Apple container — Linux OCI in per-container VMs"
Platforms:               []string{"darwin"}
Architectures:           []string{"arm64"}
Requires:                "Apple container CLI + macOS 26 (Tahoe)"
InstallHint:             "https://github.com/apple/container"
BaseModeName:            IsolationModeVM       // vm-tier: it IS per-container-VM isolation (see "Selection modeling")
AgentProvisionedByBackend: false              // uses our OCI profile image; agent installed via Dockerfile
HostFromContainer:       ""                    // no host.docker.internal analogue; use routable IP
Capabilities: BackendCaps{
    NetworkIsolation: true,                    // in-guest iptables verified
    OverlayDirs:      true,                     // overlayfs + CAP_SYS_ADMIN verified
    CapAdd:           true,
    HostFilesystem:   false,
    VMRuntimeDir:     "",                        // literal mount paths → /yoloai default works (no Tart-style remap)
}
Probe:         installed = LookPath("container")+macOS26; running = `container system status` (two-tier, see #5)
VersionString: `container --version`
CleanupHint:   func(img) → "container image delete " + img
```

`SecretsConsumedTimeout`: sub-second VM boot (spike: per-container VM starts
fast), so the default is likely fine — unlike Tart's 180s. Confirm against a
cold `system start`.

### Interface method → CLI verb

| `runtime.Backend` method | `container` CLI |
|---|---|
| `Setup` | `container system start` **and `container builder start`** if needed (the builder is a separate VM, *not* running by default — AC3 cold-start, pulls a builder image); build image via `container build -t yoloai-<profile> -f <abs>/Dockerfile <abs-context>` — the **context path MUST be absolute**: a relative `.` silently transfers an empty (2-byte) context and every `COPY` fails (AC1 → backend-idiosyncrasies). No Docker daemon needed. Or `image pull` for pull-only profiles. |
| `IsReady` | `container system status` reachable + target image present (`image inspect`). |
| `Create` | `container create --name <n> <image> <init-cmd>` + mounts/caps/env/resources flags (below). |
| `Start` / `Stop` / `Remove` | `container start` / `container stop` / `container delete`. Map "already gone/stopped" to nil. |
| `Inspect` | `container inspect <n>` → JSON; read `.status.state` → `InstanceInfo.Running`. `ErrNotFound` on exit≠0/empty. Mounts render **nested** (`mounts[].type = {virtiofs:{}}` + an `options[]` array), not flat `type/source/destination` — parse the enum object (AC6). |
| `Exec` | `container exec <n> <cmd>`; capture stdout, propagate exit code into `ExecResult`. |
| `InteractiveExec` | `container exec -i -t -u <user> -w <workDir> <n> <cmd>`; normalize non-zero exit via `runtime.InteractiveExitError`. |
| `Prune` | `container list -a --format json`, filter `yoloai-*` not in `knownInstances`, `container delete`. |
| `DiagHint` | `container logs <n>` / `container system logs`. |
| `TmuxSocket` | "" (uid default) unless we pin one. |
| `AttachCommand` | `container exec -i -t <n> tmux -S <sock> attach …` (same shape as docker). |

### Mount-mode translation

`InstanceConfig.Mounts` (`MountSpec{HostPath, ContainerPath, ReadOnly}`) and the
sandbox dir-modes map to `container` flags (all verified in the spike):

| yoloAI mode | `container` invocation |
|---|---|
| `:copy` (default) | host-side copy + git baseline (backend-agnostic), mounted like any dir. |
| `:rw` | `--mount type=virtiofs,source=<host>,target=<guest>` (rw default). Live, both directions. |
| `:ro` | `--mount type=virtiofs,source=<host>,target=<guest>,readonly`. |
| `:overlay` | overlayfs inside guest; requires `--cap-add CAP_SYS_ADMIN`. Same shape as the docker backend's overlay path → reuse `mounts.Build`'s overlay lower/upper layout; `VMRuntimeDir:""` means the existing `/yoloai/overlay/...` paths work unchanged. |

No host↔guest path remapping (literal targets) → the docker mount-building path
applies almost directly; far less custom code than Tart's `mounts.go`.

`InstanceConfig` field coverage: `CapAdd`→`--cap-add`, `ContainerEnv`→`--env`,
`Resources`→`-m`/cpu, `UseInit`→`--init` (verify flag name).
`Ports`→published ports (`container run -p`; verify create-time support).
`Labels`→ stamp into instance (verify label support; else persist in our
environment.json like Tart/Seatbelt). `Privileged`→ ignored (see "Privileged").

### Privileged: N/A (a VM is already privileged)

A VM is already "privileged" — the hypervisor *is* the security boundary, and
nothing inside the guest can reach the host. There is **no privileged mode** on
this backend. `IsolationModeContainerPrivileged` exists only because shared-
kernel containers are deliberately *de*-privileged and DinD needs that
constraint lifted (`isolation.go:110` "docker-in-docker, etc."); a VM has no
such constraint to lift. So:

- `apple` does **not** advertise `IsolationModeContainerPrivileged`, and it stays
  out of any privileged decision matrix. `--isolation container-privileged`
  remains a **container-slot** mode (docker/podman) — the right home for "DinD in
  a shared-kernel container."
- `apple` never sees `InstanceConfig.Privileged == true` (only the privileged
  isolation mode sets it, and that routes to the container slot), so the field is
  simply ignored by this backend — nothing to map.
- Workloads on `apple` that need elevated capabilities (DinD or otherwise) get
  them via plain `--cap-add`, granted freely because it's host-safe — not gated
  behind a mode. (The in-guest default cap set is still restricted — the spike
  needed `--cap-add CAP_SYS_ADMIN` for overlay — so it's an explicit `--cap-add`,
  not automatic.) DinD on a real-kernel VM should be cleaner than nested DinD in
  a container (real cgroups + disk; see
  [dind-storage-drivers](../research/dind-storage-drivers.md)).

### Network isolation

Verified: `--cap-add CAP_NET_ADMIN` + in-guest `iptables` enforces the allowlist
(own per-VM kernel — none of gVisor's userspace-netstack problem). The allowlist
machinery (`runtime/docker/resources/entrypoint.py:isolate_network`:
resolve `allowed_domains`→IPs, build ipset+iptables) is **backend-agnostic** — it
needs only an iptables-honoring kernel (✓), `iptables`+`ipset` in the image,
`CAP_NET_ADMIN`, and a working `/etc/resolv.conf`. **AC10 caveat:** in an apple
guest `/etc/resolv.conf` is the **vmnet gateway** (`192.168.64.1`, Apple's own
resolver), *not* the host's nameservers and *not* a fixed subnet — so the
default-deny OUTPUT chain must ACCEPT the gateway's `:53` (it should fall out of
the existing "allow resolv.conf nameservers" rule, but verify no host-DNS
assumption is hard-coded). `ipset` rode on iptables-nft fine in the spike, and the
entrypoint already has a `use_ipset=False` fallback. **Needs a live end-to-end
`--network-isolated` test**, not just the raw-iptables capability. `NetworkIsolation: true`.

## Curated-env keyset (`HostEnv`)

`container` reads these (from source grep — see research doc). Define a purpose
method (e.g. `EnvForAppleContainer`) producing a curated keyset, mirroring the
`EnvForDaemonDiscovery`/`GitEnv` pattern:

- **Always (CLI must function):** `PATH` (locate binary + plugins under install
  root), `HOME` (default `CONTAINER_APP_ROOT` = `~/Library/Application
  Support/com.apple.container` resolves from it).
- **State/install/log roots (pass through if set):** `CONTAINER_APP_ROOT`,
  `CONTAINER_INSTALL_ROOT`, `CONTAINER_LOG_ROOT`.
- **Image ops:** `CONTAINER_DEFAULT_PLATFORM`; registry creds
  `CONTAINER_REGISTRY_HOST` / `CONTAINER_REGISTRY_USER` / `CONTAINER_REGISTRY_TOKEN`
  (only when pulling private images).
- **Optional UX:** `CONTAINER_DEBUG`, `NO_COLOR`, `BUILDKIT_COLORS`.
- **Deliberately NOT forwarded:** `SSH_AUTH_SOCK` — `container` forwards the host
  SSH agent into the container/machine; we don't want the host agent inside a
  sandbox. Omit from the keyset (defense-in-depth; matches [never-swallow /
  curated-env hygiene]).

**Key difference from Docker/Podman:** `container` reaches its daemon via a
**per-user XPC mach service**, not a socket env var. There is **no
`CONTAINER_HOST`/`DOCKER_HOST` analogue**, so:

- `runtime.DaemonEnvVars` (probe.go) does **not** need new keys for apple — its
  probe ignores the env map for daemon discovery.
- The apiserver is **shared per macOS user** across principals; per-principal
  state isolation (if ever needed) would go through `CONTAINER_APP_ROOT` and is
  out of scope for the single-principal backend (note for
  [principal-isolation](../research/principal-isolation.md)).

## macOS backend selection & priority

`apple` is a **vm-tier** backend (`BaseModeName: IsolationModeVM`) — it *is*
per-container-VM isolation, not a container-slot member. The load-bearing rule:

> **VM-tier is the default only where VMs are cheap to start.** Apple container
> VMs boot sub-second, so on macOS the vm-tier (`apple`) is the *preferred
> default*. On Linux, VM isolation (containerd/Kata) is heavy and slow to start,
> so it stays low-priority — never the default, reached only via explicit
> `--isolation vm`.

### macOS default pick (nothing specified), by installed-ness

1. **`apple`** (vm) — fast per-container VM, strongest isolation, native, no Docker Desktop
2. **`orbstack`** → `docker` backend, `~/.orbstack/run/docker.sock`
3. **`docker-desktop`** → `docker` backend, `~/.docker/run/docker.sock`
4. **`podman`** → `podman` backend

The user picks from these four named systems they recognize and never needs to
know OrbStack and Docker Desktop are internally the *same* `docker` backend at
different endpoints — that mapping is hidden below a user-facing "container
system" selector (#4). Note this priority list **spans tiers**: `apple` wins as
the vm-tier default *above* the container slot (orbstack/docker-desktop/podman),
not as a member of it.

### Routing (resolved)

| `--isolation` | host | routes to |
|---|---|---|
| *(unspecified)* default | macOS | `apple` if installed, else container slot (orbstack > docker-desktop > podman) |
| *(unspecified)* default | Linux | container slot (docker > podman); VM **not** default |
| `container` / `container-enhanced` | any | container slot only — **never** `apple` |
| `vm` (Linux workload) | macOS | **`apple`** — was: degrade to docker (containerd is Linux-only, so today it falls through to the container slot) |
| `vm` (Linux workload) | Linux | containerd/Kata |
| `vm` + `--os mac` | macOS | `tart` (macOS *guest*) — unchanged |

The `""`(default) vs `"container"`(explicit) distinction is what lets the
default prefer `apple` while an explicit `--isolation container` still yields
shared-kernel docker. `tart` and `apple` coexist on the vm tier without
collision: `tart` serves macOS guests, `apple` serves Linux guests.

### What changes

1. **`SelectBackend` routing (`runtime/probe.go`).** Add darwin-host
   handling so that (a) the unspecified default prefers `apple` when installed
   before the container slot, and (b) `--isolation vm` for a Linux workload
   routes to `apple` instead of falling through to docker. `apple` is **not**
   added to `containerBackends()` — it's vm-tier; the cross-tier default
   preference lives in the darwin branch of `SelectBackend`, keeping
   `containerBackends()` = docker/podman. **An explicit `container_backend`
   preference must win over the apple-default**: the darwin default branch honors
   a non-blank `container_backend` (the user chose a container system) before
   falling to "prefer apple" — otherwise a user who picked docker would be
   silently overridden by apple on every launch. The darwin pre-step must gate on
   `isolation==Default` (blank): an explicit `--isolation container` must still
   bypass apple and hit the container slot (AC11).

2. **Container-slot order stays explicit** (the orbstack/docker-desktop/podman
   sub-order, used for the explicit-container path and the Linux default): keep
   docker > podman, and resolve OrbStack vs Docker Desktop via #3.

3. **OrbStack before Docker Desktop in docker socket discovery.**
   `runtime/docker/dockerhost.go` `wellKnownDockerSockets` currently
   lists Docker Desktop **before** OrbStack — reorder to OrbStack first
   (dead-endpoint fallback only; explicit `DOCKER_HOST` / active `docker
   context` still wins).

4. **CLI/config surface — four first-class named choices.** A **"container
   system" selector layer** above `BackendType`: a user-facing id resolves to an
   internal (`BackendType`, endpoint-override) pair.

   | User-facing choice | Resolves to |
   |---|---|
   | `apple` | (`apple` backend, —) |
   | `orbstack` | (`docker`, `~/.orbstack/run/docker.sock`) |
   | `docker-desktop` (or `docker`) | (`docker`, `~/.docker/run/docker.sock`; bare `docker` = auto-endpoint) |
   | `podman` | (`podman`, —) |

   `--backend` (`internal/cli/lifecycle/new.go`) and `container_backend` (config)
   already accept any string — extend the resolver (`ResolveBackend` /
   `SelectBackend`) to accept these ids and map `orbstack`/`docker-desktop` to
   the docker backend with a pinned `DOCKER_HOST`. List all detected systems in
   `yoloai system backends`. Bare `--backend docker` stays valid (auto-endpoint).
   No dedicated `--podman` flag exists; this is the same generic mechanism.

5. **Two-tier probe for all backends (resolved — cross-cutting).** Standardize a
   distinction every backend honors:
   - **installed** — the binary/tool exists (`LookPath`/file stat). Cheap,
     side-effect-free.
   - **running** — the daemon/service is actually reachable now (stat/dial the
     socket, `container system status`, `podman machine` reachable, …).

   **Today this is inconsistent:** the single `BackendDescriptor.Probe` means
   "installed" for tart/seatbelt but "running" for docker, with `IsAvailable`
   only covering static compile-in. Split it so every backend exposes both
   (e.g. a status `Absent`/`Installed`/`Running` + reason, or two funcs).
   **Selection/priority and the wizard's "(not installed)" tag use *installed*;
   point-of-use uses *running*** — and when a backend is installed-but-stopped,
   start it on demand where possible (`apple` → `container system start`; podman
   → `podman machine start`) or surface "installed but not running; start with
   …" where not (Docker Desktop). This is what lets an installed-but-stopped
   `apple` be the preferred default (we just start it) — the speed-tier rule.

   This touches all five backends' probes + the descriptor contract — apple is
   the forcing function but the change is general. Sequence it before the apple
   probe so apple plugs into the new contract rather than overloading the old.

6. **Setup wizard (`yoloai system setup`) → default-environment presets (rework).**
   The backend step (`internal/cli/system/setup.go` — `availableBackends` +
   `resolveChoice`, today writing only `container_backend`) becomes a **flat list
   of default-environment presets**. The key realization: on macOS a pick implies
   up to **three** config keys — guest `os`, `isolation` tier, and
   `container_backend` — not just a backend. The technology implies the os, so we
   never ask "mac or linux?" separately (one flat list, decided).

   **Preset → config mapping (macOS host):**

   | Preset | `os` | `isolation` | `container_backend` |
   |---|---|---|---|
   | `apple` *(recommended)* | — | — | — |
   | `orbstack` | — | — | `orbstack` |
   | `docker-desktop` | — | — | `docker-desktop` |
   | `podman` | — | — | `podman` |
   | `tart` (macOS guest) | `mac` | `vm` | — |
   | `seatbelt` (macOS sandbox) | `mac` | — | — |

   - **Write the keys a preset uses; `Reset` the ones it doesn't** — so switching
     `tart`→`apple` clears `os`/`isolation` rather than leaving stale `mac`/`vm`.
     `Config().Set(key, "")` does **NOT** clear (verified, AC2: it writes an empty
     string and `mergeStringField` treats an empty override as "keep base"). Use
     the existing `DeleteConfigField` / `ConfigAdmin.Reset`
     (`config/yamlnode.go:249`, `system_config.go:88`) for the unused keys.
   - **`container_backend` is blank for `apple`/`tart`/`seatbelt`** — the
     preference lives in `os`/`isolation`. So blank `container_backend` now means
     *any* of: setup not run, or a VM preset (`apple`/`tart`), or the process
     preset (`seatbelt`). These map onto routing (#1): `os=mac`→tart/seatbelt;
     `container_backend` set→container slot (honored over the apple-default);
     all-blank→apple default.
   - **Flat list, all presets shown regardless of installed**, in the shared
     order (#7); absent ones tagged **"(not installed)"**; default highlight =
     `apple` "(recommended)". Always prompts; a not-installed pick is saved and
     falls back gracefully at launch.
   - **Novice-friendly hint text** per option (`setupChoice.Blurb`) — "Fastest +
     strongest isolation, macOS-native (recommended)" rather than "Linux OCI in
     per-container VMs".
   - **Non-macOS hosts** collapse to the relevant presets (Linux: docker/podman,
     containerd-vm; no apple/tart/seatbelt) via the same mechanism.

7. **Single shared priority order.** The backend preference order — `apple` >
   `orbstack` > `docker-desktop` > `podman` — is defined **once** and consumed by
   both the blank-config auto-pick (#1) and the wizard's preset ordering, so they
   can't drift. The wizard additionally lists the macOS-guest presets (`tart`,
   `seatbelt`) below these; those are wizard-only (opt-in) and are never
   auto-selected as the default.

### Selection modeling decision (resolved — needs a D-entry)

`apple`'s `BaseModeName` is **`IsolationModeVM`**: it is genuine per-container-VM
isolation, so it lives on the **vm tier**, not the container slot. This rejects
the earlier "force it into the container slot" framings (relabel base mode, or
add a `ContainerSlot` flag) as dishonest about its isolation strength.

It fills a real gap: macOS currently has **no Linux-VM-isolation backend** —
`--isolation vm` for a Linux workload degrades to shared-kernel docker because
containerd/Kata is Linux-only. `apple` is that backend, coexisting with `tart`
(macOS guests) on the vm tier.

The macOS **default** prefers vm-tier `apple` over the container slot **because
apple VMs are cheap to start** — the platform-specific "VM-default only where
VMs are fast" rule above is the load-bearing rationale and the heart of the
D-entry. Consequence to record: on macOS with `apple` installed, the default
sandbox becomes VM-isolated; `--isolation container` is the opt-back to docker.
Record in `decisions/working-notes.md`.

## Sequencing

1. **Two-tier probe refactor (cross-cutting, prerequisite):** split installed vs running across all backends + the descriptor contract; selection uses installed, point-of-use uses running with start-on-demand. Settles the last probe question and unblocks apple's probe + the wizard "(not installed)" tag.
2. Backend skeleton: descriptor + registration + installed/running probes + `system status`/`build`/lifecycle/`inspect` + Exec/InteractiveExec. Get a sandbox running with `:copy`.
3. Mount modes `:rw`/`:ro`/`:overlay`; network-isolation iptables path.
4. Selection: shared priority order (one definition) → darwin-host routing (default prefers `apple`, explicit `container_backend` wins over it; `--isolation vm` Linux → `apple`) + OrbStack-first sockets + container-system selector; `yoloai system backends` output.
5. **Wizard rework → default-environment presets:** flat preset list writing `os`/`isolation`/`container_backend` per the mapping table, clearing unused keys via `DeleteConfigField`/`Reset` (not `Set("")` — AC2); "(not installed)"/"(recommended)" tags + hint text.
6. Curated-env keyset (`EnvForAppleContainer`) + forbidigo-gate per the env-access-seal pattern.
7. Tests (lifecycle, mounts, installed/running probe, selection-priority matrix, wizard preset→config mapping) + `make check`; GUIDE backend section; backend-idiosyncrasies entries for any v1 quirks; decision-log entry.

Naming (`apple`) is settled; no design decisions gate the work now.

## Open questions / risks

- **v1 churn.** Bespoke CLI, no stable API contract; pin to the probed version,
  re-verify JSON/flags on upgrades. Biggest real risk.
- **Two-tier probe (resolved → cross-cutting work).** installed = binary exists; running = reachable now; selection by installed, point-of-use by running (start-on-demand where possible). Touches all five backends' probes + the descriptor contract — bigger than apple, but apple is the forcing function. No design decisions remain open.
- **Wizard preset clears stale keys (resolved, AC2).** `Config().Set(key, "")` does NOT clear (empty override ignored by `mergeStringField`); the preset writer must `DeleteConfigField`/`Reset` unused keys. The primitive already exists — no design fork.
- **Default isolation shifts to VM on macOS** when `apple` is installed — a behavior change for existing users (stronger default; `--isolation container` opts back). Call out in release notes / `BREAKING-CHANGES.md`.
- **Image build (resolved, AC1/AC3).** Existing Dockerfiles build under apple's
  own builder — **no Docker daemon needed** (the feared docker-coupling fallback
  is ruled out; "no Docker Desktop" survives). Two required steps: `container
  builder start` before first build (separate builder VM, cold-start), and an
  **absolute** build-context path — a relative `.` silently transfers an empty
  context and all `COPY`s fail. Add the absolute-context quirk to
  `backend-idiosyncrasies.md`.
- **Network allowlist — end-to-end test still needed (AC10).** The machinery is
  backend-agnostic and ported in the spike, but apple's DNS is the **vmnet
  gateway** (`192.168.64.1`), not host resolv.conf — the default-deny chain must
  ACCEPT gateway:53. Run the *real* `--network-isolated` path end-to-end (not
  just raw iptables) before calling it done. See the Network isolation section.
- **macOS version gate (AC14).** `container` actually *runs* on macOS 15 (with
  limitations: no container-to-container net, no `container network`, IP
  conflicts) and some features want **M3+**. Keep the strict `installed =
  LookPath + macOS≥26` probe gate (a safe over-gate avoiding the macOS-15
  footguns); note the M3-for-some-features caveat in GUIDE.
- **Memory not released to host** (virtio balloon) — minor for ephemeral sandboxes; note in GUIDE.
- **Labels / ports at create (confirmed, AC14).** `--label`, `-p/--publish`, `--init`, `--cidfile` all exist at create time — no environment.json fallback needed for labels.
- **Suspend/resume + VS Code attach (confirmed, AC14).** No `suspend`/`pause`/`checkpoint` subcommand → `InstanceInfo.Suspended: false`. No docker-compat surface → `ContainerAttach: false` (like Tart); VS Code "Attach to Running Container" won't work, `exec`-based attach does.
- **Privileged: N/A** — a VM is already privileged, so `apple` has no privileged mode and doesn't advertise `container-privileged` (a container-only special case). Workloads needing caps use plain `--cap-add` (host-safe). Settled — see "Privileged" above.

## References

- [research/apple-container.md](../research/apple-container.md) — the verified spike + env-var catalog.
- [research/linux-vm-backends.md](../research/linux-vm-backends.md) §8 — prior "evaluate when macOS 26 adopted" note this supersedes for the spike.
- `runtime/runtime.go` — `Runtime`, `InstanceConfig`, `BackendCaps`, `BackendDescriptor`.
- `runtime/probe.go` — `SelectBackend` / `SelectContainerBackend` / `orderCandidates` (priority change).
- `runtime/docker/dockerhost.go` — `wellKnownDockerSockets` (OrbStack-vs-Desktop order).
- `runtime/tart/tart.go` — closest mechanism analog (shell-out descriptor/registration).
- `internal/cli/cliutil/client.go` — `ResolveBackend` / `container_backend`.
- `internal/cli/lifecycle/new.go:40` — the `--backend` flag.
- `internal/cli/system/setup.go` — the setup wizard's backend step (`availableBackends`, `resolveChoice`); third selection surface.
