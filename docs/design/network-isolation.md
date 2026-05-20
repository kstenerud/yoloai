> **Design documents:** [Overview](README.md) | [Commands](commands.md) | [Config](config.md) | [Setup](setup.md) | [Security](security.md) | [Environments](environments.md) | [Research](../dev/RESEARCH.md) | [research/](../dev/research/)

# Network Isolation

**Status:** Design Proposal. Supersedes the in-container iptables approach currently described in [`security.md`](security.md) and implemented in `runtime/docker/resources/entrypoint.py`.

## Goals

Define what `--network-isolated` means in yoloAI, what threats it defends against, and what guarantees it offers across all supported backends. The current implementation works well on plain Docker and Podman but silently no-ops on gVisor and was never enabled on Kata. This design unifies enforcement under a single mechanism and makes failure modes loud rather than silent.

## Threat Model

yoloAI's network isolation defends against two classes of agent misbehavior. They are described separately because they have different bypass profiles, but they share a defense surface.

### Errant agent (primary)

An agent that has lost track of its task, hallucinated, or misinterpreted instructions. Errant agents are not adversarial — they're not actively trying to bypass defenses, they just blunder into them. Typical errant behaviors that network isolation should catch:

- Running `curl` against a hallucinated URL.
- Installing an npm/pip package whose postinstall script phones home.
- Pushing code to a misremembered git remote.
- Calling an API the user never authorized.

For this threat, **any** filter that says "no" to the right destinations is sufficient. The agent reads the error, backs off, tries something else. The bar is correctness, not bypass resistance.

### Rogue agent (secondary)

An agent that is actively trying to exfiltrate data or contact an unauthorized controller — either because of a prompt injection in the materials it's reading, or (much more rarely) because the model itself behaves adversarially. For this threat the filter must survive an actor that's looking for the door. Two important properties:

- The rules must be **outside the agent's reach**. If the agent (or a setup command, or an installed dependency) can gain root inside the sandbox, in-sandbox iptables rules can be flushed and the filter is gone. Host-side rules cannot be reached without breaking out of the sandbox itself.
- The rules must not have **silent partial-application** modes. A rogue agent benefits from any case where the user *believes* isolation is enforced but it isn't.

The errant/rogue boundary is fuzzy in practice: a sufficiently confused errant agent can behave almost like a rogue one, and a deliberately constrained rogue agent can be hard to distinguish from an errant one. The design therefore treats errant as the primary threat while ensuring rogue defenses degrade gracefully rather than catastrophically.

### Out of scope

These threats are acknowledged but not addressed by this design:

- **Covert channels through the agent's permitted LLM connection.** A rogue agent can embed arbitrary data in its prompts to the LLM service. This connection is necessarily allowed (the agent cannot function without it), is encrypted, and is high-bandwidth. Any defense against this requires inspecting LLM traffic itself, which is a separate problem with its own design.
- **DNS tunneling.** Real but low-bandwidth, and entirely dominated by the LLM-channel exfil path above. Defending against DNS while leaving the LLM channel open would be security theater.
- **Sandbox escape via kernel/runtime exploit.** This is gVisor's and Kata's domain. Network isolation cannot help if the agent breaks out of the sandbox entirely. See [`security.md`](security.md) and `docs/dev/research/security.md`.
- **Side channels** (timing, cache, power) and **traffic-shape inference**. Out of scope for any agent-runner product at this layer.
- **macOS-native backends** (Seatbelt, Tart). These have their own filtering surface (`pf`) and will be addressed in a follow-up doc once the Linux mechanism is in place. macOS *Docker Desktop* (Docker backend on a macOS host) and WSL2-hosted Docker are not out of scope — they're treated separately in "Cross-platform support" below, because they share the Linux netfilter machinery but not the host topology.

## Design

### One mechanism: host-side filtering

All network isolation rules live in the **host network namespace**, applied to the host-side interface that the sandbox connects through (a `veth` peer for Docker/Podman, a TAP device for Kata VMs; gVisor inherits the netns Docker created and reuses the same host-side `veth`). The in-sandbox kernel is never asked to enforce anything.

This eliminates the entire class of "does the backend support iptables-in-the-sandbox" questions. The host kernel sees every packet entering or leaving the sandbox interface, regardless of what's inside — Docker default runtime, gVisor's userspace netstack, Kata's guest Linux kernel, all the same.

It also strengthens the rogue defense: rules in the host netns are unreachable from inside the sandbox short of a full sandbox escape. A rogue agent that gains root inside the container still cannot flush them.

