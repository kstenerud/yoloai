> **Design documents:** [Overview](README.md) | [Commands](commands.md) | [Config](config.md) | [Setup](setup.md) | [Research](../dev/RESEARCH.md)

## Credential Management

API keys are injected via **file-based credential injection** following OWASP and CIS Docker Benchmark guidance (never pass secrets as environment variables to `docker run`):

1. yoloAI writes the API key(s) to temporary file(s) on the host. The agent definition specifies which env vars to inject (e.g., `ANTHROPIC_API_KEY` for Claude, `CODEX_API_KEY` for Codex).
2. Each key file is bind-mounted read-only into the container at `/run/secrets/<key_name>`.
3. The container entrypoint reads the file(s), exports the appropriate env var(s) (since CLI agents expect credentials as env vars), then launches the agent.
4. The host-side temp file is cleaned up immediately after container start.

**What this protects against:** `docker inspect` does not show the key. `docker commit` does not capture it. `docker logs` does not leak it. No temp file lingers on host disk. Image layers never contain the key.

**Accepted tradeoff:** The agent process has the API key in its environment (unavoidable — CLI agents read credentials from env vars). `/proc/<pid>/environ` exposes it to same-user processes inside the container. This is acceptable because the agent already has full use of the key.

The user sets the appropriate API key in their host shell profile (`ANTHROPIC_API_KEY` for Claude, `CODEX_API_KEY` or `OPENAI_API_KEY` for Codex). yoloAI reads the required key(s) from the host environment at sandbox creation time based on the agent definition.

