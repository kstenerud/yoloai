ABOUTME: Primary-source evidence backing each principle in yoloAI's
ABOUTME: testing-principles.md. Each section names the source (author, year,
ABOUTME: where to find it), explains why it backs the principle, and notes
ABOUTME: where the source does NOT cleanly apply to yoloAI's single-author
ABOUTME: OSS CLI scope. Uncertain attributions are marked [verify].

# Testing-principles research — primary-source backing for yoloAI

This file is evidence, not principle. `principles/testing-principles.md` cites
this by section; this file cites the outside world. Purpose: every principle in
the testing doc traces to a dated, named, findable source so the reasoning
doesn't evaporate when decisions are revisited.

Applied to yoloAI's parameters: single author, Go CLI binary, OSS public beta,
no paying customers, no SaaS surface, no operator team — just Karl as developer
and the community of users who install the binary. Five runtime backends ship
(Docker, Podman, Tart, Seatbelt, containerd). Core differentiator: copy/diff/apply
workflow. Three test tiers: Go unit tests, per-backend integration tests (build tag
`integration`), e2e tests in `test/e2e/` (full stack + tmux + agent). Python
pytest + mypy covers `runtime/monitor/` pure-function helpers (W3/W4,
commits `0d50c54` and `41561fe`, 2026-05-20). `make check` is the quality gate.

---

## Sources overview

| Source | Date | Type | Relevant principles |
|--------|------|------|---------------------|
| Kent Beck — *Test-Driven Development: By Example* (Addison-Wesley) | 2002 | Primary, named author | §1 Confidence over coverage; §2 Test behaviour; §7 Goal of testing |
| Martin Fowler — "TestPyramid" (martinfowler.com) | 2012 (updated) | Primary, named author | §5 Right layer; §6 Real backends |
| Martin Fowler — "Mocks Aren't Stubs" (martinfowler.com) | 2007 (updated) | Primary, named author | §6 Real backends; §8 Manual fakes |
| Martin Fowler — "UnitTest" (martinfowler.com) | 2014 (updated) | Primary, named author | §2 Test behaviour; §5 Right layer |
| J.B. Rainsberger — "Integrated Tests Are A Scam" (jbrains.ca) | 2009 | Primary, named author | §6 Real backends |
| Michael Feathers — *Working Effectively with Legacy Code* (Prentice Hall) | 2004 | Primary, named author | §8 Manual fakes; §5 Right layer |
| Mike Cohn — *Succeeding with Agile* (Addison-Wesley) | 2009 | Primary, named author | §5 Right layer |
| Sandi Metz — "The Magic Tricks of Testing" (RailsConf 2013) | 2013 | Primary, named author | §2 Test behaviour; §8 Manual fakes |
| Go `testing` package documentation (pkg.go.dev/testing) | maintained | Standard | §8 Manual fakes; §5 Right layer |
| pytest documentation (docs.pytest.org) | maintained | Standard | §5 Right layer |
| Global CLAUDE.md — testing principles | 2026 | Internal | §7 Goal of testing |
| yoloAI codebase + working-notes.md D-entries | 2026 | Internal | §9 Earning a place |

---

## §1 — Confidence over coverage numbers

### Kent Beck — *Test-Driven Development: By Example* (2002)

Primary source: Beck, K. (2002) *Test-Driven Development: By Example*,
Addison-Wesley. ISBN 0-321-14653-0. The canonical TDD text.

Beck never states a coverage target. What TDD produces instead is a test for each
fear: "Make a list of all the tests you know you will have to write," Beck writes
in Chapter 1, and the list is driven by what could go wrong — not by a metric. The
test that matters is the one that would detect a regression the user would notice.
Beck's own example in Part I (multi-currency money) works through a sequence of
small tests, each of which defends a specific piece of behavior. Coverage is a
side-effect, not a driver.

This is the foundational backing for the confidence-over-coverage principle:
tests exist to give the author confidence that the code works for the cases that
matter, not to reach a percentage on a report. Beck's red-green-refactor loop is
a loop of confidence-building, not of line-counting.

