ABOUTME: Engineering practice for yoloAI. YAGNI/KISS/DRY/SOLID vocabulary,
ABOUTME: boundary discipline, validate-at-every-layer, parse-don't-validate,
ABOUTME: fail-fast, warnings-are-signal, justify-every-discard, no-half-finished,
ABOUTME: plan-then-execute cleanup, make-check gate, iterate-when-first-approach
ABOUTME: -fails. How to write yoloAI code so future-you can change it safely.

# Development principles

How yoloAI is built — engineering-practice conventions that shape code structure, code review, and debuggability. Specialised application of `general-principles.md` to the engineering surface.

Established in D22 (`../working-notes.md`). Primary-source backing: `../research/principles/development-principles-research.md`.

## Framing — write the code so future-you can change it

yoloAI is single-author across five runtime backends. Every line of production code is a line Karl personally maintains. The structural value of the codebase is *malleability* — can a future change (a sixth backend, a new agent, a refactored sandbox state) land without rewriting the world?

The principles below are how we keep the code malleable. They are concrete engineering practice — the *what* and *how* questions standards answer ground out in these principles' *why*.

Complementary docs:
- `general-principles.md` — strategic disposition (boring tech, ecosystem-first, blast radius bounded). Applies across surfaces.
- `testing-principles.md` — how we test it. Tests are the safety net that makes the refactoring this doc demands actually safe.
- `security-principles.md` — what containment guarantees the production code must preserve.
- `../standards/GO.md` (planned), `../standards/CLI.md` (planned), `../CODING-STANDARD.md`, `../CLI-STANDARD.md` — concrete style and naming rules.

---

## §1. Engineering values — the cross-cutting vocabulary

A concise reference for the engineering values that shape every code path in yoloAI. Most aren't yoloAI-specific; they're the established vocabulary of professional software engineering.

**YAGNI — You Aren't Gonna Need It.** Don't build for hypothetical future requirements. Features, abstractions, and generalisations earn their place when a second concrete use case appears — not before. For a single-author project, every unused capability is maintenance Karl pays per backend.

**KISS — Keep It Simple.** The simplest solution that correctly satisfies the requirement is the right solution. Prefer flat over nested, explicit over clever, boring over novel. A future maintainer (or future-Karl debugging at 2am) should be able to read any code path and understand it without surprises.

**DRY — Don't Repeat Yourself.** Every piece of knowledge has one authoritative location. Duplicated logic means that when the knowledge changes, it must be found and applied in N places — and N−1 will be wrong. But — Sandi Metz: "duplication is far cheaper than the wrong abstraction." Extract on the second concrete use case; don't extract preemptively.

**SOLID.** Five principles for package and type design:
- *S — Single Responsibility*: each package, type, and function has one reason to change. `internal/runtime/` is runtime backends; `internal/sandbox/` is sandbox lifecycle; `internal/cli/` is the CLI surface. Mixing these creates coupling that blocks change.
- *O — Open/Closed*: extend behaviour by adding code, not modifying stable core paths. New backends add a `runtime.Runtime` implementation; they don't edit the dispatch. New agents add agent definitions; they don't edit the agent registry's internals.
- *L — Liskov Substitution*: every implementation of `runtime.Runtime` must be fully substitutable. A backend that panics on `Stop()` is not a valid `Runtime`; it violates the contract the caller relies on.
- *I — Interface Segregation*: narrow interfaces at consumption sites. The CLI takes `runtime.Runtime`, not `*runtime.docker.Backend`. Optional behaviours (e.g., overlay support) live behind optional interfaces (W11 step 3).
- *D — Dependency Inversion*: depend on interfaces, not concrete types, at every significant boundary. Runtime backends, agent definitions, idle detectors, store helpers — all interface-mediated. The composition root (`internal/cli/helpers.go::newRuntime`) is the only place concrete types are wired.

**Separation of Concerns.** Each layer has one job: CLI handlers parse args and format output; domain packages hold business logic; runtime backends call the underlying daemon. Mixing these creates coupling that prevents independent testing and evolution. See §2 for the project-specific expression.

