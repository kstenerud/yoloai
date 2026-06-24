# Session layer — the durable I/O channel over an agent process

**Status:** Design converged 2026-06-23 (design conversation), not yet implemented. The target surface for
the **session** refinement of [plans/public-layering.md](plans/public-layering.md) — shaped as-if-public, to
live behind `internal/` until the promotion move. A *consumer* of the substrate ([D84](../decisions/working-notes.md));
the module-split's deferred "C-full". Closes [DF31](findings-unresolved.md) and [DF33](findings-unresolved.md)
(the Go↔Python carve); dissolves [DF13](findings-unresolved.md) (the restart trust race); the credential slice
is carved off as [DF38/DF39](findings-unresolved.md). Driven by the **control-eval** consumer (an AI-control
eval harness; `~/experiments/control-eval/docs/yoloai-trial-engine-report.md`). Backed by
[research/container-init-delineation.md](research/container-init-delineation.md).

**One-line definition.** An `IOSession` is a *durable, reattachable I/O channel to a running agent process* —
four operations (**attach · inject · capture · persist**) over one of two channel strategies
(`SessionKind {PTY, Stream}`). It concentrates today's ~70-file tmux scatter into one handle, and lifts the
agent session off the container's mandatory init so the substrate underneath stays agent-free.

## The model (the decisions behind the surface)

1. **An `IOSession` is a channel, not a process.** It is the durable, reattachable conduit through which you
   *drive* (inject) and *observe* (attach/capture) a running agent, persisting across disconnects. The agent's
   *execution* is a separable concern: the one-shot `-p` agent runs with **no `IOSession` at all** (a bare
   substrate `Launch`+`Wait`). `attach`/`capture`/`inject` are a surface over a running agent that does not
   care whether anyone is attached — that detachability *is* the "durable/reattachable" essence. Named
   `IOSession` (not `Session` — collides conversationally with the agent's own *conversation*; not
   `TerminalSession`/`Console` — channel-shaped, see §1). Likely reached as `sb.Agent().IOSession()`.

2. **Two channel strategies, chosen by the agent's needs — `SessionKind {PTY, Stream}`.** Only *channel
   provision* differs between them; persist/reattach/inject/capture are common. **PTY** (terminal) — rich
   cursor/colors/redraw; needs a *broker* (the agent CLI isn't independently reattachable, so it runs under
   tmux/dtach). **Stream** — durable stdio, no PTY: stdout→a log, input←a fifo; attach=tail, inject=write-fifo,
   capture=read-log. `SessionKind` is **orthogonal to `PromptMode`** (how the prompt arrives) — a PTY session
   can take a CLI-arg prompt; a Stream session is the genuine no-tmux path. Stream is **designed but deferred**
   (no consumer forces dropping the broker yet — control-eval is served by PTY/`-p`; real-demand rule).

3. **`lifetime ∈ {one-shot, persistent}` — caller intent, a third axis.** Distinct from `PromptMode` and
   `SessionKind`: does the agent live beyond one prompt? **One-shot** has two realizations chosen by *agent
   capability*: an agent with a headless one-shot command (`claude -p`) → substrate `Launch`+`Wait(exit)`
   (primary; exit=done, `--output-format stream-json` yields usage; no `IOSession`; smallest attack surface);
   a TUI-only agent → composition over a persistent `IOSession` (inject → wait-turn-done → read → shutdown).
   **Persistent** is the interactive/multi-turn case. The caller declares `lifetime`; capability selects the
   realization.

4. **The carve: a neutral PID 1; the agent session is a `Launch` on top (closes DF31/DF33).** The container
   init becomes a dumb keep-alive (`tini`/`pause`: reap, hold the namespace, know nothing of agents/tmux). The
   Python entrypoint's work is **demoted from "the init" to "a process the Go session layer starts via
   `Substrate.Launch`"**. *Neutral PID 1 ≠ no in-container helpers* — helpers become explicitly-launched,
   session-scoped processes, not mandatory middleware. Not greenfield: seatbelt already has the bare keep-alive
   (`bareInstance`) vs full split and tart a P1-only Start; the carve **promotes the P1 keep-alive to the
   default**.

