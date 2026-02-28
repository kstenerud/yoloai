> **Design documents:** [Commands](commands.md) | [Config](config.md) | [Setup](setup.md) | [Security](security.md) | [Research](../dev/RESEARCH.md)

# yoloAI: Sandboxed AI Coding Agent Runner

## Goal

Run AI coding CLI agents (Claude Code, Codex, and others) with their sandbox-bypass flags inside disposable, isolated containers so that the agent can work autonomously without constant permission prompts. Project directories are presented as isolated writable views inside the container. The user reviews changes via `yoloai diff` and applies them back to the originals via `yoloai apply` when satisfied.

**Scope:** Currently ships with Claude Code and a deterministic test agent. OpenAI Codex, overlay strategy, network isolation, profiles, Viper config, and aux dirs are planned. The architecture is agent-agnostic — Docker, overlayfs, network isolation, diff/apply are not agent-specific. Adding a new agent requires only a new agent definition (install command, launch command, API key env vars, state directory). See [RESEARCH.md](../dev/RESEARCH.md) "Multi-Agent Support Research" for additional agents researched.

## Value Proposition

**The gap in the market:** Existing tools either give the agent live access to your files (risky — one bad command and your work is gone) or provide isolation without a review workflow (you have to manually figure out what changed). No tool lets an agent work freely while protecting your originals and giving you a clean review step before changes land.

**Core differentiator — copy/diff/apply:** yoloAI is the only tool where your originals are protected by default. The agent works on an isolated copy; you review exactly what changed via `yoloai diff` and choose what to keep via `yoloai apply`. Every other tool in the space (Docker Sandboxes, cco, deva.sh, sandbox-runtime) either live-mounts your files or syncs changes immediately.

**What yoloAI does that nobody else does:**

- **Copy/diff/apply workflow.** Protected originals, git-based diffs, explicit apply. Docker Sandboxes does bidirectional file sync (immediate, no review). cco and deva.sh are live-mount only. The archived TextCortex project came closest with git branch + diff review, but is gone.
- **Per-sandbox agent state.** Each sandbox gets its own agent configuration and session history. Every other tool either shares the host's agent config (losing isolation) or starts fresh (losing context). yoloAI's `agent_files` system seeds each sandbox independently.
- **Workdir + aux dir model with per-directory access control.** Explicit separation of the primary project (`:copy` or `:rw`) from dependencies (read-only by default). Most tools mount a single directory. deva.sh has partial multi-directory support but no workdir/aux distinction.
- **Profile system with user-supplied Dockerfiles.** Reproducible environments with full flexibility — not limited to package lists or pre-built templates. Paired with `profile.yaml` for runtime config (mounts, directories, resources).

**Strong but shared with some competitors:**

- **Network isolation with domain allowlisting.** Docker Sandboxes and sandbox-runtime also have this. yoloAI's layered approach (internal network + proxy sidecar + iptables + DNS control) is the most thorough among open-source options.
- **Agent-agnostic design.** deva.sh and cco also support multiple agents. yoloAI's agent definition abstraction is cleaner but the capability isn't unique.
- **Session logging.** Several tools have some form of this.

**Where competitors are stronger:**

- **Docker Sandboxes:** Hypervisor-level isolation (stronger than containers), credential proxy (key never enters VM). But: no user config carryover, no port forwarding, broken OAuth, ~3x I/O penalty on macOS.
- **cco:** Zero-config UX, multi-backend (no Docker needed on macOS/Linux), Keychain integration. But: exposes entire host filesystem read-only, no copy/diff/apply, no profiles.
- **sandbox-runtime:** Native OS-level isolation, no Docker needed, largest community (3k+ stars). But: known bypass routes (DNS exfiltration, proxy bypass), Claude can autonomously disable it.

**The pitch:** yoloAI is the only tool that lets an AI agent work freely in a disposable sandbox while protecting your originals — you review exactly what changed and choose what to keep.

## Architecture

```
┌────────────────────────────────────────────────────────────┐
│  Host (any machine with Docker)                            │
│                                                            │
│  yoloai CLI (Go binary)                                    │
│    │                                                       │
│    ├─ docker run ──► sandbox-1  ← ephemeral                │
│    │                  ├ tmux                               │
│    │                  ├ AI agent (Claude, Codex, ...)      │
│    │                  ├ project dirs (mirrored host paths) │
│    │                  └ agent state (per-sandbox)          │
│    │                                                       │
│    ├─ docker run ──► sandbox-2                             │
│    └─ ...                                                  │
│                                                            │
│  ~/.yoloai/sandboxes/<name>/  ← persistent state           │
│    ├── work/          (overlay upper dirs or full copies)  │
│    ├── agent-state/  (agent's state directory)             │
│    ├── log.txt        (session output)                     │
│    ├── prompt.txt     (initial prompt)                     │
│    └── meta.json      (config, paths, status)              │
└────────────────────────────────────────────────────────────┘
```