**Law of Demeter.** A method should call methods on its own direct collaborators, not reach through them. `runtime.Stop(name)` is correct; `runtime.dockerClient.containerService.stop(...)` reaches through an intermediary and exposes internal structure.

**Fail Fast.** Detect and surface errors at the earliest possible point. Validate inputs at the boundary before they reach domain logic; return errors immediately rather than carrying partial state forward; reject invalid construction arguments in constructors rather than storing bad state for a later crash. See §5.

**Principle of Least Astonishment.** Code should behave the way a reader expects. A function named `Resolve` should resolve, not mutate. A middleware named `RequireAuth` should reject unauthenticated requests. An error named `ErrNotFound` should mean not-found, not a transient failure.

**Encapsulation and Information Hiding.** Internal implementation details should not leak to callers. Unexported struct fields, package-private constructors, and narrow interfaces all serve this goal. Backend-specific types (Docker SDK structs, Tart VM handles, Seatbelt SBPL profiles) never leak past their `runtime/<backend>/` package boundary.

**Convention over Configuration.** Established Go conventions and project standards are followed by default. Diverging from a convention requires a documented reason. Convention reduces cognitive load — a developer reading unfamiliar code in a familiar structure spends mental energy on the logic, not the layout.

**Tell, Don't Ask.** Tell objects what to do rather than asking for their state to decide for them. Business logic about a sandbox's state belongs inside the sandbox domain, not in CLI handlers that query fields and branch on them.

**Premature Optimization is the Root of All Evil.** Write for correctness and clarity first; optimise only when measurement identifies a real bottleneck. Structural decisions (algorithm choice, copy-vs-clone, N+1 patterns) belong in the design phase; micro-optimisations belong nowhere until they're measured to matter.

**Command–Query Separation.** A function either changes state (command) or returns information (query) — not both. Mixing them makes reasoning harder: a caller asking a question shouldn't worry about hidden side effects. Where a command must also return data (e.g., `Create` returns the new sandbox handle), document the exception.

**Robustness Principle.** Be conservative in what you emit, liberal in what you accept. Apply with care — the modern critique (Allman 2011) is that "liberal" can become "buggy by design." For yoloAI: accept legitimate input variation (e.g., paths with trailing slashes, different YAML quoting styles); reject invalid input at the boundary. Don't auto-fix what the user typed wrong; surface it.

### Sources

Kent Beck (YAGNI); Robert C. Martin (SOLID, *Clean Code*); Sandi Metz ("The Wrong Abstraction", 2016); Eric Raymond *The Art of Unix Programming* (2003); David L. Parnas (modularity, 1972); Bertrand Meyer *OOSC* (Command-Query Separation, 1988); Donald Knuth + Tony Hoare (premature optimization); Jon Postel (RFC 760, robustness); Eric Allman (ACM Queue 2011, robustness reconsidered); 37signals/DHH (Convention over Configuration). Full citations: `../research/principles/development-principles-research.md §1`.

---

## §2. Boundary discipline — thin interface layer

**Principle.** CLI commands and the (planned) public Go API are thin wrappers around the domain layer. Their allowed code is exactly: parse arguments → call one or two domain methods → format output. No business logic at the interface layer. No backend types past the runtime boundary. The same domain function is invoked from both surfaces, which guarantees behavioural consistency by construction.

### Pattern

Import direction is strict:

```
cmd/yoloai/main.go     → internal/cli/  + internal/sandbox/
internal/cli/          → internal/sandbox/ + internal/agent/ + internal/config/
internal/sandbox/      → internal/runtime/ + internal/agent/ + internal/config/
internal/runtime/<be>/ → backend SDK + internal/runtime/ (interfaces only)
internal/config/       → (leaf — nothing internal)
```

No reverse imports. No circular imports. Backend types do not appear outside `internal/runtime/<backend>/`. Backend name strings do not appear in the dispatch (W10).

### What does NOT live in the interface layer

