# CLI Critique — Deferred Items

Items from the CLI critique that were not addressed in Pass 1 (flag validation, --json, overlay wiring, Cobra groups). Organized by planned pass.

## Pass 2: New flags/features on existing commands

Completed in Pass 2:
- `attach --resume` — reconnect to a detached agent session
- `start/restart --prompt/--prompt-file` — re-send or replace prompt on start
- `apply --dry-run` — show what would be applied without applying
- `apply` refs + path filters combined — now works together
- `diff --name-only` — list changed files without content
- `new --env KEY=VAL` — pass environment variables to the sandbox
- `sandbox prompt` / `sandbox config` — read-only inspection of sandbox settings

Dropped (already working):
- `new` without workdir — already works when `--profile` is set
- `reset --clean` on stopped sandboxes — already works (stop is a no-op when already stopped)

## Pass 3: New commands

Completed in Pass 3:
- `sandbox clone` — clone an existing sandbox

## Later review

- Command classification issues (section 3 of critique) — whether commands are in the right groups
- `--no-*` flag naming inconsistency — some flags use `--no-X`, others don't follow a pattern
- `sandbox network-allow` verb naming — whether this should be a different verb
