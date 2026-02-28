# yoloAI Roadmap

Core copy/diff/apply workflow with Claude Code ships today. Docker, Tart, and Seatbelt backends are implemented.

For the full list of designed-but-unimplemented features, see [dev/plans/TODO.md](dev/plans/TODO.md). For design specs, see [design/](design/).

## Next up

- **Overlayfs copy strategy** — instant sandbox setup, space-efficient for large repos
- **Network isolation** — domain-based allowlisting via proxy sidecar (`--network-isolated`)
- **Profiles** — reusable environment definitions with user-supplied Dockerfiles
- **Codex agent** — OpenAI Codex support (architecture is agent-agnostic)

## Also planned

- Auxiliary directory mounts (`-d` flag)
- User-defined extensions (`yoloai x`)
- Auto-commit intervals for crash recovery
- macOS VM backend improvements (Tart)
- Community-requested agents (Aider, Goose, etc.)
