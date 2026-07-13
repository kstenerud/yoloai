# yoloAI

**Sandboxed runner for AI coding agents. No permission fatigue, no credentials in the box, no changes to your project until you approve them.**

[![CI](https://github.com/kstenerud/yoloai/actions/workflows/ci.yml/badge.svg)](https://github.com/kstenerud/yoloai/actions/workflows/ci.yml)
[![Nightly Audit](https://github.com/kstenerud/yoloai/actions/workflows/audit.yml/badge.svg)](https://github.com/kstenerud/yoloai/actions/workflows/audit.yml)
[![Release](https://img.shields.io/github/v/release/kstenerud/yoloai)](https://github.com/kstenerud/yoloai/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/kstenerud/yoloai.svg)](https://pkg.go.dev/github.com/kstenerud/yoloai)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

AI coding agents work best with the guardrails off, and that's a terrible way to run them on your real machine. yoloAI gives the agent a disposable sandbox where it can edit anything and run anything, unattended. Your project, your credentials, and your network stay under your control. When the agent is done, review the diff and apply what you want to keep.

```text
You                          Sandbox                        Your project
 │                              │                                │
 ├─ yoloai new fix-bug .        ├─ sandbox copy of project       │
 │                              │                                │
 ├─ << your prompt(s) >>        ├─ agent works freely            │
 │                              │  (no permission prompts)       │
 │                              │                                │
 ├─ yoloai diff fix-bug         ├─ shows what changed            │
 │                              │                                │
 ├─ yoloai apply fix-bug        │                                ├─ patches applied
 │  (you choose which ones)     │                                │
 │                              │                                │
 ├─ yoloai destroy fix-bug      ├─ destroys sandbox              │
```

## Why?

Permission prompts exist because agents make mistakes. After the hundredth approve/deny you stop reading them, and `--dangerously-skip-permissions` is one confused agent away from a very bad day. yoloAI shrinks the blast radius until the prompts are unnecessary:

- **Your files are safe.** The agent works on an isolated copy of your project. `diff` shows exactly what changed, `apply` patches your real project while preserving individual commits, and your originals never change until you apply.
- **Your secrets are safe.** The sandbox starts from a minimal, locally built environment; host environment variables stay on the host. Credentials arrive as read-only file mounts, never environment variables. Where credential brokering applies (Claude today, on by default), the API key stays host-side entirely: a local proxy injects it on the way to the provider, so even a fully compromised agent has nothing to exfiltrate.
- **Your network is yours.** `--network-isolated` restricts egress to the agent's API endpoints plus domains you allow. `--network-none` removes the network entirely.
- **Your machine is isolated.** Pick your comfort level, from Linux namespaces through gVisor to hardware VMs.

See [Security](docs/GUIDE.md#security) for the full model, including honest limitations.

## Install

### Prebuilt binary (recommended)

Download the archive for your platform from the [latest release](https://github.com/kstenerud/yoloai/releases/latest), extract the `yoloai` binary, and put it on your `PATH`:

```bash
# Linux x86-64 (swap in linux_arm64 / darwin_amd64 / darwin_arm64 as needed)
VERSION=0.7.0
curl -fsSL "https://github.com/kstenerud/yoloai/releases/download/v${VERSION}/yoloai_${VERSION}_linux_amd64.tar.gz" \
  | tar -xz yoloai
sudo install yoloai /usr/local/bin/
```

Each archive also ships shell completions, the `LICENSE`, and the changelog. Releases are signed with cosign (`checksums.txt`) and carry GitHub build provenance (`gh attestation verify yoloai_… --repo kstenerud/yoloai`). Debian/RPM packages are attached to each release too.

### Homebrew (macOS / Linux)

```bash
brew install --cask kstenerud/tap/yoloai
```

### Using `go install`

```bash
# Latest release
go install github.com/kstenerud/yoloai/cmd/yoloai@latest

# Latest development version (unstable)
go install github.com/kstenerud/yoloai/cmd/yoloai@main
```

Requires Go 1.26+. The binary is placed in `$GOPATH/bin` (typically `~/go/bin`).

### From source

```bash
git clone https://github.com/kstenerud/yoloai.git
cd yoloai
git checkout v0.7.0   # or stay on main for the latest development version
make build
sudo install yoloai /usr/local/bin/
```

It's a single Go binary with no runtime dependencies beyond your chosen backend. On first run, yoloAI builds its base image and creates `~/.yoloai/` (or whichever directory you point `--data-dir` to).

## Quick start

### Non-interactive

```bash
# Authenticate (yoloAI picks up existing credentials automatically)
export ANTHROPIC_API_KEY=sk-ant-...   # Claude Code
export GEMINI_API_KEY=...             # Gemini CLI
# Or just let it pick up your already authenticated session

# 1. Spin up a sandbox. The agent starts working immediately when you supply a prompt
yoloai new fix-bug ./my-project --prompt "fix the failing tests"

# 2. See what the agent changed
yoloai diff fix-bug

# 3. Apply the good parts to your real project
yoloai apply fix-bug

# 4. Toss the sandbox
yoloai destroy fix-bug
```

### Interactive

```bash
yoloai new exploration ./my-project -a
# You're inside the agent, running in tmux in the sandbox.
#   Ctrl-B, D to detach.
#   yoloai attach exploration to reconnect.
```

### Iterating

For longer sessions, work in a loop: tell the agent to commit as it goes, and run `yoloai apply` from another terminal whenever you want to pull the finished commits into your real project. Each apply brings over only the new commits since the last one. When you're happy with the result, push as usual and destroy the sandbox. See the [Usage Guide](docs/GUIDE.md) for the full workflow.

## Demo

Creating a sandbox, prompting the agent, and applying the results:

https://github.com/user-attachments/assets/9d6740b4-a34e-4253-82ec-cb0e4c7a8bd9

## Features

**Sandboxing**

- Six backends: Docker, Podman, containerd (Kata), Apple Container, Tart, and Seatbelt. Runs on Linux, macOS, and Windows (WSL2).
- Selectable isolation strength per sandbox, from runc through gVisor up to Kata VMs (QEMU or Firecracker).
- Network policy per sandbox: open, allowlist, or none.
- Minimal environment inside the sandbox. Anything from the host is an explicit opt-in (`--env`, `--dir`).
- Resource limits (`--cpus`, `--memory`) and port forwarding (`--port`).
- Cheap workdir copies: whole-tree clones on macOS (APFS `clonefile`), per-file reflinks on Linux filesystems that support them (btrfs, XFS). Filesystems without reflink (ext4) get a regular copy.

**Credentials**

- Picks up your existing agent logins automatically: API keys, subscription credentials, macOS Keychain.
- Credential brokering keeps the API key host-side (Claude today); other credentials are delivered as read-only file mounts.

**Workflow**

- Copy/diff/apply with git running sandbox-side, so repos with filters and hooks behave correctly.
- Apply your way: replay commits (default), squash to a single patch, export `.patch` files, pick commits by ref, or `--dry-run` first.
- Full lifecycle: create, attach, stop, restart, wait, clone, reset, destroy. Agent state survives stops and restarts.
- Headless one-shots for scripts and CI: `yoloai run --prompt ... --rm`, with `--json` output on every command.
- When the agent exits, its tmux pane falls to a shell so you can inspect the sandbox.

**Integration**

- Claude Code, Codex, Gemini CLI, Aider, and OpenCode built in, plus a `shell` mode for anything else.
- VS Code: attach to the container, or open a Remote Tunnel from inside the sandbox (`--vscode-tunnel`).
- MCP in both directions: `yoloai mcp serve` lets an outer agent drive sandboxes as tools; `yoloai mcp proxy` runs MCP servers inside a sandbox.
- Profiles: per-project images and defaults (Dockerfile + config, with inheritance).
- Extensions: add your own subcommands as YAML-wrapped shell scripts (`yoloai x`).
- Embeddable: the CLI is a thin layer over a public [Go API](https://pkg.go.dev/github.com/kstenerud/yoloai).
- Single static binary. State lives in `~/.yoloai/` (relocatable with `--data-dir`).

## Supported infrastructure

### Sandbox backends

| Backend    | Supported Hosts              | Dependencies                                                       |
|------------|------------------------------|--------------------------------------------------------------------|
| docker     | Linux, macOS, Windows (WSL2) | [Docker Engine](https://docs.docker.com/engine/install), [Docker Desktop](https://docs.docker.com/get-docker), or [OrbStack](https://orbstack.dev) |
| podman     | Linux, macOS                 | [Podman](https://podman.io/get-started) (`brew install podman` on macOS) |
| containerd | Linux                        | [Kata Containers](https://katacontainers.io) |
| apple      | macOS (Apple Silicon)        | [Apple Container](https://github.com/apple/container) |
| tart       | macOS (Apple Silicon)        | [Tart](https://github.com/cirruslabs/tart) (`brew install cirruslabs/cli/tart`) |
| seatbelt   | macOS (any)                  | None (uses built-in `sandbox-exec`)                                |

**Note**: Tart provides a full macOS VM, enabling you to run simulators within the sandbox.

### Isolation modes

Optionally upgrade the OCI runtime for stronger isolation. gVisor modes are available on docker and podman; the VM modes come with the containerd backend.

| Mode | Description |
|------|-------------|
| `container` | Default `runc`: standard Linux namespaces and cgroups |
| `container-enhanced` | Userspace kernel (gVisor/runsc): syscall interception, no KVM needed |
| `container-privileged` | All capabilities, seccomp/AppArmor unconfined. Use for Docker-in-Docker and Compose |
| `vm` | Kata Containers (QEMU): hardware VM isolation |
| `vm-enhanced` | Kata + Firecracker microVM: lightweight VM isolation |

```bash
# Use gVisor for all new sandboxes
yoloai config set isolation container-enhanced

# Or per sandbox
yoloai new task . --isolation container-enhanced
```

### Agents

| Mode       | Description |
|------------|-------------|
| `claude`   | Runs [Claude Code](https://github.com/anthropics/claude-code) via API key or subscription credentials (default) |
| `codex`    | Runs [Codex](https://github.com/openai/codex) via API key or subscription credentials |
| `gemini`   | Runs [Gemini CLI](https://github.com/google-gemini/gemini-cli) via API key or subscription credentials |
| `aider`    | Runs [Aider](https://github.com/Aider-AI/aider) (your config is copied in) |
| `opencode` | Runs [OpenCode](https://github.com/anomalyco/opencode) (your config is copied in) |
| `shell`    | Runs a tmux shell with all agent credentials seeded |
| `idle`     | Runs an idle process to allow MCP proxying |

Use `yoloai system agents` to list available agents.

## Learn more

- **[Usage Guide](docs/GUIDE.md)**: commands, flags, workdir modes, configuration, security
- **[Roadmap](docs/ROADMAP.md)**: upcoming features
- **[Architecture](docs/contributors/architecture/README.md)**: code navigation for contributors

## Status

Public beta. The core workflow is stable and exercised daily; interfaces may still change between 0.x releases, and every breaking change is documented in [BREAKING-CHANGES](docs/BREAKING-CHANGES.md). [Feedback welcome.](https://github.com/kstenerud/yoloai/issues)

## License

[MIT](LICENSE)
