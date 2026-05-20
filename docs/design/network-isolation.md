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
- **macOS backends** (Seatbelt, Tart). Linux is the target of this design. macOS has its own filtering surface (`pf`) and will be addressed in a follow-up doc once the Linux mechanism is in place.

## Design

### One mechanism: host-side filtering

All network isolation rules live in the **host network namespace**, applied to the host-side interface that the sandbox connects through (a `veth` peer for Docker/Podman, a TAP device for Kata VMs, the runsc-allocated `veth` for gVisor). The in-sandbox kernel is never asked to enforce anything.

This eliminates the entire class of "does the backend support iptables-in-the-sandbox" questions. The host kernel sees every packet entering or leaving the sandbox interface, regardless of what's inside — Docker default runtime, gVisor's userspace netstack, Kata's guest Linux kernel, all the same.

It also strengthens the rogue defense: rules in the host netns are unreachable from inside the sandbox short of a full sandbox escape. A rogue agent that gains root inside the container still cannot flush them.

### Filtering layer: iptables + ipset (Linux)

`iptables -t filter -A FORWARD -i <sandbox-iface>` for outbound packets leaving the sandbox, with an `ipset` per sandbox holding the allowlisted IPs. Mirror rules with `ip6tables` for IPv6. The structure mirrors the current in-sandbox approach but moves it to the host.

Per-sandbox isolation is achieved by:

- A dedicated iptables chain per sandbox (e.g., `YOLOAI_<sandbox-hash>`).
- An ipset per sandbox, named the same way.
- The `FORWARD` rules match the sandbox's host-side interface and jump into the per-sandbox chain.

On sandbox destroy, the chain and ipset are removed. On host reboot, they're recreated at sandbox start (idempotently — `ipset create -exist`).

### Allowlist composition

Already correct in `sandbox/create_prepare.go:646`. The effective allowlist is:

```
effective = agentDef.NetworkAllowlist  ∪  opts.NetworkAllow
```

The per-agent `NetworkAllowlist` is the **bare minimum** required for the agent to function (e.g., `api.anthropic.com` for Claude). The user-supplied `opts.NetworkAllow` adds anything else the user explicitly grants. No change to this composition is needed.

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

Domains are resolved for both `A` and `AAAA` records. The IPv4 set is `hash:net`; the IPv6 set is `hash:net,family=inet6`. Rules are applied via `iptables` and `ip6tables` in parallel. The default-deny `REJECT` exists in both tables.

