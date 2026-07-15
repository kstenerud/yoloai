> **ABOUTME:** Dockerfile conventions for yoloAI's base image and the user-authored profile
> Dockerfiles built on top of it — base-distro choice, apt/layer/pinning discipline, and the
> runtime contract every profile inherits via `FROM yoloai-base`. Covers only the base image;
> profile Dockerfiles are user-owned and asked, not forced, to follow it.

# Dockerfile Standard

Reference for yoloAI's Dockerfiles: the base image at `runtime/docker/resources/Dockerfile` and user-supplied profile Dockerfiles at `~/.yoloai/profiles/<name>/Dockerfile`.

See also: `../principles/general-principles.md §2` (boring tech — Debian + apt, not Alpine + apk); `../principles/security-principles.md §4` (least privilege — non-root runtime user); `../principles/development-principles.md §6` (warnings are signal — hadolint findings get justified suppressions or fixes); `MAKEFILE.md §The make check contract` (hadolint runs in `make check`).

## Two contexts, different audiences

| Context                                            | Authored by         | Constraints                                                                                    |
| -------------------------------------------------- | ------------------- | --------------------------------------------------------------------------------------------- |
| **Base image** (`runtime/docker/resources/Dockerfile`) | yoloAI itself       | Hadolint clean. Pinned versions where it matters. Documents the project's runtime contract. |
| **Profile Dockerfile** (`~/.yoloai/profiles/<name>/Dockerfile`) | The user           | Starts with `FROM yoloai-base`. Adds project-specific tools. User-owned.                       |

This standard covers the base image. Profile Dockerfiles are user-owned; yoloAI documents the `yoloai-base` contract in `docs/contributors/design/config.md` and trusts users to apt-install what they need on top.

## Base image conventions

### Base distro: `debian:bookworm-slim`

Debian bookworm-slim is the base. Reasons:

