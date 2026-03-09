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

**Approach categories not fully explored:** macOS `sandbox-exec` tools (5+ projects), Nix-based isolation, micro-VM alternatives (boxlite, Fly.io Sprites, E2B), commercial platforms (E2B, Daytona, Fly.io Sprites, Northflank, Vercel Sandbox), HTTP API wrappers (agentapi, sandbox-agent).

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

## Community Pain Points (from GitHub issues, Reddit, HN, blogs)

### Top complaints (ranked by frequency):
1. **Approval fatigue** — constant permission prompts break flow, even "always allow" doesn't stick
2. **Root user rejection** — `--dangerously-skip-permissions` refuses to run as root in containers (issues #927, #3490, #5172)
3. **Network whitelist is HTTP-only** — SSH/TCP connections can't traverse the proxy (#11481, #24091)
4. **Credential management fragility** — getting API keys/SSH keys/git config into containers reliably
5. **User config not carried over** — CLAUDE.md, plugins, skills lost in sandboxes
6. **No middle ground** between "approve everything" and "skip everything"
7. **Cross-platform inconsistency** — works on Linux, breaks on macOS/Windows
8. **Data exfiltration risk** — agent has same network access as developer

### What users wish existed:
- A clean middle ground: sandbox provides safety, Claude operates freely within it
- Granular network controls (domain + port level, not just HTTP)
- Workspace-only filesystem confinement
- Audit logging for all actions
- Time-scoped credentials that auto-expire
- Multi-agent management from a single interface

### HN thread insights (2026-03-09, Agent Safehouse, 471 points, 109 comments):

**"Sandboxing is necessary but insufficient"** — strong consensus that filesystem/process restrictions alone don't prevent agents from operating "perfectly within permissions and still producing garbage" (devonkelley). Three additional layers were discussed:

1. **Credential scoping** (silverstream): "An agent inside sandbox-exec still has your AWS keys, GitHub token, whatever's in the environment." Solution: scoped short-lived JWTs instead of raw credentials. Clawvisor (Go project) implements this as a credential proxy that injects credentials server-side — agents never see the raw tokens.

2. **Dynamic permission shrinking** (zmmmmm): Sandbox permissions should contract when agents are "tainted" by untrusted content (e.g., reading a malicious file or processing untrusted input). This is the "confused deputy" problem — a sandboxed agent with legitimate credentials can be tricked into misusing them.

3. **Tool-level auth scoping at MCP layer** (gnanagurusrgs, Arcade): Sandboxing the runtime alone is insufficient; each tool/MCP server needs individually scoped authorization with JIT OAuth. The granularity should be at the tool call level, not the process level.

4. **Supervisor agent frameworks** (nemo44x): Sandboxing prevents catastrophic damage but doesn't prevent cascading failures from hallucinations or goal drift. A supervisor agent (or human-in-the-loop) that monitors *what* the agent is doing, not just *what it can access*, is needed for production reliability.

**Relevance to yoloAI:** Our copy/diff/apply workflow already addresses the "produces garbage" problem — changes are reviewed before landing. The credential scoping discussion reinforces that our file-based secrets injection is better than env vars, but could be improved with short-lived/scoped tokens. The supervisor/monitoring angle aligns with our idle detection and status monitoring work.

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

---

## Feature Comparison

*Note: This table covers a subset of the landscape. See section 8 for additional tools.*

| Feature | deva.sh | TextCortex | Docker Sandbox | rsh3khar | cco | sandbox-runtime | Safehouse | yolobox | **yoloAI** |
|---------|---------|------------|----------------|----------|-----|-----------------|-----------|--------|-----------------|
| Copy/diff/apply workflow | No | No (git branch + diff review) | No (file sync) | No (auto-commit) | No | No | No | No | **Yes** |
| Per-sandbox agent state | No | No | No | No | No | No | No | Yes (persistent volumes) | **Yes** |
| Session logging | No | Web terminal | No | No | No | No | No | No | **Yes** |
| User-supplied Dockerfiles | No | Custom Dockerfile | Templates | No | No | N/A (no container) | N/A | No | **Yes** |
| Multi-directory with primary/dep | Partial | No | No | Worktrees | No | N/A | No | No | **Yes** |
| Review before applying changes | No | Diff review (git) | No | No | No | No | No | No | **Yes (core feature)** |
| Multi-backend isolation | No | No | MicroVM | No | Yes (sandbox-exec/bwrap/Docker) | Yes (bwrap/Seatbelt) | No (sandbox-exec only) | Yes (Docker/Podman/Apple) | **Yes (Docker/Tart/Seatbelt)** |
| No Docker dependency | No | No | Docker Desktop | No | Yes (native modes) | Yes | Yes | Partial (Podman/Apple) | Partial (Seatbelt mode) |
| Dynamic toolchain detection | No | No | No | No | No | Yes (glob patterns) | Yes | No | No |
| Per-agent compatibility docs | No | No | No | No | No | No | Yes | No | No |
| Network disable flag | No | No | Proxy-based | No | No | Yes | No | Yes (`--no-network`) | **Yes** |

---
