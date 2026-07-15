> **ABOUTME:** Audit plan that re-validates the public-layering Move (S6) against what the
> branch actually built, before executing it — output is decisions and a resequenced plan,
> not code. Records verdicts inline (D97) as re-audits land, most recently confirming
> `runtime` is Move-ready.

# Pre-Move audit — re-validate "the Move" (S6) before executing it

**Status:** Audit plan, 2026-06-26. Written to survive a context compaction and be
**executed fresh**. The goal is NOT to do the Move — it is to decide *whether and
how the Move plan should change* given everything learned on the `public-layering`
branch since the Move was framed. Output is a set of decisions + a (possibly
re-sequenced) Move plan, not code.

## Why now

"The Move" (S6 of [public-layering.md](public-layering.md)) promotes the
**substrate** (`internal/runtime`) and **persistence** (`internal/store`) to public
packages, routes `Environment`→`Handle`, and un-blocks the deferred netpolicy N1c.
It was framed *before* this branch built: the fall-to-shell/resume layer (D96), the
four per-agent detection strategies (the merge-gate work), the `SeedFile.Content` /
`Definition.SettingsFileName` agent-layer additions, the netpolicy Strategy/CanEnforce
axis (D90), the secure-secrets broker design (D95), and a pile of agent-auth /
seeding / build-infra learnings. The public surface should reflect what we now know.

## Inputs to read (ground truth — read before judging)

- **The Move itself:** [public-layering.md](public-layering.md) (the Frame),
  [module-split.md](module-split.md).
- **Design cluster (D84–D96):** `../substrate-interface.md` (D84),
  `../persistence-helper.md` (D87), `../session-layer.md` (D88), `../agent-layer.md`
  (D89), `../netpolicy.md` (D90), `../envsetup.md` (D91), `../secure-secrets.md`
  (D95), `../copyflow-layer.md` (D86), `../agent-detection.md` (D96), and
  `../../decisions/working-notes.md` entries **D84–D96**.
- **This branch's tail:** [agent-detection-strategies.md](agent-detection-strategies.md)
  (merge gate + per-agent strategy outcomes), [agent-owned-detection.md](agent-owned-detection.md).
- **The code as it actually is:** `internal/runtime/` (the `Backend`/`Process`
  interfaces in `runtime.go`/`registry.go`, the six backends, `runtimeconfig`),
  `internal/store/`, `internal/agent/`, `internal/envsetup/`,
  `internal/orchestrator/envspec/`, `internal/netpolicy/`, and the **current public
  root** (`yoloai` package: `client.go`, `sandbox.go`, `environment.go`,
  `agent.go`, `backend.go`, `network.go`, etc.).
- **What changed:** `git log --oneline main..public-layering` (the whole branch).

## Audit dimensions (each produces findings: *holds / changed / new-question*)

Run these as independent investigations (good fan-out for parallel subagents),
then synthesize. For each, the bias is "what did the branch teach that the Move
plan didn't know?"

1. **Assumptions still hold?** List the Move's load-bearing assumptions and test each:
   - **Substrate is agent-free (D84).** Did the detection/seeding work leak
     agent-awareness into `internal/runtime`? (Check: `runtimeconfig` still fuses
     agent fields; the monitor scripts; does promoting `internal/runtime` drag agent
     concepts public?)
   - **The session-layer carve (D88: `ProvisionSpec`/`ProcSpec`/`AgentLaunchSpec`,
     neutral PID 1).** Is it built or still deferred? The branch shipped fall-to-shell
     + detection on top of the *un-carved* `runtime-config.json` DTO. **Is the carve a
     prerequisite for the substrate going public** (else the public substrate exposes
     the agent-fused DTO)? This is likely the central question.
   - **Agent-layer boundary (D89).** The branch added `SeedFile.Content`,
     `Definition.SettingsFileName`, four per-agent hook/plugin/flag injectors, and the
     embedded opencode plugin. Does `agent → envsetup → substrate` still hold one-way?
     Is the public agent *catalog* shape still right with detection strategies in play?
   - **Persistence/store (D87).** Did the branch change the store boundary (perms.go
     moved into store, `store.Environment` usage)? Is `store` ready to be public?

