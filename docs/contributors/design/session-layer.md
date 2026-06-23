# Session layer — interactive/stream I/O channel refinement

**Status: DESIGN IN PROGRESS (not converged, no D-number yet).** Captured 2026-06-15 mid-design so
the conversation can resume after a machine restart. **Advanced 2026-06-16** (control-eval consumer) —
the `lifetime` axis and the completion-signal staging converged. **Advanced 2026-06-18** — the structural
spine (the Go↔Python carve) converged: neutral PID 1, relocate-the-monitor, coarse `Launch`,
Launch-unit-by-`SessionKind`, and the `ProvisionSpec`/`ProcSpec`/`AgentLaunchSpec` schema split. See the
dated sections below. **Advanced 2026-06-23** — re-launch semantics converged (restart=re-run-the-launch;
`lifetime` decides prompt replay; persistent restart = **fresh agent**, cross-agent-parity rule; trust is a
fresh-environment event, not a restart event). Still no D-number — tier-2 hook-log shape / `Session` naming /
inject-capture security remain (RESUME-HERE).
This is the framing reached so far + the open
questions — *not* a finalized spec like [substrate-interface.md](substrate-interface.md) /
[copyflow-layer.md](copyflow-layer.md) / [persistence-helper.md](persistence-helper.md). The session
refinement of [public-layering](plans/public-layering.md); a *consumer* of the substrate (D84). It is
the module-split's deferred "C-full" (the real session abstraction); C-minimal already lifted
`TmuxSocket`/`AttachCommand` off the core `Runtime` into the optional `InteractiveSession` interface
(which is itself still tmux-shaped — the refinement abstracts it properly).

## Grounding (measured 2026-06-15)

- tmux is the codebase's biggest tangle: **61 Go files + 9 Python files**. It does **five conflated
  jobs**: (1) **PTY provision**, (2) **persistence of the interactive channel** (survives disconnect),
  (3) **reattachment** (`yoloai attach`), (4) **input injection** (`send-keys`/`paste-buffer` —
  prompt delivery, follow-ups; in `engine.go`/`restart.go`/`reset.go`/`sandbox-setup.py`), (5)
  **output capture** (`capture-pane` — snapshots, idle detection, bug reports; in
  `terminal.go`/`status-monitor.py`/`bugreport.go`).
- The agent already declares `PromptMode {Interactive (tmux send-keys), Headless (CLI arg)}` — but
  that is only *how the prompt arrives*, and **even Headless agents run inside tmux today.** There is
  no `SessionKind` deciding whether a terminal exists at all.

## The framing reached so far

**Essence.** A session is a **durable, reattachable I/O channel for a process**, with four operations
— **attach · inject · capture · persist** — over a channel of one of two **kinds**:
- **Terminal (PTY)** — rich (cursor/colors/redraw), *needs a broker* (the agent CLI isn't
  independently reattachable, so it runs under tmux/dtach/a PTY broker). The interactive default.
- **Stream** — durable stdio, *no PTY*: stdout→a log, input←a fifo; attach=tail, inject=write-fifo,
  capture=read-log. The headless strategy.