### Container Technology: Docker

- Works natively on Linux/macOS, inside LXC with Proxmox nesting enabled, etc.
- Provides process, filesystem, and network namespace isolation
- Ephemeral by default — `docker rm` and it's gone
- All persistent state lives on the host in `~/.yoloai/sandboxes/`

### Key Principle: Containers are ephemeral, state is not

The Docker container is disposable — it can crash, be destroyed, be recreated. Everything that matters lives in the sandbox's state directory on the host:
- **`work/`** — copies of project directories (what the agent modifies)
- **`agent-state/`** — the agent's state directory (session history, settings)
- **`prompt.txt`** — the initial prompt to feed the agent
- **`log.txt`** — captured tmux output for post-mortem review
- **`meta.json`** — sandbox configuration captured at creation time. Used by all lifecycle commands to reconstruct the container environment on restart.

**`meta.json` schema:**

```json
{
  "yoloai_version": "1.0.0",
  "name": "fix-build",
  "created_at": "2025-01-15T10:30:00Z",

  "agent": "claude",
  "profile": "go-dev",                    // [PLANNED] profile name
  "model": "claude-sonnet-4-5-20250929",

  "copy_strategy": "overlay",             // [PLANNED] "overlay" | "full"

  "network": {
    "mode": "isolated",                   // Currently a flat "network_mode" string ("none" or "")
    "allow": ["api.anthropic.com", "statsig.anthropic.com", "sentry.io"]  // [PLANNED]
  },

  "workdir": {
    "host_path": "/home/user/projects/my-app",
    "mount_path": "/home/user/projects/my-app",
    "mode": "copy",
    "baseline_sha": "a1b2c3d4..."
  },

  "directories": [                        // [PLANNED] aux dirs
    {
      "host_path": "/home/user/projects/shared-lib",
      "mount_path": "/usr/local/lib/shared",
      "mode": "rw"
    },
    {
      "host_path": "/home/user/projects/common-types",
      "mount_path": "/home/user/projects/common-types",
      "mode": "ro"
    }
  ],

  "has_prompt": true,

  "ports": ["8080:8080"],
  "resources": {                          // [PLANNED] stored in meta; currently applied from config only
    "cpus": 4,
    "memory": "8g"
  }
}
```

Field notes:
- **No `status` field.** Container state (`running`/`stopped`/`exited`) is queried live from Docker, not stored. Runtime state (whether agent files have been initialized) is tracked separately in `state.json` alongside `meta.json` — no `state.json` = not initialized.
- **`baseline_sha`** — always present for `:copy` dirs. For git repos, the HEAD SHA at copy time. For non-git dirs, the SHA of the synthetic initial commit (`git init` + `git add -A` + `git commit`). Never null — `yoloai diff` always uses `git diff <baseline_sha>` with no special cases.
- **`network.mode`** — `"none"`, `"isolated"`, or `"default"`. Drives proxy sidecar lifecycle.
- **`network.allow`** — the fully resolved allowlist (agent defaults + config + CLI), stored so the proxy can be recreated on restart.
- **`model`** — `null` if the agent's default was used.
- **`has_prompt`** — whether `prompt.txt` exists. The prompt content lives in `prompt.txt`, not here.
- **`workdir` and `directories`** store the resolved state at creation time. The `work/` subdirectory for each `:copy` dir is derived from `host_path` via caret encoding (not stored).

## Directory Layout

```
~/.yoloai/
├── config.yaml                  ← global defaults (no profiles — those live in profiles/)
├── Dockerfile                   ← seeded from embedded defaults, user-editable
├── entrypoint.sh                ← seeded from embedded defaults, user-editable
├── cache/
│   └── overlay-support          ← cached overlay detection result
├── extensions/
│   ├── lint.yaml                ← user-defined extension (one file per command)
│   └── review.yaml
├── profiles/
│   ├── go-dev/
│   │   ├── Dockerfile           ← FROM yoloai-base
│   │   └── profile.yaml        ← runtime config (mounts, env, resources, workdir, directories)
│   └── node-dev/
│       ├── Dockerfile
│       └── profile.yaml
└── sandboxes/
    └── <name>/
        ├── meta.json            ← original paths, mode, profile, timestamps
        ├── config.json          ← entrypoint configuration (bind-mounted into container)
        ├── state.json           ← runtime state (agent files initialized, etc.)
        ├── prompt.txt           ← initial prompt (if provided)
        ├── log.txt              ← tmux session log
        ├── agent-state/         ← agent's state directory (per-sandbox, read-write)
        └── work/                ← overlay upper dirs (deltas) or full copies, for :copy dirs only
            ├── ^2Fhome^2Fuser^2Fmy-app/    ← caret-encoded host path
            └── ^2Fhome^2Fuser^2Fshared/    ← (one subdir per :copy directory)
```