- **Boring** (`../principles/general-principles.md §2`). Wide Debian familiarity in the developer community; apt is well-documented; package availability is broad.
- **Slim variant** drops docs, locale data, and other bulk that the sandbox doesn't need.
- **Rejected alternatives**: Alpine (musl libc → glibc-specific tools fail; the Bun-bundled Claude Code installer had documented issues per `docs/contributors/design/questions-unresolved.md` #2), Ubuntu (no advantage over Debian; larger), distroless (can't apt install at runtime, doesn't compose with profile system).

### Shell: bash with pipefail

```dockerfile
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
```

This is non-default. Docker's default `/bin/sh -c` doesn't honour pipefail; without it, `cmd1 | cmd2` exits zero whenever `cmd2` succeeds even if `cmd1` failed. Setting `bash -o pipefail` early is the recommended hadolint-aligned pattern (DL4006).

### apt-install pattern

Every apt install follows the pattern:

```dockerfile
RUN apt-get update && apt-get install -y --no-install-recommends \
    pkg1 \
    pkg2 \
    pkg3 \
    && rm -rf /var/lib/apt/lists/*
```

- `apt-get update` and `apt-get install` in a single `RUN` — separating them risks stale package lists in cached layers (hadolint DL3009).
- `--no-install-recommends` — keeps the image small; recommended packages get pulled by surprise otherwise.
- `rm -rf /var/lib/apt/lists/*` at the end — drops apt cache from the layer.
- Packages listed one-per-line, sorted-by-purpose-then-alphabetically when the purpose is clear, alphabetically otherwise.

### Pinning

Pin versions when the package's behaviour can change in ways that break us. Don't pin when the package's behaviour is stable across versions (apt-installed dev tools, system libraries).

- `golang-go` — *not* pinned in apt; Go is installed separately by tag (see "Go install" pattern below).
- `nodejs` — pinned via NodeSource repository to Node 22 LTS (rationale: `docs/contributors/design/questions-unresolved.md` #2).
- Downloaded binaries (gosu, ko, etc.) — pinned by version + checksum where possible.
- apt packages without a moving-target risk — left unpinned (hadolint DL3008 is suppressed for those `RUN` lines with `# hadolint ignore=DL3008` and a comment explaining the unpinned choice).

### Hadolint compliance

The base Dockerfile is hadolint clean (`make hadolint` in CI). Suppressions follow the same justification rule as Go lint suppressions (per `../principles/development-principles.md §6`):

```dockerfile
# hadolint ignore=DL3008
# We don't pin Debian apt packages because Debian stable rarely changes
# package behaviour and pinning every package would force constant updates.
RUN apt-get update && apt-get install -y --no-install-recommends \
    ...
```

The `# hadolint ignore=...` directive immediately precedes the `RUN` line. The explanatory comment is above the directive.

### Non-root runtime user

The container runs as user `yoloai` matching the host UID/GID, not root. This is the least-privilege application (`../principles/security-principles.md §4`):

- Claude Code refuses to run as root for `--dangerously-skip-permissions`.
- `yoloai` user has passwordless `sudo` (commit `83ac029`, 2026-03-12) for cases where a recipe needs elevated commands — opt-in, not default.
- The actual UID is set at container creation time so bind-mounted files have correct ownership.

### Layer ordering

Layers that change frequently go last; layers that rarely change go first:

1. Base distro + system packages (rare changes).
2. Language runtimes (Node, Python — change with version bumps).
3. Tool downloads (gosu, dev tools).
4. Project-specific configuration (user/group, sudo).
5. Entrypoint scripts (change most often during development).

Caching benefit: a change to the entrypoint script doesn't invalidate the apt-install layer.

## What goes in the base image

The base image carries everything yoloAI assumes is present in a sandbox:

- **tmux** — session management; every backend uses it.
- **git** — required for `:copy` mode's git-based diff/apply.
- **iptables + ipset** — required for `--network-isolated`.
- **dnsutils** — `dig` for domain resolution in network-isolation entrypoint.
- **sudo** — for the `yoloai` user passwordless escalation.
- **Standard dev tooling** (build-essential, cmake, clang, python3, curl, jq, ripgrep, fd-find, etc.) — broad coverage so most agents work out-of-box.
- **Node.js 22 LTS** — for Claude Code, Codex, Gemini CLI installation.
- **Docker CE + Compose plugin** — for Docker-in-Docker (D22 `--isolation container-privileged`).

What does NOT go in the base image:

- **Agent CLIs themselves** — installed at sandbox creation time per agent definition. Otherwise upgrading an agent would require rebuilding the base image.
- **API keys** — injected at runtime via `/run/secrets/` (`../principles/security-principles.md §6`).
- **User-specific configs** — handled via `agent_files` seeding mechanism.
- **Anything per-project** — that's what profile Dockerfiles are for.

## Profile Dockerfiles (user-supplied)

Profile Dockerfiles live at `~/.yoloai/profiles/<name>/Dockerfile`. They must begin with:

```dockerfile
FROM yoloai-base
```

Beyond that, the user has full Dockerfile expressiveness. The base image's user and entrypoint are inherited; profile Dockerfiles typically add language-specific tooling (Go toolchain, Rust toolchain, project-specific lint tools, etc.).

Profile Dockerfiles are NOT hadolint-checked by yoloAI's CI — they're user-authored. The hadolint discipline is documented as a recommendation in `docs/contributors/design/config.md`.

## Embedded vs bind-mounted resources

The base image's entrypoint files (`entrypoint.sh`, `entrypoint.py`, status monitor scripts) are *embedded* in the binary and bind-mounted into the container at run time (commit `294679e`, 2026-05-03). This means:

- The base image does not need rebuilding when entrypoint scripts change.
- The same binary running against an older base image gets the updated entrypoint.
- Resource checksums are tracked (commit `ffe99eb`, 2026-02-24) so the binary can detect user customisations.

This is documented in `docs/contributors/architecture/README.md`; the Dockerfile only needs to provide the *environment* the entrypoints will run in.

## ENTRYPOINT vs CMD

The base image does not set a final `ENTRYPOINT` / `CMD` — yoloAI specifies them at `docker run` time. The container starts with `entrypoint.sh` (the trampoline, per `SHELL.md`) which exec's into `entrypoint.py`.

Rationale: the entrypoint is a runtime contract, not a build-time one. Different backends (Tart, Seatbelt) don't even use a Docker entrypoint; codifying one in the base image would create a false consistency.

## Cross-references

- `../principles/general-principles.md §2` — boring tech (Debian + apt over Alpine + apk).
- `../principles/security-principles.md §4` — least privilege (non-root user, capability discipline).
- `../principles/security-principles.md §6` — credentials never in env vars (the Dockerfile must not bake them in).
- `../principles/development-principles.md §6` — warnings are signal (hadolint findings get justified suppressions).
- `MAKEFILE.md` — `make hadolint` is part of `make check`.
- `SHELL.md` — the entrypoint trampoline pattern (`#!/bin/sh`, minimal).
- `docs/contributors/design/config.md` — profile system design (how user profiles compose with the base image).
