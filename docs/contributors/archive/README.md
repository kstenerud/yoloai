> **ABOUTME:** Index of the archive: what belongs here, what its contents are worth, and why
> nothing in it is maintained. The only live document under `archive/` — everything it indexes
> is frozen.

# Archive

**This is the only archive.** Everything retired lives here, under `docs/contributors/archive/`.
There is no second archive tree; there was one briefly (`design/archive/`, holding a single file)
and consolidating it away is why this paragraph exists.

## What the contents are worth

**Treat everything here as aged and possibly rotted.** Not wrong on purpose, but not checked
either: these documents were true when written, nobody has re-read them since, and the code they
describe has moved. Nothing here is a live reference. Do not resolve a question by citing an
archived doc — find the current answer and cite that.

What the archive *is* good for is archaeology, which is a real need and the reason to keep it:

- **Did we consider X?** — usually yes, and the plan that considered it is here.
- **Did we ever do X?** — the plan says what shipped and what was dropped.
- **Why was X decided that way?** — the reasoning, as it stood at the time. Cross-check the
  decision log (`../decisions/`) for whether it still holds.

## Frozen means frozen

Archived files are **not swept and not conformed**. Convention sweeps, ABOUTME headers, renames
and citation fixes stop at this boundary (`../standards/markdown.md` → Exempt). Two reasons: a
frozen doc reformatted to today's conventions implies someone vouched for its contents, which is
exactly the opposite of the warning above; and a sweep puts a fresh commit on prose no one re-read,
which makes aged material look current.

A file moves here **whole**, when its work is complete or abandoned, and stops changing. If an
archived doc turns out to matter again, the answer is a new live document that supersedes it, not
an edit here. This README is the exception — it is the index, so it stays current.

**One edit is carved out: the `> **ARCHIVED …**` banner every file here carries** (D128). It is
the single exception to "not swept", and it is the exception because it is the *inverse* of one:
the reason sweeps stop at this boundary is that conforming a frozen doc implies someone vouched
for it, and this banner says the opposite in the file's own first four lines. It is stamped once,
on arrival, and never revised — a `git mv` into `archive/` adds it, and nothing else here changes
again.

Why it exists rather than living only in this README: **an agent that greps arrives mid-file and
never passes through an index.** PR #44 is the worked example — an outside contributor's agent
found [`plans/apple-container-backend.md`](plans/apple-container-backend.md), read its
implementation table as a specification, and shipped a pre-D126 image name, a stale path to the
plan's own pre-archive location, and reasoning copied from a design that had since moved. Every
word of the warning above was already written and correct. None of it was on the path the agent
took. See [DF151](../design/findings-unresolved.md).

## Layout

| Subdir | What's here |
| --- | --- |
| `plans/` | Completed or superseded implementation plans. |
| `research/` | Completed/superseded research (mostly the layering epic's spikes). |
| `investigations/` | Point-in-time investigations and audits (a snapshot, not a living doc). |
| `design/` | Superseded design specs. |
| `old/` | The original pre-archive `old/` pile (initial PLAN + phase notes + early flag/devcontainer designs). |

## Finding things

There is no index of files here, deliberately. The one that used to sit in this README drifted
out of date four times without anyone noticing, and a partial index of an archive is worse than
none: ask "did we ever consider a microvm backend?", grep a list that has silently lost that
entry, and you get a confident **no** when the plan is sitting in `plans/`. The directory itself
cannot be incomplete.

- **What's here:** `ls` the subdir. Filenames are lowercase kebab-case and name their subject.
- **What it says:** open it. Most files here have no ABOUTME header, since frozen files are
  exempt from that convention; the ones that do carried it in from before they were archived.
  Either way the opening paragraph is the summary.
- **When and why it was archived:** `git log --diff-filter=A -- <path>` for the commit that moved
  it, whose message says what superseded it.
- **Whether the reasoning still holds:** it may not. Check `../decisions/` — that log is
  append-only and is the authority on what was decided and what superseded it.
