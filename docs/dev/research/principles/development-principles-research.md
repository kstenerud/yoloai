ABOUTME: Primary-source evidence backing each principle in yoloAI's
ABOUTME: development-principles.md. Each section names the source (author, year,
ABOUTME: where to find it), explains why it backs the principle, and notes where
ABOUTME: the source does NOT cleanly apply to yoloAI's single-author OSS CLI scope.

# Development-principles research — primary-source backing for yoloAI

This file is evidence, not principle. `principles/development-principles.md` cites
this by section; this file cites the outside world. Purpose: every principle in
the development doc traces to a dated, named, findable source so the reasoning
doesn't evaporate when decisions are revisited.

Applied to yoloAI's parameters: single author, Go CLI binary, OSS public beta,
pluggable `runtime.Runtime` interface with five backends (Docker, Podman, Tart,
Seatbelt, containerd). Core differentiator: copy/diff/apply workflow. Domain
logic in `sandbox/`, recently carved into subpackages (`archetype/`, `patch/`,
`store/`) per architecture remediation W12. CLI in `internal/cli/` using Cobra.
Python helpers in `runtime/monitor/` with typed pytest tests.

---

## Sources overview

| Source | Date | Type | Relevant principles |
|--------|------|------|---------------------|
| Kent Beck — YAGNI (XP / C3 project) | late 1990s | Primary, named author | §1 Engineering values / YAGNI |
| Robert C. Martin — "The Principles of OOD" (objectmentor.com) | 1995–2002 | Primary, named author | §1 SOLID |
| Robert C. Martin — *Clean Code* (Prentice Hall) | 2008 | Primary, named author | §1 SOLID / SRP |
| Sandi Metz — "The Wrong Abstraction" (sandimetz.com) | 2016 | Primary, named author | §1 DRY / KISS |
| Eric Raymond — *The Art of Unix Programming* | 2003 | Primary, named author | §1 KISS / Rule of Modularity |
| David L. Parnas — "On the Criteria To Be Used in Decomposing Systems" | 1972 | Primary, named author | §1 Encapsulation / SoC |
| Bertrand Meyer — *Object-Oriented Software Construction* | 1988 / 2nd ed. 1997 | Primary, named author | §1 CQS / DbC |
| Ian Cooper / Martin Fowler — Tell-Don't-Ask | 2004–2009 | Named authors | §1 TDA |
| David Holub — "Getter and Setter Methods Are Evil" | 2003 [verify] | Named author | §1 Tell-Don't-Ask |
| David H. Hansson / 37signals — Convention over Configuration | 2004–2006 | Primary, named author | §1 CoC |
| Jon Postel — RFC 760 / RFC 793 | 1980 / 1981 | Primary standard | §1 Robustness Principle |
| Eric Allman — "The Robustness Principle Reconsidered" (ACM Queue) | 2011 | Primary, named author | §1 Robustness Principle |
| Donald E. Knuth — "Structured Programming with goto Statements" | 1974 | Primary, named author | §1 Premature Optimization |
| Tony Hoare — Turing Award Lecture | 1980 | Primary, named author | §1 Premature Optimization |
| Alexis King — "Parse, don't validate" (lexi-lambda.github.io) | 2019 | Primary, named author | §4 Parse, don't validate |
| Effective Go (go.dev/doc/effective_go) | 2009, maintained | Official | §6 / §7 errcheck, discard |
| Go Code Review Comments (go.dev/wiki/CodeReviewComments) | 2013, maintained | Official | §6 / §7 errcheck, discard |
| Uber Go Style Guide (github.com/uber-go/guide) | 2018, maintained | Industry standard | §6 / §7 |
| Google Go Style Guide (google.github.io/styleguide/go/) | 2022, maintained | Industry standard | §6 / §7 |
| yoloAI codebase and D-entries (cited inline) | 2026 | Internal | all principles |

---

## §1 — Engineering values: the cross-cutting vocabulary

### YAGNI — Kent Beck (late 1990s)

YAGNI — "You Aren't Gonna Need It" — originates from the Chrysler Comprehensive
Compensation (C3) project by Kent Beck and Chet Hendrickson in the late 1990s as
part of the Extreme Programming discipline. The original coinage is credited to
Beck/Hendrickson in conversation; Ron Jeffries documents it at
ronjeffries.com/xprog/articles/practices/pracnotneed/. The principle: do not add
capability until it is needed.

Martin Fowler's canonical write-up (martinfowler.com/bliki/Yagni.html, 2015)
identifies four costs of a premature feature: cost of build, cost of delay, cost
of carry, and cost of repair. His refinement — "Yagni requires (and enables)
malleable code" — is the key: YAGNI is not a justification for poor design, it is
a discipline that forces design quality so the deferred feature can actually be
added later without surgery.

Applied to yoloAI: each presumptive feature is complexity maintained across all
five backends. A feature no user triggers still has a test surface, a documentation
surface, and a per-backend divergence surface. YAGNI is load-bearing here in
proportion to the number of backends.

### KISS — Eric Raymond (2003)

