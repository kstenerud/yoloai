> **Design documents:** [Overview](README.md) | [Commands](commands.md) | [Config](config.md) | [Setup](setup.md) | [Security](security.md)

# `yoloai reconfigure` Design

## Goal

Allow users to modify a sandbox's configuration after creation — adding directories, changing resource limits, updating env vars, etc. — while preserving all accumulated state (work copies, overlay layers, agent-state).

## Background

Sandbox configuration is currently fixed at creation time and stored in `environment.json`. Docker mounts are immutable once a container is created, so any change to directory layout or container parameters requires recreating the container. The sandbox's persistent state (`:copy` work trees, `:overlay` upper layers, `agent-state/`) all lives on the host and survives container recreation, so container recreation is safe.

`yoloai sandbox <name> allow/deny` already demonstrates a different pattern: live-patching the running container without a restart. `reconfigure` is for changes that cannot be live-patched.

## What is in scope

Changes that require container recreation and therefore an agent restart:

| Change | Flag(s) |
|---|---|
| Add an auxiliary directory | `-d <path>[:<mode>]` |
| Remove an auxiliary directory | `--remove-dir <path>` |
| Change a directory's mount mode | `-d <path>:<newmode>` (re-specify the dir) |
| Add or update an env var | `--env KEY=VAL` |
| Remove an env var | `--unset-env KEY` |
| Change CPU limit | `--cpus <value>` |
| Change memory limit | `--memory <value>` |
| Add or remove a capability | (future) |
| Change the agent | `--agent <name>` |

Changes that are **out of scope** (handled elsewhere):

- **Network allowlist** — `yoloai sandbox <name> allow/deny` already live-patches without restart.
- **Model** — changed inside the agent CLI itself.

## Command shape

```
yoloai reconfigure [flags] <name>
```

The command always requires an explicit sandbox name. There are no positional arguments beyond the name.

### Flags

```
  -d, --dir <path>[=<mountpath>][:<mode>]   Add an auxiliary directory (repeatable)
      --remove-dir <path>                   Remove an auxiliary directory by host path (repeatable)
      --env KEY=VAL                         Add or update an environment variable (repeatable)
      --unset-env KEY                       Remove an environment variable (repeatable)
      --cpus <value>                        CPU limit (e.g. 4, 2.5)
      --memory <value>                      Memory limit (e.g. 8g, 512m)
      --agent <name>                        Agent to use
  -y, --yes                                 Skip confirmations
  -a, --attach                              Attach after reconfigure completes
```

`-d` uses the same syntax as `yoloai new`: `<path>`, `<path>:copy`, `<path>:rw`, `<path>=<mountpath>:ro`, etc.

At least one flag must be provided; bare `yoloai reconfigure <name>` is an error.

## Behavior

### Execution sequence

1. Load `environment.json` for the named sandbox.
2. Validate all requested changes (paths exist, no conflicts, etc.).
3. Warn about changes that have consequences (see below).
4. Stop the running container if it is running (same as `yoloai stop`).
5. Apply directory setup for any new dirs (copy for `:copy`, create overlay dirs for `:overlay`).
6. Update `environment.json` with the new configuration.
7. Recreate and start the container with the updated mounts (same `recreateContainer()` path used by `start`).
8. Optionally attach (`--attach`).

### Delta semantics

- `-d` is **additive**: it adds a directory not already mounted. Re-specifying an existing host path with a different mode is an error — use `--remove-dir` first.
- `--remove-dir` removes by host path. If the directory has a `:copy` mode and has accumulated changes (unapplied diff), warn and require `--yes` or `--force` to proceed. Changes are not automatically applied.
- `--env KEY=VAL` adds or replaces a key. `--unset-env KEY` removes it. Both are additive to the existing env.
- `--cpus` / `--memory` replace the current value outright.
- `--agent` replaces the current agent. Agent-state for the previous agent remains on disk but the new agent starts fresh.

### Warnings and confirmations

| Situation | Behavior |
|---|---|
| Sandbox is running | Print "Stopping sandbox…" and stop it before proceeding. |
| `--remove-dir` targets a `:copy` dir with unapplied changes | Warn: "Removing this directory will discard unapplied changes. Use `yoloai apply` first, or pass `--force` to discard." Requires `--yes` or `--force`. |
| `--remove-dir` targets the workdir | Error: workdir cannot be removed. |
| `--agent` changes the agent | Warn: "Agent state from <old> will not be migrated. The new agent will start fresh." Requires `--yes`. |

## Output

Plain text (default):

```
Stopping sandbox my-task...
Adding /home/user/docs (read-only)...
Updating environment...
Starting sandbox my-task...
Reconfigured.
```

JSON (`--json`): emits the updated `Meta` after completion, same shape as `yoloai sandbox <name> info --json`.

## What is preserved

The following survive `reconfigure` unchanged:

- `:copy` work trees and their git baselines (`~/.yoloai/sandboxes/<name>/work/`)
- `:overlay` upper layers
- Agent state (`~/.yoloai/sandboxes/<name>/agent-state/`)
- Logs
- Files exchange dir (`~/.yoloai/sandboxes/<name>/files/`)

## Open questions

- Should re-specifying an existing dir with the same mode be a no-op or an error? (Probably a no-op with a notice.)
- Should `--remove-dir` on a `:copy` dir with changes offer to apply them automatically, or just warn? Auto-apply could be surprising.
- Should there be a `--no-start` flag to update config without immediately restarting (for scripting)?
