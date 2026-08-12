> **ABOUTME:** Replace the two network booleans with one four-valued `--network` flag, so the
> product can say which adversary each mode defends against — and so the mode that defends against
> a compromised agent has somewhere to live.

# Network mode reshape — one flag, four modes

- **Status:** PLANNED — designed, no code. The mode it exists to make room for (`restricted`) is
  built by [egress-proxy-build.md](egress-proxy-build.md); this plan is the user-facing surface
  and can land ahead of it with `restricted` absent.
- **Depends on:** egress-proxy-build.md
- **Rides:** **breaking.** Five separate breaks, each independently rule-1: `--network-none` and
  `--network-isolated` become deprecated aliases; an allowlist under `none` starts being refused
  ([DF196](../findings-unresolved.md)); `--port` starts being refused under `restricted`; a named
  mode a backend cannot deliver starts being refused ([D138](../../decisions/working-notes.md));
  and DNS starts being denied under `isolated` with an empty allowlist. Escalate the version in
  `next-release.md` when the first lands.

## Why the current shape cannot express what is true

Two booleans, `--network-isolated` and `--network-none`, and between them no way to say the thing
[D137](../../decisions/working-notes.md) established: **the allowlist is a guardrail against a
confused agent and is not adversarially sound on any backend.** The agent has `sudo` by design, and
the residue behind that is unbounded.

