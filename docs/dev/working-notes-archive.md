ABOUTME: Archived decision log for yoloAI — entries D1–D44, covering work
ABOUTME: completed through the F5 god-package carve + 31-finding layering
ABOUTME: critique (concluded 2026-05-29). Still cited by D-number; the
ABOUTME: citations resolve here. Live log (D45 onward): working-notes.md.

# Working notes — archive (D1–D44)

Historical decision log. Principles (`principles/`) and standards (`standards/`) cite these entries by D-number; the citations resolve here. New decisions are **not** appended to this file — they go in [working-notes.md](working-notes.md). Conventions and entry structure are described there.

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

## D31 — F23: cross-backend enumeration moves to SystemClient (3 of 4; allow was already done)

**Date:** 2026-05-28. **Status:** Accepted. **Context:** F23 (critique) flagged that `ls`, `system doctor`, `system info`, and `sandbox <name> allow` reach the runtime directly via `cliutil.NewRuntime` / `internal/sandbox` instead of through the Client, and triaged "add four SystemClient methods (ListAcrossBackends, Doctor, Info, AllowDomain)." Verified each before executing.

**Decision.** Add `SystemClient.ListAcrossBackends`, `Doctor`, and `Info` (with `Backends`); the CLI command handlers call these instead of enumerating backends themselves. `Info` returns paths + per-backend availability in one call; disk stays the separate (slower) `DiskUsage` method; build metadata (version/commit/date) stays CLI-only. `DoctorOptions{BackendFilter, IsolationFilter}`; `BackendReport` re-exported as a yoloai alias so `Doctor`'s signature stays off the F1 fence.

