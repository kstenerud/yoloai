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

## 1. Scope and Constraints

### In scope

- Running agents on a remote host over SSH
- Built-in provisioner to install required tools on the remote host
- `:copy` workdir mode (rsync-based, full diff/apply workflow)
- Read-only aux directory sync
- Interactive attach via remote tmux session
- Per-sandbox API key injection (secrets never persist on remote)
- Port forwarding for agent UIs

### Out of scope (non-goals)

- `:overlay` mode — overlayfs is a kernel capability that yoloAI cannot guarantee on an
  arbitrary remote host. Could be added later as an opt-in for known Linux targets.
- `:rw` mode — live bind-mount semantics are impossible over SSH without FUSE (sshfs),
  which adds fragile dependencies. Not supported; creates an error at sandbox creation.
- Network isolation — iptables manipulation requires root on the remote. Not supported
  initially; could be added as an optional feature for hosts with sudo access.
- Windows remote hosts — SSH on Windows works but path conventions and tool availability
  differ enough that it is a separate effort. Linux remote hosts only.
- Multiple SSH hosts per profile — one host per sandbox. Parallelism across machines is
  achieved by creating sandboxes with different `--ssh-host` values.

---

## 2. Connection Model

### SSH ControlMaster multiplexing

Every `Exec()` call over SSH normally pays the full TCP+handshake cost (~50–200 ms).
For a backend that runs many exec calls (status checks, git operations, log tailing),
this is prohibitive.

SSH ControlMaster solves this: the first connection establishes a master socket; all
subsequent connections reuse it with negligible overhead (< 5 ms).

yoloAI manages one ControlMaster socket per sandbox:

```
ControlPath: ~/.yoloai/ssh-control/<sandbox-name>.sock
ControlMaster: auto
ControlPersist: 600s
```

`ControlMaster: auto` means: become master if none exists, attach to existing master
if one is already running. `ControlPersist: 600s` keeps the master alive for 10 minutes
after the last client disconnects, so short gaps between commands don't incur reconnect
overhead.

The master socket is established on `Start()` and explicitly killed on `Stop()`:

```
ssh -O exit -o ControlPath=~/.yoloai/ssh-control/<name>.sock placeholder
```

### SSH arguments used for all connections

```
-o StrictHostKeyChecking=accept-new
-o BatchMode=yes
-o ConnectTimeout=10
-o ControlMaster=auto
-o ControlPath=~/.yoloai/ssh-control/<name>.sock
-o ControlPersist=600s
[-i <key_file>]
[-p <port>]
```

`StrictHostKeyChecking=accept-new` adds new host keys automatically but rejects changed
keys (TOFU — trust on first use). Avoids interactive prompts on first connect while still
protecting against MITM after initial setup. `BatchMode=yes` disables password prompts
so failures are immediate rather than hanging.

---

## 3. Configuration

### Specifying the remote host

SSH backend requires a target host. Three layers, highest priority wins:

1. **CLI flag**: `--ssh-host user@192.168.1.100`
2. **Profile config** (`~/.yoloai/profiles/<profile>/config.yaml`): `ssh_host: user@192.168.1.100`
3. **At create time** the resolved host is written into `environment.json` so all
   subsequent lifecycle operations (start, stop, diff, apply) use the correct host
   without requiring the flag again.

Additional optional fields in profile config:

```yaml
ssh_host: user@192.168.1.100   # required for SSH backend
ssh_key:  ~/.ssh/yoloai_key    # default: SSH agent / default key
ssh_port: 22                   # default: 22
```

The `ssh_host` field can include the user (`user@host`) or omit it to use the SSH
config file's default for that host. Host aliases in `~/.ssh/config` are respected
since yoloAI shells out to the `ssh` binary.

### Backend selection

```
yoloai new --backend ssh --ssh-host user@192.168.1.100 /path/to/project
```

Or with a profile that has `ssh_host` pre-configured:

