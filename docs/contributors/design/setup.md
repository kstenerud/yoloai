> **ABOUTME:** Design for first-run readiness and the `yoloai system setup` wizard — the
> non-interactive library-side setup step, the one-time CLI onboarding tip, and the sole
> interactive path for choosing tmux/backend/agent defaults. One page of the split design-doc set.

> **Design documents:** [Overview](README.md) | [Commands](commands.md) | [Config](config.md) | [Security](security.md) | [Environments](environments.md) | [Research](research/README.md) | [research/](research/)

## First-Run Experience

There is no `setup_complete` flag and no interactive prompt on `yoloai new`. First-run readiness is split into a non-interactive library step and an opt-in CLI wizard.

### `EnsureSetup` (library, non-interactive)

`EnsureSetup` runs at the start of `Client.CreateSandbox` (and from the MCP server). It is idempotent, safe to call on every run, and **never prompts** — "wizard-has-run" ceremony is the app's concern, not the library's. It:

1. Scaffolds the data directory if absent (the library owns everything under its `DataDir`; under the CLI that root is `~/.yoloai/library/`).
2. Writes a default `config.yaml` with opinionated declarative defaults if missing — notably `tmux_conf: default+host`, so the common case "just works" with no question asked.
3. Seeds `Dockerfile.base` and `entrypoint.sh` (overwriting only when the embedded version changed).
4. Builds the base image if missing or outdated.

The data directory itself is *created/validated* by the startup gate, not by setup — see the migration-gate design ([`archive/plans/migration-gate.md`](../archive/plans/migration-gate.md)). On an un-migrated install the gate fails fast and tells the user to run `yoloai system migrate`; it never auto-migrates.

### One-time CLI tip (app-side)

After the first successful `yoloai new`, the CLI prints a single onboarding nudge ("enable shell completions"). This is app state, keyed off `first_run_tip_shown` in `~/.yoloai/cli/state.yaml` (the CLI realm) — *not* library state. Migration carries a legacy `setup_complete: true` forward to `first_run_tip_shown: true`, so upgraders don't see the tip again.

### `yoloai system setup` (the only interactive path)

The dedicated, opt-in wizard. It is the sole place that prompts the user, and it lives **entirely in the CLI** — there is no library `Setup`/`SetupStatus` verb (D65 collapsed the wizard into the app; the library exposes no setup ceremony). It discovers what the host can offer through the read model — `System().BackendTypes(…)` and `System().AgentTypes(…)` for the available choices, plus the host probes behind `yoloai doctor` — prompts for tmux config / default backend / default agent, then persists each answer through the ordinary config surface, `System().Config().Set(ctx, key, value)` (the same path as `yoloai config set`). Re-runnable at any time to redo choices.

**`--agent`, `--backend`, `--tmux-conf` flags** skip the matching prompt by supplying the choice directly (for automation: Ansible, dotfiles scripts, CI). Backend/agent prompts are also skipped automatically when only one option is available.

**Tmux configuration prompt:**

Detect the user's `~/.tmux.conf` and classify:
- **No config exists:** Show yoloai's defaults and prompt.
- **Small config (≤10 significant lines):** Likely a new user who cobbled together just enough to make tmux work. Show their config, show ours, prompt.
- **Large config (>10 significant lines):** Power user. Skip the prompt and auto-pick `default+host` (their config sourced after ours).

"Significant lines" = non-blank, non-comment lines (after stripping leading whitespace and `#`-prefixed lines).

For the small/no config case:

```
yoloai uses tmux in sandboxes. Your tmux config is minimal, so we'll
include sensible defaults (mouse scroll, colors, vim-friendly settings).

Your config (~/.tmux.conf):
  set-option -g default-shell /bin/bash
  set -g default-command "${SHELL}"

  [Y] Use yoloai defaults + your config (yours overrides on conflict)
  [n] Use only your config as-is
  [p] Print merged config and exit (for manual review)
```

- **`Y` (default):** `tmux_conf: default+host` — yoloai defaults sourced first, user config second; user settings win on conflict (tmux is last-write-wins, no subtle ordering issues).
- **`n`:** `tmux_conf: host` (no-config case: `none`, raw tmux). Only the user's config is used.
- **`p`:** Print the concatenated config to stdout and exit **without writing** anything. The user can review, hand-edit their `~/.tmux.conf`, and re-run `yoloai system setup`.

On completion the wizard prints:

```
Setup complete. To re-run setup at any time: yoloai system setup
```

## Tmux Configuration

yoloAI sandboxes use tmux for agent interaction. Tmux's out-of-the-box defaults are notoriously hostile to new users (no mouse scroll, escape delay, login shells on every pane, tiny scrollback). At the same time, experienced tmux users have carefully tuned configs they don't want overridden.

### Container tmux defaults

The container ships a `/yoloai/tmux.conf` with sensible defaults:

```
# --- yoloai sensible defaults ---
# Mouse support (scroll, click, resize)
set -g mouse on

# No escape delay (critical for vim/neovim)
set -sg escape-time 0

# Color support
set -g default-terminal "tmux-256color"

# Window/pane numbering from 1 (matches keyboard layout)
set -g base-index 1
setw -g pane-base-index 1

# Larger scrollback (default 2000 is tiny)
set -g history-limit 50000

# Non-login shell (prevents .bash_profile re-sourcing on every pane)
set -g default-command "${SHELL}"

# Auto-renumber windows on close
set -g renumber-windows on

# Longer message display (default 750ms too fast to read)
set -g display-time 4000

# Forward focus events (vim autoread, etc.)
set -g focus-events on

# System clipboard via OSC 52
set -g set-clipboard on
```

### User config handling

The `tmux_conf` setting in `config.yaml` controls how user tmux config interacts with the container:

```yaml
tmux_conf: default+host    # default+host | default | host | none
```

- **`default+host`**: yoloai sensible defaults sourced first, then user's `~/.tmux.conf` (bind-mounted read-only). User settings override on conflict. This is the recommended mode and what the new-user prompt sets on `[Y]`.
- **`default`**: yoloai sensible defaults only. No host config mounted. Set when user has no `~/.tmux.conf`, or when `--power-user` is used without a host config.
- **`host`**: User's `~/.tmux.conf` only. No yoloai defaults. For power users who want full control. Set on `[n]` in the prompt, or directly in config.
- **`none`**: Raw tmux with no config files. For debugging or users who want full control via profile Dockerfiles.

### Entrypoint integration

The entrypoint sources tmux config based on the `tmux_conf` value passed via `config.json`:

| `tmux_conf` | `/yoloai/tmux.conf` | `/home/yoloai/.tmux.conf` |
|---|---|---|
| `default+host` | sourced first | bind-mounted, sourced second |
| `default` | sourced | not mounted |
| `host` | not sourced | bind-mounted, sourced |
| `none` | not sourced | not mounted |

Implemented by passing `-f /yoloai/tmux.conf` to `tmux new-session` when applicable, then `tmux source-file /home/yoloai/.tmux.conf` if the file exists. User settings win on conflict since they're sourced second. Tmux config is purely declarative and last-write-wins — no subtle ordering issues.

