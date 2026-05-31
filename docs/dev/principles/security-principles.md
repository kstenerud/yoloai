ABOUTME: Sandbox security principles for yoloAI. Threat model is bounded
ABOUTME: (agent mistakes + supply-chain, not nation-state); containment over
ABOUTME: prevention; defense-in-depth as opt-in layers; least privilege by
ABOUTME: mode; credentials via file injection; default-deny; agent output
ABOUTME: untrusted; verify isolation claims; document residual risk; power-user
ABOUTME: escapes are explicit. NOT customer-trust UX; this is containment.

# Security principles

Sandbox-containment security for yoloAI. The threat model is the agent that yoloAI runs, not the user that runs yoloAI. The user is the principal; the agent is the untrusted process; the container/VM/sandbox is the containment layer. Specialised application of `general-principles.md` to the security surface.

Established in D22 (`../working-notes.md`). Primary-source backing: `../research/principles/security-principles-research.md`. Concrete implementation: `docs/design/security.md` and `docs/design/network-isolation.md`.

## Framing — what yoloAI protects against, and what it doesn't

yoloAI runs autonomous AI coding agents inside containers / VMs / sandboxes. The threat actors:

1. **The agent itself making mistakes.** Hallucinated commands, wrong file paths, infinite loops, runaway resource usage.
2. **Prompt injection turning the agent against the user.** Documented attack class with measurable success rates (Anthropic + OpenAI + DeepMind, Nov 2025).
3. **Malicious code the agent fetches and runs.** Compromised dependencies, supply-chain attacks, fake `npm install` targets.
4. **Accidental data loss from a runaway operation.** `rm -rf` on the wrong directory; `git push --force` to the wrong remote.

Out of scope:

- **Nation-state APT** targeting individual users.
- **The user as adversary.** The user runs the tool; if they want to point it at their own filesystem destructively, that's their call (with `:force` flags + warnings).
- **Multi-tenant isolation.** yoloAI is single-user-per-machine; no isolation between users on the same host.

A bounded threat model justifies bounded defenses. The defenses below are sized to actual threats, not to imagined ones. (See `general-principles.md §5` on blast radius — the security surface is one of its main applications.)

The compositions:

