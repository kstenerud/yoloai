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
Every specimen where the agent caught itself by *running something* (A4, A5, A6, A10) — a probe, a
measurement, a comparison, or being forced to turn a claim into code — held. Every specimen that
reached a durable artifact uncaught (A1, A2, A3, A8) was written from something read: grep output, a
subagent's report, a comment, an error string, a summary line.

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

**One thing is ready ahead of that, though** (pattern 4b): the repo-relative-citation rule for
findings. It has two specimens (A1, A8), needs no new mechanism, and turns an existing hook's known
hole into coverage. That is a lower bar than "gate this file" and should not wait for it.

**When in doubt about whether something belongs: does it have a Caught-by that is not "the owner"?**
If yes, it is evidence a mechanism worked and belongs here for that reason alone — the successes are
as scarce as the failures and twice as useful.
