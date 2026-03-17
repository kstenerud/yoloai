SECURITY AND CREDENTIALS

  yoloai is designed to protect your original files and credentials.

COPY PROTECTION

  Workdirs use :copy mode by default — the agent works on an isolated
  copy, never your originals. Use :rw only when you explicitly want
  live access. Dangerous directories ($HOME, /) are refused unless
  you append :force.

CREDENTIAL INJECTION

  API keys are mounted as read-only files at /run/secrets/ inside the
  container, not passed as environment variables. Temp files on the
  host are cleaned up after container start.

  On macOS, yoloai checks the Keychain for Claude Code OAuth
  credentials automatically.

CLAUDE CODE SUBSCRIPTION USERS (Pro/Max/Team)

  If you use a Claude subscription (not an API key), run:

     claude setup-token

  Then export the token:

     export CLAUDE_CODE_OAUTH_TOKEN=<token>

  This generates a long-lived token that works reliably in sandboxes.
  Without it, yoloai falls back to ~/.claude/.credentials.json, which
  contains short-lived OAuth tokens (~30 min) that break when any
  other Claude Code instance refreshes them first.

DIRTY REPO WARNING

  If your workdir has uncommitted git changes, yoloai prompts before
  proceeding so you don't lose work.

NETWORK ISOLATION

  Disable network access entirely:

     yoloai new task . --network-none

  Allow only agent API traffic (blocks everything else):

     yoloai new task . --network-isolated

  Add extra domains to the allowlist:

     yoloai new task . --network-allow api.example.com

  Each agent has a default allowlist (e.g., api.anthropic.com for
  Claude). Use --network-none for maximum isolation.

OCI RUNTIME SECURITY MODES

  Applies to Docker and Podman backends only. Upgrades the OCI runtime
  for stronger isolation beyond standard Linux namespaces:

  standard          Default runc (Linux namespaces + cgroups)
  gvisor            Userspace kernel (gVisor/runsc) — syscall interception,
                    no KVM required.
  kata              Kata Containers with QEMU VM isolation (experimental).
  kata-firecracker  Kata Containers with Firecracker microVM (experimental).

  Set a default:

     yoloai config set security gvisor

  Or per sandbox:

     yoloai new task . --security gvisor

  Security modes are silently ignored on non-container backends (tart,
  seatbelt). Specifying --security explicitly on an incompatible backend
  is an error.

SETUP: GVISOR

  1. Install runsc (the gVisor binary):
        https://gvisor.dev/docs/user_guide/install/

  2. Register it with Docker in /etc/docker/daemon.json:
        {"runtimes": {"runsc": {"path": "/usr/local/bin/runsc"}}}

  3. Restart the Docker daemon:
        sudo systemctl restart docker

  Both steps are required. Installing the binary is not enough —
  Docker must also know about it. yoloai checks both.

SETUP: KATA (EXPERIMENTAL)

  1. Install Kata Containers 3.x:
        https://github.com/kata-containers/kata-containers/releases

  2. Register with Docker in /etc/docker/daemon.json:
        {"runtimes": {
          "kata-qemu": {"path": "/usr/bin/kata-qemu"},
          "kata-fc":   {"path": "/usr/bin/kata-fc"}
        }}

  3. Restart the Docker daemon.

INCOMPATIBILITIES

  gVisor + :overlay directories:
    gVisor's VFS2 kernel does not support overlayfs mounts inside the
    container. Combine --security gvisor only with :copy or :rw
    directories. yoloai detects and rejects this combination.

NON-ROOT EXECUTION

  Containers run as a non-root user with UID/GID matching your host
  user.

More info: https://github.com/kstenerud/yoloai/blob/main/docs/GUIDE.md#security
