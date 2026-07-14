# Tart network liveness detection

**Status:** Partially implemented (2026-07-14). The doctor surfacing below is
done: `runtime/tart/netcheck.go` implements the two-signal probe behind a new
backend-neutral `runtime.NetLivenessReporter` optional interface (mirroring
`VMCensusReporter`); `yoloai doctor` reports per running VM (`network: ok` /
`WEDGED` with the directive restart message / `could not determine`), includes
a `net_liveness` section in `--json`, and exits non-zero on a confirmed wedge.
Verified end-to-end against a live wedged VM (DF86 — which also established
that a wedged session poisons networking for *new* VMs host-wide, raising the
stakes for detection). The `info`/`ls` status path and smoke-harness fail-fast
remain unimplemented; the in-guest monitor option remains speculative. The
incident itself is documented in
[backend-idiosyncrasies.md](../../backend-idiosyncrasies.md#tart-vmnet-session-wedges-on-a-long-idle-vm-host-sleep--subnet-re-pick--guest-drops-to-a-169254-link-local-address-agent-gets-connectionrefused).

## Problem

A Tart VM left running across a host sleep / network transition can lose its
vmnet session entirely: macOS re-picks the shared subnet, the guest's DHCP
lease dies, and — the real disease — the vmnet L2 link backing the running
`tart run` process wedges so that no frames pass in either direction. The only
recovery is a VM restart.

From the user's side this is invisible until the agent starts failing:
`yoloai ls` showed the sandbox as `active`, `tart exec`/`attach` worked
normally (the control channel is Virtualization.framework, not IP), and the
agent just spun on `ConnectionRefused` retries. Nothing in yoloAI's surface
said "the VM's network is dead; restart it" — the user had to debug from raw
symptoms. Prevention isn't available to us (the wedge is inside the host's
vmnet stack), so the deliverable is **detection + a directive message**.

## Detection signals

Ordered by cost; the two cheap ones are each sufficient for a directive
diagnosis:

1. **`tart ip <vm>` returns nothing for a running VM** — host-side only, no
   guest exec. This is how the incident first manifested to tooling. Caveat:
   also transiently true during boot, so it needs a "VM has been up > N
   minutes" or "was previously reachable" qualifier.
2. **Guest `en0` holds a link-local address** (`ipconfig getifaddr en0`
   returns `169.254.*` — verified live: it prints the link-local address, it
   does not come back empty) on a running VM — one `tart exec`,
   definitive for "DHCP failed", and it distinguishes "network dead" from
   "yoloai can't determine the IP for some other reason".
3. **Both-ways ARP `(incomplete)`** after forcing a static IP — this is the
   conclusive manual test for the wedge, but it mutates guest state and is
   too invasive for automated probing. Signals 1+2 together are enough to
   recommend a restart; we never need to prove the wedge mechanically.

## Where to surface it

- **`yoloai doctor`** — the natural first home. It already owns backend
  health sections (VM slots census in `runtime/tart/census.go`). Add a
  per-running-Tart-sandbox network-liveness check: signal 1, confirmed by
  signal 2, reported as `network: WEDGED — restart to recover
  (yoloai stop <name> && yoloai start <name>)`. Doctor reports only, never
  restarts — consistent with the VM-slots precedent.
- **`yoloai info` / `ls` status** — worth considering after doctor: `active`
  was actively misleading here (the agent process was alive but could do no
  useful work). A distinct status such as `active (net-dead)` needs the probe
  on the status read-model path (`internal/orchestrator/status/status.go`),
  so probe cost matters more; signal 1 is nearly free (one `tart ip` we may
  already be paying for) and could gate the one-exec signal-2 confirmation.
- **The in-guest monitor** (`runtime/monitor/`) already watches the agent —
  it could notice the link-local address from inside and record it in
  `agent-status.json`, giving the host a zero-additional-exec signal. This is
  the most speculative option; only worth it if the status-path probe proves
  too expensive.

## Remediation stance

Recommend, don't act. A restart kills the live agent process (the on-disk
session survives; the in-flight turn doesn't), so auto-restart on detection is
wrong by default — the same "gate, don't guess" posture as the unapplied-work
probes. The message should name the exact commands and say why: "VM network is
wedged (host slept or changed networks); only a restart recovers it; agent
session state survives".

## Open questions

- Does the probe belong in `doctor` only (do it first, cheap), or also on the
  `info`/`ls` status path from the start?
- Should `yoloai wait` / the smoke harness fail fast on a net-dead sandbox
  instead of waiting out the agent's retry loop?
- Generalize beyond Tart? Kata/QEMU guests have their own netns failure modes
  (see the Kata warm-up race entries) with different signals. Scope this to
  Tart; keep the surfacing (a per-sandbox network-health field) backend-neutral
  so another backend can feed it later.
