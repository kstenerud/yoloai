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
block drop out from <yoloai_src_0> to any
pass  out from <yoloai_src_0> to <yoloai_dst_0>
…repeated per slot
```

Empty tables match nothing, so unused slots are inert. A 16-slot ruleset (64 rules) parses clean.
pf's last-match-wins ordering makes the `pass` override the `block` for allowlisted destinations.
**The slot count caps concurrent network-isolated sandboxes** — the one real cost of this design, and
it needs a decision on default size and on what happens when the pool is exhausted (refuse, or fall
back to today's behavior with the disclosure).

**IPv6 comes free, and closes an existing hole.** pf accepts `inet6` rules and **mixed-family
tables** — one table holding both v4 and v6, with an unqualified `block drop out from <table>`
covering both. The in-guest iptables allowlist leaves IPv6 entirely unfiltered today (it is in the
flag help). Parse-verified only; runtime confirmation is owed.

### The authorization

Literal command path plus an anchored regex on arguments. Globs are unusable here: sudoers matches
arguments as **one concatenated string**, so `-a com.apple/yoloai*` also permits
`-a com.apple/yoloai -f /etc/pf.conf`. sudo's own manual recommends the regex form and notes its
ToCToU caveats "do not apply to rules where only the command line options are matched using a
regular expression."

```
<user> ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\.apple/yoloai -t yoloai_(src|dst)_([0-9]|1[0-5]) -T (add|delete|flush)( [0-9a-fA-F.:/]+)?$
```

Measured against a live policy: both intended commands permitted, and **seven escapes refused** —
main-ruleset load, disable pf, flush all, a different anchor, a stdin ruleset, table-from-file, and
table kill. The cache control passed, so those refusals reflect policy rather than a warm sudo
timestamp.

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

- **A sandbox's address changes across its own stop/start.** apple went `192.168.64.22` →
  `192.168.64.23` over one stop/start cycle. So the address is re-read and the table rewritten at
  every start; **caching a sandbox's address is a defect**, not an optimization. This is not
  fail-open — the start path runs and installs the new address — but it does mean every restart
  leaves an orphan entry behind if stop did not clean up.
- **Addresses are handed out incrementally and the pool is finite.** apple walked `.20`, `.22`,
  `.23`; the host's historical lease log holds 253 distinct addresses across the vmnet subnets. A
  pool that increments and wraps *will* re-issue an address that a stale `block` entry still names,
  and the victim is then a sandbox — or an unrelated VM — that is silently denied egress while
  looking healthy. On a long-lived host this is a certainty with a horizon, not a residual risk.

Note the two backends do not share a mechanism: apple's address has **no record in
`/var/db/dhcpd_leases`** at all, so it is not bootpd-issued, while tart's is. Nothing about
addressing should be generalized from one to the other.

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

- **Fail-open on address change while running.** A `block from <table>` rule stops matching if a
  sandbox's address changes, silently unfiltering it. Not observed while running: 120 samples over
  240s of induced bridge churn, through a full tart bring-up, held one address with working egress.
  That is a **negative result, not stability** — it bounds the risk over minutes, not over the days a
  sandbox lives. Every address change actually observed (`.5`→`.2`, `.2`→`.3`, `.22`→`.23`)
  accompanied a **restart of the sandbox**, where the start path re-runs and reinstalls — so the
  dangerous variant needs an address to move underneath a sandbox nobody restarted, which nothing has
  yet produced. **Lease renewal on a long-running sandbox remains untested**, and tart's lease was
  short enough to be near expiry within minutes, so renewal happens constantly rather than rarely.
  Mitigation if it proves real: re-verify membership on the existing `SandboxNetHealth` probe tick,
  which already runs and already knows the guest's current address.
- **Is `set skip` honored inside an anchor?** Unmeasured — the harness's own verdict was invalid
  (`grep -c` prints `0` *and* exits 1, so `|| echo 0` produced `0\n0` and the comparison errored into
  the not-honored branch unconditionally). Fixed, and moot for this plan, since the stdin form is out
  on Q2's evidence alone. It would matter again if anyone revives `-f -`.
- **IPv6 enforcement is parse-verified only**, not runtime-verified.
- **Slot exhaustion behavior is undecided**, as is the default pool size.

## Not in scope

The `apple`/`podman`/`containerd` `NET_ADMIN` grant on Linux, which is DF179's own problem and has a
different fix per backend.
