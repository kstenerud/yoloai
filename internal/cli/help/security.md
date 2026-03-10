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

NON-ROOT EXECUTION

  Containers run as a non-root user with UID/GID matching your host
  user.

More info: https://github.com/kstenerud/yoloai/blob/main/docs/GUIDE.md#security
