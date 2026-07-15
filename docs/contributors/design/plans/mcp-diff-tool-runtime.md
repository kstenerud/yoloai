> **ABOUTME:** Thread a `runtime.Backend` handle into the MCP server so its diff tool can
> generate diffs on VM-backed sandboxes (Tart) instead of only Docker's host-side git.

# Pass runtime to MCP diff tool for non-Docker backends

- **Status:** UNSPECIFIED — idea only; not started.
- **Depends on:** —

`internal/mcpsrv/tools.go` calls `patch.GenerateDiff` with `Runtime: nil`, which works for Docker (host-side git) but fails for Tart (where git runs inside the VM via the runtime exec). The MCP server doesn't currently have a runtime handle; it would need one to support diff for VM-backed sandboxes.

Fix: thread the active `runtime.Backend` through the MCP server struct (`internal/mcpsrv/server.go`) and pass it via `patch.DiffOptions.Runtime` for the affected MCP tools. Verify against Tart on Apple Silicon once that backend is fully tested.

Source TODO: `internal/mcpsrv/tools.go:304-307` ("MCP is primarily used with Docker backends, we pass nil for now").