Disabling IPv6 in the sandbox would be simpler but breaks users on dual-stack hosts (GitHub, most cloud providers, Cloudflare's edge). The cost of parallel rules is modest; the cost of breaking IPv6 is not.

### DNS handling

DNS to the nameservers in the sandbox's `/etc/resolv.conf` is **permitted unconditionally** as part of the bare minimum. Allowlisted domains cannot be reached by name without working DNS, and the alternative — running a host-side resolver that only answers for allowlisted domains — is significant operational complexity for marginal benefit, since DNS tunneling is already out of scope (see Threat Model).

The DNS allowance is added to the rules as: `-A <chain> -d <nameserver> -p udp --dport 53 -j ACCEPT` (and same for TCP), once per nameserver discovered in `/etc/resolv.conf` at sandbox creation.

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
| `iptables -A` for any required rule (loopback, established, DNS, allowlist, final REJECT) fails | Sandbox start | Sandbox start fails; log under `network.iptables_*_failed`. The final REJECT is explicitly checked — silent omission of the deny rule would be a wide-open sandbox. |
| Same as above, for `ip6tables` | Sandbox start | Same — IPv6 rules are not optional. |
| Allowlisted domain resolution fails (A or AAAA both empty for a given name) | Sandbox start | Log under `network.resolve_failed` with the domain name; **continue**. This is the one non-fatal case: an errant domain in the allowlist (typo, transient outage) should not block the sandbox, and the deny posture is still applied. The domain simply has no entries in the ipset. The log makes the symptom diagnosable. |
| `/etc/resolv.conf` empty or unreadable | Sandbox start | Log under `network.no_nameservers` warning that DNS will be blocked; **continue**. Pure-IP allowlists (no domain names) remain usable; name-based allowlists will fail in a visible way. |
| Sandbox destroy can't clean up chain/ipset | Sandbox destroy | Log under `network.cleanup_failed` and continue with destroy. Stale chains accumulate on the host but don't affect any running sandbox. |

Two non-failures explicitly worth calling out as **not silent**:

- The current in-sandbox approach catches `iptables` exit codes nowhere (all calls use `capture_output=True` without `check=True`). On any backend or host where `iptables`/`ipset` fails — missing binary, missing capability, gVisor's incomplete iptables emulation, anything — the entrypoint logs `iptables default-deny applied` and the container starts wide open. That class of bug is what this design eliminates.
- gVisor's netstack does not honor in-sandbox iptables rules. Under the current implementation, `--network-isolated` on `docker-cenhanced` is a silent no-op: rules are "installed" inside the sandbox kernel that gVisor ignores. Under this design, all rules are on the host, so gVisor's netstack is irrelevant — outbound packets leaving the gVisor sandbox traverse the host veth and hit the host iptables rules like any other backend.

## Test Plan

The test suite asserts one specific property per test, runs across every Linux backend without per-backend skips, and would have caught the regressions that motivated this design.

| Test | Asserts |
|---|---|
| `test_egress_deny_ipv4` | curl to a non-allowed IPv4 address fails fast (not timeout). |
| `test_egress_deny_ipv6` | `curl -6` to a non-allowed IPv6 address fails fast. |
| `test_egress_allow_default_port` | `--allow github.com` permits TCP 443 to github; TCP 22 to the same host fails. |
| `test_egress_allow_explicit_port` | `--allow smtp.example.com:587` permits 587, blocks 443. |
| `test_bare_minimum_reachable` | The agent's required LLM endpoint is reachable with no user-added allowlist. |
| `test_dns_works` | DNS resolution to the host's configured resolver succeeds; DNS to an unrelated resolver fails. |
| `test_rogue_cannot_flush` | `yoloai exec <name> -- iptables -F OUTPUT` returns success but has no effect on host-side rules; subsequent egress test still blocked. |
| `test_agent_not_root` | The agent process inside the sandbox runs as a non-root user. |
| `test_creation_fails_loudly` | On a host configured to make iptables unavailable to yoloai, `yoloai new --network-isolated` fails with a specific error rather than creating an unenforced sandbox. |

The current `test_isolation_check` becomes a subset of `test_egress_deny_ipv4` and is retired.

## Implementation Notes

### Interface discovery per backend

The host-side interface that needs filtering differs by backend:

- **Docker**: container's veth peer on `docker0` (or user-defined bridge). Discoverable via `docker inspect` → container PID → `/proc/<pid>/net/igmp` or `nsenter` to read `ip link` index, then matching the host-side veth by index. Alternatively, label the container so the host-side veth name follows a yoloai convention.
- **Podman**: same as Docker.
- **containerd + Kata**: TAP device created by the CNI bridge plugin. Discoverable via the CNI result JSON (currently captured during sandbox setup in `runtime/containerd/cni.go`).
- **containerd + gVisor (docker-cenhanced uses Docker, not containerd)**: same veth discovery as Docker. The gVisor sandbox writes to the same veth that Docker created.

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
- ipset creation (`hash:net` for v4, `hash:net,family=inet6` for v6).
- Rule installation in the per-sandbox chain (loopback, established, DNS to discovered resolvers, allowlist matches, final REJECT).
- The `FORWARD` jump: `iptables -I FORWARD -i <iface> -j YOLOAI_<hash>` (and `-o <iface>` for the return-path jump, if needed for any DNAT scenarios).
- Cleanup on destroy: remove the FORWARD jump first, then flush and delete the chain, then destroy the ipset.

The current in-sandbox `isolate_network` function in `runtime/docker/resources/entrypoint.py` is removed. The Python entrypoint becomes simpler — it no longer needs `CAP_NET_ADMIN`, `iptables`, or `ipset` inside the container at all.

### Capability changes

- `CAP_NET_ADMIN` is **no longer added to the container**. It's required on the *host* by the yoloai process (or by sudo escalation) at sandbox creation. This is a meaningful reduction in container privilege.
- The container image no longer needs `iptables` or `ipset` installed. Profile Dockerfiles can drop those packages.

### Migration

Implementation is gated behind a config flag (`network.host_side: true`) initially, with the in-sandbox approach kept as fallback. Once parity is verified across all Linux backends, the flag is removed and the in-sandbox code is deleted. The in-sandbox failure-loud fix already made to `entrypoint.py` (`NetworkIsolationError`, `_run_strict`) remains useful during the migration window because users on the old path should still get loud failures rather than silent no-ops.

## Open Questions

These need to be resolved before implementation:

1. **Sandbox interface discovery on Docker with user-defined networks.** The default bridge case is straightforward, but if a user configures their profile to attach the sandbox to a custom network, the veth discovery needs to handle that. Likely fine but worth verifying against the existing `runtime/docker/network.go` flow.
2. **CNI return path for Kata.** The Kata TAP device is documented in CNI results, but the exact field varies between CNI plugins. Confirm what `runtime/containerd/cni.go` currently captures and whether it's the right interface for host-side filtering.
3. **Conflict with user iptables setup.** If the user has their own `iptables` rules on the host (firewall, Docker's own NAT chains), yoloai's rules need to coexist. Placement in `FORWARD` with a dedicated chain should be safe, but ordering of `-I FORWARD` vs Docker's `DOCKER-USER` chain needs verification.
4. **Per-agent allowlist correctness audit.** This design assumes each agent's `NetworkAllowlist` in `agent/agent.go` covers exactly what's needed and nothing more. Worth a once-over: is `sentry.io` really required for Claude to function, or is it telemetry that should be opt-in?

## Out of Scope (Future Work)

- **SNI-based filtering.** Would eliminate the IP-rotation problem and allow finer-grained allowlists (e.g., `*.github.com`), but requires either TLS inspection (terminates user privacy) or SNI peek (limited to non-ECH traffic). Not v1.
- **macOS backends (Seatbelt, Tart).** Need a `pf`-based equivalent design. Until then, `--network-isolated` on those backends returns `ErrHostSideFilterUnsupported` at creation.
- **Per-domain accounting/audit log.** Useful for forensic review but adds noise. Defer until requested.
- **Dynamic re-resolution daemon.** See "Resolution timing" above. Add only if real users hit the problem.
- **IPv6 nameserver support.** If `/etc/resolv.conf` lists IPv6 nameservers, current logic should still allow them via `ip6tables`. Implementation needs to handle both address families in the resolv.conf parse.
