ABOUTME: Cross-doc rule sheet for the principles — one imperative line per
ABOUTME: principle plus the symptom that should make you reach for it. The
ABOUTME: working-memory layer: load this whole file, match your current action
ABOUTME: against "Bites when", then open the cited section for the full why.

# Principles — rule index

The fast-lookup layer over the five principles docs. Each row is one principle distilled to a single imperative **Rule** plus a **Bites when** symptom — the thing you pattern-match your current action against. When a row matches, open the cited section for the rationale, worked examples, and cost-vs-benefit.

This file is small on purpose: load all of it at the start of a task. It carries the *rules*; the section bodies carry the *why*. The one-line Rule here is verbatim the spine that opens each section, so a grep for `## §N` in the cited doc lands you on the same words with the full treatment underneath.

**Cite format.** `DEV §N` = section N of `development-principles.md`; likewise `GEN` (general), `ARCH` (architecture), `TEST` (testing), `SEC` (security). To jump to a section, grep `## §N` in the named doc. The short form is the canonical cite; the long form (`development-principles.md §N`) remains valid wherever it already appears.

## development-principles.md

| Cite    | Rule (one line)                                                                                                                              | Bites when                                                                                  |
| ------- | ------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------- |
| DEV §1  | The shared engineering vocabulary (YAGNI/KISS/DRY/SOLID/Demeter/CQS). Default to the simplest correct solution; earn abstractions on the *second* use. | Adding an abstraction, generalisation, or knob for a single or hypothetical use case.       |
| DEV §2  | Policy owns what/why, mechanism owns how; neither questions the other. Only a typed refusal crosses back up — no library prompt/fallback/mode-switch, no CLI reach-through. | The library prompts/falls back/reinterprets intent, or the CLI imports the engine/runtime subtree instead of calling a `yoloai.*` verb. |
| DEV §3  | Every layer-boundary function re-validates its inputs against the domain invariants; the callee never trusts that the caller checked.        | Skipping a check because "the layer above already validated."                                |
| DEV §4  | Parse at the boundary into a domain type that *proves* the invariant; downstream takes the parsed type and never re-validates. Collapse constrained field-combos into typed enums. | Raw strings or bool-combos flow deep into the API, or one invariant is re-checked at many call sites. |
| DEV §5  | Surface errors at the earliest point — validate at the boundary, reject bad construction args in the constructor, return immediately; no partial state, no silent fallback. | Returning nil/zero on a bad state "to handle later," or adding a silent fallback default.    |
| DEV §6  | A lint/scanner/type warning is information; every suppression carries a co-located comment saying *why the finding doesn't apply here*.       | Adding a `//nolint` / `#nosec` or raising a complexity threshold to get the gate green.      |
| DEV §7  | Every return value carries information; don't discard one silently. Bare `_` on a non-trivial value (especially an error) needs a comment, outside the closed policy-justified categories. | Dropping an error with `_ =` or no check at all.                                             |
| DEV §8  | A feature is shipped (works + tests + docs) or removed — never half-landed. Partial code only when tracked in `design/plans/README.md` with an owner. | Leaving an untracked `// TODO: hook this up`, or landing a feature without tests/docs.       |
| DEV §9  | Architecture cleanup lands as a numbered, audited plan of single-responsibility commits — not opportunistic refactors inside feature work.   | Refactoring unrelated code inside a feature change.                                          |
| DEV §10 | `make check` (gofmt + lint + mod-tidy + Go tests + Python) must pass before any change is complete; never loosen the gate to land code.       | Calling a change "done" without a green `make check`, or weakening the gate to pass.         |
| DEV §11 | After three failed attempts at the same approach, stop and rethink the architecture rather than grinding another workaround; record the failure trail. | Reaching for "one more workaround" on a problem that has already resisted several.           |
| DEV §12 | Resolve ambient state (env, `$HOME`, cwd, terminal, identity) once at the outermost edge; library takes explicit args. An edge-resolved default implies a *pure accessor* — panic if read pre-edge, never a lazy fallback. | Calling `os.Getenv` / `os.UserHomeDir` / `os.Getwd` below the CLI, or adding a "just for tests" fallback default. |
| DEV §13 | Keep data in the shape it already has until a *present* consumer needs a different one; every conversion must serve a concrete consumer right where it happens. | Pre-emptively reshaping into a "rich" struct no current consumer needs, or a transform downstream just reverses. |

