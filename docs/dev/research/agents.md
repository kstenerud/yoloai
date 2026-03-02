# AI Coding Agents

## AI Coding CLI Agents: Multi-Agent Support Research

This section documents the headless/Docker characteristics of major AI coding CLI agents. Claude Code and Codex are supported in v1; additional agents are researched for future versions.

**Resolved research gaps (v1):**
- **Codex proxy support:** Not supported. The static Rust binary does not honor `HTTP_PROXY`/`HTTPS_PROXY`. Open upstream issues: github.com/openai/codex#4242, github.com/openai/codex#6060.
- **Codex required network domains:** Confirmed `api.openai.com` only. Telemetry uses user-configured OTLP endpoints, not hardcoded domains.
- **Codex TUI behavior in tmux:** Confirmed working. Interactive mode runs correctly in tmux.

### Viable Agents

#### Claude Code

- **Install:** `npm i -g @anthropic-ai/claude-code`
- **Headless command:** `claude --dangerously-skip-permissions -p "task"`
- **API key env vars:** `ANTHROPIC_API_KEY`
- **State dir:** `~/.claude/`
- **Model selection:** `--model <model>`
- **Sandbox bypass:** `--dangerously-skip-permissions`
- **Runtime:** Node.js
- **Root restriction:** Refuses to run as root
- **Docker quirks:** Requires tmux with double-Enter workaround to submit prompts; needs non-root user

#### OpenAI Codex

