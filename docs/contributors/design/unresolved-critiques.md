<!-- ABOUTME: Active queue of open design critiques for yoloAI. Resolved items drain to -->
<!-- ABOUTME: resolved-critiques.md; deferred to deferred-critiques.md; abandoned to abandoned-critiques.md. -->

# Open critiques

Active design critiques awaiting action. Each is drained to one of three co-located sinks once
settled: [`resolved-critiques.md`](resolved-critiques.md) (applied),
[`deferred-critiques.md`](deferred-critiques.md) (parked with a `Trigger:`), or
[`abandoned-critiques.md`](abandoned-critiques.md) (dropped with a `Why:`). Keep only live items
here — resolved entries belong in the sink, not as stubs.

> The **2026-05-30 Post-F1-Close round** is fully drained: G1/G2/G3/G4/G5/G6/G8 →
> [`resolved-critiques.md`](resolved-critiques.md); G7 (extension residue) →
> [`abandoned-critiques.md`](abandoned-critiques.md) (D66); the D53 read-model reshape closed with
> commit `2916e24`; carried-forward findings F6/F7/F9 done 2026-06-01.

> The **2026-06-03 Public-API "right reasons" round (A1–A4)** is fully drained: A1 (mirror-vs-alias)
> and A2/A3 (surface split on backend-liveness; `SystemClient` junk drawer; homeless agent noun) →
> [`resolved-critiques.md`](resolved-critiques.md) (A2/A3 implemented C1–C4, D67); A4 (public
> struct-tags premise) → [`abandoned-critiques.md`](abandoned-critiques.md), with the genuine CLI
> `--json` residual tracked as [`unresolved-findings.md`](unresolved-findings.md) DF17.

> The **2026-06-03 Internal-code round (IC1–IC16)** is fully drained (2026-06-04, one commit per
> item on `layering-refactor`): IC1–IC15 (incl. IC14's `Info`→`SandboxInfo` rename, applied once the
> branch was confirmed to be a cumulative breaking delta vs `main` already) →
> [`resolved-critiques.md`](resolved-critiques.md); IC16 (host-git wrappers) plus the IC14/IC15
> won't-do sub-points → [`abandoned-critiques.md`](abandoned-critiques.md). Recurring lesson:
> IC12/IC15/IC16 were all "looks dead/dup" findings that proved load-bearing on a closer read.

> The **2026-06-04 Post-D67/D68 public-surface residue round (R1–R6)** is fully drained to
> [`resolved-critiques.md`](resolved-critiques.md): R1 (stranded `Clone` godoc), R2 (stale pre-D68
> field names in doc comments), R3 (redundant `BackendType` identity casts), R4 (`Files()` missing
> from the `Sandbox` sub-handle list), R5 (`Agent().AgentLog()`→`TerminalLog()` de-stutter) all
> applied on `layering-refactor`; R6 (`.Type` vs `.BackendType` field-name convention) documented as a
> clarification under D68. `make check` green.

---

## 2026-06-04 Testing-critique round (T1–T15)

Critique of the test suite (168 Go test files, ~33.6k LOC across the 4 tiers: untagged unit,
`//go:build integration`, `//go:build e2e`, Python smoke). Healthy on the §6/§7/§8 axes (real
backends, no daemon mocks, no mock libraries, manual fakes). Weaknesses cluster in three areas:
cross-backend/e2e **duplication** instead of parametrization, **error-path coverage** lagging behind
the by-design ~100% line coverage, and a few **mis-placed/over-gated** tests dragging the unit tier
toward an ice-cream cone.

### Placement / gating

- **T1 — `patch/{apply,diff}_test.go` gratuitously Docker-gated.** `getTestRuntime(t)`
  (`apply_test.go:27`) forces a Docker daemon, but in `:copy` mode the runtime is never used —
  diff/apply shell out to host git via `hostGitExec`. ~40 tests should run in the **unit** suite
  with no daemon. (Real git here is correctly *not* faked — they test git plumbing, §6/§8.)
  *Fix:* drop the gate for the `:copy` paths; keep it only where a live runtime is genuinely
  exercised (overlay).
- **T3 — `tart/stop_integration_test.go` misleadingly named.** Flagged as "missing
  `//go:build integration`", but a closer read shows the opposite: it uses a *fake* tart binary, no
  real VM/daemon, bounded 200ms timeouts, and explicitly reasons about the *unit* suite budget
  (line 75). Gating it behind `integration` would wrongly force macOS+AppleSilicon+tart for a test
  that needs none. The package is already unix-only (production `build.go`/`tart.go` use
  unconstrained unix syscalls), so Windows portability is moot. *Fix (applied):* rename file to
  `stop_escalation_test.go` and correct its ABOUTME from "Integration test" → "Unit test"; keep it
  in the unit suite where it belongs.

