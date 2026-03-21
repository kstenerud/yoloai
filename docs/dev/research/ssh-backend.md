# SSH Backend Design

Research/design date: 2026-03-21

## Problem Statement

All existing yoloAI backends run locally: Docker/Podman/containerd on the host machine,
Tart and Seatbelt on macOS. There is no way to point yoloAI at a remote machine — a
dedicated server, a cloud VM, a Raspberry Pi cluster — and run agents there instead.

A remote SSH backend solves three problems:

1. **Resource offloading.** Agent workloads (long compiles, large context windows, many
   parallel sandboxes) can be run on a beefy remote server instead of a laptop.

2. **Bare-metal isolation.** Running on a separate physical machine provides the strongest
   possible isolation: even a full kernel exploit on the remote host cannot reach the
   user's local machine. This is stronger than containers (shared kernel) and comparable
   to VMs — but without hypervisor overhead.

3. **Persistent environments.** A remote machine can be provisioned once and reused across
   many sessions, unlike ephemeral containers that rebuild from an image each time.

---

## 1. Scope

### In scope

- Running agents on a remote Linux host over SSH
- Built-in provisioner to install required tools on the remote host
- `:copy` workdir mode (rsync-based, full diff/apply workflow)
- `:rw` workdir mode (sync-on-start, sync-back-on-stop, with clear semantics)
- Read-only aux directory sync
- Interactive attach via remote tmux session
- Per-sandbox API key injection (secrets never touch remote disk)
- Port forwarding for agent UIs
- Keypair generation and remote user provisioning

### Out of scope

- `:overlay` mode — overlayfs is a kernel capability yoloAI cannot guarantee on an
  arbitrary remote host.
- Network isolation — iptables manipulation requires root on the remote.
- Windows remote hosts — path conventions and tool availability differ enough to
  warrant a separate effort.
- Multiple SSH hosts per sandbox — one remote host per sandbox instance.

---

## 2. Runtime Interface Changes

The SSH backend reveals that several `Runtime` interface methods have
container-centric names that make no sense for non-container backends. Since backwards
compatibility is not a constraint, these are renamed as part of this work.

### `EnsureImage()` → `Setup()`

For Docker, "ensure the image is built." For Tart, "ensure the base VM exists." For
SSH, "ensure the remote host is provisioned." The word "image" is meaningless outside
containers.

```go
Setup(ctx context.Context, sourceDir string, output io.Writer, logger *slog.Logger, force bool) error
```

### `ImageExists()` → `IsReady()`

Similarly, "does the image exist" maps to "is the remote host provisioned" for SSH.
`IsReady()` is accurate for all backends.

```go
IsReady(ctx context.Context, imageRef string) (bool, error)
```

### `PreferredTmuxSocket()` → `TmuxSocket(name string) string`

The current signature returns a fixed string — suitable for Docker (one fixed socket
shared across all containers) but not for backends that need per-sandbox sockets.
The SSH backend uses `/tmp/yoloai-tmux-<name>.sock`, one socket per sandbox on the
remote host. Seatbelt also uses per-sandbox sockets.

Changing the signature to accept the instance name is the correct design. Docker and
Podman ignore the `name` argument and return their fixed socket path. SSH and Seatbelt
use it.

```go
TmuxSocket(name string) string
```

### `ShouldSeedHomeConfig()` → `NeedsNpmAgentInstall() bool`

The current name is opaque. The actual question being asked is: "does this backend
run the agent from an npm-installed binary (as in Docker images), or from a natively
installed binary (as in Seatbelt and SSH)?" The npm-installed copy has its install
method recorded as `"npm-global"` in `.claude.json`; the native copy says `"native"`.
Renaming makes the intent clear.

```go
NeedsNpmAgentInstall() bool
```

Returns `true` for Docker/Podman/containerd, `false` for Tart, Seatbelt, and SSH.

### `InstanceConfig` — no changes

