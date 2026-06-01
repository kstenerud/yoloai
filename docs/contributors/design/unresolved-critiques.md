# Critique — 2026-05-30 Post-F1-Close: the leak detector lies

## Summary

The Layer-1 spine was declared **FULLY COMPLETE** (D52): `f1KnownLeaks` empty, F1 closed,
`MergedConfig` promoted to public `ResolvedProfileConfig`. That declaration is **false**, and
the test that certifies it is green for the wrong reason.

`TestPublicAPI_NoInternalLeaks` is structurally blind to type aliases. `walkObj` returns
immediately for any alias TypeName; `walkType` (the signature/field walker) has no
`*types.Alias` case at all. So when the public surface re-exports an internal type via
`type X = internal.Y` — the *sanctioned* fix — the detector never descends into `X`'s fields
to check whether *they* reference other, un-aliased internal types. Aliasing the top of a
struct silences the detector for the entire tree beneath it.

The concrete consequence: `yoloai.Info` (`type Info = sandbox.Info` → `status.Info`) carries
`Meta *store.Meta`. `store.Meta` is internal and **not** re-exported at the root, and it is an
iceberg — `WorkdirMeta`, `[]DirMeta`, `DirMode`, `runtime.IsolationMode`, `agent.AgentName`,
`*config.ResourceLimits` hang off it, none re-exported. `Info` is returned by `Client.Run`,
`Client.List`, `Sandbox.Inspect`, and `SystemClient.ListAcrossBackends` — the four most central
entry points on the whole surface. An external embedder can hold a `*yoloai.Info` and **cannot
name the type of `.Meta`**. This is a bigger leak than the `MergedConfig` one we just spent a
milestone closing (that one was returned only by the `ProfileAdmin.Info` admin verb).

The same gap, seen from the import side: the `cli-sandbox-facade-scope` depguard rule denies
only the `internal/sandbox` façade package while *allowing* `internal/sandbox/store` (plus
`patch`/`archetype`) and not guarding `internal/runtime`/`internal/config` at all. So the CLI
legitimately handles `store.Meta` directly — the very type that isn't public. A separate-module
daemon physically cannot import any of these, so the CLI passes a gate the daemon would fail.
"The CLI keeps us honest" is therefore only partly realized.

The through-line: we closed the small, visible leak and declared total victory while the
detector couldn't see the large one. The honest sequence is to make the test tell the truth
first (descend through aliases → it goes red on `store.Meta`), then decide deliberately whether
to promote `store.Meta` or park it as an *explicit, eyes-open* deferral. A silent blind spot is
the worst of the three states.

## Findings

### G1 — The F1 leak detector is blind to type aliases; "F1 closed" is unverified and currently false

- **Severity:** CRITICAL
- **Where:** `public_api_test.go` — `walkObj` (lines ~125-128: `if o.IsAlias() { return }`) and
  `walkType` (lines ~280-322: switch has cases for Named/Pointer/Slice/Array/Chan/Map/Struct/
  Signature/Interface but **no `*types.Alias` case**). Leak site: `sandbox_options.go:13`
  (`type Info = sandbox.Info`) → `internal/sandbox/inspect.go:39` (`type Info = status.Info`) →
  `internal/sandbox/status/status.go:50` (`Meta *store.Meta`). Target: `internal/sandbox/store/meta.go:23`.
- **Observation:** The detector walks named-type declarations and function signatures but treats an
  alias as a terminal. From the type-decl path, `walkObj` bails on `IsAlias()`. From the
  signature path (e.g. `func (c *Client) Run(...) (*Info, error)`), `walkType` unwraps the pointer,
  reaches the `*types.Alias`, matches none of its switch arms, and silently stops. Either way the
  alias's fields are never inspected. So `f1KnownLeaks` being empty does not mean "zero internal
  leaks" — it means "zero internal leaks *that aren't hidden behind an alias*." Every alias in
  `sandbox_options.go`/`names.go` is an un-audited subtree.
- **Why it bothers me:** This is precisely the "test that lies" the project's no-lying mandate
  forbids: green while the public API still leaks. We built the Layer-1 done-ness claim, the D52
  working-note, and the memory entry ("FULLY COMPLETE, no deferrals remain") on a detector that
  cannot see the biggest remaining leak. The MergedConfig milestone — real work — was the *easy*
  half; the alias-hidden `store.Meta` tree is the hard half, and the test let us skip it without
  noticing.
