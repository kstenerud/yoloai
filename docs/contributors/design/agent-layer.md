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

## Openness — the file/data path is open; code-adapter deferred

Revised 2026-06-24 from an earlier "closed now, seam-shaped" lean. Once the capability model + re-homing made
the Definition *mostly data*, opening the data path turned out cheap — so we open it now rather than dancing
around it (the residual "code" was mostly mis-homed payload, not real mechanism).

- **File-defined agents are open** — `~/.yoloai/agents/<name>.yaml` (**own files** — an agent is a different
  noun than a profile, which *selects* an agent). A file inherently carries only the **data-expressible** subset
  (no Go func), so **the file format *is* the data/code boundary, enforced by construction.** This covers the
  realistic want — *wrap a non-Claude tool as an agent* (e.g. a security-research harness agent). A user-defined
  agent is **trusted config** (same trust level as a profile / Dockerfile the user writes), *not* untrusted
  input — which bounds the security surface (declaring seed files / a network floor / a launch command is the
  user configuring their own setup). Work to open: a **schema + loader + validator** (the capability model paid
  for the rest).
- **The code-adapter stays internal** — agents needing a procedural mechanism (Claude's settings-merge,
  opencode's validation) stay Go-defined. The public adapter interface is the *one* genuinely-deferred piece,
  gated on a real *code-needing* third-party agent.
- Declarative-izing the shipped agents' `ApplySettings` (wanted anyway; the re-homing already shrank it — the
  hook part leaves for completion, Gemini's is a key-flip, Claude's residual is key-flips + a shared merge
  helper) makes the shipped agents mostly file-expressible too — dogfooding the schema.

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
| **Context** | the `DEF`-injection **method** (append-at-`StateDir`/`ContextFile`, or aider's launch-flag) *(data)* | each layer's fragment → assembled **DEF** | generic `DEF`-deliverer → provision/envsetup *(ABC already seeded by Credentials)* |
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

### Context — the DEF-injection model (survey-confirmed)

Two refinements collapsed the earlier "context sink" into something smaller. **(1) The user's global config
(ABC) is already seeded** — the **Credentials/State** capability copies the agent's home dir (`SeedFiles` +
the agent-files copy governed by `AgentFilesExclude`), and the global `CLAUDE.md` is *not* excluded. So Context
does **not** reach outside; it is a *purely internal* step: deliver yoloAI's collected context (`DEF`) to where
the agent reads it. **(2)** A web survey of all five shipped agents
([research/agent-global-context.md](research/agent-global-context.md), verified mid-2026) found four fit a
single home-dir markdown file, and **Aider is a structural outlier** (no auto-read global file).

So the Context capability is **the agent's `DEF`-injection *method*, declared as data** — two shapes:

- **append-to-context-file** (Claude `~/.claude/CLAUDE.md`, Gemini `GEMINI.md`, Codex/OpenCode `AGENTS.md`):
  append `DEF` at the agent's already-declared `StateDir`/`ContextFile`. Since ABC is already seeded there, this
  is just an in-sandbox append. **Resolve the *effective* path** (footguns: Gemini's configurable
  `context.fileName`; Codex's `AGENTS.override.md` precedence + `CODEX_HOME`; OpenCode's CLAUDE.md fallback).
- **launch-flag** (Aider): no auto-read global file exists — write `DEF` to a scratch file and inject
  `--read <file>` into the launch command. This **crosses into the Launch capability** (a launch-arg the agent
  declares), the robust route vs mutating `~/.aider.conf.yml` (last-wins; a project config can override).

Both are **data** (a tagged method + parameters), so the capability *generalized* across the divergence rather
than breaking — confirming the "data + thin adapter" shape held without forcing code. The **fan-in** is
unchanged: `DEF` is assembled from each concerned layer's fragment (file-exchange → Q&A, sandbox → orientation,
netpolicy → isolation-notice), each owned by its contributor; the agent owns only the injection method.

**Findings (cleanups this surfaces):**
- The Q&A-protocol injection must become **agent-agnostic** — today hardcoded Claude-only
  (`if ContextFile == "CLAUDE.md"`, `context.go:177`); it is a `DEF` fragment, delivered by *each agent's*
  method. A mis-homing the principle predicts.
- **Append, don't clobber:** the append-method must append `DEF` to the *seeded* ABC, never overwrite it (the
  current write-then-append into the context file is the thing to fix).
- **The injected block is the weakest layer** — all agents let a closer project file win on conflict. Fine for
  operating-instruction defaults; the design must not *rely* on `DEF` being authoritative (not a containment
  concern — the sandbox is the real boundary).
- **AGENTS.md convergence:** Codex+OpenCode are native `AGENTS.md`, Gemini can be pointed at it; a *future*
  agent most likely uses `AGENTS.md` — a sane default for new registrations.

## The public surface (2026-06-24)

"Agent" means two things publicly, with different surfaces: the agent **type** (Claude as a declarative
capability bundle — the agent layer's own surface) and the agent **instance** (the agent running in *this*
sandbox — `sb.Agent()`, a *composition* over layers).

1. **Separability is *why* the agent layer is opt-in.** A caller wanting only sandboxing imports
   `substrate`/`runtime` and **never pulls in** the agent machinery. Guaranteed by the dependency direction —
   `agent → substrate/session/copyflow`, **never back** (the carve enforces it). The root `yoloai` package is
   batteries-included; `substrate` is the agent-free island. (This — not a registration API — was the original
   reason for the opt-in layer.)
2. **The type surface = a read-only capability catalog** (`yoloai.AgentTypes()` → `AgentInfo`), enriched from
   today's thin auth/model fields into the **public capability declaration**: one-shot/headless support,
   idle-mode (hook?), native-resume, prompt-mode, the network floor — the flags an embedder needs to choose
   *how* to run an agent. The internal `Definition` + the code-adapter interface stay internal.
3. **File-defined agents are open** — `~/.yoloai/agents/<name>.yaml` (see *Openness* above). The data path is
   open now; the code-adapter is the one deferred piece.
4. **The instance handle `sb.Agent()` is a join**, not the agent layer — agent identity (type/model) + prompt
   (`AgentLaunchSpec` read) + status (completion sidecar) + **`.IOSession()`** (the session channel, D88). The
   interaction primitives (`Attach`/`SendInput`/`CaptureTerminal`/`TerminalLog`) **move to `IOSession`** per
   D88; `Agent` slims to the agent-specific reads + the join. It lives in the **root `yoloai`** composition,
   not the agent package.

## Open questions — RESUME HERE

The capability-model spine, the re-homing map (+ the global-context survey,
[research/agent-global-context.md](research/agent-global-context.md)), and the public surface are all
**resolved**.

The old RESUME-HERE #2 — **the package boundary** — is now a **corollary, not an open question**: re-homing
fixes what lives in the agent package (the capability data model, the catalog, the model-resolver, the thin
code-adapter, the file loader/validator) while the runners live in their consuming layers; separability fixes
the dependency direction (`agent → lower`, never back); `sb.Agent()` lives in the root composition. The
concrete code-move from today's scattered `invocation`/`provision`/`create`/`context`/`network` is a
**Shape-phase** task, not a design decision.

So the agent layer is **ready for its D-number + a finalized spec** (like substrate/session/copyflow/
persistence) — consolidate this doc into a clean topic-organized spec + mint the next D-number. After it:
netpolicy, envsetup remain in the design cluster, then Shape and Move.
