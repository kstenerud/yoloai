> **ABOUTME:** Architectural principles for yoloAI — the stance and shape of the module (what an
> embedder is allowed to see and call, and how host knowledge is allowed to enter it), as
> distinct from development-principles.md's code-practice mechanics. Cross-references rather
> than restates the development-principles.md sections it extends.

# Architecture principles

## Framing — yoloAI is a library; everything else embeds it

This doc captures the durable architectural *stance* — who the code is for, what the
contract is, and how host knowledge enters it. It is the **why** behind the mechanics
that `development-principles.md` enforces: §2 ("none of your business" — the policy /
mechanism layer contract) and §12 ("no ambient configuration"). Those sections own the
code-practice mechanics; this doc owns the shape they serve. Where they overlap, this
doc **cross-references** rather than restates.

Distilled out of the decision log (D55–D58) so future readers don't have to mine the
chronological D-entries to recover the architecture. The decision log remains the
justification of record; principles here cite it by D-number.

---

## §1. Library-first — the CLI is the honesty-keeper

> **Rule.** yoloAI is a library first; the CLI and mcpsrv are embedders, not insiders. Anything the CLI can do it must do through the public `yoloai` surface alone — a reach into `internal/` marks a missing public verb; the fix is to add the verb, never widen the reach. Litmus: could a separate-module daemon do this?
>
> **Bites when:** the CLI imports/reaches `internal/` for a capability instead of calling a `yoloai.*` verb. · **See also:** ARCH §2, DEV §2.

**Principle.** yoloAI is a Go *library* first. The engine runs in-process behind a public
`yoloai` surface; the CLI (`internal/cli/`) and the MCP server (`internal/mcpsrv/`) are
*embedders* of that surface, not privileged insiders. A future separate-module daemon
(REST / web / MCP-over-the-wire) is the design target: it cannot import `internal/`, so
**anything the CLI can do, it must be able to do through the public surface alone.**

The CLI's second job — beyond being the one-shot user tool — is to **keep the library
honest**. Because the in-tree CLI *can* technically reach `internal/`, every such reach
is a smell: it marks a capability the public surface is missing. The fix is never to
widen the CLI's reach; it is to add the missing public verb and route the CLI through it.
The litmus question for any new capability: *could a separate-module daemon do this?* If
no, the surface has a gap.

### Pattern

- The public surface is the root `yoloai` package: one top-level `Client` noun with
  `System()` / `Sandbox()` sub-handles (`Sandbox` in turn exposes `Workdir()` /
  `Network()` / `Agent()`), plus the public option/result types. Everything under
  `internal/` is sealed.
- **Supported surface vs. public-path building blocks.** The *supported consumer* API is
  the root `yoloai` package, and it alone is gated leak-free by the F1 fence
  (`internal_leak_fence_test.go`, empty `f1KnownLeaks`). The promoted substrate packages
  (`runtime/`, `store/`, `copyflow/`) live at public *paths* only because Go's `internal/`
  rule forces it for the root package to compose them — they are layering building blocks,
  **not** the supported API. So an exported `store`/`runtime` function taking
  `config.Layout` (an `internal/config` type) is *not* a leak to fix: promoting `Layout`
  would elevate the entire on-disk layout to a supported public contract, making future
  path changes breaking — the opposite of what the fence enforces. The fence scans the
  root package precisely because that is where the contract lives. (DF64.)
- A capability the CLI reaches `internal/` for becomes a public verb on the lower layer,
  not a retype of the CLI to wrap an internal value. (This is the D56 downward half of
  §2: *policy must not know the how*; a reach-through is converted to a verb.)
- The litmus is mechanically checked, so "the CLI keeps us honest" is enforced, not
  aspirational — see §2's teeth.

### Worked examples

- **The G7 verb series (D55).** Each CLI/mcpsrv reach into `internal/orchestrator/{store,…}`
  or `runtime` was replaced by a public verb: metadata/log/discovery/prompt
  reads route through `SystemClient` / `Client` / `Sandbox`; backend probing gained
  `SystemClient.CheckBackend` rather than reaching for `runtime.New`. After the series,
  cli+mcpsrv held **zero** non-test imports of those subtrees.
