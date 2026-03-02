# yoloAI Roadmap

Core copy/diff/apply workflow ships today with Claude Code, Gemini CLI, Aider, Codex, and OpenCode agents. Docker, Tart, and Seatbelt backends are implemented. Network isolation, profiles, auxiliary directories, and overlay mode are shipped.

For the full list of designed-but-unimplemented features, see [dev/plans/TODO.md](dev/plans/TODO.md). For design specs, see [design/](design/).

## Next up

- **Codex agent polish** — agent definition exists, needs end-to-end verification

## Also planned

- User-defined extensions (`yoloai x`)
- `list` filters (`--running`, `--stopped`)
- macOS VM backend improvements (Tart)
- Recipes in profiles (`cap_add`, `devices`, `setup`)
