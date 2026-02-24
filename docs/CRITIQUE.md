# Critique — Round 12

Post-Round-11 audit. Focus: incomplete fixes from Round 11, remaining stale references, example consistency.

## Findings

### Incomplete Fixes from Round 11

- **C149.** DESIGN.md destroy smart confirmation (line 643) still says "detected via `git diff` on `:copy` directories." C146 fixed this in PLAN.md (line 247) but missed the same text in DESIGN.md. Should be `git status --porcelain` — consistent with PLAN.md and DESIGN.md's own `yoloai list` CHANGES column (line 671).

- **C150.** C145 was marked as applied ("Added `Ports:` line to creation output examples in both PLAN.md and DESIGN.md") but only the omission *rule* was updated — no actual creation output example with a `Ports:` line was added to either document. Add an example (analogous to the `Network: none` example) showing `Ports:` in creation output when `--port` is used.

- **C151.** PLAN.md creation output rule (line 182) says "Profile and network lines omitted when using defaults" but doesn't mention ports. DESIGN.md (line 586) correctly says "Profile, network, and ports lines are omitted when using defaults." PLAN.md should match.

### Stale References

- **C152.** PLAN.md deferred features list (line 13) still includes `tail` at the end. `yoloai tail` was renamed to `yoloai log` (which is in MVP, line 9). Remove `tail` from the deferred list.

### Example Consistency

- **C153.** CLI-STANDARD.md argument ordering examples (lines 15-16) show `yoloai new ... my-sandbox ./src ./lib` with two positional directories after the name. The current `yoloai new` syntax is `<name> [<workdir>]` — `./lib` would be an unrecognized extra positional. Same issue in the help text Examples section (line 167). Update both to use single workdir (e.g., `my-sandbox ./src`).

## Applied

- **C149.** Fixed DESIGN.md destroy smart confirmation from `git diff` to `git status --porcelain` (missed in C146).
- **C150.** Added `Ports:` creation output example to both DESIGN.md and PLAN.md.
- **C151.** Updated PLAN.md creation output rule to mention ports (matching DESIGN.md).
- **C152.** Removed stale `tail` from PLAN.md deferred features list.
- **C153.** Updated CLI-STANDARD.md argument ordering and help text examples to use single workdir.

## Needs Input

(none — all fixable autonomously)

## Noted (acceptable)

(none)