The KISS principle ("Keep It Simple, Stupid" — or in Raymond's framing, "Rule of
Simplicity") is documented in Raymond, E.S. *The Art of Unix Programming*, Addison-
Wesley, 2003, Chapter 1 §Rule of Simplicity: "Write simple parts connected by clean
interfaces." Raymond documents the Unix tradition that complexity is the enemy of
correctness, and that the Unix tools that survived longest are the ones that solved
one problem simply.

Raymond's companion "Rule of Modularity" (same chapter) is directly relevant to
yoloAI's pluggable backend design: "Write simple parts connected by clean
interfaces" — the `runtime.Runtime` interface (`runtime/runtime.go`) is exactly
this. Seventeen methods, no backend-specific types at the interface boundary.
Backends are simple modules behind the interface; the interface is simple enough
to implement in five different ways without contradictions.

### DRY — Andy Hunt and Dave Thomas (1999)

DRY — "Don't Repeat Yourself" — is introduced in Hunt, A. and Thomas, D. *The
Pragmatic Programmer: From Journeyman to Master*, Addison-Wesley, 1999, tip #11:
"Every piece of knowledge must have a single, unambiguous, authoritative
representation within a system." The authors distinguish DRY from "avoid code
duplication" — DRY is about knowledge, not text. Duplicated knowledge is the
failure mode; duplicated structure that expresses different knowledge is not.

The most important refinement for yoloAI is Sandi Metz's "The Wrong Abstraction"
(sandimetz.com/blog/2016/1/20/the-wrong-abstraction, 2016): "duplication is far
cheaper than the wrong abstraction." Metz's canonical failure path: see duplication
→ extract prematurely → add a parameter for each exception → the abstraction now
does something nobody wanted. She names the escape: "prefer duplication over the
wrong abstraction." This directly backs the "premature abstraction" warning in
`docs/dev/standards/GO.md` §What to Avoid: "Three similar lines of code are
better than a premature helper function."

### SOLID — Robert C. Martin (1995–2002)

The SOLID acronym was assembled by Michael Feathers from five principles originally
published by Robert C. Martin (Uncle Bob) at objectmentor.com between 1995 and
2002. The five:

- **SRP** (Single Responsibility Principle) — Martin, R.C. "The Single
  Responsibility Principle," objectmentor.com, 1995. "A class should have only one
  reason to change." Applied to yoloAI: the W12 architecture remediation carved
  `sandbox/` into `archetype/`, `patch/`, and `store/` precisely because a single
  package had acquired multiple independent reasons to change (archetype resolution,
  diff logic, state storage). Each subpackage now has one reason.
- **OCP** (Open/Closed Principle) — the pluggable registry (`runtime/registry.go`)
  is the canonical OCP implementation: backends are added by registering a
  `(Factory, BackendDescriptor)` pair. The registry code never changes when a new
  backend is added. Adding the sixth backend is purely additive.
- **LSP** (Liskov Substitution Principle) — every `runtime.Runtime` implementation
  must substitute for any other without callers in `sandbox/` observing a behavior
  difference for the shared contract. Backend-specific behavior (Tart VM path
  translation, Seatbelt SBPL setup) must not break the invariants callers rely on.
- **ISP** (Interface Segregation Principle) — the optional interface pattern in
  `runtime/runtime.go` (`UsernsProvider`, `CopyMountResolver`, `CachePruner`,
  `DiskUsageReporter`, `StdioExecer`, `IsolationCapabilityProvider`) is textbook
  ISP: small, focused interfaces rather than one large interface with methods most
  backends don't need. Backends implement only what applies to them.
- **DIP** (Dependency Inversion Principle) — `sandbox/` depends on the `runtime.Runtime`
  interface, not on `runtime/docker.Docker`. The interface is defined in the
  `runtime` package (the higher-level policy layer), not in any backend package.

Martin also published *Clean Code* (Prentice Hall, 2008) which popularized SRP
beyond OOP into a general function/method discipline. Chapter 3 "Functions" — "The
first rule of functions is that they should be small. The second rule is that they
should be smaller than that" — backs the 40-line function guideline in
`standards/GO.md`.

### Separation of Concerns — Edsger W. Dijkstra (1974)

Dijkstra coined the phrase in: Dijkstra, E.W. "On the role of scientific thought,"
selected writings on computing: a personal perspective, Springer, 1982, written
1974. The canonical statement: "It is what I sometimes have called 'the separation
of concerns', which, even if not perfectly possible, is yet the only available
technique for effective ordering of one's thoughts." The concept predates the
phrase: it is the organizing principle of structured programming.

In yoloAI's architecture: `runtime/` manages backend lifecycle; `sandbox/` manages
sandbox state and orchestration; `internal/cli/` handles argument parsing and
output. These are three separate concerns. The design rule that CLI commands are
thin wrappers — "parse args, call into domain packages, format output"
(`standards/GO.md` §CLI Framework) — is SoC applied structurally.

### Encapsulation — David L. Parnas (1972)

The information-hiding principle, which is the intellectual foundation of
encapsulation, is from Parnas, D.L. "On the Criteria To Be Used in Decomposing
Systems into Modules," *Communications of the ACM*, 15(12), December 1972. The
key distinction Parnas draws: decompose by the *design decisions that are likely
to change*, not by functional steps. Each module hides a decision. This paper is
also the origin of the module boundary concept that SOLID later systematized.

Applied to yoloAI: the `runtime.Runtime` interface hides the backend implementation
decisions (Docker API version, containerd snapshotter, Tart VM path translation).
No code outside a backend package knows about `docker.ImageBuild`, `tart.VM`, or
`seatbelt.SBPLProfile`. This is Parnas-style encapsulation: the things hidden are
the things most likely to change across platforms.

### Law of Demeter (Principle of Least Knowledge) — Lieberherr & Holland (1987)

The Law of Demeter was formulated by Karl J. Lieberherr and Ian M. Holland at
Northeastern University in 1987. The canonical statement appears in: Lieberherr,
K.J. and Holland, I.M. "Assuring Good Style for Object-Oriented Programs," *IEEE
Software*, September 1989, pp. 38–48. The rule: a method should only call methods
on objects it directly owns (self, passed parameters, created objects, direct
fields). It should not reach through intermediaries.

Applied to yoloAI: CLI commands call sandbox functions, which call runtime methods.
CLI code does not reach into runtime internals. The "boundary discipline" principle
(§2 below) is LoD applied at the package level.

### Principle of Least Astonishment — attributed to various sources

The Principle of Least Astonishment (POLA) is widely attributed to the Unix and
design communities without a single canonical author. The phrase appears in the
context of UI design in Apple's human interface guidelines and in Raymond's *The Art
of Unix Programming* (2003). The principle: a program should behave in the way that
least surprises the user.

For yoloAI, D3 in `docs/dev/working-notes.md` is the worked example: directories
mount at mirrored host paths because a user reading an error trace inside the
sandbox should see the same path they see outside. Remapping paths to `/work/`
would have been astonishing. The POLA argument is explicit in D3: "Principle of
least astonishment: an agent reading an error trace inside the sandbox should see
the same path the user sees outside." The `standards/GO.md` names the principle:
"Names should enhance readability and describe 'what', not 'how'."

### Convention over Configuration — David H. Hansson (2004–2006)

Convention over Configuration (CoC) is a design philosophy popularized by David H.
Hansson (DHH) in the Ruby on Rails framework, starting with the 2004 release.
Hansson describes it in the Rails documentation and in his talks as: "when the
framework can make a reasonable default decision, it should, and only expose
configuration when the user needs to deviate."

The principle appears in yoloAI's two-layer API design (`standards/GO.md` §API
Design, §Two-Layer API): the high-level API covers 80% of use cases with sensible
defaults; the low-level API handles the rest. The mount mode taxonomy (D4) is CoC
applied to yoloAI: `:copy` is the safe default for workdir, `:ro` is the safe
default for aux dirs. Users opt into less-safe modes explicitly. The profile
system (D12) is CoC for environment configuration: the `base` profile is auto-
created; the user overrides only what needs to change.

