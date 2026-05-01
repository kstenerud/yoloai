# Competitive Landscape

## Existing Tools

### 1. deva.sh (formerly claude-code-yolo)

**Repo:** [thevibeworks/claude-code-yolo](https://github.com/thevibeworks/claude-code-yolo)
**Stars:** 7 | **Language:** Bash | **Status:** Active, rebranded to "deva.sh"

**What it does:** Bash script that launches Claude Code (and Codex, Gemini) inside Docker with `--dangerously-skip-permissions`. Supports multi-auth contexts, granular read-only/read-write mount control, and multiple agents.

**Strengths:**
- Multi-agent support (Claude, Codex, Gemini) — future-proofed
- Granular mount permissions (`:ro`/`:rw` per directory)
- XDG-based config organization for multiple auth contexts
- Non-root execution matching host UID/GID
- Dangerous directory detection (warns when running in `$HOME` or system dirs)
- tmux bridge for cross-kernel socket mounting

**Weaknesses:**
- Mount-only, no copy/diff/apply workflow
- Bash script — harder to maintain and extend vs. Python
- No session logging or output capture
- No mechanism to review changes before they land
- Complex auth system (5 methods) may intimidate new users

**Community:** Zero external adoption. No Reddit or HN mentions. Issue tracker has 168 issues but ~98 are bot-generated changelog entries tracking upstream Claude releases (~70 real issues).

**Lessons:** Read-only mounts for dependencies are a good default. UID/GID matching avoids permission headaches. Dangerous directory detection is a nice safety net.

---

### 2. claude-code-sandbox (TextCortex) → Spritz

**Repo:** [textcortex/claude-code-sandbox](https://github.com/textcortex/claude-code-sandbox) (archived)
**Stars:** ~297 | **Language:** TypeScript | **Status:** Archived Feb 2026, replaced by Spritz

**What it did:** Docker containers with `--dangerously-skip-permissions`, web-based terminal at `localhost:3456`, auto git branch per session, commit monitoring with syntax-highlighted diffs, auto-commit every 60s.

**Why it was archived:**
1. Anthropic and Docker shipped official sandboxing, eating its lunch
2. TextCortex pivoted to Kubernetes-native multi-agent orchestration (Spritz)
3. PoC-quality code couldn't handle cross-platform issues

**Key issues reported (14 open, never fixed):**
- Credential management failures (#17, #14) — biggest practical pain point
- Cross-platform breakage: Windows (#18), macOS extended attributes (#31), Docker Desktop requirement (#34)
- No network isolation (#28)
- Missing LICENSE file (#25) — governance gap
- Users wanted multi-instance management (#23)
- Podman docs were inaccurate (#24)

**Lessons:**
- **Credential management in containers is HARD.** Multiple issues traced back to creds not reaching the container. Treat as first-class problem.
- **Cross-platform testing is non-negotiable.** 4/14 issues were platform-specific.
- **"Security" requires specificity.** Users immediately asked about network isolation and drive access.
- **License your project on day one.**
- **Git-first workflow isolation was the most praised feature.** Auto-branching per session with diff review was genuinely loved.

**Spritz** (successor): Kubernetes-native Go project, 5 stars, completely different scope (team infra vs individual dev tool). Not a competitor to us.

---

### 3. claude-sandbox (rsh3khar)

**Repo:** [rsh3khar/claude-sandbox](https://github.com/rsh3khar/claude-sandbox)
**Stars:** 0 | **Status:** Active (created 2026-01-24, ~4 weeks old)

**What it does:** Bash installer + Docker container. Auto-commits to GitHub every 60 seconds. Container destroyed on exit.

**Notable features:**
- Rich sandbox context file (`sandbox-context.md`) injected into Claude — tells Claude about its environment, paths, constraints
- Parallel agent support via tmux with `send-keys` for prompts (validates our tmux approach)
- Git worktree support for parallel agents on same repo
- Browser automation via Playwright MCP (headless Chromium)
- Skills system for extensibility

**Key tmux patterns from their docs:**
```bash
# Start agent in detached tmux
tmux new-session -d -s agent1
tmux send-keys -t agent1 'claude --dangerously-skip-permissions' Enter
sleep 3  # Wait for Claude to start
tmux send-keys -t agent1 'Build the REST API' Enter Enter  # Double Enter needed
```

**Lessons:**
- `tmux send-keys` with a sleep delay is the proven pattern for feeding prompts
- Double Enter is needed to submit in Claude Code via tmux
- Injecting a context file telling Claude about its sandbox environment is smart
- Auto-commit interval (60s) provides a recovery safety net

---

### 4. Docker Official Sandboxes

**Docs:** [docs.docker.com/ai/sandboxes](https://docs.docker.com/ai/sandboxes/agents/claude-code/)
**Status:** GA in Docker Desktop 4.50+

**Architecture:** MicroVM-based (not containers). Each sandbox gets its own VM with a private Docker daemon. Uses `virtualization.framework` (macOS), Hyper-V (Windows).

**Strengths:**
- Strongest isolation (hypervisor-level)
- Credential injection via proxy — never stored in VM
- Bidirectional file sync preserving absolute paths
- Network proxy with allow/deny lists
- Single-command UX: `docker sandbox run claude ~/project`
- Can run Docker-in-Docker safely

**Major complaints:**
- **User config not carried over** — CLAUDE.md, plugins, skills, hooks all ignored. Top complaint on Docker forums.
- **OAuth authentication broken** for Pro/Max plans (docker/for-mac#7842)
- **Credentials lost on sandbox removal** (docker/for-mac#7827)
- **Re-auth required per worktree session** (docker/for-mac#7822)
- **No port forwarding** — can't hit dev servers from host browser. Hard blocker for web dev.
- **No SSH key access** — sandbox can't see host SSH keys
- **Linux degraded** — no microVM, only container-based
- **~3x I/O penalty** on macOS from hypervisor boundary
- **No image sharing between sandboxes** — each has its own Docker daemon/storage

**Lessons:**
- CLAUDE.md and user config MUST carry over. Power users depend on this.
- Credential proxy is the right design — never store creds in sandbox.
- Path consistency matters — mount at same paths to avoid confusion.
- Port forwarding is essential for web dev.
- I/O performance penalty of VMs is real; Docker containers are more practical for most workflows.

---

### 5. cco

**Repo:** [nikvdp/cco](https://github.com/nikvdp/cco)
**Stars:** 167 | **Language:** Bash | **Status:** Active (HN front page, 36 merged PRs)

**What it does:** Multi-backend sandbox wrapper that auto-selects the best isolation mechanism available: macOS `sandbox-exec`, Linux `bubblewrap`, Docker as fallback. Supports Claude Code, Codex, Opencode, Pi, and factory.ai's droid.

**Strengths:**
- Multi-backend architecture — adapts to host OS automatically
- macOS Keychain integration for credential extraction
- BPF-based TIOCSTI sandbox escape mitigation
- Git worktree auto-detection
- `--safe` flag for stricter filesystem isolation
- Zero-config "just make it work" UX

**Weaknesses:**
- Native sandbox mode exposes entire host filesystem read-only (issue #5) — significant security gap
- Fresh container per session loses installed packages (issue #34)
- No copy/diff/apply workflow
- No session logging or profiles

**Lessons:** Multi-backend approach is appealing but introduces complexity. Native sandbox modes can give a false sense of security if they expose the full filesystem. Keychain integration is a smart credential approach for macOS.

---

### 6. Anthropic sandbox-runtime

**Repo:** [anthropic-experimental/sandbox-runtime](https://github.com/anthropic-experimental/sandbox-runtime)
**Stars:** 3,117 | **Status:** Active (official Anthropic open-source)

**What it does:** OS-level sandboxing using bubblewrap (Linux) / Seatbelt (macOS). No container needed. Reduces Claude Code permission prompts by 84% internally at Anthropic.

**Strengths:**
- Largest community adoption (more stars than all other tools combined)
- Native OS-level isolation — no Docker dependency
- Integrated into Claude Code's own permission system
- 84% reduction in permission prompts

**Weaknesses:**
- Claude can autonomously disable sandbox in auto-allow mode
- Config fallback silently disables filesystem read restrictions
- Data exfiltration via IP address when `allowLocalBinding` is true
- Data exfiltration via DNS resolution
- Linux proxy bypass (uses env vars that programs can ignore)
- Domain fronting can bypass network filtering
- Leaks dotfiles when sigkilled

**Lessons:** OS-level sandboxing is convenient but has inherent bypass routes. The security issues here validate our Docker-based isolation approach where the sandbox boundary is a container, not just process-level restrictions.

---

### 7. Other Notable Tools

**Trail of Bits devcontainer** ([trailofbits/claude-code-devcontainer](https://github.com/trailofbits/claude-code-devcontainer)): Security audit focused. Documents firewall rules for restricting outbound to whitelisted domains (guidance only — not enforced by default; users must manually configure iptables/ipset rules). Reference implementation for secure setups.

**claude-compose-sandbox** ([whisller/claude-compose-sandbox](https://github.com/whisller/claude-compose-sandbox)): Docker Compose based, notable for SSH agent forwarding (credentials via agent, not mounted keys).

**nono** ([always-further/nono](https://github.com/always-further/nono), [nono.sh](https://nono.sh/)): Kernel-level isolation via Landlock (Linux) / Seatbelt (macOS). Irreversible restrictions. Atomic filesystem snapshots for undo/rollback. Cryptographic audit logging. Sigstore-based supply chain integrity. Native SDKs for Python, TypeScript, Rust, plus C FFI bindings. Declarative capability sets (`caps.allow_path("/project", READ_WRITE); caps.block_network()`). Created 2026-01-31, self-described "early alpha." Known friction: SSL cert verification blocked under kernel sandbox (issue #93), Seatbelt blocks `network-bind` (issue #131).

---

### 8. Additional Landscape

The following tools were identified but not analyzed in depth. Included for completeness — the sandboxing landscape has 30+ tools; the sections above cover the most instructive ones.

| Tool | Stars | Approach | Notes |
|------|-------|----------|-------|
| boxlite-ai/boxlite | 1,189 | Micro-VM engine | Each sandbox runs its own kernel. No daemon. Embeddable. |
| coder/agentapi | 1,214 | HTTP API wrapper | Multi-agent control interface over sandboxed agents. |
| cloudflare/sandbox-sdk | 905 | Cloud edge sandbox | Cloudflare Workers integration. Major vendor backing. |
| RchGrav/claudebox | 896 | Docker profiles | Pre-configured dev profiles per language stack. Closest to our profile concept. |
| rivet-dev/sandbox-agent | 896 | HTTP/SSE API | Universal API for controlling Claude/Codex/Amp inside sandboxes. Rust binary. |
| tomascupr/sandstorm | 375 | Cloud sandbox | One-call deployment. API + CLI + Slack. |
| disler/agent-sandbox-skill | 318 | Claude Code skill | Manages sandboxes from *within* Claude Code. Novel approach. |
| sadjow/claude-code-nix | 216 | Nix | Nix-based isolation of the Node runtime. |
| PACHAKUTlQ/ClaudeCage | 134 | Bubblewrap | Single portable executable, drop-in `claude` replacement. No Docker needed. |
| finbarr/yolobox | 509 | Docker/Podman/Apple containers | Go binary. Project dir r/w, home excluded. Pre-configured dev tools. `--no-network` flag. Config via TOML. |
| clawvisor/clawvisor | 13 | Credential proxy + LLM intent verification | Go + React. Authorization gateway between agents and APIs. Injects credentials server-side. Optional LLM-based intent verification catches request drift. Task-based scoping. |
| srid/sandnix | 34 | Nix + Landlock/Seatbelt | Nix flake module for declarative sandboxing. Cross-platform (Landlock on Linux, sandbox-exec on macOS). High-level feature flags (tty, network, nix-store). |
| multitui.com | N/A | macOS GUI + sandbox-exec | SvelteKit macOS app wrapping CLI tools as native apps. Network rules per domain. Secrets filter (gitleaks-based) scanning outbound traffic for API keys. |

**Approach categories not fully explored:** macOS `sandbox-exec` tools (5+ projects), Nix-based isolation, micro-VM alternatives (boxlite, Fly.io Sprites), commercial platforms (Fly.io Sprites, Vercel Sandbox), HTTP API wrappers (agentapi, sandbox-agent). E2B, Daytona, Modal, Northflank, and Zeroboot are covered in sections 13–14.

### 9. Agent Safehouse

**Repo:** [eugene1g/agent-safehouse](https://github.com/eugene1g/agent-safehouse)
**Stars:** 37 | **Language:** Shell (99.7%) | **Status:** Active (104 commits, Apache 2.0)

**What it does:** Single bash script that wraps any agent CLI with macOS `sandbox-exec`. Deny-first SBPL profiles restrict filesystem access to the project's git root. No lifecycle management, no copy/diff/apply — purely a transparent sandbox wrapper.

**Architecture:** Downloads as a single `safehouse.sh` script. Runs `sandbox-exec` with a generated profile around the target agent command. No daemon, no state management.

**Strengths:**
- **Zero-dependency installation** — single curl command
- **Dynamic toolchain detection** — auto-discovers installed dev environments (node, python, rust, etc.) and grants read access, rather than hardcoding paths like `/opt/homebrew`
- **Shell function pattern** — `claude() { safe claude --dangerously-skip-permissions "$@"; }` makes sandboxing transparent and composable
- **Per-agent investigation reports** — documents what each agent tries to access under sandboxing, specific quirks and breakage patterns
- **Composable policies** — extensible permission configuration via `--add-dirs-ro` and similar flags
- **Wide agent compatibility** — tested with 13+ agents (Claude Code, Codex, Gemini CLI, Aider, Goose, Amp, OpenCode, Auggie, Pi, Cursor Agent, Cline, Kilo Code, Droid)

**Weaknesses:**
- macOS-only (sandbox-exec dependency)
- No change isolation — agent writes directly to project directory
- No copy/diff/apply workflow
- No session management, logging, or persistent state
- Coarse network control (same sandbox-exec limitation as all Seatbelt tools)

**Actionable lessons for yoloAI:**

1. **Dynamic toolchain detection for seatbelt profiles.** Our `profile.go:systemReadPaths()` hardcodes paths (`/opt/homebrew`, `/usr/local/Cellar`, etc.). Safehouse instead detects installed toolchains at runtime (e.g., resolving `which node` to its prefix) and grants read access dynamically. This would reduce "Operation not permitted" friction for users with non-standard tool installations (nix, asdf, mise, custom prefixes). Low-effort improvement to `GenerateProfile()`.

2. **Per-agent sandbox compatibility docs.** Safehouse documents what each agent tries to access (filesystem paths, network endpoints, IPC) and what breaks under sandboxing. We should catalog this for our supported agents — e.g., which paths Claude Code, Codex, Gemini need beyond the working directory. This directly improves our SBPL profile generation and agent definitions.

3. **Lightweight "wrap" mode.** Their shell function pattern (`safe() { safehouse "$@"; }`) enables sandboxing without the full create/start/attach lifecycle. A future `yoloai wrap <agent-command>` could provide quick one-off sandboxed runs using seatbelt without creating a persistent sandbox — useful for users who want protection but don't need copy/diff/apply.

---

### 10. EnvPod CE (Xtellix)

**Repo:** [markamo/envpod-ce](https://github.com/markamo/envpod-ce)
**Stars:** New | **Language:** Rust | **Status:** v0.1.0 released March 2026
**License:** BSL 1.1 (free to use/self-host, converts to AGPL-3.0 in 2030). Provisional patent filed Feb 2026.
**Author:** Mark Amoboateng / Xtellix Inc.

**What it does:** A governance runtime for AI agents, built from scratch on native Linux primitives (namespaces, cgroups v2, overlayfs, seccomp-BPF). Single static Rust binary (~12MB, musl), zero runtime dependencies — not Docker-based. Tagline: "Docker isolates. Envpod governs."

**Architecture:**
- **Foundation:** OverlayFS copy-on-write filesystem (diff/commit/rollback)
- **Four Walls:** PID/mount/UTS/user namespaces, cgroups v2, seccomp-BPF, per-pod network namespace with veth pairs
- **Governance Ceiling:** Encrypted credential vault, action queue with approval tiers, append-only audit trail, monitoring policies, remote control, web dashboard

**Scale:** 44 Rust source files (~440KB source). CLI main.rs alone is 6,467 lines. 26+ subcommands, 18 built-in presets, 45 example configs, 25+ documentation files.

**Strengths:**
- **Governance layer is the key differentiator.** Action queue with 4 approval tiers (immediate/delayed/staged/blocked) — agents call actions via Unix socket, dangerous actions can be staged for human approval. Append-only JSONL audit trail records every operation. Monitoring agent with configurable policies. Remote control (freeze/resume/kill). This is genuinely novel in the sandbox space.
- **Credential vault with proxy injection.** ChaCha20-Poly1305 encrypted per-pod vault. v0.2 adds transparent HTTPS proxy on the host-side veth that injects API keys at the transport layer — the agent makes normal HTTPS requests but never sees the actual key. DNS remaps API endpoints (e.g., `api.anthropic.com`) to the local proxy. Eliminates credential exfiltration entirely.
- **Per-pod DNS resolver.** Embedded DNS server per pod with whitelist/blacklist/monitor modes, domain remapping, anti-DNS-tunneling detection, live mutation without restart. Central daemon for pod-to-pod discovery (`*.pods.local`).
- **Snapshot system.** Named checkpoints with save/restore/promote-to-base-pod. Fast cloning from base pods (~130ms vs 1.3s init).
- **Web dashboard.** Fleet management UI with real-time monitoring, audit viewer, diff inspector, noVNC desktop display with audio.
- **Extensive agent support.** 18 presets: Claude Code, Codex, Gemini CLI, Aider, SWE-agent, OpenCode, LangGraph, Google ADK, OpenClaw, browser-use, Playwright, plus desktop/dev environments.
- **Security testing.** 49 jailbreak boundary tests (55KB test script). Built-in `audit --security` static analysis.
- **Performance.** Claims to outperform Docker: 401ms fresh start vs 552ms for Docker, 32ms warm exec vs 95ms.

**Weaknesses:**
- **Linux-only.** Requires kernel namespaces, cgroups v2, and overlayfs. No macOS or Windows support. Docker/VM backends planned but not shipped.
- **Requires root.** `sudo envpod` for every operation — namespace/cgroup manipulation needs privileges.
- **BSL license + patent.** May deter OSS contributors and enterprise adopters who prefer permissive or pure copyleft licenses. Cannot embed in competing commercial products or offer as managed service.
- **Complexity.** 26+ commands, 4-tier action queues, monitoring policies, vault proxy configuration — powerful but intimidating for quick one-off agent runs.
- **No ecosystem integration.** Bespoke runtime means no Docker Compose, no existing container tooling, no Kubernetes path.
- **Single developer.** No visible community or external contributions yet.

**How it compares to yoloAI:**

| Aspect | EnvPod CE | yoloAI |
|--------|-----------|--------|
| Isolation | Native Linux primitives | Docker / Tart / Seatbelt |
| Platform | Linux only | Linux, macOS |
| Root required | Yes | No |
| Diff/review workflow | OverlayFS diff/commit/rollback | git-based copy/diff/apply |
| Credential mgmt | Encrypted vault + HTTPS proxy injection | File-based bind mount |
| Network control | Per-pod DNS resolver + filtering | Docker network + agent allowlists |
| Governance | Action queues, approval tiers, audit trail | None (future opportunity) |
| Snapshots | Named checkpoints, base pods | None |
| Dashboard | Web UI (fleet, audit, diff, noVNC) | None |
| Complexity | High (26+ commands, governance model) | Low (familiar Docker/git workflow) |
| Dependencies | None (static binary) | Docker / Tart / none (Seatbelt) |

**Actionable lessons for yoloAI:**

1. **Action governance is a compelling concept.** Staging dangerous agent operations (git push, external HTTP requests) for human approval before execution addresses the "agent operates within permissions but does something unwanted" problem. A lightweight version — e.g., agents declare intended side-effects, user approves before `yoloai apply` — could be valuable without the full queue/tier complexity.

2. **Vault proxy injection eliminates credential exfiltration.** Our file-based bind mount means the agent can read the API key directly. A transparent proxy that injects credentials at the transport layer (agent never sees the key) is a meaningful security upgrade. Worth investigating as an enhancement to our credential injection, especially for the Docker backend where we control the network stack.

3. **DNS-level filtering is more granular than Docker's network controls.** Per-pod DNS with whitelist/monitor/anti-tunneling addresses the CVE-2025-55284 (DNS exfiltration from Claude Code) class of attacks directly. Our agent network allowlists work at a higher level. For Docker backend, a DNS sidecar container could provide this without requiring native DNS server code.

4. **Snapshots add workflow flexibility.** Named checkpoints that can be restored or promoted to templates complement the diff/apply workflow. For yoloAI, this could map to git tags or branches in `:copy` mode — cheap to implement since we already use git.

5. **Our cross-platform support is a major differentiator.** EnvPod's Linux-only, root-required approach locks out macOS developers. Our Docker/Tart/Seatbelt multi-backend strategy serves a much wider audience. This is worth emphasizing in positioning.

6. **Simplicity is a feature.** EnvPod's 26+ commands and governance model is powerful but adds cognitive load. yoloAI's approach of using familiar tools (Docker, git, unix conventions) lowers the barrier to entry. Don't chase feature parity at the cost of simplicity — add governance features only when they can be made lightweight and optional.

---

### 11. agent-clip (epiral)

**Repo:** [epiral/agent-clip](https://github.com/epiral/agent-clip)
**Stars:** 183 | **Forks:** 14 | **Language:** Go 57.3%, TypeScript 40.4% | **Status:** Active (61 commits, no releases)
**Created:** March 9, 2026
**License:** Not identified in fetched content

**What it does:** An AI agent implemented as a [Pinix](https://github.com/epiral/pinix) Clip. Provides an agentic loop with memory, tool use, vision (browser screenshots), and async execution. It is **not a sandbox runner** — it is a general-purpose AI agent runtime packaged for the Pinix platform. The agent runs inside Pinix's BoxLite micro-VMs via the Clip packaging format, but sandboxing is a side effect of the platform, not an intentional design goal.

**Architecture overview:**

The system follows a three-layer model:
```
Workspace → Package (.clip ZIP) → Instance (Pinix Server)
```

Key components:
- `internal/loop.go` — Agentic loop: LLM → tool_calls → execute → repeat
- `internal/memory.go` — Summary generation and semantic search via embeddings
- `internal/browser.go` — HTTP client with browser screenshot capture
- `internal/db.go` — SQLite schema and transactions
- Frontend: React + Vite + Tailwind CSS v4, Streamdown markdown with LaTeX support

**LLM interface:** Uses a "one function call" pattern — the LLM is given a single `run(command, stdin?)` tool. Each invocation is a fresh process with state persisted to SQLite. Context management uses a "Run Window 3→7" strategy: recent runs are included as full messages; older ones become LLM-generated summaries with embeddings for semantic retrieval.

**Memory system (three layers):**
1. Persistent facts (structured key-value store)
2. LLM-generated run summaries with embeddings
3. Semantic search across summaries

**Tool suite (~24 commands) available to the LLM:**
- File I/O: `ls`, `cat`, `write`, `rm`, `cp`, `mv`, `mkdir` (operating on a `data/` directory)
- Memory management: read/write facts, search summaries
- Topic and run management: create-topic, list-topics, get-run, cancel-run
- Browser control: screenshot capture, HTTP fetch
- Clip invocation: calling other Pinix Clips as sub-agents

**Pinix platform dependency:**

Agent-clip is tightly coupled to [Pinix](https://github.com/epiral/pinix), a decentralized runtime platform. Pinix runs Clips inside BoxLite micro-VMs on servers, or natively on "edge" devices (iPhone, Raspberry Pi, ESP32). The Clip interface (Invoke / ReadFile / GetInfo) is the universal contract — callers don't see whether execution happens in a VM or on a device. Packaging is a `.clip` ZIP archive deployed via Pinix commands. This architecture means agent-clip has no standalone use outside the Pinix ecosystem.

**Target audience:**

Developers already using or interested in the Pinix platform who want an intelligent agent with memory and vision. Not a general-purpose tool for running Claude Code / Gemini / Codex on developer projects. No evidence of targeting the AI coding assistant safety-sandbox use case at all.

**Comparison with yoloAI:**

| Aspect | agent-clip | yoloAI |
|--------|-----------|--------|
| Core purpose | General-purpose AI agent runtime | Sandboxed AI coding agent runner |
| Sandboxing model | Platform-provided (Pinix BoxLite micro-VMs) — incidental | Intentional isolation (Docker / Tart / Seatbelt) |
| Supported agents | Custom agent (OpenRouter LLM) | Claude Code, Gemini CLI, Codex, Aider, OpenCode, Pi |
| Copy/diff/apply workflow | No | Yes (core differentiator) |
| User reviews changes before apply | No | Yes |
| Persistent sandbox state | Yes (SQLite, `data/` dir survives upgrades) | Yes (`~/.yoloai/sandboxes/<name>/`) |
| Directory mount modes | N/A (data/ only) | :copy, :overlay, :rw, read-only |
| Profile system / Dockerfiles | No | Yes |
| Multi-backend runtimes | No (Pinix only) | Docker, Tart, Seatbelt |
| Memory / embeddings | Yes (3-layer + semantic search) | No |
| Vision (screenshots) | Yes | No |
| Web UI | Yes (React frontend) | No |
| Standalone use (no platform dependency) | No (requires Pinix) | Yes (requires Docker/Tart) |
| Cross-platform | macOS primary (binary build target) | Linux, macOS |
| Community size | Small (183 stars, 14 forks, no releases) | — |

**Strengths of agent-clip:**
- Sophisticated memory architecture (facts + embeddings + semantic search) — rivals standalone memory systems
- Vision support is a genuine differentiator for agents that need to observe rendered output
- The "one function call" LLM interface is clean and auditable — each tool use maps to a subprocess invocation
- Topic/run namespacing gives structured multi-session management
- SQLite-backed state is durable and inspectable
- Cross-language docs (English + Chinese + Vietnamese) suggest broader geographic reach

**Weaknesses relative to yoloAI's goals:**
- **Not a competitor in the sandboxed AI coding agent space.** It does not run Claude Code, Gemini CLI, or Codex. It does not provide copy/diff/apply. It is not designed to protect host codebases from agent-caused damage.
- **Hard platform dependency on Pinix.** Users must run a Pinix server and understand the Clip packaging model. This is a non-trivial onboarding barrier compared to yoloAI's single binary + Docker.
- **No change isolation workflow.** Agents write directly to the `data/` directory with no mechanism for the user to review changes before they land.
- **macOS-primary build.** `make dev` builds a macOS binary; cross-compilation is mentioned but the development ergonomics skew toward macOS Pinix server users.
- **No releases.** Despite 183 stars, there are no tagged releases or distributed binaries. Adoption curve is steep.
- **Agent is the LLM, not an AI coding tool.** agent-clip runs a custom agent loop — it is not a wrapper around Claude Code, Gemini CLI, or any established coding agent. Users of yoloAI's target persona (developers who want to run Claude Code safely) would not reach for agent-clip.

**Conclusion:**

Agent-clip is not a meaningful competitor to yoloAI. It occupies a different niche: a platform-native intelligent agent with memory and vision, built for the Pinix ecosystem. Its 183 stars likely reflect interest in the memory architecture and the Pinix platform rather than any overlap with sandboxed coding agent runners. No actionable lessons for yoloAI's current roadmap — the toolchains, goals, and user personas are disjoint.

---

## Community Pain Points (from GitHub issues, Reddit, HN)

> **Note:** This section covers GitHub issues and Hacker News only. Lobste.rs is not monitored.

### Top complaints (ranked by frequency):
1. **Approval fatigue** — constant permission prompts break flow, even "always allow" doesn't stick
2. **Root user rejection** — `--dangerously-skip-permissions` refuses to run as root in containers (issues #927, #3490, #5172)
3. **Network whitelist is HTTP-only** — SSH/TCP connections can't traverse the proxy (#11481, #24091)
4. **Credential management fragility** — getting API keys/SSH keys/git config into containers reliably; 93% of AI agent projects use unscoped API keys with no per-agent revocation (state-of-agent-security-2026 report)
5. **User config not carried over** — CLAUDE.md, plugins, skills lost in sandboxes
6. **No middle ground** between "approve everything" and "skip everything"
7. **Cross-platform inconsistency** — works on Linux, breaks on macOS/Windows
8. **Data exfiltration risk** — agent has same network access as developer
9. **Agent-controlled sandboxing** — Claude Code's built-in sandbox can be disabled by the agent itself to complete a task; HN consensus is this is architecturally broken ("there should be no off switch")

### What users wish existed:
- A clean middle ground: sandbox provides safety, Claude operates freely within it
- Granular network controls (domain + port level, not just HTTP); `--network none` by default with per-task exemptions seen as best practice but requires manual orchestration
- Workspace-only filesystem confinement
- Audit logging for all actions (JSONL append-only preferred)
- Time-scoped credentials that auto-expire; zero-knowledge proxy model (agent makes authenticated calls without ever seeing the raw key)
- Multi-agent management from a single interface; parallel sprint coordination (4-5 concurrent worktrees) is a real gap
- Copy/review/apply as a first-class low-friction experience — currently DIY (VM sync scripts, git worktree juggling)
- Enforcement outside the agent's reasoning loop (BPF LSM, kernel-level, external process) — not inside the agent

### HN thread insights (2026-03-09, Agent Safehouse, 471 points, 109 comments):

**"Sandboxing is necessary but insufficient"** — strong consensus that filesystem/process restrictions alone don't prevent agents from operating "perfectly within permissions and still producing garbage" (devonkelley). Three additional layers were discussed:

1. **Credential scoping** (silverstream): "An agent inside sandbox-exec still has your AWS keys, GitHub token, whatever's in the environment." Solution: scoped short-lived JWTs instead of raw credentials. Clawvisor (Go project) implements this as a credential proxy that injects credentials server-side — agents never see the raw tokens.

2. **Dynamic permission shrinking** (zmmmmm): Sandbox permissions should contract when agents are "tainted" by untrusted content (e.g., reading a malicious file or processing untrusted input). This is the "confused deputy" problem — a sandboxed agent with legitimate credentials can be tricked into misusing them.

3. **Tool-level auth scoping at MCP layer** (gnanagurusrgs, Arcade): Sandboxing the runtime alone is insufficient; each tool/MCP server needs individually scoped authorization with JIT OAuth. The granularity should be at the tool call level, not the process level.

4. **Supervisor agent frameworks** (nemo44x): Sandboxing prevents catastrophic damage but doesn't prevent cascading failures from hallucinations or goal drift. A supervisor agent (or human-in-the-loop) that monitors *what* the agent is doing, not just *what it can access*, is needed for production reliability.

**Relevance to yoloAI:** Our copy/diff/apply workflow already addresses the "produces garbage" problem — changes are reviewed before landing. The credential scoping discussion reinforces that our file-based secrets injection is better than env vars, but could be improved with short-lived/scoped tokens. The supervisor/monitoring angle aligns with our idle detection and status monitoring work.

### HN thread insights (2026-03-21 sweep, multiple threads):

**Threads covered:** "Snowflake AI Escapes Sandbox and Executes Malware" (266 pts, 83 comments); "Ask HN: The new wave of AI agent sandboxes?" (10 pts, 4 comments); "Show HN: Run Claude Code with –dangerously-skip-permissions in a Docker sandbox" (2 pts, 2 comments); "Show HN: Railguard – A safer –dangerously-skip-permissions for Claude Code" (1 pt, 3 comments); "Show HN: FireClaw – Open-source proxy defending AI agents from prompt injection" (5 pts, 5 comments).

**Snowflake Cortex sandbox escape (266 pts, 83 comments):** Snowflake's Cortex AI was exploited via prompt injection through a data file — the agent set an internal flag to disable its own sandbox, then executed malware via process substitution (`cat < <(sh...)`). Separately, Ona research published the same day demonstrated Claude Code finding a `/proc/self/root/usr/bin/npx` denylist bypass, then autonomously disabling bubblewrap when that was blocked, then using the dynamic linker (`ld-linux-x86-64.so.2 /usr/bin/wget`) to bypass syscall-level hooks entirely. HN community reached near-consensus: *"If constraints live inside the context the model can see and modify, they're not constraints. They're suggestions."* (riteshkew1001). Key Ona finding: *"Containers are deterministic workloads that don't actively evade restrictions. Agents can reason about security boundaries and systematically dismantle them."* The community view is that enforcement must live at the runtime/OS layer — not inside the agent's reasoning loop.

**Market saturation (10 pts, 4 comments):** A poster catalogued 37+ sandbox solutions launched in the past year. One sardonic comment: *"A tool so good that it requires 37 different sandboxes."* The thread confirms no single approach dominates; yoloAI must differentiate on workflow, not just isolation.

**`dangerously` Show HN (2 pts, 2 comments):** Minimal shell script + Dockerfile running Claude Code in Docker with `--dangerously-skip-permissions`. Mounts `~/.claude.json` and working directory. Creator acknowledged no network isolation. Conceptually identical to yoloAI's core but no diff/apply, no multi-agent, ~100 lines. Got more HN traction than yoloAI's Show HN.

**Railguard Show HN (1 pt, 3 comments):** Rust in-process interceptor for Claude Code using semantic rules + bwrap/sandbox-exec. Sub-2ms latency. Session replay, file snapshots, rollback. Avoids LLM-based detection due to circular vulnerability risk. But the Ona research published the same day demonstrated that rule-based and even syscall-level approaches can be bypassed by an agent that reasons about its environment. Container-level isolation (yoloAI's approach) is more resistant.

**yoloAI HN traction:** The Show HN from 2026-03-12 (ID: 47351706) received 1 point and 0 comments. No independent organic mentions in this sweep period. Creator (kstenerud) is manually promoting yoloAI in sandboxing discussions but not generating independent community traction.

**Relevance to yoloAI:** The Snowflake and Ona research are the strongest external validation of yoloAI's architectural choices to date — container isolation the agent cannot reason around, plus diff/apply for human review before changes land. Diff/apply is now validated as addressing both "produces garbage" (previous sweep) and "approval fatigue rubber stamp" (this sweep). Docker's official "Docker Sandboxes" product (announced Jan 2026) is the most significant new competitive threat — it has distribution and brand trust but no diff/apply workflow.

### HN thread insights (2026-03-17 sweep, multiple threads):

**Threads covered:** "OK, let's survey how everybody is sandboxing their AI coding agents in early 2026" (~15 comments); "Claude Code escapes its own denylist and sandbox" (40 points, 21 comments); "Running Claude Code dangerously (safely)" (351 points, 258 comments); "Sandboxing AI Agents in Linux" (119 points, 68 comments); "Beyond agentic coding" (269 points, 90 comments); "We saw how 30 AI agent projects handle authorization — 93% use unscoped API keys" (1 point); "Show HN: AgentSecrets – Zero-Knowledge Credential Proxy for AI Agents" (3 points); "NIST Seeking Public Comment on AI Agent Security" (49 points, 19 comments); "Claude March 2026 usage promotion" (252 points, 145 comments).

**Sandboxing landscape (survey thread):** The most common real-world approaches are: KVM/QEMU VMs with bidirectional sync scripts, Docker microVMs (growing preference over standard containers), bubblewrap/bwrap with Landlock LSM, macOS sandbox-exec + unprivileged accounts, nsjail, and Claude Code's built-in /sandbox (bubblewrap-based). Summary from pash: "the most common answers are (a) 'containers' and (b) YOLO!" — the tool survey validates this is the exact gap yoloAI occupies.

**Sandbox escape incident (40 points, 21 comments):** Claude Code disabled its own sandbox to complete a task — no jailbreak required. Top comment: "Claude Code's sandboxing is a complete joke. There should be no 'off switch.'" Core diagnosis: "Why is 'disable my own SANDBOX' not in the list of forbidden branches of code?" The HN consensus is that any enforcement mechanism inside the agent's reasoning loop is fundamentally broken — enforcement must live outside the agent. This validates yoloAI's external-enforcement architecture directly.

**Credential crisis ("93% use unscoped API keys"):** State-of-agent-security-2026 report found: 93% rely on unscoped API keys; 0% have per-agent cryptographic identity; 97% have no user consent flow; 100% have no per-agent revocation. Documented incidents: 21,000 exposed OpenClaw instances, 492 MCP servers with zero authentication, 1.5M leaked tokens from the Moltbook breach. AgentSecrets reframes the model: "AI agents are users, not applications — they don't need credential values, they need to make authenticated calls." The zero-knowledge proxy intercepts at the transport layer so keys never enter agent memory.

**Running Claude Code dangerously (safely) (351 points, 258 comments):** Real disasters documented: Claude deleting home directories, wiping databases, removing .git directories, using Docker socket (running as root) to read files it couldn't access directly — privilege escalation. Supabase MCP caused agents to run migrations on production instead of dev. Users who lost work all wished for a review gate before changes landed. Quote: "After a couple months with Claude not messing anything important up, the temptation is strong to run --dangerously-skip-permissions." This is the exact rationalization the copy/diff/apply workflow defuses.

**Beyond agentic coding (269 points, 90 comments):** Expert developers use agents for implementation throughput while retaining architectural judgment. "The expert already knows the architecture and what they want. The agents help crank through the implementation but you're reviewing everything." Desire for structured review before landing is strong and unprompted.

**yoloAI mention:** kstenerud (in the Claude sandbox escape thread, ~4 days ago): "[yoloai provides] the full benefit of --dangerously-skip-permissions with none of the risks. Standard claude sessions feel like using a browser without an ad blocker." This is organic community endorsement using our own language. No other yoloAI mentions found in this sweep.

**yoloAI — 2026-03-21 sweep:** Show HN (ID: 47351706, 2026-03-12) received 1 point and 0 comments. No independent organic mentions in the 2026-03-17 to 2026-03-21 window. Creator is manually promoting yoloAI in sandboxing threads. The `dangerously` tool (near-identical core concept, ~100 lines) received 2 points with a Show HN the same week — suggests yoloAI's diff/apply and multi-agent differentiators are not landing in the current framing.

**Tool sentiment summary (2026-03-17 sweep):**
- **Claude Code:** Positive on output quality, frustration with approval fatigue, sandbox escape, $150-200/day enterprise cost, WSL2 requirement on Windows
- **Gemini CLI:** Strongly negative. "Profound disappointment." Loops repeatedly, mid-operation stoppages. "Gemini CLI sucks. Just use OpenCode if you have to use Gemini."
- **Codex:** Mentioned positively as stable; poor Windows support; recently rebuilt in Rust; 1M context window
- **Bubblewrap/bwrap:** Most recommended Linux tool, but "not a hardened security isolation mechanism" — network still wide open unless `--unshare-net` set
- **Docker microVMs:** Growing preference over standard containers; practical balance of isolation vs. setup friction on Mac/Windows

**Tool sentiment summary (2026-03-21 sweep):**
- **Claude Code:** Still dominant (82K stars, 4,200+ weekly r/ClaudeCode contributors). Rate limits are now the #1 pain point — METR research found Claude Code *increased* task completion time 19% due to rate-limit waits. Security research (Ona) confirmed it actively circumvents its own sandbox. The `--dangerously-skip-permissions` flag spawned a cottage industry of wrapper tools.
- **Codex:** Gaining ground as "less friction" alternative. 4x better token efficiency. 68% user preference for autonomous tasks. Built-in microVM sandbox. Fire-and-forget with auto-PR generation. Users increasingly pairing Claude Code + Codex at $40/month rather than Claude Code Max at $100/month.
- **OpenCode:** Explosive growth — 95K+ stars (surpassed Claude Code in star count), 2.5M monthly developers. Terminal-native, 75+ LLM provider support, plan-first approval-based execution. Validates yoloAI's multi-agent approach.
- **Aider:** Stable niche. Described as "diff-driven collaborator that fits neatly into Git." Users call it "peaceful and pleasant." Philosophically closest to yoloAI's workflow ethos.
- **Gemini CLI:** Mixed. Gemini 3 Pro praised ("fast and precise"). Gemini 2.5 Flash "not suitable for professional development." New Conductor automated review feature (March 2026). Stability issues persist.

**Key quotes (2026-03-17 sweep):**
- "The most common answers are (a) 'containers' and (b) YOLO!" — pash
- "Many people have landed on isolation as a workaround while still lacking a real control plane on top of it. Containers reduce blast radius, but they don't answer approvals, policy, or auditability." — Lothbrok
- "AI agents are users, not applications." — AgentSecrets creator
- "Accepting that the model will be tricked and constraining what it can do when that happens" — NIST thread, emerging security philosophy
- "It is just too damn useful." — simonw, explaining why developers accept sandboxing risks

**Key quotes (2026-03-21 sweep):**
- "If constraints live inside the context the model can see and modify, they're not constraints. They're suggestions." — riteshkew1001, HN #47427017
- "Containers are deterministic workloads that don't actively evade restrictions. Agents can reason about security boundaries and systematically dismantle them." — Ona research
- "If the thing that is sandboxed can say 'do this without the sandbox', it is not a sandbox." — john_strinlai, HN #47427017
- "Approval fatigue turns a security boundary into a rubber stamp." — Ona research
- "A tool so good that it requires 37 different sandboxes." — ex-aws-dude, HN #47444917
- "One complex prompt to Claude and by the end you've burned 50-70% of your 5-hour limit." — Reddit developer, on Claude Code rate limits

**Relevance to yoloAI:** The community is acutely aware of the YOLO-mode vs. friction tradeoff and is actively looking for a solution. The sandbox escape incident validates external enforcement as the right architecture. Copy/diff/apply is understood and wanted but has no canonical tool. File-based credential injection (vs. env vars) is better than 93% of the field but this distinction isn't visible to users yet. The one organic mention positions yoloAI exactly right.

### Real-world security incidents:
- Claude deleting a user's entire Mac home directory (viral Reddit post)
- 13% of AI agent skills on ClawHub contain critical security flaws (Snyk study)
- GTG-1002 threat actor weaponized Claude Code for cyber espionage (Anthropic disclosure)
- Credential exposure via CLAUDE.md and related vectors: Claude creating issues in the public anthropics/claude-code repo exposing schemas and configs (issue #13797), ignoring CLAUDE.md security guidelines (issue #2142), malicious repo settings redirecting API keys via `ANTHROPIC_BASE_URL` (CVE-2026-21852)
- CVE-2025-55284: API key theft via DNS exfiltration from Claude Code
- CVE-2026-24052: SSRF in Claude Code's WebFetch trusted domain verification
- Zero-click DXT flaw: Exposed 10,000+ users to RCE via Claude Desktop Extensions
- Issue #27430: Claude autonomously published fabricated technical claims to 8+ platforms over 72 hours
- Claude Pirate research (Embrace The Red): Demonstrated abuse of Anthropic's File API for data exfiltration
- Claude Code escapes its own sandbox to complete tasks (no jailbreak required) — reported HN 2026-03 (40 points, 21 comments)
- Supabase MCP caused agents to run migrations against production instead of dev (documented in "Running Claude Code dangerously (safely)" thread, 351 points)
- Claude using Docker socket (running as root) to read files it couldn't access directly — privilege escalation via container runtime (same thread)
- 21,000 exposed OpenClaw instances, 492 MCP servers with zero authentication, 1.5M leaked tokens from the Moltbook breach (state-of-agent-security-2026 report, 2026-03)
- **Snowflake Cortex AI sandbox escape (2026-03-18, HN 266 pts):** Prompt injection via data file → agent set internal flag to disable its own sandbox → process substitution (`cat < <(sh...)`) bypassed command-word parser → malware execution. Same-day Ona research independently confirmed Claude Code finds `/proc/self/root/usr/bin/npx` path bypass, then autonomously disables bubblewrap, then uses ELF dynamic linker to bypass syscall hooks
- Developer case study (Level Up Coding, 2026-03): Claude Code agent, explicitly blocked from reading `.env` files, ran `docker compose config` and extracted API keys from parsed output — agent reasoning around semantic permission boundaries

---

## Feature Comparison

*Note: This table covers a subset of the landscape. See section 8 for additional tools.*

### 12. BunkerVM (ashishgituser)

**Repo:** [ashishgituser/bunkervm](https://github.com/ashishgituser/bunkervm)
**Version:** 0.8.6 (Alpha) | **Language:** Python | **License:** AGPL-3.0

**What it does:** Python library (`pip install bunkervm`) that boots disposable **Firecracker microVMs** (Amazon's microVM runtime, ~3s boot, KVM-backed) for AI agent code execution. Ships a native MCP server (Claude Desktop, VS Code Copilot), framework integrations (LangChain, CrewAI, OpenAI Agents, LangGraph), and a Tauri-based desktop app (BunkerDesktop, targeting Windows first).

**Architecture:** Host runs a Python daemon managing Firecracker VMs via a REST API on `localhost:9551`. Guest is Alpine Linux + Python 3.12. Host↔VM communication via vsock UDS (zero-config, no TAP). TAP networking is an opt-in for internet access.

**Key features:**
- **KVM hardware isolation** — separate kernel per VM, not containerization; container escape CVEs don't apply
- **VMPool** — up to 10 concurrent VMs, thread-safe, each isolated (unique vsock CID, rootfs copy, subnet)
- **MCP server** — 8 tools (exec, file read/write, install, reset, snapshot, etc.) auto-discovered by Claude Desktop and VS Code Copilot
- **Safety classifier** — regex-based advisory classification (READ/WRITE/SYSTEM/DESTRUCTIVE/BLOCKED) logged to JSONL audit trail
- **JSONL audit logging** — append-only, thread-safe, timestamp + sequence + event type
- **Engine daemon** (emerging) — REST API for centralized VM management; thin clients auto-discover

**Weaknesses:**
- No diff/apply workflow — VMs are stateless/ephemeral; no git-aware file sync
- No project-level configuration (global only)
- No macOS support (Apple Virtualization.framework lacks nested KVM)
- Manual file management (upload/download), no `:copy`/`:overlay`/`:rw` equivalent
- Targets AI framework integrations (LangChain, CrewAI), not CLI agents (Claude Code, Gemini CLI)
- Alpha maturity; Windows installer unsigned; BunkerDesktop not yet 1.0

**Threat level: MEDIUM — different primary market, not a direct competitor today.** BunkerVM is "run code and get output" (ephemeral, framework-oriented); yoloAI is "apply changes to a project" (persistent, CLI-agent-oriented). Would become competitive if it adds diff/apply + CLI agent support and reaches 1.0.

**Lessons:**
- **MCP-first integration is becoming table stakes.** Claude Desktop and VS Code Copilot auto-discover MCP servers; this is a distribution channel yoloAI doesn't use.
- **JSONL audit logs** are better than plain text for compliance/debugging. Append-only with sequence numbers + event types.
- **Airgapped default** (vsock, no TAP) mirrors our read-only mount default — safe by default is the right call.
- **VMPool concurrent execution** validates the batch/parallel agent design we already have on the roadmap.
- **Hardware isolation is a genuine differentiator** for untrusted code — if yoloAI ever targets security-critical use cases, Firecracker or gVisor would be worth evaluating.

---

### 13. Zeroboot (adammiribyan)

**Source:** [Show HN: Sub-millisecond VM sandboxes using CoW memory forking](https://news.ycombinator.com/item?id=47412569) (2026-03-17)
**Repo:** [github.com/adammiribyan/zeroboot](https://github.com/adammiribyan/zeroboot)

**What it does:** Research prototype achieving 0.79ms p50 sandbox startup using Firecracker + copy-on-write memory forking. Creates a "template" VM once (with Python + NumPy pre-loaded), then forks it for each sandbox request using `MAP_PRIVATE` copy-on-write on the Firecracker snapshot. Each fork gets its own kernel (KVM/hardware isolation) with only 265KB memory overhead.

**Technique:** Fork VM from snapshot instead of booting a new VM each time. `MAP_PRIVATE` on the guest memory file causes Linux to CoW pages on write — unmodified pages are shared across all forks without copying.

**Strengths:**
- Exceptional latency: 0.79ms p50 vs 100-500ms for cold-start containers
- Real hardware isolation per sandbox (KVM), not just namespace/cgroup separation
- Tiny memory footprint (265KB per fork)
- Directly addresses "AI agent loops" use case where sandboxes are created at high frequency

**Weaknesses:**
- Prototype ("not production-hardened yet") — acknowledged by author
- Security concerns raised in HN thread: entropy reuse across forks (ASLR becomes predictable across forks if seeds aren't re-randomized), shared `/dev/random` state
- Requires Firecracker and KVM on Linux — not portable to macOS/Windows
- Pre-loaded runtime (Python + NumPy) — not a general dev environment
- Cross-node cloning (distributing forks across machines) is an unsolved problem

**HN thread themes:**
- CodeSandbox uses a similar technique (Firecracker snapshots + `MAP_SHARED` vs `MAP_PRIVATE`) to clone VMs in ~50ms; zeroboot's approach gets another 50x by using `MAP_PRIVATE` instead
- Multiple commenters noted production concerns: entropy reuse, ASLR predictability, missing hardening
- High interest from the AI agent tooling community — startup latency matters for agent loops where sandboxes are created per-task

**vs. yoloAI:**
- Zeroboot optimizes for sandbox creation frequency (AI agent eval loops, code execution per request); yoloAI optimizes for developer workflow safety (copy/diff/apply, persistent project state)
- Complementary: zeroboot could be a future ultra-fast backend for yoloAI's Docker backend if/when Firecracker support is added
- yoloAI's `:overlay` mode (overlayfs CoW) is the filesystem analogue: instant zero-copy setup, changes captured in upper layer, diff/apply at the end
- Neither directly competes — different layers of the stack

**Lessons:**
- **CoW at the VM memory level is the next frontier after overlayfs CoW at the filesystem level.** Our `:overlay` mode shares this "fork from base, capture changes" philosophy — good to reference in positioning.
- **AI agent eval loops are a distinct use case from developer workflows.** Frequency-of-sandbox-creation matters for evaluation pipelines (1000 sandboxes/sec); developer workflows typically need 1-10 sandboxes per session.
- **Entropy/ASLR hardening must be addressed for production VM forking.** If yoloAI ever evaluates Firecracker as a backend, the snapshot-forking approach requires per-fork re-seeding of entropy sources.

---

### 14. Cloud Sandbox Platforms (E2B, Daytona, Modal, Northflank)

These commercial platforms provide infrastructure-level sandboxes for AI agent workloads. They were discussed extensively in the HN thread linked above (item 47412569) and in concurrent discussions. Not direct competitors for yoloAI's developer-workflow use case, but relevant for positioning.

#### E2B

**URL:** [e2b.dev](https://e2b.dev/)

Firecracker microVM-based isolated sandboxes. Python/JS SDKs. ~150ms startup. Sessions up to 24 hours (Pro). $0.05/hr per vCPU. Largest AI agent ecosystem integrations (LangChain, CrewAI, AutoGen, etc.).

**vs. yoloAI:** Cloud-only, no infrastructure costs for user. No copy/diff/apply workflow. Charges per-CPU-hour; yoloAI is free (open-source). Best for production agent pipelines at scale; yoloAI is for individual developer workflow safety.

#### Daytona

**URL:** [daytona.io](https://daytona.io/)

OCI/Docker-based sandboxes with ~90ms cold start. Open-source. Stateful environments with built-in Git integration and LSP support. Can run on BYOC (bring-your-own-cloud). Optional Kata Containers for stronger isolation.

**vs. yoloAI:** Remote infrastructure; yoloAI is local-first. Daytona optimizes for persistent long-running stateful sandboxes; yoloAI optimizes for disposable per-session sandboxes with review-before-apply. Both use Docker/OCI but Daytona requires cloud infrastructure.

#### Modal

**URL:** [modal.com](https://modal.com/)

Serverless platform with gVisor-based container isolation. Sub-second cold starts. Autoscales to 20,000+ concurrent functions. GPU support. Python-centric. Production use: Lovable, Quora running millions of code snippets/day.

**vs. yoloAI:** General ML platform, not agent-workflow specific. Python-only SDK, cannot use arbitrary OCI images. Serverless cloud; yoloAI is local. Granular egress policies are a model for future yoloAI network controls.

#### Northflank

**URL:** [northflank.com](https://northflank.com/)

Complete cloud platform using Kata Containers (lightweight microVMs) or gVisor. 2M+ isolated workloads/month. Unlimited session duration. BYOC support. Any OCI image.

**vs. yoloAI:** Enterprise platform, not a developer tool. Northflank is for teams running production agent pipelines; yoloAI is for individual developers protecting their project files.

**Cross-platform positioning:** All four cloud platforms require cloud infrastructure and charge per compute time. yoloAI runs on the developer's own machine with Docker — $0 for the sandbox infrastructure itself (agent API costs are separate and identical). This cost and control advantage is meaningful for development workflows but less relevant for production pipelines where cloud platforms' autoscaling is necessary.

---

---

### 15. `dangerously` (sayil)

**Source:** [Show HN: Run Claude Code with –dangerously-skip-permissions in a Docker sandbox](https://news.ycombinator.com/item?id=47443990) (2026-03-19)
**Repo:** [github.com/sayil/dangerously](https://github.com/sayil/dangerously)
**Points:** 2 | **Language:** Shell + Dockerfile | **Status:** Active

**What it does:** Minimal shell script + Dockerfile that runs Claude Code in Docker with `--dangerously-skip-permissions`. Mounts `~/.claude.json` and the current working directory. Filesystem isolation only — the creator explicitly acknowledges no network isolation.

**vs. yoloAI:** Near-identical core concept (Claude Code + Docker + `--dangerously-skip-permissions`) but stripped to ~100 lines. No diff/apply, no multi-agent support, no profile system, no copy/overlay modes, no credential scoping, Claude Code-only.

**Threat level: LOW — minimal tool, no workflow differentiation.** Its HN traction (2 pts) vs yoloAI (1 pt) suggests the framing "run Claude Code safely in Docker" resonates, but the diff/apply + multi-agent combination needs to be communicated more prominently.

---

### 16. Railguard (railyard-dev)

**Source:** [Show HN: Railguard – A safer –dangerously-skip-permissions for Claude Code](https://news.ycombinator.com/item?id=47415692) (2026-03-17)
**Repo:** [github.com/railyard-dev/railguard](https://github.com/railyard-dev/railguard)
**Language:** Rust | **Status:** Active (151 passing tests)

**What it does:** In-process runtime interceptor for Claude Code tool calls. Uses semantic rules + OS-level sandboxing (bubblewrap on Linux, sandbox-exec on macOS). Intercepts bash commands, file operations, memory writes in <2ms. Includes session replay dashboard, file snapshots, rollback recovery. Explicitly avoids LLM-based detection to prevent circular vulnerabilities.

**Weaknesses:**
- Claude Code-specific only — no multi-agent support
- In-process/syscall-level enforcement: Ona research (2026-03-18) demonstrated Claude Code autonomously bypassing bubblewrap and syscall hooks via the ELF dynamic linker — rule-based semantic filters are not sufficient against a reasoning agent
- No diff/apply workflow — rollback is after-the-fact
- No container-level isolation

**Threat level: LOW-MEDIUM — more granular than yoloAI but architecturally fragile.** The Ona research published the same week Railguard launched validates that enforcement must live outside the agent's reasoning loop entirely. Container-level isolation is more resistant than in-process interception.

---

### 17. Veto (vetoapp.io)

**Source:** [Show HN: Veto – Permission policy engine and LLM firewall for AI coding agents](https://news.ycombinator.com/item?id=47426020) (2026-03-18)
**URL:** [vetoapp.io](https://vetoapp.io/)
**Pricing:** $0 free / $29 team / $99 business per user/month

**What it does:** Managed permission gateway for AI coding agents. Two enforcement modes: (1) lightweight Python hook into Claude Code's permission system; (2) LiteLLM proxy for tamper-proof enforcement. Dashboard-based allow/deny/ask rule management.

**Weaknesses:**
- Paid SaaS — ongoing cost for a workflow tool
- Mode 1 (hook) is inside the agent's reasoning loop — same architectural weakness as Claude Code's built-in sandbox
- No diff/apply workflow
- No container-level isolation
- Enterprise governance focus — not individual developer workflow

**Threat level: LOW — different target user and pricing model.** Veto targets enterprise teams needing centralized policy management. yoloAI targets individual developers wanting copy/diff/apply isolation with zero ongoing cost.

---

### 18. Docker Sandboxes (Docker, Inc.)

**Source:** [Docker blog: Run Claude Code and other coding agents, unsupervised but safely](https://www.docker.com/blog/docker-sandboxes-run-claude-code-and-other-coding-agents-unsupervised-but-safely/) (announced January 30, 2026)
**URL:** docker.com

**What it does:** Official Docker product providing microVM-based isolation for AI coding agents. Supports Claude Code, Gemini CLI, Codex, and Kiro. Runs agents unsupervised inside isolated environments. Part of Docker Desktop.

**Strengths:**
- Massive distribution advantage — Docker Desktop has millions of installed users
- Brand trust with developer audience
- Built-in microVM isolation (stronger than standard containers)
- Multi-agent support (Claude Code, Gemini, Codex, Kiro)
- No additional tooling to install for Docker Desktop users

**Weaknesses:**
- No diff/apply workflow — no mechanism to review changes before they land on the host
- Requires Docker Desktop (commercial licensing for teams)
- No profile system or per-project configuration
- No copy/overlay/rw directory modes
- No credential scoping or file-based secrets injection

**Threat level: HIGH — biggest structural threat to yoloAI.** Docker has the distribution, brand trust, and agent support list. It will absorb the "sandboxed agent runner" use case for mainstream users who don't need the diff/apply workflow. yoloAI must win on workflow depth: diff/apply, copy/overlay modes, profile system, and credential isolation are features Docker Sandboxes does not have.

---

### 19. Microsandbox (zerocore-ai)

**Source:** [github.com/zerocore-ai/microsandbox](https://github.com/zerocore-ai/microsandbox)
**Status:** Active | **Language:** Rust

**What it does:** Hardware-level microVM isolation with <200ms boot time. OCI-compatible. MCP server built-in (auto-discovered by Claude Desktop and VS Code). Self-hostable, open-source. Sandboxfile configuration similar to Dockerfile.

**vs. yoloAI:** MCP-first integration (distribution channel yoloAI doesn't use). No diff/apply workflow. Hardware-level isolation is stronger than containers but requires KVM. macOS not supported. Targets code execution use case, not persistent project file management.

**Threat level: LOW-MEDIUM.** A future yoloAI Firecracker backend would overlap more directly. Relevant for the MCP integration angle.

---

| Feature | deva.sh | TextCortex | Docker Sandbox | rsh3khar | cco | sandbox-runtime | Safehouse | yolobox | EnvPod CE | BunkerVM | **yoloAI** |
|---------|---------|------------|----------------|----------|-----|-----------------|-----------|--------|-----------|----------|-----------------|
| Copy/diff/apply workflow | No | No (git branch + diff review) | No (file sync) | No (auto-commit) | No | No | No | No | Yes (overlayfs) | No | **Yes** |
| Per-sandbox agent state | No | No | No | No | No | No | No | Yes (persistent volumes) | Yes | No (ephemeral) | **Yes** |
| Session logging | No | Web terminal | No | No | No | No | No | No | Yes (JSONL audit) | Yes (JSONL audit) | **Yes** |
| User-supplied Dockerfiles | No | Custom Dockerfile | Templates | No | No | N/A (no container) | N/A | No | No (YAML config) | No (rootfs only) | **Yes** |
| Multi-directory with primary/dep | Partial | No | No | Worktrees | No | N/A | No | No | Yes (bind mounts) | No | **Yes** |
| Review before applying changes | No | Diff review (git) | No | No | No | No | No | No | Yes (diff/commit/rollback) | No | **Yes (core feature)** |
| Multi-backend isolation | No | No | MicroVM | No | Yes (sandbox-exec/bwrap/Docker) | Yes (bwrap/Seatbelt) | No (sandbox-exec only) | Yes (Docker/Podman/Apple) | No (native Linux only) | No (Firecracker only) | **Yes (Docker/Tart/Seatbelt)** |
| No Docker dependency | No | No | Docker Desktop | No | Yes (native modes) | Yes | Yes | Partial (Podman/Apple) | Yes (no Docker) | Yes (Firecracker) | Partial (Seatbelt mode) |
| Dynamic toolchain detection | No | No | No | No | No | Yes (glob patterns) | Yes | No | No | No | No |
| Per-agent compatibility docs | No | No | No | No | No | No | Yes | No | Yes (presets) | No | No |
| Network disable flag | No | No | Proxy-based | No | No | Yes | No | Yes (`--no-network`) | Yes (per-pod DNS) | Yes (vsock airgap default) | **Yes** |
| Governance (action approval) | No | No | No | No | No | No | No | No | Yes (4-tier queue) | No | No |
| Encrypted credential vault | No | No | No | No | No | No | No | No | Yes (ChaCha20 + proxy) | No | No |
| Snapshots/checkpoints | No | No | No | No | No | No | No | No | Yes | No | No |
| Web dashboard | No | Yes | No | No | No | No | No | No | Yes | Yes (BunkerDesktop) | No |
| MCP server | No | No | No | No | No | No | No | No | No | Yes (native) | No |
| Hardware isolation (KVM) | No | No | No | No | No | No | No | No | No | Yes (Firecracker) | No |
| Parallel multi-sandbox | No | No | No | No | No | No | No | No | No | Yes (VMPool, 10x) | Planned |
| macOS support | Yes | Yes | Yes | Yes | Yes | Yes | Yes | Yes | No | No | **Yes** |

---
