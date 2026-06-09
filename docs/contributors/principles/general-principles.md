ABOUTME: Cross-cutting strategic principles for yoloAI. Thirteen principles
ABOUTME: scoped to a single-author OSS CLI: pragmatism, boring tech, ecosystem
ABOUTME: composition, reversibility, blast-radius, safe defaults, factual
ABOUTME: accuracy, document-the-no, surface-failures, cross-platform, default-
ABOUTME: to-public, design-as-hypothesis, speak-up-against-the-plan. Specialised
ABOUTME: principles (development, testing, security) cite back here.

# General principles

Cross-cutting strategic and decision-making principles for yoloAI. The meta layer above the three specialised principles docs (`development-principles.md`, `testing-principles.md`, `security-principles.md`); several specialised principles are applications of general principles to a specific surface.

Established in D22 (`../working-notes.md`). Primary-source backing: `../research/principles/general-principles-research.md`.

## Framing — cost-vs-benefit + scope discipline

yoloAI's decision discipline is the same across all four principles docs:

1. *Damage / cost of NOT acting*: user data lost, agent misbehaviour unprotected, host filesystem compromised, regression introduced, OSS reputation damaged.
2. *Cost of acting*: engineering build, ongoing maintenance across five runtime backends, complexity burden, dependency increase, time-to-ship delay.
3. *Threshold*: surfaced explicitly. Where damage clearly exceeds cost, act; otherwise default to "let it slide."

yoloAI is single-author at v1; **maintenance time across N backends** is the constrained resource. A feature that requires hooking into every backend pays five times the implementation tax. A dependency that misbehaves on any one of Docker / Podman / Tart / Seatbelt / containerd produces a debugging session Karl must personally complete. Recommendations that compound that tax must clearly justify the cost.

The scope difference vs. larger projects: there is no product manager to require the feature; there is no support team to absorb the documentation gap; there is no acquisition horizon to optimise diligence for. The relevant scope is "what does the next contributor (likely an AI agent, possibly a human) need to understand to land a correct change?" The principles below answer that question.

---

## §1. Pragmatic over perfect (YAGNI applied)

> **Rule.** Decide under cost-vs-benefit; default to the smallest intervention that produces real user benefit and defer features whose need is hypothetical. (Not a licence to ship low-quality code — malleability is the prerequisite; see DEV.)
>
> **Bites when:** building down the long tail of presumptive features, or gold-plating past the user-visible benefit. · **See also:** DEV §1, GEN §4.

**Principle.** yoloAI has a CLI to ship and a public-beta user base that needs reliability more than features. Decisions must be made under cost-vs-benefit. Don't go too far down the long tail of presumptive features. Default to the smallest intervention that produces real user benefit; defer features whose need is hypothetical. *This does not justify shipping low-quality code* — see `development-principles.md` for the malleable-code prerequisite that makes future iteration cheap.

### Pattern

For each proposed feature, ask: (a) does a current user need this *now*? (b) what's the cost of carry — does it slow future features or multiply the maintenance tax across all five backends? (c) what's the cost of delay — what feature is *not* shipped while we build this? Threshold: presumptive features are presumed guilty (Fowler 2015) — burden of proof shifts to the proposer to show current user pain. For a solo-author project the cost-of-carry term dominates: every feature Karl builds is one Karl must personally maintain.

### Worked examples

- The `--template` flag on `profile create` was designed and then dropped (commit `4c37f72`, 2026-03-01) because the use case was hypothetical and the implementation would have multiplied profile-system complexity.
- Test agent harness was deferred (commit `e08d354`, 2026-03-07) after `IdleSupport` design clarified the right abstraction; the harness would have been built against the wrong model.
- The proxy-sidecar approach to network isolation was researched and then rejected in favour of iptables + ipset (D11, `../working-notes.md`) because the proxy approach added significant operational complexity for marginal additional protection.
- Vault integration, OAuth/SSO, credential rotation are documented as deferred in `docs/contributors/design/security.md` — the v1 user base does not require them.
- Compare with `development-principles.md §No half-finished implementations` — same shape applied at the code-surface level.

### Cost-vs-benefit

Cost of applying: discipline at design time (~minutes per decision). Damage prevented: per Fowler's empirical claim, ~⅔ of features built without validation generate negative ROI. Threshold: ship the smallest thing that produces real benefit; defer the rest. Override when the cost of doing the small thing first + the big thing later exceeds doing the big thing now (rare).

### Sources

Kent Beck, Martin Fowler, 37signals. Full citations: `../research/principles/general-principles-research.md §1`.

Originally established in D22.

---

## §2. Boring tech, innovation tokens are scarce — spend them on the differentiator

> **Rule.** yoloAI has ~3 innovation tokens — spend them on the differentiated product surface; everywhere else choose boring tech with well-understood failure modes. Each new dependency is a future operational/security liability.
>
> **Bites when:** reaching for a novel or heavy dependency for a non-differentiating need. · **See also:** GEN §3.

**Principle.** yoloAI has ~3 innovation tokens (McKinley 2015). Spend them on the product surface that's differentiated; everywhere else, choose boring. Each new dependency adds a future liability whose ongoing operational and security cost dominates its build-time convenience. "Boring" means well-understood failure modes — yoloAI can list how it will let us down.

### Pattern

Every new dependency must answer "what failure mode does this introduce, and can I list it?" If the answer is "I don't know," the dependency loses by default. Refuse dependencies whose transitive tree we cannot audit; refuse tools whose "boring" applies only to the vendor, not to our understanding.

### Worked examples — yoloAI's concrete token accounting

| Token | Spent on                                    | Justification                                                                                                                                                              |
| ----- | ------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1     | Pluggable `runtime.Runtime` abstraction     | Five backends ship today (Docker / Podman / Tart / Seatbelt / containerd). The architectural bet is the moat.                                                              |
| 2     | Copy/diff/apply via `git format-patch`/`am` | The differentiator. No competitor protects originals with a review step. The bet is that this UX wins the category.                                                        |
| 3     | MCP server (`mark3labs/mcp-go`)             | `internal/mcpsrv/` exposes yoloAI as an MCP tool surface so editor-side agents can drive sandboxes. The bet is that MCP becomes the integration interface for AI tooling.  |

