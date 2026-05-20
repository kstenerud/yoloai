# Architecture Remediation Plan

Concrete plan to address findings from [`../architecture-audit-2026-05.md`](../architecture-audit-2026-05.md). Third revision after two critique rounds (v1 committed `868a5b0`; v2 committed `1e9c558`).

**Scope.** Multi-quarter program. Phase 1 alone is ~1 week of focused work; full plan is ~4–5 months of focused architectural work, spread over multiple quarters as feature work and bug fixes also progress. Workstreams are independent — don't block on completing all of them.

**Each workstream lists explicit acceptance criteria** so "done" is observable, not asserted.

**Sizing legend.** XS = under a day · S = 1–3 days · M = 1–2 weeks · L = 2+ weeks.

## Guiding principles

- Keep Python in-container; cross-arch portability is real value.
- Eliminate sources of drift between Go and Python.
- Make the boundary testable, not unified.
- Make the boundary contractual via versioned schema.
- Don't add interface surface in one workstream that another wants to remove.
- Every workstream must be shippable independently.

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

## Discovered-findings policy

Anything found mid-workstream that wasn't in the original audit goes in `docs/dev/discovered-findings.md` (create on first hit). W14's re-audit incorporates it. Don't expand a workstream's scope to absorb new findings — leave the workstream scoped to what was planned.

**Severity gate.** **Critical findings escalate immediately, do not park.** Critical = observable data loss, security issues, observable regressions in shipped behavior, or anything that would block the current release. Everything else (architectural smells, latent bugs without observable symptoms, refactoring opportunities) parks in `discovered-findings.md` until W14. The discoverer makes the call; when uncertain, escalate.

---

## Workstreams

### W1 — Unify the agent-command-wrap source of truth *(addresses F1 issue 2)*

The Tart node@24 and Seatbelt swift-wrapper bugs both came from Go's `Runtime.PrepareAgentCommand()` and Python's `prepare_launch_command()` disagreeing. Eliminate the duplication: Go computes the wrap once at sandbox creation; both initial launch (Python) and restart (Go) read the same stored string.

Two explicit phases — the rollback story only makes sense if both phases exist:

#### W1a — Ship behind a gate (Phase 1 work)

**Steps:**

0. **Prerequisite sweep.** Read every `prepare_launch_command` per-backend impl in `sandbox-setup.py`. Confirm all wrappers are static strings (no runtime-state dependencies). If any are dynamic — e.g., a wrapper that reads `/proc` or queries `tart exec` at launch time — W1's design needs an extension: Python re-computes from data Go provides, but the wrapper itself stays in Python for that case. If all wrappers are static, proceed.
1. In Go's sandbox creation path, compute the fully-wrapped agent command via `Runtime.PrepareAgentCommand(rawCmd)`. Write the result to `runtime-config.json` as `agent_command_final` (keep raw `agent_command` for diagnostics).
2. In Python `sandbox-setup.py::launch_agent`, **if `runtime-config.use_stored_agent_cmd: true`**, read `agent_command_final` and `send-keys` it verbatim. Otherwise fall back to `prepare_launch_command()`. Per-backend Python overrides remain.
3. **`sandbox/lifecycle.go::respawn-pane` reads `agent_command_final` from `runtime-config.json` and uses it verbatim** when the gate is on; otherwise unchanged. This is the single-source-of-truth invariant: `PrepareAgentCommand` is called exactly once per sandbox, at creation.
4. Set the gate to `true` for new sandboxes; existing sandboxes (created before W1a) keep their old behavior since they don't have the field.

**Acceptance for W1a:**
- `agent_command_final` is written by Go at sandbox creation when the gate is on.
- `lifecycle.go::respawn-pane` reads it from `runtime-config.json` (with the gate); does not re-invoke `Runtime.PrepareAgentCommand`.
- Both code paths work; integration tests pass.

#### W1b — Remove the gate + legacy code (one release later)

