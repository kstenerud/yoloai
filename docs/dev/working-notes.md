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
- *Keep `standards/GO.md` and `standards/CLI.md` as the entire surface* — rejected because they answer *how* but not *why*; principles questions ("should this be a feature flag?", "should this validate at this layer?") had no canonical home.
- *Skip working-notes / D-log* — rejected because principles need provenance; without it, a future contributor has no way to judge whether a rule still applies.
- *Include business-principles* — rejected; yoloAI has no customer-facing surface in the foley sense.

**Why.** The codebase has grown past the size where "ask Karl" is the canonical answer. A discoverable principles layer is the right shape now.

**Consequences.**
- `docs/dev/principles/` and `docs/dev/standards/` exist. README in each is the index.
- This working-notes file is the decision log. New decisions land here first.
- Standards moved into `standards/` (Phase 3 of the rollout).

**Composition.** Establishes the scaffolding that every later D-entry cites back to.

---

## D23 — Tests inject the Layout; env swaps reserved for HOME-reading subprocesses

**Date:** 2026-05-28. **Status:** Accepted.

**Decision.** Unit tests steer yoloAI code via explicit inputs — a `config.Layout` (DataDir, HomeDir, HostUID/GID, Env) passed through `WithLayout`, an injected `io.Reader`/`io.Writer` — not by mutating global process state (`t.Setenv("HOME", …)`, swapping `os.Stdin`). Codified as `testing-principles.md §10`. The §12 no-ambient-configuration work made this possible: library code now reads the Layout it is handed, so a `HOME` swap to steer a yoloAI path is manipulating a global the code no longer reads.

**Rejected.**
- *Keep the `t.Setenv("HOME")` scaffolding* — rejected: ~130 such swaps are vestigial after §12 (the code reads the explicit Layout), they couple tests to process-global state, and `t.Setenv` silently forbids `t.Parallel`.
- *Blanket-remove every `HOME` swap* — rejected: ~85 are load-bearing. Tests that spawn real `git` set `HOME` to isolate the subprocess from the developer's `~/.gitconfig`; removing those would let dev config leak in and flake the suite.

**Why.** The seam (Layout + injected readers) makes a test's inputs visible and its failures isolable (§5), removes a class of cross-test interference, and unlocks `t.Parallel`. The git-isolation case is the genuine exception — there the swap shields a HOME-reading *subprocess*, not yoloAI code.

**Consequences.**
- `testing-principles.md §10` + over-generalisations row added.
- 82 vestigial `HOME` swaps removed across 10 files (per-file verified). git/cliutil/e2e/lock-helper swaps retained as load-bearing.
- **`t.Parallel` audit — rejected.** The unit suite was ~2.5s except one test (`tart.TestStopVM_EscalatesToSIGKILL`) at 15.1s. `t.Parallel` overlaps multiple tests; it can't speed up a single slow one, and overlapping the already-fast packages saves <2s against real latent-shared-state flakiness risk (global registries, injectable package vars, remaining `t.Setenv` API-key tests). The real lever was that one test's two hardcoded escalation timeouts — converted `tartGracefulStopTimeout`/`tartSigtermWait` from `const` to `var` and shrunk them to 200ms in the test (the test validates escalation *logic*, not the production durations). Suite wall time 18s → ~5.6s.

**Composition.** Applies `development-principles.md §12` to the test surface; extends D22's testing principles.

---

## D24 — Create refuses (typed), never prompts; ambient backend selection stays at the boundary

**Date:** 2026-05-28. **Status:** Accepted. **Context:** discovered while implementing the F1+F3+F4 public creation surface (`f1-f3-public-surface.md`).

**Decision (two coupled findings).**

