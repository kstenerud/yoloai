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

DIRTY REPO WARNING

  If your workdir has uncommitted git changes, yoloai prompts before
  proceeding so you don't lose work.

NETWORK ISOLATION

  Disable network access entirely:

     yoloai new task . --network-none

  Agents have a default network allowlist. Use --network-none for
  maximum isolation.

NON-ROOT EXECUTION

  Containers run as a non-root user with UID/GID matching your host
  user.

More info: https://github.com/kstenerud/yoloai/blob/main/docs/GUIDE.md#security
