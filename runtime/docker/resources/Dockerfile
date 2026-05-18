FROM debian:bookworm-slim

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# hadolint ignore=DL3008
RUN apt-get update && apt-get install -y --no-install-recommends \
    tmux \
    git \
    build-essential \
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
    less \
    file \
    unzip \
    openssh-client \
    pkg-config \
    libssl-dev \
    iptables \
    ipset \
    dnsutils \
    sudo \
    uidmap \
    slirp4netns \
    fuse-overlayfs \
    iproute2 \
    && rm -rf /var/lib/apt/lists/*

# Docker CE + Compose plugin
# hadolint ignore=DL3008
RUN install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc \
    && chmod a+r /etc/apt/keyrings/docker.asc \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian bookworm stable" \
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


# Node.js 20 LTS via NodeSource (22 has syscall incompatibilities with gVisor ARM64)
ARG NODE_MAJOR=20
# hadolint ignore=DL3008
RUN mkdir -p /etc/apt/keyrings \
    && curl -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key \
       | gpg --dearmor -o /etc/apt/keyrings/nodesource.gpg \
    && echo "deb [signed-by=/etc/apt/keyrings/nodesource.gpg] https://deb.nodesource.com/node_${NODE_MAJOR}.x nodistro main" \
       > /etc/apt/sources.list.d/nodesource.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# Go (latest stable)
ARG GO_VERSION=1.26.2
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-$(dpkg --print-architecture | sed 's/i386/386/').tar.gz" \
      -o /tmp/go.tar.gz \
    && tar -C /usr/local -xzf /tmp/go.tar.gz \
    && rm /tmp/go.tar.gz
ENV PATH="/usr/local/go/bin:${PATH}"
# Also set via profile.d so shells started fresh (e.g. by AI coding agents)
# pick up the path even when Docker ENV is not inherited.
# hadolint ignore=SC2016
RUN echo 'export PATH="/usr/local/go/bin:$PATH"' > /etc/profile.d/golang.sh

# golangci-lint
# hadolint ignore=DL3062
RUN GOPATH=/tmp/gopath go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest \
    && mv /tmp/gopath/bin/golangci-lint /usr/local/bin/golangci-lint \
    && rm -rf /tmp/gopath

# Rust via rustup (installed system-wide)
ENV RUSTUP_HOME=/usr/local/rustup \
    CARGO_HOME=/usr/local/cargo \
    PATH="/usr/local/cargo/bin:${PATH}"
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs \
      | sh -s -- -y --default-toolchain stable --profile minimal --no-modify-path \
    && chmod -R a+rw "$RUSTUP_HOME" "$CARGO_HOME"
# hadolint ignore=SC2016
RUN echo 'export PATH="/usr/local/cargo/bin:$PATH"' > /etc/profile.d/cargo.sh

# AI coding agents — versions intentionally unpinned; agents must run at
# current versions to function correctly with their respective APIs.
# Claude Code via npm (not native binary — native bundles Bun which ignores
# proxy env vars and auto-updates; see docs/dev/research/implementation.md)
ENV DISABLE_INSTALLATION_CHECKS=1
# hadolint ignore=DL3016
RUN npm install -g @anthropic-ai/claude-code \
    && npm install -g @google/gemini-cli \
    && npm install -g @openai/codex \
    && npm install -g opencode-ai
# hadolint ignore=DL3013
RUN pip install --break-system-packages --no-cache-dir aider-chat

# VS Code CLI for Remote Tunnels (code tunnel)
RUN ARCH=$(dpkg --print-architecture | sed 's/amd64/x64/') \
    && curl -fsSL "https://code.visualstudio.com/sha/download?build=stable&os=cli-linux-${ARCH}" \
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

# gosu for dropping privileges
ARG GOSU_VERSION=1.17
RUN curl -fsSL "https://github.com/tianon/gosu/releases/download/${GOSU_VERSION}/gosu-$(dpkg --print-architecture)" \
      -o /usr/local/bin/gosu \
    && chmod +x /usr/local/bin/gosu \
    && gosu --version

# Docker daemon config: use fuse-overlayfs storage driver.
# overlay2 requires an unprivileged user namespace mount that is blocked by the
# outer Docker layer (EPERM); fuse-overlayfs works in all nesting configurations.
RUN mkdir -p /etc/docker \
    && printf '{"storage-driver":"fuse-overlayfs"}\n' > /etc/docker/daemon.json

# Add /sbin and /usr/sbin to PATH so tools like sysctl are found by subprocesses
# (rootlesskit child scripts, dockerd hooks) that don't inherit a full login PATH.
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
COPY sandbox-setup.py /yoloai/bin/sandbox-setup.py
COPY status-monitor.py /yoloai/bin/status-monitor.py
COPY diagnose-idle.sh /yoloai/bin/diagnose-idle.sh
RUN chmod +x /yoloai/bin/entrypoint.sh /yoloai/bin/entrypoint.py /yoloai/bin/diagnose-idle.sh

ENTRYPOINT ["/yoloai/bin/entrypoint.sh"]