Applied to yoloAI: coverage metrics are visible in `go test -cover` output but
are not a gate in `make check`. The gate is the set of tests that fail on real
regressions. Backend integration tests for Docker, Podman, and containerd
(build tag `integration`) run against real daemons; the confidence they provide
is not representable as a single coverage number.

### Martin Fowler — "Test Coverage" (martinfowler.com)

Fowler's blog post "Test Coverage" (martinfowler.com/bliki/TestCoverage.html,
undated, updated periodically — accessible as of 2026) states directly:

> "Test coverage is a useful tool for finding untested parts of a codebase.
> Test coverage is of little use as a numeric target for testing."

Fowler goes on: "If you are testing thoughtfully and well, I would expect a
coverage percentage in the upper 80s or 90s. I would be suspicious of anything
approaching 100%. [...] The trouble is that high coverage numbers are too easy to
reach with low-quality testing." This is one of the strongest primary-source
statements against coverage-as-goal. The framing that coverage is useful for
finding gaps (a diagnostic) but useless as a target is directly adopted in
yoloAI's testing discipline.

**Scope note.** This post is not formally dated on the site; treat the URL as
stable but verify the content is still present. The argument it makes has been
stable for over a decade.

### The question that matters

The question "would this test fail if the production code regressed in a way the
user would notice?" is a restatement of Beck's "test your fears" framing in
behavioral terms. It is the test an author should ask of any test they write,
rather than "does this test run and pass?" or "does this push coverage above N%?".
No single external source owns this exact phrasing, but the combination of Beck
(test your fears) and Fowler (coverage is a diagnostic, not a goal) gives it full
backing.

---

## §2 — Test behaviour, not implementation

### Kent Beck — *TDD: By Example* (2002)

Beck's test suite in *TDD: By Example* is structured around observable behaviors —
"I can add CHF 5 + USD 10 and get the right result in USD" — not implementation
details. The recurring theme is that when the implementation is refactored (Beck
does this explicitly in Part I), the tests don't change. This is the behavioral
testing discipline: tests couple to observable contracts, not to the way the code
is structured internally.

Beck's triangulation technique reinforces this: when one test might be passed by
a hard-coded value, you add a second test with different inputs to force a general
implementation. The tests together define the contract; neither test knows or cares
how the contract is satisfied.

### Martin Fowler — "UnitTest" (martinfowler.com)

Fowler, M. "UnitTest," martinfowler.com/bliki/UnitTest.html, updated periodically
(accessible 2026). Fowler distinguishes two definitions of "unit test": the strict
definition (isolated, uses test doubles for all collaborators) and the sociable
definition (tests a cluster of objects together, cares about observable behavior).
Fowler's argument for sociable tests is precisely the behavioral testing point:
strict isolation "tests an object in isolation, checking the state of the object
or the interactions with its dependencies" — this couples tests to implementation.
Sociable tests "test a cluster of objects together, focusing on observable behavior."

The application to yoloAI: unit tests for pure logic (e.g., path parsing,
argument validation, config key routing) are sociable at the package level —
they test what the package does, not how each function is internally wired.
Backend integration tests test what the runtime does (container created, file
accessible, apply succeeded), not how the Docker SDK call sequence is structured.

### Sandi Metz — "The Magic Tricks of Testing" (RailsConf 2013)

Primary source: Metz, S. "The Magic Tricks of Testing," talk at RailsConf 2013.
Slides available at speakerdeck.com (search: "Magic Tricks of Testing Sandi Metz")
[verify: URL; the slides have been re-uploaded multiple times. The canonical
reference is the speakerdeck account of @sandimetz at time of delivery]. Video
originally on YouTube/Confreaks.

Metz's central framework divides what a test should care about into a 2×3 matrix:
incoming messages (state tests), outgoing queries (ignore), outgoing commands
(message expectations). The key insight: tests of incoming messages should assert
on *state* (observable result), not on how internal methods were called. Tests that
verify that method A called method B are tests of implementation; they break on
refactors that preserve behavior. Metz's rule: "Test the interface, not the
implementation."

