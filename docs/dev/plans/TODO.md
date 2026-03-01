# Unimplemented Features

Designed features not yet implemented. Each links to its design spec.
Create a plan file in this directory before starting implementation.

## Major Features

| Feature | Design Reference | Plan | Notes |
|---------|-----------------|------|-------|
| Overlayfs copy strategy | [config.md](../../design/config.md) | — | `copy_strategy: auto \| overlay \| full`; instant setup, space-efficient |
| Network isolation | [commands.md](../../design/commands.md), [security.md](../../design/security.md) | — | `--network-isolated`, proxy sidecar, iptables, DNS control |
| Profiles | [config.md](../../design/config.md) | — | `~/.yoloai/profiles/<name>/` with Dockerfile + profile.yaml |
| Codex agent | [commands.md](../../design/commands.md) | — | Agent definition exists; needs end-to-end testing and polish |
| [DONE] Aux dirs (`-d`) | [commands.md](../../design/commands.md) | — | Repeatable flag for auxiliary directories |
| [DONE] Custom mount points (`=<path>`) | [commands.md](../../design/commands.md) | — | Mount directories at custom container paths |
| Extensions (`yoloai x`) | [commands.md](../../design/commands.md) | — | User-defined YAML commands in `~/.yoloai/extensions/` |
| Sandbox context file | [commands.md](../../design/commands.md) | — | `/yoloai/context.md` describing environment for the agent |

## Commands

| Feature | Design Reference | Plan | Notes |
|---------|-----------------|------|-------|
| ~~`restart`~~ | [commands.md](../../design/commands.md) | — | Implemented |
| `--resume` on `start` | [commands.md](../../design/commands.md) | — | Re-feed original prompt with continuation preamble |
| `--power-user` on `setup` | [setup.md](../../design/setup.md) | — | Non-interactive setup for automation |
| `profile create` | [config.md](../../design/config.md) | — | Scaffold Dockerfile + profile.yaml |
| `profile list` | [config.md](../../design/config.md) | — | List profiles in `~/.yoloai/profiles/` |
| `profile delete` | [config.md](../../design/config.md) | — | Delete profile with confirmation |
| `--json` flag | [commands.md](../../design/commands.md) | [json-flag.md](json-flag.md) | Structured output for scripting |
| `list` filters | [commands.md](../../design/commands.md) | — | `--running`, `--stopped`, `--json` |
| `build` profile/--all | [commands.md](../../design/commands.md) | — | Build specific profile or all images |

## Config Options

| Feature | Design Reference | Notes |
|---------|-----------------|-------|
| `copy_strategy` | [config.md](../../design/config.md) | `auto \| overlay \| full` |
| `auto_commit_interval` | [config.md](../../design/config.md) | Background auto-commit for `:copy` dirs |
| `agent_files` | [config.md](../../design/config.md) | Files seeded into agent-state/ on first run |
| `mounts` | [config.md](../../design/config.md) | Bind mounts added at container run time |
| [DONE] `env` | [config.md](../../design/config.md) | Environment variables forwarded to container via /run/secrets/ |
| `network_isolated` | [config.md](../../design/config.md) | Enable network isolation by default |
| `network_allow` | [config.md](../../design/config.md) | Additional allowed domains |
| `resources` | [config.md](../../design/config.md) | Container resource limits (cpus, memory) |
| Recipes (`cap_add`, `devices`, `setup`) | [config.md](../../design/config.md) | Advanced setups (Tailscale, GPU) |
| [DONE] Tilde expansion in paths | [config.md](../../design/config.md) | `~/` expanded via `ExpandPath()` in dir args, prompt files, etc. Config path fields will get tilde expansion when implemented. |
| [DONE] Env var interpolation | [config.md](../../design/config.md) | `${VAR}` syntax in CLI path args and config.yaml values via `expandEnvBraced()` |
| User-configurable model aliases | [commands.md](../../design/commands.md) | Custom aliases + version pinning |
