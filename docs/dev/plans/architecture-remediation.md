# Architecture Remediation Plan

Concrete plan to address findings from [`../architecture-audit-2026-05.md`](../architecture-audit-2026-05.md). Revised 2026-05-20 after a critique pass found three technical defects and a phase-ordering issue in the first draft (committed as `868a5b0`, superseded by this file).

**Scope.** Multi-quarter program. Phase 1 alone is ~1 week of focused work; full plan is ~4–5 months sequential, less if parallelized. Workstreams are independent — don't block on completing all of them.

**Each workstream lists explicit acceptance criteria** so "done" is observable, not asserted.

**Sizing legend.** XS = under a day · S = 1–3 days · M = 1–2 weeks · L = 2+ weeks.

## Guiding principles

- Keep Python in-container; cross-arch portability is real value.
- Eliminate sources of drift between Go and Python.
- Make the boundary testable, not unified.
- Make the boundary contractual via versioned schema.
- Don't add interface surface in one workstream that another wants to remove.

## Phase ordering

Boundary safety leads because the audit's biggest finding (F1) reduces to it. Mechanical hygiene comes later — it builds momentum but doesn't move the risk needle.

| Phase | Items | Rationale |
|---|---|---|
| **1 — Boundary safety** | W1, W2 | Close source-of-truth drift, define the contract. Highest risk reduction. |
| **2 — Python testability** | W3, W4 | pytest infra (cheap), then I/O seams + race tests (the real safety net). |
| **3 — Coverage** | W5, W6 | Test parametrization, then CI matrix. Only useful once Phase 1–2 has something worth running cross-backend. |
| **4 — Hygiene** | W7, W8, W9, W10 | Mechanical wins. Interleavable anywhere; don't block other phases. |
| **5 — Structural** | W11, W12 | Big diffs. Do when Phase 1–3 is stable. |
| **6 — Polish + closure** | W13, W14 | Small wins plus a checkpoint that verifies findings actually closed. |

---

## Workstreams

### W1 — Unify the agent-command-wrap source of truth *(addresses F1 issue 2)*

The `Tart node@24` and `Seatbelt swift-wrapper` bugs both came from Go's `Runtime.PrepareAgentCommand()` and Python's `prepare_launch_command()` disagreeing about what wrapping to apply. Eliminate the duplication: Go computes the wrap once at sandbox creation; both initial launch (Python) and restart (Go) read the same stored string.

**Steps:**

1. In Go's sandbox creation path, compute the fully-wrapped agent command via `Runtime.PrepareAgentCommand(rawCmd)`. Write the result to `runtime-config.json` as `agent_command_final` (keep raw `agent_command` for diagnostics).
2. In Python `sandbox-setup.py::launch_agent`, read `agent_command_final` and `send-keys` it verbatim. Delete `prepare_launch_command()` and all per-backend overrides (`DockerBackend.prepare_launch_command`, `TartBackend.prepare_launch_command`, `SeatbeltBackend.prepare_launch_command`).
3. **`sandbox/lifecycle.go::respawn-pane` reads `agent_command_final` from `runtime-config.json` and uses it verbatim — does NOT re-invoke `Runtime.PrepareAgentCommand`.** This is the single-source-of-truth invariant: `PrepareAgentCommand` is called exactly once per sandbox, at creation.
4. Document the new field in W2's schema bump.

**Rollback:** gate behind `runtime-config.use_stored_agent_cmd: true` for one release window. Python falls back to legacy `prepare_launch_command` if the field is missing. After a release with no regressions, remove the gate and delete `prepare_launch_command`.

**Acceptance:**
- `sandbox-setup.py` no longer defines `prepare_launch_command` (any backend).
- `lifecycle.go::respawn-pane` does not call `Runtime.PrepareAgentCommand` — reads `agent_command_final` from runtime-config.json.
- The two restart-bypass entries in `backend-idiosyncrasies.md` (Tart node@24, Seatbelt swift-wrapper) are deleted with a commit note pointing here.

**Size:** S · **Risk:** medium (boundary change) · **Blocks:** W2 needs to know about the new field.

### W2 — Schema versioning, tiered *(addresses F1 issue 4, F10)*

Four files cross the Go↔Python boundary: `runtime-config.json` (Go writes, Python reads), `environment.json` (Go writes/reads), `sandbox-state.json` (Go writes/reads), `agent-status.json` (Python writes, Go reads). None have an explicit version or schema.