"One release later" anchors to: **the release immediately following W1a's release, with a minimum 4-week gap from W1a's tag to W1b's start.** The gap exists to let regression reports surface; 4 weeks is a coarse beta-cadence approximation. If a regression surfaces during the gap, W1b waits a further release cycle.

**Steps:**

1. After one release (per the cadence above) with no reported regressions, remove the `use_stored_agent_cmd` gate.
2. Delete `prepare_launch_command()` from `sandbox-setup.py` and all per-backend overrides.
3. Rewrite the two restart-bypass entries in `backend-idiosyncrasies.md` (Tart node@24, Seatbelt swift-wrapper) to focus on the *environmental* facts (Cirrus base image's .zprofile; macOS sandbox-exec doesn't nest) and note that yoloAI handles them via the stored final command. The environmental facts don't go away when W1 lands — only the failure mode does.

**Acceptance for W1b:**
- `grep -r "prepare_launch_command" runtime/monitor/` returns no production code.
- `lifecycle.go` no longer references `use_stored_agent_cmd`.
- The two backend-idiosyncrasies.md entries describe the environmental fact + yoloAI's current handling, not a yoloAI failure mode.

**Size:** S (W1a) + XS (W1b, scheduled later) · **Risk:** medium (boundary change, mitigated by gate) · **Blocks:** W2 needs to know about the new field.

### W2 — Schema versioning, tiered *(addresses F1 issue 4, F10)*

Four files cross the Go↔Python boundary: `runtime-config.json` (Go writes, Python reads), `environment.json` (Go writes/reads), `sandbox-state.json` (Go writes/reads), `agent-status.json` (Python writes, Go reads). None have an explicit version or schema.

#### Why Tier 1 is probably enough

Tier 1 (version field + loud-fail) plus the existing safety from typed serialization on both sides catches the two problem classes we actually have:

- **Version bumps** — caught by the version check directly.
- **Field-shape regressions** — Go marshals from a tagged struct, so type mismatches surface at marshal time. Python parses into a typed `dataclass`/`TypedDict` with `mypy --strict` (per W3), so type mismatches surface at parse time.

What Tier 1 doesn't catch is **semantic regressions** — a field's meaning silently changes while its type stays the same. That's the only motivation to add Tier 2. Defer Tier 2 until a concrete semantic-regression bug is observed.

#### Tier 1 — version field with loud-fail (do)

**Bump policy:**

- **Additive changes** (new optional fields with sensible defaults) do not require a bump. Both readers tolerate missing fields.
- **Required-field changes, removals, renames, and semantic changes** require a bump and a coordinated Go-side + Python-side update, documented in `docs/BREAKING-CHANGES.md`.

**Downgrade policy:**

When a newer yoloai wrote files and an older binary reads them (downgrade scenario), the older binary **hard-fails** with a specific message rather than risking silent misinterpretation. This trades user inconvenience for safety: re-creating the sandbox loses agent session state (chat history, work-in-progress), so the practical workaround is "use the newer binary." The hard-fail is conservative because (a) silent best-effort reads hide real incompatibilities and (b) yoloAI is in public beta where breaking changes are explicit. If user reports indicate downgrade is a common practical scenario, revisit: a `--accept-newer-schema` flag could opt the user into best-effort reads with a clear warning. Documented as a trade-off, not a "straightforward" workaround.

**Steps:**

1. Add `schema_version int` field (json tag `"schema_version"`) to the Go struct backing each of the four files. Start at 1.
2. On read (both Go and Python), compare `schema_version` against an expected constant. Mismatch → fail loudly with a specific message: `"schema_version mismatch: got N, expected M (runtime-config.json was written by an incompatible yoloai version; re-create the sandbox)"`.
3. Document the bump policy (above) in `docs/BREAKING-CHANGES.md`.

**Tier 2 — JSON Schema validation (deferred):** revisit only if a concrete semantic-regression bug is observed. Documented here so it isn't lost.

