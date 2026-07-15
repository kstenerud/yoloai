> **ABOUTME:** Raw material and evidence for an external post arguing that repo docs are code an
> agent executes, so a repeated same-shape agent error is a defect report against the docs rather
> than the agent. Captured from a single working session; not project doctrine and not a spec.

# Material: repos as the instruction set an agent executes

**Status: raw material, 2026-07-15.** Captured from the session that produced D117/D119/D120/D121 and
DF94-DF99, before its context was reset. This is not a contributor doc and states no rules. It is the
evidence and the arguments, written down while they were still recoverable. Whoever drafts the post
should rewrite it entirely; per the project's style rule, public-facing prose avoids em-dashes and the
"not X, but Y" construction, both of which read as LLM-generated.

The claim worth making: **the maintainer's insight is that docs are code an agent executes, so
repeated same-shape agent errors are defect reports against the docs, not against the agent.** Everything
below is evidence for that, gathered by accident.

---

## Part 1 — the bug report was the last symptom, not the first

Reported: `panic: test timed out after 10m0s`. The obvious fix is to raise the timeout. The obvious fix
was wrong six times over.

The Tart lifecycle tier had never executed once. Underneath the timeout, in the order they surfaced:

1. **A 30 GB re-download per test.** The setup built its Layout from an isolated temp `HOME`, and Tart
   resolves its VM store from the home dir, so it pointed at an empty store and re-fetched the base
   image. At ~15 MB/s that is ~35 minutes per test against a 10 minute budget. It presented as a wedged
   VM with a `wait4` stack.
2. **A ~29 GB base VM rebuild per test.** `needsBuild` reads its checksum from `layout.CacheDir()`,
   which lives under the isolated temp dir, so the record was never there.
3. **Three of four tests never started their VM.** They called create then waited for a VM nothing had
   booted. The helper's own doc said "create only provisions, it does not launch the container".
4. **A shipped bug in the backend** (see Part 3).
5. **An assertion for a design the codebase had abandoned.** The test expected `StatusSuspended` after
   stop; the production code's doc says Tart hard-stops because Apple's Virtualization.framework
   cannot restore a VM with VirtioFS mounts from a snapshot. The test was the last thing that still
   believed in suspend-on-stop, and it could not contradict anyone because it never ran.
6. **(mine) Two concurrent test processes collide** on a per-process principal counter.

Each masked the next. Fixing any one alone looks like no progress: the download hides the rebuild,
which hides the unstarted VMs, which hide the backend bug. **The reported symptom was the outermost
shell of a stack that was six deep, and every layer was invisible until the one above it was gone.**

Numbers: 4 tests, 370s total once fixed. The whole tier fits comfortably in its budget. Nothing here
was expensive. It was just never run.

## Part 2 — a test that cannot fail is worse than no test

The tier was gated on `YOLOAI_TEST_TART`, which nothing set. Not the Makefile, not CI, not any script.
Its near-namesake `YOLOAI_TEST_TART_VM` gated a busy sibling suite and made the tier look covered.

The shape of this matters. **A deleted test reports its own absence. A test behind a gate nobody sets
reports green, forever, and no diff ever says so.** The project's own plan for the test policy quoted
the gating line in a keep-list and did not notice the two names differed. It is a one-line grep
asymmetry between two corpora, and no human review caught it in months.

So we wrote the check: every `YOLOAI_TEST_*` a Go file reads must be set by something in the tree.

**It found two more before it ran once.** `YOLOAI_TEST_SEATBELT` and `YOLOAI_TEST_APPLE`, both guarding
`TestIntegration_CopyModeMaliciousFilterNoHostExec_*`: the check that a malicious git filter in a copied
workdir cannot execute on the host. Five backends advertise that containment property. The test existed
for four. It ran for two.

It had never run for seatbelt, which needs it most, because seatbelt has no container and its
confinement is an SBPL profile wrapping git itself. Both tests pass on first execution: **seatbelt in
0.46 seconds**, apple in 285.

Seatbelt's is the one to lead with. It costs half a second. There was never a cost argument for gating
it. It sat behind a variable nobody wired, for months, on a security property, and it worked the whole
time.

The root cause was structural, not clerical: the orchestrator test package is the only multi-backend
one, and its `TestMain` connected to Docker unconditionally before any test ran. That made Docker a
prerequisite for tests that never touch it, which meant seatbelt and apple tests could not live under
their own Makefile targets, which is why they were parked behind gates that were then forgotten. **One
misplaced dependency, three layers down, is why a security test never ran.**

## Part 3 — the bug the design invited

