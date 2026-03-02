# Unimplemented Features

Designed features not yet implemented. Each links to its design spec.
Create a plan file in this directory before starting implementation.

## Major Features

| Feature | Design Reference | Plan | Notes |
|---------|-----------------|------|-------|
| ~~Overlayfs copy strategy~~ | [config.md](../../design/config.md) | — | Done — `:overlay` directory mode |
| Codex agent polish | [commands.md](../../design/commands.md) | — | Agent definition exists; needs end-to-end testing |
| Extensions (`yoloai x`) | [commands.md](../../design/commands.md) | — | User-defined YAML commands in `~/.yoloai/extensions/` |

## Commands

| Feature | Design Reference | Plan | Notes |
|---------|-----------------|------|-------|
| ~~`--resume` on `start`~~ | [commands.md](../../design/commands.md) | — | Done |
| `--power-user` on `setup` | [setup.md](../../design/setup.md) | — | Non-interactive setup for automation |
| `list` filters | [commands.md](../../design/commands.md) | — | `--running`, `--stopped` |

## Config Options

| Feature | Design Reference | Notes |
|---------|-----------------|-------|
| `copy_strategy` | [config.md](../../design/config.md) | `auto \| overlay \| full` — global default for `:copy` dirs |
| `agent_files` | [config.md](../../design/config.md) | Files seeded into agent-state/ on first run |
| Recipes (`cap_add`, `devices`, `setup`) | [config.md](../../design/config.md) | Advanced setups (Tailscale, GPU) |
| ~~User-configurable model aliases~~ | [commands.md](../../design/commands.md) | Done — `model_aliases` in config.yaml |
| ~~Network isolation~~ | [commands.md](../../design/commands.md) | Done — `--network-isolated`, `--network-allow` |
| ~~Profiles~~ | [config.md](../../design/config.md) | Done — `--profile`, profile chain, Dockerfile support |
| ~~Auxiliary directories~~ | [commands.md](../../design/commands.md) | Done — `-d` flag with `:copy/:overlay/:rw` modes |
