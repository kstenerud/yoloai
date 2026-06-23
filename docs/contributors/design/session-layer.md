# Session layer — interactive/stream I/O channel refinement

**Status: DESIGN IN PROGRESS (not converged, no D-number yet).** Captured 2026-06-15 mid-design so
the conversation can resume after a machine restart. **Advanced 2026-06-16** (control-eval consumer) —
the `lifetime` axis and the completion-signal staging converged. **Advanced 2026-06-18** — the structural
spine (the Go↔Python carve) converged: neutral PID 1, relocate-the-monitor, coarse `Launch`,
Launch-unit-by-`SessionKind`, and the `ProvisionSpec`/`ProcSpec`/`AgentLaunchSpec` schema split. See the
dated sections below. **Advanced 2026-06-23** — re-launch semantics converged (restart=re-run-the-launch;
`lifetime` decides prompt replay; persistent restart = **fresh agent**, cross-agent-parity rule; trust is a
fresh-environment event, not a restart event) + tier-2 hook completion signal converged (authoritative
per-turn idle via the agent's turn-stop hook, hook-authoritative-exclusive with a deferred `hook-unreliable`
escape hatch, turn-cursor contract) + handle named **`IOSession`** (channel-agnostic; disambiguated from the agent's
conversation "session") + inject/capture **security** converged (one-way trust valve; attach=ssh-model
pass-through, snapshot/bug-report=sanitize+redact+opt-in, inject=argv-parameterized; all accepted gated on
load-bearing risk **documentation**). **ALL DESIGN THREADS RESOLVED 2026-06-23** — ready for its D-number (D88)
+ a finalized consolidated spec.
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

## 2026-06-23 — tier-2 hook completion signal converged

