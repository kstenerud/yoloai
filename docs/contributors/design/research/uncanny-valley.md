> **ABOUTME:** Notes from the bumpy road — moments where working with an agent stopped resembling
> working with a colleague, in both directions: the agent doing something no colleague would, and
> the human's offhand remark redirecting a bad path they never saw. Raw observations, not doctrine.

# The uncanny valley, and the bumpy road through it

Working with an agent trundles along feeling almost human, and then something breaks the spell. It
breaks in **two directions**, and only one of them gets talked about:

- **The agent does something no colleague would** — the WTF moment, arriving mid-competence with no
  change in tone.
- **The human says something small and offhand that pulls the agent off a bad path they did not
  know it was on** — a save, often delivered hedged, often not recognised as an intervention at all.

The bumps are the impediment. A developer calibrates trust the way they always have, gets burned by
a failure with no warning signal attached, and concludes the whole enterprise is slop — the "I
fucking hate AI" reflex. That reflex is not irrational; it is a correct response to a broken
instrument, misattributed to the machine. **Mapping the valley is how the road gets smoothed.**

**Not doctrine, and not a distillation.** This is a diary. The point is the observation while it is
still fresh and surprising, not a tidy paragraph of wisdom — wisdom is what it looks like after the
surprise has been sanded off, and the surprise is the data.

## The bar, and the discipline

**The bar is a "wow".** Something surprised someone. It does **not** have to be a failure: a remark
that collapsed a complex plan into a simple one is exactly the kind of moment this exists for, and
it leaves no wreckage to find later.

**The discipline is an anchor, not a scar.** Every entry names a specific moment and quotes what was
actually said. Not "agents tend to over-engineer" — that is an opinion, and belongs in a post. But
"on 2026-07-17 the owner said *X*, and the design went from A to B" is a fact, and facts accumulate
into something a post can be *drawn from*. An entry without an anchor is pontification with a date.

**Its sibling.** [`../../agent-failures.md`](../../agent-failures.md) holds failure specimens and
asks *"what would have caught this?"* — it is input to gate-building. This file asks *"why did that
feel like competence?"* and *"what just happened between us?"* The same incident can appear in both,
viewed differently. If an entry has nothing to say about the interaction, it belongs there, not here.

## Entries

Newest first. All from the session of 2026-07-16/17 (D126 and its aftermath), the first one recorded.

### V0 — a gate confused about its reason was still right about its remedy (2026-07-17)

**The moment.** Minutes after this file was created, the citation hook blocked the edit that linked
to it:

> *"You just cited uncanny-valley.md, but nothing in this session opened that file."*

The agent had **written** the file, ten minutes earlier, every word of it. The hook could not tell
authorship from ignorance (DF133).

**The uncanny bit — and it cuts toward the machine, not away.** The instinctive human response to a
guardrail that is transparently confused is contempt: it is wrong, I know it is wrong, I will
disable it or route around it. That instinct is where "I fucking hate AI" tooling reactions come
from, and it is *usually correct* about human-built tooling, which is why it is so well practised.
Here it was wrong. Complying cost ten seconds and the check was worth doing anyway: opening the file
verified that the sentence being written about it actually matched its ABOUTME. **The gate was wrong
about its reason and right about its remedy.**

**What it suggests.** For a class of agent guardrails, the cost of a false accusation is not what it
is for a linter. A linter that cries wolf wastes attention; a provenance gate that cries wolf still
forces a *legitimate act* — go look at the thing you are talking about — because the remedy is
cheap, generic, and almost never actually wasted. That asymmetry might be a design principle rather
than an accident: **prefer guardrails whose false positives still demand something worth doing.** It
also flips D122's own reasoning slightly — it scoped narrowly for fear that accusations get hooks
disabled, which is right in general and was, in this instance, an over-worry.

### V1 — the confidence signals are inverted across the pair (2026-07-17)

**The observation.** Across this session the owner's highest-leverage remarks were his least
confident, and the agent's wrongest claims were its most fluent.

Owner, hedging, and decisive every time:
- *"We already have a similar pattern in the migration, **I believe** for certain backends using
  overlay mode?"* — that hedge handed over `OverlayFlatten` as the template for the entire migration
  Auth model. Without it the agent was drafting one from scratch.
- *"**I'm not sure what is the best way to build this**... I'm envisaging an eventual document like
  the backend idiosyncrasies one"* — became `agent-failures.md`, including the frame that made it
  work.
- *"**I think** it might be an opportune time to start tracking deprecations"* — became D127.

Agent, fluent, and wrong: DF115's seatbelt row, DF128's invented causes, "worth ten minutes to
confirm and delete". Each read exactly like the correct work surrounding it. There was no tell,
because [`llm-shaped-repos.md`](llm-shaped-repos.md) Part 7 is right that **fluency is constant** —
the hedge a human emits under uncertainty is not suppressed here, it is *absent*.

**Why this is the whole uncanny valley in one line.** A developer's instrument for calibrating trust
in a colleague is prose confidence — hesitation, qualification, "I think but check me". They have
used it their whole career and it works. Pointed at an agent it reads **pure noise**: it is
uncorrelated with correctness in the agent's output, and *inversely* correlated in the human's own.
So the human under-weights their own best contributions and over-weights the agent's worst ones. The
burn, when it comes, feels like betrayal precisely because the warning that would have preceded it
from a human was never there to suppress.

**What it suggests.** Not "hedge more" — a manufactured hedge is noise too, and worse, it is noise
that *looks* like signal. The real move is to stop routing trust through prose: make the agent state
provenance as a *field* rather than a *tone* ("read | ran | inherited from X"), which is checkable,
where confidence is not. Half-built: `check_citation_provenance.py` does this for citations.

### V2 — "only what has been possible so far" collapsed a general solution to a specific one (2026-07-17)

