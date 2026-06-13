ABOUTME: Engineering practice for yoloAI. YAGNI/KISS/DRY/SOLID vocabulary,
ABOUTME: boundary discipline ("none of your business" — comply-or-complain),
ABOUTME: validate-at-every-layer, parse-don't-validate, fail-fast,
ABOUTME: warnings-are-signal, justify-every-discard, no-half-finished,
ABOUTME: plan-then-execute cleanup, make-check gate, iterate-when-first-approach
ABOUTME: -fails, raw-until-it-has-to-change, library-defaults-are-safety-only,
ABOUTME: name-for-the-reader's-distance.
ABOUTME: How to write yoloAI code so future-you can change it safely.

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
- `../standards/GO.md` and `../standards/CLI.md` — concrete style and naming rules.

---

## §1. Engineering values — the cross-cutting vocabulary

> **Rule.** The shared vocabulary the other sections draw on — YAGNI, KISS, DRY, SOLID, Demeter, least-astonishment, command/query separation. Default to the simplest correct solution; earn an abstraction on the *second* concrete use, not the first.
>
> **Bites when:** adding an abstraction, generalisation, or config knob for a single (or hypothetical) use case. · **See also:** §8, §9.

A concise reference for the engineering values that shape every code path in yoloAI. Most aren't yoloAI-specific; they're the established vocabulary of professional software engineering.

**YAGNI — You Aren't Gonna Need It.** Don't build for hypothetical future requirements. Features, abstractions, and generalisations earn their place when a second concrete use case appears — not before. For a single-author project, every unused capability is maintenance Karl pays per backend.

**KISS — Keep It Simple.** The simplest solution that correctly satisfies the requirement is the right solution. Prefer flat over nested, explicit over clever, boring over novel. A future maintainer (or future-Karl debugging at 2am) should be able to read any code path and understand it without surprises.

**DRY — Don't Repeat Yourself.** Every piece of knowledge has one authoritative location. Duplicated logic means that when the knowledge changes, it must be found and applied in N places — and N−1 will be wrong. But — Sandi Metz: "duplication is far cheaper than the wrong abstraction." Extract on the second concrete use case; don't extract preemptively.

**SOLID.** Five principles for package and type design:
- *S — Single Responsibility*: each package, type, and function has one reason to change. `internal/runtime/` is runtime backends; `internal/orchestrator/` is sandbox lifecycle; `internal/cli/` is the CLI surface. Mixing these creates coupling that blocks change.
- *O — Open/Closed*: extend behaviour by adding code, not modifying stable core paths. New backends add a `runtime.Backend` implementation; they don't edit the dispatch. New agents add agent definitions; they don't edit the agent registry's internals.
- *L — Liskov Substitution*: every implementation of `runtime.Backend` must be fully substitutable. A backend that panics on `Stop()` is not a valid `Backend`; it violates the contract the caller relies on.
- *I — Interface Segregation*: narrow interfaces at consumption sites. The CLI takes a `runtime.Backend`, not `*runtime.docker.Backend`. Optional behaviours (e.g., overlay support) live behind optional interfaces (W11 step 3).
- *D — Dependency Inversion*: depend on interfaces, not concrete types, at every significant boundary. Runtime backends, agent definitions, idle detectors, store helpers — all interface-mediated. The composition root (`internal/cli/helpers.go::newRuntime`) is the only place concrete types are wired.

**Separation of Concerns.** Each layer has one job: CLI handlers parse args and format output; domain packages hold business logic; runtime backends call the underlying daemon. Mixing these creates coupling that prevents independent testing and evolution. See §2 for the project-specific expression.

**Law of Demeter.** A method should call methods on its own direct collaborators, not reach through them. `runtime.Stop(name)` is correct; `runtime.dockerClient.containerService.stop(...)` reaches through an intermediary and exposes internal structure.

**Fail Fast.** Detect and surface errors at the earliest possible point. Validate inputs at the boundary before they reach domain logic; return errors immediately rather than carrying partial state forward; reject invalid construction arguments in constructors rather than storing bad state for a later crash. See §5.

**Principle of Least Astonishment.** Code should behave the way a reader expects. A function named `Resolve` should resolve, not mutate. A middleware named `RequireAuth` should reject unauthenticated requests. An error named `ErrNotFound` should mean not-found, not a transient failure.

**Encapsulation and Information Hiding.** Internal implementation details should not leak to callers. Unexported struct fields, package-private constructors, and narrow interfaces all serve this goal. Backend-specific types (Docker SDK structs, Tart VM handles, Seatbelt SBPL profiles) never leak past their `internal/runtime/<backend>/` package boundary.

**Convention over Configuration.** Established Go conventions and project standards are followed by default. Diverging from a convention requires a documented reason. Convention reduces cognitive load — a developer reading unfamiliar code in a familiar structure spends mental energy on the logic, not the layout.

**Tell, Don't Ask.** Tell objects what to do rather than asking for their state to decide for them. Business logic about a sandbox's state belongs inside the sandbox domain, not in CLI handlers that query fields and branch on them.

**Premature Optimization is the Root of All Evil.** Write for correctness and clarity first; optimise only when measurement identifies a real bottleneck. Structural decisions (algorithm choice, copy-vs-clone, N+1 patterns) belong in the design phase; micro-optimisations belong nowhere until they're measured to matter.

**Command–Query Separation.** A function either changes state (command) or returns information (query) — not both. Mixing them makes reasoning harder: a caller asking a question shouldn't worry about hidden side effects. Where a command must also return data (e.g., `Create` returns the new sandbox handle), document the exception.

**Robustness Principle.** Be conservative in what you emit, liberal in what you accept. Apply with care — the modern critique (Allman 2011) is that "liberal" can become "buggy by design." For yoloAI: accept legitimate input variation (e.g., paths with trailing slashes, different YAML quoting styles); reject invalid input at the boundary. Don't auto-fix what the user typed wrong; surface it.

### Sources

Kent Beck, Robert C. Martin, Sandi Metz, Parnas, Meyer, Postel, Knuth/Hoare. Full citations: `../research/principles/development-principles-research.md §1`.

---

## §2. None of your business — boundary discipline, comply-or-complain

> **Rule.** Policy owns *what* and *why*; mechanism owns *how*; neither questions the other. The only thing crossing back up is a typed refusal — the library never prompts, falls back, reinterprets intent, or makes a UX choice, and policy never reaches into the mechanism's internals (the fix for a missing capability is a new public verb, not a reach-through).
>
> **Bites when:** the library prompts / silently falls back / mode-switches, or the CLI imports the engine/runtime subtree instead of calling a `yoloai.*` verb. · **See also:** §4, §12, §13.

**Principle. None of your business.** The boundary has two sides under one contract, and each is deliberately ignorant of the other. The **upper (policy) layer** owns the *what* and the *why*; the **lower (mechanism) layer** owns the *how*. Neither questions or depends on the other's reasons. The *only* thing that crosses in the reverse direction is a typed refusal — *"you've asked for the impossible, and here's why."* Everything else — the upper layer's motives, the lower layer's implementation — is none of the other's business.

The **policy layer** — CLI commands, the public Go API entry points, embedders — decides *what* to do and *how to react*: which domain operation to call, whether to prompt, whether to fall back, how to format output. It stays thin: parse arguments → call one or two domain methods → format output. No business logic; no backend types past the runtime boundary. The same domain function is invoked from every surface, so behaviour is consistent by construction. And it is forbidden to reach into the *how*: it names a domain verb and consumes its result-or-typed-error; it never imports, inspects, or reconstructs the mechanism's internals. When the policy layer needs something the mechanism doesn't yet expose, the fix is a new public verb on the lower layer — not a reach-through (D56).

