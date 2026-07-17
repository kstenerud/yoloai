> **ABOUTME:** The design doc ("Frame") that laid out yoloAI's public-layering strategy —
> staging composable public packages behind `internal/`, auditing each layer, and promoting by
> mechanical move once its contract stabilizes. Records the audit methodology and the phased
> endgame.

# Public layering: stage the composable API behind `internal/`, promote by move

- **Status:** IN-PROGRESS — active on the `public-layering` branch (cut from `main` after the
  module-split renames landed); the runtime/store/copyflow move it designed has landed, but the
  branch does not merge to `main` until per-agent custom detection strategies are wired (merge
  gate below), and Phase 3's remaining integration/e2e/smoke CI runs plus a doc-accuracy follow-up
  (DF51) are still open. This is the **Frame** doc — it fixes the layer boundaries, the strategy, and
  the audit methodology. Per-layer exported surface is deliberately *not* specified here; it
  emerges from the audit cycles (decision: facts drive the surface). Supersedes the deferred
  C-full / F notes in [D83](../../decisions/working-notes.md) and builds directly on
  [module-split.md](module-split.md).
- **Depends on:** —

> **🚧 Merge gate (decided 2026-06-25).** This branch does **not** merge to `main`
> until per-agent custom detection strategies are wired for every agent that
> exposes a native turn-completion callback — see
> [agent-detection-strategies.md](../../archive/plans/agent-detection-strategies.md). The shipped
> fall-to-shell + resume work ([agent-owned-detection.md](../../archive/plans/agent-owned-detection.md))
> deferred that strategy formalization to its own task; it is a release blocker, not
> dropped.

## Goal

Decompose yoloAI into a stack of **composable public layers** so a consumer can take exactly
the capability it wants — "run and manage a sandbox" without agents, "diff/apply" without a
PTY, "the backend abstraction" under its own orchestration — and the library itself is built
on those *same* public layers (no parallel private path). The shape is 80/20:

- **80% surface** — the current `yoloai` package (`Client` / `Sandbox` / `System`), unchanged.
  Most embedders never look below it.
- **20% surface** — the layer packages (`runtime`, `store`, `copyflow`, `agent`, …) for power
  users who compose pieces directly.

Increased API surface is acceptable *because* of the 80/20: the top stays small and stable;
the breadth is opt-in.

## Strategy — stage behind `internal/`, promote by move

We commit to the **design** now without committing the **semver surface** yet:

