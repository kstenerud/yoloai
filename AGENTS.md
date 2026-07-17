# AGENTS.md

yoloAI runs AI coding CLI agents inside disposable sandboxes (containers, Tart VMs, macOS
Seatbelt) with a copy/diff/apply workflow. Go binary, no runtime deps beyond the backend.
**Public beta:** breaking changes are allowed, but must be recorded — rule 1.

This is the contract for changing yoloAI, and the canonical instruction file for every agent
working on it. `CLAUDE.md` imports it. Keep it short; detail lives behind the pointers.

**Docs last swept: 2026-07-15 (D116, D124).** Docs drift silently — nothing executes prose. If
that date is more than ~3 months ago, say so, unprompted, before relying on a doc being right.
Bump it when a sweep lands. Why this exists: `general-principles.md` §16.

**Deprecations reviewed: 2026-07-17 (D127) — nothing due; next 2026-09-12.** Compatibility code
is invisible once written, so it is never retired. `docs/contributors/deprecations.md` registers
each with the date it was incurred and a per-entry due date. If today is past the date above,
read the register and **say so, unprompted** — the owner decides what retires, but only if asked.
This is a nag, not a gate: nothing fails, ever. Bump the date when a review lands.

The marker is a claim, so treat it as one. When first written it named a sweep that had never
opened `architecture/`, which at that moment documented a mode retired two releases earlier and
a package that has never existed — the drift-detector's own first reading was wrong. D124 made
it true and gated the names, so `architecture/` now fails loudly rather than drifting quietly.
No other tier has that backstop.

## Where things are

Docs are organised by **role**. Pick your tier, then read that directory's `README.md`:

| Tier | Directory |
| --- | --- |
| Users — running yoloAI | `docs/` |
| Integrators — embedding it as a library | `docs/integrators/` |
| Contributors — changing it | `docs/contributors/` |

Two you will want by name: `architecture/where-to-change.md` maps a change onto the files
that make it. `backend-idiosyncrasies.md` catalogues backend behaviour that contradicts its
own docs — read it *before* diagnosing any backend problem; add to it when you find more.

`next-release.md` stages whatever ships next: it carries the next release version (a fact — it
escalates on its own when something breaking lands) and points at the work considered for the cut.
It is permanent and drains at each release, like `## Unreleased`. **It never carries an item's
status, and never its own reason** — an entry there is an ID and the record's own title, nothing
more. *"What's left for this release?"* and *"why is this in?"* are both answered by following its
links, never by trusting the page. The scope reason lives in the record, in a `- **Rides:**` field
naming the kind of release the fix needs (**any** / **breaking** / **a migration**) and which half
qualifies when only one does. Add that field to anything you propose for a release. It exists
because a reason composed on the staging page cannot be checked against anything, so a right one and
a wrong one read identically — and both duly happened, in opposite directions, on the same day.

`agent-failures.md` is its sibling for the component that writes the code. **The trigger is an
owner correction**: when the owner contradicts a claim you made, or asks a question whose answer
turns out to be "I was wrong", that is the event — record it there before moving on. Not every
fumble; only what reached a durable artifact or would have. A correction is a fact about a
repeating behaviour, and it is the only input from which the next gate gets built — the two
`scripts/check_*.py` gates exist because someone wrote the specimen down. On the evidence so far,
the owner's question is the most reliable detector in this repo, which is the problem the file
exists to fix.

## Preparing a PR

Full detail and the reasoning behind each rule:
**`docs/contributors/procedures/pull-requests.md`**. Ordered by how likely you are to trip.

1. **A user-visible break needs a `docs/BREAKING-CHANGES.md` entry in the same PR** — under
   `## Unreleased`, never under a `## vX.Y.Z` heading (those are frozen once tagged). Renamed
   or removed flags and config keys, changed defaults, newly-rejected input. That file's
   preamble has the format and the trap. *This is the most-missed rule here.* A **removed or
   renamed** config key or flag is now gated on the PR (`scripts/check_breaking_changes.py`);
   a changed default or newly-rejected input is not, and stays yours to notice. A break also
   **escalates the next release version** — bump the field in `next-release.md` when you land one.
2. **Invalidate a name, sweep every surface that names it.** Config keys, flags, and agent
   names are mirrored verbatim into shipped text that nothing typechecks — including
   `internal/cli/helpcmd/help/*.md`, which is `//go:embed`ed **shipped UI** despite living
   under `internal/`. `make check` catches none of this. Append-only history (BREAKING-CHANGES,
   `archive/`, `decisions/`, the `*-resolved`/`*-deferred`/`*-abandoned` sinks) is exempt from
   the sweep — but not from rule 1. Surface list: see the procedure.
3. **Code commits carry a prose body** saying what was wrong or missing and why this fixes
   it — not a restatement of the diff. `feat`/`fix`/`refactor`/`perf`. Near-universal here.
4. **Subject is `type(scope): summary`** — `feat`, `fix`, `docs`, `test`, `refactor`, `build`,
   `ci`, `chore`, `perf`. Imperative, no trailing period (0 of 2137 have one). Breaking gets
   `!` after the scope; never a `BREAKING CHANGE:` footer. Don't infer the format from
   `git log` — pre-June-2026 commits use a superseded convention.
5. **Attribute AI work by model:** `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.
   Never a bare `Claude`, never a `🤖 Generated with` footer. Humans and bots omit it.
6. **Cite the rationale ID** (`D<n>` decisions, `DF<n>` findings) wherever you write
   rationale. Never invent one; never strip one. No decision behind the change? Cite nothing.
   Allocating a new one? Run **`scripts/next-id.sh D`** / **`scripts/next-id.sh DF`**; it prints
   the next free number. Don't grep for the highest — every duplicate ID this repo has had came
   from a grep that missed a sink.
7. **File the defects you don't fix** in `design/findings-unresolved.md`. Fixing in scope is
   fine *if you record it*; silently working around it never is.
8. **An idea worth building is a plan file** in `design/plans/` — not a bullet in a README, which
   is where a backlog goes to be invisible. Each carries a metadata list under its title:
   `- **Status:**` (`UNSPECIFIED` no design yet / `PLANNED` designed / `IN-PROGRESS` partly built /
   `IMPLEMENTED` / `ABANDONED`) and `- **Depends on:**` (live plan filenames, or `—`). A one-commit
   fix needs no plan. `design/plans/` holds only the unfinished three: once the work is done or
   dropped the plan is archaeology, and it moves whole to `archive/plans/` in the same PR. Gated,
   so none of it is something to remember.
9. **Writing a migration? Register it** in `docs/contributors/deprecations.md` with the date you
   incurred it (D127). A migration *is* a deprecation: it commits the project to supporting the
   old form, on the day it lands. Same for any compatibility reader, alias, or shim. The entry
   costs three lines and is the only thing that will ever make retiring it possible — the
   register's own audit found 16 such mechanisms, of which **0** recorded a date.

## The quality gate

`make check` must pass before every PR (Claude Code runs it via `.claude/settings.json`
hooks). **It is your local gate, not all of CI**, and it does not read prose — shipped help
text and `docs/` sit outside it. See the PR procedure for the CI jobs and required tooling.
