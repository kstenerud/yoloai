# Critique — Round 8

Pre-implementation audit. Focus: contradictions between DESIGN.md and PLAN.md, gaps that would block dogfooding, stale references from previous critique rounds, and implementation risks.

## Applied

- **C82.** DESIGN.md and PLAN.md creation output — removed stale `yoloai tail` reference (removed from MVP in C80). Now suggests `yoloai attach` and `yoloai diff` only.
- **C83.** PLAN.md Phase 3 `BuildBaseImage` — changed from "creates build context tar from `resources/`" to seed `~/.yoloai/` from embedded resources first, then build from `~/.yoloai/`. Added `SeedResources` function.
- **C84.** PLAN.md Phase 4 — added explicit `EnsureSetup(ctx)` first-run auto-setup: create `~/.yoloai/` structure, seed Dockerfile/entrypoint, build base image if missing, write default config.yaml, print completion instructions.
- **C85.** PLAN.md Phase 7 `yoloai start` — added the "container running but agent exited → relaunch agent in existing tmux session" case.
- **C86.** PLAN.md Phase 6 diff — removed the no-op `':!.git'` exclusion note. `git diff` never includes `.git/` internals by definition.
- **C87.** PLAN.md Phase 4 credential cleanup — changed from "Clean up temp key file" immediately to "Wait for container entrypoint to read secrets (poll for agent process start with 5s timeout), then clean up temp key file."
- **C88.** PLAN.md Phase 4 — added `--prompt -` (stdin) to flag parsing and step 11.
- **C89.** PLAN.md Phase 6 apply — added note to use `git apply --unsafe-paths --directory=<path>` for non-git original dirs.
- **C90.** PLAN.md Phase 3 entrypoint step 8 — changed from "Wait for tmux session to end" to `exec tmux wait-for yoloai-exit` (blocks indefinitely; container only stops on explicit `docker stop`).
- **C91.** PLAN.md Phase 4 — added `--agent` flag to `yoloai new` (validate: only `claude` for MVP, error on anything else).
- **C92.** PLAN.md Phase 7 `yoloai reset` — expanded from one sentence to 8 enumerated steps covering stop, delete, re-copy, baseline, meta.json update, clean option, start, and prompt re-send.
- **C93.** Updated Node.js from 20 LTS to 22 LTS across DESIGN.md, PLAN.md, RESEARCH.md, and OPEN_QUESTIONS.md. Node 20 EOL is April 2026; Node 22 LTS has maintenance until April 2027. Claude Code's `engines` field (`>=18.0.0`) confirms compatibility.
- **C94.** DESIGN.md and PLAN.md — made dirty repo warning skippable with `--yes` on `yoloai new`. Added `--yes` to `yoloai new` options in DESIGN.md.
- **C95.** PLAN.md `yoloai show` — omit profile line for MVP (profiles deferred), noted to add back when implemented.
- **C96.** CODING-STANDARD.md project structure — added "(post-MVP)" note to `internal/config/` entry.
- **C97.** PLAN.md Architecture Decisions — added note to use Cobra's `CountP` (not `BoolP`) for `--verbose` to preserve the stacking contract from CLI-STANDARD.md.
- **C98.** PLAN.md `yoloai list` — omit PROFILE column for MVP (profiles deferred), noted to add back when implemented.

## Deferred

(none)
