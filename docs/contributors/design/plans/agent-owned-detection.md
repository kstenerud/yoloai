# Agent-owned detection + fall-to-shell + resume — design & build plan

**Status:** Plan 2026-06-25, not yet started. Supersedes the deferred
"fall-to-shell resume" item (public-layering S3). This is a **plan for a fresh
session** — read it top to bottom, then execute the phases in order, Docker-
testing at each checkpoint before moving on. Detection is the most fragile,
historically-troubled subsystem in yoloai (DF44/DF46, the tier-2 blip, the
stale-idle window) — the whole point of this plan is to change it *carefully,
validated on real Docker at every step*, not in one improvised leap.

## Goal

Make **session detection the agent's own responsibility** (each agent declares
how it is detected and how it resumes), and on agent exit **fall to an
interactive shell** with a resume hint instead of killing the pane — so the user
lands in a usable shell and a single **`yoloai-resume`** command re-establishes
the agent *and* all the detection machinery. This dissolves the current
monitor-vs-wrapper race (one owner of `active`/`idle`/`done`, no clobbering) and
fixes the UX where quitting the agent drops you into a dead pane.

## Why a rearchitecture, not a tweak

The naive fall-to-shell breaks done-detection: today `status-monitor.py` (a
separate, durable process — DF46) derives `done` from **pane death**. If a shell
takes over the pane on agent exit, the pane never dies, so `done` never fires;
worse, in heuristic mode the monitor reads the idle shell as **`idle`**,
masquerading an exited agent as waiting. Fixing this cleanly means making the
detection layer **agent-owned and exit-aware**, which is a rearchitecture of the
exact code just stabilized — hence: design first, phase-gated Docker testing.

## Current state (facts to build from)

- **Launch:** the agent runs in a tmux pane via
  `setup_helpers.build_agent_launch_command` → `{exports}cd '<wd>' && exec
  {agent_command}`. The `exec` means the agent replaces the shell, so its exit
  code becomes the pane's and **the pane dies on agent exit**.
- **Detection:** `runtime/monitor/status-monitor.py` is a **separate
  durable process** watching the pane *externally* (via `tmux capture-pane` /
  `list-panes` for pane-pid, wchan, pane-dead). It is NOT in the user↔agent tty
  path. Main loop (`status-monitor.py:~643`): (1) `check_pane_dead` → write
  `done` + latch `in_done`; (2) **hook-authoritative** mode → the agent's hook
  writes `active`/`idle`; the monitor runs *no* heuristics, only the
  pane-death→done + a one-shot respawn idle-seed; (3) **heuristic-only** mode →
  run the detector stack (pane-content / wchan / pipe-pane markers) against the
  pane. `idle_mode` + `detectors` come from `runtime-config.json`
  (`invocation.ResolveIdleMode`).
- **Agent declarations:** `agent.Definition.Idle` (`ReadyPattern`,
  `ContextSignal`, `WchanApplicable`, `Hook`) + `SubmitSequence` etc. The agent
  already *declares* detection params; the monitor is a generic consumer of them.
- **Tier-2 invariants that MUST NOT regress** (re-verify every phase): no startup
  blip (hook-authoritative runs no heuristics), no stale-idle window on restart
  (active written synchronously before submit), `done` fires on real exit,
  restart-brings-it-back (in-place respawn re-detected, DF46), `DetectStatus`
  safe-default (empty/stale/missing → active).

## Target architecture

1. **The agent owns detection.** An agent-agnostic **`DetectionSpec`** (compiled
   from `agent.Definition` at the orchestrator boundary, exactly like `EnvSpec`
   /`ProcSpec`) declares the agent's detection: the *mode* (hook-authoritative /
   heuristic), the heuristic params (ready pattern, wchan applicability, idle
   markers), and the **resume command**. The detection runner consumes the spec;
   it never branches on the concrete agent.