## Prerequisites

- Docker installed and running (clear error message if Docker daemon is not available)
- Distribution: binary download from GitHub releases, `go install`, or Homebrew. No runtime dependencies — Go compiles to a static binary.
- API key for your chosen agent set in environment (`ANTHROPIC_API_KEY` for Claude, `CODEX_API_KEY` or `OPENAI_API_KEY` for Codex)
- If running from inside an LXC container: nesting enabled (`security.nesting=true`). Unprivileged containers also need `keyctl=1` (Proxmox: `features: nesting=1,keyctl=1`). Available on any LXC/LXD host — Proxmox exposes this as a checkbox, but it's a standard LXC feature. Note: runc 1.3.x has known issues with Docker inside LXC containers.
- **Windows/WSL:** Expected to work via Docker Desktop + WSL2. Known limitations: path translation between Windows and WSL paths, UID/GID mapping differences, `.gitignore` line ending handling. Not a primary target but should degrade gracefully.

## Resolved Design Decisions

1. ~~**Headless mode?**~~ Agent definition specifies prompt delivery mode. Claude always uses interactive mode via tmux (`--prompt` fed via `tmux send-keys`). Codex uses headless mode (`codex exec`) when `--prompt` is provided, interactive mode otherwise. Tmux used in all cases for logging and attach.
2. ~~**Multiple mounts?**~~ Yes. Workdir is primary (cwd), aux dirs are dependencies. Error on container path collision.
3. ~~**Dotfiles/tools?**~~ Config file with defaults + profiles. Profiles use user-supplied Dockerfiles for full flexibility.
4. ~~**Resource limits?**~~ [PLANNED] Will be configurable in `config.yaml` under `defaults.resources`.
5. ~~**Auto-destroy?**~~ No. Sandboxes persist until explicitly destroyed.
6. ~~**Git integration?**~~ Yes. Copy mode auto-inits git for clean diffs. `yoloai apply` excludes `.git/`.
7. ~~**Default mode?**~~ All dirs read-only by default. Per-directory `:rw` (live) or `:copy` (staged) suffixes. Workdir defaults to `:copy` if no suffix given; `:rw` must be explicit.
8. ~~**Container work directory?**~~ Directories are mounted at their original host paths (mirrored) by default, so configs, error messages, and symlinks work without translation. Custom paths available via `=<path>` override. `/yoloai/` reserved for internals. The `/work` prefix was considered but rejected — path consistency (matching host paths) outweighs the minor safety benefit, and dangerous directory detection already prevents mounting over system paths.
9. ~~**Copy strategy?**~~ OverlayFS by default (`copy_strategy: auto`). The original directory is bind-mounted read-only as the overlayfs lower layer; writes go to an upper directory in sandbox state. Git provides diff/apply on the merged view. Falls back to full copy if overlayfs isn't available. Works cross-platform — Docker on macOS/Windows runs a Linux VM, so overlayfs works inside the container regardless of host OS. VirtioFS overhead for macOS host reads is acceptable (70-90% native after page cache warms). Config option `copy_strategy: full` available for users who prefer the traditional full-copy approach or want to avoid `CAP_SYS_ADMIN`.
10. ~~**Config template generation?**~~ Addressed by `yoloai config get/set`. `config get` shows all known settings with effective values (defaults + overrides). Agent-specific config generation deferred to v2.

## Design Considerations

### [PLANNED] Overlay + existing `.git/` directories

When the original directory is a git repo, `.git/` is in the overlay lower layer (read-only). Git operations inside the sandbox (add, commit, etc.) write to `.git/` internals (objects, index, refs), and these writes go to the overlay upper directory via copy-on-write. The agent sees the full project; writes go to upper only. This means: (a) the upper directory will contain modified `.git/` files alongside project changes, and (b) `yoloai diff` must diff against the *original* repo's HEAD SHA (recorded in `meta.json`), not whatever HEAD the sandbox has moved to. This works correctly because `meta.json` records the original HEAD at sandbox creation time, and `yoloai diff` uses `git diff <original-HEAD>` regardless of subsequent commits inside the sandbox.
