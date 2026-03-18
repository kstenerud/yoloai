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
