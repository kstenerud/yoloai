> **ABOUTME:** Raw, unedited output from the macOS isolation spike runs, kept so every figure
> quoted in the plans traces to a line someone can read rather than to prose about a run.

# Raw run output

The parent README's numbers come from these files. They are verbatim stdout, including the runs
that produced nothing usable — those are the more instructive half, and deleting them would leave
the impression that the harness worked first time.

| File | What it is |
| --- | --- |
| `coherence-apple.txt` | host→guest shape matrix, apple, n=5, 1 ms poll |
| `coherence-tart.txt` | same, tart |
| `pf-main-run.txt` | the pf run everything is cited from: per-VM scoping, allowlist, reaping, gid, ICMP, tart tamper |
| `pf-p4-tamper.txt` | the privileged in-guest tamper on apple, the one with an efficacy control |
| `vmnet-switch.txt` | the controlled tart/apple switch that **refuted** the cross-backend exclusivity claim |
| `pf-run1-invalidated.txt` | first pf run — the gid section returned empty strings because sudo refused the runas group; kept because the blanks are what a broken probe looks like |
| `pf-p4-skipped.txt` | P4 correctly refusing to run against a stranded sandbox |
| `df175-write-patterns.txt` | every host write pattern tried against a path the guest had read, plus the `msync` repair verified here and the prior art that reports the same defect |
| `reboot-snapshot.txt` | the pre-reboot control the post half compares against — written by `reboot_pre.sh`, machine-read, not prose. `CONSUMED=` at the end means the post half has read it and the round is closed |
| `reboot-pre.txt` | the pre half. **Not round 3's** — that log was destroyed, twice over; what is on disk is the first ten lines of an aborted round 4 (see the round 3 caveats). The enforcement round 3 measured is in the snapshot instead |
| `reboot-post.txt` | the post half, after a **real** restart: anchor survival, pf's own state, and unattended recovery from the pinned file |
| `df175-rmput-tart.txt` | DF175's residual gap, **confirmed**: `files rm` then `files put` of the same name leaves a tart guest serving the whole original file (20 bytes) where the host holds 5. Its P1 line also caught [DF181](../../../findings-unresolved.md) — the repair reporting failure on a guest that then converges. P2 classifies three outcomes, because "refused loudly" and "silently corrupted" are the whole distinction and collapsing them loses it. **P0 needs a warmed guest**: it did not reproduce on two brand-new sandboxes and the run aborts rather than passing vacuously |
| `df175-rmput-apple.txt` | the cross-backend control for the above: apple runs the identical sequence correctly, so the gap is tart-specific like the rest of DF175 |
| `df175-rmput-gate-removed.txt` | the same harness against a build with the `res.Replaced` gate **removed** — the "obvious fix" for the gap. It is the arm that shows why that fix is a net regression, and it exists because the DF175 entry's A/B table was written before any artifact backed its left column |
| `df160-dind-baseline.txt` | extracted from a gitignored `.testcache` run, so DF160's 6.5s-against-120s baseline is quotable. Not reproducible from the file itself; it records what the run reported |
| `dns-parity.txt` | host vs apple-guest resolution of the same domains, using the call the product uses. **No divergence** on this host, CDN domains included — a negative result, and one whose value is entirely in the resolver lines it prints beside it. Does not cover split-horizon names or rotation over time |
| `df181-timing.txt` | **pre-fix.** Settles DF181's mechanism: the repair's `STALE` verdict is **premature**, not an invalidation failure. 2 of 5 replacements reported `STALE`; both self-healed while idle, and the control — an unrepaired replacement over the same wait — stayed stale, which is what makes that attributable to the repair rather than to a coherent guest |
| `df181-timing-postfix.txt` | **post-fix acceptance**, same harness against the shipped delay. A separate filename on purpose: the pre-fix run is the evidence for the mechanism and re-running in place would have destroyed it — which is exactly what happened to the first attempt at this artifact |
| `df176-prompt.txt` | DF176's loose end, **closed**: negative lookups do not stick on tart, and `reset --no-prompt` restores `prompt.txt` visibly to the running guest. Note the row it closes named an in-place reset, which tart cannot reach at all — see the finding |
| `pf-acquire-cost.txt` | what § 1c's slot-acquisition sequence costs on the start path: one `sudo pfctl -T` call, the full 35-call form, sudoers policy-size scaling, and both against a real `yoloai new`. Its C2 split is the load-bearing part — **85% of every call is `sudo`, not `pfctl`** — and its C6 refutes the "dump every table's contents in one call" hypothesis across ten forms |
| `pf-scrub-collapse.txt` | the follow-up C6's own output suggested: `-s Tables -vv` reports every table's **address count** in one call, so provably-empty slots need no delete and the scrub becomes O(running sandboxes). Measured against a same-run blind baseline at five occupancies. **Run 3** — the two invalidated runs below are the same file's earlier attempts |
| `pf-scrub-collapse-run1-invalidated.txt` | **invalidated, kept.** The K1 parser anchored the table-name pattern to end-of-line; pfctl emits a third column naming the owning anchor, so nothing matched and every table read as empty. K1 failed correctly — but K2 timed a "collapsed" sequence issuing **zero deletes** and reported 7x at every k |
| `pf-scrub-collapse-run2-invalidated.txt` | **invalidated, kept, and the more instructive of the two.** K1 fixed and passing; K2 still issuing zero deletes, because the sequence invokes the dump through `sudo -n` under the shipped grant — which K3 in that same run proves refuses it. The probe measured the cost of a capability it had simultaneously demonstrated it did not have. Both runs' tell was identical: **a cost that does not move between k=0 and k=8 is not doing per-slot work** |
| `pf-midlife-wipe.txt` | whether anything wipes the anchor **mid-life**, under a running sandbox — the macOS counterpart to the Linux `flush ruleset` finding. Seven candidates against a live enforcing sandbox, each judged on rules, membership **and** live egress in both directions. **Nothing wiped the anchor.** Its value is mostly in the two that did something else: `pfctl -F all` and a `pf.conf` reload leave our state intact and take out **vmnet's NAT**, so the guest loses egress entirely — fail-*closed*, the opposite of Linux. Also carries the census of who else writes pf anchors here |
| `restart-control.txt` | the **no-reboot** control: a plain stop/start moves every guest's address on both backends, each with a freshly generated MAC. Replicates `lease-binding.txt` L2 and extends it — the new parts are the MAC mechanism, the exhausted lease pool, and a census resolving L1's UNKNOWN. Written to stop the reboot halves attributing to the reboot what an ordinary restart does anyway |

