> **ABOUTME:** Recommendations for the verification loop that produced the enforcement pass — why
> its runs keep getting invalidated and its conclusions keep reversing, separated into two failures
> with different remedies. Drawn from the existing corpus; not project doctrine and states no rules
> yet.

# Verification Method

> **STATUS (2026-08-11): superseded in part by [D136](../../decisions/working-notes.md), which is
> what the project actually does.** This file is kept verbatim as the analysis that prompted it —
> its reasoning is the evidence, and rewriting it to match the outcome would destroy that. Read D136
> for the live rules. The three places they diverge:
>
> - **R1 is adopted in a stronger form.** A `REFUTES=` declaration is prose written by the same
>   author the frame captured, so it shrinks the class rather than closing it. D136 requires the
>   probe to *demonstrate*, in the same run, that it reports failure when the mechanism is absent.
> - **R7's ratio was recounted and is much more lopsided than estimated** — see D136 § *The
>   calibration count*. The direction was right; the magnitude was not, and the two populations it
>   totals together turn out to need separating.
> - **R4 is adopted with the half it does not state:** "round" has to be defined by something
>   outside the agent's judgement, or freezing the plan mid-round degrades to freezing it whenever
>   the agent feels finished. See [`procedures/verification-rounds.md`](../../procedures/verification-rounds.md).

**What this is.** An outside reading of `design/research/macos-isolation-spike/results/README.md`,
`design/research/linux-enforcement/results/README.md`, `design/plans/macos-pf-privileged-path.md`,
`agent-failures.md` and `principles/testing-principles.md §11`, aimed at one reported symptom: the
agent ping-pongs between optimal solutions, each invalidated by the next hardware run, some reversed
twice, some reversed by prior art.

**What it is not.** Doctrine. Nothing here has been run against the tree, and every recommendation
is a hypothesis in the D-entry sense — provisional until something implements it and finds out. Where
it proposes a rule, the rule should land as a D-entry first, per `principles/README.md`.

## The two failures, separated

The reported symptom is one experience of two mechanisms, and they have opposite remedies. Both
present identically from inside the loop as *the last run was wrong*.

- **Harness defects.** A run is invalidated because the probe was broken, not because the world
  surprised anyone. Remedy: treat the harness as code under review. More runs make this worse.
- **Design overfitting.** A conclusion reverses because the design was re-derived to optimality
  against an incomplete fact set, and the next fact broke the fit. Remedy: hold the design rougher
  for longer and synthesize in batches. More runs make this *better*, but only if they land together.

The corpus already records which is which, entry by entry, in the two results READMEs. What it does
not do is aggregate them, so the split is invisible and the loop cannot tell which remedy it needs.
That is recommendation **R7** below, and it is the cheapest thing on this list.

## Part 1 — the harness is untested code

`testing-principles.md §11` is the right principle and it does not cover enough. It catches
*vacuity*: a control satisfied by something other than the mechanism under test. Several of the
invalidated runs pass §11 cleanly and are still backwards.

### The classes §11 does not reach

Each row names the specimens it rests on, per `agent-failures.md`'s own convention.

| Class | What happens | Specimens |
| --- | --- | --- |
| **Frame capture** | The probe is written from the same model that produced the hypothesis, so the disconfirming arm is gated on a condition the hypothesis makes unreachable. Controls pass; mechanism present; §11 satisfied. | `pf-no-state-run1` (the fixing arm gated on "did correctness break", which a return rule does not affect), `pf-no-state-run5` (control built from the census cell, verdict drawn from a different one) |
| **Instrument inside the measured region** | The apparatus contributes to the quantity being measured. | `pf-pool-scaling` run 1 (the drop to the unprivileged user timed inside the region, 2.09× uniform inflation), the round-4 `timestamp_type=global` sudo ticket that made every refusal check unanswerable |
| **Predicate and parser bugs** | The verdict turns on a string match that matches the wrong thing, or nothing. | `pf-anchor-eval-run1-predicate-bug` (`com.apple` matched a different top-level anchor), `pf-scrub-collapse-run1-invalidated` (table-name pattern anchored to end-of-line) |
| **Inference overreach** | The measurement is sound; the verdict claims a population it never sampled. | `tart-net-key` run 1 (measured interface *existence*, concluded *keyability*), `df190-mechanism` run 1 (checked the victim only, so a defect changing hands read as recovery) |
| **Confounded arms** | Two changes exercised together, the verdict attributed to one. | `pf-revocation` before R5a/R5b (return rule and state kill run together, rule alone later shown insufficient) |
| **Evidence without a verdict** | The run prints the data and declines to conclude, which reads as a pass at a glance. | `net-ceiling-run1-undecided` |

