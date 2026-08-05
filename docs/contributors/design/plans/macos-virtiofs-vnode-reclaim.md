> **ABOUTME:** The one measured mechanism that restores a tart guest's view of a host-rewritten
> file without a reboot — forcing guest vnode reclamation — and why it is not shipped yet.

# macOS virtiofs: bounded vnode reclaim

- **Status:** UNSPECIFIED — a mechanism with third-party evidence and no design. Not attempted here.
- **Depends on:** —
- **Rides:** **any.** Additive; it would replace a refusal with a repair.

## Why this exists

[DF175](../findings-unresolved.md) is not repairable by anything yoloAI currently does. Apple lists
"modify in place, host → guest" and "delete, host → guest" as **unsupported** for virtiofs shares
([forums thread 828366](https://developer.apple.com/forums/tags/virtualization/?page=2&sortBy=newest),
Feedback [FB22905515](https://feedbackassistant.apple.com/feedback/22905515)), and v0.11.0 therefore
ships a **refusal**: `files put` declines to recreate a name removed while the guest was running.

That is honest but it is a wall, not a fix. This plan holds the one direction with measurements
behind it.

## The mechanism

The guest's page cache is tied to a vnode that macOS does not reclaim under normal pressure.
Attributes refresh on a ~1 s tick; **content does not refresh until the vnode is reclaimed**.

[augur#135](https://github.com/h1d3mun3/augur/issues/135) measured, on a macOS guest:

- staleness persisting **904.9 s** across 171 consecutive 5 s polls;
- content becoming fresh **10.3 s after a deliberate vnode-reclaim burst**;
- **860 vnodes** recycled naturally in a 30-minute window;
- read tearing (old and new bytes within one read), and `lstat`/`open` succeeding on paths already
  gone from `readdir`.

So the lever is **eviction, not invalidation**. That distinction explains both of this project's own
results: `msync(MS_INVALIDATE)` repairs an in-place overwrite because the vnode still exists to be
mapped ([DF181](../findings-unresolved.md)), and cannot repair rm+recreate because the retained
vnode is re-associated with the reappearing name.

## Why it was not shipped

1. **It works by inducing cache pressure in the guest** — walking enough paths to force the kernel to
   recycle vnodes. That is a deliberate resource-exhaustion nudge inside a sandbox running someone's
   agent, on a shipped command.
2. **No safe bound is established.** augur reports the reclaim burst worked and explicitly leaves the
   bound undetermined. Too little does nothing; too much competes with the agent.
3. **n=1, third-party.** Reproducing the burst on this project's hardware is the first step, and it
   has not been done.
4. A refusal was available, is measurable, and needs none of the above.

## What would settle it

- Reproduce the burst on this hardware: poison a path, apply bounded reclaim pressure, measure
  whether content refreshes and at what cost. `df175_rmput.sh` already provides the poisoning half.
- Establish a bound that refreshes reliably without starving the agent, and how it scales with the
  guest's vnode table size.
- Decide whether it belongs behind the existing `GuestFileRefresher` seam (a stronger repair) or
  as an explicit operator action.
- Watch FB22905515. If Apple exposes a way to disable the cache, all of this is moot — that is the
  outcome worth waiting for rather than engineering around.
