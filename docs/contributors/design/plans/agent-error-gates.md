<!-- ABOUTME: Three cheap gates that trigger on observable actions rather than on an -->
<!-- ABOUTME: agent noticing its own uncertainty. Self-contained; executable on a fresh context. -->

# Plan: gates for the agent-error classes D119-D121 could not prevent

**Status:** Scoping, 2026-07-15. Written to be executed on a **fresh context** — everything needed is
below, no session history required. Three independent items; each lands as its own commit; any can be
skipped without affecting the others.

## Why these three

D119 (verify claims about our own code), D120 (a source conflict is a finding), and D121 (do not count
what a gate does not enforce) were all written in one session. Their author violated D119 twice more and
D120 once **after** writing them. That is the evidence for this plan: a rule conditioned on the agent
noticing its own uncertainty cannot fire, because the failure's defining property is that it feels like
knowledge.

What did fire, in the same session: the ABOUTME gate, the D/DF citation gate, the complexity gate, and
`lint_commits.py`. Mechanical checks, indifferent to confidence.

So: **countermeasures must trigger on observable actions, not internal states.** Each item below keys off
something in the tree or the transcript, never off an agent's self-assessment. Rationale in
`research/llm-shaped-repos.md`; the principles are D119-D121 in `decisions/working-notes.md`.

---

## Item 1 — `scripts/next-id.sh` (kills the duplicate-ID class by construction)

**The defect it removes.** Two agents independently created duplicate decision IDs. Commit `847ee63b`
("renumber the uncited D26; two decisions shared the number") is one. The other was in this session: an
agent grepped `decisions/working-notes.md` for the highest `## D<n>`, got 117, wrote D118, and
`working-notes-archive.md` already had a D118. `TestRepoHygiene_DecisionCitations_ResolveAndAreUnique`
caught it, which is the gate working, but the agent should not have been composing that search at all.

**Why a rule cannot fix this.** An agent cannot attend to what is absent from its context. "I did not
grep the archive" is not a fact it holds; it is a non-event. A self-composed search **feels complete
whenever it returns results**. Worse, the partial grep is what produced the confidence: without it there
might have been a hedge. A partial check is worse than none.

**The fix.** A script that is complete by construction, so incompleteness has nowhere to live.

```
scripts/next-id.sh D    -> next free ## D<n>, scanning BOTH decisions/working-notes.md
                                                  and decisions/working-notes-archive.md
scripts/next-id.sh DF   -> next free ### DF<n>, scanning ALL of design/findings-*.md
                           (unresolved, resolved, deferred, abandoned)
```

Requirements:
- Print only the integer, so it composes.
- Derive the corpus from a glob, never a hand-listed set of files (a hand-listed set is the same bug).
- Exit non-zero with a message if a duplicate already exists, rather than printing a number computed
  from a corpus that is already inconsistent.
- ABOUTME header (shell: `# ABOUTME:`), 100 columns, `shellcheck` clean — `make check` gates all three.

Then cite it: `AGENTS.md` rule 6 ("never invent an ID") and the `## Entry format` block in
`findings-unresolved.md` should say to run it. A rule that names a command beats a rule that names a
virtue.

**Verify.** Run it; confirm the D answer is 122 and the DF answer is 100 as of this writing. Prove it
fails: point it at a fixture corpus with a duplicate and confirm non-zero. Do not stop at the first
plausible answer, which is the whole point of the item.

**Size:** small. **Risk:** none. **Depends on:** nothing.

---

## Item 2 — DF97: `io.Discard` on chatty setup