**Tier 1 — version field with loud-fail (must do):**

1. Add `schema_version int` to the Go struct backing each of the four files. Start at 1.
2. On read (both Go and Python), compare `schema_version` against an expected constant. Mismatch → fail loudly with a clear message ("schema version 2 not supported by this binary; runtime-config.json was written by a newer yoloai").
3. Document the bump policy in `docs/BREAKING-CHANGES.md`: changing the on-disk shape requires a `schema_version` bump and a coordinated Go-side + Python-side update.

**Tier 2 — JSON Schema validation (deferred):** only adopt if Tier 1 misses a case (e.g., field-shape regression caught at runtime instead of at boundary read). Requires `invopop/jsonschema` codegen on Go side and a `jsonschema` Python dep on the other. Revisit after one release of Tier 1 in production.

**Acceptance (Tier 1):**
- All four boundary files carry `schema_version: 1`.
- Go and Python both fail loudly with a specific message on version mismatch.
- `docs/BREAKING-CHANGES.md` describes the bump policy.

**Size:** S (Tier 1 only) · **Risk:** low · **Blocks:** nothing (W1 can land first or together).

### W3 — Python pytest infra + pure-function tests *(addresses F1 issue 1, partial)*

The Python is ~2k lines, zero tests. A subset of functions is genuinely pure and testable without infrastructure: `lifecycle_preamble`, `_cmd_str`, `read_secrets` filter logic, `deliver_prompt` content composition (the part before tmux calls). Add pytest, cover those.

**Steps:**

1. Add `runtime/monitor/tests/` with `conftest.py` and `pytest.ini`.
2. Target **Python 3.11+** (matches Debian bookworm in the Docker base image; verify via `python3 --version` inside `yoloai-base`).
3. Add `make python-test` target: `python3 -m pytest runtime/monitor/tests/ -v`.
4. Make `make check` depend on `make python-test`. The Stop hook gate then catches Python regressions.
5. Add **type hints to the tested modules** and run `mypy --strict` as part of `make python-test`. mypy failures fail the build.
6. Write unit tests covering: `lifecycle_preamble`, `_cmd_str`, `deliver_prompt` content composition, `read_secrets` (with a fake filesystem via `tmp_path`), one parse function from `runtime-config.json` reading.

**Acceptance:**
- `runtime/monitor/tests/` exists with ≥5 pure-function unit tests; `make python-test` runs them.
- `make check` invokes `make python-test`; CI runs it; failure blocks merge.
- mypy --strict passes on all tested modules.
- README/CLAUDE.md mentions the new target.

**Size:** S · **Risk:** low (purely additive) · **Blocks:** W4 (which builds on the pytest infra).

### W4 — Python I/O seams + race-coordination tests *(addresses F1 issue 5)*

W3 covers pure functions. The actual race-prone code (tmux orchestration, threading.Event coordination — what the `5a060b9` bug lived in) needs injectable I/O seams to be testable.

This is the more invasive refactor. Doing it in W4 (after W3) means the pytest infra exists and the test harness style is established.

**Steps:**

1. Extract `tmux(...)`, `tmux_output(...)`, `setup_tmux_session(...)` into a `tmux_io.py` module with a module-level injectable runner: `set_runner(fn)` swaps `subprocess.run` for a fake in tests.
2. Same for `subprocess.run` callers outside `tmux_io.py` — `read_secrets`, lifecycle commands, gosu/chown invocations.
3. Refactor `run_lifecycle_background` and `launch_agent` to take the threading.Event coordination object as a parameter (already done for `pane_ready` in `5a060b9`; extend pattern).
4. Write the **race-catch demonstration test**: spawn the lifecycle-banner thread with a no-op `pane_ready`, assert no tmux calls fire before the event is set; set it, assert ordering of calls matches expectation. This test would have caught `5a060b9`'s race before commit.
5. At least one more race-prone path gets a similar test (candidates: secrets-passing concurrent with agent launch, status-monitor concurrent with tmux pane-pipe).

**Acceptance:**
- All tmux/subprocess calls in `sandbox-setup.py` and `status-monitor.py` route through injectable runners.
- `test_lifecycle_banner_waits_for_pane_ready` exists and passes; reverting `5a060b9`'s `pane_ready.wait()` call causes it to fail.
- At least one additional race-coordination test exists.

