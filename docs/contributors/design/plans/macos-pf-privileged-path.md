> **ABOUTME:** Build plan for enforcing `--network-isolated` from host `pf` on macOS, where the
> allowlist today is either weak (apple grants the guest NET_ADMIN) or absent (tart, seatbelt).
> Covers how yoloAI acquires the privilege `pf` needs without installing a root daemon.

# macOS: enforce the network allowlist from host `pf`

- **Status:** PLANNED — mechanism, authorization and enforcement measured on hardware
  2026-08-02/04, **revised 2026-08-09 against a completed verification pass** (M1–M8 plus four
  unqueued extras, run against a parallel Linux pass), and **re-run the same day against an audit
  that found three of its conclusions unsupported**. Nothing built. The pass added two required
  mechanisms — a bridge-scoped default-deny and connection-state teardown — and settled the
  verification, polling and pool-size questions with numbers. Two of those numbers then changed:
  connection-state teardown appeared to need a `pfctl -k` grant, and **the pool figures were 2×
  too high**, which reopens the 8-slot decision. **Then the enforcement plan was rewritten onto an
  interface key (2026-08-10), and a further pass withdrew the `-k` requirement entirely** — see
  § *Post-rewrite verification*, which supersedes the `-k` half of § *Audit remediation* and of
  § *What the verification pass settled*. Read the post-rewrite section before quoting either.
