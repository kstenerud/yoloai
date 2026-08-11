# Apple's `container build` rejects a Dockerfile over 16 KiB (apple/container
# #735, still open 2026-07-30), so this file has a hard size budget that nothing
# in the build itself enforces. TestDockerfile_FitsAppleBuilderLimit guards it.
# Comments are ~58% of the bytes here: keep the rationale that a reader needs in
# order to act correctly, and put the archaeology in the finding it cites.
FROM debian:trixie-slim

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# DL3008 unpinned-apt rationale: standards/dockerfile.md "Pinning".
# binutils-gold is required for cgo links on linux/arm64 (trixie split gold out
# of binutils; without it an arm64 base build cannot find `ld`) —
# backend-idiosyncrasies.md.
# hadolint ignore=DL3008
RUN apt-get update && apt-get install -y --no-install-recommends \
    tmux \
    git \
    build-essential \
    binutils-gold \
    cmake \
    clang \
    python3 \
    python3-pip \
    python3-venv \
    curl \
    wget \
    ca-certificates \
    gnupg \
    jq \
    ripgrep \
    fd-find \
    shellcheck \
    less \
    file \
    unzip \
    openssh-client \
    pkg-config \
    libssl-dev \
    iptables \
    ipset \
    dnsutils \
    rsync \
    sudo \
    uidmap \
    slirp4netns \
    fuse-overlayfs \
    iproute2 \
    && rm -rf /var/lib/apt/lists/*

# Docker CE + Compose plugin
# DL3008: as above; pinning would freeze the sandbox on an old Docker.
# hadolint ignore=DL3008
RUN install -m 0755 -d /etc/apt/keyrings \
    && curl --retry 5 --retry-delay 2 --retry-all-errors -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc \
    && chmod a+r /etc/apt/keyrings/docker.asc \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian trixie stable" \
       > /etc/apt/sources.list.d/docker.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
       docker-ce \
       docker-ce-cli \
       docker-ce-rootless-extras \
       containerd.io \
       docker-buildx-plugin \
       docker-compose-plugin \
    && rm -rf /var/lib/apt/lists/*


# Node.js 22 LTS via NodeSource. Was 20 from 2026-03 to 2026-07 on an
# unevidenced gVisor/ARM64 claim; that cost an EOL runtime and froze Claude Code
# at 2.1.197 (2.1.198+ need Node >=22). Full account and the residual risk:
# DF158. If gVisor on ARM64 does dislike Node 22 it shows up in
# `container-enhanced` there only, and `sandbox.node_check` already logs the node
# version before agent launch so it surfaces. Revisit with evidence, not a
# hypothesis.
ARG NODE_MAJOR=22
# DL3008: pinned at the NodeSource repo URL (NODE_MAJOR), the right level for an
# LTS line; pinning the apt minor would lock us to one security release.
# hadolint ignore=DL3008
RUN mkdir -p /etc/apt/keyrings \
    && curl --retry 5 --retry-delay 2 --retry-all-errors -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key \
       | gpg --dearmor -o /etc/apt/keyrings/nodesource.gpg \
    && echo "deb [signed-by=/etc/apt/keyrings/nodesource.gpg] https://deb.nodesource.com/node_${NODE_MAJOR}.x nodistro main" \
       > /etc/apt/sources.list.d/nodesource.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# Go (latest stable)
ARG GO_VERSION=1.26.2
RUN curl --retry 5 --retry-delay 2 --retry-all-errors -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-$(dpkg --print-architecture | sed 's/i386/386/').tar.gz" \
      -o /tmp/go.tar.gz \
    && tar -C /usr/local -xzf /tmp/go.tar.gz \
    && rm /tmp/go.tar.gz
ENV PATH="/usr/local/go/bin:${PATH}"
# Also via profile.d so freshly-started shells pick up the path when Docker ENV
# is not inherited. SC2016: the literal `$PATH` is intended — it expands when the
# file is sourced, not at build time.
# hadolint ignore=SC2016
RUN echo 'export PATH="/usr/local/go/bin:$PATH"' > /etc/profile.d/golang.sh

# golangci-lint — DL3062 unpinned on purpose: this copy is for agents working in
# the sandbox and wants the current linter set, not yoloAI's release cadence
# (yoloAI's own `make check` pins it via `go run`). Retry-wrapped because
# `go install` has no --retry and a proxy hiccup would fail the whole base build.
# hadolint ignore=DL3062
RUN n=0; until [ "$n" -ge 5 ]; do \
      GOPATH=/tmp/gopath go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest && break; \
      n=$((n+1)); echo "golangci-lint install failed (attempt $n/5); retrying in 5s"; sleep 5; \
    done; \
    [ "$n" -lt 5 ] || { echo "golangci-lint install failed after 5 attempts"; exit 1; }; \
    mv /tmp/gopath/bin/golangci-lint /usr/local/bin/golangci-lint \
    && rm -rf /tmp/gopath

# Rust via rustup (installed system-wide)
ENV RUSTUP_HOME=/usr/local/rustup \
    CARGO_HOME=/usr/local/cargo \
    PATH="/usr/local/cargo/bin:${PATH}"
RUN curl --retry 5 --retry-delay 2 --retry-all-errors --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs \
      | sh -s -- -y --default-toolchain stable --profile minimal --no-modify-path \
    && chmod -R a+rw "$RUSTUP_HOME" "$CARGO_HOME"
# SC2016: as golang.sh above — `$PATH` expands at source time, not build time.
# hadolint ignore=SC2016
RUN echo 'export PATH="/usr/local/cargo/bin:$PATH"' > /etc/profile.d/cargo.sh

# AI coding agents — a MINIMUM version, maximum left open: the clients must keep
# tracking their servers' APIs (an exact pin fails within weeks), while yoloAI
# drives them by flags hardcoded in internal/agent/agent.go and needs to state
# which versions have them. Each floor is a version this repo has run against.
# RAISE ONE ONLY when yoloAI depends on something newer, and say why in the
# commit; that rebuilds the base and every profile image on it, which is the
# intended mechanism. Rationale and both traps in full: DF158.
#
#  - QUOTE THE SPEC. Unquoted, sh reads `>=2.1.197` as a redirection and npm gets
#    the bare name — a floor that enforces nothing, silently. Gated by
#    TestDockerfile_AgentInstallsCarryAQuotedFloor.
#  - `>=X.Y.Z` is not "latest": npm picks the newest version whose `engines` this
#    image satisfies, and raising a floor past an engine boundary only warns
#    (EBADENGINE), leaving an unsupported combination. Check
#    `npm view <pkg>@<ver> engines.node` against NODE_MAJOR first.
#
# Claude Code via npm, not the native binary: that bundles Bun, which ignores
# proxy env vars and auto-updates. The "fullscreen renderer" upsell that stalled
# smokes is handled at the agent layer — settings.tui="default", DF52.
ENV DISABLE_INSTALLATION_CHECKS=1
# Retry-wrapped (like the golangci-lint install above): a transient npm-registry
# hiccup otherwise fails the whole multi-minute base build with no recovery.
RUN n=0; until [ "$n" -ge 5 ]; do \
      npm install -g "@anthropic-ai/claude-code@>=2.1.197" \
        && npm install -g "@google/gemini-cli@>=0.53.0" \
        && npm install -g "@openai/codex@>=0.146.0" \
        && npm install -g "opencode-ai@>=1.18.9" && break; \
      n=$((n+1)); echo "npm install failed (attempt $n/5); retrying in 5s"; sleep 5; \
    done; \
    [ "$n" -lt 5 ] || { echo "npm install failed after 5 attempts"; exit 1; }
# Aider via uv on a bundled Python 3.12. aider-chat pins exact dependency
# versions and caps requires-python at <3.13, so it cannot install against the
# base image's system Python (3.13 on trixie) — a plain `pip install` backtracks
# to an ancient release whose deps fail to build. uv installs aider isolated on
# its own managed Python 3.12, yielding the *current* aider while keeping it
# unpinned (this is aider's own recommended install path). Tools + bundled
# interpreters live under /opt/uv (world-readable for the runtime yoloai user);
# the `aider` launcher lands in /usr/local/bin. Retry the network fetch like the
# npm/golangci-lint installs above.
RUN curl --retry 5 --retry-delay 2 --retry-all-errors -LsSf https://astral.sh/uv/install.sh \
      | env UV_INSTALL_DIR=/usr/local/bin sh \
    && UV_TOOL_DIR=/opt/uv/tools UV_TOOL_BIN_DIR=/usr/local/bin UV_PYTHON_INSTALL_DIR=/opt/uv/python \
       uv tool install --python 3.12 "aider-chat>=0.86.2"

# VS Code CLI for Remote Tunnels (code tunnel)
RUN ARCH=$(dpkg --print-architecture | sed 's/amd64/x64/') \
    && curl --retry 5 --retry-delay 2 --retry-all-errors -fsSL "https://code.visualstudio.com/sha/download?build=stable&os=cli-alpine-${ARCH}" \
    -o /tmp/vscode-cli.tar.gz \
    && tar -xzf /tmp/vscode-cli.tar.gz -C /usr/local/bin \
    && rm /tmp/vscode-cli.tar.gz

# Agent launcher shortcuts (yolo-<agent> runs with full permissions bypassed)
RUN printf '#!/bin/sh\nexec aider --yes-always "$@"\n' > /usr/local/bin/yolo-aider \
    && printf '#!/bin/sh\nexec claude --dangerously-skip-permissions "$@"\n' > /usr/local/bin/yolo-claude \
    && printf '#!/bin/sh\nexec codex --dangerously-bypass-approvals-and-sandbox "$@"\n' > /usr/local/bin/yolo-codex \
    && printf '#!/bin/sh\nexec gemini --yolo "$@"\n' > /usr/local/bin/yolo-gemini \
    && printf '#!/bin/sh\nexec opencode "$@"\n' > /usr/local/bin/yolo-opencode \
    && chmod +x /usr/local/bin/yolo-aider /usr/local/bin/yolo-claude /usr/local/bin/yolo-codex /usr/local/bin/yolo-gemini /usr/local/bin/yolo-opencode

# hadolint: not in Debian, so it is a pinned download (shellcheck is apt, above).
# Both are REQUIRED by `make check` (D112). The Makefile falls back to running
# them via Docker, but that needs a daemon the sandbox only has under
# container-privileged — so without these a sandbox cannot run the gate of the
# project it was built to work on. Checksums per standards/dockerfile.md.
ARG HADOLINT_VERSION=2.15.1
RUN arch="$(dpkg --print-architecture)" \
    && case "$arch" in \
         amd64) ha=x86_64; sum=c7187db94eeeeca956519a6af171adc31453941a1e777961f6e680f697c8c507 ;; \
         arm64) ha=arm64;  sum=f6198ef8090f404dbb771abfee086eb8c48ac177f30da7fd3510aca35b344b5d ;; \
         *) echo "hadolint: unsupported arch $arch" >&2; exit 1 ;; \
       esac \
    && curl --retry 5 --retry-delay 2 --retry-all-errors -fsSL \
       "https://github.com/hadolint/hadolint/releases/download/v${HADOLINT_VERSION}/hadolint-linux-${ha}" \
       -o /usr/local/bin/hadolint \
    && echo "${sum}  /usr/local/bin/hadolint" | sha256sum -c - \
    && chmod +x /usr/local/bin/hadolint \
    && hadolint --version

# gosu for dropping privileges
ARG GOSU_VERSION=1.17
RUN curl --retry 5 --retry-delay 2 --retry-all-errors -fsSL "https://github.com/tianon/gosu/releases/download/${GOSU_VERSION}/gosu-$(dpkg --print-architecture)" \
      -o /usr/local/bin/gosu \
    && chmod +x /usr/local/bin/gosu \
    && gosu --version

# Docker daemon storage driver is intentionally NOT pinned (no /etc/docker/daemon.json).
# yoloAI mounts a real-filesystem volume at /var/lib/docker for privileged sandboxes
# (see docker.go Create), so the nested dockerd auto-selects the native overlay driver —
# fast, low-disk, and exec-safe on every provider (Linux + all macOS VMs). fuse-overlayfs
# stays installed (above) as a fallback for an overlay backing; sandbox-setup.py's
# start_dockerd selects it only then. A static pin can't express that and conflicts with a
# --storage-driver flag.

# Add /sbin and /usr/sbin to PATH so tools like sysctl are found by subprocesses
# (rootlesskit child scripts, dockerd hooks) that don't inherit a full login PATH.
# SC2016: same reason as the golang.sh and cargo.sh writes — `$PATH` is the
# literal we want in /etc/profile.d/, expanded by the sourcing shell at runtime.
# hadolint ignore=SC2016
RUN echo 'export PATH="/usr/sbin:/sbin:$PATH"' > /etc/profile.d/sbin-path.sh

# Non-root user (placeholder UID/GID — entrypoint adjusts at runtime)
RUN groupadd -g 1001 yoloai \
    && useradd -m -u 1001 -g yoloai -s /bin/bash yoloai \
    && echo 'yoloai ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/yoloai \
    && chmod 0440 /etc/sudoers.d/yoloai \
    && echo "yoloai:1001:64535" > /etc/subuid \
    && echo "yoloai:1001:64535" > /etc/subgid \
    && usermod -aG docker yoloai

# Pre-create agent state directories and home seed file placeholders so
# bind mount targets exist in the image. Kata Containers (and any OCI runtime
# that does not auto-create missing targets like runc does) requires that both
# directory and file bind mount destinations exist before the mount is applied.
# The file placeholders are deliberately EMPTY and generic — agent-specific
# defaults (e.g. aider's "{}" conf) live in the agent definition via
# SeedFile.Content, which always stages the file so it is bind-mounted *over*
# the empty placeholder; the placeholder is never the file the agent reads.
RUN mkdir -p \
        /home/yoloai/.claude \
        /home/yoloai/.gemini \
        /home/yoloai/.codex \
        /home/yoloai/.local/share/opencode \
        /home/yoloai/.config/github-copilot \
        /home/yoloai/.config/opencode \
        /home/yoloai/.vscode/cli \
    && touch \
        /home/yoloai/.claude.json \
        /home/yoloai/.opencode.json \
        /home/yoloai/.aider.conf.yml \
        /home/yoloai/.tmux.conf \
    && chown -R yoloai:yoloai /home/yoloai

# UTF-8 locale — C.UTF-8 is always present on Debian/Ubuntu without locale-gen.
# Without this, apps like Claude Code fall back to ASCII rendering.
ENV LANG=C.UTF-8

# Internal directory for sandbox runtime files
ENV YOLOAI_DIR=/yoloai
RUN mkdir -p \
        /yoloai/bin \
        /yoloai/tmux \
        /yoloai/logs \
        /yoloai/files \
        /yoloai/cache \
        /yoloai/overlay \
    && touch \
        /yoloai/agent-status.json \
        /yoloai/runtime-config.json \
        /yoloai/prompt.txt \
    && mkdir -p /run/secrets \
    && chown -R yoloai:yoloai /yoloai

COPY tmux.conf /yoloai/tmux/tmux.conf
COPY entrypoint.sh /yoloai/bin/entrypoint.sh
COPY entrypoint.py /yoloai/bin/entrypoint.py
COPY firewall.py /yoloai/bin/firewall.py
COPY install-firewall.py /yoloai/bin/install-firewall.py
COPY sandbox-setup.py /yoloai/bin/sandbox-setup.py
COPY setup_helpers.py /yoloai/bin/setup_helpers.py
COPY tmux_io.py /yoloai/bin/tmux_io.py
COPY status-monitor.py /yoloai/bin/status-monitor.py
COPY diagnose-idle.sh /yoloai/bin/diagnose-idle.sh
COPY agent-run.sh /yoloai/bin/agent-run.sh
COPY yoloai-resume /yoloai/bin/yoloai-resume
# chmod the exec scripts, and put /yoloai/bin on PATH so the fall-to-shell user
# can run `yoloai-resume` by name (D96). SC2016: the literal `$PATH` is intended —
# it is expanded by the shell that sources the profile, same as golang.sh above.
# hadolint ignore=SC2016
RUN chmod +x /yoloai/bin/entrypoint.sh /yoloai/bin/entrypoint.py /yoloai/bin/install-firewall.py /yoloai/bin/diagnose-idle.sh /yoloai/bin/agent-run.sh /yoloai/bin/yoloai-resume \
    && echo 'export PATH="/yoloai/bin:$PATH"' > /etc/profile.d/yoloai-bin.sh

# Marks this image (and every profile image built FROM it — LABELs are inherited)
# as yoloai-authored, matching the com.yoloai.managed label already stamped on
# yoloai-created volumes. Seeded now but NOT YET USED to scope image pruning:
# `system prune --images` stays daemon-wide until the switch registered in
# docs/contributors/deprecations.md comes due, so pre-label images on existing
# installs still get reclaimed in the meantime.
LABEL com.yoloai.managed="true"

ENTRYPOINT ["/yoloai/bin/entrypoint.sh"]
