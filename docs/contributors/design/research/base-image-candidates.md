<!-- ABOUTME: Survey of pre-built Docker base images that could replace debian:bookworm-slim -->
<!-- ABOUTME: for yoloai-base, with the gotchas that bite yoloAI specifically and a recommendation. -->

# Base Image Candidates for `yoloai-base`

**Status:** Research note · **Date:** 2026-05-24 · **Disposition:** Parked

Current base is `FROM debian:bookworm-slim` (`runtime/docker/resources/Dockerfile:1`). On top we install ~30 system packages + Docker CE + Node 20 + Go 1.26.2 + Rust + golangci-lint + AI agent CLIs (claude-code, gemini, codex, opencode, aider) + VS Code CLI + gosu. The question prompting this note: could a "batteries included" image avoid most of the apt+rustup+npm work?

This document captures the survey so we don't redo it.

---

## 1. Candidates

| Image | What it pre-installs | Size | Notes |
|---|---|---|---|
| `mcr.microsoft.com/devcontainers/universal:linux` | Python, Node, Go, .NET, Java, Ruby, Rust, Conda, common CLIs, oh-my-zsh, git, GH CLI | ~10–15 GB | Ubuntu-based; fixed `codespace` user at uid 1000. Devcontainer "features" rely on YAML composition at devcontainer-build time, not raw Dockerfile. |
| `mcr.microsoft.com/devcontainers/base:bookworm` | Common shell tooling, git, build-essential. No languages. | ~600 MB | Debian-family; minimal user setup (`vscode` uid 1000). |
| `gitpod/workspace-full` | Go, Node, Python, Rust, Ruby, Java, Docker-in-Docker, GH CLI | ~10 GB | Ubuntu-based; `gitpod` user fixed at uid 33333. |
| `buildpack-deps:bookworm` | Debian + every common build header/lib (curl, git, make, gcc, OpenSSL headers, …); no languages. | ~830 MB | Same Debian family as our current base. No fixed non-root user. Designed to be the layer below language images. |
| `nikolaik/python-nodejs:python3.12-nodejs20` | Python + Node together | ~1 GB | Saves Node install; doesn't help with Go / Rust / Docker. Community-maintained. |

---

## 2. yoloAI-specific gotchas

These bite all five of the "batteries included" options in some combination.

1. **Docker-in-Docker.** `yoloai-base` installs `docker-ce` + `fuse-overlayfs` + `iptables` + `ipset` because users run `--isolation container-privileged` and need a working **inner** docker daemon. Codespaces / devcontainers / Gitpod images standardize on *docker-from-docker* (bind-mount the host socket) and won't run an inner daemon out of the box. Switching would mean undoing their setup before our setup runs.
2. **`yoloai` user at uid 1001.** `runtime/docker/resources/Dockerfile:170-176` bakes a `yoloai` user at uid/gid 1001 because the entrypoint adjusts it to match the caller's uid at runtime, and aux directories (e.g. Kata) need a fixed target uid for bind mounts to work. The devcontainer/gitpod/codespace base images each bake in a different fixed user (`vscode` at 1000, `gitpod` at 33333, `codespace` at 1000) — possible to delete and recreate, but it's a friction point every release.
3. **AI agent CLIs are yoloAI-specific.** Claude Code / Gemini / Codex / opencode / aider must live in the image regardless of base. No prebuilt image bundles them. That layer doesn't compress.
4. **Build cost is amortized.** The image is built once per user per yoloAI version (or per `yoloai system build`). Cold build is ~5–10 min on a clean host; rebuild from cache after a source change is seconds. The biggest steady-state cost is `npm install -g` + `rustup` install, neither of which any prebuilt base covers.

---

## 3. VM backends — not applicable

The question of "swap the base image" only applies to backends that consume an OCI image:

- **Docker, Podman, containerd, Kata (via containerd)**: all share `yoloai-base`. A base swap propagates uniformly. Kata is just a Linux VM running an OCI container, so a Linux base swap works there too.
- **Tart**: runs a real macOS VM from a *macOS* base (`ghcr.io/cirruslabs/macos-sequoia-base` etc.). Tools come from Homebrew + Xcode on the VM side, not from any Linux OCI image. Different stack entirely.
- **Seatbelt**: not an image. `sandbox-exec` against tools already installed on the macOS host.

So this is a Docker-family discussion only.

---

## 4. Recommendation

The only **honest** win is `buildpack-deps:bookworm`:

- Same Debian family → no `apt` source rewriting, no user-conflict.
- Pre-installs the curl/git/make/gcc/OpenSSL/etc. headers our first apt layer already pulls.
- Estimated save: ~30 s of build time, ~200 MB of duplicated installs (rough estimate — not measured).
- No conflicts with Docker-in-Docker setup or our `yoloai` user.

Anything heavier (`devcontainers/universal`, `gitpod/workspace-full`) probably costs more in image pull time + user-uid friction than it saves. The remaining wall-clock cost is in `npm install -g` for the agent CLIs and `rustup` install — neither solvable by base swap.

**Decision: defer.** Not worth the churn for a ~30 s build-time delta when steady-state user experience is unaffected. Revisit if (a) the language install layers grow substantially, (b) we start shipping a tested-on-CI prebuilt `yoloai-base` to a registry, or (c) a devcontainer feature lands that handles user-uid remapping cleanly.

---

## 5. References

- Current Dockerfile: `runtime/docker/resources/Dockerfile`
- Devcontainer images index: https://github.com/devcontainers/images
- Gitpod workspace images: https://github.com/gitpod-io/workspace-images
- `buildpack-deps`: https://hub.docker.com/_/buildpack-deps
- Container backends that share the image: see `docs/contributors/design/research/linux-vm-backends.md`