### Tell-Don't-Ask — Ian Cooper, Martin Fowler (2004–2009)

The Tell-Don't-Ask principle is attributed to Ian Cooper (2003–2004) and documented
by Martin Fowler at martinfowler.com/bliki/TellDontAsk.html (September 2013).
The principle: instead of asking an object for its data and then making decisions
based on that data, tell the object to do something (and let it make the decision).
This is what distinguishes OOP from procedural programming using objects as data
bags.

David Holub's "Getter and Setter Methods Are Evil" (JavaWorld, 2003) [verify] is
a commonly cited earlier statement of the same idea. In the Go context, the
principle is softer — Go's simpler type system and lack of inheritance make pure
TDA harder — but it shapes interface design: `runtime.Runtime` methods tell the
runtime what to do (`Create`, `Start`, `Stop`, `Exec`) rather than exposing state
for callers to read and act on.

### Command-Query Separation — Bertrand Meyer (1988)

CQS was introduced by Bertrand Meyer in: Meyer, B. *Object-Oriented Software
Construction*, Prentice Hall, 1988 (second edition 1997). Chapter 23. The rule:
a function that returns a value should not have side effects (query); a function
that has side effects should return void (command). "Asking a question should not
change the answer."

Applied to yoloAI: `runtime.Runtime.Inspect()` (query — returns state, no side
effects) and `runtime.Runtime.Create()` (command — creates the instance, returns
only an error). The `IsReady()` predicate is a query; `Setup()` is a command. The
`standards/GO.md` enforces this indirectly: typed error returns from domain
packages use the error value to signal failure, not to carry side-effect state.

Meyer's *Object-Oriented Software Construction* is also the source of Design by
Contract (DbC) — the precondition/postcondition/invariant framework. yoloAI's
"Validate at every layer" principle (§3 below) is DbC applied to Go's error-return
idiom: each function has an implicit contract (preconditions checked, postconditions
guaranteed, invariants maintained), expressed through defensive validation.

### Premature Optimization — Donald E. Knuth (1974)

The canonical quote is from: Knuth, D.E. "Structured Programming with goto
Statements," *Computing Surveys*, 6(4), December 1974, p. 268: "We should forget
about small efficiencies, say about 97% of the time: premature optimization is the
root of all evil." The full sentence is often truncated; Knuth continues: "Yet we
should not pass up our opportunities in that critical 3%."