2. **Mechanism location is the agent interface's concern — uniform first.**
   Whether detection *watches the pane externally* or *runs inline (pty layer)*
   is expressible per-agent, but **the first implementation makes them all the
   same**: keep the **external-sibling watch** (today's model — lowest risk,
   reuses the working detectors, stays out of the agent's interactive TUI). The
   interface leaves room for an inline/pty variant later without a contract
   change.
3. **The wrapper owns the exit lifecycle.** The launch command becomes a wrapper
   that: runs the agent (no longer `exec`), and on exit **writes `done`**
   (authoritatively, not via pane-death), prints a resume hint, and `exec`s an
   interactive shell — the pane stays alive as a usable shell.
4. **`done` is wrapper-authoritative.** The detection runner *honors* a
   wrapper-written `done` (latches it, like the pane-dead latch) and does **not**
   re-derive `active`/`idle` from the fall-to-shell shell. Pane death now means
   "user closed the fall-to-shell shell" (a terminal event), still → `done`.
5. **`yoloai-resume`.** A script staged in the sandbox (`/yoloai/bin/yoloai-resume`
   or similar) that the fall-to-shell hint tells the user to run: it re-launches
   the agent with its resume command (e.g. `claude --continue`), re-establishes
   detection, and resets status — so resume is one command and all machinery
   comes back. Mirrors the in-place respawn path (DF46) but user-invoked from the
   shell.

## Design decisions to resolve (write the spec first)

Write a short design doc + decision (next Dxx) resolving these BEFORE coding:

- **DD1 — done contract.** Exact `agent-status.json` write the wrapper performs on
  exit (schema, exit_code, timestamp — reuse the tier-2 format); how the runner
  latches it; how pane-death (shell close) vs wrapper-done compose; what clears
  the latch on resume (hook → `active`; heuristic → agent-process-reappears).
- **DD2 — DetectionSpec shape.** What the agent declares; how it compiles from
  `Definition` (the agent-agnostic boundary, like `envspec.BuildEnvSpec`); how
  hook-authoritative vs heuristic both map onto one runner contract.
- **DD3 — wrapper construction.** Inline shell one-liner vs a staged wrapper
  script (`/yoloai/bin/agent-run.sh`). Lean **staged script** (the inline
  `run; write-done; hint; exec shell` is fragile to escape, esp. with the
  resume command containing flags/quotes). Where it's staged, how secrets/exports
  compose (today's `build_secret_exports`), how the working-dir cd composes.
- **DD4 — resume.** The per-agent **resume command** (new `Definition` field,
  e.g. `ResumeCmd` — `claude --dangerously-skip-permissions --continue`; "" for
  agents with no resume → hint offers a fresh start). The `yoloai-resume` script
  contents + how it re-spawns detection. Whether resume is purely in-sandbox
  (user runs it in the shell) or also a host CLI verb (`yoloai resume <name>`
  that drives the in-sandbox script).
- **DD5 — default vs opt-in + rollout.** Ship fall-to-shell **gated** at first
  (a runtime-config flag, default off OR on only for hook-authoritative) so a
  detection regression can't break the default path while it bakes. The simple
  default must stay safe.
- **DD6 — compose with the carve.** How the wrapper fits the session `Launch`
  (it's the `ProcSpec` the launcher runs / the sandbox-setup `launch_agent`
  command) and the durable monitor (DF46) — does the separate monitor stay (as
  the external-sibling runner) or fold into the wrapper-spawned runner.

## Build phases (each ends with a Docker checkpoint — do not proceed until green)

**Phase 0 — Design. ✅ DONE 2026-06-25.** DD1–DD6 resolved in
[../agent-detection.md](../agent-detection.md) (the build contracts) + decision
**D96**. Gate: awaiting design review before Phase 1.

**Phase 1 — Fall-to-shell + wrapper-writes-done, hook-authoritative only
(Claude). ✅ DONE 2026-06-25 (verified real-Docker).** The safe increment: change the launch command (DD3 wrapper) so on
Claude's exit it writes `done`, prints the resume hint, and execs a shell. The
monitor (hook-authoritative) already runs no heuristics, so the wrapper's `done`
survives. *Docker checkpoint:* create a Claude sandbox; quit the agent; verify
(a) `agent-status.json` = `done` with the real exit code, (b) the pane is a live
shell with the resume hint printed, (c) `yoloai attach` lands in that shell, (d)
no startup blip and no stale idle on the run before exit (tier-2 regression
guard). Sentinel-style harness like the E3 verification.

**Phase 2 — `yoloai-resume` + the resume command (Claude). ✅ DONE 2026-06-25
(mechanism verified real-Docker; conversation-content resume is Claude's
`--continue`, correctly invoked).** Add `Definition.ResumeCmd`
and the `yoloai-resume` script; wire the hint to it. *Docker checkpoint:* from the
fall-to-shell shell, run `yoloai-resume`; verify the agent resumes the *prior*
conversation (claude `--continue`), detection re-establishes, and status goes
`done`→`active`/`idle` correctly (no stale-idle clobber, no blip).

**Phase 3 — honor wrapper-done in the heuristic runner; extend to heuristic
agents. ✅ DONE 2026-06-25 (verified real-Docker).** Made the detection runner
latch a wrapper-written `done` and not clobber it with the idle shell (DD1), and
fixed the process-detection prerequisite the heuristic path needs under the
wrapper: `get_agent_pid` now **descends through the wrapper to the real agent**
(the wrapper sits in `do_wait`, which `ACTIVE_WCHANS` would misread as a
permanently-active agent). `ResolveFallToShell` now enables fall-to-shell for all
agents (hook + heuristic). *Docker checkpoint (passed):* gemini (heuristic)
agent-only exit → wrapper wrote `done` exit_code 137, pane alive as bash, and the
heuristic monitor **held `done` across many cycles** (no idle clobber); the pid
descent returned the gemini node (86), not the wrapper sh (71); `yoloai-resume`
relaunched fresh ("no native resume") and status recovered; a claude
(hook-authoritative) run still passed turn→idle→`/exit`→`done`→fall-to-shell with
no clobber (no cross-mode regression).

*Note — `DetectionSpec` formalization deferred (not a scope cut).* The plan
originally bundled formalizing a compiled `DetectionSpec` (gathering
`idle_mode`/`detectors`/`fall_to_shell`/`resume_cmd` into one strategy-shaped
spec) into this phase. The verified research
([research/agent-callbacks.md](../research/agent-callbacks.md)) showed the
strategy abstraction's real consumer is **wiring the vendor turn-completion
callbacks** (Codex/Gemini/OpenCode/Aider) — which is a *separate* effort, not in
this fall-to-shell/resume plan. Building the `DetectionSpec`/strategy framework
now, before any second strategy is wired, is premature abstraction (YAGNI). It is
deferred to land **with** the vendor-callback wiring, where it has a real second
consumer — consistent with the reserved-seam decision (D96 refinement) and the
"strategy-first so the enum need not be widened later" intent. The functional
goal of *this* plan (fall-to-shell + resume + don't-clobber-done) is met by the
honor-done latch + the pid descent above.

**Phase 4 — polish + docs. ✅ DONE 2026-06-25.** Reduced from the original
"unify + own" because the strategy formalization (the bulk of the original Phase 4)
was deferred to [agent-detection-strategies.md](agent-detection-strategies.md).
What landed: (a) the `FallToShell` gate **reframed** from "rollout gate" to the
**persistent-PTY gate** (always-on now; the session-layer `lifetime` axis drives it
off for one-shot later) — kept, not removed, since session-layer.md gates
fall-to-shell on lifetime × SessionKind; (b) **user docs** — GUIDE.md "When the
agent exits (fall-to-shell)" section covering the behavior + `yoloai-resume`,
distinguished from the host-side `--resume`. *No `BREAKING-CHANGES.md` entry:* the
public contract is preserved — `status=done` still fires on agent exit (now
wrapper-written with the agent's real exit code), and pane-death was never a public
API. *Regression guard:* `scripts/smoke_test.py` requires `ANTHROPIC_API_KEY`/
`CLAUDE_CODE_OAUTH_TOKEN` **in the environment**, which isn't set here (Claude auth
is the seeded credentials file), so the full harness couldn't run; substituted an
**auth-free deterministic guard** with the synthetic `idle` agent (heuristic):
fall-to-shell → `done` (exit 137) + live bash pane + honor-done held across cycles
→ `yoloai-resume` fresh relaunch → status recovered. Combined with the Phase-1–3
real-Docker checkpoints (hook + heuristic, real-auth conversation resume), the
lifecycle is covered.

## Docker testing strategy

- **Harness:** the real-Docker sentinel approach used for E3 (build the binary,
  `yoloai system build --backend docker` to refresh the base image, create a
  sandbox with a sentinel `ANTHROPIC_API_KEY`, drive tmux via `docker exec`
  `tmux -S /tmp/yoloai-tmux.sock`), plus `scripts/smoke_test.py` for end-to-end.
- **Per-phase, assert the status transitions explicitly** by reading
  `agent-status.json` inside the container at the key moments (working → done →
  resume → active/idle), not just "did the agent run."
- **Regression guards every phase** (the tier-2 invariants): no startup blip,
  no stale-idle window on restart/resume, `done` only on real exit, DF46
  restart-brings-it-back. A phase is not green until these still hold.
- **Validate the core mechanism in Phase 1** before building 2–4: if
  wrapper-writes-done + fall-to-shell is wrong, we find out at the cheapest point.
- **Watch the known gotchas:** `pane_dead` empty-status race (Docker Desktop
  macOS), the tmux-env propagation path, the readiness gate (DF44), and that the
  base-image rebuild is needed after a binary change.

## Risks & mitigations

- **Detection regression (highest):** phase-gated Docker testing + the tier-2
  regression guards + start hook-authoritative + ship gated (DD5).
- **TUI interference:** keep detection external-sibling in the first cut (don't
  put a pty layer between user and agent); the interface allows inline later.
- **Resume re-entrancy:** `yoloai-resume` must compose with the carve's Launch +
  the durable monitor (DD6); test the resume path explicitly (Phase 2/3).
- **Escaping fragility in the wrapper:** stage a script (DD3), don't hand-build a
  giant inline shell command with the resume flags embedded.

## Cross-references

- Supersedes the deferred fall-to-shell item in
  [plans/public-layering.md](public-layering.md) (S3).
- Detection background: [research/idle-detection.md](../research/idle-detection.md),
  [research/orchestration.md](../research/orchestration.md),
  [session-layer.md](../session-layer.md). Tier-2 history + DF44/DF46:
  [findings-resolved.md](../findings-resolved.md) / the decisions log.
- Compose with the carve: [substrate-interface.md](../substrate-interface.md) (D84),
  [session-layer.md](../session-layer.md) (D88), [envsetup.md](../envsetup.md) (the
  `EnvSpec` boundary pattern `DetectionSpec` mirrors).
