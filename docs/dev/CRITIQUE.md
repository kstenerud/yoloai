# Critique — 2026-05-30 Post-F5 Façade Pass

## Summary

The F5 carve was the right move and it landed cleanly: the `internal/sandbox` god-package's *code* is now split into leaf subpackages along a real DAG (`state ← {mounts, invocation, provision, profiles, runtimeconfig} ← launch ← {create, lifecycle} ← sandbox`), each leaf has a name and an explicit `state.Deps` seam, and the W11/W12 enforcement (forbidigo §12 bans, the cli-backend-scope depguard rule) still holds. The typed-error surface, the runtime-backend abstraction, and the RUNTIME_CONFIG schema-version fence are all in good shape.

But the carve split the *code* without splitting the *coupling*. `internal/sandbox` is now a façade that re-exports 202 exported symbols (59 of them plain `type X = leaf.X` / `var X = leaf.X` aliases) drawn from nine downstream packages — errors, paths, status, config, formatting, parsing, notices, prompts. It is still the single most-imported internal package: 42 of the CLI's files import it directly. So we now carry *two* costs where we used to carry one: the god-package's import gravity (unchanged) *plus* a new indirection layer that the reader has to see through to find where a symbol actually lives. The CLI consumes two parallel "public" surfaces in parallel — `yoloai.*` (36 files) and `internal/sandbox.*` (42 files) — with no rule and no doc explaining which is canonical, and `ARCHITECTURE.md:49` actively claims the second one doesn't happen. Two policy concerns (terminal prompting via `Confirm`, human-readable formatting via `FormatSize`/`FormatAge`) sit in the domain layer in direct contradiction of development-principles §2. The public `yoloai` package reaches back through the internal façade to construct its errors. `lifecycle/lifecycle.go` is a relocated 1474-line god-file. And `api_surface.go` now self-certifies its own deletion condition ("deleted in W-L8b") while sitting at ~55–60% drift from the real surface.

The through-line: F5 proved the seams exist (the leaves compile and test independently). The façade is now load-bearing scaffolding that should be dismantled, not preserved — let the CLI and the public package import the leaves directly, and the god-package's gravity finally dissipates instead of being re-hosted one indirection up.

I also flag two doc/principle divergences the user explicitly asked to hear about: §2's stale module-root import paths, and the false `ARCHITECTURE.md:49` claim.

## Findings

### The façade and its coupling

#### F1 — `internal/sandbox` is a 202-symbol re-export hub; F5 split the code but the façade preserved the god-package's gravity

- **Severity:** HIGH
- **Where:** `internal/sandbox/*.go` — 202 exported symbols (`grep '^(type|var|func|const) [A-Z]'`), of which 59 are pure re-export aliases (`^(type|var) X +=`), aggregating from `yoerrors` (24), `status` (15), `lifecycle` (8), `state` (4), `profiles` (4), `create` (2), `store` (1), `errors` (1) plus ~20 const enums. Importers: 42 files under `internal/cli/`.
- **Observation:** After F5, the leaf packages (`status`, `config`, `store`, `workspace`, `yoerrors`) are all independently importable — they have no reason to be funneled through `internal/sandbox`. Yet the façade re-exports them, so a CLI file that wants `status.FormatAge` can reach it as `sandbox.FormatAge`, and most do. The façade is the single most-imported internal package, exactly as the pre-F5 god-package was. We split `create.go` (2019 lines) and `lifecycle.go` into leaves, but the *coupling graph* is unchanged: everyone still depends on `internal/sandbox`, which now transitively depends on all nine leaves.
- **Why it bothers me:** The whole point of a leaf-DAG carve (general-principles §1 pragmatic decomposition; the W12 model) is that a reader follows a symbol to where it lives and a change blast-radius shrinks to one leaf. The façade defeats both: `sandbox.NewUsageError` hides that the symbol lives in `yoerrors`; `sandbox.FormatSize` hides `status`. The reader now sees through *two* layers (façade → leaf) instead of *zero* (direct import). We added indirection and called it decoupling. A re-export façade is justified when it presents a *curated, smaller* surface than the sum of its leaves (a true Facade pattern). 202 symbols re-exporting ~all of nine packages is not curation — it's a passthrough.
- **Greenfield alternative:** Delete the gratuitous re-exports. Let CLI and the public package import leaves directly (`status.FormatAge`, `yoerrors.NewUsageError`, `store.X`). Keep `internal/sandbox` only if it earns its place as a *thin orchestration façade* — i.e. it holds the handful of cross-leaf entry points (`Create`, `Start`, `Stop`, `Reset`) that genuinely compose multiple leaves, and re-exports *nothing* that callers can import directly. Target: the façade's exported surface drops from 202 to the ~dozen orchestration verbs.
- **Migration cost:** Multi-week but almost entirely mechanical: each removed alias is a find-and-replace of `sandbox.X` → `leaf.X` across importers, one leaf at a time, each step independently green. Start with the leaves that have zero composition value in the façade (`yoerrors`, `status`, `store`, `workspace`).

