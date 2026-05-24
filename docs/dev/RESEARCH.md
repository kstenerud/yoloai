# Research

Research documents supporting yoloAI's design decisions. Each file covers a broad topic area with verified facts, community sentiment, and design implications.

| File | Topics |
|------|--------|
| [Competitors](research/competitors.md) | Existing tools, community pain points, feature comparison |
| [Agents](research/agents.md) | AI coding CLI agents, shell mode, Aider local-model integration |
| [Security](research/security.md) | Credential management, network isolation, proxy sidecar, Claude Code proxy support |
| [Sandboxing](research/sandboxing.md) | Alternative filesystem isolation, macOS VM sandbox, macOS process/FS isolation |
| [Implementation](research/implementation.md) | Env var interpolation, Claude Code installation, Go libs vs shell commands, tmux defaults |
| [Parallel Agents](research/parallel-agents.md) | Multi-agent coordination, idle detection, spec-driven development, batch orchestration |
| [Orchestration](research/orchestration.md) | Agent idle/done detection mechanisms, orchestrator ecosystem, agent SDK interfaces, handoffs, user pain points |
| [Idle Detection](research/idle-detection.md) | Full audit of current idle detection code, external research on detection approaches, pluggable detector architecture |
| [macOS Idle Detection](research/macos-idle-detection.md) | macOS-specific process idle detection: sysctl KERN_PROC, libproc, DTrace, Mach APIs, network monitoring |
| [Agentic Workflows](research/agentic-workflows.md) | Community sentiment on autonomous agents, TDD subagent patterns, authority splitting, review gap, skill ecosystem |
| [Podman](research/podman.md) | Podman backend: SDK options, Docker-compat socket, rootless file ownership, overlay concerns, implementation scope, open questions |
| [Linux VM Backends](research/linux-vm-backends.md) | VM-level isolation beyond Docker: Firecracker, gVisor, Kata Containers, Cloud Hypervisor, Lima, Sysbox; comparison matrix and yoloAI recommendation |
| [SSH Backend](research/ssh-backend.md) | **Deferred 2026-05.** Design for a remote SSH backend (connection model, provisioning, file sync, workdir modes, secrets, diff/apply). Value prop eroded by VS Code Remote Tunnels + `DOCKER_HOST=ssh://`. Interface-rename ideas lifted into layering W-L11. |
| [Runtime interface spike](research/runtime-interface-spike.md) | W11 spike — catalogs `Runtime` interface methods across 5 backends, classifies static-vs-dynamic, recommends BackendDescriptor + ≤14-method core + optional interfaces. |
| [Base image candidates](research/base-image-candidates.md) | **Parked 2026-05-24.** Survey of pre-built bases (devcontainers/universal, gitpod/workspace-full, buildpack-deps, …) as replacements for `debian:bookworm-slim` under `yoloai-base`. Gotchas: Docker-in-Docker, `yoloai` uid 1001, AI-agent layer doesn't compress. Tart/Seatbelt not applicable. Recommendation: defer; only honest win is `buildpack-deps:bookworm` and it's not worth the churn. |
