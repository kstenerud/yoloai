# Breaking Changes

Tracks breaking changes made during beta. Each entry should be included in release notes for the version that introduces it.

## Unreleased

### `sandbox <name> log` redesigned around structured JSONL

**Previous behavior:** `yoloai log <name>` (and `yoloai sandbox <name> log`) displayed the raw agent terminal output (`logs/agent.log`) with ANSI stripped by default. `--raw` preserved ANSI escape sequences. `--json` returned `{"content": "..."}`.

**New behavior:** Default view is a pretty-printed, merge-sorted stream of all four structured JSONL logs (`logs/cli.jsonl`, `logs/sandbox.jsonl`, `logs/monitor.jsonl`, `logs/agent-hooks.jsonl`). Agent terminal output is accessed via dedicated flags. `--raw` now emits raw JSONL lines.

New flags:
- `--agent` — show agent terminal output with ANSI stripped (replaces old default)
- `--agent-raw` — show raw agent terminal stream (replaces old `--raw`)
- `--raw` — emit structured log as raw JSONL (new meaning)
- `--source cli,sandbox,monitor,hooks` — filter to specific log sources
- `--level debug|info|warn|error` — filter by minimum level (default: `info`)
- `--since <duration|time>` — filter by timestamp (e.g. `5m` or `14:20:00`)
- `--follow` / `-f` — tail all sources live; auto-exits when sandbox is done

`--json` flag no longer has special handling for the log command.

**Rationale:** Structured JSONL is the primary log format — the pretty-printed interleaved view is far more useful than raw terminal output for debugging. Agent output is still accessible via `--agent`/`--agent-raw` for cases where it's needed.

**Migration:**
- `yoloai log <name>` — now shows structured log. Add `--agent` to see agent output as before.
- `yoloai log <name> --raw` — now emits raw JSONL. Use `--agent-raw` for old behavior.
- `yoloai log <name> --json` — no longer returns `{"content": "..."}`. Read `logs/agent.log` directly.

### Caret encoding updated to current spec

**Previous behavior:** Caret encoding used raw hex for all unsafe characters (e.g., `/` → `^2F`, `^` → `^5E`). `.` and `~` were encoded as unsafe. The decoder accepted `g/G`, `h/H`, `i/I`, `j/J` as hex width modifiers (3-6 digit forms).

**New behavior:** Caret encoding uses single-letter shortcuts where defined by the spec (e.g., `/` → `^s`, `^` → `^^`, `:` → `^k`, `@` → `^o`). `.` and `~` are now in the safe set (passed through unencoded), except `.` is still encoded when it's the last character of a path component (trailing dots are stripped by Windows). The decoder treats `g`–`v` (and uppercase) as shortcut codes, not hex width modifiers. Hex encoding (`^2F`, etc.) is still accepted by the decoder for backward compatibility.

