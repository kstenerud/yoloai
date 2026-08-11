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
- **Tool-surface** — present only when the false belief was made *possible* by the shape of the
  tools the agent works through, rather than by the reasoning on top of them: a view that truncated
  without saying so, an exit code standing in for a structured error, an output whose silence is
  indistinguishable from success. It names the property a file-and-command surface would need for
  the failure to be impossible or self-announcing. **Deliberately distinct from "Gated now?"**,
  which asks what *this repo* can enforce — a tool-surface answer is normally something no
  `make check` can ever reach, because the defect is in the interface the agent reasons through and
  not in the code under review. Kept separate because collapsing them loses the distinction: A31's
  "not gateable" is true of a repo gate and false of a tool surface, and writing only the first
  would have recorded the failure as unaddressable when it is merely not addressable *here*.
- **Gated now?** — what, if anything, would catch it today.

## Patterns so far

Thin, and every pattern **names the specimens it rests on**, because a pattern asserted from a
corpus this young is a hypothesis and the entries are the only thing that makes it checkable.
Update as the corpus grows; **do not** let this section become a claim the specimens do not support.

**Cite the entries, never a total.** This section carried a running tally ("a pattern asserted from
seven specimens", "a corpus of seven") until 2026-07-17, and an agent adding an entry duly
*incremented* it in two places and added a third — while editing the sentence that cites D121
against exactly that. A count is a second copy of what the list below already says, it drifts on the
next entry, and nothing enforces it. The IDs don't drift, and a reader who wants the sample size can
see it.

**1. Execution catches my errors. Reading does not — and A8/A10 show reading can make it worse.**
Every specimen where the agent caught itself by *running something* (A4, A5, A6, A10, A31) — a probe,
a measurement, a comparison, or being forced to turn a claim into code — held. Every specimen that
reached a durable artifact uncaught (A1, A2, A3, A8) was written from something read: grep output, a
subagent's report, a comment, an error string, a summary line. A31 sharpens what "running something"
has to be: the check that disagreed was the one **not derived from the reading that produced the
claim**, and it only ran because a task step happened to re-invoke it.

**A8 looked like the exception and A10 proved it was not.** A8 was "caught by reading the primary
source", and its conclusion was still wrong — because the read answered the question being asked
rather than the question that mattered. Reading is aimed by the premise, so it confirms the frame it
starts in; running the thing is not, which is why it is the only reliable detector here. Note what
this does to "check your work": A10 was checked three times and each check made it **more** wrong and
**more** citable. The defence D119 already states — *"a finding parks its fix, never its
verification"* — is loaded every session and failed anyway (see "how this gets written", below).

**2. The false belief always arrives second-hand, and second-hand is invisible from the inside.**
Not one of A1–A3 was invented. Each was *inherited*: from a plan's audit, a subagent's summary, a
code comment, an error message. Each source was written by someone reasoning about a different case
than the one in front of me, and none of them announced that. This is Part 7's "No provenance on a
fact", and with A8 it is the largest class here: A1, A2, A3, A8.

**2b. A source does not have to be wrong to mislead; it only has to be silent.** A8's source was
*accurate* and cited the exact line numbers. What it omitted — that the code runs at a migration
boundary — was simply not its subject, and an omission carries no marker. So "check the source" is
not sufficient guidance if the source is another document: a true sentence about a file is not the
file, and the gap between them is invisible precisely because nothing is wrong.

**3. I reason from the read site about a write site.** A1, A2 and A3 are all the same movement: I
examined the code that *consumes* a value or state and asserted a fact about what *produces* it.
"Who writes this?" is a grep, not a model — which is why it is mechanisable and why the failure is
embarrassing rather than deep.

**4. Almost everything here was caught by the owner; A0 alone was caught by a mechanism — and it
arrived within an hour of the file existing.** A1, A2, A3 and A8 (the interpretive class) were each
caught by the owner — three by a plain question, one by an *instruction to proceed*; A9 by the owner
watching an edit go past. No hook, test or gate fired on any of them. Then **A0** was caught by `check_citation_provenance.py`, on a live edit, unprompted — and it
was a *false accusation* that was nonetheless worth complying with. Two things follow, and both
should be re-checked as entries accumulate: the gates do fire, and so far they fire on **citation
provenance**, not on the interpretive class that does the damage. If that split holds, it says the
guardable surface and the dangerous surface are not yet the same surface.

**4b. The surfaces may be closer than pattern 4 assumes — one unbuilt rule spans two specimens.**
A1 and A8 would *both* be caught by the hook that already exists, if findings were required to cite
source paths repo-relative rather than as bare basenames (`check_citation_provenance.py:48` calls
the basename hole out by name). That is not a new mechanism; it is a format rule feeding one already
built and already running. It is the first concrete candidate this corpus has produced for gating
the interpretive class, and it came from two entries that look unrelated until filed side by side —
which is the argument for the file.

**5. Proximity is not enforcement, and it sits awkwardly with D56 (A9, one specimen — read it as a
warning, not a result).** A9's rule was inside the sentence being edited, cited D121 by ID, in the
file about this failure mode, and did not fire. D56 says to place a principle where it will stick —
fused onto the mechanism it governs — and that is still the best available *placement* heuristic.
A9 does not refute it; it bounds it. Placement decides whether a rule is **read**, and reading is
not applying. Where the wrong state can simply be made unrepresentable — no count to update, an
`InstancePrefix` that panics — that beats any placement, and the corpus has no specimen of a
poka-yoke failing. Worth watching: if a second entry lands where a co-located rule was read and not
applied, the honest conclusion is that prose placement is a *readability* strategy that has been
doing duty as an enforcement strategy.

**5b. That second entry landed, twice, and both times the rule was in the file being edited.**
A28's harness carried the comment *"a load that silently no-ops is indistinguishable from one that
worked (A22)"* and then measured an effect from a rule that never loaded. A30's harness opens by
declaring *"the snapshot IS the control, so no verdict here can be produced by a test that could not
have gone the other way"* — and its control was the one input never tested. So the warning above
should now be read as the result: a rule written into the artifact it governs is **read** reliably
and **applied** to the case in front of it, not to the artifact stating it. Both were closed by
making the wrong state unrepresentable instead — a load-gate, and a refusal to render any verdict
when a control field is empty — which is pattern 5's own preferred answer and still has no
counter-specimen.

## Specimens

Newest first. Every entry here is from a single session (2026-07-16/17) — the first sustained
attempt to record them, so the corpus is deep on one session and empty before it. That skew is
itself worth knowing when reading pattern 4.

### A12 — I verified the parallel path on the one backend that could not exercise its bug (2026-07-19)

- **Claimed:** that the parallelised conformance harness "works — verified on real docker (green,
  3.0 s)", and committed it, wrote it into the plan, and handed it to the mac on that basis.
- **True:** it panicked immediately on containerd (and would on tart/seatbelt): those backends isolate
  via `IsolatedHome`, which calls `t.Setenv`, and Go **panics on `t.Setenv` inside a `t.Parallel()`
  test**. The harness called `setup(t)` inside each parallel subtest. Docker and podman were the
  *only* backends whose setup does not touch the environment — so docker was the single backend that
  **could not reproduce the failure**, and it was the one I verified on.
- **Source of the false belief:** I verified on the backend that was runnable unprivileged on the
  Linux host (docker) and generalised "docker green" to "the parallel path is correct." The property
  that breaks it — a setup that calls `t.Setenv` — is invisible from docker, because docker's setup
  is the one that doesn't. The convenient instance and the risk-exercising instance were different
  instances, and I only had the convenient one.
- **Caught by:** **`releasetest` on a real `IsolatedHome` backend** (`TestContainerdConformance`) — a
  *mechanism*, not the owner's question. Worth flagging loudly: the running corpus claim is that the
  mechanism catch-rate for interpretive errors is ~0 and the owner's question catches everything. This
  is a counter-example — the integration suite on a backend docker could not stand in for caught it
  cold. The lesson is not "mechanisms don't work" but "the mechanism has to run the case the shortcut
  skipped."
- **Cost:** medium. It reached committed code, the plan's "verified" claim, and the mac handoff, and
  broke `releasetest`. Caught before merge; the fix is call-setup-once.
- **Class:** sibling of A11 — verifying the wrong thing. A11 checked a file's contents and not its
  build tag; this checked one backend and not the property that differs across backends. Both are
  *green on the reachable case, generalised to the unreachable one*. The tell both share: the check
  ran, so `check_citation_provenance` and my own confidence were both satisfied, while the thing that
  mattered was never in the run.
- **Gated now?** Partially, and better than most. A fake-backend regression test now calls `t.Setenv`
  in setup on the non-sharing (parallel) path, reproducing the exact panic on Linux — so *this*
  regression is caught by the untagged... no: it is integration-tagged, but it runs without a real
  daemon, so any run of the integration suite catches it. The general lesson — verify on the instance
  that exercises the risk, not the one that is cheap to reach — is not mechanisable; it is the same
  movement as pattern 3, one level out.

### A11 — I verified the file's contents and took its build tag on trust, in the same read (2026-07-19)

- **Claimed:** in DF145's disposition, that the Phase-2 sites (`runtime/apple`, `runtime/tart`,
  `keychain_darwin.go`) are *"`//go:build darwin` and cannot be built off a Mac"* — that being the
  stated reason the whole error-fix workstream splits into a Linux phase and a Mac phase.
- **True:** `runtime/apple` and `runtime/tart` carry **no** build tag; `go build ./runtime/apple/`
  and `./runtime/tart/` both exit 0 on this Linux host. They gate at *runtime* — `tart.go:213`'s
  `New()` returns a `PlatformError` when `isMacOS()` is false — not at compile time. Only
  `keychain_darwin.go`, a `_darwin.go` suffix file, is genuinely un-compilable off darwin. The real
  reason to defer is *verification* (needs the tart CLI / Apple Silicon / `security` tool), which the
  claim conflated with *compilation*.
- **Source of the false belief:** the opaque-error audit subagent's summary, which called them
  "darwin-tagged files". I had *opened* apple.go and tart/build.go minutes earlier — the citation
  hook forced it — and verified the error-handling claims line by line. But I checked the file's
  *contents* and never ran `head -1` on it, so the *build-tag* claim rode along unchecked inside a
  file I had genuinely read.
- **Caught by:** the owner's question — *"Shouldn't tart be limited to macos only?"* It was not a
  correction of the build-tag claim; it was a worry about a different thing (why tart compiles on
  Linux at all) that forced the `head -1` / `go build` check, which then exposed the DF145 wording.
  The owner's question again, catching a thing it wasn't even aimed at.
- **Cost:** medium. It reached a filed, committed finding and was the stated rationale for the
  Linux/Mac phase split; corrected the same session before any Phase-2 work rested on it. Had it
  stood, a later reader would have believed the apple/tart edits *needed* a Mac to compile, narrowing
  where the work could be done for no real reason.
- **Class:** No provenance on a fact — but a sharper sibling of A10's: I *did* open the source, so
  `check_citation_provenance` passed cleanly. Verifying one property of a file (its contents) lent
  unearned confidence to a different, unchecked property of the same file (its build constraint).
  Candidate row: *reading a file for claim X does not verify claim Y about it* — the hook proves the
  open, not the check, and the two feel identical from the inside.
- **Gated now?** No. The provenance hook saw the reads and was satisfied — that is precisely its
  blind spot: it checks that the file was opened, not that the asserted property was the one looked
  at. The cheap mechanisable rule: a platform/build-tag claim is a one-line check (`head -1`, or
  `go build ./pkg/` on the dev OS), cheaper than the sentence that makes the claim.

### A10 — three corrections, each more fluent, all circling a struct I never opened (2026-07-17)

- **Claimed:** that DF113's fix is schema-gated and must ride v0.9.0. Three times, in three
  incompatible ways: (1) it needs a read-time backfill like `ImageRef`; (2) **A8** — no, it needs the
  v4→v5 migration's backfill, *"the same bill DF126 pays"*; (3) no, it needs an `environment.json`
  `metaVersion` 3→4 bump, which forces every sandbox through `system migrate`, and v0.9.0 already
  forces that — so it is free now and costs a forced migration later.
- **True:** no new field is needed at all. `store.Environment` already carries **`CreatedAt`**, and
  `internal/orchestrator/create/create.go:701` writes `CreatedAt: time.Now()` — a per-sandbox
  timestamp, on disk, written by the create that provisioned the instance, present on every record.
  That is exactly the "fact on disk that `start` can read" I kept arguing had to be added. The
  genuinely missing half is a **runtime capability** to report an instance's age or identity, which
  is an optional interface — the shape D126 shipped for `runtime.Renamer`, with no schema bump.
  Interfaces ship in any release. Nothing about DF113 was ever release-gated.
