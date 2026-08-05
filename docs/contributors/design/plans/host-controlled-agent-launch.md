> **ABOUTME:** The property "yoloAI decides when the agent starts" on every backend, behind one
> interface, by whichever mechanism each backend can actually offer. The enabler for enforcing a
> network allowlist the agent cannot tamper with.

# Plan: yoloAI decides when the agent starts

- **Status:** PLANNED — researched 2026-08-02 against the code; the gVisor item and the whole macOS
  half verified by experiment on real hardware (§ The macOS half). No production code written.
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
| podman | **verified working** | available | opt-in is one field |
| containerd / Kata | needs design (systemd guest) | **available — shared sandbox dir** | B is the cheap path |
| apple | **available — verified** | **verified, ~10 ms via `readdir`** | measured 2026-08-02 |
| tart | **already does A — verified** | **verified, ~10 ms via `readdir`** | measured 2026-08-02 |
| seatbelt | **holder already exists** (bare path) | n/a — no guest | **already has the property — confirmed** |

**Seatbelt already satisfies it and is not a gap — confirmed on hardware 2026-08-02.** Its keep-alive
model is `KeepAliveHostKeepAlive` — no container or VM; the sandbox is a host process group the host
spawns. `runtime.go` says there is "no 'inside' to Launch into". The host owns the launch moment by
construction. It needs an interface declaration, not an implementation.

One correction to how that is usually stated: the host does not spawn the *agent*, it spawns
`sandbox-setup.py` under `sandbox-exec` (`runtime/seatbelt/seatbelt.go:357-373` — argv built at
`:357`, `cmd.Start()` at `:373`), and that script starts
tmux and the agent. Observed on the host: `sandbox-setup.py` → `agent-run.sh` → `claude`, all as host
processes, with the same detector finding nothing before creation. So on the **production path**
there is exactly one host-controlled moment — the window before that single spawn, which is
unbounded and entirely the host's. That alone satisfies the firewall ordering requirement.

**A holder already exists, though, and an earlier draft of this section wrongly said it did not.**
`runtime/seatbelt/seatbelt.go:342-346` picks a bare keep-alive — `sandbox-exec -f <profile> tail -f
/dev/null` — whenever the sandbox layer provisioned no `runtime-config.json`, described in its own
comment as "a running, exec-able instance … with no monitor. Mirrors tart's P1/P2 split", with
`Exec` running fresh `sandbox-exec`'d commands against it. That is precisely a neutral holder with a
second injection point. It is currently selected only on the bare path (direct `runtime.Backend` use
and the conformance suite), which is a fact about the branch condition, not about expressibility.

**And it is exercised, not merely present** — verified 2026-08-03 rather than read from code, which
matters because the claim it replaces ("seatbelt has no holder") was also read from code and was
wrong. `go test -tags=integration -run Conformance ./runtime/seatbelt/` brings up instances on that
bare path and passes `CreateStartStopRemove`, `InspectRunning`, `ExecSimple`, `ExecNonZeroExit` and
both `InteractiveExec` cases against them. An instance that starts, stays running with no agent, and
accepts `Exec` is mechanism A's premise in full. So adopting it means changing which branch the
production path takes, not building a holder — and the assertion that would guard it has somewhere
to live already.

**gVisor is one field plus a shared decision.** `SupportsAgentFreeLaunch` excludes it because
`docker exec --user <name>` resolves the username against the image's stale `/etc/passwd`. Real —
and `store.ContainerUser` has returned the **numeric host UID** for exactly that defect since
2026-03-18, three months before the exclusion was written. Verified 2026-08-02 on this host's
`runsc`: with the exclusion lifted and the launch user made numeric, a real container-enhanced
sandbox took the agent-free path, launched the session-runner as the host user, ran the agent, and
an agent write reached `yoloai diff`. Route the launch user through `ContainerUser`'s decision rather
than adding a third spelling; it also handles `UsernsMode == "keep-id"`, which the launch path
ignores today.

**podman: verified 2026-08-02, and it is one field.** The concern was that an earlier attempt at this
exact flip was parked and later called "moot" — but that was about *brokering*, which says nothing
about launch. Tested directly: a throwaway binary with `AgentFreeLaunch: true` on podman's descriptor
brought up a **rootless** podman sandbox that took the agent-free path
(`entrypoint.keepalive_only`, "no agent launched"), launched the session-runner, and ran the agent to
`active`. The promoted `Launch` works through the docker-compat exec API and the uid mapping lands.
The separate question of whether a rootless *sidecar* can enforce is also answered — yes, see the
consumer plan.

**containerd/Kata: take B, design A later.** A needs a holder defined against a full systemd guest
rather than borrowed from docker's `sleep infinity`, and a readiness signal that means something in a
VM whose init is not ours. B needs only a barrier in the guest setup script — the same barrier the
deferred legacy path already needs.

## The macOS half — measured on hardware 2026-08-02

macOS 26.5.1 / arm64, tart 2.32.1, `container` 1.0.0. All four questions answered; **both mechanisms
are available on both VM backends**, and seatbelt's property is confirmed rather than assumed.

**1. Mechanism B works on both — and the barrier's *predicate* matters more than its shape.**
Guest-side medians, n=3, 1 ms poll, including a 200 ms settle pause (subtract 200 for
time-after-the-host-acted). Raw output and harness:
[macos-isolation-spike](../research/macos-isolation-spike/README.md).

