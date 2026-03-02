# Unimplemented Features

Designed features not yet implemented. Each links to its design spec.
Create a plan file in this directory before starting implementation.

## Major Features

| Feature | Design Reference | Plan | Notes |
|---------|-----------------|------|-------|
| Extensions (`yoloai x`) | [commands.md](../../design/commands.md) | — | User-defined YAML commands in `~/.yoloai/extensions/` |

## Commands

| Feature | Design Reference | Plan | Notes |
|---------|-----------------|------|-------|
| `--power-user` on `setup` | [setup.md](../../design/setup.md) | — | Non-interactive setup for automation |
| `list` filters | [commands.md](../../design/commands.md) | — | `--running`, `--stopped` |

## Config Options

| Feature | Design Reference | Notes |
|---------|-----------------|-------|
| Recipes (`cap_add`, `devices`, `setup`) | [config.md](../../design/config.md) | Advanced setups (Tailscale, GPU) |