- **Source of the false belief:** a one-line gloss on the staging page — *"wants a provenance field
  in metadata, i.e. schema"* — which I then defended rather than checked. Every subsequent argument
  refined the *consequences* of a new field; not one asked whether a field was needed. The premise
  entered as someone's shorthand and was never again visible as a claim.
- **Caught by:** myself, and only because building it forced me to decide the field's *shape* — at
  which point I opened the struct and the field was already there. The trigger was **implementation**,
  the same as A4/A5/A6: not scepticism, but the moment a claim had to become code and could no longer
  stay a sentence.
- **Cost:** high, and it is the most-travelled error in this corpus. It reached the finding (twice),
  the staging page, a `**Rides:**` field, three commit messages, an entire release-scope decision,
  and A8 — *an entry in this very file, filed as a lesson about being wrong, which was itself wrong.*
  It shaped the build order and was about ten minutes from being built.
- **The shape worth keeping: I was right first, and corrected myself into error, twice.** The
  original instinct — "additive, ships anytime" — had the right conclusion and a wrong reason. Each
  "correction" fixed the reason and broke the conclusion, and each read as *more* rigorous than the
  last: A8 even cites the exact line and quotes the code's comment. **Fluency rose monotonically
  while accuracy oscillated.** `research/llm-shaped-repos.md` Part 7 says "fluency is constant"; this
  says something worse — under repeated self-correction, fluency *compounds*, because each pass adds
  real detail to an unexamined premise. A wrong claim with three citations is far more dangerous than
  a wrong claim with none, and I built it myself, incrementally, in good faith.
- **Class:** No provenance on a fact, plus a candidate new row — *the premise is invisible to the
  argument that rests on it*. Refinement operates on the reasoning; the assumption underneath is
  never in the frame, so more thinking makes it *stronger*, not weaker. This is why "check your work"
  is not a defence: I checked my work three times and each check made it worse.
- **Gated now?** No, and none of the existing gates come close — every one of the three arguments
  cited real files with real line numbers, and I had read those files, so `check_citation_provenance`
  passes cleanly. The only thing that broke it was needing to write the code. The nearest thing to a
  rule: **a claim that a thing must be *added* is a claim about absence, and absence is the one thing
  reading cannot verify** — you can only check that it isn't there. `grep` for the field before
  arguing about the field's cost. That is mechanisable in principle and unwritten in practice; it is
  the same movement as pattern 3 (reasoning from the read site about a write site), one level up.

### A9 — the rule was in the sentence I was editing, and it did not fire (2026-07-17)

- **Claimed:** nothing, explicitly. While adding A8 I updated this section's running tally of
  specimens from "seven" to "nine" in two places, and wrote a third count ("four of nine") into
  pattern 2.
- **True:** the tally is a hand-maintained duplicate of the list directly below it, drifting on every
  new entry — D121's "don't count what nothing enforces" and the same denormalization DF103 is filed
  for. The pre-existing "seven" was already the defect; I did not evaluate it, I *incremented* it.
- **Source of the false belief:** the number was already there. An existing field in a doc I was
  editing read as a thing to keep current, and "keep it current" is a well-formed, virtuous-feeling
  action. Nothing in the act of updating a number prompts the question of whether the number should
  exist.
- **Caught by:** the owner, watching the edits go past — *"You're counting things again. I've watched
  you convert 'seven' to 'nine' in two places."* No gate; `make check` does not read prose.
- **Cost:** would have been three drifting counts in the file whose subject is claims that drift.
  Caught in the same session, before commit.
- **Class:** Coherence pressure, in its cheapest form — matching the local shape of the text instead
  of asking what the text is for. Note the neighbours: A2 propagated a comment's claim, A8 propagated
  a citation's silence, and this propagated a number's existence. All three are *inheritance from the
  artifact I was already looking at*, which is the corpus's dominant movement.
- **The finding worth more than the specimen: proximity is not enforcement.** The violated rule was
  not in some unread doc — it was **inside the sentence being edited**, naming D121 by ID, in a file
  *about* this exact failure mode. It still did not fire. This is the strongest available evidence
  for what the instruction corpus keeps assuming and should stop: that a rule near the work will be
  applied at the work. Reading it and applying it are different operations, and only one of them was
  happening. If a rule co-located with its own violation cannot fire, **no placement can** — the
  lever is mechanical (make the wrong state unrepresentable: no number, no drift) or it is nothing.
  Compare D126's poka-yoke, which did not ask anyone to remember not to write `"yoloai-"`; it made
  `InstancePrefix` panic.
- **Gated now?** No, and this one is plausibly gateable — a count in prose has a shape (a number-word
  or digit near "specimens"/"entries"/"of nine"). But that is a linter for one file, which is the
  wrong trade. The durable fix is the one applied: **remove the counts**, so there is nothing to
  keep current. An absent field cannot drift.

### A8 — the correction was the error; the page it corrected was right (2026-07-17)

