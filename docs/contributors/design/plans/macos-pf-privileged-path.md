> **ABOUTME:** Build plan for enforcing `--network-isolated` from host `pf` on macOS, where the
> allowlist today is either weak (apple grants the guest NET_ADMIN) or absent (tart, seatbelt).
> Covers how yoloAI acquires the privilege `pf` needs without installing a root daemon.

# macOS: enforce the network allowlist from host `pf`, via opt-in `sudo`

- **Status:** PLANNED — mechanism measured on hardware 2026-08-02/03, approach chosen by the owner
  2026-08-03. Nothing built.
- **Depends on:** tamper-resistant-network-isolation.md

## Why this exists

On macOS, `--network-isolated` is either weak or absent. `apple` installs the allowlist inside the
sandbox and grants it `NET_ADMIN`, so the agent can flush its own rules (DF179, measured). `tart`
and `seatbelt` refuse `--network-isolated` outright, so they have none at all.

The research settled that this is **not** a platform limitation. Host `pf` enforces per-sandbox on
all three macOS backends, keyed on something the guest cannot change, and it holds against an agent
that actively attacks it. The full measurement, its controls and its two discarded harness
generations are in [tamper-resistant-network-isolation.md](tamper-resistant-network-isolation.md)
§ The macOS half; the raw runs are in
[macos-isolation-spike](../research/macos-isolation-spike/README.md). What blocks it is one thing
only: **`pf` needs root, and yoloAI on macOS has no privileged path.**

## The decision, and the constraint that drove it

`AGENTS.md` opens with "Go binary, no runtime deps beyond the backend". That is not decoration — it
is why yoloAI installs by copying a file. Three ways to get root, and what each costs:

| Approach | Gets the property | Cost |
| --- | --- | --- |
| **`sudo` on demand, opt-in** | yes | a password prompt per privileged operation (or a sudoers line the user writes); requires an admin user; needs a reaping story |
| Installed helper (`SMJobBless` / launchd) | yes | an installer, code signing, an uninstall story, and a permanently root-privileged daemon on disk |
| Do nothing | no | macOS stays materially weaker than Linux |

**Chosen: `sudo` on demand, behind an explicit opt-in.** `sudo` ships on every Mac, nothing is
installed, nothing persists, the shipped artifact is still one file, and the decision is reversible
— if the prompts prove intolerable the installed helper is still available later, and the rule
authoring work is the same either way.

**Why Linux never needed this.** On Linux yoloAI borrows privilege from the backend instead of
acquiring it: the allowlist is installed by asking Docker to run a sidecar with `NET_ADMIN`
(`RunNetnsSidecar`, `CapAdd: ["NET_ADMIN"]`). The daemon is already root and already trusted with
root by the user who installed it, and it has a defined way to delegate one capability into one
namespace. macOS has no equivalent: `pf` is a host kernel facility with no delegation mechanism,
and `seatbelt` has no daemon at all. macOS is the first case where the privilege would have to be
**yoloAI's own**, which is exactly the line that sentence in `AGENTS.md` was drawing.

## What the mechanism has to be, per backend

Measured; do not re-derive from assumptions.

| Backend | Enforcement key | Note |
| --- | --- | --- |
| apple | source address on the vmnet bridge | a second VM on the same bridge was unaffected — that discriminator is what makes it per-sandbox |
| tart | source address on the vmnet bridge | same shape, same result |
| seatbelt | **gid owning the socket** | `pf`'s `user`/`group` selectors match **TCP and UDP only**, so non-TCP/UDP egress is unfiltered by construction |

Three hazards, each of which produced a false result before it was understood:

1. **Never touch the main ruleset.** `pfctl -f` *replaces* it, and that is where vmnet's NAT lives —
   the first harness silently destroyed egress for every VM on the host and then measured
   "blocked". Reloading `/etc/pf.conf` does not restore the NAT; only restarting the vmnet service
   does. Everything must live in a nested anchor under `com.apple/`.
2. **`quick` follows the direction keyword.** `block drop quick in on …` does not parse;
   `block drop in quick on …` does. A rule that fails to parse reads exactly like traffic that
   cannot be filtered.
3. **Key on the sandbox address, never the interface name.** vmnet re-picks subnets when a bridge is
   re-created, and bridge indices move between backends (DF172) — a rule keyed on `bridge101` can
   silently attach to a different backend's traffic after a restart.

For seatbelt there is a second privileged step: launching the agent under a dedicated gid.
`setegid()` to a supplementary group returns `EPERM` **even for a member of that group**, so the
gid has to be set by a privileged launcher, not by the agent process dropping into it.

## Shape of the work

**Phase 1 — opt-in and the privileged call site.** A config key and/or flag that says "use host `pf`
for network isolation on macOS". Off by default; when off, nothing changes and nothing prompts. When
on, `--network-isolated` becomes available on `tart` and `seatbelt` (today refused) and stops
granting `NET_ADMIN` on `apple`.

**Phase 2 — rule lifecycle.** Rules are written to a per-sandbox nested anchor
(`com.apple/yoloai_<principal>_<sandbox>`) at start and removed at stop. Two things need designing
rather than assuming:

- **Reaping.** If yoloAI is killed between start and stop, the anchor outlives the sandbox. A stale
  `block` rule keyed on an address that vmnet later hands to something else is a live hazard, not
  just litter. Options: reap-on-next-run by comparing anchors against live sandboxes (cheap, but
  leaves a window), or a `yoloai doctor` sweep, or both. This is the part most likely to be got
  wrong quietly.
- **How often it prompts.** One `sudo` per sandbox start plus one per stop is the naive shape.
  Whether that is tolerable, and whether to document a scoped `sudoers` line as the escape hatch
  (`NOPASSWD` for one exact `pfctl` invocation), is a UX decision this plan should not pre-empt.

**Phase 3 — seatbelt's gid launcher.** Allocate a per-sandbox gid, launch the agent under it
privileged, and write the matching `pf` rule. Note the TCP/UDP-only limitation in user-facing text
rather than letting it be discovered.

## Acceptance test

Every assertion pairs a **block** with a **positive control in the same run**. This is not
belt-and-braces: a macOS sandbox stranded by the vmnet subnet re-pick has *no* network at all, so it
passes "egress to a non-allowlisted destination is refused" for free, on every destination — which
silently invalidated the first run of the `pf` research harness (DF172). A conformance case that
asserts only the denial certifies nothing.

So, per backend, in one run: an allowlisted destination **succeeds**, a non-allowlisted destination
**fails**, a second sandbox on the same bridge is **unaffected**, and after the sandbox stops the
anchor is **gone**. The tamper arm — the sandbox tries to remove the rule and cannot — is the
property the whole plan exists for and belongs in `runtime/runtimetest` rather than a spike script.

## Not in scope

IPv6 (the allowlist is IPv4-only on every backend today), and the `apple`/`podman`/`containerd`
`NET_ADMIN` grant on Linux, which is DF179's own problem and has a different fix per backend.
