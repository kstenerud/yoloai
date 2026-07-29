> **ABOUTME:** Maintainer procedure for reviewing a contribution from outside the project —
> reviewing it normally, and then reading the review a second time as an execution trace of the
> contributor docs, which is the only way this project can find out whether those docs work.

# Reviewing an outside contribution

An outside PR is reviewed twice: once as code, then once as **evidence about the documentation**.
The second pass is the part that is easy to skip and the part that pays.

The reason it exists is GEN §17's corollary: **you cannot test your own documentation by reading
it, because you already know what it means.** Every gap — a rule positioned where nobody arrives,
an example narrower than the rule it illustrates, an invariant that lives only in a sink — reads
as fine to the person who wrote it. A contributor who lacks that context is the only instrument
that detects any of them, and a reviewer who treats their mistakes purely as mistakes throws the
reading away.

This is a maintainer procedure. Nothing here is asked of the contributor.

## The method

**1. Review the code normally.** Findings are findings; do not soften them into doc observations.
The contributor's PR is a PR, not an experiment they signed up for.

**2. Re-read your own review as a trace.** For each finding, ask what the docs say and how this
reader would have reached it. Four outcomes, and only two of them are yours to fix:

| The docs… | Remedy |
| --- | --- |
| said it, reachably, and it was ignored | **Nothing.** Restating a rule that was already read is the one move known not to work (GEN §17). |
| said it, but not where this reader arrived, or too narrowly | Fix the **position or the scope**, not the wording's force. |
| never said it | Write it, in a live doc — not in a finding, which is a sink (rule 7's converse). |
| could not have said it — a judgement call | Leave it to review. That is review working, not a gap. |

The count matters more than any single row. Fifteen findings that collapse into three causes is a
documentation problem; fifteen that stay fifteen is an ordinary PR.

**3. Fix the docs before the contributor's next round**, and tell them what changed **without
restating the fixes**. This is the discipline the whole method rests on, and it is
counter-intuitive: detailed review comments *spend* the measurement. Once you have named the fix,
the next round tests whether an agent can follow a list, which was never in question. Say what
moved structurally, cite the rules by number, and stop.

**4. Measure the next round on what you did not say.** Before it arrives, write down which
variables are still unspent — a rule that was stated but not exemplified, a structural change
nobody was told about, a stated rule that was ignored once already. Those are the results. The
rest is compliance.

**5. Record it.** A D-entry for what changed and why; findings for what was not fixed (rule 7).
Bugs the review surfaced in the project's own code are the project's, not the contributor's —
fix them on `main` and let the PR rebase onto the fix. A first-time contributor should not be
carrying a latent defect that predates their branch.

## Cycle log

The running record of what each outside contribution taught. **Append an entry per cycle**, dated;
this is where the programme lives, and it is here rather than in a finding because a finding tracks
a defect and this tracks an activity that does not end. It is also the page someone opens *before*
reviewing a PR, which is where the accumulated lessons need to be (GEN §17).

Keep entries short and factual: what the trace showed, what changed because of it, and what the
next round measured. No running totals — a count is a claim nothing enforces (D121).

### 2026-07 — PR #44 (apple profile Dockerfiles), the first substantial outside contribution

**Round one: fifteen findings, three causes.** An archived plan was read as a specification (one
line of `archive/plans/apple-container-backend.md` accounts for four of the fifteen). Two
engineering conventions the PR broke lived only in `findings-resolved.md`, which the docs' own rules
classify as unswept archaeology. And `AGENTS.md` had no testing rule at all — rules 1–9 were
entirely publishing ritual — while the largest single review comment was that the fix's core was
untested.

**What changed:** an `ARCHIVED` banner in every file under `archive/`; rule 10; rule 7's converse;
both orphaned conventions backfilled into live docs; `ProfileImageBuilder` moved to
`runtime/runtime_optional.go` so backends can compile-assert it. Recorded as D128.

**Round two, measured only on what the review did not name:** every structural fix held. Rule 7 —
the one rule ignored in round one despite being in the shortest document in the repo — produced a
filed finding unprompted. The compile-time assertion nobody mentioned was added. No archived doc
was mined. An argv test appeared with the revert-verification rule 10 asks for.

**The one miss is the most useful result.** Rule 10 named argv as its example, argv was tested, and
a second behaviour change in the same PR went untested — reverting it left the suite green. The
rule was followed exactly as worded, so the wording was the defect. That plus round one's archive
failure is the same shape twice: correct content, narrower reading than intended, invisible from
inside the document. Generalised as GEN §17 and remediated in D129.

**Side effects, neither of them the goal:** writing an invariant down surfaced a live bug that had
been latent for months (DF150), and again on the follow-up (DF154). And an outside PR exposed an
infrastructure assumption single-maintainer work could not — `scripts/next-id.sh` allocated a
finding ID a fork's open PR already held.

Full record: [DF151](../design/findings-resolved.md), D128, D129.

## What this is not

**There is no PR template or contributor checklist, deliberately.** One cycle is one data point,
and a form built from it would encode this PR's specifics as though they were the general case —
the failure mode GEN §17's own evidence argues against, since a checklist is *emphasis* and the
finding was that emphasis is not what reaches people. Rule 7 was already in the shortest document
in the repo and was still missed; a checkbox above it would have changed nothing.

**And there is no dead-name gate**, though it was proposed and designed. Three variants were
measured (see [DF151](../design/findings-resolved.md)): the best-scoped one produced 3 hits across
46 files with **0 true positives**, because the tier where name drift would be dangerous is already
the tier that gets maintained. Both real drifts that exercise found were outside any scope on which
the rule is precise. The transferable part is the method: **measure a proposed gate's precision
before building it** — a noisy gate gets disabled, and a blanket suppression then reads as
coverage (D122).

Revisit when there are enough cycles for a pattern to be visible rather than inferred, and prefer
a **gate** to a checkbox even then — a gate cannot be skimmed. The current instruments are the
`scripts/check_*.py` gates, and each one exists because a specimen was written down first.