**Rationale:** Aligns with the current caret-encoding spec (https://github.com/kstenerud/caret-encoding). Shortcuts produce shorter, more readable directory names. `.` and `~` are "Problematic" (position-dependent) in the spec, not "Reserved", and are shown unencoded in spec examples.

**Migration:** Existing sandbox directory names on disk will not match new encodings (e.g., `^2Fhome^2Fuser` → `^shome^suser`). Destroy and recreate affected sandboxes. No automatic migration — this is a pre-release change.

### `reset` redesigned: in-place default, cache/files cleared, new flags

**Previous behavior:** `yoloai reset` stopped and restarted the container by default. `--no-restart` kept the agent running (in-place reset). `--clean` wiped agent state and cache. `--clean` and `--no-restart` were mutually exclusive.

**New behavior:** `yoloai reset` resets in-place by default (agent stays running). Cache and files directories are cleared by default. New flags:
- `--restart` — stop and restart container
- `--clear-state` — wipe agent runtime state (replaces `--clean`, implies `--restart`)
- `--keep-cache` — preserve cache directory
- `--keep-files` — preserve files directory
- `--attach` now implies `--restart`

Automatic upgrades to `--restart`: overlay mode, container not running, or `--clear-state` set.

**Rationale:** In-place reset is the better default — it preserves agent context while syncing workspace changes. The old `--no-restart` flag required opting in to the better UX. Cache and files are now cleared by default for a clean slate, with `--keep-X` flags for opt-out. `--clean` mixed too many concerns (agent state + cache); `--clear-state` is more precise.

**Migration:**
- `yoloai reset <name>` — now resets in-place (was restart). Add `--restart` to stop and restart the container.
- `yoloai reset <name> --no-restart` — remove `--no-restart` (now the default).
- `yoloai reset <name> --clean` — replace with `--clear-state`. Note: cache is now cleared by default, so `--clear-state` only adds agent runtime state wipe.
- Cache/files are now cleared by default. Add `--keep-cache` and/or `--keep-files` to preserve them.

### `files` command: name before subcommand

**Previous behavior:** `yoloai files put <sandbox> <file>...` — subcommand before sandbox name.

**New behavior:** `yoloai files <sandbox> put <file>...` — sandbox name before subcommand.

**Rationale:** Name-first ordering is more ergonomic (name is the "context", action is the "verb") and consistent with top-level commands (`yoloai diff <name>`) that already put the name first.

**Migration:** Swap the sandbox name and subcommand positions. For example, `yoloai files put mybox file.txt` becomes `yoloai files mybox put file.txt`.

### `sandbox` command: name before subcommand

**Previous behavior:** `yoloai sandbox info <name>`, `yoloai sandbox log <name>` — subcommand before sandbox name.

**New behavior:** `yoloai sandbox <name> info`, `yoloai sandbox <name> log` — sandbox name before subcommand. `sandbox list` is unchanged (no sandbox name).

**Rationale:** Same as `files` — name-first ordering is more ergonomic and consistent with top-level commands.

**Migration:** Swap the sandbox name and subcommand positions. For example, `yoloai sandbox info mybox` becomes `yoloai sandbox mybox info`.

### `sandbox network` flattened to `allow`/`allowed`/`deny`

**Previous behavior:** Network allowlist management used nested subcommands: `yoloai sandbox network add <name> <domain>...`, `yoloai sandbox network list <name>`, `yoloai sandbox network remove <name> <domain>...`.

**New behavior:** Flattened to direct subcommands with name-first ordering: `yoloai sandbox <name> allow <domain>...`, `yoloai sandbox <name> allowed`, `yoloai sandbox <name> deny <domain>...`.

**Rationale:** Reduces nesting depth and uses clearer verb names (`allow`/`deny` instead of `add`/`remove`, `allowed` instead of `list`).

**Migration:** Replace `sandbox network add` with `sandbox <name> allow`, `sandbox network list` with `sandbox <name> allowed`, `sandbox network remove` with `sandbox <name> deny`.

### `sandbox clone` removed

**Previous behavior:** `yoloai sandbox clone <src> <dst>` was available as an alias for `yoloai clone`.

**New behavior:** Only the top-level `yoloai clone <src> <dst>` is available.

**Rationale:** The `sandbox clone` alias conflicted with the name-first dispatch pattern (where `clone` would be interpreted as a sandbox name). The top-level command is the canonical form.

**Migration:** Replace `yoloai sandbox clone` with `yoloai clone`.

### `files get` signature changed: positional destination replaced with `-o` flag

**Previous behavior:** `yoloai files get <sandbox> <file> [dst]` — single file, optional positional destination argument.

**New behavior:** `yoloai files get <sandbox> <file/glob>... [-o dir]` — multiple files/globs, destination specified via `-o`/`--output` flag (defaults to `.`).

**Rationale:** Positional destination prevented accepting multiple file arguments. The `-o` flag is a standard convention (`curl -o`, `tar -C`) and removes ambiguity between file arguments and the destination.

**Migration:** Replace `yoloai files get <sandbox> <file> <dst>` with `yoloai files get <sandbox> <file> -o <dst>`.

### Entrypoint shell scripts consolidated into Python

**Previous behavior:** Each backend had its own shell entrypoint script: `entrypoint-user.sh` for Docker, `entrypoint.sh` for seatbelt, and `setup.sh` for Tart. These scripts contained ~80 lines of near-identical logic for config reading, tmux setup, agent launch, ready-pattern detection, prompt delivery, and status monitoring.

**New behavior:** A single Python script `sandbox-setup.py` replaces all three shell scripts. Backend-specific setup is dispatched by a CLI argument (`docker`, `seatbelt`, `tart`). The script is embedded in the Go binary via `runtime/monitor/` and deployed identically to `status-monitor.py`. The Docker root entrypoint (`entrypoint.sh`) remains shell — it handles system-level setup (iptables, usermod, gosu).

**Rationale:** The duplicated shell logic meant every bug fix or feature change had to be applied three times. Shell is also fragile for the complex polling/state logic these scripts contain. Python provides `json.load()` (eliminating 8+ `jq` calls per script), proper string handling, and threading for background tasks.

**Migration:** If you customized `entrypoint-user.sh` in a Docker profile, port your changes to Python by modifying the `setup_docker()` function in `sandbox-setup.py`. Docker images must be rebuilt (`yoloai system build`).

### Legacy sandbox support removed

**Previous behavior:** Old sandboxes (created before the directory layout reorganization) were supported via automatic fallbacks to legacy file names (`meta.json`, `config.json`, `state.json`, `status.json`, `agent-state/`) and legacy file locations (PID files, tmux sockets, profile files at the sandbox root). Config migration from the old flat `~/.yoloai/` layout ran automatically on startup.

**New behavior:** Legacy fallbacks are removed. Only the current file names and directory layout are supported. Config migration from the old flat layout is removed. The `destroy` command always succeeds (returns nil if the sandbox directory doesn't exist, warns instead of failing on directory removal errors). Non-destroy commands that fail on a sandbox include the sandbox directory path and a `yoloai destroy` hint in the error message.

**Rationale:** Legacy support was causing recurring issues during sandbox start, reset, and destroy operations. During early development, maintaining backward compatibility with old sandboxes added complexity without sufficient benefit.

**Migration:** Destroy old sandboxes with `yoloai destroy <name>` and recreate them. If you have an old `~/.yoloai/config.yaml` with the `defaults:` nesting from the pre-profile layout, delete `~/.yoloai/` and run `yoloai setup`.

### Sandbox directory layout reorganized; `YOLOAI_DIR` abstraction added

**Previous behavior:** Sandbox state files had generic names (`meta.json`, `config.json`, `state.json`, `status.json`) in a flat layout. The `agent-state/` directory held agent runtime state. Docker hardcoded `/yoloai/` paths; seatbelt and tart used different variable names. Scripts, tmux config, and backend-specific files all lived at the sandbox root.

**New behavior:** Files are renamed for clarity and organized into subdirectories:
- `meta.json` → `environment.json`
- `config.json` → `runtime-config.json`
- `state.json` → `sandbox-state.json`
- `status.json` → `agent-status.json`
- `agent-state/` → `agent-runtime/`
- Scripts moved to `bin/` (entrypoint.sh, status-monitor.py, diagnose-idle.sh)
- Tmux config moved to `tmux/` (tmux.conf, tmux.sock)
- Backend-specific files moved to `backend/` (instance.json, profile.sb, pid, stderr.log)

All entrypoint scripts now use `$YOLOAI_DIR` instead of hardcoded paths. Docker sets `ENV YOLOAI_DIR=/yoloai`, seatbelt exports `YOLOAI_DIR=$SANDBOX_DIR`, tart exports `YOLOAI_DIR=$SHARED_DIR`.

**Rationale:** Generic names like `config.json` and `status.json` didn't convey purpose. The flat layout mixed scripts, configs, state, and backend files. Hardcoded `/yoloai/` paths in hook commands broke on seatbelt (where the sandbox dir is a host-local path, not `/yoloai/`).

**Migration:** Automatic. The code checks for new filenames first, then falls back to legacy names. Existing sandboxes continue to work. New sandboxes use the new layout. Docker images must be rebuilt (`yoloai system build`) for new sandboxes.

### Sandbox status `running` renamed to `active`; `--running` flag renamed to `--active`

**Previous behavior:** The agent status was `"running"` when actively working. `yoloai ls --running` filtered for active sandboxes.

**New behavior:** The agent status is `"active"`. `yoloai ls --active` filters for active sandboxes.

**Rationale:** `"running"` was ambiguous -- the container process is also "running" when the agent is idle. `"active"` clearly means the agent is actively working on a task.

**Migration:** Replace `--running` with `--active` in scripts. Old sandboxes with `"running"` in the agent status file are handled automatically (backward compatible parsing).

### `container_id` removed from JSON output

**Previous behavior:** `yoloai ls --json` and `yoloai sandbox info --json` included a `container_id` field in the output.

**New behavior:** The `container_id` field is no longer present.

**Rationale:** The field was always empty — it was never populated with a value. Removing it cleans up the JSON API.

**Migration:** Remove any code that reads `container_id` from yoloAI JSON output. The field was always empty, so no information is lost.

### `yoloai new --replace` renamed to `--force`

**Previous behavior:** `yoloai new --replace` replaced an existing sandbox with the same name.

**New behavior:** `yoloai new --force` replaces an existing sandbox. `--replace` still works but prints a deprecation warning to stderr and will be removed in a future release.

**Rationale:** `--force` is the standard convention for "proceed despite conflict" across CLI tools (docker, git, etc.). `--replace` was non-standard and also conflicted with the `--force` flag used in `apply` for a similar "override safety check" purpose.

**Migration:** Replace `--replace` with `--force` in scripts. `--replace` continues to work during the deprecation period.

### `yoloai new` no longer auto-attaches by default

**Previous behavior:** `yoloai new` auto-attached to the tmux session after creation. `--detach`/`-d` skipped the attach.

**New behavior:** `yoloai new` starts the sandbox in the background (detached). Use `--attach`/`-a` to auto-attach. `--detach`/`-d` is removed.

**Also applies to:** `yoloai start` now supports `--attach`/`-a` with the same semantics (detached by default).

**Rationale:** Consistent unix-y model — both `new` and `start` are detached by default, both accept `-a` to attach. Avoids confusing asymmetry where `new` used `-d` (detach) while `start` used `-a` (attach).

**Migration:** Add `-a` to auto-attach: `yoloai new -a ...`. Remove `-d`/`--detach` from any scripts.

### Tmux mouse mode no longer enabled by default

**Previous behavior:** Sandbox tmux sessions had `set -g mouse on`, enabling mouse-wheel scrolling, click-to-select-pane, and drag-to-resize. OSC 52 clipboard and `MouseDragEnd1Pane` bindings were configured to compensate for mouse mode breaking copy/paste.

**New behavior:** Mouse mode is off. Text selection and copy/paste work normally via the terminal. Scrollback is accessed with `^b [` (shown in the status bar). The OSC 52 and MouseDragEnd workarounds are removed.

**Rationale:** Mouse mode breaks copy/paste in many terminal emulators, and the clipboard workarounds (OSC 52, MouseDragEnd1Pane pipe-and-cancel) don't work reliably across all setups. Broken copy is worse UX than needing a keybinding to scroll.

**Migration:** If you prefer mouse mode, add `set -g mouse on` to your own `~/.tmux.conf` or a custom profile's tmux config.

### `--backend` moved from global flag to per-command flag

**Previous behavior:** `--backend` was a global flag available on all commands.

**New behavior:** `--backend` is a local flag on `new`, `build`, and `setup` only. Lifecycle commands (`start`, `stop`, `destroy`, `reset`, `attach`, `exec`, `sandbox info`) read the backend from the sandbox's `meta.json` automatically. `list` uses the config default.

**Rationale:** The backend is a property of the sandbox, not the CLI invocation. Lifecycle commands should use the backend the sandbox was created with, not require the user to remember and pass it every time.

**Migration:** Remove `--backend` from lifecycle command invocations. If you were passing `--backend` to `start`/`stop`/etc., it now happens automatically via the sandbox's environment metadata.

### Config paths restructured: `defaults.` prefix removed, config moved to profile

**Previous behavior:** Config lived at `~/.yoloai/config.yaml` with settings nested under `defaults:` (e.g., `defaults.backend`, `defaults.agent`, `defaults.env.<NAME>`). Operational state (`setup_complete`) was stored in the same file.

**New behavior:** Config lives at `~/.yoloai/profiles/base/config.yaml` with a flat schema (e.g., `backend`, `agent`, `env.<NAME>`). Operational state moved to `~/.yoloai/state.yaml`. Resource files (Dockerfile, entrypoint.sh, tmux.conf) moved from `~/.yoloai/` to `~/.yoloai/profiles/base/`.

**Rationale:** Base config is now a profile — same structure and code path as user profiles. Flat schema is simpler and the `defaults:` wrapper added no value. Separating operational state from user preferences keeps config clean.

**Migration:** Automatic. On first run, yoloai detects the old layout and migrates: moves resource files to `profiles/base/`, flattens `defaults:` mapping to root level in `profiles/base/config.yaml`, extracts `setup_complete` to `state.yaml`. For manual config commands, drop the `defaults.` prefix (e.g., `yoloai config set backend docker` instead of `yoloai config set defaults.backend docker`).

### `tmux_conf` and `model_aliases` moved to global config

**Previous behavior:** `tmux_conf` and `model_aliases` were stored in the base profile config (`~/.yoloai/profiles/base/config.yaml`).

**New behavior:** These settings are stored in the global config (`~/.yoloai/config.yaml`), which is not overridable by profiles.

**Rationale:** `tmux_conf` and `model_aliases` are user preferences that should apply to all sandboxes regardless of profile. They don't belong in profile-overridable config.

**Migration:** Automatic. On first run, yoloai moves `tmux_conf` and `model_aliases` from the base profile config to the global config. No manual action needed.
