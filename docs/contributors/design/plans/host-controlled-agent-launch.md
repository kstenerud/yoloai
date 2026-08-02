> **ABOUTME:** The property "yoloAI decides when the agent starts" on every backend, behind one
> interface, by whichever mechanism each backend can actually offer. The enabler for enforcing a
> network allowlist the agent cannot tamper with.

# Plan: yoloAI decides when the agent starts

- **Status:** PLANNED — researched 2026-08-02 against the code; one item verified by experiment on
  real hardware. No production code written.
- **Depends on:** —
- **Rides:** **any.** Additive per backend; nothing here withdraws a promise.

## The property, stated before any mechanism

**yoloAI, not the guest, decides the moment the agent process begins.** That is the requirement.
Everything below is implementation, and backends may satisfy it differently as long as they satisfy
it observably.

Why it is worth having, in order of weight:

1. **It is a precondition for enforcing the network allowlist outside the agent's reach** — the
   allowlist must be installed *before* the agent can emit a packet, which requires a moment between
   "environment exists" and "agent runs" that the host owns.
   [tamper-resistant-network-isolation.md](tamper-resistant-network-isolation.md) is the consumer.
2. **It lets yoloAI wait for the network to stabilize.** Today, on every weld backend, the agent's
   first API call races the network warm-up and can lose — evidenced by DF9's preserved snapshot,
   where the agent sat retrying `FailedToOpenSocket` while the host's own probe was still timing out.
3. **It keeps secrets out of the container filesystem** where the mechanism allows env delivery.

## Two mechanisms satisfy it, and the cheap one is nearly universal

An earlier draft of this plan assumed one mechanism — the D88 keepalive-holder plus a host-side
`ProcessLauncher` — and therefore concluded that four backends needed a port of it. That framing was
mechanism-first and overpriced the work.

**Mechanism A — host launches (`ProcessLauncher`).** The box comes up on a neutral holder; the host
`exec`s the agent into it when ready. The strongest form: there is no agent process at all until the
host makes one, and secrets can travel in the launched process's environment rather than through the
filesystem. Requires a launcher and a holder concept that fits the backend's init model.

**Mechanism B — guest waits (host-written barrier).** The guest's own setup script blocks on a
marker the host writes when it is satisfied, then execs the agent. Weaker in one specific way — the
guest *process tree* is up, so a compromised image could ignore the barrier — but it delivers the
property against the actual threat (an agent that starts before the network or the firewall is
ready), needs no launcher, and works on **any backend that shares a filesystem with the host**,
which is all of them.

The repo already relies on B without naming it as general: the tamper-resistance plan's deferred
legacy path is exactly "the entrypoint must block on a *firewall ready* marker the host writes after
the sidecar succeeds". Generalising that barrier is the cheapest route to the property everywhere,
and it composes with A rather than competing — a backend can gain B now and A later.

## One interface, so "different means" stays safe

The runtime layer should express the **property**, not the mechanism:

- A capability the backend declares — *the host controls agent launch* — with the mechanism behind
  it, replacing today's `Capabilities.AgentFreeLaunch` (which names a mechanism) and the
  `usesAgentFreeLaunch` predicate that reads it.
- Callers ask the property. `UsesSidecarFirewall` currently asks "is this agent-free", which is the
  wrong question — it needs "is there a moment before the agent runs", and mechanism B answers yes.

**The interface that actually protects us is the conformance case, not the Go type.** The tiering
work just demonstrated the shape: `assertSandboxTiers` asserts its invariant *from inside the guest*,
one assertion serving backends that reach it by different mechanisms, with a mandatory stated reason
for any backend that skips. The same applies here, and it is what makes divergent mechanisms
acceptable:

- **Assert the property:** with a host-side barrier held closed, the agent has not started; once
  released, it starts.
- **Any backend that cannot must say why in its skip reason** — the rule that stopped the tier suite
  from passing vacuously.

Note the trap that suite already paid for ([A22](../../agent-failures.md)): a negative assertion —
"the agent has not started" — passes for every wrong reason, including the command failing to run at
all. It needs the positive control beside it: release the barrier and require the agent to actually
start.

## Per-backend, from the code