**Future directions:** Credential proxy (the MITM approach used by Docker Sandboxes) could provide stronger isolation by keeping the API key entirely outside the container. If CLI agents add `ANTHROPIC_API_KEY_FILE` support, the env var export step can be eliminated. macOS Keychain integration (cco's approach) could serve as an alternative credential source. These are deferred to future versions.

**Industry expectations:** yoloAI's file-based injection follows OWASP and CIS guidance and matches what Docker official images (MySQL, Postgres) and most competitors (deva.sh, cco) use. Key gaps vs. enterprise expectations:
- **Credential rotation/expiry:** Not supported. Most users use long-lived API keys. Not a v1 blocker, but enterprise users expect time-scoped credentials.
- **Vault integration:** HashiCorp Vault, AWS Secrets Manager, GCP Secret Manager are expected in enterprise tooling. Deferred.
- **OAuth/SSO:** Claude supports OAuth for Pro/Max/Team plans. v1 supports API key auth only. Docker Sandboxes supports OAuth but reports it as broken (docker/for-mac#7842).
- **Assessment:** File-based injection is the industry standard for developer tools at this level. Credential proxy and vault integration are enterprise features for future versions.

## Security Considerations

- **The agent runs arbitrary code** inside the container: shell commands, file operations, network requests. The container provides isolation, not prevention.
- **All directories are read-only by default.** You explicitly opt in to write access per directory via `:rw` (live) or `:copy` (staged).
- **`:copy` directories** protect your originals. Changes only land when you explicitly `yoloai apply`.
- **`:rw` directories** give the agent direct read/write access. Use only when you've committed your work or don't mind destructive changes. The tool warns if it detects uncommitted git changes.
- **API key exposure:** The agent's API key (e.g., `ANTHROPIC_API_KEY` for Claude, `GEMINI_API_KEY` for Gemini) is injected via file-based credential injection (bind-mounted at `/run/secrets/`, read by entrypoint, host file cleaned up immediately). This hides the key from `docker inspect`, `docker commit`, and `docker logs`. The key is still present in the agent process's environment (unavoidable — CLI agents expect env vars). Use scoped API keys with spending limits where possible. See [RESEARCH.md](../dev/RESEARCH.md) "Credential Management for Docker Containers" for the full analysis of approaches and tradeoffs.
- **SSH keys:** If you mount `~/.ssh` into the container (even read-only), the agent can read private keys. Prefer SSH agent forwarding: add `${SSH_AUTH_SOCK}:${SSH_AUTH_SOCK}:ro` to `mounts` and `SSH_AUTH_SOCK: ${SSH_AUTH_SOCK}` to `env` in config. This passes the socket without exposing key material.
- **Network access** is unrestricted by default (required for agent API calls). The agent can download binaries, connect to external services, or exfiltrate code. Use `--network-isolated` to restrict traffic to the agent's required API domains, `--network-allow <domain>` for finer control, or `--network-none` for full isolation.
- **[PLANNED] Network isolation implementation:** `--network-isolated` uses iptables + ipset inside the container, following the same pattern as Anthropic's own Claude Code devcontainer and Trail of Bits' devcontainer (both verified production implementations):
  1. **iptables + ipset rules:** The entrypoint resolves allowlisted domains to IPs via `dig`, populates an ipset (`hash:net`), and sets default-deny iptables policy. Only traffic to allowlisted IPs is permitted. Requires `CAP_NET_ADMIN` (a separate capability from `CAP_SYS_ADMIN` — both must be granted when using overlay + `--network-isolated`; for `copy_strategy: full`, only `CAP_NET_ADMIN` is added). The entrypoint configures rules while running as root, then drops privileges via `gosu` — the agent never has `CAP_NET_ADMIN`.
  2. **Per-agent allowlist:** Each agent definition includes required domains (e.g., `api.anthropic.com` for Claude). `--network-allow <domain>` adds user-specified domains. The combined allowlist is stored in `meta.json` for container recreation.
  3. **Single container:** No proxy sidecar, no internal Docker network. Simple, debuggable, same approach that Anthropic ships.

  **Multi-backend:** Docker uses iptables+ipset as described. Seatbelt can restrict network access via sandbox profile rules (binary allow/deny per host, or full `--network-none`). Tart VMs would need VM-level network configuration (deferred — Tart network isolation is not yet designed).

  Known limitations: DNS exfiltration remains possible — UDP 53 must be allowed for domain resolution (same limitation as Anthropic's devcontainer and Trail of Bits'). Domain-to-IP resolution happens at container start; CDN IP rotation can make rules stale (restart to refresh). Domain fronting remains theoretically possible on CDNs that haven't disabled it. These limitations are shared by all iptables-based implementations. See [RESEARCH.md](../dev/RESEARCH.md) "Network Isolation Research" for detailed analysis of bypass vectors.

  **[DEFERRED] Proxy sidecar architecture:** A more robust approach using an internal Docker network + proxy sidecar container + DNS control could mitigate DNS exfiltration and handle CDN IP rotation dynamically. This is well-researched (see RESEARCH.md) but adds significant operational complexity (sidecar lifecycle management across all commands, proxy image building, health checks, failure modes). The iptables-only approach covers the primary threat vectors at a fraction of the complexity. The proxy architecture remains documented in RESEARCH.md if stronger isolation is ever needed.
- **Runs as non-root** inside the container (user `yoloai` matching host UID/GID). Claude Code requires this (refuses `--dangerously-skip-permissions` as root); Codex runs non-root by convention.
- **[PLANNED] `CAP_SYS_ADMIN` capability** is granted to the container when using the overlay copy strategy (the default). This is required for overlayfs mounts inside the container. It is a broad capability — it also permits other mount operations and namespace manipulation. The container's namespace isolation limits the blast radius, but this is a tradeoff: overlay gives instant setup and space efficiency at the cost of a wider capability grant. Users concerned about this can set `copy_strategy: full` to avoid the capability entirely.
- **Dangerous directory detection:** The tool refuses to mount `$HOME`, `/`, macOS system directories (`/System`, `/Library`, `/Applications`), or Linux system directories (`/usr`, `/etc`, `/var`, `/boot`, `/bin`, `/sbin`, `/lib`) unless `:force` is appended, preventing accidental exposure of your entire filesystem.
- **[PLANNED] Privilege escalation via recipes:** The `setup` commands and `cap_add`/`devices` config fields enable significant privilege escalation. These are power-user features for advanced setups (e.g., Tailscale, GPU passthrough) but have no guardrails — a misconfigured recipe could undermine container isolation. Document risks clearly when these features are used.