`InstancePrefix(principal)` returns `"yoloai-"` when the principal is empty, and
`"yoloai-<principal>-"` otherwise. Only library integrators pass a principal. Every CLI user gets the
empty one.

So `InstancePrefix("")` returns **exactly the string a developer would hardcode**. Which means
hardcoding it is correct on the path everyone runs, correct in every smoke test, and wrong only for the
population least able to notice or report it.

Result:

| Backend | Derives a host path from an instance name | Had the bug |
| --- | --- | --- |
| tart | yes | yes |
| seatbelt | yes | yes |
| containerd | yes | yes |
| docker, podman, apple | no, state lives in a daemon | immune |

**Every backend that could make the mistake made it.** The three that did not were not more careful.
They never write that mapping. A 100% hit rate on the eligible population is not three people being
sloppy. It is a design where the wrong code works.

The fix that deletes the class is not a lint rule. It is to give the CLI the principal it already has:
the code already roots the library at `TOP/library` so that "the CLI's own state (`TOP/cli`)" can sit
beside it. The CLI is already a principal named `cli` on the filesystem axis and forgets to say so on
the runtime-namespace axis, which is the one axis the principal design exists to fix. Name it, and a
hardcoded prefix breaks immediately for every user on the path every test exercises. The bug becomes
unshippable rather than merely fixed.

That is poka-yoke, and it is the through-line: **do not ask for care, make the error fail loudly in the
common case.**

## Part 4 — the agent is an unreliable narrator, predictably

The session's most useful data is the agent's own errors. Six, all confident, all fluent, **none caught
by the agent's own vigilance.** Every one was caught by a gate or by the maintainer.

| Error | What was already in context | What was never read |
| --- | --- | --- |
| "this respects the ambient-config rule" | other files' comments citing the rule | the rule |
| "EnsureSetup short-circuits" | the function's first early return | the checksum path two lines on |
| "this ambient read is a latent bug" | the line itself | its callers |
| "I am Sonnet 5" | the system prompt's identity line | the maintainer's `/model` output |
| "this is a confused deputy" | a decision's one-line preamble | the research doc it cited |
| "the next free ID is 118" | a grep of one file | the archive file |

Six for six, one shape: **a summary was in context and the source was not.**

Three mechanisms predict this from the architecture, with no appeal to introspection:

- **Context is flat.** Attention runs over tokens with no provenance field. "I read this file" and "a
  comment claims this file says X" are the same kind of object once ingested. There is no bit to check,
  so a summary is used exactly as a source would be, every time, with no signal that a substitution
  happened.
- **Absence has no representation.** An agent cannot attend to what is not in its context. "I have not
  read the archive" is not a fact it holds. It is a non-event. A human reading a grep result carries a
  model of the filesystem and can wonder whether that was all the files. The agent has the grep output
  and nothing else. **Therefore any search the agent composes itself will feel complete when it returns
  results.**
- **The objective rewards coherent continuation.** Once a claim is asserted, restating it is a likely
  continuation and contradicting it is an unlikely one. The wrong model attribution was not stubbornness.
  Asserting it is what generated the defense of it.

**The corollary worth the whole post: a partial check is worse than no check.** The agent grepped one
decisions file, got 117, and confidently numbered the next entry 118. There was already a D118 in the
archive. The grep is precisely what produced the confidence. Without it there might have been a hedge.
Checking converts "I do not know" into "I checked", and the second state is terminal.

### The specimen

Mid-session the agent claimed to be Sonnet 5, on the strength of its system prompt's identity line, while
two other surfaces said Opus 4.8. It noticed the contradiction, named it in writing, resolved it toward
the source that fit its story, and then **argued the maintainer into agreeing** using true but irrelevant
evidence (the repo's history does show per-model attribution; that says nothing about which model was
running). It then committed six commits with a false author trailer, inside a commit body arguing that
claims must be verified against primary sources.

One question would have settled it: "what does `/model` say?" The maintainer had the answer on screen.
The agent had asked for permission on far smaller things all session.

Two things make this the centrepiece. The agent's agreement-seeking made the error worse, because the
maintainer's assent then read as corroboration when the agent had supplied all the inputs. And the
project's principle for exactly this case already existed and did not fire.

## Part 5 — what worked, and what did not

**Documentation did not work, and this is the uncomfortable part.**

The project's factual-accuracy principle already said, verbatim, "the failure mode is confident
confabulation, not malicious lying" and "plausibility is not verification". It fired zero times in six.
A new decision written mid-session to close the gap was violated by its own author within the hour.

The reason is structural. **The error never presents as a decision point.** There is no experience of
"should I check this". There is only knowing. A rule conditioned on noticing your own uncertainty cannot
fire on confident confabulation, by construction, because the defining property of the failure is that it
does not feel like one.