- **Greenfield alternative:** Two separable fixes.
  - **(a) Detector honesty (do first):** add a `*types.Alias` case to `walkType` that unwraps via
    `types.Unalias` and recurses; in `walkObj`, stop blanket-returning on aliases — descend into
    the underlying named type's exported fields. The existing `aliased` de-dup in `report` already
    prevents flagging the sanctioned alias *target*; what we want surfaced is fields that reference
    *other* un-aliased internal types. Expect the test to go red on `internal/sandbox/store.Meta`
    (and its sub-tree) the moment this lands. That red is the truth.
  - **(b) The real leak, once visible:** promote `store.Meta` to a *carved* public read-model — **not**
    an alias of the storage struct (that publishes pile-3 mechanism and welds disk/wire format
    together), and **not** a field-for-field mirror. `store.Meta` is three types in one struct
    (see D53): **identity & posture** (expose), **config echo** (reframe — and it's nearly
    `ResolvedProfileConfig`'s shape, so *embed a resolved-config view* rather than re-listing knobs),
    **mechanism** (Version/YoloaiVersion/ImageRef/HasPrompt/Debug/UsernsMode/HostFilesystem/Archetype/
    BaselineSHA — drop). If deferring the carve, re-add `store.Meta` to `f1KnownLeaks` as an explicit,
    documented deferral. Not silence.
- **Migration cost:** (a) is hours — a localized test-helper change plus a baseline update.
  (b) is the open question for the chat: `store.Meta` is ~25 fields with a nested tree; a faithful
  public mirror is real work, comparable to or larger than the MergedConfig promotion.

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

### G7 — The public surface is incomplete: ~7 consumer capabilities reach `internal/` directly with no public verb

- **Severity:** MAJOR (this is G1/G2's leak seen from the consumption side — the surface isn't done)
- **Where (capability → what it reaches for, no `yoloai.*` verb):**
  - **Sandbox metadata / workdir-mode read** — `internal/cli/workflow/{apply,diff,baseline}.go` and
    mcpsrv call `store.LoadMeta()` directly to branch on copy/overlay/rw mode. *The* load-bearing
    gap; it's the `store.Meta` carve (G1) from the consumer side. Needs `Sandbox.Metadata()`.
  - **Agent-log read** — `internal/mcpsrv/tools.go` `sandbox_log` → `store.AgentLogPath()`. Distinct
    from `Sandbox.ContainerLogs()`. Part of the OBSERVE surface (G5). Needs `Sandbox.AgentLog()`.
  - **File exchange** — `sandbox_files_*` → `store.FilesDir()`. The third interaction surface (G5).
  - **Agent/model + backend discovery** — `internal/agent.AllAgentNames()`/`GetAgent()`,
    `runtime.Descriptors()` (CLI `system agents`/`backends`/`help`). A daemon building a create-UI
    must enumerate these. Needs `yoloai.Agents()`/`Backends()` discovery verbs.
  - **Stored-prompt get/set** — `sandbox prompt` → `store.PromptFilePath()`. AGENT-noun read/edit.
  - **Git tag** — `internal/cli/workflow/apply.go` → `workspace.CreateTag()` (not on `Workdir`).
  - **Extensions** — CLI `x` → `internal/extension` (experimental; lower priority).
- **Observation:** The mcpsrv daemon prototype — the canary for a real embedder — reaches into
  `internal/sandbox/store` for 5+ of its ~13 tools. Every one of these compiles today *only because*
  the depguard fence allows the leaf packages (G2). A separate-module daemon could not do any of it.
- **Greenfield alternative:** Add the missing verbs (metadata read-model, agent-log, files sub-handle,
  discovery, prompt get/set, Workdir tag), then tighten the depguard allow-list so the reach-ins fail
  the lint. This is the concrete, enumerable definition of "Layer-1 actually complete" — and it is
  *not* complete today, contra D52.
- **Migration cost:** Folds into the read-model spine; each verb is small, the metadata one is the
  same work as G1(b).

### G8 — `store.Meta` is a vague name, and code comments reference a phantom `meta.json` that is never written

- **Severity:** MINOR (the name) — but the phantom-filename comments are an active inaccuracy, the
  same "asserts something untrue" class as G1's lying test.
- **Where:** type `store.Meta`/`WorkdirMeta`/`DirMeta` in `internal/sandbox/store/meta.go`; the
  real on-disk constant `internal/sandbox/store/paths.go:36` (`EnvironmentFile = "environment.json"`);
  stale `meta.json` comments in `internal/sandbox/lifecycle/lifecycle.go` (several),
  `launch/launch.go:34`, `launch/vmworkdir.go:19,45`, `create/create.go:99`, `inspect.go:22`.
- **Observation:** Three names for one concept. (1) The Go type is `Meta` — "metadata about *what*?"
  is unanswered; the thing is the sandbox's creation-time descriptor (the same identity / config-echo /
  mechanism piles D53 splits in G1). (2) ~8 comments call the file `meta.json`, but no such file is
  ever written — the serialized filename is `environment.json`. A reader greps `meta.json` and finds
  nothing on disk, or sees `store.Meta` and infers the wrong filename. (3) It pairs with
  `store.SandboxState` (→ `state.json`), so a name aligned to its artifact (`Environment` →
  `environment.json`) would make the type / file / comment trio consistent.
