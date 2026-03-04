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

## Pass 4: Remaining CLI critiques

Completed in Pass 4:
- Command classification — moved `files` and `x` from Admin to Workflow in design doc
- `--no-*` flag naming — documented convention in CLI-STANDARD.md (--no-X for default-on, --X for default-off)
- `sandbox network-allow` naming — replaced with `sandbox network {add,list,remove}` namespace
