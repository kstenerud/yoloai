<!-- ABOUTME: Phased implementation plan for the CLI/orchestration/backend layering refactor -->
<!-- ABOUTME: defined in docs/contributors/design/layering.md. W-L workstreams, independent, with acceptance criteria. -->

# Layering Refactor — Implementation Plan

Phased plan to implement the architecture defined in [`docs/contributors/design/layering.md`](../../design/layering.md). Backed by the [layering leak audit](../research/layering-leak-audit.md) and [comparator research](../research/layering-comparators.md).

**Scope.** Multi-quarter program. Phase 1 is ~1 week. Phase 3 (the `yoloai.Client` refactor) is the bulk of the work — likely 6–10 weeks spread across multiple releases, depending on how much parallel feature work is allowed against the moving boundary.

**Sizing legend.** XS = under a day · S = 1–3 days · M = 1–2 weeks · L = 2+ weeks.

**Workstream naming.** Prefixed `W-L` (Layering) to distinguish from `W` workstreams in [architecture-remediation.md](architecture-remediation.md). The two plans are independent and can interleave.

---

## Phase ordering

| Phase | Items | Rationale |
|---|---|---|
| **1 — Cleanup & naming** | W-L1, W-L2, W-L9 | Low-risk, ship-independent. Removes the false-generic `system runtime` name, moves misplaced files, closes the `--security` doc bug story. |
| **2 — Capability scaffolding** | W-L3, W-L4, W-L5, W-L6, W-L7 | Grow `BackendDescriptor` and optional interfaces so generic code can stop name-checking backends. Each workstream removes a specific leak from the audit. |
| **3 — Orchestration boundary** | W-L8 (a–e) | The structural refactor: `yoloai.Client` becomes the CLI's spine. The largest and highest-impact phase. Each sub-step is shippable. |
| **4 — Enforcement** | W-L10 | A test or linter rule preventing regression. Without enforcement, layering erodes under schedule pressure. |
| **5 — Greenfield consolidation** | W-L12, W-L13, W-L14 | Optional, post-W-L8e. Package-path migration + CLI directory reorg + Tart concurrent-VM detection. Mechanical but disruptive; defer until layering is stable. |

Phases 1 and 2 are largely independent — can be parallelized. Phase 3 depends on Phase 2 (the descriptor extensions are consumed by `yoloai.Client`). Phase 4 lands once Phase 3 is far enough along that the forbidden import set is stable.

---

## Guiding principles

- **Every workstream is independently shippable.** No multi-PR landings; each W-L workstream goes in as one PR (or a small series with each piece passing CI on its own).
- **No new leaks during the refactor.** Reviewers reject PRs that add backend-name string checks or concrete-backend imports in disallowed locations, even if the leak is "temporary until the next workstream."
- **Refactor in place of rewrite.** `yoloai.Client` grows to absorb CLI orchestration; the CLI shrinks to consume it. No third package, no greenfield.
- **`make check` passes after every commit.** Per project convention.
- **Add tests for moved logic before deleting old paths.** Especially for the `yoloai.Client` migration — the CLI's existing tests cover orchestration today; tests must follow the logic.

---

## Discovered-findings policy

Same convention as architecture-remediation.md: mid-workstream discoveries that weren't in [the audit](../research/layering-leak-audit.md) go in `docs/contributors/design/unresolved-findings.md`. Critical (correctness/security/regression) escalates; everything else parks. Don't expand a workstream's scope.

---

## Workstreams

### W-L1 — Move `EmbeddedTmuxConf` out of `runtime/docker`; clean up leftover audit doc/string leaks

`sandbox/setup.go` imports `runtime/docker` to call `EmbeddedTmuxConf()`. The tmux config isn't Docker-specific — every backend that runs tmux uses it. Move to a neutral package. Bundle in two minor user-facing leaks from the audit (L9, L25) — same character as the `--security` doc bug, low risk to land together.

**Steps:**

1. **EmbeddedTmuxConf relocation (L27):**
   - Create `internal/resources/tmux/` (or `sandbox/tmuxconf/`; pick whichever fits the package taxonomy better). Move the embedded file and `EmbeddedTmuxConf()` there.
   - Update `sandbox/setup.go:307,423` to import the new location.
   - Update `runtime/docker/`: remove the embed and the function.
2. **L9 — Replace "overlay sandbox" with ":overlay sandbox" in `internal/cli/diff.go:172,175`.** The leading colon matches the documented user-facing mount-mode vocabulary (see L10 for the canonical form).
3. **L25 — Fix `internal/cli/help/workdirs.md:14,27`.** Replace "Docker only" / "not available with seatbelt or tart" with accurate container-backend phrasing: "container backends only (docker, podman, containerd)".
4. Run `make check`.

