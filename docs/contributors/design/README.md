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
| Critiques | [unresolved-critiques.md](unresolved-critiques.md) | [resolved](resolved-critiques.md) · [deferred](deferred-critiques.md) · [abandoned](abandoned-critiques.md) |
| Questions | [unresolved-questions.md](unresolved-questions.md) | [resolved](resolved-questions.md) · [deferred](deferred-questions.md) · [abandoned](abandoned-questions.md) |
| Findings | [unresolved-findings.md](unresolved-findings.md) | [resolved](resolved-findings.md) · [deferred](deferred-findings.md) · [abandoned](abandoned-findings.md) |

## Sub-clusters

| Directory | What's here |
| --- | --- |
| [plans/](plans/README.md) | Designed-but-unimplemented features (backlog). Implemented plans move to [`../archive/plans/`](../archive/README.md). |
| [research/](research/README.md) | Research topics and spikes backing the design decisions. |
