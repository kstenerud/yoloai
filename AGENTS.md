# AGENTS.md

yoloAI runs AI coding CLI agents inside disposable sandboxes (containers, Tart VMs, macOS
Seatbelt) with a copy/diff/apply workflow. Go binary, no runtime deps beyond the backend.
**Public beta:** breaking changes are allowed, but must be recorded — rule 1.

This is the contract for changing yoloAI, and the canonical instruction file for every agent
working on it. `CLAUDE.md` imports it. Keep it short; detail lives behind the pointers.

**Docs last swept: 2026-07-15 (D116).** Docs drift silently — nothing executes prose. If that
date is more than ~3 months ago, say so, unprompted, before relying on a doc being right. Bump
it when a sweep lands. Why this exists: `general-principles.md` §16.

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

## Preparing a PR

Full detail and the reasoning behind each rule:
**`docs/contributors/procedures/pull-requests.md`**. Ordered by how likely you are to trip.

1. **A user-visible break needs a `docs/BREAKING-CHANGES.md` entry in the same PR** — under
   `## Unreleased`, never under a `## vX.Y.Z` heading (those are frozen once tagged). Renamed
   or removed flags and config keys, changed defaults, newly-rejected input. That file's
   preamble has the format and the trap. *This is the most-missed rule here.*
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

## The quality gate

`make check` must pass before every PR (Claude Code runs it via `.claude/settings.json`
hooks). **It is your local gate, not all of CI**, and it does not read prose — shipped help
text and `docs/` sit outside it. See the PR procedure for the CI jobs and required tooling.
