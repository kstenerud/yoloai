# Egress-broker host reachability — how a sandbox reaches the host-side injector (per backend)

ABOUTME: Per-backend map of how an in-sandbox agent reaches a host-side TCP listener (the
credential injector), and where that listener should bind so only the sandbox — not the LAN —
can reach it. Backs the `InjectorReach{BindHost,DialHost}` discovery seam for egress-proxy
step 2b-2 (D105/D106). Linux findings verified; macOS (docker-Desktop/OrbStack, seatbelt, tart,
apple) verified by the 2026-06-28 Mac spike — see "Mac spike results".

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
| **podman (rootful**, system socket) | real host bridge; reuses docker.Runtime | bridge gateway IP (host iface) | same gateway IP | same as docker |
| **podman (rootless**, netavark default) | bridge **inside** the rootless netns; gateway (e.g. `10.88.0.1`) **not** on any host iface | `10.0.2.2` (slirp4netns host alias) — **requires `slirp4netns:allow_host_loopback=true`** | `127.0.0.1` (loopback; safe) | same iptables |
| **containerd** (Kata VM) | CNI bridge `yoloai0`, 10.89.0.0/16 (`runtime/containerd/cni.go`) | CNI gateway `10.89.0.1` ✅ verified | same gateway IP ✅ host-bindable | same iptables |
| **docker** (Desktop / OrbStack, macOS) | daemon in a Linux VM; bridge gateway lives *inside* the VM | `host.docker.internal` (alias; implicit on OrbStack) | macOS `127.0.0.1` (loopback — verified reachable via alias) | in-sandbox iptables |
| **seatbelt** (macOS) | host process, host network stack, **no netns** | `127.0.0.1` | `127.0.0.1` | none (rejects `isolated`) |
| **tart** (macOS VM) | Apple vmnet/NAT, **per-VM bridge** (verified `192.168.65.1`) | host vmnet gateway IP (guest default route), e.g. `192.168.65.1` | same vmnet gateway IP (VM-only, not LAN) | none (rejects `isolated`) |
| **apple** (macOS 26) | Apple vmnet/NAT, **shared subnet** (verified `192.168.64.0/24`) | shared vmnet gateway `192.168.64.1` (guest default route) | same vmnet gateway IP (VM-only, not LAN) | in-guest iptables |

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

## Rootless podman (verified on Linux, podman 4.9.3, netavark — 2026-06-28)

The original map assumed podman == docker ("gateway-IP-for-both"). That holds for **rootful**
podman (system socket: a real host bridge whose gateway is a host interface, bindable). It is
**false for rootless** podman, the common case. Verified empirically on this host (yoloai sandbox
on the default rootless `podman` network, container gateway `10.88.0.1`):

- **The bridge gateway is not host-bindable.** A host process binding `10.88.0.1:0` fails with
  `EADDRNOTAVAIL (errno 99)` — the netavark bridge and its gateway live *inside* the rootless
  network namespace, not on any host interface (`ip addr` shows no such address). So the injector
  cannot bind the gateway, and "gateway-IP-for-both" cannot work. (Symptom in the launch path:
  `start credential injector: broker: sidecar handshake failed: EOF` — the sidecar's `net.Listen`
  fails and it exits before the handshake.)
- **`host.docker.internal` / `host.containers.internal` resolve to the host's LAN IP**
  (`192.168.111.33` here), not loopback. Binding the injector there would expose an open
  credential-injecting proxy to the LAN — a credential-exposure regression, so it is **not** an
  acceptable default. (Confirmed: a loopback-bound server is `Connection refused` via that IP.)

Two **safe** paths reach a **loopback-bound** injector (`BindHost = 127.0.0.1`, no LAN exposure),
each requiring a specific per-sandbox network mode (verified container → host loopback server):

| Network mode | DialHost (agent base_url host) | Verified |
|---|---|---|
| `--network slirp4netns:allow_host_loopback=true` | **`10.0.2.2`** (fixed slirp host alias) | ✅ reached; plain `slirp4netns` (no flag) → unreachable |
| `--network pasta:--map-gw` | container default gateway (host LAN gw, **varies**, e.g. `192.168.111.1`) | ✅ reached |
| `--network pasta` (default) / `host.containers.internal` | LAN gw / host LAN IP | ❌ |