**Size:** M · **Risk:** medium (refactor touches load-bearing I/O) · **Blocks:** nothing, but W6 is more valuable after this.

### W5 — Parametrize integration tests by backend *(addresses F1 issue 3, prerequisite half)*

`internal/cli/integration_test.go` hardcodes `dockerrt.New(...)`. To run it against Podman (or any other backend), the test harness must become backend-agnostic. This is the actual work behind "cross-backend coverage" — the CI change (W6) is trivial; this parametrization is medium-large.

**Steps:**

1. Add a backend-resolution helper in `internal/cli/integration_main_test.go` (or `internal/testutil/`) that returns a `runtime.Runtime` based on an env var (`YOLOAI_TEST_BACKEND`, default `docker`).
2. Update `internal/cli/integration_test.go` to use the helper instead of `dockerrt.New`.
3. Verify the existing Docker integration suite still passes.
4. Confirm `TestCLI_StartAfterDone` runs unchanged through the helper.

**Acceptance:**
- No file in `internal/cli/` hardcodes `dockerrt.New` (or backend-specific types) inside test setup.
- `YOLOAI_TEST_BACKEND=docker go test -tags=integration ./internal/cli/` and `YOLOAI_TEST_BACKEND=podman go test -tags=integration ./internal/cli/` both work locally.

**Size:** M · **Risk:** low (test-side refactor) · **Blocks:** W6.

### W6 — CI matrix expansion *(addresses F1 issue 3, the trivial half)*

After W5, the CI change is small.

**Steps:**

1. Extend `make integration-podman` to run a subset of `./internal/cli/` against Podman (use `YOLOAI_TEST_BACKEND=podman`). Subset should include `TestCLI_StartAfterDone` and other launch/lifecycle tests.
2. Update `.github/workflows/ci.yml`'s `integration-podman` job to invoke the expanded target.
3. Track containerd+Kata as future work in this file (needs a Kata-capable runner — not in scope for this plan).

**Acceptance:**
- CI's integration-podman job runs the parametrized `internal/cli/` subset against Podman.
- A breakage in `sandbox-setup.py` that only manifests on Podman fails CI on the Podman job.

**Size:** XS · **Risk:** medium (CI time increases by ~5 min; flake exposure for second backend) · **Blocks:** nothing.

### W7 — Typed errors to neutral package *(addresses F5)*

`runtime/docker`, `runtime/podman`, `runtime/seatbelt`, `runtime/tart` all import `config/` for typed-error constructors — the only direction violation in the dependency graph.

**Steps:**

1. **Decision:** move (not re-export). `yoloai.Client` doesn't expose `config.NewXxxError`; there's no external API to preserve. Pick: full move to `internal/yoerrors/`.
2. Create `internal/yoerrors/` containing `UsageError`, `ConfigError`, `ActiveWorkError`, `DependencyError`, `PlatformError`, `AuthError`, `PermissionError` and their constructors.
3. Update all import sites (`config/`, `runtime/*`, `internal/cli/`, `sandbox/`).
4. Run `make check`.

**Acceptance:**
- `internal/yoerrors/` exists; `config/errors.go` no longer defines typed-error constructors.
- `grep -r "kstenerud/yoloai/config" runtime/` returns no matches.
- All `errors.Is` / `errors.As` call sites updated.

**Size:** S · **Risk:** low · **Blocks:** nothing.

### W8 — Replace error-text matches with `errors.Is`/`errors.As` *(addresses F8)*

Five brittle `strings.Contains(err.Error(), ...)` sites listed in the audit.

**Steps:**

1. For each site, **run a small reproducer** that triggers the error condition and prints `fmt.Sprintf("%#v\n%T\n%+v", err, err, err)` for the unwrapped chain. Identify the upstream type or sentinel.
2. Likely targets: `errors.Is(err, fs.ErrPermission)` / `errors.Is(err, syscall.EACCES)` for the "permission denied" cases; `errors.Is(err, syscall.EADDRINUSE)` for the address-in-use case. For containerd-shim errors, check the `errdefs` package.
3. If a site has no usable typed target, **acknowledge it explicitly** — wrap the upstream call to return a local sentinel, document why text-matching is irreducible.
4. Each replacement gets a negative test: feed a wrong-error to the call site, confirm the wrong branch isn't taken.

**Acceptance:**
- All 5 sites listed in the audit (`sandbox/apply.go:385`, `runtime/containerd/{caps,lifecycle,containerd}.go`, `runtime/docker/docker.go:80`) replaced or explicitly documented as irreducible.
- Each replacement has a negative test.

