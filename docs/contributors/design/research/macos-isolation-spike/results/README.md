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
| `pf-canary-probe.txt` | builds and validates the behavioural check proposed to close the verification gap, and prices it. It discriminates both ways on a healthy host and **detects the fault** — reaching a destination through an *empty* allowlist in 0.03s while all three D132 checks read healthy. Its C3 is the consequential half: **`block return` enforces identically to `block drop` and answers in 0.00s against 1.00–3.01s**, which is what makes the probe affordable and separately stops agents hanging on every denied connection |
| `pf-main-ruleset-writers.txt` | how long a broken host stays broken, and what else writes the main ruleset. **Nothing restored the anchor reference in 4 minutes, and a sleep/wake did not restore it either** — the fail-open window is bounded only by a reboot or a manual repair. Sleep/wake and content caching both left a healthy host fully intact. Also the first real `-X` result, with a token that was actually valid when released |
| `pf-mixed-backend.txt` | three apple guests and two tart guests in **one** five-slot pool, across two vmnet bridges and two subnets: every diagonal reached, all 20 off-diagonal paths refused, zero cross-backend leaks. Crosses the two previously-orthogonal results (two guests on two bridges; eight guests on one bridge), and confirms `block return` on tart |
| `dns-split-horizon-sim.txt` | the DNS case nothing had caught: one name, two answers. Created with an `/etc/hosts` entry, then the host's answer installed into `dst` as the design would. **The guest could not reach the name it was allowlisted for while the installed address was reachable** — silent, with no component in error. Also: pf accepts loopback, link-local, multicast and the vmnet gateway into a `dst` table, so validating resolver output is yoloAI's job |
| `dns-split-horizon-sim-mdns-invalidated.txt` | **invalidated, kept, and it became an idiosyncrasy entry.** The harness flushed DNS and restarted `mDNSResponder` so the host would notice its `/etc/hosts` edit — which took the *guest's* resolver down with it, still dead 25s later, because the guest resolves through the vmnet gateway which forwards through the host's `mDNSResponder`. The run aborted rather than reporting a dead resolver as a divergence. The fix was to stop flushing: macOS watches `/etc/hosts` and picked the entry up unaided |
| `pf-flush-reference.txt` | `pfctl -F all` shown to be a **fail-open** trigger in one run: enforcing → flush → restore only NAT → `allow=301 deny=301` with the anchor still holding correct rules. Corrects `pf-midlife-wipe.txt`, which read the same event as fail-*closed* because NAT death masked it. Also answers the `-E` question: a token we hold does **not** survive another process's `pfctl -d`, so enforcement cannot be defended by reference-counting — only detected |
| `dns-gaps.txt` | the two gaps `dns-parity.txt` named and left open. Split-horizon **does** diverge, though not where predicted: tailnet FQDNs agree (the vmnet gateway forwards MagicDNS), while **search-domain** and **mDNS** names are host-only — and the host's own `.local` resolves to `127.0.0.1` and the vmnet gateway, i.e. host-relative addresses whose meaning changes inside a guest. Plus an hour of rotation polling in which `github.com` moved within 10 minutes |
| `net-ceiling.txt` | the vmnet allocator, measured because M1 proposes a per-sandbox network as a non-address key. 16 networks created without hitting a limit (the probe's own cap, not vmnet's), ~0.1s each. **Its finding is N3: the allocator FILLS HOLES** — deleting `yb-n-2` released `192.168.66.0/24` and the next network created was handed that exact range back. So a subnet, and the bridge carrying it, recycles immediately the way an address does. N1b adds the reason the key is worse than it looks: a network gets **no host bridge until a container attaches**, and the bridge is torn down on detach, so `bridgeN` names an attachment, not a network |
| `net-ceiling-run1-undecided.txt` | **superseded, kept.** Same data, but N3 printed "compare these two lines yourself" instead of comparing them — a check that emits evidence and no verdict reads as a pass at a glance. It also mislabelled `lo0`'s address as a bridge. Nothing in it is wrong; it just declined to conclude |
| `ne-install-ceremony.txt` | M6. Two halves, labelled: **measured** on this host — `systemextensionsctl developer` is refused while SIP is enabled, and zero codesigning identities exist — and **read** from Apple's docs for the rest. Corrects the impression that Apple must approve the NetworkExtension entitlement; it has not been case-by-case since 2016. The real cost is a changed deliverable: a Developer-ID-signed, notarized `.app` under `/Applications` plus an unscriptable user approval |
| `pf-anchor-eval.txt` | **the sharpest finding in this directory.** A loaded anchor pf never evaluates, because the main ruleset lost its `anchor "com.apple/*"` line. All three of D132's start-path checks report healthy — pf enabled, pool loaded with the full rule count, address in its slot — and a denied destination answers 301. The deciding state lives in the main ruleset, which the grant deliberately cannot read. Also records the repair, which is two steps in a fixed order |
| `pf-anchor-eval-run1-predicate-bug.txt` | **invalidated, kept.** Same run with `grep -c 'com\.apple'`, which also matches `anchor "com.apple.internet-sharing"` — a different, top-level anchor a system service re-inserts. It declared the ruleset healthy and skipped the repair while the host was broken. Two anchor names sharing a 9-character prefix; `com.apple/` is the discriminator and `com.apple` is not. Its E2 is still valid and is the first capture of the three-green-checks fail-open |
| `pf-parent-anchor.txt` | the mid-life candidate family the previous run excluded — rewrites of the **parent** `com.apple` anchor. A direct parent reload, a sibling sub-anchor write, and an Application Firewall toggle: all three left our sub-anchor's rules, membership **and live enforcement** intact, with the slot never re-armed. Its B section is an honest UNKNOWN and its by-product is a fact: restarting a *sandbox* does not make vmnet reinstall the bridge NAT, only restarting the apple *daemon* does |
| `pf-pool-occupancy.txt` | the slot pool at n=8 rather than n=2: 8 live sandboxes, 8 distinct allowlists, and the full 64-cell matrix — every diagonal reached, all 56 off-diagonal paths refused. Also confirms the one-call dump names exactly the occupied slots (which is how a start path finds a free one) and reports FULL at exhaustion |
| `pf-pool-occupancy-run1-invalidated.txt` | **invalidated, kept, and the reason the file above has a canary.** Identical harness on a host whose main ruleset had lost its `com.apple/*` reference: 56 of 56 leaks, reported as a failure of the slot design. It was the host. The baseline gate could not catch it — it proves the guest *has* a network, never that pf *would* block — so one confirmed block now runs before the matrix |
| `pf-midlife-wipe.txt` | whether anything wipes the anchor **mid-life**, under a running sandbox — the macOS counterpart to the Linux `flush ruleset` finding. Seven candidates against a live enforcing sandbox, each judged on rules, membership **and** live egress in both directions. **Nothing wiped the anchor.** Its value is mostly in the two that did something else: `pfctl -F all` and a `pf.conf` reload leave our state intact and take out **vmnet's NAT**, so the guest loses egress entirely — fail-*closed*, the opposite of Linux. Also carries the census of who else writes pf anchors here |
| `restart-control.txt` | the **no-reboot** control: a plain stop/start moves every guest's address on both backends, each with a freshly generated MAC. Replicates `lease-binding.txt` L2 and extends it — the new parts are the MAC mechanism, the exhausted lease pool, and a census resolving L1's UNKNOWN. Written to stop the reboot halves attributing to the reboot what an ordinary restart does anyway |

