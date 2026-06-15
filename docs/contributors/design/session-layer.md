# Session layer — interactive/stream I/O channel refinement

**Status: DESIGN IN PROGRESS (not converged, no D-number yet).** Captured 2026-06-15 mid-design so
the conversation can resume after a machine restart. This is the framing reached so far + the open
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

## Open questions — RESUME HERE

The kickoff question put to the user (unanswered at the restart) was: does the framing fit, and which
thread first —
1. **Scope vs the C-full deferral** — design the *abstraction* now (`Session` interface + `SessionKind`,
   tmux as one strategy) but *implement* the stream strategy only when a real headless consumer appears
   (real-demand rule)? Lean: yes — shape the boundary, don't build stream speculatively.
2. **Capture feeds Q103** — the monitor consumes `Session.Capture` for idle/snapshots, but a *stream*
   session has no terminal to capture (only a byte log), so idle detection leans on the channel-agnostic
   hook-log signal with `capture` as a PTY-only extra. Ties back to the substrate status model.
3. **The Go↔Python boundary (the heavy one)** — how much session logic moves to Go (launch the broker
   via `Launch`, inject/capture via `Exec`) vs stays in a thin in-container helper.

Also still to do for this layer (not yet discussed): the **security** angle (input injection / capture
within the sandbox vs across the host boundary — lighter than copyflow's hermetic seal, but capture can
egress agent-printed secrets into bug reports), and the **handle/name** for the `Session` type.
