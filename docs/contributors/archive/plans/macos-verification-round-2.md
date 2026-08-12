> **ARCHIVED — not maintained, not swept, not a live reference.** Everything below was
> true when written and has not been checked since. It is **not a specification** — its
> conclusions have been applied to the live documents listed under *Where this landed*, and
> those are the current answer. Good for archaeology: what was asked, what was run, and which
> runs were thrown away. See [`../README.md`](../README.md).

> **ABOUTME:** The fixed item set for the second macOS enforcement verification round — what
> is still a claim rather than a measurement, what each item decides, and what it costs.
> Records what to run, not code.

# macOS verification round 2

- **Status:** IMPLEMENTED — opened and closed 2026-08-12 under [D136](../../decisions/working-notes.md). Every item ran or was dropped in writing; the single synthesis pass is [`macos-pf-privileged-path.md`](../../design/plans/macos-pf-privileged-path.md) § *Round 2 verification*, and **that is what to cite**.
- **Depends on:** —

**Where this landed.** The outcomes below are recorded as they were found; the conclusions drawn
from them are live elsewhere:

- [`macos-pf-privileged-path.md`](../../design/plans/macos-pf-privileged-path.md) § *Round 2 verification* — the
  protocol-agnostic rule shape, the host-side detector, the costs, the forced gateway decision,
  the corrected tart row, and an invariants-versus-mechanisms split per D136 §5.
- [D132](../../decisions/working-notes.md) — **amended**: the grant's two widenings measured
  together, the verbose read replacing the behavioural canary, `<dst>` holding addresses as a
  forced decision, and whether the record is needed for apple at all.
- **Two things needing an owner's decision rather than another run**, both named in the
  synthesis: whether the credential injector gets a fixed port, and whether host `pf` on apple
  is built as defence in depth now that the bounding-set drop exists.
- **Two things that should be filed as findings** and are not this agent's write surface:
  W10's unreachable gateway (a shipped path, `runtime/apple/reach.go`) and W9's unmeasured
  Private Relay risk.

**The raw runs are not archived** and remain the citable evidence:
`design/research/macos-isolation-spike/results/w*.txt`.

**This file is the round's boundary** ([D136](../../decisions/working-notes.md) §3,
[`procedures/verification-rounds.md`](../../procedures/verification-rounds.md)). The item set below
was fixed before the first run. The round closes when every item has run or been **explicitly
dropped, in writing, here**. Until then [`macos-pf-privileged-path.md`](../../design/plans/macos-pf-privileged-path.md)
and D132 are **not edited** — intermediate optima are working state, and one synthesis pass applies
the whole fact set at close.

Adding an item mid-round is normal; a run that opens a question is doing its job. Adding it *to this
file* is what keeps the boundary real.

**Why this round is macOS-only.** The Linux half ran its round 2 separately
([`archive/plans/enforcement-verification-round-2.md`](enforcement-verification-round-2.md))
and named "macOS anything" as explicitly out of it. This is the other half.

**Why this directory is the case for the round rules.** It carries 18 invalidation markers against
the Linux half's 10, and the difference is structural rather than about care: the Linux pass ran
against a queue and synthesized once, and the macOS pass went item by item. This is the first macOS
round with a queue.

## The prior-art gate (D136 §1), discharged 2026-08-12

Run before the item set was fixed, and **it changed the item set twice** — which is the point.
`prior-art-egress-enforcement.md` was read first; three of its sections bear on macOS, and two
searches beyond it turned up mechanisms that document does not carry.

- **§4, NetworkExtension, needs nothing new.** `ne-install-ceremony.txt` already measured the install
  ceremony on this host and priced the deliverable change. Not re-opened.