| `pf-spoof.txt` | **the pass's most consequential result.** With the `--network-isolated` path measured end to end, the agent reaches root, holds `CAP_NET_ADMIN`, flushes its own in-guest allowlist (DF179 reproduced) and then **takes an address that is in no `src` table at all — which pf does not filter, because every rule is keyed on membership.** S4 closes it with one bridge-scoped default-deny that preserves the whole matrix. S5 reproduces the escape on tart, so it is not backend-specific |
| `pf-spoof-run2-invalidated.txt` | **invalidated, kept, and the most dangerous file in this directory until it was renamed.** 7 PASS / 0 FAIL concluding the guest *cannot* modify its interfaces — the exact inverse of the file above. The sandbox it measured was a plain one, which does not carry `CAP_NET_ADMIN`; the capability arrives only with `--network-isolated`. Every "Operation not permitted" it records is real and none of it is about the shipped isolated path. **A free negative that reads identically to a security result** |
| `pf-spoof-run3-invalidated.txt` | **invalidated, kept, same inversion, different cause.** It found root inside the sandbox and still reported CANNOT, because `yoloai exec` runs with a `PATH` lacking `/sbin` so the tools simply were not found. The same PATH gap made M7 report tart guests holding no IPv6. Absolute paths afterwards. Between the two runs: **one capability bit and one PATH entry, and both produced a confident, fully-passing inverse of the truth** |
| `pf-v6-hole.txt` | the allowlist is v4-only and the guests are dual-stack: on **both** backends a destination refused over v4 answers over v6 on the same host and port. V3 is the half that makes it actionable — pf **does** enforce on IPv6 inside this anchor, so the hole is an omission in the rules and not a limitation of the mechanism |
| `pf-concurrent-acquire.txt` | the case the pool exists for, and the one every timing file excluded. Concurrent acquisitions on **distinct** slots are clean; two acquisitions racing for the **same** slot **merge their allowlists** and each guest reaches the other's destination. That is the cross-sandbox lock rule 3 already assumes, measured failing in its absence |
| `pf-uninstall-residue.txt` | what survives a user uninstalling yoloAI. **A standing `NOPASSWD` grant on `/sbin/pfctl` outlives the program it was installed for** — inert litter would be one thing, and U5a exercises it to show it is a live capability. Stale table membership survives too, and the anchor node itself can be emptied but not removed without a reboot |
| `pf-revocation.txt` | whether removing an allowlist entry stops traffic **already flowing**. It does not: the stream kept advancing while new connections were refused, so revocation changes future policy only. `pfctl -k` alone does not fix it either — it kills states sourced *from* the guest while the surviving state was sourced from the *host*. **R5a/R5b are the arms an audit forced**: the earlier run tested the return-direction rule and the state kill *together*, saw the transfer stop, and concluded the rule sufficient. Run alone the rule fails (54→108 bytes, states intact); rule+kill stops it (45→45). **The two are necessary together**, which puts a `pfctl -k` grant back on D132's table |
| `pf-pool-scaling.txt` | acquisition cost at 8/16/32 slots, and the file that had to correct itself. Run 1 timed `sudo -u <user> -H sudo -n pfctl` — the drop to the user **inside** the timed region, two sudo invocations per call where the product issues one — and produced figures uniformly 2.09× `pf-acquire-cost.txt`'s. Both shapes now run back to back: the gap is **7.95 ms/call** against 7.9 ms for one sudo. Corrected: **93 / 158 / 297 ms**, i.e. 14 / 25 / 46% of a `container run -d`. Its P2 now states linearity against the n=16 spread (fit error 1.7%, spread ±1.8%) rather than as a bare "0.0% error" |
| `pf-liveness-detect.txt` | three candidate detectors against two faults, priced. Detector A reports **HEALTHY on a shadowed host** — an inspection is still a proxy. **V3b is the audit's item**: the adopted canary returned HEALTHY iff the probe returned `000`, which is also what a dead container or a guest with no egress returns, so it read healthy for every fault but its own. Fixed with an exec/curl-status sentinel and same-path controls, and verified with the **old sentinel running beside the new one on the same fault** — old HEALTHY / new UNKNOWN, twice. The fix costs three probes: **320–385 ms, not the 83–105 quoted** (83 is in `pf-liveness-detect-run3.txt`, superseded; the round-4 file's own minimum was 90.8, and that file has since been overwritten in place by the re-run — the pre-remediation numbers are in git at `66dc3341`), and **15.3 s** against a dropping path |
| `pf-change-signal.txt` | whether macOS emits any signal on a pf change, or whether polling is forced. It is forced. **S1a is the audit's item**: the notify half always proved its watcher alive by posting to itself, and the log half had no equivalent. It does now, and it *narrowed the finding rather than overturning it* — the predicate and query do match a message emitted in the window, so the silence is real, but `process == "pfctl"` matched **zero** entries for a deliberate `pfctl` invocation at both default and `--info --debug`. That clause is inert here, so the negative rests entirely on the two `eventMessage` clauses |
| `pf-nonaddress-key.txt` | M1: process identity, MAC, tags across NAT, per-sandbox networks. K3 is the positive — an ingress tag keyed on the bridge survives NAT and is matchable on egress. **Its K4c conclusion is superseded by `pf-interface-key.txt`**: K4c deleted the network before recreating it, so "the bridge index RECYCLED" measured recycling *after release*, which is not the question a claim-time check turns on |
| `pf-interface-key.txt` | re-asks whether macOS has a per-sandbox non-address key, after the Linux pass refuted the claim that neither platform does. **Split answer.** A *held* index is stable (four held, two more created, no collision — and the control confirms the allocator does recycle, so that negative is not free). The **ingress tag is genuinely per-sandbox** under one network per sandbox: one guest blocked, one not, under a ruleset containing no address at all. But **an ordinary restart releases the index** — a sandbox started in that window took the restarting sandbox's bridge — so a claim-time read is not sufficient and the tag inherits the instability. **I4 then decides what that window costs**: a rule re-attaches **by name** when its interface returns — same index, no reload, still enforcing, and the guest's *address* changed underneath a rule that never named it — so the window is a window and not a permanent lapse. **I5 then prices the window instead of naming it**, with disjoint allowlists so a leak is observable at all: a stranger that took a departed sandbox's index **reached the destination only that sandbox was granted AND was refused its own** — a cross-sandbox leak of X3's class plus a denial of the real policy, because pf is first-match-with-quick and the stale pair sits ahead of the stranger's own. I5b measures the remedy rather than advising it: withdrawing the stale rule restores the stranger's policy exactly. Taken together the key is usable, and the whole price is one lifecycle rule: withdraw on detach, re-read the index on attach. Its **I2c is a separate backend defect**: the displaced sandbox, still `running` with no policy loaded, **lost its egress entirely** |
| `pf-revocation-alt.txt` | the two escapes X2's remedy hoped for, both closed. A **`tcp.established` timeout in the anchor needs no new grant and does not work twice over**: an anchor accepts `set timeout` and silently ignores it (pfctl exits 0, anchor still reports the global 86400s against 20s requested), and a timeout could not reach this case anyway — a busy state decayed **0s in 20s** against an idle control's full 20s, because traffic resets the timer. **`-K` was never a candidate**: `pfctl(8)` says it kills source-tracking entries, not states, and it killed 0. What does work is **`-k <guest> -k <gateway>`**, which names both endpoints so a grant can pin the peer — **directional, and the intuitive order is the broken one**: gateway-first kills 0 states and reports success while the transfer continues. Its T3control is the reason any of this is readable: plain `-k` is run first because it is known to work, and the first version of the section omitted it and drew four verdicts from a fixture whose tables had been silently emptied |
| `pf-no-state.txt` | **the highest-value experiment of the post-rewrite round: can pf evaluate policy on every packet, as Cilium does, and thereby drop macOS's state-teardown asymmetry?** It can, and the price is a rule shape rather than a keyword. `no state` loads and is honoured, but on the ingress rule *alone* it changes nothing: every rule the interface-keyed design writes is `in` on the bridge, so the host's reply travels `out`, matches no rule, meets pf's **default pass — and a passed packet creates state**. That state is bidirectional and carries the guest's forward packets past rule evaluation. Adding a return rule scoped to the allowlist (`pass out ... no state`) does reach a **zero state census under a live transfer**, and revocation *still* fails — because every block in that design is also `in`, so the download direction is **never evaluated against a block at all**. Only the complete bidirectional shape revokes. The 2x2, both cells of each row measured: stateless+egress-block **STOPS**; stateless alone survives; stateful+egress-block survives; stateful alone survives. **Neither ingredient is sufficient, and the egress block is the one the rewrite does not currently have.** Measured on both destination classes with a control each — the host-terminated gateway path and the **NAT'd external path**, where the translated state on `en0` (created outside our anchor, by rules we do not own) survives the revocation and does **not** rescue the flow, closing the open question about pf's floating state policy. Revocation is a `-T delete` and nothing else, which is already inside D132's shipped grant: **no `pfctl -k`, no grant widening, and the gateway-pinning tension goes with it**. `container exec` keeps working under the egress block, so the backend's control plane does not ride bridge TCP |
| `pf-no-state-run{1..5}*.txt` | five superseded runs kept for what each one got wrong, because every one of them would have shipped a wrong sentence. **run1** confirmed the state mechanism and then skipped the arm that fixes it, because that arm was gated on "did correctness break" — a return rule is not needed to make traffic work, it is needed to stop pf minting state, so correctness passed and the section printed "the prediction above is refuted" one screen above the census confirming it. **run2** counted every state naming the guest, so it measured accumulation: it never stopped the in-guest curl, mixed in NAT'd states created outside the anchor, and ran the gateway probe onto the very endpoint it counted — reporting a tidy 3/4/5 that had nothing to do with the rules. **run3** cleared with `pfctl -k <guest>` and left one state behind every time, because a download's state is sourced from the **gateway**, not the guest. **run4** read only the `block in` counter while the rule that fired was `block out`, giving a stopped transfer against a frozen counter — a free negative by this directory's own standard. **run5** built its control from the census winner (S2) while the verdict came from S3, leaving the one cell that decides the design unmeasured. A sixth near-miss never reached a file: the NAT'd arm's 200MB download **completed** in 10s, and the flat zero that followed was the transfer finishing, not a revocation — caught only because the counter had not moved |
| `pf-lifecycle.txt` | **implements the one rule macOS needs that Linux does not, and races it.** `pf-interface-key.txt` measured withdrawal as an outcome; this runs it as a mechanism against a live restart, with the I5 hazard reproduced as a control first (no mechanism: the stranger inherits A's policy — reaches A's destination, refused its own). With a 50 ms `ifconfig` poll the stranger gets its **own** policy. **L4 is the point of the file**: three intervals, not one. The backend takes **5212 ms** to actually release the index, which is not our cost; detection plus withdrawal completes **33 ms** after release; a stranger holds an interface **816 ms** after release. The margin is **783 ms** — a fact about this host's sandbox start time and not a property of the design, and **the adopted canary's own 320–385 ms cadence would consume most of it** if detection were folded into that loop. **The re-read half is NOT REACHABLE by this route and stays unverified**: a returning sandbox reclaims its old index rather than taking a new one, so the case that half exists for never arises. That reclaim is itself the finding — see the DF190 row below |
| `df190-mechanism.txt` | **DF190 goes from "symptom with no mechanism" to owner, mechanism, workaround and a corrected severity.** **Ownership:** it reproduces with **zero rules of ours loaded** — every yoloAI anchor asserted empty first — so nothing yoloAI does is involved and it is a defect in Apple's `container`. **Mechanism, demonstrated not inferred:** the daemon log shows the departing network's vmnet helper *staying alive* across the stop (`released session [allocations=1]`, not a shutdown) and re-allocating on the same network id when its sandbox returns; D5 then confirms causation by re-running with the network **deleted** while empty, whereupon no displacement occurs and both sandboxes keep egress. So a network object outliving its last container re-attaches onto a bridge index handed out in the meantime. **That is also the workaround:** delete the network when its last sandbox goes. **Severity, corrected:** run 1 called a stop/start a clean recovery because it only checked the victim. Checking both shows the "recovery" **moves the defect** — the restarted sandbox regains egress and the other one loses it, because a single contested index simply changes hands. **D0 protects the interface-key claim** on the way past: three concurrent per-sandbox networks get three distinct bridges with one gateway each, so co-tenancy is not a thing and the key stays per-sandbox |
| `tart-net-key.txt` | **the plan's `tart: none` is right in effect and wrong in its reason, which matters.** tart is not a backend without per-sandbox networks: in BOTH shared NAT and Softnet each VM gets its own host-side `vmenetN` **member** interface, with the VMs sharing one bridge — the exact shape Linux met in `k2-veth-key-shared-bridge.txt`. What fails is pf. A rule `on <member>` blocks **nothing** (counter 0) while the same rule on the shared bridge blocks **both** VMs (counter 12), and that control is what makes the negative readable rather than free. **T0b closes the obvious escape before spending the VM boots**: OpenBSD's `received-on`, pf's own answer to `physdev`, is a **syntax error** on this pf, with a plain rule loading in the same anchor to prove the absence is real and not a broken anchor. So both member-matching forms the pf literature offers are accounted for, and tart is out of reach because of **macOS pf**, not because of tart's networking. Consequence the plan does not currently carry: tart's own `--net-softnet-allow`/`-block` per-VM CIDR lists are then the only enforcement surface that backend has, and nothing has tested whether they work. Run 1 is kept — it measured interface *existence* and concluded *keyability*, reporting "tart converges" from a claim it had not made |
| `pf-grant-matrix.txt` | **the matrix the brief asked for is moot; the one that replaced it found a conflict and its fix.** `pf-no-state.txt` removed the `-k` question entirely — revocation is a table delete — so there is no kill form to grant and no gateway peer to pin. What replaced it: D132's grant lets the unprivileged side change **table membership** and reload **one pinned file** root wrote, and never author rule text. The interface-keyed rewrite's rules say `on bridge101`, with indices assigned dynamically and reloaded on every detach and return. **G4 confirms the conflict** — no route installs an interface-keyed rule: not `-f <own file>`, not rewriting the pinned file (root-owned), not `-f -`. **G5 finds the fix and measures it**: invert the pool so there is one slot per **bridge index** rather than per sandbox. 164 rules covering bridge100–140 load fine (most naming interfaces that do not exist), and two real sandboxes then get **independent policy with nothing but table membership changed**. Interface keying fits D132 after all, provided the pool is inverted and the table regex covers the index range — a pattern change, not a model change. The cleaner idea, pf **interface groups**, is closed by measurement: macOS `ifconfig` answers `group: bad value` and its man page never mentions them. **And the method that `pf-liveness-detect.txt` V4 could not use now works**: the harness runs as root, removes its own blanket sudo grant for the duration, and refuses to report anything unless `sudo -n /usr/bin/true` is first shown to fail |
| `dns-intercept.txt` | whether the host can see and take over a guest's DNS. It can, on apple: queries are observable on the bridge, `rdr` redirects UDP/53 to a host listener end to end, and `--dns` plus one rule makes it stick with 53 closed to everything else. **D4 is a bounded negative** — the same rule loaded on tart and nothing arrived, so tart's resolver path is not the same and is not characterised here |

