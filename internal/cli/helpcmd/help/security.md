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

  Allow only agent API traffic (blocks everything else over IPv4):

     yoloai new task . --network-isolated

  Add extra domains to the allowlist:

     yoloai new task . --network-allow api.example.com

  Each agent has a default allowlist (e.g., api.anthropic.com for
  Claude). Use --network-none for maximum isolation.

  WHERE THE ALLOWLIST IS ENFORCED, AND WHAT IT IS WORTH

  On docker, the rules are installed from a separate helper container and
  the sandbox is denied NET_ADMIN, so an agent inside it cannot remove
  them. This is the strongest case, and it is still not containment for a
  hostile agent: the sandbox grants sudo by design, and root can turn on
  IPv6 (below) and leave through a family these rules never covered.

  On apple, podman and containerd the sandbox installs the rules itself
  and therefore holds NET_ADMIN, so an agent that tries can flush them.
  Treat the allowlist there as a guardrail against accidental or careless
  egress, not as containment.

  On tart and seatbelt there is no network isolation at all --
  --network-isolated is refused rather than silently unenforced.

  Apple Container rejects --network-none because that backend has no
  enforcing adapter. Seatbelt's native no-network policy is a separate
  backend mechanism; the shared Apple platform name does not imply parity.

  On Apple Container, custom --dns values compose with
  --network-isolated. The guest
  allows UDP and TCP port 53 only to those selected resolvers before broad
  allowlist rules. The existing NET_ADMIN caveat still applies: an agent can
  change in-guest rules on Apple Container.

  IPv6 IS NOT FILTERED. The allowlist is enforced with IPv4 iptables
  rules only; no ip6tables rules are installed on any backend. On the
  networks yoloAI creates today the guest gets no globally-routable
  IPv6 address, so there is no v6 egress to restrict — but that is a
  property of those networks, not a guarantee this flag makes. If your
  guest has routable IPv6, the allowlist does not apply to it.

  This is not only a property of your network. The agent has sudo, and
  root can re-enable IPv6 from inside the sandbox, so a hostile agent
  can create the condition rather than having to find it. Denying
  NET_ADMIN stops the rules being flushed; it does not stop this. Use
  --network-none where it matters — it removes the interface rather
  than filtering it, which is why it holds where the allowlist does not.

ISOLATION MODES

  The --isolation flag upgrades the OCI runtime for stronger isolation
  beyond standard Linux namespaces. Applies to container backends only
  (docker, podman, containerd).

  container             Default — runc (Linux namespaces + cgroups).
  container-enhanced    gVisor (runsc) — userspace kernel with syscall
                        interception. No KVM required. Docker/Podman.
  container-privileged  Default runc with --privileged. Use for
                        Docker-in-Docker workloads. Reduces isolation.
  vm                    Kata Containers + QEMU VM. Requires containerd
                        backend. Experimental.
  vm-enhanced           Kata Containers + Firecracker microVM. Requires
                        containerd backend. Experimental.

  Set a default:

     yoloai config set isolation container-enhanced

  Or per sandbox:

     yoloai new task . --isolation container-enhanced

  Isolation modes apply only to container backends. They are not
  available on macOS backends (tart, seatbelt).

SETUP: CONTAINER-ENHANCED (GVISOR)

  1. Install runsc (the gVisor binary):
        https://gvisor.dev/docs/user_guide/install/

  2. Register it with Docker in /etc/docker/daemon.json:
        {"runtimes": {"runsc": {"path": "/usr/local/bin/runsc"}}}

  3. Restart the Docker daemon:
        sudo systemctl restart docker

  Both steps are required. Installing the binary is not enough —
  Docker must also know about it. yoloai checks both.

SETUP: VM / VM-ENHANCED (KATA, EXPERIMENTAL)

  VM isolation modes require the containerd backend, not Docker.

  1. Install Kata Containers 3.x:
        https://github.com/kata-containers/kata-containers/releases

  2. Configure containerd to use the kata-qemu (vm) or kata-fc
     (vm-enhanced) shim. See:
        https://github.com/kata-containers/kata-containers/blob/main/docs/install/container-manager/containerd/containerd-install.md

INCOMPATIBILITIES

  container-enhanced (gVisor) + :overlay directories:
    gVisor's VFS2 kernel does not support overlayfs mounts inside the
    container. Combine --isolation container-enhanced only with :copy
    or :rw directories. yoloai detects and rejects this combination.

NON-ROOT EXECUTION

  Containers run as a non-root user with UID/GID matching your host
  user.

More info: https://github.com/kstenerud/yoloai/blob/main/docs/GUIDE.md#security
