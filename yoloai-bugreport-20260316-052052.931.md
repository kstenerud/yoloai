## yoloai Bug Report — 2026-03-16T05:20:52Z

> ⛔ UNSAFE REPORT — unsanitized, contains all logs and agent output.
> Do not share publicly.

**Version:** dev (ec21f2c, 2026-03-16T05:20:08Z)
**Type:** unsafe
**Command:** `yoloai ls --bugreport unsafe`

<details>
<summary>System</summary>

- **OS/Arch:** linux/amd64
- **Kernel:** Linux dev 6.17.4-1-pve #1 SMP PREEMPT_DYNAMIC PMX 6.17.4-1 (2025-12-03T15:42Z) x86_64 GNU/Linux
- **XDG_RUNTIME_DIR:** `/run/user/1000`
- **HOME:** `/home/karl`
- **Data dir:** `/home/karl/.yoloai`
- **Disk usage:** 134.0MB

</details>

<details>
<summary>Backends</summary>

- **docker:** available — Client: 29.2.1 / Server: 29.2.1
- **podman:** unavailable — podman socket not found: no podman socket found (checked $CONTAINER_HOST, $DOCKER_HOST, $XDG_RUNTIME_DIR/podman/podman.sock, /run/podman/podman.sock)
hint: run 'systemctl --user start podman.socket' or 'podman machine start'
- **tart:** unavailable — tart is not installed. Install it with: brew install cirruslabs/cli/tart
- **seatbelt:** unavailable — seatbelt backend requires macOS

</details>

<details>
<summary>Configuration</summary>

**Global config** (`/home/karl/.yoloai/config.yaml`):

```yaml
# yoloai global configuration
# These settings apply to all sandboxes regardless of profile.
# Run 'yoloai config set <key> <value>' to change settings.
#
# Available settings:
#   tmux_conf                Tmux configuration: default, default+host
#   model_aliases.<alias>    Custom model alias (overrides agent built-in aliases)

{tmux_conf: default+host}
```

**Profile config** (`/home/karl/.yoloai/profiles/base/config.yaml`):

```yaml
agent: claude
mounts:
    - ~/.gitconfig:/home/yoloai/.gitconfig:ro
ports: []
resources:
    cpus: 4
    memory: 8g
```

</details>

<details>
<summary>Live log</summary>

```
{"ts":"2026-03-16T05:20:53.225Z","level":"debug","msg":"list complete","event":"sandbox.list","count":1}
```

</details>

**Exit code:** 0