## Classifying a discarded run (D136)

**From 2026-08-11, a run being invalidated carries two fields, written by whoever invalidates it at
the moment they do it** — while the cause is in hand and the classification is a recollection rather
than a reconstruction.

- **`Class:`** — `free-negative` (a control satisfied by something other than the mechanism, the
  [§11](../../../../principles/testing-principles.md) class and still the largest bucket),
  `frame-capture`, `instrument-in-region`, `predicate-bug`, `inference-overreach`,
  `confounded-arms`, `no-verdict`. Definitions and specimens:
  [`verification-method.md`](../../verification-method.md) — most of which are drawn from this
  directory.
- **`Direction:`** — `confirmed` or `contradicted`. Whether the bad run agreed with the hypothesis
  in play. `pf-spoof-run2-invalidated.txt` is the reason this field exists: 7 PASS / 0 FAIL for the
  exact inverse of the truth, reading as a security result. The
  [D136](../../../../decisions/working-notes.md) count found invalidated runs splitting **29
  confirming to 8 contradicting**, and every contradicting one was chased inside its round.

**The rows above are deliberately not annotated.** Classifying one's own bad runs after the fact is
the same interpretive act that produced them. D136 records one hand count, dated and labelled as
soft; it is not repeated, and the aggregate is a `grep -c` computed on demand. Never store a total.

