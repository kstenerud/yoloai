> **ABOUTME:** Build plan for enforcing `--network-isolated` from host `pf` on macOS, where the
> allowlist today is either weak (apple grants the guest NET_ADMIN) or absent (tart, seatbelt).
> Covers how yoloAI acquires the privilege `pf` needs without installing a root daemon.

# macOS: enforce the network allowlist from host `pf`

- **Status:** PLANNED — mechanism, authorization and enforcement measured on hardware
  2026-08-02/04. Nothing built.
- **Depends on:** tamper-resistant-network-isolation.md, host-controlled-agent-launch.md
- **Rides:** **any** — the user-visible surface only gains capability. `--network-isolated` becomes
  *accepted* on tart where it is refused today (newly-accepted input is not a break), and on apple
  it becomes stronger without changing its spelling. Seatbelt is unchanged. The `BackendCaps` change
  is internal. If any phase ends up *withdrawing* isolation somewhere, that half is **breaking** and
  needs a `docs/BREAKING-CHANGES.md` entry.

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
block drop in quick from <yb_src_0> to any
…repeated per slot
```

Per sandbox at start: claim a free slot, `-T add` its address to `yb_src_N` and its resolved
allowlist to `yb_dst_N`. At stop: `-T delete`.

**`in quick` is load-bearing.** `in` on the bridge is evaluated **before** NAT, so the packet still
carries the guest's address; `quick` makes the `pass` win by appearing first. A `block drop out` form
sees the host's post-NAT source, **matches nothing, and leaves the sandbox wholly unfiltered while
loading cleanly** (`pf-enforce.txt` E1 ran both candidates in one pass). An earlier draft of this
plan proposed the `out` form.

Measured: 32 slots load (64 rules), a **high** slot index enforces, an unassigned sandbox is
untouched, two slots give two sandboxes **independent allowlists**, an empty `dst` table fails
**closed**, and **table contents survive a ruleset reload** — so resizing or repairing the pool does
not de-isolate running sandboxes (`pf-assumptions.txt` D1/D2/D4, `pf-shapeb.txt` B1/B2).

**Backend-agnostic, measured rather than predicted.** The pool enforces on **tart** as well as
apple, and an apple guest and a tart guest hold **different allowlists in different slots
simultaneously**, on separate vmnet bridges, with teardown by table delete restoring either
(`pf-tart-pool.txt` T1/T2/T3). Nothing in the design distinguishes the backends because the rules
key on source address; that was a prediction until this run.

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

**Phase 1 — capability, setup, verification.** Larger than it looks:

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
runtime. Any v6 rule must use the same `in quick` form. Measured limit: an apple guest holds a
**ULA** (`fd00::/8` space) and has no IPv6 egress; tart guests have none; this host has no v6
upstream, so whether guests would egress over v6 elsewhere is unmeasured.

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
  the product uses (`socket.getaddrinfo(…, AF_INET)`, `runtime/docker/resources/firewall.py:63`)
  on both sides, 3 rounds each, over `api.anthropic.com`, `registry.npmjs.org`, `github.com`,
  `objects.githubusercontent.com` and `example.com`: **every guest address was in the host set,
  for every domain** — including the CDN-heavy ones, where 12 Cloudflare addresses and 4
  githubusercontent addresses matched set-for-set. The two sides do *not* share a resolver (guest
  `192.168.64.1`, the vmnet gateway; host `100.100.100.100`, Tailscale MagicDNS), so the gateway
  forwards consistently with the host's upstream. Raw output:
  [`results/dns-parity.txt`](../research/macos-isolation-spike/results/dns-parity.txt).
  **Three limits before this is treated as settled:** it is one host's resolver configuration;
  only public domains were tested, and a **split-horizon name** — a MagicDNS or VPN-internal host
  the guest's resolver cannot see at all — is the case most likely to diverge and was not
  exercised; and it says nothing about the *temporal* hazard below, which is the one already
  breaking sandboxes.
- **Resolution is one-shot, so parity at start says nothing about hour six.** `resolve_domains`
  runs once, and CDN rotation moves the addresses under a long-lived sandbox regardless of whether
  the two sides agreed at launch. Inherited, not caused by host `pf` — but host `pf` inherits it,
  and a table loaded once is exactly as stale as an ipset loaded once. Unmeasured.
- **Pool size and exhaustion behaviour** are undecided.
- **Whether yoloAI should hold its own `pfctl -E` reference.** Deliberately untested: getting `-X`
  wrong drops the count and breaks vmnet NAT for every VM on the host. The reboot narrowed this —
  pf is enabled at boot without help, so this is about surviving another process's `-X`, not about
  starting from a disabled host.

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