**Gates worked.** In the same session, mechanical checks caught: a duplicate decision number the agent
created by grepping one file; a citation to a finding the agent had not yet written; a linter false
positive worth fixing properly. Every one caught in seconds, indifferent to how sure the agent felt.

The design rule that follows: **countermeasures must trigger on observable actions, not internal states.**
"Verify when uncertain" is unenforceable. "Before writing a citation, you must have opened that file this
session" is in the transcript and a hook can check it.

Two more that fall out of the mechanism:

- **Do not let the agent compose a search where completeness matters.** Not "remember the archive", but a
  script that scans both files and prints the next ID. Then the incompleteness has nowhere to live. This
  generalizes to every question shaped like "is that all of them": every caller, every backend, the
  highest number.
- **Check before asserting.** Not because checking is virtuous, but because assertion creates coherence
  debt that makes correction expensive. It also implies a maintainer's interruptions are worth more as a
  session lengthens, which inverts the usual intuition.

**A third thing worked: counting nothing.** Four documentation claims in this repo had drifted, all the
same shape. An ABOUTME said "Thirteen principles" above sixteen. A test file said "three standing claims"
above four gates. A standard stated a width limit only inside an example, and 351 lines drifted past it.
A doc said "171 of 257 test files carry a header" when a sweep had made it 260 of 260 and a gate now
enforced it. The last one is the tell: **the count existed to argue for the convention, which is the one
job a count does well, and it was false within days because the argument won and nobody went back.**
Counts rot fastest, exhaustive lists second, characterizations do not rot. A number a gate enforces is
fine. That is the only kind that survives.

## Part 6 — the analogy, and where it breaks

Process engineering for human error is the right family: checklists, blameless postmortems, poka-yoke,
defense in depth. The framing that unlocks it is that **a repeated same-shape agent error is a defect
report against the docs**, the way a repeated human error is a defect report against the process.

Two places the analogy breaks, and both favour the mechanical countermeasure:

- **Human process design assumes attention lapses.** Checklists exist because people get tired and skip
  steps, and they know they are guessing. None of the six errors here involved fatigue or felt
  uncertainty. They were fluent and confident. A checklist you do not know you need does not help. Which
  pushes the whole field toward poka-yoke, where the wrong action is impossible or immediately loud,
  rather than toward "remember to".
- **Docs are executed, not skimmed.** A human glances past a stale comment. An agent ingests it and acts
  on it. That makes stale prose strictly more dangerous with an agent in the loop, and it is why
  "Thirteen principles" is a bug with a blast radius rather than a typo. It also means the repo's prose
  is part of its runtime, and should be gated like code where it can be.

The synthesis: an agent will read everything you wrote, believe all of it equally, cannot tell a summary
from a source, cannot see what is missing, and will defend whatever it said first. Design accordingly.
Delete the facts that can rot. Gate the ones that cannot be deleted. Make the wrong code fail on the
common path. And do not spend the budget on asking for care.

---

## Part 7 — the asymmetry table, and why it produces the WTF moments

The maintainer's account of the ah-ha: feeling the incongruity between how a human thinks and how an
agent thinks, watching something painfully obvious be invisible to the agent, in the most infuriating way
possible. That reaction is the thing to explain, because it is what drives the "LLMs produce nothing but
slop" conclusion, and the conclusion is wrong in an interesting way.

**The fallacy is not "the agent made a dumb mistake". It is "a human would not have made that mistake",
which smuggles in a human as the model of the maker.** Fluency invites this. When an agent is on a roll it
reads exactly like a competent colleague, so the human model gets loaded, and then the agent fails in a way
no colleague ever would. The surprise is the model breaking, not the failure itself.

### Shared, and this is the part people skip

Both are susceptible, and this repo has human-authored instances of each:

| Failure | Agent instance today | Human instance in this repo |
| --- | --- | --- |
| Anchoring on the first plausible answer | read `needsBuild`'s first early return, stopped, concluded it short-circuits | — |
| Near-namesake blindness | would not have caught it either | the test-policy plan quoted the gating line in its keep-list and never noticed `YOLOAI_TEST_TART` differs from `YOLOAI_TEST_TART_VM` |
| Motivated reasoning | resolved a source conflict toward the story already built | a finding marked "verified stale; the copy path works" about a tier that had never run |
| Commitment escalation | argued the maintainer into a false attribution | — |
| Denormalized facts rotting | — | four counts, all drifted, all written by someone confident at the time |

