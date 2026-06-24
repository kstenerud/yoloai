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

## The re-homing map (2026-06-24)

Re-homing is a **two-way sort**, and it reveals the agent layer is *thinner* than "gather all agent logic":
each capability splits **three** ways — the agent keeps its **declaration** (data + a thin adapter); the
cross-layer **payload** leaves for its owner; and the generic **runner** (detector-loop, seed-stager,
policy-composer, prompt-deliverer) belongs to the *consuming* layer, parameterized by the agent's declaration.
So the agent layer absorbs only agent-specific *data + tiny adapters*; the runners distribute outward.

| Capability | Agent keeps (data + thin adapter) | Payload → owner | Generic runner → layer |
|---|---|---|---|
| **Completion** | hook event-map + registration shape *(data)* + a *shared* append helper | status-writer cmd **+ turn-cursor** → completion | detector-loop / monitor → completion |
| **Launch** | command template + `PromptMode` + `SubmitSequence` + `ModelFlag` *(data)* | prompt text → session/lifetime; model name → caller | prompt-delivery (inject vs bake) + template-fill → session/lifetime |
| **Model** | alias table + prefix rules *(data)* + opencode validation *(thin adapter)* | requested model name → caller | the generic resolver → **agent layer** *(keeps resolver with its alias data)* |
| **Credentials** | env-var names + seed-file list + state dir *(data)* | credential **values** → caller (D63/DF38) | secrets-staging + seed-copy → **envsetup** |
| **Network** | required domains, a *floor* *(data)* | effective policy → **netpolicy** | policy-composer + enforcer → netpolicy |
| **Context** | the **global-context-file location** (`StateDir`+`ContextFile`) | each layer's fragment → assembled **DEF** | generic "append DEF to ABC at location" → provision/envsetup |
| **Self-config** | `folderTrust`/`sandbox`/notif key-flips *(data)* + `ApplySettings` residual *(thin adapter)* | — *(none)* | settings-writer → envsetup |

Byproduct: once the hook *command* leaves for completion, even Claude's "hard" mechanism collapses to **data**
(an event-name map + a *shared* append helper, not a per-agent func) — so the thin code-adapter shrinks to
~just opencode's model-validation and the settings-merge residual.

**Boundaries pinned with unbuilt layers** (the point of re-homing):
- **completion ←** the agent hands a registration request; completion owns the status-writer **and the
  turn-cursor maintenance** (a shell hook keeping a monotonic counter is completion's implementation detail —
  which *confirms* the command is completion's, not the agent's).
- **netpolicy ←** the agent's domain-*floor*; netpolicy composes floor + user-adds + isolation + enforcement.
- **envsetup ←** the seed-staging + settings-write mechanism, parameterized by the agent's
  credential-shape/seed-list/self-config/exclude-rules. **Credentials shed from the agent entirely** — shape
  to the agent, values to the caller, staging to envsetup.

**Forks resolved:** model-resolver → agent layer (keep the resolver with its alias data); credential staging →
envsetup; context → the global-context model below.

### Context — the global-context model

The clean shape (the "fuzzy multi-owner" framing reduced to a fan-in + a generic append):

- **Outside:** user's global `~/.claude/CLAUDE.md` = `ABC` · yoloai's collected context = `DEF` · workdir
  `CLAUDE.md` = `GHI`. **Inside:** the global file = **`ABC`+`DEF`** (yoloai's appended to the user's) · workdir
  file = `GHI` (copied untouched via copyflow — no one's concern).
- **The only agent-specific datum is the global-context-file location** (`StateDir`+`ContextFile`, already
  declared). Everything else is generic: *read the user's config there (ABC) → append yoloai's collected
  context (DEF) → write the result to that location inside.* No per-agent sink logic — a location + a generic
  append.
- **The fan-in:** `DEF` is assembled from each concerned layer's **fragment** — file-exchange → the Q&A
  protocol, sandbox → orientation, netpolicy → "you're network-isolated" later. Each fragment is owned by its
  contributor (the payload side); the agent owns only the location.

**Findings (cleanups this surfaces):**
- The Q&A-protocol injection must become **agent-agnostic** — today it is hardcoded Claude-only
  (`if ContextFile == "CLAUDE.md"`, `context.go:177`); it should be a `DEF` fragment appended at *each agent's*
  declared location. A mis-homing the principle predicts.
- **Append, don't clobber:** the runner must append `DEF` to the *seeded* user config, never overwrite it (the
  current write-then-append into the context file is the thing to fix).
- Agents with **no global-context location** (`StateDir`/`ContextFile` empty — e.g. aider today) currently
  receive no `DEF`. A capability gap declared by *absence* — acceptable, but those agents miss the operating
  instructions.

**Open verification (side trip, in progress):** the "single global-context-*file* location" shape is being
surveyed across all shipped agents (research note `research/agent-global-context.md`, pending) — to confirm no
agent has a structurally different mechanism (a config key, multiple hierarchical files, a `/memory` command,
or nothing) that the capability must generalize to rather than break on. The user's steer: *each agent knows
how to consolidate its own context; the outside only needs "how do I find your global stuff to pass it to
you?"* — so if an agent diverges, the divergence lives in that agent's thin adapter, not in the generic
runner.

## Open questions — RESUME HERE

The re-homing map (2026-06-24) is **resolved** (above), pending the global-context survey that verifies the
Context capability's per-agent shape. Remaining:

1. **The public surface.** What the `agent` package exposes — the capability/`Definition` types, the catalog,
   the code-adapter interface — and how it relates to the runtime `sb.Agent()` handle (Attach/SendInput/Prompt/
   CaptureTerminal/Logs, today a read-only accessor).
2. **The package boundary** relative to `invocation`/`provision`/`create`/`context`/`network`, which hold
   today's scattered agent-aware logic that this layer gathers.

Once these drain (and the global-context survey lands), the agent layer earns its D-number + a finalized spec
(like substrate/session/copyflow/persistence). After it: netpolicy, envsetup remain in the design cluster,
then Shape and Move.