| Backend | Mechanism A | Mechanism B | Status |
| --- | --- | --- | --- |
| docker, runc | **shipped** | available | done |
| docker, gVisor | **exclusion unnecessary — verified** | available | [DF171](../findings-unresolved.md) |
| podman | inherited, untested | available | one experiment |
| containerd / Kata | needs design (systemd guest) | **available — shared sandbox dir** | B is the cheap path |
| apple | needs design | **likely** | macOS research |
| tart | needs design | **likely** | macOS research |
| seatbelt | n/a | n/a | **already has the property** |

**Seatbelt already satisfies it and is not a gap.** Its keep-alive model is `KeepAliveHostKeepAlive`
— no container or VM; the sandbox is a host process group the host spawns. `runtime.go` says there is
"no 'inside' to Launch into". The host owns the launch moment by construction. It needs an interface
declaration, not an implementation.

**gVisor is one field plus a shared decision.** `SupportsAgentFreeLaunch` excludes it because
`docker exec --user <name>` resolves the username against the image's stale `/etc/passwd`. Real —
and `store.ContainerUser` has returned the **numeric host UID** for exactly that defect since
2026-03-18, three months before the exclusion was written. Verified 2026-08-02 on this host's
`runsc`: with the exclusion lifted and the launch user made numeric, a real container-enhanced
sandbox took the agent-free path, launched the session-runner as the host user, ran the agent, and
an agent write reached `yoloai diff`. Route the launch user through `ContainerUser`'s decision rather
than adding a third spelling; it also handles `UsernsMode == "keep-id"`, which the launch path
ignores today.

**podman: determine, do not assume.** It embeds `*docker.Runtime`, so `Launch` is promoted and the
opt-in is one field — but an earlier attempt at this exact flip was parked, and the plan that
superseded it called the flip "moot" *for brokering*, which says nothing about launch. Two testable
unknowns: does the promoted `Launch` work through podman's docker-compat exec API, and does the uid
mapping land under **rootless** podman, where the container user is a host subuid rather than a
remap.

**containerd/Kata: take B, design A later.** A needs a holder defined against a full systemd guest
rather than borrowed from docker's `sleep infinity`, and a readiness signal that means something in a
VM whose init is not ours. B needs only a barrier in the guest setup script — the same barrier the
deferred legacy path already needs.

## The macOS half — a research brief, not a scope estimate

tart, apple and seatbelt cannot be evaluated from a Linux host, so this states **questions with the
evidence that would answer them**. Nothing here should be costed until they are answered on hardware.

1. **Does the guest-side barrier (B) work on tart and apple?** Both share the sandbox directory with
   the host, so in principle the guest setup script can block on a host-written marker. Confirm the
   marker is visible *promptly* from inside the guest — VirtioFS and the `container` framework's
   sharing have their own coherence behaviour, and a barrier the guest observes late is a stall while
   one it never observes is a hang. Measure it rather than assuming it matches a bind mount.
2. **Can either backend host-launch (A)?** Both implement `Exec`, which is the raw material. The
   question is whether a neutral holder is expressible — for tart, whether the VM can boot to a state
   with no agent and be exec'd into afterwards; for apple, the same against the `container`
   framework's lifecycle.
3. **Seatbelt: confirm the property rather than assume it.** The claim above is read from the
   keep-alive model and a code comment. Verify on hardware that the host genuinely spawns the agent
   and that nothing in the sandbox scripts starts it earlier.
4. **For each, what does the conformance assertion need in order to hold the barrier closed?** That
   is the deliverable that makes mechanism divergence safe, and it is backend-specific plumbing even
   though the assertion is not.

## What "done" means

Every backend either has the property behind the common interface, or carries a mechanism-level
reason it cannot, in this file. **A stated blocker is a hypothesis until someone tries it** — the
gVisor exclusion is the cautionary case: a real reason, in a docstring, that any reader would accept,
and still wrong, because the reason had been solved elsewhere and never applied here.

## Related

- [DF171](../findings-unresolved.md) — the gVisor exclusion and its experiment.
- [tamper-resistant-network-isolation.md](tamper-resistant-network-isolation.md) — the consumer.
- [D88](../../decisions/working-notes.md) — the keepalive-holder + Launch bring-up (mechanism A).