Tony Hoare has been credited with an earlier formulation ("premature optimization
is the root of all evil") in his 1980 Turing Award lecture, but Hoare himself has
credited Knuth; the Knuth cite is the standard. The key discipline is the word
"premature" — optimization driven by measurement of actual bottlenecks is exactly
what Knuth advocates. Optimization driven by speculation is what is condemned.

Applied to yoloAI: `standards/GO.md` §What to Avoid explicitly names "Premature
abstraction" as the Go-specific manifestation. The abstraction anti-pattern (extract
a helper before the duplication makes the pattern clear) is premature optimization
applied to code structure: optimizing for a reuse case that doesn't yet exist.

### Robustness Principle — Jon Postel (1980)

The Robustness Principle is from Postel, J. "Transmission Control Protocol," RFC
793, IETF, September 1981. The relevant clause (originally in RFC 760, Internet
Protocol, January 1980): "Be conservative in what you do, be liberal in what you
accept from others." The original context is TCP/IP protocol implementation.

Applied naively to software design, the Robustness Principle produces problems that
Eric Allman documents in: Allman, E. "The Robustness Principle Reconsidered," *ACM
Queue*, 9(9), September/October 2011. Allman's argument: accepting invalid input
liberally makes interoperability worse over time because senders never know whether
their errors were caught. Bad inputs become de-facto standards.

For yoloAI, the principle applies in its restricted, Allman-aware form: be strict
at system boundaries (validate sandbox names at the CLI boundary — D10; resolve
symlinks before safety checks — D6), be tolerant at presentation boundaries (error
messages are human-readable rather than machine-strict; `yoloai config get` prints
a friendly note rather than exiting non-zero for an undefined key). The Allman
caveat explicitly applies: do not silently accept malformed sandbox names or path
traversals ("be liberal" does not mean "skip the check").

---

## §2 — Boundary discipline: domain packages don't leak runtime/backend types

### Internal source — D7 (`docs/dev/working-notes.md`)

The design decision is D7: "No backend-specific types leak outside their package."
The registry design (W11, commit `1f4457c`) formalizes this: `runtime.New()` takes
a backend name string and returns a `runtime.Runtime` interface. No caller outside
a backend package holds a `*docker.Runtime` or `*tart.Runtime`.

The `internal/cli/helpers.go` dispatch function `newRuntime()` is the single
crossing point where a backend name string becomes a `runtime.Runtime`. This is the
anti-corruption layer pattern from domain-driven design (Evans, E. *Domain-Driven
Design*, Addison-Wesley, 2003, Chapter 14 "Maintaining Model Integrity"). Evans's
framing: a boundary layer translates between external models and the internal
domain model. yoloAI's `newRuntime()` is exactly this: it translates from the
user's string (the external model) into the domain's interface (the internal model).

### Parnas — information hiding applied to package boundaries

The Parnas 1972 paper (cited in §1) directly backs this principle. Parnas's
criterion for good module decomposition: hide "design decisions which are likely
to change." The Docker SDK, the Tart API, and the containerd gRPC interface are all
"likely to change" — Docker has deprecated and re-introduced APIs; Tart is a young
tool; containerd's snapshotter API changed between v1 and v2. All of these are
hidden behind the `runtime.Runtime` interface.

The failure mode Parnas documents: decompose by functional steps (step 1 pulls the
image, step 2 creates the container, step 3 starts the container) instead of by
change reasons. A functional decomposition would leak Docker types into step 1,
2, and 3 logic. The interface decomposition hides them in one place.

### Eric S. Raymond — "Rule of Modularity" and the thin-interface-layer pattern

Raymond, E.S. *The Art of Unix Programming*, Chapter 1, Rule of Modularity: "Build
simple parts connected by clean interfaces." The corollary rule, Rule of Separation:
"Separate policy from mechanism." In yoloAI, the runtime backends are mechanism
(how to create a container); the sandbox package is policy (when to create it, what
to put in it, what mode to use). The interface enforces the separation structurally:
the policy layer (`sandbox/`) can only call the mechanism layer (`runtime/`) via the
`runtime.Runtime` interface methods — no policy logic has access to mechanism
internals.

### yoloAI-specific: optional interfaces as ISP at the boundary

The optional interface pattern in `runtime/runtime.go` (`UsernsProvider`,
`CopyMountResolver`, `CachePruner`, etc.) is the boundary discipline applied to
the "features not all backends support" problem. The alternative — add every
feature to the core `Runtime` interface — would force all backends to implement
methods irrelevant to them, or return `errors.ErrUnsupported`, making the interface
a lie. The optional interface design (type-assert to check; call if present; skip
if absent) is honest: it does not promise what it cannot deliver.

This pattern is documented in `standards/GO.md` §Runtime Backend Extension:
"Construction-time params specific to one backend belong in `New()`, not in
`InstanceConfig`." The corollary: per-invocation optional features belong in
optional interfaces, not in `InstanceConfig` with ignored-per-backend fields.

---

## §3 — Validate at every layer: defense in depth across function boundaries

### Bertrand Meyer — Design by Contract (1988)

The DbC framing (cited in §1 under CQS) is the theoretical foundation. Meyer's
insight: each function is a contract. The caller's side of the contract is the
precondition; the callee's side is the postcondition. Maintaining the contract
means checking preconditions at the boundary — not relying on callers to have
already validated.

Applied to yoloAI: `sandbox/parse.go` validates sandbox names at the CLI boundary
(D10). `sandbox/create.go` re-validates path parameters before filesystem operations.
`runtime/docker/docker.go` validates that the Docker daemon is reachable before
attempting creates. Each layer validates because the layer above may be bypassed
(tests call sandbox functions directly; future callers may not go through the CLI).

