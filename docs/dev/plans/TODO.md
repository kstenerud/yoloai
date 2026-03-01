# Unimplemented Features

Designed features not yet implemented. Each links to its design spec.
Create a plan file in this directory before starting implementation.

## Major Features

| Feature | Design Reference | Plan | Notes |
|---------|-----------------|------|-------|
| Overlayfs copy strategy | [config.md](../../design/config.md) | — | `copy_strategy: auto \| overlay \| full`; instant setup, space-efficient |
| Codex agent | [commands.md](../../design/commands.md) | — | Agent definition exists; needs end-to-end testing and polish |
| Extensions (`yoloai x`) | [commands.md](../../design/commands.md) | — | User-defined YAML commands in `~/.yoloai/extensions/` |

## Commands

| Feature | Design Reference | Plan | Notes |
|---------|-----------------|------|-------|
| `--resume` on `start` | [commands.md](../../design/commands.md) | — | Re-feed original prompt with continuation preamble |
| `--power-user` on `setup` | [setup.md](../../design/setup.md) | — | Non-interactive setup for automation |
| `--json` flag | [commands.md](../../design/commands.md) | [json-flag.md](json-flag.md) | Structured output for scripting |
| `list` filters | [commands.md](../../design/commands.md) | — | `--running`, `--stopped`, `--json` |

## Config Options

| Feature | Design Reference | Notes |
|---------|-----------------|-------|
| `copy_strategy` | [config.md](../../design/config.md) | `auto \| overlay \| full` |
| `auto_commit_interval` | [config.md](../../design/config.md) | Background auto-commit for `:copy` dirs |
| `agent_files` | [config.md](../../design/config.md) | Files seeded into agent-state/ on first run |
| `mounts` | [config.md](../../design/config.md) | Bind mounts added at container run time |
| `network_isolated` | [config.md](../../design/config.md) | Enable network isolation by default |
| `network_allow` | [config.md](../../design/config.md) | Additional allowed domains |
| Recipes (`cap_add`, `devices`, `setup`) | [config.md](../../design/config.md) | Advanced setups (Tailscale, GPU) |
| User-configurable model aliases | [commands.md](../../design/commands.md) | Custom aliases + version pinning |
