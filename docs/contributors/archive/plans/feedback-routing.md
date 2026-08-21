> **ARCHIVED — not maintained, not swept, not a live reference.** Everything below was
> true when written and has not been checked since; the code it describes has moved. It is
> **not a specification** — do not build from it or cite it as the current answer. Good for
> archaeology only: see [`../README.md`](../README.md).

> **ABOUTME:** Replace the threaded `io.Writer` feedback channel with structured records emitted
> through a threaded `*slog.Logger`, so the caller — CLI, MCP server, embedder, daemon — decides
> where output goes and what it looks like, instead of the library deciding it is text for a human.

# Feedback routing — one record, four consumers, the caller renders

- **Status:** IMPLEMENTED. All seven steps built. The `feedback` package (`Notice`, `Progress`,
  their sinks, `Collector`, `Tee`, the writer adapters); every advisory and every progress line
  converted; the prune trio returning typed items; every diagnostic destination declared and
  `forbidigo`-enforced; the ambient-API ban generalised to a class; every public `io.Writer`
  deleted; and the bypass gated by `TestArch_LibraryTakesNoFeedbackWriter`. Verified on real Docker
  end-to-end: create, restart, destroy, a forced base rebuild streaming BuildKit through
  `ProgressWriter`, and prune across three backends including `--json`.
- **Depends on:** —
- **Rides:** **breaking.** `SandboxCreateOptions.Output` and `ClientOptions.Output` are public
  `io.Writer` fields (`sandbox_options.go:140`, `client_options.go:60`); an embedder sets them today.
  See [D145](../../decisions/working-notes.md).

## Why

`forbidigo` already bans `fmt.Print*`, `println` and `log.Print*` repo-wide — the *"no printing
outside the routing layer"* rule is real. The bypass it leaves open is `fmt.Fprintf(w, …)` to a
threaded writer, and that is the dominant pattern: **19 writer parameters in `internal/`, 26
`fmt.Fprint*` calls in `internal/orchestrator/` alone.**

A threaded writer looks disciplined — an explicit parameter, not a global — while hardcoding the one
assumption a non-CLI consumer cannot satisfy: that a human is watching a text stream, in order, now.
By the time bytes reach it, the level, the event identity and the fields a router would need are
gone.

## Four mechanisms today

| Mechanism | Scale | Disposition |
| --- | --- | --- |
| threaded `io.Writer` + `fmt.Fprint*` | 19 params / 26 calls | **replace** |
| `noticeWriter` → `StartResult.Notices` | start/reset | **generalise** — closest to right |
| threaded logger calls | 114 | **the target** |
| process-global logger | **~111** — 8 `slog.Default()` + 103 package-level `slog.Info/Warn/Error/Debug(` | **triage, do not mass-convert** |

**The last row is not 8, as an earlier draft said.** Package-level `slog.Info(…)` writes to the same
process-global handler; there are 103. But most are **diagnostics**, and diagnostics are allowed to
use a singleton — see below. Only the feedback ones convert.

## Feedback vs diagnostics — the line that sizes this work

**Who is addressed?**

- **The caller of this API** → feedback. Per-call, per-principal; must be threaded or returned. A
  process-global sink cannot say "this belongs to that caller", and a multi-principal daemon merges
  principals into one stream.
- **The operator of the process** → diagnostics. Process-scoped by nature, so a **singleton beats
  DI**: threading a logger through every function to serve a process-wide concern is ceremony.

**The rule is "no undeclared destination", not "no globals".** A singleton whose handler is
explicitly installed at process start — by the CLI, or by a daemon before it launches its web
service — is a *declared* destination. What is forbidden is *implicit defaulting*: reaching for the
global when nothing has set it, so output lands wherever the runtime decides.

**So the conversion is the 19 writers / 26 `Fprint` calls plus whichever of the 103 turn out to be
caller-addressed — not all 111.** Triaging that set is step 1's real work, and the sizing cannot be
trusted until it is done.

## Settled shape (D145 amendment, 2026-08-17)

