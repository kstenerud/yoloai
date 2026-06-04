<!-- ABOUTME: Terminal sink for abandoned critiques — decided "won't act on", drained from unresolved-critiques.md. -->
<!-- ABOUTME: Distinct from resolved- (applied) and deferred- (parked w/ trigger): these are permanently dropped. -->

# Abandoned critiques

Critiques permanently dropped — decided **"won't act on."** Distinct from
[`resolved-critiques.md`](resolved-critiques.md) (the critique was *applied*) and
[`deferred-critiques.md`](deferred-critiques.md) (parked with a revival trigger): items here
are terminal and not expected to come back. Each carries a short **`Why:`** line recording the
reason for abandonment. Newest first.

## IC16 — 2026-06-04 — two host-git wrappers are deliberately distinct, not a dedup target

Filed (spun off IC12) as "two near-identical host-git wrappers coexist — consider one chokepoint."
Digging deeper showed the resemblance is superficial. `workspace.NewGitCmd`/`RunGitCmd` trims output
and returns plain errors; it builds the create-time git baseline **host-side, before any container
exists**. `runtime.hostGitExec` is the **host arm of the `GitExecFor` dispatch seam**: it does NOT
trim (patches are whitespace-sensitive), returns a `*ExecError` carrying the exit code (so
`git diff --quiet` exit 1 reads as "diffs present"), and Tart overrides it via `GitExecer` to run git
**in-container**. The only real overlap is the one-line `core.hooksPath=/dev/null`.

**Why:** the proposed unification would be a regression, not a dedup. Routing `workspace` through
`GitExecFor` would push pre-container baseline git into a container that may not exist; routing
`hostGitExec` through `workspace` would drop the exit-code typing diff/apply depends on. Same
benign-duplication mis-diagnosis as IC12/IC15 in the same round.

## IC14 / IC15 (sub-points) — 2026-06-04 — YAGNI-arg-wrapping and the `NewEngine` invariant panic

Two sub-recommendations from the IC14/IC15 cleanup sweep, declined while the rest of each item was
applied (`8b9a44a`):
- **IC14 — wrap single-primitive args (`force bool`, `name string`) in `<Noun><Verb>Options` structs.**
  The convention is for multi-field params, not a struct per lone bool/string; the bare named args
  read fine.
- **IC15 — make `NewEngine` return `(*Engine, error)` instead of panicking on missing `WithLayout`.**
  The panic mirrors `config.NewLayout`'s panic on empty input — an internal constructor guarding a
  programmer invariant (`sandbox` is `internal/`; embedders go through `yoloai.Client`). Converting it
  would make it *inconsistent* with `NewLayout` and force 13 call sites to handle an error that can't
  occur in correct code.

**Why:** both are churn against a guarantee that already holds — YAGNI for the arg wrapping, and a
deliberate, consistent invariant-panic convention for `NewEngine`.

## A4 (original premise) — 2026-06-03 — public Go struct tags are not the live contract

A4 was filed as "the public output structs are not a serialization contract (split-brain JSON
tags)" — observing that some `yoloai.*` output structs carry `json` tags and others don't, and
proposing to tag them all uniformly. On re-examination the premise is wrong: the contract that
*actually ships and gets parsed* is the **CLI `--json` output** (apps shell out to the `yoloai`
binary), not the public Go struct tags. The Go struct tags only bind an **in-process daemon** that
calls `json.Marshal` directly — and that daemon is deferred (Layer 3). Auditing the live CLI
`--json` surface showed casing is already uniformly snake_case (including the two direct-marshal
sites, `sandbox info` → `*yoloai.Info` and `lifecycle new` → `*yoloai.Environment`, because those
contract structs were hand-mirrored with snake_case tags in F1/G1), and the direct-marshal sites
are the *healthy* pattern, not a risk.

**Why:** the finding mis-located the contract. Tagging every public struct is busywork for a
consumer that does not exist yet; when the daemon lands, the tagging question is reopened against a
real consumer. The one genuine residual — the CLI `--json` output has no *structural* convention
(list-envelope rule, error/empty shape) — is split out as a CLI-owned finding ([[DF17]]) rather
than carried as a public-API critique. Decision context: see the 2026-06-03 re-examination.

## G7 residue (extensions) — 2026-06-03 — won't add a public verb

G7 ("the public surface is missing verbs") was substantially resolved by the D55–D57 verb series;
the one deferred item was **extensions** — the `x` command reaches `internal/extension` with no
`yoloai.*` verb. Closed here as **won't act on**, and the package relocated to
`internal/cli/extension` (commit `390f83f`) so its CLI-private status is structural. Decision: D66.

**Why:** Extensions are a CLI macro system (user YAML wrapping `sh -c` scripts that invoke the
`yoloai` binary + host tools like `gh`/`jq`/`git`), not a library capability. There is nothing to
wrap and no daemon use case — loading arbitrary per-user scripts to exec would be a liability.
Adding a public verb would invent a library surface for a feature with no library/daemon consumer,
the opposite of what G7 was about. The honest fix is reclassification, not a verb.