- **mcpsrv as the canary.** The MCP-server prototype is the in-tree stand-in for a real
  separate-module embedder; its tool set is the proof that the public surface is
  sufficient. When it reached `store` for ~5 of its tools, that was the
  signal the surface was incomplete — not licence to keep the reach.

### Cost-vs-benefit

Cost: every consumer capability must earn a named public verb before the CLI can use it;
no quick reach-in shortcuts. Damage prevented: a CLI that silently diverges from what an
embedder can do, so the daemon discovers missing capabilities only when it is built as a
separate module (the most expensive time to find them). Library-first front-loads that
discovery into every CLI change.

### Sources

Public-API direction (library-first; daemon embeds it; CLI is honesty-keeper). Project
decisions: D55 (F1 closed via the G7 verb series), D57 (the import fence that made the
litmus real). See also `development-principles.md §2`.

---

## §2. The public surface is the contract; the embedder is the audience

> **Rule.** The public surface (`yoloai`, `yoerrors`, `runtime`, `store`, `copyflow`) is a published contract for an embedder who can name only exports: no public type may expose a type from `internal/` (even via a type alias), and in-tree embedders reach sandbox/runtime behaviour only through the surface. Both enforced mechanically (F1 detector + depguard) so drift fails CI.
>
> **Bites when:** a public type leaks an internal type, or you'd widen a depguard allow-list instead of adding a verb. · **See also:** ARCH §1, DEV §2.

**Principle.** The boundary between the public surface (`yoloai` + `yoerrors` + the promoted substrate packages `runtime`/`store`/`copyflow`) and `internal/` is a
*published contract*, and its audience is an external embedder who can name **only** what
is exported. Two consequences follow: (1) a public type may not expose a type from `internal/` —
not even transitively, not even behind a type alias; (2) the in-tree embedders (cli,
mcpsrv) may reach sandbox/runtime behaviour *only* through that surface. Both are enforced
mechanically so drift fails CI, not review.

This is the *surface* view of the layer discipline `development-principles.md §2`
describes: §2 governs how policy and mechanism layers talk (comply-or-complain, upward /
downward ignorance); this section governs what an outside embedder can *see and name*.
The two share the same enforcement teeth.

### Pattern — the teeth

- **F1 leak detector** (`TestPublicAPI_NoInternalLeaks`, `public_api_test.go`). Walks the
  public surface and fails the build if any exported type exposes an internal type. It is
  **alias-descent aware**: `type X = internal.Y` no longer hides the subtree beneath `X`
  (the detector once was blind to aliases — a hole closed during Layer-1). The leak
  baseline `f1KnownLeaks` is the explicit, documented set of *sanctioned* exceptions;
  "empty and honestly so" is the completion bar, never "empty because the detector can't
  see it."
- **depguard fences** (`.golangci.yml`). The twin rules `cli-sandbox-scope` and
  `cli-runtime-scope` deny `internal/orchestrator`, `store`, `copyflow`,
  and `runtime` to non-test cli+mcpsrv code — the orchestrator subtree by prefix
  (façade *and* every leaf: `archetype`/`status`/…), plus the three public substrate packages
  `store`, `copyflow`, and `runtime` by explicit deny entries. There are no
  sanctioned exceptions: `internal/cli/system/tart` now reaches the backend only
  through the public `yoloai` surface.

When a fence or the detector goes red, the resolution is a public verb (§1), never a
widened allow-list.

### Worked examples

- **Honest completion over a green-but-blind test (D55).** The Layer-1 close required
  *first* teaching the detector to descend through aliases (it went red on a real leak it
  had been hiding), *then* carving the public read-model — truth before cleanup. A test
  that passes because it cannot see the leak is worse than one that fails.
- **G2 fence tightening (D57).** Dropping the three leaf allow-entries
  (`store`/`copyflow`/`archetype`) and denying `internal/orchestrator` by prefix made the fence
  mean what it claimed: a 3-import probe yields 3 denials; the CLI is now a faithful proxy
  for a separate-module daemon over those subtrees.

### Cost-vs-benefit