**Acceptance:**
- All four boundary files carry `schema_version: 1`.
- Go and Python both fail loudly on mismatch with the specific message above; verified by a unit test that constructs a malformed file and asserts the error.
- `docs/BREAKING-CHANGES.md` has the bump and downgrade policies above.

**Size:** S · **Risk:** low · **Blocks:** nothing (W1 can land first or together).

### W3 — Python pytest infra + pure-function tests *(addresses F1 issue 1, partial)*

The Python is ~2k lines, zero tests. A subset of functions is genuinely pure and testable without infrastructure: `lifecycle_preamble`, `_cmd_str`, `read_secrets` filter logic, `deliver_prompt` content composition (the part before tmux calls). Add pytest, cover those.

**Python-deps-not-installed policy:** `make python-test` detects whether `python3 -m pytest` is available. If not, it prints `"Python tests skipped (install pytest + mypy via 'make setup-dev-python' to enable)"` and exits 0. CI explicitly installs the deps and treats `make python-test` as required. This keeps `make check` working on fresh clones without Python tooling while ensuring CI catches regressions.

**Steps:**

1. Add `runtime/monitor/tests/` with `conftest.py` and `pytest.ini`.
2. Target **Python 3.11+** (matches Debian bookworm in the Docker base image; verify via `python3 --version` inside `yoloai-base`).
3. Add `make python-test` target: detects pytest availability; runs `python3 -m pytest runtime/monitor/tests/ -v` if present, else skips with the message above.
4. Add `make setup-dev-python` target: `pip install -r runtime/monitor/tests/requirements-dev.txt` (pytest, mypy, type stubs).
5. Make `make check` depend on `make python-test`. The Stop hook gate then catches Python regressions (when pytest is installed).
6. Add **type hints to the tested modules** and add a `make python-typecheck` target running `mypy --strict` on those modules. `make python-test` depends on `make python-typecheck`. Type failures fail the build.
7. Write unit tests covering: `lifecycle_preamble`, `_cmd_str`, `deliver_prompt` content composition, `read_secrets` (with a fake filesystem via `tmp_path`), one parse function from `runtime-config.json` reading.

**Acceptance:**
- `runtime/monitor/tests/` exists with ≥5 pure-function unit tests; `make python-test` runs them when pytest is available.
- `make check` invokes `make python-test`; CI runs it with deps installed; failure blocks merge.
- `make python-typecheck` runs `mypy --strict` on the tested modules; failure blocks the build.
- CLAUDE.md mentions both targets and `make setup-dev-python`.

**Size:** S · **Risk:** low (purely additive) · **Blocks:** W4 (which builds on the pytest infra).

### W4 — Python I/O seams + race-coordination tests *(addresses F1 issue 5)*

W3 covers pure functions. The actual race-prone code (tmux orchestration, threading.Event coordination — what the `5a060b9` bug lived in) needs injectable I/O seams to be testable.

**Robust test pattern for thread ordering** — Python's `threading.Event` doesn't expose "is the wait blocked right now." To verify ordering without timing-dependent flakes:

- Each fake-tmux call appends an entry to a shared `call_log: list[tuple[int, str]]` with a monotonically increasing sequence number.
- The test sets up the threads, drives them to completion (e.g. `event.set()`, `thread.join(timeout=5)`), then asserts ordering on the resulting log.
- No `time.sleep()` for synchronization; no "no calls fire while event is unset" assertions that depend on timing. The log captures *what happened*, not *what wasn't happening at moment X*.

**Steps:**

1. Extract `tmux(...)`, `tmux_output(...)`, `setup_tmux_session(...)` into a `tmux_io.py` module with a module-level injectable runner: `set_runner(fn)` swaps `subprocess.run` for a fake in tests.
2. Same for `subprocess.run` callers outside `tmux_io.py` — `read_secrets`, lifecycle commands, gosu/chown invocations.
3. Refactor `run_lifecycle_background` and `launch_agent` to take the coordination object as a parameter.
4. Write the **race-catch demonstration test** using the sequenced-log pattern above: spawn the lifecycle-banner thread with `pane_ready` unset; spawn the agent-launch thread (or directly call the launch logic); set `pane_ready`; join both; assert that all `launch_agent` log entries precede all `lifecycle banner` entries. This test would have caught the `5a060b9` race before commit.
5. At least one more race-prone path gets a similar test (candidates: secrets-passing concurrent with agent launch, status-monitor concurrent with tmux pane-pipe).