**The common root, and it is not carelessness.** In every one of these the harness ran clean, exited
zero, and asserted what its author intended. What they share is the A31 shape from
`agent-failures.md`: *the check that disagreed was the one not derived from the reading that produced
the claim.* In this workstream the harness **is** derived from the claim. That is the specific thing
that has gone wrong — pattern 1 holds that execution catches errors where reading does not, and it
holds only while the probe is independent of the hypothesis. Here it is not, so running has inherited
reading's failure mode without losing its authority.

### R1 — Every probe declares its own refutation before it runs

The strongest existing instance is already in the tree: `pf-grant-matrix.txt` runs as root, removes
its own blanket sudo grant for the duration, and **refuses to report anything unless `sudo -n
/usr/bin/true` is first shown to fail**. Generalise that from a technique to a required section.

A harness opens with a `REFUTES=` declaration naming the observation that would make the current
design wrong, and a guard that aborts if that observation is unreachable in the run as configured.

- `pf-no-state` run 1 dies at authoring: its declared refutation was a non-zero state census, and the
  arm that could produce one was gated behind a correctness check that a return rule does not move.
- `net-ceiling` run 1 dies: no verdict emitted means the declaration was never discharged.
- `tart-net-key` run 1 dies: "interfaces exist" cannot refute "interfaces are keyable".

This is pattern 5's preferred answer rather than a placement heuristic — the wrong state is
unrepresentable, not merely warned against. Per A28/A30, that is the only shape with no
counter-specimen in the corpus.

### R2 — The harness gets the review the code under test gets

The harness is currently the largest unreviewed component in the project. `testing-principles.md`
governs the code being tested; nothing governs the thing doing the testing, and roughly three of
every four wrong sentences in this workstream came from it.

Minimum viable version, no new machinery:

- A harness diff is reviewed **before** its run is trusted, not after its output looks surprising.
- The review asks exactly one question, which is R1's declaration restated: *what would this print if
  the design were wrong?* If the answer is "the same thing", the harness is not ready.
- Predicates that discriminate on strings (`grep`, `awk`, table-name regexes) get an explicit
  negative fixture. `com.apple` versus `com.apple/` is the whole `pf-anchor-eval` story, and it is the
  same near-namesake class as `YOLOAI_TEST_TART` versus `YOLOAI_TEST_TART_VM` from
  `research/llm-shaped-repos.md` — a set difference no human or agent catches by reading.

### R3 — State the instrument's boundary in the file

One line per harness naming what is inside the timed or measured region and what is scaffolding.
`pf-pool-scaling` run 1 and the round-4 sudo ticket are the same defect at two scales: the
measurement apparatus was part of the measurement, and in the second case the scaffolding got
reported as a finding about the product.