- **No universal record type.** Results are per-API and typed; the common thing is the *advisory*,
  which is the already-public `Notice`. Give it a chosen level and fields, and make it emittable.
- **Sinks, not channels.** A channel imposes drain/close lifecycle on the caller — an unbuffered one
  that nobody reads deadlocks mid-call. `OnNotice = func(n) { ch <- n }` is one line; the reverse is
  a goroutine and a close protocol forced on everyone.
- **Event ID is internal and semantic** — what happened, not who emitted it. It is what makes the
  renderer a lookup rather than a message-text match. Dotted, self-describing, readable without the
  code. The five CLI/orchestrator collisions resolve by **dropping the CLI's duplicates**.
- **No `Error` notices** — errors are returned.
- **Progress is a third category**: only meaningful live, not accumulated into a result. ~20 in
  `runtime/tart` alone. Stays on the stream.
- **Writers stay where they carry a stream we do not own** — `Setup`, `BuildProfileImage`,
  `StdioExec`. They go from `Prune`/`PruneCache`/`PruneStaleBases`, whose lines are a *report* and
  become richer typed results. **Three interface methods change, not eight.**
- **Counts, corrected:** 108 advisory writes in library code, 79 of them in `runtime/`; ~18 of 103
  package-level slog calls are caller-addressed.

## Exposure is per-API

The same record is legitimately a **return value** on `Create` — the caller has a result and wants
notices attached — and a **stream event** on a long-running `Start` or `Attach`, where there is
nothing to attach to yet. So: **emission is uniform (one record, one helper); exposure is declared by
each API surface as part of its contract.** `Notice`/`NoticeLevel` are already public
(`types.go:187,191`) and `noticeWriter` is a half-built collector to build on.

This is deliberately *not* one global answer — choosing one would fit `Create` and fight `Attach`.

## Order of work

1. **Triage the 103 package-level calls** into caller-addressed (convert) and operator-addressed
   (leave, but ensure the destination is declared), and write the emission API — one helper, one
   shape, so a converted site cannot invent a variant. **The sizing of this plan is not known until
   this step is done**; an earlier draft's "8 sites" was wrong by an order of magnitude.
2. **Stand up the collector + handler pair** behind the existing `Output` fields, so nothing changes
   for callers yet and the old and new paths produce identical bytes. This is the step that lets the
   rest be mechanical.
3. **Convert the 26 orchestrator call sites**, then the rest. Each becomes a record with fields —
   the transparency bullets, `filterAvailablePorts`' dropped-port warnings, the archetype output,
   and D144's credential disclosure (`launch.go`'s `discloseInjectedCredentials`, instance 27, which
   converts **with** the class and not before it — a lone divergent site is `security-principles.md`
   §11's hygiene defect).
4. ~~**Make every entrypoint declare its handler**~~ — **done.** `forbidigo` now bans `slog.Default`
   outside `internal/cli/` and `cmd/`, which is where the handler is installed
   (`cliutil.InitLogger` calls `slog.SetDefault`), so reading it back there is propagating a
   declared destination rather than reaching for one nobody set. The library's own default is
   **silence**: `ClientCreateOptions.Logger` unset yields `slog.New(slog.DiscardHandler)`, not
   `slog.Default()` — a break, recorded in BREAKING-CHANGES. `state.Deps` gained a `Logger` so the
   four sites that fabricated `slog.Default()` one frame before calling a function that takes one
   now pass what the caller declared; `System` holds the Client's logger for the same reason. Two
   sites keep the global with an inline nolint: `runtime/docker`'s inspect-flap warnings are
   operator-addressed and sit on a path (`IsReady` → `imageExists`) with no logger to thread.
5. ~~**Generalise the `forbidigo` ban from a list to a class**~~ — **done.** `filepath.Abs` and
   `os.TempDir` are banned; `internal/cli/` is excluded for `filepath.Abs`, because its working
   directory *is* the caller's and that is the one place it is. `exec.LookPath` is **declared as a
   legitimate exception in the config rather than banned**: it answers "is this backend's binary on
   this host", a question about the host with no non-ambient form. Three sites carry a justified
   inline nolint, and one of them turned out to be a real defect rather than an exception —
   `ImportFile` resolves a caller's relative path against the *process's* cwd, which is right only
   because a single-principal CLI makes two values coincide (**DF222**, parked: the fix is a public
   break).