1. **Shape every layer as if it were already public** — package boundary, responsibility,
   exported surface, and path — but keep it under `internal/`. We keep `internal/`'s freedom to
   churn (we renamed `Runtime`→`Backend` last week; we can't do that to a public type) until the
   very end.
2. **Mirror the future public paths 1:1.** Design `internal/runtime` to *be* `yoloai/runtime`;
   never "design `internal/foo`, rename to `bar` at move time." The promotion must be a pure
   path change.
3. **The final stage is a mechanical move** — `git mv internal/<layer> <layer>` + import-path
   sweep, using the playbook proven by the rename pass (gopls + scoped seds, the Makefile/non-Go
   reference gotcha, `make releasetest` as the real gate).
4. **One module throughout.** Go tree-shakes at package granularity, so public packages already
   deliver "import one layer without the baggage" at build time. A separate `go.mod` per layer
   (which additionally prunes the consumer's dependency *graph*) is out of scope until a real
   consumer needs the pruned graph — the original Phase-F trigger.
5. **Beta semver.** Once promoted, a layer's surface is a tracked breaking-change surface
   (`docs/BREAKING-CHANGES.md`). Promote a layer **only when its contract has stabilized**, one
   layer at a time — never big-bang.

## The layer model (boundaries only)

Bottom-up. "As-public path" is the path the internal package should already occupy (or move to)
so promotion is a rename. Exported surface is **out of scope for this doc** — it is the output
of the audit + shape cycles.

| Layer | Responsibility | As-public path(s) | Source today | Stays internal? |
|---|---|---|---|---|
| **Foundations** | pure plumbing (paths, sudo-safe FS, locks, subprocess) | — | `internal/{config,fileutil,locking,sysexec}` | **yes** (but `config.Layout`/`HostEnv` cross every layer — see Q105) |
| **Substrate — backend** | pluggable create/start/stop/destroy/exec/transfer | `yoloai/runtime` (+ `/docker`, `/tart`, …), `yoloai/runtime/caps` | `internal/runtime*` | no |
| **Substrate — record** | persisted sandbox metadata + path layout | `yoloai/store` | `internal/store` | no |
| **Substrate — managed lifecycle** | agent-free create/start/stop/destroy *with* persistence + liveness | the **`Substrate`** handle ([substrate-interface.md](../substrate-interface.md), D84) | carve from `internal/orchestrator/lifecycle` | no |
| **Refinement — copyflow** | copy/diff/apply review over a backend + git | `yoloai/copyflow` | `internal/copyflow` | no |
| **Refinement — session** | interactive PTY / tmux attach over exec | `yoloai/session` | carve from `orchestrator/attach` + `runtime/ptybridge` | no (later) |
| **Refinement — netpolicy** | network isolation / allowlist | `yoloai/netpolicy` | threaded in `runtime/containerd` (DF34) | no (later) |
| **Refinement — envsetup** | archetype detection, mount specs | `yoloai/archetype`, … | `internal/orchestrator/{archetype,mounts}` | no (later) |
| **Agent catalog** | agent install/launch/idle definitions | `yoloai/agent` | `internal/agent` | no |
| **Orchestration (glue)** | weaves substrate-lifecycle + refinements + agent + idle | — | `internal/orchestrator` | **yes** (the root `Client` owns it) |
| **Product** | `Client` / `Sandbox` / `System` | `yoloai` (root) | root pkg | — (already public) |

The orchestration glue stays internal *on purpose*: it is essentially what the root `Client`
already wraps, so exposing both is redundant. The valuable cuts are the layers *below* it.

**Substrate target surface — designed (D84, D85).** The bottom rung's as-public surface
(`Backend`/`Substrate`/`Process` + the liveness-only status model, mechanism-not-policy boundary,
channels-emergent rule, principal-out identity) is specified in
[substrate-interface.md](../substrate-interface.md). It resolves Q103/Q106 (D84) and Q104/Q105 (D85,
persistence + foundation boundaries) and gives DF31/DF32/DF33 their resolution direction.

**Copyflow refinement — designed (D86).** The copy/diff/apply review layer's as-public model
(per-dir repo-aware handle, seeding-vs-propagation, `--all` as collection-never-merge,
characterize-and-surface, the hermetic-git security seal, copyflow-owned baselines) is specified in
[copyflow-layer.md](../copyflow-layer.md), with the seal a verify-the-code finding (DF35).

**Foundation — the persistence helper — designed (D87).** The pattern under D85/D86 (each layer
persists its own facts) generalized into a shared foundation: scoped versioned **handles** over **one
doc per ownership domain** (library / cli / mcp — the D60 bifurcation), a **monotonic-version +
append-only raw-JSON migration registry** (balk + explicit `system migrate`, never auto-migrate),
**`flock` + atomic-rename** concurrency, and the **library/tool single-source-of-truth** ownership
boundary — daemon-optional, file-locks sound inside our envelope. Spec:
[persistence-helper.md](../persistence-helper.md); research:
[shared-state-concurrency.md](../research/shared-state-concurrency.md); findings DF36/DF37. Home/name
of the package deferred (low-stakes behind the `Handle` interface).

**In progress — the session refinement.** Design started, not converged (no D-number):
[session-layer.md](../session-layer.md) has the framing-so-far (a `Session` consumer of the substrate
over a `SessionKind {PTY, Stream}` strategy; concentrate the tmux scatter; separate `SessionKind` from
`PromptMode`; move agent-session launch off the Python entrypoint to a Go-driven `Launch`) and a
**RESUME-HERE** section with the open questions. This is where the work paused.

## Audit methodology

Two independent audits, run per intended layer, draining to the existing queues.

### 1. Mechanical separation (escaped dependencies → `findings-unresolved.md`, DF series)

The test is the one the module-split plan named: **`go list -deps <layer>` must contain only
that layer's allowed downward dependencies** — its own layer and below, never sideways or
upward. The allowed-DAG:

```
Foundations      → (stdlib + third-party only)
Substrate        → Foundations
Refinements      → Substrate (+ the refinement's own third-party), Foundations   [NOT each other]
Agent catalog    → Foundations                                                   [standalone]
Orchestration    → Substrate + Refinements + Agent
Product (yoloai) → Orchestration + every public layer
```

Any import that violates this is an **escaped dependency** → a `DF` finding. Once a layer's DAG
is clean, a depguard fence (module-split Phase D) locks it so it can't silently re-tangle.

### 2. Semantic conflation (→ `questions-unresolved.md`)

A package can be import-clean and still *conflate two concepts in one type* — the harder audit.
The seed example: "idle" is meaningless without an agent (Q103). These need a **decision**, not
a mechanical fix, so they go to the questions queue and earn a D-number when resolved.

### Cycle

For each layer, in promotion order: run audit 1 + audit 2 → log DF/Q → **Shape** (resolve them
behind `internal/`, each resolution a D-entry) → re-audit until the layer's intended DAG holds
and its conflations are resolved → only then is it a promotion candidate. "100% separation" is
not assumed; the audit is iterated until it's measured.

## Seeded register (first audit pass)

The audit has already turned up escaped deps and conflations (measured, 2026-06-14):

- **Escaped deps:** [DF31](../findings-unresolved.md) (substrate `Backend` bakes in tmux +
  monitor), [DF32](../findings-unresolved.md) (no agent-free managed lifecycle —
  `orchestrator/lifecycle` pulls `agent` + `copyflow`), [DF33](../findings-unresolved.md)
  (`runtimeconfig` mixes substrate + agent-launch fields), [DF34](../findings-unresolved.md)
  (netpolicy threaded into the containerd backend).
- **Conflations:** Q103 ("idle" without an agent — liveness vs activity), Q104
  (`store.Environment` carries agent payload), Q105 (`config.Layout`/`HostEnv` crosses every
  layer — foundation publicity), Q106 (the `sandbox` noun — name of the managed-lifecycle layer
  vs the `yoloai.Sandbox` handle). See [questions-unresolved.md](../questions-unresolved.md).

This is a seed, not the full set — later cycles will add more.

## Stages

1. **Frame** — this doc. *(in progress)*
2. **Audit cycles** — mechanical + semantic, per layer, draining DF/Q. Iterate to a clean
   intended-DAG behind `internal/`.
3. **Shape** — restructure behind `internal/` to the as-public layout/surface, resolving each
   finding/conflation; each resolution a D-entry. Includes the substrate managed-lifecycle carve
   (the load-bearing one — DF32) and the idle/liveness split (Q103).
   - **3b. Substrate surface-cleanup (pre-Move, D97).** The pre-Move audit
     ([plans/move-audit.md](move-audit.md), D97) found the *functional* carve done (Launch +
     keepalive + agent-reroute, S0–S3) but the *public surface* still agent-shaped: a pure
     `git mv` would freeze agent concepts into semver. Before promoting `runtime`/`store`, a
     bounded behavior-preserving sub-phase must extract the agent-shaped exports
     (`BackendDescriptor.{AgentProvisionedByBackend,AgentInstallMethod,AgentLaunchPrefix}`,
     `AgentCommandPreparer`, `InteractiveSession`/tmux → DF31; `store.Environment.{AgentType,Model}`
     → Q104), keep the `paths.go` on-disk *filename* constants unexported-as-API (expose helpers
     only), and clear two stale comments. **Ordering constraint:** `store` imports `runtime`
     (`BackendType`), so `runtime` promotes before/with `store`; `store` cannot go first.
4. **Move** — promote `internal/<layer>` → `yoloai/<layer>` per stabilized layer, mechanically
   (only after 3b for runtime/store). One module. Add depguard fences at each promotion; the
   move is the proven `git mv` + import sweep + fence-repoint, gated by `make releasetest`
   (+ `go vet -tags 'integration e2e'` for the build-tagged tests). See D97 for the verdict.

### Endgame roadmap (D99 — supersedes the piecemeal 3b.x / per-layer step lists)

The program now runs to a **solid, mergeable state** in one branch (`substrate-move`), landing on
`main` as **one clean break** — so *incidental per-commit API/contract churn is fine* (each commit
compiles + passes `make check`; only the public contract may churn between commits). We build
straight toward the final shape, no inter-commit stability dance. Three phases + a low-priority
remainder ([D99](../../decisions/working-notes.md)):

- **Phase 1 — seal every interface (behind `internal/`).** (1a) the **session-carve public
  realization** [long pole]: `IOSession` on `sb.Agent()` (concentrate the tmux scatter), the final
  `Launch`/`ProcSpec` contract, `AgentLaunchPrefix` off the public descriptor, slim
  `sandbox-setup.py` / neutral PID-1 default, **+ build one-shot `-p`/Tier-3** (control-eval).
  (1b) **Q104** agent/model → `sb.Agent().Type()/Model()` + `agent.json` + M2 migration
  ([store-workload-split.md](../../archive/plans/store-workload-split.md)). (1c) `paths.go` helpers-only. (1d) re-home
  the residual `entrypoint.py` secrets-read for legacy backends.
- **Phase 2 — near-term consumer surface (control-eval).** **Done:** `yoloai wait` + `sandbox_wait`
  (pre-existing); `yoloai run` + headless/Tier-3 (D100/D101); structured diff `--json` +
  `Workdir.Changes()`; MCP `sandbox_run` + native concurrency (the mark3labs server runs tool calls
  in parallel). **Deferred follow-up:** `Sandbox.Usage()` token/cost ledger — needs the headless
  agent to emit `--output-format json` + stdout capture + per-agent parsing (claude/gemini/… differ);
  decided 2026-06-26 to ship the rest and leave Usage as a focused later task. **UX fixes DONE
  (2026-06-27):** the `new` summary already prints the resolved model (since Q104's
  `printCreateSummary` reads `sb.Agent().Model()`); name-format validation now runs up front at the
  shared CLI edge (`resolveCreateOptions`, covering `new`+`run`) so a typo'd or swapped-with-workdir
  name fails instantly with the "looks like a path" hint instead of after client/setup work, and
  `cliutil.ValidateName` became a pure `config.ParseSandboxName` call (no `System`/root-Layout
  dependency); the charset error gained a valid example.
- **Phase 3 — the Move.** `git mv` the sealed layers → public (default `runtime`+`store`+`copyflow`
  +`agent`; plumbing layers stay clean-internal, promote later additively), fences, `releasetest`,
  one `BREAKING-CHANGES` entry (D97). Final promotion set decided at the Move.
  - **DONE (2026-06-27, commit `10004e1a`): `runtime`+`store`+`copyflow` promoted.** `git mv` to
    module root, tree-wide import sweep (174 files / 155 renames), fence repoint (`.golangci.yml`
    depguard + runtime-core glob; Makefile/lock paths), constructed-path + doc-comment fixes.
    Verified green: `make check`, golangci-lint, `go vet -tags 'integration e2e'`, `make build`,
    tagged-test link. **`agent` is NOT git-mv'd** — its public surface is the root capability
    catalog (D89), already sealed. **No `BREAKING-CHANGES` entry: the promotion is additive** —
    external consumers could never import `internal/*`, and the root `yoloai` API is unchanged, so
    new public packages add surface without breaking anything. Remaining: the integration/e2e/smoke
    *runs* need real backends (CI); narrative/design-doc path references are an accuracy follow-up
    (DF51, findings-unresolved).
- **Remainder (post-merge):** Stream `SessionKind`, D95 broker, netpolicy egress-proxy,
  plumbing-layer promotions, macOS findings, backend research, op-hardening.

The earlier "3b" framing (above) is now Phase 1's substrate slice; what 3b deferred as "Move-prep"
is folded into Phase 1 directly (no stability constraint to defer it for).

## Non-goals

- Separate `go.mod` per layer (one module until a pruned-dep-graph consumer appears).
- Publishing external libraries / separate repos.
- Promoting the orchestration glue (it duplicates the root `Client`).
- Big-bang promotion (per-layer, as each stabilizes).