## general-principles.md

| Cite     | Rule (one line)                                                                                                       | Bites when                                                                       |
| -------- | --------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| GEN §1   | Decide under cost-vs-benefit; smallest intervention with real user benefit; defer hypothetical features (not licence for low-quality code). | Building presumptive long-tail features, or gold-plating past the visible benefit. |
| GEN §2   | ~3 innovation tokens — spend them on the differentiator; choose boring tech with known failure modes elsewhere.        | Reaching for a novel/heavy dependency for a non-differentiating need.            |
| GEN §3   | Check whether git/Docker/unix/the runtime already solve it; build the glue, not the primitives.                       | About to hand-build what git/docker/unix already does.                           |
| GEN §4   | Match process to reversibility: Type 2 ships fast at ~70%; Type 1 gets gates; beware Type 1.5 (CLI surface, config schema). | Heavy process on a reversible call, or shipping a CLI/schema change as if reversible. |
| GEN §5   | Every consequential op has an explicit upper bound (timeout/refusal/gate/pre-flight) + a clear failure message.       | Adding an op with no bound on damage/cost/time.                                  |
| GEN §6   | The safe setting is the default; the user types the dangerous one. Defaults are a safety-net, not a preference.        | Defaulting a flag to its convenient-but-dangerous setting.                       |
| GEN §7   | Verify claims against primary sources before citing; marketing/plausibility aren't evidence.                          | Stating a competitor/security/kernel fact you haven't checked.                   |
| GEN §8   | Record what yoloAI explicitly does NOT do, and why, in a D-entry — rejected alternatives matter.                       | A non-trivial decision with no recorded rejected alternatives.                   |
| GEN §9   | Surface the specific cause early (pre-flight refusal) over a catch-all mid-op failure. Diagnostic-first.               | A catch-all error that defers diagnosis to the user.                             |
| GEN §10  | Not cross-platform until verified per platform; document platform tradeoffs explicitly.                               | Asserting cross-platform behaviour verified on one platform.                     |
| GEN §11  | When in doubt, publish — docs, research, decisions, principles. Trivial cost, compounding trust.                      | Keeping a doc/decision private by default.                                       |
| GEN §12  | A design/spec is a provisional, falsifiable model — load-bearing only after implementation verifies it; facts win.    | Treating an unimplemented design as a contract; coding to the doc when reality diverges. |
| GEN §13  | Raise anything that looks off — even when agreed/spec'd/planned; agreement is a strong prior, not a gag order.         | Silently following an agreed plan you've spotted a problem in.                   |

## architecture-principles.md

| Cite     | Rule (one line)                                                                                                       | Bites when                                                                       |
| -------- | --------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| ARCH §1  | Library-first; CLI/mcpsrv are embedders. Everything goes through the public surface — a reach into internal/ means a missing verb; add the verb. | The CLI imports/reaches internal/ instead of calling a yoloai.* verb.            |
| ARCH §2  | yoloai↔internal is a published contract: no public type exposes an internal one (even via alias); embedders reach the engine only via the surface. Enforced (F1 + depguard). | A public type leaks an internal type, or widening a depguard allow-list instead of adding a verb. |
| ARCH §3  | *(Emerging, research-gated.)* The why behind DEV §12 (not a second read-ban): CLI is safe by accident (kernel ACL), a many-principal daemon isn't — host knowledge must bind at an embedder-controlled lifetime. | Designing a library API that reaches host ambient, or treating "thread it down" as ergonomics not security. |