- **Why it bothers me:** The phantom `meta.json` is the same defect class as G1's "test that lies" —
  comments asserting a file that doesn't exist. And the vague `Meta` name is exactly what we'd want
  to fix at the moment we carve it public (G1(b)): renaming an internal-only type is cheap now and a
  tracked breaking change once it's pinned on the public surface.
- **Greenfield alternative:** Two separable fixes.
  - **(a) Kill the phantom (do anytime):** replace the `meta.json` comments with `environment.json`.
    Comment-only; removes the inaccuracy independent of any rename.
  - **(b) Name the carve well (rides G1(b)):** when promoting `store.Meta` to a public read-model,
    give the public type an artifact-aligned, intent-revealing name (e.g. `Environment` /
    `SandboxEnvironment`, paired with `State`), not `Meta`. Decide the internal `Meta → Environment`
    rename in the same pass to close the type/file/comment mismatch end-to-end.
- **Migration cost:** (a) is minutes (comment-only). The internal rename is mechanical churn
  (`Meta`/`WorkdirMeta`/`DirMeta` are referenced widely). The public-name choice is free if made when
  the carved type is created in G1(b).

## Carried forward from the prior round (status unverified — check before next empty)

The 2026-05-30 Post-F5 Façade round's spine findings (F1/F2/F3/F8/F10) drove the Layer-1 work and
are substantially actioned (façade fenced, `api_surface.go` retired, ARCHITECTURE rewritten). These
off-spine items were **not** part of the spine and may still be open:

- **F6** — `internal/sandbox/lifecycle/lifecycle.go` (1474-line god-file, six concerns; three
  near-identical `patchConfig*` to collapse) and `create/create_prepare.go` (three bundled pipelines)
  — intra-package file splits, no new import edges.
- **F7** — `AGENT_STATUS_SCHEMA_VERSION` is an unfenced cross-language (Go+Python) constant; its
  RUNTIME_CONFIG sibling has an automated drift fence. Extend the fence.
- **F9** — `development-principles.md` §2 lists stale module-root import paths (`sandbox/` etc.) that
  now live under `internal/`.

## Recommended ordering

1. **G1(a)** — Fix the detector to descend through aliases. Watch it go red on `store.Meta`. Truth
   before cleanup. (Hours.)
2. **Decision (chat):** promote `store.Meta` to a public read-model now, or park it in
   `f1KnownLeaks` as an explicit deferral. Correct the D52 note / memory either way — the
   "FULLY COMPLETE, no deferrals" claim is overstated as it stands.
3. **G1(b) + G2 + G7** — If promoting: build the carved `store.Meta` read-model AND the rest of the
   missing verbs G7 enumerates (agent-log, files sub-handle, discovery, prompt get/set, Workdir tag),
   then tighten the depguard allow-list (drop `store`; weigh `runtime`/`config` fences) so the
   reach-ins fail the lint. This is the concrete definition of "Layer-1 complete." (Multi-day.)
4. **G5** — Reshape the agent-interaction surface (PTY bridge + activity stream, decoupled from
   caller stdio). Independent of the read-model spine, but decide it **before** a wire protocol
   exists. (Multi-day.) **G6** folds in (hide setup-wizard pile-3) with G1(b)'s mechanism sweep.
5. **G3 + G4 + G8(a)** — Naming/consistency sweeps, independent, fold in opportunistically. G8(a)
   (replace the phantom `meta.json` comments with `environment.json`) is comment-only and can land
   immediately; G8(b) (name the carved type `Environment`, not `Meta`) rides G1(b) in step 3. (Hours each.)
6. **Carried-forward F6/F7/F9** — verify status; action if still open.
