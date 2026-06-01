<!-- ABOUTME: History sink for resolved critiques drained from unresolved-critiques.md. -->
<!-- ABOUTME: Item-queue pattern: active items live in the unresolved- file, done ones land here. -->

# Resolved critiques

History of critiques that have been addressed and applied. Items are moved here from
[`unresolved-critiques.md`](unresolved-critiques.md) once resolved, so the active file stays
a working set. Newest first.

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

_(no older entries)_
