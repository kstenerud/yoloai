<!-- ABOUTME: History sink for resolved critiques drained from unresolved-critiques.md. -->
<!-- ABOUTME: Item-queue pattern: active items live in the unresolved- file, done ones land here. -->

# Resolved critiques

History of critiques that have been addressed and applied. Items are moved here from
[`unresolved-critiques.md`](unresolved-critiques.md) once resolved, so the active file stays
a working set. Newest first.

## G5 (2026-05-30 critique) — The agent-interaction surface was bound to caller stdio, not contracted for an embedder

- **Severity:** MAJOR. **Resolved:** 2026-06-03.
- **The finding split into three surfaces; each is now resolved:**
  - **PTY bridge (conversation) — already decoupled (no code change).** The critique's
    central claim ("a daemon can't hand the engine an `io.Writer`; it needs a PTY bridged over a
    socket") was **stale**. `IOStreams` (aliased at `yoloai.go` as `runtime.IOStreams`) already
    carries `In io.Reader`/`Out io.Writer`/`Err io.Writer`/`TTY`/`Rows,Cols`/`Term`/`Resize
    <-chan TermSize` — the library never touches the process stdin FD or `$TERM`. `Sandbox.Attach`
    is tmux-backed (persistent, multi-client re-attach), so a daemon wires `IOStreams` to a
    websocket→xterm.js exactly as the CLI wires it to the local terminal. The prompt path's `"-"`
    stdin sentinel is likewise already injectable via `Options.Input`. So the PTY half was a faithful
    embedder contract before this round; the finding's premise no longer held.
  - **Activity stream (observation) — carved into the library (this round).** The transport that
    lived in `internal/cli/sandboxcmd/log.go` (open the four JSONL sources, backlog read,
    merge-sort, follow tail-poll, terminal done-detection via `agent-status.json`) moved down to
    `internal/sandbox/logstream.go` (`StreamLogs`) and is exposed as the public verb
    **`SystemClient.Logs(ctx, name, LogOptions) (<-chan LogEvent, error)`** — a subscribable,
    time-ordered stream, sibling to `AgentLog`. `LogEvent` carries the **verbatim** JSONL line
    (`Raw`) plus the two projections the transport must parse to order/filter (`Time`, `Level`);
    the library does **not** reshape the payload — a consumer that wants a richer view parses `Raw`
    itself (the user's "raw until it has to change" principle). The CLI keeps only rendering
    (`parseLogRecord`/`formatRecord`/`levelCode`/`terminalWidth`/`stripANSI`) and `--since`
    local-tz input parsing. No speculative daemon-side converter helpers (YAGNI).
  - **Structured file exchange — already done (G7).** `Sandbox.Files()` + the mcpsrv file-exchange
    carve gave this surface a public home; tracked under G7's verb series, not re-done here.
- **Decision shape (per the user).** Two complementary surfaces (PTY bridge + activity stream) plus
  the file-exchange sub-handle — *not* a single terminal-flavored seam, and *not* an event-union
  reshape of the log payload. Prompt injection stays internal (the one-time `RunOptions.Prompt`;
  re-delivery is internal lifecycle), so there is no `SendPrompt` verb.

## G3 (2026-05-30 critique) — Public Options field-naming inconsistent on a soon-to-be-versioned contract

- **Severity:** MINOR. **Resolved:** 2026-06-03.
- **Resolution.** Audited the public `*Options` structs against the `feedback_dangerous_option_naming`
  rule (name the *consequence*, like `AbandonUnappliedWork`). The only generic safety-override misnomer
  was `CreateOptions.Force` → renamed `AbandonUnappliedWork` (mirrors `DestroyOptions.AbandonUnappliedWork`;
  the CLI `--force` flag maps to it). All other toggles (`Replace`, `Overwrite`, `AllowDirtyWorkdir`,
  `RestartContainer`, and the benign behavioral flags) already comply.
- **Propagation (per user feedback).** Naming clarity must not stop at the public boundary — the rename
  was carried inward through `internal/sandbox/create.Options.Force` → `AbandonUnappliedWork` and its
  call sites, so the "force" misnomer is gone from the guts too. Tracked in BREAKING-CHANGES.md.

## G4 (2026-05-30 critique) — Scattered surface inconsistencies (enum homes, dual-residence error, nil-vs-empty slices)

- **Severity:** MINOR. **Resolved:** 2026-06-03 (three sub-items).
- **G4a — error re-export.** Added a dedicated root `errors.go` aliasing every catchable `yoerrors`
  type plus `ExitCoder` into `yoloai.*`, so embedders match errors with `errors.As`/`Is` against
  `yoloai.*` alone and never import `yoerrors`. The canonical defs stay in `yoerrors` (internal packages
  construct them without an import cycle); `DirtyWorkdirError`/`DirtyDir` aliases moved out of `names.go`.
- **G4b — nil-vs-empty slices.** Normalized the public List/slice surfaces to return `[]T{}` (not `nil`)
  on the empty-*success* path, for JSON `[]` rather than `null`: `tartVersionsToPublic`, `listTartBases`,
  `Workdir.Tags`, `SystemClient.Doctor`, and the `collectExchangeGlobs`/`parseBaselineLog` helpers.
  Error paths still return `nil`; the internal `tartVersionsToInternal` (feeds the backend, not a JSON
  surface) stays `nil`-for-`nil`.
- **G4c — dead clone Overwrite field.** `CloneOptions.Overwrite` was inert. Wired it into `Client.Clone`,
  which now destroys a pre-existing destination (resolving that destination's *own* recorded backend,
  which may differ from the source's) before the disk-only copy. Removed the dead internal
  `sandbox.CloneOptions.Force`; the CLI dropped its hand-rolled `forceDestroyIfExists` and passes
  `Overwrite: force`.
- **Enum homes (assessed, no change).** Shared enums in `names.go` vs handle-local `DomainSource`/
  `ApplyMode` beside their handles was judged already-principled; left as-is.

## G8 (2026-05-30 critique) — `store.Meta` is a vague name; comments reference a phantom `meta.json`

- **Severity:** MINOR. **Resolved:** 2026-06-01 (two passes).
- **G8(b) — public name (done earlier, D55/G1(b)).** When `store.Meta` was carved to the
  public read-model it was named `yoloai.Environment` (artifact-aligned to `environment.json`,
  paired with `State`), not `Meta` — exactly the recommendation.
- **G8(a) + internal rename (2026-06-01).** Closed the type/file/comment trio end-to-end:
  - **Type/file:** `store.Meta`/`WorkdirMeta`/`DirMeta` → `store.Environment`/`WorkdirEnvironment`/
    `DirEnvironment`; `LoadMeta`/`SaveMeta` → `LoadEnvironment`/`SaveEnvironment`; source file
    `store/meta.go` → `store/environment.go`. Internal `state.SandboxState.Meta` field also renamed
    to `Environment` for consistency.
  - **Phantom comments:** every `meta.json` comment (lifecycle, launch, create, patch, status,
    inspect, cliutil, tests) replaced with `environment.json` — the file is never named `meta.json`
    anywhere in code (the `EnvironmentFile = "environment.json"` constant is the only filename).
  - **Public field (breaking):** the public `yoloai.Info.Meta` field → `Info.Environment`, JSON tag
    `"meta"` → `"environment"`. `sandbox info`/`list` `--json` now nest settings under `"environment"`.
    Tracked in BREAKING-CHANGES.md (layer-1 reshape section).
- **Scope note.** Local variable identifiers named `meta` (and helper func names `buildMeta`/
  `buildConfigAndMeta`) were left as-is — the critique targeted the type, file, and public field, not
  internal plumbing var names. `make check` green; F1 leak detector still empty/honest.

## G2 (2026-05-30 critique) — depguard fenced only the façade, not the leaf it leaks through

- **Severity:** MAJOR. **Resolved:** 2026-06-01 (D57, `.golangci.yml`).
- **Resolution.** The `cli-sandbox-facade-scope` rule's three leaf allow-entries
  (`internal/sandbox/{store,patch,archetype}`) were dropped; the `deny` on
  `internal/sandbox` now matches the whole subtree by prefix. Rule renamed
  `cli-sandbox-facade-scope` → `cli-sandbox-scope`. Twin runtime fence
  `cli-runtime-scope` (G7) added separately, denying `internal/runtime` to
  cli+mcpsrv (only `internal/cli/system/tart/` exempt). Net: non-test
  `internal/cli/**`/`internal/mcpsrv/**` may now reach sandbox/runtime behavior
  *only* through the public `yoloai` spine — the CLI is finally a faithful proxy
  for a separate-module daemon.
- **Divergence from the critique's prescribed sequence (per §13/D54).** The critique
  said to "sequence after G1(b)" — i.e. first promote `store.Meta` to a public
  read-model, *then* drop the allow-entry. We took a different path that reached the
  same end: the **G7 verb series** gave every CLI/mcpsrv leaf reach-in a public verb
  (e.g. `printCreateSummary` now takes a public `SandboxMetadata`, not `*store.Meta`;
  metadata/log/discovery/prompt reads route through `SystemClient`/`Client`/`Sandbox`).
  So the CLI stopped importing the leaves entirely **without** requiring a single
  field-for-field `store.Meta` mirror first. Verified: zero non-test leaf imports in
  cli+mcpsrv; a 3-import probe yields 3 `cli-sandbox-scope` denials; `make check` green.
- **Still open (tracked elsewhere, not G2).** The deeper read-model reshape the
  critique's G1(b) gestured at — collapsing `store.Meta` into the three-noun public
  view (identity/posture + embedded resolved-config echo, dropping pile-3 mechanism) —
  remains future work under D53; it is a *public-surface shape* question, distinct from
  this *import-fence* finding. `internal/config` still has no analogous CLI fence (7
  cli files import it); that is genuine future work, not part of G2.

## G1 (2026-05-30 critique) — the F1 leak detector was blind to type aliases; "F1 closed" was false

- **Severity:** CRITICAL. **Resolved:** 2026-05-31 (D55), drained 2026-06-01.
- **The lie.** `TestPublicAPI_NoInternalLeaks` treated every alias as terminal — `walkObj`
  blanket-returned on `IsAlias()` and `walkType` had no `*types.Alias` arm — so `type X =
  internal.Y` silenced the detector for the entire field tree beneath `X`. `f1KnownLeaks`
  empty meant "no leaks *not hidden behind an alias*", not "no leaks". The concrete hidden
  leak: `yoloai.Info` (`type Info = sandbox.Info` → `status.Info`) carried `Meta *store.Meta`,
  an internal iceberg (`WorkdirMeta`/`[]DirMeta`/`DirMode`/`IsolationMode`/`AgentName`/
  `*ResourceLimits`), returned from the four most central entry points (Run/List/Inspect/
  ListAcrossBackends). Bigger than the `MergedConfig` leak D52 had just closed.
- **G1(a) — detector made honest (truth first, committed `3e29ac4`).** `walkType` gained a
  `*types.Alias` case that unwraps via `types.Unalias` and recurses; `walkObj` now descends
  into an aliased named type's exported fields instead of bailing. The existing `aliased`
  de-dup still prevents flagging the sanctioned alias *target*; what surfaces is fields that
  reference *other* un-aliased internal types. Landing this turned the test red on
  `store.Meta` — that red was the truth, produced *before* any cleanup.
- **G1(b)-1 — store.Meta → public `Environment` (committed `d5f8787`).** `Info` de-aliased
  from `sandbox.Info` to a hand-written struct; `Meta` retyped to `*yoloai.Environment`
  (environment.go), a field-for-field mirror with byte-identical JSON; converters wired at
  Inspect/Run/List/pollUntilDone/ListAcrossBackends. `store.Meta` stayed internal.
- **G1(b)-2 — caps doctor tree → public `BackendReport` (committed `d335b10`).** Removed
  `type BackendReport = caps.BackendReport`; hand-written mirrors (Availability/BackendReport/
  CapabilityCheck/Capability/FixStep) + converter in doctor_report.go; `Capability` drops the
  func fields. JSON + human output byte-unchanged.
- **Outcome (committed `f2c6ba6`).** `f1KnownLeaks` is now empty **and** honest — the detector
  can no longer be fooled by an alias. The decision from the critique's step 2 (promote vs.
  park `store.Meta`) was **promote**, not a documented deferral.
- **Known residual limit (not a lie).** The *named-type* case still reports-but-doesn't-descend:
  a named type is reported as a unit, and its fields are walked only at its own declaration. A
  brand-new internal type hung off an already-public named struct would not be auto-flagged.
  This is a structural bound of the walker, documented here so it stays eyes-open — distinct
  from the alias blind spot, which was the actual defect and is fixed.
- **Still open (tracked elsewhere, not G1).** The `store.Meta`→`Environment` carve is the
  *field-for-field mirror*, not D53's three-noun reshape (expose identity/posture, embed a
  resolved-config view, drop pile-3 mechanism) — that read-model refinement remains future
  work. The G7 missing-verb series and G2 depguard fence (both since resolved) were the
  consumption-side twin of this finding.

_(no older entries)_