The results README already carries this as a caveat after the fact ("any other refusal check in this
directory has the same exposure"). Moving it into the harness converts a caveat someone must remember
to read into a precondition the run asserts.

## Part 2 — the design thrash

The reversals are a different animal. X2 went *sufficient* → *confirmed wrong, needs `-k`* →
*superseded, no `-k` at all*. K4c was superseded by I2. The 8-slot decision closed, reopened on an
artifact, and is open again. The grant matrix the brief asked for became moot before it was answered.

Every one of those positions was correct on the facts available when it was written. They broke
because each was fitted tightly to an incomplete set, and the fit had no slack.

**Why the agent cannot leave slack unaided.** This is Part 7's *absence has no representation*,
applied to a design rather than a search. A human holds a rougher design deliberately, because their
model says more facts are coming and roughly what they will be about. The agent has facts and no
model, so it cannot estimate what is still unmeasured, so re-deriving to optimality after each result
is the only move available to it. The remedy has to be structural; asking for restraint is asking a
rule to fire on a state that does not exist.

### R4 — Run the round, then synthesize once

The Linux half already does this and says so: *"Synthesis applied these results on 2026-08-09 — cite
those documents, not this index."* The macOS half has no such barrier, and its plan now carries three
generations of position with a reader instruction to check which section supersedes which.

- Results land in `results/` continuously. That directory is a fact store and should keep doing
  exactly what it does.
- The plan is **not touched** mid-round. Intermediate optima are working state.
- At round close, one synthesis pass rewrites the plan against the whole fact set.

`agent-failures.md` sets the bar for its own entries at *reached a durable artifact*. The same bar
applied to designs is the whole recommendation: a mechanism choice that will be revisited before the
round ends has not earned a place in the plan.

### R5 — Plans state invariants; mechanisms live in the round

Sort every claim in the plan into two bins and keep them visibly apart.

- **Invariant** — what the mechanism must achieve. *Revocation must stop an in-flight transfer.*
  *Policy must not be inheritable by a recycled identity.* *Enforcement liveness must be detectable
  from outside the guest.* Every one of these survived the entire pass unchanged.
- **Mechanism** — how. `-k` versus a return rule versus a stateless bidirectional shape; address key
  versus interface key versus ingress tag; slot-per-sandbox versus slot-per-bridge-index. All of the
  thrash is here.

`GEN §12` already says a design is a provisional falsifiable model, load-bearing only after
implementation verifies it. The plan does not currently mark which of its claims that applies to, so
an invariant and a mechanism guess read with identical authority — and when a mechanism reverses, the
reversal looks like the plan failing rather than the plan working.

### R6 — Sequence cheap constraints ahead of expensive measurement

`prior-art-egress-enforcement.md` is dated 2026-08-08, states plainly that Cilium's approach *"points
away from where our design was heading"*, and the interface-key rewrite landed on 2026-08-10. Reading
costs hours and prunes the design space; a hardware round costs days and resolves one cell. Run in
that order, expensive results get discarded by cheap ones — which is precisely the "reversed when
compared to existing solutions in the wild" complaint.

`GEN §3` is currently read as *check whether a library already does it*. This pass shows it also needs
to mean *check whether the industry converged on a different shape*. Two concrete consequences:

- Prior art is a **gate on opening a verification round**, not a research item that runs beside it.
- The Cilium finding is the strongest single result in the workstream and is filed as research. If
  resolve-at-the-guest dissolves split-horizon and TTL decay structurally, that is a constraint on the
  plan, not a note next to it.

## Part 3 — make the ratio visible

### R7 — Classify every invalidated run, and never total it

Do this one first; everything above is easier to prioritise once it exists.

Each row in the two results READMEs already records *what* a bad run got wrong. Nothing records
*which class* it belongs to, so nobody can see the distribution — and the distribution is the whole
question of whether to measure more or to review the harness.

Add one field to each invalidated row, drawn from the Part 1 table: `Class:`. Then the aggregate
is a `grep -c`, computed on demand, never stored. This follows `agent-failures.md`'s own rule —
**cite the entries, never a total** — for exactly the reason that file gives: a stored count drifts on
the next entry and nothing enforces it.

The one number worth computing by hand once, to calibrate: how many invalidated runs were caused by a
harness defect versus by the world being different from the model. My reading of the READMEs puts
harness defects at roughly three to one over genuine discoveries, but I am counting from summaries
rather than from the runs, and you should recount before acting on the figure. The direction is what
matters. If it holds, "measure more before deciding" is the wrong instinct and is actively making the
thrash worse.

**Also worth a field: `Direction:`.** Whether the bad run confirmed the hypothesis in play or
contradicted it. Every specimen I can classify from the READMEs confirmed it, or produced a free
negative that read as a security result — `pf-spoof` run 2 is the extreme case, 7 PASS / 0 FAIL for
the exact inverse of the truth. If that asymmetry is real and not an artifact of my reading, it is the
single strongest piece of evidence for R1, and it is one column away.

## What to do first

1. **R7** — one field per invalidated row, plus a hand count of harness-defect versus discovery. A
   few hours, and it decides whether the rest of this list is aimed correctly.
2. **R1** — the `REFUTES=` declaration and its abort guard. This is the poka-yoke and it retires the
   largest class.
3. **R4** — freeze the plan mid-round. Costs nothing, stops the reversals from becoming durable.
4. **R6** — prior art as a round-opening gate.
5. **R2, R3, R5** — as the pass allows.

## What this does not address

- **The architecture rung stays hard.** `ARCH §3` is marked emerging and unenforced because whether
  host knowledge binds at the right lifetime is a claim about what the system is *for*. Nothing here
  reaches that; R5 only stops mechanism churn from being mistaken for it.
- **`rule-index.md` remains a summary loaded in place of its sources**, which is the exact mechanism
  `agent-failures.md` pattern 2 names as the origin of every inherited false belief. The verbatim-spine
  rule defends against wording drift, not scope drift. Worth watching for a specimen whose false
  belief traces to a rule-index row rather than to a comment or a subagent summary; that would be a
  new class rather than an instance of an existing one.
- **`where-to-change.md` recipe length is an unexploited fitness function.** A recipe that grows a
  step is a coupling report, and step counts are countable — the `backend` → `container_backend` sweep
  shipping a dead key for fifteen releases is what that signal looks like when it is only recorded as
  a procedure. Out of scope here, but it is the one architecture-altitude signal in the tree that is
  already being computed in prose.