5. **Coarse `Launch` — one Launch per session; Go owns policy, not in-container mechanism.** PTY → `Launch` a
   *slimmed in-container session process* (today's `sandbox-setup.py` minus being-PID-1 and minus the
   agent-free provisioning): it brings up the broker, starts the agent in a pane, starts the monitor. Stream →
   `Launch` the bare agent with stdio→files. Go owns *which `SessionKind`, what spec, when to launch/relaunch*;
   it does **not** own in-container step choreography (rejected: fine-grained Go-driven tmux/agent/monitor steps
   — chatty, fragile, re-implements timing-sensitive logic). `IOSession.Inject/Capture/Attach` are host-side Go
   methods wrapping `Substrate.Exec` (send-keys/capture-pane for PTY; fifo-write/log-tail for Stream).

6. **The `Launch` unit differs by `SessionKind` — which is *why* exit=done is Stream-only.** PTY: the tracked
   process is the **broker** (substrate liveness = "broker up"); the agent going idle/done is the **monitor's
   sidecar** (§7), not a substrate transition. Stream: the tracked process **is the agent**, so substrate
   liveness = agent liveness and **exit = done** falls straight out. Substrate liveness (tracked process) and
   agent-status (monitor sidecar) stay cleanly separated — the completion tiers (§6) map onto exactly this
   split.

7. **Three-bucket schema split (closes DF33; refines D84).** Today's `runtimeconfig` DTO fuses substrate and
   agent fields. It splits into: **`ProvisionSpec`** (substrate, container-up: image, mounts, resources,
   network, isolation, ports, env — agent-free, backend-consumed); **`ProcSpec`** (the substrate's `Launch`
   input: command, args, TTY, env, cwd — *agent-neutral*, "run this process this way"); **`AgentLaunchSpec`**
   (agent layer: ready-pattern, detector stack, prompt + prompt-mode, submit-sequence, model, resolved workdir
   path). The agent layer **compiles** `AgentLaunchSpec` → a neutral `ProcSpec` + a **staged agent-config
   artifact** the in-container session process + monitor read (the descendant of `runtimeconfig.json`, minus
   what became `ProvisionSpec`). D84's "agent `command/ready/idle` move to the agent layer's `ProcSpec`"
   conflated two types; the resolution is **two types** — agent fields never enter a substrate type. That is
   what makes DF31+DF33 *close* rather than relocate.

8. **The monitor is relocated, not rewritten.** The proven idle-detection stack (hook → wchan → ready-pattern
   → output-stability) **stays in-container**, started by the Go session layer as a sibling to the agent (not
   as PID 1), writing a **sidecar status file** the host reads (the [D85](../decisions/working-notes.md)
   agent→sidecar shape). Rejected: a host-side Go monitor driving capture via `Exec` (big rewrite; a
   host↔container round-trip per poll). The carve's goal is a neutral *substrate*, not "no in-container code".
   On the Stream/`-p` path there is **no monitor** (exit = done).

## Completion — the three tiers

All answer "is the agent's work complete?" at rising reliability/finality. A tier is selected by the agent's
capabilities; the host waits via `Sandbox.Wait` regardless of which is in play.

- **Tier-1 — idle (heuristic).** The monitor *guesses* the turn ended (ready-pattern, output-stability). The
  agent-agnostic baseline; works for any agent. Already shipped (`Sandbox.Wait`, `yoloai wait`, `sandbox_wait`).
- **Tier-2 — idle (authoritative).** The agent *declares* the turn ended via its own turn-stop hook (Claude's
  `Stop`). A **per-agent capability** (heuristic stack = agnostic baseline; hook = the upgrade — same parity
  rule as native-resume in §re-launch). See §"Tier-2".
- **Tier-3 — exit=done.** The agent *process* terminated. Substrate liveness — available only for the Stream/`-p`
  realization where the agent *is* the tracked process (§6). Gated on the carve.

control-eval rides tier-1 on `main` today and migrates to tier-3 (the clean `-p` one-shot, with `stream-json`
usage) when the carve lands — `run --lifetime one-shot` swaps the realization underneath it.

## Tier-2 — authoritative per-turn idle

The hook *declares* turn completion, vs tier-1's guess. It lives as **agent-status in a sidecar, above
substrate liveness** (D84-clean); the relocated monitor (§8) is its consumer. ("Done" is loose — tier-2 is
authoritative *idle / turn-complete*, not session-over; that is tier-3 / liveness.)