**This directory is also the case for the round rules.** It has 18 invalidation markers against the
Linux half's 10, and the difference is structural rather than about care: the Linux pass ran against
`archive/plans/enforcement-verification-queue.md` and synthesized once, and this one went item by
item. See [`procedures/verification-rounds.md`](../../../../procedures/verification-rounds.md).

## What these files do NOT support

Read this before quoting anything from them.

- **Every pf result is n=1 run on one host.** The apple coherence matrix is n=2 (two sandboxes);
  nothing else is replicated.
- **The round-4 re-runs were taken under a `timestamp_type=global` sudo ticket, and one check cannot
  run under it.** The operator primed a passwordless sudo session so the harnesses could be driven
  unattended. `pf-liveness-detect.txt` V4 asks whether removing our grant restores the refusal — which
  is unanswerable when the user can already run anything under `sudo -n`, and the first re-run duly
  reported the scaffolding as a **finding about the product** (`FAIL: the grant survived removal`).
  It now detects the permissive environment and reports `UNKNOWN` instead. **Any other refusal check
  in this directory has the same exposure** and should be re-run with `/etc/sudoers.d` clear before
  its negative is trusted.
- **`pf-liveness-detect.txt`'s detector B result did not reproduce.** Under the shadowed fault the
  committed run has B reporting BROKEN; the 2026-08-09 re-run has it reporting **HEALTHY** with the
  evaluation counter advancing 0→10, i.e. **missing the fault**. Nothing else in that section changed.
  Detector B was not the adopted detector, so this was not chased — but its behaviour under shadowing
  is now *unsettled*, and the file shows one result while this README's neighbour rows describe the
  other. Do not quote B's fault-detection either way without a third run.
