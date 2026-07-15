# Contributors

Docs for changing yoloAI itself. If you are *running* yoloAI, start at [`../GUIDE.md`](../GUIDE.md);
if you are *embedding* it, see [`../integrators/`](../integrators/).

**Start with [`AGENTS.md`](../../AGENTS.md)** — the contract for changing this project. Then
come back here to route.

## Where to go

| You want to | Read |
| --- | --- |
| Open a PR | [`procedures/pull-requests.md`](procedures/pull-requests.md) |
| File or triage an issue | [`procedures/issues.md`](procedures/issues.md) |
| Find the files a change touches | [`architecture/where-to-change.md`](architecture/where-to-change.md) |
| Understand the package layout | [`architecture/README.md`](architecture/README.md) |
| Diagnose a backend problem | [`backend-idiosyncrasies.md`](backend-idiosyncrasies.md) — **read before diagnosing** |
| Know *why* something is the way it is | [`principles/README.md`](principles/README.md), [`decisions/README.md`](decisions/README.md) |
| Know *what* and *how* for a technology | [`standards/README.md`](standards/README.md) |
| Shape a feature before building it | [`design/README.md`](design/README.md) |

## The directories

- **`architecture/`** — how the code is put together. `code-map.md` (packages, key types,
  command→code map), `data-flows.md` (runtime call chains), `host-layout.md` (`~/.yoloai/`),
  `where-to-change.md` (change recipes), `testing.md` (test tiers), `overview.md` (concepts).
  Keep in sync when the architecture changes.
- **`principles/`** — **why**. Cite the relevant section when you make a non-obvious choice.
  A principle wins over any conflicting standard.
- **`standards/`** — **what** and **how**, per technology: `go.md`, `cli.md`, `shell.md`,
  `python.md`, `makefile.md`, `dockerfile.md`, `markdown.md`.
- **`decisions/`** — append-only, D-numbered. `working-notes.md` is the live log (D45 onward);
  `working-notes-archive.md` holds D1–D44. Non-trivial decisions land here first; principles
  and standards cite them by number.
- **`design/`** — the shaping cluster: feature specs, `design/plans/` (designed but not yet
  built), `design/research/`, and the review queues.
- **`procedures/`** — how we work: PRs, issues.
- **`backend-idiosyncrasies.md`** — backend behaviour that contradicts its own documentation.
  Has a symptom index. Add an entry when you find a new one.
- **`archive/`** — completed or abandoned work, frozen. Kept for archaeology ("did we consider
  X?", "why was X decided that way?"), never as a live reference: treat its contents as aged and
  possibly rotted, and don't sweep or conform them. It is the *only* archive.

## Doc conventions

Every directory's `README.md` is its index. Filenames are lowercase kebab-case and name the
subject, not its status.

Three content-retirement patterns:

- **Item queues** keep active items in `<topic>-unresolved.md` and drain each to one of three
  co-located sinks: `<topic>-resolved.md` (done), `<topic>-deferred.md` (parked; carries a
  **`Trigger:`** stating what revives it, and can flow back), or `<topic>-abandoned.md`
  (won't do; carries a **`Why:`**). Used for critiques, questions, and findings.
- **Append-only logs** (`decisions/`) grow and age-split.
- **File-documents** (plans, specs, research) move whole to `archive/` when complete.

## How we work

- **Research before design changes.** When a design question comes up ("should we use
  overlayfs?"), research it in `design/research/` with verified facts first, then update the
  design from the findings.
- **Critique cycle.** Write a critique in `design/critiques-unresolved.md`, apply the
  corrections, then drain the item to `-resolved`, `-deferred` (add a `Trigger:`), or
  `-abandoned` (add a `Why:`). Findings found mid-work follow the same flow via
  `findings-unresolved.md`; open questions via `questions-unresolved.md`.
- **Factual accuracy matters.** Star counts, feature claims, and security assertions must be
  verified. Don't repeat marketing language or unverifiable numbers.
- **Cross-platform awareness.** Always consider Linux, macOS (Docker Desktop + VirtioFS), and
  Windows/WSL. Note platform-specific tradeoffs explicitly.
- **Record new idiosyncrasies** in `backend-idiosyncrasies.md` before committing the fix, with
  a row in the symptom index. Keep entries concise: symptom, explanation, fix, code pointer.
- **Commit granularity.** One commit per logical change. Research, design updates, and
  critique application are separate commits.
