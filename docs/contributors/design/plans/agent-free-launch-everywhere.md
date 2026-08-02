> **ABOUTME:** Bring the D88 agent-free launch (box up on a neutral holder, host launches the
> agent over it) to every backend that can have it, and record hard evidence for every backend
> that cannot. The enabler for out-of-guest network isolation.

# Plan: agent-free launch on every backend, or evidence why not

- **Status:** PLANNED — researched 2026-08-02 against the code; one item verified by experiment on
  real hardware. No production code written.
- **Depends on:** —
- **Rides:** **any.** Nothing here is a break; the capability is additive per backend.

## Why this matters beyond tidiness

Agent-free launch is the only model in which **the host chooses when the agent starts**. Three
things follow from that moment existing, and none of them is available without it:

- **The tamper-resistant firewall.** `UsesSidecarFirewall` requires it explicitly — the box comes up
  on a neutral holder, so the allowlist can be installed from outside before any agent egress.
  [tamper-resistant-network-isolation.md](tamper-resistant-network-isolation.md) is the consumer.
- **Secrets never touching the container filesystem.** Agent-free passes them in the launched
  process's env; the legacy weld stages them to host files bind-mounted at `/run/secrets`.
- **Any future "wait for X before the agent runs"** — the network-readiness question that started
  this research is one instance, and on a weld backend there is no seam to put it in.

## The matrix, from the code

| Backend | `ProcessLauncher` | Opts in | Status |
| --- | --- | --- | --- |
| docker, runc isolation | yes | **yes** | Shipped. The only one. |
| docker, gVisor | yes | **excluded** | **Exclusion is unnecessary — verified.** [DF171](../findings-unresolved.md) |
| podman | **inherited** (embeds `*docker.Runtime`) | no | Untested. Cheapest unknown. |
| containerd / Kata | no | no | Needs a `Launch`; has `Exec`. Model differs — see below. |
| apple | no | no | Needs a `Launch`; has `Exec`. |
| tart | no | no | Needs a `Launch`; has `Exec`. |
| seatbelt | n/a | n/a | **Already has the property by construction** — see below. |

**Seatbelt is not a gap and should not be counted as one.** Its keep-alive model is
`KeepAliveHostKeepAlive`: there is no container or VM, the sandbox *is* a host process group, and
the host spawns the agent itself. `runtime.go` says it directly — "there is no 'inside' to Launch
into in the container/VM sense". The host already owns the launch moment, which is the whole
property agent-free exists to obtain. Any work item for seatbelt here would be a mechanism in
search of a gap.

## Ordered by evidence, cheapest first

**1. gVisor — remove the exclusion. Verified, not theorised.**

`SupportsAgentFreeLaunch` excludes container-enhanced because `docker exec --user <name>` resolves
the username against the image's *original* `/etc/passwd`, so the launched process runs as the stale
image UID and its first write `EACCES`es silently. True — and `store.ContainerUser` has returned the
**numeric host UID** for that exact defect since 2026-03-18, three months before the exclusion was
written. The launch path never adopted it.

Verified 2026-08-02 on this host's `runsc`: with the exclusion lifted and the launch user made
numeric, a real `--isolation container-enhanced` sandbox took the agent-free path
(`entrypoint.keepalive_only`), wrote `.substrate-ready`, launched the session-runner **as the host
user**, ran the agent, and an agent write reached `yoloai diff`. Full detail in
[DF171](../findings-unresolved.md).

**The fix is not the experiment's two lines.** Route the launch user through the same decision
`ContainerUser` already makes rather than adding a third spelling of it; that function also handles
`UsernsMode == "keep-id"` (rootless podman), which the launch path does not consider and which
matters the moment item 2 lands.

**2. podman — determine, do not assume.** It embeds `*docker.Runtime`, so `Launch` is promoted and
`NetnsSidecarRunnerOf` should already succeed; the opt-in is one field. But an earlier attempt at
exactly this flip was parked, and the plan that superseded it declared the flip "moot" — for
*brokering*, which was solved another way. That says nothing about the launch path, and the two
questions have been conflated once already. The unknowns are concrete and testable on this host:
does the promoted `Launch` work through podman's docker-compat exec API, and does the uid mapping
land correctly under **rootless** podman, where the container user is a host subuid rather than a
remap. Run it before designing anything.

**3. containerd / Kata — real design, not a capability bit.** It has `Exec`, so a `Launch` is
implementable. What is not transferable is the holder: docker's neutral box is `sleep infinity` under
`KeepAliveContainerInit`, while containerd is `KeepAliveGuestOSInit` — a **full systemd guest**. "Come
up neutral" has to be defined against systemd rather than borrowed, and the readiness signal has to
mean something in a VM whose init is not ours. This is the item most likely to be wrongly costed by
analogy with docker, and the podman flip is the precedent for that mistake.

**4. apple and tart — same shape as 3, on macOS.** Both have `Exec` and both are
`KeepAliveGuestOSInit`. Neither can be evaluated from this host at all, so they are macOS-agent work
and should not be scoped from here beyond noting that they are open.

## What "done" means

Every backend either has the capability, or has a written mechanism-level reason it cannot, in this
file, of the kind item 1 turned out **not** to be. The gVisor exclusion is the cautionary case: it
carried a real reason, in a docstring, that a reader would reasonably accept — and it was still wrong,
because the reason had been solved elsewhere and never applied here. **A stated blocker is a
hypothesis until someone tries it.**

## Related

- [DF171](../findings-unresolved.md) — the gVisor exclusion, with the experiment.
- [tamper-resistant-network-isolation.md](tamper-resistant-network-isolation.md) — the consumer;
  its remaining scope is gated on this.
- [D88](../../decisions/working-notes.md) — the keepalive-holder + Launch bring-up.