- **`pf-interface-key.txt`'s I1b negative is bounded by six networks and one host.** It shows that no
  *held* index was reassigned across two creations while four were held. It does not establish that
  none ever is, and the allocator's policy is not characterised — the control's released index came
  back, but a different released index did not, so "recycles on release" is the shape rather than a
  rule. The decisive result there is I2, which is reproducible: three consecutive runs took the
  restarting sandbox's index.
- **I2c's egress loss is a symptom with no mechanism.** The displaced sandbox reports `running`, holds
  an address and a gateway, and cannot reach a destination the other sandbox reaches from the same
  host in the same second — and its gateway is on no host bridge. What *causes* the bridge not to be
  rebuilt is not measured, and no `container` log was read. It is a reproducible symptom and an
  unopened question, not a diagnosis.
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
- **`pf-no-state.txt` does not measure what statelessness costs.** Linux established that dropping
  the fast-path is free at allowlist sizes 1/1000/10000; nothing here measures per-packet evaluation
  on pf at any allowlist size, with any number of sandboxes, or under load. "It costs nothing" is a
  Linux result and does not transfer.
- **Its `block drop out` is measured for revocation, not for what else it blocks.** The shape denies
  host-initiated TCP to the sandbox unless the source is allowlisted. `container exec` was checked
  and survives, but the credential broker's injector endpoint on the gateway address was not
  exercised, and every sandbox's allowlist would have to contain its gateway for injection to work
  at all. That is a design consequence, not a detail.
