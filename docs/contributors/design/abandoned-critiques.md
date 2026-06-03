<!-- ABOUTME: Terminal sink for abandoned critiques — decided "won't act on", drained from unresolved-critiques.md. -->
<!-- ABOUTME: Distinct from resolved- (applied) and deferred- (parked w/ trigger): these are permanently dropped. -->

# Abandoned critiques

Critiques permanently dropped — decided **"won't act on."** Distinct from
[`resolved-critiques.md`](resolved-critiques.md) (the critique was *applied*) and
[`deferred-critiques.md`](deferred-critiques.md) (parked with a revival trigger): items here
are terminal and not expected to come back. Each carries a short **`Why:`** line recording the
reason for abandonment. Newest first.

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
