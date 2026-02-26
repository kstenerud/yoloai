# yoloAI Roadmap

yoloAI is under active development. The current MVP covers the core copy/diff/apply workflow with Claude Code. Here's what's planned next.

## More agents

- OpenAI Codex support (the architecture is agent-agnostic — adding an agent is a definition, not a rewrite)
- Community-requested agents (Aider, Goose, etc.)

## Network isolation

- Domain-based allowlisting — let the agent reach its API but nothing else (`--network-isolated`, `--network-allow <domain>`)
- Proxy sidecar for fine-grained traffic control

## Profiles

- Reusable environment definitions (`~/.yoloai/profiles/<name>/`) with user-supplied Dockerfiles
- Per-profile config: custom mounts, resource limits, environment variables

## Overlayfs copy strategy

- Instant sandbox setup using overlayfs instead of full copy (space-efficient, fast for large repos)

## Other

- Auxiliary directory mounts (`-d` flag for read-only dependencies)
- Custom mount points (`=<path>` syntax)
- Auto-commit intervals for crash recovery
- Config file generation (`yoloai config generate`)
- User-defined extensions (`yoloai x <extension>`)