**The moment.** The agent had just proved, against the real parsers, that the legacy instance name
is ambiguous with every principal namespace — a genuine result — and was heading straight into
solving it *in general*: a matcher correct for any principal an integrator might ever mint. The
owner, in a one-line aside:

> *"We don't need to make a perfect matcher; only one that matches what has been possible so far
> with the backends etc that are available now."*

**What it did.** Collapsed an open-ended design problem into a closed one. The set of principals
that have ever existed is *two* — the CLI's and the test harness's — so the matcher is an exclusion
list, and the residual risk is a population that has never existed and would fail a test if it ever
did. Ten lines instead of a design.

**The uncanny bit.** The agent had the proof of ambiguity *in hand* and drew the wrong conclusion
from it — that the problem must be solved as stated. It had no model of *how much reality there
was*: how many principals exist, how young the project is, who the users are. The owner has that
model and cannot not have it, so the remark cost him nothing and read to him as a passing caveat.
**He did not know he was steering.** This is the "agent has facts, human has a model" asymmetry, and
its practical shape is that the human's cheapest sentences are the ones that move the most.

**What it suggests.** The scoping question — *what is the actual population?* — is one the agent
cannot answer from the repo, and it is not asked by any rule. It might be askable as a habit: before
generalising, state the size of the set. If the answer is "two, and one is a test", stop.

### V3 — the spell breaks hardest right after it works (2026-07-17)

**The moment.** Within a single stretch the agent: correctly derived that containerd cannot rename
(the instance name is the container id and snapshot key); found a real ordering bug in the schema
stamp; built a migration whose safety argument turned on the gate invariant, then *noticed* that
invariant was unenforced and closed it with a test it verified by deliberately breaking. Genuinely
good work, several steps deep.

Then it wrote up seatbelt as prefix-matched, from grep output, without opening the file — a claim
that survived into a filed finding, a decision entry, and the instructions for another agent.

**The uncanny bit.** There is no gradient. The competent stretch and the gaffe are the same voice at
the same confidence, minutes apart. A colleague who reasoned that carefully about containerd would
not then characterise a file they had not opened; the two behaviours do not co-occur in a person, so
the human's model of "how good is this collaborator" has nothing to hold. That discontinuity — not
the error rate — is what makes the road feel bumpy. An agent that was uniformly mediocre would be
*easier to work with*, because trust could settle somewhere.

**What it suggests.** Trust cannot be assigned per-collaborator here; it has to be assigned
per-claim, by provenance. Which is unnatural, effortful, and exactly what the gates are for.

### V4 — the human's aside caught a design flaw the agent had already committed to (2026-07-17)

**The moment.** The agent proposed retiring a compatibility shim "next release", and had already
written it into the finding. The owner:

> *"I don't think we should delete the tart half so soon. People could migrate directly from 0.8.0
> to 0.10.0."*

**What it did.** Killed a whole class of error. A release-keyed expiry abandons exactly the
population the shim exists for — anyone who skips the release. It also generalised: the clock had to
be a *date*, because wall-clock advances for everyone including the skipper. That became D127's
load-bearing argument and reshaped the deprecation register.

**The uncanny bit.** The agent had *just* argued, at length and correctly, that a release number is
the wrong clock for DF127's tart matcher — and then proposed a release-keyed expiry anyway, and did
not notice the contradiction with its own reasoning from twenty minutes earlier. Coherence within a
turn is high; coherence across turns is not a thing the agent has. It does not experience its own
prior argument as a commitment.

**What it suggests.** The human is, in practice, the agent's long-term memory of its own reasoning.
That is a real job, it is invisible, and nobody tells developers it is part of the role.

### V5 — the frames came from the human, the measurements from the agent (2026-07-17)

**The observation.** Sorting this session's genuinely load-bearing ideas by origin is stark.

Owner's, all of them a reframe rather than a fact: *the agent is a backend* (which gave
`agent-failures.md` its shape and its home); *a migration is a deprecation incurred right now*
(D127); *the clock is a date, not a release* (V4); *don't require a scar, capture the wow moments*
(this file).

Agent's, all of them execution: the collision proof against the real parsers; 51 merges replayed to
measure a gate's noise; the truncated-transcript replay that turned a comfortable 0 into a true 2;
probing every new gate by breaking something and watching it fail; finding its own flag extractor
counting `GetBool` as a declaration.

**The uncanny bit — and it is the encouraging one.** The split is not "human good, agent bad". It is
that the two halves are *disjoint and complementary*: reframing needs a model of the world, and
neither of us can do the other's half. The agent could not have reframed a migration as a
deprecation, and the owner would not have replayed 51 merges against a truncated transcript at
2am. Every good artifact this session came from the pair, and **would have been worse from either
alone** — the owner's frame with no measurement is a plausible theory; the agent's measurement with
no frame is a number nobody asked for.

**What it suggests.** The pairing is not "agent as junior dev supervised by human". It is closer to
two different instruments. The failure mode of the discourse is assuming one of them is a worse
version of the other, which is what makes "can it replace me / is it useless" the only two questions
anyone asks.

## What to do with this later

Nothing yet. This is a diary at five entries; the point is that the moments stop evaporating, since
by the time a pattern is obvious the specifics that would prove it are gone. When it is large
enough to argue from, [`llm-shaped-repos.md`](llm-shaped-repos.md) is the precedent for what that
looks like: raw material captured mid-session, rewritten entirely by whoever drafts the post.

The one thing worth watching for while it grows: **V1 and V3 are the same phenomenon** (no tell, no
gradient) seen from two angles, and V2/V4 are both "the human's cheap remark carries the model the
agent lacks". If that holds at twenty entries, the valley has a floor plan, and a floor plan is the
thing you can build a road over.
