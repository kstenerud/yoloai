# Critique — 2026-05-30 Post-F1-Close round (remaining open findings)

## Summary

This round opened by exposing that the "Layer-1 FULLY COMPLETE / F1 closed" claim (D52) was
**false** — `TestPublicAPI_NoInternalLeaks` was structurally blind to type aliases, so it ran
green while `yoloai.Info.Meta` leaked the internal `store.Meta` iceberg. That central finding
(**G1**) and its consumption-side twins (**G2** import fence, **G7** missing public verbs) are
now **RESOLVED** — the detector descends through aliases, `f1KnownLeaks` is empty *and* honest,
`store.Meta` was carved to the public `Environment` read-model, and the depguard fence covers
the whole `internal/sandbox`+`internal/runtime` subtrees (see
[resolved-critiques.md](resolved-critiques.md) for G1/G2/G8).

What remains in this file is the **off-spine** work the round also surfaced: the setup-wizard pile-3
leak (G6) and the unverified carry-forward items (F6/F7/F9). The agent-interaction reshape (G5) is now
**RESOLVED** — the PTY bridge was already daemon-consumable, the activity stream became the public
`SystemClient.Logs` verb, and the file exchange landed under G7 (see
[resolved-critiques.md](resolved-critiques.md)). The two naming/consistency sweeps (G3, G4) are now
**RESOLVED** (see
[resolved-critiques.md](resolved-critiques.md)). The D53 three-noun read-model reshape — turning the
field-for-field `Environment` mirror into an identity/posture + embedded-resolved-config view that
drops pile-3 mechanism — also remains, tracked under D53 rather than as a numbered finding here.

## Findings

### G1 — RESOLVED 2026-05-31 (D55) → see [resolved-critiques.md](resolved-critiques.md)

