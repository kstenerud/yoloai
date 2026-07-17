> **ABOUTME:** Holding pen for findings parked as "not now" but still potentially actionable.
> Each entry carries a revival trigger that, once it fires, pulls it back to the unresolved queue.

# Deferred findings

Findings (issues discovered mid-work) parked as "not now." Unlike
[`findings-resolved.md`](findings-resolved.md) (terminal history), every item here is still
potentially actionable and carries a **`Trigger:`** line — the condition that should pull it
back into [`findings-unresolved.md`](findings-unresolved.md). The trigger may be unlikely, but
it must exist so the item can be evaluated for eviction later. Newest first.

### DF127 — tart's legacy-CLI VM matcher is a heuristic that should not live forever

- **Discovered:** 2026-07-17 · **Workstream:** fixing [DF125](findings-resolved.md) (pre-upgrade orphans left unreapable by D126)
- **Severity:** LOW — it over-reaches only for a principal that has never existed (an integrator running tart under their own principal on a shared host, with a colliding sandbox name). Every other backend's half of DF125 is exact and permanent; this is the one heuristic.
- **Disposition:** DEFERRED — deliberately kept, because the alternative is worse: without it, a tart VM whose sandbox dir is gone holds one of the host's **capped VM slots** forever with no yoloai command able to name it. Tart records no labels (DF124 — for a label-less backend the name is the identity), so there is nothing to read; the name is all there is, and the legacy form `yoloai-<S>` provably overlaps every principal namespace (DF125), so no exact matcher exists. `legacyCLIVMName` therefore claims `yoloai-<S>` minus the namespaces that have actually been minted — `yoloai-base`, `cli-*`, and the test principals' `tNNNNNNN-`.
- **Trigger:** EITHER of —
  1. **A second real principal on tart.** The moment an integrator can run tart under their own principal, the matcher can reach their VMs and must go. `TestPruneLegacyMatchOverreachesForAnUnseenPrincipal` pins this exact compromise and will fail, forcing the decision rather than letting a sweep quietly delete a VM.
  2. **Legacy VMs are gone in practice** — a settling period long enough that pre-D126 (v0.8.0-and-earlier) installs are not still upgrading. **Explicitly NOT a release number** (owner, 2026-07-17): a user may upgrade v0.8.0 → v0.10.0 directly and still hold legacy VMs, so retiring this "next release" would abandon exactly the population it exists for. The condition is evidence, not a version.
- **Description:** `runtime/tart/prune.go`'s `legacyCLIVMName` lets the CLI's orphan sweep reclaim VMs named `yoloai-<sandbox>` — the form tart VMs carried before the CLI adopted the `cli` principal (D126). It is gated on the CLI's own principal, so an integrator's sweep can never adopt the unprincipaled namespace (that would rebuild DF115 by hand). Retiring it is a deletion: drop the function, its call in the candidate condition, and the three tests that cover it, leaving the plain `InstancePrefix` match.
- **Pointer:** `runtime/tart/prune.go` (`legacyCLIVMName`, `testPrincipalVMRe`); `runtime/tart/prune_test.go` (`TestPruneReclaimsLegacyCLIVMs`, `TestPruneSparesKnownLegacyVM`, `TestPruneLegacyMatchOverreachesForAnUnseenPrincipal`); [DF125](findings-resolved.md) (why it exists, and why the label backends need no equivalent); [DF124](findings-resolved.md) (why tart has nothing to read); D126.

### DF45 — base-image build lock is keyed by data-dir but the image tag is global to the docker daemon

