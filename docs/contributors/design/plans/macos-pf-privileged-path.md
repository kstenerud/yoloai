> **ABOUTME:** Build plan for enforcing `--network-isolated` from host `pf` on macOS, where the
> allowlist today is either weak (apple grants the guest NET_ADMIN) or absent (tart, seatbelt).
> Covers how yoloAI acquires the privilege `pf` needs without installing a root daemon.

# macOS: enforce the network allowlist from host `pf`

- **Status:** PLANNED — mechanism and authorization both measured on hardware 2026-08-02/03.
  Nothing built.
- **Depends on:** tamper-resistant-network-isolation.md

## Why this exists

On macOS, `--network-isolated` is either weak or absent. `apple` installs the allowlist inside the
sandbox and grants it `NET_ADMIN`, so the agent can flush its own rules (DF179, measured). `tart`
and `seatbelt` refuse `--network-isolated` outright, so they have none at all.

The research settled that this is **not** a platform limitation. Host `pf` enforces per-sandbox on
all three macOS backends, keyed on something the guest cannot change, and it holds against an agent
that actively attacks it. The measurement, its controls and its two discarded harness generations
are in [tamper-resistant-network-isolation.md](tamper-resistant-network-isolation.md) § The macOS
half; raw runs in [macos-isolation-spike](../research/macos-isolation-spike/README.md). What blocks
it is one thing: **`pf` needs root, and yoloAI on macOS has no privileged path.**

## The decision

**A generated `NOPASSWD` sudoers line, authorizing table membership only.** Installing that line is
the opt-in — no flag, no isolation mode, no password prompt at runtime.

The route here was not obvious and three earlier candidates died to measurement. The evidence is in
[pf-authz-unprivileged.txt](../research/macos-isolation-spike/results/pf-authz-unprivileged.txt) and
[pf-authz-privileged.txt](../research/macos-isolation-spike/results/pf-authz-privileged.txt).

**Why not a runtime `sudo` prompt.** Rules must be removed at stop, and stop is the least
interactive moment in the lifecycle: a script, a signal handler, an MCP call, a crash sweep. A
teardown that needs a password strands a `block` rule keyed on an address vmnet will later reassign.
So the unattended path is required, which forces `NOPASSWD`, which makes the authorized command set
the security boundary. Confirmed: `sudo -n` succeeds with no controlling tty and stdin closed (Q5).

