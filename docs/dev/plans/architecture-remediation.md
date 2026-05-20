# Architecture Remediation Plan

Concrete plan to address findings from [`../architecture-audit-2026-05.md`](../architecture-audit-2026-05.md). Each workstream is sized (XS/S/M/L), risk-flagged, and tied back to the finding it addresses.

## Guiding principles

- **Keep the Python in-container.** No per-architecture build steps. The boundary is the problem, not the language choice.
- **Eliminate sources of drift.** Where Go and Python both encode the same fact (e.g. agent launch wrappers), make one the single source of truth and have the other read it.
- **Make the boundary testable.** If the Python is risky because it has no unit tests, the answer is unit tests — not a port to Go.
- **Make the boundary contractual.** If `runtime-config.json` is the data interface, it should have a versioned schema both sides validate against.
- **Preserve cross-arch portability** that Python in-container gives us today.

## Workstreams

Ordered roughly by the order they unblock each other and by risk-ascending — early items are isolated mechanical wins, later items are structural.

---

### W1 — Move typed errors to a neutral package *(addresses F5)*

The four `runtime/*` packages all import `config/` solely for `config.NewDependencyError`, `config.NewPermissionError`, etc. That's the only direction violation in the dependency graph.

**Steps:**

1. Create `internal/yoerrors/` (or `errs/`) with the typed-error constructors and `errors.Is`/`As` targets currently in `config/errors.go`.
2. Update `config/` to re-export or import the new package.
3. Update `runtime/docker`, `runtime/podman`, `runtime/seatbelt`, `runtime/tart` to import from the new location.
4. Run `make check`.

**Size:** S · **Risk:** low (mechanical refactor) · **Blocks:** nothing.

---

### W2 — Replace 5 error-text matches with `errors.Is`/`errors.As` *(addresses F8)*

**Locations (from audit):**

- `sandbox/apply.go:385` — exec-exit-code-1 match
- `runtime/containerd/caps.go:179` — "permission denied"
- `runtime/containerd/lifecycle.go:442` — "address in use"
- `runtime/containerd/containerd.go:82` — "permission denied"
- `runtime/docker/docker.go:80` — "permission denied"

**Steps:**

1. For each, identify the upstream error the library actually returns (containerd, docker SDK). Verify via a quick test or by unwrapping in a debug session.
2. Replace `strings.Contains(err.Error(), ...)` with `errors.Is(err, fs.ErrPermission)` / `errors.Is(err, syscall.EACCES)` / a typed `errdefs` check / a wrapped sentinel.
3. Where the upstream error doesn't have a clean target, add a sentinel in the local package and document the constraint.

**Size:** S · **Risk:** low · **Blocks:** nothing.

---

### W3 — Configure `sloglint` to enforce slog conventions *(addresses F9)*

`sloglint` is already enabled in `.golangci.yml`; no settings are configured.

**Steps:**

1. Add `sloglint` settings: `attr-only: true`, `context: scope`, `static-msg: true`, `key-naming-case: snake`, `args-on-sep-lines: true`. Pick the exact subset that matches current convention (`event` first, `err` key for errors).
2. Run `golangci-lint run` and fix the violations the rule surfaces.
3. Audit the ~10 sites in `runtime/tart` and `sandbox/create_prepare.go` that omit `event`.
4. Standardize `error` vs `err` key — pick one (recommend `err`), sweep.

**Size:** XS · **Risk:** none · **Blocks:** nothing.

---

### W4 — Close the three backend-name leaks *(addresses F4)*

**Steps:**

1. **`internal/mcpsrv/proxy.go:225`** — replace the hardcoded `exec.Command("docker", ...)` with a call through `runtime.Runtime`. If the existing interface doesn't expose what the proxy needs, add a small method (e.g. `ProxyCommand(name string) []string` or similar) to the relevant backends.
2. **`sandbox/create.go:522`** — the `m.backend != "tart"` check guarding the `--runtime` flag should become a `Capabilities` field (e.g. `SupportsAppleSimulatorRuntimes bool`) or move into Tart's `Create()` as a precondition check.
3. **`internal/cli/sandbox_bugreport.go:213-222`** — replace the `switch backend { case "docker": ... case "podman": ... }` block with `runtime.Runtime.Logs(...)`. If the bugreport needs richer log output than `Logs()` provides today, extend the interface once rather than switching everywhere.