`InstanceConfig` contains several container-specific fields (`ImageRef`,
`ContainerRuntime`, `Snapshotter`, `UseInit`, `UsernsMode`, `CapAdd`, `Devices`) that
are irrelevant to SSH. The SSH backend ignores them.

The alternative — splitting `InstanceConfig` into a common base and per-backend
extensions — is appealing in principle but is not pursued here. The problem is that
some "container-specific" fields are actually per-invocation decisions driven by CLI
flags (e.g. `Resources`, `CapAdd` based on overlay mode). A proper split would require
rethinking how these values flow from CLI → sandbox logic → backend, which is a larger
refactor than the SSH backend warrants. Adding SSH-specific params to `InstanceConfig`
would make the problem worse, so SSH-specific config (`host`, `key`, `port`) is passed
to `ssh.New()` at construction time, not through `InstanceConfig`.

---

## 3. Connection Model

### SSH ControlMaster multiplexing

Every `Exec()` call over SSH normally pays a full TCP connection and authentication
round-trip (~50–200 ms). For a backend that makes many exec calls (status checks,
git operations, log tailing), this is prohibitive.

SSH ControlMaster solves this: the first connection establishes a master socket; all
subsequent connections reuse it with negligible overhead (< 5 ms).

yoloAI manages one ControlMaster socket per sandbox, stored under yoloAI's own state
directory:

```
ControlPath:    ~/.yoloai/ssh/control/<sandbox-name>.sock
ControlMaster:  auto
ControlPersist: 600s
```

`ControlMaster: auto` means: become master if no socket exists, otherwise attach to
the existing master. `ControlPersist: 600s` keeps the master alive for 10 minutes
after the last client disconnects, covering brief gaps between commands without
incurring reconnect overhead.

The master socket is explicitly established on `Start()` and killed on `Stop()`:

```bash
# Establish
ssh -o ControlMaster=auto -o ControlPath=~/.yoloai/ssh/control/<name>.sock \
    -o ControlPersist=600s -fN user@host

# Kill
ssh -O exit -o ControlPath=~/.yoloai/ssh/control/<name>.sock _
```

### Standard SSH flags for all connections

```
-o StrictHostKeyChecking=accept-new
-o BatchMode=yes
-o ConnectTimeout=10
-o ControlMaster=auto
-o ControlPath=~/.yoloai/ssh/control/<name>.sock
-o ControlPersist=600s
[-i ~/.yoloai/ssh/keys/<name>]
[-p <port>]
```

`StrictHostKeyChecking=accept-new` implements TOFU (trust on first use): adds new host
keys automatically but rejects changed keys. Avoids interactive prompts on first
connect while still catching MITM attempts after initial setup. `BatchMode=yes`
disables password prompts so failures are immediate.

---

## 4. Configuration

### Specifying the remote host

SSH backend requires a target host. Three layers, highest priority wins:

1. **CLI flag**: `--ssh-host user@192.168.1.100`
2. **Profile config** (`~/.yoloai/profiles/<profile>/config.yaml`): `ssh_host: user@192.168.1.100`
3. **Sandbox record**: at create time the resolved values are written into `environment.json`
   so all subsequent lifecycle operations (start, stop, diff, apply) use the correct
   host without re-specifying flags.

Profile config fields:

```yaml
ssh_host: user@192.168.1.100   # required for SSH backend; user@ prefix is optional
ssh_key:  ~/.yoloai/ssh/keys/myserver   # default: SSH agent or ~/.ssh/id_ed25519
ssh_port: 22                            # default: 22
```

Host aliases defined in `~/.ssh/config` are respected, since yoloAI shells out to
the system `ssh` binary.

### Backend selection

```
yoloai new --backend ssh --ssh-host user@192.168.1.100 /path/to/project
```

Or with a profile that has `ssh_host` pre-configured:

```
yoloai new --backend ssh /path/to/project
```

---

## 5. Provisioning

### Approach: built-in SSH provisioner