**Acceptance:**
- All tmux/subprocess calls in `sandbox-setup.py` and `status-monitor.py` route through injectable runners.
- `test_lifecycle_banner_orders_after_agent_launch` exists and passes; reverting `5a060b9`'s `pane_ready.wait()` call causes it to fail (verified by removing the wait call and confirming the test fails on the log-ordering assertion).
- At least one additional race-coordination test exists using the sequenced-log pattern.

**Size:** M · **Risk:** medium (refactor touches load-bearing I/O) · **Blocks:** nothing, but W6 is more valuable after this.

### W5 — Parametrize integration tests by backend *(addresses F1 issue 3, prerequisite half)*

`internal/cli/integration_test.go` hardcodes `dockerrt.New(...)`. To run it against Podman (or any other backend), the test harness must become backend-agnostic.

**Steps:**

1. Add a backend-resolution helper in `internal/cli/integration_main_test.go` (or `internal/testutil/`) that returns a `runtime.Runtime` based on an env var (`YOLOAI_TEST_BACKEND`, default `docker`).
2. Update `internal/cli/integration_test.go` to use the helper instead of `dockerrt.New`.
3. Verify the existing Docker integration suite still passes.
4. Confirm `TestCLI_StartAfterDone` runs unchanged through the helper.

**Acceptance:**
- No file in `internal/cli/` hardcodes `dockerrt.New` (or backend-specific types) inside test setup.
- `YOLOAI_TEST_BACKEND=docker go test -tags=integration ./internal/cli/` and `YOLOAI_TEST_BACKEND=podman go test -tags=integration ./internal/cli/` both work locally (assuming both backends are installed).

**Size:** M · **Risk:** low (test-side refactor) · **Blocks:** W6.

### W6 — CI matrix expansion *(addresses F1 issue 3, the trivial half)*

After W5, the CI change is small.

**Steps:**

1. Extend `make integration-podman` to run a subset of `./internal/cli/` against Podman (set `YOLOAI_TEST_BACKEND=podman`). Subset should include `TestCLI_StartAfterDone` and other launch/lifecycle tests.
2. Update `.github/workflows/ci.yml`'s `integration-podman` job to invoke the expanded target.

Containerd+Kata coverage in CI is tracked in this plan's Out-of-scope section (needs a Kata-capable runner).

**Acceptance:**
- CI's integration-podman job runs the parametrized `internal/cli/` subset against Podman.
- A breakage in `sandbox-setup.py` that only manifests on Podman fails CI on the Podman job.

**Size:** XS · **Risk:** medium (CI time increases by ~5 min; flake exposure for second backend) · **Blocks:** nothing.

### W7 — Typed errors to neutral package *(addresses F5)*

`runtime/docker`, `runtime/podman`, `runtime/seatbelt`, `runtime/tart` all import `config/` for typed-error constructors — the only direction violation in the dependency graph.

**Steps:**

1. **Verify first.** Grep `yoloai.go`, `yoloai_test.go`, and any external API tests for references to `config.NewXxxError`, `config.UsageError`, etc. If any external consumer touches these, the move is a breaking API change requiring a BREAKING-CHANGES.md entry; if none, the move is internal-only.
2. Based on step 1: if external references exist, add re-exports in `config/` after the move (preserve API surface). If none, do a clean move.
3. Create `internal/yoerrors/` containing `UsageError`, `ConfigError`, `ActiveWorkError`, `DependencyError`, `PlatformError`, `AuthError`, `PermissionError` and their constructors.
4. Update all import sites (`config/`, `runtime/*`, `internal/cli/`, `sandbox/`).
5. Run `make check`.

