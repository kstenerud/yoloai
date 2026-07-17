> **ABOUTME:** Index for the design/ directory — the shaping cluster of feature specs, review
> queues, and their supporting sub-clusters. Each document listed below owns its own content;
> this file only routes to it.

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
| [backend-topology.md](backend-topology.md) | **Verified reference (D92).** Where PID 1 / the agent / the monitor actually run on all six backends. The unifying fact: all six launch the *same* `sandbox-setup.py`, so the carve is uniform — only *locality* (container / VM-guest / host) and launch mechanism differ. Corrects "seatbelt/tart run on the host" (only seatbelt is a host process; tart/containerd/apple are VM guests). Grounds the carve's per-backend treatment. |
| [envsetup.md](envsetup.md) | The inside-the-sandbox environment preparer — the dual of the substrate (substrate provisions the agent-free *shell*; envsetup provisions its agent-specific *contents*: credentials, seed files, settings patches, the `DEF` context, the resolved env). Host-side staging (D88); consumes an agnostic `EnvSpec` the agent layer compiles (separability). The **security home** where credentials cross into the sandbox — DF38 (secure delivery) + DF39 (`$HOME` bleed) live here, baseline now + secure-secrets seam deferred. Decision [D91](../decisions/working-notes.md). |
| [netpolicy.md](netpolicy.md) | The network-policy refinement over the substrate (resolves DF34). Carve: substrate ← network *provisioning* (CNI netns/bridge/IP), netpolicy ← *policy + enforcement*. Domain-centric policy (`mode × agent-floor × user allow/deny`); enforcement is a **pluggable strategy axis** — `ip-filter` (in-sandbox iptables, best-effort IP-approx, *not* adversarial) now, `egress-proxy` (out-of-agent-control L7, hostile-grade) as a committed post-revamp feature; the structural room is that **the enforcement point is not assumed in-sandbox**. Capability per-(backend × strategy). Decision [D90](../decisions/working-notes.md). |
| [secure-secrets.md](secure-secrets.md) | The credential boundary (DF38 design). A host-side, out-of-agent-reach **egress proxy** holds the real credentials, injects them as headers on allowlisted egress (TLS-MITM with a yoloai CA the agent trusts), and **refreshes** short-lived tokens transparently — so the live credential never enters the sandbox and the agent never holds a token it can exfiltrate. The **credential half of netpolicy's reserved `egress-proxy` strategy** (one boundary for creds + network, on a different principal/namespace). Refresh is load-bearing (env delivery can't refresh → short-lived-token + long-session *requires* the proxy). Extends the D63 caller-`Env` contract with a refresh-capable `CredentialSource`. Resolves DF38 + DF39; build phased. Decision [D95](../decisions/working-notes.md). |
| [agent-layer.md](agent-layer.md) | Per-agent adapters over the agent-free lower layers. Core principle: an agent declares the *mechanism*, the consuming layer supplies the *payload* (agent owns no cross-layer payload). Capability groups each owned by one consuming layer; re-homing is a three-way sort (agent keeps the declaration, payload→owner, generic runner→consuming layer) so the layer is thin; Context = a `DEF`-injection method (survey-backed); **file-defined agents open** (`~/.yoloai/agents/*.yaml`, data-only by construction); public surface = read-only capability catalog + the `sb.Agent()` join. Decision [D89](../decisions/working-notes.md). |
| [agent-detection.md](agent-detection.md) | **Build contracts (D96)** for agent-owned detection + fall-to-shell + resume — the concrete shapes behind D88's fall-to-shell/tier-2 and D89's Resume capability. The launch wrapper authoritatively writes `done` (reusing the fenced v1 schema) and falls the pane to a usable shell; a compiled `DetectionSpec` (mirroring `EnvSpec`) carries the mode selector + `ResumeCmd`; `yoloai-resume` re-launches + re-detects in one command; gated rollout, hook-authoritative first. Phase 0 of [plans/agent-owned-detection.md](../archive/plans/agent-owned-detection.md). |
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
| [plans/](plans/README.md) | Unfinished work — one file per idea, from `UNSPECIFIED` (no design yet) through `PLANNED` to `IN-PROGRESS`. `ls` it for the complete list; each file's Status line says where it stands. Once `IMPLEMENTED` or `ABANDONED` a plan is archaeology and moves whole to [`../archive/plans/`](../archive/README.md) — gated, not remembered. |
| [research/](research/README.md) | Research topics and spikes backing the design decisions. |