Running an agent on a remote host requires `tmux`, `git`, `rsync`, and the agent CLI
(e.g. `claude`, `gemini`, `codex`). These must be installed before yoloAI can use
the host.

Ansible is the natural infrastructure tool for this, but requiring Ansible adds Python
and pip to the user's local machine — significant friction, especially on macOS. A
built-in provisioner that shells out idempotent commands over SSH covers the common
case with no external dependencies. Users who already have Ansible infrastructure can
export a playbook and run it themselves; the built-in path is the default.

### `yoloai provision` command

```
yoloai provision --backend ssh [user@]host [flags]

Flags:
  --key string         path to SSH private key (default: SSH agent)
  --port int           SSH port (default: 22)
  --agents strings     agent CLIs to install (default: all known agents)
  --user string        create a dedicated sandboxing user with this name
  --generate-key       generate a dedicated ed25519 keypair for this host
  --export-playbook    write Ansible inventory + playbook instead of executing
```

Provisioning steps (each is idempotent — skipped if already satisfied):

1. **Verify SSH connectivity** — fast timeout, clear error message on failure
2. **Detect OS** — `uname -s`, `/etc/os-release`
3. **Install system packages** — via the host's package manager:
   - Debian/Ubuntu: `apt-get install -y tmux git rsync curl`
   - RHEL/Fedora/Rocky: `dnf install -y tmux git rsync curl`
   - Arch: `pacman -S --noconfirm tmux git rsync curl`
   - macOS: `brew install tmux git rsync`
4. **Install Node.js** — via `nvm` if not already present (needed for npm-based agents)
5. **Install agent CLIs** — `npm install -g @anthropic-ai/claude-code` etc.
6. **Create sandbox user** (if `--user` specified) — `useradd -m -s /bin/bash <user>`,
   no sudo access, install the generated/provided public key to `~<user>/.ssh/authorized_keys`
7. **Create directory structure** — `~/.yoloai/`, `~/.yoloai/sandboxes/`, `~/.yoloai/ssh/`
8. **Write sentinel** — `~/.yoloai/provisioned.json` with timestamp, agent versions,
   and provisioner version

Progress is streamed to stdout. The sentinel records the provisioner version so a
future `yoloai provision` call can detect and re-run stale provisioning automatically.

### Keypair generation (`--generate-key`)

```
yoloai provision --backend ssh user@host --generate-key
```

Generates `~/.yoloai/ssh/keys/<hostname>` (ed25519) and installs the public key on
the remote. The key path is saved to profile config so subsequent commands use it
automatically. Using a dedicated keypair per host means compromising one host does not
expose the user's primary identity key.

### Integration with `Setup()`

`Setup()` (the renamed `EnsureImage()`) in the SSH backend:

1. Checks SSH connectivity
2. Reads `~/.yoloai/provisioned.json` on the remote
3. If missing or stale (provisioner version mismatch): runs provisioning automatically
   when `force` is true, otherwise prints the `yoloai provision` hint and errors

`IsReady()` (renamed `ImageExists()`) returns `true` if `~/.yoloai/provisioned.json`
exists on the remote and its provisioner version matches the current binary.

### Ansible export

```
yoloai provision --backend ssh user@host --export-playbook ./ansible/
```

Writes `inventory.ini` and `playbook.yml` to the given directory without executing
them. Users can inspect, modify, and run them with their existing Ansible tooling.
The playbook mirrors the built-in provisioner steps exactly.

---

## 6. Remote Directory Layout

```
~/ (SSH user's home on remote)
├── .yoloai/
│   ├── provisioned.json              ← provisioner sentinel (version, agents, date)
│   └── sandboxes/
│       └── <name>/
│           ├── environment.json      ← sandbox metadata (mirrors host copy)
│           ├── logs/
│           │   └── agent.log
│           ├── work/
│           │   └── <encoded-path>/  ← synced copy of :copy/:rw workdir
│           ├── aux/
│           │   └── <encoded-path>/  ← synced read-only aux dirs
│           ├── files/               ← file exchange dir (/yoloai/files/)
│           ├── cache/               ← cache dir (/yoloai/cache/)
│           └── backend/
│               └── instance.json   ← host, port, key path, tmux socket path
└── .claude/                         ← agent state (synced from local on start)
```

