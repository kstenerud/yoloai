# Agent-owned detection + fall-to-shell + resume — the build contracts

**Status:** Design converged 2026-06-25 (Phase 0 of
[plans/agent-owned-detection.md](plans/agent-owned-detection.md)), not yet
implemented. This is the **build-facing contract doc** the plan's Phase 0 calls
for: it pins DD1–DD6 to concrete shapes so Phases 1–4 implement against a spec,
not an improvisation. The *rationale* lives in the converged layer designs — this
doc does not re-argue it:

- **Fall-to-shell** is [session-layer.md](session-layer.md) §"Fall-to-shell resume
  hint" (the `… ; exec $SHELL` launch shape, gated persistent × PTY, the
  user-initiated alternative to declined auto-relaunch).
- **Resume** is [agent-layer.md](agent-layer.md) Resume capability (resume command
  *or* `"none"`, honest characterization, session-id support flag).
- **Tier-2 / the mode selector** is [session-layer.md](session-layer.md) §Tier-2
  (`{hook-authoritative, heuristic-only, (future) hook-assisted}`, the blip fix,
  active-before-submit, the conditional respawn seed). Decision
  [D88](../decisions/working-notes.md)/[D89](../decisions/working-notes.md); this
  doc's own decision is **D96**.

**One-line definition.** Detection becomes the agent's own declaration (a compiled
`DetectionSpec`, mirroring `EnvSpec`/`ProcSpec`); the launch wrapper — not pane
death — **authoritatively writes `done`** on agent exit, drops the pane to an
interactive shell with a resume hint, and a staged **`yoloai-resume`** re-launches
the agent and re-establishes detection in one command.

## The problem the contracts must solve (from current state)

- Launch is `…cd '<wd>' && exec {agent_command}`
  (`setup_helpers.build_agent_launch_command`). The **`exec`** means the agent
  replaces the shell, so the **pane dies on agent exit** — that pane death is the
  *only* thing that writes `done` today (`status-monitor.py` `check_pane_dead` →
  `write_status(done, ec)`, latched by `in_done`).
- Naive fall-to-shell (drop the `exec`, append `; exec $SHELL`) keeps the pane
  alive, so **`done` never fires**; worse, the **heuristic** monitor reads the idle
  shell as `idle` — masquerading an exited agent as waiting. So `done` must become
  **wrapper-authoritative** and the detection runner must **honor** it.
- The mode selector already exists (`ResolveIdleMode` → `hook-authoritative` /
  `heuristic-only`). In hook-authoritative mode the monitor **runs no heuristics
  while the pane lives** — so a wrapper-written `done` survives untouched there.
  That is why Phase 1 ships hook-authoritative-only (Claude): it needs **no monitor
  change**. The heuristic clobber is the Phase-3 problem.

## DD1 — the `done` contract (wrapper-authoritative)

- **The write.** On agent exit the wrapper writes the **same `agent-status.json`
  v1 schema** — `{schema_version:1, status:"done", exit_code:<agent's real $?>,
  timestamp}`. It must **reuse `status-monitor.py`'s `write_status()`**, never
  hand-roll JSON in shell: the schema is fenced to `agentStatusSchemaVersion` in
  `internal/orchestrator/status/status.go` by `schema_version_test.go`, and a
  shell duplicate would silently drift. Mechanism: a thin status-writer CLI entry
  the wrapper invokes (`python3 status-monitor.py --write-status done --exit-code
  "$rc"` over the same `status_file`, or a shared `write_status` module imported by
  both) — single-sourced.
- **The monitor honors it (Phase 3).** The detection runner treats an on-disk
  `done` as a **latch equivalent to `in_done`**: while the agent process is absent
  (fall-to-shell shell in the pane) it does **not** run heuristics and does **not**
  clobber `done`. In hook-authoritative mode this already holds for free (no
  heuristics while the pane lives); the Phase-3 change adds it to the **heuristic**
  runner.
- **Composition with pane death.** Pane death now means "the user closed the
  fall-to-shell shell" — still a terminal event → `done` (idempotent; same schema).
  Both producers write the same record; the latch makes them compose.