- **UDP is untested there, and DNS rides on it.** Every rule and every probe in that file is
  `proto tcp`. pf's stateless handling of UDP is a separate question that nothing has asked.
- **`set state-policy if-bound` was never loaded.** The NAT'd arm shows a translated state on the
  egress interface failing to rescue a revoked flow, which is the outcome that matters — but pf's
  default floating policy was left as found, so this is one measured instance rather than a
  characterisation of state scoping.
- **The NAT'd arm depends on a public file server and is throttled client-side.** Its throughput is
  whatever the internet gave that minute, and a stop can have causes this rig cannot see. That is
  why its no-counter case is reported as unusable rather than as a success.
- **`pf-lifecycle.txt`'s margin is one measurement on one idle host.** 783 ms is the gap between a
  33 ms withdrawal and an 816 ms sandbox start, both n=1, on a machine doing nothing else. A busier
  host, a warmer image cache, or a poll interval anywhere near the canary's 320–385 ms narrows it
  toward zero. Nothing there varies the poll interval, so the margin is one point on a curve nobody
  has drawn, and the mechanism is not shown to win by construction — only to have won here.
- **Its re-read half is unverified, and the file says so rather than passing.** The section could not
  create the condition it tests, because a returning sandbox reclaims its old index. Whether the
  re-read is therefore unnecessary, or merely untested, is not decided by that run.
