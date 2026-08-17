> **ABOUTME:** Replace the threaded `io.Writer` feedback channel with structured records emitted
> through a threaded `*slog.Logger`, so the caller — CLI, MCP server, embedder, daemon — decides
> where output goes and what it looks like, instead of the library deciding it is text for a human.

# Feedback routing — one record, four consumers, the caller renders

- **Status:** PLANNED — designed, no code.
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
4. **Make every entrypoint declare its handler** — CLI, MCP server, test mains — and make the
   declaration checkable, so an unconfigured default is a failure rather than a silent fallback to
   whatever the runtime chose. Replace the `slog.Default()` accessor sites; leave operator-addressed
   package-level calls alone once the destination is declared.
5. **Generalise the `forbidigo` ban from a list to a class**: any API whose behaviour depends on
   ambient process state rather than its arguments. The list is already incomplete —
   **`filepath.Abs` (4 uses) silently calls the banned `os.Getwd()`**, and `os.TempDir` (2 uses)
   reads `TMPDIR`. `exec.LookPath` (28 uses) is likely a legitimate exception and should be declared
   as one rather than left as an oversight.
6. **Retire the public `Output` fields**, or redefine them as "install this handler" — a public API
   change either way, with its `BREAKING-CHANGES.md` entry.
7. **Gate the bypass**: a `forbidigo` rule for `fmt.Fprint*` to a threaded writer outside the routing
   layer. Without it nothing stops mechanism five, and the ban stays decoration.

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