**Why not `sudo yoloai …` with a privilege drop.** Mechanically available — `syscall.Setuid` returns
EPERM rather than ENOTSUP on darwin, so Go implements it — and yoloAI already has the sudo-awareness
layer (`fileutil.HostUID`, `ChownIfSudo`, `OwnershipAudit`). It dies on credentials instead: sudo
strips `ANTHROPIC_API_KEY` and `CLAUDE_CODE_OAUTH_TOKEN` (Q6, both confirmed stripped, and this
host's `env_keep` preserves HOME/MAIL/EDITOR/TZ/SSH_AUTH_SOCK but neither), and
`fileutil.SudoParentEnv` recovers them by reading `/proc/<ppid>/environ`, which does not exist on
macOS. `sudo yoloai new` would launch an agent that cannot authenticate. HOME itself survives, so
the paths were never the problem.

**Why not authorize a yoloAI subcommand.** `NOPASSWD: /usr/local/bin/yoloai _pf-sync` would move
rule generation into our own code. But `/opt/homebrew/bin` is user-writable — Homebrew's Apple
Silicon default, not a local quirk — and yoloAI on the test host resolves to `~/bin/yoloai`. A
`NOPASSWD` grant on a binary any user process can overwrite *is* passwordless root. Not reachable by
the agent we are containing (the seatbelt profile is `(deny default)`), but reachable by anything
else running as the user. `/sbin/pfctl` is the opposite: SIP-`restricted`, root-owned, unwritable
even by root.

**Why not load rules from stdin.** `pfctl -a <anchor> -f -` is the natural mechanism, and sudoers
cannot constrain stdin at all — it matches argv only. Measured consequence: nat and rdr rules **are**
installed when loaded into an anchor (Q2), and `/etc/pf.conf` references `com.apple/*` as
`nat-anchor`, `rdr-anchor`, `scrub-anchor`, `dummynet-anchor` and `anchor`, so they are evaluated by
the main ruleset rather than sitting inert. That grant is a traffic-interception primitive, not a
per-sandbox firewall knob.

## The mechanism

**Static rules, dynamic table membership.** Rules are loaded exactly once, at setup, by a
password-authenticated call. Everything per-sandbox is table membership, whose entire argument
surface is one address.

A fixed pool of slots is what makes this work. Per-sandbox allowlists mean per-sandbox *rules*, and
dynamic rule loading is what we just ruled out — so the rules are pre-loaded for N slots against
empty tables, and a sandbox is assigned a free slot at start:

```
table <yoloai_src_0> persist
table <yoloai_dst_0> persist
pass  in quick from <yoloai_src_0> to <yoloai_dst_0>
block drop in quick from <yoloai_src_0> to any
…repeated per slot
```

> **The direction and `quick` are load-bearing, and an earlier draft of this plan had them
> wrong.** It wrote `block drop out from <src>`, reasoning from last-match-wins. But pf applies
> NAT to outbound traffic, so a rule filtering `out` sees the *host's* address as the source and
> matches nothing — the sandbox is then wholly unfiltered, silently. The form that actually
> enforced in the spike is `in` on the bridge, where the packet still carries the guest's
> address, with `quick` making the `pass` win by appearing first. **The table variant above drops
> the spike's `on <ifc>` qualifier** (hazard 3 — interface names move), which is a deviation from
> what was measured and is what `pf_enforce.sh` exists to validate.

Empty tables match nothing, so unused slots are inert. Verified on hardware: a 16-slot pool loads
all **32 filter rules**, table add/show works at both ends of the pool, and — the part that actually
matters — **rule order is preserved**, with each `pass` landing after its `block`. pf is
last-match-wins, so had the order not survived the load, the allowlist would be inert and every
isolated sandbox simply cut off. That is a failure which looks exactly like working enforcement from
the outside, so it belongs in the conformance suite rather than in a one-off check.

**The slot count caps concurrent network-isolated sandboxes** — the one real cost of this design, and
it needs a decision on default size and on what happens when the pool is exhausted (refuse, or fall
back to today's behavior with the disclosure).

**IPv6 comes free, and closes an existing hole.** pf accepts `inet6` rules and **mixed-family
tables** — one table holding both v4 and v6, with an unqualified `block drop out from <table>`
covering both. The in-guest iptables allowlist leaves IPv6 entirely unfiltered today (it is in the
flag help), because `firewall.resolve_domains` requests `AF_INET` only. Confirmed at runtime that one
table holds `192.0.2.7` and `2001:db8::1` together, and that the grant permits adding both.

**This is not insurance — the gap is live on apple today.** Measured: an apple guest holds a
**global-scope IPv6 address** (`fd96:9d70:8059:3047::/64`, three `inet6` addresses in total) and a
**default IPv6 route advertised by the host itself** — the RA router's MAC is `bridge101`'s, and the
host carries an address in the guest's own /64. Neighbour discovery from the guest resolves that
host address to the bridge's lladdr and marks it `router REACHABLE`. So guest→host and guest→guest
IPv6 both work right now, entirely outside an IPv4-only allowlist, and on any host with upstream
IPv6 the guests would have unfiltered v6 egress as well — they are already configured for it and
only the host's missing upstream stops them. **tart guests have no IPv6 on `en0` at all**, so this
is apple-specific.

What is still owed is end-to-end blocking of real IPv6 *egress*, which cannot be measured on this
host: it has no IPv6 upstream (ULAs on `bridge100` and a Tailscale `utun`, `curl -6` returns 000).
Guest→host blocking over IPv6 is measurable here and should be in the acceptance test.

**Where the allowlist gets resolved changes, and that is a new failure mode.** The `dst` table holds
resolved addresses, exactly as the ipset does today — `resolve_domains` is one-shot at start with no
refresher anywhere, so CDN rotation already breaks long-lived sandboxes and this design neither
fixes nor worsens that. What *is* new: resolution moves from the guest to the host. A domain the
host and guest resolve differently — split-horizon DNS, geo/anycast variance, a VPN resolver on one
side only — yields a guest connecting to an address the host never put in the table, which is then
**blocked despite being allowlisted**. In-guest enforcement cannot have this failure because the same
resolver answers both questions. Needs either resolution parity (ask the guest, filter on the host)
or an explicit documented limit; it should not be discovered by a user whose allowlisted domain
intermittently fails.

### The authorization

Literal command path plus an anchored regex on arguments. Globs are unusable here: sudoers matches
arguments as **one concatenated string**, so `-a com.apple/yoloai*` also permits
`-a com.apple/yoloai -f /etc/pf.conf`. sudo's own manual recommends the regex form and notes its
ToCToU caveats "do not apply to rules where only the command line options are matched using a
regular expression."

```
<user> ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\.apple/yoloai -t yoloai_(src|dst)_([0-9]|1[0-5]) -T (add|delete|flush)( [0-9a-fA-F.:/]+)?$
```

**Measured against a live policy, in this exact form**
([pf-authz-final.txt](../research/macos-isolation-spike/results/pf-authz-final.txt), 27/27, no
failures). All five intended invocations permitted — v4, v6, CIDR, delete, and argument-less
`flush` — and **twelve refused**, split between the four widenings this regex introduces over the
narrower one first tested and the escapes any form must reject:

| Refused | |
| --- | --- |
| widenings | slot 16 (out of range), arbitrary table name, `-T kill`, `-T replace`, `-T add -f <file>`, a path where an address belongs |
| escapes | main-ruleset load, disable pf, flush all, a different anchor, a stdin ruleset, anchor `-F rules` |

The cold-cache control passed, so those refusals reflect policy rather than a warm sudo timestamp.

**Escaping: use the raw form.** `man sudoers` requires `:` and `\` escaped in command arguments but
prefixes that with "Unless a regular expression is specified", which leaves the `:` in an IPv6
charset and the `\.` genuinely ambiguous — and wrong in either direction fails silently. Both
variants were installed and tested: the **unescaped form parses and permits**; the double-escaped
form is not needed. Do not "fix" it later by adding escapes.

Three things this must get right, all of them load-bearing:

1. **`visudo -c` cannot validate it.** It accepted the safe and unsafe forms identically. So the
   line is **generated by yoloAI and verified by a permit/refuse matrix**, never documented for a
   user to hand-copy. A doc typo that widens the grant to passwordless root looks identical to the
   correct line to every validation tool on the machine.
2. **The regex must exclude `-f`.** `pfctl -T add -f <file>` is legal, so a `.*` argument form would
   reopen file-sourced loading. The `[0-9a-fA-F.:/]+` form above does not.
3. **Worst case is bounded by who holds the grant.** Its abuse ceiling is adding or removing
   addresses in yoloAI's own tables, which can only affect traffic yoloAI's own rules already match.
   The party holding it is the host user, who already has unrestricted network access — so this is
   not an escalation for them. That is precisely what the stdin form failed: NAT and redirect reach
   *other* traffic on the host, which is a capability the user did not previously have.

### What the enforcement keys on, per backend

| Backend | Key | Note |
| --- | --- | --- |
| apple | source address on the vmnet bridge | a second VM on the same bridge was unaffected — the discriminator that makes it per-sandbox |
| tart | source address on the vmnet bridge | same shape, same result |
| seatbelt | **gid owning the socket** | `pf`'s `user`/`group` selectors match **TCP and UDP only**, so non-TCP/UDP egress is unfiltered by construction — a materially weaker guarantee that must be stated in user-facing text, not discovered |

For seatbelt there is a second privileged step: `setegid()` to a supplementary group returns `EPERM`
**even for a member**, so the gid must be set by a privileged launcher rather than by the agent
process dropping into it. That is a second authorized command, and its argument surface (a gid and a
program path) is materially harder to constrain than an IP address. **Seatbelt should be its own
phase and may not be worth doing.**

## Hazards

Each produced a false result during the research before it was understood.

1. **Never touch the main ruleset.** `pfctl -f` *replaces* it, and that is where vmnet's NAT lives.
   The first harness silently destroyed egress for every VM on the host and then measured "blocked".
   Reloading `/etc/pf.conf` does not restore the NAT; only restarting the vmnet service does.
   Everything lives in a nested anchor under `com.apple/`.
2. **`quick` follows the direction keyword.** `block drop quick in on …` does not parse;
   `block drop in quick on …` does. A rule that fails to parse reads exactly like traffic that
   cannot be filtered.
3. **Key on the address, never the interface name.** vmnet re-picks subnets when a bridge is
   re-created and bridge indices move between backends (DF172), so a rule keyed on `bridge101` can
   silently attach to another backend's traffic after a restart.

## Ordering, and the enforcement window

The sandbox address does not exist at launch — it is read host-side after the guest comes up.
Measured on both address-keyed backends: apple's appears **2s** after `yoloai new` begins against
**3s** for the command to return; tart's at **8s** against **39s**. So the sequence

```
start guest → wait for address → add to table → VERIFY present → launch agent
```

is achievable synchronously in-process, with no post-start hook. Only the last arrow is a security
boundary; boot traffic before the rule exists is unfiltered, which is acceptable only because no
agent code has run yet. **If agent launch is ever reordered ahead of the table add, the guarantee is
void and nothing about the sandbox looks different.** That belongs in a test, not a comment.

## Reaping — mandatory, not housekeeping

Table membership is reconciled against live sandboxes on every yoloAI run, rather than being treated
as an append/remove log. A missed teardown is then self-healing at the next invocation, and there is
no reaper process to supervise. Teardown itself is a table delete — no rule reload, and it works
unattended (Q5).

Two measurements move this from tidiness to correctness
([lease-binding.txt](../research/macos-isolation-spike/results/lease-binding.txt)):

- **A sandbox's address changes across its own stop/start, on both backends.** apple went
  `192.168.64.22` → `.23`; tart went `192.168.65.2` → `.4`. So the address is re-read and the table
  rewritten at every start; **caching a sandbox's address is a defect**, not an optimization. This is
  not fail-open — the start path runs and installs the new address — but every restart leaves an
  orphan entry behind if stop did not clean up.
- **The address pool is consumed per START, not per sandbox.** tart's **MAC also changed** across the
  restart (`8a:cb:49:70:e1:7b` → `d2:f0:23:b:24:29`), so bootpd issues a fresh lease rather than
  honoring a binding. That explains the host's lease log holding 253 distinct MACs against 253
  distinct addresses with no address ever bound to two MACs — and it **refutes the inference drawn
  from that log**, which was that a MAC-keyed binding would preserve a sandbox's address. It does
  not, because the MAC is not stable either. Consumption is therefore one address per start, so a
  `/24` wraps far faster than "one per sandbox" suggests, and a wrapped pool *will* re-issue an
  address some stale `block` entry still names. The victim is a sandbox — or an unrelated VM — that
  is silently denied egress while looking healthy.

Note also that the two backends do not share an addressing mechanism: apple's address has **no
record in `/var/db/dhcpd_leases`** at all, so it is not bootpd-issued, while tart's is. They happen
to agree on the behavior that matters here, but that agreement was measured, not derived.

## Shape of the work

**Phase 1 — capability and setup.** Probe with `sudo -n /sbin/pfctl -a … -T show`; report through
`yoloai doctor` like any other host prerequisite, with a command that generates and installs the
policy plus the static slot ruleset. `pfctl -n -f -` validates a generated ruleset unprivileged
(exit 0 valid / 1 invalid, confirmed), so yoloAI verifies before spending a privileged call — note
it always prints the "Use of -f option" warning to stderr, which must be filtered rather than read
as failure.

**Phase 2 — apple and tart.** Slot assignment, membership add/verify/delete around the lifecycle,
reconciliation, and removing the `NET_ADMIN` grant on apple when host enforcement is active. With
the capability absent, tart keeps today's refusal and apple keeps today's weak path plus DF179's
disclosure, so nothing that works today stops working.

**Phase 3 — seatbelt.** The gid launcher, or a decision not to. See the table note above.

## Acceptance test

Every assertion pairs a **block** with a **positive control in the same run**. This is not
belt-and-braces: a sandbox stranded by the vmnet subnet re-pick has no network at all, so it passes
"egress to a non-allowlisted destination is refused" for free, on every destination — which silently
invalidated the first `pf` harness (DF172). A conformance case asserting only the denial certifies
nothing.

Per backend, in one run: an allowlisted destination **succeeds**, a non-allowlisted destination
**fails**, a second sandbox on the same bridge is **unaffected**, and after stop the address is
**gone from the table**. The tamper arm — the sandbox tries to remove the rule and cannot — is the
property the whole plan exists for and belongs in `runtime/runtimetest`, not a spike script.

## Unmeasured, and known limits

- **Fail-open on address change while running — closed for tart, open for apple.** A
  `block from <table>` rule stops matching if a sandbox's address changes, silently unfiltering it.
  tart's lease turned out to be **559s**, so renewal is constant rather than rare, and a 28-minute
  watch caught **five renewals** directly — visible as the lease-remaining sawtooth
  (`318→498`, `377→556`, `315→494`, `373→552`, `311→489`) — with the address unchanged across every
  one. That closes the renewal path for tart on this host, and it is a positive result rather than
  an absence of evidence, because the renewals are counted rather than assumed.
  **apple's equivalent is unmeasured**: it has no record in `/var/db/dhcpd_leases` at all, so it does
  not renew through bootpd and nothing here observed its address over time. Every apple address
  change seen (`.5`→`.2`, `.22`→`.23`) accompanied a restart, where the start path reinstalls anyway.
  Cheap mitigation regardless: re-verify membership on the existing `SandboxNetHealth` probe tick,
  which already runs and already knows the guest's current address.
- **IPv6 end-to-end blocking is unverified.** Mixed-family tables and the grant are confirmed; real
  IPv6 traffic being dropped is not, and whether the guests have IPv6 egress at all is unchecked.
- **Slot exhaustion behavior is undecided**, as is the default pool size.
- **Resolution parity** between host and guest (see above) is unresolved.

Now settled, recorded so nobody re-opens them: **`set skip` is NOT honored inside a nested anchor**
— re-measured after the first harness produced an unfalsifiable verdict, this time requiring the
companion block rule to have loaded before a negative is accepted. So the stdin form's excess
privilege was nat/rdr traffic interception, not a host-wide filter disable. It stays rejected on
that evidence; this only bounds how bad it was.

## Not in scope

The `apple`/`podman`/`containerd` `NET_ADMIN` grant on Linux, which is DF179's own problem and has a
different fix per backend.
