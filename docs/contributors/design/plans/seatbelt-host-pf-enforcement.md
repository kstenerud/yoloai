> **ABOUTME:** Rough design for enforcing `--network-isolated` on the seatbelt backend from host
> `pf`, keyed on a per-sandbox gid rather than an IP address. **Parked deliberately** — the design
> works on paper but costs far more than the guarantee it buys, and the reasons are recorded here so
> the next person does not re-derive them.

# seatbelt: host `pf` enforcement via a per-sandbox gid — parked

- **Status:** PLANNED — designed to the point of knowing it is not worth building yet. **Parked**,
  not abandoned: the mechanism is sound and the cost may change (see *What would unpark this*).
- **Depends on:** macos-pf-privileged-path.md

## Why this is parked

`--network-isolated` is **refused** on seatbelt today (`NetworkIsolation: false`,
`runtime/seatbelt/seatbelt.go:38`), and `netpolicy.CanEnforce` turns that into a named error. That
refusal is **honest**: it tells the user the guarantee is unavailable rather than pretending. The
status quo is therefore not a lie that needs fixing — it is a correct advertisement of a real limit.

Against that baseline, the four costs below buy a guarantee that is **partial by construction**. The
apple and tart backends already give a whole one from the same privileged path. So the ordering is:
ship macos-pf-privileged-path.md for apple and tart, keep seatbelt's honest refusal, and revisit
only if something on the list below changes.

## The mechanism, in outline

seatbelt runs the agent **as the invoking user, on the host**, inside a `sandbox-exec` profile.
There is no guest, no VM, and no separate IP — its traffic is the host's traffic. So the source
address that keys enforcement on apple and tart does not exist here.

`pf` does offer a different key: filter rules can match the **user or group owning the socket**.

1. Allocate a dedicated gid per sandbox.
2. Launch the agent with that gid as its **primary** group.
3. Write per-sandbox `pf` rules keyed on `group <gid>` into that sandbox's sub-anchor, exactly as
   macos-pf-privileged-path.md does with addresses.
4. Reap the gid and the rules at stop.

Steps 3 and 4 reuse the parent/sub-anchor structure and grant already validated. Steps 1 and 2 are
where the cost is.

## The four costs

**1. A second privileged operation, with a much worse argument surface.**
`setegid()` to a supplementary group returns `EPERM` **even for a member of that group**, so the
agent process cannot drop into the gid itself — it must be launched under it by something
privileged. That is a second grant, and where the pf grant's argument surface is one anchor name
matched by an anchored regex, this one is *a gid and a program path*. "Launch this binary under this
group" is uncomfortably close to granting arbitrary execution, and the binary in question is an AI
agent whose argv is attacker-influenced. Pinning it safely is a design problem in its own right, not
a variation on the solved one.

**2. The guarantee is partial by construction.**
`pf`'s `user`/`group` selectors match **TCP and UDP only**. Everything else — ICMP, and any other
protocol — is unfiltered no matter what rules are written. On apple and tart, address-keyed rules
cover all protocols. So seatbelt would ship a `--network-isolated` that means something materially
weaker than the same flag on every other backend, which then has to be explained in user-facing text
rather than discovered. Two flags spelled the same with different guarantees is exactly the
confusion DF179 exists to record.

**3. The containment argument is weaker here than anywhere else.**
The whole reason a `NOPASSWD` grant is acceptable is that its holder is the host user, who already
has unrestricted network access. On apple and tart the *contained agent* is not that party — it is
inside a VM. **On seatbelt the agent runs as the same uid on the host.** The only thing between it
and the grant is the seatbelt profile being `(deny default)` (`runtime/seatbelt/profile.go:25`). If
that profile ever permits exec of `sudo`, or write to any directory on `PATH`, the contained agent
*is* the grant holder and the entire security argument collapses. That is a permanent coupling
between an unrelated file's contents and this feature's soundness.

**4. gid allocation modifies system state, and recycles.**
Creating and destroying groups means `dscl` writes to the local directory node — system state, not
files under `~/.yoloai`. It needs its own allocation, collision and cleanup story, and gids recycle
exactly as addresses do, so a stale rule keyed on a reused gid silently filters an unrelated
process. The address version of that hazard is already measured and is bad enough with a namespace
yoloAI does not share; a gid namespace is shared with the whole machine.

## A trap that already cost a run

`sudo -u <user> -g <group>` is the obvious way to launch under a gid and **does not work here**: on
a default macOS sudoers, root is refused a runas-group the target user does not belong to —

```
Sorry, user root is not allowed to execute '...' as karlstenerud:yoloaispike
```

and a dedicated per-sandbox gid is *by definition* one the user is not in. Worse, the refusal is
silent in the way that matters: the probe returns an empty string, which reads exactly like a
blocked connection. An early run of the spike harness reported an entire gid allowlist section as
blank for this reason. Dropping privilege directly (`as_gid.py` in the spike) avoids the policy
layer. Anyone building this must not route through `sudo -g`.

## What was actually measured

Enough to know the mechanism is real, not enough to build on:

- `pf` `group` rules do filter a process launched under a dedicated gid — established in the
  original spike's gid section.
- `setegid()` returns `EPERM` for a supplementary group even for a member, so a privileged launcher
  is unavoidable.
- The `user`/`group` selectors are TCP/UDP-only per `man pf.conf`.
- **Not measured:** anything about gid allocation at scale, recycling, or the launcher's grant.

## What would unpark this

Any one of these changes the arithmetic:

- **seatbelt stops running the agent as the invoking user** (a dedicated service account per
  sandbox). That fixes cost 3 outright and makes cost 1 tractable, because the launcher's target
  becomes a fixed, non-interactive account.
- **A user actually needs it.** Today seatbelt is chosen for speed on macOS-native work; nobody has
  asked for a tamper-resistant allowlist on it. If that changes, revisit.
- **`pf` gains protocol-complete user/group matching**, removing cost 2. Unlikely.
- **The launcher already exists for another reason.** If a privileged per-sandbox launcher lands for
  some unrelated feature, cost 1 is already paid and this becomes cheap.

## What to do instead, now

Nothing, beyond what macos-pf-privileged-path.md already covers: keep the honest refusal, and make
sure `yoloai help security` says plainly that seatbelt has no network allowlist and why. A user who
needs the guarantee on macOS should be pointed at apple or tart, where it is whole.