2. **Surface — what should be public?** For each Move-public package (substrate
   `runtime`, `store`), enumerate the would-be-exported surface and judge fit against
   the branch's reality. Then: are there **new things that deserve public surface** —
   the netpolicy strategy axis (D90), the agent capability catalog (D89 §public
   surface), `SeedFile.Content`/seed mechanism, the detection-strategy concept? Or do
   they stay internal until their own effort?

3. **Hidden — what must stay internal?** Anything currently exposed or planned-public
   that the branch shows should stay private: the `runtime-config.json` DTO, the
   monitor/python scripts, per-agent hook-registration details, the fall-to-shell
   wrapper internals, the home-seed bind-mount/placeholder mechanism.

4. **Layout / hierarchy.** Is the package structure still right? The branch added
   `internal/netpolicy`, `internal/envsetup`, `internal/orchestrator/envspec`, and a
   *deferred* `detectspec`. Should detection get its own package? Does the
   substrate/session/agent/copyflow/envsetup/netpolicy split still map cleanly to the
   intended public layers, or has anything drifted?

5. **Sequencing — what should land BEFORE the Move?** Candidates, each with a
   hold-or-block judgment:
   - **The session-layer carve (D88)** — likely a hard prerequisite (don't publish an
     agent-fused substrate DTO).
   - **The merge-gate strategy work** (`agent-detection-strategies.md`: DetectionSpec
     formalization + the python-spine) — does the public *agent* surface need to
     stabilize first? (It's already a merge blocker; is it also a *Move* blocker?)
   - **Secure-secrets / credential broker (D95)** — does the credential surface need
     shaping before `store`/substrate go public (CredentialSource reserved seam)?
   - **Aider's hook-assisted active-gap**, the bind-mount-placeholder codegen idea, the
     reserved netpolicy N1c (un-blocked *by* the Move, so it's after).

6. **De-risk the Move.** It's a large mechanical promote (internal→public + route
   `Environment`→`Handle`). Propose the verification: `make releasetest` /
   `go vet -tags 'integration e2e'` (the layering fences + tagged tests `make check`
   skips), the runtime conformance suite, the `internal_leak_fence` tests, and a
   real-Docker smoke. Confirm the move-by-move resume strategy.

## Method

- Read the inputs; build a *current-vs-planned* map per dimension.
- Produce findings per dimension (holds / changed / new-question), each with a
  file:line or design-doc anchor.
- **Synthesize** into: (a) concrete Move-plan modifications, (b) a sequenced list of
  prerequisite tasks (esp. is the D88 carve a blocker?), (c) surface/hide/layout
  decisions, (d) open questions that are the user's call.
- Record outcomes the project way: a decision entry (next **D97**) for any settled
  re-shaping, critiques for anything contested, and update `public-layering.md` +
  this file with the verdict. Don't silently rewrite the Move — surface the deltas.

## Execution notes

- Fan out dimensions 1–5 to parallel read-only investigators (Explore/general-purpose
  or `fork`), each returning a structured finding set; synthesize in the main agent.
  (Not a Workflow unless the user opts in — plain `Agent` calls.)
- Keep it an *audit*: read + decide, don't refactor. The only writes are docs
  (decisions/critiques/plan updates).
- Start from `git log --oneline main..public-layering` to ground "what changed,"
  then the design cluster, then the code.

## Verdict (2026-06-26) — see D97

**Audit run.** Six dimensions fanned to read-only investigators; synthesized into
**[D97](../../decisions/working-notes.md)**. Headline: the **functional** carve (D88 S0–S3:
Launch primitive, agent-free keepalive, agent-reroute-over-Launch) is **built and verified**, but
the substrate/store **public surface is not yet agent-free**, so the Move is **not yet a pure
`git mv`**. It needs a bounded **surface-cleanup Shape sub-phase** first (now **Stage 3b** in
[public-layering.md](public-layering.md)), shaped by the WHAT/HOW lens recorded as
**architecture-principles §4** (substrate verbs are request-in / no-mechanism-out): most of the
runtime "agent-leak" is mis-named substrate HOW that **folds into `Launch`/`InteractiveExec`**
(ingredient-vendors), one field is a kept **fact-query** (`AgentProvisionedByBackend`), and one
is agent config **payload** to re-home (`AgentInstallMethod`). See D97 for the full cut.

**By dimension:**
1. **Assumptions** — `InstanceConfig`/`ProcSpec` are agent-free; the agent-fused DTO lives in
   `orchestrator/runtimeconfig` (not the substrate). BUT agent-shaped exports survive in
   `internal/runtime` (`BackendDescriptor`'s 3 agent fields, `AgentCommandPreparer`,
   `InteractiveSession`/tmux — DF31) and `internal/store` (`Environment.{AgentType,Model}` — Q104).
2. **Surface** — ~85 runtime + ~90 store symbols are clean substrate; the 7 above are the only
   agent-leaky ones. New surface (netpolicy `Strategy`/`CanEnforce`, agent catalog) is **not** part
   of this move — separate later promotions.
3. **Hidden** — keep `runtimeconfig` DTO, monitor scripts, hook details, fall-to-shell internals,
   and the `paths.go` on-disk *filename* constants internal (expose path *helpers* only).
4. **Layout** — new packages map cleanly; the deferred `detectspec` was correctly not built
   (YAGNI, validated by the strategies task). `netpolicy/compose.go` imports `agent` (upward
   escaped-dep) — invert via the D89 floor-as-payload re-homing before netpolicy promotes.
5. **Sequencing** — **ordering constraint:** `store` imports `runtime` ⇒ runtime first/with store.
   D95 seam reserved & sufficient; detection merge-gate satisfied (clears main-merge, independent of
   the Move). Surface-cleanup (3b) is the one hard prerequisite.
6. **De-risk** — repoint `.golangci.yml` fences (lines 66/98/112 + forbidigo) + `internal_leak_fence`
   auto-passes; `make releasetest` is the gate; `go vet -tags 'integration e2e'` recompiles the ~28
   build-tagged files; procedure proven by three prior moves.

**Rejected:** "move-then-clean" (promote agent-shaped fields now, break later) — violates Frame
strategy #5 and the no-scope-cut standard.

**Open (user's call):** Q-A store split vs. opaque-transport for `AgentType/Model`; Q-B
branch-capstone vs. merge-then-fresh-branch for the Move.

## Re-audit (2026-06-27, on `substrate-move`) — post-Q104 surface re-check

Re-ran the surface dimensions after Q104 (D102) landed and the carve-free work, to
get a current (not D97-stale) Move-readiness map. Two read-only investigators
audited the `copyflow` and `agent` export surfaces; both initially recommended
"unexport ~25 / ~3 symbols" — **both lists were rejected on verification**: the
copyflow "leaks" are already-lowercase helpers (the agent misread `go doc -u`,
which shows unexported symbols), and the agent fields (`ApplySettings`, `SeedFiles`,
`HeadlessCmd`, `PromptMode`, …) are read by *separate* packages
(`orchestrator`/`envspec`/`envsetup`/root `discovery.go`) so they cannot be
unexported without breaking the build. Verify subagent surface claims against actual
cross-package callers.

**Current state:**
- **Import DAG is clean and promotable.** `agent` is a zero-import leaf; `runtime`
  is base; `store → runtime` (clean of agent/orchestrator post-Q104);
  `copyflow → {runtime, store}`. None import `orchestrator` or `agent`.
- **Promotion order is import-constrained.** A public package can't import an
  internal one, so **`store`/`copyflow` cannot promote until `runtime` is public**,
  and `runtime` still exports `AgentLaunchPrefix` (the deferred, macOS-coupled 1a-ii)
  + the `InteractiveSession`/tmux primitive (DF31 — *accepted as substrate* per the
  D99 endgame; IOSession is the later ergonomic promotion, not a Move blocker).
  `AgentProvisionedByBackend` stays a substrate fact-query (D97).
- **`copyflow` is Move-ready.** Clean imports + high-level verbs. Only an *optional*
  trim: ~9 exported funcs have no caller outside the package
  (`AdvanceBaseline`, `GenerateFormatPatch{,ForRefs}`, `GenerateOverlayPatch`,
  `GenerateUncommittedDiff`, `LoadAllDiffContexts`, `UpdateOverlayBaselineToHEAD`) —
  candidates to unexport to slim the frozen surface; not blockers (`ParseNumstat`/
  `ResolveRef[s]` are public vocabulary even if not yet called externally).
- **`agent`: the public surface is the root catalog, not a package `git mv`.** Per
  **D89 §public-surface**, the agent layer's public surface is the read-only
  capability catalog (`yoloai.AgentTypes()` → `AgentInfo`); the fat `Definition` +
  code-adapter **stay internal** (`orchestrator`/`envspec` read them; they can't be
  public). So "promoting agent" is **enriching the root `AgentInfo` catalog**, not
  exposing `internal/agent`. Done 2026-06-27: added `AgentInfo.SupportsResume`
  (native-resume, the one capability D89 named that was still missing; only Claude
  sets `ResumeFlag`). The agent layer's public surface is now complete; there is no
  separate `yoloai/agent` package and no `internal/agent` `git mv`.

**Net:** the agent layer's public surface is sealed. The `git mv` procedure first
applies to **`runtime`+`store`+`copyflow`**, gated solely on the `runtime`
`AgentLaunchPrefix` cleanup (1a-ii) — Linux-buildable but Tart/Seatbelt
prefix-application needs the mac queue. (User chose agent-first + narrow-the-surface,
2026-06-27; the narrowing was already D89's design — `Definition` stays internal.)

**Update (2026-06-27) — `AgentLaunchPrefix` cleared; runtime Move-unblocked.** The
per-backend agent-launch wrap moved off `runtime.BackendDescriptor` into the
orchestrator's launch layer (`internal/orchestrator/launch/prefix.go`, keyed by
backend type) — it is launch-assembly knowledge, not a substrate fact, so it lives
above the substrate (D97 / arch-principles §4). The reshape is behavior-preserving:
the same constant is written to `runtime-config.json`'s `agent_launch_prefix` and
applied unchanged in-sandbox (`sandbox-setup.py`) and on restart; only the
create-time *source* and the migration resolver moved (both now read the launch
table). The two readers (`create.go`, `system.go`'s migration resolver) were
repointed; the DTO field + application path are untouched. Tart/Seatbelt confirmatory
smoke is queued as **V3** (low risk — the wrap value/application are byte-identical).
With this, `runtime`'s only remaining "Agent"-named export is `AgentProvisionedByBackend`
(a kept substrate fact-query), and `InteractiveSession`/tmux stays as an accepted
substrate primitive (D99; IOSession is the later ergonomic). **`runtime` is now
Move-ready**, so `store` (→runtime) and `copyflow` (→runtime,store) are unblocked.
The next step is the mechanical `git mv runtime store copyflow` + import sweep +
`.golangci.yml` fence repoint (depguard L66/98/100 + the runtime-glob L112) +
`make releasetest`.

## Cross-references

- [public-layering.md](public-layering.md) (the Frame this audits), the D84–D96
  decisions, and the branch memory `project_public_layering.md` §SHAPE PHASE.
