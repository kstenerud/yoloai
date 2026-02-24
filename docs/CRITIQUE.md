# Critique — Round 9

Comprehensive pre-implementation audit. Focus areas: cross-document consistency, implementability by an agent, UX ergonomics across scenarios, technology choices, architecture, phase allocation, and implementation gotchas.

## Applied

- **C99.** DESIGN.md Container Startup step 7 — changed from "should poll for the agent's ready indicator" to "MVP uses a configurable fixed delay; polling is a future improvement."
- **C100.** DESIGN.md Container Startup step 1 — marked context file generation as `[POST-MVP]`.
- **C101.** OPEN_QUESTIONS.md #82 — replaced stale `yoloai tail` reference with `yoloai attach` + `yoloai diff`.
- **C102.** PLAN.md Phase 4 Create() — reordered steps: validation (name, workdir exists, API key, prompt flag exclusivity) now precedes safety checks (dangerous dir, path overlap). Safety checks require valid, existing paths.
- **C103.** PLAN.md Architecture Decisions — added explicit note that config.yaml is written for user reference but MVP does not read it.
- **C105.** DESIGN.md `yoloai new` options and PLAN.md — added mutual exclusivity note for `--prompt` and `--prompt-file`.
- **C107.** PLAN.md Phase 4 step 8 — changed `cp -a` to `cp -rp` (POSIX-portable; `-a` is GNU-specific and unavailable on macOS).
- **C108.** PLAN.md Phase 4 step 13 — specified `os.CreateTemp` with `0600` permissions. Noted SIGKILL tradeoff.
- **C112.** DESIGN.md creation output and PLAN.md creation output — added `(Ctrl-b d to detach)` hint to attach suggestions.
- **C118.** PLAN.md Phase 4 Create() — added decomposition note: "decompose into helper methods (`prepareSandboxState`, `createContainer`, `deliverPrompt`) called sequentially."
- **C119.** PLAN.md Phase 5 `yoloai show` — specified status detection ordering: check Docker container state FIRST, only query tmux if container is running.
- **C122.** PLAN.md Phase 6 apply — clarified `git apply` invocation for git vs non-git original dirs, added note to test edge case early.
- **C123.** DESIGN.md and PLAN.md entrypoint — clarified exit code 12 handling: check if UID already matches (no-op), otherwise warn and continue.
- **C125.** PLAN.md Phase 3 verify — added `--init` / `tmux wait-for` interaction verification step.
- **C104.** `--replace` — inherits destroy's smart confirmation (prompts on running agent or unapplied changes). `--yes` skips.
- **C106.** DESIGN.md — added `[POST-MVP]` markers throughout for deferred features (Codex, profiles, overlay, network isolation, recipes, etc.).
- **C111.** Added `-p` short flag for `--prompt`, `-f` for `--prompt-file` in DESIGN.md and PLAN.md.
- **C113.** Workdir stays explicit (`.` required) — firm decision. Added strong "do not revisit" note to OPEN_QUESTIONS.md #42.
- **C114.** Added `-m` short flag for `--model`. Added built-in model aliases to agent definitions (e.g., `sonnet` → `claude-sonnet-4-latest`). User-configurable aliases + version pinning marked `[POST-MVP]`.
- **C115.** Added CHANGES column to `yoloai list` — detected via `git diff --quiet` on host-side work directory. Shows `yes`/`no`/`-`.
- **C117.** Docker SDK — pin to latest `github.com/docker/docker` v28.x. The `+incompatible` suffix is expected (Go modules artifact). SDK auto-negotiates API version with older engines.
- **C120.** Split Phase 4 into 4a (infrastructure: errors, safety, manager, EnsureSetup with verify step) and 4b (Create workflow + CLI wiring).
- **C121.** Integration test stays in Phase 8 — it depends on diff/apply/destroy from Phases 5-7. Phase 4b's verify step (docker ps) is sufficient for creation.
- **C126.** Moved `--network-none` from POST-MVP to MVP. Trivial implementation (`NetworkMode: "none"`), useful for testing sandbox setup without API calls.

## Needs Input

(none)

## Noted (acceptable for MVP)

- **C109.** EnsureSetup idempotency — works correctly by design but not explicitly called out.
- **C110.** No concurrent access protection — acceptable for single-user dogfooding.
- **C116.** No issues with core technology choices.
- **C124.** Unbounded log growth — acceptable for short-lived sandboxes.

## Deferred

(none)