#### F2 — The CLI consumes two parallel "public" surfaces (`yoloai.*` and `internal/sandbox.*`); no rule or doc says which is canonical, and ARCHITECTURE.md:49 claims the second doesn't happen

- **Severity:** HIGH
- **Where:** `internal/cli/` — 36 files import the `yoloai` root package, 42 import `internal/sandbox` directly. `docs/dev/ARCHITECTURE.md:49`. `.golangci.yml` depguard `cli-backend-scope` rule (only blocks CLI→`internal/runtime/tart`).
- **Observation:** `ARCHITECTURE.md:49` states: "The CLI doesn't reach into `internal/sandbox/*` … for orchestration — every command goes through `yoloai.Client` or `yoloai.SystemClient`." This is false today: 42 CLI files import `internal/sandbox` directly, more than import the `yoloai` package. Nothing in depguard prevents it — the only CLI scoping rule blocks the Tart backend, not the sandbox façade. So the layering claim is aspirational doc, not enforced fact.
- **Why it bothers me:** Two parallel surfaces with no canonical choice is the worst of both worlds (this is the same anti-pattern the prior round's F2 flagged for the Client/sub-handle mix, recurring one layer down). A reader can't predict whether a given CLI operation goes `yoloai.Client.X` or `sandbox.X`; both are "normal." The principle (§2 boundary discipline) and the doc both assert a single path that the linter doesn't enforce and the code doesn't follow. Either the layering claim is real and must be enforced, or it's false and the doc must stop asserting it.
- **Greenfield alternative:** Decide the layer contract and enforce it. If `yoloai.Client` is the CLI's only orchestration entry point, add a depguard rule denying `internal/cli` → `internal/sandbox` (allowing only direct leaf imports for pure helpers like `status`/`yoerrors` if those are explicitly "shared utility" tier). If instead the CLI is *allowed* to use the sandbox domain directly (reasonable — it's all `internal/`), then delete the `yoloai.Client` façade's redundant methods and stop claiming single-path layering. Pairs with F1: once the façade stops re-exporting, "import the leaf" becomes the obvious canonical answer.
- **Migration cost:** The depguard rule + doc fix is a day. The underlying convergence folds into F1.

#### F3 — The public `yoloai` package constructs its errors through the internal façade (`sandbox.NewUsageError`), not `yoerrors`

- **Severity:** MED
- **Where:** `yoloai`-root error construction call sites routing through `internal/sandbox/errors.go:40` (`var NewUsageError = yoerrors.NewUsageError`); `internal/yoerrors/errors.go:60` is the canonical home. `ARCHITECTURE.md` claims yoerrors is "exported via the yoloai package."
- **Observation:** The error constructors live in `internal/yoerrors`. They're re-exported by the `internal/sandbox` façade. The public `yoloai` package — which is *above* `internal/sandbox` in the layering — reaches back *down and sideways* through the façade alias to build errors, instead of importing `yoerrors` directly. ARCHITECTURE describes the export direction backwards.
- **Why it bothers me:** The public package depending on the internal façade for a primitive (error construction) is exactly the inverted dependency that makes the façade impossible to remove — F1 can't fully land while `yoloai` itself is a façade consumer. And the doc's "exported via yoloai" is the reverse of what the code does, which will mislead the next person trying to trace the contract.
- **Greenfield alternative:** `yoloai` imports `internal/yoerrors` directly. Fix the ARCHITECTURE sentence to describe the real direction (yoerrors is the leaf; both `yoloai` and the CLI import it directly).
- **Migration cost:** Hours. Mechanical import swap + one doc sentence.

#### F4 — `Confirm` (terminal prompt) lives in the domain layer, violating §2 "the mechanism layer never prompts"

