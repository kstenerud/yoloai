# Critique

Architecture review — February 2026. Assessed implementation against design docs, coding standards, and roadmap.

## What's Working Well

- Copy/diff/apply workflow backed by git is well-implemented and clean.
- Safety layer is thorough: dangerous dir checks, path overlap detection, dirty repo warnings, read-only defaults.
- Docker Client interface enables testability; compile-time satisfaction check is good practice.
- Package boundaries (agent/sandbox/docker/cli) are clear at the top level.
- Coding standards are being followed consistently.
- Test coverage is solid (4,375 lines of test code, 18 test files).
- The MVP delivers what the design promised — no major gaps between spec and implementation.

## Findings

### 1. `sandbox` package concentration (Medium)

**Observation:** `internal/sandbox/` is 7,400 lines in one package. It owns creation, lifecycle, diff, apply, setup, config, paths, safety, inspection, confirmation, and error types. The file-per-concern split helps readability, but everything shares one package namespace — all internals are mutually accessible, and change impact is hard to reason about.

**Evidence:** The package-level functions `GeneratePatch`, `CheckPatch`, `ApplyPatch`, `GenerateDiff`, etc. don't use Manager at all. They operate on sandbox names and paths. This is a natural extraction boundary that's already visible in the code.

**Recommendation:** Split along the seams that already exist:

- `internal/sandbox/` — Manager, create, lifecycle, meta, state types (orchestration)
- `internal/workspace/` — diff, apply, patch generation, git baseline, format-patch (copy/diff/apply engine)
- `internal/setup/` — first-run setup, config.yaml manipulation, tmux configuration

This would also make roadmap features land more cleanly: overlayfs belongs in workspace, profiles touch setup, network isolation touches sandbox.

**When:** Before overlay or profiles implementation begins.

### 2. `create.go` mixes creation logic with shared utilities (Low-Medium)

**Observation:** `create.go` (920 lines) holds the creation workflow alongside git helpers (`newGitCmd`, `runGitCmd`, `gitBaseline`, `gitHeadSHA`), file operations (`copyDir`, `removeGitDirs`, `expandTilde`), JSON helpers, secrets handling, mount building, prompt reading, port parsing, and seed file logic. The git helpers in particular are used by diff.go, apply.go, and lifecycle.go — they're shared infrastructure that happens to live in the creation file.

**Recommendation:** Extract shared helpers into dedicated files regardless of whether the package splits:

- `git.go` — `newGitCmd`, `runGitCmd`, `gitHeadSHA`, `gitBaseline`, `stageUntracked`
- `util.go` or similar — `copyDir`, `removeGitDirs`, `expandTilde`, `readJSONMap`, `writeJSONMap`

This reduces `create.go` to the creation workflow itself and makes the shared code discoverable.

**When:** Anytime. Easy refactor.

### 3. Docker subprocess calls bypass the SDK (Low)

**Observation:** `waitForTmux` and `attachToSandbox` in `cli/commands.go` shell out to `docker exec`/`docker inspect` via `os/exec`. The coding standard says: "Use `github.com/docker/docker/client` (official SDK), not subprocess calls to `docker` CLI."

`attachToSandbox` is a legitimate exception — `docker exec -it` with full TTY passthrough is genuinely difficult via the SDK. But `waitForTmux` could use `ContainerExecCreate`/`ContainerExecAttach` (already on the Client interface) and `ContainerInspect` (also on the interface).

**Recommendation:** Document the TTY-attach exception explicitly. Convert `waitForTmux` to use the SDK. The subprocess calls can't be mocked via the Client interface, which hurts testability.

**When:** When touching those code paths.

### 4. Agent definitions need a user-facing format before the second agent ships (Low now, High later)

**Observation:** The `agents` map in `agent.go` is a Go map literal. Adding Codex or Aider means editing Go source and recompiling. The roadmap lists "community-requested agents (Aider, Goose, etc.)" — at some point users will want to define custom agents without rebuilding the binary.

The current design doesn't specify a user-facing agent definition format. Profile `profile.yaml` could carry agent overrides, but there's no schema for it.

