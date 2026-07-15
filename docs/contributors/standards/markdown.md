> **ABOUTME:** Prose conventions for yoloAI's Markdown: what an ABOUTME header is and where it
> goes, heading and table style, cross-references, and why docs/ is never auto-formatted. The
> authority for how a doc in this repo should read.

# Markdown Standard

Reference for prose documentation in yoloAI: `README.md`, `CONTRIBUTING.md`, `docs/**`. Excludes Markdown produced as program output (help text, JSON schemas).

See also: `../principles/general-principles.md §11` (default to public — docs are written to be read); `../principles/development-principles.md §1` (Principle of Least Astonishment — prose conventions reduce surprise).

## ABOUTME header (source files)

Every yoloAI source file — Go, Python, shell, or Markdown under `docs/contributors/` — opens with an ABOUTME block saying what the file is for and why it exists. It goes at the very top: before package declarations, module docstrings, or the document title.

**In Go, Python and shell**, every line carries the language's comment marker and repeats the prefix:

```go
// ABOUTME: One-line description of what this file is for.
// ABOUTME: Continue here if needed.
```

**In Markdown**, the block is a blockquote and the prefix appears once:

```markdown
> **ABOUTME:** One-line description of what this file is for.
> Continue here if needed.

# Document Title
```

The two forms differ because the constraints do, and the difference is not cosmetic. A comment marker is invisible when rendered, so repeating `ABOUTME:` on every line costs nothing and keeps each line independently greppable. Markdown has no comment syntax and it *joins* consecutive lines into a single paragraph — so the same per-line prefix renders as a run-on with `ABOUTME:` repeated mid-sentence. The blockquote marks the block once, lets the prose flow, and renders as a set-apart callout in every renderer rather than only on GitHub.

The header stays **visible** on purpose. It is written for humans as much as for agents, and it is the answer to "what is this file for?" that a reader wants before the title, not a machine annotation to be hidden. An HTML comment (`<!-- ABOUTME: ... -->`) renders as nothing and is therefore **not** the form, despite having been half of a 38/38 split in the tree before this rule existed.

A package godoc or module docstring does not substitute — see below.

**Each line wraps at 100 columns**, comment marker included. This is enforced by `TestRepoHygiene_ABOUTMEHeaders_AllTrackedFilesCompliant`, so it cannot drift. The limit was 80 until D117 and was stated only inside the example above rather than as a rule — 351 lines had drifted past it, which is what an unenforced number in an example block gets you. 100 is what the code already does: exactly four lines exceeded it, and they were reflowed rather than grandfathered.

Say something true and specific. "Tests for foo.go" restates the filename and is worse than nothing — it is documentation that will be trusted.

**Say what the file is for. Don't inventory what it contains** (D121; DEV §1's DRY corollary). An ABOUTME that copies facts the file already states is a second authoritative location for them, and nothing keeps the two in step:

- **Never state a count.** `general-principles.md` opened with "Thirteen principles" while sixteen `## §n` headings sat below it, and `repo_hygiene_test.go` claimed "three standing claims" with four gates defined. A count carries no information the artifact doesn't, and the next addition makes it false.
- **Avoid exhaustive lists.** They carry names, which is worth something, but they imply completeness and the next addition falsifies them in silence. Several of the principles docs name fewer sections than they have. This bullet used to say "three of the five" — there are seven, so the example arguing against counts had itself rotted.
- **Characterize instead.** Say the file's purpose and role; leave the contents to the headings, which are the authoritative location and are greppable. The ABOUTMEs in `principles/` that cannot drift are exactly the ones that describe rather than enumerate.

The test: if adding a section to the file would oblige you to edit its ABOUTME, the ABOUTME is a copy and will be wrong the first time someone forgets. The 100-column rule above is the counter-example that proves the shape of the fix — it is a number that survives *because* a gate enforces it. Ungated numbers rot.

### Required

- All Go files (`*.go`, including `*_test.go`), wherever they live. Test files are not exempt: the doc used to exempt them against the evidence of a codebase that already mostly carried the header, and D117 closed the gap. This is enforced by `TestRepoHygiene_ABOUTMEHeaders_AllTrackedFilesCompliant`, so compliance is total by construction and no count belongs here — the previous "171 of 257 (~two-thirds)" was falsified the moment the remaining files were filled in, and stood as its own argument for D121.
- All Python files in `runtime/monitor/`, including test files. The test files under `runtime/monitor/tests/` already carried ABOUTME headers despite the (former) exemption below.
- All shell scripts under `scripts/`.
- All Markdown source documents under `docs/contributors/` (principles, standards, research, plans, working-notes, ARCHITECTURE, etc.).

A package godoc or module docstring is **not** a substitute — the two coexist, ABOUTME first. Many Go files carry both, and `yoerrors/errors.go` and the `runtime/monitor/*.py` scripts that once relied on their docstring now do too. A substitution carve-out was briefly written down and removed (D117): it contradicted the code, and "the godoc must actually describe the purpose" is a judgement no gate can make, so it would have meant an allowlist — which is grandfathering by another name.

