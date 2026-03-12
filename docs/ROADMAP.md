# yoloAI Roadmap

Core copy/diff/apply workflow ships today with Claude Code, Gemini CLI, Aider, Codex, and OpenCode agents. Docker, Tart, and Seatbelt backends are implemented. Network isolation, profiles, auxiliary directories, overlay mode, user-defined extensions, list filters, and profile recipes (`cap_add`, `devices`) are shipped.

For the full list of designed-but-unimplemented features, see [dev/plans/TODO.md](dev/plans/TODO.md). For design specs, see [design/](design/).

## Next up

- Profile setup commands (run custom scripts when a sandbox starts)
- Batch sandbox creation (`yoloai batch`)
- Sandbox chaining/pipelines
- Shared cache volumes for package managers
