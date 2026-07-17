> **ABOUTME:** Catalogues major failures by the agents that work on this repo — what was claimed,
> what was true, and above all what caught it — so the commonalities become visible and gateable.
> The sibling of `backend-idiosyncrasies.md`: the agent is a component too, and this is its entry.

# Agent failures

The agent is a backend. `backend-idiosyncrasies.md` catalogues external components that
contradict their own docs and cannot be changed, only characterised and worked around; this
catalogues the one component that writes the code. It is not deterministic in the way containerd
is, but it has identifiable, repeating behaviours — the docs prove they can be **guided**, and
`scripts/check_citation_provenance.py` and `scripts/check_breaking_changes.py` prove they can be
**guarded**. Neither is possible without specimens.

**Why the raw specimen and not just the lesson.** The lesson is recoverable; the specimen is not.
Once you know the answer, the error looks obviously avoidable, and the honest account of *why the
wrong claim felt exactly like knowledge* evaporates — leaving "be more careful", which is the one
conclusion known not to work here. `research/llm-shaped-repos.md` is a one-session capture written
for the same reason ("the evidence and the arguments, written down while they were still
recoverable"), frozen at 2026-07-15. This file is the standing version it should have been. Its
Part 7 asymmetry table is the classification scheme; entries here cite its rows.

**The bar is a major gaffe, not a mistake.** A wrong first guess that the next tool call corrects
is the process working. An entry belongs here when the false belief **reached a durable artifact**
— a commit, a decision, a filed finding, a recommendation acted on — or would have, but for
someone catching it. Volume is the enemy: a log of every fumble is unreadable and therefore
unanalysable.

**This file is only half the story, and the darker half.** A failure leaves wreckage to find; the
other kind of bump leaves nothing — the owner says one offhand sentence, a complex plan collapses
into a simple one, and there is no scar to file. Those go in
[`design/research/uncanny-valley.md`](design/research/uncanny-valley.md), which collects moments the
interaction stopped resembling a human one **in either direction**. Do not let the existence of this
file imply the interesting moments are the failures; on the evidence so far they are not.

**This file is only half the story, and the darker half.** A failure leaves wreckage to find; the
other kind of bump leaves nothing — the owner says one offhand sentence, a complex plan collapses
into a simple one, and there is no scar to file. Those go in
[`design/research/uncanny-valley.md`](design/research/uncanny-valley.md), which collects moments the
interaction stopped resembling a human one **in either direction**. Do not let the existence of this
file imply the interesting moments are the failures; on the evidence so far they are not.

## Fields, and the one that matters

- **Claimed / True** — the gap.
- **Source of the false belief** — where it came from: grep output, a subagent's summary, a code
  comment, an error string, a doc's prose. This is the *provenance of the error*, and it is what
  the class turns out to be about.
- **Caught by** — **the load-bearing field.** It is the only one that says what to build. If most
  entries read "the owner asked a question", no amount of instruction-writing is the answer and
  the honest conclusion is that the mechanisms are absent, not weak.
- **Cost** — how far it travelled.
- **Class** — the `llm-shaped-repos.md` Part 7 row, or a new one this file is proposing.
- **Gated now?** — what, if anything, would catch it today.

## Patterns so far

Thin, and stated with the sample size attached, because a pattern asserted from seven specimens
is a hypothesis. Update as the corpus grows; **do not** let this section become a claim the
specimens do not support (D121: don't count what nothing enforces — the same applies to a pattern).

**1. Execution catches my errors. Reading does not.** Every specimen below where the agent caught
itself (A4, A5, A6) involved *running something* — a probe, a measurement, a comparison of two
numbers. Every specimen that reached a durable artifact uncaught (A1, A2, A3) was a claim written
from something *read*: grep output, a subagent's report, a comment, an error string. The defence is
therefore not "read more carefully" but "execute something", which is what D119 already says — *"a
finding parks its fix, never its verification"* — and which failed anyway (see "how this gets
written", below).

**2. The false belief always arrives second-hand, and second-hand is invisible from the inside.**
Not one of A1–A3 was invented. Each was *inherited*: from a plan's audit, a subagent's summary, a
code comment, an error message. Each source was written by someone reasoning about a different case
than the one in front of me, and none of them announced that. This is Part 7's "No provenance on a
fact", and it is the class with the highest hit rate in this corpus.

**3. I reason from the read site about a write site.** A1, A2 and A3 are all the same movement: I
examined the code that *consumes* a value or state and asserted a fact about what *produces* it.
"Who writes this?" is a grep, not a model — which is why it is mechanisable and why the failure is
embarrassing rather than deep.

**4. The catch rate of the mechanisms, on this corpus, is one — and it arrived within an hour of
the file existing.** A1, A2 and A3 (the interpretive class) were each caught by the owner asking a
plain question; no hook, test or gate fired on any of them. Then **A0** was caught by
`check_citation_provenance.py`, on a live edit, unprompted — and it was a *false accusation* that
was nonetheless worth complying with. Two things follow, and both should be re-checked as entries
accumulate: the gates do fire, and so far they fire on **citation provenance**, not on the
interpretive class that does the damage. If that split holds, it says the guardable surface and the
dangerous surface are not yet the same surface.

## Specimens

Newest first. Every entry here is from a single session (2026-07-16/17) — the first sustained
attempt to record them, so the corpus is deep on one session and empty before it. That skew is
itself worth knowing when reading pattern 4.

### A0 — the gate fired on its own author, and was right anyway (2026-07-17)

- **Claimed:** implicitly — that citing a research doc this session had *composed* needed no
  further provenance.
- **True:** the hook disagreed and blocked the edit: *"You just cited uncanny-valley.md, but nothing
  in this session opened that file."* It exempted the file being edited, but nothing authored earlier
  in the session, and `Write` is excluded from `READ_TOOLS` by design. It could not tell "I wrote it"
  from "I never saw it" (DF133, fixed).
- **Caught by:** **`check_citation_provenance.py`** — a mechanism, on a live edit, unprompted. The
  first entry in this corpus whose Caught-by is not "the owner asked".
- **Cost:** none. It cost one file read and produced a fix plus a finding.
- **Class:** a *false accusation* — the failure mode D122 named as the one that gets a hook disabled.
  Its trigger was ordinary (write a doc, wire it into an index); it had simply never fired because no
  research doc had been authored since the hook landed two days earlier.
- **Worth keeping for the shape, not the bug.** The demand was absurd — *open the file you wrote ten
  minutes ago* — and the action it forced was legitimate: the read verified that the claim being made
  about the doc actually matched its ABOUTME. **A gate can be wrong about its reason and right about
  its remedy.** The reflex on being blocked by an obviously-confused gate is to delete or bypass it;
  the check was worth doing regardless of the confusion, and complying cost ten seconds.
- **Gated now?** It *is* the gate. Fixed to count authorship (path only, never body), with tests
  pinning both halves.

### A1 — seatbelt was recorded as a defect it structurally cannot have (2026-07-17)

- **Claimed:** seatbelt's prune matches instances by name prefix, so it is affected by DF115 and
  needs the label-equality fix. Written into DF115's disposition, into D126's audit sentence, and
  into hand-off instructions for a macOS agent.
- **True:** seatbelt identifies candidate processes by *path under its own `SandboxesDir()`* — a
  stronger guarantee than label equality, and structurally immune. The `InstancePrefix` call in its
  prune is a `TrimPrefix` normalising the *known* set, not a candidate filter. The file says so at
  `prune.go:66`.
- **Source of the false belief:** an audit in the plan I was implementing, corroborated by grep
  output showing `config.InstancePrefix` at `seatbelt/prune.go:80`. Both true. Neither meant what I
  took them to mean.
- **Caught by:** the owner asking "what is left for DF115?" — which made me open the file for the
  first time. Nothing else would have; I had already shipped the claim twice.
- **Cost:** high. A filed finding, a decision entry, and instructions that would have sent another
  agent to implement a fix for a defect that did not exist, in a file that was already correct.
- **Class:** No provenance on a fact + reasoning from the read site.
- **Gated now?** Partially. `check_citation_provenance.py` now gates source paths cited in findings
  — but only fully-qualified ones, and this citation was **`seatbelt (prune.go:80)`**, a bare
  basename with the package in prose. Four files are named `prune.go`; the session had opened two.
  The gate cannot resolve it. Closing that needs a finding-format rule requiring repo-relative
  paths — filed as the open half of DF129's neighbourhood.

### A2 — a live recovery path was recommended for deletion as dead code (2026-07-17)

- **Claimed:** `MigrateCLI`'s stamp-only branch "exists for unreleased builds, so no released
  version ever produced that layout; if that is confirmed, it is already dead code". Written into
  the deprecation register, and pitched to the owner as "worth ten minutes to confirm and delete".
- **True:** its *comment* says "an interim build". Its *condition* is any TOP where `library/` or
  `cli/` exists without the CLI stamp — which a shipped install reaches whenever the library realm
  is created without the CLI. Deleting it would have removed the repair for a state the startup
  gate refuses.
- **Source of the false belief:** a subagent's inventory, plus the code's own comment. I read the
  comment and propagated it; I never opened the function.
- **Caught by:** the owner asking "What is clischema.go? What does it do?" — a question with no
  suspicion in it.
- **Cost:** would have been a deleted recovery path. Caught one question before anyone acted.
- **Class:** No provenance on a fact; a comment is a *claim*, and I treated it as an observation.
- **Gated now?** Yes, this one. The deprecation register is in
  `check_citation_provenance.py`'s scope, and `internal/cli/cliutil/clischema.go` was cited as a
  full path I had never opened — it would block today.

### A3 — a severity was justified with invented reachability (2026-07-17)

- **Claimed:** DF128, MEDIUM: `TOP/cli` without `TOP/library` means a populated install lost every
  sandbox, and `system migrate` "launders the evidence". Reachable via "a wrong `--data-dir`, a
  partial restore, or a backup that captured `TOP/cli` and not `TOP/library`".
- **True:** `initFreshDataDir` creates the CLI realm and *then* the library realm. Any interruption
  between those two lines lands exactly there, on a first run holding nothing. Migrate's repair is
  correct. The three causes I listed were invented — none observed, none checked.
- **Source of the false belief:** the gate's own error string, *"a realm went missing"*. It is the
  author's belief about a state, and I read it as a fact about how the state arises.
- **Caught by:** the owner asking "Is that for half-initialized (and then it crashed) or a
  migration?" — the question the filing itself should have contained.
- **Cost:** a filed MEDIUM finding, wrong in its direction: it implied making migrate *more
  paranoid*, when the actual defect is the gate's message being wrong about a routine state.
- **Class:** Code-says beats system-means — *"a true fact about the code and no model of what the
  code is for"* — plus fluency: the invented causes read exactly like the verified ones beside them.
- **Gated now?** No. Nothing checks that a severity resting on "this state occurs" names the code
  that *writes* the state. A finding-format field (`Produced by:`) would force the grep, and the
  trigger is observable — reachability language in a Severity line. Not built; see DF132.

### A4 — the proposed fix for DF125 would have rebuilt DF115 by hand (2026-07-17)

- **Claimed:** to let the CLI reclaim its pre-D126 orphans, "prefix backends accept
  `yoloai-<name>`". Pitched to the owner as the plan.
- **True:** `yoloai-` is a prefix of *every* principal's namespace, so that matcher reaps
  `yoloai-acme-*` too — precisely the cross-principal destruction DF115 exists to name, which I had
  removed structurally hours earlier.
- **Source of the false belief:** symmetry. The label backends could adopt the legacy identity
  safely, so I generalised to the prefix backends without re-deriving why the label version was
  safe (it compares by equality; a prefix does not).
- **Caught by:** myself — working the grammars after the owner asked whether the legacy form could
  be identified deterministically. The proof took two minutes and I had not attempted it before
  proposing.
- **Cost:** none. Caught at proposal, before code.
- **Class:** coherence pressure — I had just built the label half and the prefix half inherited its
  correctness by association rather than by argument.
- **Gated now?** No, and probably not gateable. What caught it was executing a collision test
  against the real parsers, prompted by a question.

### A5 — the gate I built to catch this class was silently blind (2026-07-17)

- **Claimed:** `check_breaking_changes.py` detects a renamed CLI flag. Its noise measurement — 0
  firings across 51 real merges — was reported as evidence it was quiet.
- **True:** the extractor matched `Flags().GetBool("debug")`, a *reader*, as a declaration. A flag
  renamed at its declaration stayed in the set via its readers, so the whole flag half detected
  nothing. The 0/51 was partly "quiet" and partly "blind", and the two are indistinguishable from
  the output.
- **Source of the false belief:** my own regex. I inferred that `Flags().X("name")` meant
  declaration from the shape of the pattern, without asking what else matches — the same movement
  as A1, against a pattern I wrote myself.
- **Caught by:** myself, probing the gate with a real rename instead of trusting it. **The only
  specimen in this corpus caught by a mechanism**, and the mechanism was "run the thing".
- **Cost:** none — caught pre-merge. Would have shipped a gate that passes green forever.
- **Class:** a candidate new row — *a gate is code, and gets the same credulity as any other claim*.
  A green gate and an absent gate are indistinguishable without a deliberate failure probe.
- **Gated now?** By convention only: every gate added this session was probed by breaking something
  and watching it fail. That convention is not written down anywhere enforceable.

### A6 — the measurement was contaminated by the act of measuring (2026-07-17)

- **Claimed:** replaying the provenance hook over a real session's transcript produced **0** blocks
  across four finding commits — reported as "the extension is free".
- **True:** 2 blocks, both true positives. The replay used the *finished* transcript; the hook fires
  at *edit time*. Worse, the commands I ran to investigate (`echo "did I cite seatbelt.go..."`, a
  script listing the basename, a grep for it) put the path into the very blob under test. Asking
  whether I had read a file marked it read.
- **Source of the false belief:** a real measurement, run correctly, answering a question one step
  removed from the one I was asking.
- **Caught by:** myself — the number disagreed with an earlier crude estimate, and the discrepancy
  was too large to wave through.
- **Cost:** none, and it produced DF129 (the hook's false-pass path) as a by-product.
- **Class:** a candidate new row — *the observer is in the transcript*. Any check that reads the
  agent's own history is perturbed by the agent investigating it.
- **Gated now?** No. Recorded in the hook's own comments so the next reader inherits the trap.

### A7 — `git add -A` swept an untracked script onto a throwaway branch, which was then deleted (2026-07-17)

- **Claimed:** implicitly, that `git add -A` on a probe branch touches only the probe's edit.
- **True:** it also staged the new, untracked `check_breaking_changes.py`. Deleting the probe branch
  took the only copy with it.
- **Caught by:** `git status` showing a clean tree where a new file should have been. Recovered from
  a dangling commit via `git fsck --lost-found`.
- **Cost:** minutes, and only because the object store forgives. A `git gc` away from real loss.
- **Class:** a candidate new row — *blast radius of a convenience flag*. The agent reaches for the
  broad form (`-A`, `-rf`, `--force`) because it usually works, and the failure is silent.
- **Gated now?** No. Worth considering a rule: never `git add -A` on a branch you intend to delete;
  stage paths explicitly.

## How an entry gets written

This is the honest weak point, and pretending otherwise would make the file another instance of
what it catalogues.

**The trigger is an owner correction.** When the owner contradicts a claim, or asks a question
whose answer turns out to be "I was wrong", that is an **event you can see** — not an uncertainty
you feel. It is the same shape as GEN §7's D123 corollary, which is the corpus's own template for a
rule that fires: *"a returned delegation is an event you can see, unlike the uncertainty you don't
feel."* Every one of A1–A3 has that trigger, and it fired reliably — the owner's question is,
empirically, the most dependable detector in this repo.

**What does not work, on the evidence:** "record your failures" as a standing instruction. That is
the noticing register, and DF132 is the finding that says it does not fire. D119 — *"a finding parks
its fix, never its verification"* — is loaded every session, and DF128 (A3) was filed with an
unverified claim by a session that could recite it.

**Not gated, deliberately, for now.** A corpus of seven cannot justify a mechanism, and a gate on a
file this young would be pinning a shape nobody has learned yet. The thing to gate, when the shape
is known, is probably not the *writing* of an entry but the **format** — a "Caught by" field that
cannot be omitted, so pattern 4 stays computable. Revisit at ~20 specimens, or when an entry appears
whose class already has three siblings.

**When in doubt about whether something belongs: does it have a Caught-by that is not "the owner"?**
If yes, it is evidence a mechanism worked and belongs here for that reason alone — the successes are
as scarce as the failures and twice as useful.