- **Clearing the latch on resume.** Hook mode: the hook writes `active` on
  turn-start (existing). Heuristic mode: the **agent process reappears** (the
  wrapper re-execs the agent under `yoloai-resume`) — the runner sees a live agent
  pid / changed pane and resumes detection. The existing **conditional respawn
  seed** (seed `idle` only if on-disk is still `done`) is preserved unchanged, so
  it cannot clobber a synchronous pre-submit `active`.
- **`done` is "loose" (agent-process exited), not tier-3 session-over.** A
  persistent/PTY session whose agent `/quit`s is *not* session-over (the pane lives
  as a shell); `status=done` faithfully means "the agent process is not running,"
  which is exactly what host readers / `Wait` want. No new status value is
  introduced.

## DD2 — `DetectionSpec` (agent-owned, compiled at the boundary)

- A Go `DetectionSpec` is **compiled from `agent.Definition` at the orchestrator
  boundary**, exactly like `envspec.BuildEnvSpec` (new `detectspec.BuildDetectionSpec(def)`,
  parallel package; the orchestrator owns it, the runner never branches on the
  concrete agent). It **gathers** today's scattered detection inputs
  (`idle_mode` + the detector params + the resume declaration) into one compiled
  object serialized into the staged agent-config artifact the in-container monitor
  reads (the descendant of `runtime-config.json`, per the carve's schema split).
- **Fields:**
  - `Mode` — `{hook-authoritative, heuristic-only, (future) hook-assisted}`
    (`ResolveIdleMode` today; the third mode is the reserved seam, not built).
  - Heuristic params — `ReadyPattern`, `WchanApplicable`, `ContextSignal` (from
    `IdleSupport`); the hook event-map for hook mode (Claude `Stop`→stop,
    `PreToolUse`/`UserPromptSubmit`→start — the agent→completion contract, D92).
  - `ResumeCmd` — the resume command template, or `""`/`"none"` (DD4).
  - `FallToShell` — the rollout gate (DD5).
- **One runner contract.** The monitor consumes `Mode` + params; hook-authoritative
  and heuristic both map onto the one runner it already is. This is a
  *formalization* (gather what exists into a typed spec), not a rewrite of the
  detector stack.

### DD2 refinement — detection is a *strategy*, with a per-agent python-spine seam (reserved)

`Mode` is the enum form of a more general idea: **detection is a strategy the
agent supplies, defaulting to the heuristic stack.** The honest model (an
extension of D89's mechanism-vs-payload — detection is a per-agent *mechanism*):

- **The default strategy is the heuristic detector stack** — used by every agent
  with no better signal, *and by user/file-defined agents* (`~/.yoloai/agents/*.yaml`),
  which carry no code. This is today's `heuristic-only`.
- **An agent with a native turn-completion signal supplies its own strategy** —
  today only Claude (the `Stop` hook → `hook-authoritative`), but the survey
  ([research/agent-callbacks.md](research/agent-callbacks.md), **verified
  2026-06-25**) settled the load-bearing question: **all four other shipped agents
  expose a usable turn-completion callback** — Codex (`notify` →
  `agent-turn-complete`, rich JSON), Gemini (`AfterAgent` hook, GA ≥ v0.26.0 — note
  *not* `Stop`), OpenCode (`session.idle` via a plugin hook or the SSE stream), and
  Aider (`--notifications-command`, a bare *data-free* pulse). So the **heuristic
  stack is the *floor*** (legacy versions, unknown/file-defined agents), not the
  common case — which is what makes the strategy seam **worth building**, not just
  Claude-vs-default. Two design consequences the contract must honor: (1) callbacks
  come in **two families** — *agent-runs-our-command* (Codex/Gemini/Aider, and
  OpenCode's plugin) vs *we-subscribe-to-a-stream* (OpenCode's SSE) — the strategy
  interface must cover both; (2) Aider's payload-free pulse means the contract is a
  **thin "a turn ended" signal**, not "hand me the assistant message" — keep the
  strategy boundary minimal so the weakest callback still fits.
- **Stacking is reserved as exactly one composite, not a framework.** The one
  combination we already know we want is `hook-assisted` (hook authoritative +
  heuristic as a conservative backstop for a *missed* hook — session-layer.md
  §Tier-2's reserved third mode). General N-way parallel strategies with a
  winner-policy is **YAGNI** until a *second* composite actually appears; do not
  build the framework for one known stack.
- **The implementation seam — a per-agent python module ("the spine").** Detection
  runs **in the sandbox** (the python monitor), not in the host library, so the
  natural extension point for a code-needing strategy is a per-agent **namespaced
  python module loaded by convention** — not a Go adapter. This is a meaningful
  openness refinement of D89: D89 keeps *host-side procedural* mechanisms internal
  (Go-only), but an *in-sandbox detection* strategy can be a droppable python file,
  so **file-defined agents could eventually bring detection logic, not just data**.
  Trust holds: in-sandbox detection is **not** a security boundary — the host
  already treats `agent-status.json` as a hint (tier-2 is reliability, not
  security; session-layer.md), and the agent already runs arbitrary code as the
  same sandbox user — so a pluggable/user-supplied strategy widens no real attack
  surface. **Reserved, not built:** populate the spine with only the two real
  strategies (heuristic default, Claude hook) until research surfaces a third; the
  same generalizes to *other* per-agent in-sandbox specifics later. Mirrors how
  netpolicy reserved `egress-proxy` and envsetup reserved secure-secrets.

## DD3 — the wrapper (a staged generic script, parameterized)

- **Staged script, not an inline one-liner.** The exit lifecycle (run agent →
  capture `$?` → write `done` → print hint → `exec $SHELL`) plus an embedded resume
  command with flags/quotes is too fragile to hand-build as a giant `send-keys`
  string. Stage **one generic script** (e.g. `/yoloai/bin/agent-run.sh`, baked into
  the image beside `status-monitor.py`/`setup_helpers.py`); it is **not
  agent-specific** — agent specifics (resume command, hint text, the status-writer
  invocation) arrive via **env**, so a single script serves every agent.
- **The launch shape changes minimally.** `build_agent_launch_command` becomes
  `…cd '<wd>' && exec agent-run.sh <agent_command…>`: we still `exec` the
  **wrapper** (it becomes the pane's process), but the wrapper does **not** `exec`
  the agent — it runs the agent as a child, then takes over the pane as a shell.
  Sketch:
  ```sh
  "$@"                              # run the agent (argv = the agent command)
  rc=$?
  "$YOLOAI_WRITE_DONE" "$rc"        # thin CLI over status-monitor's write_status
  printf '%s\n' "$YOLOAI_RESUME_HINT"
  exec "${SHELL:-/bin/sh}" -l       # pane becomes a usable login shell, not dead
  ```
- **Secrets compose unchanged.** Today's inline `build_secret_exports` prefix
  exports into the wrapper's env, inherited by the agent child; the E3
  secrets-as-launch-env values arrive via `ProcSpec.Env`, already in the wrapper's
  env. The wrapper just runs `"$@"`. The `cd <workdir>` composes as today.

## DD4 — resume (`yoloai-resume` + the resume command)

- **New `Definition.ResumeCmd` (data).** Claude: `claude
  --dangerously-skip-permissions --continue` (or `--resume <session-id>` if a
  session id is injected at launch). `""`/`"none"` for agents with no native resume
  (Aider) → the hint offers a **fresh** start and *says so* (honest
  characterization, agent-layer.md — never print "resumed" when it wasn't).
- **`yoloai-resume`** — a staged script (`/yoloai/bin/yoloai-resume`) the
  fall-to-shell hint tells the user to run. It re-launches the agent **through the
  same wrapper** (`exec agent-run.sh <ResumeCmd>`) so a second `/quit` also falls to
  shell. The **durable monitor is still running** (DF46), so resume needs no new
  process — it re-spawns the agent into the pane and the monitor re-detects (hook
  flips `active`, or the heuristic runner sees the agent reappear). Mirrors the
  in-place respawn path, user-invoked from the shell.
- **Session-id (verify at Phase 2).** Deterministic resume of the *right*
  conversation can need a session id; for the single-session sandbox, Claude
  `--continue` (most-recent conversation in the workdir) is expected to suffice —
  Phase 2 verifies this on real Docker, and pins `--session-id <known>` at launch +
  `--resume <same-id>` only if `--continue` proves ambiguous. The agent declares
  whether it can *set* a session id at launch.
- **Host CLI verb is out of scope now.** `yoloai-resume` (in-sandbox, one command
  from the shell the user is already in) is the deliverable; a host `yoloai resume
  <name>` would be a thin future wrapper over the same mechanism (existing
  `restart` already covers host-side relaunch) — not built now (YAGNI).

## DD5 — rollout gate

- Ship fall-to-shell **gated** by `DetectionSpec.FallToShell` so a detection
  regression cannot break the default path while it bakes:
  - **Phase 1–2:** `FallToShell` resolves **on for hook-authoritative**, **off for
    heuristic-only** (heuristic would clobber `done` until the Phase-3 honor-change
    lands). The simple default stays safe.
  - **Phase 3:** the heuristic runner honors wrapper-`done`; `FallToShell` may turn
    on for heuristic agents too.
  - **Phase 4:** the gate is **removed** — fall-to-shell is the behavior for every
    persistent × PTY session once the agent matrix is green. (One-shot/`-p` and
    Stream sessions have no terminal to drop into: exit = done, no wrapper-shell —
    the gate is on `lifetime × SessionKind`, per session-layer.md.)

## DD6 — composition with the carve

- **The wrapper *is* the launch `ProcSpec`.** Today it slots into
  `build_agent_launch_command`'s `send-keys` string; under the carve it is the
  agent layer compiling its launch into the neutral `ProcSpec` the session `Launch`
  runs, with the wrapper script **staged by envsetup** (a baked image script /
  seed) and its path named in the `ProcSpec`. The same script works pre- and
  post-carve, so building it now is forward-compatible.
- **Two owners, clean split.** The **wrapper owns the exit lifecycle** (write
  `done` + shell + hint); the **durable monitor stays the external-sibling runner**
  (session-layer.md §8 — relocated, not rewritten; not folded into the wrapper) and
  owns **active/idle detection + honoring the latch**. The agent's exit is the
  wrapper's authoritative signal; the monitor consumes the `DetectionSpec` and
  never re-derives `done` from the idle shell.

## Tier-2 invariants that MUST NOT regress (re-verify every phase)

1. **No startup blip** — hook-authoritative runs no heuristics.
2. **No stale-idle window** on restart/resume — `active` written synchronously
   before submit; the respawn seed stays conditional (seed `idle` only if on-disk
   is still `done`).
3. **`done` only on real exit** — wrapper writes it with the agent's real `$?`;
   the monitor never invents it from an idle shell.
4. **Restart-brings-it-back (DF46)** — the durable monitor re-detects an in-place
   respawn; `yoloai-resume` rides the same path.
5. **`DetectStatus` safe-default** — empty/stale/missing → `active`.

## Cross-references

- **Plan:** [plans/agent-owned-detection.md](plans/agent-owned-detection.md) (the
  4-phase build with Docker checkpoints; this doc is its Phase 0).
- **Rationale (converged):** [session-layer.md](session-layer.md) (fall-to-shell,
  tier-2, the carve's `ProcSpec`), [agent-layer.md](agent-layer.md) (Resume
  capability, mechanism-vs-payload), [envsetup.md](envsetup.md) (the `EnvSpec`
  boundary `DetectionSpec` mirrors; the wrapper as a staged artifact).
- **Decisions:** [D88](../decisions/working-notes.md) (session/tier-2),
  [D89](../decisions/working-notes.md) (agent capabilities); this doc's own entry
  **D96**.
- **Findings/history:** DF44/DF46 + the tier-2 blip in
  [findings-resolved.md](findings-resolved.md) / the decisions log.
</content>
</invoke>