**Size:** S · **Risk:** low · **Blocks:** nothing (but enables F2 work by removing call sites that touch the interface).

---

### W5 — Unify the agent-command-wrap source of truth *(addresses F1 issue 2)*

The `Tart node@24` and `Seatbelt swift-wrapper` bugs both came from Go's `Runtime.PrepareAgentCommand()` and Python's `prepare_launch_command()` disagreeing about what wrapping to apply. Eliminate the duplication by giving Go ownership and having Python read a finalized string.

**Steps:**

1. In Go's sandbox creation path, compute the fully-wrapped agent command via `Runtime.PrepareAgentCommand(rawCmd)`. Write the result to `runtime-config.json` as a new field `agent_command_final` (keep the raw `agent_command` for diagnostics).
2. In Python `sandbox-setup.py::launch_agent`, read `agent_command_final` and `send-keys` it verbatim. Delete `prepare_launch_command()` and the per-backend overrides.
3. The Go restart path (`sandbox/lifecycle.go::respawn-pane`) already calls `Runtime.PrepareAgentCommand` — no change there, except that it can now read from `runtime-config.json` instead of recomputing, eliminating any state-drift risk.
4. Document the new field in the W6 schema work.

**Size:** S · **Risk:** medium (changes the in-container code path; integration tests will catch regressions) · **Blocks:** W6 (the schema needs the new field).

**Effect on backend-idiosyncrasies entries:** the two `restart bypass` entries (Tart node@24, Seatbelt swift-wrapper) become non-issues — the wrapper is computed once in Go and stored. Mark those entries as "fixed by W5" and consider removing them once W5 lands.

---

### W6 — Schema and validation for the Go↔Python contract *(addresses F1 issue 4, F10)*

Three JSON files cross the boundary: `runtime-config.json`, `environment.json`, `sandbox-state.json`. None have an explicit schema or version.

**Steps:**

1. Define each file's schema in Go as a tagged struct, with a top-level `schema_version int` field. Start at version 1.
2. Generate a `runtime-config.schema.json` (JSON Schema) from the Go struct at build time (or write it by hand and verify with a test). Embed it.
3. Add Go-side validation on write (already implicit via struct serialization; add an explicit schema-version check on read).
4. In Python, parse `runtime-config.json` through a `dataclasses`/`TypedDict`-typed reader that asserts `schema_version` matches a Python-side constant. Mismatch → loud failure at sandbox start.
5. Document the migration policy in `docs/BREAKING-CHANGES.md`: bumping `schema_version` is a breaking change for in-container scripts; the Go writer and Python reader must move together.

**Size:** M · **Risk:** low (additive; existing fields keep working) · **Blocks:** future Python tests (W7) that want to construct test fixtures.

---

### W7 — Add Python unit tests *(addresses F1 issue 1, F1 issue 5)*

The Python is ~2k lines and zero tests. Most of it is pure logic that doesn't need a tmux session or a container.

**Steps:**

1. Add `runtime/monitor/tests/` with pytest fixtures and a `conftest.py`.
2. Refactor `sandbox-setup.py` to separate pure functions (config parsing, command building, lifecycle preamble, retry-loop coordination) from I/O (subprocess, tmux, file system). Concretely:
   - Extract `lifecycle_preamble`, `_cmd_str`, the retry/event-coordination logic into a `lifecycle.py` module.
   - Extract `tmux(...)`, `tmux_output(...)`, `setup_tmux_session(...)` into a `tmux_io.py` module that takes a runner callable so tests can inject a fake.
   - Keep `sandbox-setup.py` as a thin entry point that wires modules together.