> **A8 is itself wrong, and is kept for that reason — see [A10](#a10--three-corrections-each-more-fluent-all-circling-a-struct-i-never-opened-2026-07-17).**
> Its *fact* holds: the `ImageRef` backfill really does run inside `func migrate`, at the migration
> boundary, deliberately. Its *conclusion* does not: no new field was ever needed, because
> `Environment.CreatedAt` already existed, so the schema question A8 settles so carefully was moot.
> The page it defends as "right" was wrong. The original claim it talks me out of had the right
> conclusion and a bad reason, and A8 replaced a bad reason with a *good* reason for a **worse**
> answer — which is exactly why it is left standing. An entry in a file about being wrong, written
> as a lesson, wrong. Read the two together or neither.

- **Claimed:** DF113's remedy is not schema-gated. A provenance field is "additive with a legacy
  backfill — exactly what `ImageRef` already does for pre-existing records (`environment.go:184-188`)",
  so it ships in any point release and `next-release.md`'s *"i.e. schema"* was wrong. Pitched to the
  owner as a correction to file, and accepted — *"file the correction"*.
- **True:** that backfill is inside **`func migrate(meta *Environment)`**, and the comment beside it
  states the convention it is an instance of: backfill **at the migration boundary**, *"rather than
  coercing it at read time, so the rest of the codebase can treat an empty BackendType as genuinely
  broken metadata."* A field whose absence means "legacy, trust it" is exactly what this codebase
  does *with* a migration. `BackendType` and `ImageRef` both took that route. The staging page was
  right, and my correction to it would have been wrong.
- **Source of the false belief:** DF126's prose, which cites the backfill **accurately** — *"with a
  legacy backfill to `yoloai-base` (`environment.go:184-188`)"* — and says nothing about where it
  runs, because that was not its subject. I read a true sentence written for another purpose and
  supplied the missing half without registering that I had supplied anything.
- **Caught by:** the owner saying *"file the correction"* — which sent me to open the file to cite the
  precedent properly. Not scepticism; **agreement**. The instruction to act is what forced the read
  that killed the claim.
- **Cost:** none, by one tool call — but it was travelling. Its conclusion ("DF113 ships in 0.9.1")
  would have dropped the item from the last release that can carry it cheaply, and it would have
  landed as a *correction*, a genre that reads as having been checked.
- **Class:** No provenance on a fact, with a wrinkle this corpus has not had: **the citation was
  correct and the inference from it was not.** A1–A3 inherited claims that were wrong for my case.
  Here the source was accurate, precise, and load-bearingly *incomplete* — and incompleteness has no
  marker on it. That is Part 7's "Absence has no representation", applied to prose I was reading
  rather than to state on disk. The tell would have been noticing that "I know where this line is"
  and "I have seen this line" felt identical. They always do.
- **A dangerous adjacent shape: agreement is not verification.** The owner approving a claim was, in
  the moment, indistinguishable from the claim being checked — and it is the strongest such signal
  available. The corpus says the owner's *question* is the most reliable detector here; the mirror is
  that the owner's *assent* is not a detector at all, and it feels like one.
- **Gated now?** **Not as written — but the fix is already named, and this is its second specimen.**
  `check_citation_provenance.py` requires a repo-relative path that resolves (`(repo_root /
  m.group(1)).is_file()`); **bare basenames are deliberately unmatched** (a known hole, stated at
  `scripts/check_citation_provenance.py:48`). My citation would have been `` `environment.go:184-188` ``
  — bare — so the hook stays silent. Written repo-relative it **would have fired**: `environment.go`
  appears in no tool input this session before I opened it. So the gate's coverage here rests
  entirely on citation *format*, which nothing enforces. **A1 identified exactly this** ("closing that
  needs a finding-format rule requiring repo-relative paths"). Two independent specimens now turn on
  one unbuilt rule, on a hook that already exists and would work — see DF129's neighbourhood.

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

### A13 — a "24 of 103" measurement counted file renames and renumberings as filings (2026-07-29)

- **Claimed:** 24 of 103 resolved findings were "born resolved" — filed straight into
  `findings-resolved.md` rather than drained from the active queue. Written into two live docs
  ("about a quarter of the entries below arrived this way") and a commit message, as the evidence
  that the direct path is normal practice.
- **True:** unknown, and lower. The method was `git log -S "### DF<n> — " -- findings-resolved.md`,
  which reports every commit where that string's count in that path changed — so it cannot
  distinguish *an entry being created there* from *the file being renamed* or *an entry being
  renumbered into existence*. DF1 and DF9 came from `docs: reverse queue-file naming to topic-first`
  (a rename). DF23 came from `docs(findings): renumber duplicate DF19/DF20 to DF23/DF24`. Both were
  counted as deliberate filings.
- **Source of the false belief:** an exhaustive, scripted measurement over the whole corpus. It felt
  like the opposite of a guess — 103 entries, no sampling, a real tool — and the flaw was in what
  the tool's output *means*, one level below where the rigour was being applied.
- **Caught by:** the owner, offering an unrelated hypothesis about *why* items land there ("put
  together in the same commit at the end"). Testing that hypothesis meant opening the commits, which
  is the first time anything looked at what the commits actually were. The count would have stood
  otherwise; nothing about it looked shaky.
- **Cost:** two live docs and a commit message carried the figure for one commit. Corrected in the
  next.
- **Class:** the Part 7 *primary-source* row, one turn removed — the primary source was consulted
  and answered a question adjacent to the one asked. A5 and A6 are the same shape: a real
  measurement, run correctly, whose result did not mean what it was read to mean. `-S` answers "did
  this string's count change here", never "was this entry authored here".
- **Gated now?** No, and probably not gateable. The transferable rule is narrower and cheap:
  **`git log -S` over a path measures churn, not authorship** — when the question is "where did this
  entry come from", read the commits, and expect renames and renumberings in any corpus old enough
  to have been reorganised. D121's "don't state a count nothing enforces" would also have stopped
  this at the doc, independently of the method being wrong.

### A14 — reimplemented an approach the backend-idiosyncrasies entry had explicitly rejected (2026-07-29)

- **Claimed:** DF152's cheap fix is to key the profile build marker by daemon endpoint, recommended
  over the label-based scheme as "cheap, independent, no interface change". Built, tested, committed.
- **True:** the base-image path had already faced this exact choice and gone the other way, and
  `backend-idiosyncrasies.md` records it — *"Stop tracking docker base-image freshness with a
  host-side marker at all"* — including a rejected patch that keyed the marker by daemon identity.
  My key is better than the rejected one (`DaemonHost()` is client-side and never empty; `docker
  info .ID` is daemon-reported and empty on podman), so it is not the same flaw. It is the same
  *shape*: partition a host-side marker by store, on the sibling of the path that abandoned exactly
  that. The commit closes a real bug and is the wrong end state.
- **Source of the false belief:** reading `runtime/docker/build.go` and `docker.go` — the code — and
  reasoning from the mechanisms found there. The comments in those files explain what the base path
  *does*; the idiosyncrasy entry explains what it *rejected and why*, which is the half that decides
  a design question. Code shows the surviving branch, never the pruned one.
- **Caught by:** the owner, reframing the question rather than correcting a fact — "this feels more
  about bringing mechanisms into harmony". Checking that frame meant looking for prior art on the
  mechanism, which is what surfaced the entry. Nothing in the code would have said it.
- **Cost:** one committed fix that is now marked interim, and a recommendation to defer the correct
  approach. Also produced two by-catches: the entry's apple justification is wrong ("a Tart VM
  image, not OCI" describes tart; apple is OCI), and that misattribution was load-bearing — it read
  as a hard obstacle to harmonising.
- **Class:** the Part 7 *primary-source* row, with a twist worth naming — **the primary source for a
  design decision is not the code, it is the record of the decision.** A1/A8's lesson was to read
  the source; this one is that "the source" for *why not X* is never the file where X is absent.
- **Gated now?** No. `AGENTS.md` already says to read `backend-idiosyncrasies.md` **before**
  diagnosing any backend problem, and I did not — so the instruction exists and was not reached,
  which is GEN §17's failure mode rather than a missing rule. The narrow, transferable habit:
  **before implementing a mechanism on one path, grep the idiosyncrasies file and the sibling path's
  history for the same mechanism** — divergence between siblings is this repo's most repeated defect
  (four instances in one release; see DF152).

### A15 — six passing tests certified a function no backend could reach (2026-07-30)

- **Claimed:** DF156's resume half is built. `InstanceInfo.ImageID` is populated from what each
  backend records, `warnIfImageLineageStale` compares the instance's image lineage against the base,
  and *"six branches, one test each, three failing on revert"*. Committed as working.
- **True:** the function returned on its first line on every backend, always, and had never once run.
  It hung off `StatusSuspended`, which is set only by `InstanceInfo.Suspended`, which only **tart**
  sets — and tart implements no `ProfileImageBuilder`, so the capability guard rejected it every
  time. The intersection of "reports Suspended" and "has images" is **empty**. The case that does
  occur, Done/Failed relaunching inside the existing container, was never wired. Two of the three
  backends could not have answered anyway: apple's digest was read from the wrong JSON path and its
  image is unresolvable *and* deleted on rebuild, and containerd handed back a tag that
  `ImageLabels` then re-resolved — the exact question the design was built to avoid.
- **Source of the false belief:** the tests. `lineageFake` reported `Suspended: true` **and**
  implemented `ProfileImageBuilder`. No backend has that shape, and nothing prevented inventing it.
  Each branch was verified against a mock of a backend that does not exist, so all six passed and
  all six were vacuous. Revert-testing was performed and did not help — reverting a line inside an
  unreachable function still turns its test red, because the test calls the function directly.
- **Caught by:** the owner asking *"verify that this actually is going to solve the issue properly"* —
  a question about the whole, where every check I had run was about a part. Tracing the call graph
  upward from the function to a real backend takes one grep, and nothing in the task as I had framed
  it ever asked for it.
- **Cost:** none shipped — caught in the same session. But the feature was complete, tested,
  committed, and documented as done in the finding, which is as far as a defect can travel here.
- **Class:** a new one, and the sharpest in the file — **a fake is free to invent a capability
  combination the product does not contain**, and when it does, its tests certify a dead path. It is
  the mirror of A5 (a gate that was silently blind): here the tests were the blind gate. Note it
  defeats rule 10 as written: *"actually revert each and watch it fail"* is satisfied by a vacuous
  test. Revert-testing proves a test is connected to the code; it says nothing about whether the
  code is connected to the program.
- **Gated now?** Yes, both halves. **Rule 10 in `AGENTS.md` was amended** (2026-07-30) to say
  red-on-revert is necessary and not sufficient, and to require naming the backends that pass a
  capability guard and confirming the intersection is non-empty *before* the first test — this entry
  had identified rule 10 as insufficient and then left it unchanged, which is the reachability
  failure GEN §17 warns about: the lesson sat in this log, and this log is a write target that
  nothing routes an agent to read. Structurally, the fix also moved the guarantee into
  `runtimetest`'s `InstanceLabelsRoundTrip`, a **conformance** case gated on the real capability:
  every backend that builds images must round-trip Create's labels through Inspect, and no fake can
  satisfy it. That covers this mechanism. The general habit it argues for, unguarded: **when a check
  is capability-guarded, name the backends that pass the guard and the statuses that reach it, and
  confirm the intersection is non-empty** — before writing the first test. Also a specific
  by-catch worth keeping: the docker/podman conformance helper builds its `container.Config` through
  the SDK and had silently stopped covering `Labels`, so a harness that bypasses the interface it
  certifies drifts the same way.

### A16 — verified an exploit thoroughly, never asked whether the attacker needed it (2026-07-30)

- **Claimed:** that delivering the runtime scripts as a read-only mount (DF156 remedy c) was also a
  security fix, because `/yoloai/bin` is writable by the sandbox user. Was one message away from
  filing it as a finding with a severity.
- **True:** it is not a security fix at all. The scripts run *as the agent's own principal*, so
  anything the agent gains by rewriting `status-monitor.py` it gains just as easily by killing the
  process and running its own copy. A read-only mount raises the effort by nothing and prevents
  nothing. As a control against the agent it is theater.
- **Source of the false belief:** the quality of the verification, which was real and beside the
  point. I proved the mechanism carefully — that a root-owned file inside a user-owned directory
  can be unlinked and replaced, demonstrated live as uid 1001 against both `status-monitor.py` and
  `install-firewall.py` — and then checked three containment paths and correctly found each one
  closed. Every step was sound and every step was downstream of an unexamined premise: that an
  attacker with code execution as the reader would bother editing the file. Confidence tracked how
  much I had checked, and I had checked a great deal, none of it the load-bearing thing.
- **Caught by:** the owner, in one sentence — *"it could just kill the running process and relaunch
  its own modified version."* Note the shape: the owner had *supplied* the security framing in the
  previous turn and then withdrew it. Agreement from the owner is not evidence, and an aside is not
  a premise; I adopted it and then spent a long stretch making it look well-founded.
- **Cost:** none shipped. Caught before the finding was written, one turn after the claim.
- **Class:** new, and the mirror of [A15](#a15--six-passing-tests-certified-a-function-no-backend-could-reach-2026-07-30).
  There, thorough checking certified a path nothing could reach. Here, thorough checking certified a
  mechanism that works and does not matter. Both are the same underlying failure — **verification
  aimed one level below the claim** — and neither is detectable by doing more of what I was already
  doing. The question that dissolves this one takes a sentence: *who is the attacker, what do they
  already have, and does this mechanism give them anything they lack?* For A15 it was: *which real
  backend reaches this line?*
- **A specific habit it argues for:** when a change acquires a *second* justification mid-design,
  re-derive it from scratch rather than inheriting it. DF156's real grounds — a missing file causes a
  hard launch failure, and baking couples our releases to the user's rebuild cost — were sound the
  whole time and needed no help. The security claim was a bonus argument that arrived from outside
  the analysis, and a bonus argument gets audited least precisely because nothing depends on it.
- **Gated now?** No, and it is not gatable — no script can ask whether a threat model holds. What is
  durable is the record: DF156's remedy (c) now states explicitly that the directory is
  agent-writable and that this is **not** an argument for the mount, so the next contributor who
  notices the permissions does not repeat the inference. Recording a *rejected* rationale is the
  point; code shows the surviving branch and never the pruned one ([A14](#a14--reimplemented-an-approach-the-backend-idiosyncrasies-entry-had-explicitly-rejected-2026-07-29)).

### A17 — reported a capability as missing that I had built myself, earlier the same day (2026-07-30)

- **Claimed:** that DF156's remaining gap was that a base Dockerfile change "still leaves profile images
  on the old base", which "now fails as drift rather than as a failed launch, and the detection half
  reports it". Written into the finding, into two commit messages, and into two summaries to the owner.
- **True:** it rebuilds. `EnsureProfileImage` seeds the chain checksum from the base image's own label,
  so a moved base changes every descendant's expected checksum and rebuilds it — and `ensureImageLineage`
  runs that on the launch path, so `start` on a stopped or removed sandbox gets it too. Verified live
  after the owner asked: base rebuilt, profile image rebuilt, container recreated on the new image. I
  had built that seed myself, earlier in the same session, and had *watched it work* — the log line
  `Building profile image yoloai-cli-df156p...` is in my own verification output from hours before.
- **Source of the false belief:** describing the system by the part I had most recently worked on. The
  last thing I built was `warnIfImageLineageStale`, so "what happens when the base moves?" retrieved
  *warn*. The rebuild lives on a different path, was finished earlier, and had already passed out of
  working memory. Nothing contradicted me, because a warning genuinely does fire — on the two paths
  that reuse an existing container. I generalised a real behaviour from a narrow case to the whole
  system without noticing the case was narrow.
- **Caught by:** the owner asking *"why does that have to be a warning rather than a rebuild like you'd
  get if you weren't using a profile?"* — a question whose premise (that a rebuild is what should
  happen) was simply correct, and which I could only answer by going and looking.
- **Cost:** a finding carried as open that was in fact closed, and two commit messages overstating what
  was left. No code was wrong; the description of it was.
- **Class:** new, and the most mundane of the four recent ones — **recency, not reasoning**. A15 and A16
  were verification aimed one level below the claim; this needed no verification at all, only recall.
  It is worse in one respect: those failures were about code I had not exercised, and this was about
  code whose success I had personally observed. The tell is *tense* — I described a capability in the
  present ("still leaves") on the strength of a memory of the problem rather than of the fix.
- **Gated now?** No. The habit it argues for is narrow and checkable: **before writing "X still
  happens" about the system, name the function that makes it happen and read it.** A claim about
  present behaviour is a claim about code, and this repo has a grep for that. Note also the smell in the
  original phrasing — "the ordinary half remains" was inherited verbatim from the finding's own
  pre-fix text, which is how a stale description survives the work that invalidates it.

### A18 — researched whether a claim was true, never asked whether it had a source (2026-07-30)

- **Claimed:** writing up why the base image pins Node 20, that the gVisor/ARM64 rationale in
  `bca8af21` was *"the hypothesis that motivated the downgrade, not an observation that survived it.
  **The observation was a silent crash.**"* Committed to `findings-unresolved.md` in that form.
- **True:** there was no observation, because there was no platform. **This project has never had a
  Linux ARM system** — and no CI workflow has ever had an ARM64 runner; the only ARM64 anywhere in
  the repo is `darwin/arm64` compile-only cross-lint and the macOS tart path. `linux/arm64` appears
  in no workflow, matrix or smoke tier. The diagnosis named an architecture nobody involved could
  ever have run.
- **Source of the false belief:** I treated a commit message as a primary record. Asked to research
  the claim, I researched *the world* — eight web searches across the gVisor tracker, the Node
  tracker, gVisor's arm64 syscall table — and correctly reported no public corroboration. Every one
  of those searches presupposed that the local claim was sourced and the only open question was
  whether the outside world agreed. I never asked the one-line question that settles it: **on what
  hardware?** A `grep arm64 .github/workflows` would have done it, and I had already run greps over
  that directory for other reasons.
- **Caught by:** the owner, stating a fact about their own hardware — *"We've never had a linux ARM
  system."* Not a correction of reasoning; a correction of a premise I had never thought to test.
- **Cost:** the wrong framing reached a finding and a commit message, and I had just spent a
  research pass building a careful case on top of it. The finding also inherited a softer error:
  I called the absence of public reports "weak evidence", treating it as one side of a balance, when
  in fact there was nothing on the other side to balance against.
- **Class:** new, and it is the inverse of [A16](#a16--verified-an-exploit-thoroughly-never-asked-whether-the-attacker-needed-it-2026-07-30).
  There I verified a mechanism and never asked whether it mattered; here I verified a claim against
  external sources and never asked whether it was ever grounded internally. Both are effort spent
  one level away from the load-bearing question. The tell is specific and checkable: **a claim about
  a platform is also a claim about access to that platform.** When a rationale names hardware, an
  architecture, or an environment, establish that the project has it *before* investigating whether
  the claim about it is true — provenance first, then truth.
- **Wider point worth keeping.** The claim had sat in the Dockerfile for four and a half months
  reading as settled fact, and was load-bearing: it pinned Node at 20, which silently froze Claude
  Code at 2.1.197 and left the project shipping a runtime that went EOL in April 2026 — the exact
  outcome `questions-resolved.md` #2 had decided against. An unfalsifiable diagnosis is not a
  harmless one; it is the kind that survives longest, because nothing can dislodge it.
- **Gated now?** No, and a script cannot ask "did we have that machine". What is durable is
  [DF158](design/findings-unresolved.md), which now records the provenance gap alongside the
  technical one, so the next person to read that Dockerfile comment meets the caveat with it.

### A19 — silenced a linter by narrowing a test, on a platform I could not run (2026-07-30)

- **Claimed:** in a test comment, that holding `127.0.0.1:P` still collides with the
  `filterAvailablePorts` bind of `0.0.0.0:P`, so *"the collision under test is unaffected"*.
- **True:** on Linux. On macOS/BSD the second bind **succeeds**, so the port is never reported busy,
  no warning is emitted, and the test silently stops testing anything. It failed on the owner's Mac
  within the hour.
- **Source of the false belief:** the change was made to satisfy gosec **G102** ("binds to all
  interfaces"), not to improve the test. Narrowing the holder to loopback silenced the linter and
  changed the test's meaning, and I wrote a comment asserting the change was semantically neutral —
  on the one platform I could execute. The original `:0` was correct precisely because it **mirrored
  what production binds**; the "fix" broke that correspondence.
- **Caught by:** the owner's `make check` on darwin. The test's own failure message was the right one
  (*"a host port already in use must be dropped"*), so the diagnosis took one read — which is the
  only part of this that went well.
- **Class:** related to [A18](#a18--researched-whether-a-claim-was-true-never-asked-whether-it-had-a-source-2026-07-30)
  — an unverified platform claim, written the same day, *after* recording A18 — but with its own
  distinct trigger worth naming: **a lint fix can change what a test covers.** A linter objects to a
  shape, not to a meaning; when the shape is load-bearing (here: matching production's bind), the
  compliant version can be quietly weaker. Two habits follow. When a test must reproduce production
  behaviour, **the test should do what production does, and the suppression comment should say so** —
  which is what it now says. And a comment claiming cross-platform equivalence is a claim I usually
  cannot check, so it should be written as the assumption it is, or not written.
- **Gated now?** No. `make check` on Linux cannot catch a darwin-only divergence; the cross-lint
  targets compile for `darwin/arm64` but do not run tests. The real gate is the owner's Mac and CI,
  which worked.

### A23 — declared a release had never shipped, from a listing my own command could not show it in (2026-08-02)

- **Claimed:** that `v0.10.0` was prepared but never cut — no tag locally, none on the remote, the
  newest being `v0.9.0`. Written into `next-release.md` as a section with two resolutions and a
  ruled-out third, committed, pushed, and reported to the owner as the first thing to decide.
- **True:** `v0.10.0` is tagged, at exactly the `chore(release): prepare v0.10.0` commit, with 82
  commits since — an ordinary release followed by ordinary work. Every downstream fact I called
  wrong was right: the changelog's frozen `## v0.10.0` heading, and "escalated from `v0.10.1`". I
  corrected a correct file.
- **Source of the false belief:** `git tag -l | tail -5`. Git sorts refs **lexicographically**, so
  `v0.10.0` lands directly after `v0.1.1` — near the *start* of that list. `tail` shows the end of an
  ordering, and I read it as "the newest releases". That reading held for this repo's whole history
  and stopped holding the moment the minor version crossed from 9 to 10, which is the first release
  where the two orderings disagree. `git tag -l --sort=v:refname` is the version-ordered view, and
  `git tag -l 'v0.10*'` was one command away.
- **What made it feel checked:** I ran it twice — once on local tags, once on `git ls-remote --tags
  origin` — and treated the agreement as corroboration. Both were `| tail -5` on the same
  lexicographic sort, so the second check could only ever repeat the first. **Two lookups that share
  a defect read exactly like two independent confirmations**, and the more careful I was being, the
  more convincing the wrong answer got.
- **The shape, which is the reusable part:** this was a **negative existence claim** — "no such tag"
  — drawn from a *truncated* listing. A truncation can only ever support a positive claim about what
  it shows; it is silent on what it omits, and it does not say that it omitted anything. The rule is
  narrow enough to act on: never assert absence from a `head`/`tail`/`grep -m` view. Ask for the
  thing by name, and let the empty result be the evidence. **A31 widens this**: the same defect sinks
  a *count* or a *scope*, not only an absence, and A31's specimen shows the widened form is the one
  that actually recurs.
- **Tool-surface:** a result that states its own completeness — a window onto a larger set marked as
  a window, rather than one that looks exactly like the whole. Also a declared ordering: `tail` was
  read as "newest" because the sort was inferred, and lexicographic vs. version order is precisely
  the kind of thing a surface can state and a pipe cannot.
- **Caught by:** the owner opening the GitHub releases page and saying he saw 0.10.0. Nothing in the
  repo would have caught it — `make check` has no opinion about tags, and the write-up was internally
  consistent, cited real commits, and proposed sensible remedies for a problem that did not exist. It
  is DF88's lesson one turn later (*the primary source for a claim about the past is the artifact,
  not the index of it*), in a session that had read DF88 that same day while reviewing DF160.
- **Cost:** a wrong section in the release-staging file, a commit and a push carrying it, a memory
  entry asserting it, and a report to the owner naming it as the release's first decision. Reverted
  in `next-release.md`; the branch survey in the same commit was independently verified and stands.
- **Gated now?** No, and a gate looks unpromising: the failure was a shell idiom, not a rule anyone
  could have restated. What would have caught it is asking `git tag -l 'v0.10*'` — i.e. querying for
  the specific thing whose absence was the whole claim.

### A24 — grouped four findings into a "family" on their symptom profile, and committed the grouping (2026-08-02)

- **Claimed:** that DF8, DF28, DF159 and DF160 were one problem — four findings on one backend
  sharing the assumption that `task.Start` returning means the guest is ready, "each patched on its
  own symptom rather than on the shared cause". Written into DF28 and DF160 as reciprocal
  cross-links, committed, pushed, and offered to the owner as the case for building one readiness
  gate instead of a fourth patch.
- **True:** DF8 and DF28 do share that mechanism. DF160 does not, and its own entry says so. Its
  recorded evidence is `sandbox.ready` at 16–26s on all three cells, then a ~110s gap with no
  events and `hook.active` never reached — with the explicit sentence *"the boot was never the
  problem"* sitting in the bullet directly above where I added my cross-link. A mechanism that
  fires before readiness cannot explain a stall that starts after it. DF159's relationship is
  plausible but unproven, and I asserted it flatly.
- **Source of the false belief:** I grouped on **symptom profile** — heaviest virtualised cells,
  flaky under churn, green on retry — which all four genuinely share, and then described that
  grouping in the language of mechanism. The four-way pattern was more satisfying than the true
  two-way one, and it made a better argument for the work I was already proposing.
- **What makes this specific rather than "be careful":** AGENTS.md rule 7 asks you to grep for a
  defect's *shape* and says three of a kind means the architecture is generating them. Following
  that instruction is what produced the error — I found a shape, and a shape is a symptom. The rule
  needs a companion: **a family claim has to name the mechanism and a discriminator that would
  separate its members if they were not related.** Here the discriminator was one field —
  `sandbox.ready`, present in every autopsy — and checking it takes seconds.
- **Caught by:** the owner asking to look deeper and "make sure we're not chasing ghosts", which
  sent me back to DF160's own text. Nothing else would have: the grouping was internally coherent,
  cited real findings, and the gate it argued for is still worth building for DF8+DF28.
- **Cost:** two wrong cross-links in the findings file and a commit carrying them; a proposal to the
  owner that overstated its own evidence by a factor of two. Retracted in place, with DF160
  repointed at the agent-stall family (DF13) as a **hypothesis**, with the log events that would
  confirm it named.
- **Gated now?** No. What would have caught it is asking, of each member, *what would have to be
  true for this to be the same bug* — and for a timing family that question has a mechanical answer:
  do the failures fall on the same side of a known checkpoint.

## How an entry gets written

This is the honest weak point, and pretending otherwise would make the file another instance of
what it catalogues.

**The trigger is an owner correction.** When the owner contradicts a claim, or asks a question
whose answer turns out to be "I was wrong", that is an **event you can see** — not an uncertainty
you feel. It is the same shape as GEN §7's D123 corollary, which is the corpus's own template for a
rule that fires: *"a returned delegation is an event you can see, unlike the uncertainty you don't
feel."* Every one of A1–A3 has that trigger, and it fired reliably — the owner's question is,
empirically, the most dependable detector in this repo.

**A8 adds a second trigger, and it is not a correction: opening the primary source and finding the
claim you already made about it does not survive.** That is equally an event you can see, and it
came from the owner *agreeing* — the instruction to file forced the read. Worth stating because the
first trigger, taken alone, implies that assent is safe and only challenge is informative. It is the
reverse: a question makes me look again, while agreement is the moment the claim stops being
examined, by both of us at once. Approval is the least-checked state a claim can be in.

**What does not work, on the evidence:** "record your failures" as a standing instruction. That is
the noticing register, and DF132 is the finding that says it does not fire. D119 — *"a finding parks
its fix, never its verification"* — is loaded every session, and DF128 (A3) was filed with an
unverified claim by a session that could recite it.

**Not gated, deliberately, for now.** A corpus this small cannot justify a mechanism, and a gate on a
file this young would be pinning a shape nobody has learned yet. The thing to gate, when the shape
is known, is probably not the *writing* of an entry but the **format** — a "Caught by" field that
cannot be omitted, so pattern 4 stays computable. Revisit at ~20 specimens, or when an entry appears
whose class already has three siblings.

### A28 — wrote the vacuity guards, then wrote ten verdicts that could not have come out otherwise (2026-08-04)

- **What happened:** across the macOS `pf` workstream I produced ten measurements whose "PASS" was
  unreachable by any other value. The family is one shape: a shell idiom for *supply a default on
  failure* colliding with a command that **already emits a value on failure**. `cmd | head` reported
  head's exit status. `grep -c X || echo 0` printed `0` *and* exited 1, so the fallback appended a
  second line, `"0\n0"` failed `[ -gt ]` with "integer expected", and the non-zero status fell into
  the not-honored branch — unconditionally. `curl -w '%{http_code}' || echo 000` yielded `"000000"`,
  which `!= "000"`, so a **blocked** destination read as reachable and inverted a correct result into
  a reported failure. An interface name taken from `ifconfig`'s `$1` carried a trailing colon, making
  the rule a syntax error that never loaded — and "no effect" was published as "inert". A `set skip`
  detector grepped for a literal tab that never appears, so a grep that cannot match and a true
  negative were the same reading. A `rdr` placed after filter rules violated pf's mandatory rule
  order, so nothing loaded. A refusal detector scored **any** non-zero exit as "refused by policy",
  so a `pfctl` parse error counted as a sudoers denial — which is how a row testing a file that was
  neither user-writable nor a valid ruleset passed while testing nothing.
- **Source of the false belief:** the idioms are correct-looking and near-universal, and each one
  fails only in the presence of a *second* signal channel — a command that reports failure through
  both its exit status and its stdout. Reading the line does not reveal it; running the line does.
  Every one of these survived my own review because reviewing shell means reading it.
- **What makes this specific:** I had written the guard against exactly this. `pf_authz.sh` carries
  the comment *"a load that silently no-ops is indistinguishable from one that worked (A22)"*, and
  the harness containing it then measured `rdr` inertness from a rule that never loaded. The
  correction after each discovery was also consistently too narrow — fixing the colon, not the
  missing load-gate; fixing the arithmetic, not the detector that had never been shown to fire.
- **Caught by:** three independent audit agents, and the discriminating behaviour is worth naming:
  they **reproduced the idioms in a shell** rather than reading them. The `load anchor` finding is
  the cleanest case — `pfctl` silently ignores that directive under `-a`, so a containment test
  concluded "the parent was not written" from a file that was never opened, and the auditor
  established it with a three-line experiment (`include` under `-a` errors on a bad file;
  `load anchor` under `-a` does not; without `-a` it does). Nothing in reading the harness would
  have shown that.
- **Cost:** two published results files carried vacuous PASSes into a design document, one of them
  the *only* evidence for the design's sole security property. Two more sat unretracted in files
  whose siblings had already been corrected for the same bug. Two harnesses still carried a live
  instance on a branch that had never executed — one of which would have reported FAIL-OPEN for a
  sandbox that was in fact stranded.
- **Class:** `llm-shaped-repos.md` Part 7 — the asymmetry between *producing* an artifact and
  *verifying* it, here at its sharpest, because the artifact's whole purpose was verification.
- **Gated now?** Partially, and only by convention. Each harness now gates an effect measurement on
  the rule having **loaded**, pairs every denial with a positive control **in the same run**, and
  separates `refuse-by-policy` from `ran-but-failed` by reading stderr. None of that is enforced.
  The transferable rule is narrower than "be careful" and is the one thing that actually worked:
  **before trusting a verdict, construct the input that should produce the opposite one.** Where
  that is impossible — a detector that has never returned anything but zero — the verdict is not
  evidence, whatever it says.

### A29 — asserted facts about this codebase without reading it, in a document whose value is being right (2026-08-04)

- **What happened:** five load-bearing claims in a design plan, each stated flatly, each false or
  unsupported. "Nothing in the repo configures a second enforcement path" — `LivePatchNetwork` does.
  "A conformance case gated on `NetworkIsolation` has an empty backend set" — apple sets it `true`,
  and apple is the backend the plan targets. "tart's pf enforcement has not been run" — it had been,
  three times, with controls, in results files I had already cited for other things. "No concurrency
  controls exist" — taken from an `UNSPECIFIED` plan, while `store.AcquireLock` has shipped a
  per-sandbox `flock` used in five call sites. And an apple guest's ULA was called a "global-scope
  IPv6 address" with an inference built on top of it, when `runtime/apple/apple.go` says *"vmnet
  hands the guest a ULA"* twelve lines from code I had read that day.
- **Source of the false belief:** three distinct provenances, which is why it is one pattern and not
  five accidents. Two came from **prose** — a stale plan's status line, and my own earlier summary of
  a results file rather than the file. Two came from **partial reading** — I had opened the file for
  another purpose and generalised from the part I needed. One came from **an inference presented as a
  measurement**: `/etc/pf.conf` declares `nat-anchor "com.apple/*"`, so I wrote that translation rules
  in a sub-anchor "are evaluated", and only later measured it. That one happened to be true, which is
  worse than if it had been false.
- **What makes this specific:** the counter-habit was available and I applied it inconsistently. In
  the same workstream I refused to test `pfctl -E`/`-X` because a wrong `-X` would break the host's
  networking, and I re-ran a harness rather than accept a verdict I had produced. The discipline was
  present for *measurements* and absent for *claims about code*, which is exactly backwards: a
  measurement announces its own uncertainty, a grep does not.
- **Caught by:** independent audit agents that opened the files, and — for the one the auditors
  missed — the owner asking a one-line question ("can't a user just call `sudo -E`?") about an
  argument I had used to kill a design alternative. That argument was wrong, and the repo contained
  `sudo -E yoloai` in a comment and throughout an archived plan.
- **Cost:** the claims survived one full plan rewrite. Two of them were used to *reject a design
  alternative*, and the correct comparison — reached only after three audits — reversed the
  recommendation.
- **Class:** `llm-shaped-repos.md` Part 7 — prose read as fact. A26 is the same row for a *finding's*
  prose; this is it for a *plan's*, and for my own summaries, which is the harder case because a
  summary in context is indistinguishable from a source that was read. `scripts/check_citation_provenance.py`
  exists because of exactly this asymmetry, and it is scoped to `research/*.md`, so none of these
  were in range.
- **Gated now?** No. The provenance checker's scope could be widened, but the honest reading is that
  four of the five were *code* claims, which no citation gate reaches. The rule that would have
  caught all five: **a claim about what the code does gets a file:line, or it gets hedged.** The plan
  now carries line references for every code assertion, which at least makes the unsourced ones
  visible.

### A30 — built the harness whose control was the whole point, then let three of its verdicts run against a control that never loaded (2026-08-04)

- **What happened:** `reboot_post.sh` opens by saying *"every question is answered by comparison
  against a recorded pre-reboot value, so no verdict here can be produced by a test that could not
  have gone the other way — the snapshot IS the control."* `reboot_pre.sh` then wrote that snapshot
  with **unquoted** values, so the four carrying spaces did not survive being sourced: `PRE_DATE`,
  `PF_STATUS` and `SANDBOXES` failed to assign, and `BRIDGES` truncated to its first token in
  silence. The run rendered a verdict for each anyway. P6 concluded *"bridges differ across reboot"*
  by comparing a one-token remnant against a host that had **no vmnet bridges at all** because
  nothing was running. Two more printed empty `before:` lines and carried on. Separately, P8 reported
  **PASS "tart address preserved across reboot"** for a VM in state `stopped`, on a host where its
  bridge did not exist — `tart ip` resolves through `/var/db/dhcpd_leases`, and the lease file
  survives a reboot. And the live half of the experiment measured an idle machine throughout: the
  one thing a reboot guarantees is that nothing is running, and the harness never restarted anything,
  so three of nine questions went unmeasured and now need a second reboot.
- **Source of the false belief:** the three `command not found` lines sat at the **top of the results
  file**, above every verdict, in the file I read to write the summary. They are shell noise of the
  kind that precedes working output constantly, and I read past them to get to the section headers.
  The truncation produced no line at all.
- **What makes this specific:** A28 — written two days earlier, in this same workstream — says
  *"before trusting a verdict, construct the input that should produce the opposite one."* I applied
  it to every **probe** in this harness and not once to the **control**, which is the input that
  decides what every probe means. The header quoted above is the tell: the design was explicitly
  built around the control, and the control was the only part never tested.
- **Caught by:** A8's second trigger — opening the primary source. The owner ran the test and said
  so; nothing was contradicted. Reading the raw output rather than summarising it surfaced the
  `command not found` lines, and the tart PASS fell to re-running the harness's own command four
  minutes later, where `tart ip yoloai-cli-rb-t` answered *"no IP address found, is your VM
  running?"* with rc=1. Neither needed review of the script.
- **Cost:** one wrong published verdict (P6), one withdrawn PASS, three questions unmeasured, and a
  second reboot of the owner's machine to get them. The five verdicts that matter most — anchor
  survival, pf's own state, the main ruleset, the files, unattended restore — used single-token
  controls, loaded correctly, and stand.
- **Class:** `llm-shaped-repos.md` Part 7, and the third sibling of A22/A28 — a check that passes
  because it never ran. That is the threshold this file names for revisiting the gating question.
- **Gated now?** Partially, and the split is worth keeping. `reboot_pre.sh` now single-quotes every
  value; `reboot_post.sh` refuses to render any verdict unless all twenty snapshot fields loaded
  non-empty, and its numeric fields default to empty rather than `0` so a plausible-looking default
  cannot sail through. Run against the snapshot that actually shipped, that guard names three of the
  four — **not** `BRIDGES`, which loaded truncated rather than empty, so the quoting is the fix and
  the guard is only the backstop. `ipof` now refuses to read an address for a guest that is not
  running. The transferable rule: **the control is an input too — corrupt it deliberately and
  confirm the harness refuses rather than reports.**
- **Tool-surface:** three separate properties, which is why this entry is the densest specimen here.
  Values that survive a write-then-read round trip without a quoting layer — `BRIDGES` lost its tail
  to word-splitting on `source`, which is a data-model failure wearing shell syntax. A truncation
  that announces itself, since that loss was silent. And errors delivered as records rather than
  interleaved into the same stream as results, because the three `command not found` lines were not
  hidden — they were *adjacent to* the output, which is how they got read past.

### A20 — accepted a skip reason as a constraint, then engineered around the defect it was hiding (2026-07-30)

- **Claimed:** that the conformance mount section could not run on seatbelt or tart, because it binds
  at `/mnt/test` and neither macOS-family backend can create `/mnt`. Reported it as a fixed cost and
  designed around it — a new `TierIsolation` conformance section with a new fixture field, so the
  tiering invariant would have somewhere to live that did not need the skipped section.
- **True:** `/mnt/test` was an arbitrary literal in two subtests. Moving it under `/tmp` un-skips both
  backends, and all four other backends are unaffected. The skip had concealed a **second** and worse
  fact: seatbelt's read-only mounts were not read-only whenever a broader rule granted write over the
  same path ([DF162](design/findings-resolved.md)), which is user-reachable via `--dir <path>:ro` and
  needed no compromised agent. Neither skip was hiding a capability gap; one was hiding a security
  defect.
- **Source of the false belief:** both skip strings are *well-written*. They name a real mechanism
  (SIP-sealed root volume; `/mnt` unwritable without root), cite the unit tests that cover the gap,
  and one explicitly says it is "the conformance's container-path assumption, not a capability gap".
  I read them as a diagnosis and inherited it. They are a diagnosis — of the symptom. Nothing in
  either says "and this literal is arbitrary", because the person writing a skip is explaining why
  the test cannot run, not auditing whether the test had to ask for that path.
- **Caught by:** the owner, in one line — *"why do we need /mnt/test? Isn't there another way?"* Note
  what it is not: it is not a correction of a fact. Every fact I had reported was true. It questions
  whether the constraint was a constraint, which is the one move that was unavailable from inside a
  frame where the skip reason was the premise.
- **The second half is the sharper one.** After un-skipping, seatbelt's `ReadOnly` subtest failed. I
  diagnosed it correctly — the profile's broad temp grant covers `t.TempDir()` — and then wrote a
  plan to give the suite a fixture root outside that grant, and filed the constraint as "a trap for
  whoever fixes this". That is engineering *around* a defect, having already found it. The failing
  test was not telling me its fixture was in the wrong place; it was telling me `:ro` did not work.
  One is a test problem and one is a product bug, and I picked the reading that kept the scope small.
- **Cost:** none shipped — both were caught inside the session, the second by the owner's standing
  "do it properly" rather than a specific correction. But the workaround was fully designed, written
  into a finding as advice to the next person, and would have shipped a permanent fixture-placement
  rule enshrining a security bug as an environment property.
- **Class:** new, and it is the inverse of [A14](#a14--reimplemented-an-approach-the-backend-idiosyncrasies-entry-had-explicitly-rejected-2026-07-29). There I re-derived a decision the repo had already made because I read the code and not the record. Here I read the record — the skip strings — and treated it as settled *because* it was well-argued. **A written rationale is evidence about what someone concluded, never about what they checked.** The tell is specific: a skip, a `nolint`, a `t.Skip`, a "known limitation" comment is a place where someone stopped, and the reason they stopped is the part least likely to have been re-examined since.
- **Gated now?** No, and the useful habit is narrow enough to state: **when a test is disabled, check what it would assert if enabled, before accepting why it is disabled.** Two questions, in this order — *is the obstacle essential or incidental?* and, if you get past it and the test fails, *is this a test problem or a product problem?* The second is the one that pays: a newly-enabled test that fails is reporting on the product by default, and treating its failure as a fixture problem needs positive evidence, not convenience. Both skips here were years-cheap to keep and one of them was hiding a HIGH-severity defect the whole time.

**One thing is ready ahead of that, though** (pattern 4b): the repo-relative-citation rule for
findings. It has two specimens (A1, A8), needs no new mechanism, and turns an existing hook's known
hole into coverage. That is a lower bar than "gate this file" and should not wait for it.

**When in doubt about whether something belongs: does it have a Caught-by that is not "the owner"?**
If yes, it is evidence a mechanism worked and belongs here for that reason alone — the successes are
as scarce as the failures and twice as useful.

### A21 — audited a design correctly, then recommended against it on a criterion I had invented (2026-07-31)

- **Claimed:** that the v5→v6 tier move should be built as an in-place rename shuffle rather than the
  staged copy-and-promote the plan described. The supporting audit was sound and every fact in it
  checked out — `repopulate` deep-copies (`promote.go:356`) so tree-level promotion costs 2× the
  sandboxes dir; the migrators are not "parameterised on the root"; scratch resumability contradicts
  `scratch.go`'s own invariant. From those I concluded that staging was "much larger than the
  disease" and proposed the cheap alternative.
- **True:** the cheap alternative is the one design that cannot satisfy the requirement. The owner,
  in one sentence — *"If the migration fails, then the user will be stuck halfway, unable to
  downgrade and unable to run the current version"* — named the criterion: what matters is the state
  a **failed** migration leaves the user in. Under it, an in-place shuffle is strictly worst (a
  deterministic failure bricks both directions), and the duplication I had costed as the objection
  is the entire point. My follow-up was no better: I proposed hardlinks to recover the cost, and got
  *"hardlinks are fine until you encounter a fs that doesn't support it"*.
- **Source of the error, and it is not a factual one.** I evaluated the designs on the axis I could
  measure — resource cost on the success path — and treated the failure state as a residual
  probability to minimize. The owner's axis was the failure state itself, with cost as the free
  variable: *migration is rare, therefore it can be heavy*. Both axes are defensible; only one was
  the owner's, and I never asked which. Every fact in the audit stayed true when the axis flipped;
  the recommendation inverted completely.
- **Caught by:** the owner, in one line, twice. Note what neither line was: a correction of a fact.
  The audit was not wrong, which is exactly why nothing internal to it could have flagged the
  problem — a fact-check on my own analysis returns clean.
- **The cost, and the shape of it.** Nothing shipped; it was a design conversation and the plan was
  rewritten afterwards to the corrected design. But the correction also *deleted* most of what I had
  been elaborating — the staged ladder, the `Requires` axis, the `repopulate` opt-out, the
  hardlink/reflink tier — because once "heavy is fine" is on the table the machinery collapses.
  Roughly: I had been optimizing a constraint the owner would have relaxed for free if asked.
- **Class:** new, and the inverse of the A14/A20 pair. Those are failures to check a *premise* I
  inherited from the repo. This is a failure to check a premise I supplied myself — an evaluation
  criterion, which is the least visible kind because it never appears as a claim, only as the shape
  of the recommendation. **A20's lesson was that a written rationale is evidence about what someone
  concluded, never about what they checked. This one's is narrower: an audit is evidence about the
  facts, never about which of them matter.**
- **Gated now?** No, but the habit is cheap and states in one line: **when a recommendation trades
  safety against cost, the acceptable failure state and the resource budget are the owner's to set —
  ask for both before recommending, not after.** The tell is a design comparison where one option
  wins on effort and another wins on what happens when it breaks. That is not a technical tie to be
  broken by judgement; it is a question with an owner.

### A22 — wrote the guard for a security invariant, and it passed on a shell parse error (2026-08-01)

- **Claimed:** that the new `SandboxTiers` conformance section verified the tier invariant. It ran
  green on seatbelt — `host/` unreadable and unwritable, `ro/` not writable through the view — and I
  moved on to run it against tart.
- **True:** on seatbelt every one of those denial assertions passed *without anything being denied*.
  The section built its write commands as `sh -c "echo x > " + path`, unquoted. A tart guest reaches
  the tiers under `/Volumes/My Shared Files`, so the shell split the path on the space and failed
  with `sh: /Volumes/My: Permission denied` — and `assert.Error` accepts a shell parse failure
  exactly as happily as a kernel denial. The seatbelt paths have no spaces, so *there* the commands
  parsed and the assertions were real; the bug was invisible on the backend I checked first.
- **Source of the false belief:** a green test I had just written, on the backend where the defect
  does not manifest. Nothing was read second-hand here — this is the failure mode of `assert.Error`
  itself, which asserts that *something* went wrong and never that the thing I care about did.
  AGENTS.md rule 10 states this exact trap one case over ("asserting only *that* an error was
  wrapped tests the wrapping, not the fix"); I had read it that morning and still wrote the
  negative-space version of it.
- **Caught by:** running it on the second backend, where the path has a space in it. Pure luck of
  the platform — had tart's share been at a space-free path, or had I taken the seatbelt green as
  sufficient for a "backend-agnostic" section, the branch would carry a security guard that passes
  on any machine where the command cannot run at all. The fix was to quote the paths and route reads
  through argv with no shell, and the thing that makes the section trustworthy now is not the
  quoting but the **positive control**: a write to `rw/` that must *succeed*. That one assertion
  fails loudly whenever the mechanism is broken rather than the permission — which is the only
  reason a future regression of this shape gets noticed.
- **Cost:** none shipped; caught within the hour, before the commit. Filed because it would have:
  the green was on the backend I would have called representative, and the artifact is a guard for
  DF136, where a vacuous pass is worth less than no test at all — it converts an open question into
  a settled one.
- **Class:** the A15 family (a test that certifies nothing while looking exactly like one that
  certifies something), but by a different mechanism. A15's tests could not reach the code; these
  reached it and mistook the *transport* failing for the *policy* holding. Generalised: **a negative
  assertion is only as good as the proof that the action was attempted.** Any test whose pass
  condition is "this failed" needs a sibling whose pass condition is "the same machinery succeeded",
  or it is asserting that the test harness works.
- **Tool-surface:** an interface where *failed to execute* and *executed and refused* are different
  structured outcomes rather than the same non-zero exit. The guard could not tell a shell parse
  error from a denied write because the shell gives both the same shape, and no amount of care at
  the call site recovers a distinction the transport discarded.
- **Gated now?** No, and it is not obvious what would gate it — a linter cannot tell an intended
  denial from an accidental one. The transferable defence is the positive control, which is a review
  question, not a check: *for every assert-it-fails, what asserts it was even tried?*

### A25 — reported that a lint gate had silently skipped, inferring "did not run" from "printed nothing" (2026-08-02)

- **Claimed:** to the owner, that `make check`'s shellcheck target had not run over four new shell
  scripts because "it runs shellcheck only `if command -v shellcheck`; it isn't installed here, so
  the target passed by doing nothing" — offered as an instance of the exact hazard `CLAUDE.md`
  warns about ("a target that *skips* rather than fails reports that into a void"). The owner
  replied "yes I want that hole closed", authorising work on the strength of it.
- **True:** the target does not skip. It falls back to Docker — which was running — and `exit 1`s
  if neither shellcheck nor Docker is available. It had run, cleanly, over the tracked scripts.
  The real gap was narrower and had nothing to do with skipping: `git ls-files '*.sh'` lists only
  **tracked** files, and the new scripts were untracked, so they were out of scope rather than
  unlinted-because-nothing-ran.
- **Source of the false belief:** I grepped the `make check` log for "shellcheck", got zero hits,
  and read that as "the target did not execute". A clean shellcheck prints nothing, and the recipe
  is `@`-prefixed so the command is not echoed either — so zero hits is exactly what *success*
  looks like. I then found the `if command -v` line, which fitted the story, and stopped reading
  four lines above the `elif docker info` branch that refutes it.
- **What makes this specific rather than "read more carefully":** it is [A22](#a22--wrote-the-guard-for-a-security-invariant-and-it-passed-on-a-shell-parse-error-2026-08-01)
  inverted, and I had re-derived A22's own rule from scratch that same session. A22: a *failure* you
  did not prove was attempted is not evidence. A25: an *absence of output* you did not prove was
  reachable is not evidence either. Both are the same missing question — **what would this look
  like if the thing had worked?** — and for a silent-on-success tool the answer is "identical",
  which is checkable in one command (`make shellcheck; echo $?`) and I never ran it.
- **Caught by:** going to fix the hole. Reading the target in order to change it showed the Docker
  fallback in the first ten seconds. Nothing else would have — the claim was plausible, cited a real
  line of the Makefile, and matched a hazard the project documents.
- **Cost:** a false statement to the owner, and a work request authorised on it. The work itself
  turned out to be worth doing for the *other* reason, so the artifact survives; the justification
  in the first commit message would have been wrong. Corrected to the owner before any of it
  landed, and the Makefile comment now states the real scope rule.
- **Contrast worth recording:** in the same message the owner also asked me to verify every place I
  had claimed "this needs higher privileges". Those claims **held** — `/dev/pf` is root-only, a
  non-root anchor load fails, `setegid()` to a supplementary group returns `EPERM` even for a
  member. So the corrective instinct was right and the specific target of it was not the one that
  was wrong, which is an argument for the owner's habit of asking rather than against it.
- **Gated now?** Partly, and only for this instance: `TestRepoHygiene_ShellcheckScope_CoversUntrackedScripts`
  reads the argv out of the Makefile and fails if the scope regresses. Nothing gates the general
  shape — "agent infers a tool did not run from an empty log" — and a linter cannot. The
  transferable defence is a habit, not a check: **before reporting that something did not happen,
  run the thing and look at its exit code.**
- **Tool-surface:** an explicit outcome record per operation, so *ran and produced nothing* is a
  different observation from *never ran*. Silence-on-success is the whole defect: a surface that
  reports what it did, rather than one whose success condition is the absence of output, makes the
  inference I drew unavailable rather than merely unwise.

### A26 — recorded a fix lead that a measurement in the same workstream had already refuted (2026-08-03)

- **What happened:** DF175 shipped with a stated lead — *"have `files put` write a new path and move
  it into place, rather than overwriting the existing one"* — and that lead went into
  `next-release.md` as the blocking item for v0.11.0. It was wrong. The spike's own coherence matrix,
  written by me two days earlier and committed in the same directory, records
  `overwrite_rename` on tart as `read NEVER, stat NEVER`. Temp+rename is not merely ineffective:
  re-measured directly, the guest keeps the old bytes **and** the old size, where an in-place
  overwrite at least updates `st_size`. Shipping it would have removed the last signal a guest has
  that anything changed.
- **Source of the false belief:** a narrative built from one comparison that was not a comparison.
  `reset` did not reproduce the corruption, so I asked what `reset` does differently, answered
  "it re-copies rather than truncating in place, so the parent dentry is replaced", and wrote that
  down as the lead. The reasoning is plausible and the sentence reads like a finding. But `reset`
  changed a file in the **workdir**, which `workcopy.Materialize(..., WipeAndCopy, ...)` replaces
  wholesale — a different share, a different mechanism, and nothing `files/` can imitate, since
  `clearCacheAndFiles` empties it in place by design (DF149).
- **What makes this specific rather than "check your work":** the refuting evidence was not missing,
  hard to obtain, or in someone else's repo. It was a table I had produced, in a file I had written,
  cited two paragraphs above the lead in the same finding. The failure is not ignorance of the
  measurement; it is that **prose and measurement were never put in the same room**. A lead composed
  by reasoning about a mechanism reads exactly like one derived from data, and DF175 contained both
  with nothing marking which was which.
- **Caught by:** going to implement it. The first thing the work needed was the guest path for the
  rename, which meant opening the coherence results, which showed the row.
- **Cost:** none shipped — but the lead was the stated plan for a release blocker, and the owner had
  approved fixing DF175 on the strength of it. One directed experiment would have been spent
  confirming a refuted hypothesis.
- **Gated now?** No, and this one resists a gate: nothing can diff a paragraph against a table. The
  transferable habit is narrow enough to state — **when a finding proposes a fix, name the
  measurement that supports it, or mark it as untested.** DF175 now separates the two explicitly,
  and the retraction is left in place rather than edited away.

### A27 — wrote a fix, watched every test pass, and shipped something that did nothing (2026-08-03)

- **What happened:** the DF175 repair (verify a host write reached the tart guest, repair it with
  `msync`) was implemented, unit-tested from both sides, mutation-checked 7/7, and `make check` was
  green. Run against the real sandbox, the guest still read the fabricated bytes. `refreshGuestView`
  began `rt := e.Runtime()`, and the `files` command builds a **backend-less** Engine — the handle's
  own docstring says file exchange "needs no container backend" — so `rt` was `nil` on the only path
  that mattered and the function returned immediately, every time.
- **Source of the false belief:** the tests supplied a runtime, because I wrote them to. Each one
  constructed an Engine *with* a mock backend and asserted the refresh behaved correctly given one.
  Every assertion was true. None of them asked the question the product asks, which is whether a
  backend is there at all.
- **What makes this specific:** this is precisely the hazard AGENTS.md rule 10 spells out —
  *"red-on-revert proves a test is wired to the code, never that the code is wired to the
  program"* — and I hit it while following the rest of that rule carefully. The mutation checks were
  real and they all passed, because mutating a function nothing reaches still turns its tests red.
  The gap was never in the function; it was in the fixture, which quietly granted a capability the
  caller does not have.
- **Caught by:** running the shipped binary against real hardware with an unpatched control. Nothing
  else would have. The corrected tests now build the Engine the way the CLI does — backend-less,
  resolving the backend from the sandbox's `environment.json` — and re-introducing `e.Runtime()`
  turns three of them red.
- **Cost:** one build-and-verify cycle. It would have been a shipped no-op fix for a HIGH-severity
  data-integrity defect, with a green gate and a confident commit message behind it.
- **Gated now?** For this instance, yes — the tests construct the Engine the real caller constructs,
  and a registered test backend goes through `runtime.Descriptor`/`runtime.New` rather than
  bypassing the capability gate. The general shape is not gateable. The habit is: **build the
  fixture the way the caller builds it, and if a test hands the code a dependency, check the caller
  actually has one.**

### A31 — sized a fix from a diff I had truncated at 60 lines, and put the number in the commit (2026-08-05)

- **What happened:** during the local branch cleanup, `layering-refactor` was the one branch whose
  content had only partly reached `main`. To find the residue I ran
  `git show 0f0b77f9 -- scripts/smoke_test.py | head -60`, read the four hunks it printed, and
  reported to the owner that the branch held **three** unmerged `stdin=subprocess.DEVNULL` call
  sites — one of the four having already landed. I then wrote the fix, the tests, and a commit whose
  subject was *"detach stdin on the three yoloai calls that still inherit it"*. The branch commit had
  fixed **ten** sites, not four; only `Test.run` had landed. Nine were missing, and five further
  sites had been added to the harness after the branch was cut, for thirteen in total.
- **Source of the false belief:** my own `head -60`. The hunks it showed were real, the reading of
  each was correct, and nothing in the output announced that it had been cut — `head` closes the pipe
  and the remaining six hunks simply never existed as far as the transcript was concerned. The
  truncated view is indistinguishable from a complete one, because completeness has no marker; only
  incompleteness would have needed one, and that is the marker `head` cannot emit.
- **What made it feel checked:** the same command printed `git show --stat`'s
  `15 insertions(+), 8 deletions(-)` a few lines above. The four hunks I read account for about nine.
  The refutation was in my own output, six lines from the claim, and I never did the subtraction —
  because I was not looking for a discrepancy, I was reading hunks. **A number that contradicts the
  claim is inert if nothing makes you compare them.**
- **The shape, which is the reusable part:** this generalises A23. A23's rule was *never assert
  absence from a `head`/`tail`/`grep -m` view*. This was not an absence claim — it was a **count**,
  which fails identically and less visibly. Any claim about a *complete set* drawn from a truncated
  view has the same defect: the view is evidence only for what it displays, never for the size or
  boundary of what it was drawn from. So the rule widens: **do not assert absence, a count, a scope,
  or "that's all of them" from a truncated view.** For a diff specifically, `--stat` first and make
  the hunk count agree with it, or drop the pipe — `git show` on one file is not large.
- **Caught by:** execution, not review. After committing I re-ran the containment check
  (`git merge-tree --write-tree main layering-refactor`), which still reported the branch as not
  contained. I had assumed that step was a formality. An AST pass over the harness then produced the
  real inventory. Note the counterfactual: had the branch been deleted straight after the commit —
  which is what the cleanup task was for — nothing would ever have re-run that check, and the wrong
  scope would have been the permanent record.
- **Cost:** a wrong scope stated to the owner in a summary table, and a commit subject and body
  asserting "three" twice. Amended before it was pushed, so nothing left the machine. The unamended
  version would have been a durable, confident, and specific lie about how much of a branch had been
  recovered — in the one commit whose entire purpose was to make that branch safe to delete.
- **Gated now?** No, and probably not gateable — `head` in a Bash call is not something `make check`
  can see. The mechanism that did the work was the **independent re-verification**: the containment
  check was computed from the repo rather than from my belief, so it disagreed. The transferable
  habit is to keep one check in the loop that is not derived from the reading that produced the
  claim. For this specific class the test now carries a completeness guard that walks the harness's
  AST instead of a human reading call sites, so the count is machine-derived from here on.
- **Tool-surface:** a result that carries its own completeness, so a truncated view cannot be
  mistaken for a whole one. `head` cannot supply this — it closes the pipe and the omitted remainder
  leaves no trace in the output. The property is that incompleteness is a fact *in the result*, not
  a thing the reader must remember having caused.

### A32 — the loop proving rule 10 was itself running against stale bytecode (2026-08-05)

- **What happened:** A31's fix touched thirteen call sites, so rule 10 needed each one proven
  red-on-revert. I scripted it: revert one site, run pytest, restore the file from a backup, repeat.
  The sweep reported every site as covered. It was wrong — reverting site 2
  (`_capture_terminal_snapshot`) reported site 1's test as the failure. CPython validates a cached
  `.pyc` against the source's **(mtime-in-seconds, size)** pair (PEP 552 timestamp mode). The
  revert/restore cycle rewrote `smoke_test.py` several times within the same second, and restoring
  from backup returned it to byte-identical size — so a `.pyc` compiled during a *different* iteration
  satisfied the check and pytest executed bytecode for source that was no longer on disk.
- **Source of the false belief:** the harness's own output. Each iteration printed a real pytest
  result with a real failing test name; nothing distinguished "compiled from the file I just wrote"
  from "compiled two iterations ago". The write itself was never in doubt and never wrong — the file
  on disk was correct every time. What was stale was a consumer's private cache, and its staleness
  had no representation anywhere in what I could see.
- **What makes this specific:** it is the [A6](#a6--the-measurement-was-contaminated-by-the-act-of-measuring-2026-07-17)
  family — the measurement contaminated by the act of measuring — but the contaminating agent is the
  *verification loop's own file-rewriting*, defeating the staleness detector of the runtime it was
  measuring with. Rapid identical-size rewrites are not an exotic pattern; they are the natural shape
  of any revert-and-test sweep, which is to say the shape rule 10 asks for. **The more mechanically
  you execute rule 10, the more reliably you trip this.**
- **Caught by:** the failing test having the wrong *name*. I expected site 2's test and got site 1's,
  and only the specificity of that expectation exposed it. This is luck of the design: had each
  revert been to code whose failure looked interchangeable — a parametrised suite, or a single test
  covering all thirteen — every iteration would have gone red for the wrong reason and read as
  success. The sweep would have "verified" a claim it never tested.
- **Cost:** none reached an artifact; caught within the step and re-run with `PYTHONDONTWRITEBYTECODE=1`,
  `__pycache__` cleared between iterations, and `-p no:cacheprovider`. Filed under the "or would have"
  clause: the next thing I would have written is that all thirteen sites were proven red-on-revert,
  which is precisely the claim rule 10 exists to make trustworthy, in a commit message, on evidence
  that had silently measured nothing.
- **Tool-surface:** identity by **content**, not by a proxy for content. Every mechanism in this
  failure — the `.pyc` validity check, and my own reliance on it — keys file identity on
  `(size, timestamp)`, a pair that is cheap, usually right, and silently wrong exactly when a tool
  rewrites a file quickly. A surface that names files by content digest cannot express this bug. The
  narrower, more actionable property: a write that changes content but leaves `(mtime-to-the-second,
  size)` unchanged is a **hazard the writer can compute and report**, since it holds both digests —
  and every timestamp-based staleness detector downstream (`.pyc`, `make`, `rsync`'s default
  quick-check, most file watchers) will fail to notice that write.
- **Gated now?** No. `make check` cannot see a Bash loop's cache hygiene, and the general shape —
  "a consumer's cache disagreed with the disk" — is not gateable from inside this repo. The habit
  worth keeping is narrow enough to state: **when a verification loop rewrites the same file
  repeatedly, disable the caches of whatever runs it, and make each iteration's expected outcome
  specific enough that the wrong one is recognisable.** A sweep whose iterations all fail
  identically cannot detect that it stopped measuring.

### A33 — restated a finding's severity in the direction that made it interesting, and did not check the mechanism (2026-08-08)

- **What happened:** relaying the macOS DNS results, I told the owner that host-side resolution
  "can return host-relative addresses — `.local` resolving to `127.0.0.1` and the vmnet gateway — and
  writing those into `dst` **allowlists the guest itself**", and framed it as "a security regression
  created by the security fix". The owner asked the obvious question: if it resolves to `127.0.0.1`
  the guest can contact itself, which seems like not a big deal. That is correct. Loopback traffic
  never leaves the guest's own stack, so no host-side rule evaluates it — the entry is **inert**. The
  dominant failure is *functional and fail-closed*: the user allowlists a name and the guest still
  cannot reach it.
- **Source of the false belief:** the plan's own sentence, which I read as a finding and repeated
  without evaluating. `macos-pf-privileged-path.md` said writing `127.0.0.1` into `dst` "allowlists
  *the guest itself*" — literally true and operationally empty, because "the guest itself" is exactly
  the destination a host-side rule cannot see. I did not ask what packet would traverse the
  enforcement point, which is the only question that decides whether an allowlist entry does anything.
- **What makes this specific:** the two other addresses in the same answer — the vmnet gateway and the
  host's LAN address — *are* a genuine widening, and modest. So the finding was real and I inflated
  it: not invented, **mis-severitied**, in the direction that made it worth reporting. That is harder
  to catch than a fabrication, because every component of the sentence is true and the citation
  resolves. It also skipped a check I had just performed in the other direction: two days earlier I
  measured on Linux which chain types a packet actually traverses, and the same question applied
  here would have answered it in one step.
- **Caught by:** the owner reading the claim and finding it implausible on the mechanism. No
  mechanism caught it, and none could have — the artifact says what I said it says. The severity was
  the unchecked part, and severity is not something a citation gate can verify.
- **Cost:** the wrong framing reached the owner in conversation and sat in
  `macos-pf-privileged-path.md`; both corrected the same day. The plan's *conclusion* — validate
  resolver output, reject loopback, link-local, multicast and the guest's gateway — was right
  throughout and is unchanged, which is why the error was easy to miss: a right remedy resting on a
  wrong reason.
- **The rule:** for any claim of the form "X is a security hole", state the packet, the path, and the
  enforcement point it crosses before reporting it. If no packet crosses, the entry is inert
  regardless of how wrong it looks. **Severity claims need their own verification; inheriting one
  from a document is not verifying it.**

### A34 — applied A33's own rule as a formula, naming an enforcement point I never checked the packet crossed (2026-08-09)

- **What happened:** L2 measured that host-side DNS resolution puts the host's own LAN address into a
  guest's allowlist. I reported that the guest then "completed a TCP connection to it", and called it
  "a widening that **happened**, not one inferred", citing: *packet `172.17.0.2 → 192.168.111.33:22`,
  forward hook, matching the `@allowed` accept*. The connection was real. **The attribution was
  invented.** A packet addressed to any address the host itself holds is delivered locally and
  traverses prerouting → **input**; it never enters the forward hook, so the `@allowed` rule never
  adjudicated it. Re-run with no allowlist and no accept rule at all, the guest reaches the same
  address anyway (`r6-host-destined-traffic.txt`: forward counter 3, input counter 8).
- **Source of the false belief:** the connection succeeded, an accept rule existed that *would* have
  matched had the packet arrived there, and its counter was non-zero. I read the counter as
  attribution without checking that its two increments were the positive control's — they were.
- **Why this one is worse than A33:** A33's rule was mine, taken one day earlier, and it says *state
  the packet, the path, and the enforcement point it crosses*. I stated all three. Stating a path is
  not measuring one, and the sentence I wrote to prevent the error is satisfied word-for-word by
  committing it. **A rule phrased as "say X" gets discharged by saying X.** The version that would
  have worked is *show which rule's counter moved for this packet, and rule out the alternatives* —
  a check with an outcome, not a required assertion.
- **Caught by:** myself, and only incidentally — I was answering "is there anything else to verify on
  hardware?" and checked whether forward-hook rules cover host-destined traffic, expecting to find a
  gap in the design. The gap was real; that it also falsified my own earlier claim was a by-product.
  Nothing was looking for the error. Had that question not been asked, the wrong mechanism would have
  stayed in a decision record.
- **Cost:** the claim reached **D133**, a decision record, and the archived verification queue. Both
  now carry the retraction; D133's decision is unchanged, because it never depended on this half.
  The replacement finding is larger than the retracted one — forward-hook enforcement cannot express
  any policy about sandbox-to-host traffic — so the error also delayed a real gap by a day.
- **The rule:** **a counter is not attribution.** Before crediting a rule with a packet's fate,
  re-run with that rule absent and confirm the outcome changes. Both A33 and this are the same
  underlying habit — asserting a mechanism that fits the observation instead of the one that produced
  it — and the corpus now says a stated mechanism is exactly as unreliable as a stated severity.

### A35 — spent three days measuring my way to a correction I had already found, written down, and labelled as one (2026-08-11)

- **What happened:** on 2026-08-08 I committed `prior-art-egress-enforcement.md` under the subject
  *"find the prior art, and one of it corrects my own advice"*. It states plainly that Cilium's
  approach points away from where the design was heading. On 2026-08-11 the Linux mechanism was
  rewritten from an address key to a netdev interface key — arriving, from hardware measurement, at
  the shape that document had described three days earlier. In between: R7 through R15, a full
  measurement pass.
- **Source of the false belief:** there wasn't one, and that is what makes this different from A33
  and A34. Nothing I believed was false. The prior art was read, understood, correctly summarised,
  and correctly labelled as a correction to my own advice. It simply had **no standing** — it was
  filed as research, and research sits beside a plan rather than gating it. So the design went on
  being derived from measurement while the answer sat one directory away.
- **Why the existing rules did not fire:** GEN §3 is *don't reinvent the wheel*, and every reading of
  it in this repo is about **building** — check whether a library already does it. Nothing said the
  same discipline applies to **measuring**. And the failure is invisible from inside: each individual
  run was well-formed, controlled, and produced a real result. There is no moment where the loop
  notices it is re-deriving, because re-deriving and deriving are the same activity.
- **Caught by:** an outside analysis the owner commissioned (`design/research/verification-method.md`),
  after the owner reported the symptom as *the design keeps reversing and I don't trust it*. Not by
  me, and not by any gate. Consistent with this corpus's own count: **the owner's report remains the
  detector with the highest catch rate**, and this is the first specimen where the correcting evidence
  was already inside the repo, written by me, at the time of the error.
- **Cost:** roughly three days of hardware time, and — larger — it is one of the reversals that made
  the owner distrust a design whose measurements were sound. The thrash was the deliverable's real
  defect, not any of its results. Fixing that is [D136](decisions/working-notes.md), which cost a day
  the build brief had already been scheduled against.
- **The rule:** **prior art gates opening a verification round; it does not run beside it.** Reading
  costs hours and prunes the design space, a round costs days and resolves one cell, and in that order
  cheap results discard expensive ones. Now in [GEN §3](principles/general-principles.md) as its
  second reading and in [`procedures/verification-rounds.md`](procedures/verification-rounds.md) §1.
  The generalisation worth carrying past this instance: **a document with no standing is not evidence
  the loop can act on.** Filing something correct in a place nothing consults is indistinguishable
  from not having found it — and it feels *better*, because the finding is on the record.

### A36 — audited harnesses for a defect class by reading the code and never opened the results (2026-08-12)

- **What happened:** asked whether any runs should be migrated to harness v2 and redone, I checked every
  Python harness in `linux-enforcement/` against v2's new invariant and reported two defective:
  `r11_netdev_lifecycle.py` and `r13_rootless_netdev.py`. Both compute
  `inherited = (not reach) and counter_moved`, and I showed that a broken rig yields
  `inherited = False`, which **passes** the claim "the stale chain does not capture its successor".
  I called it a free negative in a load-bearing result, proposed a queue, a port and a re-run, and the
  owner said yes. **Both results files record `reachable=True`.** The probe reached the denied host in
  each run, so the path was demonstrably live and the negative is self-controlling. Nothing needed
  redoing.
- **Source of the false belief:** the code. The hazard I described is real *in the construction* —
  that branch would produce a free negative — and I promoted "this could fail" to "this did fail"
  without opening the artifact that says which branch was taken. Two files, one `grep`, thirty
  seconds.
- **Why this one is not A34 repeating:** A34 asserted a mechanism that fit an observation. This
  asserted a defect with **no** observation, in an area where the observation is a committed file
  whose entire purpose is to be read. The corpus already names the class — `verification-method.md`
  calls it *evidence without a verdict*; this is its inverse, a verdict without evidence — and both
  come from the same place, which is that reading source feels like measuring.
- **The aggravating detail:** this happened three commits after landing [D136](decisions/working-notes.md),
  whose §1 is *a probe must demonstrate it can report failure rather than declare that it could*. I
  applied that standard to eleven harnesses and not to my own claim about them. **A rule aimed at an
  artifact does not attach to the reasoning that audits the artifact** — which is A31's shape again,
  the check not derived from the reading that produced the claim.
- **Caught by:** myself, one step before the rig — but only because the port required knowing which
  branch the run took, so the data became load-bearing for the *fix*. Had the remedy been "add a
  control" rather than "restructure around the recorded value", I would have re-run two sound
  experiments and reported the unchanged answer as a confirmation. The owner's question is what
  started it, consistent with every other entry here.
- **Cost:** none durable. The proposed DF would have asserted a defect that does not exist, and it was
  one step away. No commit, no hardware time.
- **The rule:** **an audit of a recorded run reads the recording, not the recorder.** Source says what
  a harness *can* produce; only the results file says what it *did*. When the two are both available
  and only one has been opened, the claim is unsupported no matter how well the code reads — and if
  the artifact is committed specifically so it can be audited later, then not opening it is the whole
  failure.

### A37 — measured a rig I built, and reported the property of the product (2026-08-12)

- **What happened:** across a session of macOS containment work I measured a root agent's reach
  inside apple containers — 763 writable `/proc/sys/net` knobs, a rewritable `firewall.py`, a
  shadowable `iptables` — and concluded that **running the agent non-root** would close the sysctl
  class and the root-owned binaries. I carried that to the owner as the highest-value single change
  available, they approved it, and I opened the launch path to implement it. `entrypoint.py:324`
  already execs `gosu yoloai python3 sandbox-setup.py`, branching on `running_as_root` rather than on
  backend. **The agent has been uid 1001 on every backend all along. There was nothing to implement.**
  And the fact I had not checked, `Dockerfile:229`, is `yoloai ALL=(ALL) NOPASSWD:ALL` — so the
  recommendation would not have helped even if it had been outstanding.
- **Source of the false belief:** every container I measured was one I launched, with
  `--entrypoint /bin/sh`, because that is what a harness needs in order to control the setup. That
  container runs as root. I then described *the agent* as running as root, having never launched one
  the way the product does. The rig was faithful to what it was built for and was never the subject.
- **What caught it:** going to write the code. Not a control — none of the harnesses were wrong, and
  each remains valid for the process it actually measured. The claim that failed was the one
  connecting them to the product, and no probe in any of them was pointed at that.
- **How far it travelled:** to a recommendation the owner accepted, and one step from a commit. It did
  not reach a durable artifact only because implementing it required reading the file that refutes it.
- **Why this is not A34 or A36 repeating.** A34 asserted a mechanism that fit an observation; A36
  asserted a defect with no observation. This one has an observation, a sound harness, and a correct
  measurement — and applies it to a subject it never sampled. The defect is not in the evidence or its
  absence but in the **population**: `inference-overreach` (D136 §6) where the overreach is about
  *whose process it was* rather than *how many*.
- **The rule:** **when a harness constructs its own subject, the claim is about the construction until
  something checks the product builds it the same way.** A rig that must control setup will diverge
  from the product's setup — that is what makes it a rig — so "how does the product launch this?" is
  not a detail to confirm later, it is the step that decides whether the measurement transfers at all.
  One `grep` for `gosu`, at any point in the session, closes it.


### A38 — framed a reversal as a refinement, and could not see it because I wrote both records the same day (2026-08-13)

- **What I claimed:** that D138 *refined* [D135](decisions/working-notes.md) rather than reversing it.
  D135 says yoloAI never refuses a sandbox for lack of enforcement capability. D138 says an
  explicitly-named mode that cannot be delivered is refused. My argument was that naming the modes
  made refusal safe, because a refused `restricted` still leaves `isolated` available — so the
  situation had changed rather than the position.
- **What was true:** it was a reversal. An independent audit took the argument apart in three moves,
  each checkable: the safety argument **does not reach the bottom rung** (refusing `isolated` leaves
  only `open`, which defends nothing, or `none`, which no agent can run under — precisely D135's
  rejected option 1); the premise that the tiers had stopped being implicit was **false**, because the
  enum gave D135's *two* in-sandbox tiers a single name; and the half of D138 that was safe **was
  already shipped** (`internal/netpolicy/strategy.go:42` already refuses `isolated` where the backend
  lacks the capability), so the delta was exactly the reversal.
- **Caught by:** an independent audit the owner asked for, on a plan I had told them I was the wrong
  person to judge. The owner then settled it in the other direction — retiring degradation outright,
  on comprehensibility rather than on safety, which is an honest reversal and a better decision than
  the one I was defending.
- **How far it travelled:** into `working-notes.md` as D138, and into a plan built on it. Both were on
  an unpushed branch, so nothing shipped.
- **The tell I had and did not use.** D137 and D138 were written **the same day, by me**. D137's
  rejected alternative 4 reads *"Refuse `--network-isolated` where it cannot be enforced adversarially.
  **Rejected on D135**"* — and D138 does that, without citing D137 and without marking the rejection
  superseded. A contradiction that plain, four hours apart, in one file.
- **Why it survived.** Not for want of scepticism: I flagged the exact question as the one I could not
  judge, and asked for an audit of it. But I had already written the record, and a record you wrote is
  read as an argument you are checking rather than as text that might be wrong — the same reading
  problem A35 names for prior art, pointed inward. Writing the decision *and* its justification
  in one sitting removes the only reader who would have noticed.
- **The rule:** **a decision that supersedes another must state which one it is — refinement or
  reversal — and name the superseded text explicitly.** If the two records were written by the same
  author within a day, that claim is unreviewed by construction and needs an outside reader before it
  lands. The cheap check is mechanical: grep the older decision's *rejected options* for the thing the
  new one is deciding. It costs one search and it would have caught this.

### A39 — classified a corpus by filename prefix from a truncated listing, and built a trust ledger on it (2026-08-13)

- **What I claimed:** in the prior-art gate of the mac-channel round, that "all of
  `linux-enforcement/` is pre-v2", and from that, that **D139's argument for shape (B) over shape (A)
  rested on pre-v2 evidence** and could not be cited as measured. That went into the round's queue
  file as a load-bearing finding, under a heading announcing that the corpus splits by harness
  generation.
- **What was true:** seven of that directory's 59 results are v2, and they are exactly the two D139
  cites. "V5's ifindex result" is `v2-v5-netdev-device-binding.txt` and "V6b showed a counter cannot
  distinguish it" is `v6b-foreign-chain-shadowing.txt`. D139's fail-open leg meets the current bar,
  and my correction to it was the thing that was wrong.
- **How it happened, mechanically:** I listed the directory with a signature check piped through
  `head -30`. The listing is alphabetical, `v*` sorts last, and 59 files do not fit in 30 lines. I
  then wrote a conclusion quantified over the whole directory — "all of" — from a sample I had not
  noticed was a sample. The parenthetical I added, "(K/L/C items)", is the tell: it names exactly the
  three prefixes the truncation happened to show, and I read it as a description of the directory
  rather than of my output.
- **Caught by:** the owner relaying another agent's provenance work, which named the trap directly —
  in `linux-enforcement/`, `v*` is a round-2 *item* and `r*` a round-1 item; the letter is the item,
  not the harness version. I had inverted a naming convention I never checked existed.
- **How far it travelled:** into the round's queue file, which is the artifact the round's synthesis
  reads. It was there for one turn. Nothing built on it, because C1 does not depend on D139's
  reasoning — but the queue file is precisely where a wrong fact would have been laundered into the
  synthesis pass as settled prior art.
- **Why it survived my own scepticism.** The claim *widened* a caution the owner had just given me.
  Being told "prior research may have silently given wrong answers" made a finding that more research
  was untrustworthy feel like diligence rather than like a claim needing its own evidence — the
  D136 §3 asymmetry (**a bad run that agrees with you is the one that survives**) operating on a
  reading rather than on a run. I applied a discrimination standard to every file in the corpus and
  none to my own listing of it.
- **The rule:** **a claim quantified over a directory needs a count, and the count has to come from
  the same command that produced the claim.** `ls | wc -l` against the number of lines actually
  classified; if they differ, the conclusion is about the sample. And a corpus-wide census is
  code — a `for` loop over a full glob with its totals printed — not a listing read by eye. Concretely
  here: `for f in *.txt; do ...; done | sort | uniq -c` prints 52/7 and cannot be truncated into
  agreement.

### A40 — treated “Apple” as one backend and shipped the Seatbelt mechanism on Apple Container (2026-08-13)

- **Claimed:** Apple Container could preserve `--network-none` by passing the vendor’s native
  `--network none` adapter. I rewrote the implementation, tests, shipped help, design records, and
  PR description around that claim, then pushed the branch.
- **True:** “Apple” names two different backends in this repository. Seatbelt supports a native
  no-network policy; Apple Container does not. The DNS change must reject Apple Container
  `--network-none` before setup rather than transplanting Seatbelt’s capability onto it.
- **Source of the false belief:** DF198 called its measured subject “apple” and proposed
  `container run --network none` as the remedy. I treated that backend label as proof of the
  product/runtime intersection instead of re-establishing which Apple mechanism the result
  described. The shared platform name hid the backend boundary.
- **Caught by:** the owner naming the ambiguity directly: “Apple” can mean Seatbelt or Container.
  No local test could catch the mistake because I had rewritten the fake-binary expectation to
  certify the same false adapter.
- **Cost:** two commits pushed to the fork and a complete PR body drafted around the wrong
  behavior; PR creation failed independently before the claim reached the base repository.
- **Class:** coherence pressure across same-platform siblings — one backend’s demonstrated
  mechanism inherited by another because both were called “Apple.”
- **Gated now?** No. The concrete check is to name the backend type and executable in every
  platform-level network claim: `seatbelt`/SBPL versus `apple`/`container`. A test derived from the
  mistaken adapter cannot establish that distinction.