**Acceptance:**
- `internal/yoerrors/` exists; `config/errors.go` no longer defines typed-error constructors (or contains only re-exports if step 1 found external consumers).
- `grep -r "kstenerud/yoloai/config" runtime/` returns no matches.
- All `errors.Is` / `errors.As` call sites updated.

**Size:** S · **Risk:** low · **Blocks:** nothing.

### W8 — Replace error-text matches with `errors.Is`/`errors.As` *(addresses F8)*

Five brittle `strings.Contains(err.Error(), ...)` sites listed in the audit.

**Steps:**

1. For each site, **run a small reproducer** that triggers the error condition and prints `fmt.Sprintf("%#v\n%T\n%+v", err, err, err)` for the unwrapped chain. Identify the upstream type or sentinel. Some conditions ("address in use" from a containerd shim race) are hard to reproduce in isolation; for those, instrument the existing code path to log the error chain during a known-failing integration run.
2. Likely targets: `errors.Is(err, fs.ErrPermission)` / `errors.Is(err, syscall.EACCES)` for "permission denied"; `errors.Is(err, syscall.EADDRINUSE)` for "address in use". For containerd-shim errors, check the `errdefs` package.
3. If a site has no usable typed target, **acknowledge it explicitly**: wrap the upstream call to convert the textual match into a local sentinel at one chokepoint, then matched via `errors.Is` everywhere else. Document why the text match at the chokepoint is irreducible.
4. Each replacement gets a negative test: feed a wrong-error to the call site, confirm the wrong branch isn't taken.

**Acceptance:**
- All 5 sites listed in the audit replaced or explicitly chokepointed-and-documented.
- Each replacement has a negative test.

**Size:** S · **Risk:** low · **Blocks:** nothing.

### W9 — Configure sloglint *(addresses F9)*

`sloglint` is enabled in `.golangci.yml` but unconfigured.

**Steps:**

1. Add these `sloglint` settings to `.golangci.yml`:
   - `static-msg: true` — message must be a string literal (already true everywhere).
   - `key-naming-case: snake` — locks the existing `event=sandbox.create` style.
   - `forbidden-keys: ["error"]` — forces the canonical `err` key. The wider Go ecosystem (slog stdlib examples, zerolog, zap) standardizes on `err`; the codebase currently mixes both.
2. Run `golangci-lint run`; fix violations (≤10 sites in `runtime/tart/tart.go` and `sandbox/create_prepare.go` per the audit; plus any `"error"` keys to rename to `"err"`).
3. Do **not** add `attr-only: true` — current key-value variadic style is fine and switching is out of scope.

**Acceptance:**
- `.golangci.yml` has the three `sloglint` settings above (and only those).
- `golangci-lint run` passes with no `//nolint:sloglint` suppressions added.
- Every `slog.Info`/`Warn`/`Debug`/`Error` call in non-test code uses `"err"` (not `"error"`) for error attributes.

**Size:** XS · **Risk:** none · **Blocks:** nothing.

### W10 — Close backend-name leaks *(addresses F4, aligned with W11 + soc-refactor.md)*

Three leaks identified.

**Steps:**

1. **`internal/mcpsrv/proxy.go:225`** hardcodes `exec.Command("docker", ...)`. First, read the file to see what it's actually doing. If `docker exec`-style bridging, use existing `Runtime.InteractiveExec`. If something `Runtime` doesn't expose (e.g., raw port forwarding), define a new optional interface `MCPBridge` (W11-compatible pattern), not a method on the core `Runtime`.
2. **`sandbox/create.go:522`** has `m.backend != "tart"` gating the `--runtime` flag. **Do NOT** add a new `BackendCaps` bool — that's the exact pattern `soc-refactor.md` Issue 1 is removing. Instead: move the precondition check into `runtime/tart/tart.go::Create` (returns an error if `--runtime` was set but the backend isn't Tart). The sandbox layer stops knowing about Tart.
3. **`internal/cli/sandbox_bugreport.go:213-222`** switches on backend to call `docker logs` / `podman logs`. Replace the entire switch with `runtime.Runtime.Logs(ctx, name, n)`. If the bugreport needs richer output than `Logs()` returns today, extend `Logs()` once (or add an optional `LogsVerbose` interface) — don't switch on backend name.