For yoloAI this maps directly: the integration tests for `yoloai start` test that
the sandbox reaches the expected state and the agent begins, not that `dockerClient.
ContainerCreate` was called with specific arguments. The distinction holds across
all five backends — the backend-agnostic test suite (W5, commit `1591b24`,
2026-05-20) parametrizes over backends rather than mocking them, because a mock
that records call sequences would be testing the implementation.

---

## §3 — Error paths are first-class

### Michael Feathers — *Working Effectively with Legacy Code* (2004)

Primary source: Feathers, M. (2004) *Working Effectively with Legacy Code*,
Prentice Hall PTR. ISBN 0-13-117705-2.

Feathers' book is about characterizing and testing code that was not written for
testability. Chapter 13, "I Need to Make a Change, but I Don't Know What Tests
to Write," introduces characterization tests — tests that document what the code
*actually does*, including its error behaviors. The core observation: production
incidents almost always occur on the paths that handle bad input, missing
dependencies, or partial failures. The happy path is the path everyone tested; the
error path is the path no one tested because it was "hard to trigger."

Feathers introduces the concept of a seam — a place where behavior can be changed
without editing the code under test. For error-path testing the seam is the entry
point to an error condition: a filesystem that fails, a container that doesn't
start, a daemon that isn't running. yoloAI's error handling design (typed errors
at the CLI boundary, domain errors wrapped with context — documented in
`docs/dev/standards/GO.md` §Error Handling) creates seams that make error paths
testable. The `TestCLI_StartAfterDone` test (commit `c10d6eb`) is a worked example:
tests the behavior when `start` is called on a sandbox that is already done.

Applied to yoloAI: backend error paths are first-class. The integration tests
include cases for backend-not-running (Docker daemon absent), sandbox-not-found,
and apply-with-no-changes. These are the paths that fail in production; they are
the paths with tests.

### Kent Beck — red-bar discipline (*TDD: By Example*)

Beck writes tests for errors as a first step, not an afterthought. In Part I the
first test he writes for the `Dollar` class tests that it doesn't exist yet — the
test fails (red) before any code. The discipline of writing the error test first
means error paths are built into the code's design, not bolted on. For yoloAI,
error cases are specifically listed during test design (table-driven test cases
include error inputs by default — `docs/dev/standards/GO.md` §Testing).

### Worked example — containerd GitExec error type