**Recommendation:** Plan the agent definition schema (likely YAML in `~/.yoloai/agents/` or embedded in profiles) before the second agent ships, so the format isn't shaped by Claude's specific needs alone. The Definition struct fields are a good starting point for the schema.

**When:** Before Codex ships. The schema design should consider all planned agents.

### 5. Overlayfs will stress the current `sandbox` package structure (Medium)

**Observation:** Overlayfs replaces `copyDir` with mount setup, requires different cleanup on destroy/reset, changes how the git baseline works (original `.git/` is read-only lower layer), and needs capability detection with fallback logic. This cuts across create, lifecycle, and diff — all within the already-large sandbox package.

The current `create.go` flow is 920 lines and will grow with overlay code paths.

**Recommendation:** The workspace extraction from finding 1 would cleanly separate "how to set up a work copy" (full copy vs overlay) from "how to manage a container." A `workspace.Strategy` interface with `FullCopy` and `Overlay` implementations would keep the creation flow readable and the strategy logic isolated.

**When:** Decide before overlay implementation starts. The package split and the overlay work can be done together or sequentially, but the split should be designed first.

### 6. `meta.json` has no schema versioning (Low)

**Observation:** `Meta` is a flat struct with no version field. Aux directories (roadmap) will need `AuxDirs []WorkdirMeta` or similar — a schema change. Existing sandboxes won't have this field.

Go's JSON unmarshaling handles missing fields gracefully (zero values), so this works today. But as the schema evolves across beta, explicitly tracking the version would make migration logic cleaner.

**Recommendation:** Add a `Version int` field to Meta. Existing meta.json files without it unmarshal to `Version: 0`, which is a valid sentinel for "MVP-era schema." Worth adding to OPEN_QUESTIONS for discussion.

**When:** When aux dirs or other schema changes ship.

### 7. Viper may not be needed (Low)

**Observation:** The design calls for Viper post-MVP for config file + env var + flag precedence binding. But the current approach — raw YAML manipulation with `updateConfigFields` preserving comments — is simpler and more predictable. Viper pulls ~15 transitive dependencies.

Profiles will have their own `profile.yaml`. The main thing Viper gives is precedence binding (env > config > flag), which could be built with a thin layer over `go-yaml` + Cobra flags. The design already notes this as a fallback.

**Recommendation:** Make the thin-layer approach the primary plan rather than the fallback. The comment-preserving YAML manipulation already works and is a feature Viper doesn't offer. Only adopt Viper if the precedence binding logic becomes genuinely complex.

**When:** Decision point before config features expand.

### 8. `ErrSetupPreview` overloads error return for flow control (Low)

**Observation:** In `cli/commands.go:153`, `Create` can return `ErrSetupPreview` which the CLI treats as a clean exit (`return nil`). This means `Create` has a success path that returns an error — overloading the error channel for flow control.

The user-cancelled path already returns `("", nil)` cleanly. The preview path should do the same.

**Recommendation:** Either return a `CreateResult` struct with a `Preview bool` field, or handle the preview path inside `Create` and return `("", nil)` like the cancellation path.

**When:** Anytime. Cosmetic fix.

## Summary Table

| # | Finding | Severity | When to Address |
|---|---------|----------|-----------------|
| 1 | `sandbox` package too large | Medium | Before overlay or profiles |
| 2 | `create.go` mixes workflow with shared helpers | Low-Medium | Anytime |
| 3 | Docker subprocess calls bypass SDK | Low | When touching those paths |
| 4 | Agent definitions need user-facing format | Low now, High later | Before Codex ships |
| 5 | Overlayfs will stress current structure | Medium | Before overlay starts |
| 6 | `meta.json` has no schema versioning | Low | When schema changes ship |
| 7 | Viper may not be needed | Low | Before config expansion |
| 8 | `ErrSetupPreview` flow control via error | Low | Anytime |

## Verdict

The architecture is in good shape for what it does today. No critical issues. The main structural concern is that the `sandbox` package concentration will become a bottleneck as overlay, profiles, and network isolation land. Splitting it along the workspace/setup seams before those features arrive is the highest-value structural change. The other findings are minor and can be addressed opportunistically.
