ABOUTME: Testing philosophy for yoloAI. Confidence over coverage, behaviour
ABOUTME: over implementation, error paths first-class, regression by default,
ABOUTME: test at the right layer, real backends for integration, manual fakes,
ABOUTME: production-code-health over green-bar. Applied across Go unit/integration
ABOUTME: /e2e tiers and the Python pytest surface in runtime/monitor/.

# Testing principles

Testing philosophy for yoloAI. How we decide what to test, where to test it, and what counts as a real test versus a green-bar exercise. Specialised application of `general-principles.md` to the testing surface.

Established in D22 (`../working-notes.md`). Primary-source backing: `../research/principles/testing-principles-research.md`.

## Framing — what testing is for

Tests exist to give the author confidence that the code works for the cases that matter. Coverage is a diagnostic for finding gaps; it is not a goal. A passing test that doesn't tell us the production code is healthy is theatre. A red test that surfaces a real regression is the asset we're building.

yoloAI has three test tiers and a Python surface:

- **Unit tests** (`go test ./...`): pure logic, no I/O, no backend. Fast feedback, isolated cause.
- **Integration tests** (`go test -tags=integration ./...`): require a real backend daemon (Docker, Podman, containerd); test the runtime interface at the boundary.
- **e2e tests** (`test/e2e/` + smoke tests): full stack including tmux + agent. Slow, fragile, but the only layer that catches certain failure modes. Run on developer machines and a CI subset.
- **Python tests** (`runtime/monitor/tests/`, pytest + mypy): the `runtime/monitor/` surface is split into pure functions (W3) and I/O seams (W4) so each can be tested at the right layer.

`make check` is the gate. It runs gofmt, golangci-lint, go mod tidy, Go tests, and Python pytest + mypy. The Stop hook (D20) enforces it before completion.

The cost-vs-benefit framing is the same as the general principles: cost of writing the test + cost of maintaining it vs. damage prevented when the regression would otherwise ship. For a single-author project across five backends, the damage prevented is amplified — a regression in any backend is a debugging session Karl must personally handle.

---

## §1. Confidence over coverage numbers

**Principle.** Coverage is a useful diagnostic for finding *untested* code; it is useless as a target. The question that drives a test isn't "does this push coverage past N%?" — it's "would this fail if the production code regressed in a way the user would notice?"

### Pattern

Threshold: coverage is reviewed (via `make cover`) to find gaps, not to hit a number. Any test added because of a coverage number is suspect; ask whether it would catch a real regression. A test whose answer is no is a test that adds maintenance burden without adding confidence.

### Worked examples

- `make check` does not gate on coverage percentage. The gate is "tests pass + lint passes + types check," not "coverage ≥ X%."
- `make cover` exists (`f7dfcdc`, 2026-03-04) so the author can spot untested code paths, but it doesn't fail the build.
- Commit `545144a` ("Add unit tests to fill coverage gaps across 4 packages") — coverage gaps were filled, but each added test asserts on real behaviour (config parsing, path resolution, error handling). The test count went up because real behaviour was missing tests; the coverage number was the symptom, not the goal.
- Integration tests for the five backends provide confidence that's not visible in a coverage percentage — they test "container created, file accessible, apply succeeded" against real daemons.

### Cost-vs-benefit

Cost of applying: a habit of asking "what does this test prove?" rather than "what does this test cover?" Damage prevented: bloated test suites full of tests that pass but don't catch real failures; CI time burnt on assertions that don't matter; the false sense that high coverage means healthy code.

### Sources

Kent Beck *TDD: By Example* (2002); Martin Fowler "Test Coverage" (martinfowler.com/bliki/TestCoverage.html). Full citations: `../research/principles/testing-principles-research.md §1`.

Originally established in global `CLAUDE.md` §Testing and ratified in D22.

---

## §2. Test behaviour, not implementation