**Size:** S · **Risk:** low · **Blocks:** nothing.

### W9 — Configure sloglint *(addresses F9)*

`sloglint` is enabled in `.golangci.yml` but unconfigured. The first draft of this plan recommended `attr-only: true` and `context: scope` — both wrong: `attr-only` requires switching from key-value variadics to `slog.LogAttrs`, a codebase-wide rewrite the audit didn't ask for; `context` is unrelated to the actual `event` / `err` finding.

**Steps:**

1. Add these `sloglint` settings to `.golangci.yml`:
   - `static-msg: true` — message must be a string literal (already true everywhere).
   - `key-naming-case: snake` — locks the existing `event=sandbox.create` style.
   - `forbidden-keys: ["error"]` — forces the canonical `err` key (current usage mixes both; recommend canonicalizing on `err`).
2. Run `golangci-lint run`; fix violations (≤10 sites in `runtime/tart/tart.go` and `sandbox/create_prepare.go` per the audit; plus any `"error"` keys to rename to `"err"`).
3. Do **not** add `attr-only: true` — current key-value style is fine and switching is out of scope.

**Acceptance:**
- `.golangci.yml` has the three `sloglint` settings above (and only those).
- `golangci-lint run` passes with no `//nolint:sloglint` suppressions added.
- Every `slog.Info`/`Warn`/`Debug`/`Error` call in non-test code uses `"err"` (not `"error"`) for error attributes.

**Size:** XS · **Risk:** none · **Blocks:** nothing.

### W10 — Close backend-name leaks *(addresses F4, aligned with W11 + soc-refactor.md)*

Three leaks identified. Each addressed in a way that's compatible with W11's interface shrinkage and `soc-refactor.md`'s `BackendCaps` cleanup.

**Steps:**

1. **`internal/mcpsrv/proxy.go:225`** hardcodes `exec.Command("docker", ...)`. First, **read the file** to see what it's actually trying to do. If it's running `docker exec` to bridge MCP traffic, the existing `Runtime.InteractiveExec` covers that. If it needs something `Runtime` doesn't expose (e.g., port forwarding), define a new **optional interface** `MCPBridge` (W11-compatible pattern), not a method on the core `Runtime`.
2. **`sandbox/create.go:522`** has `m.backend != "tart"` gating the `--runtime` flag. **Do NOT** add a new `BackendCaps` bool — that's the exact pattern `soc-refactor.md` Issue 1 is removing. Instead: move the precondition check into `runtime/tart/tart.go::Create` (it returns an error if `--runtime` was set but the backend isn't Tart). The sandbox layer stops knowing about Tart.
3. **`internal/cli/sandbox_bugreport.go:213-222`** switches on backend to call `docker logs` / `podman logs`. Replace the entire switch with `runtime.Runtime.Logs(ctx, name, n)`, which exists. If the bugreport needs richer output than `Logs()` returns today, extend `Logs()` once (or add an optional `LogsVerbose` interface) — don't switch on backend name.

**Acceptance:**
- `grep -E '"(docker|podman|tart|seatbelt|containerd)"' internal/mcpsrv/ internal/cli/sandbox_bugreport.go sandbox/create.go` returns no business-logic matches (only legitimate user-facing strings like help text).
- `BackendCaps` did **not** gain a new boolean field as part of this work.
- soc-refactor.md Issue 1's pattern (capability checks as runtime methods, not BackendCaps fields) is what gets followed in step 2.

**Size:** S · **Risk:** low · **Blocks:** nothing, but aligned with W11.

### W11 — Split the `Runtime` interface *(addresses F2)*

24-method interface. Backends like Seatbelt return empty/false from many methods. Violates ISP and makes mocking painful.

**Spike (do first):** catalog every existing `Capabilities()` and per-method implementation across the 5 backends. Identify which constant facts are truly static (could move to a `BackendDescriptor`) vs which depend on runtime host probing (must stay as interface methods). If a meaningful chunk is dynamic, the descriptor-struct idea needs adjustment.

**Steps (after spike):**