- **Hook-authoritative-and-exclusive (the blip fix).** When an agent is hook-capable, idle is determined
  **only** by the hook; the heuristic detectors run **only** for hook-less agents. The startup blip — a false
  early `idle` before real work — is a *heuristic artifact* (the hook never fires on it), so trusting the hook
  alone removes it. The crash/hang case (hook never fires) is covered by **liveness** (process exit →
  done/failed), not heuristics — so for hook-capable agents the heuristics add nothing but the blip.
- **Escape hatch (deferred, designed-for).** Idle detection has a long painful history, so we do *not* bet
  everything on the hook: a per-agent **`hook-unreliable`** flag is a planned future mode that runs the hook
  *and* heuristics together (hook when it fires; heuristics backstop a *missed* hook). Built only if testing
  shows a flaky hook; the open detail (combining without reviving the blip — likely heuristics as a *late,
  conservative* backstop) is a testing-time problem. **Design-now requirement:** detector resolution is a
  per-agent **mode selector** `{hook-authoritative, heuristic-only, (future) hook-assisted}`, not a hardcoded
  hook-XOR-heuristics, so the third mode slots in without rework. Build the first two.
- **The contract carries a turn cursor.** Even an authoritative hook can be read *stale*. The signal carries a
  **monotonic turn index (+ timestamp)**: the agent emits turn events (turn-start → `active`, turn-stop →
  `idle`, incrementing the counter) into the agent sidecar — a single overwritten `agent-status.json` carrying
  the counter (YAGNI over an append-only event log). `Sandbox.Wait(WaitForIdle)` waits for *completed-turn >
  the turn at which my prompt was submitted*, so it cannot trip on an earlier idle. Tier-2 is thus a
  **reliability upgrade to the existing `Wait`**, not a new API.

## Re-launch — restart, stop/start, create

Three operations distinguished by **which layer cycles** — a payoff of the carve's independent
substrate/session lifecycles:

- **Restart (session-level):** substrate stays **up**; the orchestrator terminates the `Launch`'d session tree
  and re-`Launch`es it. `Substrate.Start` is *not* called. Light, fast, environment unchanged.
- **Stop → Start (substrate-level):** substrate cycles down/up: `Substrate.Start` = agent-free env-up + neutral
  PID 1 **only** (supervision is caller policy, D84), then the orchestrator re-issues `Launch`.
- **Create / reset-to-fresh:** fresh substrate and (for create) a fresh workdir copy.

**Persisted vs ephemeral.** `ProvisionSpec` (env definition) + `AgentLaunchSpec` (agent intent) persist —
`ProvisionSpec` in the substrate store record, `AgentLaunchSpec` in a D85 agent **sidecar**; both re-read on
start. The session process tree is **always ephemeral**, re-materialized by `Launch` — never the old PID
("re-open-env-never-process", D84). A bare `start` uses the *persisted* `AgentLaunchSpec` (changing model/etc.
is `reset`/recreate, not a silent start-time swap).

**Prompt replay is decided by `lifetime`, not a flag.** "Restart = re-run the launch (`ProcSpec`)" gives the
right answer for both because the prompt is *in* the launch command for one-shot and not for persistent:
one-shot/headless bakes it in (`claude -p "task"`) → restart **re-injects** (what a script driver wants);
persistent/interactive injects the prompt post-launch → restart re-runs the agent command only → **no replay**.
A script wanting re-injection uses one-shot / `reset --prompt`, not a flipped default.

**Persistent restart = fresh agent** (resolves the resume-vs-fresh question). Two reasons: the environment
(work copy, `~/.claude` history, progress) **persists**, so the original prompt is stale by definition; and the
**cross-agent parity rule** — the baseline re-launch must be the lowest common denominator, and "relaunch a
fresh agent" is the only thing *every* agent supports. Native conversation-resume (Claude's `--continue`) is a
*deferred per-agent capability* on `AgentLaunchSpec`, never the baseline. Matches the current `relaunchAgent`
default.

