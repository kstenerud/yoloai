# yoloAI Roadmap

Core copy/diff/apply workflow ships today with Claude Code, Gemini CLI, Aider, Codex, and OpenCode agents. Docker, Tart, and Seatbelt backends are implemented. Network isolation, profiles, auxiliary directories, overlay mode, user-defined extensions, list filters, and profile recipes (`cap_add`, `devices`) are shipped.

For the full list of designed-but-unimplemented features, see [dev/plans/TODO.md](dev/plans/TODO.md). For design specs, see [design/](design/).

## Next up

- Profile setup commands (run custom scripts when a sandbox starts)
- Batch sandbox creation (`yoloai batch`)
- Sandbox chaining/pipelines
- Shared cache volumes for package managers

## Future backends and isolation layers

Tracked here to avoid architectural decisions that make them difficult to add later.

**Lima (macOS Linux VMs)** — Lima is a growing Docker Desktop alternative on macOS that exposes
containerd directly via a Linux VM. The containerd runtime being built for `--isolation vm` would
work against Lima with minimal changes (same gRPC socket, same client). When Lima reaches sufficient
adoption, add it as a backend option for macOS users who prefer it over Tart. No structural work
needed now — just avoid hardcoding `/run/containerd/containerd.sock` without a fallback socket
discovery path (similar to `podmanrt.discoverSocket()`).

**gVisor as a containerd shim** — gVisor ships `io.containerd.runsc.v1`, a containerd shim that
runs containers under the gVisor kernel without going through Docker. This would allow
`--isolation container-enhanced` on the containerd backend, collapsing the backend×isolation
matrix: container-enhanced would no longer require Docker specifically. Relevant when containerd
becomes the default or preferred container backend on a system. The `BackendCaps.OCIRuntime` field
added during the containerd implementation correctly flags that containerd doesn't use OCI runtime
names — gVisor-on-containerd would use a shim type string instead, fitting the same pattern as
Kata.

## Deferred migrations

**Claude Code native installer on container backends** — The npm package
`@anthropic-ai/claude-code` was deprecated in Jan 2026 (v2.1.15) and will
eventually stop being published. The container backends (Docker, Podman,
containerd) still install via npm because the native installer bundles Bun,
whose `fetch()` ignores `HTTP_PROXY`/`HTTPS_PROXY`
([#14165](https://github.com/anthropics/claude-code/issues/14165)) — which would
silently break `--network-isolated` (proxy + domain allowlist) for Claude Code.
The Tart backend already uses the native installer because it has no
proxy-based isolation (`NetworkIsolation: false`), so the Bun limitation does
not apply there. **Upgrade path:** once #14165 is resolved (Bun honors proxy
env vars), switch the container backends to `curl -fsSL https://claude.ai/install.sh | bash`
and drop the ~100 MB Node.js dependency from the base image. Until then npm
remains the only proxy-capable install path. See
`docs/dev/research/implementation.md` ("Claude Code Installation Research").

## Future infrastructure

**Pre-provisioned Tart base image (published OCI artifact)** — Instead of provisioning
`yoloai-base` locally on first run (clone the upstream macOS image, boot it, `brew install`
tmux/node/jq/ripgrep + `npm install` Claude Code), build the provisioned base once and publish it
as an OCI image (e.g. `ghcr.io/kstenerud/yoloai-base:<yoloai-version>`). Tart pulls/pushes OCI
natively, so users would `tart pull` a ready base — the same ~30 GB download they already do for the
upstream base, but with the tools baked in. Benefits: no per-user provisioning step, and a
bit-for-bit identical, reproducible base for everyone (a breaking `brew`/`npm` change can't fail
each user's local build independently). Xcode stays host-mounted via VirtioFS (not redistributable),
so only redistributable tools are baked in — no licensing issue.

Scope of the win is narrow: it removes only the one-time first-build cost (a few minutes) plus gives
cross-user reproducibility. Steady-state sandbox creation is identical either way — both just APFS-clone
the local `yoloai-base`. So this is *not* a prerequisite for fast local/test runs; the local build
(see below) already reaches the same steady state, and the smoke harness builds the base once and
reuses it across runs.

Why it's deferred: the blocker is **building and testing the image in CI**, not hosting it (ghcr.io
public is free). Tart requires nested virtualization, which GitHub-hosted macOS runners do not provide
(they are themselves VMs on M1-class hardware; Apple only added nested virt on M3+/macOS 15). So
neither building the blessed image nor smoke-testing it can run on free GitHub-hosted runners. The
realistic FOSS path is **Cirrus CI** (built by Tart's authors, native Tart support, free OSS tier) or
a self-hosted Apple Silicon runner (free hardware-wise, but maintenance plus the security caveat that
fork-PR code must not run on it). Re-check current CI capabilities before pursuing — Apple Silicon
runner and nested-virt support move fast.

The local imperative build remains the source of truth and the fallback regardless (it must handle
`tart.image` overrides and custom tooling), so this is a pure first-run/reproducibility optimization
layered on top of a correct local build, not a replacement for it.
