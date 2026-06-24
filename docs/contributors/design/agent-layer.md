# Agent layer — per-agent adapters over the agent-free lower layers

**Status: DESIGN IN PROGRESS (no D-number yet).** Started 2026-06-24. The **agent** refinement of
[plans/public-layering.md](plans/public-layering.md): the one place that knows "this is Claude", sitting on
the now-agent-free substrate ([D84](../decisions/working-notes.md)), session ([D88](../decisions/working-notes.md)),
copyflow ([D86](../decisions/working-notes.md)), and persistence ([D87](../decisions/working-notes.md)). The
**capability-model spine** has converged; the re-homing map and the public surface remain (RESUME-HERE).
Grounded in a source map of `internal/agent` + its consumers.

## The problem

The agent "layer" **does not exist as a layer yet.** What exists is a flat ~24-field `Definition`
(`internal/agent/agent.go`) plus agent-aware logic *scattered* across `invocation` (model/command/detector
resolution), `provision` (seed files / secrets / settings), `create`+`context` (validation / context files),
`network` (allowlist provenance), `discovery` (the public catalog), and `state`. So the design job is to
**gather that scattered translation into one coherent layer on the agent-free lower layers** — and give it a
shape. (This is the agent-aware half of [DF32](findings-unresolved.md).)

## The core principle — mechanism vs payload

> **An agent declares the *mechanism* (HOW it does a thing); the consuming layer supplies the *payload* (WHAT
> to do). The agent layer owns its adapter mechanisms and its own static self-config — never another layer's
> payload.**

The agent layer is thus a set of **per-agent adapters**: each translates an agent-*agnostic* request from an
upper layer into *this* agent's specifics, and holds nothing that belongs elsewhere.

**Worked example (the hardest case).** Claude's `injectIdleHook` (`agent.go:441`) fuses two things: (a) Claude's
hook-registration *shape* — `settings.hooks.<Event> = [{hooks:[{type:"command", command:X}]}]`, appended under
`Stop`→idle / `PreToolUse`+`UserPromptSubmit`→active — which is genuinely agent-specific *mechanism*; and (b)
the status-writer *command* `X`, a shell blob that writes yoloai's `agent-status.json` in the schema the
monitor reads — which is the **completion layer's payload**, mis-homed into `agent.go`. The principle re-homes
`X` to completion; the agent keeps only the registration shape, parameterized by completion's command. The
tell that `X` belongs to completion: when tier-2's **turn-cursor** lands (the current command writes `status`
but no turn index), `X` changes — and that change must live with completion, not in `agent.go` constants.

## Capability groups — each owned by exactly one consuming layer

| Capability | Mechanism (agent declares HOW) | Payload (layer supplies WHAT) | Owning layer |
|---|---|---|---|
| **Completion** | hook-registration shape / ready-pattern / wchan-applicability | the status-writer command (the turn-cursor contract) | completion (tier-2, D88) |
| **Launch** | command template + `PromptMode` + `SubmitSequence` + `ModelFlag` | the prompt text; the model; lifetime intent | session / lifetime (D88) |
| **Model** | alias table + prefix rules + format-validation | the requested model name | caller / `invocation` |
| **Credentials** | which env-vars, which seed files, the state dir | the credential **values** | caller ([D63](../decisions/working-notes-archive.md)/DF38) |
| **Network** | the required domains (a *floor*) | the actual policy (isolation + user-adds + enforcement) | netpolicy (future) |
| **Context** | "I read `CLAUDE.md`/`GEMINI.md`" | the context content | context/provision |
| **Self-config** | `folderTrust` off, `sandbox` off, notif-channel | — *(no payload — the agent's own "run-me-like-this")* | agent-owned |

Refinement: some capabilities are **mechanism-only** (static self-config — folder-trust-off is the agent
saying how *it* wants to run in a sandbox, not a payload from elsewhere). The full rule: *agent owns mechanism
+ its own config; never another layer's payload.*

**Folder-trust as an agent-layer principle.** Claude's trust dialog (DF13) and Gemini's
`security.folderTrust.enabled=false` (`agent.go:217`) are the same capability: *the sandbox is the real trust
boundary, so agent-level folder-trust is pre-satisfied at provision time* — declaratively where the agent
allows it (Gemini), by pre-trusting the workdir at create where it's a dialog (Claude; the re-launch hardening
in D88).

## Declaration shape — data + a thin code-adapter (chosen: option a)

Most of an agent declaration is **data**: alias tables, env-var lists, network allowlists, command templates
with slots, settings key-flips, even the hook-registration *shape*. A **thin typed code-adapter interface**
holds the residual genuinely-procedural mechanisms — the settings array-*append*/merge (RFC 7386 merge-patch
can't append, so preserving a user's existing hooks needs code or a richer patch) and opencode's
model-format validation. The code-adapter **is the seam**.

**Rejected for now: fully-data (option b)** — an append-aware patch format + declarative validation rules so
even the residual is config. Deferred to when open-registry is actually wanted; no consumer needs it yet
(YAGNI; the ROADMAP's new agents are *shipped* additions, and control-eval uses claude).

## Open vs closed — closed now, seam-shaped

The registry stays the shipped closed set (it is a hardcoded map today with zero extension points; the public
`AgentInfo` catalog in `discovery.go` is read-only). The capability model + code-adapter is the **seam** that
makes opening cheap later: the principle revealed that most of the agent's "code" was *mis-homed payload*, not
real mechanism, so after re-homing the residual code is small. No `Register()` until a real consumer (e.g. a
custom non-Claude harness agent for the security-research direction) forces it — same "shape the seam, defer
the build" move as Stream (D88) and the `hook-unreliable` mode.

## Open questions — RESUME HERE

1. **The re-homing map.** Confirm each payload's destination layer (most are obvious from the table). The
   sharp ones cross into layers not yet built: **completion ← the hook-command** (the completion layer must own
   the status-writer + turn-cursor, the agent only the registration shape) and **netpolicy ← the
   allowlist-floor** (the agent declares its required domains; netpolicy composes the policy). These two pin
   boundaries with unbuilt refinements.
2. **The public surface.** What the `agent` package exposes — the capability/`Definition` types, the catalog,
   the code-adapter interface — and how it relates to the runtime `sb.Agent()` handle (Attach/SendInput/Prompt/
   CaptureTerminal/Logs, today a read-only accessor).
3. **The package boundary** relative to `invocation`/`provision`/`create`/`context`/`network`, which hold
   today's scattered agent-aware logic that this layer gathers.

Once these drain, the agent layer earns its D-number + a finalized spec (like substrate/session/copyflow/
persistence). After it: netpolicy, envsetup remain in the design cluster, then Shape and Move.