## testing-principles.md

| Cite      | Rule (one line)                                                                                                      | Bites when                                                                       |
| --------- | -------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| TEST §1   | Coverage finds untested code; it's not a target. Ask "would this fail if production regressed noticeably?"            | Writing/justifying a test to hit a coverage number.                             |
| TEST §2   | Assert what the code does, not how; a test that breaks on a no-behaviour-change refactor tests implementation.        | A test asserts on internal mechanics / call sequences.                          |
| TEST §3   | Make error paths trigger-able and test them by default — incidents happen on the unhappy path.                       | Testing only the happy path because the error path is "hard to trigger."        |
| TEST §4   | A shipped bug gets its regression test alongside the fix.                                                            | Fixing a bug without the test that pins it.                                      |
| TEST §5   | Test each behaviour at the lowest layer that verifies it with confidence; higher layers can't isolate a cause.        | Reaching for e2e/integration for logic a unit test would pin.                    |
| TEST §6   | Integration tests hit real backends, never mocks — mocks miss cross-backend/version differences.                     | Mocking the daemon/backend in an integration test.                              |
| TEST §7   | A passing test is an artefact of healthy production code; on failure ask if the code is correct/necessary/better-doable. | Editing a test to pass without asking whether the production code is right.      |
| TEST §8   | Hand-rolled fakes over generated mocks — fakes test behaviour and survive refactor.                                  | Reaching for a mock-generator or call-sequence assertions.                       |
| TEST §9   | A test that surfaces a new failure mode beats coverage on an already-working path.                                   | Adding coverage on a working path instead of pinning the new failure mode.       |
| TEST §10  | Steer tests via explicit inputs (config.Layout, injected io.Reader/Writer), not global process state (t.Setenv("HOME"), os.Stdin). | A test mutates HOME/os.Stdin to steer a code path instead of passing inputs.     |

## security-principles.md

| Cite     | Rule (one line)                                                                                                       | Bites when                                                                       |
| -------- | --------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| SEC §1   | Enumerate in-scope AND out-of-scope threats; size defenses to scope, reject out-of-scope complexity.                  | Adding a defense without checking it's in the threat model.                      |
| SEC §2   | Containment, not prevention: the sandbox bounds what arbitrary agent code can reach, not whether it's safe.           | Designing a control to make agent code "safe" rather than limit its reach.       |
| SEC §3   | Stronger isolation is opt-in via explicit flags; standard Docker is the default.                                     | Making a stronger-isolation layer mandatory/default.                             |
| SEC §4   | Grant a capability only when a feature needs it; default mode pays zero.                                             | Granting a capability unconditionally instead of gating on the feature.          |
| SEC §5   | Agent output is untrusted data; defenses are structural, never detection-based.                                     | Relying on detecting/recognising malicious agent output.                         |
| SEC §6   | Inject secrets via file bind-mount at /run/secrets/<key>, not -e env; host temp file deleted after.                 | Passing a secret through an environment variable.                                |
| SEC §7   | Allow-known-good over block-known-bad; allowlists are enumerable and auditable.                                     | Building a blocklist for a security-relevant decision.                           |
| SEC §8   | A backend's isolation claim is a hypothesis until tested in yoloAI's real conditions.                               | Asserting an isolation property from docs/marketing without a real test.         |
| SEC §9   | Surface every defense's residual risk in docs, guide, and error messages.                                           | Shipping a defense without stating what it doesn't cover.                        |
| SEC §10  | Containment-undermining features are explicit and documented; no global "make me unsafe" flag.                      | Adding an isolation-weakening option silently or as a catch-all switch.          |
| SEC §11  | One convention per security mechanism codebase-wide; a divergent one-off is itself a vuln surface.                  | Adding a one-off guard that differs from its peers.                              |