- **Severity:** MED
- **Where:** `internal/sandbox/confirm.go:45` — `func Confirm(ctx, prompt string, input io.Reader, output io.Writer) (bool, error)`. Called by 8+ CLI files (apply_nocommit, new, prune, apply_selective, apply_format_patch, profile, destroy, apply_overlay).
- **Observation:** development-principles §2 explicitly says the mechanism/domain layer "never prompts" and carries "no human-readable strings." `Confirm` is a terminal y/n prompt — pure interaction policy — sitting in the sandbox domain package. Every caller is a CLI command; none is domain code.
- **Why it bothers me:** This is the canonical §2 violation: an interaction decision (do we ask the human? what do we ask?) encoded in the layer whose job is to *do the thing*, not to negotiate with a user. It also makes the domain untestable-without-IO for no reason — the policy belongs where the IOStreams already live (the CLI).
- **Greenfield alternative:** Move `Confirm` to `internal/cli/cliutil` (or `cliutil.Prompt`). The domain functions that currently gate on it should instead take an already-decided `confirmed bool` / `force bool` parameter, or return a typed "needs confirmation" sentinel (`yoerrors`) that the CLI catches and resolves. The latter keeps the domain pure and lets the CLI own the prompt copy.
- **Migration cost:** Day. 8 call sites, each already at a CLI boundary; the gating predicate moves up one layer.

#### F5 — Formatting helpers (`FormatSize`/`FormatAge`/`DirSize`) live in the `status` domain leaf; §2 says formatting is policy

- **Severity:** MED
- **Where:** `internal/sandbox/status/status.go:59` (`FormatAge`), `:74` (`DirSize`), `:93` (`FormatSize`).
- **Observation:** §2 reserves human-readable string production for the policy/CLI layer. `FormatAge` ("3 days ago") and `FormatSize` ("1.4 GiB") are presentation concerns embedded in a domain read-model leaf. `DirSize` is fine in the domain (it's a measurement); `Format*` are not (they're rendering).
- **Why it bothers me:** Same §2 boundary as F4, lower stakes. It's the kind of helper that looks harmless but is exactly why the domain leaf can't be reused by a non-CLI embedder without dragging a presentation opinion (units, "ago" phrasing, locale) along with it.
- **Greenfield alternative:** Move `FormatAge`/`FormatSize` to `internal/cli/cliutil` (or a `render` helper). `DirSize` stays. The status leaf exposes raw `time.Time` / `int64`; the CLI renders.
- **Migration cost:** Hours. Two functions, CLI-only callers.

### Relocated god-files

#### F6 — `lifecycle/lifecycle.go` is a relocated 1474-line / 46-function god-file spanning six concerns

- **Severity:** HIGH-MED
- **Where:** `internal/sandbox/lifecycle/lifecycle.go` (1474 lines, 46 funcs). Sibling: `internal/sandbox/create/create_prepare.go` (1025 lines, 3 pipelines).
- **Observation:** F5 moved `lifecycle.go` into its own leaf but did not decompose it. One file still holds six distinct concerns: (1) Start/Stop/Destroy, (2) the Reset cluster (`resetOverlayDirs`/`resetCopyWorkdir`/`resetInPlace`/…), (3) Restart/relaunch (`recreateContainer`/`relaunchAgent`/`sendResumePrompt`/…), (4) config-patching (`patchConfigVscodeTunnel`/`patchConfigDebug`/`PatchConfigAllowedDomains` — three near-identical functions), (5) low-level process helpers (`tmuxCmd`/`tmuxShellPrefix`), (6) `rsyncDir`. `create_prepare.go` similarly bundles three pipelines (profile-merge, dir-setup, archetype/devcontainer).
- **Why it bothers me:** A leaf package is supposed to be the unit of comprehension. A 1474-line leaf with six concerns is a god-package-in-miniature — F5 changed the *address* of the gravity, not its mass. The three near-identical `patchConfig*` functions are a missed dedup that signals the file grew by accretion. This is the same finding as the prior round's F5 (create.go god-file), recurring in the carve's output.
- **Greenfield alternative:** Within the `lifecycle` leaf, split into `lifecycle.go` (Start/Stop/Destroy core), `reset.go`, `restart.go`, `config_patch.go` (and collapse the three `patchConfig*` into one parameterized helper). These are same-package file splits — no new import edges, no `Deps` plumbing — so the cost is low and the comprehension win is immediate. Same treatment for `create_prepare.go` → `prepare_profile.go` / `prepare_dirs.go` / `prepare_archetype.go`.
- **Migration cost:** Day per file. Pure intra-package file moves + one dedup; tests unchanged.