## What these files do NOT support

Read this before quoting anything from them.

- **Every pf result is n=1 run on one host.** The apple coherence matrix is n=2 (two sandboxes);
  nothing else is replicated.
- **`vmnet-switch.txt`'s log section proves nothing about detach events.** It is piped through
  `tail -40` and covers ~3 seconds of a ~4-minute run. The script asked "who tears the bridge
  down" and did not get an answer.
- **Bridge indices are not stable in general.** `vmnet-switch.txt` shows `bridge101`=apple and
  `bridge102`=tart; `pf-main-run.txt`, 26 minutes later, shows `bridge101` carrying tart's
  `192.168.65.x`. Both are true; the second is why rules must not key on an interface name.
- **The tart tamper line in `pf-main-run.txt` is weaker than the apple one.** `pfctl -d` output is
  truncated by `head -1`, and nothing established the guest's pf was enabled or had a rule to
  defeat. `pf-p4-tamper.txt` is the run that carries an efficacy control.
- **The ICMP "sharper" test in `pf-main-run.txt` is circular** — its block rule was scoped
  `proto tcp`, so ICMP passing follows from the qualifier. The harness was fixed afterwards to load
  the protocol-agnostic form; that has not been re-run, and it is the case an implementer needs.
- **The timing files measure one idle M4 MacBook Air and nothing else.** `pf-acquire-cost.txt` and
  `pf-scrub-collapse.txt` are wall-clock on a host with no competing load; a busy host is not
  described. More importantly, the 9.3ms/call figure is **85% `sudo`**, and what was varied was
  policy *size* (+500 rules cost 0.6ms), not policy *source*. A host whose sudoers arrives over
  LDAP/AD, or whose PAM stack does a network lookup, is a different measurement that nobody has
  taken — and it is the case where the call count would hurt most.
- **The start-time fraction is against an empty workdir.** `yoloai new` on a real project also
  copies it, so 13.8% is the *largest* the acquisition sequence can be as a share of start; a
  bigger project makes it smaller, never larger.
- **C6's negative is bounded by the ten forms it tried.** No pfctl invocation it tested dumps table
  *contents* anchor-wide. A form nobody thought of is not excluded, and the run says so.