- **Depends on:** tamper-resistant-network-isolation.md, host-controlled-agent-launch.md
- **Decision:** [D132](../../decisions/working-notes.md#d132--macos-pf-is-driven-through-a-generated-nopasswd-sudoers-grant-that-authorizes-pf-table-membership-and-nothing-else) — the mechanism and the five rejected alternatives. Cite that, not this file.
- **Rides:** **any** — the user-visible surface only gains capability. `--network-isolated` becomes
  *accepted* on tart where it is refused today (newly-accepted input is not a break), and on apple
  it becomes stronger without changing its spelling. Seatbelt is unchanged. The `BackendCaps` change
  is internal. If any phase ends up *withdrawing* isolation somewhere, that half is **breaking** and
  needs a `docs/BREAKING-CHANGES.md` entry.

## AUDIT REMEDIATION — 2026-08-09, all three items re-run

An independent re-derivation of every conclusion against the raw runs found three items here that did
not follow from their data. All three have now been re-run on hardware. **Two were wrong in the
direction the audit predicted; one was a measurement artifact.** The bodies below carry the corrected
statements; this table is the index.

| Claim | Resolution |
| --- | --- |
| X2: "a return-direction rule stops it… prefer that to amending D132 for `pfctl -k`" | **SUPERSEDED 2026-08-11 — no `-k` grant is needed at all; see § *Post-rewrite verification*.** The row below is the 2026-08-09 finding and was correct against the stateful shape. **CONFIRMED WRONG.** The missing arm was run (`pf-revocation.txt` R5a). Rule alone, kill removed: the transfer **survived**, 54→108 bytes, four states intact. Rule + kill: 45→45, stopped. The two are necessary *together*. The cheap fix does not exist. Both escapes were then measured and both are closed: an anchor accepts `set timeout` and **ignores it**, and a timeout cannot reach a busy state anyway (0s decay in 20s against an idle control's full 20s); `-K` kills **source tracking entries, not states**, so it was never a candidate. What does work is the two-host form `-k <guest> -k <gateway>`, which names both endpoints and so can be pinned in the grant. See § *What the verification pass settled*, and D132's 2026-08-09 remediation amendment. |
| M8's pool-cost figures, and the 8-slot decision drawn from them | **ARTIFACT, resolved.** The harness timed `sudo -u <user> -H sudo -n pfctl` — two nested sudo invocations per call, the drop *inside* the timed region. Both shapes run back to back: the difference is **7.95 ms/call** against 7.9 ms for one sudo. Corrected: **93 / 158 / 297 ms**, i.e. 14 / 25 / 46% of a container create. The audit's predicted "~47% not 99%" was right. **The 8-slot decision is open again.** |
| M3 detector C, recommended and adopted | **CONFIRMED, fixed, and re-priced.** Both fault classes induced, with the old sentinel run beside the new one: guest cut off — old HEALTHY, new UNKNOWN; container gone — old HEALTHY, new UNKNOWN. The fix needs three probes, so the canary costs **320–385 ms, not 83–105**, with a 15.3 s worst case against a dropping path. Its policy mutation and acquisition race are now stated as adoption conditions. |

Both housekeeping items are also closed. **M4's log predicate now has the positive control the notify
half always had**, and it changed the finding's *scope*: the query and predicate do match a message
emitted inside the window, so M4's silence is real — but `process == "pfctl"` matched **zero** entries
for a deliberate pfctl invocation, at default and at `--info --debug`. That clause is inert on this
host, so M4's negative rests entirely on its two `eventMessage` clauses and is narrower than the
predicate's shape suggests. And **`pf-spoof-run2/3.txt` are renamed to `*-invalidated.txt`** with their
causes recorded in the results README: run 2 measured a sandbox with no `CAP_NET_ADMIN`, run 3 had root
but a `PATH` lacking `/sbin`. One capability bit and one PATH entry, each producing a confident, fully
passing inverse of the truth.

### And the cross-platform question, re-asked (new)

Linux's refutation of "there is no stable per-sandbox non-address key" prompted re-asking it here.
**macOS gets a split answer** (`pf-interface-key.txt`), and the deciding half is new:

- **A held index is stable.** Four concurrently-held networks hold four distinct bridges, and two more
  created against them collided with none. The allocator does recycle — a released index came back in
  the control — so that negative is not free. K4c's "the index recycled" measured recycling *after
  release*, which was never the question.
- **But an ordinary restart releases it.** A network has no host bridge until a container attaches and
  loses it on detach, so a sandbox that merely restarts opens a window. A new sandbox started in that
  window **took the restarting sandbox's index**, and the original reclaimed it on return. A claim-time
  read is therefore *not* sufficient, and the reason has nothing to do with K4c.
- **The ingress tag is genuinely per-sandbox.** With one network per sandbox, a tag applied on the
  bridge and matched on egress blocked one guest and not the other under a ruleset containing **no
  address at all** — the same shape Linux's K1 established.
- **And a rule re-attaches by name when its interface returns.** This is what decides whether the
  window above is a window or a permanent lapse, and it was the open question when the paragraph
  below was first written. A sandbox restarted alone comes back on its own index and the loaded rule
  still enforces with **no reload** — denied still blocked, allowed still reaching, and the guest's
  *address* changed underneath a rule that never named it. pf resolves the interface at match time.
  It also accepts rules naming an interface that does not exist yet, so pre-loading is possible.

**Correction to the first write-up of this section: the platforms are much closer than it said.** It
concluded "neither the interface nor the tag is usable alone… nothing measured here is [a key that
survives detach]". With I4 measured, that is wrong. Everything a key needs works here — held indices
are never reassigned, the interface discriminates with no address in the rule, the tag is
per-sandbox, and rules survive their interface cycling.

**And the one hazard is now priced rather than named (I5).** It is worse than "worse than not
enforcing", and in a specific way. With the two sandboxes given *disjoint* allowlists — which is what
makes a leak observable, since with no policy every destination answers — a stranger that took a
departed sandbox's index **reached the destination only that sandbox was granted, and was refused the
destination its own allowlist permits.** A cross-sandbox privilege leak of X3's class *and* a denial
of the sandbox's real policy, from one un-withdrawn rule. The mechanism is visible in the rule dump:
pf is first-match-with-`quick`, the stale `pass` wins for the departed sandbox's destination, and the
stale `block` then catches everything else before the stranger's own `pass` is ever reached. Nothing
about this is visible to an inspection — every rule is present and well-formed.

**The remedy is measured, not advised (I5b).** Withdrawing the stale rule restores the stranger's own
policy exactly. So the price is one lifecycle rule: withdraw a sandbox's rule when its interface goes,
re-read the real index when it returns. Linux needs no such rule because its names do not recycle;
macOS needs one because its indices do. That is a lifecycle requirement, not a missing key, and a far
smaller gap than the claim that closed this investigation on macOS.

> **Unrelated defect found while measuring this, and it is not a pf problem.** When the restarting
> sandbox reclaimed its index, the sandbox that had taken it **lost its egress entirely** — still
> `state=running`, no policy loaded, its gateway on no host bridge, unable to reach a destination the
> other sandbox reached from the same host at the same moment. A running sandbox silently losing its
> network because an unrelated sandbox restarted is a backend defect independent of every keying
> question on this page, and it is reproducible — three consecutive runs.
>
> **Filed since as DF190 by the Linux pass**, and now diagnosed: it reproduces with zero rules
> of ours loaded, so it is a defect in Apple's `container` and not ours, and its cause is a network
> object outliving its last container and re-attaching onto an index handed out in the meantime.
> Deleting the network when its last sandbox goes avoids it. A stop/start does **not** recover the
> victim — it moves the defect to the other sandbox. See `df190-mechanism.txt`; the original
> observation is `pf-interface-key.txt` I2c.

## Post-rewrite verification — 2026-08-10/11

The enforcement plan was rewritten onto a host-side interface key
([`enforcement-state-reaping.md`](enforcement-state-reaping.md), 2026-08-10) and named five things
for hardware. All five ran. Two of this file's standing conclusions do not survive them.

| Question | Result |
| --- | --- |
| Does pf's `no state` give the Cilium shape, removing the state-teardown asymmetry? | **Yes, and the `-k` requirement is withdrawn.** But not for the reason the question assumed, and the shape costs more than a keyword — see below. |
| Pin the `-k` peer to our gateway, under per-sandbox gateways? | **Moot.** Nothing needs `-k`. The tension between "pin the peer" and per-sandbox networks dissolves with it. |
| DF190's mechanism, and who owns it | **Apple's.** It reproduces with zero rules of ours loaded, and a network object outliving its last container is the cause (`df190-mechanism.txt`). |
| Is tart genuinely out of reach? | **Yes, and not for the stated reason.** tart *does* give each VM its own host-side `vmenetN` in both shared NAT and Softnet; what fails is pf, which evaluates on the shared bridge. A rule `on <member>` blocks nothing (counter 0) while the same rule on the bridge blocks both (counter 12), and OpenBSD's `received-on` is a syntax error here. So tart is unreachable because of **macOS pf**, not because of tart's networking — which leaves tart's own `--net-softnet-allow`/`-block` as its only enforcement surface, untested (`tart-net-key.txt`). |
| The lifecycle rule, end to end | **Its withdraw half works and wins its race by 783 ms**; its re-read half could not be exercised (`pf-lifecycle.txt`). |

**Revocation no longer needs a state kill, and this file's `-k` prescription is withdrawn**
(`pf-no-state.txt`). `no state` on the ingress rule alone changes nothing: every rule here is `in` on
the bridge, the host's reply travels `out`, matches no rule, meets pf's default pass — and a passed
packet creates state, which is bidirectional and carries the guest's forward packets past rule
evaluation. That mechanism was written into `pf_revocation.sh`'s own R5 notes and never followed up.
Adding a return rule scoped to the allowlist reaches a genuine **zero-state census under a live
transfer**, and revocation *still* fails, because every block here is also `in` — so the download
direction is **never evaluated against a block at all**. Only the complete bidirectional shape
revokes, and the 2×2 says both ingredients are necessary and neither sufficient:

| | no egress block | with egress block |
| --- | --- | --- |
| **stateless** | survives | **stops** |
| **stateful** | survives | survives |

Measured on both destination classes with a control each, including the NAT'd path, where the
translated state on `en0` — created outside our anchor by rules we do not own — survives the
revocation and does not rescue the flow. **Revocation is then a `-T delete` and nothing else, which
D132 already permits.** So the amendment this file called for is not needed, and the security
boundary gets *narrower* rather than wider.

**But the interface key does not fit D132's grant as written, and the fix is a pool inversion**
(`pf-grant-matrix.txt`). The grant is defensible because the unprivileged side changes table
membership and reloads one pinned file root wrote; it can never author rule text. Interface-keyed
rules name a bridge, and indices are dynamic, per-sandbox, and change across a restart. All three
routes to installing such a rule are closed, as they should be. The fix: **invert the pool so there
is one slot per bridge index rather than per sandbox** — the pinned file enumerates every index the
host could hand out, each with its own table, and claiming a sandbox means adding its allowlist to
the table named after the index it landed on. Rules stay static, the key stays the interface, and the
unprivileged side still only touches membership. Measured: 164 rules covering `bridge100`–`bridge140`
load, and two real sandboxes get independent policy with no rule text written. **The grant needs its
table regex widened to the index range** — a pattern change, not a model change. pf interface groups
would have been cleaner and macOS does not have them (`ifconfig` answers `group: bad value`).

**Two consequences this file must carry forward.** A sandbox landing on an index outside the
superset's range meets no rule and is **silently unenforced — fail-open**, so the range needs a
preflight assertion that does not exist. And `block drop out` on the bridge denies host-initiated
traffic to the sandbox unless the source is allowlisted, so every sandbox's allowlist must contain
its own gateway for credential injection to work at all.

**§ *Verification is mandatory*'s V4 UNKNOWN is answerable after all.** It could not decide whether a
grant was refused because it ran under a blanket harness grant. The method that works: run the check
as root, remove the blanket for its duration — root keeps its authority because it already is root —
and refuse to report anything until `sudo -n /usr/bin/true` is shown to fail. `pf-grant-matrix.txt`
does this and returns a clean 7-permit / 9-refuse matrix.

## Why this exists

On macOS, `--network-isolated` is either weak or absent. `apple` installs the allowlist inside the
sandbox and grants it `NET_ADMIN`, so the agent can flush its own rules (DF179). `tart` and
`seatbelt` refuse it outright, so they have none at all.

Host `pf` fixes this. It enforces per-sandbox, keyed on something the guest cannot change, and it
holds against a guest that tears down its own firewall first (`pf-p4-tamper.txt`). What blocks it is
one thing: **`pf` needs root, and yoloAI on macOS has no privileged path.**

Evidence for everything below is in
[macos-isolation-spike/results](../research/macos-isolation-spike/results/); the surrounding design
is [tamper-resistant-network-isolation.md](tamper-resistant-network-isolation.md) § The macOS half.

## The decision

**Recorded as [D132](../../decisions/working-notes.md#d132--macos-pf-is-driven-through-a-generated-nopasswd-sudoers-grant-that-authorizes-pf-table-membership-and-nothing-else), which is the citable form.** This section is the working
statement; D132 is what survives this plan's archival, because the rejected alternatives are the
answer to "why sudo?" and rule 8 sends a built plan to `archive/`, which is not a specification.

**A generated `NOPASSWD` sudoers grant, authorizing pf *table membership* and nothing else.**
Installing it is the opt-in — no flag, no isolation mode, no runtime prompt.

**Why not a runtime prompt.** Teardown has no tty: `yoloai stop`, signal handlers, MCP calls, crash
sweeps. So `NOPASSWD` is forced, which makes the authorized command set the security boundary.

**Why not `sudo yoloai …` with a privilege drop.** It cannot run unattended either — every lifecycle
command touching pf would need `sudo`, including `stop` — and it puts the agent-facing process at
root, so a sandbox escape escapes to root. (An earlier draft rejected it on credentials; that was
wrong. `sudo -E`, or `Defaults env_keep +=`, surmounts the stripped API keys, and `sudo -E yoloai`
is already an established invocation on Linux. It is a papercut on a model rejected for the two
reasons above.)

**Why not authorize a yoloAI subcommand.** `/opt/homebrew/bin` is user-writable on Apple Silicon, so
a grant on the yoloAI binary is passwordless root for anything running as the user. `/sbin/pfctl` is
SIP-`restricted` and unwritable even by root.

**Why not let the grant load rulesets at all.** This decision picked the mechanism, and it was
measured both ways:

- A grant that can load a ruleset **can void every sandbox's filtering**. Loading `pass in quick all`
  into any anchor the grant can write took a sandbox from blocked to reachable (`pf-shapea2.txt` C2,
  `before=000 after=301`). `man pf.conf`: a `quick` match *"aborts the evaluation of the rules in
  other anchors and the main ruleset"*. That is a host-wide filter-bypass primitive.
- A grant restricted to **table membership cannot express it**. Nine bypasses were attempted against
  the table-only grant — load a ruleset, load into a sub-anchor, flush, disable pf, kill a table,
  reach another anchor — all refused by policy (`pf-shapeb.txt` B3, `pf-assumptions.txt` D5).

So the rules are static and only membership moves. The rejected alternative, per-sandbox sub-anchors
loaded from stdin, is recorded at the end.

## The mechanism

**A fixed pool of slots. Static rules, dynamic table membership.**

Setup, once, privileged, interactive: load a slot ruleset into `com.apple/yoloai`, and write it to a
root-owned file so it can be restored later.

```
table <yb_src_0> persist
table <yb_dst_0> persist
pass  in quick from <yb_src_0> to <yb_dst_0>
block return in quick from <yb_src_0> to any
…repeated per slot
```

**`block return`, not `block drop` — changed 2026-08-08 on measurement.** The two enforce
identically: allowed reaches, denied refuses, empty `dst` fails closed, on **both** apple and tart
(`pf-canary-probe.txt` C3, `pf-mixed-backend.txt` M2). What differs is how a refusal is delivered.
`drop` discards silently, so a blocked connection hangs until the client's own timeout; `return`
sends an RST and answers **immediately** — measured 0.00s against 1.00–3.01s for the same probe.

That decides two things at once:

- **It is what makes the verification probe affordable.** With `drop` the probe waits out a timeout
  on *every sandbox start* — ~1s against a 2.4s start. With `return` it costs a round trip.
- **It is a better experience for the thing being sandboxed.** Today an agent that hits a
  non-allowlisted domain hangs until something times out, which burns agent time and looks like a
  network fault. `return` gives it an immediate connection refused, which is the truth.

`drop` buys stealth, and there is nobody to hide from here: the agent knows it is sandboxed, and the
allowlist is the user's own policy.

Per sandbox at start: claim a free slot, `-T add` its address to `yb_src_N` and its resolved
allowlist to `yb_dst_N`. At stop: `-T delete`.

**`in quick` is load-bearing.** `in` on the bridge is evaluated **before** NAT, so the packet still
carries the guest's address; `quick` makes the `pass` win by appearing first. A `block drop out` form
sees the host's post-NAT source, **matches nothing, and leaves the sandbox wholly unfiltered while
loading cleanly** (`pf-enforce.txt` E1 ran both candidates in one pass). An earlier draft of this
plan proposed the `out` form.

Measured: 32 slots load (64 rules), a **high** slot index enforces, an unassigned sandbox is
untouched, an empty `dst` table fails **closed**, and **table contents survive a ruleset reload** —
so resizing or repairing the pool does not de-isolate running sandboxes (`pf-assumptions.txt`
D1/D2/D4, `pf-shapeb.txt` B1/B2).

**Independence is measured at n=8, not n=2.** The original claim came from two slots, which cannot
distinguish "each sandbox has its own policy" from "each has the union" in the direction that would
matter. `pf-pool-occupancy.txt` runs eight live sandboxes with eight distinct allowlists and the
full 64-cell matrix: **every sandbox reached its own destination and all 56 cross-sandbox paths were
refused.** The same run establishes two things the start path needs — the one-call occupancy dump
names exactly the occupied slots, which is how a free slot is found, and it reports FULL at
exhaustion, so a full pool need not be discovered by a failed write.

**Backend-agnostic, measured rather than predicted.** The pool enforces on **tart** as well as
apple, and an apple guest and a tart guest hold **different allowlists in different slots
simultaneously**, on separate vmnet bridges, with teardown by table delete restoring either
(`pf-tart-pool.txt` T1/T2/T3). Nothing in the design distinguishes the backends because the rules
key on source address; that was a prediction until this run.

**And it is no longer one guest per backend.** Those two results were orthogonal — `pf-tart-pool.txt`
had two guests across two bridges, `pf-pool-occupancy.txt` had eight guests on one bridge — so
neither covered many guests across bridges, which is exactly what a per-bridge assumption would get
wrong. `pf-mixed-backend.txt` crosses them: **three apple guests (192.168.64.x) and two tart guests
(192.168.65.x) in one five-slot pool, five distinct allowlists, every diagonal reached and all 20
off-diagonal paths refused — zero cross-backend leaks.**

**Pool size and exhaustion are open.** 32 was measured; the cap is this design's one structural cost.
What happens when the pool is full — refuse, or fall back to today's behaviour with DF179's
disclosure — is undecided.

### The grant

```
<user> ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\.apple/yoloai -t yb_(src|dst)_([0-9]|[12][0-9]|3[01]) -T (add|delete|flush|show)( [0-9a-fA-F.:/]+)*$
<user> ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\.apple/yoloai -f /etc/yoloai/pf-pool\.conf$
<user> ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\.apple/yoloai -s rules$
<user> ALL=(root) NOPASSWD: /sbin/pfctl ^-s info$
```

Validated as a unit (`pf-assumptions.txt` D5/D7 — the tested anchor root was `com.apple/yoloai_b`,
otherwise identical). Permitted: membership add/delete/flush/**show**, **multiple addresses in one
call** (so a 40-IP allowlist is not 40 `sudo` invocations), restore from the pinned file, and reads
of the ruleset and pf's enable state. Refused: an out-of-pool slot, an arbitrary table name, a
ruleset from stdin, a ruleset from any other file, an `-f` smuggled where an address belongs,
`-T kill`, `pfctl -d`, `-F all`. Cold-cache control passed.

Four things that must not be "tidied":

1. **Reads may name the anchor; writes are membership-only.** `-s` cannot modify anything, and
   reaping and verification both need reads. The dangerous grant is the *ruleset* write.
2. **The one ruleset write is pinned to a literal root-owned path.** `/etc/yoloai/pf-pool.conf`,
   `root:wheel 0644` in a `root:wheel 0755` directory — confirmed not user-writable. That is what
   makes unattended reboot recovery possible without handing over ruleset loading.
3. **Globs are unusable.** sudoers matches arguments as one concatenated string, so `-t yb_src_*`
   would also permit `-t yb_src_0 -T kill`. Regex only.
4. **Do not add escapes.** `man sudoers`: *"There is no need to escape sudoers special characters in
   a regular expression other than the pound sign."* And **`visudo -c` cannot validate any of this**
   — it accepts the unsafe glob and the safe regex identically. The policy is generated by yoloAI
   and verified by a permit/refuse matrix, never documented for a user to hand-copy.

## Reaping is a security requirement

**A stale entry does not merely block — it grants reach the sandbox was never configured for.** With
a stale entry naming its address in another slot, a sandbox **lost its own allowlist and inherited
the stale slot's** (`pf-assumptions.txt` D3, with a control establishing it had exactly its own
policy immediately before). `quick` is first-match over rules keyed on a recycled address.

This is **not** a cost of the slot pool: an orphaned *sub-anchor* does the identical thing
(`pf-stale-a.txt` SA2). It is a property of address-keyed enforcement and applies to any shape.

It is reachable because **addresses recycle on every start** — apple `.22`→`.23`, tart `.2`→`.4` with
a new MAC, so the pool is consumed per start, not per sandbox.

That came from `lease-binding.txt` L2, which measured it and drew the cached-address conclusion at the
time. A controlled rerun (`restart-control.txt`) replicates it on both backends and adds the mechanism
and how much worse it gets over a host's lifetime — both of which matter to reaping:

- **The MAC is regenerated by `tart run` itself**, on every start, written into the VM's own
  `config.json` — yoloAI passes no MAC flag. To vmnet's DHCP server each start is a new host, so a
  lease is burned per start and `/var/db/dhcpd_leases` grows monotonically. Full entry:
  [`backend-idiosyncrasies.md`](../../backend-idiosyncrasies.md).
- **On the test host that pool is already exhausted** — 253 records covering all of
  `192.168.65.2`–`.254`, zero free, and the file survives reboots so it does not heal. Past that
  point every start *necessarily* recycles an address a previous VM held. Recycling stops being a
  thing that might happen and becomes the only thing that can happen, which promotes the stale-entry
  hazard above from unlikely to routine on any long-lived host.
- **The two backends do not share a pool.** Apple holds zero records in that file; its vmnet plugin
  allocates from its own store, sequentially, and appears to restart from the low end after a host
  reboot. So neither backend's address behaviour may be generalised to the other, and reconciliation
  must not assume a single allocator's semantics.

None of this changes the remedies below — they were already keyed on live state rather than on stored
addresses, which is the property that survives all of it. It changes the *severity*: reconciliation
is not a tidy-up, it is the only thing standing between a recycled address and a misapplied allowlist.

Therefore:

- **At start, delete this sandbox's address from every `src` table before adding it to its own.**
  Deleting a non-member is a no-op, so this is deterministic regardless of stored state, and it
  closes D3 without depending on teardown having succeeded.
- **Reconcile on every run**: any address in a `src` table that no live sandbox holds is removed.
  Orphans are identified **by address**, not by name — which avoids the ambiguity
  `runtime/orphan.go` documents (`yoloai-acme-probe` is both principal `acme`'s sandbox `probe` and a
  legacy sandbox named `acme-probe`, DF125). Any design that names orphans re-creates that finding.
- The sweep holds no lock. Two ordering rules make it safe without one: write the sandbox record
  before its membership, and in the sweep **enumerate membership before reading records**. Then an
  address seen in a table had its record written earlier, so a live sandbox is never swept, and one
  created mid-sweep is not in the enumeration.
- **Reconciliation must key on RUNNING, not on record-exists.** A stopped-but-not-destroyed sandbox
  keeps its record, so a "record gone" predicate never reaps its stale entry.
- Per-sandbox `flock`s already exist (`store.AcquireLock`, taken in create/start/stop/destroy/reset),
  so same-sandbox races are closed. **Slot allocation is cross-sandbox and needs its own lock.**

## Verification is mandatory

**Membership without rules fails open silently** — with the ruleset flushed and the address still in
its table, the sandbox is unfiltered and nothing distinguishes it from working isolation
(`pf-assumptions.txt` D6; the sub-anchor design fails the same way, `pf-shapea.txt` S5).

So the start path verifies, all granted and read-only:

1. **pf is enabled** — `pfctl -s info`. `/etc/pf.conf` states pf is not auto-enabled and is
   reference-counted; with no holder every rule is inert. (`pfd` holds the token today, and holds it
   again across a reboot without anyone asking — `reboot-post.txt` P3. The check stays: what a boot
   restores, a `pfctl -X` elsewhere on the host can still take away.)
2. **the pool ruleset is loaded** — `-a com.apple/yoloai -s rules`, expected rule count.
3. **this sandbox's address is in its slot** — `-T show`.

Failing any is an error. Note (3) asserts *presence*, not that the address is still the sandbox's
current lease; comparing against the live address closes the remaining staleness gap and is free.

### Those three are not sufficient, and it is measured (2026-08-07)

**All three pass while the sandbox is completely unfiltered.**
[`pf-anchor-eval.txt`](../research/macos-isolation-spike/results/pf-anchor-eval.txt) prints them
beside live egress on a host in that state:

```
D132 check 1  pf enabled ............ Enabled
D132 check 2  pool loaded ........... 8 rules (want 8)
D132 check 3  our address in slot ... 192.168.64.13
==> all three checks PASS
ACTUAL egress: allow=301 deny=301
```

The cause is one line in the **main** ruleset. `/etc/pf.conf` carries `anchor "com.apple/*"`, which
is what makes pf descend into `com.apple` and evaluate its children. Remove it — `pfctl -F all` does,
and a backend restart afterwards restores only vmnet's own rules — and the anchor keeps every rule,
keeps every table, and is never consulted. This is D6's fail-open in its worst form, because D6 at
least left something to notice; here every yoloAI-visible signal reads healthy.

**The grant cannot see it.** It authorizes `-s info` and `-a com.apple/yoloai -s rules`, and neither
reveals whether the anchor is reachable from the main ruleset. Reading the main ruleset is not
granted — deliberately, since it is the object hazard 1 forbids touching. So this is not merely a
missing check; it is a check **the current security boundary cannot express**.

Three ways out, and the third is the one to take:

1. **Grant a read of the main ruleset** (`pfctl -s rules`, no `-a`). Small, read-only — but it
   discloses the host's entire filter policy to the grant holder, which is a real widening for a
   check that would still be a proxy.
2. **Grant `-s Anchors`** to confirm the anchor is enumerable. Cheaper, and **wrong**: our anchor
   was enumerable the whole time it was being ignored. It would have passed.
3. **Probe the behaviour, which needs no grant at all — built and validated 2026-08-08.**
   Enforcement is a property of packets, and every proxy for it has now been observed passing while
   the property was false. The probe cannot invent a source address, so it rides the window rule 1b
   already creates: during acquisition the claimed slot's `dst` is briefly **empty**, and an empty
   `dst` fails closed (D4). So *with our address in `src` and `dst` empty, a connection from the
   guest must fail* — and if it succeeds, the rules are not being evaluated.

   Measured ([`pf-canary-probe.txt`](../research/macos-isolation-spike/results/pf-canary-probe.txt)):
   it discriminates in both directions on a healthy host (empty `dst` → blocked; populated → 301 in
   0.03s), and on the broken host it **reached the destination through an empty allowlist in 0.03s
   while all three checks above reported healthy**. It is the check the design needs and it costs no
   privilege at all.

**Whatever is chosen, the verification list is wrong as written and must not ship as three checks.**
The research harness made exactly this mistake in miniature: `pf_pool_occupancy.sh` reported 56 of 56
cross-sandbox leaks and blamed the slot design, because its gate proved the guest *had* a network
and never that pf *would* block. It now fires one confirmed block before it trusts anything — which
is option 3, arrived at by being burned.

### The repair, since a detector implies one

Measured, and the **order matters**: reload `/etc/pf.conf` to restore the anchor reference, *then*
restart the backend to restore vmnet's NAT. Reloading alone leaves guests with no network;
restarting alone leaves the anchor unreferenced, which is the broken state itself. A backend restart
also moves every guest's address, so membership must be rebuilt afterwards — consistent with this
plan's existing rule that membership is rebuilt from live state and never restored from a stored
mapping.

### Reboot

**Measured on a real restart** (`reboot-post.txt`, controlled by a pre-reboot snapshot — the file is
rewritten in place by each round, so this cites the round taken 2026-08-04 11:30, `pass=15 fail=1`):

- **Nothing in the anchor survives.** Rules went 16 → 0 and every `src` table emptied. Restoring the
  pool at boot is mandatory, not defensive — this was the plan's largest asserted premise and it
  holds.
- **pf comes back enabled on its own**, with the same three reference rows as before the reboot. So
  a boot does not leave the host in the inert state `/etc/pf.conf` describes, and yoloAI holding its
  own `-E` reference is a robustness question rather than a prerequisite.
- **The main ruleset returns byte-identical** (same count, same `pfctl -s rules` sha).
- **The pinned file and the sudoers grant survive unchanged**, which is what makes the next line
  possible.
- **Recovery runs unattended after a real reboot.** The pool reloaded from the pinned root-owned
  file via `sudo -n` with no tty, rc=0, full rule count (also measured without a reboot in
  `pf-assumptions.txt` D7). Membership must then be re-added for live sandboxes.

- **Enforcement is restored end to end, in two slots, after a real restart.** The post half restarts
  the guests, re-adds their *current* addresses, and both slots filter on their own allowlists
  (`allow=301 deny=000` each). Round 1 could not test this — nothing was running — and the pool
  restore alone had been the only measured half. Round 2 first showed it and round 3 reproduced it;
  cite round 3, as round 2's output was overwritten before it was committed.

**Addresses move across a reboot on both backends, and that is now measured rather than assumed.**
Round 2 had reported both apple guests coming back on the addresses they went down on and called it
preservation; a no-reboot control (`restart-control.txt`) then moved every guest on both backends
with a plain stop/start, leaving two explanations the run could not separate — preservation, or an
allocator counting from the same place while the harness restarted the guests in the same order.
**Which one it was decides whether a saved slot→address mapping is merely stale or actively wrong**,
because if allocation followed start order then a restored mapping would name the *wrong sandbox*
and hand one guest another's allowlist. Round 3 separated them by restarting the guests in reversed
order (P10): A went `.5`→`.4`, B went `.6`→`.3` without taking A's old address, tart went
`65.4`→`65.2`, and all three regenerated their MACs. So allocation is neither identity-pinned nor a
clean function of start order — the pool advances, and a stored mapping is **stale in general and
wrong in the particular case that matters**, since tart's pool has wrapped (below). This plan
already assumes the worse case: membership is rebuilt from live state at boot and never restored
from a stored mapping. That is now a requirement with a measurement behind it.

**And the wrap is permanent.** Tart's lease pool read 253/253 on both sides of the reboot (P11) —
`/var/db/dhcpd_leases` is never pruned and a boot does not clear it. Every new VM therefore recycles
an address some earlier VM held, which is precisely the precondition for a stale table entry naming
a *live* sandbox. The reaping requirements below are not defensive.

**What a boot-time restore cannot assume: that the backend is even reachable.** Round 1 recorded both
apple sandboxes as `removed`, which reads as "the reboot destroyed them" and is not what happened —
apple's `container` service is not registered with launchd, so it is simply down after a restart, and
yoloAI renders an unreachable daemon as a *gone* container (**DF180**, `backend-idiosyncrasies.md`).
The sandboxes were intact. Two consequences for this plan: recovery must not infer "no sandbox needs
a slot" from a status read taken before the backend is up, and the reboot ordering is now three
events, not two — pf comes back on its own, the sandboxes do not, and the backend service does not
either. Confirmed as a mechanism rather than inferred, twice: `container system status` after the
reboot reads *"apiserver is not running and not registered with launchd"*, and once the service is
started every sandbox is present and restarts rc=0. Round 3 is the readable instance, and it adds
the state they come back in — all three `stopped`, none of them running.

## The security ceiling

The grant's reachable surface is **one or more addresses in yoloAI's own tables**. It cannot load a
ruleset, disable pf, or touch anything else — measured, not argued.

Two qualifications:

- Adding an arbitrary address to a `dst` table widens one sandbox's allowlist; adding to a `src`
  table subjects an address to a slot's policy. On a **multi-user** Mac that reaches another user's
  VM. On the single-user Mac this project targets, the holder already has unrestricted network
  access.
- **A contained agent cannot reach the grant.** macOS `sandbox-exec` denies exec of setuid binaries
  regardless of the profile's `(allow process-exec)`: `sudo` and `su` are both refused from inside a
  seatbelt sandbox while non-setuid binaries — including a root-owned one in the same directory —
  run fine. This matters because the grant is host-global and not backend-keyed, so a seatbelt
  sandbox coexisting with it would otherwise be a way in.

## Hazards

Each produced a false result during the research.

1. **Never touch the main ruleset.** `pfctl -f` replaces it, and that is where vmnet's NAT lives.
   Reloading `/etc/pf.conf` does not restore it.
2. **Rule order inside a ruleset is enforced**: options → normalization → queueing → translation →
   filtering. A misordered load is a syntax error, and a failed load looks exactly like a rule that
   loaded and did nothing.
3. **`quick` follows the direction keyword.** `block drop in quick on …` parses; `block drop quick in
   on …` does not.
4. **Key on the address, never the interface name.** vmnet re-picks subnets and bridge indices move
   between backends (DF172).
5. **`load anchor` is inert under `-a`** — `pfctl` never opens the file. An earlier run concluded
   "containment holds" from exactly this no-op; the file was never read.

## Ordering, and the dependency this needs

The rules must be installed before the agent can emit a packet. The address is knowable in time —
apple's appears 2s in against 3s for `yoloai new` to return; tart's 8s against 39s.

**But the host has no turn in which to install them.** `AgentFreeLaunch` is set by docker alone
(`runtime/docker/docker.go:63`) and `usesAgentFreeLaunch` requires it, so apple and tart take
`startLegacy`, where `entrypoint.py` execs the agent itself. There is no host-side step between
"guest booted" and "agent running".

That makes **host-controlled-agent-launch.md** a real dependency, now declared. Until it lands,
Phase 2 can only install rules concurrently with an already-starting agent — a weaker guarantee than
this plan otherwise describes, and it makes the ordering acceptance test unwritable.

## Shape of the work

**Phase 1 — capability, setup, verification, *and teardown*.** Larger than it looks:

- **There is no uninstall, and two of the three artifacts survive reboots.** Setup writes a
  `NOPASSWD` grant to `/etc/sudoers.d` and a pinned ruleset to `/etc/yoloai/`, and nothing removes
  either — so uninstalling yoloAI leaves a passwordless root grant on the host naming a binary the
  user no longer has a tool for. Teardown needs the same interactive privileged step as setup,
  because the grant authorizes only `/sbin/pfctl` and therefore cannot delete its own files (which
  is the correct design and must stay that way). Note also that **`pfctl` has no verb that removes
  an anchor** — `-F all` empties it and it remains enumerable until reboot, measured on two
  research anchors that had each been flushed by their own cleanup. See
  [enforcement-state-reaping.md](enforcement-state-reaping.md) § 2b.

- Doctor's capability model is keyed on **(backend, isolation mode)** only; there is no dimension for
  a host prerequisite that is neither. `FixStep` is **display-only** — there is no "run the fix"
  machinery. And **nothing in production code execs `sudo`**, so this is the project's first
  privileged subprocess and needs its own injection seam for tests.
- `pfctl -n -f -` validates a generated ruleset unprivileged (exit 0/1), so verify before spending a
  privileged call — it always warns on stderr, which must be filtered rather than read as failure.

**Phase 2 — apple and tart.** Also larger than "remove a line":

- **`BackendCaps.NetworkIsolation` cannot express this.** It is documented as *"(iptables domain
  filtering)"* and has exactly **two** production consumers — `netpolicy.CanEnforce` ("can the
  allowlist be enforced?") and `launch.go:1040` ("does the guest install its own rules, so it needs
  `NET_ADMIN`?"). Those are different questions that coincide only while there is one mechanism.
  Make it an enum — `None | InSandboxIPFilter | HostPF` — following this file's own
  `FilesystemLocality`/`KeepAliveModel` precedent. ~10 test fixtures move.
- **There is no strategy-selection function.** `CanEnforce` switches on `Strategy` and the sole call
  site hardcodes `StrategyIPFilter`. Selection needs the grant probe, which is I/O, so it cannot go
  in `buildInstanceConfig` (a pure function, unit-tested as such) — put it where `sidecarFirewall` is
  computed and thread one derived value to both call sites, not two conditions that will drift.
- **Dropping `NET_ADMIN` alone breaks boot.** `entrypoint.py` still runs `isolate_network` and raises
  if a rule fails. The seam exists — `YOLOAI_FIREWALL_EXTERNAL=1` (`entrypoint.py:187`, set at
  `launch.go:1016`) — but it is gated on `sidecarFirewall`, which is docker-only, so the gate must
  widen to "enforced outside the guest, by any mechanism".
- **There is no guest-address accessor.** `parseContainerNetwork` is unexported; the only exported
  surface is `SandboxNetHealth`, whose `Detail` is a human-readable clause. A new optional interface
  is needed on apple and tart — an unbudgeted production contract change.
- **`LivePatchNetwork` is a third enforcement path.** `yoloai <box> allow <domain>` execs an ipset
  script inside the container (`engine_network.go:37`). Under host pf there is no in-guest ipset, so
  it would silently no-op while appearing to succeed. It needs a `StrategyHostPF` branch that updates
  the `dst` table.

**Phase 3 — seatbelt: parked.** See
[seatbelt-host-pf-enforcement.md](seatbelt-host-pf-enforcement.md). Seatbelt keeps today's honest
refusal.

## Acceptance test

Every assertion pairs a **block** with a **positive control in the same run** — a sandbox stranded by
the vmnet subnet re-pick refuses every destination for free (DF172).

Per backend, in one run: allowlisted **succeeds**, non-allowlisted **fails**, a second sandbox is
**unaffected**, after stop the address is **gone from the table**, an **empty allowlist fails
closed**, and a **stale entry in another slot does not grant reach** (the D3 regression).

Two the earlier draft omitted: the **ordering assertion** (the agent's first egress attempt is
observably after install — unwritable until the launch dependency lands), and the **pool-missing
case** (with the ruleset flushed, starting an isolated sandbox must fail, not succeed unfiltered).

**Rule 10 intersection, named:** `NetworkIsolation` is **true on apple** today, so a conformance case
gated on it has a non-empty set including the one backend Phase 2 targets. But `runtime/runtimetest`
gates sections on **fixture-declared skip fields**, not capability reads
(`conformance_iface.go:56-60`), so the correct guard is a new fixture field. The suite is
`//go:build integration` and macOS-only, so it cannot go red in CI: rule 10 compliance comes from
unit tests of the pure pieces — ruleset generation, slot derivation, the permit/refuse matrix as an
argv table, and the three verification parsers.

## IPv6 — owned elsewhere

`--network-isolated` is IPv4-only on every backend (**DF104**), because `firewall.resolve_domains`
requests `AF_INET` only. `ipv6-network-isolation.md` and `guest-network-families.md` own this.
**This plan does not close DF104.**

What it contributes: pf accepts `inet6` rules (parse-only) and a table holds both families at
runtime. Any v6 rule must use the same `in quick` form.

**Corrected 2026-08-09 by M7** (`../research/macos-isolation-spike/results/pf-v6-hole.txt`). This
section previously said tart guests hold no v6 address. They do: **both** backends' guests hold a ULA
*and* a link-local, and with the pool loaded and the v4 allowlist empty, v4 was refused while **v6
reached the same host on the same port** on both. The hole is universal on macOS, not apple-only.

pf *can* enforce v6 in the same anchor — a v6 table pair blocked it — so this is an omission rather
than a limitation. But closing it costs more than a second table pair, and that is the part that
belongs in the design: **the table must hold every v6 address the guest holds**, because source
selection is per destination scope (a guest talking to a link-local host sources from its own
link-local, not the ULA the backend reports), and a guest can acquire more at any time via SLAAC or
privacy addresses. A v4 table never has that problem, so the v6 half is not the v4 half with a
different family constant. **Scope limit:** this host has no v6 default route, so everything measured
is guest-to-host on the link; a host *with* v6 upstream has a wider hole, not a narrower one.

## What the verification pass settled (2026-08-09)

Raw runs in `../research/macos-isolation-spike/results/`. Cross-platform conclusions live in
[enforcement-state-reaping.md](enforcement-state-reaping.md); this section is the macOS-specific
half. Two items are **new required mechanisms**, not refinements.

**The address key is defeasible, and one static rule closes it (X1 — the pass's most consequential
result).** In a `--network-isolated` sandbox the agent has passwordless `sudo`, root holds
`CAP_NET_ADMIN`, it flushes its own in-guest allowlist and regains egress (DF179 reproduced), then
takes an address **in no slot at all** and reaches a denied destination; same escape on tart via
`ifconfig alias`. The pool is an allowlist of blocks keyed on address, so it constrains only the
addresses it already holds. **A bridge-scoped default-deny** in the pinned ruleset closes it while
preserving the per-slot matrix, with no address in the rule and no new grant surface. Reproduced
independently on Linux, where the same one-rule fix works. Two verdicts stay deliberately soft: A
borrowing B's *live* address failed on the return path (ARP delivers the reply to B), which says
nothing about a stale address whose owner has exited; and the full-replace case left the guest
holding no address, which is UNKNOWN rather than a refusal.

> **SUPERSEDED, 2026-08-11.** Everything in this sub-section is true of the *stateful* ruleset it was
> measured against, and the remedy it lands on — `pfctl -k <guest> -k <gateway>` plus a D132
> amendment — is **withdrawn**. Under the stateless shape in § *Post-rewrite verification*, revocation
> is a `-T delete` and nothing else. Kept because the mechanism it documents (why the surviving state
> is sourced from the host, why order matters, why the two escapes are closed) is what led to the
> replacement, and because a reader arriving from the audit table needs to find it.

**Deleting an allowlist entry does not stop traffic already flowing (X2).** The transfer kept
advancing after the delete while new connections were refused. `pfctl -k <guest>` alone does **not**
fix it — the state dump shows it killing states sourced *from* the guest while the surviving state was
sourced from the *host*, created by an outbound packet matching no rule, because the pool is
`in`-only. A return-direction rule covers that outbound packet.

**But the rule alone does not stop an established flow either, and the claim that it did was an
artifact of the arm that tested it.** Re-run 2026-08-09 with the two arms separated
(`pf-revocation.txt` R5a/R5b): with `pfctl -k` removed and everything else identical, the transfer
**survived** — 54 bytes to 108 across the revocation, all four states intact and two still
`ESTABLISHED`. Restoring the kill stopped it dead, 45 bytes to 45. pf does not re-evaluate rules for
packets matching an existing state, so a rule loaded while that state exists cannot reach the
connection; the rule's job is to stop a *new* state being created after the kill. **The two are
necessary together, and neither is sufficient.** The 2×2 is now complete: no-kill/no-rule survives,
kill/no-rule survives, no-kill/rule survives, kill+rule stops.

So the macOS remedy costs a **D132 amendment**, and the two escapes this plan hoped for are both
closed (`pf-revocation-alt.txt`).

**The no-grant option is dead twice over.** A short `tcp.established` timeout in the pinned ruleset
would have needed no new grant at all. But an anchor **accepts `set timeout` and silently ignores
it** — `pfctl` exits 0, prints no error, and the anchor still reports the global 86400s against the
20s requested. That is worse than a refusal, because it would have shipped as a working remedy in the
one file D132 permits writing. And even where a timeout *does* apply it could not reach this case: a
busy state's expiry decayed **0s over 20 seconds** while an idle control on the same host decayed the
full 20s, because the timer is reset by the traffic itself. A timeout expires idle states, and the
flow revocation exists to stop is by definition not idle.

**`pfctl -K` was never a candidate, and the "narrower" claim was a category error** — including in an
earlier revision of this section. `pfctl(8)` is explicit: `-K` kills **source tracking entries**, not
state entries. Measured, it killed 0 src nodes and the transfer continued. Two runs' `WHAT WAS NOT
TRIED` carried it as "narrower, and the obvious next question" without anyone reading the man page.

**But there is a narrower form, and it works: `pfctl -k <guest> -k <gateway>`.** The two-host form
names *both* endpoints, so a sudoers rule can pin the second to our own gateway rather than
permitting `-k <anything>` — materially narrower than the form this plan rejected. It is
**directional and the order is counter-intuitive**: guest first stops the flow (1 state killed),
gateway first kills **0 states and reports success while the transfer continues**. A mechanism
argument from which endpoint owns the surviving state predicts the opposite order and is wrong, so
any amendment must pin this order exactly rather than derive it. Scope was measured, not assumed: a
second sandbox streamed through every kill untouched.

**And "pin the peer" is only as stable as the gateway.** It is one address today —
`192.168.64.1`, ten of ten observations — because sandboxes share the default network. The
per-sandbox networks § *the cross-platform question* recommends give each sandbox its **own**
gateway from a hole-filling allocator, so the two recommendations on this page pull against each
other and the narrow grant is weakest precisely where interface keying is adopted. Untested, and it
belongs in the permit/refuse matrix before either is built on.

This **removes** the contradiction with Linux rather than creating one. Both platforms need state
teardown, which is what the Linux half found and fixed. "One cause seen twice" was right about the
cause; the macOS prescription had diverged from it, and the finding had not.

**Slot allocation must be atomic above pf (X3).** Four concurrent acquisitions on *distinct* slots
neither corrupt tables nor cross allowlists, and concurrency gives 2.8× over serial. But two
acquisitions on **one** slot leave both addresses in it with **both** destinations, and each guest
then reaches the other's allowlisted destination — a cross-sandbox privilege leak from contention
alone. Fail-closed to the outside, still wrong. Timing-dependent: treat as *reachable*, not *always*.

**Verification must be behavioural, and the canary's price is not what was quoted (M3).** Under the
shadowed fault — anchor reference present, a `pass quick all` ahead of it, denied destination
answering 301 — `pfctl -s rules` reports **HEALTHY** while the canary reports BROKEN. That much
holds. What did not is the canary as it was written and adopted.

**It failed open.** It returned HEALTHY iff the probe's HTTP code was `000`, and the probe helper
defaults to `000` on every failure — so a dead container, a dead backend daemon, or a guest with no
egress at all also read HEALTHY. It reported the host sound for every fault except the one it was
aimed at. Both arms are now induced, with the superseded sentinel running beside the fixed one on the
same fault in the same run (`pf-liveness-detect.txt` V3b): guest blackholed, old **HEALTHY** / new
**UNKNOWN**; container removed, old **HEALTHY** / new **UNKNOWN**.

The fix separates *blocked* from *could-not-ask* using curl's exit status and `container exec`'s, and
adds a positive control on the same path either side of the probe — from inside a guest, "pf blocked
me" and "nothing routes" are otherwise identical. That costs three probes instead of one and
**re-prices the detector: 320–385 ms, not 83–105.** (The 83 ms came from
`pf-liveness-detect-run3.txt`, a superseded run still on disk; the file this document actually cited
had a minimum of 90.8, and has since been overwritten in place by the re-run — those numbers are in
git at `66dc3341`.) The canary is therefore in the
evaluation-counter's league rather than an order below it. Needing **no grant at all** remains its
distinguishing property, and is now the whole of its advantage.

**Its worst case is much worse than its median.** Every figure above is against `block return`, which
answers instantly with an RST. Against a `block drop` path the same detector took **15.3 seconds** —
three probes each waiting out a 5 s timeout. The cost is a property of how the denial is delivered,
not of the detector.

Two properties that were never stated and bound where it can be used: it **mutates live policy**
(flush the allowlist, probe, restore), so it **races every acquisition** by construction; and the
restore is unguarded, so a host that dies mid-window leaves that sandbox with an empty allowlist and
no egress. **Adopt the canary, but only behind the acquisition lock X3 already requires.**

**Polling is forced (M4).** No signal exists: zero unified-log entries on a pf predicate, zero of
nine watched notify(3) keys with the watcher proven alive in the same run, and `/etc/pf.conf`'s mtime
unchanged by every event. One free partial: `pfctl -s info`'s *Enabled for* counter resets on
`-F all` but not on a plain reload, and `-s info` is already granted — so a poll that remembers the
previous value catches a flush by seeing time run backwards. It cannot see a reload that drops the
anchor line, which is the fault that defeats everything else. Poll costs: `-s info` 2.0 ms,
`-s rules` 4.2 ms, plus ~9.3 ms of `sudo`.

**Pool size is a real lever, and the first figures for it were the harness's own (M8).** That run
timed every call as `sudo -u <user> -H sudo -n pfctl` — the drop to the user **inside** the timed
region, so two sudo invocations per call where the product issues one, from a process already running
as the user. Both shapes have now been measured back to back in a single run
(`pf-pool-scaling.txt` P1/P1b): the nested shape costs **7.95 ms per call** more, against an
independently measured 7.9 ms for one sudo invocation. That is the entire 2.09× and nothing else
differs. The tell needed no hardware — the fitted 18.81 ms/call was exactly twice a measured 9.3 —
and no one compared the two harnesses' numbers.

Corrected, on the shipped shape: **8 slots 93 ms, 16 slots 158 ms, 32 slots 297 ms**; per-call
8.48 ms, fixed overhead ≈0. Against `container run -d` at **651 ms**, acquisition is **14% of a create
at 8 slots, 25% at 16, and 46% at 32** — not the 99% the superseded numbers implied. Concurrency does
not serialize, so a start *burst* is cheaper than the per-start figures suggest.

Acquisition is linear, and the honest form of that claim is narrower than the one it replaces: the
fit error at n=16 is **1.7% against a run-to-run spread of ±1.8% at that same point**. No departure
from linearity is *visible*, which is not the same as the "0.0% error" previously quoted — that was a
coincidence at the mean, below the noise the measurement carries.

> **The 8-slot decision rests on the superseded set and is open again (owner).** It was taken on
> "217 ms against a 676 ms create is about a third; 16 would be +54% and 32 would double start
> latency." On the corrected numbers 8 slots is 93 ms (14% of a create), **16 slots costs 65 ms more
> than 8** (25%), and 32 costs 204 ms more (46%) — so 32 no longer doubles anything and the gap
> between 8 and 16 is a tenth of a container create. The *asymmetry* argument is untouched and may
> well still decide it: **too small is a visible, recoverable, configurable error** that names the cap
> and how to free a slot, while **too large is invisible latency nobody attributes to a pool they
> never use.** But the latency half of the trade shrank by half, and the decision is the owner's to
> re-take rather than this document's to re-derive.

Three consequences that are not obvious and must survive into the build:

- **The ceiling is set at install, not at runtime.** Pool size is baked into the pinned root-owned
  ruleset file, because D132 makes the rules static and only membership mutable. So the config field
  cannot be an ordinary key that takes effect on next start — changing it rewrites
  `/etc/yoloai/pf-pool.conf` and needs a real `sudo` prompt. Spell it as *set at install; changing it
  re-runs the privileged install step*.
- **`doctor` reports the configured cap and current usage**, because a cap the user cannot see is a
  cap they will hit as a mystery.
- **This is a macOS-only knob.** Linux has no pool. Surface it as a platform difference in the model
  rather than inventing a Linux cap to match (§ *Settled by review* in
  [enforcement-state-reaping.md](enforcement-state-reaping.md)).

**The lever for a bigger pool is the collapsed scrub, and it was deliberately not taken.** Under the
blind form the cost is ~18.8 ms per slot on every start regardless of use; under the collapsed form
(`-s Tables -vv`, skipping provably-empty slots) it is `49 + 9.25k` ms where **k is running
sandboxes**, and an idle 32-slot pool is free — pool size stops being a latency knob entirely. That
form needs **one added `NOPASSWD` read line**, which is a change to the security boundary and
therefore D132's to make, not this plan's. If it is ever approved, revisit this default upward and
the config field largely stops mattering. Until then, 8.

**Uninstall (M5)** is a security-boundary change and amends D132 rather than this plan — the residue
is standing authority, and removing it needs a real `sudo` prompt because the grant deliberately
cannot flush.

**`NEFilterDataProvider` stays parked, with a better reason (M6).** Apple approval is **not** the
barrier — the NetworkExtension entitlement stopped being case-by-case in 2016, which corrects the
impression [prior-art-egress-enforcement.md](../research/prior-art-egress-enforcement.md) §4 leaves.
The barriers are that the deliverable changes shape: a Developer-ID-signed, notarized `.app` under
`/Applications` (this host has **zero** codesigning identities), an unscriptable user approval, and —
measured — `systemextensionsctl developer` refused while SIP is enabled, so even prototyping costs a
SIP-disabled machine. yoloAI ships an unsigned CLI binary.

## Unmeasured, and known limits

- ~~**Recovery's second half.**~~ **Closed by round 3.** All three downstream questions are now
  measured with the guests actually running: a restarted sandbox gets a *different* address on both
  backends, the vmnet bridges returned on the same subnets and indices, and enforcement filtered end
  to end in two slots on their own allowlists. What remains open is only replication — see the
  n=1 caveat below.
- ~~**Whether a tart guest keeps its address across a reboot.**~~ **Closed by round 3: it does
  not.** `rb-t` was running and answering this time (the earlier reading came from a surviving
  `dhcpd_leases` record for a stopped VM) and came back on `192.168.65.2` against `.4` before.
- **Everything the reboot settled is n=1.** One reboot, one host. The bridge-index stability in
  particular should not be promoted to a property: two other files in the same results directory
  disagree about bridge indices on this same host 26 minutes apart. What is replicated is the
  address movement, which the no-reboot control shows independently.
- **Renewal after a subnet re-pick — untested, and it is the one address change that would fail
  open.** Steady-state renewal does not move tart's address (five renewals over 28 minutes), and the
  other two ways an address moves — restart and reboot — are measured. But both of those happen
  *through yoloAI*, which reconciles on every run and rebuilds membership from live state. A subnet
  re-pick is the only candidate for an address moving **while the sandbox runs and nothing invokes
  yoloAI**, and that case is worse than stale: an address in no `src` table matches neither
  `pass in quick from <yb_src_i> to <yb_dst_i>` nor `block drop in quick from <yb_src_i> to any`, so
  it falls through to the main ruleset and the sandbox is **unfiltered** — D6's fail-open, reached
  from the other direction. Membership without rules and rules without membership fail identically
  and silently.
  **Why it is still untested.** A re-pick is triggered by the *host's environment* — joining a
  network that collides with the vmnet subnet — not by anything yoloAI does, so it cannot be
  scheduled. Forcing one means editing `com.apple.vmnet`'s `Shared_Net_Address`
  (`/Library/Preferences/SystemConfiguration/com.apple.vmnet.plist`, root-only) and restarting the
  service. That was deliberately **not** done on the spike host: its DNS runs over Tailscale
  (`100.100.100.100` on `utun2`, which also owns `100.64/10`) and its default route is
  `192.168.0.1/24`, so a re-picked subnet colliding with either takes out host name resolution or
  routing — on the machine the work is running from. **Run it on a host that is not the one you
  need, with console access**, and check the one thing that matters: whether a running guest's
  address moves, and if so whether anything re-adds it before the next yoloAI invocation.
  Note apple holds no `dhcpd_leases` record and does not renew through bootpd at all, so the two
  backends need separate answers here as everywhere else.
- **Host/guest resolution parity — measured 2026-08-04, and it did not diverge.** `dst` holds
  resolved addresses and resolution moves to the host, so where the two sides resolve a domain
  differently the guest is blocked while allowlisted. apple only — tart and seatbelt never had
  in-guest resolution, so they have no second answer to disagree with. Measured with the same call
  the product uses (`socket.getaddrinfo(…, AF_INET)`, `runtime/docker/resources/firewall.py:73`)
  on both sides, 3 rounds each, over `api.anthropic.com`, `registry.npmjs.org`, `github.com`,
  `objects.githubusercontent.com` and `example.com`: **every guest address was in the host set,
  for every domain** — including the CDN-heavy ones, where 12 Cloudflare addresses and 4
  githubusercontent addresses matched set-for-set. The two sides do *not* share a resolver (guest
  `192.168.64.1`, the vmnet gateway; host `100.100.100.100 192.168.0.1 192.168.111.1`,
  led by Tailscale MagicDNS), so the gateway
  forwards consistently with the host's upstream. Raw output:
  [`results/dns-parity.txt`](../research/macos-isolation-spike/results/dns-parity.txt).
  **Three limits before this is treated as settled:** it is one host's resolver configuration;
  only public domains were tested, and a **split-horizon name** — a MagicDNS or VPN-internal host
  the guest's resolver cannot see at all — is the case most likely to diverge and was not
  exercised; and it says nothing about the *temporal* hazard below, which is the one already
  breaking sandboxes.
- **Split-horizon: measured 2026-08-07, and it DOES diverge — but not where expected.**
  [`dns-gaps.txt`](../research/macos-isolation-spike/results/dns-gaps.txt), against a real tailnet,
  with public domains agreeing on both sides first so a miss is attributable:

  | Name class | Result |
  | --- | --- |
  | tailnet FQDN (`host.<tailnet>.ts.net`) | **agree** — the vmnet gateway forwards MagicDNS through |
  | bare short name relying on a **search domain** | **host-only**; the guest has no such search domain |
  | **mDNS** `.local` | **host-only**; the guest does not do mDNS |

  So the VPN itself is *not* the hazard — the guess that MagicDNS names would be invisible to the
  guest was wrong. The hazard is **resolver configuration that is not forwarded**: search domains
  and mDNS. That generalises well beyond Tailscale, since corporate DHCP hands out search domains
  routinely, and it produces a silent **fail-closed**: the host resolves, writes an address into
  `dst`, and the guest — which cannot resolve the name at all — never sends there. The user
  allowlisted a domain and the sandbox still cannot reach it.

  **And the answer contains addresses that mean something different in the guest.** The host
  resolved its own `.local` name to `127.0.0.1 192.168.0.157 192.168.139.3 192.168.64.1` — loopback,
  the host's LAN address, and the vmnet gateway.

  **Corrected 2026-08-08, on the owner's challenge.** An earlier version of this bullet said writing
  `127.0.0.1` into a guest's `dst` "allowlists the guest itself", and that was carried to the owner
  as a security regression. It is not one. Loopback traffic never leaves the guest's own stack, so
  no host-side rule evaluates it: the entry is **inert**, granting nothing. The dominant failure
  here is *functional and fail-closed* — the user allowlisted a name and the guest still cannot
  reach it, which is the same outcome the split-horizon reproduction below measures at `000`.

  The genuine widening in that answer is the **other** addresses. `192.168.64.1` is the vmnet
  gateway, so installing it permits guest→host on the bridge **across all ports**, where only `:53`
  normally needs to be reachable; `192.168.0.157` is the host's LAN address. Both are modest and
  both are real, and they are what makes output validation worth doing — not the loopback entry.
  Moving resolution to the host is still not a transparent substitution, and the rule below still
  stands; only the stated consequence changes.
- **The both-sides-resolve-differently case, reproduced end to end
  ([`dns-split-horizon-sim.txt`](../research/macos-isolation-spike/results/dns-split-horizon-sim.txt)).**
  The runs above found *host-only* names, which produce an inert allowlist entry. The worse case —
  one name, two answers — was created with an `/etc/hosts` entry, a faithful reproduction of the
  mechanism rather than an approximation: two resolvers, one name, two answers. With the host's
  answer installed into `dst` exactly as the design would, **the guest could not reach the name it
  was allowlisted for (000) while the installed address was reachable (301)** as the control.
  Nothing is misconfigured, no component errs, and no error is produced anywhere. **This is the
  strongest argument in the file for re-resolving guest-side or reconciling the two answers**, and
  it applies to Linux equally — a container has its own `resolv.conf` too.
- **pf will not save the design from a bad answer.** `dst` tables accept `127.0.0.1`,
  `169.254.1.1`, `224.0.0.1` and the vmnet gateway without complaint (only `0.0.0.0` failed to
  land). So **validating resolver output — rejecting loopback, link-local, multicast and the
  guest's own gateway — is yoloAI's job**, and a requirement rather than a refinement.
- **Resolution is one-shot, so parity at start says nothing about hour six — and the decay is now
  measured.** `resolve_domains` runs once, and CDN rotation moves the addresses under a long-lived
  sandbox regardless of whether the two sides agreed at launch. Inherited, not caused by host `pf` —
  but host `pf` inherits it, and a table loaded once is exactly as stale as an ipset loaded once.
  `dns-gaps.txt` polled five domains every 5 minutes for an hour: **`github.com` changed address
  within 10 minutes** (`+1/-1`) and later changed back. One movement in one hour on one resolver is
  not a rate, but it is enough to settle the question of *whether* — an allowlist resolved at start
  is not still correct later in an ordinary session, and the design must either re-resolve or state
  that it accepts the decay. The other four domains, including the 12-address `registry.npmjs.org`
  set, held steady for the full hour.
- **Pool size and exhaustion behaviour** are undecided — but pool size is no longer *also* a latency
  decision. Under the blind cross-slot scrub it was: every start paid 9.3 ms per slot whether or not
  anything occupied it (8/16/32 slots = 101/175/320 ms). The address-count dump measured in
  [enforcement-state-reaping.md](enforcement-state-reaping.md) § *The scrub collapses* makes the cost
  track running sandboxes instead, so an idle pool is free and sizing it is purely a question of how
  many concurrently-isolated sandboxes to support. That collapse costs one added read in the grant
  and is **not** approved here; without it, pool size and start latency stay the same dial.
- ~~**Whether yoloAI should hold its own `pfctl -E` reference.**~~ **Answered: it would not help.**
  `pf-flush-reference.txt` R2 took a real `-E` token, confirmed it in `pfctl -s References`
  alongside `pfd`'s, and then had another process run `pfctl -d`. pf went **Disabled anyway**, and
  every token — ours and `pfd`'s — was destroyed: `No pf starter references held`. Reference
  counting does not defend against `-d`, so **yoloAI cannot protect its own enforcement by holding
  a reference; it can only detect the state.** That makes the fourth verification check above the
  whole answer rather than one of two.
  Two limits. `-X` releasing the *last* reference is still untested: R3 tried and got
  `token invalid`, because the earlier `-d` had already destroyed the token, so that PASS is
  vacuous and is recorded as such. And with pf disabled the guest reaches nothing (vmnet's NAT is
  implemented in pf rather than merely coexisting with it), which is fail-closed — but per
  `pf-flush-reference.txt` R0 that appearance is exactly what masked the `-F all` fail-open, so it
  should not be read as safety.

## Sweep surfaces

Per AGENTS.md rule 2, landing this changes shipped text that nothing typechecks:
`internal/cli/helpcmd/help/security.md` (says tart/seatbelt have no isolation),
`internal/cli/lifecycle/new.go` (the `--network-isolated` flag help), plus `flags.md`, `topics.md`,
`docs/GUIDE.md` and `docs/README.md`.

## Rejected alternative: per-sandbox sub-anchors

A filter-only parent (`anchor "*"`) with each sandbox's rules loaded from stdin into
`com.apple/yoloai/<instance>`. It works: sub-anchor filter rules enforce and stay scoped, `rdr`
inside them is inert while it is evaluated one level up, `load anchor` is inert under `-a`, `set
skip` is not honored from a sub-anchor, and `-s Anchors` enumerates them. It has no slot cap.

Rejected on two measured grounds:

1. **Its grant can void all filtering.** `pass in quick all` in any writable sub-anchor takes another
   sandbox from blocked to reachable. The table grant cannot express this.
2. **Its reaping must identify orphans by anchor name**, which is the ambiguous
   `yoloai-<principal>-<name>` form `runtime/orphan.go` forbids (DF19/DF115/DF125). Table membership
   is identified by address, so the problem does not arise.

Its supposed advantage — per-sandbox allowlists — is not unique to it: two slots give two sandboxes
independent policy. The cap is the only real difference.

## Not in scope

The `apple`/`podman`/`containerd` `NET_ADMIN` grant on Linux (DF179's own problem, different fix per
backend), and closing DF104.