**Re-scoped from "all four."**
- `sandbox <name> allow/deny/allowed` was **already** migrated to `Sandbox().Network()`. The critique's `AllowDomain` on SystemClient would be wrong — network allow/deny is per-sandbox, so it belongs on the sandbox handle, not a cross-backend admin method. Dropped.
- `system info` had **no** `NewRuntime` leak (it read static `runtime.Descriptors()` + `cliutil.CheckBackend`). Moved it anyway because a consolidated "describe my install" API (`SystemClient.Info`) is genuinely useful for embedders (owner's call), not because it was leaking.

**Consequences.**
- `cliutil.NewRuntime` stays (now used only by `cliutil.CheckBackend` — the availability-probe chokepoint behind `system info` / `system backends` / bugreport — and the backend-scoped `system tart` subtree). Its `.golangci.yml` allowlist + ARCHITECTURE.md:39 updated to say so; the "four commands bypass via NewRuntime" framing is gone.
- `sandbox.ListSandboxesMultiBackend` now has a single caller (`SystemClient.ListAcrossBackends`).

**Composition.** Applies `general-principles.md §12` (verify the hypothesis) and the critique principle "research must be verified"; extends the SystemClient cross-backend idiom (DiskUsage/Prune/Build/Check). Sibling to D30 (same verify-before-execute pattern on F18).

---

## D32 — Self-healing cleanup: classify sandbox dirs by recoverability; promote doctor to top-level

**Date:** 2026-05-28. **Status:** Accepted. **Context:** Broken sandbox dirs, stale lock files, orphaned backend resources, and multi-GB backend caches accumulate over time; an end user has no way to know where any of it lives or how to clean it safely. The design lives in `docs/dev/plans/system-repair-cleanup.md` (phases 0–5).

**Decision.** `SystemClient.Prune` classifies every dir under `sandboxes/` by **recoverability, not brokenness**, and the bulk path only ever *removes* zero-stakes items. The classifier crosses the `store.LoadMeta` failure kind with a meta-independent probe `sandbox.ProbeWorkData` (copy mode: `detectChanges` on `work/<enc>/.git`; overlay mode: non-empty `work/<enc>/upper/` — both host-side, no container):

- meta loads → **known** (untouched);
- work detected (any failure kind) → **refuse + report** (`RefusedDataBearing`; user runs diff/destroy);
- missing meta + no work dir → **delete** (never-init, zero-stakes);
- corrupt / version-too-new meta with no detectable work → **quarantine to trash** (the safe default).

Trash is a lightweight quarantine: `os.Rename` into `~/.yoloai/trash/<name>` (`store.QuarantineSandbox`). **No dedicated restore command** — recover with `mv`. Trash deletion is decoupled from `--cache` and conservative: prune prompts before emptying (it may hold wanted data); `--yes` skips. Lock files are swept only when no live flock holder exists (try-acquire); normal `Destroy` / failed `Create` now remove their own lock file (self-heal on the happy path, not just via prune).

`doctor` is promoted from `yoloai system doctor` to top-level **`yoloai doctor`** and extended into a **pure read + delegate** repair advisory: it runs a dry-run prune + DiskUsage and reports four sections (reclaimable-now → `system prune`; reclaimable-space → `system prune --cache`; unreviewed-work → diff/destroy; trash → mv/prune). doctor never deletes — it only prints the command that does.

**Rejected.**
- *Silently deleting unreadable-meta dirs.* A corrupt `environment.json` doesn't mean the `work/` tree is worthless. Quarantine-by-default trades a little disk for never destroying unrecoverable data.
- *A dedicated `restore`/`untrash` command.* Trash entries are plain dirs; `mv` is the obvious, composable recovery. Building a bespoke command would reinvent the filesystem (violates "don't reinvent the wheel").
- *`doctor --fix`.* Keeping doctor read-only (option a) preserves a clean see/clean/remove verb split: see → `doctor`; clean invisible → `system prune`; remove visible → `destroy`. A `--fix` flag would blur that and put deletion behind a diagnostic.
- *Folding lock removal into prune only.* Lock files would still accumulate between prunes; removing them on Destroy/Create-rollback keeps the steady state clean.

**Consequences.**
- New public surface on `SystemClient`: `PruneResult.{Trashed, RefusedDataBearing, TrashContents}`, `EmptyTrash`; `sandbox.ProbeWorkData`; `store.{RemoveLockFile, SweepStaleLocks, QuarantineSandbox}`; `PruneKind{SandboxDir,LockFile}`; layout `TrashDir`.
- `doctor` moves to its own package `internal/cli/doctorcmd`; removed from `system`'s subcommand set. Breaking change tracked in `docs/BREAKING-CHANGES.md`.
- flock removal-while-held is safe because flock binds to the fd, not the path — documented in `backend-idiosyncrasies.md`.

**Composition.** Builds on the SystemClient cross-backend idiom (D31) and the thin-CLI / library-owns-logic boundary (D27, D-F layering). Applies the design principles "copy/diff/apply protects originals" and "don't reinvent the wheel" (trash = `mv`).

---

## D33 — F8 final phase: Create's progress stream is a per-call `CreateOptions.Output`, not `m.output`

**Date:** 2026-05-29. **Status:** Accepted. **Context:** F8 (decouple the Manager from a long-lived `m.output` writer) had already migrated Destroy/Start/Reset to structured `Notices` and moved the post-create summary into the CLI (`printCreateSummary`). The Create pipeline still streamed ~12 advisories and the profile-image **build log** straight to `m.output` — and a build log is a live stream, so it can't be a returned `Notice` like the lifecycle messages. The owner chose "Targeted decouple": route Create's own output through a per-call writer now, defer removing the `m.output` field (still used by `EnsureSetup`/`recreateContainer`/`setup.go`) to a later phase.

**Decision.** Add `Output io.Writer` to both `sandbox.CreateOptions` and the public `yoloai.CreateOptions` (mapped in `toInternal`). The create pipeline never reads `m.output` directly; every consumption site resolves through `func (m *Manager) outputFor(o io.Writer) io.Writer` — returns `o` when non-nil, else `m.output`, **never nil**. So a per-call writer wins (concurrent Creates on one Manager keep separate streams), nil falls back to the Client's `Options.Output`, and a leaf writer can't panic on a nil `io.Writer` no matter which create helper a caller enters through.

**Why a resolver instead of defaulting `opts.Output` once in `Create`.** `resolveAndApplyArchetype` is an independently unit-tested seam (18 callers in `archetype_resolution_test.go`) that builds `CreateOptions` inline and bypasses `Create`. A single default in `Create` left those helpers panicking on `fmt.Fprintf(nil, …)`. `outputFor` makes *every* entry point safe uniformly rather than relying on "Create defaulted it."

**Consequences.**
- `sandboxState.output` holds the raw (possibly nil) per-call writer; the sole consumer (`launchContainer` → `filterAvailablePorts`) wraps it in `outputFor`. `recreateContainer`'s restart-path state literal sets `output: m.output` so its behavior is unchanged.
- CLI behavior is identical: `new` leaves the public `Output` nil, so `outputFor(nil)` resolves to `m.output` = the Client's `mgrOutput` (stderr, or `io.Discard` under `--json`). No `runNewCmd` change needed.
- New tests `TestCreateOutput_PerCallWriterOverridesManager` / `TestCreateOutput_NilWriterFallsBackToManager` pin the contract.

**Deferred.** The `m.output` field and `NewManager`'s output param stay — `EnsureSetup` (first-run system setup, also reachable via `system setup`), `recreateContainer`, and `setup.go` still use them. Removing the field (and giving `Client` a stored `output` to seed per-call defaults) is the next F8 phase.

**Composition.** Completes F8 alongside the structured-`Notices` work (D-F8 lifecycle migration). The per-call-writer-with-resolver mirrors the optional-interface `XFor` helper idiom (D30) and the thin-CLI / library-owns-I/O boundary (D27).

---

## D34 — F8 tail: the `Manager.output` field is gone; output is per-call or returned

**Date:** 2026-05-29. **Status:** Accepted. **Context:** D33 routed Create's stream to a per-call writer but left the embedded `m.output io.Writer` for the last holders (`EnsureSetup`, `recreateContainer`, `setSetupComplete`). The field is the F8 anti-pattern: a shared writer on a Manager documented concurrency-safe would interleave concurrent operations.

**Decision.** Remove the `output` field from `Manager` and the `output` param from `NewManager`. Each remaining holder gets its writer explicitly or stops writing:
- **`EnsureSetup(ctx, out io.Writer)` / `EnsureSetupNonInteractive(ctx, out io.Writer)`** — the base-image build stream + first-run "Tip" go to `out`. `Client.EnsureSetup` passes `c.output`; `Create` passes `m.outputFor(opts.Output)`.
- **`recreateContainer(ctx, …, n *notices)`** — the restart path's only writer use is `filterAvailablePorts`. Rather than thread a raw writer into Start/Reset (which return Notices, not a stream), a small `noticeWriter` adapter (notice.go) turns each newline-terminated line into a `Notice` at the given level, so the port-availability warning surfaces through the Start/Reset result's Notices. Create keeps using a real `io.Writer` (its `CreateOptions.Output`); `launchContainer` stays shared and writer-based.
- **`setSetupComplete`** — its "Setup complete" `Fprintln` was already **dead** (SystemClient's Manager used `io.Discard`; the CLI prints the line itself at `internal/cli/system/setup.go`). Deleted; the method now only persists state.
- **`Client`** gains a stored `output io.Writer` (from `Options.Output`, defaulted to `io.Discard`) that seeds `Create`'s and `EnsureSetup`'s per-call writers.
- **`Manager.outputFor`** fallback flips from `m.output` to `io.Discard` (a nil per-call writer means a direct library caller opted out; the Client always seeds it).

**Consequences.**
- `NewManager`'s signature drops one arg — wide but mechanical test ripple (~33 sites). Helper `newTestManager` lost its `output` param; tests that captured create-pipeline output now pass `CreateOptions.Output`.
- CLI behavior unchanged: `Client.Output` (= `mgrOutput`, stderr or `io.Discard` under `--json`) still feeds the build stream / advisories via the Client-seeded per-call writers.
- The Manager now holds **no** I/O sink — every operation's human-readable output is either a per-call writer arg or a returned `Notice`.

**Composition.** Finishes F8. Realizes the §12 "no ambient state on library objects" stance at the Manager level and the D27 thin-CLI boundary (library returns data/streams, CLI renders). Sibling to D33.

---

## D35 — Prune invariant: plain `prune` never forces a rebuild; `--cache`→`--images`; two-tier reporting

**Date:** 2026-05-29. **Status:** Accepted. **Context:** A user on Linux Docker (containerd image store) saw free space shrink every smoke run, with `yoloai system prune --cache` reclaiming little and `system disk` reporting docker at **33.66 GiB** for a base image that reads ~5 GiB on macOS Docker Desktop. Root cause was two backend behaviors now in `backend-idiosyncrasies.md`: on the containerd store the BuildKit build cache pins image layers (so `image rm` frees ~0 until the build cache is pruned), and the SDK's `SpaceReclaimed` undercounts the cascading GC by ~4x. The old bare `prune` never touched the build cache, so the obvious reclaimable space sat unclaimed; `--cache` was both too blunt (always forced a rebuild) and too generically named to convey that cost.

**Decision — the invariant.** `yoloai system prune` reclaims as much as it can **without leaving yoloai in a state where it must rebuild something**: after a plain prune, any command (`new`, etc.) still runs without triggering a build. Concretely, plain prune now also reclaims each backend's *no-rebuild* cache (Docker/Podman build cache, retired volumes, dangling images) — the tagged base image keeps its own layer pins, so it survives. The rebuild-forcing tier (base/profile images) moves behind a renamed flag **`--images`** (was `--cache`).

**Mechanics.**
- `runtime.CachePruner.PruneCache(ctx, includeImages, dryRun, output) (int64, error)`: docker prunes ContainersPrune → BuildCachePrune(all) → VolumesPrune → NetworksPrune → (if `includeImages`) ImagesPrune(dangling=false). tart/containerd have no no-rebuild cache distinct from the base image, so `includeImages=false` is a no-op there.
- Reclaim is measured as a `statfs` free-space delta on the daemon data root (`measuredReclaim`/`freeBytes`/`daemonDataRoot`) because `SpaceReclaimed` is unreliable; falls back to the SDK sum when the data root isn't host-visible (Docker Desktop LinuxKit VM). **(Superseded by D37 — the statfs delta cross-contaminates across backends sharing one filesystem; reclaim is now a per-backend CacheUsage before/after delta.)**
- `runtime.CacheUsage` splits into `{CachedBytes (no-rebuild, ≥0), ImageBytes (rebuild-forcing, −1=unknown), Detail}`. `splitCacheBytes` maps `LayersSize`→images, container `SizeRw`+volumes+build cache→cached. `SystemClient` always calls `PruneCacheFor` (no longer gated on the flag) and aggregates the reclaimed bytes into the pre-existing `PruneResult.FreedBytes`.
- Reporting splits to match: `system disk` → `CACHE`/`IMAGES` columns; `doctor` → two reclaim tiers (`renderReclaimTier` ×2); prune prints `Reclaimed <n>` and emits `freed_bytes` in `--json`.

**Why `--images` over `--include-base-image`** (api_surface.go's aspirational name): shorter, and "images" is the word users already use for the thing that gets rebuilt. The api_surface note is aspiration, not spec — the fact (what forces a rebuild) drove the name.

**Consequences.** Breaking: `--cache` removed in favor of `--images`, and bare `prune` now clears the build cache (BREAKING-CHANGES entry added). Only regenerable cache is touched by the default, so the invariant holds.

**Composition.** Extends the D21 disk/prune surface and the D32 doctor advisory (read + delegate; doctor still never deletes). Thin-CLI/library-owns-I/O boundary (D27): the library measures and returns `FreedBytes`; the CLI renders it.

## D36 — Accurate disk reporting + pruning for Podman and containerd; socket-only measurement

**Date:** 2026-05-29. **Status:** Accepted. **Context:** With the D35 two-tier reporting in place, a testbed on this dev machine (the only thing it runs is yoloai) sat at 67% disk while `doctor` reported the whole machine's reclaimable cache as ~64 MiB — off by ~25 GB. Two backend-specific gaps (both now in `backend-idiosyncrasies.md`): Podman's docker-compat `/system/df` returns `LayersSize: 0`, so the D35 `splitCacheBytes` (which trusts `LayersSize`) reported podman images as 0 B; and the containerd backend punted entirely (`ImageBytes = -1`, "unknown") *and* its prune hardcoded the overlayfs snapshotter, so the devmapper thin-pool copy (used by `--isolation vm-enhanced`) was neither sized nor pruned — leaking snapshots that fill the pool (a likely contributor to the `smoke-containerd-disk-pressure` ENOSPC stalls).

**Decision — measure through the backend socket/API, never the host filesystem.** yoloai may run unprivileged (Docker/Podman group, containerd group) and `/var/lib/{docker,containerd}` is root-only, so `du`/`dmsetup` are off the table on the normal path. All sizing goes through the daemon API.

**Mechanics.**
- **Podman image bytes:** `docker.Runtime` gains an injectable `imageBytesFn` (default: `du.LayersSize`); `splitCacheBytes` becomes a method that uses it. Podman's `New` injects `podmanImageBytes`, which dedups from per-image fields: `Σ(Size − SharedSize) + max(SharedSize)` — unique bytes plus the shared layer set counted once. Exact for a single-base build chain; slight underestimate with multiple independent bases. Fixes both `CacheUsage` and the dry-run estimate with one hook.
- **containerd sizing:** `CacheUsage` sums snapshot `Usage(ctx, key).Size` across **both** snapshotters (`overlayfs` + `devmapper`) in the yoloai namespace into `ImageBytes` (both physically occupy disk → the sum is the honest footprint). `-1` survives only as an error fallback. `snapshotNames` returns `present=false` to silently skip an unconfigured snapshotter (devmapper on a plain Linux box).
- **containerd prune:** `pruneSnapshots` iterates both snapshotters, measures each removed snapshot's `Usage` for the returned reclaim total, and prints the devmapper caveat. **(Removal ordering refined in D37: snapshots are removed leaf-first so every `Remove` succeeds synchronously rather than relying on a later GC pass.)**

**The devmapper caveat (honesty).** Removing a devmapper thin snapshot returns blocks to the pool but the pool's fixed-size backing loopback file does not shrink, so host `df` is unchanged. Prune says so explicitly; the reported reclaim is pool-block reclaim, not freed host disk. The pool is a host prerequisite, not yoloai-owned — yoloai prunes only the snapshots it created.

**CLI labels.** `FreedBytes` aggregates the no-rebuild tier *and* (under `--images`) the rebuild-forcing tier, so the prune banner/`Reclaimed` line now say "backend cache + base images" when `--images` is set, "backend cache" otherwise.

**Validated on testbed (2026-05-29):** podman 5.18 GiB (matches `/system/df` dedup exactly), containerd 10.87 GiB (overlayfs 5.30 + devmapper 5.53, matching summed snapshot `Usage` and `dmsetup` ~57% of the 10 GB pool); dry-run lists 28 overlayfs + 28 devmapper snapshots. Pre-fix doctor reported ~64 MiB total.

**Composition.** Extends D35 (same `CachePruner`/`DiskUsageReporter` surface, same `FreedBytes` aggregation). Thin-CLI/library-owns-I/O (D27): backends measure and return bytes; CLI renders. **macOS unverified** — see `docs/dev/macos-disk-reporting-checklist.md` (Docker Desktop's LinuxKit VM hides the data root from host `statfs`; Tart/Seatbelt have entirely different disk models).

## D37 — Reclaim is a per-backend CacheUsage before/after delta (not statfs, not SpaceReclaimed); leaf-first snapshot removal

**Date:** 2026-05-29. **Status:** Accepted. **Supersedes** the reclaim-measurement mechanics of D35/D36 (the sizing/reporting decisions of D36 stand). **Context:** Rebuilding the testbed (this machine runs only yoloai) and running a single multi-backend `system prune --images` with all of docker + podman + containerd populated exposed that *both* prior reclaim-measurement strategies are wrong:

- **statfs delta (D35) cross-contaminates.** `measuredReclaim`/`freeBytes`/`daemonDataRoot` read host free-space change on the daemon data root. On a shared `/` (the common Linux case), backends are pruned in one pass, so one backend's free-space delta absorbs bytes freed by another (e.g. containerd's async GC running while docker's delta is measured), misattributing reclaim.
- **SDK `SpaceReclaimed` is unreliable in both directions on the docker-compat API.** Measured live: Podman's docker-compat `ImagesPrune` returns `SpaceReclaimed` as the **un-deduplicated sum** of every removed image's size — 38 images sharing one ~5 GiB base reported **142.27 GB** for a 5.18 GiB footprint. On the Docker containerd image store `SpaceReclaimed` *undercounts* (returns before GC frees the layers the pruned build cache had pinned). So neither raw `SpaceReclaimed` nor a statfs delta is trustworthy.

**Decision — report reclaim as this backend's own `CacheUsage` drop across the prune: `before − after`.** `CacheUsage` is already validated-accurate per backend (D36: podman dedup is exact, docker `splitCacheBytes`, containerd summed snapshot `Usage`). Its before/after delta is therefore the truthful, *self-attributed* reclaim: each backend reads only its own daemon's usage, so cross-contamination is impossible by construction, and the prune total reconciles with the `doctor`/`disk` figures by definition (same measurement). This is the truest form of the "logical self-report" direction — `SpaceReclaimed` was a wrong implementation of it.

**Mechanics.**
- **docker/podman** (`docker.Runtime.PruneCache`, shared by both): sample `reclaimableBytes(ctx, includeImages)` (= `CacheUsage` `CachedBytes` + `ImageBytes` when `includeImages`) before pruning, run the prune calls (ignoring their `SpaceReclaimed`), sample again, report `max(0, before−after)`. The `statfs` helpers (`measuredReclaim`/`freeBytes`/`daemonDataRoot`) and the `syscall` import are deleted. Podman's missing BuildKit cache (`BuildCachePrune` → "Not Found") is warned and harmless — the delta still captures actual reclaim.
- **containerd** (`pruneSnapshots`/`pruneSnapshotter`): unchanged in spirit — sums each removed snapshot's `Usage`, which *equals* the before/after `CacheUsage` delta. Removal is now **leaf-first** (`orderLeafFirst`, a Kahn topological pass over in-memory `Parent` links): children precede parents, so every `Remove` succeeds synchronously and the whole chain is freed in one pass rather than leaving the bulk to a later GC. A snapshot whose `Remove` genuinely fails is excluded from the total and warned about.

**Validated on testbed (2026-05-29), single multi-backend `prune --images`:** podman reported **5.18 GB** (was 142.27 GB under `SpaceReclaimed`; matches its 5.184 GiB `/system/df` dedup exactly), docker **19.63 GB** (its logical footprint), all backends cleared to 0. Host `df` freed 17.8 GiB; the **logical-vs-physical gap is expected and documented**: on the containerd image store the build cache and image layers share content, so the logical sum (cache + images counted separately) exceeds the physical bytes freed. Each backend reported only its own footprint — no cross-contamination.

**Idiosyncrasies recorded** (`backend-idiosyncrasies.md` + symptom index): Podman docker-compat `ImagesPrune` `SpaceReclaimed` un-dedup inflation; containerd's image-import path inconsistently materializes overlayfs snapshots (some imports unpack, some only link); and a leftover lease (e.g. from a manual `ctr images mount`) GC-roots an orphaned child snapshot, making the base layer un-removable via synchronous `Remove` until the lease is dropped and GC runs — a test-scaffolding artifact, not a yoloai-flow bug.

**macOS still unverified** — see `docs/dev/macos-disk-reporting-checklist.md`, updated to drop the deleted `statfs` references and check the before/after delta instead. **(Verified on macOS 2026-05-29 — see D38.)**

## D38 — macOS disk reporting verified; Tart gains DiskUsageReporter + real reclaim; docker/podman/seatbelt confirmed accurate

**Date:** 2026-05-29. **Status:** Accepted. **Composition:** verifies D36 (sizing) and D37 (before/after reclaim delta) on macOS; closes the "macOS unverified" note on D36. **Context:** the D35–D37 disk-reporting/prune work was Linux-only; this is the macOS pickup (`docs/dev/macos-disk-reporting-checklist.md`). The test Mac runs Docker via **OrbStack** (not Docker Desktop), Podman 5.8.1 (Podman Machine `applehv`), Tart 2.31.0 (Apple Silicon), and Seatbelt.

**What was verified (read-only, against each backend's own tool + `du`).**
- **Docker (OrbStack).** Classic `overlay2` store on btrfs, containerd-snapshotter **off**. `image_bytes` byte-exact vs `docker system df` (`5023481654` = `5.023GB`); `cached_bytes` matches Local Volumes. The socket/API sizing path is store-/VM-agnostic, so it works unchanged; the containerd-store layer-pinning gap does **not** apply on the classic store. **No code change.** Lesson recorded: check `docker info` context/store — "macOS Docker" ≠ Docker Desktop.
- **Podman 5.8.1.** Raw `/system/df` returns `LayersSize: 5018303449` — **not 0**. The Linux `LayersSize: 0` bug is **version-specific**; the unconditional `podmanImageBytes` dedup computes the identical value here, so it's harmless redundancy. Matches `podman system df` exactly. **No code change.**
- **Seatbelt.** Provisions nothing on disk (Setup only checks PATH); runs agents via host tools. Its only footprint is the per-sandbox dir already counted by the `sandboxes` row. No backend cache → `CacheUsage`/`PruneCache` correctly absent (no-op). **No code change** — documented as intentional.

**What was fixed (Tart — the one real gap).** Tart held ~56 GiB (`~/.tart/vms` + `~/.tart/cache/OCIs`) but reported `IMAGES: ?` / `0 B` and pruned with `0` reclaimed: it implemented `PruneCache` but **no `DiskUsageReporter`**, and `PruneCache` returned a hardcoded 0. Two tart idiosyncrasies drove the design (now in `backend-idiosyncrasies.md`): (1) `tart list` shows one pulled OCI image as **two rows** (tag `:latest` + digest `@sha256:…`) over a **single** on-disk copy — naive summing double-counts; (2) `tart delete <tag>` leaves the digest row pinning the copy, so a tag-only prune frees ~0.

**Decision.** Tart now implements `DiskUsageReporter`: `CacheUsage` = provisioned local VM + base-repo OCI rows **deduped to one** (max Size per repo, like the podman "shared once" approach), reported as `ImageBytes` (no no-rebuild cache → `CachedBytes`=0). `PruneCache` deletes the provisioned VM **and all** base-repo OCI rows (tag *and* digest), then reports reclaim as the `CacheUsage` before−after delta — the same self-attributed measure as docker/podman (D37). Scope is intentionally yoloai's base images only, **not** every VM tart tracks nor live sandbox clones: tart is the user's general-purpose VM tool, so the IMAGES column must reconcile with what `prune --images` actually frees and must never imply deletion of unrelated personal VMs. `tart list --format json` Size is whole-GB, so the figure is coarse (±~0.5 GB/image) — accurate enough for a "should I prune?" signal.

**Rejected.**
- *Measure tart via `du ~/.tart`* — rejected: stay API-only (use `tart list`), consistent with the socket-only principle even though tart files are user-owned. (`du` was used only as the verification ground truth, not in product code.)
- *Report all VMs tart tracks (docker/podman semantics)* — rejected: would count the user's unrelated VMs as "yoloai reclaimable," implying `prune --images` deletes them. Scoped to the base images yoloai owns.
- *Add a CacheUsage/prune cache to seatbelt* — rejected: it has no backend store; a no-op is the honest model.

**Validated (2026-05-29).** `system disk` now: docker 4.68 GiB (=`docker system df`), podman 4.67 GiB (=`podman system df` dedup), tart **55.88 GiB** (≈ `du` ~56 GiB), seatbelt no-op. Unit tests added (`tart/diskusage_test.go`). End-to-end reclaim confirmed by a real `system prune --images` (see the run recorded in `macos-disk-reporting-checklist.md`).

---

## D39 — F5 god-package carve: `sandbox.Manager` renamed to `sandbox.Engine`; root stays the façade; behaviour dissolves into subpackages

**Date:** 2026-05-29. **Status:** Accepted (in progress — this entry covers the design + the first slice, the rename). **Closes:** F5, the last open finding of the 31-finding architecture critique (`critique-followup.md`). **Context:** `internal/sandbox/` was a ~16K-line single package with 75 methods on one `Manager` type — both a god-*package* (no internal boundaries) and a god-*object* (one type as the entry point for everything).

**Two design forks resolved (owner, 2026-05-29).**
1. *Where do the public types live?* **Root stays the façade.** `internal/sandbox/` keeps the externally-used surface (`CreateOptions`, `DirSpec`, `Info`, `Status*`, error types) unchanged; the heavy machinery moves into subpackages beneath it. Rejected: moving everything to `internal/sandbox/manager/`, which would have rippled ~300 external call sites or required a wall of re-export aliases for identical decoupling.
2. *Do the 75 methods survive as delegators?* **No.** Thin delegators on the façade would relocate the bodies but keep the god-*object*. Instead the behaviour becomes **free functions** distributed by concern across `create/`, `lifecycle/`, `mounts/` (taking primitives: `runtime.Runtime`, `config.Layout`, `*slog.Logger`, `state.State`). The public caller is the already-decomposed `yoloai.Client` + sub-handles (F2/F22/F23), so each handle calls the matching subpackage — no new god-object appears at the Client layer.

**The forced shape (Go's no-foreign-methods rule).** A subpackage cannot define methods on a type it doesn't own, so the resolved per-operation `sandboxState` must move to a shared leaf package both `create/` and `mounts/` import → `internal/sandbox/state/` (with `DirSpec`/`DirMode` moved down too, root keeping `type DirSpec = state.DirSpec` aliases, else `state ↔ sandbox` cycles). Dependency order is therefore: `state/` (leaf) ← `mounts/` ← `create/`; `lifecycle/` ← `state/`; façade on top. The type that survives at root is renamed **`Engine`** (a deps-holder bundling runtime+layout+logger+input, plus cross-cutting `Layout()`/`Runtime()`/`EnsureSetup()`); "Manager" was a vague catch-all smell.

**Landing sequence (each its own green commit):** F5.0a rename `Manager`→`Engine` (this commit); F5.0b extract `state/`; F5.1 `mounts/`; F5.2 `create/` (dissolve ~22 methods); F5.3 `lifecycle/` (dissolve ~29 methods). Interim wart: surviving method receivers stay `m` until the final phase rather than churning bodies that later get deleted.

## D40 — F5.2 refinement: the create/lifecycle shared base splits into three purpose-named leaf packages (`invocation`, `provision`, `launch`)

**Date:** 2026-05-29. **Status:** Accepted (owner, 2026-05-29). **Refines** D39's "F5.2 create/, F5.3 lifecycle/" step. **Context:** before carving `create/`, dependency analysis showed `lifecycle.go` already calls a large chunk of create's machinery — `launchContainer` + the whole container-launch stack, `ReadPrompt`, `buildAgentCommand`, `copySeedFiles`, `ensureContainerSettings`, `recoverSudoCredentials`, `hasAnyAPIKey`, etc. `create` reaches into `lifecycle` only once (`replaceSandboxIfNeeded`→`destroy`). So the two are not siblings: there is a shared launch/seed/command **base** that both consume.

**Decision.** Extract that base into three purpose-named leaf packages rather than one `common`/`util` grab-bag (a `common` package is how a god-package quietly reassembles itself; the name must describe *what*, not *where*). The call graph among the shared functions is a clean DAG — `launch → invocation` and `launch → provision`, with `invocation` and `provision` depending on nothing shared — so the split adds no cross-package cycles or back-plumbing.

- **`internal/sandbox/invocation/`** (~200 LOC, leaf): agent command + model resolution — `ResolveModel`, `ApplyModelPrefix`, `ValidateModel`, `BuildAgentCommand`, `SanitizeTunnelName`, `ResolveDetectors`, `ReadPrompt` (+ unexported `shellEscapeForDoubleQuotes`). All were already free funcs (pure move + export). Imports `agent`, `config`, `yoerrors`.
- **`internal/sandbox/provision/`** (~375 LOC, leaf): credentials + seed files — `CopySeedFiles` (+helpers), `EnsureContainerSettings`, `EnsureHomeSeedConfig`, `CreateSecretsDir`, `RecoverSudoCredentials`, `HasAnyAPIKey`/`AuthFile`/`AuthHint`, `DescribeSeedAuthFiles`, plus dissolving `Engine.seedSandbox`.
- **`internal/sandbox/launch/`** (~385 LOC): resolved `State` → running container — `LaunchContainer`, `BuildAndStart`, `BuildInstanceConfig`, `BuildContainerConfig`, `VerifyInstanceRunning`, resource/overlay/cap application, port parsing/filtering, plus the launch config types (`containerConfig`, `lifecycleConfig`, `overlayMountConfig`). Dissolves 4 `Engine` methods; takes the shared `Deps` (runtime/layout/logger/input/progress) which lands in the `state/` leaf.

Generic file utils (`mkdirAllPerm`, `writeFilePerm`) go to the existing `internal/fileutil`, not the new packages. Revised layering: `state/`(types+`Deps`) ← {`mounts/`, `invocation/`, `provision/`} ← `launch/` ← {`create/`, `lifecycle/` (siblings)} ← façade. The lone `create→destroy` back-edge is broken when `create/` is carved (extract the teardown primitive so `replaceSandboxIfNeeded` doesn't import `lifecycle/`).

**Revised sequence:** F5.2a `invocation/`; F5.2b `provision/`; F5.2c `launch/` (+`Deps` in `state/`); F5.2d `create/` orchestration (Client.Create → `create.Run`); F5.3 `lifecycle/`. Each its own green commit.

## D41 — F5.2c refinements: `BuildInstanceConfig` narrowed to `BackendDescriptor`, config.json assembly deferred to `create/`, slim `state.Deps`

**Date:** 2026-05-29. **Status:** Accepted (owner, 2026-05-29). **Refines** D40's `launch/` carve as executed.

Three deviations from D40's literal grouping, each forced by the dependency facts the carve surfaced:

- **`buildInstanceConfig` takes `runtime.BackendDescriptor`, not `runtime.Runtime`.** It only ever read the backend's capabilities/name off `rt`. Narrowing the param to the descriptor it actually consumes (a) makes the dependency honest and (b) lets the launch tests construct a literal `runtime.BackendDescriptor{...}` instead of dragging in the sandbox-package `mockRuntime`. Net: launch's tests no longer couple to the façade's test doubles.
- **`buildContainerConfig` + the launch config types (`containerConfig`, `lifecycleConfig`, `overlayMountConfig`) + `buildLifecycleConfig` + `lifecycleCmdToJSON` stay in the façade, deferred to F5.2d `create/`** — *not* moved to `launch/` as D40's bullet listed. Reason: this is config.json *assembly*, and only the create path builds config.json. `lifecycle.recreateContainer` reads the *existing* config.json off disk; it never rebuilds. So this machinery belongs with create orchestration, not the shared launch base. D40's grouping was written before that read/write asymmetry was confirmed.
- **`state.Deps` shipped slim: `{Runtime, Layout}` only**, not the fuller `{runtime, layout, logger, input, progress}` D40 sketched. `LaunchContainer` needed only runtime+layout; the rest stay Engine fields until a later phase proves a free function needs them. Added a doc note that fields will grow as more methods dissolve. Avoids speculative width (YAGNI).

Outcome: `create_instance.go`/`_test.go` deleted (fully absorbed), `launch.LaunchContainer(ctx, m.deps(), st)` called from both create.go:233 and lifecycle.go:929, `make check` + `go vet -tags=integration` green.

## D42 — F5.2d: `create/` carve — `Engine.Create` dissolves to `create.Run`, `EnsureSetup` hoisted to the Client

**Date:** 2026-05-29. **Status:** Accepted (owner, 2026-05-29). **Implements** D40's `create/` leaf; **continues** D41's deferral (config.json assembly lands here).

The create pipeline (`create.go` + `create_prepare.go` + `context.go`, ~2060 LOC) moves wholesale into a new `internal/sandbox/create/` leaf (`package create`). The 17 `Engine` methods across those files dissolve into free functions; the ~28 functions that were already receiverless move unchanged. The façade keeps only thin public *aliases* — no create machinery and no `Engine.Create` delegator survives in `package sandbox`.

Decisions forced by the carve:

- **`Engine.Create` is dissolved, not delegated.** New entry point: `create.Run(ctx context.Context, d state.Deps, opts Options) (name string, err error)`. The sole production caller, `yoloai.Client.Create`, now calls `create.Run` directly. The façade `Engine` retains no `Create` method (consistent with the F5 mandate: dissolve the 75 methods, don't keep delegators).
- **`state.Deps` gains `Input io.Reader`.** Justified because `m.input` is read by *both* the create path (`resolveAgentParams`→`invocation.ReadPrompt`) and the lifecycle path (`preparePromptForStart`) — it is a genuinely shared dependency, not create-only. `Engine.deps()` populates it from `m.input`. (D41 anticipated Deps growing as methods dissolve.)
- **`m.backend` is *not* threaded** — derived in-leaf as `d.Runtime.Descriptor().Name`, which is exactly how `NewEngine` seeds the field (engine.go:71). Avoids widening `Deps` with a derivable value.
- **`m.progress` is not threaded** — the create pipeline never reads it (only `WithProgress` sets it). Confirmed by grep.
- **`EnsureSetup` stays in the façade and is hoisted to `Client.Create`** (called *before* `create.Run`). `EnsureSetup` is name-independent, global first-run scaffolding that does not need the per-sandbox lock. This flips its order relative to `checkIsolationPrerequisites` (previously inside `Create`, before `EnsureSetup`); the reorder is benign — `EnsureSetup` is idempotent and a near-instant no-op after first run, and the MCP entry path already calls `EnsureSetup` before `Create` today, so "setup before isolation check" is already the de-facto order there. `create.Run` therefore assumes setup has run.
- **`yoloai.Client` gains an `input io.Reader` field** (populated in `NewWithOptions`, where `input` is already computed) so `Client.Create` can build `state.Deps{Runtime, Layout, Input}` without reaching into the `Engine`.
- **`ErrSandboxExists` relocates to `package create`** (only `create` produces "already exists"); `sandbox.ErrSandboxExists` becomes an alias so the public symbol and its `yoloai.ErrSandboxExists` re-export are unchanged.
- **`type State = state.State` alias relocates** from create.go to engine.go (still referenced by the staying façade files engine.go/setup.go/lifecycle.go). Inside `create/`, the canonical `state.State` is used directly.
- **Public option types alias from the façade:** `type CreateOptions = create.Options`, `type NetworkMode = create.NetworkMode`, and the three `NetworkMode*` consts. External callers (`yoloai` root's `create_options.go`, `names.go`) keep compiling through the aliases.
- **Config.json assembly lands here** (per D41's deferral): `buildContainerConfig`, the `containerConfig`/`lifecycleConfig`/`overlayMountConfig` types, `runtimeConfigSchemaVersion`, `buildLifecycleConfig`, `lifecycleCmdToJSON` all move into `create/`.
- **Test placement:** white-box tests of create internals (buildContainerConfig, gitBaseline, removeGitDirs, archetype/devcontainer, dir parsing, context) move to `package create` — they pass literal args and need no runtime double (same self-containment as D41's launch tests). The end-to-end `TestCreate_CleansUp*` tests, which need the stateful façade `mockRuntime`, stay in `package sandbox` and call `create.Run` directly. `TestBackendCaps` (a runtime-descriptor test, only incidentally in create_test.go) moves to engine_test.go.

## D43 — F5.2d shared-symbol homing: `runtimeconfig/` + `profiles/` leaves; `Engine.Create` removed cleanly

**Date:** 2026-05-29. **Status:** Accepted (owner, 2026-05-29). **Refines** D42 (supersedes its "config.json assembly lands in create/" and the interim "Engine.Create delegator" / duplicated-profile-build shortcuts taken during implementation).

While carving `create/`, three symbols turned out to be shared across the create/lifecycle sibling boundary (D40's DAG forbids a `lifecycle → create` edge, which a naïve carve introduced). Resolved by homing each in the *lowest leaf both consumers can import* rather than dumping them into `launch/`:

- **`ContainerConfig` → new `internal/sandbox/runtimeconfig/` leaf** (`package runtimeconfig`). This is the Go↔Python `runtime-config.json` *contract*: pure data (`ContainerConfig`, `OverlayMountConfig`, `LifecycleConfig`) plus the versioned `SchemaVersion` const and its cross-language fence test (`schema_version_test.go`, moved here). `create/` *writes* it (the `buildContainerConfig`/`buildLifecycleConfig` assemblers stay in `create/` and now construct `runtimeconfig.ContainerConfig{...}`); `lifecycle.go` *reads* it. Both import `runtimeconfig`; neither imports the other. **This corrects D41/D42's premise that config.json assembly could live wholly in `create/`** — the *type* is shared, so it belongs in a lower leaf even though the *assembler* stays in `create/`.
- **Profile image building → existing (empty) `internal/sandbox/profiles/` leaf** (`package profiles`): `EnsureProfileImage`, `AutoBuildSecrets`, `ValidateBuildSecret`, `ProfileImageBuilder`. Consumed by both façade `EnsureSetup`/`system_client.go`/CLI and the `create/` pipeline (`create_prepare.go`). The façade keeps thin re-export aliases (`var EnsureProfileImage = profiles.EnsureProfileImage`, `type ProfileImageBuilder = profiles.ProfileImageBuilder`, etc.) so `sandbox.X` stays stable for CLI callers. A verbatim unexported copy that had been duplicated into `create/` is deleted.
- **`Engine.Create` removed outright** (no delegator). The ~29 integration-test call sites (`integration_test.go`, `integration_tart_test.go`) now go through a `createSandbox(ctx, mgr, opts)` test helper that calls `create.Run` with `state.Deps{Runtime: mgr.Runtime(), Layout: mgr.Layout(), Input: strings.NewReader("")}` — faithfully reproducing the deleted `Engine.deps()`. `EnsureSetup` is still done once in `integrationSetup`.
- **Rationale for not using `launch/`:** `launch/` is container start/stop/teardown; `provision/` is per-sandbox credential/seed prep. Neither image building (a *profile* concern) nor the config.json contract (its own versioned data shape) is a launch behavior. Single-responsibility placement keeps `launch/` from becoming a grab-bag.
- **F5.3 follow-up:** `CheckIsolationPrerequisites` still lives in `create/` and is referenced by façade `lifecycle.go` (legal today — `package sandbox` may import `create`). When `lifecycle/` is carved into its own sibling package, that symbol must also relocate to a shared lower leaf (likely `runtimeconfig`-adjacent or a new `caps`-using leaf) to avoid a `lifecycle → create` edge.

## D44 — F5.3: `lifecycle/` + `status/` leaves carved; F5 (and the 31-finding critique) complete

**Date:** 2026-05-29. **Status:** Accepted (owner, 2026-05-29). **Implements** D40's `lifecycle/` leaf and the read-model split; **resolves** D43's F5.3 follow-up on `CheckIsolationPrerequisites`.

Final F5 carve, in three steps:

- **F5.3a — `status/` leaf.** All of `inspect.go` (the sandbox read-model: `DetectStatus`, `InspectSandbox`, `ListSandboxes`, `ProbeWorkData`, the `Status`/`AgentStatus`/`WorkDataState` types + constants, age/size formatting) moves wholesale into `internal/sandbox/status/` (`package status`). The façade `inspect.go` becomes pure aliases (`type Info = status.Info`, `var InspectSandbox = status.InspectSandbox`, `const StatusActive = status.StatusActive`, …). `IsolationPerms`/`Perms` are `state` re-exports (consumed by lifecycle) and stay aliased in the façade, not moved to `status/`.
- **F5.3b — `CheckIsolationPrerequisites` → `launch/`.** D43 flagged this as the last `lifecycle → create` back-edge risk. Resolved by homing it in `launch/` (the lowest leaf both create/ and lifecycle/ already import), *not* a new caps-leaf as D43 speculated — `launch/` already depends on `runtime`/`caps` and is the natural shared-launch home, so no new package was warranted. `create.go` drops its copy and its `runtime/caps` import; both create/ and lifecycle/ now call `launch.CheckIsolationPrerequisites`. This makes create/ and lifecycle/ true siblings with **no** edge between them.
- **F5.3c — `lifecycle/` leaf, methods dissolved.** `lifecycle.go` + `notice.go` (~1450 LOC) move into `internal/sandbox/lifecycle/` (`package lifecycle`). The Engine lifecycle methods (`Stop/Start/Destroy/Reset/NeedsConfirmation` + ~40 helpers) dissolve into free functions taking `state.Deps` — no delegators. `yoloai.Sandbox` calls `lifecycle.Stop(ctx, s.c.deps(), name)` etc. directly; `Client.deps()`/the new `Sandbox` plumbing build `state.Deps` from `c.rt`/`c.layout`/`c.input`. `Engine` loses its lifecycle methods and the now-unused `deps()` helper; only `SendInput` remains a method (it is genuinely Engine-scoped tmux plumbing, not lifecycle orchestration). Façade `lifecycle.go`/`notice.go` keep alias re-exports (`StartOptions`, `ResetOptions`, `PatchConfigAllowedDomains`, `Notice`/`NoticeLevel`/`*Result`).

Decisions / notes:

- **Per-leaf test mocks.** Each carved leaf (`status/`, `lifecycle/`) gets its own `fakeruntime_test.go` implementing the full `runtime.Runtime` (unimplemented methods return a sentinel) instead of importing the façade's shared `mockRuntime`. Helpers that `clone_test.go`/`terminal_test.go` (still in `package sandbox`) had borrowed from the old `lifecycle_test.go` were relocated into `testhelpers_test.go` so those tests keep compiling.
- **Integration tests.** `mgr.Stop/Start/Reset/Destroy` call sites route through `stopSandbox`/`startSandbox`/`resetSandbox`/`destroySandbox` helpers that build `state.Deps` from `mgr.Runtime()`/`mgr.Layout()`/`strings.NewReader("")`, mirroring the F5.2d `createSandbox` helper.
- **F5 done.** With lifecycle/ + status/ carved, `package sandbox` is a thin façade (Engine deps-holder + aliases + a few un-carved helpers: clone, parse, setup, terminal/attach). The full DAG `state ← {mounts, invocation, provision, profiles, runtimeconfig} ← launch ← {create, lifecycle} ← façade` holds, enforced by the absence of import cycles. **This closes F5 and the entire 31-finding layering critique.**