**Principle.** A test asserts on what the code does, not on how it does it. A test that breaks on refactor without a behaviour change is testing implementation; rewrite the test or delete it. The behavioural property is the contract the user (or caller) depends on; that's the property worth defending.

### Pattern

Threshold: write the test against the observable result (return value, state change, error returned), not against the call sequence. If a test asserts "function A was called with arguments X, Y, Z," ask whether that's the behaviour or the mechanism. Behaviour: black-box. Mechanism: white-box, and almost always too coupled.

### Worked examples

- Integration tests for `yoloai start` assert "container is running, agent is ready, prompt was delivered" — not "Docker client `ContainerCreate` was called with these specific arguments."
- The backend-parametrized integration suite (W5, commit `1591b24`, 2026-05-20) runs the same assertions against Docker, Podman, and containerd. The assertions don't know which backend they're hitting; they care that the contract holds.
- Tests for `runtime.Runtime` consumers (above the seam) use a fake backend that implements the contract. The tests would pass against the fake or against real Docker; they don't assert on which methods got called.
- Tests for the `auto_commit_interval` feature (commit `c8c58b0`, 2026-03-03) assert that commits are produced at the expected cadence, not that any specific git command was invoked.

### Cost-vs-benefit

Cost of applying: discipline to ask "is this assertion about behaviour or about mechanism?" at test-writing time. Damage prevented: test suites that explode on every refactor (Sandi Metz's "tests that fight you"); developer reluctance to refactor because the test suite resists; false-negative test failures that say nothing about real correctness.

### Sources

Kent Beck *TDD: By Example* (2002); Martin Fowler "UnitTest" (martinfowler.com/bliki/UnitTest.html); Sandi Metz "The Magic Tricks of Testing" (RailsConf 2013). Full citations: `../research/principles/testing-principles-research.md §2`.

Originally established alongside D14 (pluggable idle detection — multiple rejected approaches forced behavioural tests).

---

## §3. Error paths are first-class

**Principle.** Production incidents happen on the unhappy path. The happy path is the one everyone tested. The error path is the one no one tested because it was "hard to trigger." Make error paths trigger-able and test them by default.

### Pattern

Threshold: every function that can fail has at least one test for each failure mode (or a defensible reason why a failure mode is unreachable). Table-driven tests include error inputs by default. The boundary code (CLI handlers, runtime backends) gets the most error-path coverage because that's where failures originate and where the user sees them.

### Worked examples

- `TestCLI_StartAfterDone` (commit `c10d6eb`) — tests the behaviour when `start` is called on a sandbox that's already finished. The error case.
- Sandbox name validation tests (`internal/sandbox/name_test.go`) cover the path-traversal cases, the empty case, the too-long case, the invalid-characters case. The error inputs are the test set.
- Containerd `GitExec` returns `*runtime.ExecError` on non-zero exit (commit `8749864`, 2026-05-21): the failure-mode contract is tested explicitly. Before the fix, error-path callers received the wrong type and couldn't inspect the error correctly.
- Integration tests include `daemon-not-running`, `sandbox-not-found`, `apply-with-no-changes` cases. These are the failure modes that bite in production.
- The `:rw`-on-dirty-repo warning has a dedicated test (`commit-aware diff and selective apply`, `ca0b8e4`): the warning path is part of the contract.

### Cost-vs-benefit

Cost of applying: write the error-case test alongside the happy-case test. Damage prevented: silent failure modes shipping to production; the "we never tested what happens when X" debugging session; users hitting cryptic errors that the test suite would have caught.

### Sources

Michael Feathers *Working Effectively with Legacy Code* (Prentice Hall, 2004) — seams for error-path testing; Kent Beck *TDD: By Example* (2002) — red-bar first. Full citations: `../research/principles/testing-principles-research.md §3`.

Originally established alongside D6 (symlink resolution before safety checks — the error path *is* the security boundary).

---

## §4. Regression by default

**Principle.** When a bug ships, the test goes in alongside the fix. The test documents "this was wrong, here's the shape of the wrongness." The fix makes the test pass. The test prevents recurrence. Skipping the test means we will rediscover the same bug, possibly with worse consequences.

### Pattern

Threshold: any bug fix is accompanied by a test that would have caught it. The test is in the same PR / commit / change unit as the fix. The exception is reproduction cost too high to be worth automation (rare); in that case, document the manual repro steps in `docs/dev/backend-idiosyncrasies.md` with a symptom-index entry.

### Worked examples

- `internal/cli/files_test.go` (commit `1243d89`, 2026-04-03): regression test for `files put` with missing `files/` directory. The test was written alongside the bug fix.
- Smoke test stop/start fix on Kata VM backends (commit `53b849e`): the flaky test path was fixed and a stable assertion added. Flaky tests don't catch regressions; they train people to ignore red.
- `TestIntegration_FullLifecycle` (commit `028e86d`): exercises the full container lifecycle as a regression guard against the recurring "container vanished" failure modes.
- The containerd `GitExec` typed-error fix (commit `8749864`) shipped with a regression test for the error type contract.
- `standards/GO.md` §Testing states the rule explicitly: "Bug fixes require a regression test."

### Cost-vs-benefit

Cost of applying: a few extra minutes per fix to write the test. Damage prevented: the same bug shipping twice; user trust erosion ("you fixed this, why is it broken again?"); the cumulative debug burden of rediscovering the same root cause.

### Sources

Kent Beck *TDD: By Example* (2002); Martin Fowler "Eradicating Non-Determinism in Tests" (martinfowler.com/articles/nonDeterminism.html, 2011). Full citations: `../research/principles/testing-principles-research.md §4`.

---

## §5. Test at the right layer

**Principle.** For any given behaviour, find the lowest layer where it can be tested with confidence. Pure logic at the unit layer. Backend interaction at the integration layer. Full user workflow at the e2e layer. Tests at higher layers can't replace tests at lower layers because they can't isolate the cause of a failure.

### Pattern

Threshold: ask which layer can prove this behaviour without spurious dependencies. Unit if the behaviour is pure logic. Integration if a backend is required. E2e if tmux + agent + user-visible state are required. Avoid the ice-cream-cone anti-pattern (Fowler) — few unit tests, many e2e tests — because e2e tests are slow, fragile, and bad at diagnosis.

### Worked examples

- **Unit:** path normalization, caret encoding (`encode_test.go`), config key routing (`IsGlobalKey` tests), argument validation (`name_test.go`). No backend, no I/O.
- **Integration:** `runtime.Runtime` implementations against real daemons (`runtime/docker/*_test.go`, `runtime/podman/*_test.go`, `runtime/containerd/*_test.go`). Build tag `integration`; CI runs the Docker subset; Podman CI subset added in W6 (commit `b99b46e`, 2026-05-20).
- **e2e:** `test/e2e/` — full sandbox lifecycle including tmux session and agent launch. Smoke tests use the two-stage sentinel + disk pre-flight (D21).
- **Python:** `runtime/monitor/tests/` — pure functions (W3, commit `0d50c54`, 2026-05-20) tested without spawning processes; I/O seams + race coordination (W4, commit `41561fe`) tested with explicit fixtures.
- **What doesn't go in CI:** Tart e2e (requires Apple Silicon hardware); macOS Seatbelt e2e on Linux CI; full smoke suite (too slow per commit). Documented honestly rather than aspirationally.

### Cost-vs-benefit

Cost of applying: discipline at test-writing time to pick the right layer. Damage prevented: slow CI from over-testing at the e2e layer; under-diagnostic failures from over-testing at the unit layer (mocking everything); the "all tests passed but production broke" failure mode that comes from skipping integration tests.

### Sources

Mike Cohn *Succeeding with Agile* (2009); Martin Fowler "TestPyramid" (martinfowler.com/bliki/TestPyramid.html); Go `testing` documentation (pkg.go.dev/testing); pytest documentation (docs.pytest.org). Full citations: `../research/principles/testing-principles-research.md §5`.

Originally established alongside D19 (W3–W6 of the architecture remediation made each layer testable).

---

## §6. Integration tests hit real backends — no mocking the daemon

**Principle.** Integration tests at the backend boundary use real Docker / Podman / containerd / Tart / Seatbelt instances, not mocks. Mocks of these APIs are records of how yoloAI currently calls the backend; they don't catch behavioural differences between backends or across backend versions. Real backends do.

### Pattern

Threshold: any test that crosses the `runtime.Runtime` boundary requires a real backend instance. Skip with `t.Skipf` if the backend isn't available locally; mark with the `integration` build tag so unit-test runs don't require backends. Cross-backend tests are parametrised over the backend (W5), not duplicated per backend.

### Worked examples

- All `runtime/*/integration_test.go` files require a real daemon. The Docker tests skip if `docker info` fails; same for Podman and containerd.
- W6 (commit `b99b46e`, 2026-05-20): Podman CI path runs a CLI lifecycle subset against a real Podman daemon. Catches behavioural differences from Docker that a mock would miss (rootless UID mapping, HOME directory differences, tmux exec user — all of which produced real bugs visible in commits `13c58bd`, `ce8abb0`, `8ce5ff7`, `214c32c`).
- The runtime registry tests (`runtime/registry/*_test.go`, W11 step 4 commit `1f4457c`) test the registration contract, not the implementations — that's the right layer for the registry; implementations are tested separately against real backends.
- `TestIntegration_FullLifecycle` (commit `028e86d`) is the canonical lifecycle integration test. It creates a sandbox, exercises start/stop/restart, applies a patch, and destroys it — all against a real backend.

### Cost-vs-benefit

Cost of applying: integration tests are slower than unit tests and require backends to be available. Damage prevented: behavioural drift between backends silently breaking yoloAI on one of them; mock test suites that pass while the production binary fails; the false confidence of "all green" against a fake.

### Sources

J.B. Rainsberger "Integrated Tests Are A Scam" (jbrains.ca, 2009) — read carefully; Rainsberger argues against integration tests as a *replacement* for unit tests, not against them at the integration boundary; Martin Fowler "Mocks Aren't Stubs" (martinfowler.com/articles/mocksArentStubs.html, 2007/2014). Full citations: `../research/principles/testing-principles-research.md §6`.

Originally established alongside D7 (pluggable runtime interface required tests at the seam).

---

## §7. The goal is healthy production code, not passing tests

**Principle.** A passing test is an artefact of the real goal: healthy production code. When a test fails, the question is not "how do I make this green?" — it's:

1. *Is the production code working properly according to its mission?*
2. *Is the production code even necessary in the grand scheme of the project? Should that production code and its test even exist?*
3. *Is there a better way to accomplish what the production code is attempting to do?*

This is verbatim from global `~/.claude/CLAUDE.md` §Testing and adopted as a yoloAI rule.

### Pattern

When a test goes red: stop and ask the three questions before changing anything. If the production behaviour is correct and the test is out of date, update or delete the test (after confirming question 2). If the production behaviour is wrong, fix the production code. If the production code is unnecessary, delete both.

The trap is to default to "make the test pass" without asking which thing was wrong. This produces tests that lock in bad behaviour or production code that exists only to satisfy a test.

### Worked examples

- Commit `8932f95` ("Fix stale TestCLI_Log integration test") and `13449a1` ("Fix TestCLI_LsJSON to match new ls --json output format") — the tests were updated because the production behaviour deliberately changed. The fix was in the test, not the production code; the test was honest about its staleness.
- Commit `f6c0aba` ("Fix TestE2E_JSONLs to match new ls --json output format") — same shape; the JSON output format was the contract being changed; the e2e test now asserts on the new contract.
- W5 parametrize-by-backend (commit `1591b24`, 2026-05-20): the existing per-backend duplicate tests were collapsed into a parametrised suite. The motivation was healthier production code structure, not test coverage.
- Global `CLAUDE.md` §Testing: "When reworking tests due to changed functionality, first check if the test still adds value."

### Cost-vs-benefit

Cost of applying: pause to ask the three questions when a test fails. Damage prevented: tests that lock in obsolete behaviour; production code that exists only to satisfy outdated tests; the lost opportunity to delete code that shouldn't exist.

### Sources

Global `/home/karl/.claude/CLAUDE.md` §Testing; Kent Beck *TDD: By Example* (2002); Martin Fowler refactoring writing. Full citations: `../research/principles/testing-principles-research.md §7`.

Originally established in global CLAUDE.md; ratified for yoloAI in D22.

---

## §8. Manual fakes over mock libraries

**Principle.** When the unit layer needs a substitute for a collaborator, write a hand-rolled fake that implements the interface, not a generated mock that records call sequences. Fakes test behaviour; mocks test mechanics. Behaviour tests survive refactor; mechanism tests break.

### Pattern

Threshold: define the interface at the consumer's site (Go convention — "accept interfaces, return structs"). The interface is small (only the methods the consumer uses). A fake implementing that interface is cheap to write — usually a struct with a few fields and a few methods. No mock framework, no code generation, no test-time configuration of expected calls.

### Worked examples

- `runtime.Runtime` is the canonical interface. The current codebase tests it primarily at the *integration* layer against real daemons (`runtime/<backend>/integration_test.go`, build tag `integration`); a dedicated `runtime.Fake` for unit-layer tests above the seam does not yet exist. The principle still applies: when a unit test does need a `runtime.Runtime` substitute, write a hand-rolled fake (struct implementing the interface with predetermined results), not a generated mock that records call sequences.
- `standards/GO.md` §Testing: "Mocking: define interfaces at the consumption site, not the implementation site. Mock via interface satisfaction."
- `standards/GO.md` §Code Organization Patterns: "Accept interfaces, return structs — define interfaces at the point of consumption, not alongside the implementation."
- No `gomock` / `mockery` / `mockgen` in `go.mod`. Verified by `grep` against the lockfile.
- `testify/assert` is used for assertion sugar; `testify/mock` is *not* used. The split is deliberate.

### Cost-vs-benefit

Cost of applying: a few lines of code to write each fake. Damage prevented: generated-mock test suites that break on every refactor; tests that assert on call sequences (mechanism) rather than results (behaviour); the maintenance overhead of keeping mocks in sync with interface changes.

### Sources

Martin Fowler "Mocks Aren't Stubs" (martinfowler.com/articles/mocksArentStubs.html, 2007/2014); Michael Feathers *Working Effectively with Legacy Code* (Prentice Hall, 2004) — the seam model; Go `testing` documentation; Effective Go (go.dev/doc/effective_go) §Interfaces. Full citations: `../research/principles/testing-principles-research.md §8`.

Originally established in `standards/GO.md` §Testing; ratified in D22.

---

## §9. Tests that catch new failure modes earn their place

**Principle.** A test that surfaces a previously-invisible failure mode is worth more than a test that adds coverage on a path that already worked. When a confusing failure has been diagnosed, add the test that would have made it un-confusing.

### Pattern

Threshold: every failure mode observed in production (or in development if it would otherwise have shipped) earns a test. The test runs upstream of the symptom — pre-flight checks, contract assertions, behaviour assertions on the boundary that previously hid the cause. The test's job is "transform an inscrutable symptom into an actionable error."

### Worked examples

- **Disk pre-flight + two-stage smoke sentinel** (D21, commits `0d8d650`, `d894f00`, 2026-05-21): a containerd-VM smoke failure presented as "agent idle 9s+" with no useful diagnostic. The cause was ENOSPC. The new pre-flight check refuses upfront with a clear ENOSPC message; the two-stage sentinel distinguishes "test never started" from "test started but didn't finish." The smoke suite now surfaces disk pressure as the first failure, not the last.
- **Containerd typed-error contract** (commit `8749864`, 2026-05-21): an error path was behaving inconsistently across backends. The regression test asserts the error type contract.
- **`files put` regression test** (commit `1243d89`, 2026-04-03): a missing-directory failure had unhelpful error output. The test captures the failure mode and verifies clean error handling.
- **Memory entry**: `project_smoke_disk_pressure.md` documents the symptom-to-test trail explicitly: "Smoke fails on containerd-vm but not docker → check `df -h /` first; ENOSPC manifests as 'agent idle 9s+' not a clear error."

### Cost-vs-benefit

Cost of applying: writing the test alongside the diagnostic. Damage prevented: rediscovering the same confusing failure mode; users hitting the same inscrutable symptom; the recurring debug session that ends with "oh right, it was disk space again."

### Sources

Kent Beck *TDD: By Example* (2002) — bug-then-test discipline; project `CLAUDE.md` §Recording new idiosyncrasies; D21. Full citations: `../research/principles/testing-principles-research.md §9`.

Originally established in D21.

---

## §10. Inject the seam; don't manipulate the process

**Principle.** A unit test steers library behaviour by constructing the explicit inputs the code reads — a `config.Layout` (DataDir, HomeDir, HostUID/GID, Env), an injected `io.Reader` / `io.Writer` — not by mutating global process state (`t.Setenv("HOME", …)`, swapping `os.Stdin`). After the §12 no-ambient-configuration work (`development-principles.md §12`), library code reads its configuration from the `config.Layout` it is handed; a test that sets `HOME` to steer a yoloAI code path is manipulating a global the code no longer reads — dead scaffolding that also forbids `t.Parallel`.

### Pattern

Threshold: if the behaviour under test is yoloAI code, give it explicit inputs:

- Build a `config.Layout` (or the package's `testLayout` / `newTestEngine` helper) with explicit fields and pass it via `WithLayout`. The library reads the Layout; no `HOME` is involved.
- For stdin-driven paths, pass an `io.Reader` (e.g. `strings.NewReader(...)`); do not swap `os.Stdin`.
- For `${VAR}` expansion, set `layout.Env`; do not `t.Setenv`.

The **one** legitimate `t.Setenv("HOME", …)` is isolating a HOME-reading *subprocess*: `git` reads `$HOME/.gitconfig` and `$HOME/.config/git`, so tests that spawn real git (`internal/workspace/*_test.go`, `internal/sandbox/patch/*_test.go`) set `HOME` to a temp dir to shield the test from the developer's git config. That swap is load-bearing — keep it (equivalently, `GIT_CONFIG_GLOBAL`). The other legitimate case is a test that deliberately exercises the CLI's `cliutil.Layout()` ambient-`$HOME` fallback.

### Worked examples

- `internal/sandbox` unit tests build `config.NewLayout(filepath.Join(t.TempDir(), ".yoloai"))` and pass it through `WithLayout`; the `HOME` swap they once carried is vestigial after §12 and removed in D23's cleanup.
- `ReadPrompt` takes an `io.Reader`; `TestReadPrompt_StdinDash` supplies `strings.NewReader("…")` instead of swapping the global `os.Stdin`.
- `${VAR}` config-expansion tests pass an explicit env map / set `layout.Env` (`internal/config/config_test.go`, `pathutil_test.go`) rather than `t.Setenv`.
- Load-bearing counter-example: `internal/sandbox/patch/apply_test.go` keeps `t.Setenv("HOME", tmpDir)` because it runs real `git`; removing it would let the developer's `.gitconfig` leak into the test.

### Cost-vs-benefit

Cost: construct an explicit Layout/reader instead of one `t.Setenv` line (usually a shared helper). Damage prevented: tests coupled to process-global state; the silent `t.Parallel` lockout that `t.Setenv` imposes; cross-test interference; the misleading signal that a test "needs" `HOME` when the code under test never reads it.

### Sources

`development-principles.md §12` (no ambient configuration); Go `testing` docs (`t.Setenv` forbids `t.Parallel`). D23.

---

# Common over-generalisations to avoid

The cost-vs-benefit discipline rejects testing-principle-shaped statements that don't pay off at yoloAI's scale.

| Over-generalisation                          | Why yoloAI rejects                                                                                                                                                                                                                                                                                                  |
| -------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **100% coverage as a target**                | §1 explicitly rejects this. Coverage is a diagnostic, not a target. Hitting 100% is achievable with low-quality tests that pass without proving anything — the failure mode Fowler names directly.                                                                                                                  |
| **Mock everything for "isolation"**          | §6 + §8 reject this. Mocks of backend APIs are interaction tests; they couple to mechanism and break on refactor. Use manual fakes at the unit layer; use real backends at the integration layer.                                                                                                                  |
| **TDD as a religion**                        | Tests-first is a useful discipline but not a project rule. yoloAI accepts test-after-fix when the bug repro is established by manual reproduction. The Kent Beck discipline is the spirit; rigid TDD ceremony is not. (§4 + §7.)                                                                                   |
| **CI must run every test**                   | Tart e2e needs Apple Silicon hardware; full smoke suites are too slow per commit. CI runs unit + Docker integration + Podman lifecycle subset; the rest runs on dev machines. §5 — test at the right layer applies to the *CI* surface too. Honest scoping beats aspirational coverage.                          |
| **A flaky test is better than no test**      | Not necessarily — §4. Flaky tests train developers to ignore red; that erodes the regression-by-default discipline. A flaky test either gets fixed or removed; living with it is the worst option.                                                                                                                  |
| **Test the framework / library / stdlib**    | A test that asserts `len([]int{1,2,3}) == 3` tests Go, not yoloAI. Same for `json.Marshal` round-trip on a struct with no custom marshalling. These tests pass forever and prove nothing about yoloAI. §1 — confidence over coverage.                                                                              |
| **Integration test as the only test**        | §5 — the ice-cream-cone anti-pattern. Integration tests are slow and bad at diagnosis. They're necessary at the backend boundary; they're not a replacement for unit tests at the unit boundary.                                                                                                                   |
| **No regression test unless reproducible**   | The test belongs alongside the fix. If the bug isn't deterministically reproducible, that's the test's first job — make it reproducible. §4. Truly non-deterministic failures (timing-sensitive races) get documented in `backend-idiosyncrasies.md` instead, but that's the exception, not the default.            |
| **Swap `HOME`/env to steer the code under test** | §10 rejects this. Post-§12, library code reads the injected `config.Layout`, not the process env — a `HOME` swap is dead scaffolding that also forbids `t.Parallel`. Env swaps are legitimate *only* to isolate a HOME-reading subprocess (git → `~/.gitconfig`) or to exercise the `cliutil` ambient fallback.              |

---

## Closing note

Testing is the safety net that makes refactoring possible. Most of the actual code-improvement work in yoloAI's architecture remediation (W1–W14) was unblocked by tests at the right layer: the W11 runtime-registry refactor would have been terrifying without the integration tests at the backend boundary; the W12 sandbox carve-out would have been terrifying without the unit tests inside the domain code. Tests aren't there to make CI green; they're there so future-Karl (or a future contributor, or an AI agent) can change the code without breaking it.

The specialised tie-ins:

- `development-principles.md` describes how the production code should be structured to make tests possible (seams, interfaces, small functions). Both docs reinforce each other.
- `security-principles.md` describes what the integration and e2e tiers must cover for the security-relevant paths (capability grants, dangerous-directory refusal, credential injection).
- `general-principles.md §5 (blast radius)` and `§9 (surface failures honestly)` both manifest at the testing layer: the disk pre-flight + smoke sentinel pattern is testing applied to bounded blast radius and honest diagnostics.