```
yoloai new --backend ssh /path/to/project
```

The backend name is `"ssh"` in `newRuntime()` dispatch and stored as `"ssh"` in
`environment.json`.

---

## 4. Provisioning

### The dependency problem

Running an agent on a remote host requires: `tmux`, `git`, `rsync`, and the agent
CLI tool (e.g. `claude`, `gemini`, `codex`). These must be installed before yoloAI
can use the host.

Ansible is the natural tool for remote provisioning, but requiring Ansible as a
dependency adds Python and pip to the user's local machine requirements — a significant
friction point, especially on macOS where Python management is already fraught.

**Recommendation: built-in SSH provisioner, no Ansible dependency.**

yoloAI implements provisioning as a series of idempotent shell commands executed over
SSH. This covers the common case (Debian/Ubuntu, macOS with Homebrew) with no external
deps. For users who prefer Ansible, the provisioner generates a standard
`inventory.ini` + `playbook.yml` that they can inspect and run themselves.

### `yoloai provision` command

```
yoloai provision --backend ssh user@192.168.1.100 [--key ~/.ssh/yoloai_key] [--agents claude,gemini]
```

Steps:

1. Verify SSH connectivity (fail fast with a clear error if unreachable)
2. Detect OS (`uname -s`, `uname -m`, `/etc/os-release`)
3. Install system packages based on detected OS:
   - Debian/Ubuntu: `apt-get install -y tmux git rsync curl`
   - RHEL/Fedora: `dnf install -y tmux git rsync curl`
   - Arch: `pacman -S --noconfirm tmux git rsync curl`
   - macOS (Homebrew): `brew install tmux git rsync`
4. Install Node.js (via `nvm` or system package manager) if not present
5. Install requested agent CLIs via their standard install commands
   (e.g. `npm install -g @anthropic-ai/claude-code`)
6. Create `~/.yoloai/` directory structure on remote
7. Write `~/.yoloai/provisioned` sentinel file with timestamp and agent list

Each step is idempotent: skipped if already satisfied. Progress is streamed to stdout.

### Integration with `EnsureImage()`

`EnsureImage()` in the SSH backend:
1. Checks SSH connectivity
2. Checks for `~/.yoloai/provisioned` sentinel OR verifies tool presence directly
   (`which tmux && which git && which rsync`)
3. If tools are missing: runs provisioning automatically (same as `yoloai provision`)
   if `--force` is set; otherwise errors with:
   ```
   remote host not provisioned — run: yoloai provision --backend ssh user@host
   ```

`ImageExists()` returns `true` if `~/.yoloai/provisioned` exists on the remote.

### Ansible export (optional, advanced)

```
yoloai provision --backend ssh user@host --export-playbook ./ansible/
```

Writes `inventory.ini` and `playbook.yml` to the target directory without executing
them. Users can modify and run these with their existing Ansible infrastructure.
This is a convenience feature, not the primary path.

---

## 5. Remote Directory Layout

The remote machine mirrors the sandbox directory structure used by containers:

```
~/ (SSH user's home on remote)
├── .yoloai/
│   └── sandboxes/
│       └── <name>/
│           ├── environment.json       ← sandbox metadata (read-only reference)
│           ├── logs/
│           │   └── agent.log
│           ├── work/
│           │   └── <encoded-path>/   ← rsynced copy of :copy workdir
│           ├── aux/
│           │   └── <encoded-path>/   ← rsynced read-only aux dirs
│           ├── files/                ← file exchange dir (/yoloai/files/ inside sandbox)
│           ├── cache/                ← cache dir (/yoloai/cache/ inside sandbox)
│           └── backend/
│               └── instance.json    ← SSH connection params + session name
└── .claude/                         ← rsynced agent state (credentials, config)
```

The `/yoloai/` path convention used by containers maps to `~/.yoloai/sandboxes/<name>/`
on the remote host. The agent is given environment variables pointing at the correct
remote paths for each special directory.