### Duplication

- **T2 — Runtime backend triplets not parametrized (W5 violation).**
  `docker/integration_test.go` and `podman/integration_test.go` are line-for-line twins (~15
  identical tests); containerd is a third copy; Seatbelt/Tart partial 4th/5th. *Fix:* one
  parametrized integration suite keyed on a backend table.
- **T4 — E2E ice-cream-cone.** `test/e2e/{workflow,error,json}_test.go` re-run what
  `internal/cli/integration_test.go` already covers (`TestE2E_NewDiffApplyDestroy` ≈
  `TestCLI_NewAndDestroy`+`TestCLI_Diff`; `TestE2E_JSONLs` ≈ `TestCLI_LsJSON`). Exit-code/help e2e
  tests (`error_test.go:43,49`) build the whole binary for what a unit test covers. *Fix:* trim e2e
  to the JSON-output-contract tests it uniquely justifies; let the cli integration tier own workflow
  coverage.
- **T5 — Bugreport tested at 3 tiers** (unit `writer_test.go` + integration `:318,366` + e2e
  `:69,111`) plus e2e self-duplication (unsafe vs safe). *Refined on inspection:* the e2e tests are
  NOT pure copies — they drive the `--bugreport` global flag through the real `Execute()` wrapper
  (which writes the flag-only Live-log/Exit-code sections via `finalizeBugReport`, root.go:68),
  whereas the integration pair drives the in-process `sandbox … bugreport` subcommand (which the
  `runCLI` helper routes around `Execute()`, so it cannot exercise those sections). The genuine
  redundancy is the safe/unsafe section-redaction matrix — owned by the ~50 unit tests and
  re-covered by the integration subcommand pair. *Fix (applied):* collapse the two e2e tests into a
  single flag-path smoke asserting only the flag-unique markers (Live log / Exit code); keep the
  integration subcommand pair as the command-path coverage; the matrix stays unit-owned.
- **T6 — "Sandbox not found" tested 3× in the root package:** `TestClient_Sandbox_NotFound`
  (sandbox_test.go), `TestClient_Sandbox_NotFoundHandle` (workdir_test.go),
  `TestSandbox_MissingReturnsNotFound` (system_test.go). *Fix:* keep one canonical case.
- **T10 — Round-trip / literal-dup tests.** `store/sandbox_state_test.go:31`/`:23`,
  `store/environment_test.go` field-subset variants redundant with `SaveLoadRoundTrip:20`,
  `provision_test.go:19`/`:37`, diff covered in both `diff_test.go` and `integration_test.go:74`.
  Round-trips prove `encoding/json` works, not our state logic. *Fix (applied):* enriched
  `TestMeta_SaveLoadRoundTrip` to cover every persisted field (Resources/Principal/NetworkAllow/Ports)
  and deleted the four subset round-trips (`WithPortsAndNetwork`/`NetworkAllowRoundTrip`/
  `ResourcesRoundTrip`/`PrincipalRoundTrip`) — a full-struct `assert.Equal` subsumes them; deleted
  `TestSandboxState_FalseValue` (a flipped-bool re-run of `_Roundtrip`; `_MissingFile`/`_InvalidJSON`
  keep the real branch coverage); deleted `TestHasAnyAPIKey_HostEnv` (a literal dup of `_Set` — both
  pass an `ANTHROPIC_API_KEY` host-env map to the claude agent and expect true). Kept the distinct-logic
  tests (omitempty, version stamping, migration, version-too-new). The diff "double-coverage" was
  assessed and KEPT: `TestCLI_Diff` is the CLI command-wiring smoke (integration tier), the
  `patch/diff_test.go` cases are algorithm branch coverage (unit tier) — legitimate pyramid, not dup.

### Coverage gaps (behind ~100% line coverage)

- **T12 — `new.go:118-204` usage-error paths untested at any tier.**
  `parseNewCmdPositional`/`resolveNewCmdOptions`: name required, workdir required, `--json`+`--attach`
  conflict, `--port`+`--network-none` incompatible, invalid port/env. Richest untested surface; pure
  table-driven unit tests, no binary build. *Fix:* add them.
- **T13 — Error branches second-class despite §3.** Overlay diff/apply error paths only at
  integration tier; `Reset` asserted as `_, _ = Reset(...)`; `HasUncommittedChanges` ExecError
  branch untested; dead-daemon-mid-op, image-missing, exec-on-stopped-container, prune-failure
  unhit; **Seatbelt and Tart have no real run coverage at all.** *Fix:* promote error paths to
  first-class assertions where cheap; track the backend run-coverage gaps separately.

### Low-value / misleading tests