### The effective filter is a static set of IPs

The load-bearing property of this design — important enough to call out before the mechanics — is that **domain names appear only at sandbox creation**. At creation, allowlisted names are resolved to IPs and those IPs are written to an ipset. At runtime, every packet leaving the sandbox is matched against that ipset by destination IP. The agent's own DNS lookups don't drive the filter; only the IP table does.

This is what neutralizes rogue-DNS redirection. A poisoned resolver telling the agent "`google.com = 6.6.6.6`" cannot grant egress, because 6.6.6.6 was never written to the ipset. The same property makes the "DNS to host nameservers is allowed" concession (see below) defensible: DNS responses can lie, but they can't change which IPs the host kernel forwards to.

### Filtering layer: iptables + ipset (Linux)

`iptables -t filter -A FORWARD -i <sandbox-iface>` for outbound packets leaving the sandbox, with an `ipset` per sandbox holding the allowlisted IPs. Mirror rules with `ip6tables` for IPv6. The structure mirrors the current in-sandbox approach but moves it to the host.

Per-sandbox isolation is achieved by:

- A dedicated iptables chain per sandbox.
- An ipset per sandbox, named the same way.
- A pair of `FORWARD` jumps for the sandbox's host-side interface: one for the egress direction (`-i <iface>`, the outbound path being filtered), and one for the return-path (`-o <iface>`, where the `-m state --state ESTABLISHED,RELATED -j ACCEPT` rule lives so replies to the agent's permitted connections come back).

**Chain and set naming.** iptables chain names are capped at `XT_FUNCTION_MAXNAMELEN = 30` characters (29 usable + NUL). With a `YOLOAI_` prefix (7 chars) that leaves 22 for the identifier. Use a SHA-256 hash of the sandbox name, truncated hex to 20 chars: `YOLOAI_<sha256(name)[0:20]>`. 80 bits of name space is more than enough to avoid collisions across the 10–15 sandboxes a host runs concurrently. ipset names share the same cap and use the same string.

On sandbox destroy, the FORWARD jumps are removed first, then the chain is flushed and deleted, then the ipset is destroyed. On host reboot, they're recreated at sandbox start (idempotently — `ipset create -exist`, and `iptables -N` is wrapped to ignore "chain already exists").

**Orphan reaping.** Ungraceful exits (host kill, OOM, crash) leave stale chains and sets behind. Two mitigations: (1) on `yoloai` startup, compare host iptables/ipset state to the known-sandbox list in `~/.yoloai/sandboxes/` and remove anything prefixed `YOLOAI_` that doesn't correspond to a known sandbox; (2) `yoloai prune` performs the same sweep on demand. Without this, the FORWARD chain grows monotonically on long-running hosts.

### Allowlist composition

The current composition lives in `sandbox/create_prepare.go:676-677` and shapes the effective allowlist as:

```
effective = agentDef.NetworkAllowlist  ∪  opts.NetworkAllow
```

This composition stands, but the per-agent `NetworkAllowlist` itself needs splitting. The current Claude entry in `agent/agent.go:154` is:

```go
NetworkAllowlist: []string{"api.anthropic.com", "claude.ai", "platform.claude.com",
                            "statsig.anthropic.com", "sentry.io"},
```

`statsig.anthropic.com` (feature flagging) and `sentry.io` (error reporting) are **telemetry**, not load-bearing operational endpoints. Mixing them into a "bare minimum" list misrepresents what the agent needs to function and gives users no way to deny them without also denying the LLM endpoint.

The agent definition splits into two fields:

```go
type AgentDef struct {
    NetworkAllowlist  []string  // load-bearing: agent will not function without these
    TelemetryAllowlist []string // opt-in: telemetry/crash/feature-flag endpoints
    // ...
}
```

And composition becomes:

```
effective = agentDef.NetworkAllowlist
          ∪ (opts.AllowTelemetry ? agentDef.TelemetryAllowlist : ∅)
          ∪ opts.NetworkAllow
```

`opts.AllowTelemetry` is a new `--allow-telemetry` flag (and matching config key) that defaults to **true** to preserve existing behavior on upgrade. Security-conscious users set it to `false` to strip telemetry from the allowlist; the agent still runs because only the load-bearing list is included by default.

For Claude specifically:

- `NetworkAllowlist`: `api.anthropic.com`, `claude.ai`, `platform.claude.com`. These cover the LLM endpoint and OAuth/login paths.
- `TelemetryAllowlist`: `statsig.anthropic.com`, `sentry.io`.