### Cross-language and design-checkpoint hygiene

#### F7 — `AGENT_STATUS_SCHEMA_VERSION` is an unfenced cross-language constant (Go + Python), unlike its RUNTIME_CONFIG sibling which has an automated fence

- **Severity:** MED
- **Where:** `internal/sandbox/status/status.go:213` (`const agentStatusSchemaVersion = 1`), `internal/runtime/monitor/sandbox-setup.py:129` (`AGENT_STATUS_SCHEMA_VERSION = 1`), referenced in `status-monitor.py:83`. Compare: `internal/sandbox/runtimeconfig/schema_version_test.go` fences `RUNTIME_CONFIG_SCHEMA_VERSION`.
- **Observation:** The agent-status contract version is duplicated as a literal `1` in Go and Python with only a comment ("Must equal the AGENT_STATUS_SCHEMA_VERSION constants in sandbox-setup.py") binding them. There is no test that fails when they drift. The RUNTIME_CONFIG schema version had exactly this problem and the prior round's F25 fixed it with an automated cross-language check. The fix was applied to one of the two cross-language constants and not the other.
- **Why it bothers me:** A schema-version constant whose entire job is to detect contract drift, that itself can silently drift, is the contract bug it was meant to prevent. The asymmetry (one fenced, one not) is worse than both-unfenced because it implies the class is handled.
- **Greenfield alternative:** Extend the `schema_version_test.go` pattern (or a shared fence) to assert `agentStatusSchemaVersion` (Go) == `AGENT_STATUS_SCHEMA_VERSION` (parsed from the Python source) at `make check` time. Same mechanism as F25.
- **Migration cost:** Hours. The test harness for the runtime-config fence already exists to copy.

#### F8 — `api_surface.go` self-certifies its own deletion condition while sitting at ~55–60% drift; retire it