- **T9 — Tautologies / no-assert tests.** `runtime/.../names_test.go:8-24` (`BackendDocker ==
  "docker"`); containerd tests that call a method and assert nothing (`:73-105`, `:186-198`);
  `containerd_test.go` re-implements `context`/`isWSL2` and tests the re-implementation. *Fix
  (applied):* converted to real behavioral assertions rather than blanket deletion.
  `names_test.go` was KEPT and reframed — the `BackendType` constants are the on-disk wire format
  (environment.json `backend`, config `backend:`), so the test is a deliberate golden-value
  serialization guard, not a tautology; comment now says so. `TestWithNamespace` strengthened from
  "ctx != background" to assert `namespaces.Namespace(ctx) == "yoloai"`. The `isWSL2` trio
  (no-assert smoke + two inline reimplementations that asserted on stdlib `strings.Contains`, not
  production) was replaced: extracted a pure `procVersionIsWSL2([]byte) bool` classifier from
  `isWSL2` and table-test it directly (wsl2 / generic / empty), giving real branch coverage of the
  production logic. `TestKataConfigPath` deleted — `kataConfigPath(_ string)` ignores its argument
  and always returns `""`, so a three-input parametrized test implied a non-existent
  input-dependence; the rationale already lives in the function's doc comment.
- **T11 — Assert-only-error tests** that can't distinguish "feature works" from "daemon absent":
  `e2e/destroy_test.go:22,39`, the tart stop tests. They pass whether or not the thing works. *Fix:*
  gate on a backend probe and assert real post-conditions, or drop.

### Cosmetic / hygiene

- **T7 — Zero `t.Parallel()` across all 168 files.** §10/§12 (injected seams, no ambient process
  state) specifically *enable* parallelism; nothing uses it. *Fix:* adopt on the pure-logic unit
  tier; run under `-race` to also exercise the D67 `ensureRuntime` once-guard.
- **T8 — ~30 vestigial `t.Setenv("HOME")` sites** (create_test.go:26,55; engine_test.go:109-225;
  store/lock_test.go). Not the §12-dangerous variant (lib uses injected layout) but dead ceremony;
  `status_test.go:26` shows the correct no-HOME pattern. *Fix:* remove.
- **T14 — `public_api_test.go` misnamed.** It is a go/packages static fence against internal-type
  leaks (`TestPublicAPI_NoInternalLeaks`), not behavioral API tests. *Fix:* rename to reflect the
  fence (e.g. `internal_leak_fence_test.go`).
- **T15 — Stale `SystemClient` test names post-D67.** `system_test.go`:
  `TestSystemClient_Info`, `TestSystemClient_ValidateSandboxName`,
  `TestSystemClient_ListAcrossBackends_Empty` — production renamed `SystemClient`→`System`, tests
  didn't. *Fix:* rename to `TestSystem_*`.

**Fix order:** T3 (tag-fix) → T1 (un-gate patch) → T15 (stale names) → T12 (new.go usage errors) →
T8/T14 (hygiene) → T6/T5/T10 (dedup) → T9/T11 (low-value) → T2 (parametrize triplets, largest) →
T4 (e2e trim) → T13 (error-path coverage).

**Progress (2026-06-04, `layering-refactor`, `make check` green):**
- ✅ **T3** — renamed `stop_integration_test.go`→`stop_escalation_test.go` + ABOUTME (827bfa5).
- ✅ **T1** — `getTestRuntime`→`hostGitRuntime()` (nil); ~67 patch tests off Docker (30d50e7).
- ✅ **T15** — `TestSystemClient_*`→`TestSystem_*` in system_test.go + discovery_test.go (9addc32).
- ✅ **T12** — added new.go usage-error unit tests (parse positional / flag conflicts / port / env)
  (d36c04c).
- ✅ **T14** — `public_api_test.go`→`internal_leak_fence_test.go` (b9ab232).
- ✅ **T8** — inject layout directly instead of steering it through `$HOME` (1541d37).
- ✅ **T6** — collapse three duplicate sandbox-not-found tests to one (743e23a).
- ✅ **T5** — collapse two e2e bugreport tests to one flag-path smoke; matrix stays unit-owned.
- ✅ **T10** — enrich `SaveLoadRoundTrip` + delete 4 subset round-trips; delete `SandboxState_FalseValue`
  + `HasAnyAPIKey_HostEnv` literal dups; keep CLI-diff smoke (legit tier separation).
- ✅ **T9** — extract `procVersionIsWSL2` + table-test it (replaces the 3 reimpl/no-assert isWSL2
  tests); strengthen `TestWithNamespace`; delete misleading `TestKataConfigPath`; keep+reframe
  `names_test.go` as a wire-format guard.
- ⏳ Remaining: T11 (low-value); T2, T4, T13 (large/judgement);
  T7 (broad `t.Parallel` adoption — partially seeded in new_test.go).