The `/yoloai/` path convention used inside containers maps to
`~/.yoloai/sandboxes/<name>/` on the remote host. The agent receives environment
variables pointing at the correct paths for each special directory.

---

## 7. Workdir Path Mapping

In container backends, workdirs are bind-mounted at their original host path inside
the container. The agent sees `/home/karl/Projects/myapp` — the same path as on the
host — and any hardcoded paths in the project work without modification.

The SSH backend replicates this with **symlinks on the remote**. On `Create()`:

```bash
ssh host "mkdir -p $(dirname /home/karl/Projects/myapp) && \
  ln -sfn ~/.yoloai/sandboxes/<name>/work/<encoded>/ /home/karl/Projects/myapp"
```

The symlink is removed on `Remove()`. `ResolveCopyMount()` returns the original host
path unchanged, because the symlink makes it valid on the remote.

**Collision handling:** if a symlink already exists at the target path and points to a
different sandbox, `Create()` returns an error rather than silently overwriting it.
The user must destroy the conflicting sandbox first. This makes the constraint explicit.

The same approach is used for read-only aux dirs.

---

## 8. File Sync

### Tool: rsync

rsync is fast, incremental, handles deletions and permissions, and is available on all
relevant platforms. yoloAI shells out to the local `rsync` binary and routes it
through the established ControlMaster socket to avoid reconnect overhead:

```bash
rsync -az --delete \
  --rsh="ssh -o ControlPath=~/.yoloai/ssh/control/<name>.sock -o BatchMode=yes" \
  /local/path/ \
  user@host:~/.yoloai/sandboxes/<name>/work/<encoded>/
```

### Workdir mode support

| Mode | Support | Semantics |
|------|---------|-----------|
| `:copy` | Yes | rsync to remote at create; git baseline; diff/apply workflow |
| read-only (default) | Yes | rsync once at create; agent can read, not write |
| `:rw` | Yes | rsync to remote at start; rsync back to local at stop |
| `:overlay` | No | Error at create |

### `:rw` semantics

The `:rw` mode over SSH is explicitly **not live**: changes made on the remote are
not reflected on the local host until the sandbox is stopped (or until the user
explicitly runs `yoloai sync <name>`). This is stated in the help text and error
messages. The mode is still useful for workflows where the user wants the agent to
have write access but is comfortable reviewing after the session.

Sync-on-stop flow:

1. `Stop()` syncs the remote workdir back to local before killing the tmux session:
   ```bash
   rsync -az --delete --rsh="ssh ..." \
     user@host:~/.yoloai/sandboxes/<name>/work/<encoded>/ \
     /local/path/
   ```
2. The local copy now reflects whatever the agent did.
3. The user reviews changes with standard tools (git diff, etc.).

This is honest: `:rw` provides write access with deferred sync, not a live bind mount.
The documentation says so plainly. No diff/apply workflow for `:rw` — the user gets
the raw changes directly in their workdir.

### Sync event table

| Event | Direction | What |
|-------|-----------|------|
| `Create()` | local → remote | `:copy`/`:rw` workdirs, read-only aux dirs |
| `Start()` | local → remote | Agent seed files (credentials may have rotated) |
| `Stop()` (`:rw` only) | remote → local | `:rw` workdirs |
| `diff` | remote → local | Patch stream via git diff |
| `apply` | local → remote | Post-apply rsync to resync remote copy |
| `reset` | local → remote | Full rsync from original baseline state |
| `yoloai sync` | remote → local | Manual sync for running `:rw` sandboxes |

---

## 9. Secrets Handling

API keys must reach the agent on the remote without touching remote disk at any point.

### Approach: stdin piping to the remote shell