3. Write unit tests for:
   - `lifecycle_preamble` (already pure)
   - `pane_ready` event coordination (the race we just fixed in `5a060b9` — a unit test would have caught it: spawn a thread, don't set the event, verify the banner is not delivered; set the event, verify ordering)
   - `deliver_prompt` content composition (preamble + prompt concatenation)
   - `read_secrets` (with fake filesystem)
   - Backend `prepare_environment` paths (with mocked subprocess)
4. Wire pytest into `make check`. The Stop hook gate will then catch Python regressions before commits.

**Size:** M · **Risk:** low (additive; refactor mostly mechanical) · **Blocks:** nothing, but pairs naturally with W5 and W6.

**Race-bug-catch demonstration:** add `test_lifecycle_banner_waits_for_pane_ready` that constructs the daemon-thread setup, starts the lifecycle thread, asserts no tmux calls fire while `pane_ready` is unset, sets the event, asserts the banner delivers. The race from `5a060b9` would have failed this test before the fix.

---

### W8 — Cross-backend integration coverage in CI *(addresses F1 issue 3)*

Today `make integration-podman` only runs `./runtime/podman/`. The Python orchestration path is exercised only by Docker integration tests.

**Steps:**

1. Extend `make integration-podman` to also run a *subset* of `./internal/cli/integration_test.go` against the Podman backend. The subset should include `TestCLI_StartAfterDone` and any other tests that exercise launch + lifecycle.
2. Parametrize the integration suite by backend where feasible (a small helper that loops over `[]string{"docker", "podman"}` and runs the test body against each). Skip the loop locally when the backend isn't available.
3. Track containerd+Kata as a future addition (CI cost, would require a Kata-capable runner) — file under "future work" rather than in this plan.

**Size:** M · **Risk:** medium (CI time goes up; flake exposure for second backend) · **Blocks:** nothing.

---

### W9 — Split the `Runtime` interface *(addresses F2)*

24-method interface mixing lifecycle, descriptor, and adapter concerns. Backends like Seatbelt return empty/`false` from many methods.

**Steps:**

1. Define a smaller core `Runtime` (or rename it `Lifecycle`) with the 10 lifecycle methods plus `Close`, `Logs`, `DiagHint`.
2. Move the constants (`Name`, `BaseModeName`, `Capabilities`, `AgentProvisionedByBackend`, `SupportedIsolationModes`) into a `BackendDescriptor` struct populated by the registry alongside the `Factory`.
3. `Setup`/`IsReady` go to a `Builder` interface — only used by `system build` and `EnsureSetup`.
4. Convert the per-backend adapters to optional interfaces:
   - `TmuxSocketProvider` (currently only seatbelt implements meaningfully)
   - `AttachCommander` (currently every backend implements, but the impls are short and the indirection is wasted for most callers)
   - `AgentCommandPreparer` (after W5, this might disappear from the interface entirely — if Go computes the wrapped command once at creation, runtime never re-wraps)
   - `ResolveCopyMounter` (currently only seatbelt rewrites)
   - `IsolationCapabilityProvider` (`RequiredCapabilities`, plus `SupportedIsolationModes` if not moved to descriptor)
5. Update callers: most call sites only use the lifecycle subset; ensure they take the narrower interface.
6. Backends type-assert for optional interfaces at the call site, with a fallback when not implemented.

**Size:** L · **Risk:** medium-high (touches every backend + every consumer; needs careful PR sequencing) · **Blocks:** nothing, but harmonizes nicely after W5/W6.

**Effort note:** this is the highest-effort item in the plan and shouldn't be undertaken until W1–W4 are done. Doing it earlier means re-doing it after the leaks close.

---

### W10 — Carve `sandbox/` into subpackages *(addresses F3)*

33+ files in one package across distinct concerns. Coordination with [`soc-refactor.md`](soc-refactor.md), which already addresses part of this.

**Steps (additive to `soc-refactor.md`):**

1. Confirm `soc-refactor.md` lands first or in parallel — its Issue 2 (`create.go` god file) is a prerequisite.
2. After `create.go` is split, group files by concern:
   - `sandbox/archetype/` — `archetype.go`, `devcontainer.go`, `yoloaiyaml.go`, `vscode.go`, plus tests
   - `sandbox/patch/` — `diff.go`, `apply.go`, plus tests
   - `sandbox/store/` — `paths.go`, `meta.go`, `sandbox_state.go`, plus tests
   - `sandbox/` — `Manager`, lifecycle, create*, clone, inspect, keychain, lock, agent_files
3. Update imports. Run `make check`.
4. Document the new layout in `ARCHITECTURE.md`.

**Size:** L · **Risk:** medium (large diff but mostly mechanical move + import updates) · **Blocks:** nothing, but harmonizes after `soc-refactor.md`.

---

### W11 — Smaller items *(addresses F6, F7, smaller findings)*

Group of independent small wins.

1. **Split `internal/cli/commands.go` (690 lines).** Move `newNewCmd`, `newLsAliasCmd`, `newLogAliasCmd`, `newExecAliasCmd`, `newCompletionCmd`, `newVersionCmd` into their own files alongside the other one-command-per-file pattern. Keep `registerCommands()` and the helpers (`attachToSandbox`, `waitForTmux`) where they are or move helpers into `helpers.go`.
2. **Split `internal/cli/apply.go` (1068 lines).** Two workflows (squash, selective-commit) plus `--export` can become `apply.go`, `apply_squash.go`, `apply_selective.go`, `apply_export.go`. (S, low risk)
3. **Decide on `yoloai.Client` public API.** Either document an external consumer and add an API-stability note to BREAKING-CHANGES.md, or remove it and have the CLI hit `sandbox.Manager` directly. (XS)
4. **Generic `Wait` helper.** Replace `internal/testutil/wait.go`'s `WaitForActive`/`WaitForStopped` with `Wait[T any](t, get func() (T, error), pred func(T) bool, timeout time.Duration)`. (XS)
5. **Run `gopls modernize` once.** Apply Go 1.26 idiom updates (`for range n`, `min`/`max`, `slices.Concat`, etc.). Single commit, easy to review. (XS)

**Size:** XS each · **Risk:** none-to-low · **Blocks:** nothing.

---

### W12 — Dual command dispatch sanity check *(addresses F7, deferred)*

This is the one finding without a clear remediation step. Decision needed first.

**Steps:**

1. Add usage telemetry (if the project has any) or simply ask users which form they use. The verb-first form (`yoloai diff <name>`) is the historical CLI shape; the name-first form (`yoloai sandbox <name> diff`) was added more recently.
2. If both are well-used, keep them and add a test that asserts every command works through both dispatch paths.
3. If one dominates, deprecate the other with a quiet warning, then remove in a future breaking-changes window.

**Size:** XS (decision) · **Risk:** low if quiet · **Blocks:** nothing.

---

## Execution order

Concretely suggested PR sequence:

| Phase | Items | Rationale |
|---|---|---|
| **Phase 1 — Hygiene** | W1, W2, W3, W4 | Mechanical, isolating, no architectural impact. Get the easy wins. |
| **Phase 2 — Boundary safety** | W5, W6, W7 | Eliminate the source-of-truth drift, define the contract, add the test surface. The biggest risk-reducer. |
| **Phase 3 — Coverage** | W8 | Now there's something to actually run. Without Phase 2, the second-backend coverage just exercises the same untested Python. |
| **Phase 4 — Structural** | W10 (after `soc-refactor.md`), then W9 | Both are large diffs; do them when the foundation is stable. |
| **Phase 5 — Polish** | W11, W12 | Independent small items, can interleave anywhere. |

## Out of scope (explicitly rejected for this plan)

- **Unifying Go and Python into a single binary.** Would require per-architecture builds (linux-amd64, linux-arm64, macos-arm64 for Tart, macos-amd64 for Seatbelt) plus libc variants. Per-arch build complexity is exactly what Python-in-container avoids. The underlying problems (no tests, two sources of truth, no schema, no cross-backend coverage) are addressed by W5–W8.
- **Replacing the dual-dispatch CLI model.** Tracked as W12 — needs data before deciding.
- **Removing yoloai.Client.** Tracked as W11 item — needs an external-user check first.
- **containerd in CI.** Out of scope for W8; needs a Kata-capable runner. File under future work.

## Open questions

- W4 (mcpsrv proxy): does the proxy need a new method on `Runtime`, or can it use `Exec()` with the right shape? Worth a small spike before committing to an interface change.
- W5: are there any agents whose `prepare_launch_command` depends on container-runtime state that isn't known at sandbox creation time? Sweep `sandbox-setup.py` per-backend impls to confirm none do.
- W6: do we generate `runtime-config.schema.json` from Go structs, or hand-write? Hand-writing is fine for v1; generation is a tooling question.
- W9: is moving `Capabilities()` to a static descriptor compatible with cases where capabilities depend on host probing (e.g. a backend that needs to know whether `runsc` is installed before claiming `container-enhanced` support)? Need to check current usage.