Cost: promoting an internal type to a hand-written public mirror + one-directional
converter (the `SystemOptions` / `config.Layout` precedent) rather than a cheap alias; the
detector occasionally forces this work before a feature can land. Damage prevented: an
embedder receiving a public value whose fields it cannot name (the `*store.Meta` iceberg);
a public surface that *looks* complete because the detector was structurally blind; CLI
behaviour that an external daemon cannot reproduce.

### Sources

`public_api_test.go` (F1 detector); `.golangci.yml` (`cli-sandbox-scope`,
`cli-runtime-scope`). Project decisions: D55 (detector honesty + carve), D57 (fence
tightening + rename). See also `development-principles.md §2`.

---

## §3. Host knowledge binds at a lifetime; the library never synthesizes it *(emerging — D58/D59, not yet enforced)*

> **Rule.** *(Emerging — D58/D59, research-gated.)* This section is the architectural *why* behind DEV §12, **not** a second copy of the read-ban (DEV §12 + forbidigo already enforce that). The why: the library never synthesizes host knowledge from process ambient because the CLI is safe only *by accident* (process identity == principal, so a forged `HOME` hits a kernel ACL), while a many-principal daemon loses that net — synthesis becomes a confused-deputy / cross-tenant leak. Host knowledge must enter as explicit input bound to an embedder-controlled lifetime.
>
> **Bites when:** designing a library API that reaches host ambient state, or treating "thread it down" as ergonomics rather than a security boundary. · **See also:** DEV §12 (the enforced read-ban), SEC §6.

**Principle (direction accepted 2026-06-01, D58 — not yet implemented).** Host knowledge
— filesystem paths, the home directory, environment values, the requesting principal's
UID/GID, credential locations — enters the library only as *explicitly-supplied input
bound to a lifetime the embedder controls*. The library acts only against the principal
scope it is handed; it **never synthesizes one from process ambient** — no
`os.UserHomeDir`, no `~`/`${VAR}` expansion of its own, no autonomous reach into
`~/.claude`. This is the architectural reason behind `development-principles.md §12` (no
ambient configuration); §12 bans the *reads*, this section frames *why* and adds the
lifetime axis.

Two ideas make it precise:

- **`~` is `$HOME`, and `${VAR}` is an environment reference** — both are *ambient
  host-environment references*, not portable path syntax. Resolving them is **boundary
  policy**, not library work. The library loads config **raw / un-expanded**; the boundary
  resolves references against an explicit environment (a nil environment ⇒ no resolution,
  which is the daemon-safe default). This is the §2 comply-or-complain split applied to
  configuration: mechanism returns the raw datum; policy decides what host context to
  bind.
- **Deployment scope vs principal scope.** Host knowledge fuses two independent
  lifetimes: a **deployment scope** (the data dir — operator-owned, configure-once) and a
  **principal scope** (home, env, UID/GID, credential seeding — the *requesting* party's,
  bound for that party's lifetime). The CLI collapses both into one process; a daemon
  must keep them separate — one deployment, many principals.

### Why the lifetime axis is a *security* boundary, not just ergonomics

The CLI's safety today is **kernel-ACL-derived**: the process identity *is* the requesting
principal, so an injected `HOME=/root` for a non-root user simply yields `EACCES` on
`open()` — expansion cannot grant access the process lacked. A **many-principal** process
(web session, daemon request) loses that safety net: the process owner is not the
principal, so resolving an ambient reference on the principal's behalf is a
confused-deputy / cross-tenant credential-leak risk. The principal-multiplicity axis
(single vs many), **not** the process-lifetime axis (short vs long), is what removes the
kernel safety net; lifetime adds only staleness (expiry / revocation). A library that
synthesizes host knowledge is safe in the CLI by accident and unsafe in a daemon by
construction — so the library must never do it.

### A second axis — isolation (D59)

Binding is only half of multi-principal. The orthogonal half is **isolation**: can one
principal reach another's resources? D59 settles three directions (still pre-implementation,
gated on security research): sandbox storage gets a **physical partition**
(`sandboxes/<principal>/<name>/`, fails closed) rather than a flat namespace with per-verb
ACL checks (fails open); **`HomeDir` dissolves** into a typed credential/preferences bundle
the caller supplies (retiring the last ambient-shaped input); and the principal scope is an
**embedder-lifetime handle**. The deployment/principal seam was found to run *through*
`DataDir` itself — `sandboxes/` is per-principal storage rooted in an otherwise
deployment-shared tree.

