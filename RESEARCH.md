# Competitive Landscape Research: Claude Code Sandboxing Tools

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

**nono** ([always-further/nono](https://github.com/always-further/nono)): Kernel-level isolation via Landlock (Linux) / Seatbelt (macOS). Irreversible restrictions. Basic audit trail with session JSON and merkle roots (signed attestation is "Coming Soon" per issues #130, #127). Created 2026-01-31, self-described "early alpha." Most security-focused option in concept.

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

**Approach categories not fully explored:** macOS `sandbox-exec` tools (5+ projects), Nix-based isolation, micro-VM alternatives (boxlite, Fly.io Sprites, E2B), commercial platforms (E2B, Daytona, Fly.io Sprites, Northflank, Vercel Sandbox), HTTP API wrappers (agentapi, sandbox-agent).

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

| Feature | deva.sh | TextCortex | Docker Sandbox | rsh3khar | cco | sandbox-runtime | **yolo-claude** |
|---------|---------|------------|----------------|----------|-----|-----------------|-----------------|
| Copy/diff/apply workflow | No | No (git branch + diff review) | No (file sync) | No (auto-commit) | No | No | **Yes** |
| Per-sandbox Claude state | No | No | No | No | No | No | **Yes** |
| Session logging | No | Web terminal | No | No | No | No | **Yes** |
| User-supplied Dockerfiles | No | Custom Dockerfile | Templates | No | No | N/A (no container) | **Yes** |
| Multi-directory with primary/dep | Partial | No | No | Worktrees | No | N/A | **Yes** |
| Review before applying changes | No | Diff review (git) | No | No | No | No | **Yes (core feature)** |
| Multi-backend isolation | No | No | MicroVM | No | Yes (sandbox-exec/bwrap/Docker) | Yes (bwrap/Seatbelt) | No (Docker only) |
| No Docker dependency | No | No | Docker Desktop | No | Yes (native modes) | Yes | No |

---

## Alternative Filesystem Isolation Approaches

The current design uses full directory copies (`:copy` mode) with git-based diffing. Several copy-on-write (COW) filesystem technologies could replace or complement this approach, trading portability for speed and space efficiency.

### OverlayFS

The most directly applicable alternative. Layers a writable "upper" directory over a read-only "lower" directory; changes (including deletions via whiteout character devices) go to upper only.

```
mount -t overlay overlay \
  -o lowerdir=/original,upperdir=/changes,workdir=/tmp/ovl \
  /merged
```

**How it maps to yolo-claude's workflow:**
- **Setup:** Instant mount instead of full copy — zero startup time regardless of project size.
- **Diff:** The upper directory *is* the diff. [overlayfs-tools](https://github.com/kmxz/overlayfs-tools) provides `diff` and `merge` commands for extracting/applying changes. Whiteout files represent deletions natively.
- **Apply:** Copy upper directory contents back to original, interpreting whiteouts as deletions.
- **Space:** Only modified/created files consume disk.

**Requirements inside Docker:**
- `CAP_SYS_ADMIN` capability (not full `--privileged`).
- Kernel 5.11+ for unprivileged overlayfs in user namespaces; `-o userxattr` mount option uses `user.overlay.*` instead of `trusted.overlay.*`.
- Nested overlayfs-on-overlayfs (Docker uses overlay2 internally) works on kernel 5.11+ but is unreliable on older kernels. Fallback: `fuse-overlayfs` (userspace implementation, available via package managers).

**Limitations:**
- **Linux only.** No macOS equivalent (macOS `mount -t union` is broken since Sierra and effectively abandoned by Apple).
- File timestamps may not be preserved during copy-up. Metacopy feature (kernel `CONFIG_OVERLAY_FS_METACOPY`) optimizes metadata-only changes but has security caveats with untrusted directories.
- 128 lower layer limit (practical limit ~122 due to mount option size); irrelevant for our use case (single lower layer).
- CVE-2023-2640 and CVE-2023-32629: privilege escalation via overlayfs xattrs — mitigated in patched kernels.

### ZFS Snapshots and Clones

`zfs snapshot` + `zfs clone` creates instant COW copies sharing all blocks with the parent. Zero initial space consumption.

**Strengths:**
- Snapshot creation: milliseconds, regardless of dataset size.
- `zfs diff` shows changed files (format: `M /path`, `+ /path`, `- /path`, `R /old -> /new`). Note: shows *which* files changed, not content diffs — still need traditional diff tools for content.
- ARC (Adaptive Replacement Cache) sharing means multiple clones of the same data are very cache-friendly.
- Docker has a ZFS storage driver that uses ZFS datasets for image layers.

**Critical limitation for our use case:** No merge-back primitive. `zfs promote` reverses the clone/parent relationship (replacement, not merge). Applying changes back requires file-level tools (rsync, patch). This negates much of the benefit — we'd still need git or rsync for the apply step.

**Availability:**
- Linux: Ubuntu ships ZFS kernel module; Red Hat and SUSE exclude it (CDDL/GPL license conflict). Manual install on Fedora, Debian, Arch.
- macOS: OpenZFS on OS X exists but is experimental/community-maintained. Apple dropped native ZFS in 2009.
- Windows: OpenZFS on Windows is beta quality with stability issues.
- **Verdict:** Too niche for a general-purpose tool. Requires the host filesystem to be ZFS — can't be assumed.

### Btrfs Subvolume Snapshots

Similar COW semantics to ZFS. `btrfs subvolume snapshot` is instant and writable by default.

**Strengths:**
- GPL-licensed, mainline Linux kernel — no licensing issues.
- `btrfs send/receive` can extract incremental changes between snapshots (binary instruction stream, not text diffs).
- Docker has a Btrfs storage driver.
- Writable snapshots by default (more flexible than ZFS which requires snapshot → clone).

**Limitations:**
- Same merge-back problem as ZFS — no built-in way to selectively apply changes back.
- Send/receive produces a binary stream, not standard diffs.
- RAID5/6 still not production-ready in 2026.
- Performance degrades with quota groups (qgroups) enabled.
- Requires host filesystem to be Btrfs — even less common than ZFS in practice.

### APFS Clones (macOS)

macOS APFS supports instant file-level COW clones via `cp -c`. Clones share data blocks until modified.

**Strengths:**
- Native on all modern macOS. No additional software.
- Instant regardless of file size.
- Works at file granularity — could clone individual project files.

**Limitations:**
- File-level only, no directory-level overlay semantics. Can't layer a writable view over a read-only base.
- APFS snapshots are volume-level and read-only — can't mount as writable overlays.
- Doesn't help with diff/apply workflow — you'd still need git or rsync to identify changes.
- `tmutil` only works on the boot drive.

### FUSE-Based Overlays

**fuse-overlayfs:** Userspace overlayfs implementation. Primary use: rootless containers on Linux. Same logical behavior as kernel overlayfs but runs as a FUSE daemon.
- 2-3x slower than kernel overlayfs due to userspace context switches.
- Uses reflinks on XFS/Btrfs for efficient copy-up.
- Available via package managers. Good fallback when kernel overlayfs isn't available unprivileged.

**unionfs-fuse:** Works on macOS via macFUSE or FUSE-T (kextless, NFS-based, better performance than macFUSE). However, known issues with Finder compatibility (file copy operations fail with error -50). Works for command-line use cases.

**FUSE-T:** Modern macOS FUSE implementation using NFSv4 local server instead of kernel extension. Better performance and stability than macFUSE. No kernel crashes or lock-ups. macOS 26 adds native FSKit backend as an alternative.

### Bubblewrap

Lightweight Linux sandboxing tool (used by Flatpak). Has built-in overlay support:

```
bwrap --overlay-src /original --overlay /upper /work /merged -- /bin/bash
```

- Unprivileged via user namespaces. No daemon. No Docker dependency.
- Requires Linux 4.0+ for overlay support.
- Production-ready (powers Flatpak sandboxing).
- Could be used as a lighter-weight alternative to Docker for Linux-only deployments, though outside yolo-claude's current Docker-based architecture.

### Docker's Own Container Diff

`docker diff <container>` tracks all filesystem changes in the container's writable layer. On its own, too noisy for code review — it captures everything (installed packages, temp files, logs), not just project changes. However, combining it with git inside the container solves the noise problem (see Recommendation below).

### Comparison for yolo-claude's `:copy` Workflow

| Approach | Setup time | Space | Diff mechanism | Apply-back | macOS | Needs special host FS |
|----------|-----------|-------|----------------|------------|-------|----------------------|
| **Current (full copy + git)** | Slow (proportional to size) | Full duplicate | git diff | git patch | Yes | No |
| **OverlayFS + git** | Instant | Deltas only | git diff (ignores non-project noise) | git patch | No | No |
| **ZFS clone** | Instant | Deltas only | `zfs diff` (file list only) | Manual (rsync/patch) | Experimental | Yes (ZFS) |
| **Btrfs snapshot** | Instant | Deltas only | `btrfs send` (binary stream) | Manual (rsync/patch) | No | Yes (Btrfs) |
| **APFS clone** | Instant (file-level) | Deltas only | None built-in | Manual | Yes (native) | No (but macOS only) |
| **FUSE overlay + git** | Instant | Deltas only | git diff | git patch | Partial (CLI only) | No |

### Recommendation

**OverlayFS for the mount, git for the diff** — combining both techniques gives the best result:

1. Mount the original project directory as the overlayfs lower layer (read-only).
2. Empty upper directory receives all writes — instant setup, no copy, space-efficient.
3. `git init` + `git add -A` + `git commit` on the merged view to create the baseline.
4. Claude works on the merged view. Changes go to the upper layer, but git only tracks project files — installed packages in `/usr/lib`, temp files in `/tmp`, and other system-level noise are outside the git tree and invisible to `git diff`.
5. `yolo diff` uses `git diff` as today — clean, familiar output.
6. `yolo apply` uses `git diff | git apply` as today — handles additions, modifications, and deletions.

This eliminates the upfront copy (the main performance cost for large projects) while keeping the same git-based diff/apply workflow. No whiteout interpretation needed, no new diff format to parse — git handles everything.

**Requirements:** `CAP_SYS_ADMIN` inside the Docker container (or kernel 5.11+ with user namespaces and `-o userxattr`). Both are available in our architecture since we control the `docker run` invocation.

**Cross-platform story:**
- **Linux:** OverlayFS + git. Instant setup, deltas-only storage.
- **macOS:** Falls back to current full copy + git. Docker on macOS runs a Linux VM, so overlayfs *could* work inside the container even on a macOS host — needs validation. If it works, the optimization is transparent and cross-platform.
- **Older kernels:** Falls back to full copy + git.

This is a **post-v1 optimization** — the current full-copy approach works everywhere and is simpler to implement and debug. The overlayfs optimization is worth adding once the core workflow is stable, primarily to improve startup time and reduce disk usage for large projects.

ZFS and Btrfs are too host-dependent to serve as primary mechanisms (require the host filesystem to be ZFS/Btrfs). APFS clones lack overlay semantics. FUSE-based overlays on macOS have reliability concerns (Finder issues, FUSE-T maturity) but could serve as an intermediate option if kernel overlayfs isn't available.

---

## Design Implications

### Must-haves informed by research:
1. **Inject a sandbox context file** telling Claude about its environment (stolen from rsh3khar)
2. **Double Enter for tmux send-keys** — Claude Code requires it
3. **Sleep delay** between launching Claude and sending the prompt
4. **Non-root execution** — create a user inside the container to avoid the root rejection
5. **CLAUDE.md must carry over** — top complaint about Docker Sandboxes
6. **Read-only mounts for dependency dirs** by default (stolen from deva.sh)
7. **License the project on day one** (learned from TextCortex)
8. **Cross-platform testing** in CI from the start

### Should-haves:
9. **Periodic git commits inside sandbox copy** (like rsh3khar's 60s auto-commit) for recovery
10. **SSH agent forwarding** option instead of mounting keys (stolen from claude-compose-sandbox)
11. **Dangerous directory detection** — warn if mounting `$HOME` or system dirs (stolen from deva.sh)
12. **Network policy option** — at minimum, support `--network none` for paranoid mode

### Nice-to-haves (post-v1):
13. **Credential proxy** instead of passing env vars directly
14. **Notification on Claude exit** (webhook, file flag)
15. **Port forwarding** for web dev workflows
16. **Multi-agent support** (multiple Claude instances in one sandbox)
