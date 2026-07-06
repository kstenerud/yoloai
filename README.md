# yoloAI

**Sandboxed runner for AI coding agents. No more permission fatigue. Your files stay untouched until you say otherwise.**

AI coding agents want to edit your files and run commands, so you must choose between them constantly asking your permission, or bypassing permissions and risking a catastrophe.

Until now.

Let your agent live dangerously in a sandbox, then review the changes and decide what to keep.

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

https://github.com/user-attachments/assets/9d6740b4-a34e-4253-82ec-cb0e4c7a8bd9

## Why?

**Permission fatigue is real.** After a hundred approve/deny prompts you stop reading and just hit "yes" — or you reach for `--dangerously-skip-permissions` and hope for the best. Neither is great.

**Disabling permissions is dangerous! ... Unless you've sandboxed your agent, that is!**

yoloAI takes a different approach: let the agent do whatever it wants inside a disposable container. Your original files are never modified. When the agent is done, review the diff and choose what to keep.

- **Your files are untouchable.** The agent works on an isolated copy. Originals never change until you say so.
- **Git-powered review.** `diff` shows exactly what changed. `apply` patches your project cleanly, preserving individual commits.
- **No permission prompts.** The container is disposable — agents run with full access inside the sandbox.
- **Persistent agent state.** Session history and config survive stops and restarts.
- **Easy retry.** `yoloai reset` re-copies your original for a fresh attempt.

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

Each archive also ships shell completions (`completions/`), the `LICENSE`, and the changelog. Verify the download against `checksums.txt` (signed with cosign; every archive also carries GitHub build provenance — `gh attestation verify yoloai_… --repo kstenerud/yoloai`). Debian/RPM packages (`.deb`/`.rpm`) are attached to each release too.

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
git tag
# then git checkout your chosen tag
make build
sudo mv yoloai /usr/local/bin/  # or add to PATH
```

It's a single Go binary, with no runtime dependencies beyond your chosen backend. On first run, yoloAI builds its base image and creates `~/.yoloai/`.

## One-Shot workflow

### Non-Interactive

```bash
# Authenticate (yoloAI picks up existing credentials automatically)
export ANTHROPIC_API_KEY=sk-ant-...   # Claude Code
export GEMINI_API_KEY=...             # Gemini CLI
# Or just let it pick up your already authenticated session

# 1. Spin up a sandbox. Agent starts working immediately when you supply a prompt here
yoloai new fix-bug ./my-project --prompt "fix the failing tests"

# 2. See what the agent changed
yoloai diff fix-bug

# 3. Apply the good parts to your real project
yoloai apply fix-bug

# 4. Toss the container
yoloai destroy fix-bug
```

### Interactive

```bash
yoloai new exploration ./my-project -a
# You're inside the agent, running in tmux in the sandbox.
#   Ctrl-B, D to detach.
#   yoloai attach exploration to reconnect.
```

## Iterative workflow

For longer tasks, work in a commit-by-commit loop. Keep two terminals open — one for yoloAI, one for your normal shell.

```
┌─ YOLO shell ──────────────────────┬─ Outer shell ─────────────────────┐
│                                   │                                   │
│ yoloai new myproject . -a         │                                   │
│                                   │                                   │
│ # Tell the agent what to do,      │                                   │
│ # have it commit when done.       │                                   │
│                                   │ yoloai apply myproject            │
│                                   │ # Review and accept the commits.  │
│                                   │                                   │
│ # ... next task, next commit ...  │                                   │
│                                   │ yoloai apply myproject            │
│                                   │                                   │
│                                   │ # When you have a good set of     │
│                                   │ # commits, push:                  │
│                                   │ git push                          │
│                                   │                                   │
│                                   │ # Done? Tear it down:             │
│                                   │ yoloai destroy myproject          │
└───────────────────────────────────┴───────────────────────────────────┘
```

The agent works on an isolated copy, so you can keep iterating without risk. Each `apply` patches the real project with only the new commits since the last apply.

## Supported Infrastructure

### Sandbox Backends

| Backend    | Supported Hosts              | Dependencies                                                       |
|------------|------------------------------|--------------------------------------------------------------------|
| docker     | Linux, macOS, Windows (WSL2) | [Docker Engine](https://docs.docker.com/engine/install), [Docker Desktop](https://docs.docker.com/get-docker), or [OrbStack](https://orbstack.dev) |
| podman     | Linux, macOS                 | [Podman](https://podman.io/get-started) (`brew install podman` on macOS) |
| containerd | Linux                        | [Kata Containers](https://katacontainers.io) |
| apple      | macOS (Apple Silicon)        | [Apple Container](https://github.com/apple/container) |
| tart       | macOS (Apple Silicon)        | [Tart](https://github.com/cirruslabs/tart) (`brew install cirruslabs/cli/tart`) |
| seatbelt   | macOS (any)                  | None (uses built-in `sandbox-exec`)                                |

### Isolation Modes

Optionally upgrade the OCI runtime for stronger isolation:

| Mode | Description |
|------|-------------|
| `container` | Default `runc` — standard Linux namespaces and cgroups |
| `container-enhanced` | Userspace kernel (gVisor/runsc) — syscall interception, no KVM needed |
| `container-privileged` | All capabilities, seccomp/AppArmor unconfined — use for Docker-in-Docker and Compose |
| `vm` | Kata Containers (QEMU) — hardware VM isolation |
| `vm-enhanced` | Kata + Firecracker microVM — lightweight VM isolation |

```bash
# Use gVisor for all new sandboxes
yoloai config set isolation container-enhanced

# Or per sandbox
yoloai new task . --isolation container-enhanced
```

`vm` and `vm-enhanced` require Kata Containers to be installed.

### Agent Modes

| Mode       | Description |
|------------|-------------|
| `claude`   | Runs [Claude Code](https://github.com/anthropics/claude-code) via API key or subscription credentials (default) |
| `codex`    | Runs [Codex](https://github.com/openai/codex) via API key or subscription credentials |
| `gemini`   | Runs [Gemini](https://github.com/google-gemini) via API key or subscription credentials |
| `aider`    | Runs [Aider](https://github.com/Aider-AI/aider) (your config is copied in) |
| `opencode` | Runs [OpenCode](https://github.com/anomalyco/opencode) (your config is copied in) |
| `shell`    | Runs a tmux shell with all agents credentials seeded |
| `idle`     | Runs an idle process to allow MCP proxying |

Use `yoloai system agents` to list available agents.

## Learn more

- **[Usage Guide](docs/GUIDE.md)** — commands, flags, workdir modes, configuration, security
- **[Roadmap](docs/ROADMAP.md)** — upcoming features
- **[Architecture](docs/contributors/architecture/README.md)** — code navigation for contributors

Early access. Core workflow works, rough edges expected. [Feedback welcome.](https://github.com/kstenerud/yoloai/issues)

## License

[MIT](LICENSE)