The detector now descends through aliases (`walkType`/`walkObj` unwrap `*types.Alias`),
`store.Meta` was carved to the public `Environment` read-model, the caps doctor tree to
public `BackendReport`, and `f1KnownLeaks` is empty *and* honest. The "F1 FULLY COMPLETE"
claim is now true. Residual structural limit (named-type case reports-but-doesn't-descend)
and the still-pending D53 reshape are noted in the resolved entry.

### G2 — RESOLVED 2026-06-01 (D57) → see [resolved-critiques.md](resolved-critiques.md)

The depguard fence now denies the whole `internal/sandbox` subtree (façade + leaves)
to cli+mcpsrv (rule renamed `cli-sandbox-scope`), with the twin `cli-runtime-scope`
covering `internal/runtime`. Closed via the G7 verb series rather than the critique's
prescribed G1(b)-first sequence; details and divergence note in the resolved sink.

### G3 — RESOLVED 2026-06-03 → see [resolved-critiques.md](resolved-critiques.md)

The only generic safety-override misnomer was `CreateOptions.Force` → renamed `AbandonUnappliedWork`
(and propagated inward to `internal/sandbox/create.Options`); all other toggles already named the
consequence. Details in the resolved sink.

### G4 — RESOLVED 2026-06-03 → see [resolved-critiques.md](resolved-critiques.md)

Three sub-items: (a) all catchable errors re-exported into a dedicated root `errors.go`; (b) public
List/slice surfaces normalized to non-nil empty on success (JSON `[]` not `null`); (c) the dead
`CloneOptions.Overwrite` field wired into `Client.Clone` (cross-backend destroy of a pre-existing
destination) and the internal `Force` field removed. Enum homes assessed as already-principled.

### G5 — RESOLVED 2026-06-03 → see [resolved-critiques.md](resolved-critiques.md)

Split into three surfaces, all now resolved: (a) the **PTY bridge** was already daemon-consumable —
`IOStreams` is stdio-decoupled and `Sandbox.Attach` is tmux-backed/multi-client, so the critique's
"daemon can't hand the engine an `io.Writer`" premise was stale (no code change); (b) the **activity
stream** transport was carved out of `internal/cli/sandboxcmd/log.go` into
`internal/sandbox/logstream.go` and exposed as the public verb `SystemClient.Logs(...) (<-chan
LogEvent, error)` — verbatim `Raw` JSONL frames plus `Time`/`Level` projections, no payload reshaping;
(c) the **file exchange** already had a public home via G7. Details + the decision shape in the
resolved sink.

### G6 — First-run setup UX leaked into the library contract

- **Severity:** MINOR
- **Where:** `TmuxConfigClass`, `SetupChoice`, `SetupStatus`, interactive `SetupOptions`.
- **Observation:** "Which class of tmux config" is pure CLI onboarding mechanism; no embedder
  decides anything from it. `Doctor`/`Check` (health) are legitimately consumer-facing; the
  interactive setup wizard types are pile-3 (D53) bleeding into the contract.
- **Greenfield alternative:** Demote/hide the setup-wizard types; keep `Doctor`/`Check`.
- **Migration cost:** Hours; folds into the same "hide pile-3 mechanism" sweep as G1(b).

### G7 — SUBSTANTIALLY RESOLVED 2026-06-01 (D55–D57, Units 1–15) → see [project memory / working-notes]

The enumerated missing verbs were all added and their consumers de-leafed: `SandboxMetadata`,
`AgentLog`, `Files`, agent/backend `Agents()`/`Backends()` discovery, `Prompt` read + reset-set,
`Workdir().TransferTags`, plus VS Code attach, unlock, archetype/runtime-config paths, and the
mcpsrv file-exchange carve. With G2 (D57) the depguard fence then went prefix-wide over
`internal/sandbox`+`internal/runtime`, so the reach-ins now fail the lint. Non-test cli+mcpsrv
import zero sandbox/runtime leaves.

- **Residue (the one deliberately-deferred capability):** **Extensions** — the CLI `x` command
  still reaches `internal/extension` with no public verb. Left out because the extension surface
  is experimental; revisit when extensions stabilize. `workspace.CreateTag` was checked and ruled
  *not* a gap (zero direct CLI/mcpsrv callers — it flows through `Workdir().TransferTags`).

## Carried forward from the prior round (status unverified — check before next empty)

The 2026-05-30 Post-F5 Façade round's spine findings (F1/F2/F3/F8/F10) drove the Layer-1 work and
are substantially actioned (façade fenced, `api_surface.go` retired, ARCHITECTURE rewritten). These
off-spine items were **not** part of the spine and may still be open:

- **F6 — DONE 2026-06-01.** `lifecycle.go` (1482 lines) split into `start.go`/`reset.go`/`restart.go`
  + a slim `lifecycle.go` (terminal verbs + `resolveAgentArgs`); the three near-identical
  `patchConfig*` collapsed onto one shared `patchRuntimeConfig(dir, mutate)` helper in
  `runtimeconfig_patch.go`. `create/create_prepare.go` (1025 lines) split into the three pipelines it
  bundled (`prepare_profile.go`/`prepare_dirs.go`/`prepare_archetype.go`). Intra-package, no new
  import edges, no behavior change; `make check` + F1 leak detector green.
- **F7 — DONE 2026-06-01.** `AGENT_STATUS_SCHEMA_VERSION` had *four* unfenced writers (Go const
  `agentStatusSchemaVersion` in `internal/sandbox/status/status.go`, a named Python constant in
  `sandbox-setup.py`, a bare literal in `status-monitor.py`, and two literals in `agent.go`'s shell
  hook commands). New cross-language fence `internal/sandbox/status/schema_version_test.go`
  (`TestAgentStatusSchemaVersion_CrossLanguageAgreement`) asserts every writer equals the Go const at
  each `go test ./...`, modelled on the existing runtime-config fence; stale `sandbox/inspect.go`
  pointers in the writer comments corrected to point at the const + fence.
- **F9 — DONE 2026-06-01.** `development-principles.md` §2 stale module-root import paths (`sandbox/`
  etc.) rewritten to current `internal/` truth (D57 fence shape); §4 SandboxName/ResolvedPath marked
  aspirational with a `†` note and parked as DF15 (deferred-findings.md).

## Recommended ordering

The spine findings (G1/G2/G7), the G8 naming sweep, the G3/G4 consistency sweeps, and the G5
agent-interaction reshape are **done** — see [resolved-critiques.md](resolved-critiques.md). What
remains:

1. **G6** — Hide the setup-wizard pile-3 types (`TmuxConfigClass`/`SetupChoice`/`SetupStatus`/
   interactive `SetupOptions`); keep `Doctor`/`Check`. (Hours.)
2. **D53 read-model reshape** — turn the field-for-field `Environment` mirror into an
   identity/posture + embedded-resolved-config view that drops pile-3 mechanism. Shares the
   "hide mechanism" sweep with G6. (Tracked under D53.)
3. **G3 + G4** — Naming/consistency sweeps — **DONE 2026-06-03** (see resolved sink).
4. **G7 residue** — public verb for extensions (`x` → `internal/extension`) when that surface
   stabilizes.
5. **Carried-forward F6/F7/F9** — all done 2026-06-01.