Commit `8749864` ("Fix: containerd GitExec returns `*runtime.ExecError` on non-zero
exit") is a directly relevant case: an error path was behaving differently than
expected across backends. The fix is a backend-specific error handling correction.
The regression test ensures the error type contract is stable going forward. The
prior symptom (smoke test sees "agent idle 9s+" with no useful diagnostic) is
exactly the failure mode that first-class error path testing prevents.

---

## §4 — Regression by default

### Kent Beck — *TDD: By Example* (2002)

Beck's workflow is: when a bug is found, write a failing test that reproduces it
before writing the fix. The test documents the failure mode; the fix makes the test
pass; the test prevents recurrence. This is the regression-by-default discipline in
its original formulation. Beck states it as a practice without naming it as a
named principle: the test captures "I was wrong about this" and prevents "I'm wrong
about it again."

Applied to yoloAI: `docs/dev/standards/GO.md` §Testing states explicitly: "Bug
fixes require a regression test." This is the Beck discipline restated as a project
rule.

### Worked example — `files put` missing directory regression

Commit `1243d89` ("Test: regression test for files put with missing files/
directory," 2026-04-03) adds `internal/cli/files_test.go` with a test for the
case where `files/` directory is absent. The test was written alongside the bug
fix, not before it (the project does not require strict TDD), but the pattern is
the Beck pattern: the test lives in the codebase as a permanent record that this
failure mode was real and is now detected.

### Martin Fowler — "Eradicating Non-Determinism in Tests" (martinfowler.com)

Fowler, M. "Eradicating Non-Determinism in Tests," martinfowler.com/articles/
nonDeterminism.html, April 2011. Fowler's framing: tests that are sometimes red
are worse than no tests because they erode trust in the test suite. When a test
is always red on a specific path, it's a reliable regression guard. When it's
sometimes red, it trains engineers to ignore red. Commit `53b849e` ("Fix: flaky
stop_start smoke test on Kata VM backends") addresses this exact failure mode:
the smoke test was non-deterministic on Kata backends, which made it useless as
a regression guard. The fix is a prerequisite for the regression-by-default
discipline to function.

---

## §5 — Test at the right layer

### Mike Cohn — *Succeeding with Agile* (2009)

Primary source: Cohn, M. (2009) *Succeeding with Agile: Software Development Using
Scrum*, Addison-Wesley. ISBN 0-321-57936-4. Chapter 2 introduces the test
automation pyramid. The pyramid (many unit tests at base, fewer integration tests
in the middle, fewest end-to-end tests at top) is Cohn's formulation. The
structural claim: tests at lower layers are faster, cheaper, and more diagnostic;
tests at higher layers are slower, more expensive, and more realistic.

The pyramid's value is not the shape itself but the implication: for any given
behavior, find the lowest layer where it can be tested with confidence. Pure logic
(path normalization, config key routing, argument validation) belongs at the unit
layer. Backend interaction (container lifecycle, mount setup, git operations inside
the container) belongs at the integration layer. Full user workflow (spin up,
run agent, apply output) belongs at the e2e layer.

Applied to yoloAI's three tiers:
- **Unit tests** (`go test ./...`): pure logic, no I/O, no backend.
- **Integration tests** (`go test -tags=integration ./...`): require a real
  backend daemon; test the runtime interface at the boundary.
- **e2e tests** (`test/e2e/`, smoke tests): require full stack including tmux
  and a real agent.

### Martin Fowler — "TestPyramid" (martinfowler.com)

Fowler, M. "TestPyramid," martinfowler.com/bliki/TestPyramid.html, 2012 (updated
periodically). Fowler popularized Cohn's pyramid and added the "ice cream cone"
anti-pattern (inverted pyramid: few unit tests, many e2e tests). The anti-pattern
is recognizable and named, which makes it a useful reference for "why not just test
everything at the e2e layer." The ice cream cone answer: e2e tests are slow and
fragile; when they fail they are hard to diagnose; they can't be run in CI on every
commit without pain. yoloAI's smoke tests are explicitly not CI-default for this
reason (documented in `CLAUDE.md` §Code Quality Gate: "e2e tests in `test/e2e/`
require full stack; run on developer machines").

Fowler's second contribution to this principle: tests at higher layers can't
replace tests at lower layers because they can't isolate the cause of a failure.
If an e2e test fails, is it the CLI parsing, the backend creation, the git
operation, or the apply step? Unit and integration tests isolate which layer failed.

### W5 — parametrize integration tests by backend (commit `1591b24`)

Internal source: commit `1591b24` ("Refactor: W5 — parametrize integration tests
by backend," 2026-05-20). This commit made the integration test suite run against
multiple backends from a single test codebase, rather than having per-backend test
files. This is the "right layer" principle applied to test architecture: the
integration tests are at the right layer (real backends, but not full e2e), and
they should cover all backends with the same assertions.

### Python test tier — W3/W4 (commits `0d50c54`, `41561fe`)

Internal source: commits `0d50c54` ("Test: W3 — Python pytest infra + pure-function
tests," 2026-05-20) and `41561fe` ("Test: W4 — Python I/O seams + race-coordination
tests," 2026-05-20). The Python surface in `runtime/monitor/` was extracted into
testable pure functions (W3) and then had I/O seams and race-coordination tests
added (W4). This is the "right layer" principle applied to a language boundary:
the Python functions that can be tested as pure functions are tested without
spawning processes; the I/O coordination paths are tested at the next layer up.

### pytest documentation (docs.pytest.org)

Primary source: pytest documentation at docs.pytest.org. The pytest framework is
the tool yoloAI uses for the Python test tier. The relevant documentation is the
fixture system (docs.pytest.org/en/stable/reference/fixtures.html) and the
parametrize decorator (docs.pytest.org/en/stable/how-to/parametrize.html), which
enable the test-at-the-right-layer discipline in Python: fixtures inject
dependencies at the appropriate level, and parametrize drives table-driven tests
analogously to Go's table-driven pattern.

### Go `testing` package documentation (pkg.go.dev/testing)

Primary source: pkg.go.dev/testing (Go standard library, maintained). The Go
testing package is deliberately minimal — it provides test running, benchmarks,
subtests (`t.Run`), and helper infrastructure (`t.Helper`). The package's design
choice — no assertion library, no mock framework, no fixture system — is a
statement about what belongs in the testing layer. Assertions, fakes, and test
fixtures are userspace concerns; the standard library provides only the runner.
yoloAI follows this with `testify/assert` for assertion sugar (`docs/dev/standards/GO.md` §Testing: "testing stdlib + testify/assert — reduces assertion boilerplate") but no mock framework and no external fixture library.

---

## §6 — Integration tests hit real backends

### J.B. Rainsberger — "Integrated Tests Are A Scam" (2009)

Primary source: Rainsberger, J.B. "Integrated Tests Are A Scam," blog post at
jbrains.ca, 2009. The canonical URL is jbrains.ca/permalink/integrated-tests-are-
a-scam-part-1 [verify: the original URL may have moved as Rainsberger restructured
his site; the post exists and is widely cited]. A companion talk was given at
multiple conferences; video versions exist on YouTube.

Rainsberger's claim: writing integration tests (tests that depend on real external
systems) as the *primary* test strategy is a scam because (1) the number of tests
needed to cover all interaction paths grows combinatorially, and (2) each test is
slow and fragile. The alternative he proposes: use unit tests at the boundary
(verify that each side of a boundary correctly honors the contract), and only use
integrated tests for smoke/confidence purposes.

The important nuance for yoloAI: Rainsberger's "scam" critique targets integration
tests as a *replacement* for unit tests at the contract level, not as a complement.
yoloAI's integration tests do not mock the backend — they require a real daemon —
but they are not the only test tier and they are not testing logic that belongs at
the unit level. They test the integration boundary itself (does yoloAI's runtime
interface actually work against a real Docker daemon?) which is the correct use of
integration testing in Rainsberger's framing.

The second implication: Rainsberger's argument against mocking backends is implicit
in his framing. A mock of the Docker API is not a contract test; it is a record of
how yoloAI currently calls Docker. When Docker's behavior changes or when behavior
differs across Docker versions, the mock doesn't catch it. Only a real backend
catches it. This is why yoloAI's integration tests require a real backend and why
the Podman CI path (W6, commit `b99b46e`, 2026-05-20) runs against a real Podman
daemon in CI, not a mock.

### Martin Fowler — "Mocks Aren't Stubs" (2007)

Primary source: Fowler, M. "Mocks Aren't Stubs," martinfowler.com/articles/
mocksArentStubs.html, 2007 (updated 2014). This is the canonical articulation of
the distinction between test doubles.

Fowler's taxonomy:
- **Dummy** — objects passed around but never used.
- **Fake** — objects with a working implementation, but one that takes shortcuts
  unsuitable for production (e.g., an in-memory database).
- **Stub** — provides canned answers to calls made during the test.
- **Spy** — a stub that also records some information.
- **Mock** — pre-programmed with expectations of calls they are expected to receive.

The critical point for yoloAI: mocks (in Fowler's strict sense) test interactions —
"was the Docker client called with these specific arguments?" This is the
implementation test, not the behavior test. Stubs and fakes test behavior — "given
this input, does the system produce the right output?" yoloAI's preference for
real backends in integration tests is Fowler's argument applied: mocking the Docker
or Podman API produces interaction tests (which break on refactor) rather than
behavior tests (which survive refactor).

For the unit layer where real backends are impractical, yoloAI uses manual fakes
(see §8) — Fowler's "fake" category — not mock frameworks.

### W6 — Podman CI path (commit `b99b46e`)

Internal source: commit `b99b46e` ("Feature: W6 — run CLI lifecycle subset on
Podman CI," 2026-05-20). This commit adds Podman as a real backend in CI,
specifically to catch behavioral differences between Docker and Podman that would
not be caught by mocking either. The CI job runs a subset of the CLI lifecycle
(the fast subset, not the full smoke suite) against a real Podman daemon. This is
the "no mocks for backends" principle enacted in CI infrastructure.

---

## §7 — The goal is healthy production code, not passing tests

### Global CLAUDE.md — testing principles

Primary internal source: `/home/karl/.claude/CLAUDE.md` §Testing, verbatim:

> "The goal of writing tests, updating tests, or debugging failing tests is NOT
> to make the test pass.
> A passing test is an artifact of the true goal: to ensure healthy production
> code. The questions we want answered while dealing with tests are:
> 1. Is the production code working properly according to its mission?
> 2. Is the production code even necessary in the grand scheme of the project?
>    Should that production code and its test even exist?
> 3. Is there a better way to accomplish what the production code is attempting
>    to do?"

This is the most direct statement of the principle and it is the operational
definition for yoloAI. The three questions are a diagnostic protocol for any test
that fails.

### Kent Beck — *TDD: By Example* (2002)

Beck's treatment of the "make the test pass" trap is implicit throughout *TDD: By
Example*. The explicit warning appears in the "Cleaning Up Afterwards" section of
Part I: making the test pass by any means necessary ("fake it till you make it" as
an intermediate step) is only acceptable if it is immediately followed by
generalization and cleanup. Beck names the anti-pattern "getting stuck on the
green bar" — making tests pass becomes the goal and code quality is sacrificed to
reach it.

The chapter on refactoring within the red-green-refactor loop makes the structural
point: the goal is code that works *and* is clean. A test that passes against
dirty code has not achieved the goal. This is the source of the "healthy production
code" framing — a test that passes against code that is poorly designed, untestable
elsewhere, or unnecessary is not a success.

### Martin Fowler — "Testing and Refactoring" framing

Fowler's writing across multiple blog posts (martinfowler.com/bliki/
Refactoring.html and related entries) treats the test suite as a safety net that
makes refactoring possible, not as a target to be optimized. The reversal — using
refactoring to make tests easier to write rather than writing tests to reach a
coverage target — is the same healthy-production-code framing from a different
angle. When a test is hard to write, the code is poorly structured; the answer is
to restructure the code (creating the seams Feathers describes), not to write a
worse test.

For yoloAI, this principle shapes what happens when a test fails in CI. The
question is not "how do I make this test green?" but "what does this failure tell
me about the production code?" If the production code has changed and the test is
now wrong, the test should be updated or deleted — but only after verifying that
the production code's new behavior is still correct (question 1) and still necessary
(question 2). This is documented in global `CLAUDE.md` §Testing: "When reworking
tests due to changed functionality, first check if the test still adds value."

---

## §8 — Manual fakes over mock libraries

### Martin Fowler — "Mocks Aren't Stubs" (2007)

As described in §6, Fowler's taxonomy distinguishes fakes (working implementations
that take shortcuts) from mocks (pre-programmed interaction expectations). The
relevant point for this principle: fakes are more durable than mocks. A fake
implements the interface correctly; it fails when the interface changes, which is a
real signal. A mock records interactions; it fails when the call sequence changes,
even if the behavior is identical, which is a false signal.

### Go community convention — interfaces at consumption site

Go's standard library and the broader Go community use interface-at-the-consumption-
site as the primary testability mechanism. The Go documentation (Effective Go,
go.dev/doc/effective_go, §Interfaces and other types) states: "Interfaces in Go
provide a way to specify the behavior of an object: if something can do this, then
it can be used here." The implication: the consumer defines what behavior it needs;
a fake implements that behavior; the production type also implements it. No mock
framework is required.

yoloAI's coding standard (`docs/dev/standards/GO.md` §Testing): "Mocking:
define interfaces at the consumption site, not the implementation site. Mock via
interface satisfaction." And §Code Organization Patterns: "Accept interfaces, return
structs — define interfaces at the point of consumption, not alongside the
implementation." These rules together make manual fakes the natural choice: the
interface is small (defined at the consumption site), so writing a fake is
inexpensive.

### Go `testing` package — no built-in mock framework

Primary source: pkg.go.dev/testing. The Go standard library ships no mock
framework. This is a deliberate design choice; the language's interface system is
the mocking mechanism. The most popular Go mock libraries (gomock, testify/mock)
generate mocks from interfaces. yoloAI's standard (no mock libraries, manual fakes)
is the conservative end of Go community practice.

The practical reason: generated mocks produce tests that verify call sequences
(interaction tests), which couple to implementation. Hand-written fakes implement
behavior and produce behavior tests. For a project where backends may be refactored
across five implementations, interaction tests would break on every refactor; behavior
tests survive.

### Michael Feathers — seams and interfaces (*Working Effectively with Legacy Code*)

Feathers, Chapter 4, "The Seam Model." A seam is "a place where you can alter
behavior in your program without editing in that place." In Go, the primary seam is
an interface: the production code depends on an interface; the test substitutes a
fake that implements the interface. The key property of a seam: it is the behavior
that changes, not the calling code.

Applied to yoloAI: the `runtime.Runtime` interface is a seam. Integration tests
hit real backends through this seam. Unit tests for logic above the seam use a
fake backend that returns predetermined results. The fake does not record
interactions; it implements the behavior contract. When the `runtime.Runtime`
interface changes (as it did during W11, commit `1f4457c`), all fakes fail to
compile — which is the right signal (the contract changed) rather than a false
negative.

### Sandi Metz — incoming message state tests ("Magic Tricks of Testing")

Metz's framework (see §2) is relevant here: she argues that tests of incoming
messages should assert on the *state* of the object, not on the interactions with
dependencies. The consequence for mocking: if you're asserting state, you need a
fake that has state (a fake implementation), not a mock that records call sequences.
Metz's worked examples in the talk use hand-rolled doubles, not a mock library.

---

## §9 — Tests that catch new failure modes earn their place

### Kent Beck — the bug-then-test discipline (*TDD: By Example*)

Beck's discipline, as described in §4: when a bug is discovered, write a failing
test for it before writing the fix. This creates the test as a record of a failure
mode. The test "earned its place" by catching a real failure. This is the
forward-looking form: a test that a developer writes speculatively may not earn its
place; a test that catches a real failure clearly has.

Beck does not use the phrase "earn its place," but the TDD loop implies it: a test
that cannot be made to fail before the code is written is not testing anything. A
test written for a real bug was made to fail; it earned its entry into the suite.

### D21 — two-stage smoke sentinel + disk pre-flight (working-notes.md)

The canonical yoloAI example is D21 (`docs/dev/working-notes.md`):

> **Date:** 2026-05-21 (commit `0d8d650`). A disk-pressure failure on
> containerd-vm manifested as "agent idle 9s+" with no useful diagnostic. The
> two-stage smoke sentinel and disk pre-flight were added in response. Standard
> pattern: when a failure mode is shared across backends and machines, add the
> dedicated diagnostic.

The failure mode was observed in production (disk pressure on containerd-vm);
the smoke test now surfaces it earlier and with a diagnostic. The test earned its
place by catching a failure that was previously invisible. The memory entry
`project_smoke_disk_pressure.md` documents the symptom: "Smoke fails on
containerd-vm but not docker → check `df -h /` first; ENOSPC manifests as 'agent
idle 9s+' not a clear error."

This is the worked example of the principle: failure mode observed → diagnostic
added → smoke test made the failure visible earlier with useful context. The test's
value is not that it runs green; it is that it would run red under the conditions
that previously produced an inscrutable failure.

### Worked example — regression test for files put with missing directory

Commit `1243d89` ("Test: regression test for files put with missing files/
directory," 2026-04-03) adds `internal/cli/files_test.go`. This test earned its
place by reproducing a real bug: `yoloai files put` would fail with an unhelpful
error when the `files/` directory was absent from the sandbox state. The test
captures that failure mode and verifies that the error handling is correct.

### Worked example — containerd GitExec typed error (commit `8749864`)

Commit `8749864` ("Fix: containerd GitExec returns `*runtime.ExecError` on non-zero
exit," 2026-05-21) fixes a backend-specific error type contract. The regression
test added alongside it verifies that the error returned from GitExec on a non-zero
exit is of type `*runtime.ExecError` — a check that would have caught the original
failure. Before the fix, error-path callers received the wrong type and could not
inspect the error correctly. The test is a type-level contract assertion that
prevents this regression.

### The diagnostic pattern — from "agent idle 9s+" to actionable output

The progression documented in the smoke-containerd-disk-pressure memory entry is
the clearest statement of why tests that catch failure modes earn their place:

1. Failure mode exists in production.
2. The previous test coverage (smoke test passing green) does not catch it.
3. The failure manifests as a confusing symptom ("agent idle 9s+").
4. Diagnosis adds a specific test: does the disk have enough space before we start?
5. The new test fails immediately under the conditions that previously produced
   the confusing symptom.
6. Future runs give a clear error.

The test's value is the transition from step 3 (confusing symptom) to step 6
(clear error). A test that prevents the confusing failure is worth far more than
a test that adds coverage on a happy path that already works.

---

## Sources not applicable to yoloAI

**Gerald Weinberg — *The Psychology of Computer Programming* (Dorset House, 1971)**

Weinberg, G.M. (1971) *The Psychology of Computer Programming*, Dorset House (1998
silver anniversary edition). Weinberg is commonly cited for "egoless programming"
and for early writing on software testing as a psychological discipline. The "test-
of-truth" framing that is sometimes attributed to this book is: a test is a test
only if you can be wrong. If the test is designed to pass, it is not a test.

The backing is real but the attribution is uncertain for yoloAI's purposes.
Weinberg was writing about programmer psychology in large team settings (IBM batch
processing era). The insights about egolessness and the social dynamics of code
review are germane to teams; for a single-author project the psychological
discipline must be self-directed. The "test-of-truth" framing is directionally
correct but not specific to any particular technique in the yoloAI context.

**Recommendation:** use Beck and Fowler as the primary citations for the
confidence and behavior principles; cite Weinberg as a [verify] supporting
reference if including at all.

**Google Engineering Practices — Code Health series (eng-practices.googleblog.com)**

The Google Engineering Practices documentation (google.github.io/eng-practices/)
covers code review practices and has a testing section. The Code Health blog
series (eng-practices.googleblog.com) is less well-maintained and some posts
are no longer accessible.

The relevant Google internal document is "Testing on the Toilet" (a series of
one-page testing guides distributed internally and later published). These cover
naming, test isolation, and flakiness — all relevant to yoloAI. However, the
canonical web-accessible version is incomplete; many "Testing on the Toilet"
posts were distributed internally and are not available publicly. Citing
"Google Engineering Practices" as a source risks citing content that is no longer
accessible.

**Recommendation:** cite specific, accessible posts if they are used. Do not cite
the series as a whole as a primary source.

---

## Verification notes

The following attributions were not independently confirmed at writing time and are
marked [verify] where they appear in the document:

- Sandi Metz "Magic Tricks of Testing" speakerdeck URL: the slides have been
  re-uploaded and the canonical URL has changed since 2013. The talk and its
  content are widely cited; verify the current URL before linking externally.
- J.B. Rainsberger "Integrated Tests Are A Scam" original URL at jbrains.ca:
  Rainsberger restructured his site and some old permalink forms redirect; verify
  the current URL.
- Fowler "Test Coverage" post: not formally dated on martinfowler.com/bliki/;
  verify it is still present and the content is unchanged.
- Weinberg "test-of-truth" attribution: the concept appears in *The Psychology of
  Computer Programming* but the exact phrasing is not pinned to a specific chapter
  or page; treat as [verify] if quoting directly.

All Go commits referenced are present in the yoloAI repository as of 2026-05-21.
All internal D-entries are sourced from `docs/dev/working-notes.md` as of
2026-05-21.