- **`cirruslabs/softnet` is a purpose-built, published solution to this exact problem, and the corpus
  treats it as a footnote.** It is a **userspace packet filter sitting between the VM and
  `vmnet.framework`** that enforces, per VM: source MAC and source IP pinning (so it structurally
  closes `pf-spoof.txt`'s S4 escape), destination CIDR allow/block with longest-prefix matching and
  block precedence, a `@host` identifier for gateway traffic, default-deny via `block=["0.0.0.0/0"]`,
  and — the part that matters most — **dynamic policy updates over a Unix-socket JSON-RPC channel
  that clear the flow table on change.** That last is `pf-no-state.txt`'s entire five-run
  investigation, solved upstream, in the dependency we already ship. It also needs root via SUID or
  passwordless sudo, i.e. it arrived at D132's privileged-path answer independently.
  **Consequence for this round:** W8 stops being "check tart's remaining surface for completeness"
  and becomes "measure the shape the industry converged on for this platform". There is also a
  **`openai/softnet` fork**, which is weak evidence that someone else is running agent VMs on macOS
  behind it; the fork's diff was not read and nothing here rests on it.

- **`apple/container` discussion #719 is our problem, upstream, with a maintainer answer.** It
  independently derives `in`-direction, subnet-as-source rules — the same conclusion `pf-enforce.txt`
  E1 reached — and states that **`block out` and guest-side rules do not cover vmnet traffic**. A
  maintainer records that **the host gateway remains reachable from an `--internal` network, so any
  host service bound to `0.0.0.0` is accessible**, which is W5's hazard stated by the people who own
  the backend. And the maintainers **will not accept a solution requiring manual `pfctl`**, citing UX
  and **iCloud Private Relay conflicts**. So there is no upstream fix coming that would retire this
  plan, and the Private Relay clause is a hazard nothing in our corpus has ever named.

- **Private Relay is a live, published pf hazard and it became item W9.** Mullvad's write-up states
  that **Private Relay mostly disables itself as soon as any firewall rule is added to pf**, and
  Apple's own discussion forums carry "PF/PFCTL breaks iCloud Private Relay". Our design writes rules
  into `com.apple/yoloai` on every start. If that switches off a user's Private Relay, it is a
  user-visible side effect of installing yoloAI that this plan does not mention and no acceptance
  test would catch.

- **The cap-bounding-set route to DF179 was never considered, and it is in our own tree.** This is
  not external prior art — it is [GEN §3](../../principles/general-principles.md) read the way A35
  says to read it, against code we already ship. `apple/container` has `--cap-add`/`--cap-drop` and
  documents that a container starts **without** `CAP_NET_ADMIN` by default; yoloAI adds it
  (`runtime/apple/apple.go:206`) so the in-guest allowlist can be installed. The Docker path already
  ends with *"the entrypoint configures rules while running as root, then drops privileges — the
  agent never has `CAP_NET_ADMIN`"*, and `network-isolation.md:233` has a test asserting the
  capability is out of the **bounding set**. [DF179](../../design/findings-unresolved.md) reasons entirely
  about *where enforcement sits* and concludes apple needs host `pf` because it has no shareable
  netns — it never asks whether the capability can be dropped from the bounding set after the rules
  are installed, which no root agent could then regain. **Consequence:** W6 exists, and if it holds
  it changes what this plan is *for* on the apple backend. It was fixed into the item set before
  being run, so a null result costs the round nothing.

- **Not re-opened:** the Cilium/`toFQDNs` DNS finding (§1). It is already recorded as the strongest
  result in the workstream, `dns-intercept.txt` already measured host-side interception on apple, and
  the design question it raises is a design question, not a hardware one. W1 touches it only where
  the *new* rule shape could break the interception path.

## Working model

Each item names what to run, **what it decides**, and what it costs. An item that decides nothing is
not an item. Every harness is written against
[`scripts/research_harness_v2.py`](../../../../scripts/research_harness_v2.py) (D136 §3), so an
expectation resting on a probe never shown reporting failure refuses to render.

**The instrument boundary, stated once for the round** (D136 §3): every harness here runs under a
time-boxed blanket `NOPASSWD` grant so it can be driven unattended, and that grant is *scaffolding*,
inside no measured region. Any item whose result is a **refusal** must remove the blanket for the
duration and refuse to report until `sudo -n /usr/bin/true` is shown to fail — the method
`pf-grant-matrix.txt` established. Any item that reports a **duration** states what is inside the
timed region, because `pf-pool-scaling.txt` run 1 inflated every figure 2.09× by timing its own
privilege drop.

## Items

| ID | Question | Decides | Cost |
| --- | --- | --- | --- |
| W1 | Does the adopted bidirectional stateless shape cover **UDP and ICMP**, and does DNS survive `block drop out`? | Whether the shape has a containment hole or a functional one. Every rule and probe behind this design is `proto tcp`, so "the allowlist contains it" is a claim about one protocol; the corpus's one ICMP result is circular by its own admission. Linux closed the same gap in V3. | med |
| W2 | Does `pfctl -a com.apple/yoloai -vvs rules` read `Evaluations: 0` under the shadowed fault, discriminate inside the 41-index superset, and can the grant widen to it safely? | [DF192](../../design/findings-unresolved.md) — whether the forgeable in-guest canary is replaced by a host-side counter read. The plan names exactly these three plus a traffic baseline, and calls none of them arguments. | high |
| W3 | What does the stateless bidirectional shape cost per packet, and what does the 41-index superset cost at evaluation time? | Whether Linux's "the fast-path is free" transfers. `pf-no-state.txt` declines to price statelessness and `pf-grant-matrix.txt` widens the gap; this is the corpus's largest unpriced claim and the counterpart of Linux V7. | med |
| W4 | What bridge indices does this host actually hand out, and what happens to a sandbox landing outside the pinned superset? | Whether the pool inversion's **fail-open** is boundable by a preflight assertion, or whether it is unbounded and the inversion needs a different shape. | low |
| W5 | Does host→guest credential injection survive `block drop out`, and what does putting the gateway in every `dst` table expose? | Whether the adopted shape breaks the broker, and whether its remedy re-opens the host — the hazard `apple/container`'s own maintainer names. | low |
| W6 | Can a root agent on the **apple** backend be denied `CAP_NET_ADMIN` after the in-guest allowlist is installed, by dropping it from the bounding set? | Whether [DF179](../../design/findings-unresolved.md) closes on apple with no host component at all. If it does, this plan is defence in depth for apple rather than the only route, and its priority changes. | med |
| W7 | Is the lifecycle rule's **re-read** half reachable once DF190's workaround is applied, and does a **killed** guest or a daemon restart release an index differently from a clean stop? | Whether the re-read half is unnecessary or merely untested, and whether the 783 ms margin holds on the release paths the product actually has. `pf-lifecycle.txt` stops sandboxes cleanly and says so. | med |
| W8 | Does **Softnet** actually enforce a per-VM destination allowlist on tart, and does its policy channel revoke a live flow? | tart's only enforcement surface, currently asserted from `--help` text. Prior art says it is the shape this platform converged on, including the live-revocation problem `pf-no-state.txt` needed five runs for. | med |
| W9 | Does loading our anchor **disable iCloud Private Relay** on this host, and what else writes pf here? | Whether installing yoloAI has an unstated user-visible side effect on the host's own networking — the reason `apple/container` refuses `pfctl` solutions. | low |
| W10 | **Added mid-round 2026-08-12, from W5's baseline failing.** Can an apple guest on the **`default`** vmnet network reach a host service bound to its own gateway, as `runtime/apple/reach.go` states it can? | Whether credential brokering works on the apple backend at all. This sits entirely beneath the enforcement design — no pf rule is involved — and it contradicts a shipped file's stated measurement. | low |
| W2b | **Split out of W2 2026-08-12.** Does D132's grant still refuse everything it refused, once it widens for *both* the inverted pool's table range and the detector's verbose read? | Whether the boundary can move once and stay defensible. Separated because a refusal is only measurable with the round's blanket grant removed, which needs the harness to run as root — a different rig from W2's. | med |
| W6b | **Added mid-round 2026-08-12, from W6's result.** Does `container exec` restore the capability the entrypoint dropped? | How far W6's property reaches. The bounding set belongs to a process *tree* and the daemon parents an exec, so W6 may protect the agent and nothing else. Not an escape — a guest cannot reach `container exec` — but it decides whether a design can rely on the property for everything yoloAI runs in the sandbox, including the canary. | low |

**Deliberately not an item, recorded so it is not lost.** `pf-liveness-detect.txt`'s detector B is
unsettled (BROKEN in the committed run, HEALTHY in the re-run, same fault) and this README says not to
quote it either way without a third run. B was never the adopted detector, and W2 decides whether the
whole behavioural-detector family is replaced. **If W2 lands, B is moot and gets dropped in writing;
if W2 fails, B comes back as an item.** It is not being run on spec.

> **DROPPED 2026-08-12, explicitly, after W2.** W2 and W2b together retire the behavioural-detector
> family: enforcement liveness is now read host-side from our own anchor's `Evaluations`, with the
> guest nowhere in the trust path and the grant widening measured. Detector B was a candidate within
> the family that is being replaced, so its premise is gone and a third run would settle a question
> nothing now asks. Recorded rather than deleted, because an item that vanishes quietly is how a
> round pretends to have closed.

## Outcomes

Filled in as each item runs. Raw output in
[`../research/macos-isolation-spike/results/`](../../design/research/macos-isolation-spike/results/).

| ID | Outcome |
| --- | --- |
| W1 | `w1-protocol-coverage.txt`. **The shape as written contains TCP and nothing else.** Loaded exactly as `pf-no-state.txt` and the reaping plan state it, a denied host is refused over TCP (`000`) and **answers over UDP** — `dig @1.0.0.1` returned real records — and **answers ICMP echo**. The egress block counter never moved. Dropping the `proto tcp` qualifier and changing nothing else closes both: UDP times out, ICMP gets no reply, TCP is unaffected, and the block counters move in *both* directions (`in 0→7`, `out 0→5`). **DNS through the vmnet gateway survives the egress block in both arms**, so the protocol-agnostic form does not cost the guest its resolver. |
| W2 | `w2-evaluations-detector.txt`. **Three of the four sub-questions land; the fourth is W2b.** (1) Under the shadowing fault the anchor's `Evaluations` is exactly flat (`0 → 0`) while the sandbox is genuinely fail-open — pf-anchor-eval's three-green-checks state, reproduced by loading `pass quick all` into a sibling sub-anchor that sorts ahead of ours, so nothing writes the main ruleset. Healthy, the same traffic on the same path moves it `151 → 184`. (2) It still discriminates **inside the 164-rule superset** (`106 → 106` shadowed, `106 → 763` healthy) with 41 indices loaded, most naming interfaces that do not exist. (3) The idle/inert ambiguity is answerable, but **not by the counter** — an idle sandbox's evaluations range 27–944 per 30 s and are *host-to-guest* traffic (`Ipkts` flat at 0 while `Opkts` moves). The signal that tracks the guest is the bridge's `Ipkts`: 0 idle, 9 under load, and it needs no grant. Attaching a capture was controlled for and does not move the floor. |
| W2b | `w2b-grant-widening.txt`. **The boundary moves safely, and it moves once.** With the table regex widened to the bridge-index range (100–140) *and* a line added for `-vvs rules` on our own anchor, all 16 refusals hold — including six that exist only because of the widening: a table above and below the range, a verbose read of the main ruleset, of the parent anchor, of a sibling anchor, of states, of tables, and the recursive `-A` form. All 6 intended permits work. Each refusal is baselined **permitted under the blanket grant**, so the blanket that made `pf-liveness-detect.txt` V4 unanswerable is what makes these answerable. The verbose read discloses **no line of the main ruleset** and names no anchor but ours — with a control proving it returned rules at all. One thing it does hand over: `Inserted: uid N pid N`, naming the process that wrote the rules, which the plain form withholds. |
| W3 | `w3-stateless-cost.txt`. **Below the noise floor, stated as an upper bound rather than as "free".** Four rulesets back to back on the same path — empty, stateful, stateless, and stateless plus the 164-rule superset — give medians of 0.072 s for 400 short connections and 0.045–0.059 s per 256 MiB, i.e. ratios of 0.99–1.02×. But the **within-ruleset** spread is 6–38%, so a 0.1% between-ruleset difference decides nothing: what this establishes is that the adopted shape costs *less than this rig can resolve*, not what it costs. The `no state` keyword demonstrably took — the anchor counted **55 k** evaluations against the stateful arm's **1.2 k** — so this is not a cheap number for a ruleset nobody evaluated. Superset load time is 11.4 ms, measured outside the transfer's timed region. |
| W4 | `w4-bridge-index-range.txt`. **The fail-open is real, and the range is boundable.** With 20 rules loaded covering `bridge114`–`bridge118` and the live sandbox on `bridge104`, the denied host answers **301** — silently unenforced, with a control proving the rules loaded. The same sandbox under a superset that covers its index is contained (`000`). On the range: indices are handed out **contiguously from the lowest free one** (101–106 here, `bridge100` pre-existing from tart), and they **fill holes** — freeing `bridge104` and creating a new network put it back on `bridge104` rather than advancing past the `bridge106` high-water mark. So the highest index in use tracks *concurrent* networks, not lifetime ones, and a fixed pinned range is sound in principle. It still needs the preflight assertion nobody has written. |
| W5 | `w5-gateway-exposure.txt`. **Three results, and the third is the problem.** (1) Under the adopted shape the guest **cannot reach the injector endpoint** on its gateway (`000`), so credential injection is broken unless something is done. (2) Putting the gateway in `<dst>` restores it (`204`) — and **also opens an unrelated host service on the same address** (`204`), because `<dst>` holds *addresses*: permitting the injector permits every port the host has bound on its bridge address. That is the `apple/container` maintainer's own hazard, ours. (3) A **port-scoped** `pass` fixes it exactly — injector `204`, bystander `000`. **But it cannot be written**: the injector binds an *ephemeral* port (`internal/broker/host.go:237`), so its number is unknown until the sandbox starts, and D132's pinned file is static text root wrote at install time. The narrow rule works and is unavailable. |
| W10 | `w10-gateway-host-route.txt`. **A guest on one vmnet bridge cannot reach anything the host binds on its own gateway — which is exactly where `reach.go` puts the credential injector.** In a fleet of three identically-created networks, `bridge100` and `bridge102` answer `204` and `bridge101` answers `000`, while on that same bridge external egress is a clean `301`, the **host itself reaches the same address and port** (`204`), and ARP resolves both ways with no pf rule loaded anywhere. It tracks the bridge **index** across a dozen observations — different subnets, both creation orders, daemon restarts, and after synthetic stale bridges are used to shift which index a network lands on. Consequence: yoloAI's apple sandboxes all use the `default` network (`apple.go:236` never passes `--network`), and `ipAssignedToHost(gw)` is true in the broken case, so `InjectorReach` does **not** take its degradation path — it succeeds and binds an address the agent cannot dial. **`Run 1 is invalidated and kept** (`…-run1-confounded.txt`, `Class: confounded-arms`, `Direction: confirmed`): it attributed this to built-in-vs-created *network kind*, which co-varied with bridge index in both its live trials, and the trial that would have separated them was voided for a DF190 displacement. **Two mechanisms were then tested and refuted** — a missing `lo0` host route, and layer 2. What causes it is not established. |
| W6 | `w6-cap-bounding-set.txt`. **Yes.** Baseline reproduces DF179 on this backend — a root agent flushed its own allowlist and reached a denied host. With `CAP_NET_ADMIN` out of the bounding set the same root agent could not: the denied host stayed at `000` while an allowlisted one answered `301` in the same second, so the refusal is the allowlist and not a dead box. `sudo` does not help (a bounding set is not something sudo can exceed) and neither does a new user namespace; a *private netns* succeeds and is worthless, having no route out. |
| W6b | `w6b-exec-vs-bounding-set.txt`. **The drop does not reach `container exec`.** The exec'd process carries the container's full configured set (`a80435fb`) against the holder's dropped `a80425fb`, and it removed the allowlist and reached the denied host (301). So W6 protects **descendants of the entrypoint** — which is the agent — and nothing the host starts afterwards. |
| W7 | `w7-lifecycle-reread.txt`. **DF190's workaround unlocks the half its defect made untestable.** Baseline reproduces the reclaim `pf-lifecycle.txt` could not get past — sandbox on `bridge100`, stopped, stranger takes `bridge100`, sandbox returns on **`bridge100`**. With the network deleted while empty, the same sequence returns it on **`bridge101`**: it cannot reclaim, so the case the re-read half exists for finally arises. The stranger also keeps its egress (`301`), so the workaround stops the displacement as well. **And the release paths differ**, which `pf-lifecycle.txt` never tried: `stop` and `kill` both free the index immediately once the command returns, a **daemon restart holds it 5.61 s**. (Not comparable with that file's 5212 ms, which is timed from the stop being *issued* and includes the command's own duration; this starts when it returns.) What is still not run: the re-read itself — this shows the case is reachable, not that an implementation handles it. |
| W8 | `w8-softnet-enforcement.txt`. **tart has a working per-VM egress allowlist, and the corpus had the flag backwards.** `--net-softnet-allow=1.1.1.1/32` — read as an allowlist naming one permitted destination — leaves a *different* address reachable (`301 / 301`), because `--help` says `--allow` **widens** the default private-address restriction rather than narrowing it. The allowlist form is `--net-softnet-block=0.0.0.0/0` relaxed by `--net-softnet-allow`, and it enforces: permitted `301`, denied **`000`**, with the permitted address still answering so it is an allowlist and not an outage. `tart exec` rides the Guest Agent's vsock rather than the filtered path, so a successful block cannot read as a dead VM. **Not run: the dynamic policy channel** — Softnet's JSON-RPC socket with flow-table clearing on change, which is live revocation solved upstream. `tart run` exposes only boot-time flags and nothing here drives that socket. |
| W9 | `w9-private-relay.txt`. **Unanswerable on this host, and recorded as that rather than as an all-clear.** iCloud Private Relay is **not a service on this account**, and the daemon held no privacy-proxy policy when the run started — so a pf change cannot be shown to disable a feature that is already off, and both arms are **voided** rather than reported. The prior-art hazard is therefore neither confirmed nor refuted and stays live for users who do have it. What is measured: 12 pf load/flush cycles touching no interface, and 16 network create/destroy cycles loading no pf rule, both over 180 s against a quiet 180 s baseline, with the log predicate first shown finding the 20 transitions it is known to contain. The correlation that made this an item — a stable policy from 03:45 becoming 18 transitions in the 25 minutes W1/W2/W2b/W4 ran — is kept as an **unattributed observation**; with Private Relay off it belongs to some other privacy-proxy consumer and nothing here says which. |

## Explicitly out of this round

- **Linux anything.** Owned by the Linux half's own round.
- **IPv6.** `pf-v6-hole.txt` measured the hole and `ipv6-network-isolation.md` owns the fix. Nothing
  here re-measures it, and the new rule shape's v6 behaviour is named as unmeasured rather than run.
- **seatbelt.** It has no address to filter, which is why that half needs the dedicated-gid machinery
  and why `seatbelt-host-pf-enforcement.md` is parked. Unchanged by anything in this round.
- **A subnet re-pick under a running guest.** Still the one address change that would fail open, and
  still unschedulable on this host without taking out its own DNS or routing — see the plan's
  *Unmeasured* section. It needs a host that is not the one the work runs from.
- **Defeating the sudoers regexes.** `pf-grant-matrix.txt` is a permit/refuse matrix and says so.
  Argument smuggling, alternate `pfctl` paths, symlinks and `--` handling are a review of the
  pattern, not a run, and they are not in this round.
- **Replication.** Every pf result in this corpus is n=1 on one host and this round does not change
  that. Items that produce a number say so in their own bounding section.
