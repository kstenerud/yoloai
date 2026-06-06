<!-- ABOUTME: Holding pen for deferred critiques parked from critiques-unresolved.md. -->
<!-- ABOUTME: Each item carries a revival trigger; when it fires the item flows back to unresolved. -->

# Deferred critiques

Critiques parked as "not now." Unlike [`critiques-resolved.md`](critiques-resolved.md)
(terminal history of applied critiques), every item here is still potentially actionable and
carries a **`Trigger:`** line — the condition that should pull it back into
[`critiques-unresolved.md`](critiques-unresolved.md). The trigger may be unlikely, but it must
exist so the item can be evaluated for eviction later. Newest first.

## 2026-06-04 Testing-critique round — T7

- **T7 — Zero `t.Parallel()` across the test suite.** testing-principles §10/§12 (injected seams, no
  ambient process state) specifically *enable* parallelism, yet nothing uses it. The pure-logic unit
  tier — store round-trips, patch/diff git plumbing, name parsing, config routing, the `yoerrors`
  mapping table — is embarrassingly parallel and would shave wall-clock off every `go test` and
  `make check`. *Fix:* adopt `t.Parallel()` on the pure-logic unit tier (skip tests that mutate
  process-global state like `t.Setenv`, or that share a daemon); run the suite under `-race` while
  doing so to also exercise the D67 `ensureRuntime` once-guard under concurrency. **Parked
  2026-06-04:** only affects the Go unit tier, already the cheapest part of the suite — the
  disproportionate test cost is the Python smoke harness, which `t.Parallel()` cannot touch.
  Pursuing smoke-harness perf first.
- **Trigger:** revisit when (a) the Go unit tier's wall-clock becomes a felt bottleneck in
  `make check`, or (b) a concurrency-sensitive change (e.g. reworking the D67 `ensureRuntime`
  once-guard) wants a `-race` parallel suite as a regression net.
