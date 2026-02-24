# Critique — Round 11

Comprehensive pre-implementation audit after extensions design addition. Focus: cross-document consistency, stale references, extension design correctness, MVP plan completeness.

## Findings

### Cross-Document Consistency

- **C139.** PLAN.md Risks section (line 332) still says "Large copies — `cp -a` with `node_modules` is slow." Should be `cp -rp` — this was the last remaining `cp -a` reference missed in Round 10.

- **C140.** DESIGN.md command table (line 329) shows `yoloai x <extension> [options] [name] [workdir]` with `[name]` as optional, but the detailed section specifies `<name>` as always required (first positional, built-in). The command table should use `<name>` to match.

- **C141.** DESIGN.md extensions `iterate.yaml` example (lines 824-836) is logically broken. It runs `yoloai destroy --yes "${existing}"` first, then tries `yoloai show "${existing}" --json | jq -r .workdir` — but the sandbox metadata was already deleted by destroy. Fix: capture the workdir before destroying.

- **C142.** The `iterate.yaml` example uses `yoloai show "${existing}" --json`, but `--json` is not documented on `yoloai show` — only `yoloai list` has `--json`. Either add `--json` to `yoloai show` or use a different approach (e.g., read meta.json directly via `jq`).

- **C143.** CLI-STANDARD.md help text example (line 152) shows `yoloai new [flags] <name> <dir> [<dir>...]` — outdated. Should be `yoloai new [flags] <name> [<workdir>]` since aux dirs (`-d`) are `[POST-MVP]`.

- **C144.** PLAN.md context line (line 5) says "OPEN_QUESTIONS.md #1–85" but questions now go through #90. Should say "#1–90".

- **C145.** PLAN.md Phase 4b creation output examples don't include a `Ports:` line for when `--port` is used. Ports are non-default (similar to `Network: none`) and should be shown in creation output when specified. DESIGN.md creation output section also lacks this.

- **C146.** PLAN.md Phase 7 destroy (line 247) says "check via `git diff` on `:copy` dirs" for smart confirmation, but CHANGES detection was updated to `git status --porcelain` in Round 10. Destroy's unapplied change detection should also use `git status --porcelain` for consistency (catches untracked files too).

- **C147.** DESIGN.md extensions validation section mentions name collision with built-in commands, but doesn't address flag name collision with yoloai's global flags (`--verbose`, `-v`, `--yes`, `-y`, `--quiet`, `-q`, `--no-color`). Since extensions use their own arg parsing (not Cobra), there's no technical collision — but it's confusing UX if `yoloai x lint my-lint . --verbose` is ambiguous. Worth a note.

### Stale References

- **C148.** OPEN_QUESTIONS.md #51 still references "`yoloai tail` and `yoloai list`" — `tail` was removed. Should reference `yoloai log` instead.

## Applied

- **C139.** Fixed last `cp -a` → `cp -rp` in PLAN.md Risks section.
- **C140.** Changed `[name]` → `<name>` in DESIGN.md command table for `yoloai x`.
- **C141.** Fixed `iterate.yaml` example to read meta.json before destroying the sandbox.
- **C142.** Replaced `yoloai show --json` with direct `jq` read of meta.json (avoids undocumented flag).
- **C143.** Updated CLI-STANDARD.md help text example to `<name> [<workdir>]`.
- **C144.** Updated PLAN.md context line from #1–85 to #1–90.
- **C145.** Added `Ports:` line to creation output examples in both PLAN.md and DESIGN.md.
- **C146.** Changed destroy smart confirmation from `git diff` to `git status --porcelain` in PLAN.md.
- **C147.** Added flag collision note to DESIGN.md extensions validation section.
- **C148.** Fixed stale `yoloai tail` → `yoloai log` in OPEN_QUESTIONS.md #51.

## Needs Input

(none)

## Noted (acceptable)

(none)