### Exempt

- **Shell scripts whose purpose is part of the project's runtime contract** rather than a standalone utility: `.claude/hooks/*.sh` (Claude Code hook signaling), `runtime/docker/resources/entrypoint.sh` (container entrypoint trampoline). Their role is documented in adjacent code or design docs.
- **User-facing Markdown** at the top level of `docs/` (the GUIDE, BREAKING-CHANGES, ROADMAP, README). These are content destinations, not source context.
- **The contents of `docs/contributors/archive/`.** The archive is frozen: it is kept for archaeology, is not maintained, and its reliability is to be treated as aged and possibly rotted. Conforming a frozen document to a current convention implies someone vouched for it, which is the opposite of what the archive says about itself — and it would put a fresh timestamp on prose nobody re-read. Sweeps stop at the boundary. Its own `README.md` is the exception: an index is live navigation, so it carries a header and stays current. See `../archive/README.md`.
- **Files generated by tooling** (`vendor/`, build output). Not committed in this project, but if such files appear they don't need headers.

### When in doubt

If the file is hand-edited source code and lives alongside other source code, write the ABOUTME header — test files included. If it's user-facing reference content, you can skip. If a class of file isn't covered here, propose a D-entry rather than improvising.

## Metadata list (plans)

A document with structured facts about itself carries them as a bolded list directly under its
title, one field per line. Plans in `design/plans/` are the case that exists today:

```markdown
> **ABOUTME:** What this plan would build, and why.

# Plan title

- **Status:** PLANNED — designed 2026-07-01; the long pole of Phase 1a.
- **Depends on:** store-workload-split.md
```

The list is greppable per field (`grep '^- \*\*Status:\*\*' *.md`), renders as a list rather than
as noise, and extends without changing shape. It is deliberately not YAML front-matter: that
renders as junk in a plain Markdown view, which is the same mistake as hiding the ABOUTME in an
HTML comment.

**The ABOUTME and the metadata list divide cleanly, and must.** The ABOUTME says what the document
is *for* — prose, stable for the document's life. The list says where it *stands* — structured,
changing. Status in an ABOUTME is a second copy of a fact the list owns two lines below it, and
that copy is what goes stale: plans here read "Planned, not started" above their own Progress
section recording four completed phases with commit hashes.

**Only add a field that answers a question someone asks, and prefer one a gate can check.**
`Status` answers "what can I build / what's underway / what haven't we fleshed out", and a gate
enforces the vocabulary. `Depends on` answers "what can I start now", and a gate resolves each
name to a live plan, so it cannot rot and archiving a plan something still needs fails loudly.
`Effort` and `Layer` were proposed and left out: no gate can check either, so both would age in
silence the way every ungated number in this repo has (D121). A field nobody reads is worse than
no field, because the next reader trusts it.

**A relation is declared in one direction only.** "A depends on B" is a single fact, and it lives
in A. There is deliberately no `Unblocks:` field — it would store that same edge a second time, on
B, pointing the other way, and the two can then disagree with nothing to catch it. Direction is not
arbitrary either: a plan's author knows what their plan needs, so `Depends on` is local knowledge;
`Unblocks` would mean editing B whenever some later A starts needing it, by someone who may never
open B. The reverse view is a grep, and grep cannot be out of date:

```
grep -l 'Depends on:.*session-carve.md' docs/contributors/design/plans/*.md   # what B unblocks
```

## Headings

- **ATX-style** (`#`, `##`, `###`) — never Setext-style (`===`, `---` underlines). ATX is unambiguous at all heading levels.
- **`#` is the document title** — exactly one per file. Levels go up to `####` (rare); deeper nesting usually means the section needs splitting.
- **Title Case for H1**, sentence case for H2 and below. Example: this doc's H1 is "Markdown Standard" (title case); sub-headings are "Headings", "Tables", etc. (sentence case unless a proper noun appears).
- **Blank line before and after every heading.**

## Tables

Tables are pipe-aligned when the column count is small (≤4) and the content fits on one screen width:

```markdown
| Source                     | Date | Type                  |
| -------------------------- | ---- | --------------------- |
| Kent Beck — YAGNI          | 1999 | Primary, named author |
| Martin Fowler "Yagni"      | 2015 | Primary, named author |
```

Long-column tables (where alignment fights readability) can drop the alignment as long as the table parses. Markdown linters tolerate either form.

The convention is *readability over rigour* — if pipe-alignment forces a column to wrap awkwardly, drop the alignment.

## Lists