Other agents are audited similarly in the implementation step; the principle is that anything whose unavailability produces a *degraded but functional* agent goes in `TelemetryAllowlist`, not `NetworkAllowlist`.

### Granularity: host+port

Allowlist entries are `host[:port]` tuples. Port defaults to **443** if unspecified, since the bare-minimum entries are all HTTPS and that's the overwhelming common case for user-added entries too.

```
api.anthropic.com         → api.anthropic.com:443    (default port)
smtp.example.com:587      → smtp.example.com:587     (explicit port)
ssh.example.com:22        → ssh.example.com:22       (explicit port)
```

This matters most for the rogue case: an `api.anthropic.com` allowance must not also permit hypothetical egress to that host on other ports. It also matters for the security-conscious user, who expects the number of open ports to be small and well-known.

Parsing: split on the last `:`. If the right side is a positive integer ≤ 65535, treat as port; otherwise treat the whole string as host (so `[2001:db8::1]` IPv6 literals don't trip the parser — bracketed form required for IPv6+port).

### IPv4 and IPv6 parity

Domains are resolved for both `A` and `AAAA` records. The IPv4 set is `hash:ip`; the IPv6 set is `hash:ip,family=inet6`. (We hold single IPs, not CIDRs — `hash:ip` reflects that. Switch to `hash:net` if CIDR allowlist entries are ever introduced.) Rules are applied via `iptables` and `ip6tables` in parallel. The default-deny `REJECT` exists in both tables.

Disabling IPv6 in the sandbox would be simpler but breaks users on dual-stack hosts (GitHub, most cloud providers, Cloudflare's edge). The cost of parallel rules is modest; the cost of breaking IPv6 is not.

### DNS handling

DNS to the nameservers in the sandbox's `/etc/resolv.conf` is **permitted unconditionally** as part of the bare minimum. Allowlisted domains cannot be reached by name without working DNS, and the alternative — running a host-side resolver that only answers for allowlisted domains — is significant operational complexity for marginal benefit, since DNS tunneling is already out of scope (see Threat Model).

The DNS allowance is added to the rules as: `-A <chain> -d <nameserver> -p udp --dport 53 -j ACCEPT` (and same for TCP), once per nameserver discovered in `/etc/resolv.conf` at sandbox creation.

**Resolver mismatch is a known gotcha.** Pre-resolution at creation uses the *host's* resolver; the agent's runtime DNS uses the *sandbox's* `/etc/resolv.conf`. If the two return different IPs for the same name (split-horizon DNS, geo-targeted endpoints, CDN edge variance), the runtime lookup will return an IP not in the ipset and the connection will fail despite the domain being "allowed". To avoid this, resolution at creation time runs inside the sandbox's netns (via `nsenter` once the sandbox is up far enough to read its `resolv.conf`, or by sharing the resolver list explicitly between creation-time and runtime). Implementation MUST take this path; resolving from the host without alignment is a footgun that produces silent connectivity failures.

### Resolution timing: at creation, no daemon

Allowlisted domains are resolved to IPs **once, at sandbox creation**. The resulting IPs are populated into the ipset and never refreshed automatically.

The alternative — a host-side daemon that periodically re-resolves and updates ipsets — was considered and rejected:

- Users routinely run 10–15 sandboxes per host. A per-sandbox daemon multiplies that, and a shared daemon becomes a critical service with its own lifecycle, recovery, and failure modes.
- Major LLM endpoints (Anthropic, OpenAI, Google) use stable Anycast IPs. CDN-fronted endpoints can churn, but in practice this is rare on the timescales of an agent session.
- The escape hatch is "restart the sandbox," which re-resolves cleanly.

If users hit IP-rotation problems often enough to warrant action, a `yoloai sandbox refresh-net <name>` command can be added later as a strictly additive feature.

### Failure modes — loud by default

This is the load-bearing property of the design: **if the rules cannot be installed correctly, the sandbox does not start.** Silent partial-application is the worst possible failure mode — the user believes the agent is contained when it is not. Every failure path below produces a structured log entry and a non-zero exit.

| Failure | Detection point | Behavior |
|---|---|---|
| Host doesn't support iptables/ipset (binaries missing, no `CAP_NET_ADMIN` for yoloai itself) | Sandbox creation, before any rules attempted | Sandbox creation fails with a specific error naming the missing capability or binary. No partial state. |
| Per-sandbox chain creation fails | Sandbox start | Sandbox start fails; structured log under `network.chain_create_failed`; any partial rules are removed. |
| `ipset create`/`add` fails | Sandbox start | Sandbox start fails; log under `network.ipset_*_failed`. |
| `iptables -A` for any required rule (DNS, allowlist, final REJECT, return-path ESTABLISHED) fails | Sandbox start | Sandbox start fails; log under `network.iptables_*_failed`. The final REJECT is explicitly checked — silent omission of the deny rule would be a wide-open sandbox. |
| Same as above, for `ip6tables` | Sandbox start | Same — IPv6 rules are not optional. |
| Allowlisted domain resolution fails (A or AAAA both empty for a given name) | Sandbox start | Log under `network.resolve_failed` with the domain name; **continue**. This is the one non-fatal case: an errant domain in the allowlist (typo, transient outage) should not block the sandbox, and the deny posture is still applied. The domain simply has no entries in the ipset. The log makes the symptom diagnosable. |
| `/etc/resolv.conf` empty or unreadable | Sandbox start | Log under `network.no_nameservers` warning that DNS will be blocked; **continue**. Pure-IP allowlists (no domain names) remain usable; name-based allowlists will fail in a visible way. |
| Sandbox destroy can't clean up chain/ipset | Sandbox destroy | Log under `network.cleanup_failed` and continue with destroy. Stale chains accumulate on the host but don't affect any running sandbox. |

Two non-failures explicitly worth calling out as **not silent**:

- Until commit `bc512b9`, the in-sandbox approach caught `iptables` exit codes nowhere (all calls used `capture_output=True` without `check=True`). On any backend or host where `iptables`/`ipset` failed — missing binary, missing capability, gVisor's incomplete iptables emulation, anything — the entrypoint logged `iptables default-deny applied` and the container started wide open. The `_run_strict` wrapper in `entrypoint.py` now raises `NetworkIsolationError` on any failure, but the *category* of bug — silent partial application of in-sandbox rules — is exactly what this design eliminates by moving rules to the host.
- gVisor's netstack does not honor in-sandbox iptables rules. Until the per-isolation-mode check landed in `runtime/isolation.go` (`IsolationEnforcesInSandboxIptables`), `--network-isolated` on `docker-cenhanced` was a silent no-op: rules were "installed" inside the sandbox kernel that gVisor ignores. That check now rejects the combination at sandbox creation. Under this design, all rules live on the host, so gVisor's netstack is irrelevant — outbound packets leaving the gVisor sandbox traverse the host veth and hit the host iptables rules like any other backend, and the rejection check can be removed.

### Cross-platform support

The host-side iptables/ipset machinery exists in a Linux kernel. Three deployment shapes need to be distinguished:

**Linux host, native Docker / containerd / Podman.** The yoloAI binary runs on the same kernel that holds the veth and the iptables chain. This is the design's primary target — everything in this document applies directly.

**macOS host, Docker Desktop.** Docker Desktop runs Docker inside a LinuxKit VM; the veth peers live inside that VM, not on the macOS host. The macOS-side `pf` doesn't see them. Two implementation options:

1. *Reject at sandbox creation.* If `runtime.Runtime.SandboxInterface(...)` detects it's running on a Docker-Desktop daemon (queryable via `docker info`'s `OperatingSystem` field), return `runtime.ErrHostSideFilterUnsupported` and fail `--network-isolated` loudly. The user gets a specific error pointing them at Tart (with a `pf`-based design once that's written) or a Linux host.
2. *Tunnel rule installation into the VM.* Docker Desktop exposes a shell into the LinuxKit VM via `docker run --rm --privileged --pid=host justincormack/nsenter1`. We could shell into the VM and install rules there. This is fragile, undocumented, and version-specific, so v1 picks option 1.

**WSL2 host, Docker Desktop or WSL-distro Docker.** Two sub-cases:
- *Docker Desktop's WSL2 backend* uses a dedicated `docker-desktop` distro for the daemon, identical in topology to the macOS case. Same option-1 treatment: reject and tell the user.
- *Docker running inside the user's own WSL distro* (Ubuntu or similar with `dockerd` installed directly): the veth lives in that distro's namespace and yoloAI runs there too. This is functionally equivalent to native Linux, except (a) the user may need to install `iptables`/`ipset` manually, and (b) `CAP_NET_ADMIN` is available without ceremony because they're already inside their own user namespace. The design works here unchanged, modulo a clear error message if the binaries are missing.

The Linux backend matrix in the Test Plan below is what CI must run. macOS Docker Desktop and Docker-Desktop-on-WSL2 are tested manually until rejection-at-creation is verified to produce a clear error.

## Test Plan

The test suite asserts one specific property per test and runs across every Linux backend in scope without per-backend skips. Target backends:

- `docker` (default Linux runtime)
- `docker-privileged` (Docker with `--privileged`, for users who need it)
- `docker-cenhanced` (Docker + gVisor / runsc)
- `containerd-vm` (containerd + Kata)
- `containerd-vmenhanced` (containerd + Kata + Firecracker)

macOS Docker Desktop and WSL2-hosted Docker are explicitly out of scope here (see "Cross-platform support" below); the Linux backend matrix above is what CI must run.

| Test | Asserts |
|---|---|
| `test_egress_deny_ipv4` | curl to a non-allowed IPv4 address fails fast (not timeout). |
| `test_egress_deny_ipv6` | `curl -6` to a non-allowed IPv6 address fails fast. |
| `test_egress_allow_default_port` | `--allow github.com` permits TCP 443 to github; TCP 22 to the same host fails. |
| `test_egress_allow_explicit_port` | `--allow smtp.example.com:587` permits 587, blocks 443. |
| `test_bare_minimum_reachable` | The agent's required LLM endpoint is reachable with no user-added allowlist. |
| `test_dns_works` | DNS resolution to the host's configured resolver succeeds; DNS to an unrelated resolver fails. |
| `test_rogue_dns_redirect` | A poisoned DNS response that maps `github.com` to an unrelated IP does not grant egress — the static-IP property holds. |
| `test_rogue_cannot_flush` | The sandbox is launched with `--cap-add NET_ADMIN` (overriding the default capability drop) so the agent can actually run iptables. `yoloai exec <name> -- iptables -F` succeeds, but the next egress test to a non-allowed IP still fails — host-side rules are unreachable from inside. |
| `test_capability_dropped_by_default` | Without the test override above, `iptables` inside the sandbox fails with EPERM — `CAP_NET_ADMIN` is not in the container's bounding set. |
| `test_agent_not_root` | The agent process inside the sandbox runs as a non-root user. |
| `test_creation_fails_loudly` | On a host configured to make iptables unavailable to yoloai, `yoloai new --network-isolated` fails with a specific error rather than creating an unenforced sandbox. |
| `test_orphan_reaped_on_startup` | A stale `YOLOAI_<hash>` chain left over from a killed sandbox is removed when `yoloai` next starts (or on `yoloai prune`). |

The current `test_isolation_check` becomes a subset of `test_egress_deny_ipv4` and is retired.

## Implementation Notes

### Interface discovery per backend

The host-side interface that needs filtering differs by backend:

- **Docker**: container's veth peer on `docker0` (or user-defined bridge). Discoverable via `docker inspect` → container PID → read `/sys/class/net/eth0/iflink` inside the netns (`nsenter -t <pid> -n cat /sys/class/net/eth0/iflink`) to get the host-side ifindex, then match against `/sys/class/net/*/ifindex` on the host to find the veth name.
- **Podman**: same as Docker.
- **containerd + Kata**: TAP device created by the CNI bridge plugin. Discoverable via the CNI result JSON (currently captured during sandbox setup in `runtime/containerd/cni.go`).
- **Docker + gVisor** (`docker-cenhanced`): same veth discovery as Docker. gVisor inherits the netns Docker built and writes to the same host-side veth that Docker created.

The runtime backend interface gains a single method:

```go
type Runtime interface {
    // existing methods ...
    SandboxInterface(ctx context.Context, name string) (string, error)
}
```

This returns the host-side interface name for a running sandbox. Backends that don't support host-side filtering (Seatbelt, Tart on macOS) return `runtime.ErrHostSideFilterUnsupported`, and `--network-isolated` on those backends fails at sandbox creation with that error.

### Rule application

A new package `runtime/netfilter/` (Linux-only build tag) encapsulates:

- Per-sandbox chain creation (`iptables -t filter -N YOLOAI_<hash>` and matching ip6tables).
- ipset creation (`hash:ip` for v4, `hash:ip,family=inet6` for v6).
- Rule installation in the per-sandbox egress chain, in order:
  1. DNS to each discovered resolver: `-d <ns> -p udp --dport 53 -j ACCEPT` and the same for TCP.
  2. Allowlist match: `-p tcp -m set --match-set YOLOAI_<hash> dst -m multiport --dports <ports> -j ACCEPT` (one rule per distinct port in the allowlist; the ipset gives the host set, the multiport gives the port set).
  3. Final `-j REJECT --reject-with icmp-admin-prohibited` (and `icmp6-adm-prohibited` for IPv6).
  Loopback is *not* in this list — loopback traffic never traverses FORWARD; it stays inside the sandbox's own netns.
- The two `FORWARD` jumps:
  - Egress: `iptables -I FORWARD -i <iface> -j YOLOAI_<hash>` — feeds packets *leaving* the sandbox into the per-sandbox chain above.
  - Return path: `iptables -I FORWARD -o <iface> -m state --state ESTABLISHED,RELATED -j ACCEPT` — allows replies to already-permitted connections back. This explicitly does NOT live inside the per-sandbox chain; if it did, a new inbound flow not initiated by the agent could be permitted just because a stale conntrack entry exists.
- Cleanup on destroy: remove both FORWARD jumps first, then flush and delete the chain, then destroy the ipset.

The current in-sandbox `isolate_network` function in `runtime/docker/resources/entrypoint.py` is removed. The Python entrypoint becomes simpler — it no longer needs `CAP_NET_ADMIN`, `iptables`, or `ipset` inside the container at all.

### Capability changes

- `CAP_NET_ADMIN` is **no longer added to the container**. It's required on the *host* by the yoloai process (or by sudo escalation) at sandbox creation. This is a meaningful reduction in container privilege.
- The container image no longer needs `iptables` or `ipset` installed. Profile Dockerfiles can drop those packages.

### Migration

No config flag. yoloAI is in public beta (per `CLAUDE.md`); breaking changes are tracked in `docs/BREAKING-CHANGES.md` rather than gated behind opt-in flags. Maintaining two parallel network-isolation paths — the in-sandbox approach and the host-side approach — would double the surface area and reintroduce the silent-partial-application risk the design is built to eliminate.

The cutover is a single change set per release:

1. Land `runtime/netfilter/` and the new `Runtime.SandboxInterface(...)` method on every Linux backend.
2. Switch `--network-isolated` to invoke the host-side path.
3. Remove `isolate_network` and the `CAP_NET_ADMIN`/`iptables`/`ipset` install from `entrypoint.py` and the profile Dockerfiles.
4. Remove the `IsolationEnforcesInSandboxIptables` rejection from `runtime/isolation.go` and `sandbox/create_instance.go` — `docker-cenhanced` now works with `--network-isolated` like every other backend.
5. Add a `BREAKING-CHANGES.md` entry describing the moved enforcement layer and the dropped container capability.

The `IsolationEnforcesInSandboxIptables` check and the `_run_strict` wrapper in `entrypoint.py` were the *interim* fix for the silent-no-op bug. They remain valuable until step 4 lands, at which point both are deleted as part of the same change.

## Open Questions

These need to be resolved before implementation:

1. **Sandbox interface discovery on Docker with user-defined networks.** The default bridge case is straightforward, but if a user configures their profile to attach the sandbox to a custom network, the veth discovery needs to handle that. Likely fine but worth verifying against the existing `runtime/docker/network.go` flow.
2. **CNI return path for Kata.** The Kata TAP device is documented in CNI results, but the exact field varies between CNI plugins. Confirm what `runtime/containerd/cni.go` currently captures and whether it's the right interface for host-side filtering.
3. **Conflict with user iptables setup.** If the user has their own `iptables` rules on the host (firewall, Docker's own NAT chains), yoloai's rules need to coexist. Placement in `FORWARD` with a dedicated chain should be safe, but ordering of `-I FORWARD` vs Docker's `DOCKER-USER` chain needs verification.

## Out of Scope (Future Work)

- **SNI-based filtering.** Would eliminate the IP-rotation problem and allow finer-grained allowlists (e.g., `*.github.com`), but requires either TLS inspection (terminates user privacy) or SNI peek (limited to non-ECH traffic). Not v1.
- **macOS backends (Seatbelt, Tart).** Need a `pf`-based equivalent design. Until then, `--network-isolated` on those backends returns `ErrHostSideFilterUnsupported` at creation.
- **Per-domain accounting/audit log.** Useful for forensic review but adds noise. Defer until requested.
- **Dynamic re-resolution daemon.** See "Resolution timing" above. Add only if real users hit the problem.
- **IPv6 nameserver support.** If `/etc/resolv.conf` lists IPv6 nameservers, current logic should still allow them via `ip6tables`. Implementation needs to handle both address families in the resolv.conf parse.