**Recommendation: `slirp4netns:allow_host_loopback=true` with `InjectorReach = {BindHost:
"127.0.0.1", DialHost: "10.0.2.2"}`** — the DialHost is a fixed constant (no per-host inspect),
versus pasta's `--map-gw` whose DialHost varies with the host's LAN gateway. Cost: slirp4netns is
userspace TCP/IP (slower than the netavark bridge), acceptable for the LLM-proxy path; and the
brokered rootless-podman sandbox must be **created** with this network mode. The mode is a
**create-time** decision but brokering is currently decided post-start — the open design choice is
*always* use it for rootless podman vs. only when brokering will engage (the brokering inputs —
brokerable agent, key present, posture, open networking — are knowable at create time). Podman
therefore needs its **own** `InjectorReach` that branches on rootless (the `Runtime.rootless`
field already exists): rootful → gateway-for-both; rootless → `{127.0.0.1, 10.0.2.2}`.

The agent-free bring-up itself works on rootless podman once it opts into `AgentFreeLaunch` (it
embeds `*docker.Runtime`, inheriting `Launch`/`Ready`); that was validated separately. The only
blocker was injector reachability, resolved as above. (Aside discovered en route: `yoloai system
build --backend podman` no-ops against a stale podman image because the build-inputs checksum is
host-side and shared across backends — a docker build marks it "current" so podman never rebuilds.
Force a rebuild by removing the podman image first.)

## containerd / Kata (verified on Linux — 2026-06-28)

`InjectorReach` is **implemented** (`runtime/containerd/reach.go`): gateway-for-both on the CNI
bridge gateway `10.89.0.1` (derived from the conflist subnet via `cniGateway`). Verified end-to-end
on real Kata (`--isolation vm`): a host process **binds** `10.89.0.1` (the persistent `yoloai0`
bridge is a host interface), and from inside the Kata guest the default route is `10.89.0.1` and
`curl http://10.89.0.1:<port>` **reaches** the host listener. So the gateway-for-both model holds
for containerd exactly as for Linux Docker Engine — no rootless-podman-style namespace problem.

**First-run ordering caveat (handled):** the bridge is created during the *first* sandbox's CNI ADD
(`setupCNI`, `lifecycle.go`), which in the decoupled flow runs *after* the broker would bind the
gateway. So on a host that has never run a yoloai containerd sandbox the gateway isn't yet bindable.
`InjectorReach` checks whether the gateway IP is assigned to a host interface and returns
`ErrInjectorUnsupported` when absent → brokering safely degrades to direct delivery for that launch
and engages on the next sandbox (the bridge persists). The clean fix — eagerly ensure the bridge
before brokering (so even the first sandbox brokers) — is a follow-up; it generalizes to any
per-container-network backend (rootless podman's slirp likewise needs its network mode set at
create). Live broker integration on containerd needs the test harness under sudo + `--isolation vm`.

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

## Mac spike results (2026-06-28)

Run on macOS 26.5.1 (build 25F80), Apple Silicon. All four backends were available. Host
listeners: `python3 -m http.server 8801 --bind 127.0.0.1` (loopback-only) and `... 8802 --bind
0.0.0.0` (all interfaces); python's logger reports the client IP. No real key used; read/test-only.

| Backend | Available | DialHost (guest base_url) | Safe BindHost | LAN-exposed? | Isolation note |
|---|---|---|---|---|---|
| **docker** (OrbStack) | yes | `host.docker.internal` | **`127.0.0.1`** (loopback) | no | gateway `172.17.0.1` unreachable; alias implicit |
| **seatbelt** | yes | `127.0.0.1` | `127.0.0.1` | no | `(allow network*)` — host loopback reachable |
| **tart** | yes | `192.168.65.1` (per-VM vmnet gw) | `192.168.65.1` (vmnet) | no (VM-only) | guest `127.0.0.1` ≠ host; loopback bind unreachable |
| **apple** | yes | `192.168.64.1` (shared vmnet gw) | `192.168.64.1` (vmnet) | no (VM-only) | shared subnet, not per-VM; loopback bind unreachable |

**Did the Docker Desktop caveat hold? Yes — and with a useful refinement.** This machine runs
**OrbStack** (`docker info` → `Operating System: OrbStack`, a Desktop-class engine: daemon in a
Linux VM), not literal Docker Desktop. The caveat held exactly: the Linux bridge gateway
`172.17.0.1` is **unreachable** from the container, and only `host.docker.internal` works — so
`DialHost ≠ BindHost` must stay expressible. Two refinements over the doc's assumptions:

1. **The alias resolves *without* `--add-host` on OrbStack** (unlike Linux Docker Engine, where
   the live Linux spike needed `--add-host=host.docker.internal:host-gateway`). `--add-host` also
   works but is redundant here. Whether it's implicit is engine-specific — the safe portable move
   is still to set `ExtraHosts` when on a Desktop-class engine.