**Acceptance:**
- `grep -E '"(docker|podman|tart|seatbelt|containerd)"' internal/mcpsrv/ internal/cli/sandbox_bugreport.go sandbox/create.go` returns no business-logic matches (only legitimate user-facing strings like help text).
- `BackendCaps` did NOT gain a new boolean field as part of this work.
- soc-refactor.md Issue 1's pattern (capability checks as runtime methods, not BackendCaps fields) is what gets followed in step 2.

**Size:** S · **Risk:** low · **Blocks:** nothing, aligned with W11.

### W11 — Split the `Runtime` interface *(addresses F2)*

24-method interface. Backends like Seatbelt return empty/false from many methods. Violates ISP and makes mocking painful.

#### Spike (do first)

Catalog every existing `Capabilities()` and per-method implementation across the 5 backends. Identify which constant facts are truly static (could move to a `BackendDescriptor`) vs which depend on runtime host probing (must stay as interface methods).

**Spike failure path:** if >30% of `Capabilities()` values across the matrix turn out to be dynamic (host-probed at runtime), abort the descriptor-struct refactor and propose an alternative — e.g., a `Metadata()` method on the interface with a default implementation supplied by the registry, parameterized by host environment. Document the finding in `docs/dev/discovered-findings.md` and revise this workstream's design before proceeding.

#### Steps (after spike confirms feasibility)