The reserved-token slot is now fully spent. Future departures from boring require a deliberate D-entry justifying the spend; there is no headroom for casual novelty.

Boring choices in the operational + supporting stack (no token spent):

- Go (current toolchain pinned in `go.mod`) + standard library + Cobra (CLI framework).
- Docker / Podman / containerd via existing client libraries; no custom container runtime.
- `git diff` / `git format-patch` / `git am` as the diff/apply primitive.
- `iptables` + `ipset` for network isolation (same as Anthropic devcontainer, Trail of Bits devcontainer).
- `tmux` for in-container session management.
- Standard YAML via `yaml.Node` for config (preserves comments).
- Python 3 + pytest + mypy for the `runtime/monitor/` Python surface.
- `overlayfs` for `:overlay` mode (Linux kernel feature, well-understood).
- `sandbox-exec` (Seatbelt) on macOS — Apple-supplied, documented.
- `tart` for macOS VMs — well-maintained, single-purpose tool.

Declined per §2:

- *Custom Go proxy sidecar for network isolation* — originally planned (commit `5e5cca3`, 2026-02-23); rejected in favour of iptables + ipset (D11) because the consensus implementation covers the threat model at a fraction of the operational complexity.
- *Custom diff/patch format* — rejected; `git format-patch` is the established mechanism.
- *Bun / `curl | bash` Claude installer* — rejected for proxy + AMD64 instability (`docs/contributors/design/questions-unresolved.md` #2).
- *Viper for configuration* — evaluated, not adopted (`standards/GO.md` §CLI Framework).
- *Mockgen / mockery for tests* — manual fakes + table-driven tests are the convention (`standards/GO.md`).

### Cost-vs-benefit

Cost of applying: an hour of research per major dependency at selection time. Damage prevented: vendor outages, transitive supply-chain risk (xz-style), pricing changes, debug sessions for failure modes you didn't expect. Threshold: 3-token budget for the architectural stack; everything else must be boring.

### Sources

Dan McKinley, DHH, post-xz dependency hygiene consensus. Full citations: `../research/principles/general-principles-research.md §2`.

Originally established in D22.

---

## §3. Don't reinvent the wheel — ecosystem-first

> **Rule.** Before designing a feature, check whether git, Docker, unix tools, or the runtime already provide the workflow; build the glue (the composition), not the primitives.
>
> **Bites when:** about to hand-build something git / docker / unix already does. · **See also:** GEN §2.

**Principle.** Before designing a feature, check whether git, Docker, unix tools, or the runtime already provide a workflow that solves it. yoloAI's value is the *composition* — copy/diff/apply on top of git, sandboxes on top of Docker / Podman / Tart, idle detection on top of agent hooks. Build the glue, not the primitives.

### Pattern

For every proposed feature: (a) is there an existing tool that solves this? (b) if yes, what does composing with it cost vs. reimplementing? (c) if we reimplement, what does the maintenance tax compound to across five backends? Default: compose. Override only when the existing tool's failure modes are genuinely worse than the bespoke alternative.

### Worked examples

- **`yoloai diff` / `yoloai apply`** uses `git format-patch <baseline>..HEAD` + `git am` (D9, commits `5ca1003`, `29895db`). The alternative — a bespoke diff format — was rejected; git is the established diff/apply mechanism, and using it preserves agent commit history rather than squashing.
- **Network isolation** uses iptables + ipset, the same mechanism Anthropic's Claude Code devcontainer and Trail of Bits' devcontainer use (D11). The custom-proxy sidecar was researched and rejected on cost-vs-benefit grounds.
- **Profile system** is "a Dockerfile + a config.yaml" (D12). The alternative — a templating layer over Dockerfile — was rejected; users already know Dockerfile syntax, and the templating layer would be another thing to learn, document, and debug.
- **Environment archetypes** (D18, `docs/contributors/design/environments.md`) parse existing `.devcontainer/devcontainer.json` files rather than requiring projects to learn a new format. Don't reinvent — work with files projects already have.
- **In-sandbox monitoring** uses `tmux` panes for session logging + screen capture (`docs/contributors/design/bugreport.md`). The alternative — a bespoke terminal multiplexer — would have been weeks of work to reach feature parity with tmux.
- **`yoloai system disk` / `system prune` (and `--images`)** (D21, D35) surface backend-native disk/cache information rather than reimplementing inventory. Compose with `docker system df`, `podman system df`, etc.

### Cost-vs-benefit

Cost of applying: discipline at design time + research into what exists. Damage prevented: reimplementation cost, ongoing maintenance of the duplicate primitive, divergence from ecosystem conventions, user confusion ("why doesn't `yoloai diff` accept `--stat` like git does?"). Threshold: when the existing tool's failure modes are bounded and documented, compose; when they aren't, consider reimplementing as a token spend (§2).

### Sources

Unix Philosophy — McIlroy, Thompson, Kernighan & Pike. Full citations: `../research/principles/general-principles-research.md §5`.

Originally established alongside D8.

---

## §4. Action speed is inversely proportional to irreversibility cost — Type 1 and Type 2 doors

> **Rule.** Match process to reversibility: reversible (Type 2) decisions ship fast at ~70% info; irreversible (Type 1) ones get confirmation gates. Watch for **Type 1.5** — looks reversible but isn't (CLI surface, config schema, user muscle memory).
>
> **Bites when:** applying heavy process to a reversible call, or shipping a CLI/config-schema change as if it were reversible. · **See also:** GEN §5.

**Principle.** Reversible decisions (Type 2 — two-way doors) ship fast at ~70% information; irreversible decisions (Type 1 — one-way doors) slow down with confirmation gates. The trap is applying Type 1 process to Type 2 decisions — slowness without benefit. Equally, mis-classifying Type 1 as Type 2 produces unrecoverable mistakes. Beware **Type 1.5** — looks reversible but isn't (e.g., CLI surface changes that retrain user muscle memory, config schema changes that break existing user files).

### Pattern

Threshold: every irreversible action gets explicit user-initiation + slow-path confirmation. Every reversible action ships without ceremony at ~70% information; if Karl is hesitating past 70%, that's slowness, not thoroughness. Pre-1.0 status (`CLAUDE.md` §Project Status) widens the Type 2 surface: breaking changes are allowed if tracked in `docs/BREAKING-CHANGES.md`.

### Worked examples

- *Type 1 (one-way doors)*: `yoloai destroy <name>` (removes a sandbox's full state, including the agent's uncommitted work); `yoloai system prune --images` (reclaims every available backend's base images, forcing a multi-minute yoloai-base rebuild on next `new` — plain `prune` is *not* a one-way door: it only reclaims regenerable cache); `yoloai apply` itself when the target directory has uncommitted changes. All three require explicit user-initiation; `apply` adds the dirty-repo warning.
- *Type 2 (two-way doors)*: flag rename, command grouping, help text rewording, error message refinement. CLI Pass 1–4 (commits `370599a`–`fa3ca8d`, 2026-03-04) shipped these without ceremony. Each was reversible; speed was the right call.
- *Type 1.5*: CLI surface changes that retrain user muscle memory (`--detach`/`-d` → `--attach`/`-a` flip in commit `94f46c4`); config schema changes (multiple `defaults.*` reorganisations). Treated as Type 1 by recording the breaking change in `docs/BREAKING-CHANGES.md` and surfacing migration guidance.
- The 12 rounds of pre-implementation critique (D2) were Type 1 process applied to Type 1 decisions (architecture, security model, capability grants). Not Type 1 process applied to flag names.

### Cost-vs-benefit

Cost of applying: a few seconds at decision time to classify. Damage prevented: solo-author time on Type-2 decisions (slowness without benefit); unrecoverable mistakes on Type-1 (irreversibility without due care). The pre-1.0 status amplifies the value: ship Type 2 fast, learn from real usage, refine.

### Sources

Jeff Bezos (Type 1/Type 2, 70% rule); Dave Snowden Cynefin. Full citations: `../research/principles/general-principles-research.md §3`.

Originally established in D22.

---

## §5. Worst-case is bounded — blast radius is a design-time question

> **Rule.** Every operation with real-world consequence has an explicit upper bound — timeout, refusal, confirmation, or pre-flight — paired with a clear failure message. "What's the worst case if this misbehaves?" is a required design-time question.
>
> **Bites when:** adding an operation with no bound on damage / cost / time. · **See also:** GEN §6, SEC §2, DEV §5.

**Principle.** Every operation with real-world consequence has an explicit upper bound — a timeout, a refusal, a confirmation gate, a pre-flight check. "What's the upper bound on damage / cost / time if this misbehaves?" is a required design-time question. The bound is paired with a clear failure message (the bound limits damage; the message prevents recurrence).

### Pattern

Threshold: **bound any operation whose worst case is bounded by machine or agent behaviour** (sandbox containing an autonomous AI agent, disk fill, runaway process, infinite-loop network resolution). **Do NOT bound operations whose worst case is bounded by human user behaviour** (number of sandboxes created, number of profiles, number of allowed network domains) until observed evidence shows runaway. Pair every bound with a clear error message.

### Worked examples

- **Dangerous-directory refusal** (`docs/contributors/design/security.md`): mounting `$HOME`, `/`, `/etc`, `/usr`, `/var`, `/boot`, `/bin`, `/sbin`, `/lib`, macOS system directories, requires `:force`. Default refuses; the bound is "no entire-host-filesystem mounts by accident."
- **Sandbox name validation** (D10): names matching `[a-zA-Z0-9_-]+` only. Path-traversal-shaped names refused at the CLI boundary, before any filesystem operation.
- **Dirty-repo warning**: warns when `:rw` is used on a directory with uncommitted git changes. The warning is the bound — the user can choose, but cannot do it accidentally.
- **`:copy` mode as default** (D4): the workdir defaults to `:copy`. Originals are protected unless the user explicitly opts into `:rw`.
- **Idle detection timeout** (D14): a sandbox with an unresponsive agent surfaces idle state through `agent-status.json`. The bound prevents "agent ran forever; nothing told us."
- **Disk pre-flight** (D21, commit `0d8d650`): a smoke test that would have failed with ENOSPC mid-way is refused upfront. The bound converts a confusing partial failure into a clear pre-flight error.
- **Two-stage smoke sentinel** (D21): early sentinel + final sentinel. Distinguishes "test never started" from "test started but didn't finish."
- **`yoloai system prune`** (D21, D35): bounds the disk-fill failure mode by giving users a primary lever to recover (`--images` for the deeper, rebuild-forcing reclaim).
- **NOT bounded**: number of sandboxes created (no daily limit); number of profiles (no cap). Both will be observed; bounds added if real evidence shows runaway.

### Cost-vs-benefit

Cost of applying: design-time discipline + bound implementation (small one-off cost per operation). Damage prevented: silent corruption, ENOSPC mid-operation, agent runaway, accidental mount of system directories. Threshold: machine/agent-behaviour worst cases default-bounded; user-behaviour worst cases observed-then-bounded.

### Sources

Chaos Engineering principles; Michael Nygard *Release It!*; Martin Fowler. Full citations: `../research/principles/general-principles-research.md §4`.

Originally established in D22.

---

## §6. Safe defaults — the protected mode is the default mode

> **Rule.** Every flag whose dangerous setting could cost the user data has its SAFE setting as the default; the user must type the dangerous one. Defaults are a safety-net, not a preference.
>
> **Bites when:** defaulting a flag to its convenient-but-dangerous setting. · **See also:** SEC §7, GEN §5.

**Principle.** Every flag whose dangerous setting could cost the user data has its safe setting as the default. The user must type the dangerous one. Defaults are not preferences — they are the safety-net that catches the new user, the rushed user, and the user who didn't read the docs.

### Pattern

For every flag with a safety dimension: pick the safe default. The unsafe option is opt-in by explicit syntax. Document the trade-off so users opting in know what they're accepting. Do not provide global "make me unsafe" flags; force the user to make the decision per-operation.

### Worked examples

- **All aux dirs read-only by default** (D4). Write access is opt-in per-directory via `:rw` (live bind-mount). `:copy` and `:overlay` are workdir-only (Q-U): the diff/apply workflow operates on the workdir, and the aux multi-dir surface was removed in beta.
- **Workdir defaults to `:copy`**, not `:rw` (D4). The protected mode is the default. `:rw` requires typing.
- **`:overlay` requires `CAP_SYS_ADMIN`** and is opt-in. The default (`:copy`) avoids the capability grant entirely.
- **Sandbox name required, no auto-generation** (`docs/contributors/design/README.md` §Design Principles). Anonymous sandboxes lead to sprawl; named sandboxes are findable.
- **Credentials via file-based bind-mount, not env vars** (`docs/contributors/design/security.md`). OWASP and CIS Docker Benchmark guidance is "never pass secrets as env vars to `docker run`"; yoloAI follows it.
- **Seatbelt default-deny on host filesystem and host environment** (D15). The user opts in to credential access via `env:` and `mounts:` config.
- **Network is unrestricted by default** but `--network-isolated` and `--network-none` are first-class. The default reflects what most users need (agent API calls); the safety flags are easy to add.
- **Dirty-repo warning** (`docs/contributors/design/security.md`): `:rw` on a directory with uncommitted changes triggers a warning. The default doesn't refuse, but it doesn't go silent either.
- **Run as non-root** in the container (user `yoloai` matching host UID/GID), not as root.

### Cost-vs-benefit

Cost of applying: design-time discipline to pick the safe default and verify it. Damage prevented: data loss from agent misbehaviour, accidental writes to system directories, credential exposure via env-var probing, sandbox-sprawl, and the "I didn't realize that flag did that" class of incident. The cost is low and one-off; the protection is permanent.

### Sources

OWASP, CIS Docker Benchmark; Saltzer & Schroeder (1975). Full citations: `../research/principles/general-principles-research.md §6`.

Originally established alongside D4 + D15 + D16.

---

## §7. Factual accuracy bar — verify before you cite

> **Rule.** Verify claims (competitor features, counts, security properties, kernel behaviour, vendor SLAs) against primary sources before citing; marketing language and plausibility are not evidence.
>
> **Bites when:** about to state a competitor / security / kernel fact you haven't checked. · **See also:** GEN §12, SEC §8.

**Principle.** Research must be verified. Claims about competitor features, star counts, security properties, kernel behaviour, and vendor SLAs are not statements until they are checked against primary sources. Marketing language is not evidence. Plausibility is not verification.

### Pattern

Threshold: every factual claim in a design doc, research file, or competitive comparison is traceable to a dated, named, findable source. If a claim is "everyone knows this," check it anyway — the failure mode is confident confabulation, not malicious lying. Star counts, version numbers, feature presence/absence, security claims, and platform-specific behaviour get the highest scrutiny.

### Worked examples

- **Critique cycle** (D2) — twelve rounds of pre-implementation critique against `docs/contributors/design/` and `docs/contributors/design/research/` shaped the v1 design. Each round found verification gaps that would have shipped as bugs.
- **Competitor research** (`docs/contributors/design/research/competitors.md`, sentiment tracking in `docs/contributors/design/research/sentiment.md`) — feature claims and star counts are dated and sourced. The Agent Safehouse / BunkerVM / Zeroboot research files all cite the source HN item or repo.
- **Backend idiosyncrasies catalog** (`docs/contributors/backend-idiosyncrasies.md`) — every entry has a symptom, an explanation, a fix, and a code pointer. Includes a symptom index for fast lookup. "Read this before diagnosing any backend problem" is the explicit project rule.
- **Network isolation claim** ("Anthropic's devcontainer and Trail of Bits' devcontainer use iptables + ipset") — verified by reading both projects' devcontainer.json + entrypoint scripts. Cited in `docs/contributors/design/security.md` and `docs/contributors/design/network-isolation.md`.
- **Podman backend plan** (`docs/contributors/design/research/podman.md`) — verified against Podman source rather than against blog posts (commit `77f9dab`, "Verify Podman research claims against Docker/Podman/Buildah source").
- **The platform-specific test** — every claim of the form "X works on Y" is verified against Y specifically. macOS Docker Desktop + VirtioFS, Tart on Apple Silicon, gVisor on macOS (commit `d078db6`, "Block gVisor on macOS with error pointing to Claude Code issue") all carry their own verification trails.

### Cost-vs-benefit

Cost of applying: 5–30 minutes per non-trivial claim to verify. Damage prevented: design built on a wrong fact, security claim that doesn't hold under load, competitor framing that misleads users (and embarrasses the project when corrected publicly). Threshold: any claim that drives a design decision must be verified; trivial supporting claims may inherit verification from a primary source.

### Sources

LLM hallucination literature (Maynez et al.); project critique discipline (D2, D5). Full citations: `../research/principles/general-principles-research.md §7`.

Originally established in D5.

---

## §8. Document the "no" — D-entries are the future-self preservation layer

> **Rule.** Every meaningful decision records what yoloAI explicitly does NOT do, and why, in a D-entry — the rejected alternatives matter as much as the chosen path, or the debate gets relitigated.
>
> **Bites when:** making a non-trivial decision without recording the rejected alternatives. · **See also:** GEN §11.

**Principle.** Every meaningful decision records what yoloAI explicitly does NOT do, alongside what it does. The rejected alternatives and the rationale are as important as the chosen path — without them, future-Karl (or a future contributor, or an AI agent) will relitigate the same debate. D-entries are 1–2 pages each, written as a conversation with a future maintainer.

### Pattern

Threshold: D-entry for any decision whose reversal would cost more than a day of work. Skip for trivial choices (flag names, copy edits). Canonical sections (Nygard 2011): Date / Status / Decision / Rejected / Why / Consequences. Modern extensions: **Composition** (which earlier D-entries this builds on) and **Expiration trigger** where useful ("revisit if a sixth backend is added" / "revisit if we go 1.0").

### Worked examples

- `../working-notes.md` D1–D22 are the project's D-log. Each has:
  - "Decision" (what yoloAI does).
  - "Rejected" (the alternatives — the *no*).
  - "Why" (the cost-vs-benefit reasoning).
  - "Consequences" (what fell out, including downstream design / code references).
  - "Composition" (which earlier decisions this builds on).
- The retroactive D1–D21 reconstruct major past decisions from commit history (Python → Go, critique cycle, mount-mode taxonomy, runtime interface, network isolation choice, etc.) — flagged **(retroactive)** so future-readers know the rationale is inferred from outcomes.
- D22 itself documents the decision to adopt this principles/standards structure, including why business-principles was rejected.
- The "Common over-generalisations to avoid" section below (and the parallel sections in `development-principles.md`, `testing-principles.md`, `security-principles.md`) is the same pattern at the principle layer.

### Cost-vs-benefit

Cost of applying: ~5–15 minutes per meaningful decision. Damage prevented: months of re-litigation when a previous decision is forgotten; weeks of post-hoc rationalisation when a contributor asks "why isn't this X?"; the cost of changing direction on a decision that already cost real time. **This is the lowest-cost, highest-leverage principle in the set.**

### Sources

Michael Nygard, ADR templates (Joel Parker Henderson). Full citations: `../research/principles/general-principles-research.md §8`.

Originally established in D22 (this set).

---

## §9. Surface failures honestly — diagnostic-first, not catch-all

> **Rule.** When a failure could be ambiguous (timeout vs ENOSPC vs crash vs misconfig), surface the specific cause as early as possible — a pre-flight refusal beats a confusing mid-operation failure. Diagnostic-first, not catch-all.
>
> **Bites when:** writing a catch-all error that defers diagnosis to the user. · **See also:** DEV §5.

**Principle.** When a failure could be ambiguous (timeout vs. ENOSPC vs. agent crash vs. configuration error), surface the specific cause as early as possible. Catch-all error messages defer diagnosis to the user; specific error messages move it to the tool. The pre-flight check is the canonical pattern: refuse upfront with a clear error rather than failing mid-operation with a confusing one.

### Pattern

Threshold: when a failure mode has been observed twice and produced confusion, write the diagnostic. Pre-flight checks are preferred over post-mortem analysis. Idiosyncrasies that have caused real bugs get an entry in `docs/contributors/backend-idiosyncrasies.md` with a symptom index.

### Worked examples

- **Disk pre-flight** (D21, commit `0d8d650`): a smoke test that was failing as "agent idle 9s+" was actually disk-full. The pre-flight refuses upfront with a clear ENOSPC message. The memory entry `project_smoke_disk_pressure.md` records this case explicitly.
- **Two-stage smoke sentinel** (D21): distinguishes "test never started" from "test started but didn't finish."
- **`yoloai system disk`** + **`system prune` (and `--images`)** (D21, commit `d894f00`; D35): surface disk inventory and give users the primary recovery lever directly.
- **Backend idiosyncrasies catalog** (`docs/contributors/backend-idiosyncrasies.md`): every observed surprise (e.g., "containerd GitExec returns `*runtime.ExecError` on non-zero exit", commit `8749864`) becomes a documented entry with a symptom index. "Read this before diagnosing any backend problem" is the project rule.
- **Structured logging + bug-report bundle** (`docs/contributors/design/bugreport.md`, commits `222bf71` → `ec21f2c`, March 2026): a single `yoloai bugreport` produces a sanitized, timestamped diagnostic bundle. The user doesn't have to manually collect logs from multiple sources.
- **Capture container logs before removal** (commit `387f278`, 2026-03-17): an explicit fix for "container vanished before we could diagnose."
- **`--debug` flag**: opt-in entrypoint debug logging, captured in `log.txt`. Available when the default output isn't enough.

### Cost-vs-benefit

Cost of applying: a one-off diagnostic cost per failure mode + the discipline to write the pre-flight when the failure mode is first observed. Damage prevented: user frustration (the "what does this error even mean?" experience); re-occurrence of the same diagnosis; reputation cost when the tool is opaque. Threshold: the second observation of an ambiguous failure earns a diagnostic.

### Sources

Charity Majors / Honeycomb on observability; Michael Nygard *Release It!*. Full citations: `../research/principles/general-principles-research.md §9`.

Originally established in D21.

---

## §10. Cross-platform awareness — verify per platform, document tradeoffs

> **Rule.** A claim isn't cross-platform until verified per platform (Linux, macOS Docker/Tart/Seatbelt, Windows/WSL); document platform-specific tradeoffs explicitly rather than hiding behind "works on my machine."
>
> **Bites when:** asserting cross-platform behaviour verified on only one platform. · **See also:** GEN §7, SEC §8.

**Principle.** Linux, macOS Docker Desktop, macOS Tart, macOS Seatbelt, Windows/WSL each behave differently in ways that matter (kernel features, filesystem semantics, capability grants, user namespace mapping, mount support). A claim is not a cross-platform claim until it is verified per platform. Platform-specific tradeoffs are documented explicitly, not hidden behind "it works on my machine."

### Pattern

Threshold: every feature that touches the kernel, the filesystem, or the network is verified on all platforms it claims to support. Platform-specific limitations are surfaced (in design docs, in `docs/contributors/backend-idiosyncrasies.md`, in CLI error messages). Default tooling targets Linux + macOS Docker Desktop; macOS-specific backends (Tart, Seatbelt) and Windows/WSL are explicit secondary targets with documented limitations.

### Worked examples

- **gVisor blocked on macOS** (commit `d078db6`, 2026-03-17): macOS + Claude Code under gVisor hangs in an infinite `epoll_pwait`. yoloAI refuses with a clear error pointing to the upstream bug rather than letting the user hit an infinite hang.
- **`--storage-opt size=`** noted as Linux-only in design discussions — Docker Desktop on macOS doesn't support it. Documented to avoid users hitting the limitation in production.
- **Tart for macOS VMs** (commit `814d379`, 2026-02-26): the macOS-only backend. Linux users get a clear error rather than a confusing failure if they try `--backend tart` on Linux.
- **Seatbelt** is macOS-only, restricted to default-deny credential access (D15). Documented in `docs/contributors/design/security.md` §Seatbelt Backend Security.
- **VirtioFS on macOS Docker Desktop**: known ~3x I/O penalty for bind mounts (documented in `docs/contributors/design/research/sandboxing.md` and competitor research). Influenced the decision to support `:copy` and `:overlay` as alternatives.
- **gVisor + bind-mount permissions** (`docs/contributors/design/security.md`): gVisor uses the same restrictive 0750/0600 host-side permissions as standard Docker — the container runs as the invoking host UID, so no relaxed/world-readable bits are needed. An earlier assumption to the contrary was empirically disproved (finding DF20).
- **Windows/WSL** documented as expected-to-work-with-known-limitations (`docs/contributors/design/README.md` §Prerequisites): path translation, UID/GID mapping, `.gitignore` line endings.
- **Architecture audit (2026-05)** W6 (commit `b99b46e`): CLI lifecycle subset runs on Podman in CI to catch backend-specific regressions.

### Cost-vs-benefit

Cost of applying: testing across platforms + documenting platform-specific tradeoffs. Damage prevented: "works on my machine" failures, silent platform-specific data corruption (the gVisor permission bug would have produced exactly this), user reports of "yoloAI is broken" when the cause is platform-specific. Threshold: verify before claiming, document the tradeoffs, refuse known-bad combinations explicitly.

### Sources

Operationalised through project research and idiosyncrasies docs; no single external canonical source.

Originally established in D5 (cross-platform clause in `CLAUDE.md` critique principles).

---

## §11. Default to public

> **Rule.** When in doubt, publish — design docs, research, decisions, breaking-changes, the principles themselves. The cost of publishing is trivial; the trust benefit compounds.
>
> **Bites when:** keeping a doc or decision private by default. · **See also:** GEN §8.

**Principle.** When in doubt, publish. Design docs, research files, the idiosyncrasies catalog, the BREAKING-CHANGES log, the roadmap, the decision log, the principles themselves. yoloAI is OSS; the cost of publishing is trivial; the trust benefit compounds (users, contributors, AI agents reading the docs to land changes).

### Pattern

Threshold: publish unless one of the following applies — (a) the information materially aids an attacker against a specific user (e.g., a yet-unfixed escape path); (b) the information is contractually restricted (rare for an OSS project); (c) the information identifies individuals without consent. The default flips from "internal unless required to publish" to "public unless required not to."

### Worked examples

- All design docs (`docs/contributors/design/`) are public.
- All research files (`docs/contributors/design/research/`) are public.
- The decision log (`../working-notes.md`) is public.
- The principles docs (this set) are public.
- The backend-idiosyncrasies catalog (`docs/contributors/backend-idiosyncrasies.md`) is public — including failures, workarounds, and "yoloAI itself was the bug" entries.
- `docs/BREAKING-CHANGES.md` is public — every breaking change since public beta with rationale and migration steps.
- The roadmap (`docs/ROADMAP.md`) is public — agents, network isolation refinements, overlayfs, profiles.
- The README is structured as an elevator pitch (commit `33907a1`, 2026-02-26) — what yoloAI does, what it doesn't, what it competes with.
- Public sentiment tracking (`docs/contributors/design/research/sentiment.md`) records what the community is saying about the category, including where yoloAI is mentioned.

### Cost-vs-benefit

Cost of applying: a few minutes per document to remove sensitive content (rare). Damage prevented: trust gap (users can't tell what yoloAI does); contributor friction (a contributor has to ask for things that should be on the website); SEO miss (public content is searchable); community-engagement loss (yoloAI's design choices can inform the indie AI-tooling community + receive reciprocal feedback).

### Sources

37signals transparency culture; OSS norms; RFC 9116. Full citations: `../research/principles/general-principles-research.md §11`.

Originally established in D22.

---

## §12. A design is a hypothesis — aspirational until verified against reality

> **Rule.** A design / spec / API-checkpoint is a provisional, falsifiable model — load-bearing only after implementation verifies it against the real capability. When facts contradict the model, the facts win: revise it and record why.
>
> **Bites when:** treating an unimplemented design as a contract, or coding to the doc when reality diverges. · **See also:** GEN §7, GEN §13.

**Principle.** A design — a design doc, an API-surface checkpoint, a spec — is a *model*: a deliberately lossy compression of reality, not a contract. A model hides detail to make a problem thinkable, and that same hiding is what lets it be wrong: parts of any model break down when implementation surfaces facts the model omitted. A design is therefore **provisional and falsifiable until it has been implemented and verified to work against the real internal capability.** When facts contradict the model, the facts win: revise the model and record *why*. This mirrors the scientific method — design is the hypothesis, implementation the experiment, divergence the analysis, the updated doc the conclusion. The mirror image of §7: §7 requires a design be backed by *research*; §12 requires it also be backed by *implementation* before it is load-bearing.

The deeper reason this is a standing principle and not a one-off caution: we are not omniscient, so our models *will* eventually diverge from reality once the rubber meets the road. A process that cannot revise its own map after that divergence will encode the first wrong guess permanently. So the models here are **designed to be changeable** — the obligation is to keep the map honest, never to defend it.

### Pattern

When implementing against a design, start from the observed facts (what the internal layer actually does), design the cleanest surface for *that*, and where it diverges from the design, **revise the design doc (or mark it superseded), cross-referenced to a D-entry.** Two symmetric failure modes are guarded: (a) slavishly implementing an unverified model — building behaviour the doc described that has no basis in fact; (b) silently diverging without updating the doc — leaving a stale map so the next implementer re-derives the same collision. The obligation on divergence is **revise + log the why**, not "ignore the design." Aspiration informs direction; facts and clean architecture decide.

### Worked examples

- **The `//go:build never` design-checkpoint convention.** The project's mechanism for an API-surface hypothesis is a Go file tagged `//go:build never` — literally uncompiled, so the tag *is* the "unverified hypothesis" marker: cite it for direction, never as binding. The canonical instance was `api_surface.go`, which carried the proposed `yoloai.Client` surface through the layering refactor.
- **Retiring a concluded hypothesis is itself §12 (D45).** Once the layering refactor *implemented* that surface, the hypothesis was concluded — experiment run, result known. At that point `api_surface.go` had drifted ~55% from the shipped surface, and keeping a husk file alive only to satisfy doc cross-references would have let the map dictate the territory — the exact inversion this principle warns against. So it was retired: its resolved decisions were salvaged into the decision log (D45) and the file deleted. The live godoc is now the surface of record. Conclude and log; don't preserve a stale map.
- **F2 re-rooting (D25):** the checkpoint designed `Status()` (deferred — no cheap status-only path in the manager), an elaborate Restart isolation-transition policy (deferred — no internal basis), and "delete `NeedsConfirmation`" (kept as `HasActiveWork` — the batch `destroy` command needs a side-effect-free pre-check). Each was a hypothesis that didn't survive the facts; the checkpoint was annotated with the divergence rather than forcing the implementation to match.
- **F4 / F21 (D24):** the checkpoint's empty-`Backend` isolation/OS routing collided with F4's "require Backend." Resolved by reconciling to the fact that backend selection is ambient (belongs at the boundary), not by preserving the routing.
- **Contrast with §1 (YAGNI):** §1 is about not building *future* features; §12 is about not building *present design* that the facts can't support. Both reject speculative work; §12 names the design-doc as a specific speculative source.

### Cost-vs-benefit

Cost of applying: the discipline to revise the doc on divergence (~minutes) and to resist both copying the doc verbatim and ignoring it. Damage prevented: speculative validation logic for behaviour with no basis (wasted build + dead public API that must later be removed); stale design docs that mislead the next implementer into re-deriving a known collision. Threshold: any time implementation reveals a design can't be cleanly realised, stop and reconcile the doc to the facts.

### Sources

Karl Popper on falsifiability; George Box ("all models are wrong, some are useful"). Operationalised via the `//go:build never` checkpoint convention and D-entry reconciliation process.

Originally established in D25.

---

## §13. No plan survives contact with the enemy — surface divergence and better ideas, even against an agreed plan

> **Rule.** If something looks off — even when agreed, in the spec, planned in detail — raise it and discuss; if you find a better way, raise it. Agreement is a strong prior, not a gag order.
>
> **Bites when:** silently following an agreed plan you've already spotted a problem in. · **See also:** GEN §12.

**Principle.** No design or plan is perfect. We work with incomplete information, and reality supplies the missing facts only once we act on them — so we *will* make mistakes, and the only question is whether we catch them. The standing obligation: **if something looks off — even if it is agreed upon, in the design, in the spec, planned to the minutest detail — you bring it up and we discuss it. If you discover a better way, you bring it up and we discuss it.** Agreement is a strong prior, not a gag order; a prior decision is a closed question only until new information reopens it. The discipline is to stay flexible when we discover our model of the world is wrong, and to treat that discovery as information to act on, not an inconvenience to suppress. Helmuth von Moltke the Elder: *"No plan survives contact with the enemy."*

This is the operating-discipline corollary of §12. §12 is the *epistemics* — a design is a falsifiable hypothesis; when facts contradict the model, the facts win. §13 is the *duty that follows*: the moment you sense the falsification — mid-plan, mid-implementation, anytime — the obligation is to **raise it**, not to push through to honour the agreement. §12's named failure mode is *silently diverging from a design*; §13's is *silently complying with a plan you have reason to doubt* — finishing the agreed thing while withholding the observation that it's wrong. §13 is also broader: it governs plans and in-flight decisions, not only design docs.

### Pattern

When a fact, an observation, or a cleaner option contradicts an agreed plan: **stop and surface it, with the reason, before continuing** — don't silently comply, and don't silently reroute either (that's the §12 divergence failure). Bring the specific observation, not generic unease. The collaboration loop is *raise → discuss → revise-or-confirm → log the why* (a D-entry when it changes a decision). The trigger is **new information**: a concrete signal that something is off, or a specific better way — not a mood, not a preference, and not a wish to relitigate a settled call. Reopening an agreement requires a reason; absent one, the prior decision stands and you proceed.

### Worked examples

- **The D52 "Layer-1 complete" reversal (CRITIQUE G1).** "Layer-1 spine FULLY COMPLETE / no deferrals" was agreed and logged in D52. A later critique found it false — the F1 leak detector was blind to type aliases, so `store.Meta` leaked through `yoloai.Info` unseen. §13 in action: raise it and reopen the "complete" verdict rather than build further on an agreed-but-wrong claim. Truth before cleanup.
- **The interaction-surface audit (D53 corrections).** The consumer model had been "written down" as two interaction surfaces (PTY + activity stream). A code audit then found a *third* (structured file exchange via `store.FilesDir()`) and ~7 missing public verbs. Bringing those up *after* the model was agreed — because the audit surfaced new facts — is the duty, not second-guessing.
- **Contrast with §4 (70% rule).** §13 is not "delay everything to discuss." §4 still says reversible decisions ship at ~70% information. §13 fires when you have a *specific* reason to doubt, not as a standing invitation to re-deliberate; the two compose — move fast by default, speak up the instant a real divergence appears.

### Cost-vs-benefit

Cost of applying: the friction of interrupting forward motion to raise a concern (~minutes), plus the judgement to distinguish a real signal from generic doubt. Damage prevented: shipping an agreed-but-wrong plan to completion because no one wanted to reopen it (the most expensive failure — it's discovered after the work is built on top); and its inverse, a contributor silently rerouting around the plan so the map goes stale (§12). Threshold: any time a fact or a clearly-better option contradicts the agreed path, surface it before proceeding. Guardrail: the trigger is new information, not preference — see the "Speak-up-as-licence-to-relitigate" over-generalisation below.

### Sources

Helmuth von Moltke the Elder (*Über Strategie*, 1871); Popper; George Box. Operationalised via the critique discipline and D-entry reconciliation (D52→CRITIQUE-G1, D53 audit).

Originally established in D54.

---

# Common over-generalisations to avoid

The cost-vs-benefit discipline (Framing) explicitly rejects principle-shaped statements that don't pay off at yoloAI's scale. The following are documented so future-yoloAI doesn't drift toward them.

| Over-generalisation                          | Why yoloAI rejects                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| -------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **DRY-at-all-costs**                         | Some duplication is cheaper than the wrong abstraction (Sandi Metz, "The Wrong Abstraction," 2016). yoloAI accepts limited duplication across the five backends when the abstraction's coupling cost would exceed the redundancy cost. Cf. `development-principles.md`.                                                                                                                                                                                                                                                          |
| **Choose-new-as-best**                       | Opposite of §2. Novelty is not a goal; boring is the goal except where yoloAI spends an innovation token deliberately. New tech for new tech's sake is a maintenance multiplier across five backends.                                                                                                                                                                                                                                                                                                                            |
| **Generality-by-default**                    | §1 + §3 reject pre-emptive generality. Build the concrete first; abstract only when a second concrete use case appears. The pluggable runtime interface (D7) is the exception that proves the rule — generalised only when the second backend (Tart) made the abstraction load-bearing.                                                                                                                                                                                                                                          |
| **Zero-bugs / zero-regressions framing**     | Denies the cost-vs-benefit floor. yoloAI accepts non-zero bugs (per chaos-engineering tradition) and uses bounded blast radius (§5) + safe defaults (§6) + factual accuracy (§7) to limit user impact when bugs ship. The discipline is bounding worst case, not eliminating it.                                                                                                                                                                                                                                                  |
| **All-features-must-be-tested-in-CI**        | CI tests what is cheap to run there. End-to-end Tart tests require Apple Silicon hardware; Podman rootless tests require a Linux host; containerd tests require nerdctl. Coverage is layered (unit, integration per backend in CI, e2e on developer machines). Forcing full e2e in CI would require renting Apple Silicon CI; the cost-vs-benefit doesn't pay.                                                                                                                                                                    |
| **Backwards-compat-forever**                 | yoloAI is in public beta. Breaking changes are allowed and tracked (D16, `docs/BREAKING-CHANGES.md`). Permanent compat shims accumulate vestigial code and slow design evolution. Each shim is justified or removed; the `runtime-config.json` fallback was added and removed in seven minutes (D16).                                                                                                                                                                                                                            |
| **Status-page-as-PR**                        | yoloAI doesn't have a status page (it's a CLI, not a service), but the same shape applies to the idiosyncrasies catalog: it exists to inform users, not to make yoloAI look impressive. Surface honestly (§9).                                                                                                                                                                                                                                                                                                                  |
| **Blast-radius-as-defensive-over-engineering** | The §5 reading that bounds *everything*, including user-behaviour worst cases without evidence of runaway. yoloAI bounds machine/agent-behaviour worst cases at design time and observes user-behaviour worst cases before bounding — prevents the circuit-breaker-on-low-volume-call pattern that creates new failure modes.                                                                                                                                                                                                  |
| **Type-1/Type-2-as-cover-for-slow-decisions** | §4 explicitly says reversible decisions ship at 70% information. Using "this is Type 1, we need more info" as default cover for indecision is the §4 trap. Type 1 is the exception; Type 2 with fast iteration is the default. Pre-1.0 status widens the Type 2 surface further.                                                                                                                                                                                                                                                 |
| **Public-by-default-without-curation**       | §11 encourages publishing; it does NOT justify dumping unreviewed working notes into the public docs surface. Public means *intentionally* public — design docs reviewed, research files cited, idiosyncrasies cataloged with code pointers. Unreviewed scratch lives in `docs/contributors/old/` or `docs/contributors/design/plans/`.                                                                                                                                                                                                                  |
| **Cross-platform-by-default-without-test**   | §10 requires per-platform *verification*, not per-platform *aspiration*. Claiming a platform without testing on it is the failure mode §10 prevents. The Windows/WSL "expected to work, not a primary target" stance (`docs/contributors/design/README.md`) is honest scoping, not avoidance of §10.                                                                                                                                                                                                                                          |
| **Design-doc-as-contract**                   | §12 rejects implementing a design (an API-surface checkpoint, a spec) verbatim before it's verified against the internal facts. The doc is a hypothesis; building what it describes without checking it can be cleanly realised produces speculative behaviour and dead public API (the deferred Restart isolation-policy / `Status()` in F2). Cite a design for *direction*, never as binding.                                                                                                                                                |
| **Facts-win-as-licence-to-ignore-designs**   | The inverse failure of §12: using "facts beat the model" as cover to skip designs entirely or diverge silently. §12's obligation on divergence is *revise the doc + log the why* (a D-entry), so the map stays honest. Abandoning the design without recording the reconciliation just makes the next implementer re-derive the same collision.                                                                                                                                                                                  |
| **Speak-up-as-licence-to-relitigate**        | §13 obliges raising a *specific* divergence or a *concrete* better option; it is NOT licence to reopen settled decisions on generic preference, or to stall forward motion on endless what-ifs. That inverts §4 (reversible decisions ship at ~70%; Type-2-with-fast-iteration is the default). The trigger is new information, not a mood; agreement is a strong prior that reopens only with a reason. Bring the observation, not the unease.                                                                            |
| **None-of-your-business-as-silent-failure**  | `development-principles.md §2` says the mechanism doesn't question the *why* and the policy doesn't know the *how* — but the *one* sanctioned cross-boundary signal is a typed refusal carrying its reason ("impossible, and here's why"). Using "none of your business" to justify a *silent* failure, an unexplained error, or a swallowed diagnostic inverts the principle and violates §9 (surface failures honestly). Mutual ignorance of *reasons* is not licence to withhold the *refusal*. The complaint is mandatory and must be diagnostic. |

The pattern: every entry in this list is a true principle's failure mode at the wrong threshold. Future-yoloAI should re-evaluate only if the project's scale or scope materially changes (e.g., yoloAI gets a maintainer team, goes 1.0, takes on paying customers, or pivots away from the CLI-tool model).

---

## Closing note

The general principles parallel the development, testing, and security principles in shape: cost-vs-benefit + scope-discipline framing, explicit threshold per principle, worked examples cross-referencing prior decisions, and the consistent posture of preferring small intentional decisions over large unconsidered ones.

The three specialised docs (`development-principles.md`, `testing-principles.md`, `security-principles.md`) each apply general principles to a specific surface — code structure, testing practice, sandbox containment. The general doc names the abstract pattern; the specialised docs ground it in their surface.

Future principles land here as design work surfaces new cross-cutting patterns that don't fit cleanly into any of the three specialised surfaces.
