# Implementation Research

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