SSH can pipe stdin to a remote command. The entire startup script — including the
secret — is piped over the encrypted SSH channel to a remote `bash -s` invocation.
The key is processed in-memory by the remote shell and passed to tmux as an
environment variable; it is never written to any file on the remote host.

```go
script := fmt.Sprintf(`
tmux -S %s new-session -d -s main \
  -e ANTHROPIC_API_KEY='%s' \
  -c '%s' \
  -- %s
`, tmuxSocket, apiKey, workDir, agentCmd)

cmd := exec.Command("ssh",
    "-o", "ControlPath="+controlPath, ...,
    userAtHost, "bash", "-s",
)
cmd.Stdin = strings.NewReader(script)
```

The script appears on the SSH channel as encrypted data. The remote shell holds the
key in memory only for the fraction of a second needed to pass it to tmux's environment.
Once tmux launches the agent, the key lives in the tmux server's memory as an
environment variable — standard for any process launched with env vars.

This is strictly cleaner than the temp-file approach (which requires a disk write +
delete window, even with mode 700). The stdin approach has no disk exposure at all.

### Key visibility on the local side

The API key is read from the local keyring or key file into Go memory, written into
the script string, and sent over SSH. It never appears in command-line arguments
(which would expose it in `ps aux` on the local host). Go string memory is not
swapped to disk under normal operation, but the OS makes no such guarantee.

This is the best achievable level of protection for a key that must be delivered to
a remote process. For extremely sensitive keys, users should pre-install them on the
remote host (out-of-band) and configure the agent to read from `~/.profile` or a
secrets manager — yoloAI will then inject nothing.

---

## 10. Agent State Sync

### Remote is authoritative during a session

For container backends, the agent's state directory (e.g. `~/.claude/`) is bind-mounted
from the host into the container. Changes the agent makes are immediately visible on
the host. When the container stops, no sync is needed.

For SSH, the state directory is rsynced to the remote at `Start()`, then the agent
modifies it on the remote for the duration of the session. On `Stop()`, the remote
state is synced back to the local host:

```
Start():  local ~/.claude/ → remote ~/.claude/     (local overwrites remote)
Stop():   remote ~/.claude/ → local ~/.claude/     (remote overwrites local)
```

This means **the remote is authoritative for state changes during a session**. When
the user starts a new session, they always get the latest state from the previous
session. Credentials sync in both directions but the most recent version wins at each
start, which means if the user updates their API key locally between sessions, the new
key reaches the remote on next `Start()`.

One-directional sync (local → remote only) was considered and rejected: it would
discard any learned preferences, conversation history, or cached state the agent
accumulated during the session — exactly the persistent environment advantage the SSH
backend is supposed to provide.

---

## 11. Lifecycle Implementation

### `Create(ctx, cfg InstanceConfig)`

1. Verify SSH connectivity (3-second timeout; clear error if unreachable)
2. Check that `IsReady()` returns true; error with provisioning hint if not
3. Create remote sandbox directory structure
4. Create symlinks at original host paths for all workdirs and aux dirs
5. rsync `:copy` and `:rw` workdirs to remote
6. rsync read-only aux dirs to remote
7. Write `backend/instance.json` with: host, user, port, key path, tmux socket path

### `Start(ctx, name)`

1. Load `backend/instance.json`
2. Establish ControlMaster connection (idempotent)
3. rsync agent seed files from local to remote (credentials may have rotated)
4. Build and pipe the startup script over SSH stdin (see §9)
5. Poll `has-session` until the tmux session confirms start (up to 10 s)

### `Stop(ctx, name)`

1. For `:rw` workdirs: rsync remote → local before stopping the agent
2. Rsync agent state remote → local (remote is authoritative)
3. Kill tmux session: `tmux -S <socket> kill-session -t main`
4. Kill ControlMaster

### `Remove(ctx, name)`