The **mechanism layer** — the domain/library — does exactly what it is asked, or **complains** with a typed error. *Comply-or-complain: never a silent third thing.* It does not prompt, reinterpret intent, switch modes, fall back, or make UX choices — those are all policy, owned by the caller. "Can't comply" is always a typed refusal the caller handles; it is never papered over with a guess. It also never asks *why*: the caller's motive is none of its business, so it cannot make a decision that depends on one.

### Pattern

Import direction is strict. The **public surface** lives at the module root: the `yoloai` package itself (the Layer-1/2 contract) and the `yoerrors/` error-sentinel package. Everything else is private under `internal/` — the embedders (`internal/cli/`, `internal/mcpsrv/`), the engine (`internal/orchestrator/` and its `store`/`copyflow`/`archetype`/`lifecycle`/`launch`/`status`/`create` subpackages), the runtime backends (`internal/runtime/` + `docker`/`tart`/`seatbelt`/`containerd`/`podman`/`caps`), and the support packages (`internal/agent/`, `internal/config/`, `internal/workspace/`, `internal/fileutil/`, `internal/testutil/`). The layering:

```
cmd/yoloai/main.go         → internal/cli
internal/cli/, mcpsrv/     → yoloai (root) + yoerrors    (fenced OFF internal/orchestrator + internal/runtime — D57)
yoloai (root)              → internal/orchestrator + internal/runtime + internal/config + yoerrors
internal/orchestrator/     → internal/runtime + internal/agent + internal/config + internal/workspace
internal/workspace/        → internal/config   (git via os/exec; no runtime dep)
internal/runtime/<backend> → backend SDK + internal/runtime (interfaces only) + internal/config (leaf types)
internal/agent/            → internal/config   (agent definitions reference config types)
internal/config/           → (leaf — nothing internal)
```

No reverse imports. No circular imports. Backend types do not appear outside `internal/runtime/<backend>/`. Backend name strings do not appear in the dispatch (W10). The embedders reach the engine **only** through the root `yoloai` package — the depguard fences (`cli-sandbox-scope`, `cli-runtime-scope`, D57) forbid `internal/cli`/`internal/mcpsrv` from importing the engine or runtime subtrees directly, which is what makes the CLI a faithful proxy for a future separate-module daemon. `internal/config/` is depended on by everyone but depends on nothing internal — it carries the leaf types (paths, profile names, mount specs) that compose the rest.

### What does NOT live in the policy (interface) layer

