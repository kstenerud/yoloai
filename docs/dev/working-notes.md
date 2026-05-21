ABOUTME: Append-only decision log for yoloAI. D-numbered entries record the
ABOUTME: choices that shape principles, standards, and design. Retroactive
ABOUTME: entries (D1–D25 below) reconstruct major past decisions from the
ABOUTME: commit history and design docs; subsequent entries are written at
ABOUTME: the point of decision.

# Working notes

Append-only decision log. Each entry is a proto-ADR: what was decided, what was rejected, why, and the consequences. Principles (`principles/`) cite these by D-number; standards (`standards/`) cite them when a rule was forced by a specific decision.

## Conventions

- One D-number per meaningful decision. Trivial choices (rename a flag, fix a typo) don't get a D-number.
- Each entry includes: **Date**, **Decision**, **Rejected**, **Why**, **Consequences**, **Composition** (which earlier D-entries this builds on), and where useful an **Expiration trigger** (the condition under which we'd revisit it).
- Retroactive entries are marked **(retroactive)** at the start of the decision line. They are reconstructed from commit history and design docs — the consequences are observed, but the original reasoning may be partial.

---

## D1 — Implementation language: Go (not Python)

**Date:** 2026-02-22. **Status:** Accepted. **(retroactive)**

**Decision.** yoloAI is implemented in Go. The original prototype was Python; this commit switched the entire design to Go (commit `3595b42`).

**Rejected.**
- *Python* — original choice; rejected for distribution reasons (interpreter + venv burden on users, slow startup, no static binary).
- *Rust* — considered for sandbox-tool fit; rejected because the ecosystem for Docker / container runtimes / VM tooling is weaker than Go's, and the build-time cost did not pay for itself at v1.

**Why.** yoloAI is a CLI that users install once and run repeatedly against ephemeral sandboxes. The acceptance test for distribution is "download a binary, run it, no other steps." Go satisfies this. Python doesn't.

**Consequences.**
- Single static binary, no runtime deps beyond Docker (or Tart / Seatbelt / Podman / containerd).
- `go install`, GitHub releases, and Homebrew all work without packaging gymnastics.
- We pay Go's costs: nominal type system, generics-late, no sum types — accepted.

**Composition.** Establishes the baseline that `principles/general-principles.md §Boring + portable distribution` builds on.

---

## D2 — Critique cycle as design discipline

**Date:** 2026-02-22 (rounds 1–12 between 2026-02-22 and 2026-02-24). **Status:** Accepted, ongoing. **(retroactive)**

**Decision.** Before implementation begins on a feature, a critique pass runs against the design docs in `docs/design/` and the research files in `docs/dev/research/`. The pass identifies factual errors, internal contradictions, and unstated assumptions. Findings are applied; `CRITIQUE.md` is emptied for the next round.

**Rejected.**
- *Ship designs as-written and fix at implementation time* — rejected because design errors compound, and finding them after code exists is more expensive than finding them at the design layer.
- *Treat critique as one-shot* — rejected; we ran twelve rounds against the v1 design before any code shipped and found new findings each round.

**Why.** AI-assisted design generates plausible-sounding but unverified claims at scale. The critique cycle is the explicit factual-accuracy gate. The cost is cheap (an hour per round); the damage prevented (architectural drift, security claims that don't hold, vendor-feature claims that aren't true) is structural.

**Consequences.**
- `docs/dev/CRITIQUE.md` is the rolling document; entries are applied and the file is emptied after each pass.
- The 12-round pre-implementation pass shaped most of the v1 design.
- Captured in project `CLAUDE.md` §Critique Principles.

**Composition.** Built on by D24 (rolling architecture-remediation plans use the same shape).

---

## D3 — Mirrored host paths default mount

**Date:** 2026-02-22. **Status:** Accepted. **(retroactive)**

**Decision.** Directories mounted into the sandbox appear at the same path inside the container as on the host (commit `ce6230074`). The previously-planned `/work/<dirname>/` prefix was dropped. Custom paths still available via `=<path>` override.

**Rejected.**
- *`/work/<dirname>/`* — the canonical container-friendly approach. Rejected because configs (`go.mod` paths, IDE projects, error message paths) carry host-path assumptions that break under remapping.
- *Container-only paths with translation layer* — rejected as complexity for no user benefit.

**Why.** Principle of least astonishment: an agent reading an error trace inside the sandbox should see the same path the user sees outside. Dangerous-directory detection (D11 / `docs/design/security.md`) prevents the safety risk that the `/work` prefix was originally meant to guard against.

**Consequences.**
- Container paths == host paths by default. Symlinks, error messages, and configs work without translation.
- `/yoloai/` reserved for internals so the namespace can't collide.
- Path consistency outweighed the minor safety benefit of `/work`.

**Composition.** D11 covers the safety guard this relies on; D3 is one of the worked examples in `principles/general-principles.md §Principle of least astonishment`.

---

## D4 — Mount mode taxonomy: `:copy` / `:overlay` / `:rw` / `:ro`

**Date:** 2026-02-22 (overlay added 2026-03-02). **Status:** Accepted. **(retroactive)**

**Decision.** Each directory mounted into a sandbox has an explicit mode:

- `:copy` (default for workdir) — full directory copy on the host, diff/apply workflow.
- `:overlay` — overlayfs upper layer, instant setup, requires `CAP_SYS_ADMIN`.
- `:rw` — live bind-mount, no protection (writes hit originals).
- `:ro` (default for aux dirs) — read-only bind-mount.

**Rejected.**
- *Always-overlay* — rejected because `CAP_SYS_ADMIN` is broad and not every user wants to grant it.
- *Always-copy* — rejected because copy of a large monorepo is slow.
- *Implicit upgrade from `:ro` to `:rw` on first write* — rejected as the kind of magic that produces incidents.

**Why.** Copy/diff/apply is yoloAI's differentiator. Mode is explicit so the user can't accidentally turn protection off. Overlay is an explicit opt-in because of its capability cost.

**Consequences.**
- Originals are protected by default. Granting write access is an explicit per-directory decision.
- `/docs/design/security.md` documents the capability tradeoffs.
- Workdir defaults to `:copy` because that's the safe path; `:rw` must be typed.

**Composition.** Foundational for `principles/general-principles.md §Safe defaults` and `principles/security-principles.md §Least privilege by mode`.

---

## D5 — Critique principles in `CLAUDE.md`

**Date:** 2026-02-22 (commit `1eb29ff`). **Status:** Accepted. **(retroactive)**

**Decision.** Project `CLAUDE.md` carries an explicit critique-principles section: research must be verified, focus on what affects the design, separate facts from tradeoffs, platform-specific claims need platform-specific verification, security claims need the highest scrutiny.

**Rejected.**
- *Trust agent output* — rejected; agents hallucinate, especially on numerical claims and competitor feature lists.

**Why.** Defends against the failure mode where plausibility passes for verification.

**Consequences.** Every research file under `docs/dev/research/` is expected to cite primary sources. Star counts, feature claims, and security assertions are verifiable. See `docs/dev/CRITIQUE.md` workflow.

**Composition.** Cited by `principles/general-principles.md §Factual accuracy`.

---

## D6 — Symlink resolution before safety checks

**Date:** 2026-02-23 (commit `67826e0`). **Status:** Accepted. **(retroactive)**

**Decision.** Path safety checks (dangerous-directory refusal, mount-point validation) operate on resolved (`filepath.EvalSymlinks`) paths, not on the path as typed.

**Rejected.**
- *Check paths as-typed* — rejected because `~/safe-link` could be a symlink to `/etc`, and a "safe" check on the link would pass while the actual mount points at `/etc`.

**Why.** The mount system call follows symlinks. Safety checks that don't are theatre.

**Consequences.** All path inputs go through resolution before validation. Documented in `docs/design/security.md` and enforced in code review.

**Composition.** Worked example in `principles/development-principles.md §Validate the real thing, not the surface`.

---

## D7 — Pluggable `runtime.Runtime` interface

**Date:** 2026-02-26 (commit `a3df31b`). **Status:** Accepted. **(retroactive)**

**Decision.** The Docker-specific sandbox implementation was extracted behind a `runtime.Runtime` interface in `internal/runtime/`. Subsequent backends (Tart 2026-02-26, Seatbelt 2026-02-27, Podman 2026-03-15, containerd 2026-03-18) implement the same interface. No backend-specific types leak outside their package.

**Rejected.**
- *Docker forever* — rejected once Tart (macOS VM) became a desired backend. macOS users wanted stronger isolation than Docker Desktop gives; a hardcoded Docker call site couldn't accommodate.
- *Per-backend CLI binaries* — rejected as a UX failure (users would have to remember which binary).
- *Runtime selection at compile time* — rejected; users move between machines with different capabilities.

**Why.** The diff/apply workflow is backend-agnostic. The container is interchangeable infrastructure. Forcing Docker would have foreclosed the macOS VM path and the containerd / rootless-Podman paths that followed.

**Consequences.**
- Five backends ship today (Docker, Tart, Seatbelt, Podman, containerd).
- `newRuntime()` in `internal/cli/helpers.go` is the dispatch point.
- W11 of the 2026-05 architecture remediation (commit `1f4457c`) registers `(factory, descriptor)` tuples in a registry so adding a backend is purely additive.
- Backend-name leaks (W10) and error-text matches (W8) are explicitly flagged as anti-patterns.

**Composition.** Foundational for `principles/development-principles.md §Boundary discipline`.

---

## D8 — Ecosystem-first design principles

**Date:** 2026-02-25 (commit `b047728`). **Status:** Accepted. **(retroactive)**

**Decision.** Project `CLAUDE.md` carries two explicit ecosystem-first principles:

- **Don't reinvent the wheel.** Before designing a feature, check whether git, docker, or unix tools already provide a workflow.
- **Ecosystem ergonomics.** The tool should compose naturally with pipes, git, and unix philosophy.

**Rejected.**
- *Build a custom diff engine* — rejected; `git diff` is the diff engine.
- *Build a custom patch format* — rejected; `git format-patch` / `git am` is the patch format (see D9).
- *Build a custom sandbox state format* — rejected; environment.json + meta.json are JSON, queryable by `jq`.

**Why.** Innovation tokens are scarce (`principles/general-principles.md §Innovation tokens`). Spend them on copy/diff/apply (the differentiator). Borrow everywhere else.

**Consequences.** Most yoloAI features are thin wrappers around `git`, `docker`, `iptables`, `tmux`, `overlayfs`. The composition is the design.

**Composition.** Foundational for `principles/general-principles.md §Don't reinvent the wheel`.

---

## D9 — Commit-preserving apply via `format-patch` / `am`

**Date:** 2026-02-25 (commits `5ca1003`, `29895db`). **Status:** Accepted. **(retroactive)**

**Decision.** `yoloai apply` uses `git format-patch <baseline>..HEAD` inside the sandbox + `git am` on the host. Individual sandbox commits are preserved on the host. A squashed-diff approach was prototyped and rejected.

**Rejected.**
- *Squash to a single diff and apply* — rejected because the agent's commit history is informative (intent, what-was-tried, why). Squashing throws that away.
- *`git push` from sandbox to host* — rejected as backend-dependent (only works when host is reachable; doesn't compose with overlay mode).

**Why.** Respect git workflow. The user's review surface is "what did the agent do," not "what's the final state."

**Consequences.** Per-commit review via `yoloai diff`. Selective apply via commit range. Tag transfer added 2026-03-16 (commit `2670029`) for the same reason.

**Composition.** Worked example for `principles/general-principles.md §Don't reinvent the wheel`.

---

## D10 — Sandbox name validation against path traversal

**Date:** 2026-02-28 (commits `b75e2ec`, `01bfe81`). **Status:** Accepted. **(retroactive)**

**Decision.** All CLI entry points validate sandbox names against a regex (alphanumerics + `-` + `_`) before any filesystem operation. Validation happens at the CLI boundary, not deeper.

**Rejected.**
- *Validate at the filesystem operation* — rejected; by then the value has flowed through five functions and missing one is a bug waiting to happen.
- *Allow `/` in names with sanitization* — rejected; sanitization has gaps, refusal does not.

**Why.** Names become directory components under `~/.yoloai/sandboxes/`. A name like `../etc` is a path traversal. Refusing is cheaper than sanitizing.

**Consequences.** Foundation for the validate-at-the-boundary discipline in `principles/development-principles.md`.

---

## D11 — Network isolation: iptables + ipset, no proxy sidecar

**Date:** 2026-03-01 (commit `ed19f9d`). **Status:** Accepted. **(retroactive)**

**Decision.** `--network-isolated` uses iptables + ipset inside the sandbox container. Default-deny with an allowlist resolved at sandbox start. No proxy sidecar.

**Rejected.**
- *Custom Go proxy sidecar* — originally planned (commit `5e5cca3`, 2026-02-23). Rejected after the iptables approach was prototyped and proved to cover the primary threat model at a small fraction of the operational complexity.
- *No network isolation* — rejected as a v1 gap.

**Why.** Anthropic's own Claude Code devcontainer uses iptables + ipset. Trail of Bits' devcontainer uses iptables + ipset. We're not innovating on network isolation; we're following the consensus.

**Consequences.** Known limitations (DNS UDP must be open; domain fronting theoretically possible on CDNs) are documented and shared with the consensus implementations.

**Composition.** Worked example for `principles/general-principles.md §Don't reinvent the wheel` and `principles/security-principles.md §Threat model is bounded`. The 2026-05 redesign (commit `561993e`) moves enforcement to host netns but keeps the iptables-based shape.

---

## D12 — Base-as-profile restructure

**Date:** 2026-03-01 (commits `c1fadd5`, `1eaf402`). **Status:** Accepted. **(retroactive)**

**Decision.** `~/.yoloai/profiles/<name>/` is the canonical customization mechanism. The base profile lives at `~/.yoloai/profiles/base/` and is auto-created. Custom user setups are *profiles*, not a separate plugin system.

**Rejected.**
- *Separate "config" and "profile" systems* — rejected; we already had a Dockerfile-per-profile, and adding a plugin layer would have created two ways to do the same thing.
- *Templating layer over Dockerfile* — rejected because users already know Dockerfile syntax.

**Why.** Profiles ARE the customization mechanism. Multi-profile inheritance was the right shape because users had multiple project types.

**Consequences.** Two config files split (D13): global config + profile config. `IsGlobalKey()` routes commands to the right file.

**Composition.** Cited by `principles/general-principles.md §Don't reinvent the wheel`.

---

## D13 — Two config files: global + profile

**Date:** 2026-03-01 (commits `4fb6a0a`, `89dd8e8`). **Status:** Accepted. **(retroactive)**

**Decision.** Two YAML config files:

- `~/.yoloai/config.yaml` — global user preferences (tmux_conf, model_aliases).
- `~/.yoloai/profiles/<name>/config.yaml` — profile-overridable defaults (agent, model, backend, env, etc.).

Operational state (`setup_complete`) lives in `~/.yoloai/state.yaml`.

**Rejected.**
- *Single mega-config* — rejected because some keys are user-scoped (model aliases) and others are profile-scoped (which agent to use for *this* project). Conflating them produced bad UX.
- *Per-key config file* — rejected as fragmentation.

**Why.** Conceptual separation of "who I am" (global) from "what I'm working on right now" (profile).

**Consequences.** `yoloai config set/get` routes via `IsGlobalKey()`. `yoloai profile info --diff` shows parent-relative changes.

**Composition.** Cited by `principles/development-principles.md §Boundary discipline`.

---

## D14 — Pluggable idle detection

**Date:** 2026-03-08 (commit `dbec36f`). **Status:** Accepted. **(retroactive)**

**Decision.** Per-agent idle detection. Each agent specifies an `IdleSupport` strategy (hook-based for Claude Code, screen-stabilization for others). A Python `status-monitor` runs inside the sandbox writing `agent-status.json`.

**Rejected.**
- *Tmux `window_bell_flag` polling* — tried, broken (`pane_last_activity` doesn't update for TUI agents).
- *Fixed-delay polling* — tried, flapped between active/idle.
- *Single global detector* — rejected because agents differ structurally (hooks-based vs. screen-based).

**Why.** No single signal works across agents. The detector must adapt; the detector strategy is part of the agent definition.

**Consequences.** `runtime/monitor/` Python helpers, typed pytest tests (`runtime/monitor/tests/`), and the W3/W4 architecture-remediation work that made the Python surface testable.

**Composition.** Worked example for `principles/development-principles.md §Iterate when the first approach doesn't work`. Cites `docs/dev/research/idle-detection.md` for the trail of rejected approaches.

---

## D15 — Default-deny credential access in Seatbelt

**Date:** 2026-03-09 (commit `0ee3a1b`). **Status:** Accepted. **(retroactive)**

**Decision.** The Seatbelt (macOS sandbox-exec) backend operates default-deny on the host filesystem and the host environment. Only an explicit allowlist is granted: safe environment variables (`PATH`, `HOME`, `USER`, locale), and selected paths (`~/.local/`, `~/.gitconfig`, `~/.config/git/`).

**Rejected.**
- *Pass full environment + restrict only known dangerous paths* — rejected because allowlists are the right shape for credentials; denylists miss new credential locations as they appear.

**Why.** A blocklist of credential locations is a moving target. An allowlist of necessary access is enumerable.

**Consequences.** `docs/design/security.md` §Seatbelt Backend Security documents the allowlist. Users opting in to credential access do so via config `env:` and `mounts:`.

**Composition.** Worked example for `principles/security-principles.md §Default-deny over default-allow`.

---

## D16 — Remove all legacy backwards-compat shims

**Date:** 2026-03-10 (commit `be22f6a`). **Status:** Accepted. **(retroactive)**

**Decision.** Pre-1.0 yoloAI tracks breaking changes in `docs/BREAKING-CHANGES.md` and removes legacy shims promptly. The `runtime-config.json` fallback for the older `config.json` name was added in `fdfe0c3` and removed seven minutes later in `be22f6a`.

**Rejected.**
- *Keep legacy compat indefinitely* — rejected; we're in public beta and breaking changes are allowed with migration notes.
- *Never break compat* — rejected as the path to permanent vestigial code.

**Why.** Public-beta scope is explicit (`CLAUDE.md` §Project Status). Removing legacy shims keeps the code surface small. Migration notes preserve user trust.

**Consequences.** `docs/BREAKING-CHANGES.md` is the contract. Each entry: previous behavior, new behavior, rationale, migration steps.

**Composition.** Worked example for `principles/development-principles.md §No half-finished implementations`.

---

## D17 — `--security` flag for OCI runtime selection (gVisor, Kata)

**Date:** 2026-03-17 (commit `87956ac`). **Status:** Accepted, scoped. **(retroactive)**

**Decision.** Defense-in-depth options (gVisor user-space kernel; Kata Containers VM-isolated) ship as `--isolation` values (`container-enhanced`, `vm`, `vm-enhanced` — renamed 2026-03-18 in commit `098672c`). Standard Docker is the default; harder isolation is opt-in.

**Rejected.**
- *Standard Docker only* — rejected because users with stronger threat models exist.
- *gVisor by default* — rejected; gVisor has permission and platform quirks (relaxed bind-mount permissions per `docs/design/security.md`, blocked on macOS due to a known Claude Code bug).

**Why.** Different users have different threat models. The default is the one most users want; the flag exists for the rest.

**Consequences.** `--isolation` is a first-class flag. Documented in `docs/GUIDE.md`. The 80/20 UX model (commit `7ec549d`) — isolation and backend are separable concepts.

**Composition.** Worked example for `principles/security-principles.md §Defense in depth as opt-in layers`.

---

## D18 — Environment archetypes: devcontainer / yoloai.yaml / archetype

**Date:** 2026-05-19 (commits `16e124e`, `f7de765`). **Status:** Accepted. **(retroactive)**

**Decision.** Three archetypes for environment definitions:

- *devcontainer* — reads existing `.devcontainer/devcontainer.json` and translates to a yoloAI sandbox.
- *yoloai.yaml* — yoloAI-native config in the project root.
- *archetype* — built-in templates (e.g., `go`, `node`).

**Rejected.**
- *yoloai.yaml only* — rejected because the devcontainer ecosystem is established; supporting it costs little and buys instant onboarding for projects that already have one.
- *devcontainer only* — rejected because yoloAI has features devcontainer doesn't model (sandbox isolation, copy mode).

**Why.** Don't reinvent — work with files that already exist. (Composes with D8.)

**Consequences.** Archetype parsers in `internal/sandbox/archetype/`. Lifecycle commands execute inside the sandbox.

**Composition.** Worked example for `principles/general-principles.md §Don't reinvent the wheel`.

---

## D19 — Architecture remediation cycles (W-numbered work)

**Date:** 2026-05-20 (architecture audit `868a5b0`, plan revisions through `7932c75`, implementation commits W1a–W14). **Status:** Accepted, ongoing. **(retroactive)**

**Decision.** Periodic architecture audits produce a numbered remediation plan (`W1`, `W2`, …). Each work item is a discrete commit. The plan tracks status (`docs/dev/architecture-audit-2026-05.md`) and a memory entry tracks completion across sessions.

**Rejected.**
- *Refactor opportunistically* — rejected because opportunistic refactors don't compose; the W-plan ensures the bundle lands as a coherent shape.
- *One-shot rewrite* — rejected as the kind of thing that never lands.

**Why.** Drift accumulates as features add. Periodic audits + a plan + numbered work items keep cleanup tractable.

**Consequences.** Phases 1–6, W11 (runtime registry), W12 (sandbox subpackage carving) shipped 2026-05-20. W1b (scheduled) remains.

**Composition.** Cited by `principles/development-principles.md §Plan-then-execute on cleanup`.

---

## D20 — `make check` enforcement via Claude Code Stop hook

**Date:** 2026-05-20 (commit `bf5c79e`). **Status:** Accepted. **(retroactive)**

**Decision.** A Claude Code Stop hook runs `make check` before any AI-assisted edit can complete. If `make check` fails, the hook blocks completion and feeds the output back to the agent. The hook scripts are checked into the repo (`.claude/hooks/post-edit.sh`, `.claude/hooks/on-stop.sh`).

**Rejected.**
- *Trust the agent to run `make check`* — rejected because agents skip it under time pressure.
- *Pre-commit hook only* — rejected because `make check` failures should block earlier; pre-commit catches them at the worst time.

**Why.** Code quality gates work only when they can't be skipped. Putting the gate in the agent's stop sequence makes it structural.

**Consequences.** Every clone gets the enforcement automatically. CI is the second line of defense.

**Composition.** Worked example for `principles/development-principles.md §Code quality gate`. Cited from project `CLAUDE.md` §Code Quality Gate.

---

## D21 — Two-stage smoke sentinel + disk pre-flight

**Date:** 2026-05-21 (commit `0d8d650`). **Status:** Accepted.

**Decision.** Smoke tests run a two-stage sentinel (early signal before the agent boots; final signal after). `yoloai system disk` and `system prune --cache` were added the same day to make ENOSPC diagnosable.

**Rejected.**
- *One-shot smoke* — rejected after a disk-pressure failure showed up as "agent idle 9s+" with no useful diagnostic.
- *Catch-all error message* — rejected; the specific error is what tells us what to fix.

**Why.** When a smoke test fails, you want to know *what* failed before the long path. Disk pressure is a common cause; the pre-flight surfaces it directly.

**Consequences.** Faster diagnosis on the smoke-failing-on-containerd-vm case (memory entry `project_smoke_disk_pressure.md` records this). Standard pattern: when a failure mode is shared across backends and machines, add the dedicated diagnostic.

**Composition.** Worked example for `principles/development-principles.md §Surface failures honestly` and `principles/general-principles.md §Document the no`.

---

## D22 — Standards and principles docs (this set)

**Date:** 2026-05-21. **Status:** Accepted.

**Decision.** Adopt a `docs/dev/principles/` + `docs/dev/standards/` split, modelled on the foley project but adapted for yoloAI's single-author OSS CLI scope. Four principle docs (general, development, testing, security-sandbox); standards file per language / surface. `docs/dev/working-notes.md` (this file) holds D-numbered decisions.

**Rejected.**
- *Keep `CODING-STANDARD.md` and `CLI-STANDARD.md` as the entire surface* — rejected because they answer *how* but not *why*; principles questions ("should this be a feature flag?", "should this validate at this layer?") had no canonical home.
- *Skip working-notes / D-log* — rejected because principles need provenance; without it, a future contributor has no way to judge whether a rule still applies.
- *Include business-principles* — rejected; yoloAI has no customer-facing surface in the foley sense.

**Why.** The codebase has grown past the size where "ask Karl" is the canonical answer. A discoverable principles layer is the right shape now.

**Consequences.**
- `docs/dev/principles/` and `docs/dev/standards/` exist. README in each is the index.
- This working-notes file is the decision log. New decisions land here first.
- Standards moved into `standards/` (Phase 3 of the rollout).

**Composition.** Establishes the scaffolding that every later D-entry cites back to.

---

# Convention reminders

- New decisions append at the bottom. Don't renumber.
- If a decision is superseded, update its **Status** to `Superseded by Dnn` and link forward. Don't delete.
- Retroactive entries are reconstructions; flag them with **(retroactive)** so a reader knows the rationale is inferred from outcomes, not transcribed from the moment of decision.