- **It stops sandboxes cleanly.** `container stop` is not the dangerous case; a killed VM or a daemon
  restart may release an index differently, and that is where a lifecycle rule would actually be
  tested. Untried.
- **DF190 now has a trigger but still no mechanism.** `pf-lifecycle.txt` L5 reproduces it as a rule —
  a returning sandbox reclaims its index from the incumbent, and the incumbent is left with no bridge
  and no egress while the returning sandbox is fine. That is a reliable *cause* to reproduce from. It
  is still not a diagnosis: nothing in that run explains why the displaced sandbox is not rehomed,
  and no `container` daemon log is read there.
- **`df190-mechanism.txt` explains the collision, not the silence.** D5 establishes what causes two
  networks to contend for one bridge index. Nothing there explains why the loser is never rehomed,
  why no error is logged for it, or why the daemon goes on reporting the sandbox `running` with an
  address it cannot use. Those are the parts an upstream report would need and this run does not have.
- **No privileged tracing was done.** `container system logs` is whatever the daemon chose to emit;
  there is no dtrace, no vmnet instrumentation, and no attach to the helper processes. The mechanism
  rests on a log reading plus one causal arm, which is enough to act on and not enough to call a
  diagnosis of vmnet itself.
- **Both sandboxes there are idle `sleep` containers.** Whether an incumbent holding open connections
  is displaced the same way is untested, and that is the case the product actually has.