### D6 — Symlink resolution before safety checks (`docs/dev/working-notes.md`)

D6 is the worked example the principle cites. The decision: path safety checks
operate on `filepath.EvalSymlinks(path)` results, not on the path as typed.
Rejected: "Check paths as-typed — rejected because `~/safe-link` could be a
symlink to `/etc`, and a 'safe' check on the link would pass while the actual
mount points at `/etc`." The principle this demonstrates: validate the real input
(the resolved path), not the surface representation. The bind-mount system call
follows symlinks; the validation must too.

This is the "validate the invariant, not the representation" discipline — a specific
form of DbC where the precondition must match the actual contract of the called
operation.

### Defense in depth — general security literature

The principle of defense in depth (layered security, "not relying on any single
barrier") is documented in: NIST Special Publication 800-27, "Engineering
Principles for Information Technology Security," 2004 (archived). Principle 8:
"Implement layered security (Ensure no single point vulnerability)." The principle
is also in the CIS Controls (Center for Internet Security, cisecurity.org) and the
OWASP Testing Guide.

For yoloAI, defense in depth at the validation layer means: even if a CLI
validation is bypassed, the domain layer rejects invalid input. Even if the domain
layer has a bug, the runtime layer refuses impossible operations. Even if the runtime
layer is misconfigured, the Docker daemon itself enforces container isolation.

---

## §4 — Parse, don't validate: represent valid state in the type system

### Alexis King — "Parse, don't validate" (2019)

Primary source: King, A. "Parse, don't validate," lexi-lambda.github.io/blog/
2019/11/05/parse-don-t-validate.html, November 2019. This is the canonical modern
statement of the principle. King's key argument: validation functions return a
boolean — they tell you whether the input is valid, but the type they return is
still the raw input type. The raw type can still flow into code that never sees the
validation result. Parsing functions return a new type — the type that can only
exist if the parse succeeded. After a parse, the raw type is gone.

King's example (Haskell): `validate :: Text -> Bool` versus
`parse :: Text -> Maybe Name`. After `parse`, the `Name` type is the guarantee.
After `validate`, you still have a `Text` that could be anything.

### Application to Go — D10 and the sandbox name type

King's blog post acknowledges that statically typed languages with sum types
(Haskell, Rust, Swift) make this principle more powerful than languages without
them (Java, Go). Go lacks sum types and has limited type aliases. The principle
still applies, with Go-specific adaptations.

D10 (`docs/dev/working-notes.md`): sandbox name validation happens at the CLI
boundary against a regex. The "parse" step is `parse.SandboxName(s string)` in
`sandbox/parse.go`, which returns a validated name value (or error). All downstream
code receives the validated name. No downstream code re-validates it; they trust
the type. The "parser" is the CLI boundary; the "parsed type" is the returned name
value after `parse.SandboxName` succeeds.

The Go-specific limitation King acknowledges: you cannot enforce at compile time
that a `string` was produced by `parse.SandboxName`. Go does not have phantom types
or newtype safety. The enforcement is by convention (functions that take a sandbox
name take it as a typed identifier, not a raw string) and by code review. This is
not as strong as Haskell's guarantee, but it is stronger than validating at every
use site.

### Related — Structured vs. Stringly-typed design

The broader principle (represent domain concepts as distinct types, not raw strings)
is documented in the Go Code Review Comments (go.dev/wiki/CodeReviewComments,
"Meaningful names" section): "Never use bare strings for ids or enums; create types."
The `BackendDescriptor.Name` field in `runtime/runtime.go` is a `string`, but the
registry key is validated against it on `Register()`: `if name != descriptor.Name {
panic(...) }`. This catches the case where a backend registers under the wrong name
before the type system can.

---

## §5 — Fail fast: return errors immediately; constructors reject invalid state

### "Fail fast" — general principle, multiple sources

The "fail fast" principle appears independently in several sources. Jim Shore
documented it as a design principle in: Shore, J. "Fail Fast," *IEEE Software*,
September/October 2004, pp. 21–25. His framing: detect errors immediately, at the
point where the invariant is violated, not miles away in code that assumed the
invariant held.

Martin Fowler discusses it at martinfowler.com/ieeeSoftware/failFast.pdf (2004,
the Shore article was published in IEEE Software which Fowler edited at the time).

Applied to yoloAI: the error-handling discipline in `standards/GO.md` §Error
Handling — "Happy path at minimal indentation (early returns for errors)" — is fail
fast implemented in Go's idiom. Each function checks its inputs and fails
immediately; it does not attempt to proceed with invalid state.

### Constructors that reject invalid state — DbC applied

Meyer's DbC (cited in §1) backs this: a constructor's postcondition is that the
constructed object is valid. If it can't be, the constructor should fail (return
an error in Go's idiom). The `standards/GO.md` pattern: `func NewXxx(docker
DockerClient, cfg Config) *XxxManager` — the constructor validates its inputs and
either returns a valid manager or an error. No partially-initialized objects.

In Go, this aligns with the "accept interfaces, return structs" principle
(`standards/GO.md` §Code Organization Patterns): returning a concrete struct
from a constructor communicates "this is the thing, fully initialized." An
unexported zero value is an invalid state by convention.

### Network isolation "fail loudly" — D11 (`docs/dev/working-notes.md`)

D11 documents the network-isolation implementation: iptables + ipset, default-deny
with an allowlist. The "fail loudly" behavior: if iptables setup fails (missing
`CAP_NET_ADMIN`, missing `ipset` binary, kernel module not loaded), the sandbox
creation fails immediately with a clear error. It does not proceed with a silently-
unprotected network. This is fail fast at the security boundary: the cost of
proceeding without the intended protection is higher than the cost of failing to
start.

### Go-specific — `errgroup`, `context.Context`, no fire-and-forget goroutines

`standards/GO.md` §Goroutine Discipline: "No fire-and-forget goroutines — every
goroutine must have a shutdown path." A fire-and-forget goroutine is the opposite
of fail-fast: it can fail in the background, invisibly. The `errgroup.Group`
pattern is fail-fast for concurrent operations: the first goroutine to return an
error cancels the context for all others. The error surfaces immediately, not after
a timeout.

---

## §6 — Warnings are signal; suppressions require justification

### golangci-lint and errcheck — project `.golangci.yml`

The project's `.golangci.yml` enables `errcheck`, `govet`, `staticcheck`, `gosec`,
`errorlint`, `nilerr`, `forbidigo`, and others. The linter configuration is the
operative implementation of this principle. Each linter is a category of warnings;
each enabled linter is a declared commitment that warnings in that category are
signal, not noise.

The suppressions in `.golangci.yml` are documented:
- `exhaustruct` and `varnamelen` are disabled with explicit rationale ("noisy,
  fights zero values"; "conflicts with Go's short-variable conventions").
- `forbidigo` exclusions are scoped to `internal/fileutil/fileutil.go` (the package
  that wraps the banned calls) and test files ("Tests and test helpers create temp
  files for their own use").
- `revive`'s `redefines-builtin-id` rule is disabled with the comment:
  "internal/runtime is an intentional name for the runtime abstraction layer."

This pattern — disable only with a comment explaining why — is the principle made
mechanical: suppressions are explicit claims, not silent overrides.

### Go tool philosophy — `go vet` is not optional

The Go project's own design philosophy treats `go vet` as a mandatory step, not
an advisory one. The `go vet` tool ships with the Go toolchain and is documented
at pkg.go.dev/cmd/vet. The Effective Go guide (go.dev/doc/effective_go, 2009) and
the Go FAQ (go.dev/doc/faq) both treat `go vet` output as "should fix, not
suppress." The Go team's position: `go vet` warnings are bugs, not style issues.

golangci-lint runs `go vet` as `govet` (the `govet` linter) alongside the
additional linters. By including `govet` in the enabled set, the project treats
`go vet` findings as hard failures in `make check`, not as advisory.

### "Warnings are signal" — general literature

Bryan Cantrill (Joyent / Oxide Computer) and team have written and spoken about
treating compiler and linter warnings as errors as a culture decision, not a
tooling decision: "When you allow warnings, you get warnings. When you treat
warnings as errors, you discover the bugs." This is documented in various
presentations (Cantrill's talks at GOTO and Strange Loop, 2012–2019) but does not
have a single canonical essay [verify: specific written primary source].

The Go community's strong `errcheck` culture (see §7) is a specific instance of
this broader principle.

---

## §7 — Act on every return value; justify every discard

### errcheck — Go-specific idiom

The `errcheck` linter (github.com/kisielk/errcheck) was created by Kamil Kisielk
[verify: author name] and is one of the oldest Go linters, predating golangci-lint.
It detects unchecked error return values — a common Go bug where a function returns
an error and the caller discards it with `_` or by not capturing the return value
at all.

Effective Go (go.dev/doc/effective_go, "Errors" section): "In Go, error handling
is important. The language's design and conventions encourage you to explicitly check
for errors where they occur." The convention is not merely idiomatic — unchecked
errors are the most common source of silent bugs in Go programs.

Go Code Review Comments (go.dev/wiki/CodeReviewComments): "Don't ignore errors. If
a function returns an error, check it." This is the community standard.

### The `_ = f()` pattern — explicit discard

When a return value genuinely need not be checked (e.g., `defer f.Close()` where
the error cannot be usefully handled in a defer), Go convention is to write
`_ = f.Close()` or accept the value in a named variable and document why it is
not checked. The explicit discard makes the decision visible in code review rather
than silently absent.

`standards/GO.md` §Error Handling: "Never `panic` in library code — return
errors." This is the producer side of the "act on every return value" principle:
domain packages return errors, not panics, so callers can act on them.

### Uber Go Style Guide (2018)

Uber Go Style Guide (github.com/uber-go/guide/blob/master/style.md, maintained
since 2018): §"Handle Errors" — "Do not discard errors using `_` variables. If a
function returns an error, make sure to check it." The guide also documents the
`errors.Is` / `errors.As` pattern for inspecting wrapped errors, which yoloAI uses
(`standards/GO.md` §Error Handling: "Inspect errors with `errors.Is` and
`errors.As`").

### Google Go Style Guide (2022)

Google Go Style (google.github.io/styleguide/go/, published 2022, maintained):
§"Error handling" — "In Go, control flow and error handling are intertwined. The
function that encounters an error is often best positioned to make the decision
about how to handle it." Google's internal style bans global error-absorbing
wrappers for the same reason errcheck exists: every ignored error is a potential
silent failure.

---

## §8 — No half-finished implementations: public beta means breaking changes are OK; partial code is not

### "No half-finished implementations" — distinction between beta and broken

This principle does not have a single primary-source citation; it is a
yoloAI-specific discipline derived from the project's public-beta posture
(`CLAUDE.md` §Project Status: "Breaking changes are allowed but must be tracked in
`docs/BREAKING-CHANGES.md`"). The intellectual backing comes from several directions.

### D16 — Remove legacy shims promptly (`docs/dev/working-notes.md`)

D16 is the worked example. The `runtime-config.json` fallback for the older
`config.json` name was added in commit `fdfe0c3` and removed seven minutes later
in `be22f6a`. The entry states: "Pre-1.0 yoloAI tracks breaking changes in
`docs/BREAKING-CHANGES.md` and removes legacy shims promptly." The rationale:
keeping legacy shims keeps the code surface large. The alternative — "keep legacy
compat indefinitely" — was explicitly rejected.

The distinction the principle draws: "partial code that hides intent" is not
acceptable even in a beta. Code that exists in the codebase must fully express its
intent. Code that half-implements a feature (stub methods that panic, feature flags
that are never set) is worse than no code: it makes the codebase's actual state
opaque.

### Michael Feathers — *Working Effectively with Legacy Code* (2004)

Feathers, M. *Working Effectively with Legacy Code*, Prentice Hall, 2004. Chapter 8
"How Do I Add a Feature?" and Chapter 25 "Dependency-Breaking Techniques" document
the cost of half-finished code: untested, partially-implemented paths that callers
can reach but that fail in unpredictable ways. Feathers' central argument: legacy
code is code without tests, and the cost of half-finished implementations compounds
over time as callers multiply.

Applied to yoloAI: a half-finished backend (e.g., a backend that implements
`Create()` but whose `Exec()` always returns `ErrUnsupported`) makes the
`runtime.Runtime` interface a lie for that backend. Users who select the backend
get partial behavior with no clear indication of what works. The principle is:
either implement completely or don't ship the backend yet.

### `docs/BREAKING-CHANGES.md` as the beta contract

The breaking-changes file is the mechanism that makes "no half-finished
implementations" compatible with "public beta means changes are allowed." The
contract: changes that break existing user workflows are documented with migration
steps, so users are not surprised. This allows the codebase to stay clean (no
vestigial shims) while preserving user trust (changes are announced and explained).

---

## §9 — Plan-then-execute on cleanup: avoid opportunistic refactors that don't compose

### D19 — Architecture remediation cycles (`docs/dev/working-notes.md`)

D19 is the primary internal source. The architecture remediation process:
"Periodic architecture audits produce a numbered remediation plan (W1, W2, …).
Each work item is a discrete commit. The plan tracks status."

The rejected alternative: "Refactor opportunistically — rejected because
opportunistic refactors don't compose; the W-plan ensures the bundle lands as a
coherent shape." This is the principle in one sentence: an opportunistic refactor
of one file, done in the middle of a feature, creates a mixed commit that is
harder to review, harder to revert, and harder to reason about than a planned
refactor that ships as its own committed work item.

### Michael Feathers — the cost of incremental cleanup without a plan

Feathers (2004, cited in §8) documents the failure mode: incremental cleanup done
during feature work. Each feature adds "a little cleanup here" and "a little
cleanup there." The result is a codebase that was always "being cleaned up" but
was never actually clean — because the cleanup decisions were not coherent across
the changes. The W-plan approach is the antidote: audit, plan, execute discretely.

### Software architecture — Martin Fowler on refactoring cadence

Fowler, M. *Refactoring: Improving the Design of Existing Code*, Addison-Wesley,
1999 (second edition 2018 with JavaScript examples). The relevant principle: large
refactors should be broken into small, independently shippable steps. Fowler
documents the failure of "big-bang refactors" — attempts to refactor everything
at once. The W-numbered plan at yoloAI is the Fowler model in practice: each W-item
is small, ships as one commit, and composes with the others because they were
planned together.

The 2026-05 architecture remediation (D19) shipped W1a–W14 as discrete commits
(commit `868a5b0` through `1f4457c`), each touching a bounded scope. W12 carved
`sandbox/` into `archetype/`, `patch/`, and `store/` subpackages. W11 registered
`(factory, descriptor)` tuples in the runtime registry. These were planned
dependencies of each other; no individual commit left the codebase in an
intermediate broken state.

---

## §10 — Code quality gate: `make check` is the mandatory final step

### D20 — `make check` enforcement via Claude Code Stop hook (`docs/dev/working-notes.md`)

D20 is the primary internal source. The Stop hook (``.claude/hooks/on-stop.sh``)
runs `make check` before any AI-assisted edit can complete. If `make check` fails,
the hook blocks completion and feeds the output back. Rationale: "Code quality
gates work only when they can't be skipped. Putting the gate in the agent's stop
sequence makes it structural."

The `make check` quality gate runs: `gofmt` verification, `golangci-lint`, `go mod
tidy` check, all Go tests (`go test ./...`), and the Python test/typecheck targets
(`pytest` + `mypy` for `runtime/monitor/`). The multi-target design ensures that
no single check can be silently skipped by passing another.

### Continuous Integration philosophy — general practice

The concept of a mandatory quality gate is foundational to Continuous Integration.
Martin Fowler introduced CI as a practice in: Fowler, M. and Foemmel, M.
"Continuous Integration," thoughtworks.com, 2000 (updated 2006 at martinfowler.com/
articles/continuousIntegration.html). Fowler's principle: "the build must always
be passing." A CI gate that allows a broken build to merge defeats its own purpose.

The `make check` gate at yoloAI is CI applied to the individual edit, not to the
merge. The Stop hook enforces it at the editing layer; the GitHub Actions workflow
(if present) enforces it at the merge layer. The Stop hook is the first gate; it
catches failures where the cost of fixing is smallest.

### "You can't manage what you can't measure" — applied to quality gates

The Fowler CI article makes explicit what makes quality gates work: consistency.
"The whole point of Continuous Integration is to provide rapid feedback so that
if a defect is introduced into the codebase it can be identified and corrected as
soon as possible." For a single-author project, "rapid feedback" means "before the
author moves on to the next problem." The Stop hook provides this: it blocks
completion until the quality check passes, keeping the feedback loop tight.

The Python portion of `make check` (`runtime/monitor/tests/`) being included in
the gate is notable: the gate covers not just the Go surface but the Python typed
surface. The CODING-STANDARD note: "The targets skip silently if `pytest`/`mypy`
aren't installed, so fresh clones still get a green `make check`." This is
Convention over Configuration for the quality gate: sensible default behavior
(skip if deps absent), explicit opt-in for full checking (`make setup-dev-python`).

---

## §11 — Iterate when the first approach doesn't work

### D14 — Pluggable idle detection (`docs/dev/working-notes.md`)

D14 is the primary worked example. The decision log documents four rejected
approaches before the pluggable design:

1. "Tmux `window_bell_flag` polling — tried, broken (`pane_last_activity` doesn't
   update for TUI agents)."
2. "Fixed-delay polling — tried, flapped between active/idle."
3. A single global detector — rejected because agents differ structurally.
4. Various intermediate designs documented in `docs/dev/research/idle-detection.md`.

The accepted design: per-agent `IdleSupport` strategy, a Python `status-monitor`
inside the sandbox writing `agent-status.json`. The design is pluggable because
the four failed attempts revealed that no single approach works across agents.

The principle this demonstrates: when an approach doesn't work, the right response
is to understand *why* it doesn't work and redesign at the level where the
assumption failed. All four rejected approaches assumed idle detection could be
centralized. The correct redesign moved idle detection into the per-agent
definition — a structural change, not a parameter tweak.

### "Stop and rethink" — CLAUDE.md operating principle

The global `CLAUDE.md` states it explicitly: "If an approach isn't working or
feels overcomplicated, stop and rethink rather than immediately switching tactics."
This is the principle behind D14: four failed approaches before the structural
redesign, not five quick tactical patches.

### Don't just iterate, diagnose — Kent Beck, "red-green-refactor"

Kent Beck's Test-Driven Development (TDD) red-green-refactor cycle (Beck, K.
*Test-Driven Development: By Example*, Addison-Wesley, 2002) contains an implicit
iteration principle: when the test doesn't pass, you don't just try another
implementation at random. You understand why the current implementation fails, then
write the smallest passing implementation, then refactor. The cycle is diagnostic
(understand the failure), not trial-and-error.

Applied to yoloAI's idle detection: each failed approach taught something about the
problem. `pane_last_activity` doesn't update for TUI agents → the problem is not
"which tmux variable" but "tmux variables don't reflect TUI paint events at all."
Fixed-delay polling flaps → the problem is not "what delay" but "polling interval
and agent state change interval are not aligned and can't be without in-agent
cooperation." The pluggable design follows from these diagnoses: the only stable
signal is in-agent (hook-based for Claude Code) or screen-stabilization for agents
that don't emit hooks.

### research/idle-detection.md as a failure-mode record

Internal source: `docs/dev/research/idle-detection.md`. This file documents the
trail of rejected approaches for idle detection. It exists because each failed
approach is a piece of knowledge: it says "this approach doesn't work for this
reason." Future contributors or future selves who encounter a similar problem can
consult it rather than repeating the investigation.

This is the "document the no" principle (general-principles §8) applied to
iteration: the rejected approaches are the knowledge, and they belong in a
research file, not in a commit message that gets archived.

---

## Verification notes

The following attributions were not independently confirmed at writing time and
are marked [verify] where they appear in the document:

- David Holub's "Getter and Setter Methods Are Evil" exact publication venue and
  date (JavaWorld, 2003) — the source is widely attributed but the exact original
  URL was not confirmed at writing time.
- Bryan Cantrill essays on "treat warnings as errors" — the principle is documented
  in his public talks but a specific written primary source was not confirmed.
- `errcheck` author name "Kamil Kisielk" — check the GitHub repository
  (github.com/kisielk/errcheck) for correct attribution.

All other sources cited above are primary sources verified to exist at the URLs or
library records cited, or are internal yoloAI sources (D-entries, commits) verified
in the repository as of 2026-05-21.
