> **ABOUTME:** Close the two user-facing MCP-in-sandbox gaps — missing server binaries and
> `localhost`-addressed servers — left open after OQ #93's architectural concern was resolved.

# MCP servers don't fully work inside sandboxes

- **Status:** UNSPECIFIED — workarounds exist (custom profiles); which of the listed future
  improvements to build, if any, is undecided.
- **Depends on:** —

Two limitations in MCP-inside-sandbox support today, surfaced by OQ #93
(closed 2026-05-27 — the architectural concern was resolved by W-L8b's
StdioExec abstraction, but the user-facing gaps remain):

1. **Stdio MCP servers need their binary in the sandbox image.** Claude
   Code's `settings.json` and `~/.claude.json` get seeded into the
   sandbox, but the agent then tries to spawn each configured MCP
   server (e.g. `npx @modelcontextprotocol/server-foo …`) as a child
   process. If the binary isn't installed in the sandbox image, the
   server fails to start and the agent loses that capability —
   silently, in most cases.

2. **Network MCP servers reference `localhost`.** Many MCP server
   configs point at `localhost:N` (e.g. an MCP server the user runs
   on their host). Inside the sandbox `localhost` resolves to the
   sandbox itself, not the host, so these connections fail.

**Workarounds available today:** custom profile
(`~/.yoloai/profiles/<name>/Dockerfile`) can install MCP binaries and
rewrite the MCP config to use `host.docker.internal` (Docker) / the
equivalent on other backends — `BackendDescriptor.HostFromContainer`
exposes the right hostname per backend. Users with MCP-heavy workflows
are expected to build a profile today.

**Possible future improvements:**
- Detect MCP server entries during sandbox creation and warn when
  referenced binaries aren't likely to be available.
- Auto-rewrite `localhost` MCP references to `HostFromContainer` on
  sandbox creation.
- Provide a `yoloai system mcp install …` helper that builds a profile
  with the requested MCP servers baked in.

These are open-ended feature work, not maintenance items — punt to
roadmap-driven design when MCP-in-sandbox usage justifies it.
