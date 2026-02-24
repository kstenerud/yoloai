# Critique — Round 10

Comprehensive pre-implementation audit. Focus: cross-document consistency, implementability by an agent, UX ergonomics, phase allocation, and implementation gotchas.

## Findings

### Cross-Document Consistency

- **C127.** DESIGN.md Full Copy Strategy (line 479) still says `cp -a` but PLAN.md Phase 4b step 8 was changed to `cp -rp` in Round 9 (C107). Same inconsistency in PLAN.md Phase 7 reset step 3 (line 240) which also still says `cp -a`. Both should say `cp -rp`.

- **C128.** OPEN_QUESTIONS.md #58 marks the changes indicator in `yoloai list` as **Deferred** with a performance concern ("running `git diff --stat` for every sandbox on every `list` is a performance risk"). But Round 9 (C115) added a CHANGES column using `git diff --quiet` (fast, short-circuits). #58 should be updated to reflect that this was resolved with a lightweight approach.

- **C129.** `--port` flag is listed in DESIGN.md `yoloai new` options (line 438) without a `[POST-MVP]` marker, but is absent from PLAN.md's MVP features list (line 11) and missing from Phase 4b's CLI wiring and container config steps. Either add `[POST-MVP]` to DESIGN.md or add `--port` to the MVP plan. Port forwarding is useful for dogfooding web dev scenarios (`--port 3000:3000`), and the implementation is trivial (Docker `HostConfig.PortBindings`).

- **C130.** PLAN.md Phase 1 `agent.go` — `Definition` struct lists `Name, InteractiveCmd, APIKeyEnvVars, StateDir, SubmitSequence, StartupDelay` but doesn't include `ModelAliases` (added in C114). The agent definition needs a `ModelAliases map[string]string` field for alias resolution.

- **C131.** `--network-none` was moved to MVP but PLAN.md Phase 1 meta.go says "simplified MVP meta.json (no network, no directories array, no ports/resources)". With `--network-none` in MVP, meta.json needs at least a `network_mode` field (e.g., `"none"` or `"default"`) so that `yoloai start` can reconstruct the container with the correct `NetworkMode`. Phase 7 `yoloai start` says "if container removed: recreate from meta.json" — without the network mode stored, it can't recreate correctly.

- **C132.** PLAN.md creation output examples (lines 162-178) don't show a `Network: none` line. DESIGN.md (line 585) says "Network lines omitted when using defaults." When `--network-none` is used, the network IS non-default, so the creation output should show `Network: none`. Add an example or note.

### Implementability

- **C133.** PLAN.md references `ArgsAfter`/`cmd.Flags().ArgsLenAtDash()` for `--` passthrough (line 160). `ArgsAfter` doesn't exist in Cobra. The correct approach: after Cobra parses, use `cmd.ArgsLenAtDash()` which returns the index of `--` in the positional args slice (-1 if absent). Slice the positional args at that index — everything before is normal positionals (name, workdir), everything after is agent passthrough args.

- **C134.** `--` passthrough and `--model` precedence — DESIGN.md says "First-class flags (`--model`) take precedence if duplicated." But yoloai can't control how the agent's CLI parser handles duplicate flags. If both `--model opus` (first-class) and `-- --model sonnet` (passthrough) are provided, the agent command becomes `claude --dangerously-skip-permissions --model opus --model sonnet` — behavior depends on Claude Code's arg parser. Simplest fix: document that duplicating first-class flags in passthrough args is undefined behavior. Don't try to strip or validate passthrough args.

- **C135.** CHANGES column detection — DESIGN.md and PLAN.md specify `git diff --quiet` for detecting unapplied changes. But `git diff --quiet` only detects modifications to tracked files vs the index. It will NOT detect **new untracked files** created by the agent (which are the most common type of agent change). The diff command already handles this by running `git add -A` first, but `yoloai list` shouldn't modify the index just to check for changes. Fix: use `git status --porcelain` instead — any output means changes exist. Fast, read-only, catches tracked changes + untracked files.

### Phase Allocation

- **C136.** Model alias resolution is not mentioned in any phase. Phase 1 creates the agent `Definition` struct, and Phase 4b builds the agent command. Alias resolution should be explicit in Phase 4b step 12 (when generating `agent_command` for `config.json`): look up `-m`/`--model` value in the agent definition's `ModelAliases` map; if found, substitute; if not, pass through as-is.

### Implementation Gotchas

- **C137.** `cp -rp` and external symlinks — when copying a project that contains symlinks pointing outside the copied tree (e.g., `node_modules/.bin/` → `../package/bin/`), those symlinks will be broken in the sandbox copy. This is expected behavior (same as `cp -a`), but the agent implementing this may be confused by broken symlinks in test scenarios. Worth a brief note in the plan.

- **C138.** `--` arg parsing with Cobra positionals — `yoloai new fix-bug . -- --max-turns 5` produces positional args `["fix-bug", ".", "--max-turns", "5"]` with `ArgsLenAtDash()` returning 2. The command must slice at that index: `name = args[0]`, `workdir = args[1]`, `passthroughArgs = args[2:]`. This works but is subtle — add a code comment explaining the slice boundary. If `--` is absent (`ArgsLenAtDash() == -1`), all positionals are name/workdir only.

## Applied

- **C127.** Fixed `cp -a` → `cp -rp` in DESIGN.md full copy strategy and PLAN.md Phase 7 reset step 3.
- **C128.** Updated OPEN_QUESTIONS.md #58 from Deferred to Resolved, reflecting the `git status --porcelain` approach.
- **C130.** Added `ModelAliases` field to agent `Definition` struct in PLAN.md Phase 1.
- **C131.** Added `NetworkMode` field to `Meta` struct description in PLAN.md Phase 1.
- **C132.** Added `Network: none` creation output example to PLAN.md Phase 4b.
- **C133.** Fixed `ArgsAfter` reference — replaced with correct `cmd.ArgsLenAtDash()` usage and slice explanation in PLAN.md Phase 4b CLI wiring.
- **C135.** Changed CHANGES detection from `git diff --quiet` to `git status --porcelain` in both DESIGN.md and PLAN.md.
- **C136.** Added explicit model alias resolution step (step 12) to Phase 4b creation workflow.
- **C129.** Added `--port` to MVP — features list, Phase 1 meta.json `Ports` field, Phase 4b container config and CLI wiring.
- **C134.** Documented passthrough arg ordering (built-in flags first, passthrough appended after) and duplicate first-class flags as undefined behavior in DESIGN.md, PLAN.md, and OPEN_QUESTIONS.md #86.

## Needs Input

(all resolved)

## Noted (acceptable for MVP)

- **C137.** Broken external symlinks in copies — expected behavior, brief note sufficient.
- **C138.** `--` arg parsing subtlety — implementable, just needs a clear code comment.