1. Rename the current `Runtime` interface to `Lifecycle` (or keep `Runtime` — name TBD). Reduce to ≤12 methods: Create, Start, Stop, Remove, Inspect, Exec, GitExec, InteractiveExec, Prune, Close, Logs, DiagHint.
2. Create `BackendDescriptor` struct holding the static facts: Name, BaseModeName, AgentProvisionedByBackend, SupportedIsolationModes (and `Capabilities` IFF the spike confirms static).
3. Backends register `(Factory, Descriptor)` tuples instead of just `Factory`.
4. Convert per-backend adapters to optional interfaces:
   - `TmuxSocketProvider` — seatbelt only.
   - `AttachCommander` — every backend, but the impls are short; keep as optional with a default helper.
   - `ResolveCopyMounter` — seatbelt only.
   - `IsolationCapabilityProvider` (RequiredCapabilities, optionally SupportedIsolationModes if not on descriptor).
   - After W1: `PrepareAgentCommand` may disappear from the interface entirely (called once at creation, never at restart).
5. Update callers. Most call sites only use the lifecycle subset; ensure they take the narrower interface.
6. Optional-interface call sites do `if p, ok := rt.(TmuxSocketProvider); ok { ... }` with a documented fallback when not implemented.

**Rollback:** workstream is single-shot. If mid-implementation a backend can't be made to fit the new shape, abort the workstream and revert; don't ship a half-converted interface. The pre-W11 interface is recoverable from VCS.

**Acceptance:**
- Core interface has ≤12 methods.
- `BackendDescriptor` exists; registry returns `(Factory, Descriptor)`.
- ≥3 optional interfaces; each documented with a call-site fallback pattern.
- All 5 backends compile; all tests pass.
- A new mock for testing `sandbox.Manager` no longer needs to implement methods the test doesn't exercise.

**Size:** L · **Risk:** medium-high · **Blocks:** nothing (but easier after W1 removes `PrepareAgentCommand` from the interface).

### W12 — Sandbox/ carve-up + test file reorganization *(addresses F3, M1)*

33+ files in one package; biggest tests >1000 lines.

**Prereq decision (do at workstream start):** either `soc-refactor.md` lands first (its Issue 2 is the create.go god file), or fold its scope into W12. Don't proceed with W12 until `soc-refactor.md`'s `create.go` split is done — without it, this workstream collides with that one.

**Steps:**

1. After `create.go` is split (via soc-refactor.md or as the first step of W12), group remaining files:
   - `sandbox/archetype/` — `archetype.go`, `devcontainer.go`, `yoloaiyaml.go`, `vscode.go`, plus their tests.
   - `sandbox/patch/` — `diff.go`, `apply.go`, plus their tests.
   - `sandbox/store/` — `paths.go`, `meta.go`, `sandbox_state.go`, plus their tests.
   - `sandbox/` — `Manager`, lifecycle, create*, clone, inspect, keychain, lock, agent_files.
2. Identify and break any package-cycle introductions (likely candidates: store imports from archetype, patch imports from store). Resolve via interface extraction or moving shared types up.
3. Split the four largest test files (`apply_test.go` 1343 lines, `lifecycle_test.go` 1243, `create_helpers_test.go` 1246, `setup_test.go` 632) to match new subpackage boundaries.
4. Update `ARCHITECTURE.md`.

**Acceptance:**
- `sandbox/` has ≥3 subpackages (archetype, patch, store) plus the slimmed parent.
- No `_test.go` file >800 lines.
- `make check` passes.
- `ARCHITECTURE.md` reflects new layout.

**Size:** L · **Risk:** medium · **Blocks:** nothing.

### W13 — Smaller items *(addresses F6, F7, audit Section 5)*

Group of independent small wins. Each is its own commit.

- **13a.** Split `internal/cli/commands.go` (690 lines). Move `newNewCmd`, `newLsAliasCmd`, `newLogAliasCmd`, `newExecAliasCmd`, `newCompletionCmd`, `newVersionCmd` to per-command files. **Acceptance:** `commands.go` ≤200 lines, contains only `registerCommands()` and shared helpers.
- **13b.** Split `internal/cli/apply.go` (1068 lines). Three workflows: `apply.go` (entry + shared), `apply_squash.go`, `apply_selective.go`, `apply_export.go`. **Acceptance:** no file >400 lines.
- **13c.** Decide `yoloai.Client` fate. **Acceptance:** either an external consumer is documented in CLAUDE.md plus a stability declaration in BREAKING-CHANGES.md, or `yoloai.go` is moved into `internal/` and stops being a public API.
- **13d.** Generic `Wait[T any]` helper in `internal/testutil/wait.go`. **Acceptance:** `WaitForActive` / `WaitForStopped` become one-line wrappers over the generic.
- **13e.** Run `gopls modernize` once. **Acceptance:** one commit applying analyzer-driven idiom updates (`for range n`, `min`/`max`, `slices.Concat`, etc.).
- **13f.** Audit `sandbox/setup.go` (632-line test file). **Acceptance:** either justified in a comment (genuinely complex setup) or split.