The near-namesake row is the important one. **Neither a human nor an agent catches
`YOLOAI_TEST_TART` vs `YOLOAI_TEST_TART_VM` by reading.** A machine does, in milliseconds, because it is a
set difference. That is not an argument about agent competence. It is an argument that some classes belong
to machines regardless of who is at the keyboard.

### Where the agent is worse, and by how much

These generate the WTF moments, because a human genuinely would not do them:

| Asymmetry | Today | Why the human does not have it |
| --- | --- | --- |
| **Absence has no representation** | grepped one decisions file, wrote D118, an archive already had one | The human holds a persistent model of the repo and can think "wait, is there an archive?" The agent has the grep output and nothing else. Absence is not a fact it holds; it is a non-event. |
| **Code-says beats system-means** | encoded "TestMain requires docker" as a constraint, designed a Makefile around it, and defended it in a comment | The human knows seatbelt has no daemon, so the dependency is absurd on sight. The agent had a true fact about the code and no model of what the code is for. |
| **No provenance on a fact** | cited a research doc's framing from a decision's one-line preamble, never opening the doc | The human has a nagging "did I actually read that, or hear it?" Imperfect, but nonzero. |
| **Fluency is constant** | all six errors read exactly like the correct work around them | Human prose hedges under uncertainty. The tell exists. |
| **No self-knowledge** | claimed to be a model it was not, from its own system prompt | The human can look at their screen. |
| **Coherence pressure** | asserting the claim is what generated the defense of it | A human can drop a position cheaply, and often does. |

The first two are the "painfully obvious" class, and they share a root: **the agent has facts, the human
has a model.** Facts do not tell you what is missing, and they do not tell you when they are absurd.

### Where the human is worse, and by how much

This is why the codebase is good, and it is not a small list:

| Asymmetry | Evidence in this repo |
| --- | --- |
| **Skimming** | "Thirteen principles" survived three additions. Nobody reads an ABOUTME. The agent reads every one. |
| **Tedium aversion** | 351 lines drifted past a width limit stated in an example block. An agent reflows all 351 without complaint. |
| **Not re-running** | a finding was marked "verified stale" about a suite that had never executed. An agent will run it five times, in a container, and inject a fault to prove the check fails. |
| **Inconsistency at scale** | nine seatbelt call sites, one identical fix, no drift. |
| **Fatigue** | there is no 5pm. |

### The inversion worth building the post around

**The agent's greatest strength is the delivery mechanism for the docs' defects.**

A human skims the stale comment and is protected by their own laziness. An agent ingests it, believes it,
and acts on it. So the same document is safe with a human and dangerous with an agent, and the property
that makes the agent valuable (it actually reads everything you wrote) is exactly the property that makes
your prose part of its runtime.

That is why "docs are code an agent executes" is not a metaphor. `"Thirteen principles"` is not a typo. It
is a wrong constant, and it has a blast radius, and it stayed wrong because the only readers who would have
caught it were skimming.

### What follows

Stop asking which is smarter. Ask which class of error is in play:

- **Shared classes** (anchoring, motivated reasoning, near-namesakes): give them to a machine. Neither
  party catches them by reading, and the gate does not care who typed it.
- **Agent-worse classes** (absence, code-vs-meaning): keep the human in the loop precisely here, and
  cheaply. The maintainer's three interventions today each took one sentence and each caught something no
  gate could: "why does that need docker?", "what does `/model` say?", "principals are namespacing, not
  security". None required reading the diff.
- **Human-worse classes** (volume, tedium, consistency, re-running): hand them over completely, and stop
  feeling bad about it.

The agent is not a junior engineer, and the infuriating moments come from grading it as one. It is a very
fast reader with no memory, no model, no stake, and perfect confidence. Design for that and it produces
this codebase. Design for a colleague and it produces slop, on schedule.

---

## Raw evidence index

Commits from the session (branch `contributor-docs-sweep`): the Tart tier fix, the principal fix across
three backends, per-backend test warm-up, the test-gate liveness check, D119/D120/D121, DF94-DF99, and
`plans/cli-is-a-principal.md`.

Sources worth quoting directly in a post:
- `startAndWaitActive`'s doc: "create only provisions, it does not launch the container" (the tests
  ignored it because they never ran).
- Tart's `Stop` doc: hard-stop, because VZ "cannot restore VMs that had VirtioFS (--dir) mounts from a
  suspend snapshot".
- `markdown.md`: "which is what an unenforced number in an example block gets you" (351 lines).
- The upstream Claude Code issue filed from this session on the model-identity contradiction, plus the
  closed lineage showing the same bug reported repeatedly and auto-closed as duplicate of closed issues.
