<!-- ABOUTME: Index/router for the design/ directory — the shaping cluster of feature specs, -->
<!-- ABOUTME: review queues, and the plans/ and research/ sub-clusters. Content lives in named files. -->

# Design

The **shaping cluster**: feature/design specs, the review queues, and the `plans/` and
`research/` sub-clusters. This README is an index — each document below owns its own content.

Start with **[overview.md](overview.md)** for the product/design-altitude picture (goal, value
proposition, competitive positioning, high-level architecture, directory layout, resolved design
decisions). For the code-architecture altitude, see [`../architecture/`](../architecture/README.md).

## Feature & design specs

| Document | What it covers |
| --- | --- |
| [overview.md](overview.md) | Goal, value proposition, competitive positioning, high-level architecture, directory layout, resolved design decisions. |
| [commands.md](commands.md) | Full command surface: flags, help output, directory syntax, agent definitions, command behaviors. |
| [config.md](config.md) | Configuration model: global vs profile config, keys, merge/inheritance, `IsGlobalKey` routing. |
| [setup.md](setup.md) | `yoloai system setup` flow and first-run provisioning. |
| [security.md](security.md) | Sandbox threat model, credential handling, capability tradeoffs. |
| [substrate-interface.md](substrate-interface.md) | The agent-free isolated-environment layer (`Backend`/`Substrate`/`Process`) for the public-layering program: liveness-only status, mechanism-not-policy, channels-emergent, principal-out. Decision [D84](../decisions/working-notes.md). |
| [copyflow-layer.md](copyflow-layer.md) | The copy/diff/apply review refinement: per-dir repo-aware handle, seeding-vs-propagation, `--all` as collection-never-merge, characterize-and-surface for nature mismatches, the hermetic-git security seal. Decision [D86](../decisions/working-notes.md). |
| [persistence-helper.md](persistence-helper.md) | Foundation persistence (library + tools): scoped versioned handles over one doc per ownership domain, the monotonic-version + append-only raw-JSON migration registry (balk, never auto-migrate), `flock` + atomic-rename concurrency, the library/tool single-source-of-truth boundary, daemon-optional. Decision [D87](../decisions/working-notes.md). |
| [session-layer.md](session-layer.md) | The durable I/O-channel refinement (the module-split's deferred C-full): the `IOSession` consumer of the substrate (attach/inject/capture/persist over `SessionKind {PTY, Stream}`), the `lifetime` axis + three completion tiers, the neutral-PID-1 carve (`ProvisionSpec`/`ProcSpec`/`AgentLaunchSpec`, closing DF31/DF33), tier-2 authoritative-hook idle, re-launch (persistent restart = fresh agent), and the inject/capture one-way trust valve. Decision [D88](../decisions/working-notes.md). |
| [agent-layer.md](agent-layer.md) | Per-agent adapters over the agent-free lower layers. Core principle: an agent declares the *mechanism*, the consuming layer supplies the *payload* (agent owns no cross-layer payload). Capability groups each owned by one consuming layer; re-homing is a three-way sort (agent keeps the declaration, payload→owner, generic runner→consuming layer) so the layer is thin; Context = a `DEF`-injection method (survey-backed); **file-defined agents open** (`~/.yoloai/agents/*.yaml`, data-only by construction); public surface = read-only capability catalog + the `sb.Agent()` join. Decision [D89](../decisions/working-notes.md). |
| [network-isolation.md](network-isolation.md) | Network isolation design: iptables/ipset, domain allowlisting, isolation modes. |
| [environments.md](environments.md) | Environment archetypes and detection (devcontainer / yoloAI project config). |
| [reconfigure.md](reconfigure.md) | Reconfiguring an existing sandbox. |
| [multi-agent.md](multi-agent.md) | Multi-agent design notes. |
| [bugreport.md](bugreport.md) | `yoloai sandbox bugreport` design. |
| [github-issues.md](github-issues.md) | GitHub-issues integration notes. |

## Review queues

Each topic has a four-file lifecycle: an active inbox (`unresolved-`) draining to one of three
sinks — `resolved-` (done), `deferred-` (parked with a **`Trigger:`**), or `abandoned-`
(dropped with a **`Why:`**). See the [project CLAUDE.md](../../../CLAUDE.md) "Doc conventions"
note for the full model.

| Topic | Active | Sinks |
| --- | --- | --- |
| Critiques | [critiques-unresolved.md](critiques-unresolved.md) | [resolved](critiques-resolved.md) · [deferred](critiques-deferred.md) · [abandoned](critiques-abandoned.md) |
| Questions | [questions-unresolved.md](questions-unresolved.md) | [resolved](questions-resolved.md) · [deferred](questions-deferred.md) · [abandoned](questions-abandoned.md) |
| Findings | [findings-unresolved.md](findings-unresolved.md) | [resolved](findings-resolved.md) · [deferred](findings-deferred.md) · [abandoned](findings-abandoned.md) |

## Sub-clusters

| Directory | What's here |
| --- | --- |
| [plans/](plans/README.md) | Designed-but-unimplemented features (backlog). Implemented plans move to [`../archive/plans/`](../archive/README.md). |
| [research/](research/README.md) | Research topics and spikes backing the design decisions. |