| host action | apple `read`/`stat`/`readdir` | tart `read`/`stat`/`readdir` |
| --- | --- | --- |
| **create a new file** | – / 208 / **209** | – / 993 / **214** ← use `readdir` |
| mkdir, symlink | – / ~204 / ~206 | – / ~994 / **~210** |
| overwrite in place | 206 / 205 / – | **NEVER** / 989 / – |
| overwrite via temp+rename | 205 / 1004 / – | **NEVER** / **NEVER** / – |
| delete | – / 1204 / 205 | – / **NEVER** / **212** |
| append | 1004 / 1004 / – | 992 / 992 / – |

**Poll the barrier with `readdir`, and signal by creating a new name.** That combination sees
the host's action ~10–15 ms after it happens on *both* backends. The same event observed by
`stat` costs ~800 ms on tart, and observed by `read` on a rewritten file never arrives at all.
The choice of predicate is worth ~70× on tart and is free.

**Three corrections to an earlier draft of this section, all in the same direction.**

1. It reported tart's overwrite, rename and delete as "NEVER — the change does not propagate".
   Wrong: those changes arrive in about a second. `NEVER` was a property of the *predicate* the
   harness bound to each shape — content-polling for overwrites, `stat`-polling for deletes —
   not of the filesystem. Measuring `action × predicate` and reporting it as a fact about the
   action is what produced "rename does not rescue the barrier" and "a wait-until-gone barrier
   hangs forever"; both are false, and a `readdir`-based disappearance barrier converges in
   ~12 ms.
2. It quoted ~797 ms as tart's create latency. That figure is `1000 − the harness's own 200 ms
   settle pause`. The underlying quantity is a **~1 s revalidation tick anchored to the guest's
   first lookup**: the guest's own clock reads ~1000 ms wherever in the interval the host acts.
   The honest worst case for a `stat`-polled barrier is **1 s**, quantised — and the apple-vs-tart
   difference the draft reported (803 vs 797) was noise on the same constant.
3. It called apple "not uniformly coherent … the same cache biting occasionally". There are two
   clocks, and the slow one is not occasional: apple serves *data* in ~5 ms and *metadata* (size
   after a rename, dentry removal after a delete, size growth after an append) on the same ~1 s
   tick, every round.

**The one thing that got worse, not better.** `read()` never observing a host overwrite is not a
barrier-latency problem — the guest reads the **old bytes at the new length**, with a correct
`st_size` and no error. That is a data-integrity hazard well outside this plan's scope; filed as
[DF175](../findings-unresolved.md) and catalogued in
[backend-idiosyncrasies.md](../../backend-idiosyncrasies.md).

**2. Mechanism A is expressible on both, and tart already uses it.**

- **tart already has A.** The host boots the VM and then reaches in: `tart exec <vm> bash -c "…
  sandbox-setup.py …"` (`runtime/tart/mounts.go:87-108`). Confirmed on hardware: after killing the
  agent chain inside the guest, the VM stayed up — held by the host's own `tart run` process, not by
  anything in the guest — stayed agent-free, and `tart exec` then started a process on demand.
  Nothing in the guest auto-starts the agent: no yoloai LaunchAgents or LaunchDaemons, and
  `launchctl` lists none (checked against a control showing 499 services present, so the negative is
  not a broken command). The VM *is* the neutral holder, already.
- **apple can hold neutrally.** `container run --entrypoint …` boots a genuine holder: the detector
  found no agent chain in it, while the **same detector** against a normally-booted sandbox found
  `sandbox-setup.py`, tmux, `agent-run.sh` and `claude`. `container exec` then started a process in
  the holder post-boot. Apple is the one backend here whose guest genuinely self-starts today (the
  image `ENTRYPOINT` runs the shared `entrypoint.py`), so A is a real change for it — but an
  available one.

**3. Seatbelt confirmed** — see the section above; the correction is that the host owns one launch
moment, not a holder it can exec into later.

**4. What the conformance assertion needs per backend.** The barrier itself is cheap everywhere; the
backend-specific plumbing is only *where the marker lives* and *how the assertion observes the agent*:

- apple / tart: the marker is a **created file** in the shared sandbox dir (never a rewritten one on
  tart). The agent-presence probe is a process check via `container exec` / `tart exec`.
- seatbelt: there is no barrier to hold — the assertion must instead verify that no agent process
  exists between environment setup and the host's spawn, which is a host-side `ps` check.
- Every case needs the positive control beside it ([A22](../../agent-failures.md)): release the
  barrier and require the agent to actually start. This plan's assertion is about **agent-process
  presence**, so the vacuity hazard here is a probe that cannot run (an `Exec` that fails for its own
  reasons reads as "no agent") — hence the paired release-and-start half. The *network*-flavoured
  version of that hazard belongs to the consumer plan; see [DF172](../findings-unresolved.md).

## What "done" means

Every backend either has the property behind the common interface, or carries a mechanism-level
reason it cannot, in this file. **A stated blocker is a hypothesis until someone tries it** — the
gVisor exclusion is the cautionary case: a real reason, in a docstring, that any reader would accept,
and still wrong, because the reason had been solved elsewhere and never applied here.

## Related

- [DF171](../findings-unresolved.md) — the gVisor exclusion and its experiment.
- [tamper-resistant-network-isolation.md](tamper-resistant-network-isolation.md) — the consumer.
- [D88](../../decisions/working-notes.md) — the keepalive-holder + Launch bring-up (mechanism A).