- **Discovered:** 2026-06-24 · **Workstream:** public-layering Shape (concurrency question raised during the smoke)
- **Severity:** LOW (benign redundancy, **not** corruption — surfaced for the multi-principal/[D62](../decisions/working-notes.md) direction)
- **Disposition:** DEFERRED — single-data-dir behaviour is correct today; this is benign redundancy, not corruption.
- **Trigger:** a multi-principal daemon that serves several data dirs against one docker daemon — at that point the data-dir-keyed lock no longer covers the globally-tagged image and two principals can rebuild `yoloai-base` over each other.
- **Description:** `Setup` serializes base-image builds with a proper double-checked `flock`: acquire `layout.DockerBaseLockPath("yoloai-base")` → re-check `imageExists` + `NeedsBuild` **inside** the lock → build only if needed → write the checksum inside the lock. So concurrent `yoloai new` within one data dir **cooperate** (one builds, the rest block then skip — no double build, no checksum race, no tag stomp). BUT the lock path derives from the **data-dir** (`layout`), while the image tag `yoloai-base` is **global to the docker daemon**. Two `yoloai new` with *different* `--data-dir` against the *same* daemon (the D62 multi-principal case) do **not** serialize on this lock → redundant concurrent `docker build` of the same global tag, last-write-wins. Benign (wasted work; per-data-dir checksum files don't corrupt each other), but a latent inefficiency the multi-tenant work should account for — e.g. namespace the tag per principal, or key the lock on the global image name rather than the data dir. Ties into the [shared-state-concurrency](research/shared-state-concurrency.md) research (D87): "is the lock keyed to the same scope as the resource it guards?"
- **Pointer:** `runtime/docker/docker.go:332` (`Setup`, the double-checked lock), `runtime/docker/base_lock.go` (`AcquireBaseLock` → `DockerBaseLockPath`), `runtime/docker/build.go:42-54,134` (checksum). Tart mirrors the same pattern.

### DF15 — Sandbox name + workdir path validate by a different convention than their parse-don't-validate peers

- **Discovered:** 2026-06-01 · **Workstream:** W-L1 (F9 doc-truth fix)
- **Severity:** LOW (correctness today) / **security-hygiene** (the real reason it's tracked)
- **Disposition:** PARKED — deliberately not half-converted ad-hoc; sequenced with D58/D59.
- **Description:** `development-principles.md` §4 holds up parse-don't-validate as the convention
  for security-relevant boundary values, and most are genuine parsed types (`MountMode`,
  `AllowedDomain`, `AgentType`, the W-L8a `yoloai.NetworkMode/IsolationMode/ApplyMode/LogFormat`,
  `Patch`, `BackendDescriptor`). **Two are not:** sandbox name is guarded by
  `store.ValidateName(string) error` (`internal/sandbox/store/paths.go`) and workdir path by
  `config.ExpandPath(...)` returning a bare `string` (`internal/config/pathutil.go`). Both are
  *validate*-style — the type system carries no proof, so any new call site can pass an
  unvalidated value. These are exactly the path-construction inputs (`SandboxDir(name)`, tilde/
  env resolution) on which the D58/D59 multi-principal path-confinement work hinges. **The hazard
  is the convention drift itself**, not any single missing check: a security-relevant value that
  validates by a *different convention* than its peers is what a code audit skips over, and
  ad-hoc one-off guards following divergent conventions compound that. Converting these to
  parsed types (a `SandboxName` with a `Parse` constructor; a resolved-path type after symlink/
  tilde/env resolution) restores the single convention. Not done now to avoid surface-wide churn
  (name/path flow as `string` through many signatures) whose payoff lands in the D58/D59 work.
- **Trigger:** the start of D58/D59 path-confinement / principal-isolation implementation —
  convert `SandboxName` + resolved-path to parsed types as part of that pass. Revive sooner if a
  "forgot to validate the name/path" bug surfaces, or if any *new* security guard is added that
  would otherwise introduce a third validation convention (do it consistently instead).
- **Pointer:** `internal/sandbox/store/paths.go` (`ValidateName`); `internal/config/pathutil.go`
  (`ExpandPath`); `development-principles.md` §4 (the `†` note); `security-principles.md` §11
  (one-convention-per-mechanism — DF15 is its canonical live instance); decisions D58/D59.

### DF2 — Smoke test prompt may provoke a clarifying-question idle on Haiku (containerd-vm)

- **Discovered:** 2026-05-24 · **Workstream:** observed during W-L4 validation
- **Severity:** LOW
- **Disposition:** PREVENTIVE FIX LANDED 2026-05-27 (option (a)); empirical verification still TBD on the next flake — if there ever is one — using the rendered transcript captured by DF3.
- **Description:** `stop_start/containerd-vm` failed once with the documented "agent idle for 9s+ without sentinel 'done'" signature, then passed cleanly on isolated rerun. Existing idiosyncrasy entry blames QEMU slow startup (extended by `stall_grace_secs=120` in `scripts/smoke_test.py:191,212,216`), but the prompt itself is also suspicious: `"Run this shell command exactly as written; do not modify it or ask for clarification: touch …"`. The negative phrasing ("do not ask for clarification") can prime smaller / faster models like Haiku to do exactly that — output a clarifying question (no tool call), which yoloAI's monitor classifies as `idle`. The agent then waits forever for a user response that never comes, while the smoke test waits forever for the `done` sentinel file.
- **Fix landed 2026-05-27 (option (a), preventive):** `scripts/smoke_test.py::_prompt` rephrased from `"Run this shell command exactly as written; do not modify it or ask for clarification: <cmd>"` → `"Run this shell command exactly as written, using your shell/bash tool: <cmd>"`. Keeps the explicit "Run this shell command" wrapper that previously resolved the v1 "what is this code?" failure on bare snippets, drops the negation that DF2 hypothesizes activates the failure response, and adds a positive tool reference so the model gets a hint that a tool call is expected. The full iteration history is preserved in `_prompt`'s docstring so future readers don't re-derive the rationale.
- **Why not option (b)?** Distinguishing "tool-less response on a tool-required prompt" from "real idle" requires monitor-side classifier changes (`internal/runtime/monitor/status-monitor.py` reading the agent's tmux output, parsing the last narrative turn, asking "did the model speak without calling a tool?"). That's a heavier instrument than the cost of one prompt rephrase. Revisit if option (a) doesn't eliminate the flake class — if a future failure transcript still shows Haiku producing a non-tool-using reply under the new wording.
- **Pointer:** `scripts/smoke_test.py::_prompt` (current wording + iteration history), `docs/contributors/backend-idiosyncrasies.md#qemu-slow-startup-exceeds-smoke-test-stall-grace-period` (existing complementary entry).

**Trigger:** the next post-DF10 `containerd-vm` "agent idle" flake — inspect DF3's rendered transcript to confirm whether Haiku produced a tool-less reply under the QEMU CPU profile. If no such flake recurs now that DF10 fixed the netns leak, evict this finding.