- **It has not been checked against Apple's issue tracker**, so whether this is already known upstream
  is unexamined.
- **`tart-net-key.txt` probes one destination over TCP/80 and nothing else.** `tart exec` reaches
  both guests, but only to fetch 1.1.1.1. No UDP, no DNS, no second destination, so the keyability
  negative is for that traffic shape.
- **Its `--net-bridged` mode is unmeasured.** One of tart's three networking modes was skipped as
  out of threat model, so "both modes" there means shared NAT and Softnet.
- **Softnet's allowlist is read from `--help`, never run.** `--net-softnet-allow`/`-block` are named
  in that file as the remaining enforcement surface for tart on the strength of the CLI's own help
  text. No VM was started with a blocked CIDR and no destination was confirmed unreachable. If that
  becomes the tart answer it needs the same treatment every pf rule in this directory got.
- **Interface stability across VM restart was not asked of tart**, because pf cannot key on those
  interfaces anyway. That question comes back the moment a working rule form appears.
- **`pf-grant-matrix.txt` is a permit/refuse matrix, not a review of the sudoers patterns.** It takes
  D132's regexes verbatim and checks that the intended commands pass and the obvious ones fail. No
  attempt was made to defeat them — argument smuggling, alternate `pfctl` paths, symlinks, `--`
  handling. A pattern that permits what it should and refuses what it should can still be evadable.
- **Its superset is 41 indices and nobody has bounded the real range.** A sandbox landing outside it
  meets no rule at all and is silently unenforced — fail-open, the worst direction — and no preflight
  assertion for that exists. The evaluation cost of a first-match list dominated by rules for absent
  interfaces is also unmeasured, which widens the gap `pf-no-state.txt` already declines to close.
- **It does not price the reload cadence.** Every lifecycle transition is a `sudo` invocation at
  ~7.9 ms (`pf-pool-scaling.txt`), and `pf-lifecycle.txt`'s daemon reloads on every detach and
  return. Nothing multiplies those together against a real sandbox churn rate.
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
- **`pf-midlife-wipe.txt`'s fail-closed conclusion is SUPERSEDED — read `pf-flush-reference.txt`
  instead.** It recorded `pfctl -F all` as leaving the guest reaching nothing and that was written
  up as macOS failing closed. The guest reached nothing because the same flush killed vmnet's NAT;
  restore NAT alone and the sandbox is unfiltered. The file's readings are all correct, its
  interpretation of W6 is not, and the run's own "state survival is not enforcement continuity"
  caveat is what exposed it.
- **`dns-gaps.txt`'s "guest at end" block is empty, and that is expected.** The pf experiments
  running alongside it restarted the apple daemon, which stopped that guest. The phase was
  host-side only for exactly this reason; the empty block is the caveat firing, not a resolver
  result. Its rotation figure is also one hour on one resolver — enough to settle *whether* a set
  moves, not how often.
- **`dns-gaps.txt` is redacted at the output stream.** Tailnet suffix, peer and host short names,
  and CGNAT addresses are masked, because those are stable identifiers for a private network and
  this repo is public. What the run is evidence for survives redaction; the literal names are not
  the finding.
- **`pf-flush-reference.txt` R3 is a vacuous PASS and is labelled so in the file.** It tried to
  release our `-E` token and got `token invalid`, because the preceding `-d` had already destroyed
  it — so "releasing the last reference does not disable pf" is **not** established. `-X` remains
  untested.
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