Of the five jobs, **only PTY provision differs** between strategies; persist/reattach/inject/capture
are common (terminal via the broker, stream via files). `SessionKind ∈ {PTY, Stream}` is injected by
the agent's needs. The substrate already supplies the primitives: `Launch` (the broker, or the agent
with redirected stdio), `Exec` (drive the broker), `ProcSpec.TTY` (the PTY), and the filesystem (where
the broker socket / the stream's log+fifo live, as emergent channels per the substrate model).

**What the clean model changes (three things):**
1. **Concentrate the scatter** — the ~70 files doing raw `send-keys`/`capture-pane`/`attach` collapse
   to **one `Session` handle** (`Inject`/`Capture`/`Attach`); the agent layer (prompt delivery) and the
   monitor (idle/snapshots) become *consumers* of it. That's most of the de-tangle.
2. **Separate `SessionKind` from `PromptMode`** — `PromptMode` is only *how the prompt arrives* (orthogonal); `SessionKind` is *whether there's a terminal*. Untangling them is the unlock (a PTY session can take a CLI-arg prompt; a stream session is the genuinely no-tmux path).
3. **The Go↔Python reshape (the load-bearing refactor)** — today the agent-session launch lives in the
   Python entrypoint (`sandbox-setup.py` always creates tmux + launches the agent). In the clean
   substrate model (DF31) the entrypoint becomes a neutral keep-alive, and the session+agent launch
   moves to an explicit, **Go-driven `Substrate.Launch`** with the chosen strategy. This is the
   DF31/DF33 payoff and the ~63-touchpoint piece that made C-full "large."

**Proposed model (tentative):** a `Session` refinement that is a *consumer of the substrate*, exposing
attach/inject/capture/persist over a `SessionKind`-chosen strategy (terminal-broker / stream-files),
concentrating today's scattered tmux calls, separating `SessionKind` from `PromptMode`, and moving the
agent-session launch off the mandatory Python entrypoint to an explicit Go-driven `Launch`.

## 2026-06-16 — `lifetime` converged (driven by the control-eval consumer)

A real headless consumer arrived: **control-eval**, an AI-control eval harness (`~/experiments/control-eval`;
see its `docs/yoloai-trial-engine-report.md`). One trial = run one prompt to completion in a disposable
sandbox, collect diff + token usage, destroy — hundreds of times, ideally concurrently. Driving it exposed
a third axis the model lacked and settled it.

**The `lifetime` axis (new).** Distinct from `PromptMode` (how the prompt *arrives*) and `SessionKind`
(PTY vs Stream *channel*): `lifetime ∈ {one-shot, persistent}` is a **caller intent** — does the agent
live beyond one prompt. It does not name a channel; it selects how completion is reached.

**One-shot has two realizations, chosen by agent *capability*** (the caller only declares `lifetime: one-shot`):
- **Agent has a headless one-shot command** (`claude -p`) → **substrate `Launch` + `Wait(exit)`**. The
  process *is* the turn; **exit = done** (pure D84 liveness — no new status, no hook dependency).
  `--output-format stream-json` yields token usage at the source (P2, agent-native). No `Session` needed;
  smallest attack surface (best for untrusted agents). *Primary where available.*
- **TUI-only agent** → **composition over a persistent `Session`** (inject → wait-turn-done → read →
  shutdown). Needs the tier-2 hook-log signal to be non-fragile. *Fallback.*

So one-shot-via-`-p` is **not a `SessionKind`** — it's a substrate launch. `SessionKind {PTY, Stream}`
are the two **persistent** channel strategies.

**Auth is orthogonal** — a credential-injection axis, caller-supplied (D63: `Env` snapshot resolved at the
edge; zero ambient reads in the library). Both realizations honor an injected `ANTHROPIC_API_KEY` via env
precedence. Verified against the Claude Code docs: `claude -p` is fully subscription-friendly *and* works
with an injected key; only a set `ANTHROPIC_API_KEY` or `--bare` forces metered billing. `--bare` is a
natural hermetic single-credential one-shot for the API-key world (but strips OAuth/keychain — confirm what
else before relying; don't pair with hook-dependent paths). Credential *delivery* — the MCP edge has no
per-call input, and tool-arg injection collides with "agents shouldn't hold secrets" (launch-time
env-to-server is the cleaner alternative) — is **DF38** (deferred, needs a secure-secrets design pass);
the residual `$HOME` credential-file bleed is **DF39**.

**Three completion tiers** (= the main-now / layering-later sequencing):
1. **idle** — `Sandbox.Wait`'s existing layered idle. Exists today → expose `wait` / `run --for idle` on
   CLI/MCP **now, on `main`**; unblocks control-eval immediately (strictly better than its hand-rolled
   idle+deliverable heuristic). Auth-agnostic.
2. **hook-log "done"** — a reliable per-turn, **channel-agnostic** completion signal (Claude Code Stop
   hook → a log). New; **layering-era**. The permanent fix for persistent/multi-turn *and* TUI-only one-shot.
3. **process exit** — `claude -p` launched outside tmux. Cleanest one-shot (exit=done + stream-json usage),
   lowest attack surface. **Layering-era — gated on the DF31 Go-driven `Launch` reshape** (launching the
   agent *not* tmux-wrapped *is* that reshape).

control-eval rides tier-1 on `main` today and migrates to tier-3 transparently when the reshape lands
(`run --lifetime one-shot` swaps the realization underneath it).

**What control-eval did NOT force: the Stream `SessionKind`.** PTY + composition (and later `-p`) serve it
— it never needs a human, but it also never needs to *drop the broker*. So Stream stays deferred (real-demand
rule intact): what fired was demand for the **done signal + the `lifetime`/one-shot verb**, not the no-PTY
channel.

## 2026-06-18 — structural spine converged (the Go↔Python carve)

Resolved the heavy RESUME-HERE thread (the Go↔Python boundary). The carve is **Option 1: full carve to a
neutral PID 1**, with five settled decisions. Not yet a D-number — re-launch / security / naming / tier-2
shape remain (see RESUME-HERE below).

1. **Neutral PID 1 keep-alive.** The container init becomes a dumb keep-alive (tini/pause: reap zombies,
   hold the namespace, know nothing of agents/tmux/monitoring). Everything the Python entrypoint does today
   is **demoted from "the init" to "a process the Go session layer starts via `Substrate.Launch`."** Key
   clarification: *neutral PID 1 ≠ no in-container helpers* — helpers become explicitly-launched,
   session-scoped processes, not mandatory middleware. Not greenfield: seatbelt already has the bare
   keep-alive (`bareInstance`) vs full split and tart the P1-only Start; the carve **promotes that P1
   keep-alive to the default** and makes the agent session a `Launch` on top. (Closes DF31.)

2. **Relocate the monitor, don't rewrite it.** The idle-detection stack (hook → wchan → ready-pattern →
   output-stability, the 9 Python files) **stays in-container**, but is started by the Go session layer as a
   sibling process to the agent — not as PID 1 — and writes a **sidecar status file** the host reads (the
   D85 agent→sidecar shape). Rejected: a host-side Go monitor driving capture via `Exec` (big rewrite of
   proven logic; a host↔container round-trip per poll). The carve's goal is a neutral *substrate*, not "no
   in-container code"; the session layer is *allowed* an agent-aware in-container helper. For the Stream/`-p`
   path there is **no monitor at all** (exit = done), so the monitor is purely the PTY-persistent strategy's
   consumer.

3. **Coarse `Launch` — one Launch per session.** PTY kind → `Launch` a *slimmed in-container session
   supervisor* (today's `sandbox-setup.py` minus being-PID-1 and minus the agent-free provisioning that
   moved to the substrate): it brings up tmux, starts the agent in a pane, starts the monitor; Go then drives
   the session via `Exec`. Stream kind → `Launch` the bare agent with stdio→files (no supervisor, no tmux).
   Go owns **policy** (which `SessionKind`, what spec, when to launch/relaunch); it does **not** own
   in-container *mechanism* or step choreography (rejected: fine-grained Go-driven tmux/agent/monitor steps —
   chatty, fragile, re-implements working timing-sensitive logic). `Session.Inject/Capture/Attach` are
   host-side Go methods wrapping `Substrate.Exec` (tmux send-keys/capture-pane for PTY; fifo-write/log-tail
   for Stream).

4. **The `Launch` unit differs by `SessionKind` — and that is *why* exit=done is Stream-only.** PTY: the
   tracked process is the **supervisor** (tmux); substrate liveness = "supervisor up"; the agent going
   idle/done is the **monitor's sidecar** (tiers 1–2), not a substrate transition. Stream: the tracked
   process **is the agent**, so substrate liveness = agent liveness and **exit = done** falls straight out
   (tier 3). Substrate liveness (the tracked process) and agent-status (the monitor sidecar) stay cleanly
   separated; the three completion tiers map onto exactly this split. A strong consistency check that the
   coarse boundary is the right cut.

5. **Three-bucket schema split (DF33 close + D84 refinement).** Today's `runtimeconfig` DTO fuses substrate
   and agent fields; it splits into:
   - **`ProvisionSpec`** (substrate, set at container-up): image, mounts, resources, network mode+allow,
     isolation, ports, substrate env. Agent-free; the backend consumes it directly (not a container-read file).
   - **`ProcSpec`** (the substrate's `Launch` input): command+args, TTY, env, cwd. The substrate sees *only*
     "run this process this way." **Agent-neutral.**
   - **`AgentLaunchSpec`** (agent layer): ready-pattern, detector stack, prompt + prompt-mode,
     submit-sequence, model, resolved workdir path. The agent layer **compiles** it down into a neutral
     `ProcSpec` (the supervisor command for PTY, or `claude -p …` for Stream) **plus a staged agent-config
     artifact** the in-container supervisor+monitor read (the direct descendant of `runtimeconfig.json`,
     minus everything that became `ProvisionSpec`). Because we *relocated* the monitor, the Go↔Python file
     boundary persists — just agent-only now.

   **The refinement of D84:** D84 §9 said agent `command/ready/idle` "move to the agent layer's `ProcSpec`."
   That conflated two types. If `ProcSpec` is the substrate's `Launch` input *and* carries ready/idle, the
   substrate type re-acquires agent fields (DF33 reopens at the Launch boundary); if `ProcSpec` is an
   agent-layer type that `Substrate.Launch` takes, the substrate depends on an agent type (inversion). The
   resolution: **two types** — a neutral `ProcSpec` (substrate) and a separate `AgentLaunchSpec` (agent
   layer) that compiles into `ProcSpec` + a staged config. Agent fields **never** appear in a substrate type;
   that is what makes DF31+DF33 *close* rather than relocate.

## 2026-06-23 — re-launch semantics converged

Drains RESUME-HERE thread #1 (re-launch) and closes the resume-vs-fresh question (old #B).

**Restart, stop/start, and create are three operations distinguished by *which layer cycles*** — a payoff of
the carve's independent substrate/session lifecycles:
1. **Restart (session-level)** — substrate stays **up**; the orchestrator terminates the `Launch`'d session
   tree and re-`Launch`es it. `Substrate.Start` is *not* called. Light, fast, environment unchanged.
2. **Stop → Start (substrate-level)** — substrate cycles down/up: `Substrate.Start` = agent-free env-up +
   neutral PID 1 **only** (supervision is caller policy, D84 mechanism-not-policy), then the orchestrator
   re-issues `Launch`.
3. **Create / reset-to-fresh** — fresh substrate and (for create) a fresh workdir copy.

**Persisted vs ephemeral.** `ProvisionSpec` (env definition) + `AgentLaunchSpec` (agent intent) are persisted
— `ProvisionSpec` in the substrate store record, `AgentLaunchSpec` in a D85 agent **sidecar** beside it; both
re-read on start. The session process tree is **always ephemeral**, re-materialized by `Launch` — never the
old PID ("re-open-env-never-process", D84). A bare `start` uses the *persisted* `AgentLaunchSpec` (no
re-resolve — changing model/etc. is `reset`/recreate, not a silent start-time swap).

**The trust prompt is a fresh-*environment* event, not a restart event.** Claude's folder-trust lives in the
persisted `~/.claude`, keyed to a stable workdir path — a *first-encounter-of-this-folder* event. In-place
restart never triggers it; stop/start doesn't either (state + path persist); only a genuinely-new folder
does. **DF13's dialog-on-restart was a symptom of restart-as-stop_start**; under this model it evaporates.
Belt-and-suspenders hardening: pre-trust the workdir at create so it never appears at all.

**Prompt replay is decided by `lifetime`, not a global flag** — "restart = re-run the launch (`ProcSpec`)"
gives the right answer for both automatically, because the prompt is *in* the launch command for one-shot and
not for persistent:
- **one-shot / headless** bakes the prompt into the launch (`claude -p "task"`) → restart re-runs it →
  **re-injects** (what a script/agent driver wants; restart *means* "run the trial again");
- **persistent / interactive** launches the agent and injects the prompt *separately, post-launch* → restart
  re-runs the agent command only → **no replay**.
So least-astonishment falls out of the existing `lifetime` axis — no new choice, no global default that
surprises one camp. A script wanting re-injection uses one-shot / `reset --prompt`, not a flipped default.

**Resume-vs-fresh (old open question B) → FRESH.** A persistent restart brings back a **fresh agent**, not a
resumed conversation. Two reasons, the second load-bearing:
- The environment (work copy, `~/.claude` history, progress) **persists**, so the original prompt is stale by
  definition — replaying the kickoff would fight the world the agent re-enters.
- **Cross-agent parity rule:** the session layer's baseline re-launch must be the lowest common denominator,
  and "relaunch a fresh agent" is the only thing *every* agent supports. Native conversation-resume (Claude's
  `--continue`) is Claude-shaped; baking it into the baseline would re-couple the abstraction to one agent —
  the coupling the carve exists to remove. **Fresh is the baseline**; native resume is a *deferred per-agent
  capability* declared on `AgentLaunchSpec` and opted into, never assumed.

This matches the current `relaunchAgent` default (plain respawn, no prompt) — so the change is "keep the
default, make it `lifetime`-aware," not a rewrite. The `--prompt` (new task) / `--resume` (re-feed + preamble)
opt-ins remain for explicit re-injection; `--resume`'s preamble-replay is now of dubious value given the
staleness reasoning (revisit whether it earns its keep — a CLI-surface question, not a core semantic).

## Open questions — RESUME HERE

The Go↔Python boundary (spine, 2026-06-18) and re-launch semantics (2026-06-23) are now **resolved**.
Remaining, in rough priority:

1. **The tier-2 hook-log "done" signal shape.** Channel-agnostic (a hook writing a log/sidecar, not terminal
   scraping); PTY-capture-based idle is the PTY-only *extra*; one-shot-via-`-p` needs neither (exit = done).
   Pin *where* it lives (session layer, above substrate liveness — D84-consistent) and its exact on-disk shape
   (ties to the monitor sidecar from spine #2).
2. **The `Session` handle name** (and the agent-session supervisor's name).
3. **Security.** Inject/capture trust boundary — within the sandbox vs across the host boundary; lighter than
   copyflow's hermetic seal, but **capture can egress agent-printed secrets into bug reports**. The
   credential-delivery slice is already tracked as **DF38**/**DF39**.

Once these drain, the session layer earns its D-number + a finalized spec (like substrate/copyflow/persistence).