2. **BindHost can be `127.0.0.1` (loopback), not `0.0.0.0`.** The container reaching
   `host.docker.internal:8801` hit the **loopback-only** listener, and python logged the client as
   `127.0.0.1` — OrbStack NATs container→host traffic onto host loopback. So the macOS injector
   binds the *safest possible* interface (loopback, never LAN-visible) while the agent dials
   `host.docker.internal`. This is better than the doc's earlier "binds `0.0.0.0`/`127.0.0.1` on
   the Mac" — `0.0.0.0` is unnecessary.

**Tart & apple-container both use Apple's vmnet** (not a Linux/CNI bridge): the guest's default
route points at the host's vmnet bridge IP, which the host can bind, and that network is VM-only
(host LAN is `en0`=`192.168.111.x`, a different subnet). The difference is allocation: **apple-
container shares one subnet** `192.168.64.0/24` across all its VMs (gateway `192.168.64.1`; the
buildkit helper VM and a probe alpine both sat on it) — so the doc's "per-VM gateway" guess for
apple was wrong, it's a **shared host gateway**. **Tart spins up a fresh per-VM bridge** on boot
(`bridge102`/`vmenet2` = `192.168.65.1` appeared only while the VM ran). For both, the guest's own
`127.0.0.1` is the *guest's* loopback and does **not** reach the host (verified FAIL), so the
loopback-bind trick that works on OrbStack does **not** apply — the injector must bind the vmnet
gateway IP. Residual mirrors the Linux bridge: other VMs on the same vmnet bridge can reach it
(acute for apple-container's shared subnet; the opt-in containment layer's job).

**Seatbelt** is a host process on the host network stack (no netns); the real yoloai profile
(`runtime/seatbelt/profile.go`, `writeProfileNetwork`) emits an unrestricted `(allow network*)`
whenever the network mode is not `none` — so the agent can reach a host injector on
`127.0.0.1:port` with no SBPL changes. Seatbelt rejects `--network=isolated` at the orchestration
layer (`BackendCaps.NetworkIsolation:false` → `internal/netpolicy/strategy.go`), so there is no
in-sandbox firewall to allowlist.

### Raw confirming outputs (trimmed)

```
# docker (OrbStack)
docker info        → Operating System: OrbStack
host.docker.internal:8801 (loopback listener)  → OK-8801-alias   # client logged as 127.0.0.1
host.docker.internal:8802 (0.0.0.0 listener)   → OK-8802-alias
172.17.0.1:8802                                → FAIL (Could not connect)   # caveat holds
--add-host=...:host-gateway → host.docker.internal:8802 → OK   # works but redundant

# seatbelt
sandbox-exec -f '(version 1)(allow default)(allow network-outbound)' curl 127.0.0.1:8801 → OK
real profile: writeProfileNetwork → "(allow network*)" when mode != "none"

# tart  (guest hostname Manageds-Virtual-Machine.local, uname Darwin arm64, guest en0=192.168.65.2)
guest default gateway → 192.168.65.1   (host bridge102, appeared on VM boot)
guest curl 192.168.65.1:8802 (0.0.0.0 bind)        → 200 OK
guest curl 192.168.65.1:8804 (gateway-IP bind)     → 200 OK   # host bound 192.168.65.1 directly
guest curl 192.168.65.1:8801 (host loopback bind)  → FAIL (Couldn't connect)   # expected
host route -n get default → 192.168.111.1 (LAN, separate from vmnet)

# apple container
guest default route → default via 192.168.64.1 dev eth0
guest curl 192.168.64.1:8802 (0.0.0.0 bind)        → OK
guest curl 192.168.64.1:8803 (vmnet-IP bind)       → OK   # host bound 192.168.64.1 directly
guest curl 192.168.64.1:8801 (host loopback bind)  → FAIL   # expected
host bridge101 = 192.168.64.1 (shared by buildkit VM @ .19 + probe VM)
```

## Conclusion (feeds 2b-2)

Discovery is a **backend responsibility**: an optional `InjectorReachable` interface returning
`InjectorReach{BindHost, DialHost}`. Linux docker/podman → inspect `.Gateway` (both fields equal);
containerd → CNI gateway; seatbelt → `127.0.0.1` (both equal); **Desktop-class docker (OrbStack /
Docker Desktop) → `DialHost=host.docker.internal`, `BindHost=127.0.0.1`** (the only verified case
where the two genuinely differ — the reason the split exists); **tart → vmnet gateway (guest
default route), both fields equal, read after boot**; **apple → shared vmnet gateway `192.168.64.1`,
both fields equal**. The orchestrator calls it to fill `InjectorSpec.BindHost` + the agent's
`base_url`. Backends that do not implement it fall back to today's direct credential delivery —
no flag-day. Mac note: only OrbStack/Desktop admits a loopback bind; tart/apple require binding the
vmnet gateway IP (guest `127.0.0.1` is the guest's own loopback, verified not to reach the host).
