# Egress-broker host reachability — how a sandbox reaches the host-side injector (per backend)

ABOUTME: Per-backend map of how an in-sandbox agent reaches a host-side TCP listener (the
credential injector), and where that listener should bind so only the sandbox — not the LAN —
can reach it. Backs the `InjectorReach{BindHost,DialHost}` discovery seam for egress-proxy
step 2b-2 (D105/D106). Linux findings verified; macOS items flagged for a Mac spike.

## Why this exists

The always-on key-injector (D105 layer 1, `internal/broker`) runs **host-side**, out of the
agent's reach. The agent is pointed at it via `base_url`. Two addresses must be resolved, and
**they are backend-specific**:

- **DialHost** — what the in-sandbox agent puts in `base_url` to reach the injector.
- **BindHost** — what host interface the injector listens on, chosen so *only* that sandbox's
  network can reach it (never `0.0.0.0`, which exposes it on the host LAN).

Decision (2026-06-28): **gateway-IP-for-both** on the bridge/CNI backends — the agent dials the
gateway IP and the injector binds the same IP. The `InjectorReach{BindHost, DialHost}` split is
kept because it is *required* to express Docker Desktop (see caveat), even though on Linux
Engine the two coincide.

## Current state of the tree

- `BackendDescriptor.HostFromContainer` (`runtime/runtime.go:227`) is **advisory only** today:
  read by help text (`internal/cli/helpcmd/help.go:193`), a localhost-mount hint
  (`internal/orchestrator/create/prepare_dirs.go:141`), and the discovery API
  (`discovery.go:207`). It is **not** wired into container networking.
- docker/podman declare `HostFromContainer: "host.docker.internal"` but the docker `HostConfig`
  (`runtime/docker/docker.go:493`) sets **no `ExtraHosts`** — so on **Linux Docker Engine the
  alias does not resolve inside the sandbox** (the live spike had to pass
  `--add-host=host.docker.internal:host-gateway`). containerd/tart/seatbelt/apple leave it `""`.

## Per-backend map

| Backend | Network model | DialHost (agent base_url) | BindHost (injector) | Isolation present? |
|---|---|---|---|---|
| **docker** (Linux Engine) | default bridge + NAT (172.17.0.0/16) | bridge gateway IP, e.g. `172.17.0.1` | same gateway IP | in-sandbox iptables (entrypoint.py) |
| **podman** | bridge (rootless user-netns or system); reuses docker.Runtime | bridge gateway IP (varies) | same gateway IP | same as docker |
| **containerd** (Kata VM) | CNI bridge `yoloai0`, 10.89.0.0/16 (`runtime/containerd/cni.go:41`) | CNI gateway `10.89.0.1` | same gateway IP | same iptables |
| **seatbelt** (macOS) | host process, host network stack, **no netns** | `127.0.0.1` | `127.0.0.1` | none (rejects `isolated`) |
| **tart** (macOS VM) | vmnet/NAT (host IP ~`192.168.64.1`) — **unverified** | host vmnet IP — **needs Mac spike** | host vmnet IP — **needs Mac spike** | none (rejects `isolated`) |
| **apple** (macOS 26 per-VM) | per-VM CNI bridge, Apple apiserver | per-VM gateway — **needs Mac spike** | per-VM gateway — **needs Mac spike** | in-guest iptables |

Gateway IP source on the bridge backends: `inspect` the running instance's
`.NetworkSettings.Gateway` (docker/podman); for containerd it is the CNI subnet's `.1`. The
gateway is knowable only **after** the instance is created and before the agent launches.

## Two cross-cutting facts

**1. Binding to the bridge gateway IP is the "safe bind."** A host process can bind
`172.17.0.1:port`; only containers on that bridge reach it, and it is *not* on the host's LAN
interface (`eth0`). Confirmed feasible on Linux (the live spike bound the gateway-reachable
interface and a real Dockerized Claude reached it).

**2. Network isolation blocks the gateway unless allowlisted.** When `--network=isolated`
(StrategyIPFilter) is on, the in-sandbox iptables allows loopback, DNS, and established/related
but **not** the gateway (`runtime/docker/resources/entrypoint.py`). So the opt-in containment
layer would block the agent from reaching the injector unless the injector's `BindHost:port` is
allowlisted. **Phase 1 brokers with open egress (D105 default), so this does not bite yet.**
When containment lands it is actually elegant: the agent's allowlist collapses to `[injector]`
and the *host-side* injector reaches the real upstream — the egress-proxy strategy converging.

## The Docker Desktop caveat (why BindHost≠DialHost must stay expressible)

On **Docker Desktop (macOS/Windows)** the daemon runs inside a LinuxKit VM, so the bridge
gateway `172.17.0.1` lives *inside that VM*. A macOS host process **cannot bind it**, and a
container reaches the macOS host only via `host.docker.internal` (which Docker Desktop maps to
the VM's host-gateway → the host). So on Docker Desktop the injector binds a macOS host
interface (`127.0.0.1`/`0.0.0.0` on the Mac) and the agent dials `host.docker.internal` —
i.e. **DialHost ≠ BindHost**. "Gateway-IP-for-both" is therefore a **Linux-Engine-only**
realization; the `InjectorReach` interface keeps the two fields precisely so the Docker Desktop
backend variant can return `host.docker.internal` / Mac-host-bind without reworking the seam.

## Mac spike brief (run on a macOS + Apple-Silicon host; not blocking Linux 2b-2)

Goal: for each macOS backend, verify (a) the address the guest uses to reach a host-side
listener, and (b) a host interface the listener can bind that the guest reaches. Pattern mirrors
`egress-broker-spike/` — a trivial host listener + a one-line reachability probe from the guest.

1. **Host listener:** `python3 -m http.server 8788 --bind 0.0.0.0` (or a Go one-liner). Note the
   host's reachable IPs.
2. **Docker Desktop:** `docker run --rm curlimages/curl curl -s http://host.docker.internal:8788`
   (expect OK) and `... curl http://192.168.65.* / 172.17.0.1:8788` (expect fail) — confirms
   alias required, gateway-IP unreachable. Confirm whether `--add-host=...:host-gateway` is
   needed or implicit on Desktop.
3. **Tart:** boot a VM, from the guest `curl http://<host-vmnet-ip>:8788`; determine the host IP
   the guest's default route points at (`ip route` in guest) and whether the host can bind it.
4. **Seatbelt:** from a `sandbox-exec`'d process, `curl http://127.0.0.1:8788` (expect OK — same
   network stack); confirm SBPL allows localhost TCP.
5. **apple `container`:** from the guest, find the per-VM gateway and `curl` the host listener
   bound there; confirm CNI gateway is host-bindable.

Record results back here (verified column) before wiring the macOS `InjectorReach` variants.

## Conclusion (feeds 2b-2)

Discovery is a **backend responsibility**: an optional `InjectorReachable` interface returning
`InjectorReach{BindHost, DialHost}`. Linux docker/podman → inspect `.Gateway` (both fields equal);
containerd → CNI gateway; seatbelt → `127.0.0.1`; tart → unimplemented (brokering falls back to
direct delivery, D105(g)); apple → per-VM gateway (post-Mac-spike). The orchestrator calls it to
fill `InjectorSpec.BindHost` + the agent's `base_url`. Backends that do not implement it fall back
to today's direct credential delivery — no flag-day.
