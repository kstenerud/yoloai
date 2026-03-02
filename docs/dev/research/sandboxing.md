# Sandbox Isolation Technologies

## Alternative Filesystem Isolation Approaches

The design offers two directory modes for copy/diff/apply: `:copy` (full directory copy) and `:overlay` (explicit overlayfs opt-in). This section documents the research into copy-on-write (COW) filesystem technologies that informed that design.

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
- **Outcome:** `:copy` uses full directory copies (portable, no special capabilities). `:overlay` is an explicit opt-in for overlayfs (instant setup, space-efficient, requires `CAP_SYS_ADMIN`). See the [design docs](../design/commands.md) for the full specification.

ZFS and Btrfs are too host-dependent to serve as primary mechanisms (require the host filesystem to be ZFS/Btrfs). APFS clones are not instant for directories. FUSE-based overlays on macOS have reliability concerns (Finder issues, FUSE-T maturity). Docker volumes eliminate VirtioFS overhead but don't save setup time.

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

---

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