**Acceptance:**
- `sandbox/` does not import `runtime/docker`. Verify by `grep -r "runtime/docker" sandbox/` returning no matches.
- `internal/cli/diff.go` error messages use `:overlay sandbox` (with leading colon) where they refer to the documented mount mode.
- `internal/cli/help/workdirs.md` accurately reflects all container backends supporting `:overlay`.
- `make check` passes.
- All backends still get a working tmux config (smoke test on docker + at least one other backend if available).

**Size:** XS · **Risk:** low · **Blocks:** nothing · **Addresses:** Audit L9, L25, L27.

---

### W-L2 — Rename `yoloai system runtime` → `yoloai system tart`; collapse Tart-side duplication

Today's `system runtime` command tree is structurally Tart-only but reads as generic. Rename to make the scope explicit (Pattern B; "podman machine" model). Bundle in two natural-companion cleanups from the audit (L20, L21) — they live in the same file and share the same Tart scope.

**Steps:**

1. Decide whether to keep a deprecation alias for one release. **Recommended:** yes — `yoloai system runtime ...` continues to work but emits a deprecation warning that points to `yoloai system tart ...`. Removed in next breaking-changes window.
2. Move `internal/cli/system_runtime.go` → `internal/cli/system_tart.go`. Rename Cobra command from `runtime` to `tart` under the `system` group.
3. Wire the old `runtime` name as a hidden alias with deprecation warning. Use Cobra's `Aliases` field; emit warning in `PersistentPreRunE`.
4. Update help text and any documentation references (`GUIDE.md`, `commands.md`, embedded help).
5. **L20 — Collapse the 3× Tart availability check.** Extract a single `requireTartBackend(ctx)` helper (or use a single `PersistentPreRunE` on the tart command group). Replace the three duplicated checks at `system_runtime.go:64,162,273` (now `system_tart.go`).
6. **L21 — Route the `tart` binary call through `runtime/tart`.** Replace the `exec.CommandContext(ctx, "tart", ...)` in `runTartCommand` (`system_runtime.go:390-395`) with a typed function in `runtime/tart` (e.g., `tart.ListBaseVMs(ctx)`). Once the command tree is explicitly Tart-scoped (post-rename), calling into `runtime/tart` is honest — no abstraction theatre needed.
7. Add entry to `docs/BREAKING-CHANGES.md` for the deprecation.
8. Run `make check`.

**Acceptance:**
- `yoloai system tart ...` works for all current subcommands (create, list, delete, base list, etc.).
- `yoloai system runtime ...` works but emits a deprecation warning to stderr.
- Help text under `yoloai system` lists `tart` as the canonical name.
- The Tart availability check is invoked from one place (helper or `PersistentPreRunE`); no three-times-duplicated `checkBackend(ctx, "tart")` block remains.
- `system_tart.go` calls `runtime/tart` typed functions; no `exec.Command("tart", ...)` shelling from the CLI layer.
- `BREAKING-CHANGES.md` documents the rename and the deprecation timeline.