- `development-principles.md §3` (validate at every layer) and `§4` (parse, don't validate) are the structural enforcement of containment at the code layer.
- `testing-principles.md §3` (error paths first-class) and `§6` (integration tests hit real backends) are what verifies the containment claims actually hold.
- `general-principles.md §6` (safe defaults), `§9` (surface failures honestly), `§10` (cross-platform awareness) all manifest specifically at this surface.

---

## §1. Threat model is bounded — and stated explicitly

**Principle.** The threats yoloAI defends against are enumerated. The threats it does NOT defend against are also enumerated. Defenses are sized to what's in scope; complexity for out-of-scope threats is rejected.

### Pattern

Every security feature traces to one of the four in-scope threats. Anything that doesn't is suspect — either the threat model needs expansion (rare, deliberate, D-entry required) or the feature is over-engineering.

### Worked examples

- **`docs/design/network-isolation.md` §Threat Model** states the boundary explicitly: "Sandbox escape via kernel/runtime exploit. This is gVisor's and Kata's domain. Network isolation cannot help if the agent breaks out of the sandbox entirely." Acknowledging the limit is the principle in action.
- **Anthropic + OpenAI + DeepMind "Attacker Moves Second" (Nov 2025)** sets adaptive-attack success rates for prompt injection at ~1% on the most-trained models. yoloAI's response is not "detect every injection" (impossible per the consensus paper); it's structural containment — the agent runs in a sandbox where its blast radius is bounded regardless.
- **No defense against the user**: dangerous-directory refusal can be overridden with `:force`. The user is allowed to point yoloAI at `~` if they explicitly say so. Most tools that try to defend against the user produce friction without security.
- **Audit-log obsession rejected**: yoloAI does not record everything the agent does in a tamper-evident log. The threat model doesn't include "the user is forensically investigating their own agent run after an incident"; the `log.txt` + bug-report bundle is enough.

### Cost-vs-benefit

Cost of applying: a one-page threat-model statement; periodic review. Damage prevented: feature complexity for imagined threats (audit logging, multi-tenant isolation, signature pinning on every binary) that yoloAI doesn't actually need; security theatre that erodes trust when users notice it's theatre.

### Sources

Bruce Schneier; NIST SP 800-190; Anthropic + OpenAI + DeepMind "Attacker Moves Second" (2025). Full citations: `../research/principles/security-principles-research.md §1`.

Originally established alongside D11.

---

## §2. Containment, not prevention — the sandbox limits blast radius

**Principle.** The agent runs arbitrary code. The container, VM, or sandbox does not prevent the agent from doing dangerous things — it limits what those dangerous things can reach. Defenses are about *bounded blast radius*, not about making the agent's code safe.

### Pattern

Threshold: every "prevent X" idea is reframed as "if the agent does X, what's the worst that happens?" The right question shapes the right defense. A defense that depends on the agent behaving correctly is not a defense; it's a hope.

### Worked examples

- **`:copy` mode** (D4): the agent's edits go to a copy. The agent can write anything; the originals are untouched until `yoloai apply`. Containment of the worst case ("agent destroys the project") to a bounded effect ("agent's copy is dirty; user reviews and discards").
- **Container isolation**: the agent runs as user `yoloai`, not root. It can't `chown` host files. If the agent tries something destructive on what it thinks is the host filesystem, it's actually the container's mount view; the bind-mounts are scoped.
- **Dangerous-directory refusal** (`docs/design/security.md`): refuses to mount `$HOME`, `/`, `/etc`, `/usr`, etc. The agent can't write there because we never mounted there.
- **Network unrestricted by default + `--network-isolated` opt-in**: the threat isn't "agent makes any network call" (it needs API calls to function); the threat is "agent exfiltrates sensitive data over a non-allowlisted route." Network isolation bounds the *destinations*, not the *capability*.
- **Architectural floor** (carried over from foley's framing, adapted): the agent never auto-commits state on the user side. `yoloai apply` requires explicit user action. No agent output flows back to the host filesystem without the user's review step.

### Cost-vs-benefit

Cost of applying: design-time discipline to keep asking "what's the blast radius?" rather than "how do we stop this?" Damage prevented: false confidence from "the agent can't do X" defenses that actually only require the agent to do Y to bypass; the over-engineering of detection layers when the structural defense already bounds the damage.

### Sources

Saltzer & Schroeder (1975); NIST SP 800-190; Anthropic + OpenAI + DeepMind "Attacker Moves Second". Full citations: `../research/principles/security-principles-research.md §2`.

Originally established alongside D4 (mount-mode taxonomy).

---

## §3. Defense-in-depth as opt-in layers

**Principle.** Stronger isolation (gVisor user-space kernel, Kata VMs, Tart macOS VMs, Seatbelt) is available but opt-in. Standard Docker is the default — it's the right balance for most users. Users with stronger threat models opt into stronger layers via explicit flags.

### Pattern

The default reflects what most users want: containers with namespaces + capabilities. Defense-in-depth is layered:

- `--isolation standard` (default): Docker / Podman / containerd with namespaces, capability restrictions, user `yoloai` not root.
- `--isolation container-enhanced`: adds gVisor user-space kernel for syscall filtering.
- `--isolation container-privileged`: enables Docker-in-Docker for power users (gives up some isolation).
- `--isolation vm`: Kata Containers — VM-isolated process.
- `--isolation vm-enhanced`: Kata with hardened guest kernel.

Backends Tart (macOS VM) and Seatbelt (sandbox-exec) are separate axes — chosen as `--backend`, not stacked on `--isolation`.

### Worked examples

- **gVisor `--isolation container-enhanced`** (D17, commit `87956ac`, 2026-03-17). User-space kernel intercepts syscalls. Permission-handling quirks (UID remapping → relaxed bind-mount permissions per `docs/design/security.md`) are documented as residual risks.
- **gVisor on macOS refused** (commit `d078db6`, 2026-03-17). The upstream Claude Code + gVisor `epoll_pwait` hang is a known platform-specific bug; yoloAI refuses upfront with a pointer rather than silently failing. This is `§9 — document residual risk` in action.
- **Tart for macOS VMs** (commits `814d379` and following, 2026-02-26). Full VM isolation; ~3× I/O cost on macOS (`docs/dev/research/sandboxing.md`); chosen by users who want hardware-level isolation.
- **Seatbelt** (D15, default-deny credential access, `docs/design/security.md` §Seatbelt Backend Security). macOS sandbox-exec syscall filtering. Restricted to specific paths (`~/.local/`, `~/.gitconfig`, `~/.config/git/`) and a safe environment subset.
- **Layered isolation rename** (commit `098672c`, 2026-03-18): the values were originally named more cryptically; the rename to `container / container-enhanced / vm / vm-enhanced` makes the layer explicit.

### Cost-vs-benefit

Cost of applying: maintenance of multiple isolation layers (gVisor, Kata, Tart, Seatbelt each have their own quirks). Damage prevented: users who can't get sufficient isolation from the default option have no recourse; the alternative is to harden the default and pay UX cost across all users for the benefit of the few. Layered opt-in is the right answer.

### Sources

gVisor, Tart, Apple sandbox-exec, Kata Containers docs; NIST SP 800-190 §6. Full citations: `../research/principles/security-principles-research.md §3`.

Originally established alongside D17.

---

## §4. Least privilege by mode — pay the capability cost only when needed

**Principle.** Capabilities are granted only when a specific feature requires them. The default mode pays zero capability cost. `:overlay` mode pays `CAP_SYS_ADMIN`. `--network-isolated` pays `CAP_NET_ADMIN`. Users not using these features pay nothing.

### Pattern

Threshold: every capability grant is feature-gated. The container does not get capabilities "just in case" — only when a specific path needs them. Capabilities are dropped after the entrypoint configures the privileged step (e.g., iptables rule install) and before the agent runs.

### Worked examples

- **`:copy` mode**: zero special capabilities. The base case. Most users, most of the time.
- **`:overlay` mode** (`docs/design/security.md`): requires `CAP_SYS_ADMIN` for the overlayfs mount inside the container. Documented as a "broad capability" tradeoff: the container's namespace isolation limits blast radius but `CAP_SYS_ADMIN` permits other mount operations and namespace manipulation. The cost is paid only when overlay is chosen.
- **`--network-isolated`** (D11): requires `CAP_NET_ADMIN` for iptables rule install. Granted only with `--network-isolated`. Dropped via `gosu` before the agent process is launched — the agent never has `CAP_NET_ADMIN`.
- **Run as non-root** (user `yoloai` matching host UID/GID): the agent never runs as root. Claude Code refuses to run as root for `--dangerously-skip-permissions`; this is the same model.
- **`:rw` doesn't grant capabilities**: it grants writable bind-mounts. Different axis, same principle — the capability surface and the mount surface are independent.
- **Power-user escape**: `cap_add` in profile config (D22's predecessor: profile system D12) is documented in §10 below.

### Cost-vs-benefit

Cost of applying: feature-gated capability code paths. Damage prevented: agent code running with broader privileges than it needs; the "we granted CAP_SYS_ADMIN once for X and now it leaks to everything" pattern.

### Sources

Saltzer & Schroeder (1975); CIS Docker Benchmark; OWASP; Linux capabilities(7). Full citations: `../research/principles/security-principles-research.md §4`.

Originally established alongside D4 (mount modes) and D11 (network isolation).

---

## §5. Agent output is untrusted by default

**Principle.** Output from an AI agent — file edits, generated patches, commit messages, JSON written to `/yoloai/files/`, anything else — is data that crossed a trust boundary. Defenses are structural, not detection-based. yoloAI never relies on the model recognising bad output as the boundary control. The "Attacker Moves Second" consensus (Anthropic + OpenAI + DeepMind, Nov 2025) confirms detection-based defenses fail against adaptive attackers.

### Pattern

For every place agent output flows back to the host:

1. **The agent never auto-commits to the host.** The architectural floor: `yoloai apply` is user-initiated. The user reviews the diff before changes land.
2. **Diff review is the user's job**, not the tool's. `yoloai diff` shows what changed; the user decides. The tool doesn't decide based on a "safety score."
3. **Generated patches stay in the sandbox until apply.** `git format-patch <baseline>..HEAD` produces the patch series; `git am` is what lands them — explicitly, in a user-initiated step.
4. **Files in `/yoloai/files/`** flow through `yoloai files get` — also user-initiated.
5. **Parser-level validation** on structured agent output (config edits, future structured-tool-output) — schema, not "the LLM said it was OK." See `development-principles.md §4 — parse, don't validate`.

### Worked examples

- **`yoloai apply` design** (D9, commits `5ca1003`, `29895db`): explicit `format-patch` + `am`. No auto-apply, ever.
- **Diff is git-native**: `yoloai diff` calls `git diff <baseline_sha>`. Standard format the user already understands.
- **`/yoloai/files/`** is a bidirectional exchange directory but movement in either direction is user-initiated via `yoloai files`. Not a sync mechanism that runs on agent action.
- **No agent-driven destructive operation**: there is no `yoloai apply --yolo` that auto-applies. There is no agent hook that commits to the user's main branch. The architectural floor is structural, not configurable.
- **Sandbox naming + file-system scoping**: even if the agent decides to write everywhere it can, "everywhere it can" is bounded by `:copy` mode + mount scope (§2, §4).

### Cost-vs-benefit

Cost of applying: a deliberate `yoloai apply` step every time changes need to land. Damage prevented: an injection that causes the agent to write attacker-supplied content directly to the user's main branch; a hallucinated edit shipping unreviewed; the "the AI did it" failure mode where agency is unclear.

### Sources

Anthropic + OpenAI + DeepMind "Attacker Moves Second"; `development-principles.md`. Full citations: `../research/principles/security-principles-research.md §5`.

---

## §6. Credentials never enter environment variables

**Principle.** API keys are injected via file-based bind-mount at `/run/secrets/<key>`, not via `docker run -e KEY=value`. The container entrypoint reads the file and exports the env var inside the container, after which the host-side temp file is deleted. This follows OWASP and CIS Docker Benchmark guidance.

### Pattern

For every credential the agent needs:

1. Host writes the value to a temporary file with restrictive permissions.
2. The file is bind-mounted read-only into `/run/secrets/<key>` inside the container.
3. The container entrypoint reads the file, exports the env var, deletes the host-side temp file (within seconds of container start).
4. The agent reads the env var as it normally would.

The accepted tradeoff: the agent process has the API key in its environment. `/proc/<pid>/environ` exposes it to same-user processes inside the container — but the agent already has full use of the key, so this is not a new exposure.

### What this protects against

- `docker inspect` does not show the key.
- `docker commit` does not capture it.
- `docker logs` does not leak it.
- No temp file lingers on host disk.
- Image layers never contain the key.

### Worked examples

- **`/run/secrets/<key_name>`** scheme documented in `docs/design/security.md` §Credential Management.
- **macOS Keychain fallback for Claude Code** (commit `09306e4`, 2026-02-26): if no `ANTHROPIC_API_KEY` env var is set, yoloAI can read from the host Keychain — still injected as a file, not an env var.
- **OAuth tokens for Claude Max** (commits `e75b55b`, `4d1d114`): subscription credentials also flow through `/run/secrets/`, not env vars.
- **Seatbelt environment whitelist** (D15): only safe OS variables (`PATH`, `HOME`, `USER`, `SHELL`, `TERM`, `LANG`, `LC_*`) are passed. Credential-shaped env vars (`AWS_SECRET_ACCESS_KEY`, `SSH_AUTH_SOCK`) are filtered out.
- **Defaults to non-forwarding for `env:`** in config: the user opts in to forwarding specific env vars via the profile config, not the other way around.

### Cost-vs-benefit

Cost of applying: a slightly more complex entrypoint and a host-side temp file lifecycle to manage. Damage prevented: credential leakage via `docker inspect` output (which is logged by many container management UIs); the long history of "we accidentally committed the docker-compose.yml with a real key" because env-var-style configs are easy to dump.

### Sources

OWASP Docker Security Cheat Sheet; CIS Docker Benchmark §4; Docker official-image conventions. Full citations: `../research/principles/security-principles-research.md §6`.

Originally established alongside the v1 design + D15 (Seatbelt) + D17 (`--isolation` flag preserves the same model across backends).

---

## §7. Default-deny over default-allow

**Principle.** When choosing between "block known-bad" and "allow known-good," allow known-good wins. Blocklists miss new credential locations, new exfiltration routes, new dangerous paths. Allowlists are enumerable and audit-able.

### Pattern

Threshold: anywhere the agent might reach for something we didn't anticipate, deny by default and require explicit opt-in. Anywhere we know what's needed, name it.

### Worked examples

- **Seatbelt environment** (D15): whitelist of safe variables, not blocklist of unsafe ones. The agent gets `PATH`, `HOME`, locale; it doesn't get `SSH_AUTH_SOCK`, `AWS_SECRET_ACCESS_KEY` unless the user opts in via `env:` config.
- **Seatbelt filesystem allowlist**: `~/.local/`, `~/.gitconfig`, `~/.config/git/`. Everything else is denied. The agent can't read `~/.ssh/`, `~/.gnupg/`, `~/.aws/`, `~/.git-credentials`, `~/.npmrc`.
- **Network isolation `--network-isolated`** (D11): default-deny iptables with an explicit allowlist of domains. The agent reaches Anthropic's API because we allowlisted it; everything else is denied.
- **Dangerous-directory refusal**: refuses a set of host paths unless `:force`. The list is enumerable; new dangerous paths can be added when discovered.
- **Mount modes**: `:ro` is the default for aux dirs (D4). Writable access requires explicit `:rw`. `:copy` and `:overlay` are workdir-only (Q-U).
- **Per-agent network allowlist**: each agent definition specifies its required domains (e.g., `api.anthropic.com` for Claude Code). The user can extend via `--network-allow <domain>`. The agent's baseline is the minimum it needs to function.

### Cost-vs-benefit

Cost of applying: maintaining the allowlist as the agent's needs evolve. Damage prevented: silent privilege creep when a new credential file appears in `~/`; exfiltration over a route we forgot to deny.

### Sources

Saltzer & Schroeder (1975); OWASP allowlist guidance. Full citations: `../research/principles/security-principles-research.md §7`.

Originally established alongside D15.

---

## §8. Verify isolation claims — test against real scenarios, not marketing language

**Principle.** A backend's claim that it "isolates X" is a hypothesis until verified. Vendor marketing pages list features; what matters is whether the feature behaves the way the docs say in the conditions yoloAI uses. Verify before claiming.

### Pattern

Threshold: every isolation claim added to the design docs is verified by reading the relevant project source / running the relevant attack scenario / checking the published security boundary. Documented limitations (e.g., DNS UDP must be open for resolution) are stated up-front; we don't pretend they don't exist.

### Worked examples

- **Network isolation claim** ("Anthropic's devcontainer and Trail of Bits' devcontainer use iptables + ipset") verified by reading both projects' devcontainer.json + entrypoint scripts. Cited from primary sources, not from "people on Twitter said so."
- **Podman behaviour claims** verified against source: commit `77f9dab` (2026-03-15, "Verify Podman research claims against Docker/Podman/Buildah source"). Where Podman differs from Docker on rootless UID mapping, HOME handling, tmux exec — those are documented in `docs/dev/research/podman.md` from observed behaviour, not inferred from docs.
- **gVisor permission claims** verified by running yoloAI under gVisor and observing the bind-mount permission failures. The fix (relaxed 0777/0666 for container-writable paths, documented in `docs/design/security.md`) was empirical, not assumed.
- **gVisor on macOS bug** (commit `d078db6`): the upstream issue (Claude Code infinite `epoll_pwait`) was filed; yoloAI's refusal points at it directly.
- **Backend idiosyncrasies catalog** (`docs/dev/backend-idiosyncrasies.md`): every entry is symptom + explanation + fix + code pointer. No "the vendor docs say…" hand-waving.

### Cost-vs-benefit

Cost of applying: real verification time per claim. Damage prevented: security claims that don't hold under load; the trust-destroying moment when a user discovers the documented isolation isn't actually enforced; the "we said X, but it's actually Y" walk-back.

### Sources

Project `CLAUDE.md` §Critique Principles; D2, D5. Full citations: `../research/principles/security-principles-research.md §8`.

Originally established alongside D5.

---

## §9. Document the residual risk

**Principle.** Every defense has known limits. Users decide based on whether the residual risk is acceptable for their use case. Hiding the limits doesn't make them go away; it strips users of the information they need to decide. The residual risks are surfaced in the design docs, the user guide, and the error messages — not buried.

### Pattern

For every security feature, the documented section names:

- What the feature does protect against.
- What it does NOT protect against.
- Which class of attacker / scenario it's sized for.
- Known platform-specific quirks.

### Worked examples

- **`docs/design/security.md` is structured this way**. Each defense (file-based credentials, network isolation, dangerous-directory refusal) names its accepted tradeoffs explicitly.
- **`docs/design/network-isolation.md` §Threat Model**: "Sandbox escape via kernel/runtime exploit. This is gVisor's and Kata's domain. Network isolation cannot help if the agent breaks out of the sandbox entirely." Documented as out-of-scope, not pretended.
- **Network isolation known limitations** documented: DNS over UDP 53 must be open for domain resolution; CDN IP rotation can make rules stale; domain fronting remains theoretically possible. Same limitations as the consensus implementations (Anthropic, Trail of Bits).
- **gVisor + bind-mount permissions**: relaxed permissions are documented as the cost of gVisor's user-namespace UID remapping. The user trades file permission tightness for syscall filtering; they can decide.
- **`CAP_SYS_ADMIN` for `:overlay`**: documented as a broad capability with explicit tradeoff language: "the container's namespace isolation limits the blast radius, but this is a tradeoff: overlay gives instant setup and space efficiency at the cost of a wider capability grant."
- **`:rw` is documented as protection-off**: "give the agent direct read/write access. Use only when you've committed your work or don't mind destructive changes."

### Cost-vs-benefit

Cost of applying: a few sentences in each design-doc section. Damage prevented: a user assuming a defense covers more than it does, and being surprised when it doesn't; the trust gap when a user discovers a limitation that we knew about and didn't share.

### Sources

NIST SP 800-190 §3 (risk acceptance); project `CLAUDE.md`. Full citations: `../research/principles/security-principles-research.md §9`.

Originally established alongside D11 and `docs/design/security.md`.

---

## §10. Power-user escapes are explicit and documented

**Principle.** Features that undermine isolation (cap_add, devices, `--isolation container-privileged`, `--network host`, `--force` on dangerous directories) exist for users who need them. They are explicit, not silent. They are documented as "this undermines containment, here's why we offer it." There is no global "make me unsafe" flag.

### Pattern

Threshold: every isolation-undermining feature is a discrete opt-in with a documented rationale and a documented residual risk. The user types the feature; the tool doesn't infer it. Combined opt-ins (e.g., `:overlay` + `--network-isolated`) require multiple capabilities, each named in `docs/design/security.md`.

### Worked examples

- **`cap_add` in profile recipes** (`docs/design/config.md`): the user can grant additional capabilities for power-user features (e.g., Tailscale needs `NET_ADMIN`, GPU passthrough needs `--device`). Documented in `docs/design/security.md` §Security Considerations as a "no guardrails — a misconfigured recipe could undermine container isolation" path.
- **`--isolation container-privileged`** (commit `c133792`, 2026-05-18): Docker-in-Docker for power users. Explicit, named, documented to undermine containment by design.
- **`:force` on dangerous-directory mounts**: the user types `:force` to override the default refusal. They can mount `~` or `/etc` if they really want; the typing is the friction that makes the choice intentional.
- **`--network host` / `--network <name>`** override: undermines `--network-isolated`. The user names the override; the tool doesn't silently fall back.
- **VS Code Tunnel** (commits `1dea343`, `e6a16cb`, 2026-05-18): an explicit opt-in that opens a tunnel for remote development. Documented in the vscode-tunnel help topic; the user sees the implications.
- **`mounts:` config** for direct bind-mounts (e.g., `${SSH_AUTH_SOCK}:${SSH_AUTH_SOCK}:ro`): explicit credential surface granted by the user, documented in `docs/design/security.md` with the recommended way to forward SSH (via agent socket, not by mounting `~/.ssh/`).

### Cost-vs-benefit

Cost of applying: explicit flag/config naming + documentation. Damage prevented: silent opt-ins that surprise users; the "I didn't know that flag did that" failure mode; users discovering after an incident that an option they didn't understand had loosened isolation.

### Sources

`docs/design/security.md`; `general-principles.md §6`. Full citations: `../research/principles/security-principles-research.md §10`.

---

# Common over-generalisations to avoid

| Over-generalisation                                | Why yoloAI rejects                                                                                                                                                                                                                                                              |
| -------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Maximum-isolation-for-everyone**                 | Standard Docker is the default for a reason — it's the right cost/benefit for most users. Forcing gVisor / Kata / Tart on everyone pays platform-quirk debt across the entire user base for the benefit of the few. §3 — defense-in-depth as opt-in.                              |
| **Detect-prompt-injection-perfectly**              | The consensus paper (Anthropic + OpenAI + DeepMind, Nov 2025) says detection fails against adaptive attackers. Structural containment is the floor. §1, §5.                                                                                                                       |
| **Audit-everything-tamper-evidently**              | Out of threat model. The user is the principal; the user's own forensic investigation of their own agent run isn't a threat scenario yoloAI defends against. `log.txt` + bug-report bundle is sufficient. §1.                                                                     |
| **No-power-user-escapes**                          | Refusing to ship `cap_add` / `--isolation container-privileged` / `--force` forces power users to wrap yoloAI in their own scripts to get around it, which is worse. Explicit + documented + opt-in is better than refused. §10.                                                |
| **Block-the-user**                                 | The user is the principal, not an adversary. Dangerous-directory refusal can be overridden; that's working as designed. Friction without security is theatre. §1 — bounded threat model.                                                                                          |
| **Single-isolation-knob**                          | "Just give me `--secure-mode`." Rejected — collapses orthogonal axes (backend, isolation, network, mounts) into one knob and forces wrong tradeoffs. Each axis is explicit. §3.                                                                                                  |
| **Always-most-restrictive-network**                | The agent needs network access to function (model API calls). Defaulting to `--network-isolated` would break the default user experience. The isolation is opt-in for users with the relevant threat model. §6 — credential isolation handles the primary credential surface separately. |
| **Hide-residual-risk**                             | Users decide based on knowing the limits. Hiding them produces the trust-eroding "I thought this was safe" failure mode. §9 — surface residual risk explicitly.                                                                                                                  |
| **Network-isolation-as-data-loss-prevention**      | `--network-isolated` bounds destinations, not capabilities. DNS UDP must be open; CDN IP rotation; domain fronting on shared CDNs. Documented as residual risk (§9). Treating it as DLP is a category error.                                                                       |
| **Threat-model-as-static-document**                | The threat model evolves as the AI agent landscape evolves. Prompt-injection success rates change; new exfiltration routes emerge; new backends ship. The threat model is reviewed periodically — currently after each architecture audit (D19).                                  |

---

## Closing note

The security principles parallel the general / development / testing principles in shape: bounded scope (a stated threat model rather than maximum paranoia); explicit thresholds (when to defend vs. when to surface residual risk); worked examples grounded in real commits and design docs; over-generalisations to avoid.

The specialised tie-ins:

- `general-principles.md §5` (blast radius bounded) is the strategic version of `§2` here — containment as the design-time question.
- `general-principles.md §6` (safe defaults) is the strategic version of `§7` here — default-deny as the security application.
- `development-principles.md §3` (validate at every layer) and `§4` (parse, don't validate) are the structural enforcement of containment — the code-level discipline that the design-level containment depends on.
- `testing-principles.md §3` (error paths first-class) and `§6` (integration tests hit real backends) are how the containment claims actually get verified.

Future security principles land here as the threat landscape evolves — likely as agents gain new capabilities (tool use, multi-step planning, persistent memory) and new defense surfaces become relevant.