1. Kill tmux session (best effort)
2. Remove remote sandbox directory: `rm -rf ~/.yoloai/sandboxes/<name>`
3. Remove symlinks at original host paths
4. Remove remote tmux socket
5. Kill ControlMaster

### `Inspect(ctx, name)`

```bash
ssh -o ControlPath=... host "tmux -S /tmp/yoloai-tmux-<name>.sock has-session -t main 2>/dev/null"
```

Returns `InstanceInfo{Running: true}` if exit code 0. Returns `ErrNotFound` if
`backend/instance.json` does not exist locally.

If the ControlMaster is not connected (e.g. after host reboot), the SSH call
establishes a fresh connection. This is correct: `Inspect` must work at any time,
not only when the sandbox is active.

### `Exec(ctx, name, cmd, user)`

```bash
ssh -o ControlPath=~/.yoloai/ssh/control/<name>.sock user@host "command"
```

Stdout and stderr are captured and returned in `ExecResult`. The `user` param is
ignored: the connection user IS the exec user for SSH. If the caller needs a different
user, that user must be configured as the SSH connection user.

### `InteractiveExec(ctx, name, cmd, user, workDir)`

```bash
ssh -t -o ControlPath=... user@host "cd /workdir && command"
```

Stdin/stdout/stderr are connected to the terminal. `-t` allocates a PTY.

### `Logs(ctx, name, tail)`

```bash
ssh -o ControlPath=... host "tail -n <tail> ~/.yoloai/sandboxes/<name>/logs/agent.log 2>/dev/null"
```

Returns empty string if the log file does not exist yet.

### `DiagHint(instanceName)`

Returns a human-readable hint filled in from the stored instance config:

```
check agent logs: ssh user@host tail -f ~/.yoloai/sandboxes/<name>/logs/agent.log
```

### `TmuxSocket(name string) string`

Returns `"/tmp/yoloai-tmux-<name>.sock"`. Per-sandbox, on the remote host. Written
into `runtime-config.json` at create time so all subsequent `Exec()` calls find the
same tmux server.

### `AttachCommand(tmuxSocket, rows, cols, isolation)`

```go
[]string{
    "ssh", "-t",
    "-o", "ControlMaster=auto",
    "-o", "ControlPath=" + controlPath,
    "-o", "ControlPersist=600s",
    // port forwarding entries from --port flags:
    "-L", "127.0.0.1:<hostPort>:127.0.0.1:<remotePort>",
    userAtHost,
    "tmux", "-S", tmuxSocket, "attach", "-t", "main",
}
```

### `Capabilities()`

```go
BackendCaps{
    NetworkIsolation: false,
    OverlayDirs:      false,
    CapAdd:           false,
}
```

### `NeedsNpmAgentInstall()`

Returns `false`. Agent runs from its natively installed binary on the remote.

### `ResolveCopyMount(sandboxName, hostPath string) string`

Returns `hostPath` unchanged. The symlink on the remote makes the original path valid.

### `Name() string`

Returns `"ssh"`.

---

## 12. Diff/Apply Workflow

### Diff (`:copy` mode)

Git runs on the remote, and the patch is streamed back over SSH to local stdout:

```bash
ssh -o ControlPath=... host \
  "cd ~/.yoloai/sandboxes/<name>/work/<encoded> && git add -A && git diff --binary <baseline>"
```

This is the same pattern as `:overlay` mode, which runs git inside the container via
`runtime.Exec()`.

### Apply

Apply the patch locally, then rsync the result to the remote.

1. `git apply` on the local host copy (existing apply logic, unchanged)
2. Update local baseline SHA
3. rsync local → remote to bring the remote copy in sync
4. Update remote baseline SHA via `Exec()`:
   ```bash
   ssh host "cd <workdir> && git add -A && git commit --allow-empty -m 'baseline after apply'"
   ```

The host copy remains the source of truth. This is consistent with all other backends.

**Apply on a running sandbox:** applying while the agent is actively writing files
risks a mid-apply state on the remote. `apply` on a running SSH sandbox requires
`--force` and prints a warning. The recommendation is to stop the agent before
applying, same as for other backends.