- **`pf-midlife-wipe.txt`'s negative is exactly as wide as its candidate list, and no wider.** It
  establishes that seven specific actions did not wipe the anchor. It does **not** establish that
  nothing does. Named and *not* tried: a macOS system update, a third-party firewall that writes pf
  (Little Snitch and similar are not installed on this host), Internet Sharing being toggled, and —
  the sharpest of them — anything that reloads the **`com.apple` anchor itself**. The census in W0
  shows `200.AirDrop` and `250.ApplicationFirewall` living in that same parent, so the system
  components most likely to rewrite it are right there and were never exercised.
- **Two candidates could not render a verdict on enforcement, only on state.** `pfctl -F all` and
  the `pf.conf` reload both killed vmnet's NAT, so the guest had no network and neither block nor
  pass was attributable. What survived them is read directly from pf (rules present, membership
  present); that enforcement *continued to work across* the event is **not** shown, because the
  repair path re-armed the slot before egress could be re-tested.
- **`pf-scrub-collapse.txt` measures cost, not correctness.** Skipping provably-empty slots is only
  sound while slot allocation is under the cross-sandbox lock rule 3 already requires — the run
  states that argument and tests none of it.
- **`reboot-post.txt` has been overwritten three times; know which run you are reading.** The three
  reboot files are rewritten in place by each round, so the caveats below are indexed by round and
  only the most recent one describes the file on disk. **Round 1** (`pass=8 fail=0 unknown=3`) is in
  git at `7c913d32`; **round 3** (`pass=15 fail=1 unknown=0`, taken 2026-08-04 11:30) is the current
  file. A round's caveats do not expire when it is superseded — they are why the next round exists.
- **Round 2's raw output no longer exists anywhere.** It ran at 2026-08-04 10:18 and reported
  `pass=12 fail=0 unknown=1`; it was never committed, and round 3 overwrote it in place ninety
  minutes later. What is written about it below and in `REBOOT-HANDOFF.md` is now the only record,
  which makes it prose about a run rather than a run — the exact thing this directory exists to
  prevent. **Commit a round's three files before starting the next one.** The in-place rewrite is a
  standing hazard of this harness and it has now destroyed data twice in one morning; see round 3.

### Round 3 — the current file

- **The pre half's log was destroyed, and the round survives anyway.** `reboot_pre.sh` was run a
  second time *after* the reboot, from a paste of the wrong filename. Its first act is
  `exec > >(tee reboot-pre.txt)`, which truncated the log before the run then died at the IP gate
  with `could not resolve both sandbox IPs` — a message about the stopped guests that names neither
  the reboot nor the damage. What makes the round still sound is where the evidence lived: the
  snapshot is written *after* a gate that exits non-zero unless every sandbox is enforcing, so a
  snapshot existing at all proves enforcement was live pre-reboot for all three guests. The lost log
  held the digits (`allow=301 deny=000`), not the verdict. The snapshot itself was untouched — the
  re-run died well before the line that would have rewritten it. Both halves are now fixed:
  `reboot_pre.sh` refuses to run when an unconsumed snapshot predates the current boot (which is
  exactly this mistake), and the enforcement digits go into the snapshot as `PRE_ENF_*` so no
  verdict depends on a log again.
- **The file on disk is not round 3's log at all — it is an aborted round 4.** Verifying that new
  guard meant running the pre half, the guard correctly stood aside (the round-3 snapshot had been
  stamped `CONSUMED`, which is the "a new round may begin" case), and the run got as far as
  installing the grant and loading the anchor before being interrupted. So the log was overwritten a
  second time by the very check meant to protect it. The guard now runs **before** the root check —
  no privilege is needed to read an mtime and a sysctl — so its refusal can be exercised directly,
  and all three of its branches were then verified without sudo and without touching the log.
- **The one FAIL is a real finding, not a harness fault.** P11: tart's lease pool reads 253/253 on
  both sides of the reboot. Nothing prunes `/var/db/dhcpd_leases` and a reboot does not clear it, so
  every new VM recycles an address some previous VM held. This is the first direct measurement of a
  claim `backend-idiosyncrasies.md` had been making from the restart control's evidence.
- **Round 2's "addresses preserved" is now refuted by measurement, not just by argument.** P10
  restarted the guests in *reversed* order (B first). Both addresses moved and B did not land on A's
  old one: A `.5`→`.4`, B `.6`→`.3`, tart `65.4`→`65.2`, every one with a regenerated MAC. So
  allocation is neither pinned to sandbox identity nor a clean function of start order — the pool
  simply advances. A saved slot→address mapping is unusable at boot.
