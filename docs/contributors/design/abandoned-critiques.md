<!-- ABOUTME: Terminal sink for abandoned critiques — decided "won't act on", drained from unresolved-critiques.md. -->
<!-- ABOUTME: Distinct from resolved- (applied) and deferred- (parked w/ trigger): these are permanently dropped. -->

# Abandoned critiques

Critiques permanently dropped — decided **"won't act on."** Distinct from
[`resolved-critiques.md`](resolved-critiques.md) (the critique was *applied*) and
[`deferred-critiques.md`](deferred-critiques.md) (parked with a revival trigger): items here
are terminal and not expected to come back. Each carries a short **`Why:`** line recording the
reason for abandonment. Newest first.

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
