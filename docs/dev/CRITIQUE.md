# Critique

## Previous Critique (2026-04-01, round 6): Smoke Test V2 Internal Consistency — Resolved

All issues addressed in `docs/dev/plans/smoke-test-v2.md`:

- #1: T4 (isolation_check) added to both base and full tier test lists
- #2: Summary reworded — clone distinguished as "restrict to full tier" vs removed tests "moved to integration"
- #3: Confidence statement split by tier: Docker (integration/PR gate), docker+VM (smoke base/nightly), full matrix (smoke full/pre-release)
- #4: Full tier definition now notes "isolation_check on container backends only"
- #5: "Future (from known gaps)" subsection added to implementation status with 6 test suggestions

## Previous Critique (2026-04-01, round 5): Smoke Test V2 Enterprise Reliability Gaps — Resolved

All issues addressed in `docs/dev/plans/smoke-test-v2.md`:

- #1: Status monitor accuracy added to Known Gaps with suggested first test (TestMonitor_HookDetector)
- #2: Cleanup-on-error paths added to Known Gaps with focused test suggestion (orphan detection via system prune)
- #3: Concurrent sandbox creation rewritten from "future improvement" to "known crash path" with reference to backend-idiosyncrasies.md; file lock guard suggested
- #4: Backend workaround regression coverage added to Known Gaps with prioritized subset
- #5: Credential injection "done" status scoped to happy path; noted defers handle failure path
- #6: gVisor permission mode detection added to Known Gaps; standard-mode assertion suggested for CI
- #7: Confidence statement section added — explicitly lists what the suite does and does not verify
- #8: T2 prompt fragility documented with three mitigations (minimal prompt, shell agent fallback, weaker assertion)

## Previous Critique (2026-03-31, round 4): Smoke Test V2 Correctness & Consistency — Resolved

All issues addressed:

- #1: Makefile reverted to use `--limited` (base) and bare invocation (full) — flags that actually exist in smoke_test.py
- #2: Overlay skip guard rewritten — attempt creation and skip on failure instead of euid check (mount runs inside container)
- #3: Plan's Makefile snippet updated to show both current (`--limited`) and future (`--full`) forms with `$(SMOKE_ARGS)`
- #4: `ctx.alloc_name()` → `t.sandbox(label)` (appends to `ctx.sandboxes`)
- #5: Base tier timing estimate raised from 15 to 30 minutes; escape hatch documented (T2 docker-only in base tier)
- #6: Nightly alerting section now shows correct `if: failure()` syntax with example YAML

## Previous Critique (2026-03-31, round 3): Smoke Test V2 Plan vs Reality — Resolved

All issues addressed:

- #1: Makefile `$(SMOKE_ARGS)` passthrough added to both smoketest targets
- #2: Plan now has preamble noting it's a design spec; T2 changes listed as pending in implementation status
- #3: `--limited` removal listed as pending in implementation status; plan preamble clarifies spec vs reality
- #4: T4 section now clarifies relationship to `TestIntegration_NetworkIsolation` and when T4 can be deferred
- #5: `TestIntegration_CredentialInjection` replaced fixed 3s sleep with polling loop (15s timeout)
- #6: Convention note added — each test gets its own project dir via `t.project()`, no collision risk
- #7: JUnit XML section now specifies incremental writing + `atexit` for crash resilience
- #8: Nightly failure alerting section added (GitHub notifications, API key rotation, Slack webhook)
- #9: Implementation status section added with done/pending checklists

## Previous Critique (2026-03-31, round 2): Smoke Test V2 Enterprise Posture — Resolved

All issues addressed:

- #1: T4 integration test asserts `runtime-config.json` has `network_isolated: true`
- #2: T4 scoped to container backends only (Docker, Podman, containerd-vm); skip on Tart/Seatbelt
- #3: T2 prompt writes to work copy (`echo smoke > output.txt && touch <exdir>/done`), not just exchange dir
- #4: Makefile targets updated (base uses `--limited`, full uses bare invocation; will flip when `--full` lands)
- #5: `--limited` removal designed; base+skip is the default, `--full` is the only flag (pending implementation)
- #6: `TestIntegration_ReadOnlyMountVerified` — exec write to RO aux dir fails inside container
- #7: `TestIntegration_CredentialInjection` — prompt writes env var to file, exec reads it; host cleanup verified
- #8: CAP_SYS_ADMIN containment gap acknowledged in Known Gaps section
- #9: JUnit XML output spec added (`--junit <path>` flag)
- #10: Nightly `nightly-audit` CI job runs govulncheck + hadolint + actionlint
- #11: Tier ownership table added (which tier answers which question)
- #12: Concurrent sandbox testing noted as known gap with suggested approach

## Previous Critique (2026-03-31, round 1): Smoke Test V2 — Resolved

See round 2 for superseding issues. Round 1 items (security boundary tests, WaitForStatus
abstraction, `--debug` for full tier, timing targets, CI story, capability detection,
clone-on-VM gap) were all addressed in the first pass.