- **P6 and P7 are still n=1 each.** Bridges returned on the same subnets *and* the same indices for
  a second time, which is two observations, not a property — `vmnet-switch.txt` and
  `pf-main-run.txt` still disagree about bridge indices on this same host. And "every sandbox
  restarted cleanly" is one reboot's worth of evidence about a service that had to be started by
  hand first (DF180).
- **The snapshot's `CONSUMED=` stamp was applied by hand.** The stamping mechanism was written after
  this round ran. `reboot_post.sh` did consume the snapshot, at 11:32; the line records that rather
  than a run of the new code.

### Round 2 — superseded, and its raw output is lost

- **"PASS addresses preserved across reboot" is an artifact of start order, and should be read as
  refuted.** Both apple guests came back on `.3`/`.4`, the addresses they went down on, and the
  harness called that preservation. `restart-control.txt` then showed that an ordinary stop/start —
  no reboot at all — moves every guest on both backends, each with a newly generated MAC. Nothing
  pins an address to a sandbox. The post half had restarted the guests in creation order, which is
  the order that reproduces the original assignment from an allocator counting from the same place.
  Same observation, two explanations, and the run could not tell them apart. `reboot_post.sh` now
  starts B **first** (P10) so that it can — and round 3, which did, saw both addresses move.
- **P8's tart UNKNOWN is a harness artifact, not a fact about the host.** The address lookup gated on
  `yoloai ls` reporting `active|running`, but yoloAI calls a running tart VM whose agent is not
  attached `idle` — which is the state `rb-t` was in *before* the reboot as well as after. So the
  gate excluded the guest it was measuring on both sides of its own comparison. Measured by hand four
  minutes later, the VM was running and answering: `192.168.65.3`, against `192.168.65.2` in the
  snapshot. The gate now asks the backend instead of yoloAI.
- **P6's "same subnets and the same indices" is n=1 and is contradicted elsewhere in this directory.**
  It is a real observation of one reboot; `vmnet-switch.txt` and `pf-main-run.txt` disagree about
  bridge indices 26 minutes apart on this same host. Do not promote it to a property.

### Round 1 — superseded, in git at `7c913d32`

- **Three of that run's comparisons had no working control.** `reboot_pre.sh` wrote the
  snapshot's values unquoted, so the four carrying spaces — `PRE_DATE`, `PF_STATUS`, `BRIDGES`,
  `SANDBOXES` — did not survive being sourced. Three `command not found` lines at the top of the file
  are the evidence, and `BRIDGES` truncated silently to its first token rather than erroring. So:
  **P6's UNKNOWN is an artifact**, it compared a corrupted before-value against an after-state that
  had no vmnet bridges at all because nothing was running; and P3's and P7's `before:` lines print
  empty. P3's verdict does not depend on its before-value (it reads pf's live status and reference
  count), P4's and P1/P2's controls are single-token and loaded correctly. Fixed in the scripts, and
  `reboot_post.sh` now refuses to render any verdict at all if the snapshot does not load cleanly.
- **That run's "tart address preserved across reboot" is not reproducible and should be
  read as unmeasured.** rb-t was `stopped`, `bridge102` did not exist, and four minutes later the
  exact command the harness used — `tart ip yoloai-cli-rb-t` — returned *"no IP address found, is
  your VM running?"* with rc=1. A `/var/db/dhcpd_leases` record for `192.168.65.2` did survive the
  reboot, which is the likeliest thing that PASS read. Nothing here establishes that a tart guest
  **holds** its address across a restart.
- **P8 and P9's end-to-end half were not exercised at all**, because no guest had an address to
  re-add to a slot. The pool-restore half of P9 is measured; "enforcement works again afterwards" is
  not. **Round 2 closed this**: with the guests restarted first, two slots with different allowlists
  each filtered correctly after a real reboot (`allow=301 deny=000` on both).
- **"The apple sandboxes came back `removed`" is a reading, and the wrong one.** It was taken at face
  value here and in the plan for a day. The sandboxes were intact: apple's `container` service is not
  registered with launchd, so it was simply not running after the reboot, and yoloAI renders an
  unreachable daemon as a *gone* container — reproduced deliberately by stopping the service under a
  live sandbox (**DF180**, and `backend-idiosyncrasies.md`). Anything that reads apple sandbox state
  after a restart must run `container system start` first, which `reboot_post.sh` now does.