---

## 6. Workdir Path Mapping

In container backends, workdirs are bind-mounted at their original host path inside the
container. This means the agent sees `/home/karl/Projects/myapp` — the same path as on
the host — and any hardcoded paths in the project work without modification.

The SSH backend replicates this with **symlinks on the remote**:

On `Create()`:
```bash
ssh host "mkdir -p $(dirname /home/karl/Projects/myapp) && \
  ln -sfn ~/.yoloai/sandboxes/<name>/work/<encoded>/ /home/karl/Projects/myapp"
```

The symlink is cleaned up on `Remove()`.

`ResolveCopyMount()` returns the original host path (unchanged), same as the Docker
backend, because the symlink makes the path work on the remote.

**Collision caveat:** if two SSH sandboxes point at the same workdir on the same remote
host, the second `Create()` will overwrite the symlink. This is the same constraint as
other backends (only one active sandbox per workdir per host). yoloAI already warns
about unapplied work at create time; the SSH backend adds a check for existing symlinks
and errors if one exists and points to a different sandbox.

For read-only aux dirs, the same symlink approach is used. The rsynced copy is placed
at `~/.yoloai/sandboxes/<name>/aux/<encoded>/` and symlinked at the original host path.

---

## 7. File Sync

### Tool: rsync

rsync is the right tool: fast incremental sync, checksum verification, handles
deletions, widely available. yoloAI shells out to the local `rsync` binary.

```bash
rsync -az --delete \
  --rsh="ssh -o ControlPath=~/.yoloai/ssh-control/<name>.sock" \
  /local/path/to/workdir/ \
  user@host:~/.yoloai/sandboxes/<name>/work/<encoded>/
```

`-a` (archive) preserves permissions, timestamps, symlinks. `-z` compresses for
latency-limited connections. `--delete` removes files on remote that were deleted
locally (important for :copy correctness). `--rsh` reuses the ControlMaster socket
to avoid reconnect overhead.

### Sync points

| Event | Direction | What |
|-------|-----------|------|
| `Create()` | local → remote | `:copy` workdirs, read-only aux dirs, agent seed files |
| `Start()` | local → remote | Agent seed files (credentials may have rotated) |
| `diff` | remote → local | Patch stream (git diff output piped over SSH) |
| `apply` | local → remote | Post-apply rsync to bring remote in sync with applied local state |
| `reset` | local → remote | Full rsync from original (pre-baseline) state |
| `Stop()` | — | No sync; remote state is left as-is |

### Workdir mode support

| Mode | Support | Notes |
|------|---------|-------|
| `:copy` | Yes | rsync at create; git baseline; diff/apply workflow |
| read-only (default) | Yes | rsync once at create; read-only on remote |
| `:rw` | **No** | Error at create: "`:rw` is not supported by the SSH backend" |
| `:overlay` | **No** | Error at create: "`:overlay` is not supported by the SSH backend" |

---

## 8. Secrets Handling

API keys must reach the agent process on the remote without persisting on disk longer
than necessary.

### Flow

1. `Start()` writes API keys to a temp directory on the **remote** host:
   ```bash
   ssh host "mkdir -m 700 /tmp/yoloai-secrets-<name>"
   scp -o ControlPath=... key-file user@host:/tmp/yoloai-secrets-<name>/key
   ```

2. The tmux session is started with the API key injected as an environment variable:
   ```bash
   ssh host "tmux new-session -d -s <name> \
     -e ANTHROPIC_API_KEY=$(cat /tmp/yoloai-secrets-<name>/key) \
     -- <agent-command>"
   ```

3. Immediately after the session starts, delete the temp file:
   ```bash
   ssh host "rm -rf /tmp/yoloai-secrets-<name>"
   ```

The key exists on disk for < 1 second. tmux inherits it as an environment variable
in the session, which lives only in memory (not on disk).

This matches how the Docker backend handles secrets (temp bind-mount, deleted after
container start).

