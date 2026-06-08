# Layer 1 — Honest completion (post-D52/D53)

**COMPLETE (2026-06-08).** Every phase in this program's scope (Phases 1–3, plus the
opportunistic 5/6) has landed. Phase 4 (the agent-interaction reshape) was always a
**separate later effort with its own plan** (decided 2026-05-30) and is therefore not a
remaining item of *this* program. See the per-phase markers below. Supersedes
[layer1-public-api.md](../../archive/plans/layer1-public-api.md), which delivered the
spine but declared completion on a detector that could not see the biggest remaining
leak. Governed by the CRITIQUE round **G1–G7** and working-notes **D53**.

**Implemented-shape divergences from the original plan:**
- The F1 fence test moved from `public_api_test.go` to `internal_leak_fence_test.go`;
  `f1KnownLeaks` is empty and the detector now descends through `*types.Alias` via
  `types.Unalias` (Phase 1a delivered).
- `store.Meta` was renamed to `store.Environment`; the carved public read-model is
  `yoloai.Environment` (built by the one-directional `environmentFromStore` converter),
  surfaced via `Sandbox.Metadata() (*Environment, error)`. (Phase 1b delivered, just
  under different names than the plan's `store.Meta`/`Info.Meta`.)
- Agent-log read landed as `Agent().TerminalLog()` + the streaming `Agent().Logs()`
  (G5 activity-stream carve, `a22ea04`), not a `Sandbox.AgentLog()` verb.
- The stored prompt is **read-only** in the public surface (`Agent().Prompt()`); no
  set verb was added — initial prompts are delivered at create time, matching the
  Phase 4 "prompt injection stays internal" intent.
- Git-tag handling landed as `Workdir().Tags()` + `Workdir().TransferTags()` rather
  than a standalone `Workdir().CreateTag()`; tags are created by the agent inside the
  workdir and transferred on apply.

## The two falsified claims

D52 recorded "F1 closed / Layer-1 spine FULLY COMPLETE / no deferrals." Both halves
are wrong:

1. **The F1 detector lies.** `TestPublicAPI_NoInternalLeaks` (`public_api_test.go`) is
   structurally blind to type aliases — `walkObj` returns on `IsAlias()`, and `walkType`
   has no `*types.Alias` case. So `type X = internal.Y` silences the detector for the
   *entire tree* beneath `X`. `f1KnownLeaks` being empty means "no leaks *not hidden
   behind an alias*," not "no leaks." Confirmed live leak: `yoloai.Info`
   (`= sandbox.Info` → `status.Info`) carries `Meta *store.Meta`, an internal iceberg
   (`WorkdirMeta`, `[]DirMeta`, `DirMode`, `runtime.IsolationMode`, `runtime.BackendType`,
   `agent.AgentType`, `*config.ResourceLimits`) returned by `Client.ListSandboxes`, `Sandbox.Wait`,
   `Sandbox.Inspect`, `System.AllSandboxes` — the four most central entry
   points. An embedder holding a `*yoloai.Info` cannot name `.Meta`.

2. **The surface is incomplete.** ~7 consumer capabilities reach `internal/` directly
   with no public verb (G7). The mcpsrv daemon prototype — the canary for a real
   embedder — reaches `internal/sandbox/store` for 5+ of its ~13 tools. They compile
   only because the `cli-sandbox-facade-scope` depguard fences the *façade* package but
   allows the `store`/`patch`/`archetype` leaves (G2). A separate-module daemon could
   do none of it. So the gate passes for code the daemon would fail — "the CLI keeps us
   honest" is only partly realized.

The honest sequence: **make the test tell the truth first** (it goes red on
`store.Meta`), then build the carved read-model + the missing verbs, then tighten the
gate so the reach-ins fail the lint. Truth before cleanup.

## Phases

Each phase keeps `make check` green at its boundary (Phase 1 transiently reds the F1
test between 1a and 1b — that red is the deliverable of 1a).

### Phase 1 — Truth (G1) — ✅ DONE

**1a. Detector honesty *(hours; do first, land alone)*. — ✅ DONE.** `walkType` has the
`*types.Alias` case (unwraps via `types.Unalias` and recurses); `walkObj` descends
through alias underlying types; `f1KnownLeaks` is empty. Fence now in
`internal_leak_fence_test.go`.
Teach `walkType`/`walkObj` to descend through aliases:
- Add a `*types.Alias` case to `walkType` that unwraps via `types.Unalias` and recurses.
- In `walkObj`, stop blanket-returning on `IsAlias()`; descend into the underlying
  named type's exported fields.
- The existing `aliased` de-dup in `report` already suppresses the sanctioned alias
  *target* (e.g. `Info` itself); what we want surfaced is *fields that reference other,
  un-aliased internal types*.

**Expected result: the test goes RED on `internal/sandbox/store.Meta` (and its subtree).**
That red is the truth and the acceptance criterion for 1a. Commit 1a with `store.Meta`
(and any other newly-surfaced entries) added to `f1KnownLeaks` as **explicit, documented
deferrals** so the suite is green — never silent. Phase 1b removes them.

**1b. Carve `store.Meta` into a public read-model *(the core; see "The carve" below)*. — ✅ DONE.**
Landed as `yoloai.Environment` (converter `environmentFromStore`; the internal struct was
renamed `store.Meta` → `store.Environment`), surfaced via `Sandbox.Metadata()`.
Promote to a curated `yoloai` read-model — **not** an alias of the storage struct (that
publishes mechanism and welds disk/wire format), **not** a field-for-field mirror. When
done, remove the `store.Meta` deferral from `f1KnownLeaks`. Surfaces it via the new
`Sandbox.Metadata()` verb (Phase 2's load-bearing gap) and reframes what `Info` exposes.

### Phase 2 — Missing verbs (G7) — ✅ DONE

Every capability now has a public home; the only remaining `internal/sandbox/store`
imports under `internal/cli` + `internal/mcpsrv` are in `_test.go` files. Implemented
verb names diverge from the table in a few places (see the divergence note at the top).

| Capability | Reaches today | New public verb |
|---|---|---|
| Sandbox metadata / workdir-mode read | `store.LoadMeta()` in `cli/workflow/{apply,diff,baseline}.go` + mcpsrv | `Sandbox.Metadata()` (= Phase 1b) |
| Agent-log read | `store.AgentLogPath()` (mcpsrv `sandbox_log`) | `Sandbox.AgentLog()` (distinct from `ContainerLogs`) |
| File exchange (Q&A side-channel) | `store.FilesDir()` (mcpsrv `sandbox_files_*`) | `Sandbox.Files()` sub-handle (list/read/write) |
| Agent/model + backend discovery | `agent.AllAgentTypes()`/`GetAgent()`, `runtime.Descriptors()` | `System.AgentTypes()` / `System.BackendTypes()` |
| Stored-prompt get/set | `store.PromptFilePath()` (`sandbox prompt`) | prompt get/set verb (AGENT noun) |
| Git tag create | `workspace.CreateTag()` in `cli/workflow/apply.go` | `Workdir().CreateTag()` (sibling of `Tags`) |
| Extensions | `internal/cli/extension` (CLI `x`) | ✅ resolved — CLI-private, no verb (D66); package relocated `390f83f` |

### Phase 3 — Tighten the gate (G2) — ✅ DONE 2026-06-01 (D57)

Phase 2's verb series gave every reach-in a public home, so:
- **Done:** dropped all three leaf allow-entries (`store`/`patch`/`archetype`) from the
  former `cli-sandbox-facade-scope`; the `deny` on `internal/sandbox` now covers the
  whole subtree by prefix. Rule renamed `cli-sandbox-scope`.
- **Done (G7, separately):** the analogous `internal/runtime` fence shipped as the twin
  `cli-runtime-scope` rule (only `internal/cli/system/tart/` exempt).
- **Deferred:** an `internal/config` CLI fence — 7 cli files still import it; genuine
  future work, not blocking F1/G2.
- **Now** the gate means what it claims: a separate-module daemon could do everything the
  CLI does over the `internal/sandbox` + `internal/runtime` subtrees through the public
  surface alone.

### Phase 4 — Agent-interaction reshape (G5 + G6) *(SEPARATE later effort — own plan)* — NOT part of this program

**Scoped OUT of this program (decided 2026-05-30).** *(Status note: the G5 activity-stream
half already carved out via `Agent().Logs()`/`StreamLogs` (`a22ea04`), and the G6
setup-wizard types (`TmuxConfigClass`/`SetupChoice`/`SetupStatus`) were removed by the
D65 collapse. The PTY-bridge / caller-stdio decoupling — `IOStreams` still binds the
engine to the caller's `io.Reader/Writer` — remains the open work for that future plan,
to be written before the daemon module starts.)* This program is Phases 1–3 (+
opportunistic 5/6); the interaction reshape gets its own plan, to be written and decided
**before the separate daemon module starts** (i.e. before any wire protocol exists). The
detail below is preserved as the seed for that future plan.

Independent of the read-model spine but shares the "decouple from caller stdio" theme. The agent is a TUI, so the terminal is
**intrinsic** — this is not an event-stream refactor. Split into three complementary
surfaces (D53):
- **PTY bridge** for conversation: contract is "a terminal" (bytes in/out + resize),
  attachable/multi-client/persistent, decoupled from caller stdio (today `IOStreams`
  binds the engine to the *caller's* `io.Reader/Writer`, which a daemon cannot supply).
  tmux stays the substrate behind it. CLI wires it to the local terminal; a daemon wires
  it to a websocket → xterm.js. `CaptureTerminal`/`TerminalSnapshot` become conveniences.
- **Activity stream** for observation: `AgentStatus` + `LogSource` + monitor/hooks
  promoted to a subscribable surface (this half genuinely *is* a clean event stream).
- **Structured file exchange**: the `Sandbox.Files()` sub-handle from Phase 2 (the Q&A
  side-channel) — distinct from prompt delivery.
- **Prompt injection stays internal**: initial task is one-time `RunOptions.Prompt`; the
  four lifecycle re-injection sites are internal; no `SendPrompt` verb.

**G6 folds in here:** demote/hide the first-run setup-wizard pile-3 types
(`TmuxConfigClass`, `SetupChoice`, `SetupStatus`, interactive `SetupOptions`); keep
`Doctor`/`Check` (legitimately consumer-facing).

### Phase 5 — Consistency sweeps (G3 + G4) — **DONE 2026-06-03**

- **G3:** done — only `CreateOptions.Force` violated "name after the consequence"; renamed
  `AbandonUnappliedWork` and propagated inward to `internal/sandbox/create.Options`.
- **G4:** done — (a) all catchable errors re-exported into a dedicated root `errors.go`
  (`DirtyWorkdirError` no longer dual-resident in `names.go`); (b) public List/slice surfaces
  standardized on empty-non-nil (JSON `[]` not `null`); (c) dead `CloneOptions.Overwrite` wired
  into `Client.CloneSandbox` (cross-backend destroy), internal `Force` removed. Enum homes left as-is
  (already principled). See `critiques-resolved.md`.

### Phase 6 — Carried-forward (F6/F7/F9) — ✅ DONE

- **F6:** done — lifecycle dissolved into the `internal/sandbox/lifecycle/` leaf (the old
  1474-line god-file is gone; `internal/sandbox/lifecycle.go` is now a 14-line alias
  shim).
- **F7:** done — `AGENT_STATUS_SCHEMA_VERSION` drift fence lives in
  `internal/sandbox/status/schema_version_test.go`.
- **F9:** done — `development-principles.md` §2 uses current `internal/…` paths (no stale
  module-root import paths).

## The carve — `store.Meta` → public read-model (Phase 1b detail)

`store.Meta` is three types in one struct. The litmus for each field: *would a consumer
render it or decide from it?* Yes → contract (in consumer terms); exists only due to
*how* we implement → mechanism, stays internal. Working classification (finalize against
the litmus during implementation):

- **Pile 1 — identity & posture → EXPOSE.** `Name`, `CreatedAt`, `Backend`, `Profile`,
  `Agent`, `Model`; workdir + aux dirs as host-path + **mode** (copy/overlay/rw/ro — the
  decision `apply`/`diff`/`baseline` branch on); network posture (isolated? allow-list),
  `Isolation`, `Ports`. Re-expressed in consumer terms, not the storage shape.
- **Pile 2 — config echo → REFRAME.** `Resources`, `Mounts`, `CapAdd`, `Devices`,
  `Setup`, `AutoCommitInterval` overlap heavily with `ResolvedProfileConfig`. Embed a
  *resolved-config view* rather than re-listing knobs.
- **Pile 3 — mechanism → DROP.** `Version`, `YoloaiVersion`, `ImageRef`, `HasPrompt`,
  `Debug`, `UsernsMode`, `HostFilesystem`, `Archetype`, and the workdir/dir
  `BaselineSHA`/`InceptionSHA` (git plumbing for diff baselines). (`VscodeTunnel` is
  borderline posture — judge by the litmus.)

Pattern: hand-written public mirror + unexported converter (the `SystemOptions` /
`ResolvedProfileConfig` precedent), **not** an alias. The internal `store.Meta` is the
legitimate on-disk record and is **not** renamed; the converter is one-directional
(internal → public). Caveat (D53): a field rendered inside `Info` does **not**
automatically belong on the contract — `Info` is a convenience aggregate; keep the
read-model curated.

## Scope

**This program = Phases 1–3** (detector truth → carved `store.Meta` read-model →
missing verbs → tighten the gate) — the self-contained "Layer-1 actually complete"
deliverable. **Phases 5–6** (naming/consistency sweeps; carried-forward F6/F7/F9) fold
in opportunistically. **Phase 4** (the G5 interaction reshape — the conversation half of
the value prop) is a **separate later effort with its own plan**, decided 2026-05-30;
write it before the daemon module starts.

### One open decision (confirm during 1b)

**The carve's exact field dispositions** (Pile 1/2/3 boundaries above) — a few are
judgment calls (`VscodeTunnel`, how much of the config echo to embed vs drop). Resolve
against the render-or-decide litmus when building 1b, not before.

## Verification (per phase)

- **1a:** `go test -run TestPublicAPI_NoInternalLeaks ./...` goes red on `store.Meta`
  with the detector change; green again once `store.Meta` is in `f1KnownLeaks`.
- **1b/2:** `f1KnownLeaks` empty *and* honestly so (re-run with the fixed detector);
  `make check`; JSON-stability check on any `Info`/metadata-emitting command.
- **3:** depguard green with the tightened allow-list; a negative test (inject a
  `store.*` import → flagged).
- **4:** CLI conversation/observe paths work end-to-end; engine no longer needs a
  caller-supplied `io.Reader/Writer` for the contracted paths.
- **5/6:** `make check`; targeted sweeps verified individually.

## Commit granularity

One logical change per commit (project convention). Phase 1a is its own commit (detector
+ baseline deferral). Phase 1b's carve, each Phase 2 verb, the Phase 3 gate tighten, and
each Phase 5/6 sweep are separate commits, each with its BREAKING-CHANGES entry where the
public surface changes.