**Fall-to-shell resume hint (the user-initiated alternative to auto-relaunch).** Auto-relaunch is *declined* —
it conflates "exited" (fact) with "should be running" (policy the launcher can't read): it can't distinguish an
intentional `/quit` from an accident, turns crashes into crash-loops, and breaks one-shot `lifetime` (a `-p`
run that exits *is done*; relaunch would re-run the baked prompt forever). Auto-relaunch is Tier-2 supervision,
which the substrate declines (D84 §4); it stays available only as *opt-in caller policy*, never a default.
Instead, the **launch shape** for a persistent/PTY session is `… ; exec $SHELL` (no `exec` on the agent), so
**agent-exit drops to an interactive shell in the same pane** — not a dead pane — which prints a resume hint and
carries a `resume.sh` on `PATH`. This is the concrete, shippable form of the deferred native-resume capability:
*data + a launch-wrapper, entirely in the box*, user-initiated (intent preserved). The resume **command** is an
agent-layer declaration (see [agent-layer.md](agent-layer.md) Resume capability); the session layer supplies the
launch shape + the session-id. **Gated by `lifetime` × `SessionKind`:** persistent + PTY only — one-shot/Stream
sessions have no terminal to drop into, so exit = done, no shell, no hint. Deterministic resume of the *right*
conversation needs the session id; the cleanest route is to **inject a known session id at launch** where the
agent supports it (Claude `--session-id`), so resume is exactly `--resume <same-id>` — a per-agent detail to
verify at Shape.

**The trust prompt is a fresh-*environment* event, not a restart event.** Claude's folder-trust lives in the
persisted `~/.claude`, keyed to a stable workdir path — a *first-encounter-of-this-folder* event. In-place
restart never triggers it; stop/start doesn't either. DF13's dialog-on-restart was a symptom of
restart-as-stop_start; under this model it **should dissolve** — but DF13 itself notes it needs a reproduction,
so *verify with a smoke repro at Shape*, don't assume. Belt-and-suspenders hardening: pre-trust the workdir at
create.

**Two further lifecycle legs the carve must preserve** (beyond restart/stop-start/create): **in-place `reset`**
(the rsync workdir re-sync while the agent keeps running — container backends; shells out to host `rsync`) and
**suspend/resume** (tart VM, cap-gated per D84). Both re-materialize or preserve the session under the same
"re-open-env-never-process" principle; neither introduces new session state, but the Shape must keep them
working through the carve.

## Security — the one-way trust valve

The `IOSession` is the host-side boundary to an **untrusted** agent (control-eval's adversarial case is the
worst). It is the *mirror* of copyflow's hermetic seal ([D86](../decisions/working-notes.md)): copyflow
protects the host *filesystem* (untrusted in-sandbox git mustn't write host originals); this protects the host
*terminal + shared artifacts*. Threat model: capture/attach egress untrusted bytes (**secrets** the agent
printed + **terminal-escape sequences** — OSC-52 clipboard, cursor/title injection); inject ingresses to the
untrusted box.

**Egress is minimized by the carve.** Because the monitor is in-container (§8), routine idle-capture stays *in*
the sandbox — only the minimal derived **status sidecar** crosses; tier-2 shrinks even that. Raw capture
egresses **only on explicit user actions** (attach, snapshot, bug report) — a small, deliberate surface.

- **Capture = tainted at the boundary.** **Live `attach` = pass-through** — you cannot sanitize a live TUI
  without breaking it (the escapes *are* the UI); the stance is the **ssh-to-untrusted-host model** (attaching
  exposes your terminal to the agent by your explicit choice). **Snapshot / bug report (the share vector) =
  sanitize escapes + best-effort secret-redact + opt-in inclusion** — free-form output has no schema, so
  auto-redaction is unreliable; we do **not** pretend it is auto-safe (runs through the existing `bugreport.go`
  sanitization).
- **Inject = opaque intent in** — argv-parameterized, **never** interpolated into a host shell command (the
  current `deliverPromptViaTmux` already does this; lock it as contract).
- **Accepted on one condition — documentation is load-bearing.** The risk is acceptable *only because it is
  informed*: the `attach` help / GUIDE security section must state the terminal-exposure (ssh-model) risk, and
  the bug-report/snapshot path must warn that captured output may carry secrets and that redaction is
  best-effort. A definition-of-done deliverable, not a footnote.
- **Scope boundary:** inject *authorization* (who may drive the agent) is the embedder/principal concern
  (D58/D59 isolation axes), not the session layer — mechanism here, policy above.

## Backend topology + failure semantics (2026-06-24 design-review remediation, D92)

**The carve is uniform across all six backends** — verified, all run the *same* `sandbox-setup.py`
([backend-topology.md](backend-topology.md)); the per-backend difference is only *where* it runs and *how* it's
launched, which `KeepAliveModel`/`FilesystemLocality` already abstract. The coarse-`Launch` "in-container
session process" framing (§5) is shorthand for "in the backend's locality" — container (docker/podman), **VM
guest** (containerd/tart/apple), or **host** (seatbelt).

**The one structurally-divergent case — seatbelt (`HostKeepAlive`).** There is no "inside" to launch into: the
broker/agent/monitor are **host processes** in the substrate's own process tree (`sandbox-exec`-confined). So
§6's liveness derivation needs a host-process variant: substrate liveness is the **host process group**, not a
container/guest; the "tracked process" is the host-side session process. (NB the earlier conversational claim
that *both* seatbelt and tart "run on the host" was wrong — tart/containerd/apple are VM guests; only seatbelt
is a host process.)

**Failure semantics (the happy path was the only path specified):**
- **Launch readiness vs idle-wait.** A `Launch` that partially comes up — broker up but the agent dies, or the
  monitor never writes its first status — must fail through a **bounded launch-readiness timeout distinct from
  `Wait(WaitForIdle)`** (which waits on a turn cursor that would never advance). "The monitor never reported at
  all" is a launch failure, not an idle timeout.
- **Teardown of the relocated session.** For container/VM backends the broker+monitor die with the box; for
  **seatbelt** they are host processes that must be reaped on stop/destroy (already done via `killByPID`/pgrep
  — preserve it through the carve; this is *not* the DF40 issue, which is terminal-corruption, not a leak).
- **Conformance coverage.** The carve multiplies the `Launch`/relaunch error surface;
  [DF18](findings-unresolved.md) already records that dead-daemon-mid-op / image-missing / overlay error paths
  are unhit by the conformance suite. The suite must grow to cover the new `Substrate.Launch`/`Process.Wait` and
  relaunch error branches at Shape.

*(Minor consistency note: tier-3 "exit=done" spans both the Stream `SessionKind` and the no-`IOSession` `-p`
path — both have the **agent** as the substrate-tracked process. §6 derives it from the Stream kind; the `-p`
path is the same principle without an `IOSession`. Cosmetic, not a contradiction.)*

## Implementation notes (spec-time to-dos)

- **Confirm the current `Stop`-hook wiring** before implementing tier-2 — is Claude's `Stop` already writing
  `agent-status.json`, or does the existing hook detector (`ResolveDetectors`, `IdleSupport.Hook`) read
  something else? Doesn't change the design.
- **Name the internal in-container session process** — `session-runner` if it persists as the session anchor,
  `session-setup` if it sets-up-and-hands-off to the broker. Internal, not public API.
- **Audit the inject escaping** (`deliverPromptViaTmux`) and lock the argv-parameterized payload as the
  `IOSession.Inject` contract.
- **Enumerate the doc surfaces** the security posture requires (attach help, GUIDE security section,
  bug-report warnings).
- **Stream `SessionKind` is deferred** — shape the boundary, don't build the no-PTY strategy until a consumer
  forces dropping the broker.

## Cross-references

- **Decisions:** [D84](../decisions/working-notes.md) (substrate — liveness-only status, mechanism-not-policy,
  re-open-env-never-process), [D85](../decisions/working-notes.md) (agent→sidecar persistence),
  [D86](../decisions/working-notes.md) (copyflow seal — the mirror), this layer's own entry **D88**.
- **Findings:** closes [DF31](findings-unresolved.md) (substrate bakes in tmux+monitor) and
  [DF33](findings-unresolved.md) (`runtimeconfig` mixes substrate+agent fields) via the carve + schema split;
  dissolves [DF13](findings-unresolved.md) (restart trust race); credential delivery is
  [DF38](findings-unresolved.md)/[DF39](findings-unresolved.md), out of this layer's scope.
- **Consumer:** control-eval (`~/experiments/control-eval`) — the AI-control eval harness that drove the
  `lifetime`/completion-tier model; its P1/P2/P3 asks map onto tiers 1→3, `stream-json` usage, and the one-shot
  `run` verb.