- **Install:** Static binary download or `npm i -g @openai/codex`
- **Headless command:** `codex exec --yolo "task"`
- **API key env vars:** `CODEX_API_KEY` (preferred), `OPENAI_API_KEY` (fallback)
- **State dir:** `~/.codex/`
- **Model selection:** `--model <model>` or `-m <model>` (e.g., `gpt-5.3-codex`, `gpt-5.3-codex-spark`, `codex-mini-latest`)
- **Model aliases:** `default` → `gpt-5.3-codex`, `spark` → `gpt-5.3-codex-spark`, `mini` → `codex-mini-latest`
- **Sandbox bypass:** `--yolo` (alias `--dangerously-bypass-approvals-and-sandbox`)
- **Runtime:** Rust (statically-linked musl binary, zero runtime deps)
- **Proxy support:** Not supported — does not honor `HTTP_PROXY`/`HTTPS_PROXY` (upstream issues github.com/openai/codex#4242, #6060)
- **Root restriction:** None found, but convention is non-root
- **Docker quirks:** `codex exec` avoids TUI entirely — no tmux needed; `--skip-git-repo-check` useful outside repos; Landlock sandbox may fail in containers (use `--yolo`); TUI works correctly in tmux
- **Sources:** [CLI reference](https://developers.openai.com/codex/cli/reference/), [Security docs](https://developers.openai.com/codex/security/)

#### Google Gemini CLI

- **Install:** `npm i -g @google/gemini-cli` → binary: `gemini`
- **Headless command:** `gemini -p "task" --yolo`
- **Interactive with prompt:** `gemini -i "task"` (starts interactive session with initial prompt)
- **API key env vars:** `GEMINI_API_KEY`
- **State dir:** `~/.gemini/` (contains `settings.json`)
- **Model selection:** `--model <model>` or `-m <model>` (e.g., `gemini-2.5-pro`, `gemini-2.5-flash`)
- **Sandbox bypass:** `--yolo` auto-approves all tool calls; sandbox is disabled by default
- **Runtime:** Node.js 20+
- **Root restriction:** None found
- **Auth alternatives:** OAuth login via browser flow (caches credentials locally); API key is the primary supported path
- **Network domains:** `generativelanguage.googleapis.com` (API), `cloudcode-pa.googleapis.com` (OAuth)
- **Docker quirks:** Ink-based TUI; ready pattern needs empirical testing with `tmux capture-pane`. The `>` prompt character may cause false positives with grep.
- **Sources:** [GitHub repo](https://github.com/GoogleCloudPlatform/gemini-cli), [npm package](https://www.npmjs.com/package/@google/gemini-cli)

#### Aider

- **Install:** `pip install aider-chat` (Python 3.9–3.12) or official Docker images (`paulgauthier/aider`)
- **Headless command:** `aider --message "task" --yes-always --no-pretty`
- **API key env vars:** `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `DEEPSEEK_API_KEY`, `GEMINI_API_KEY`, + many more via LiteLLM
- **State dir:** Dotfiles in project dir (`.aider.conf.yml`, `.aider.chat.history.md`, `.aider.tags.cache.*`, etc.)
- **Model selection:** `--model <model>` with LiteLLM identifiers; shortcut flags: `--opus`, `--sonnet`, `--4o`, `--deepseek`
- **Sandbox bypass:** `--yes-always` (but does NOT auto-approve shell commands — known issue #3903)
- **Runtime:** Python 3.9–3.12 (does not support 3.13+)
- **Root restriction:** None (official Docker image runs as UID 1000 `appuser`)
- **Docker quirks:** Official image sets `HOME=/app` so state files persist on mounted volume; no global git config — must set `user.name`/`user.email` in repo local config; auto-commits by default (disable with `--no-auto-commits`)
- **Sources:** [Scripting docs](https://aider.chat/docs/scripting.html), [Docker docs](https://aider.chat/docs/install/docker.html)

#### Goose (Block)

- **Install:** Install script (`curl ... | CONFIGURE=false bash`) or `brew install block-goose-cli`
- **Headless command:** `goose run -t "task"` (or `-i file.md`, or `--recipe recipe.yaml`)
- **API key env vars:** `GOOSE_PROVIDER` + provider-specific keys (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GOOGLE_API_KEY`, etc. — 25+ providers)
- **State dir:** `~/.config/goose/` (Linux); `~/Library/Application Support/Block/goose/` (macOS); overridable via `GOOSE_PATH_ROOT`
- **Model selection:** `--provider <provider> --model <model>` or env vars `GOOSE_PROVIDER`/`GOOSE_MODEL`
- **Sandbox bypass:** `GOOSE_MODE=auto` env var
- **Runtime:** Rust binary (precompiled); Node.js needed for MCP extensions
- **Root restriction:** None (official Docker example runs as root)
- **Docker quirks:** Keyring does not work in Docker — must set `GOOSE_DISABLE_KEYRING=1`; recommended headless env vars: `GOOSE_MODE=auto`, `GOOSE_CONTEXT_STRATEGY=summarize`, `GOOSE_MAX_TURNS=50`, `GOOSE_DISABLE_SESSION_NAMING=true`
- **Sources:** [Environment variables](https://block.github.io/goose/docs/guides/environment-variables/), [Headless tutorial](https://block.github.io/goose/docs/tutorials/headless-goose/)

#### Cline CLI

- **Install:** `npm i -g cline`
- **Headless command:** `cline -y "task"`
- **API key env vars:** `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, or other provider keys
- **State dir:** `~/.cline/` (overridable via `CLINE_DATA_HOME`)
- **Model selection:** `-m <model>` flag
- **Sandbox bypass:** `-y` flag (full autonomy)
- **Runtime:** Node.js 20+
- **Root restriction:** None found
- **Docker quirks:** Node.js only, no Docker image needed
- **Security warning:** Cline CLI 2.3.0 suffered a supply chain attack (Feb 2026) — stolen npm token injected malicious code affecting ~4,000 downloads. Pin versions carefully.
- **Sources:** [CLI reference](https://docs.cline.bot/cline-cli/cli-reference), [Supply chain attack report](https://thehackernews.com/2026/02/cline-cli-230-supply-chain-attack.html)

#### Continue CLI

- **Install:** `npm i -g @continuedev/cli`
- **Headless command:** `cn -p "task"`
- **API key env vars:** `CONTINUE_API_KEY` + provider-specific keys
- **State dir:** `~/.continue/`
- **Model selection:** `--config <path-or-name>` for config-based model selection
- **Sandbox bypass:** `--allow "Write()" --ask "Bash(curl*)"` granular permissions
- **Runtime:** Node.js 20+
- **Root restriction:** None found
- **Docker quirks:** Designed for CI/CD; auto-detects non-TTY and runs headless
- **Sources:** [CLI docs](https://docs.continue.dev/guides/cli), [CLI quickstart](https://docs.continue.dev/cli/quickstart)

#### Amp (Sourcegraph)

- **Install:** `npm i -g @sourcegraph/amp` or install script
- **Headless command:** `amp -x "task"` (auto-enabled when stdout is redirected)
- **API key env vars:** `AMP_API_KEY` (requires Sourcegraph account)
- **State dir:** `~/.config/amp/`
- **Model selection:** None — Amp auto-selects models
- **Sandbox bypass:** `--dangerously-allow-all`
- **Runtime:** Node.js
- **Root restriction:** None found
- **Docker quirks:** Headless mode uses paid credits only; requires network access to Sourcegraph API
- **Sources:** [Amp manual](https://ampcode.com/manual), [Amp -x docs](https://ampcode.com/news/amp-x)

#### OpenHands

- **Install:** Install script or `uv tool install openhands --python 3.12`
- **Headless command:** `openhands --headless -t "task"` (or `-f file.md`)
- **API key env vars:** `LLM_API_KEY`, `LLM_MODEL` (requires `--override-with-envs` flag)
- **State dir:** `~/.openhands/`
- **Model selection:** `LLM_MODEL` env var (LiteLLM format: `openai/gpt-4o`, `anthropic/claude-sonnet-4-20250514`)
- **Sandbox bypass:** Implicit in headless mode
- **Runtime:** Python 3.12+ (CLI); Docker (server mode)
- **Root restriction:** Permission issues if `~/.openhands` is root-owned
- **Docker quirks:** CLI mode needs no Docker; server mode requires Docker + has `host.docker.internal` resolution issues in DinD; sandbox startup ~15 seconds; 4GB RAM minimum
- **Sources:** [CLI installation](https://docs.openhands.dev/openhands/usage/cli/installation), [Troubleshooting](https://docs.openhands.dev/openhands/usage/troubleshooting/troubleshooting)

#### SWE-agent

- **Install:** `pip install -e .` from source + Docker for sandboxing
- **Headless command:** `sweagent run --agent.model.name=<model> --problem_statement.text="task"`
- **API key env vars:** `keys.cfg` file with `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, etc.
- **State dir:** `trajectories/` output directory
- **Model selection:** `--agent.model.name=<provider/model>` (LiteLLM format)
- **Sandbox bypass:** N/A (always headless, always sandboxed via Docker)
- **Runtime:** Python 3.10+ and Docker (required)
- **Root restriction:** N/A
- **Docker quirks:** Docker required for sandboxed execution; Docker-in-Docker fails (SWE-ReX hardcodes localhost); image builds can be brittle per-repo
- **Sources:** [SWE-agent GitHub](https://github.com/SWE-agent/SWE-agent), [Models and keys](https://swe-agent.com/latest/installation/keys/)

### Not Viable

| Agent | Reason |
|-------|--------|
| **Mentat** | Transitional (old Python CLI archived Jan 2025, new npm CLI at 0.1.0). No documented headless mode. |
| **GPT-Engineer** | Abandoned; team pivoted to Lovable (commercial SaaS). |
| **GPT-Pilot** | No headless mode (interactive by design). Docker setup broken (missing docker-compose.yml). |

### Cross-Agent Patterns

**What varies:**
1. Binary name and install method — no two agents are alike
2. Headless flag — `-p`, `exec --yolo`, `--message`, `run -t`, `-y`, `-p`, `-x`, `--headless -t`
3. API key env vars — each agent has its own naming convention
4. State/config directory — all different locations
5. Model selection — `--model`, `-m`, `--provider`+`--model`, auto-select, config-based
6. Sandbox bypass — `--dangerously-skip-permissions`, `--yolo`, `--yes-always`, `GOOSE_MODE=auto`, `-y`, `--dangerously-allow-all`
7. Runtime — Rust (static binary), Python (various versions), Node.js

**What's consistent:**
1. All accept a text prompt (positional arg, flag, or stdin)
2. All use environment variables for API keys
3. All have some concept of "auto-approve everything" for autonomous mode
4. All produce file changes in the working directory
5. None require a GUI
6. All work with git repos (some require it, some recommend it)

---

## Shell Mode: Pre-Installed Agents with Interactive Shell

**Idea:** Instead of launching a specific agent, yoloai could offer a mode that drops the user into a tmux shell inside the sandbox with all supported agents pre-installed and ready to use. The user picks which agent to run (or switches between them) interactively.

### Prior Art: pixels

**Repo:** [deevus/pixels](https://github.com/deevus/pixels)
**Language:** Go | **Status:** Active

**What it does:** Go CLI that provisions disposable Linux containers on TrueNAS SCALE via Incus, pre-loaded with multiple AI coding agents. Uses [mise](https://mise.jdx.dev/) (polyglot tool version manager) to install Claude Code, Codex, and OpenCode asynchronously via a background systemd service (`pixels-devtools`).

**Architecture:**
- Control plane: Go CLI communicating with TrueNAS via WebSocket API
- Compute: Incus containers (not Docker)
- Storage: ZFS datasets with snapshot-based checkpointing
- Networking: nftables rules for egress filtering (three modes: unrestricted, agent allowlist, custom allowlist)
- Access: SSH-based console (`pixels console`) and exec (`pixels exec`)

**Key workflow — checkpoint and clone:**
```
pixels create base --egress agent --console
pixels checkpoint create base --label ready
pixels create task1 --from base:ready
pixels create task2 --from base:ready
```

This creates a base container, saves a ZFS snapshot, then clones identical copies for parallel tasks. The checkpoint/clone pattern avoids re-provisioning.

**Agent provisioning pattern:**
- mise installs Node.js LTS, then Claude Code, Codex, OpenCode as npm/binary packages
- Installation runs asynchronously after container creation via systemd service
- Progress visible via `pixels exec mybox -- sudo journalctl -fu pixels-devtools`
- Provisioning can be skipped entirely (`--no-provision`) or dev tools skipped via config

**What's relevant to yoloai:**
1. **Multi-agent provisioning via mise** — mise handles Node.js, Python, Go, Rust, and arbitrary tools with a single `.mise.toml` config. This could replace yoloai's per-agent Dockerfile install logic with a unified tool manager.
2. **Shell-first workflow** — pixels doesn't auto-launch an agent. It drops you into a shell (via SSH) where all agents are available. You choose what to run.
3. **Blueprint for agent support** — pixels supports Claude Code, Codex, and OpenCode with a simple provisioning model. Its agent install patterns (npm globals, binary downloads) could inform yoloai's agent definitions.
4. **Checkpoint/clone for parallel work** — ZFS snapshots enable instant cloning. Analogous to yoloai's overlayfs plans but at the storage layer.

### How This Could Work in yoloai

**Minimal change:** A new `--shell` flag on `yoloai new` (or a `yoloai shell` command) that:
1. Creates a sandbox with all agents pre-installed (Claude Code, Codex, Aider, etc.)
2. Drops into tmux inside the container — no agent auto-launched
3. User runs agents manually, switches between them, uses standard CLI tools

**What changes vs. current design:**
- Current: one agent per sandbox, auto-launched in tmux
- Shell mode: all agents installed, user-driven, tmux session is a plain shell

**Provisioning approach options:**
1. **Fat base image** — pre-bake all agents into the Docker image. Slower build, faster create.
2. **mise-based** — install mise in base image, provision agents on first start (like pixels). Flexible but slower first start.
3. **Layered images** — base image + per-agent layers. User picks which layers to include.

**Open questions:**
- Should shell mode sandboxes still support `yoloai diff` / `yoloai apply`? (Probably yes — the copy/diff/apply workflow is orthogonal to how the agent is launched.)
- How to handle API keys for multiple agents? Currently one agent = one set of env vars. Shell mode needs all keys available.
- Should this replace the current agent-launch model or complement it? (Complement — the auto-launch model is better for scripting and CI.)

---

## Aider Local-Model Integration

Aider supports local model servers via environment variables and model name conventions.

**Ollama:** Set `OLLAMA_API_BASE=http://host.docker.internal:11434` and use model format `ollama_chat/<model>` (e.g., `ollama_chat/llama3`). `host.docker.internal` resolves to the host on macOS and Windows Docker Desktop. On Linux, `--add-host=host.docker.internal:host-gateway` is needed (future enhancement).

**Other local servers:** LM Studio, vLLM, and llama.cpp's server all expose OpenAI-compatible APIs. Aider connects via `OPENAI_API_BASE` pointed at the local endpoint, with `OPENAI_API_KEY` set to any non-empty value (local servers don't validate keys).

**Network considerations:** Local model servers run on the host. Containers reach the host via `host.docker.internal` (macOS/Windows) or `--add-host` (Linux). Network-isolated sandboxes (`--network-none`) cannot reach local servers — this is by design. Future profile support could add `--add-host` configuration.

**Profile vision:** A "local-models" profile would bundle:
- `env` entries for model server URLs
- Network configuration for host access
- Optional Dockerfile additions for model-specific dependencies