- **Hyphen `-` for unordered lists.** Not `*` or `+`.
- **Numbered lists** with `1. 2. 3.` literal numbers (not `1. 1. 1.`). Renumber when the list changes.
- **Nested lists** indent by 2 spaces (Markdown's spec is forgiving but most renderers expect 2 or 4; we pick 2 for compactness).

## Code blocks

- **Fenced (triple-backtick)** code blocks always carry a language tag:

  ```go
  func Example() error { return nil }
  ```

  Not:

  ```
  func Example() error { return nil }
  ```

  The language tag enables syntax highlighting and signals intent to the reader.

- **Inline code** (`single backticks`) for: file paths, function names, command names, flag names, type names. Anywhere that copy/paste would benefit from monospace.

## Emphasis and strong

- `*italic*` for emphasis (Markdown's `*` is the convention; `_` works but is less consistent across renderers).
- `**strong**` for stronger emphasis.
- Don't use emphasis to substitute for headings or lists.
- **Bold opening word** for "principle / pattern / decision" blocks is the project convention (visible across all four principles docs).

## Line length and wrap

yoloAI does NOT enforce a strict line-length cap on Markdown. The prose pattern is "one logical sentence per line" *sometimes* and "wrap to ~80 columns" *other times* depending on the writer's preference and the visual flow. Both styles appear in the existing docs; both are accepted.

Why no enforcement: Markdown line-breaks render the same whether you wrap at 80 or 120. Forcing one style fights every author's editor settings; the readability benefit isn't worth the friction.

What IS enforced:
- **Don't introduce hard line breaks inside a sentence** unless they're part of intentional formatting (e.g., poetry-line, table cell). Hard line breaks break Markdown's paragraph joining.
- **Use a blank line to separate paragraphs**, not multiple spaces.

## Cross-references

- **Relative paths from the current file.** A doc at `docs/contributors/principles/foo.md` linking to `docs/contributors/decisions/working-notes.md` uses `../decisions/working-notes.md`, not the absolute path. Relative links survive when files move.
- **For files outside the current subtree**, use the path relative to repo root for clarity (e.g., from `docs/contributors/design/research/principles/x.md` referencing `runtime/registry/descriptor.go`, write `runtime/registry/descriptor.go`, not `../../../../runtime/registry/descriptor.go`).
- **External links**: standard Markdown `[text](url)` form. URLs should be permalinks (commit SHA, dated archive link) for sources that can change.

## Frontmatter

yoloAI Markdown files do NOT use YAML frontmatter (`---` blocks at the top). Hugo / Jekyll / mdBook conventions are out of scope; the docs are read in a plain Markdown renderer (GitHub, IDE plugins). Anything that would go in frontmatter goes in prose or in the ABOUTME header.

## Tone

- Direct and concrete. Prefer "yoloAI rejects this" over "it is recommended that yoloAI not."
- Specifics over generalities. "Commit `b75e2ec` validates sandbox names" beats "name validation is implemented."
- No marketing language. "Differentiator" appears where it's literal (copy/diff/apply); not as filler.
- No emoji unless explicitly requested by the user.
- Cite primary sources. The four principles docs each pair with a research file under `research/principles/` that holds the source trail.

## Filename conventions

- **All-caps** for top-level entry points: `README.md`, `CLAUDE.md`, `CONTRIBUTING.md`, `LICENSE`, `BREAKING-CHANGES.md`.
- **All-caps + `-`** for top-level reference docs in `docs/`: `docs/GUIDE.md`, `docs/ROADMAP.md`.
- **All-caps for legacy / formal docs in `docs/contributors/`**: `ARCHITECTURE.md`, `OVERVIEW.md`, `RESEARCH.md`, `CRITIQUE.md`, `OPEN_QUESTIONS.md`, `SENTIMENT.md`. These predate the principles/standards split.
- **Lowercase-kebab** for design docs and research: `commands.md`, `network-isolation.md`, `competitors.md`, `idle-detection.md`. Newer convention.
- **kebab-case** for D-numbered entries indirectly (none yet; D-entries live in `working-notes.md` as sections, not separate files).

When in doubt for a new doc: kebab-case. The all-caps convention is for top-level entry points only.

## What NOT to do

- **Don't auto-format Markdown** with prettier / markdownlint --fix in CI. Prose intent is fragile under aggressive formatters. The hadolint / golangci-lint discipline does not apply here.
- **Don't auto-generate prose docs from code**. The ARCHITECTURE.md and design docs are hand-written; they encode reasoning that code can't express.
- **Don't link to external sources for first-class claims**. If a security claim depends on a vendor doc, copy the relevant quote into the research file with a date; vendor docs change.
- **Don't mix two file-naming conventions in one directory**. New `docs/contributors/design/` files are kebab-case; new `docs/contributors/` standards / principles files follow the convention they live next to.

## Cross-references

- `../principles/general-principles.md §7` (factual accuracy) — prose claims that drive decisions must be verifiable.
- `../principles/general-principles.md §11` (default to public) — docs are written to be read by users and contributors.
- `../principles/development-principles.md §1` (Principle of Least Astonishment) — conventions reduce reader surprise.
- The four principles docs themselves (`../principles/*.md`) are worked examples of this standard in use.
