<!-- ABOUTME: Holding pen for deferred critiques parked from unresolved-critiques.md. -->
<!-- ABOUTME: Each item carries a revival trigger; when it fires the item flows back to unresolved. -->

# Deferred critiques

Critiques parked as "not now." Unlike [`resolved-critiques.md`](resolved-critiques.md)
(terminal history of applied critiques), every item here is still potentially actionable and
carries a **`Trigger:`** line — the condition that should pull it back into
[`unresolved-critiques.md`](unresolved-critiques.md). The trigger may be unlikely, but it must
exist so the item can be evaluated for eviction later. Newest first.

## IC14 (sub-point) — rename top-level `Info` → `SandboxInfo` — 2026-06-04

The top-level public output type `Info` (sandbox inspect result) reads ambiguously next to
`SystemInfo`; `SandboxInfo` would disambiguate. Deferred from the IC14 naming sweep because, unlike
the receiver/field renames applied in `8b9a44a`, this is a **61-site rename of a public type that
ships on `main`** — a real breaking change requiring a `BREAKING-CHANGES.md` entry. Folding it into a
LOW cleanup commit would smuggle a breaking change into a non-breaking sweep.

**Trigger:** the next intentional public-API breaking batch (do it there, with the BREAKING-CHANGES
entry, not as a standalone LOW item).