**Size:** XS each (~2 weeks total if done sequentially) · **Risk:** none-to-low · **Blocks:** nothing.

### W14 — Re-audit checkpoint *(new, addresses M4)*

Triggered after Phase 1+2+3 (W1–W6) lands. Verifies the audit findings actually closed, doesn't just trust the workstream completion claims.

**Steps:**

1. Re-run the dependency / error / observability audits (spawn the same Explore agents the original critique used; or do manually).
2. Confirm F1, F4, F5, F8, F9 are closed and F10 has Tier 1 coverage. F2, F3 remain open (Phase 5 work).
3. Update `architecture-audit-2026-05.md` with a "Status as of <date>" addendum listing what closed and what remains.
4. If new findings emerge, file as `architecture-audit-YYYY-MM.md` (this file is a frozen snapshot).

**Acceptance:**
- A "Status as of <date>" section appended to `architecture-audit-2026-05.md`.
- Each F-number from the original audit has a current status: closed / open / partial.
- Any new findings are filed as a new dated audit, not as edits to the original.

**Size:** S · **Risk:** none · **Blocks:** nothing.

---

## Effort budget summary

| Phase | Workstreams | Rough total |
|---|---|---|
| 1 — Boundary safety | W1 (S), W2 (S) | ~1 week |
| 2 — Python testability | W3 (S), W4 (M) | ~3 weeks |
| 3 — Coverage | W5 (M), W6 (XS) | ~2 weeks |
| 4 — Hygiene | W7 (S), W8 (S), W9 (XS), W10 (S) | ~2 weeks (parallelizable) |
| 5 — Structural | W11 (L), W12 (L) | 6+ weeks |
| 6 — Polish + closure | W13 (6× XS), W14 (S) | ~2 weeks |

**Total:** 4–5 months sequential. Phase 1+2+3 (the audit's biggest risk reduction) is ~6 weeks. Phases 4 and 6 are parallelizable into the gaps.

## Out of scope (explicitly rejected for this plan)

- **Unifying Go and Python into a single binary.** Would require per-architecture builds (linux-amd64, linux-arm64, macos-arm64 for Tart, macos-amd64 for Seatbelt) plus libc variants. The underlying problems (no tests, two sources of truth, no schema, no cross-backend coverage) are addressed by W1–W6.
- **Replacing the dual-dispatch CLI model.** Tracked as `OPEN_QUESTIONS.md` #100 — needs usage data before deciding.
- **Full JSON Schema validation.** Deferred to W2 Tier 2; revisit only if Tier 1 misses a case.
- **containerd in CI.** Out of scope for W6; needs a Kata-capable runner.

## Open questions

These are *blocking* questions for specific workstreams — resolve before starting the relevant work:

1. **W1:** are there any agents whose `prepare_launch_command` depends on container-runtime state not known at sandbox creation time? Sweep all `prepare_launch_command` per-backend impls; confirm all wrappers are static strings.
2. **W7:** does `yoloai.Client` (public API) export any of the typed errors? If yes, the move is a breaking API change and needs a BREAKING-CHANGES.md entry; if no, the move is internal.
3. **W11:** does any backend's `Capabilities()` return values that depend on host probing? If yes, the static-descriptor idea needs adjustment (move only the truly-static fields, leave dynamic ones on the interface). This is the spike step in W11.

---

*Revised 2026-05-20 after critique of v1 (commit `868a5b0`). Key changes: phase ordering puts boundary safety first; W3 (sloglint) settings corrected; W4 (backend leaks) aligned with W11 and `soc-refactor.md`; W5/W6 (formerly W5) made single-source-of-truth unambiguous with explicit rollback; W6 (schema) tiered to deferrable Tier 2; W7 (Python tests) split into W3+W4; W8 (CI coverage) split into W5+W6; W12 (dual dispatch) moved to OPEN_QUESTIONS.md; new W14 re-audit checkpoint; explicit acceptance criteria per workstream; effort budget summary.*
