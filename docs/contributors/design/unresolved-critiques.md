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

What remains in this file is the **off-spine** work the round also surfaced: the agent-interaction
reshape (G5), two naming/consistency sweeps (G3, G4), the setup-wizard pile-3 leak (G6), and the
unverified carry-forward items (F6/F7/F9). The D53 three-noun read-model reshape — turning the
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

### G3 — Public Options field-naming is inconsistent on a surface about to become a versioned contract

- **Severity:** MINOR
- **Where:** the public `*Options` structs — `AllowDirtyWorkdir`, `RestartContainer`,
  `AbandonUnappliedWork`, `Overwrite` (across `yoloai.go`/`sandbox_options.go`/etc.).
- **Observation:** Mixed mood and specificity. `AbandonUnappliedWork` is the good model — names the
  *consequence*, per the project's own `feedback_dangerous_option_naming` guidance. `Overwrite` and
  the more generic toggles don't follow that rule consistently.
- **Why it bothers me:** Once a separate daemon pins this surface, every rename is a tracked breaking
  change rippling to a separate consumer. Cheap to harmonize now, expensive later.
- **Greenfield alternative:** One naming-consistency pass before the daemon work starts; apply the
  "name after the consequence" rule uniformly, judge `Overwrite`-class toggles against it.
- **Migration cost:** Hours; all breakage lands at end of branch per the beta policy.

### G4 — Scattered surface inconsistencies (enum homes, dual-residence error, nil-vs-empty slices)

- **Severity:** MINOR
- **Where:** enum constants split across `names.go` vs `workdir.go`/`network.go`; `DirtyWorkdirError`
  lives in both `yoerrors` and a `names.go` alias; `List*` methods vary between returning `nil` and
  an empty slice on the empty case.
- **Observation:** Individually trivial; collectively they're the kind of papercuts that make a
  public surface feel unowned. The nil-vs-empty variance in particular is a JSON-stability hazard
  (`null` vs `[]`).
- **Greenfield alternative:** One sweep — co-locate enum constants, pick a single home for
  `DirtyWorkdirError`, standardize `List*` on empty-non-nil slices (matching `ProfileAdmin.List`'s
  already-correct behavior).
- **Migration cost:** Hours, independent of the spine.

### G5 — The agent-interaction surface is bound to the caller's process stdio, not contracted for an embedder

- **Severity:** MAJOR (it's the conversation half of the product's value, and the daemon can't consume it as-is)
- **Where:** `IOStreams`, `TerminalSnapshot`, `Sandbox.Attach`/`SendInput`/`CaptureTerminal`, and
  `preparePromptForStart(... stdin io.Reader ...)` in `internal/sandbox/lifecycle/lifecycle.go`.
- **Observation:** Talking to the agent is a first-class consumer capability (the "converse" half of
  the value prop), but the current seam (a) tangles two distinct concerns — live conversation and
  glanceable observation — into one terminal-flavored surface, and (b) binds it to the *caller's*
  `io.Reader/Writer` (`IOStreams`). The same caller-stdio coupling appears a third time in the prompt
  path (`preparePromptForStart`'s `stdin`). The lived symptom: "talk to the agent" today requires
  VS Code tunnel → in-sandbox shell → `tmux attach` — manual ceremony, because "attach to the agent's
  terminal" isn't a first-class primitive.
- **Why it bothers me:** The agent is a TUI, so the terminal is **intrinsic** — this is *not* a
  shape error to refactor into an event stream (correcting my own earlier framing; see D53). But
  binding it to caller stdio means a daemon — the stated forcing function — cannot consume it: it
  can't hand the engine an `io.Writer`; it needs a PTY bridged over a socket. So the most
  value-bearing interactive capability is exactly the one a daemon must work *around*, not *through*.
- **Greenfield alternative (D53):** Split into two complementary surfaces.
  - **PTY bridge** for conversation — contract is "a terminal" (bytes in/out + resize),
    attachable/multi-client/persistent, decoupled from caller stdio; tmux stays the substrate behind
    it (persistence + multi-client re-attach). CLI wires it to the local terminal; a daemon wires it
    to a websocket → xterm.js. `CaptureTerminal`/`TerminalSnapshot` become conveniences on this
    primitive. VS Code tunnel demotes to optional full-IDE convenience.
  - **Activity stream** for observation — `AgentStatus` + `LogSource` + monitor/hooks promoted to a
    subscribable surface. This half genuinely *is* a clean event stream.
  - **Structured file exchange** (found by audit — a third surface the two-surface model missed):
    `store.FilesDir()` (`/yoloai/files/`), exposed by the mcpsrv daemon prototype as
    `sandbox_files_list/read/write` and carrying a Q&A protocol (agent drops `question.json`,
    consumer writes `answer.json`, then pokes via keystroke). Out-of-band structured side-channel
    that complements the PTY because large/structured content is impractical to type. Needs a public
    home (e.g. `Sandbox.Files()` sub-handle), not direct `store.FilesDir()` reach-in.
  - **Prompt injection stays internal** — the initial task is the one-time `RunOptions.Prompt`; the
    four injection sites are internal re-delivery across lifecycle events; no `SendPrompt` verb. The
    file exchange is a distinct structured channel, NOT prompt delivery.
- **Stdio-coupling confirmed by audit:** prompt reading binds to caller stdio via the `"-"` sentinel
  (`invocation.ReadPrompt` → engine `input` → `os.Stdin` at the CLI). Same coupling as `Attach`/`SendInput`.
- **Migration cost:** Real reshape, multi-day, and best decided *before* a wire protocol exists.
  Independent of the read-model spine but shares the "decouple from caller stdio" theme.

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

The spine findings (G1/G2/G7) and the G8 naming sweep are **done** — see
[resolved-critiques.md](resolved-critiques.md). What remains:

1. **G5** — Reshape the agent-interaction surface (PTY bridge + activity stream + file-exchange
   sub-handle, decoupled from caller stdio). The most value-bearing remaining work; decide it
   **before** a wire protocol exists. (Multi-day.) **G6** folds in (hide setup-wizard pile-3).
2. **D53 read-model reshape** — turn the field-for-field `Environment` mirror into an
   identity/posture + embedded-resolved-config view that drops pile-3 mechanism. Shares the
   "hide mechanism" sweep with G6. (Tracked under D53.)
3. **G3 + G4** — Naming/consistency sweeps, independent, fold in opportunistically. (Hours each.)
4. **G7 residue** — public verb for extensions (`x` → `internal/extension`) when that surface
   stabilizes.
5. **Carried-forward F6/F7/F9** — all done 2026-06-01.
