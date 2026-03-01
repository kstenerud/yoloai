# yoloAI Roadmap

Core copy/diff/apply workflow ships today with Claude Code and Gemini CLI agents. Docker, Tart, and Seatbelt backends are implemented.

For the full list of designed-but-unimplemented features, see [dev/plans/TODO.md](dev/plans/TODO.md). For design specs, see [design/](design/).

## Next up

- **Overlayfs copy strategy** — instant sandbox setup, space-efficient for large repos
- **Network isolation** — domain-based allowlisting via iptables + ipset (`--network-isolated`)
- **Profiles** — reusable environment definitions with user-supplied Dockerfiles
- **Codex agent** — testing and polish (agent definition exists, needs verification)

## Also planned

- Auxiliary directory mounts (`-d` flag)
- User-defined extensions (`yoloai x`)
- Auto-commit intervals for crash recovery
- macOS VM backend improvements (Tart)
- Community-requested agents (Aider, Goose, etc.)
