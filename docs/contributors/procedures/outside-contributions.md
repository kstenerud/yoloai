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

## What this has actually produced

One full cycle so far (PR #44, 2026-07). Round one: fifteen findings that traced to three causes —
an archived plan read as a specification, two engineering conventions that lived only in
`findings-resolved.md`, and an `AGENTS.md` whose ten rules were all publishing ritual and none
about the code. Round two, after the fixes: every structural change held, and the one miss was a
rule applied as an instance rather than as a principle — which was a defect in the rule's wording.
Both failure modes are written up as GEN §17; the remediations are D128 and D129.

Two side effects worth expecting, because neither was the goal:

- **Writing an invariant down found a live bug.** Documenting "staleness markers are keyed by
  backend" surfaced DF150, a real docker/podman defect that had been latent for months. The
  documentation pass is a defect-finding pass.
- **An outside PR exposes infrastructure assumptions that single-maintainer work never does.**
  `scripts/next-id.sh` allocated a finding ID that a branch already held, because it scanned the
  checked-out tree and the ID lived on a PR. That class only appears when work happens somewhere
  other than your own working copy.

## What this is not

**There is no PR template or contributor checklist, deliberately.** One cycle is one data point,
and a form built from it would encode this PR's specifics as though they were the general case —
the failure mode GEN §17's own evidence argues against, since a checklist is *emphasis* and the
finding was that emphasis is not what reaches people. Rule 7 was already in the shortest document
in the repo and was still missed; a checkbox above it would have changed nothing.

Revisit when there are enough cycles for a pattern to be visible rather than inferred, and prefer
a **gate** to a checkbox even then — a gate cannot be skimmed. The current instruments are the
`scripts/check_*.py` gates, and each one exists because a specimen was written down first.