---

## 9. Lifecycle Implementation

### `Create(ctx, cfg InstanceConfig)`

1. Parse SSH connection params from `cfg` (stored in a new `SSHConfig` field or
   via the instance name lookup in `backend/instance.json`)
2. Verify SSH connectivity (short timeout, clear error)
3. Create remote directory structure
4. Create symlinks for workdir and aux dir paths
5. rsync `:copy` workdirs to remote
6. rsync read-only aux dirs to remote
7. rsync agent seed files (e.g. `~/.claude/`) to remote `~/.claude/`
8. Write `backend/instance.json` with: host, user, port, key path, session name

Note: `InstanceConfig` currently has Docker/container-specific fields
(`ImageRef`, `ContainerRuntime`, `Snapshotter`, `UseInit`, `UsernsMode`, `CapAdd`,
`Devices`). The SSH backend ignores these and reads SSH-specific config from the
`instance.json` it writes at create time.

### `Start(ctx, name)`

1. Load `backend/instance.json`
2. Establish ControlMaster connection
3. rsync agent seed files (credentials may have rotated since create)
4. Copy API keys to remote temp dir
5. Start tmux session on remote:
   ```bash
   ssh host "tmux -S /tmp/yoloai-tmux-<name>.sock \
     new-session -d -s main \
     -e KEY=val \
     -c /path/to/workdir \
     -- <agent-command>"
   ```
6. Delete remote temp secrets
7. Wait for tmux session to confirm start (poll `has-session` up to 10s)

### `Stop(ctx, name)`

1. Send `tmux kill-session` on remote:
   ```bash
   ssh host "tmux -S /tmp/yoloai-tmux-<name>.sock kill-session -t main"
   ```
2. Kill ControlMaster socket:
   ```bash
   ssh -O exit -o ControlPath=~/.yoloai/ssh-control/<name>.sock placeholder
   ```

### `Remove(ctx, name)`

1. Kill tmux session (best effort, may already be gone)
2. Remove remote sandbox directory: `ssh host "rm -rf ~/.yoloai/sandboxes/<name>"`
3. Remove symlinks at original host paths
4. Remove remote tmux socket: `ssh host "rm -f /tmp/yoloai-tmux-<name>.sock"`
5. Kill ControlMaster

### `Inspect(ctx, name)`

```bash
ssh host "tmux -S /tmp/yoloai-tmux-<name>.sock has-session -t main 2>/dev/null"
```

Returns `InstanceInfo{Running: true}` if exit code is 0.

If the ControlMaster is not connected (e.g. after host reboot), the SSH command will
establish a new connection. This is intentional — `Inspect` must work even after
network interruption.

Returns `ErrNotFound` if `backend/instance.json` does not exist.

### `Exec(ctx, name, cmd, user)`

```bash
ssh -o ControlPath=... host "command args..."
```