### Status & what's still open

This is **direction, not yet enforced.** No depguard/forbidigo rule expresses it beyond
§12's existing ambient-read bans; the principal-scope handle and the partition scheme are
undesigned. The settled directions above (D58/D59) are gated on a **dedicated
security-research pass** — path confinement, principal-id unforgeability, enumeration
leaks, credential-bundle shape — before any plan doc, per the project rule that security is
never finalized ad-hoc. Treat this section as the frame to build against, not settled law.

### Sources

Project decisions D58 (binding-lifetime frame: deployment vs principal scope; library never
resolves ambient references; raw-load / boundary-resolve split) and D59 (the orthogonal
isolation axis; physical partition; HomeDir dissolution; embedder-lifetime handle). Extends
`development-principles.md §12`. Security details deferred to dedicated research.

---

## §4. Substrate verbs accept requests and encapsulate mechanism — request-in, no mechanism-out

> **Rule.** A substrate (backend) verb takes a *request* (`ProcSpec`, an exec/attach request) and owns *how* it fulfils it; it must not vend an *ingredient* of the mechanism for the caller to apply (a launch-prefix string, a "prepared" command, an attach argv). Fact-*queries* — capability values the caller reads to make its *own* decision — are fine; ingredient-*vendors* are not: fold them into the verb. The verb names the WHAT; backend-private HOW never reaches the surface.
>
> **Bites when:** a backend exposes a getter the caller concatenates/execs (`prefix + cmd`, run-the-returned-argv), or a path like restart rebuilds a command from a serialized mechanism fragment instead of routing through the launch verb. · **See also:** ARCH §1/§2, DEV §2 (mechanism owns how), D89 (mechanism-vs-payload).

**Principle.** The substrate (`runtime.Backend` and its optional interfaces) owns HOW an
environment runs a process; the caller owns WHAT runs in it — `development-principles.md §2`
and D89's mechanism-vs-payload, one layer down. So a substrate verb must be **request-in,
mechanism-stays-in**: the caller hands a request — a `ProcSpec` to `Launch`, an exec/attach
request to `InteractiveExec` — and the backend applies whatever PATH wrap, shell sourcing, or
env prefix *its* environment needs, internally. It must not hand the caller a *piece* of that
mechanism (a launch-prefix to prepend, a "prepared" command to run, an attach argv to exec)
and trust the caller to apply it. That ingredient-vendor shape leaks HOW across the boundary
and couples every caller to the backend's internals.

The line is **fact-query vs ingredient-vendor**:

- A **fact-query** returns a capability the caller reads to branch its *own* logic
  (`AgentProvisionedByBackend` → envsetup decides whether to seed; the `BackendCaps` booleans;
  `Probe`). Legitimate — the caller isn't applying the backend's mechanism.
- An **ingredient-vendor** returns a fragment of the backend's mechanism for the caller to
  assemble (`AgentLaunchPrefix` → `prefix + cmd`; `PrepareAgentCommand` → run the returned
  string; `InteractiveSession.AttachCommand` → exec the returned argv). Illegitimate — fold it
  into the request-accepting verb so the fragment never crosses the surface.

The payoff is a **smaller, stabler public surface**: request-in / no-mechanism-out means the
public contract is a handful of verbs plus honest capability facts — no mechanism fragments to
freeze into semver and later regret.

### The tell — a caller reconstructing mechanism without the backend

The reliable smell is a caller *rebuilding* what a verb should have done. `AgentLaunchPrefix` is
serialized into `runtime-config.json` and re-applied at restart
(`orchestrator/lifecycle/restart.go`) *specifically* so the command can be recomputed "without
constructing a Runtime." That convenience is the leak: restart reaches around the launch verb
and re-implements its prefixing. The fix is not to expose the fragment more cleanly — it is to
route restart *through* the backend's launch verb (the S0–S3 carve's own thesis: everything runs
via `Launch`), after which the prefix is a backend-private detail that never needs serializing or
exporting.

### Worked example — the substrate surface cleanup (D97, Stage 3b)

