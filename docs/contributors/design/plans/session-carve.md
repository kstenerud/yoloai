# Phase 1a — the session-carve public realization (the long pole)

**Status:** Scoping, 2026-06-26. The implementation plan for Phase 1a of the endgame roadmap
([D99](../../decisions/working-notes.md)) — turning the converged design
([session-layer.md](../session-layer.md), D88) into ordered, buildable steps. Branch:
`substrate-move`. Per D99, incidental per-commit contract churn is fine; build straight to the
final shape (each commit compiles + `make check` green).

## What's already built (don't re-do)

The carve's structural core is live + verified (S0–S3): the `Launch`/`Process` primitive with
`Detached` (docker), neutral-PID-1 keepalive bring-up (`keepalive_only`), the agent re-routed over
`Launch` (`startViaLaunch` launches `sandbox-setup.py` detached), durable status-monitor, readiness
gate. The **coarse-`Launch`** model (D88 §5) is the chosen shape: Go launches **one** in-container
session process (`sandbox-setup.py`); the tmux choreography **stays in Python**. So this phase is
*not* "move tmux into Go" — it's the public-surface + new-capability work on top of the built core.

## The decomposition (ordered; each its own commit(s))

### 1a-i — One-shot `-p` / Tier-3 + the `wait`/`run` verbs *(start here)*
The cleanest, highest-consumer-value piece, and greenfield (a new launch path that doesn't disturb
the persistent/tmux path). A `lifetime ∈ {one-shot, persistent}` caller axis (D88 §3): for an agent
with a headless command (`agent.Definition.HeadlessCmd`, e.g. `claude -p "PROMPT"`), one-shot =
`Substrate.Launch(ProcSpec{the headless agent})` + `Wait(WaitForExit)` — **no IOSession, no tmux, no
monitor**; the tracked process *is* the agent, so **exit = done (Tier-3)** falls straight out (§6).
Surfaces: `yoloai run --lifetime one-shot` (and the `yoloai wait` verb + MCP `sandbox_wait` from
Phase 2 B1, since `Wait` is the completion mechanism). `--output-format stream-json` gives usage
(feeds Phase 2 B2). **Open:** the bounded launch-readiness timeout distinct from `Wait` (§failure
semantics); per-agent headless-command resolution; how `run` composes create+launch+wait+destroy.

### 1a-ii — `AgentLaunchPrefix` off the public `BackendDescriptor`
Empty for all container backends; non-empty only for **tart** (`PATH=…`) and **seatbelt**
(`source ~/.swift-wrapper.sh &&`), both on the legacy (non-`Launch`) path. The field must leave the
public substrate descriptor. **Open decision (resolve here):** the clean ARCH §4 form is "the
backend applies its own launch-env when it launches the agent." For tart's PATH prefix that can be
`ProcSpec.Env` (the session process's env, inherited by the agent via the tmux server); seatbelt's
*shell-source* prefix is not env-foldable and must apply at the agent's in-pane exec. Likely
resolution: each backend self-applies internally (it owns the constant); `restart.go:342`'s
`cfg.AgentLaunchPrefix + cmd` routes through a backend method instead of reading the field. Confirm
on real backends.

### 1a-iii — `IOSession` on `sb.Agent()` (concentrate the tmux scatter; retire `InteractiveSession`)
The persistent-session refinement (D88 §1–2): a durable, reattachable channel — **attach · inject ·
capture · persist** — reached as `sb.Agent().IOSession()`, host-side Go methods wrapping
`Substrate.Exec` (send-keys/capture-pane for the PTY broker). Concentrates the ~15–20 scattered tmux
sites; `runtime.InteractiveSession` (`TmuxSocket`/`AttachCommand`) leaves the substrate for the
session layer. `SessionKind{PTY, (reserved) Stream}` shaped so Stream slots in additively (built
later). **Open:** the socket-path fact is backend-specific — does it stay a backend capability or
move to session-layer knowledge keyed by locality (§5); the `Inject` argv-parameterized contract
(security valve, §security); naming the in-container session process.

### 1a-iv — Slim `sandbox-setup.py` / neutral-PID-1 default + broaden `Launch`
Flip `keepalive_only` on by default (neutral PID 1 everywhere), retire the welded-agent legacy
entrypoint path, broaden `ProcessLauncher` to the non-docker backends (or confirm the legacy path is
acceptable per-backend), and slim `sandbox-setup.py` to the session-runner role (minus PID-1-ness,
minus the agent-free provisioning that is `ProvisionSpec`). Preserve all lifecycle legs (restart /
stop-start / in-place reset / suspend-resume) and the security valve.

### 1a-v — Formalize the three-bucket schema (only as needed)
`ProvisionSpec` (≈ today's agent-free `InstanceConfig`) · `ProcSpec` (exists, agent-neutral) ·
`AgentLaunchSpec` (agent layer compiles it → `ProcSpec` + the staged agent-config artifact). Much is
already embodied; formalize/name only where it sharpens the sealed contract — avoid ceremony.

## Sequencing & rationale

1a-i first (greenfield, consumer value, validates the `Launch`+`Wait` contract everything rests on)
→ 1a-ii (bounded; finalizes the launch input by removing the agent-named field) → 1a-iii (the big
tmux-concentration refactor; the persistent-session public surface) → 1a-iv (slim + broaden, riding
the now-final contracts) → 1a-v (formalize as needed). The order minimizes rework: the launch
contract is settled (i, ii) before the session handle (iii) and the slimming (iv) build on it.

## Cross-references

[session-layer.md](../session-layer.md) (D88, the design), [D99](../../decisions/working-notes.md)
(the endgame roadmap), [agent-layer.md](../agent-layer.md) (D89 — `sb.Agent()` home, Resume/Context
capabilities), control-eval's trial-engine report (the one-shot/wait/usage needs).