### Reset

Full rsync from the pre-baseline state. Same as the `Create()` file sync step but
targeted at the existing sandbox. Remote git baseline is reset to the original SHA.

---

## 13. Prune

1. List sandbox directories on the remote:
   ```bash
   ssh host "ls ~/.yoloai/sandboxes/ 2>/dev/null"
   ```
2. Compare against `knownInstances`
3. For each orphan: kill its tmux session + `rm -rf` its sandbox directory on the remote

When multiple sandboxes share the same remote host, prune batches the SSH operations
by host to avoid opening multiple ControlMaster connections to the same target.

---

## 14. Security Considerations

### What bare-metal isolation provides

The agent runs on a separate physical or virtual machine. A container escape, kernel
exploit, or full compromise of the remote host cannot directly affect the user's local
machine. This is the strongest isolation yoloAI offers.

What it does not provide: the agent is not isolated on the remote host itself. It runs
as the SSH user and has full access to everything that user can access. The mitigation
is a dedicated low-privilege user with no sudo.

### Recommended setup

```
yoloai provision --backend ssh root@host --user yoloai --generate-key --agents claude
```

This creates a `yoloai` system user with no sudo, generates a dedicated ed25519
keypair at `~/.yoloai/ssh/keys/<hostname>`, and installs the public key. All subsequent
sandbox operations run as this limited user.

### TOFU host key verification

`StrictHostKeyChecking=accept-new` adds new host keys to `~/.ssh/known_hosts` on first
connect and rejects changed keys on subsequent connects. If the host is reprovisioned
and gets a new key, the user must remove the old entry from `known_hosts` before
reconnecting — this is intentional friction that surfaces MITM risks.

For ephemeral cloud VMs that may reuse IPs with new host keys, users can set
`ssh_key_check: no` in profile config, with a prominent warning that MITM attacks
become undetectable.

---

## 15. Implementation Plan

### New files

```
runtime/ssh/
├── ssh.go        ← Runtime interface implementation
├── provision.go  ← Built-in provisioner (OS detection + idempotent package install)
└── resources.go  ← Embedded Ansible playbook template (for --export-playbook)
```

### Changes to existing files

| File | Change |
|------|--------|
| `runtime/runtime.go` | Rename `EnsureImage` → `Setup`, `ImageExists` → `IsReady`, `PreferredTmuxSocket()` → `TmuxSocket(name string)`, `ShouldSeedHomeConfig` → `NeedsNpmAgentInstall` |
| `runtime/docker/`, `runtime/podman/`, `runtime/tart/`, `runtime/seatbelt/`, `runtime/containerd/` | Update each backend to implement the renamed methods |
| `sandbox/create.go` | Replace all call sites for renamed methods; gate `:overlay` on `caps.OverlayDirs`; allow `:rw` for SSH via sync semantics; use `caps.OverlayDirs` rather than backend name strings |
| `sandbox/diff.go`, `sandbox/apply.go` | Add SSH-specific apply path (apply locally + rsync) |
| `internal/cli/helpers.go` | Add `"ssh"` case to `newRuntime()`; add `--ssh-host`, `--ssh-key`, `--ssh-port` flags to `newCmd` and `startCmd` |
| `config/config.go` | Add `SSHHost`, `SSHKey`, `SSHPort` to profile config; add to `IsGlobalKey()` routing |
| `internal/cli/commands.go` | Add `yoloai provision` command |
| `internal/cli/sync.go` | Add `yoloai sync` command (manual remote→local sync for running `:rw` sandboxes) |
| `docs/dev/ARCHITECTURE.md` | Add SSH backend to package map and command→code map |
| `docs/design/commands.md` | Add `yoloai provision` and `yoloai sync` command specs |
| `docs/design/config.md` | Add `ssh_host`, `ssh_key`, `ssh_port`, `ssh_key_check` config fields |