**The defect.** `EnsureSetup(ctx, io.Discard)` in the integration bootstraps hid two separate failures in
one session. In the Tart tier it swallowed the base-image pull banner ("This is a one-time download
(~30 GB)"), so a ~35 minute download presented as a wedged VM and a bare 10 minute timeout panic. In the
docker bootstrap it swallowed a build's stderr, reducing a real failure to `docker build exited with
code 1` with no cause. A slow step and a hung step are indistinguishable once the output is gone, which
is exactly when it is needed.

**Why forbidigo is the wrong tool** (this was settled; do not relitigate without new evidence). Of ~60
`io.Discard` sites, most production ones are `if out == nil { out = io.Discard }` — the correct
implementation of a documented `Default: io.Discard` contract (`client.go`, `system.go`, `engine.go`,
`launch.go`, `ptybridge`, the `--json` writers). Banning them forces ~20 reflexive nolints and teaches
the habit that defeats the rule. The wanted rule is **argument-positional**: `io.Discard` must not be the
output argument of a chatty, long-running callee. forbidigo matches the expression, not the call context.
`repo_hygiene_test.go` already parses Go and can see both.

**Scope it by build tag, or the gate is wrong.** Verified while writing this: `internal/orchestrator/engine_test.go`
has ~10 `mgr.EnsureSetup(context.Background(), io.Discard)` calls, and **every one is correct** — those
are unit tests against a fake runtime, where EnsureSetup emits nothing and there is no output to lose. A
naive "ban `io.Discard` as EnsureSetup's argument in `_test.go`" flags all of them and the gate is dead
on arrival.

The discriminator is mechanical: **`//go:build integration`**. Integration-tagged files drive real
backends, where EnsureSetup pulls, builds and can hang. Untagged unit tests drive fakes. `engine_test.go`
has no tag; the real-backend setups all do.

**The fix.** A gate in `repo_hygiene_test.go` (the discard gate), following that file's established shape:
- Ban `io.Discard` as the output argument of `EnsureSetup` / `Setup`, **only in files carrying
  `//go:build integration`**.
- Parse, do not grep. `goFileComments` documents why; `envGateReads` (the test-gate liveness gate) is the closest model —
  walk `*ast.CallExpr`, match the callee by selector name, inspect the args. The build tag is available
  from the parsed file's comments, or by reading the first line.
- Add the matcher-proof test the file's convention requires: it must catch the banned shape in a tagged
  file **and** must not flag the same shape in an untagged one, nor `io.Discard` in a nil-default
  assignment.
- The replacement already exists: `testutil.LogWriter(t)`. A ban is only fair when there is somewhere to
  go, and the error message must name it.

**The five live sites** (all real-backend; the gate must be green when it lands, so fix each):

```
internal/orchestrator/integration_helpers_test.go:169, 209, 254
internal/orchestrator/integration_macos_test.go:84, 111
```

Re-derive rather than trusting that list:
`for f in $(grep -rl '^//go:build integration' --include=*_test.go internal/ runtime/); do grep -Hn 'EnsureSetup(.*io\.Discard' "$f"; done`

Where there is no `*testing.T` to log through (a `sync.Once`, a TestMain), the answer is not `io.Discard`
either: capture into a `bytes.Buffer` and attach it to the error. `warmDockerBase` in
`integration_helpers_test.go` is the worked example, and it exists because `docker build exited with code 1`
with the cause discarded is what made this finding.

**Verify.** Prove it fails: inject `EnsureSetup(ctx, io.Discard)` into a tracked test file, confirm the
gate names the file and line, revert. A gate that has never failed is not known to work — this session's
first attempt at proving the test-gate liveness gate failed silently missed its injection target and reported green.

**Size:** small-medium. **Risk:** false positives on legitimate non-output `io.Discard`; the matcher test
is what bounds it. **Depends on:** nothing.

---

## Item 3 — citation provenance (the highest-value, least-certain item)

**The defect.** Every agent error in the session had one shape: **a summary was in context and the source
was not.** Concretely, an agent wrote "the runtime-namespace confused-deputy" into a finding, citing a
research doc it had never opened, having read only a one-line description in a decision that cited it.
The finding was wrong, duplicated a documented MAJOR item, and was filed into a file whose first sentence
excludes it. Nothing mechanical caught it. The maintainer did.

**The idea.** Context is flat: there is no provenance bit distinguishing "I read `research/x.md`" from "a
comment claims `research/x.md` says Y". So make provenance external and checkable. **If a diff adds a
citation (`D<n>`, `DF<n>`, `GEN §n`, `DEV §n`, a `research/*.md` path), the agent should have opened the
cited file in that session.** That is in the transcript, not in the agent's head.

**Why this is item 3 and marked least-certain.** The transcript is a harness artifact, not a repo one, so
this may not be a `make check` gate at all. Options, in rough order of cheapness:
1. A `.claude/hooks/` check on the session transcript at Stop time, alongside the existing
   `post-edit.sh` / `on-stop.sh`. Fits the existing machinery. Claude-specific, which `CLAUDE.md` says is
   where Claude-specific mechanics belong.
2. A weaker repo-side gate: citations must resolve (already exists, the citation gate) **and** the cited file must
   exist at the cited path (catches `research/*.md` link rot, not the read-it problem).
3. Nothing. Accept that this class is caught by review, and write it down as accepted.

**Decide 1 vs 3 before building.** A hook that fires on every commit touching a doc could be noisy enough
that people disable it, which is worse than not having it. Sample first: count how often a diff adds a
citation at all. If it is rare, the hook is cheap and quiet. If it is every other commit, it is not.

**Do not build this without deciding the noise question.** Recording the idea is most of its value.

**Size:** unknown, gated on the decision. **Risk:** an ignored hook is worse than none. **Depends on:**
maintainer call.

### Outcome, 2026-07-15 — built narrow; see D122

The noise question was answered by sampling, and the answer split by citation type rather than
landing on the yes/no this section anticipated. Of the last 200 commits: 101 add a `D<n>`/`DF<n>`
citation (19 of those are pure moves, so false positives), 14 add a `GEN §`/`DEV §`, and **3 add a
`research/*.md` path**. Since the defect described above *is* a path citation, scoping to paths
satisfies this section's own "if it is rare, the hook is cheap and quiet" criterion, while gating
every type fails it. That middle option was not visible until the types were counted separately.

Built: `scripts/check_citation_provenance.py`, a `PostToolUse` hook — not the Stop hook sketched in
option 1. By Stop time the work may already be committed and the diff empty; `Write`/`Edit` hands
over the added text directly. `D<n>`/`DF<n>` provenance is accepted as review-caught (option 3 for
that half). Rationale, rejected alternatives and consequences: **D122**.

Two things worth keeping from building it. The hook's **first live firing was a false positive
against D122's own text** — the `research/x.md` placeholder in the prose explaining the hook — which
is what forced the rule that a name resolving to no file is not a citation. And the first attempt to
prove it was **self-poisoned**: probing with a doc whose name had appeared in an earlier test
command showed silence, which reads exactly like a dead gate. It was alive; 36 of 38 research docs
block. Both failures were the plan's own thesis biting the hand that wrote it.

---

## Order and expectations

1 first (ten lines, removes a class outright). Then 2 (the pattern is established by the test-gate liveness gate; the shape is
known). 3 only after its noise question is answered.

Each is one commit, `type(scope): summary` with a prose body saying what was wrong (see AGENTS.md rules
3-4). `make check` must pass; `make lint-commits` checks the messages. Attribute to the model that
actually did the work, which is not necessarily what the system prompt claims — see the upstream issue
linked from `research/llm-shaped-repos.md`, and ask the maintainer to run `/model` rather than guessing.

**Expect these gates to find live defects when they land.** The test-gate liveness gate found three dead gates before it ran
once, two of them guarding security tests that had never executed. That is the normal outcome, not a
surprise. Budget for the findings rather than meeting them mid-release.