The pre-Move audit found three ingredient-vendors mis-named as "agent" surface in
`runtime`: `AgentLaunchPrefix`, `AgentCommandPreparer`/`PrepareAgentCommand`, and
`InteractiveSession.AttachCommand`. Under this principle they are not renamed-and-kept; they
**dissolve into `Launch`/`InteractiveExec`** and leave the public surface. `AgentProvisionedByBackend`
is a fact-query and stays a descriptor capability. `AgentInstallMethod` is neither — it is agent
(Claude) config payload — so it re-homes to the agent layer. The load-bearing change is
restart-through-`Launch` (above), verified on real Docker; the folds-into-the-verb are otherwise
behavior-preserving.

### Cost-vs-benefit

Cost: the backend must accept a richer request and do the assembly internally (e.g. `Launch`
applies the env prefix), and a caller that took a shortcut — rebuilding a command from a stored
fragment — must be rerouted through the verb, sometimes a real behavioral change. Damage
prevented: a public substrate that freezes mechanism fragments into semver; callers silently
coupled to a backend's PATH/shell internals; restart and create drifting because each
re-implements the launch wrap. Request-in / no-mechanism-out keeps the WHAT/HOW seam where the
layer boundary already is.

### Sources

Project decision D97 (pre-Move audit; the substrate surface cleanup that recast the "agent-leaky"
runtime fields along the WHAT/HOW line). Extends `development-principles.md §2` (mechanism owns
how) and D89 (mechanism-vs-payload) to the substrate verb surface. Request types:
`runtime.ProcSpec` / `ProcessLauncher.Launch` (`substrate-interface.md`, D84).

---

# Common over-generalisations to avoid

| Over-generalisation | Why yoloAI rejects |
| --- | --- |
| **The-CLI-is-special** | §1 — the CLI is *not* a privileged insider; it is an embedder that happens to live in-tree. Its ability to reach `internal/` is a temptation to resist, not a licence to use. The honest reach is zero. |
| **An-alias-is-good-enough** | §2 — `type X = internal.Y` publishes mechanism and (until the detector learned to descend) hid the leak. A public mirror + one-directional converter is the cost of a real contract; the alias is a false economy. |
| **Empty-baseline-means-done** | §2 — `f1KnownLeaks` empty proves completion *only* if the detector can actually see what it's checking. A green-but-blind test is worse than a red one. |
| **Allow-list-the-one-reach** | §1/§2 — when a fence goes red for a legitimate need, the fix is a public verb, not a per-leaf allow-entry. Allow-lists re-introduce exactly the blindness G2 removed. |
| **Resolve-`${VAR}`/`~`-in-the-library** | §3 — both are ambient host-environment references. The library loads raw; the boundary resolves against an explicit (possibly nil) environment. Doing it in the library is safe in the CLI by accident, unsafe in a daemon by construction. |
| **Long-lived-is-the-risk** | §3 — process *lifetime* (short vs long) adds only staleness. It's the *principal-multiplicity* axis (single vs many) that removes the kernel safety net. A long-lived single-principal MCP server is far safer than a short-lived many-principal request handler. |
| **Rename-the-leak-and-keep-it** | §4 — an ingredient-vendor (`AgentLaunchPrefix`, a "prepared" command, an attach argv) doesn't become clean by dropping the agent-bias from its name. If the caller *applies* the fragment, fold it into the request-accepting verb; only *fact-queries* stay on the surface. A serialized fragment a caller recomputes "without a Runtime" is the tell, not the fix. |

---

## Closing note

These architecture principles parallel the general / development / testing / security docs
in shape: framing, threshold per principle, worked examples grounded in D-entries, and the
over-generalisations to avoid. The specialised relationship:

- This doc is the **stance and shape**; `development-principles.md §2` and `§12` are the
  **code-practice mechanics** that enforce it. §1 here is the *why* behind §2's downward
  half; §3 here is the *why* behind §12.
- `general-principles.md` supplies the strategic frame (reversibility, blast-radius,
  default-to-public) that library-first (§1) is a concrete expression of.
- `security-principles.md` is where §3's still-open work lands once the dedicated
  security research is done — the binding-lifetime frame becomes containment policy.
- The decision log (`../decisions/`, D55–D58) remains the chronological justification; this
  doc is the distilled, stable landing place that points back into it by D-number.
