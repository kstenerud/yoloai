> **ABOUTME:** Design for the agent layer: the one place that knows concrete agent types exist,
> translating agent-agnostic requests from upper layers into each agent's own launch, model,
> credential, and context specifics while keeping every layer below it agent-free.

# Agent layer — per-agent adapters over the agent-free lower layers

**Status:** Design converged 2026-06-24 (design conversation), not yet implemented. The **agent** refinement of
[plans/public-layering.md](plans/public-layering.md): the one place that knows "this is Claude", layered on the
now-agent-free substrate ([D84](../decisions/working-notes.md)), session ([D88](../decisions/working-notes.md)),
copyflow ([D86](../decisions/working-notes.md)), and persistence ([D87](../decisions/working-notes.md)). The
agent-aware half of [DF32](findings-unresolved.md). Backed by
[research/agent-global-context.md](research/agent-global-context.md) (the global-context survey).

**One-line definition.** The agent layer is a set of **per-agent adapters**: each translates an
agent-*agnostic* request from an upper layer into *this* agent's specifics, and holds only the agent's own
declarations + its static self-config. It is the one layer that knows agent types exist; everything below it is
agent-agnostic mechanism.

## The model (the decisions behind the surface)

1. **Mechanism vs payload — the core principle.** An agent declares the **mechanism** (HOW it does a thing);
   the consuming layer supplies the **payload** (WHAT to do). The agent owns its adapter mechanisms and its own
   static self-config — **never another layer's payload**. The worked hardest-case: Claude's `injectIdleHook`
   fuses the hook-registration *shape* (genuine agent mechanism — keep) with the status-writer *command*
   (the completion layer's payload, encoding the `agent-status.json` + turn-cursor contract — re-home). The
   tell that the command is completion's, not the agent's: when tier-2's turn-cursor lands, the command
   changes, and that change belongs to completion.

2. **Capability groups, each owned by exactly one consuming layer.** The flat 24-field `Definition` clusters by
   *which layer consumes it* — completion, launch, model, credentials, network, context, self-config (table
   below). An agent is a bundle of capability declarations; the agent layer is the single translator from "run
   claude" into the lower layers' inputs.

3. **Declaration = data + a thin code-adapter.** Most of a declaration is **data** (alias tables, env-var
   lists, network allowlists, command templates with slots, settings key-flips, the hook-registration shape,
   the context-injection method). A **thin typed code-adapter** holds the residual genuinely-procedural
   mechanisms — the settings array-merge (RFC 7386 merge-patch can't append) and opencode's model-format
   validation. (Rejected: fully-data now — an append-aware patch format + declarative validation — deferred to
   when a code-needing third-party agent appears.)

4. **Re-homing is a three-way sort, and it makes the agent layer *thin*.** Each capability splits three ways:
   the agent keeps its **declaration** (data + thin adapter); the cross-layer **payload** leaves for its owner;
   and the generic **runner** (detector-loop, seed-stager, policy-composer, prompt-deliverer) belongs to the
   *consuming* layer, parameterized by the declaration. So the agent layer absorbs only agent-specific *data +
   tiny adapters*; the runners distribute outward (closes the agent-aware half of DF32).

5. **Openness — the file/data path is open; the code-adapter is deferred.** Once the capability model made the
   Definition mostly data, opening the data path is cheap, so **file-defined agents are open now**
   (`~/.yoloai/agents/<name>.yaml`). A file carries only the data-expressible subset (no Go func), so the file
   format *is* the data/code boundary, enforced by construction. Agents needing a procedural mechanism stay
   Go-defined behind the internal adapter — the one genuinely-deferred piece.

6. **The public surface is type vs instance.** The agent **type** (a declarative capability bundle) is the
   agent layer's own surface — a read-only catalog. The agent **instance** (`sb.Agent()`) is a *composition*
   over layers, not the agent layer. Separability (a caller can use sandboxing without agent machinery) is the
   reason the layer is opt-in, guaranteed by the dependency direction `agent → substrate/session/copyflow`,
   never back.

## Capability groups

| Capability | Agent keeps (data + thin adapter) | Payload → owner | Generic runner → layer |
|---|---|---|---|
| **Completion** | hook event-map + registration shape *(data)* + a *shared* append helper | status-writer cmd **+ turn-cursor** → completion | detector-loop / monitor → completion |
| **Launch** | command template + `PromptMode` + `SubmitSequence` + `ModelFlag` *(data)* | prompt text → session/lifetime; model name → caller | prompt-delivery (inject vs bake) + template-fill → session/lifetime |
| **Model** | alias table + prefix rules *(data)* + opencode validation *(thin adapter)* | requested model name → caller | the generic resolver → **agent layer** *(keeps resolver with its alias data)* |
| **Credentials** | env-var names + seed-file list + state dir *(data)* | credential **values** → caller (D63/DF38) | secrets-staging + seed-copy → **envsetup** |
| **Network** | required domains, a *floor* *(data)* | effective policy → **netpolicy** | policy-composer + enforcer → netpolicy |
| **Context** | the `DEF`-injection **method** (append-at-`StateDir`/`ContextFile`, or aider's launch-flag) *(data)* | each layer's fragment → assembled **DEF** | generic `DEF`-deliverer → provision/envsetup *(ABC already seeded by Credentials)* |
| **Self-config** | `folderTrust`/`sandbox`/notif key-flips *(data)* + `ApplySettings` residual *(thin adapter)* | — *(none — the agent's own "run-me-like-this")* | settings-writer → envsetup |
| **Resume** | resume command template (or *"none"*) + session-id support flag *(data)* | session id → session/lifetime | fall-to-shell launch-wrapper + hint print → session |

Refinement: some capabilities are **mechanism-only** (self-config has no consumer payload). Byproduct of
re-homing: once the hook *command* leaves for completion, even Claude's "hard" mechanism collapses to data (an
event-map + a *shared* append helper, not a per-agent func), so the thin code-adapter shrinks to ~just
opencode's validation and the settings-merge residual.

**Resume is honestly characterized, never faked.** The Resume declaration is *data* — a resume command template
or the literal *"none"*. Agents with native resume (Claude `--resume`, Codex/Gemini equivalents) run the real
thing; agents without (Aider, our known global-context outlier) declare *"none"* → the `resume.sh` relaunches a
**fresh** agent and *says so* (netpolicy-style honest characterization — never print "resumed" when it wasn't).
The session-layer fall-to-shell wrapper + session-id supply are the generic runner; see
[session-layer.md](session-layer.md) "Fall-to-shell resume hint". Deterministic resume depends on a session id —
the agent declares whether it can *set* one at launch (Claude `--session-id`), which the session layer injects.

**Folder-trust as an agent-layer principle.** Claude's trust dialog (DF13) and Gemini's
`security.folderTrust.enabled=false` are the same capability: *the sandbox is the real trust boundary, so
agent-level folder-trust is pre-satisfied at provision time* — declaratively where the agent allows it
(Gemini), by pre-trusting the workdir at create where it is a dialog (Claude; the D88 re-launch hardening).

## The re-homing map

**Boundaries pinned with unbuilt layers** (the payoff of re-homing — it writes the contracts):
- **completion ←** the agent hands a registration request; completion owns the status-writer **and the
  turn-cursor maintenance** (a shell hook keeping a monotonic counter is completion's implementation detail).
- **netpolicy ←** the agent's domain-*floor*; netpolicy composes floor + user-adds + isolation + enforcement.
- **envsetup ←** the seed-staging + settings-write mechanism, parameterized by the agent's
  credential-shape/seed-list/self-config/exclude-rules. **Credentials shed from the agent entirely** — shape to
  the agent, values to the caller, staging to envsetup.

**The package boundary follows as a corollary**, not a separate decision: re-homing fixes what lives in the
agent package (the capability data model, the catalog, the model-resolver, the thin code-adapter, the file
loader/validator); the runners live in their consuming layers; separability fixes the dependency direction;
`sb.Agent()` lives in the root composition. The concrete code-move from today's scattered
`invocation`/`provision`/`create`/`context`/`network` is a **Shape-phase** task.

## Context — the DEF-injection model

Two refinements collapsed the earlier "context sink" into something small. **(1)** The user's global config
(`ABC`) is **already seeded** by the Credentials/State capability (the agent-files copy; the global `CLAUDE.md`
is not in `AgentFilesExclude`), so Context never reaches outside — it is a *purely internal* step: deliver
yoloAI's collected operating context (`DEF`) to where the agent reads it. **(2)** The survey
([research/agent-global-context.md](research/agent-global-context.md)) found four agents fit a single home-dir
markdown file, and **Aider is the structural outlier** (no auto-read global file).

So the Context capability is **the agent's `DEF`-injection *method*, declared as data** — two shapes:
- **append-to-context-file** (Claude/Gemini/Codex/OpenCode): append `DEF` at the agent's declared
  `StateDir`/`ContextFile` (resolve the *effective* path — footguns: Gemini's configurable `context.fileName`,
  Codex's `AGENTS.override.md` + `CODEX_HOME`, OpenCode's CLAUDE.md fallback).
- **launch-flag** (Aider): write `DEF` to a scratch file and inject `--read <file>` into the launch command
  (crosses into the Launch capability; robust vs mutating the last-wins `~/.aider.conf.yml`).

Both are data, so the capability *generalized* across the divergence without forcing code. The **fan-in** —
`DEF` is assembled from each concerned layer's fragment (file-exchange → Q&A, sandbox → orientation, netpolicy →
isolation-notice) — is the payload side; the agent owns only the injection method.

## Openness — file-defined agents

- **Open now: `~/.yoloai/agents/<name>.yaml`** (own files — an agent is a different noun than a profile, which
  *selects* an agent). A file inherently carries only the **data-expressible** subset. Covers the realistic
  want — *wrap a non-Claude tool as an agent* (e.g. a security-research harness agent). A user-defined agent is
  **trusted config** (same trust level as a profile/Dockerfile the user writes), not untrusted input — which
  bounds the security surface. Work to open: a **schema + loader + validator** (the capability model paid for
  the rest).
- **The code-adapter stays internal** — agents needing a procedural mechanism (Claude's settings-merge,
  opencode's validation) stay Go-defined. The public adapter interface is gated on a real *code-needing*
  third-party agent.
  - **Refinement (2026-06-25) — the in-sandbox exception.** "Procedural code stays Go-defined and internal"
    holds for *host-side* mechanisms. Some per-agent mechanisms run **in the sandbox**, not the host library —
    the prime case is **detection strategy** (the python status monitor). For those the natural extension point
    is a droppable per-agent **python module loaded by convention** ("the spine"), which a *file-defined* agent
    could carry — so in-sandbox detection logic is open in a way host-side Go adapters are not. The trust model
    permits it: in-sandbox detection is not a security boundary (the host treats `agent-status.json` as a hint;
    the agent already runs code as the same sandbox user). Seam reserved, not built — see
    [agent-detection.md](agent-detection.md) DD2 and [research/agent-callbacks.md](research/agent-callbacks.md)
    (the gating vendor-callback survey).
- Declarative-izing the shipped agents' `ApplySettings` (wanted anyway; re-homing already shrank it) makes the
  shipped agents mostly file-expressible too — dogfooding the schema.

## The public surface

1. **Separability is why the layer is opt-in.** A caller wanting only sandboxing imports `substrate`/`runtime`
   and never pulls in the agent machinery — guaranteed by the dependency direction `agent → lower`, never back.
   Root `yoloai` is batteries-included; `substrate` is the agent-free island.
2. **Type surface = a read-only capability catalog** (`yoloai.AgentTypes()` → `AgentInfo`), enriched from
   today's thin auth/model fields into the **public capability declaration**: one-shot/headless support,
   idle-mode, native-resume, prompt-mode, network floor. The internal `Definition` + code-adapter stay internal.
3. **File-defined agents are open** (Openness above).
4. **`sb.Agent()` is a join**, not the agent layer — identity (type/model) + prompt (`AgentLaunchSpec` read) +
   status (completion sidecar) + **`.IOSession()`** (the session channel, D88). The interaction primitives
   move to `IOSession` (D88); `Agent` slims to agent-specific reads + the join. It lives in the **root
   `yoloai`** composition.

## Implementation notes (spec-time / Shape-phase)

- **Findings to fix:** the Q&A-protocol injection must become **agent-agnostic** (today Claude-only,
  `context.go:177` — a `DEF` fragment delivered by each agent's method); **append, don't clobber** the seeded
  `ABC`; the injected block is the **weakest layer** (a closer project file wins — fine for defaults, not
  authoritative; not a containment concern).
- **`AGENTS.md` convergence:** Codex + OpenCode are native `AGENTS.md`, Gemini can be pointed at it — a sane
  default for a future agent.
- **The file-defined-agents work** is a schema + loader (`~/.yoloai/agents/*.yaml`) + a validator; the shipped
  agents are good dogfood for the schema once their `ApplySettings` is declarative-ized.
- **Shape the hook-registration object** (the agent→completion contract, D92): it carries the agent's
  *event-map* — which agent hook events map to the abstract turn-start/turn-stop signals (Claude: `Stop`→stop,
  `PreToolUse`+`UserPromptSubmit`→start) — plus the settings path-and-shape to append into. Completion supplies
  the status-writer *command*; a shared helper does the append. (Sketch it like `ProcSpec`/`EnvSpec`, not just
  named.)
- **The DEF-deliverer is envsetup** (D92) — the agent declares the injection *method* only; the agent capability
  table's "→ provision/envsetup" resolves to **envsetup** (which now also *assembles* `DEF` from the fan-in).

## Cross-references

- **Decisions:** [D84](../decisions/working-notes.md) (agent-free substrate), [D85](../decisions/working-notes.md)
  (agent→sidecar), [D86](../decisions/working-notes.md) (copyflow), [D88](../decisions/working-notes.md)
  (session/`IOSession`, the completion tiers + turn-cursor this layer feeds); this layer's own entry **D89**.
- **Findings:** the agent-aware half of [DF32](findings-unresolved.md) (no agent-free managed lifecycle);
  [DF13](findings-unresolved.md) (folder-trust); credential delivery is
  [DF38](findings-unresolved.md)/[DF39](findings-unresolved.md), out of scope here.
- **Research:** [agent-global-context.md](research/agent-global-context.md) (the global-context survey).
- **Consumer:** control-eval — its custom non-Claude harness agents are the plausible near-term consumer of
  file-defined agents.