Drains RESUME-HERE #1 (tier-2 shape). Tier-2 is **authoritative per-turn completion** — the agent's own
turn-stop hook (Claude's `Stop`) *declaring* "this turn is finished, awaiting input" — vs tier-1's heuristic
*guess* and tier-3's process-exit. It is a **per-agent capability** (the heuristic detector stack is the
agnostic baseline; the hook is the per-agent upgrade — same parity rule as native-resume from re-launch) and
lives as **agent-status in a sidecar, above substrate liveness** (D84-clean); the relocated monitor (spine #2)
is its consumer. NB the word "done" was loose — tier-2 is authoritative *idle / turn-complete*, not
session-over (that stays tier-3 / liveness).

**Authoritative-and-exclusive (the blip fix).** When an agent is hook-capable, idle is determined **only** by
the hook; the heuristic detectors (ready-pattern, output-stability) run **only** for hook-less agents. The
startup blip — a false early `idle` ~20s in, before real work — is a *heuristic artifact* (the hook never
fires on it), so trusting the hook alone removes it. The crash/hang case (hook never fires) is covered by
**liveness** (process exit → done/failed), not heuristics — so for hook-capable agents the heuristics add
nothing but the blip.

**Escape hatch (deferred, designed-for).** Idle detection has a long, painful history, so we do *not* bet
everything on the hook: a per-agent **"hook-unreliable"** flag is a planned future addition that runs the hook
*and* the heuristics together (use the hook when it fires; heuristics backstop a *missed* hook). Built only if
testing shows a flaky hook. The open detail — combining the two without reviving the blip (likely: heuristics
as a *late/conservative* backstop whose threshold sits well after the early blip, catching missed hooks
without racing them) — is a testing-time problem, deferred. **Design-now requirement:** detector resolution is
a per-agent **mode selector** `{hook-authoritative, heuristic-only, (future) hook-assisted}`, not a hardcoded
hook-XOR-heuristics, so the third mode slots in without rework. Today we build the first two.

**The contract carries a turn cursor (or the blip returns at the API).** Even an authoritative hook can be
read *stale*. So the signal carries a **monotonic turn index (+ timestamp)**: the agent emits turn events
(turn-start → `active`, turn-stop → `idle`, incrementing the counter) into the agent sidecar — a single
overwritten `agent-status.json` carrying the counter (YAGNI over an append-only event log). The session layer
derives current status; `Sandbox.Wait(WaitForIdle)` waits for *completed-turn > the turn at which my prompt
was submitted*, so it cannot trip on an earlier idle (the blip). Tier-2 is thus a **reliability upgrade to the
existing `Wait`** (and the shipped `yoloai wait`), not a new API.

**Spec-phase to-do:** confirm the current hook wiring — is Claude's `Stop` already writing
`agent-status.json`, or does the existing "hook detector" (`ResolveDetectors`, `IdleSupport.Hook`) read
something else? — before implementing. Doesn't change the design.

## 2026-06-23 — naming: the `IOSession` handle

The host-side handle (attach · inject · capture · persist over a `SessionKind`-chosen strategy) is named
**`IOSession`**. Rejected: `Session` (collides *conversationally* with the agent's own *conversation* "session"
— `~/.claude`; no in-API clash, but a reader-confusion one the user wanted resolved in the name);
`TerminalSession` / `Console` / `Terminal` (**channel-shaped** — they name the abstraction after the PTY
strategy and exclude Stream, re-making the tmux-shaped coupling the refinement exists to remove); `Channel`
(collides with Go `chan` and the substrate's channels-emergent concept). `IOSession` keeps the
durable/reattachable "session" resonance while "IO" both disambiguates from the conversation *and* signals
channel-agnostic (I/O over PTY **or** stream). Likely reached as `sb.Agent().IOSession()`.

**Conceptual clarification (informs the layering).** The agent's *execution* and the `IOSession` are separable
concerns — the `-p` one-shot runs with **no `IOSession` at all** (substrate `Launch`+`Wait`; the existence
proof). `attach`/`capture`/`inject` are a separable surface over a running agent (it doesn't care if anyone is
attached — that's the durable/reattachable essence). But the *channel provision itself* differs by kind: pure
conduit in **Stream** (the agent is the process; the session wraps its stdio), versus the agent's **host
terminal** in **PTY** (the broker provides the terminal a TUI agent needs to *exist*; the agent runs *inside*
it) — the same Launch-unit asymmetry as spine #4. So `IOSession` names the *purpose* (I/O); the PTY's
terminal-provision is *how* I/O happens for a TUI, not a second job in the name.

The internal in-container process the PTY `Launch` starts (broker + agent + monitor) is **not** named yet —
`session-runner` if it persists as the session anchor, `session-setup` if it sets-up-and-hands-off to the
broker. An internal, spec-time mechanism detail, not public API.

## 2026-06-23 — security: the inject/capture trust valve converged

Drains the last RESUME-HERE thread. The `IOSession` is the host-side boundary to an **untrusted** agent; it is
a **one-way trust valve** — the mirror of copyflow's seal (copyflow protects the host *filesystem*; this
protects the host *terminal + shared artifacts*). Threat model: capture/attach egress untrusted bytes
(**secrets** + **terminal-escape sequences** — OSC-52 clipboard, cursor/title injection); inject ingresses to
the untrusted box.

**Egress is minimized by the carve.** Because the monitor is relocated in-container (spine #2), routine
idle-capture stays *in* the sandbox — only the minimal derived **status sidecar** crosses; tier-2
(hook-authoritative) shrinks even that. So raw capture egresses **only on explicit user actions** (attach,
snapshot, bug report) — a small, deliberate surface.

**Capture = tainted at the boundary**, handled by where it goes:
- **Live `attach` = pass-through** — you *cannot* sanitize a live TUI without breaking it (the escapes *are*
  the UI). Stance: the **ssh-to-untrusted-host model** — attaching exposes your terminal to the agent by your
  explicit choice.
- **Snapshot / bug report (the share vector) = sanitize escapes + best-effort secret-redact + opt-in
  inclusion.** Free-form output has no schema, so auto-redaction is unreliable; we therefore do **not** pretend
  it is auto-safe — inclusion is explicit and runs through the existing `bugreport.go` sanitization.

**Inject = opaque intent in** — argv-parameterized, **never** interpolated into a host shell command. The
current `deliverPromptViaTmux` already does this (payload as `$1`); the contract locks it (+ spec-time audit).

**Accepted on one condition — DOCUMENTATION is load-bearing.** The risk is acceptable *only because it is
informed*, so docs are a **definition-of-done deliverable**, not a footnote: (a) `attach` help / GUIDE security
section must state the terminal-exposure (ssh-model) risk; (b) the bug-report/snapshot path must warn that
captured output may carry secrets, that redaction is best-effort, and prompt review before sharing. Ties to
`design/security.md` + the security-sandbox principles.

**Scope boundary:** inject *authorization* (who may drive the agent) is the embedder/principal concern (D58/D59
isolation axes), not the session layer — mechanism here, policy above.

## Open questions — RESUME HERE

**All design threads resolved** (lifetime 2026-06-16; spine 2026-06-18; re-launch / tier-2-hook /
`IOSession`-naming / security all 2026-06-23). The session layer is **ready to earn its D-number (next is D88)
+ a finalized spec** — consolidate these chronological convergence sections into a clean topic-organized spec
like [substrate-interface.md](substrate-interface.md) / [copyflow-layer.md](copyflow-layer.md) /
[persistence-helper.md](persistence-helper.md).

Spec-time to-dos already flagged: confirm the current `Stop`-hook wiring (tier-2); name the internal
in-container process (`session-runner` vs `session-setup`); audit the inject escaping; enumerate the doc
surfaces the security posture requires.