Stdout and stderr are captured and returned in `ExecResult`. The `user` parameter
is passed as `-l user` to SSH (or ignored if same as the connection user, since SSH
doesn't support `su` implicitly — the connection user IS the exec user for SSH backend).

### `InteractiveExec(ctx, name, cmd, user, workDir)`

```bash
ssh -t -o ControlPath=... host "cd /workdir && command args..."
```

Stdin/stdout/stderr connected to the terminal. `-t` allocates a PTY.

### `Logs(ctx, name, tail)`

```bash
ssh host "tail -n <tail> ~/.yoloai/sandboxes/<name>/logs/agent.log 2>/dev/null"
```

Returns empty string if log file doesn't exist (agent may not have written yet).

### `DiagHint(instanceName)`

```
"check agent logs on the remote host: ssh <user>@<host> tail -f ~/.yoloai/sandboxes/<name>/logs/agent.log"
```

The actual host/user are filled in from the stored instance config.

### `Prune(ctx, knownInstances, dryRun, output)`

1. List `~/.yoloai/sandboxes/` on remote:
   ```bash
   ssh host "ls ~/.yoloai/sandboxes/ 2>/dev/null"
   ```
2. Compare to `knownInstances`; anything in remote but not in known list is orphaned
3. For each orphan: kill tmux session + remove directory (or report if `dryRun`)

### `AttachCommand(tmuxSocket, rows, cols, isolation)`

Returns the SSH command to attach interactively:

```go
[]string{
    "ssh", "-t",
    "-o", "ControlPath=" + controlPath,
    "-o", "ControlMaster=auto",
    "-o", "ControlPersist=600s",
    // optional: port forwarding for agent UI
    userAtHost,
    "tmux", "-S", "/tmp/yoloai-tmux-<name>.sock",
    "attach", "-t", "main",
}
```

Port forwarding (if configured via `--port`):
```
-L 127.0.0.1:<host-port>:127.0.0.1:<remote-port>
```

### `PreferredTmuxSocket()`

Returns `"/tmp/yoloai-tmux-<name>.sock"` — a per-sandbox socket, same pattern as the
Seatbelt backend. Stored in `runtime-config.json` at create time so all exec'd
processes on the remote find the same tmux server.

Note: the socket path cannot be known at `New()` time (before any sandbox exists), so
this method needs a design adjustment. Current backends return a fixed socket or empty
string; the SSH backend needs a per-sandbox socket. **See Open Questions §1.**

### `ShouldSeedHomeConfig()`

Returns `false`. The agent runs natively on the remote host from its installed binary,
not from an npm-installed copy inside a Docker image. No `.claude.json` install method
patching needed.

### `Name()`

Returns `"ssh"`.

### `Capabilities()`

```go
BackendCaps{
    NetworkIsolation: false,
    OverlayDirs:      false,
    CapAdd:           false,
}
```

---

## 10. Diff/Apply Workflow

### Diff (`:copy` mode)

Git runs on the remote. The patch is streamed back to local stdout:

```bash
ssh host "cd ~/.yoloai/sandboxes/<name>/work/<encoded> && \
  git add -A && git diff --binary <baseline>"
```

This is analogous to how the `:overlay` mode runs git inside the container via
`runtime.Exec()`. The SSH backend takes the same approach.

### Apply

**Direction: apply patch locally on host, then rsync to remote.**

Rationale: the local host copy is the source of truth for `:copy` mode. After apply,
the host copy is updated; we then rsync it to remote to bring the two in sync. This
is consistent with how apply works for other backends — the host copy is always updated.

Flow:
1. Run `git apply` locally on the host copy (existing apply logic, unchanged)
2. Update local baseline SHA
3. rsync local copy → remote:
   ```bash
   rsync -az --delete --rsh="ssh -o ControlPath=..." \
     ~/.yoloai/sandboxes/<name>/work/<encoded>/ \
     user@host:~/.yoloai/sandboxes/<name>/work/<encoded>/
   ```
4. Update remote baseline SHA via `Exec()`:
   ```bash
   ssh host "cd ~/.yoloai/sandboxes/<name>/work/<encoded> && git add -A && git commit --allow-empty -m 'applied'"
   ```

This two-step approach has a brief window where local and remote are out of sync.
For the SSH backend this is acceptable: the agent is typically stopped during apply
(the diff/apply workflow is a review step). If the agent is running and actively
modifying files, the user should stop it before applying.

### Reset

Full rsync from the original baseline state. Same as `Create()` file sync step,
but targeted at the existing sandbox directory. Remote git baseline is reset to
the original SHA via `Exec()`.

---

## 11. Capabilities and Limitations

| Feature | SSH Backend | Notes |
|---------|-------------|-------|
| `:copy` mode | Yes | rsync-based |
| Read-only mounts | Yes | rsync once at create |
| `:rw` mode | No | Error at create |
| `:overlay` mode | No | Error at create |
| Network isolation | No | No iptables control |
| Port forwarding | Yes | SSH -L tunneling |
| Resource limits (CPU/memory) | No | No cgroup control |
| Multiple sandboxes | Yes | Each gets its own remote sandbox dir |
| Interactive attach | Yes | SSH -t + tmux |
| Non-interactive exec | Yes | SSH via ControlMaster |
| Logs | Yes | SSH + tail log file |
| Prune | Yes | ls remote sandboxes dir |
| Agent state persistence | Yes | Remote `~/.claude/` etc. persists between sessions |

### Agent persistence across sessions

A significant advantage over container backends: agent state (Claude's memory, Gemini's
session history, etc.) persists on the remote host between `stop`/`start` cycles
without needing to rsync it back on every stop. The remote `~/.claude/` directory
accumulates state naturally.

The tradeoff: if the user modifies their local `~/.claude/` between sessions, the
SSH backend will rsync it on `Start()`, overwriting any remote-only changes. This is
the correct behavior (local is authoritative for credentials), but it means remote-only
state modifications are lost on the next start.

---

## 12. Security Considerations

### What "bare-metal isolation" means

The SSH backend provides **network-level isolation**: the agent runs on a separate
machine. A container escape, kernel exploit, or full host compromise on the remote
cannot directly affect the user's local machine. This is the strongest isolation
available without specialized hardware.

However:

- **The remote host itself is not isolated.** The agent runs as the SSH user on the
  remote. It can read/write any file that user owns, install software system-wide
  (if sudo is available), use the full network, etc.
- **Recommendation:** Run the SSH user as a dedicated low-privilege account with no
  sudo access. Provision this user with `useradd -m -s /bin/bash yoloai` and do not
  add it to sudoers. The provisioner should support a `--user` flag to create this
  account and `--ssh-key` to install an authorized_key.

### SSH key management

- **Recommendation:** Use a dedicated SSH keypair for yoloAI sandboxes, not the user's
  primary SSH key. `yoloai provision` can optionally generate a keypair:
  ```
  yoloai provision --backend ssh user@host --generate-key
  ```
  This writes `~/.yoloai/ssh/id_ed25519` and installs the public key on the remote.

- The key path is stored in `instance.json`. If the user rotates the key, they must
  re-provision the sandbox.

### API keys on the remote

API keys exist in memory (tmux environment) only. They are never written to disk
except during the < 1 second window between `scp` and `rm -rf`. The temp dir is
mode 700. For very sensitive deployments, `ssh-agent` forwarding or remote environment
variables in `~/.profile` (pre-provisioned) are alternatives.

### TOFU host key verification

`StrictHostKeyChecking=accept-new` implements trust-on-first-use. The remote host's
key is written to `~/.ssh/known_hosts` on first connect and verified on every
subsequent connect. If the key changes (e.g. host was reprovisioned), the connection
fails with a clear error. This is the correct security posture for bare-metal hosts.

For hosts with dynamically assigned IPs (cloud VMs that may get a new IP on reboot),
the key check may fail spuriously. Users should use `--ssh-key-check=no` as an
override, with a corresponding warning about the MITM risk.

---

## 13. Implementation Plan

### New files

```
runtime/ssh/
├── ssh.go        ← Runtime interface implementation
├── provision.go  ← Built-in provisioner (OS detection + package install)
└── resources.go  ← Embedded Ansible playbook template (for --export-playbook)
```

### Changes to existing files

- `internal/cli/helpers.go` — add `"ssh"` case to `newRuntime()` and `knownBackends`
- `internal/cli/helpers.go` — add `--ssh-host`, `--ssh-key`, `--ssh-port` flags
  (or a single `--ssh-host user@host:port` convention)
- `config/config.go` — add `SSHHost`, `SSHKey`, `SSHPort` fields to profile config
  and `IsGlobalKey()` routing
- `sandbox/create.go` — gate `:rw` and `:overlay` on `caps.OverlayDirs` /
  `isSSHBackend()` with a clear error message
- `internal/cli/commands.go` — add `yoloai provision` command
- `docs/dev/ARCHITECTURE.md` — add SSH backend to package map and command→code map
- `docs/design/commands.md` — add `yoloai provision` command spec
- `docs/design/config.md` — add `ssh_host`, `ssh_key`, `ssh_port` config fields

### `runtime.Runtime` interface changes

`PreferredTmuxSocket()` currently returns a fixed string known at backend construction
time. The SSH backend needs a per-sandbox socket path. Two options:

**Option A:** Add a `SandboxTmuxSocket(name string) string` method alongside the
existing `PreferredTmuxSocket()`. SSH backend implements both; others ignore the new
one. Slightly redundant.

**Option B:** Change `PreferredTmuxSocket()` to `TmuxSocket(instanceName string) string`
across all backends. Docker returns its fixed `/tmp/yoloai-tmux.sock` regardless of
`instanceName`. SSH returns `/tmp/yoloai-tmux-<name>.sock`. Cleaner.

**Recommendation: Option B.** Rename `PreferredTmuxSocket()` to `TmuxSocket(name string)`.
It's a small breaking change to the interface but results in a cleaner design.

### `InstanceConfig` for SSH backend

The SSH backend does not use `ImageRef`, `ContainerRuntime`, `Snapshotter`, `UseInit`,
`UsernsMode`, `CapAdd`, or `Devices`. It needs: SSH host, SSH key path, SSH port.

Rather than adding SSH-specific fields to the shared `InstanceConfig`, the SSH backend
reads its connection params from the sandbox's `backend/instance.json` (written at
create time from CLI flags / profile config). The `InstanceConfig` it receives at
`Create()` carries the standard fields (name, mounts, working dir, env); SSH-specific
params are passed via a constructor argument to `ssh.New()` or embedded in the backend
struct at construction time.