6. **Convert progress to records and delete every public `io.Writer`** (2026-08-19 amendment). Not
   "retire or redefine": they go. This is the step that makes 7 expressible.
7. ~~**Gate the bypass**~~ — **done, but not as a `forbidigo` rule.** The original wording asked for
   a rule targeting *"`fmt.Fprint*` to a threaded writer"*, which forbidigo cannot express: it
   matches call names, not argument types. An outright ban on `fmt.Fprint*` would express it at the
   cost of rewriting ~60 legitimate `strings.Builder` sites and outlawing idiomatic Go.
   `TestArch_LibraryTakesNoFeedbackWriter` states the actual invariant instead — no library function
   takes an `io.Writer` for feedback — with a declared allowlist whose entries must each still match
   a real declaration.

## Progress is a record too (2026-08-19 amendment)

The 2026-08-17 shape kept `io.Writer` on `Setup` and `BuildProfileImage` "because they carry a
stream we do not own". **That was wrong on the facts**, and the [D145 amendment of
2026-08-19](../../decisions/working-notes.md) records why: of the 32 `fmt.Fprint*` calls to a
threaded writer still in library code, **zero carry foreign bytes**. Every one is a sentence we
compose. The foreign seam is `cmd.Stdout = w`, a different mechanism that was never a `fmt.Fprint*`
call — the two were conflated.

The three advisories this section used to list as an unresolvable residual — apple's
unsupported-build-secrets warning, containerd's namespace-link fallback, tart's failure dump — are
not residual at all once progress converts. They stop being "advisories riding a stream we cannot
change" and become ordinary records on a sink the function already has.

**What changes:**

- A **`Progress` record** — `Event`, `Message`, `Fields`, and no level — plus a `ProgressSink`.
  Distinct from `Notice` because progress has no severity and because consumers route them
  differently: a notice attaches to a result, progress only ever streams.
- **Every public `io.Writer` field goes**: `SandboxCreateOptions.Output`, `ClientOptions.Output`,
  `SystemPruneOptions.Output`, and the writer parameters on `Setup` / `BuildProfileImage` /
  `EnsureProfileImage`. Backwards compatibility is explicitly not a constraint (owner, 2026-08-19).
- The **subprocess seam** adapts a sink to a writer inside the backend — the mirror of `WriterSink`
  — splitting the child's output into progress records carrying the raw line. Cost, stated: three
  sites pass the writer straight to the child (`tart/build.go:502-503`, `containerd/image.go:227-228`),
  where a TTY could today allow an in-place render that line-splitting flattens. Everything
  substantive already goes through an `io.MultiWriter`, so the child already sees a pipe.
- **`StdioExec` keeps its streams** and is not part of this. It is a live bidirectional terminal,
  not progress — the one category that really is a stream.

## Tests (rule 10)

- Every converted site: a test asserting on the **record** (event name, level, fields) rather than a
  substring. This is the bulk of the diff and mostly an improvement — a substring assertion breaks on
  rewording, a field assertion does not.
- The fan-out: one emission reaches both the handler and the result collector, once each.
- Byte-compatibility during step 2: the CLI's rendered output is unchanged while both paths exist.
  That is the safety net making steps 3–4 mechanical.
- A claim test for the invariant, cited per rule 13: **library code emits records, never formatted
  text.** The `forbidigo` rule from step 7 bans the call; the claim pins the behaviour.

## Surfaces to sweep (rule 2)

- **Code:** `internal/orchestrator/{create,launch,lifecycle}/`, `internal/envsetup/`,
  `internal/cli/cliutil/` (the routing point, `RenderNotices`), `sandbox_options.go`,
  `client_options.go`, `types.go`.
- **Docs:** `docs/integrators/` (an embedder sets `Output` today), `architecture/code-map.md`
  (gated), `principles/development-principles.md` §12 (the ambient rule this completes).