1. Value validation beyond shape (e.g., regex-validating a sandbox name is fine at the CLI; deciding whether the name *means* something is the domain's job).
2. Backend calls (CLI never calls Docker / Podman / containerd directly).
3. Conditional business decisions ("should this proceed?" lives in domain).
4. Cross-domain orchestration (lives in a third domain package that imports both — not in the handler).

### Worked examples

- `yoloai apply` (CLI) → `sandbox/patch.Apply` (domain) → `runtime.Runtime.GitExec` (backend boundary). The CLI parses flags, the domain assembles the patch, the runtime executes git. None of those three reach across.
- The W12 architecture remediation (commits `a3207eb`, `e100e4d`, `ccde491`, 2026-05-20) carved `sandbox/archetype/`, `sandbox/patch/`, `sandbox/store/` as subpackages. Each has a clean import boundary; subsequent changes to one don't ripple.
- W10 (commit `5f91cdf`, 2026-05-20) closed three backend-name leaks — `if backend == "docker"` branches that had crept into CLI / domain code. Replaced with capability checks or registry queries.
- W11 (commits `3b4a9ae`, `d525d60`, `c00d367`, `1f4457c`, 2026-05-20) introduced `BackendDescriptor` and a `(factory, descriptor)` registry. Adding a backend is now purely additive — register the descriptor, no dispatch edits.

### Cost-vs-benefit

Cost of applying: discipline at design time + occasional refactor when a boundary leaks. Damage prevented: the boundary-violation tax (every backend change forces edits in unrelated packages); divergent CLI vs. API behaviour (the two surfaces drift unless they share a single code path); tests that have to mock too many things because the layers are tangled.

### Sources

David L. Parnas "On the Criteria To Be Used in Decomposing Systems Into Modules" (CACM, 1972); Robert C. Martin SOLID (objectmentor.com 1995–2002); Effective Go §Interfaces. Project D7, D12, D13, D19. Full citations: `../research/principles/development-principles-research.md §2`.

Originally established alongside D7 (pluggable runtime interface).

---

## §3. Validate at every layer (defense in depth)

**Principle.** Every function that crosses a layer boundary validates its inputs against the domain's invariants. The runtime layer validates inputs even if the domain "already did" — the runtime doesn't know what called it. The domain validates inputs even if the CLI handler did — the handler might be a refactor that lost the check, or a new public-API caller that never had it.

This isn't paranoid; it's the same reason server-side validation matters when the frontend already validated.

### Pattern

A validation function lives with each domain concept (sandbox name, mount mode, path safety). Every boundary calls it. Errors are typed (`internal/yoerrors` per W7) so callers can branch on cause, not error-message-matching (W8).

### Worked examples

- **Sandbox name validation** (D10, commits `b75e2ec` + `01bfe81`, 2026-02-28). The regex check happens at every CLI entry point that takes a name. Path-traversal-shaped names never reach `os.MkdirAll`.
- **Symlink resolution before safety checks** (D6, commit `67826e0`, 2026-02-23). Path inputs go through `filepath.EvalSymlinks` *before* dangerous-directory detection. A "safe-looking" symlink to `/etc` is refused on the resolved path.
- **Dangerous-directory refusal** (`docs/design/security.md` §Security Considerations). Refusal happens at the mount-decision layer, not deferred to the kernel's mount syscall.
- **Capability-required vs. capability-available** (`runtime.RequiredCapabilities` in W11 step 3): the domain checks the backend descriptor before requesting a feature that needs a capability. Caller sees a clean error instead of a half-started container.

### Cost-vs-benefit

Cost of applying: validation function alongside each domain concept; the discipline to call it at every boundary. Damage prevented: a refactor that drops a single check exposing the rest of the stack to bad input; bad input flowing far enough into the system to corrupt state; debug sessions that end at "oh, this layer assumed the layer above already validated."

### Sources

David L. Parnas (1972); OWASP secure-coding guidance; project D6 + D10. Full citations: `../research/principles/development-principles-research.md §3`.

---

## §4. Parse, don't validate

**Principle.** When data crosses a trust boundary or has invariants downstream code depends on, transform it into a distinct domain type whose existence proves the invariants hold. Downstream code takes the parsed type, not the raw input. The function that constructs the type is the *only* place the checks happen; once a value has the parsed type, no further validation is needed at every call site.

Adopted from Alexis King, "[Parse, don't validate](https://lexi-lambda.github.io/blog/2019/11/05/parse-don-t-validate/)" (2019). The original argument is framed in Haskell; Go cannot enforce the discipline at the type level the way Haskell does, but the convention is still load-bearing.

### Pattern

For every domain concept yoloAI cares about:

1. *Define a distinct type*, not a primitive alias. `type SandboxName struct { value string }` — unexported field, lives in the package that owns the parser.
2. *Construct exclusively through the parser*. `func ParseSandboxName(raw string) (SandboxName, error)`.
3. *Take the parsed type at every downstream boundary*. `func (m *Manager) Create(name SandboxName, ...) error` — not `name string`.
4. *Return distinct error types from the parser*. `ErrInvalidName`, `ErrNameTooLong`, `ErrNameReserved` — caller handles each per its semantics.
5. *Parse once, at the boundary; use the parsed type internally*.

### Concrete surfaces in yoloAI

| Concept                           | Raw form          | Parsed type             | Parser proves                                     |
| --------------------------------- | ----------------- | ----------------------- | ------------------------------------------------- |
| Sandbox name (D10)                | `string`          | `SandboxName`           | Regex, length, reserved-name                      |
| Workdir path                      | `string`          | `ResolvedPath`          | Symlink-resolved (D6), not a dangerous-dir       |
| Mount mode (`copy`/`overlay`/...) | `string`          | `MountMode`             | Enum value validated                              |
| Network allowlist domain          | `string`          | `AllowedDomain`         | Valid hostname, not localhost (commit `4d9f7f6`)  |
| Agent name                        | `string`          | `AgentName`             | Known agent in the registry                       |
| Backend descriptor (W11)          | (factory return)  | `BackendDescriptor`     | Capabilities enumerated                           |
| Patch (D9)                        | `[]byte`          | `Patch` (`patch/`)      | Valid `git format-patch` output                   |

Go limits (named in the research file): same-package construction is unrestricted; no compile-time proof; `any`-typed maps and reflection can bypass. Mitigation: parser and type live in a small dedicated package so accidental bypass is hard.

### Worked examples

- `internal/sandbox/name.go` — `SandboxName` type with `Parse` constructor (D10).
- `internal/config/path.go` — resolved-path types after tilde expansion + env-var interpolation (commits `25fcb82`, `a57d765`, 2026-02-27).
- `internal/runtime/registry/descriptor.go` — `BackendDescriptor` constructed only by registered factories (W11 step 4).
- Reject-don't-strip: invalid input fails parsing; we never silently sanitise. Stripping is what attackers (and confused users) count on.

### Cost-vs-benefit

Cost of applying: more types, more parser functions, more import boundaries. Damage prevented: duplicate validation logic drifting out of sync; "I forgot to validate this path" bugs; the call site that takes `string` because the original author didn't know it should be checked.

### Sources

Alexis King "Parse, don't validate" (lexi-lambda.github.io, 2019); David L. Parnas (1972); project D6 + D10. Full citations: `../research/principles/development-principles-research.md §4`.

---

## §5. Fail fast — surface errors at the earliest possible point

**Principle.** Detect and surface errors at the earliest possible point. Validate at the boundary before bad input reaches domain logic. Return errors immediately; don't carry partial state forward. Reject invalid construction arguments in constructors rather than storing bad state for a later crash. Don't paper over failures — let them be visible.

### Pattern

Threshold: when in doubt, error. Silent fallbacks are a future debugging session. The principle composes with `general-principles.md §9` (surface failures honestly) — surface the diagnostic specifically, don't catch-and-ignore.

### Worked examples

- **Pre-flight checks**: the disk pre-flight (D21, commit `0d8d650`) refuses to start an operation that would fail mid-way. Two-stage smoke sentinel distinguishes "test never started" from "test started but didn't finish."
- **Iptables setup fails loudly** (commit `bc512b9`, 2026-05-20): network-isolation rule install errors abort the sandbox; the container does not start with broken isolation.
- **Container start failure capture** (commit `387f278`, 2026-03-17): logs are captured *before* the container is removed, so the cause is in the bug report.
- **gVisor on macOS refusal** (commit `d078db6`, 2026-03-17): we know it hangs, so we refuse upfront with a pointer to the upstream issue rather than letting users hit an infinite loop.
- **Reject invalid model URLs for containerised backends** (commit `10e781d`, 2026-03-01): a localhost model URL is invalid when the agent runs in a container; refused at parse with a backend-specific hint.
- **Stop-hook + make check** (D20, commit `bf5c79e`): a Claude Code Stop hook runs `make check` before AI completion; failures block the agent finishing the turn.

### Cost-vs-benefit

Cost of applying: more error paths to write and test. Damage prevented: silent corruption; cascading failures from partial state; debug sessions where the symptom is far from the cause.

### Sources

Effective Go §Errors; Bertrand Meyer *Object-Oriented Software Construction* — Design by Contract (1988/1997); project D21 + global `CLAUDE.md` testing principles. Full citations: `../research/principles/development-principles-research.md §5`.

---

## §6. Warnings are signal; suppressions require justification

**Principle.** Lint findings, complexity alerts, security scanner warnings, and type errors are information about the code. Suppressing one without understanding what it's telling you discards that information permanently, silently, and often incorrectly. The fact that a suppression makes the checks pass is not a reason to add it; it is the definition of what suppressions do.

Every suppression directive — `//nolint:lintername`, `#nosec`, `// nolint:cyclop`, `//noinspection`, any complexity threshold override — must be accompanied by a co-located comment explaining **why the finding does not apply here**, not why the directive was added.

### Acceptable reasons

- *Tool false positive*: "`noctx` flags this because the function signature doesn't take a `ctx`, but this is a CLI command body that uses `cmd.Context()` — the tool doesn't model that pattern."
- *Intentional trade-off with documented reasoning*: "`gocyclo` exceeds threshold here; this switch is the canonical dispatch table for backend selection — splitting it across functions would scatter the cases without reducing actual complexity."
- *Known external-library quirk*: "`deadcode` flags this function; it is called only via the runtime registry at startup, not visible to static analysis."

### Unacceptable reasons

- "Makes CI pass."
- "Linter complained."
- No comment at all.
- A comment that just restates what the directive does ("disable gocyclo check").

### Worked examples

- `bf5c79e` (2026-05-20) — `make check` enforcement via Stop hook. If lint suppression bypasses the check, the next contributor can't tell which suppressions are intentional.
- The `.golangci.yml` curated list (commits `7add069`, `576ac9c`) — each enabled linter is there because its findings are presumed meaningful.
- W9 (commit `5038ca0`, 2026-05-20) configured `sloglint` with the canonical `err` key. Code that doesn't conform gets flagged; the fix is to conform, not to suppress.

### Cost-vs-benefit

Cost of applying: a few extra characters per suppression. Damage prevented: silent accumulation of suppressions that hide real bugs; the future developer who reads `//nolint:gocyclo` and doesn't know whether to trust it; the technical debt that piles up when "linter complained" is a valid reason to silence it.

### Sources

`.golangci.yml` (project config); Effective Go; Uber Go Style Guide §Avoid using lint directives unnecessarily. Full citations: `../research/principles/development-principles-research.md §6`.

---

## §7. Act on every return value; justify every discard

**Principle.** Every return value carries information. Discarding it silently discards that information — permanently, invisibly, and often incorrectly. A caller that drops a return value without explanation has made an implicit claim that the value doesn't matter; state and defend that claim, don't assume it.

Most critical for error returns. A silently dropped error is not a handled error; it is a deferred surprise.

### Go-specific notes

Go's blank identifier `_` is a deliberate-discard signal, not a free pass. For non-trivial discards, accompany `_` with a comment explaining why the value is irrelevant here.

Idiomatic exceptions where `_` needs no comment:
- Range loops where only the index or value is needed.
- Blank receiver in interface assertions: `var _ runtime.Runtime = (*docker.Backend)(nil)`.

Deferred `Close()` on read-only resources is a documented acceptable case:

```go
defer f.Close() //nolint:errcheck // read-only file; Close errors don't affect data already read
```

### Pattern

```go
// Wrong — swallowed error.
w.Write(data)

// Wrong — swallowed with false reassurance.
_ = w.Write(data)

// Correct — explicit handling.
if _, err := w.Write(data); err != nil {
    return fmt.Errorf("write response: %w", err)
}

// Correct — writable file: handle the Close error.
defer func() {
    if cerr := f.Close(); cerr != nil && err == nil {
        err = cerr
    }
}()
```

### Worked examples

- `05701e0` (2026-03-03, "Replace silently-ignored `os.UserHomeDir` errors with `config.HomeDir`") — fixed a class of silent error discards in the config layer. The replacement makes the failure visible.
- `a11a3dc` (2026-03-03, "Document intentional error handling in diff, files, and setup") — the cases where the discard is intentional are documented in-line.
- The `.golangci.yml` `errcheck` linter is enabled; every ignored error in the codebase bears a `//nolint:errcheck` directive with a justification comment.

### Cost-vs-benefit

Cost of applying: explicit error handling, occasionally a few lines of plumbing. Damage prevented: errors that go nowhere, leaving the system in undefined state; debug sessions that end with "oh, that path silently failed."

### Sources

Effective Go §Errors; Go Code Review Comments §Don't panic; `.golangci.yml` errcheck. Full citations: `../research/principles/development-principles-research.md §7`.

---

## §8. No half-finished implementations

**Principle.** Public-beta status (`CLAUDE.md` §Project Status) means breaking changes are allowed if tracked. It does *not* mean half-implemented features are allowed. A feature is either shipped (works, has tests, is documented) or removed. The state-in-between is the worst — it confuses users and traps maintenance time.

### Pattern

Threshold: if a feature can't be completed in this change unit, it doesn't land. Partial code with a `// TODO: hook this up` is acceptable only when the missing piece is tracked in `docs/dev/plans/TODO.md` with an owner and a target. Hidden `// TODO` notes without tracking are not.

Breaking changes are explicit: documented in `docs/BREAKING-CHANGES.md` with previous behaviour, new behaviour, rationale, migration steps. Don't break silently.

### Worked examples

- `be22f6a` (2026-03-10, "Remove all legacy backwards compatibility") — the `runtime-config.json` fallback was added in `fdfe0c3` and removed seven minutes later (D16). The fallback wasn't earning its place; it shipped, was reconsidered, and removed.
- `0d1c72e` (2026-03-02) — "Remove stale `copy_strategy` references from docs" cleaning vestigial language from when a deprecated option was removed.
- `c7f9f8a` (2026-03-12, "Remove 'old behavior' and 'legacy' language from help text and docs") — same shape, applied to user-facing strings.
- `docs/dev/plans/TODO.md` is the consolidated list of designed-but-unimplemented features. When something is deferred, it goes there with a design reference, not buried as a comment.
- The `setup_complete` operational state in `~/.yoloai/state.yaml` (commit `45ed6ef`, 2026-02-26) is fully wired; first-run flow is complete or it's not enabled.

### Cost-vs-benefit

Cost of applying: occasionally a slightly-larger change unit. Damage prevented: vestigial code accumulating in the codebase; `// TODO` comments that grow into archaeology; users reporting bugs in features that were never finished; breaking changes shipped silently and discovered in production.

### Sources

Project `CLAUDE.md` §Project Status; D16; `docs/BREAKING-CHANGES.md` convention. Full citations: `../research/principles/development-principles-research.md §8`.

---

## §9. Plan-then-execute on cleanup

**Principle.** Periodic architecture cleanup is the project's right shape — not opportunistic refactors. Architecture drift accumulates; the W-numbered remediation plan ensures the cleanup lands as a coherent shape, not as a string of half-overlapping commits.

### Pattern

When the architecture has drifted enough that a single contributor (or AI agent) can't predict where to make a change, run an audit. The audit produces a numbered work plan (`W1`, `W2`, …). Each W is a discrete commit. Status tracked in `docs/dev/architecture-audit-<period>.md` and the memory entry `project_arch_remediation.md`.

### Worked examples

- **Architecture audit, 2026-05** (`868a5b0`, 2026-05-20) → W1–W14 (commits `4fa683f` … `1f4457c`, 2026-05-20). Each W is a tight, single-responsibility commit with `make check` passing.
- W7 (commit `a22878d`) moved typed errors to `internal/yoerrors` — single source for error categorisation, no more `errors.Is(err, fmt.Errorf("not running"))` text-match anti-patterns.
- W11 progressed in four steps (`3b4a9ae`, `d525d60`, `c00d367`, `1f4457c`): additive descriptor → migrate callers → optional interfaces → registry tuples. Each step independently shippable.
- W12 carved subpackages (`a3207eb`, `e100e4d`, `ccde491`). Each carve produced a cleaner import graph with `make check` passing.

### Cost-vs-benefit

Cost of applying: a few days of audit + plan + numbered execution. Damage prevented: gradual code drift that becomes a "rewrite the world" project; refactor commits that partially overlap and produce confusing histories; the indefinitely-postponed cleanup that never lands.

### Sources

D19; commit pattern visible in `git log --grep '^Refactor: W'`. Full citations: `../research/principles/development-principles-research.md §9`.

---

## §10. Code quality gate — `make check`

**Principle.** `make check` is the single quality gate. It runs gofmt, golangci-lint, go mod tidy verification, all Go tests, and the Python pytest + mypy targets. All must pass before any change is considered complete. CI runs the same targets, so green locally means green in CI.

### Pattern

`make check` is invoked manually before commit and automatically by the Claude Code Stop hook (D20, commit `bf5c79e`, 2026-05-20) — the hook blocks completion if the check fails and feeds the output back. The hook scripts at `.claude/hooks/post-edit.sh` and `.claude/hooks/on-stop.sh` are committed so every clone inherits the gate.

Skipping the gate is not an option for AI-assisted edits. Project `CLAUDE.md` §Code Quality Gate states this as a project rule.

### Worked examples

- `bf5c79e` introduced the Stop hook; the rule applies retroactively to every Claude Code session.
- `7add069` (2026-05-20) added `cyclop`, `exhaustive`, `durationcheck` linters; all existing violations were fixed in the same commit before the linters were enabled. The gate doesn't get loosened to land code.
- `576ac9c` enabled `gocognit` (min-complexity=20) with all existing violations resolved (`ba8dcc5`, `864972d`).
- Python `pytest` + `mypy` targets skip silently if the deps aren't installed, so fresh clones still get green `make check` (`CLAUDE.md` §Code Quality Gate notes this). CI installs the deps and treats both targets as required.

### Cost-vs-benefit

Cost of applying: occasional friction when `make check` fails. Damage prevented: linter-rot, format drift, untested code shipping, the cascade of "I'll fix it next commit" that doesn't happen.

### Sources

Project `CLAUDE.md` §Code Quality Gate; D20; the hook scripts at `.claude/hooks/`. Full citations: `../research/principles/development-principles-research.md §10`.

---

## §11. Iterate when the first approach doesn't work

**Principle.** When a problem resists three attempts at solution, the answer is not "try harder at the same approach." It's "step back and rethink the architecture." Some problems have a structural shape that the obvious solution doesn't match; the right answer is to find the structure, not to grind on the obvious.

### Pattern

Threshold: three failed attempts at the same approach is the trigger. Stop, write down what didn't work and why, and look for the structural insight the failures share. The failure trail is data, not waste; preserve it in `docs/dev/research/` or `docs/dev/working-notes.md` so the lesson survives.

### Worked examples

- **Pluggable idle detection** (D14, commit `dbec36f`, 2026-03-08). Tried: tmux `window_bell_flag` polling (broken — `pane_last_activity` doesn't update for TUI agents); fixed-delay polling (flapped); single global detector (wrong for hook-supporting agents like Claude Code). The fourth approach — per-agent `IdleSupport` strategy with a Python `status-monitor` writing `agent-status.json` — worked because it acknowledged the structural truth: no single signal works across agents.
- **gVisor on macOS** (D17 follow-up, commit `d078db6`). Tried multiple ARM64 PTY / setsid / socket-permission workarounds (commits `32ee4e7`, `f381305`, `fdc6db6`, `90892f9`). The Claude Code infinite-`epoll_pwait` bug is upstream; no in-yoloAI workaround was going to fix it. The fix was to refuse with a pointer to the upstream issue.
- **Network isolation** (D11). The Go proxy sidecar was planned (commit `5e5cca3`, 2026-02-23) and rejected in favour of iptables + ipset after prototyping (commit `ed19f9d`, 2026-03-01) — the iptables approach covered the threat model at a fraction of the complexity.
- The 12 rounds of pre-implementation critique (D2) are this principle applied at the design layer: each round was a rethink, not a re-grind.

### Cost-vs-benefit

Cost of applying: the discipline to stop after the third failure and rethink. Damage prevented: the spiral of "one more workaround" that produces fragile, undocumented code; the lost opportunity to find a simpler structural answer.

### Sources

Global `CLAUDE.md`: "If an approach isn't working or feels overcomplicated, stop and rethink rather than immediately switching tactics." Project D14, D11. Full citations: `../research/principles/development-principles-research.md §11`.

---

# Common over-generalisations to avoid

| Over-generalisation                          | Why yoloAI rejects                                                                                                                                                                                                                                          |
| -------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **DRY-at-all-costs**                         | Sandi Metz: duplication is cheaper than the wrong abstraction. yoloAI accepts limited cross-backend duplication when the abstraction's coupling cost would exceed the redundancy cost.                                                                       |
| **SOLID-as-religion**                        | SOLID is shorthand for sensible decoupling, not a checklist. Forcing Single Responsibility to absurdity produces hundreds of one-method types; forcing Interface Segregation produces interfaces with one method each. Use as guidance, not as a hammer.    |
| **Validate-everywhere-without-thinking**     | §3 is "validate at every layer crossing," not "validate every value every call." Validation has cost; the cost is paid where it earns its place — at trust boundaries, not at internal-method-call boundaries.                                              |
| **Parse-everything**                         | §4 applies to data crossing trust boundaries. Don't define a wrapper type for every primitive — the cost-benefit only pays for values whose invariants matter downstream.                                                                                    |
| **Fail-fast-at-the-cost-of-resilience**      | §5 is about diagnosability, not brittleness. A transient network blip should retry; an invalid input should fail. The distinction is whether re-trying could plausibly succeed.                                                                              |
| **Justify-every-line-with-a-comment**        | The standard is "comments justify the non-obvious." Naming and structure carry the obvious meaning. Over-commenting noise drowns out the comments that matter.                                                                                              |
| **No-suppressions-ever**                     | §6 is "justify, don't ban." A real linter false-positive earns a suppression with a real comment. The rule is no *unjustified* suppressions.                                                                                                                 |
| **Refactor-opportunistically-during-features** | §9 — refactoring during a feature change couples two changes that should be independent. The W-numbered cleanup model exists precisely so refactor commits land alone.                                                                                       |
| **`make check`-everywhere-no-exceptions**    | §10 — `make check` is the gate for code changes. Docs-only changes (this file) don't strictly need it. The hook system stamps the project when source files are edited; doc edits don't stamp.                                                              |
| **Iterate-forever**                          | §11 says rethink after three failed attempts, not "keep trying new tactics indefinitely." Sometimes the right answer is to acknowledge the problem is upstream (gVisor on macOS) and stop.                                                                  |

---

## Closing note

The development principles parallel the general / testing / security principles in shape: framing, threshold per principle, worked examples grounded in commits and D-entries, over-generalisations to avoid. The specialised relationship:

- `general-principles.md §3` (don't reinvent the wheel) is the strategic version of this doc's "use boring stdlib + ecosystem tools."
- `testing-principles.md` is the safety net that makes the boundary discipline (§2) and refactor-in-cleanup-batches (§9) actually safe.
- `security-principles.md` cites this doc's validate-at-every-layer (§3) and parse-don't-validate (§4) as the structural enforcement of containment.
- `../standards/GO.md` (planned) is the concrete style and naming that grounds these principles in everyday Go code.