---

## 14. Open Questions

### 1. `PreferredTmuxSocket()` interface change

Covered in §13. Recommendation is Option B (rename + add name param). Needs a decision
before implementation since it touches all backends.

### 2. rsync availability

rsync is available on essentially all Linux servers and macOS. It is not installed by
default on some minimal Docker-based cloud VM images (e.g. Alpine-based). The provisioner
should install it as part of step 3. The provisioner itself uses only `ssh` + `scp` for
its own bootstrap, so no chicken-and-egg problem.

### 3. Multiple hosts per profile (future)

The current design: one SSH host per profile. For parallel agent workflows across multiple
machines, users create sandboxes with `--ssh-host` overriding the profile default.

A future `ssh_hosts` list in profile config could enable a pool of machines for
`yoloai batch`, where sandboxes are distributed across available hosts round-robin or
by load. Not in scope now — design when batch is designed.

### 4. Agent state direction of truth

Currently: local `~/.claude/` is authoritative; rsync local → remote on Start().

If the agent creates useful state on the remote (learned preferences, cached context)
that the user wants to preserve locally, they currently have to manually rsync it back.

A future `--sync-agent-state` flag could rsync remote agent state → local on `Stop()`,
making the remote authoritative during a session and syncing back at the end.
This is a non-trivial change to the trust model and is left for a future design pass.

### 5. Partial apply window

Between `git apply` locally and rsync to remote, the remote has stale state. If the
agent is still running during apply (unusual but possible), it may read the old state
mid-apply. Mitigation: warn if apply is attempted on a running sandbox (already done
for some backends). Consider requiring `--force` to apply to a running sandbox.

### 6. SSH backend on Windows (local host)

SSH on Windows (`openssh.exe`) supports ControlMaster as of OpenSSH 8.1 (shipped with
Windows 10 2004+). The ControlPath syntax differs (must use a path without colons on
some versions). Not blocking for initial implementation — Linux and macOS local hosts
are sufficient — but worth noting for future cross-platform support.