1. **`Create` is prompt-free; the dirty/requires gates become typed refusals.** The internal manager *did* prompt (two `Confirm` calls in `checkDirtyRepos`/`checkRequires`, gated by `CreateOptions.Yes`). `Yes` conflated "non-interactive" with "proceed despite the risk" — a headless embedder setting `Yes=true` to silence prompts silently disabled the dirty-workdir guard (data-loss footgun). Fix: the library **refuses by default** with `*DirtyWorkdirError{Paths}` / `*UnverifiedRequiresError{Requires}`; the caller overrides via acks named for the *specific* refusal — `CreateOptions.AllowDirtyWorkdir`, `DirSpec.AllowDirty` (renames `DirSpec.Force`'s dirty-skip role), `CreateOptions.AllowUnverifiedRequires`. The CLI `new` catches→warns→prompts→retries with the ack. Same shape as `Destroy`→`*ActiveWorkError`.
2. **F4 beats F21 at the empty-`Backend` line.** F4 (`Backend=="" → *UsageError`) and F21 (`Backend==""` routes via `Options.Isolation`/`OS`) are the same `NewWithOptions` branch. F4 wins: require `Backend`, **delete `Options.Isolation`/`OS`** (no in-tree caller set them; the CLI already resolves the backend at its boundary and passes a concrete one). A public `yoloai.SelectBackend(ctx, preferred, isolation, os)` preserves auto-detect for embedders *explicitly*.

**Rejected.**
- *Keep `Yes` as an interactive toggle* — rejected: defensible (caller-controlled, paired with `Input`), but leaves the conflation and the footgun. The owner chose the prompt-free typed-refusal model.
- *Soften F4 / drop F4* — rejected: backend selection is ambient (probes installed daemons); §12 says resolve ambient state once at the outermost boundary, not implicitly inside library construction.

**Why.** A forgetful caller now gets a *typed error* (safe), not a silent clobber, and must name which risk it accepts. Library stays prompt-free and §12-clean (no ambient backend default). Revises `f1-f3-public-surface.md` decisions 1 and 5.

**Composition.** Extends the api_surface "library never prompts; confirmation is the caller's concern" stance (cf. `Destroy` typed refusal) and `development-principles.md §12`.

---

## D25 — A design is a hypothesis; aspirational until verified against reality

**Date:** 2026-05-28. **Status:** Accepted. **Context:** surfaced during the F2 re-rooting when api_surface.go's designed `RestartOptions` isolation-transition policy turned out to have no internal basis; the owner reframed the design doc as aspiration, not spec.

**Decision.** A design — a design doc, `api_surface.go`, a spec — is a *model*: our best-effort map of reality, not a contract. Because no one is omniscient, parts of any model break down when implementation surfaces facts the model didn't anticipate. So a design is **provisional and falsifiable until it has been implemented and verified to work against the real internal capability.** When facts contradict the model, the facts win: revise the model (update the doc, or mark it superseded) and record *why* — don't bend the implementation to preserve the aspiration, and don't silently abandon the model either. This mirrors the scientific method: design = hypothesis, implementation = experiment, divergence = analysis, the updated doc = conclusion. Codified as `general-principles.md §12`.

**Rejected.**
- *Design-doc-as-contract* (implement api_surface verbatim) — rejected: it builds speculative behaviour with no basis (e.g. the Restart isolation-transition policy, `Status()` with no cheap internal path) — wasted work and dead API.
- *Facts-win-as-licence-to-ignore-designs* — rejected: divergence carries an obligation to revise the doc + log the why, so the map stays honest and the next implementer doesn't re-derive the same collision.

**Why.** The `//go:build never` tag on `api_surface.go` is the structural tell — it is literally uncompiled, i.e. unverified. Treating it as binding inverts the relationship: the experiment validates the hypothesis, not the reverse. Mirror image of `general-principles.md §7` (design must be backed by *research*); this adds that design must also be backed by *implementation* before it is load-bearing.

**Consequences.**
- `general-principles.md` gains §12 (eleven → twelve principles); README index + ABOUTME updated; two over-generalisation rows added (design-as-contract / facts-as-licence-to-ignore).
- The F2 conclusions (deferred `Status()` + Restart policy, `NeedsConfirmation`→`HasActiveWork`) are recorded as worked examples; api_surface.go carries an inline divergence note.

**Composition.** Extends §7 (factual accuracy / verify before you cite) to the design↔implementation axis; applied in the F1/F2/F4 public-API work (D24).

---

---

## D26 — `signal_secrets_consumed` must precede `get_working_dir` in sandbox-setup.py

**Date:** 2026-05-28

**Context.** `yoloai new` on the Tart backend was silently deadlocking: the host
blocked in `waitForSecretsConsumed(180 s)` inside `buildAndStart()`, which
prevented `launchContainer()` from returning; `executeVMWorkDirSetup()` (the
rsync that creates the VM-local copy dir) only runs *after* `launchContainer()`
returns; the in-VM `get_working_dir()` polled for that dir for 120 s; and
`signal_secrets_consumed()` came *after* `get_working_dir()`. Neither side
could proceed. At 30 s timeout the host accidentally escaped by giving up; at
180 s the smoke test's 120 s command timeout fired first, producing a complete
failure.

**Decision.** Move `read_secrets()` + `signal_secrets_consumed()` to run
*before* `get_working_dir()` in `sandbox-setup.py::main()`. Secrets are
already on the VirtioFS share by the time the setup script runs
(`copySecretsToSandbox()` is called during `Create()`, before `Start()`). The
tmux session does not exist yet when `read_secrets` is called at the new
position, so `tmux set-environment` is a no-op; the agent receives credentials
via the explicit `export NAME='value'; exec …` prefix injected by
`launch_agent()` instead.

**Recorded in.** `backend-idiosyncrasies.md §Tart: signal_secrets_consumed
must run before get_working_dir`.

---

## D26 — Apply replays the commit series by default; --no-commit lands net changes unstaged

**Date:** 2026-05-28. **Status:** Accepted. **Context:** surfaced mid-Step-4 (the apply re-rooting) — the owner caught that the default apply had been built backwards.

**Decision.** The normal apply flow **replays the sandbox's commit series onto the host workdir**, preserving each commit's message/author (`git format-patch` → `git am`) — `Workdir().Apply(ApplyOptions{Mode: ApplyModeCommits})`. `ApplyOptions{Mode: ApplyModeNoCommit}` (CLI `--no-commit`, formerly `--squash`) instead applies the **net diff unstaged** into the working tree — equivalent to "replay the series, then `git reset <baseline>`" — collapsing the commits to their net effect and leaving them for the user to commit. `--no-commit` is also the only mode possible against a **non-git** host target (you can't `git am` into a non-repo).

**Mechanism/policy split.** The library does *not* silently switch modes. `Workdir().Apply` with the default (series) against a non-git target returns a typed `*UsageError` — it complies with `ApplyOptions` or complains; it never reinterprets intent. The **CLI owns the policy**: it checks the target (`workspace.IsGitRepo`) and chooses `NoCommit` for non-git itself (with a notice). An embedder gets the same deal — explicit `NoCommit`, or a typed refusal it can handle. `ApplyResult` gains `Commits []AppliedCommit{Subject, SourceSHA, HostSHA}` so the CLI's tag transfer + summary work off the real mapping; tags stay CLI-side.

**Rejected.**
- *Squash into a single commit (with a generated message)* — rejected: **no one asked for it**, and it forces a message-synthesis decision. The net-diff-unstaged behavior already covers "I'll consolidate and commit myself."
- *Squash/net-diff as the default* (the 4a/4b shape) — rejected: it inverts the normal flow. The default must mirror how the agent built the work (a commit series); flattening is the special case.
- *Library auto-falls-back to net-diff on a non-git target* — rejected: the library must not silently change behavior. It does what `ApplyOptions` says or refuses with a typed error; the non-git→`NoCommit` decision is the CLI's policy call.
- *A default apply mode* (e.g. a `NoCommit bool` defaulting to series) — rejected: the choice is consequential and mutually exclusive, and a movable default silently changed behavior when it flipped — 4c-i1 broke `apply_squash` exactly this way. `ApplyOptions.Mode` is **required**; the zero value is a `*UsageError`. The CLI (policy) picks the mode for the user; the library never assumes one. (`development-principles.md §4` — empty isn't a free default.)
- *Name it `--squash`* — rejected: it implies a squash *commit* is created; none is. `--no-commit` names the actual behavior (no commits created; net changes land in the workdir) and contrasts cleanly with the commit-preserving default.

**Why.** The product's core loop is: work + commit inside the sandbox, repeat, then land that history on the host. Mirroring the commits is the expected outcome; collapsing them is occasionally wanted (review-before-commit, or non-git targets).

**Consequences.**
- `Workdir().Apply(ApplyOptions{})` default flips from net-diff (4a/4b) to series replay; `--squash` → `--no-commit` (Type-1.5 CLI break, tracked in BREAKING-CHANGES).
- `ApplyResult` reshaped (`Commits []AppliedCommit`; drop the always-zero `FilesChanged`).
- Step-4 phasing re-centered on the series replay as the core (`apply_format_patch.go` fold), with `NoCommit` / selective `Refs` / `ExportDir` as options.

**Composition.** Applies `general-principles.md §1` (YAGNI — no squash-commit feature) and §12 (the built design didn't match the real workflow; revise it). Continues the F2 apply re-rooting (D-less; tracked in `plans/f2-f1f3-implementation.md`).

---

## D27 — Boundary discipline restated: thin policy layer, comply-or-complain mechanism

**Date:** 2026-05-28. **Status:** Accepted. **Context:** the "comply-or-complain" framing recurred across the public-API work (D24 library-never-prompts, D26 no-auto-fallback, F4 no-ambient-default); the owner asked whether to name it as its own principle or fold it into the existing boundary-discipline principle.

**Decision.** Restructure `development-principles.md §2` from a one-sided statement (the interface layer is thin) into the full two-sided boundary it always implied, and retitle it **"Boundary discipline — thin policy layer, comply-or-complain mechanism."**
- **Policy layer** (CLI, public-API entry, embedder): decides *what* to do and *how to react* — which operation, whether to prompt, whether to fall back, how to render. Stays thin: parse → call → format.
- **Mechanism layer** (the domain/library): does exactly what it is asked, or **complains** with a typed error. It never silently does a third thing — no prompting, no reinterpreting intent, no mode-switching or fallback, no UX choices. "Can't comply" always surfaces as a typed refusal the caller handles.
- Sharpen the old "should this proceed? lives in domain" bullet: the **rule** lives in the mechanism (it refuses an impermissible op with a typed error); the **policy** lives in the caller (override? prompt? fall back?).

**Rejected.**
- *A separate "comply-or-complain" principle* — rejected: it's the mechanism-side half of the same boundary §2 already governs from the policy side. A separate principle fragments one boundary into two.

**Why.** §2 only stated the policy side ("interface layer is thin"); the mechanism's behavioral contract was implicit and kept getting re-derived per feature. Naming it ("comply-or-complain") makes the contract citable and memorable, and unifies D24 / D26 / F4 under one rule.

**Consequences.**
- §2 retitled + restructured (mechanism contract added; point 3 sharpened; worked examples gain the typed refusals — `*DirtyWorkdirError`, `*ActiveWorkError`, the non-git apply `*UsageError`, F4's required Backend); README index line updated.
- Cites D24 (library never prompts), D26 (no auto-fallback; CLI owns policy), F4 (no ambient backend default).

**Composition.** Refines §2 (D7, pluggable runtime / boundary discipline); generalizes D24 and D26; sibling to `general-principles.md §12` (both sharpen how the library behaves at its boundary).

---

## D28 — "uncommitted" is the canonical term for the agent's uncommitted edits; "WIP" is banned

**Date:** 2026-05-28. **Status:** Accepted. **Context:** the WIP-vs-uncommitted naming kept resurfacing across sessions. The decision (uncommitted) had been made earlier and recorded in `plans/f2-subhandle-mapping.md` ("WIP = include uncommitted; an option, not a method"), but the code had drifted back to "WIP" (`IncludeWIP`, `--include-wip`, `WIPApplied`, `wip_applied`, `wip.diff`, `GenerateWIPDiff`, …), so it kept getting re-litigated. The owner asked to settle it once and for all.

**Decision.** The agent's uncommitted edits (changes beyond the last commit) are called **"uncommitted"** everywhere — never "WIP" or "work-in-progress". Applies to Go identifiers (`IncludeUncommitted`, `UncommittedApplied`, `GenerateUncommittedDiff`), the CLI flag (**`--include-wip` → `--include-uncommitted`**), JSON keys (`wip_applied` → `uncommitted_applied`), the exported diff filename (`wip.diff` → `uncommitted.diff`), slog fields, comments, and docs. Renamed across all `*.go` and the live/forward-looking docs in one sweep.

**Rejected.**
- *Keep "WIP" as a terse synonym* — rejected: dual vocabulary is exactly what caused the drift. One term, enforced.
- *`--uncommitted` (shorter flag)* — rejected in favor of `--include-uncommitted`, which mirrors the Go field `IncludeUncommitted` and reads as "include on top of the commits."

**Why.** "uncommitted" matches git's own vocabulary; "WIP" was informal jargon. A single enforced term stops the recurring re-litigation.

**Consequences.**
- The `wipe`/`Wipe` family (disk/state wiping) is unrelated and untouched. `HasUncommittedChanges` / `has_uncommitted_changes` were already correct and unchanged.
- Append-only history (this log's prior entries, `CRITIQUE.md`, `OPEN_QUESTIONS.md` resolved items, `old/`) left as point-in-time records; the deleted `--no-wip` flag keeps its literal name in the BREAKING-CHANGES history entry.

**Composition.** Sibling to `feedback`-level conventions; cited by the F2 apply/diff work (D26, 4a–4e).

---

## D29 — Export is its own verb (`Workdir().Export`), not an Apply mode

**Date:** 2026-05-28. **Status:** Accepted. **Context:** F2 step 4e folds the CLI's `apply --patches` (and overlay export) into the library. `api_surface.go` had aspired to a third apply mode, `ApplyExport` (+ `ApplyOptions.ExportDir`). The owner was asked to decide between that and a separate verb, and chose a separate verb.

**Decision.** Export is a distinct verb: `Workdir().Export(ctx, ExportOptions{Dir, Refs, Paths, IncludeUncommitted}) (*ExportResult, error)`. It writes patch files and never lands changes or advances the baseline. `Apply` stays purely about *landing* changes (Mode required: Commits | NoCommit). Export resolves mount mode internally (copy → format-patch files + optional `uncommitted.diff`; overlay → upper-layer diffs), mirroring how `Diff`/`Apply` resolve mode. `Dir` required (`*UsageError` if empty); `Refs` on overlay refused (`*UsageError`). Orchestration lives in `patch.Export`.

**Rejected.**
- *Third `ApplyExport` mode (the api_surface aspiration)* — rejected (§12): export doesn't apply, so a mode that "applies" by not applying strains the required-Mode contract (D26), which is about *how to land*, not *whether to land*. The aspiration's own caveats ("DryRun invalid with export", a result struct half-apply/half-export) signaled the misfit. api_surface.go reconciled: `ApplyExport`/`ExportDir`/`ApplyResult.ExportedPath` removed; `Export` verb added.

**Why.** Principle of least astonishment + a clean Apply contract. One verb per intent.

**Consequences.**
- `Client.GenerateFormatPatch` and `Client.GenerateUncommittedDiff` removed (their only callers were the now-folded CLI export helpers; `patch.Export` calls the patch-package functions directly). The CLI's `--patches` is dispatched before the apply paths, which also fixes a latent bug where `apply <refs> --patches` ignored `--patches`.
- Overlay *apply* folding into `Workdir().Apply` is the separate sub-step 4f (was 4e's second half).

**Composition.** Sibling to D26 (required apply Mode) and D27 (comply-or-complain); applies `general-principles.md §12` (aspiration is a hypothesis).

---

## D30 — F18: 3 of 5 methods moved to optional interfaces; DiagHint/TmuxSocket stay core

**Date:** 2026-05-28. **Status:** Accepted. **Context:** F18 (critique) flagged that `runtime.Runtime`'s core interface carried methods some backends implement trivially, and triaged "move all five" (`Logs`, `DiagHint`, `TmuxSocket`, `PrepareAgentCommand`, `GitExec`) to optional interfaces. Before executing, the actual backend impls were read to verify the premise (critique principle: verify; §12: api_surface/critique are hypotheses).

**Decision.** Move **three**: `Logs`→`LogTailer` (default `""`), `PrepareAgentCommand`→`AgentCommandPreparer` (default passthrough), `GitExec`→`GitExecer` (default `hostGitExec`, run git on the host). Keep **`DiagHint` and `TmuxSocket` core**.

**Why the deviation from "move all five."** F18's own bar is "core = every backend implements non-trivially." Verified impls:
- `Logs`: docker/containerd real, tart/seatbelt `""` → 2 trivial → move.
- `PrepareAgentCommand`: docker/containerd passthrough, tart/seatbelt real → 2 trivial → move.
- `GitExec`: docker/containerd/seatbelt run git on host (a shared default), tart translates to VM → 1 special-case → move (Tart implements `GitExecer`; the rest use the default).
- `DiagHint`: docker/containerd/tart/seatbelt ALL return distinct, meaningful hints → universal, no default → **keep core**.
- `TmuxSocket`: all four return a non-empty socket (docker/containerd a shared constant, tart/seatbelt their own) → universal, no sensible default → **keep core**.

Moving DiagHint/TmuxSocket would be pure churn (no backend drops them) and there's no universal default. Owner confirmed "move 3, keep 2" when shown the verification.

**Consequences.**
- `runtime.hostGitExec` returns `*runtime.ExecError` (exit-code-aware) on non-zero exit — the form `sandbox/patch/apply.go` matches via `errors.As` for `git diff --quiet` exit 1. Previously docker/seatbelt returned a plain wrapped `*exec.ExitError`; unifying on `ExecError` is a minor improvement (caller already handled both). The containerd-specific regression test now exercises `GitExecFor`'s default.
- LogTailer: docker, containerd. AgentCommandPreparer: tart, seatbelt. GitExecer: tart only.
- Internal-only change (`internal/runtime`); no public-API / BREAKING-CHANGES impact.

**Composition.** Applies `general-principles.md §12` (verify the hypothesis before acting) and the critique principle "research must be verified." Extends the existing optional-interface idiom (`CopyMountResolver`/`CachePruner`/…).

---

# Convention reminders

- New decisions append at the bottom. Don't renumber.
- If a decision is superseded, update its **Status** to `Superseded by Dnn` and link forward. Don't delete.
- Retroactive entries are reconstructions; flag them with **(retroactive)** so a reader knows the rationale is inferred from outcomes, not transcribed from the moment of decision.