1. Value validation beyond shape (e.g., regex-validating a sandbox name is fine at the CLI; deciding whether the name *means* something is the domain's job).
2. Backend calls (CLI never calls Docker / Podman / containerd directly).
3. The permissibility **rule**. Whether an operation is *allowed* (workdir dirty, sandbox has active work, target isn't a git repo) is the mechanism's call — it enforces the rule by refusing with a typed error. The policy layer owns only the *reaction* to that refusal (override, prompt, fall back), not the rule itself.
4. Cross-domain orchestration (lives in a third domain package that imports both — not in the handler).

### What the mechanism (domain) must NOT do

The flip side — the comply-or-complain contract spelled out:

1. **Prompt.** The library never reads from a terminal to ask "are you sure?". It refuses with a typed error and lets the caller prompt (D24).
2. **Reinterpret intent / mode-switch / fall back.** Asked to replay a commit series onto a non-git target, it refuses (`*UsageError`) rather than silently switching to a net-diff apply; the CLI decides the fallback (D26). No ambient defaults (F4).
3. **Make UX / formatting choices.** No human-readable strings, no "(dry run)" banners, no JSON shaping — it returns data + typed errors; the policy layer renders.
4. The escape hatch is always an **explicit, named option** the caller sets (`AllowDirtyWorkdir`, `NoCommit`, `Force`), never an implicit behavior the library chooses on the caller's behalf.

### Worked examples

- `yoloai apply` (CLI) → `internal/copyflow.Apply` (domain) → `runtime.Backend.GitExec` (backend boundary). The CLI parses flags, the domain assembles the patch, the runtime executes git. None of those three reach across.
- The W12 architecture remediation (commits `a3207eb`, `e100e4d`, `ccde491`, 2026-05-20) carved `internal/orchestrator/archetype/`, `internal/copyflow/`, `internal/store/` as subpackages. Each has a clean import boundary; subsequent changes to one don't ripple.
- W10 (commit `5f91cdf`, 2026-05-20) closed three backend-name leaks — `if backend == "docker"` branches that had crept into CLI / domain code. Replaced with capability checks or registry queries.
- W11 (commits `3b4a9ae`, `d525d60`, `c00d367`, `1f4457c`, 2026-05-20) introduced `BackendDescriptor` and a `(factory, descriptor)` registry. Adding a backend is now purely additive — register the descriptor, no dispatch edits.

The downward half — policy must not know the *how*:

- **The F1 / Layer-1 carve** (D55, the G7 series) fenced the CLI off `internal/runtime` entirely: handlers speak only the public `yoloai.*` surface (`BackendType`, `IsolationMode`, `SelectBackend`, `System.CheckBackend`), and runtime construction lives behind public verbs. When the CLI needed a backend probe it gained `System.CheckBackend` rather than reaching for `runtime.New` — a reach-through becomes a new public verb. The one sanctioned exception (`internal/cli/system/tart` importing `internal/runtime/tart`) is depguard-scoped to that single package.
- **Enforcement teeth, not just convention:** the F1 leak detector (`TestPublicAPI_NoInternalLeaks`, alias-descent aware) fails the build if a public type exposes an internal one, and depguard fails the build if a leaf package imports across a forbidden boundary. The principle is mechanically checked, so drift surfaces at CI time, not review time.

Comply-or-complain (the mechanism side):

- **Create refuses, never prompts** (D24): a dirty workdir → `*DirtyWorkdirError`, unverified `requires:` → a warning, an active sandbox on destroy → `*ActiveWorkError`. The CLI catches each, prompts, and retries with the named ack (`AllowDirtyWorkdir`, `Force`). The library has no terminal.
- **Apply complains on a non-git target** (D26): `Workdir().Apply` with the default (series replay) on a non-git host path returns a `*UsageError` instead of silently degrading to a net-diff apply; the CLI checks `IsGitRepo` and selects `NoCommit` itself.
- **No ambient backend default** (F4): an empty `Options.BackendType` selects no persistent backend rather than silently falling back to docker — a backend-bound op then returns the typed `ErrBackendRequired`. Backend selection (which probes installed daemons) is policy, resolved at the boundary via the explicit `SelectBackend` helper.

### Cost-vs-benefit

Cost of applying: discipline at design time + occasional refactor when a boundary leaks, and resisting the convenience of a silent fallback or an in-library prompt. Damage prevented: the boundary-violation tax (every backend change forces edits in unrelated packages); divergent CLI vs. API behaviour (the two surfaces drift unless they share a single code path); the footgun where a headless caller silently gets a *different* operation than it asked for (the squash-on-non-git / proceed-on-dirty class); tests that have to mock too many things because the layers are tangled.

### Sources

David L. Parnas (CACM, 1972); Robert C. Martin SOLID; Effective Go. Full citations: `../research/principles/development-principles-research.md §2`.

Originally established alongside D7 (pluggable runtime interface); restated two-sided in D27; named "None of your business" and given its explicit downward half (policy must not know the *how*) in D56.

---

## §3. Validate at every layer (defense in depth)

> **Rule.** Every function that crosses a layer boundary re-validates its inputs against the domain's invariants — the callee never trusts that the caller already checked (the caller may be a refactor that lost the check, or a new public-API path that never had it).
>
> **Bites when:** skipping a check because "the layer above already validated." · **See also:** §4 (the one-time boundary parse vs. §3's repeated guard).

**Principle.** Every function that crosses a layer boundary validates its inputs against the domain's invariants. The runtime layer validates inputs even if the domain "already did" — the runtime doesn't know what called it. The domain validates inputs even if the CLI handler did — the handler might be a refactor that lost the check, or a new public-API caller that never had it.

This isn't paranoid; it's the same reason server-side validation matters when the frontend already validated.

### Pattern

A validation function lives with each domain concept (sandbox name, mount mode, path safety). Every boundary calls it. Errors are typed (top-level `yoerrors` package, per W7/B1) so callers can branch on cause, not error-message-matching (W8).

### Worked examples

- **Sandbox name validation** (D10, commits `b75e2ec` + `01bfe81`, 2026-02-28). The regex check happens at every CLI entry point that takes a name. Path-traversal-shaped names never reach `os.MkdirAll`.
- **Symlink resolution before safety checks** (D6, commit `67826e0`, 2026-02-23). Path inputs go through `filepath.EvalSymlinks` *before* dangerous-directory detection. A "safe-looking" symlink to `/etc` is refused on the resolved path.
- **Dangerous-directory refusal** (`docs/contributors/design/security.md` §Security Considerations). Refusal happens at the mount-decision layer, not deferred to the kernel's mount syscall.
- **Capability-required vs. capability-available** (`runtime.RequiredCapabilities` in W11 step 3): the domain checks the backend descriptor before requesting a feature that needs a capability. Caller sees a clean error instead of a half-started container.

### Cost-vs-benefit

Cost of applying: validation function alongside each domain concept; the discipline to call it at every boundary. Damage prevented: a refactor that drops a single check exposing the rest of the stack to bad input; bad input flowing far enough into the system to corrupt state; debug sessions that end at "oh, this layer assumed the layer above already validated."

### Sources

David L. Parnas (1972); OWASP secure-coding guidance. Full citations: `../research/principles/development-principles-research.md §3`.

---

## §4. Parse, don't validate

> **Rule.** Parse at the boundary into a domain type whose existence proves the invariant; downstream takes the parsed type and never re-validates. Collapse constrained field-combos (mutually-exclusive bools, free-form strings that should be one of N) into typed enums so the invalid state is unrepresentable.
>
> **Bites when:** raw strings or bool-combos flow deep into the API, or one invariant is re-checked at many call sites. · **See also:** §3, §13.

**Principle.** When data crosses a trust boundary or has invariants downstream code depends on, transform it into a distinct domain type whose existence proves the invariants hold. Downstream code takes the parsed type, not the raw input. The function that constructs the type is the *only* place the checks happen; once a value has the parsed type, no further validation is needed at every call site.

The same shape applies to **API-type design even without a trust boundary**: when an Options struct has fields whose valid combinations are constrained (mutually-exclusive booleans, free-form strings that should be one of N values), collapse them into a typed enum so the invalid state is unrepresentable. Callers can't accidentally set both `NetworkIsolated=true` and `NetworkNone=true` if the type doesn't permit that combination. This is the same discipline applied to programmatically-constructed API inputs rather than parsed user input — see the W-L8a entries in the table below.

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
| Sandbox name (D10)                | `string`          | `SandboxName` †         | Regex, length, reserved-name                      |
| Workdir path                      | `string`          | `ResolvedPath` †        | Symlink-resolved (D6), not a dangerous-dir       |
| Mount mode (`copy`/`overlay`/...) | `string`          | `MountMode`             | Enum value validated                              |
| Network allowlist domain          | `string`          | `AllowedDomain`         | Valid hostname, not localhost (commit `4d9f7f6`)  |
| Agent type                        | `string`          | `AgentType`             | Known agent in the registry                       |
| Backend descriptor (W11)          | (factory return)  | `BackendDescriptor`     | Capabilities enumerated                           |
| Patch (D9)                        | `[]byte`          | `Patch` (`internal/copyflow/`) | Valid `git format-patch` output                   |
| Network policy (W-L8a)            | `(bool, bool)`    | `yoloai.NetworkMode`    | "Open / isolated / none" — invalid combo unrepresentable |
| Isolation mode (W-L8a)            | `string`          | `yoloai.IsolationMode`  | One of the five known modes; typo = compile error |
| Apply mode (W-L8a)                | `(bool, string)`  | `yoloai.ApplyMode`      | Default / squash / export; mutex enforced by type |
| Log format (W-L8a)                | `(bool,bool,bool)`| `yoloai.LogFormat`      | Structured / structured-raw / agent / agent-raw — at most one |

Go limits (named in the research file): same-package construction is unrestricted; no compile-time proof; `any`-typed maps and reflection can bypass. Mitigation: parser and type live in a small dedicated package so accidental bypass is hard.

† **Not yet converted.** Sandbox name and workdir path are the two stragglers from this convention — they are still guarded *validate*-style (`store.ValidateName(string) error`; `config.ExpandPath(...)` returns a bare `string`), not parsed into a type. The conversion is tracked as a finding ([findings-deferred.md](../design/findings-deferred.md), DF15) and sequenced with the D58/D59 path-confinement work, where a typed `SandboxName`/resolved-path earns its keep. The inconsistency is itself the hazard: a security-relevant boundary value that validates by a *different convention* than its peers is exactly what a code audit skips over. Ad-hoc, one-off security guards must not accumulate — they get missed.

### Worked examples

- Sandbox name (D10) — **target:** a `SandboxName` parsed type. **Today:** `store.ValidateName(string) error` in `internal/store/paths.go` (validate-style, not yet parsed — see † above).
- Workdir path — **target:** a resolved-path type after tilde expansion + env-var interpolation (commits `25fcb82`, `a57d765`, 2026-02-27). **Today:** `config.ExpandPath(...)` in `internal/config/pathutil.go` returns a bare `string` (see † above).
- `internal/runtime/registry.go` — `BackendDescriptor` constructed only by registered factories (W11 step 4).
- Reject-don't-strip: invalid input fails parsing; we never silently sanitise. Stripping is what attackers (and confused users) count on.

### Cost-vs-benefit

Cost of applying: more types, more parser functions, more import boundaries. Damage prevented: duplicate validation logic drifting out of sync; "I forgot to validate this path" bugs; the call site that takes `string` because the original author didn't know it should be checked.

### Empty string isn't a free default

A corollary worth calling out: `""` is **not** a valid value for typed-name / config / identity fields unless we can demonstrate a clear benefit to admitting it. The "valid combinations" framing above explicitly includes the empty value — an `Options.BackendType BackendType` field with `""` accepted as "use the default" is the same pathology as `(NetworkIsolated bool, NetworkNone bool)` admitting both true.

Concrete failure mode: an embedder constructs `Options{}` and gets *whatever* the library decides "default backend" means today. That decision flows through `runtime.SelectContainerBackend` and ends up depending on which daemons are installed on the host. The caller's Client now silently carries a backend the caller never named. Same shape as ambient HOME (§12): implicit resolution happens in library code, the answer depends on environmental state, the caller can't reason about behavior from inputs alone.

Default rule: **reject empty at the public boundary** (`*UsageError`). The caller picks a value or asks for a sentinel constant. If empty really is meaningful (e.g., "all backends" for a filter), document it explicitly and prefer a typed sentinel (`BackendAll`, `IsolationModeAny`) over the empty string.

Exceptions exist — `Profile == ""` legitimately means "no profile" because there's no other way to say it, and the no-profile case is the common path. The bar is: prove the empty-as-meaningful semantics is the cleanest shape before adopting it. Implicit default behavior tends to become evil — silent fallbacks compound across releases and turn into Hyrum's law magnets.

The one case where the library *may* fill an unset value rather than reject it is a **safety** default — see §14 for the test that bounds it (safety-sensitive field + safe default), and why every *convenience* default belongs in a layer on top instead.

The same rule covers a consequential *mode* with no good default. `ApplyOptions.Mode` (commit-series replay vs. net-diff) is **required** — the zero value is a `*UsageError`, not a silently-chosen mode (D26). The proof it earns the rule: a movable default flipped apply behavior out from under an existing caller (4c-i1: `Workdir().Apply`'s default moved from net-diff to series, silently breaking `apply_squash`), and only an integration test caught it. The CLI, as the policy layer, picks the mode for the user; the library requires it. F4 (`../CRITIQUE.md`, required `Backend`) is the originating worked example.

### Re-exporting contract types: alias by default, mirror on demand

A public contract type (one an embedder holds — an Options struct, a result struct, an enum) can be exposed two ways: a **type alias** to the internal definition (`type ApplyResult = patch.ApplyResult`) or a **hand-written mirror** in the public package with a converter at the boundary. The choice must be made on contract grounds, not on whether the internal struct *happened* to need a field dropped — that effort-driven discrimination is how the surface drifts into an inconsistent mix (A1, 2026-06-03 public-API round).

**Default: alias.** Aliasing exposes the real shape for free, with zero duplication and no boundary converter. It is the YAGNI-correct starting point — start with the interface you are comfortable exposing, and tighten only when a concrete need appears.

**Swap to a hand-written mirror the moment a field must be hidden, renamed, or retyped** for the public surface. The swap is clean for external embedders: they only ever named `yoloai.X` (they cannot name the internal type — it's in `internal/`), so changing `yoloai.X` from an alias to a distinct struct is invisible to them as long as the field *set they use* survives. The only thing that breaks is the field you are deliberately removing — which is the point. The cost paid at swap time is localized: one converter, at the one boundary method that returns the type (which until then could `return result` directly).

**The swap trigger is only partly automatic.** The F1 leak detector (`TestPublicAPI_NoInternalLeaks`) fails the build when a field whose type lives in `internal/…` appears on the public surface — that is the forcing function that catches an internal-*typed* field added to an aliased struct. But it does **not** catch a field of a primitive or already-public type (e.g. a stray `DebugTraceID string`): that publishes silently through the alias. So reviewers must still catch primitive *mechanism* fields by eye, or consciously accept that a stray primitive is low-harm (embedders ignore unknown fields). Aliasing is "start permissive, tighten on demand" with the detector as a *partial* tripwire — not a complete one.

**Always-alias exemption: pure enums / typed strings** (`BackendType`, `AgentType`, `IsolationMode`, `DirMode`, `Status`, `LogSource`, …). The underlying type is `string` with no hidden fields, so there is nothing to leak and a mirror would only force conversion churn at every call site. These alias permanently.

### Sources

Alexis King "Parse, don't validate"; David L. Parnas (1972). Full citations: `../research/principles/development-principles-research.md §4`.

---

## §5. Fail fast — surface errors at the earliest possible point

> **Rule.** Surface errors at the earliest point — validate at the boundary, reject invalid construction args in the constructor, return immediately. Never carry partial state forward or paper over a failure with a silent fallback. When in doubt, error.
>
> **Bites when:** returning nil/zero on a bad state "to handle later," or adding a silent fallback default. · **See also:** §2 (comply-or-complain), §12.

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

Effective Go §Errors; Bertrand Meyer *OOSC* (Design by Contract). Full citations: `../research/principles/development-principles-research.md §5`.

---

## §6. Warnings are signal; suppressions require justification

> **Rule.** A lint / scanner / type warning is information about the code. Every suppression (`//nolint`, `#nosec`, a complexity-threshold override) carries a co-located comment explaining *why the finding doesn't apply here* — never "makes CI pass" or a restatement of the directive.
>
> **Bites when:** adding a suppression or raising a threshold to get the gate green. · **See also:** §7, §10.

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

Effective Go; Uber Go Style Guide. Full citations: `../research/principles/development-principles-research.md §6`.

---

## §7. Act on every return value; justify every discard

> **Rule.** Every return value carries information; don't discard one silently. A bare `_` on a non-trivial value — especially an error — needs a comment, except the closed list of policy-justified categories (CLI-writer `fmt.F*`, test-fixture `gosec`, cleanup `defer Close`).
>
> **Bites when:** dropping an error with `_ =` or no check at all. · **See also:** §6.

**Principle.** Every return value carries information. Discarding it silently discards that information — permanently, invisibly, and often incorrectly. A caller that drops a return value without explanation has made an implicit claim that the value doesn't matter; state and defend that claim, don't assume it.

Most critical for error returns. A silently dropped error is not a handled error; it is a deferred surprise.

### Go-specific notes

Go's blank identifier `_` is a deliberate-discard signal, not a free pass. For non-trivial discards, accompany `_` with a comment explaining why the value is irrelevant here.

Idiomatic exceptions where `_` needs no comment:
- Range loops where only the index or value is needed.
- Blank receiver in interface assertions: `var _ runtime.Backend = (*docker.Runtime)(nil)`.

Deferred `Close()` on read-only resources is a documented acceptable case:

```go
defer f.Close() //nolint:errcheck // read-only file; Close errors don't affect data already read
```

### Category-level justifications

Three categories of suppression are policy-justified — the *category* is documented here, and per-site `//nolint:` directives don't need to repeat the rationale. Adding a per-site comment is welcome but not required for these specific patterns:

1. **`//nolint:errcheck` on `fmt.F*` writes to a CLI writer** — `cmd.OutOrStdout()`, `os.Stdout`, `os.Stderr`, a `tabwriter.Writer`, a `bufio.Writer` wrapping one of the above. The error returns reflect pipe-closure / disk-full / closed-writer conditions that have no actionable response inside a CLI command; logging the error would either spam (if the writer is closed) or recurse (if the logger is the writer).

2. **`//nolint:gosec` on `os.ReadFile` / `os.Open` of test fixtures in `*_test.go` files.** Test paths come from `t.TempDir()` or `testdata/`; G304's "path from variable" check doesn't apply at the test boundary where paths are by definition controlled. Production-code G304 suppressions still require per-site justification.

3. **`//nolint:errcheck` on `defer Close()` / `defer Cleanup()` / `defer os.Remove(All)()` cleanup paths** where the cleanup error has no actionable response and the primary path has already succeeded. (Writable-file `Close()` is a different shape — see the worked example in §Pattern below; the error there *is* actionable.)

Suppressions outside these three categories require a per-site comment explaining why the finding does not apply. The category list is closed — proposing a new category goes through a D-entry in `../working-notes.md`.

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

Effective Go §Errors; Go Code Review Comments. Full citations: `../research/principles/development-principles-research.md §7`.

---

## §8. No half-finished implementations

> **Rule.** A feature is shipped (works + tests + docs) or removed — never half-landed. Partial code is acceptable only when the missing piece is tracked in `design/plans/README.md` with an owner and target; untracked `// TODO: hook this up` is not. Breaking changes go in `BREAKING-CHANGES.md`.
>
> **Bites when:** leaving an untracked TODO, or landing a feature without its tests/docs. · **See also:** §9, §10.

**Principle.** Public-beta status (`CLAUDE.md` §Project Status) means breaking changes are allowed if tracked. It does *not* mean half-implemented features are allowed. A feature is either shipped (works, has tests, is documented) or removed. The state-in-between is the worst — it confuses users and traps maintenance time.

### Pattern

Threshold: if a feature can't be completed in this change unit, it doesn't land. Partial code with a `// TODO: hook this up` is acceptable only when the missing piece is tracked in `docs/contributors/design/plans/README.md` with an owner and a target. Hidden `// TODO` notes without tracking are not.

Breaking changes are explicit: documented in `docs/BREAKING-CHANGES.md` with previous behaviour, new behaviour, rationale, migration steps. Don't break silently.

### Worked examples

- `be22f6a` (2026-03-10, "Remove all legacy backwards compatibility") — the `runtime-config.json` fallback was added in `fdfe0c3` and removed seven minutes later (D16). The fallback wasn't earning its place; it shipped, was reconsidered, and removed.
- `0d1c72e` (2026-03-02) — "Remove stale `copy_strategy` references from docs" cleaning vestigial language from when a deprecated option was removed.
- `c7f9f8a` (2026-03-12, "Remove 'old behavior' and 'legacy' language from help text and docs") — same shape, applied to user-facing strings.
- `docs/contributors/design/plans/README.md` is the consolidated list of designed-but-unimplemented features. When something is deferred, it goes there with a design reference, not buried as a comment.
- The `setup_complete` operational state in `~/.yoloai/state.yaml` (commit `45ed6ef`, 2026-02-26) is fully wired; first-run flow is complete or it's not enabled.

### Cost-vs-benefit

Cost of applying: occasionally a slightly-larger change unit. Damage prevented: vestigial code accumulating in the codebase; `// TODO` comments that grow into archaeology; users reporting bugs in features that were never finished; breaking changes shipped silently and discovered in production.

### Sources

Project `CLAUDE.md` §Project Status; D16. Full citations: `../research/principles/development-principles-research.md §8`.

---

## §9. Plan-then-execute on cleanup

> **Rule.** Architecture cleanup lands as a numbered, audited plan of single-responsibility commits — not opportunistic refactors riding along inside feature work. One W-number, one discrete commit, `make check` green at each.
>
> **Bites when:** refactoring unrelated code inside a feature change. · **See also:** §1, §8.

**Principle.** Periodic architecture cleanup is the project's right shape — not opportunistic refactors. Architecture drift accumulates; the W-numbered remediation plan ensures the cleanup lands as a coherent shape, not as a string of half-overlapping commits.

### Pattern

When the architecture has drifted enough that a single contributor (or AI agent) can't predict where to make a change, run an audit. The audit produces a numbered work plan (`W1`, `W2`, …). Each W is a discrete commit. Status tracked in `docs/contributors/architecture-audit-<period>.md` and the memory entry `project_arch_remediation.md`.

### Worked examples

- **Architecture audit, 2026-05** (`868a5b0`, 2026-05-20) → W1–W14 (commits `4fa683f` … `1f4457c`, 2026-05-20). Each W is a tight, single-responsibility commit with `make check` passing.
- W7 (commit `a22878d`) introduced typed errors as a single source for error categorisation — no more `errors.Is(err, fmt.Errorf("not running"))` text-match anti-patterns; later promoted to the top-level `yoerrors` package (B1).
- W11 progressed in four steps (`3b4a9ae`, `d525d60`, `c00d367`, `1f4457c`): additive descriptor → migrate callers → optional interfaces → registry tuples. Each step independently shippable.
- W12 carved subpackages (`a3207eb`, `e100e4d`, `ccde491`). Each carve produced a cleaner import graph with `make check` passing.

### Cost-vs-benefit

Cost of applying: a few days of audit + plan + numbered execution. Damage prevented: gradual code drift that becomes a "rewrite the world" project; refactor commits that partially overlap and produce confusing histories; the indefinitely-postponed cleanup that never lands.

### Sources

Project D19; W-numbered remediation commits. Full citations: `../research/principles/development-principles-research.md §9`.

---

## §10. Code quality gate — `make check`

> **Rule.** `make check` (gofmt + golangci-lint + go-mod-tidy + Go tests + Python pytest/mypy) must pass before any code change is complete. Never loosen the gate — disable a linter, raise a threshold, skip a test — to land code.
>
> **Bites when:** calling a change "done" without a green `make check`, or weakening the gate to pass. · **See also:** §6.

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

Project `CLAUDE.md` §Code Quality Gate; D20. Full citations: `../research/principles/development-principles-research.md §10`.

---

## §11. Iterate when the first approach doesn't work

> **Rule.** After three failed attempts at the same approach, stop and rethink the architecture rather than grinding another workaround — some problems have a structural shape the obvious solution doesn't match. Record the failure trail; it's data, not waste.
>
> **Bites when:** reaching for "one more workaround" on a problem that has already resisted several. · **See also:** §9.

**Principle.** When a problem resists three attempts at solution, the answer is not "try harder at the same approach." It's "step back and rethink the architecture." Some problems have a structural shape that the obvious solution doesn't match; the right answer is to find the structure, not to grind on the obvious.

### Pattern

Threshold: three failed attempts at the same approach is the trigger. Stop, write down what didn't work and why, and look for the structural insight the failures share. The failure trail is data, not waste; preserve it in `docs/contributors/design/research/` or `docs/contributors/decisions/working-notes.md` so the lesson survives.

### Worked examples

- **Pluggable idle detection** (D14, commit `dbec36f`, 2026-03-08). Tried: tmux `window_bell_flag` polling (broken — `pane_last_activity` doesn't update for TUI agents); fixed-delay polling (flapped); single global detector (wrong for hook-supporting agents like Claude Code). The fourth approach — per-agent `IdleSupport` strategy with a Python `status-monitor` writing `agent-status.json` — worked because it acknowledged the structural truth: no single signal works across agents.
- **gVisor on macOS** (D17 follow-up, commit `d078db6`). Tried multiple ARM64 PTY / setsid / socket-permission workarounds (commits `32ee4e7`, `f381305`, `fdc6db6`, `90892f9`). The Claude Code infinite-`epoll_pwait` bug is upstream; no in-yoloAI workaround was going to fix it. The fix was to refuse with a pointer to the upstream issue.
- **Network isolation** (D11). The Go proxy sidecar was planned (commit `5e5cca3`, 2026-02-23) and rejected in favour of iptables + ipset after prototyping (commit `ed19f9d`, 2026-03-01) — the iptables approach covered the threat model at a fraction of the complexity.
- The 12 rounds of pre-implementation critique (D2) are this principle applied at the design layer: each round was a rethink, not a re-grind.

### Cost-vs-benefit

Cost of applying: the discipline to stop after the third failure and rethink. Damage prevented: the spiral of "one more workaround" that produces fragile, undocumented code; the lost opportunity to find a simpler structural answer.

### Sources

Global `CLAUDE.md` §Agent operating principles; project D14, D11. Full citations: `../research/principles/development-principles-research.md §11`.

---

## §12. No ambient configuration

> **Rule.** Resolve ambient state (env vars, `$HOME`/XDG paths, cwd, terminal detection, process identity) ONCE at the outermost edge — CLI startup, server bootstrap, test setup — and pass it down as explicit args; library code never reads it. An edge-resolved default implies a *pure accessor* downstream: panic if read before the edge ran, never a lazy fallback (a per-call fallback is a "hole" that re-reads ambient state and masks ordering bugs). The rule binds **outward** too: every subprocess is launched with an **explicitly constructed `Env`** (never the inherited `os.Environ()`), so a child tool reads only the env we hand it. And it binds **test code identically** — a test gets no exemption; its only edge is its outermost isolation helper.
>
> **Bites when:** calling `os.Getenv` / `os.UserHomeDir` / `os.Getwd` below the CLI layer, adding a "just for tests" fallback default, or launching a subprocess with the inherited environment (so a tool picks up an ambient `$HOME`/`TART_HOME`/`DOCKER_HOST`/`GIT_*` you never set). · **See also:** §4, §5, TEST §6.

**Principle.** Library boundaries (the `yoloai.Client`, domain functions, server entry points) accept all configuration as explicit arguments. Environment variables, `$HOME`-derived paths, `os.Getwd()`, terminal detection, and other ambient process state are resolved at the OUTERMOST layer — CLI startup, server bootstrap, test setup — and passed down explicitly. Library code never reads ambient process state.

### Pattern

Ambient state is anything not in the function's argument list: environment variables, the working directory, `$HOME`, hostname, the current user's identity, the calling process's PID. Library code refuses to read it. The CLI (or other outermost shell) reads it once at startup, validates it, packs it into typed configuration structs, and passes those down.

Concrete bans (enforced by the forbidigo rules in `.golangci.yml`, **deny by default** across the whole ambient-config surface):

- **Environment:** `os.Getenv` / `os.LookupEnv` / `os.Environ` / `os.ExpandEnv`, env mutation (`os.Setenv` / `os.Unsetenv` / `os.Clearenv`), and the `syscall.Getenv` / `Setenv` / `Environ` backdoors below `os`.
- **Home / XDG / CWD:** `os.UserHomeDir` / `os.UserConfigDir` / `os.UserCacheDir` / `os.Getwd`.
- **Identity:** `os.Geteuid` / `os.Getegid` / `os.Getgroups` / `os.Getppid`, `os.Hostname`, and `os/user`'s `Current` / `Lookup` / `LookupId`. (`os.Getuid` / `os.Getgid` are already banned by F31 — read `layout.HostUID` / `HostGID`.)
- **Process I/O:** `os.Stdin` / `os.Stdout` / `os.Stderr` — library code takes an explicit `io.Reader` / `io.Writer` / `IOStreams`.
- **Timezone:** `time.Local` — only intentional local-time parsing of user input may use it.
- **Subprocess creation:** `exec.Command` / `exec.CommandContext` — banned outside the single command-builder helper. forbidigo has no data-flow, so it can't verify a `Cmd.Env` was set; instead it bans the *raw* call and funnels every subprocess through the helper, which constructs `cmd.Env` explicitly from injected config plus a minimal allowlist. The ban guarantees nothing reaches `Run`/`Start`/`Output` carrying an inherited `os.Environ()`.

Deliberately **not** banned (documented in the lint config so a reader doesn't think they were forgotten): `os.Getpid` (our own PID — self-identity, used for lockfile ownership and unique exec IDs), `os.Getpagesize` (a hardware constant), and `os.Expand` (pure given an explicit mapping — only `ExpandEnv` is ambient).

**The boundary runs outward, and tests get no pass.** Two extensions the in-process bans don't cover on their own:

- *Child processes are ambient-config carriers.* `exec.Command` defaults `Cmd.Env` to the parent's `os.Environ()`, so a tool yoloAI shells out to (`tart`, `docker`, `git`, agent CLIs) reads whatever ambient env the process happens to have — *even though our own code read none of it*. Every subprocess goes through the command-builder helper, which sets `cmd.Env` from injected config (store location, identity, tool knobs) plus a minimal pass-through allowlist. forbidigo bans raw `exec.Command*` to force the funnel: it can't check the field is set, but the ban guarantees nothing skips the helper that sets it.
- *No exemption for test code.* Everything in §12 binds `_test.go` **identically** — a test is not a license to read ambient env or to inherit a subprocess env. A test's only "edge" is its outermost isolation helper (the analogue of CLI startup): it establishes the isolated store/env there, once, and everything below takes explicit config. This is deliberate — the DF19 data loss happened *inside a test*, so exempting tests would exempt the exact code that caused the harm.

Enforcement model — every ambient touch is **deliberate and documented**:

1. **Boundary files** whose job IS to resolve ambient state get a scoped path-exclusion in `.golangci.yml`: `internal/cli/cliutil/layout.go` (the single licensed `os.UserHomeDir` / `SUDO_USER` / `user.Lookup` HOME-resolution site), `internal/fileutil/fileutil.go` (the sudo UID/GID/chown wrappers), the `internal/cli/` layer for `os.Stdin/out/err` (the CLI owns process I/O), and `internal/mcpsrv/proxy.go` (the MCP stdio transport).
2. **Every other legitimate read** carries an inline `//nolint:forbidigo // §12: <reason>` naming why — agent API keys, sudo recovery, `YOLOAI_SANDBOX`, daemon-socket discovery (`DOCKER_HOST` etc.), terminal/UI detection (`TMUX` / `TERM` / `COLUMNS`), or subprocess env passthrough. The justification is right at the call site, so a reviewer sees the intent without leaving the file.

The single standing exception: API keys read by individual agents (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, etc.). The agent's contract IS "I read this env var" — it's part of the published interface in `agent.Definition.APIKeyEnvVars`. The CLI reads the values and passes them as part of sandbox creation; agents document the read in their definition. These sites carry the inline `//nolint` like any other justified read.

### Worked examples

- **`Client.Options.DataDir` is required** (Q-W resolution, 2026-05-25; working-notes D45). Client construction rejects empty DataDir. The CLI fills it from `$HOME/.yoloai/` at startup — its single licensed `os.UserHomeDir()` call. HTTP servers pass an explicit per-tenant path. Tests pass `t.TempDir()`. Eliminates the "daemon silently wrote to /root/.yoloai/" failure mode that the previous implicit-home design exposed.
- **Agents declare env-key dependencies in `agent.Definition.APIKeyEnvVars`** (existing). The library does not call `os.Getenv("ANTHROPIC_API_KEY")` directly; it iterates the agent's declared keys. The dependency is in the agent's published contract, not implicit in deep library code.
- **Resolve-at-edge implies a *pure accessor* downstream — never a lazy ambient fallback** (working-notes D72). When the CLI has a designed implicit default (here: `--data-dir` defaults to `$HOME/.yoloai`), do the implicit→explicit conversion AND validation ONCE at the edge, then have downstream code trust the resolved value. `SetRootLayoutFromFlag` resolves the default in the root command's `PersistentPreRunE` (the single licensed `os.UserHomeDir()` site); `cliutil.Layout()` is then a **pure accessor that panics if read before the edge ran** — it does NOT re-derive `$HOME/.yoloai` on the fly. A per-call lazy fallback is a "hole": it re-reads ambient state on every call, masks ordering bugs, and lets code paths silently work without going through the edge. The panic surfaces the bypass instead of hiding it. This is "parse, don't validate" (§4) applied to ambient config: verify at the boundary, carry a validated value inward. Tests stand in for the edge — they establish the value explicitly (`clitest.Home`), they do NOT lean on a fallback (and "keep a narrowed fallback for tests only" is the same hole renamed).
- **DF19 — the child process that ate the dev's VMs** (2026-06-09; working-notes D79). `runTart` launched the `tart` CLI with the inherited environment, so `tart` read the ambient `$HOME` and operated on the real `~/.tart`. A unit test isolated yoloAI's `DataDir`/`HomeDir` (a temp dir) but **not** the *process* `$HOME`/`TART_HOME`, then invoked the real cross-backend `Prune` — whose tart sweep deleted the developer's real `yoloai-*` VMs during `make check`. yoloAI's own code never read `$HOME`; the leak was entirely the un-constructed child env, in test code. This is why §12 now binds **outward** (explicit `cmd.Env`, raw `exec.Command*` banned) and binds **tests** (no isolation-helper edge → no pass).

### Cost-vs-benefit

Cost: one explicit `DataDir` parameter on Client construction. CLI startup code becomes the gatherer of all environment-derived values, slightly longer. Tests need explicit setup (`t.TempDir()` instead of inheriting `$HOME`).

Damage prevented:
- "Works on my machine" failures where dev tests pass because `$HOME` is set sensibly and a deployed daemon fails because `$HOME` is `/root/`, `/var/empty`, or unset.
- Multi-tenant servers accidentally writing all tenants' state to one tenant's directory because the env-resolved path is shared.
- Test pollution into the developer's real `~/.yoloai/` from a test that forgot to override the home dir.
- A subprocess (build tool, VM CLI, git) silently operating on the developer's *real* store because it inherited an ambient `$HOME`/`TART_HOME`/`DOCKER_HOST` the calling code never set — including a test that destroys real resources (DF19).
- Production daemons that pick up an unexpected env override and silently change behaviour (logging level, default backend, etc.).
- Refactors that move code between layers and inadvertently widen the new layer's access to ambient state.
- Hyrum's-law surprises: any observable env-read becomes an unwritten contract that future refactors can't safely remove.

### Sources

Twelve-Factor App factor III (config read once, passed explicitly); Zen of Python "explicit is better than implicit". Project decision: Q-W (D45); pure-accessor refinement D72; the outward (explicit subprocess `cmd.Env`, raw `exec.Command*` banned) + no-test-pass refinement D79 (motivated by DF19).

---

## §13. Raw until it has to change

> **Rule.** Keep data in the representation it already has until a *present* consumer actually needs a different shape; every conversion must serve a concrete consumer right where it happens. The complement to §4: §4 is the one justified boundary conversion (it proves an invariant); §13 forbids every other speculative reshape.
>
> **Bites when:** pre-emptively reshaping into a "rich" struct no current consumer needs, or a transform downstream just reverses. · **See also:** §4, §2.

**Principle.** Data keeps the representation it already has until a consumer actually requires a different one. Don't pre-emptively transform a value into another shape when downstream code may have to transform it back, or when the "right" target shape isn't yet forced by a real consumer. Every conversion that *does* happen must be justifiable at the point where it happens — there is a concrete consumer right there whose need the new representation serves.

This is the complement to §4 (parse, don't validate). §4 is the *one justified conversion* at a trust boundary — raw input becomes a typed value whose existence proves an invariant. §13 governs every *other* candidate conversion: a transform that proves nothing and serves no present consumer is speculative, and speculative conversions are lossy, often reversed, and they relocate a decision into the wrong layer. Where §4 says "convert here, because the invariant matters downstream," §13 says "don't convert here, because nothing downstream needs it yet."

### Pattern

A conversion is justified when, *at the point it happens*, there is a consumer whose need the new representation serves and that need won't simply be undone downstream. Threshold questions before reshaping data:

1. *Is there a consumer at this point that needs the new shape?* If the only consumer is hypothetical, keep the data raw (YAGNI).
2. *Will a downstream consumer want the original?* If a likely consumer needs the source form, converting here forces a reverse round-trip — keep it raw and let the consumer that needs the change own it.
3. *Which layer owns the conversion?* The boundary-discipline corollary (§2): the layer that owns a conversion is the one with the consumer that needs it — the seam (e.g. library vs. daemon) is decided by where the justifying need lives, not by which layer happens to touch the data first.

When a conversion is genuinely shared by a second consumer later, a helper can be added *then*, at the point the need is concrete — not pre-built against a guess.

### Worked examples

- **The G5 activity-stream carve** (commit `a22ea04`, 2026-06-03; D64). `SystemClient.Logs` emits `LogEvent.Raw` — the **verbatim on-disk JSONL line** — plus only the two fields the transport itself must understand (`Time`, `Level`, for ordering and level-filtering). The library owns *transport* (open / merge-sort across the four log sources / follow-tail / done-detect) but **not payload reshaping**: it does not decompose the JSON into typed event structs, because no library-side consumer needs that and a daemon forwarding the frames onward would only re-serialize it (a reversed round-trip). The CLI — the actual rendering consumer — parses `Raw` at the point it renders. The conversion is justified where it happens; the library does none it can't justify.
- **The justified counter-case is §4 itself.** `ParseSandboxName(string) (SandboxName, error)` *is* a conversion, and a load-bearing one — it proves the containerd-grammar invariant at the boundary so downstream code never re-checks. §13 doesn't forbid it; §13 forbids the *unjustified extras* layered on top of a value that's already in the shape its consumer needs.

### Cost-vs-benefit

Cost of applying: occasionally a consumer does its own parse at the point of use instead of receiving a pre-chewed struct, and the "rich" convenience type doesn't exist until something needs it. Damage prevented: lossy reshape-then-reshape-back round-trips; the library deciding — on an absent consumer's behalf — what the canonical form is, then being wrong when a second consumer wants the original; conversions stranded in the wrong layer, far from the need that would justify them, which turns "who converts, and when" into an awkward cross-layer negotiation instead of a local decision.

### Sources

Project decision: D64. Complements §4 (parse, don't validate) and §2 (none-of-your-business — the conversion-ownership corollary). Carried-over design conversation: "data from underneath should remain unchanged until it has to change … we could offer helpers, but I'd do that YAGNI."

---

## §14. Library defaults are safety-only; convenience lives in a layer on top

> **Rule.** A default in library code is legitimate **only to keep a caller off an *unsafe* path** — when omitting the value would force a choice between a safe and an unsafe option and some callers will pick blindly. For any value with no safety dimension the library has *no* default: accept unset, resolve through the real sources, and error if it is still unset. Convenience defaults belong in an opinionated layer built *on top of* the API, never inside it.
>
> **Bites when:** adding a library default for ergonomics / "it should just work", or to spare a caller from typing a value that carries no safety consequence. · **See also:** §4 (empty-string isn't a free default), §5 (no silent fallback), §12 (ambient), §2; `general-principles.md §6` (the safe setting is the default).

**Principle.** §4 and §5 say the library rejects an unset required value rather than inventing one. §14 carves out the *one* exception and bounds it: a library may substitute a default **when, and only when, the field is safety-sensitive and the default is the safe choice**. The reasoning is about callers, not ergonomics — if requiring the caller to choose would force them to pick between a safe and an unsafe alternative, you must assume some callers will choose arbitrarily because they don't understand the implications. **Documentation does not fix this** — they won't read it. So the safe value is supplied for them. This is the library-API form of `general-principles.md §6` ("the safe setting is the default; the user types the dangerous one").

**The test, in order:**

1. Does omitting the value force the caller toward a *safe-vs-unsafe* fork? If no → the library has no default (go to step 3). If yes → continue.
2. Is the default the *safe* option? If yes → supply it, at one named step, after the value's real sources have been consulted. Here "resist defaults" (§4/§5) and "apply the safe default" point the **same way**, so there is no tension: you look to the real source first, then fall to the safe value.
3. **No safety dimension → no library default.** Accept unset at the top, resolve through every legitimate source, and if it is *still* unset, **error** — "you must specify this somewhere." The library never fabricates a value to be helpful.

**Worked example — dir mode (2026-06-07 audit).** Workdir/aux `Mode` is safety-sensitive: `:copy` protects the original via copy/diff/apply, aux `:ro` is read-only. A blind caller who left it unset must not silently land on a live read-write mount of their source tree. So unset resolves to the safe default — but *only after* the real source (the profile merge) has had its say, at a single named step (`create.defaultDirModes`), not scattered or duplicated at the public boundary. **Had `Mode` *not* been a safety issue**, the correct shape would instead have been: accept `""` through the top layer, apply the profile, and if it were *still* `""`, error out — no default at all.

**The convenience corollary (80/20).** Convenience is real and worth serving — but its home is a layer *on top of* the API, with opinionated defaults pre-filled, not the API itself. ~80% of users ride the convenience layer; the ~20% who need the extra power learn and drive the lower layers directly. In yoloAI the **CLI is exactly this layer**: it resolves `--agent` / config / `"claude"`, selects a backend, reads `YOLOAI_SANDBOX` — all convenience defaults that the library proper refuses (it returns `ErrBackendRequired`, "agent is required", etc.). Pushing those defaults down into the library would rob the power-user (the embedder/daemon) of the explicit, no-magic surface that is the whole point of library-first (`architecture-principles.md §1`).

### Cost-vs-benefit

Cost of applying: the library surface is less "friendly" — an embedder must name values the CLI user never sees, and convenience lives in a second layer someone has to build. Damage prevented: a blind caller silently landing on an unsafe configuration (the failure documentation can't prevent); convenience defaults compounding into Hyrum's-law magnets deep in the library (§4); and the power-surface losing the explicitness that makes embedding auditable.

### Sources

Audit conversation, 2026-06-07 (the dir-mode / tmux / agent-default round). Refines §4's "Empty string isn't a free default" and §5's no-silent-fallback rule; the safety-net framing is the library-API echo of `general-principles.md §6`.

---

## §15. Name for the reader's distance

> **Rule.** The clarity a name must carry is proportional to the contextual *distance* between where it is declared and where it is read. Struct/class fields travel farthest (read across many methods and files) so they must stand entirely alone; function parameters are read at the signature, not the body, so they must read like documentation; local variables are read close to their declaration, so terseness is defensible — but the tension grows with the length of the scope. A *clarifying* comment that merely restates what the name should have conveyed (`Source string // source path` → `SourcePath`) is a smell.
>
> **Bites when:** abbreviating a struct field (`rt`, `mu`, `c`, `ts`), giving a parameter a one-letter name, or writing a comment that the name should have carried. · **See also:** §1 (least-astonishment), §4; `../standards/go.md §Naming`.

**Principle.** A name's job is to deliver meaning to the place it is *read*, which is rarely the place it is *declared*. The further a name travels from its declaration before it's used, the more a reader at the use site has paid to recover its meaning — they must navigate back to the declaration, losing their place. So the required clarity of a name scales with that distance. This is why the same abbreviation can be fine in one position and a defect in another: the cost isn't in the characters, it's in the look-up the reader is forced into.

This is the *why* under `../standards/go.md §Naming`'s "clarity over brevity" and "rename when the comment is doing the name's job" rules. The standard says *what* to type; this principle says *how much clarity each position needs and why*, so you can judge a new case instead of memorising a list.

### The three tiers, by distance

1. **Struct / class fields — maximum distance.** A field is declared once and read across many methods, frequently spread over multiple files. A reader confused at a use site (`s.c.layout`, `rec.ts`) has to navigate all the way back to the type definition to recover what the field is — and then find their place again. Field names must therefore stand entirely on their own. A field of type `runtime.Backend` is named `backend`, not `rt`; the receiver-holder is `client`, not `c`; the timestamp is `timestamp`, not `ts`.

   The corollary: a **clarifying comment on a field is a smell** — not because comments are bad, but because the comment lives at the *declaration* and never travels to the *use* site where the confusion actually is. `Source string // source path` "documents" the field exactly where no reader is confused, and is silent exactly where they are. The fix is to fold the comment into the name (`SourcePath`), so the meaning travels with every use. (This is distinct from a comment that encodes what the type system *can't* — an invariant, a side effect, a zero-value semantic, a cross-reference — which earns its keep and stays. `../standards/go.md §Naming` draws that line.)

2. **Function parameters — the signature is the documentation.** A caller, and anyone reading the signature, sees the parameter's *name and type* and nothing else — never the body. `CreateSandbox(n string, a AgentType, d []string)` forces every reader into the implementation to learn what `n`, `a`, `d` are; `CreateSandbox(name string, agent AgentType, directories []string)` reads like documentation. Parameters are part of the interface, so they pay the field-level clarity tax, not the local-variable discount.

3. **Local variables — terseness is defensible, to a point.** A local read three lines below its declaration carries its own context; the reader hasn't lost their place, so a short name (`pr` for a `profileResult`, `i` for a loop index) is fine. But the justification is *the short scope*, and it erodes as the scope grows: in a long method a terse local imposes the same look-up tax as a field, because the declaration has scrolled off-screen. The longer the function, the more a local earns a fuller name — or the more the function is asking to be split (§1). Method receivers are the standing exception: idiomatic Go keeps them 1–2 letters (`s` for `*Sandbox`) precisely because their scope and meaning are unambiguous at every use.

### Pattern

When you write or review a name, locate it on the distance axis and ask the matching question:

- *Field:* "Could a reader at a random use site, with no other context, say what this is?" If not, rename. If a comment is supplying the missing meaning, fold it into the name.
- *Parameter:* "Does the signature alone read like documentation?" If a reader would have to open the body, rename.
- *Local:* "Is the declaration visible from every use?" If the scope is long enough that it isn't, give it a fuller name (or shorten the scope).

### Worked examples

- **The 2026-06-07 field-name audit (D73).** A sweep of the public surface and internal handles produced a cluster of renames that are all the same shape — an abbreviation that read fine at its declaration and opaquely everywhere else: `Client.rt`→`runtime`, `Client.mu`→`mutex`, `Sandbox.c`→`client` (and the `Agent`/`Workdir`/`Files`/`Network` sub-handles flattened to a `client`+`name` pair), `logRecord.ts`→`timestamp`, `resolvedCreateInputs.pr`→`profile`. Method receivers (`s`, `m`, `c`) were deliberately *left* short — tier 3's standing exception.
- **The W-L8a / D45 comment-vs-name pass** (recorded in `../standards/go.md §Naming`): ~20 `yoloai.Client`-surface fields where the field *comment* was doing the name's job (`Note string // probe failure reason` → `UnavailableReason`) — the field tier of this principle, applied at API-design time.

### Cost-vs-benefit

Cost of applying: longer identifiers, and the discipline to rename when an abbreviation or a clarifying comment creeps in. Damage prevented: the per-read look-up tax a vague field imposes on every method that touches it; signatures that can't be understood without opening the body; the clarifying comment that documents a field exactly where no one is confused and stays silent exactly where they are; and the slow rot where `c.rt`, `s.c`, `rec.ts` accumulate until the code can only be read with the type definitions open in a second window.

### Sources

Global `~/.claude/CLAUDE.md` §Naming; project decision D73; concretised in `../standards/go.md §Naming` ("clarity over brevity", "field comments: rename when the comment is the name's job"). Cousin of §1 (least-astonishment) and §4 (a name's meaning, like an invariant, is established once and relied on downstream).

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
| **No-env-vars-ever**                         | §12 bans env reads in *library code* as silent defaults. The CLI startup layer reads `YOLOAI_DATA_DIR` and similar; agents declare API-key env vars in their definitions. The rule is "read env once, at the outermost boundary, with the read documented" — not "env vars are forbidden everywhere."                  |
| **Never-convert / always-pass-raw**          | §13 forbids *unjustified* conversions, not all of them. §4's boundary parse is a justified conversion (it proves an invariant); rendering a value for a human is a justified conversion (the human is the consumer). The rule is "convert where a present consumer needs it," not "never reshape data."                       |
| **No-defaults-in-the-library-ever**          | §14 bans *convenience* defaults in the library, not *safety* ones. A safety-sensitive field whose default is the safe choice may be defaulted (at one named step, after its real sources resolve); a value with no safety dimension gets no default — accept unset, resolve, and error if still unset. The rule is "library defaults are safety-only," not "the library never supplies a value."                       |
| **Spell-every-name-out**                     | §15 scales clarity to *distance*, not to a blanket maximum. A struct field read across many files must stand alone; a local read three lines down its declaration legitimately stays terse (`pr`, `i`), and a method receiver stays 1–2 letters by Go convention. The rule is "name for the reader's distance," not "never abbreviate."                                                                       |

---

## Closing note

The development principles parallel the general / testing / security principles in shape: framing, threshold per principle, worked examples grounded in commits and D-entries, over-generalisations to avoid. The specialised relationship:

- `general-principles.md §3` (don't reinvent the wheel) is the strategic version of this doc's "use boring stdlib + ecosystem tools."
- `testing-principles.md` is the safety net that makes the boundary discipline (§2) and refactor-in-cleanup-batches (§9) actually safe.
- `security-principles.md` cites this doc's validate-at-every-layer (§3) and parse-don't-validate (§4) as the structural enforcement of containment.
- `../standards/GO.md` and `../standards/CLI.md` ground these principles in concrete everyday Go and CLI code.