- **Severity:** MED
- **Where:** `api_surface.go` (2814 lines, `//go:build never`). Header (~line 38): "Once each is implemented on real types in W-L8b this file is deleted." general-principles §12 (design-is-a-hypothesis: "read it like a header for direction, never cite it as binding"); §8 (document-the-no / no half-finished).
- **Observation:** The file's own stated lifecycle says it is deleted once the surface is implemented — and the surface *is* implemented (that's what the layering refactor did). Meanwhile it has drifted: `Status()`/`Files()`/`Wait`/`ProxyMCP`/`RestartOptions` appear in the design but not the real code; `Create` exists in real code but not the design; the sub-handle shape (Q-G) is only partly wired. It's a 2814-line uncompiled file that a reader can no longer trust as either spec (drifted) or history (it claims it should be gone).
- **Why it bothers me:** §12 already warns it's non-binding, but a 2814-line non-binding file at 55% drift is a comprehension tax and a trap — the prior round had to repeatedly caveat "but the implementation didn't catch up." Keeping a design checkpoint past its self-declared expiry is the half-finished state §8 says to avoid. The valuable part — the Q-A…Q-Y resolution rationale — is the part worth preserving, and it's buried in a file flagged for deletion.
- **Greenfield alternative:** Salvage the Q-block resolutions into `docs/dev/working-notes.md` as dated D-entries (they're design decisions with rationale — exactly what working-notes is for), then delete `api_surface.go`. ARCHITECTURE already documents the real surface; the public package's godoc is the live contract.
- **Migration cost:** Half-day to extract the Q-blocks into working-notes; deletion is instant. (User explicitly raised api_surface.go — worth a direct decision.)

### Doc / principle divergences (user asked to hear these)

#### F9 — development-principles §2 documents stale module-root import paths

- **Severity:** LOW
- **Where:** `docs/dev/principles/development-principles.md` §2 (lines ~81–93) — lists layering with module-root paths `agent/`, `config/`, `runtime/`, `sandbox/`, `workspace/`, `extension/`.
- **Observation:** Those packages all live under `internal/` now (`internal/sandbox`, `internal/runtime`, …). The principle that *governs* boundary discipline cites import paths that no longer exist, so a reader checking their code against §2 can't match the paths.
- **Why it bothers me:** A principle doc that's stale on the exact thing it governs (import boundaries) undermines its own authority — and the prior round leaned on §2 heavily. Cheap to fix, high trust-cost to leave.
- **Greenfield alternative:** Update §2's paths to the real `internal/...` layout and confirm the layer ordering still matches the F5 DAG.
- **Migration cost:** Minutes.

#### F10 — `ARCHITECTURE.md:49` asserts a single-path layering the code doesn't follow (see F2)

- **Severity:** LOW (doc), but it's the load-bearing layering claim
- **Where:** `docs/dev/ARCHITECTURE.md:49`.
- **Observation:** Covered in F2 — the "CLI always goes through yoloai.Client/SystemClient" claim is contradicted by 42 direct `internal/sandbox` imports. Listed separately because the *doc* needs fixing regardless of which way F2's enforcement decision goes: either make it true (depguard) or rewrite it to describe reality.
- **Greenfield alternative:** Resolve F2 first, then make this sentence match the chosen contract.
- **Migration cost:** Minutes (follows F2).

### Calibration notes (not findings — recorded so the next round doesn't re-flag)

- **Python `sandbox-setup.py` (1332 lines) is still untested / not mypy-strict.** Pure surface (`setup_helpers.py` 204 + `tmux_io.py` 180) is ~28% of the Python. This is a known, accepted state (the imperative setup body resists unit testing). Not re-raising as a finding; noting it persists.
- **Borderline seams that are fine as-is:** `runtime/tart` (the `tart_base.go` seam reads clean), `config` (`yaml_path.go` seam), `patch/apply.go` (`refs.go`/`baseline.go` seams). These were checked for the F1/F6 god-file pattern and do *not* exhibit it — they're cohesive at their current size.

## Resolved direction (2026-05-30)

The CLI↔sandbox discussion resolved into a committed architecture (see working-notes + `project_public_api_direction` memory):

- **yoloAI is a library first.** The engine runs in-process. The CLI is a proof-of-concept consumer kept around to *keep the library honest* about completeness.
- **A separate daemon app (own module) will embed the library** to expose REST + MCP; GUIs/agents consume that daemon over the wire. yoloAI itself never becomes a daemon or a thin wire client. ("Layer 3", deferred.)
- **Committed now: "Layer 1"** — make the public Go surface complete enough that *every capability* is reachable through it, with the contract types lifted out of `internal/`.
- **Forcing function:** a separate-module daemon physically cannot import `internal/`, so the public surface must be real. The CLI only keeps us honest if it is *constrained* to that surface — today it is not (42 files reach into `internal/sandbox`).
- **Definition of done:** `internal/cli` *and* `internal/mcpsrv` compile with **zero `internal/sandbox` imports**, enforced by depguard. `internal/mcpsrv` is the closest prototype of the future daemon and is the canary.

This reframes the ordering: F1/F2/F3/F10 are no longer "nice cleanups" — they are the layer-1 spine. The chosen mechanism is **relocate, not twin-and-adapt**: move the contract-owning leaves (`yoerrors`, status read-model, dir/option input types) from `internal/sandbox/*` to public package paths and repoint every importer (engine, CLI, mcpsrv) at the public leaf. That single move makes the types public *and* lets the CLI drop the façade — no adapter boilerplate, no public/private type drift.

## Recommended ordering

**Spine — Layer 1 (do as a sequenced program; see `docs/dev/plans/`):**

1. **F8** — Retire `api_surface.go`: salvage Q-blocks to working-notes, delete the file. Clears the drifted design checkpoint before reshaping the real surface. (Half-day.)
2. **F4 + F5** — Move `Confirm` and `Format*` out of the domain into `cliutil`. Removes the two §2 policy violations and shrinks what has to be relocated. (1–2 days.)
3. **F1 + F3 (relocate)** — Lift the contract leaves out of `internal/`: `yoerrors` → public errors package; status read-model + dir/option input types → public; finish the Option lift (`StartOptions`/`CloneOptions` stragglers) and de-dup the double `DirSpec`; surface the ~6 bypass operations on the Client. Delete the façade re-exports as each leaf moves. (Multi-week, incremental, each step independently green.)
4. **F2 + F10** — Land the depguard fence (`internal/cli` + `internal/mcpsrv` denied `internal/sandbox`) and rewrite `ARCHITECTURE.md:49` to the now-true contract. This is the acceptance gate, not a prerequisite. (Day.)

**Off-spine (independent, fold in opportunistically):**

5. **F6 + F7** — Split `lifecycle.go`/`create_prepare.go` into intra-package files (collapse the three `patchConfig*`), and extend the schema-version fence to `AGENT_STATUS_SCHEMA_VERSION`. (1–2 days.)

F9 (§2 stale paths) is a minutes-long doc fix to fold in whenever §2 is next touched.