So the product's advice for a hostile agent currently resolves to `--network-none` — `yoloai help
security` says *"Use --network-none for maximum isolation"*, `GUIDE.md` says *"use --network-none
when you need a guarantee"* — and [P9](../research/proxy-chokepoint/results/p9-network-none-utility.txt)
measured what that costs: **no shipped AI agent can work under it**, because none can reach its own
API. A real agent failed a task that needed no network to perform, since it could not be told to do
it. The advice is not wrong; its only remedy is useless. That is the gap.

## The four modes

| Mode | Defends against | Mechanism | Egress channels |
| --- | --- | --- | --- |
| `open` | nothing | none | all |
| `isolated` | a **confused** agent | in-guest IPv4 allowlist | allowlisted TCP, **plus DNS** |
| `restricted` | a **compromised** agent | one destination; policy at a host-side proxy | the proxy only |
| `none` | — (quarantine) | no interface | **none** |

**`isolated` keeps its name and its meaning, and gains an honest description.** It is not being
weakened — it is being described. Nothing about its mechanism changes here.

**`restricted` is the new one** and is where D137's invariant lands: the sandbox's only egress is a
host-side proxy under a principal the agent has no privilege over. Measured viable end to end in the
[chokepoint round](../../archive/plans/proxy-chokepoint-verification-round.md): 99.2% of a real
session is HTTP or DNS with zero QUIC, a real session completed its task with one destination, every
package manager worked on standard proxy environment alone, and the guest never resolved because the
proxy does.

**`none` survives on one measured property**, not on the counterexamples first offered for it. The
owner's objection to those was correct — `test` and `idle` are not agents and pose no threat if
networking is allowed, so "they still work under `none`" argues nothing. What survives is a
differential: two `idle` sandboxes differing only in mode, **TCP blocked under both, and DNS
resolving under `isolated`**. An isolated sandbox with nothing legitimate to resolve still reaches a
nameserver, which is a working exfiltration channel — data leaves in query labels to a domain the
exfiltrator controls. So `none` is the only mode with no channel at all, and stays so under this
plan, because `restricted` always permits the proxy.

Its use is **running untrusted code rather than an untrusted agent**: a suspicious repository, a
dependency's test suite, a build script nobody has read, with `--agent idle` and `exec`, where
copy/diff/apply is the point.

**And it is kept for a reason the table cannot show: it is the only mode a human can hold in their
head as an absolute.** *"No network, ever"* needs no reader to know what the allowlist contains,
what the proxy policy is, or which backend they are on. Every other mode's guarantee is conditional
on something. That is worth a mode on its own, independently of the DNS argument below — which
remains worth doing anyway, because an empty allowlist with open DNS is a hole with no compensating
benefit.

## Why one flag with a value, not a third boolean

1. **The names do not order themselves.** "Isolated" connotes more separation than "restricted"; a
   user reaching for the strongest option could reasonably pick the weaker one. An enum makes the
   reader see the ordered list at the point of choosing, which dissolves the problem instead of
   solving it by finding better adjectives.
2. **It is already an enum underneath.** `NetworkMode` is `"none"` / `"isolated"` / `""` in the
   store and in `netpolicy.Compose`. The booleans are a CLI-only shape over a value.
3. **It is the established idiom here.** `--isolation container|container-enhanced|vm|vm-enhanced`
   is the same problem solved the same way, one flag away.
4. **Booleans need pairwise exclusion forever.** `new.go:74` already carries
   `MarkFlagsMutuallyExclusive("network-none", "network-isolated")`; a third adds two more pairs,
   and DF196 exists because a *fourth* flag (`--network-allow`) sits outside that mechanism.

## Closing DNS under `isolated`

**Rule: port 53 is denied while the effective allowlist is empty, and permitted the moment it is
not.** Today an isolated sandbox with nothing legitimate to resolve still reaches a nameserver,
which is a working exfiltration channel and buys nothing.

**It bites only where it should.** The effective allowlist includes the agent's own floor, so any
real agent keeps DNS — the rule only closes the case where nothing has any business resolving
anything, which is `--agent idle`, `--agent test`, or an agent with no floor.

**It must be reversible on a running sandbox**, and that has an ordering trap in it. Adding a domain
later re-opens 53 — but [D133](../../decisions/working-notes.md) resolves allowlisted domains **in
the guest's resolver context**, so the live patch cannot resolve the domain it is being asked to add
until DNS is already open. So `LivePatchNetwork` must **lift the DNS denial first, then resolve,
then add the address** — not resolve-then-open, which deadlocks on the first domain added to an
empty list and would look like "the allowlist is broken" rather than like an ordering bug.

The inverse holds for symmetry: removing the last domain re-closes 53, because the invariant is *no
allowlist ⇒ no DNS*, not *DNS was once open*. Worth a test in both directions — the second is the
one nobody writes.

## Failing loudly

Three refusals. Each is newly-rejected input, so each needs a `BREAKING-CHANGES.md` entry, and each
needs a test that goes red on revert (rule 10).

**1. An allowlist under `none` is an error — [DF196](../findings-unresolved.md), and it is a live
defect today.** `--network-none --network-allow example.com` currently **exits 0**, creates a
running sandbox, stores `{"version": 1, "network_mode": "none"}` with no `allow` key, and gives the
guest only `lo`. The user asked for a destination and was neither refused nor warned, and the
request left no trace for a later reader.

The fix belongs in **`netpolicy.Compose`**, not the CLI. Three doors reach it — the flag pair, a
profile setting `network.mode` and `network.allow` independently, and an integrator calling the
public API — and only `Compose` is downstream of all three. The runtime path is **already correct**
and is the model: `Network.Allow` → `requireIsolated` refuses with *"sandbox %q uses
--network-none; cannot modify network access"*. Creation is the one entry point that does not hold
the position the product already holds.

**An agent floor under `none` is refused too — decided, not deferred.** It is the same shape as a
user list: `--agent claude --network=none` supplies domains the mode cannot honour, and `Compose`
discards them just as silently. Refusing makes `none` unusable with any real agent, which
[P9](../research/proxy-chokepoint/results/p9-network-none-utility.txt) shows is *already* true —
no shipped agent can work under it — but which the product has never said. The error is it finally
saying so, at the moment the user asks, instead of leaving them to discover that their agent sits
there unable to reach anything.

**2. `--port` under `restricted` and `none` is an error.** A guest reduced to one destination cannot
answer inbound connections, and the owner's call is that this is an acceptable casualty: `--port` is
special-purpose, and failing loudly is correct. The precedent, the message and the code path already
exist — [P7](../research/proxy-chokepoint/results/p7-port-publishing.txt) confirms
`--port is incompatible with --network-none` fires at the CLI before anything is created. Extend it
to `restricted`; do not invent a second shape.

**3. A named mode the backend cannot deliver is refused — [D138](../../decisions/working-notes.md),
which refines D135 rather than overriding it.** An earlier draft of this plan had this degrading and
disclosing, on D135's authority. That was wrong, and the reason is this plan's own doing: D135's
posture rests on the guarantee being *implicit*, so that refusing a user left them with nothing.
With four named modes, refusing `restricted` leaves `isolated` still available and now sayable —
**the enum is what makes refusal safe.** And degradation is no longer invisible: `--port` works
under `isolated` and cannot under `restricted`, so a silent degrade would restore a capability the
chosen mode excludes. The error must name the mode, the backend, and the strongest mode that backend
*can* deliver, so the remedy is in the message rather than in a second command.

## What this does not decide

- **The mechanism behind `restricted`** — a normal guest stack with a one-address filter, or no IP
  stack at all with a socket to the proxy. Priced in D137 § *What the round settled*; both satisfy
  the invariant and the choice is per backend.
- **git over SSH.** It works through a `CONNECT` tunnel, no tunnel tool ships in the image, and
  allowing it hands the proxy an opaque stream that reopens the closure `restricted` exists for.
  Owner's call, recorded in D137.
- **Whether `none` survives at all**, per the port-53 alternative above.

## Order of work

1. **`Compose` refuses an allowlist under `none`** (DF196), user list and agent floor alike.
   Independently useful, fixes a live defect, and needs none of the rest.
2. **`--network=` enum**, with `--network-isolated` and `--network-none` as deprecated aliases that
   still work. Register both in `docs/contributors/deprecations.md` with the date incurred — a
   compatibility alias is a deprecation on the day it lands (rule 9).
3. **Deny port 53 under `isolated` with an empty allowlist**, with the live re-open and its
   ordering. Independent of the enum, and `none` is kept regardless — see § *The four modes*.
4. **Refuse a named mode the backend cannot deliver** (D138). Needs the enum first, because the
   error message has to name the strongest mode that backend *can* deliver.
5. **`restricted` becomes selectable** when `egress-proxy-build.md` has something behind it. Until
   then the value does not exist rather than existing and degrading to `isolated`, which would be a
   promise the product cannot keep.

## Surfaces to sweep when the names change (rule 2)

`internal/cli/lifecycle/new.go` (flags, exclusions, `printCreateSummary`), `internal/cli/helpcmd/help/{security,flags}.md` (embedded, shipped, typechecked by nothing), `docs/GUIDE.md`, `docs/contributors/design/network-isolation.md`, `netpolicy.md`, `profile_config.go` (`ProfileNetwork`), `network.go` (`Mode`, `requireIsolated`), and `internal/netpolicy/compose.go`. The three places recommending `--network-none` as a guarantee are the ones that most need rewriting, and they are in shipped help rather than in docs.
