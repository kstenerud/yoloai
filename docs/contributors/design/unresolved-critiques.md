<!-- ABOUTME: Active queue of open design critiques for yoloAI. Resolved items drain to -->
<!-- ABOUTME: resolved-critiques.md; deferred to deferred-critiques.md; abandoned to abandoned-critiques.md. -->

# Open critiques

Active design critiques awaiting action. Each is drained to one of three co-located sinks once
settled: [`resolved-critiques.md`](resolved-critiques.md) (applied),
[`deferred-critiques.md`](deferred-critiques.md) (parked with a `Trigger:`), or
[`abandoned-critiques.md`](abandoned-critiques.md) (dropped with a `Why:`). Keep only live items
here — resolved entries belong in the sink, not as stubs.

> The **2026-05-30 Post-F1-Close round** is fully drained: G1/G2/G3/G4/G5/G6/G8 →
> [`resolved-critiques.md`](resolved-critiques.md); G7 (extension residue) →
> [`abandoned-critiques.md`](abandoned-critiques.md) (D66); the D53 read-model reshape closed with
> commit `2916e24`; carried-forward findings F6/F7/F9 done 2026-06-01.

> The **2026-06-03 Public-API "right reasons" round (A1–A4)** is fully drained: A1 (mirror-vs-alias)
> and A2/A3 (surface split on backend-liveness; `SystemClient` junk drawer; homeless agent noun) →
> [`resolved-critiques.md`](resolved-critiques.md) (A2/A3 implemented C1–C4, D67); A4 (public
> struct-tags premise) → [`abandoned-critiques.md`](abandoned-critiques.md), with the genuine CLI
> `--json` residual tracked as [`unresolved-findings.md`](unresolved-findings.md) DF17.

> The **2026-06-03 Internal-code round (IC1–IC16)** is fully drained (2026-06-04, one commit per
> item on `layering-refactor`): IC1–IC15 (incl. IC14's `Info`→`SandboxInfo` rename, applied once the
> branch was confirmed to be a cumulative breaking delta vs `main` already) →
> [`resolved-critiques.md`](resolved-critiques.md); IC16 (host-git wrappers) plus the IC14/IC15
> won't-do sub-points → [`abandoned-critiques.md`](abandoned-critiques.md). Recurring lesson:
> IC12/IC15/IC16 were all "looks dead/dup" findings that proved load-bearing on a closer read.

> The **2026-06-04 Post-D67/D68 public-surface residue round (R1–R6)** is fully drained to
> [`resolved-critiques.md`](resolved-critiques.md): R1 (stranded `Clone` godoc), R2 (stale pre-D68
> field names in doc comments), R3 (redundant `BackendType` identity casts), R4 (`Files()` missing
> from the `Sandbox` sub-handle list), R5 (`Agent().AgentLog()`→`TerminalLog()` de-stutter) all
> applied on `layering-refactor`; R6 (`.Type` vs `.BackendType` field-name convention) documented as a
> clarification under D68. `make check` green.

---

_No active critiques._
