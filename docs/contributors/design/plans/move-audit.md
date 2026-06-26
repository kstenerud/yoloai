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

## Cross-references

- [public-layering.md](public-layering.md) (the Frame this audits), the D84–D96
  decisions, and the branch memory `project_public_layering.md` §SHAPE PHASE.