1. Rename the current `Runtime` interface to `Lifecycle` (or keep `Runtime` — naming TBD). Reduce to ≤12 methods: Create, Start, Stop, Remove, Inspect, Exec, GitExec, InteractiveExec, Prune, Close, Logs, DiagHint.
2. Create `BackendDescriptor` struct holding the static facts: Name, BaseModeName, AgentProvisionedByBackend, SupportedIsolationModes (and `Capabilities` IFF the spike confirms it's static).
3. Backends register `(Factory, Descriptor)` tuples instead of just `Factory`.
4. Convert per-backend adapters to optional interfaces:
   - `TmuxSocketProvider` — seatbelt only.
   - `AttachCommander` — every backend; keep as optional with a default helper.
   - `ResolveCopyMounter` — seatbelt only.
   - `IsolationCapabilityProvider` (RequiredCapabilities, optionally SupportedIsolationModes if not on descriptor).
   - After W1: `PrepareAgentCommand` may disappear from the interface entirely.
5. Update callers. Most call sites only use the lifecycle subset; ensure they take the narrower interface.
6. Optional-interface call sites do `if p, ok := rt.(TmuxSocketProvider); ok { ... }` with a documented fallback when not implemented.

**Abort criterion (pre-merge bailout):** workstream is single-shot. If mid-implementation a backend can't be made to fit the new shape, abandon the branch before merging — don't ship a half-converted interface. The pre-W11 interface is recoverable from VCS. (This is *not* a post-ship rollback; it's a pre-merge decision point.)

**Acceptance:**
- Core interface has ≤12 methods.
- `BackendDescriptor` exists; registry returns `(Factory, Descriptor)`.
- ≥3 optional interfaces; each documented with a call-site fallback pattern.
- All 5 backends compile; all tests pass.
- A new mock for testing `sandbox.Manager` no longer needs to implement methods the test doesn't exercise.

**Size:** L · **Risk:** medium-high · **Blocks:** nothing (but easier after W1 removes `PrepareAgentCommand` from the interface).

### W12 — Sandbox/ carve-up + test file reorganization *(addresses F3, M1)*

33+ files in `sandbox/`; the biggest tests are >1000 lines. Folds the scope of [`soc-refactor.md`](soc-refactor.md) — that plan's Issue 2 (the `create.go` god file) becomes the first step of W12 rather than a separate workstream. soc-refactor.md remains as the detailed design for that step.

**Steps:**

1. **First:** execute `soc-refactor.md` Issues 1–4 in order. Issue 2 (split `create.go`) is the prerequisite for everything downstream.
2. After `create.go` is split, group remaining files:
   - `sandbox/archetype/` — `archetype.go`, `devcontainer.go`, `yoloaiyaml.go`, `vscode.go`, plus their tests.
   - `sandbox/patch/` — `diff.go`, `apply.go`, plus their tests.
   - `sandbox/store/` — `paths.go`, `meta.go`, `sandbox_state.go`, plus their tests.
   - `sandbox/` — `Manager`, lifecycle, create*, clone, inspect, keychain, lock, agent_files.
3. Identify and break any package-cycle introductions (likely candidates: store imports from archetype, patch imports from store). Resolve via interface extraction or moving shared types up.
4. Split test files so no test file dominates its package's production code count. The intent: when a contributor opens a test file, they should see tests relevant to one cohesive set of behaviors, not the whole package. Apply judgement per file rather than a fixed line-count threshold.
5. Update `ARCHITECTURE.md`.

**Acceptance:**
- `sandbox/` has ≥3 subpackages (archetype, patch, store) plus the slimmed parent.
- `create.go` is split per soc-refactor.md Issue 2.
- No `_test.go` file is more than 2× the line count of its corresponding production file.
- `make check` passes.
- `ARCHITECTURE.md` reflects new layout.

**Size:** L (includes soc-refactor.md scope) · **Risk:** medium · **Blocks:** nothing.

### W13 — Smaller items *(addresses F6, audit Section 5)*

Group of independent small wins. Each is its own commit.

- **13a.** Split `internal/cli/commands.go` (690 lines). Move `newNewCmd`, `newLsAliasCmd`, `newLogAliasCmd`, `newExecAliasCmd`, `newCompletionCmd`, `newVersionCmd` to per-command files. **Acceptance:** `commands.go` ≤200 lines, contains only `registerCommands()` and shared helpers.
- **13b.** Split `internal/cli/apply.go` (1068 lines). Three workflows: `apply.go` (entry + shared), `apply_squash.go`, `apply_selective.go`, `apply_export.go`. **Acceptance:** no file >400 lines.
- **13c.** Generic `Wait[T any]` helper in `internal/testutil/wait.go`. **Acceptance:** `WaitForActive` / `WaitForStopped` become one-line wrappers over the generic.
- **13d.** Run `gopls modernize` once. **Acceptance:** one commit applying analyzer-driven idiom updates (`for range n`, `min`/`max`, `slices.Concat`, etc.). Review the diff carefully — reject any changes that are stylistic preferences rather than correctness improvements.

**Size:** XS each · **Risk:** none-to-low · **Blocks:** nothing.

*(`yoloai.Client` fate moved to `OPEN_QUESTIONS.md` #101. `sandbox/setup_test.go` 632-line audit moved to `OPEN_QUESTIONS.md` #102 — both are decisions needing analysis, not refactors.)*

### W14 — Re-audit checkpoint *(new, addresses M4)*

Triggered after Phase 1+2+3 (W1–W6) lands. Verifies the audit findings actually closed; doesn't just trust the workstream completion claims.

**Steps:**

1. **First, capture the audit commands** as scripts in `scripts/audit/`. The original critique relied on Claude Code's Explore subagent; the re-audit shouldn't. Specifically:
   - `scripts/audit/import-graph.sh` — runs the grep+awk pipeline that produced the dependency graph.
   - `scripts/audit/error-handling.sh` — counts `fmt.Errorf` calls, classifies wrapping; lists `strings.Contains(err.Error(), ...)` sites.
   - `scripts/audit/observability.sh` — slog attribute consistency check.
   - `scripts/audit/runtime-interface-shape.sh` — counts methods on `Runtime`, lists optional interfaces.
2. Run the scripts; record output.
3. Confirm F1, F4, F5, F8, F9 are closed and F10 has Tier 1 coverage. F2, F3 remain open (Phase 5 work).
4. Update `architecture-audit-2026-05.md` with a "Status as of <date>" addendum listing what closed and what remains.
5. Incorporate anything in `docs/dev/discovered-findings.md` into the addendum.
6. If new findings emerge, file as `architecture-audit-YYYY-MM.md` (the 2026-05 file is a frozen snapshot).

**Definition of "closed."** An F-finding is `closed` iff every workstream listed as addressing it has its acceptance criteria fully met (verified by running the captured scripts in `scripts/audit/` and comparing output to the original audit's evidence). `partial` means at least one but not all addressing workstreams are complete. `open` means none are.

**Acceptance:**
- `scripts/audit/` exists with at least the four scripts above; each prints structured output suitable for diffing across runs.
- A "Status as of <date>" section appended to `architecture-audit-2026-05.md`.
- Each F-number from the original audit has a current status (closed / open / partial) determined by the rule above.
- `discovered-findings.md` is reviewed; relevant items are folded into the addendum.
- Any net-new findings are filed as a new dated audit, not as edits to the original.

**Size:** S · **Risk:** none · **Blocks:** nothing.

---

## Effort budget summary

| Phase | Workstreams | Rough total |
|---|---|---|
| 1 — Boundary safety | W1a (S), W2 (S) | ~1 week |
| 2 — Python testability | W3 (S), W4 (M) | ~3 weeks |
| 3 — Coverage | W5 (M), W6 (XS) | ~2 weeks |
| 4 — Hygiene | W7 (S), W8 (S), W9 (XS), W10 (S) | ~2 weeks (parallelizable) |
| 5 — Structural | W11 (L), W12 (L) | 6+ weeks |
| 6 — Polish + closure | W13 (5× XS), W14 (S) | ~2 weeks |
| (Follow-up) | W1b (XS) | One release after W1a |

**Total focused work:** 4–5 months. Phase 1+2+3 (the audit's biggest risk reduction) is ~6 weeks. Phases 4 and 6 parallelize into gaps between phases or between feature work.

## Out of scope (explicitly rejected for this plan)

- **Unifying Go and Python into a single binary.** Would require per-architecture builds (linux-amd64, linux-arm64, macos-arm64 for Tart, macos-amd64 for Seatbelt) plus libc variants. The underlying problems (no tests, two sources of truth, no schema, no cross-backend coverage) are addressed by W1–W6.
- **Replacing the dual-dispatch CLI model.** Tracked as `OPEN_QUESTIONS.md` #100 — needs usage data before deciding.
- **Removing `yoloai.Client` public API.** Tracked as `OPEN_QUESTIONS.md` #101 — needs an external-user check.
- **Full JSON Schema validation.** Deferred to W2 Tier 2; revisit only when a concrete semantic-regression bug is observed.
- **containerd in CI.** Out of scope for W6; needs a Kata-capable runner. Future work.

---

*Revised 2026-05-20 after the v2 critique pass (commit `1e9c558`). Key changes: W1 split into W1a (ship gate) + W1b (remove gate) so rollback is honest; W2 documents Tier 1 sufficiency analysis, additive-changes policy, and downgrade hard-fail policy; W3 specifies Python-deps-not-installed behavior; W4 specifies sequenced-log test pattern; W7 verifies external API before deciding on move-vs-re-export; W11 adds spike failure path and renames "rollback" to "abort criterion"; W12 folds soc-refactor.md scope explicitly; W13c (yoloai.Client decision) moved to OPEN_QUESTIONS.md #101; W14 captures audit commands as scripts; backend-idiosyncrasies acceptance fixed; discovered-findings policy added.*
