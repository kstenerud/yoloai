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
| threaded `*slog.Logger` | 4 sites | **the target** |
| `slog.Default()` | 8 sites | **replace** — ambient, the thing §12 forbids |

The last row is why the decision says *threaded* logger and not just "slog": eight sites reach for
the global today, and consolidating without that word moves the ambient-config defect into the
feedback path rather than removing it.

## The open question this plan must answer first

**Is a record a stream event, a return value, or both?**

`StartResult.Notices` exists because a library caller wants notices *attached to the result* —
inspectable, testable, ordered with the call — not scraped from a handler they had to install. A
logger is fire-and-forget; a return value is not. The likely answer is **emit once, fan out to
both**: a handler for streaming and a collector that populates the result. `noticeWriter` is already
a half-built version of exactly that, and `Notice`/`NoticeLevel` are already public types
(`types.go:187,191`).

Settle this before converting any call site — it decides whether the emission API is
`log.InfoContext(ctx, …)` or something that returns.

## Order of work

1. **Answer the question above**, and write the emission API. One helper, one shape, so a converted
   site cannot invent a variant.
2. **Stand up the collector + handler pair** behind the existing `Output` fields, so nothing changes
   for callers yet and the old and new paths produce identical bytes. This is the step that lets the
   rest be mechanical.
3. **Convert the 26 orchestrator call sites**, then the rest. Each becomes a record with fields —
   the transparency bullets, `filterAvailablePorts`' dropped-port warnings, the archetype output,
   and D144's credential disclosure (`launch.go`'s `discloseInjectedCredentials`, instance 27, which
   converts **with** the class and not before it — a lone divergent site is `security-principles.md`
   §11's hygiene defect).
4. **Replace the 8 `slog.Default()` sites** with the threaded logger.
5. **Retire the public `Output` fields**, or redefine them as "install this handler" — a public API
   change either way, with its `BREAKING-CHANGES.md` entry.
6. **Gate the bypass**: a `forbidigo` rule for `fmt.Fprint*` to a threaded writer outside the routing
   layer. Without it nothing stops mechanism five, and the ban stays decoration.

## Tests (rule 10)

- Every converted site: a test asserting on the **record** (event name, level, fields) rather than a
  substring. This is the bulk of the diff and mostly an improvement — a substring assertion breaks on
  rewording, a field assertion does not.
- The fan-out: one emission reaches both the handler and the result collector, once each.
- Byte-compatibility during step 2: the CLI's rendered output is unchanged while both paths exist.
  That is the safety net making steps 3–4 mechanical.
- A claim test for the invariant, cited per rule 13: **library code emits records, never formatted
  text.** The `forbidigo` rule from step 6 bans the call; the claim pins the behaviour.

## Surfaces to sweep (rule 2)

- **Code:** `internal/orchestrator/{create,launch,lifecycle}/`, `internal/envsetup/`,
  `internal/cli/cliutil/` (the routing point, `RenderNotices`), `sandbox_options.go`,
  `client_options.go`, `types.go`.
- **Docs:** `docs/integrators/` (an embedder sets `Output` today), `architecture/code-map.md`
  (gated), `principles/development-principles.md` §12 (the ambient rule this completes).
