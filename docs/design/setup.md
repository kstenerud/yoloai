> **Design documents:** [Overview](README.md) | [Commands](commands.md) | [Config](config.md) | [Security](security.md) | [Research](../dev/RESEARCH.md)

## First-Run Experience

### Setup tracking

`config.yaml` includes a `setup_complete` field (boolean, default `false`). This is the sole signal for whether the first-run experience has been completed. The field is explicitly set to `true` only after setup finishes successfully. This decouples first-run detection from config file existence — we can create/modify `config.yaml` at any point without accidentally suppressing the new-user prompts.

### `EnsureSetup` (runs on every `yoloai new`)

`EnsureSetup` is called at the start of `yoloai new`. It is idempotent and safe to call repeatedly:

1. Create `~/.yoloai/` directory structure if absent.
2. Write a default `config.yaml` with sensible defaults if missing.
3. Seed Dockerfile.base and entrypoint.sh (overwrite if embedded version changed).
4. Build the base image if missing or outdated.
5. **If `setup_complete` is false:** run the new-user experience (see below).
6. Print shell completion instructions (first run only).

Steps 1-4 are non-interactive and always run. Step 5 is interactive and only runs during `yoloai new` (not `start`, `attach`, etc.) because `new` is the most likely first command and has access to stdin.

### New-user experience (step 5)

When `setup_complete` is false, `EnsureSetup` runs an interactive prompt sequence. Currently the only prompt is tmux configuration, but the framework supports adding future prompts without changing the detection mechanism.

**Tmux configuration prompt:**

Detect the user's `~/.tmux.conf` and classify:
- **No config exists:** New user. Show yoloai's defaults and prompt.
- **Small config (≤10 significant lines):** Likely a new user who cobbled together just enough to make tmux work. Show their config, show ours, prompt.
- **Large config (>10 significant lines):** Power user. Skip the prompt, use `host` mode (their config sourced after ours).

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

- **`Y` (default):** Set `tmux_conf: default+host` in config. yoloai defaults sourced first, user config sourced second. User settings win on conflict (tmux is purely last-write-wins, no subtle ordering issues). Set `setup_complete: true`.
- **`n`:** Set `tmux_conf: host`. Only user's config is used, no yoloai defaults. Set `setup_complete: true`.
- **`p`:** Print the concatenated config (yoloai defaults + user config) to stdout and exit. Do **not** set `setup_complete: true`. User can review, hand-edit their `~/.tmux.conf`, and run `yoloai new` again — the prompt re-fires because `setup_complete` is still false.

For the no-config case, `[n]` means raw tmux defaults (equivalent to `tmux_conf: none`), and `[p]` prints only yoloai's defaults.

After all prompts complete successfully, set `setup_complete: true` in `config.yaml` and print:

```
Setup complete. To re-run setup at any time: yoloai system setup
```

### `yoloai system setup`

Dedicated interactive setup command. Always runs the full new-user experience regardless of `setup_complete` — treats it as if `setup_complete` is false. This lets users redo their choices if they regret something. Shows current settings as defaults in prompts (e.g., if `tmux_conf` is already `host`, the `[n]` option is pre-selected).

**[PLANNED] `--power-user` flag:** Skip all interactive prompts. For automation (Ansible, dotfiles scripts, CI):
- No `~/.tmux.conf` exists → set `tmux_conf: default` (yoloai defaults only).
- `~/.tmux.conf` exists → set `tmux_conf: default+host` (yoloai defaults + user config, no questions asked — assume they know what they want). Power users who want *only* their config can set `tmux_conf: host` in `config.yaml` directly. Power users who want *only* yoloai defaults can supply an empty `~/.tmux.conf`.
- Perform non-interactive steps (directory creation, image build).
- Set `setup_complete: true` and exit.

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
defaults:
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

