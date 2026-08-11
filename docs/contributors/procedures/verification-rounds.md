> **ABOUTME:** How a hardware-verification round runs — what opens it, what fixes its item set,
> what a probe must demonstrate before its result is trusted, and when the plan is allowed to
> change. The remedy for two failure mechanisms that look identical from inside the loop.

# Verification rounds

**When this applies.** Any time a design question can only be settled by running something against
real hardware or a real backend — a spike, a measurement pass, a "does this actually work here"
queue. It does not apply to unit tests or to `make check`; those are [testing-principles.md](../principles/testing-principles.md).

**Why it exists.** The 2026-08 host-enforcement pass ping-ponged. Each optimal design was
invalidated by the next run, some positions reversed twice, and one was reversed by prior art that
had already been read and written down. The count that followed ([D136](../decisions/working-notes.md))
found that **28 of ~29 invalidated runs were harness defects, not the world differing from the
model** — so the loop's instinct, *measure more*, was making it worse. The rules below separate the
two mechanisms so the right remedy gets applied.

## The two mechanisms, which is the thing to hold onto

From inside the loop both present as *the last run was wrong*. They have opposite remedies.

| | Harness defect | Design overfitting |
| --- | --- | --- |
| What happened | The probe was broken. The world did not surprise anyone. | The design was re-derived to optimality against an incomplete fact set, and the next fact broke the fit. |
| More runs | make it **worse** — another chance to record a confident wrong answer | make it **better**, but only if they land together |
| Remedy | §2, §3 below — the harness is code under review | §4, §5 below — hold the design rougher, synthesize once |

## 1. Prior art gates opening the round

**Read the prior art before the round opens, not beside it.** Reading costs hours and prunes the
design space; a hardware round costs days and resolves one cell. In that order, cheap results
discard expensive ones.

The specimen: `prior-art-egress-enforcement.md` landed 2026-08-08 under the commit subject *"find
the prior art, and one of it corrects my own advice"*. The rewrite it pointed at ran 2026-08-11 —
three days of hardware time spent arriving independently at a correction that was already on disk,
already read, and already labelled as a correction.

[GEN §3](../principles/general-principles.md) has always been read as *check whether a library
already does it*. It also means **check whether the industry converged on a different shape** —
and a convergence away from your design is a constraint on the plan, not a note beside it.

## 2. A round's item set is fixed in a queue file before any run

**The round needs a definition that is not the agent's judgement.** "Synthesize at round close" is
useless if *round* means "when I feel finished" — an agent has facts and no model of what is still
unmeasured, so it cannot tell when the round should end. So:

- Before the first run, write a queue file naming every item: what to run, **what it decides**, and
  what it costs. `archive/plans/enforcement-verification-queue.md` is the worked example.
- The round is closed when every item has run or been **explicitly dropped, in writing**.
- Adding an item mid-round is allowed and normal — a run that opens a question is doing its job.
  Adding it to the queue is what keeps the round's boundary real.

The evidence for this being load-bearing is an A/B that already happened. The Linux half ran against
a queue and synthesized once, and says so in its own index. The macOS half went item-by-item without
one, and its plan now carries three generations of position with a reader instruction about which
section supersedes which. The two directories carry 10 and 18 invalidation markers respectively.

## 3. A probe must demonstrate it can report failure

**This is the rule the harness enforces, and the one class §11 does not reach.**
[testing-principles.md §11](../principles/testing-principles.md) asks whether the *mechanism* under
test was present. This asks whether the *instrument* discriminates — and a run can satisfy §11
cleanly while its probe only ever gives one answer.

> **Every expectation rests on a probe whose baseline was taken with the mechanism absent and came
> out the other way.** Not a claim that it would; the recorded observation that it did, in the same
> run, from the same callable.

`pf-spoof` run 2 is the specimen: 7 PASS / 0 FAIL concluding a guest cannot modify its own
interfaces — the exact inverse of the truth. Every command was real, every output genuine, every
control held. The sandbox simply lacked the capability under test, so every "Operation not
permitted" was free.

**Use `scripts/research_harness_v2.py`**, which makes this structural rather than remembered: an
`expect()` on a probe that was never baselined, or whose baseline already read the value being
claimed, refuses to render. A measurement with no mechanism-absent state — a capability probe, a
census, a cost — passes `unbaselined="<reason>"`, and the reason is printed under its own heading.
The waiver is the escape hatch; silence is not.

Two further rules that no library can enforce:

- **A harness diff is reviewed before its run is trusted**, not after its output looks surprising.
  The review asks one question: *what would this print if the design were wrong?* If the answer is
  "the same thing", the harness is not ready.
- **State the instrument's boundary in the file** — one line naming what is inside the timed or
  measured region and what is scaffolding. `pf-pool-scaling` run 1 timed a `sudo` drop inside the
  measured region and inflated every figure 2.09×; a round-4 sudo ticket got reported as a finding
  about the product.

**A bad run that agrees with you is the one that survives.** In the same count, invalidated runs
split **29 confirming the hypothesis to 8 contradicting it** — and every contradicting one was
chased inside its round, because a contradicting result makes you look. That asymmetry is why this
rule is structural instead of an instruction to stay sceptical.

## 4. Results land continuously; the plan does not move until the round closes

- `results/` is a fact store. Every run lands there as it happens, including the invalidated ones —
  what a bad control looked like is worth as much as the result that replaced it.
- **The plan is not touched mid-round.** Intermediate optima are working state.
- At round close, **one synthesis pass** rewrites the plan against the whole fact set.

`agent-failures.md` sets its bar at *reached a durable artifact*. The same bar applied to a design
is the whole rule: **a mechanism choice that will be revisited before the round ends has not earned
a place in the plan.**

## 5. Plans state invariants; mechanisms live in the round

Sort every claim in a plan into two bins and keep them visibly apart.

- **Invariant** — what the mechanism must achieve. *Revocation must stop an in-flight transfer.*
  *Policy must not be inheritable by a recycled identity.* *Enforcement liveness must be detectable
  from outside the guest.* Every one of these survived the entire 2026-08 pass unchanged.
- **Mechanism** — how. `pfctl -k` versus a return rule versus a stateless bidirectional shape;
  address key versus interface key versus ingress tag. **All of the thrash lived here.**

[GEN §12](../principles/general-principles.md) already says a design is a hypothesis until
implementation verifies it. A plan that does not mark which of its claims that applies to gives an
invariant and a mechanism guess identical authority — so when a mechanism reverses it reads as the
plan failing, rather than as the plan working exactly as intended.

## 6. Classify a run when you invalidate it

An invalidated run keeps two fields, written **by whoever invalidates it, at the moment they do**,
while the cause is in hand:

- **`Class:`** — `free-negative` (the §11 class, still the largest bucket), `frame-capture`,
  `instrument-in-region`, `predicate-bug`, `inference-overreach`, `confounded-arms`,
  `no-verdict`. Definitions and specimens: [`design/research/verification-method.md`](../design/research/verification-method.md).
- **`Direction:`** — `confirmed` or `contradicted`. Whether the bad run agreed with the hypothesis
  in play.

**Never sweep these retroactively.** Classifying your own bad runs after the fact is the same
interpretive act that produced them, performed by the same reader — and it manufactures the
appearance of data. D136 records one hand count, dated and labelled, and does not repeat it.

Aggregates are a `grep -c`, computed on demand. **Never store a total**: it drifts on the next entry
and nothing enforces it. Cite the entries.