**Size:** S · **Risk:** low (user-visible — coordinate with release notes) · **Blocks:** nothing · **Addresses:** Audit L19, L20, L21; OPEN_QUESTIONS Q1; [design.md D1](../../design/layering.md#7-decisions).

---

### W-L3 — Drive backend metadata from `runtime.Registered()` (collapse three registries)

`info.go` has `knownBackends`. `sandbox/setup.go` has `availableBackends`. `bugreport_writer.go` has a per-backend version-query table. These are three independent registries that already diverge (containerd missing from setup.go).

**Steps:**

1. Extend `BackendDescriptor` with the fields needed to drive the existing tables: `Description`, `Platforms []string`, `Requires string`, `Notes []string`, `Pros []string`, `Cons []string` (or whatever shape `knownBackends` uses today; mirror it).
2. Populate the descriptor in each backend's package (`runtime/docker/`, `runtime/podman/`, `runtime/tart/`, `runtime/seatbelt/`, `runtime/containerd/`).
3. Rewrite `info.go`'s rendering loop to iterate `runtime.Registered()`.
4. Rewrite `sandbox/setup.go:availableBackends` to do the same.
5. Add `VersionString(ctx) (string, error)` to `BackendDescriptor` (or as an optional `VersionReporter` interface — pick based on whether every backend can implement it). Each backend returns its version string.
6. Rewrite `bugreport_writer.go:120-129` to iterate the registry.
7. Delete the three parallel tables.
8. Run `make check`.

**Acceptance:**
- `grep -n "knownBackends\|availableBackends" internal/ sandbox/` returns no matches.
- Adding a new backend (test by adding a stub) automatically appears in `yoloai info`, the setup wizard, and bug reports without further code changes.
- `make check` passes; smoke tests on at least docker + one other backend pass.

**Size:** M · **Risk:** medium (touches setup wizard and bug report — both user-visible) · **Blocks:** nothing strictly, but Phase 3 expects the descriptor surface to exist · **Addresses:** Audit L8, L16, L26; OPEN_QUESTIONS Q5.

---

### W-L4 — `Probe()` capability replaces inline backend detection

Replace `helpers.go:dockerAvailable()` (hard-coded `/var/run/docker.sock`) and the `podmanrt.SocketExists()` named import with a uniform `Probe()` method on `BackendDescriptor`.

**Steps:**

1. Add `Probe(ctx context.Context) (available bool, reason string)` to `BackendDescriptor`. Define `reason` as a short user-facing string when unavailable (e.g., "docker daemon not running", "podman socket not found").
2. Implement `Probe()` in each backend package. For docker: socket existence + Docker SDK ping. For podman: existing `SocketExists()` logic. For tart: `tart --version` execution check. For seatbelt: always-available on darwin. For containerd: socket check.
3. Update `internal/cli/helpers.go::detectContainerBackend` to iterate registered backends with matching capability flags (e.g., `desc.Caps.LinuxContainer` or similar) and call `Probe()`. Remove `dockerAvailable()` and the `podmanrt` named import.
4. Remove the duplicated routing in `yoloai.go` (delete `resolveBackendFromConfig`'s fallback literal; have it call `helpers.go`'s function, or move both into a shared helper consumed by Client and CLI).
5. Run `make check`.

**Acceptance:**
- `grep -rn "podmanrt\|dockerrt" internal/cli/ yoloai.go` returns no matches (except the registration blank imports).
- `internal/cli/helpers.go` imports no concrete backend package.
- Probing logic centralizes one call per backend; adding a new backend requires implementing `Probe()`, not modifying CLI.
- `make check` passes.

**Size:** S · **Risk:** low · **Blocks:** nothing · **Addresses:** Audit L2, L3, L4; OPEN_QUESTIONS Q2.

---

### W-L5 — Descriptor-driven `CleanupHint`, `HostFromContainerHostname`

Three audit findings have the same fix shape: a CLI string literal that should be a backend-aware lookup.

**Steps:**

1. Add `CleanupHint(image string) string` to `BackendDescriptor` (returns empty if no cleanup applicable — e.g., Tart/Seatbelt). Docker/Podman return the appropriate `rmi` command.
2. Add `HostFromContainerHostname() string` (returns `host.docker.internal` for docker/podman, empty for tart/seatbelt — caller decides whether to substitute generic phrasing).
3. Update `internal/cli/profile.go:697` to call `desc.CleanupHint(name)` for the active backend.
4. Update `internal/cli/help.go:174` to either render the active backend's hostname dynamically, or use generic phrasing if `HostFromContainerHostname() == ""`. (See [design.md §5.3](../../design/layering.md#53-runtime-layer-capabilities-and-probes-pattern-a).)
5. Update `sandbox/create_prepare.go:347` to do the same.
6. Run `make check`.

**Acceptance:**
- A Podman user invoking `yoloai profile delete` sees `podman rmi yoloai-<name>`, not `docker rmi`.
- Tart/Seatbelt users see no cleanup hint (the `image` doesn't exist for them).
- Help text for local models no longer hard-codes `host.docker.internal`.

**Size:** S · **Risk:** low · **Blocks:** nothing · **Addresses:** Audit L14, L15, L30.

---

### W-L6 — Isolation capability functions

Two audit findings (L7, L29) check isolation-mode strings in places that should use a helper. Centralize in `runtime/isolation.go`.

**Steps:**

1. Add `SupportsOverlayDirs(isolation string) bool` to `runtime/isolation.go`. Returns `false` for `container-enhanced` (gVisor); `true` for `container`, `container-privileged`; consider VM modes case-by-case.
2. Add `IsolationAvailability(mode, hostOS string) (available bool, reason string, helpLink string)` to the same file. Encodes the rules currently in `validateIsolationOSCombo` (CLI) and the gVisor-on-macOS rule.
3. Rewrite `internal/cli/new.go::validateIsolationOSCombo` to use the new function, eliminating the 3× repeated error blocks.
4. Rewrite `sandbox/create_instance.go:126-131` to use `SupportsOverlayDirs(isolation)` instead of the string check.
5. Run `make check`.

**Acceptance:**
- `grep -n '"container-enhanced"' sandbox/ internal/cli/` returns only `runtime/isolation.go` and tests.
- `validateIsolationOSCombo`'s body shrinks to a single lookup + error format.
- All existing isolation-mode validation tests pass.

**Size:** S · **Risk:** low · **Blocks:** nothing · **Addresses:** Audit L6, L7, L29; OPEN_QUESTIONS Q3.

---

### W-L7 — `AppleSimulatorRuntimes` optional interface

Move the `*tart.Runtime` type assertion out of `sandbox/create.go`. Use an optional interface so `sandbox/` stays backend-agnostic.

**Steps:**

1. Define `AppleSimulatorRuntimes` interface in `runtime/` (or wherever optional capability interfaces live):
   ```go
   type AppleSimulatorRuntimes interface {
       ConfigureSimulatorRuntimes(ctx context.Context, opts SimulatorRuntimeOpts) error
   }
   ```
   (Name and signature TBD based on what `sandbox/create.go` actually needs.)
2. Implement the interface in `runtime/tart/` by extracting the relevant code from `sandbox/create.go:541-575`.
3. Rewrite `sandbox/create.go` to use `if asr, ok := m.runtime.(AppleSimulatorRuntimes); ok { ... }` instead of the type assertion. Remove the `runtime/tart` import.
4. Move the error message ("--runtime flag only supported on tart backend") to a generic phrasing keyed off the optional-interface absence.
5. Run `make check`.

**Acceptance:**
- `grep -rn "runtime/tart" sandbox/` returns no matches.
- The `--runtime` flag works exactly as before on Tart; produces the same error on non-Tart backends.

**Size:** S · **Risk:** low · **Blocks:** nothing · **Addresses:** Audit L28; [design.md D8](../../design/layering.md#7-decisions-for-the-user).

---

### W-L8 — `yoloai.Client` becomes the CLI's spine (Pattern C)

The structural refactor. CLI commands consume `yoloai.Client` instead of building orchestration directly. Done in five sub-workstreams, each shippable independently.

#### W-L8a — Surface design: map every CLI command to a Client method

**Steps:**

1. Catalog every public CLI command (run, new, attach, diff, apply, destroy, list, inspect, exec, reset, baseline, profile, config, info, bugreport, start, stop, restart). **Also include `wait`** — a new Client method (`Client.Wait(ctx, name, opts) (exitCode int, err error)`) that blocks until the agent exits, returning its exit code. The corresponding `yoloai wait <name>` CLI command lands in W-L8b. See [layering.md §9.2](../../design/layering.md#92-yoloai-wait-q77).

**Considerations to surface in the design review** (not blockers):
- A `yoloai.SandboxNameFromEnv() string` helper as a public-package convenience for embedded consumers that want `YOLOAI_SANDBOX` env-var semantics (D9 — currently the CLI handles env-var fallback in `internal/cli/envname.go`, but a future MCP/HTTP/library consumer would have to re-implement it).
2. For each: identify the method it should call on `yoloai.Client`, the `<Op>Options` struct shape, and the return type.
3. Note gaps: methods or options that don't exist today (audit found at least: overlay diff/apply, format-patch apply, selective apply, attach with TTY).
4. Write the API surface as a Go file (`yoloai/api_surface.go`) or as a section in `yoloai.go` doc comments. Submit for review before implementation begins. **This is a design review checkpoint** — do not start W-L8b until the surface is approved.

**Acceptance:**
- Every CLI command has a designated Client method and `<Op>Options` struct.
- Streaming/interactive operations have explicit stream-arg parameters in their signatures (per kubectl lesson — don't try to hide TTY).
- Reviewer (project lead) approves the surface.

**Size:** S · **Risk:** low · **Blocks:** W-L8b–e.

#### W-L8b — Add Client methods (move orchestration into `yoloai.Client`)

This sub-workstream adds Client methods *only* — it moves orchestration logic from CLI command files into the Client, but **the CLI continues to call the old direct paths until W-L8c/d migrates each command**. Splitting the "add method" step from the "migrate CLI" step avoids the trap of mixing two concerns in one PR.

W-L8a (the design checkpoint, 25 Q-resolutions in `api_surface.go`) added significant scope beyond "implement each missing method." The Q-resolution-derived work below ships interleaved with the method PRs — each Q has its own implementation footprint and its own BREAKING-CHANGES.md entry where applicable.

**Steps:**

1. Implement each Client method specified in `api_surface.go`. Move the orchestration logic from the CLI command into the Client method. The CLI command, for now, can either (a) keep calling its old direct path unchanged, or (b) call the new Client method as a pass-through if that's trivial — but the formal CLI migration is W-L8c/d's job.
2. Move tests as the logic moves: orchestration tests (currently in `internal/cli/*_test.go` where they test orchestration rather than presentation) get parallel coverage in `yoloai/` tests.
3. Ship each method as a separate PR. Each PR: one new Client method + its tests. No `internal/cli/` rewrites in this phase.

**Q-resolution scope folded into W-L8b** (in addition to "implement each method"):

- **Q-W — DataDir threading + no-ambient-config.** Every `config.*` and `store.*` path helper takes DataDir; single `os.UserHomeDir()` call site in CLI startup (with `--data-dir` flag override); all tests construct Client with `t.TempDir()` DataDir. *Largest single sub-task.*
- **Q-T — per-sandbox file locks.** Every write method on `*Sandbox` / sub-handles acquires the per-sandbox lock at public entry; extend (or compose) the existing `acquireMultiLock` rather than building a parallel model.
- **Q-Z — stale-lock UX.** Lock-contention path detects holder PID, surfaces `*SandboxLockedError` with recovery instructions, exposes `Sandbox.Unlock` (and `yoloai sandbox <name> unlock` CLI) for manual force-clear. See the Q-Z resolution in `api_surface.go`.
- **Q-J / Q-K / Q-O / Q-Y — type / name discipline.** Force renames to concrete names; Backend / Agent strings → `BackendName` / `AgentName`; Mounts / Ports → `[]MountSpec` / `[]PortMapping`; `Info` refactor + `SandboxMeta` type alias; `Workdir.Diff` returns `(string, error)`; `ApplyResult` flat.
- **Q-L — Prune CLI flags.** Remove `--cache` / `--all` / `--backend`; add `--include-base-image`. BREAKING-CHANGES entry.
- **Q-U — remove aux `:copy` / `:overlay`.** Delete `GenerateMultiPatch`, `apply_overlay.go` multi-dir iteration, multi-dir tests. `parse.go` rejects `:copy` / `:overlay` on `-d` with a usage error directing to alternatives. BREAKING-CHANGES entry.
- **Q-V — `Network.Allowed` provenance.** Compute `AllowedDomain.Source` (agent-requirement vs user) at read time from the bound agent's `NetworkAllowlist`.
- **Q-X — drop `UnrecoverableNotImplemented` and `UnrecoverableInternal`.** Programming-bug sites panic + recover at CLI root; existing "unknown agent" classifies as `state_corrupted`.
- **Q-P — remove `ErrNoChanges` from existing code.** Apply returns the result; the no-changes state is `Status == ApplyStatusEmpty`.
- **Q-S — Wait return type.** `(exitCode int, err error)` → `(Status, error)`. Add `Info.AgentExitCode *int` for raw exit code surfacing.

**Acceptance per method:**
- The method exists on `yoloai.Client` and is exported.
- Orchestration logic that previously lived in the CLI command file is now reachable from (or has moved into) the Client method.
- Test coverage equals or exceeds the pre-refactor coverage for that orchestration logic.
- The CLI continues to work; user-visible behavior unchanged (except for the explicitly documented breaking changes above).
- `make check` passes.

**Cross-cutting acceptance:**
- All `os.UserHomeDir()` / `os.Getenv()` / `os.Getwd()` calls in library code removed; single allowlisted CLI call site documented in `internal/cli/root.go` (or wherever `main` runs).
- Every BREAKING-CHANGES entry (Q-L, Q-U, Q-W's CLI rename for `config get/set/reset` shape if any) lands with the PR that ships the change.
- The W-L10 enforcement linter (Phase 4) covers the new bans.

**Size:** L (one method per PR plus the cross-cutting Q-resolution sub-tasks; ~20–25 PRs total) · **Risk:** medium (parallel feature work — coordinate via a moratorium on adding new direct-orchestration logic to CLI commands during this phase) · **Blocks:** W-L8c.

#### W-L8c — Migrate first CLI command (proof of concept)

The Client methods now exist (from W-L8b). W-L8c is the **first CLI-callsite migration**: rewrite one low-risk command (`list`, `inspect`, or `info` — pick at implementation time) to call only the Client method, removing direct `sandbox/` and `runtime/` imports from that command's file. The point is to establish the conventions on a small target before mass migration.

**Steps:**

1. Refactor the chosen CLI file to call only `yoloai.Client.<Method>` for orchestration. Remove direct `sandbox/` or `runtime/` imports from that file (the chokepoint in `helpers.go` still imports `runtime/` for backend registration — that's expected).
2. Establish the conventions for subsequent migrations: how options are built, how typed errors map to exit codes, how output is formatted (text vs JSON), where `IOStreams` lives. Document these in `internal/cli/CONVENTIONS.md` (new file) for W-L8d to follow.
3. Run smoke tests on multiple backends.

**Acceptance:**
- The chosen command's CLI file does not import `sandbox` or any concrete `runtime/<backend>` package (except `helpers.go`'s registration chokepoint).
- `internal/cli/CONVENTIONS.md` exists with documented conventions.
- Smoke tests pass on the available backends.

**Size:** S · **Risk:** low · **Blocks:** W-L8d.

#### W-L8d — Migrate remaining generic commands

Convert all other generic CLI commands following the conventions from W-L8c. Backend-scoped commands (`yoloai system tart ...`) are exempt.

**Steps:**

1. One command per PR. Each PR: refactor CLI file, ensure orchestration tests follow, run smoke tests.
2. Land in dependency order: simple commands first (list, inspect, info, config), then mid-complexity (start, stop, destroy, reset), then the heavy ones (new/run, attach, diff, apply with all its variants).
3. As each command is migrated, the corresponding `internal/sandbox` or `runtime` imports in its CLI file should disappear.

**Acceptance per command:**
- The CLI file does not import `internal/sandbox` or `internal/runtime` (registration chokepoint and backend-scoped command files are the only exceptions).
- All existing tests pass.
- `make check` passes.

**Size:** L · **Risk:** medium · **Blocks:** W-L8e, W-L10.

#### W-L8e — Remove redundancies; finalize boundary

Once all generic CLI commands consume `yoloai.Client`, the old direct paths can be deleted. The Client becomes the unique entry point for orchestration.

**Steps:**

1. Delete any orchestration logic in `internal/cli/` that has been fully migrated to `yoloai.Client`.
2. Update `docs/contributors/architecture/README.md` to reflect the new layering.
3. Update `yoloai.go` doc comments to reflect that the Client is the CLI's spine (still internal-grade per [design.md §6](../../design/layering.md#6-public-api-stabilitydecoupled)).
4. Run `make check`.

**Acceptance:**
- `grep -rn '"github.com/kstenerud/yoloai/sandbox"\|"github.com/kstenerud/yoloai/runtime"' internal/cli/ | grep -v _test.go | grep -v helpers.go | grep -v system_tart` returns no matches (allowing for the chokepoint in `helpers.go` and the Tart-scoped command file `system_tart.go`). **Note:** these are the current import paths (root-level packages); after W-L12 moves the packages under `internal/`, the grep must be updated accordingly.
- ARCHITECTURE.md describes the new layering.

**Size:** S · **Risk:** low (mostly deletions) · **Blocks:** W-L10.

---

### W-L9 — `--security` → `--isolation` BREAKING-CHANGES entry (conditional)

The doc bug fix happened in this same pass. If `--security` was in a tagged release, BREAKING-CHANGES.md needs an entry. If it never shipped, no entry needed.

**Steps:**

1. Check `git log --all -- internal/cli/help/security.md` and tagged releases for the presence of `--security`. Confirm via `git tag -l` and inspection.
2. If `--security` shipped: add an entry to `docs/BREAKING-CHANGES.md` documenting the rename, the value mapping (`standard` → `container`, `gvisor` → `container-enhanced`, `kata` → `vm`, `kata-firecracker` → `vm-enhanced`), and migration steps.
3. If `--security` never shipped: no action; close the workstream as N/A.

**Acceptance:**
- Either the BREAKING-CHANGES.md entry exists and is accurate, or the workstream is closed with a note explaining why no entry was needed.

**Size:** XS · **Risk:** low · **Blocks:** nothing · **Addresses:** Audit Q4; [design.md D6](../../design/layering.md#7-decisions-for-the-user).

---

### W-L10 — Enforcement: prevent regression

A test (or linter rule) that fails CI if forbidden imports or ambient-config calls appear in library code.

**Steps:**

1. Write a Go test (e.g., `internal/cli/layering_test.go`) that uses `go/packages` to enumerate imports of every `internal/cli/` file. Fail if any non-allowlisted file imports `internal/runtime/<concrete>` packages or `internal/sandbox`.
2. Allowlist: the chokepoint (`helpers.go` or wherever `newRuntime()` lives) for the registration imports; **the flat backend-scoped file `system_tart.go`** by filename (this is the only backend-scoped CLI file at W-L10 time; W-L13 later restructures into `internal/cli/system/tart/` and the allowlist must be updated to be directory-based at that time).
3. Add similar test for `sandbox/`: must not import any concrete `runtime/<backend>` package.
4. **Ambient-config ban (Q-W).** Forbid `os.UserHomeDir()`, `os.Getenv()` (when reading yoloai-namespaced vars or as silent default), and `os.Getwd()` outside one allowlisted CLI entry point. The allowlist is documented per call site in code with `// W-L10-allowlist: <reason>` comments; the linter checks both the call site count and the presence of the allowlist comment. See `principles/development-principles.md §12` for the rationale.
5. Optionally implement as a `golangci-lint` custom linter rule if test-based enforcement is awkward.
6. Add `runtime.Registered()`-iteration enforcement: a test that fails if `info.go`/`setup.go`/`bugreport_writer.go` regrows a hard-coded backend list (heuristic: search for ≥3 backend-name string literals in the same file).
7. Document the enforcement in `docs/contributors/principles/` (cross-reference §12 from this section).

**Acceptance:**
- CI fails on a PR that adds a forbidden import to `internal/cli/` (verify with a deliberately bad branch).
- CI fails on a PR that adds an unallowlisted `os.UserHomeDir()`, `os.Getenv()`, or `os.Getwd()` call to library code.
- CI fails on a PR that hard-codes a parallel backend table.
- Principle docs cite the test.

**Size:** S · **Risk:** low · **Blocks:** nothing · **Depends on:** W-L8e (the import set must be stable before enforcement).

---

## Sizing summary

Pre-AI estimates below; AI-assisted execution is observed to compress these significantly.

| Phase | Workstreams | Estimated effort (focused work) |
|---|---|---|
| 1 — Cleanup & naming | W-L1 (XS), W-L2 (S), W-L9 (XS) | ~3–5 days |
| 2 — Capability scaffolding | W-L3 (M), W-L4 (S), W-L5 (S), W-L6 (S), W-L7 (S) | ~3–4 weeks |
| 3 — Orchestration boundary | W-L8a–e (S/L/S/L/S) | ~6–10 weeks |
| 4 — Enforcement | W-L10 (S) | ~3 days |
| 5 — Greenfield consolidation | W-L12 (M), W-L13 (S), W-L14 (S) | ~2–3 weeks |

Total: roughly 11–16 weeks of focused architectural work. Spread across releases alongside ongoing feature work. The structural refactor (W-L8) is by far the largest and benefits from a code-freeze-on-direct-orchestration policy while it's in flight.

---

---

### W-L12 — Migrate package paths to `internal/*`

`sandbox/` and `runtime/` live at the repo root today because `yoloai.Client` consumed them (and historically Go's `internal/` rule would have blocked that consumption). After W-L8 lands, only the Client surface needs to import them — and the Client can import from `internal/` freely. Move both packages under `internal/` to enforce the public-surface boundary structurally rather than by convention. See [`layering-greenfield.md` §1](../../design/layering-greenfield.md#1-public-surface).

**Steps:**

1. Move `sandbox/` → `internal/orchestration/` (or `internal/sandbox/` — pick whichever fits the naming hierarchy better; "orchestration" is the greenfield doc's name).
2. Move `runtime/` → `internal/runtime/`.
3. Update every import path. Use `gofmt -r` or a structured search-replace; verify with `goimports` + `go build`.
4. Update `docs/contributors/architecture/README.md` package map.
5. Run `make check`.

**Acceptance:**

- `grep -rn '"github.com/kstenerud/yoloai/sandbox\|"github.com/kstenerud/yoloai/runtime' --include='*.go' .` returns no matches (except inside `internal/`).
- Only `yoloai` (root) and `cmd/yoloai` are non-`internal` Go packages.
- ARCHITECTURE.md reflects the new layout.
- `make check` passes.

**Size:** M · **Risk:** medium (large diff, but mechanical) · **Blocks:** nothing · **Depends on:** W-L8e (the Client surface must be stable; if the Client is mid-migration, an import-path move will conflict).

---

### W-L13 — Reorganize `internal/cli/*.go` into subdirectories

Today `internal/cli/` is flat: ~50 `.go` files at one level. The greenfield groups them: `commands/` (per-command files), `system/` (admin subtree with `system/tart/` for backend-scoped commands), `mcp/`, `profile/`, `config/`, plus `streams.go` and `format/`. The reorganization enforces the backend-scope rule structurally — a linter rule can assert "files under `cli/system/tart/` may import `runtime/tart`; files elsewhere in `cli/` may not."

**Steps:**

1. Create the subdirectory structure per [`layering-greenfield.md` §2](../../design/layering-greenfield.md#2-package-tree).
2. Move files into their new homes. Most files stay in the same `package cli`; some become subpackages (e.g. `package cli/system/tart`) — keep the package name simple per Go convention.
3. Update `internal/cli/commands.go` (or wherever Cobra wires the root command) to import from the new subpackages.
4. Convert backend-scoped commands (`system_runtime.go` → `system/tart/runtime.go` etc.) — these become a separate Go package so the layering linter (W-L10) can scope-check imports.
5. Update `docs/contributors/architecture/README.md`.
6. Run `make check`.

**Acceptance:**

- Directory structure matches `layering-greenfield.md` §2 (with tolerance for naming decisions made during implementation).
- Backend-scoped subdirectories are separate Go packages; the layering linter (W-L10) can enforce their import scope.
- ARCHITECTURE.md reflects the new layout.
- `make check` passes.

**Size:** S · **Risk:** low (mostly file moves and package renames) · **Blocks:** strengthens W-L10's enforcement · **Depends on:** W-L8e.

---

### W-L14 — Tart concurrent-VM limit detection (`ErrConcurrentVMLimit`)

Tart writes `"The number of VMs exceeds the system limit"` to stderr/`vm.log` when Apple's `VZError.virtualMachineLimitExceeded` (code 6) fires. Detect this in `runtime/tart/` and surface as a typed error that propagates through `yoloai.Client` to the CLI as a clear user message. Per [D11](../../design/layering.md#7-decisions), do **not** hard-code the VM count — defer to Tart's signal so behavior tracks Apple's policy if the limit changes.

**Steps:**

1. Add `ErrConcurrentVMLimit` to the runtime typed-error set (per [W7](architecture-remediation.md#w7) — `internal/yoerrors/` once W7 lands; or whichever package currently holds the typed errors).
2. In `runtime/tart/`'s run path: when `tart run` exits non-zero, read the captured stderr (and/or `vm.log`) for the substring `"The number of VMs exceeds the system limit"`. If matched, wrap the underlying error with `ErrConcurrentVMLimit`.
3. The `yoloai.Client` `Run()` method propagates the typed error. The CLI maps it to a user-facing message + exit code (decided in `exit-codes.md`).
4. **macOS verification before commit** (cannot do on Linux host — see [`tart-limit-detection.md` §What needs testing on a real Mac before committing`](../research/tart-limit-detection.md)):
   - Confirm the verbatim stderr prefix on a real host by trying to start a 3rd concurrent macOS VM
   - Confirm the exit code is 1 (no per-error exit codes in Tart)
   - Confirm `vm.log` is fully flushed before Tart's process exits (Swift `fputs` is unbuffered, but verify)
   - Investigate the stale-VM quota-leaking case from [tart issue #967](https://github.com/cirruslabs/tart/issues/967) — if it can leak quota, we may need a `tart list` cross-check too
5. Run `make check`.

**Acceptance:**

- `ErrConcurrentVMLimit` exists and is exported from the typed-error package.
- `runtime/tart/`'s `Run()` returns this error (wrapped) when the stderr substring matches.
- A unit test feeds a synthetic stderr buffer matching the prefix and asserts the typed error.
- **macOS verification steps in step 4 have been run on a real Apple Silicon Mac**, with results documented in a separate verification commit. All four checks (verbatim stderr prefix when starting a 3rd VM; exit code is 1; `vm.log` is fully flushed before process exit; stale-quota edge case from tart issue #967) are explicitly confirmed. If macOS hardware isn't available during implementation, W-L14 is **blocked**, not documented-around — it does not ship without verified results.
- `make check` passes.

**Size:** S · **Risk:** low (small surface, contained to runtime/tart/) · **Blocks:** nothing · **Depends on:** W7 typed-error package; coordinates loosely with W-L7 (which also touches Tart-specific surface).

---

## Coordination with [architecture-remediation.md](architecture-remediation.md)

The two plans interleave:

- **W-L1, W-L2, W-L9** can run in parallel with any phase of architecture-remediation.
- **W-L3 (descriptor extension)** touches `BackendDescriptor`; coordinate with W11 (runtime interface split) — if W11 reshapes the descriptor, do W-L3 after.
- **W-L4–L7** are independent of architecture-remediation.
- **W-L8 (yoloai.Client refactor)** is largest; ideally land architecture-remediation's W7 (typed errors) first since the Client's error surface depends on the error-package shape.
- **W-L10 (enforcement)** lands last in both plans.

---

## Open questions for resolution during implementation

These will be answered by the implementer during the workstream, not pre-decided:

- **W-L3:** ~~Should `BackendDescriptor` carry fields like `Pros`/`Cons` (currently in `knownBackends`)?~~ **Resolved (Round 7 critique):** Pros/Cons are CLI-presentation language (a selling pitch — "Most portable", "Strong isolation", etc.), not runtime facts. They belong in a CLI-side rendering struct that wraps the descriptor. `BackendDescriptor` carries only operational metadata (Name, Description, Platforms, Requires, Notes, and the new Probe/VersionString/CleanupHint additions). This keeps the runtime layer presentation-free.
- **W-L4:** Should `Probe()` cache results? A naive implementation re-probes on every `yoloai info` call. Likely yes, with a short TTL. Defer to implementation.
- **W-L7:** Does the `AppleSimulatorRuntimes` interface need to be in a new package, or can it live in `runtime/`? Likely `runtime/` — it's a runtime capability.
- **W-L8a:** How are streaming operations (attach, exec) shaped in the Client API? Pass explicit `io.Reader`/`io.Writer`, or return a typed `Stream` object? Decide during the design checkpoint.

---

## References

- [Design: Layering Architecture](../../design/layering.md) — the architecture this plan implements.
- [Layering Leak Audit](../research/layering-leak-audit.md) — finding numbers (L1–L31) referenced throughout.
- [Comparator Research](../research/layering-comparators.md) — patterns and prior art.
- [Architecture Remediation Plan](architecture-remediation.md) — sibling workstream plan; some W-L workstreams coordinate with W-numbered workstreams there.
