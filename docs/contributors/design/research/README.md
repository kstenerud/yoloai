# Research

Research documents supporting yoloAI's design decisions. Each file covers a broad topic area with verified facts, community sentiment, and design implications.

| File | Topics |
|------|--------|
| [Competitors](competitors.md) | Existing tools, community pain points, feature comparison |
| [Agents](agents.md) | AI coding CLI agents, shell mode, Aider local-model integration |
| [Security](security.md) | Credential management, network isolation, proxy sidecar, Claude Code proxy support |
| [Sandboxing](sandboxing.md) | Alternative filesystem isolation, macOS VM sandbox, macOS process/FS isolation |
| [Implementation](implementation.md) | Env var interpolation, Claude Code installation, Go libs vs shell commands, tmux defaults |
| [Parallel Agents](parallel-agents.md) | Field case study (2025-06): manual tmux parallelism, spec-driven development, **sandbox chaining / pipelines**. Defers idle-detection depth to Orchestration |
| [Orchestration](orchestration.md) | **External** signals + ecosystem: how each agent emits completion/idle (hooks/bell/SDK events), the orchestrator-tool landscape, git-worktree patterns, handoffs, user pain points |
| [Idle Detection](idle-detection.md) | **yoloAI's own** idle/done detection — audit of current code + pluggable detector architecture (the canonical internal-design doc) |
| [macOS Idle Detection](macos-idle-detection.md) | Platform deep-dive *supporting* idle-detection.md: macOS process-idle techniques (sysctl KERN_PROC, libproc, DTrace, Mach APIs, network monitoring) |
| [Agentic Workflows](agentic-workflows.md) | Community sentiment on autonomous agents, TDD subagent patterns, authority splitting, review gap, skill ecosystem |
| [Podman](podman.md) | Podman backend: SDK options, Docker-compat socket, rootless file ownership, overlay concerns, implementation scope, open questions |
| [Linux VM Backends](linux-vm-backends.md) | VM-level isolation beyond Docker: Firecracker, gVisor, Kata Containers, Cloud Hypervisor, Lima, Sysbox; comparison matrix and yoloAI recommendation |
| [SSH Backend](ssh-backend.md) | **Deferred 2026-05.** Design for a remote SSH backend (connection model, provisioning, file sync, workdir modes, secrets, diff/apply). Value prop eroded by VS Code Remote Tunnels + `DOCKER_HOST=ssh://`. Interface-rename ideas lifted into layering W-L11. |
| [Runtime interface spike](runtime-interface-spike.md) | W11 spike — catalogs `Runtime` interface methods across 5 backends, classifies static-vs-dynamic, recommends BackendDescriptor + ≤14-method core + optional interfaces. |
| [Base image candidates](base-image-candidates.md) | **Parked 2026-05-24.** Survey of pre-built bases (devcontainers/universal, gitpod/workspace-full, buildpack-deps, …) as replacements for `debian:bookworm-slim` under `yoloai-base`. Gotchas: Docker-in-Docker, `yoloai` uid 1001, AI-agent layer doesn't compress. Tart/Seatbelt not applicable. Recommendation: defer; only honest win is `buildpack-deps:bookworm` and it's not worth the churn. |
