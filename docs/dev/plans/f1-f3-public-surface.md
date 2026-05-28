# F1 + F3 — Public creation surface (design for owner review)

Combined design for the two creation findings. **Proposal — no code.** Approve
or overrule; the implementation lands *together with* the F2 re-rooting
(`f2-subhandle-mapping.md`), since both reshape the root `Run`/`Create` entry
points.

## The problem

Two creation entry points with no documented boundary:

- **`Run(ctx, RunOptions)`** — curated 8-field surface (Name, WorkDir, Prompt,
  Agent, Model, Profile, Replace, Wait + OnProgress). A *trap* for embedders:
  half the CLI's capabilities (network, ports, isolation, env, aux dirs,
  resources, archetype, …) aren't reachable, so they fall back to Create.
- **`Create(ctx, sandbox.CreateOptions)`** — takes the **internal** struct
  (26 fields). External embedders *cannot import `internal/sandbox`*, so Create
  is unusable outside the module — only the CLI/MCP (in-module) can call it.

So today there's effectively no advanced creation path for an external embedder.

## The design (F1 two-tier + F3 Run-as-sugar)

Two public tiers, with `Run` as sugar over `Create`:

```
Run(ctx, RunOptions)  ──opts.materialize()──▶  Create(ctx, CreateOptions)  ──toInternal()──▶  manager.Create
   (curated, ~8 fields,                          (PUBLIC advanced struct,                       (internal/sandbox.CreateOptions)
    + Wait/OnProgress)                            full creation surface)
```

- **Tier 1 — `RunOptions`** (unchanged): the curated convenience surface for the
  common case. Adds the run-flow extras `Wait` / `OnProgress` that aren't
  creation params.
- **Tier 2 — `yoloai.CreateOptions`** (NEW, public): a root-package struct that
  mirrors the internal one but uses only public re-exported types, so external
  embedders can construct it. `Create` takes *this*, not the internal struct.
- **`Run` materializes into `Create`** (F3): `Run` builds a `CreateOptions` from
  its curated fields, calls `Create`, then layers the `Wait`/`OnProgress`
  behavior. One creation code path underneath.

### New root re-exports required

`yoloai.CreateOptions` references types currently only in `internal/sandbox`.
Re-export at the root (type aliases, same pattern as `PortMapping`/`IsolationMode`):

- `type DirSpec = sandbox.DirSpec` (for `Workdir` / `AuxDirs`, carries mount Mode)
- `type DirMode = sandbox.DirMode` + the `DirModeCopy/Overlay/RW/RO` consts —
  `DirSpec.Mode` is a `DirMode`, so embedders need it at the root too (today it
  only exists as `sandbox.DirMode`, behind the `internal/` boundary).
- `type NetworkMode = sandbox.NetworkMode` (+ the `NetworkModeNone/Isolated` consts)

## Field split (internal `sandbox.CreateOptions` → public surface)

| Internal field | Tier-1 `RunOptions` | Tier-2 `yoloai.CreateOptions` | Dropped | Note |
|---|:---:|:---:|:---:|---|
| `Name` | ✓ | ✓ | | |
| `Workdir DirSpec` | `WorkDir string` | `Workdir DirSpec` | | curated = path (copy default); advanced = full DirSpec w/ Mode |
| `AuxDirs []DirSpec` | | ✓ | | |
| `Agent` | ✓ | ✓ | | |
| `Model` | ✓ | ✓ | | |
| `Profile` | ✓ | ✓ | | |
| `Prompt` | ✓ | ✓ | | |
| `PromptFile` | | ✓ | | |
| `Network NetworkMode` | | ✓ | | |
| `NetworkAllow []string` | | ✓ | | |
| `Ports []string` | | ✓ (`[]PortMapping`) | | retype to the public PortMapping (Q-Y) |
| `Replace` | ✓ | ✓ | | safe replace (errors on unapplied work) |
| `Force` | | ✓ | | unconditional replace; advanced only |
| `NoStart` | | ✓ | | create without launching the agent |
| `Passthrough []string` | | ✓ | | args after `--` |
| `Debug` | | ✓ | | |
| `CPUs` / `Memory` | | ✓ | | |
| `Env map[string]string` | | ✓ | | |
| `Isolation IsolationMode` | | ✓ | | |
| `Runtimes []string` | | ✓ | | Apple simulator runtimes |
| `VscodeTunnel` | | ✓ | | |
| `Archetype` | | ✓ | | |
| `Yes` | | (replaced) | ✗ | **Revised (impl finding, 2026-05-28).** `Yes` conflated "skip prompts" with "proceed despite dirty/unverified." Split into typed refusals + named acks — see *Typed creation refusals* below. The flag itself is gone. |
| `Attach` | | | ✗ | CLI-UX — attach is a separate `Sandbox(name).Attach`; not a creation param |
| `Version` | | | ✗ | library fills it from `Options.Version` (Client construction); not a per-create input |
| — `AllowDirtyWorkdir` | | ✓ | (new) | ack: override `*DirtyWorkdirError` for the workdir. OR'd with `Workdir.AllowDirty`. |
| (`Backend`) | — | — | — | stays on `yoloai.Options` (Client construction), not CreateOptions; F4 makes empty → `*UsageError` |
| `Wait` / `OnProgress` | ✓ | — | | run-flow only; not creation params |

Net: public `CreateOptions` ≈ 22 creation fields; `Attach`/`Version` never reach
it, and `Yes` is replaced by the two typed-refusal acks below.

## Typed creation refusals (replaces `Yes`)

**Impl finding (2026-05-28).** `Create` does *not* prompt — but the internal
manager did, via two `Confirm` calls gated by `Yes`:

- `checkDirtyRepos` — the **host** workdir / aux dirs have uncommitted git changes
  (data-loss risk: the agent sees/modifies your WIP; on `:copy`, apply later
  conflicts with the still-dirty host). **Real refusal.**
- `checkRequires` — the project's `.yoloai.yaml` declares `requires:` tool
  versions. **But version verification is unimplemented** — the gate is a
  placeholder prompt for a stub feature.

`Yes=true` skipped both; `Yes=false` prompted. That conflates "non-interactive"
with "proceed despite the risk," and a headless embedder setting `Yes=true`
silently disabled the dirty guard — a footgun. The §10 fix: the library **never
prompts**; for a real risk it **refuses by default** with a typed error the
caller must consciously override (same shape as `Destroy`→`*ActiveWorkError`).

- **Dirty workdir** → manager returns `*DirtyWorkdirError{Paths []string}`
  instead of prompting. Public acks (named for the *specific* refusal, verb
  `Allow`):
  - `CreateOptions.AllowDirtyWorkdir bool` — overrides the dirty refusal for the
    workdir. Effective per-dir override is `Workdir.AllowDirty || AllowDirtyWorkdir`.
  - `DirSpec.AllowDirty bool` — **new** per-directory override (needed because
    aux dirs are dirty-checked too). Generic name: a `DirSpec` may be the workdir
    *or* an aux dir.
- **Requires** → **dropped** (owner, 2026-05-28). No typed refusal, no ack.
  Gating a stub fails YAGNI, and an `AllowUnverifiedRequires` ack would die the
  moment real verification lands (the refusal would become "requirement *not
  met*", not "unverified"). `checkRequires` downgrades to a **non-blocking
  warning** (print, don't prompt). A real `*RequirementsNotMetError` gets
  designed when version verification is actually built.
- A **forgetful** caller gets the *error* (safe), not a silent clobber. To
  proceed they must name the risk they accept — no blanket "yes."
- The **CLI** `new` catches `*DirtyWorkdirError` → prints the warning → prompts →
  retries `Create` with `AllowDirtyWorkdir`. The prompt moves to the CLI; the
  library is prompt-free.

Adjacent cleanup: `DirSpec.Force` is a misnomer — its comment says "skip
dirty-repo safety check" but it actually overrides the **dangerous-directory**
refusal (the `:force` mount suffix). Renamed `DirSpec.Force` →
`DirSpec.AllowDangerousPath` (+ corrected comment). The user-facing `:force`
suffix is unchanged. This is a *different* refusal from dirty/replace; `Force`
(unconditional *replace*) and `Replace` are untouched.

## Entry points (final shape)

```go
// Tier 1 — curated convenience; waits when RunOptions.Wait.
func (c *Client) Run(ctx, RunOptions) (*Info, error)

// Tier 2 — advanced; the deep entry. Takes the PUBLIC CreateOptions.
func (c *Client) Create(ctx, CreateOptions) (string, error)
```

- Keep the name **`Create`** for the deep entry (not `RunRaw`) — it already *is*
  the creation primitive; F1/F3 only swap its parameter from the internal struct
  to the public one. `Run` is the sugar.
- Internal helpers (unexported): `RunOptions.materialize() CreateOptions` and
  `CreateOptions.toInternal() sandbox.CreateOptions`.
- The CLI's `new` is migrated to build the **public** `yoloai.CreateOptions`
  (it currently builds the internal struct directly) — bulk of the impl work,
  not this design pass.

## Decisions — RESOLVED (owner, 2026-05-28; all recommendations accepted)

1. ✅ **Drop `Attach`, `Version`; replace `Yes` with typed refusals.** Originally
   "drop all three." Implementation revealed the manager *did* prompt (gated by
   `Yes`), so a clean drop would silently disable the dirty/requires guards.
   Revised (owner, 2026-05-28): `Attach`/`Version` still dropped; `Yes` is
   replaced by `*DirtyWorkdirError`/`*UnverifiedRequiresError` + the
   `AllowDirtyWorkdir`/`DirSpec.AllowDirty`/`AllowUnverifiedRequires` acks. The
   library never prompts; the CLI catches→prompts→retries. See *Typed creation
   refusals* above.
2. ✅ **`CreateOptions.Ports` is `[]PortMapping`** (typed, per Q-Y). The CLI
   parses its `--port` flag into `PortMapping` at the boundary.
3. ✅ **`Create` returns `(string, error)`** (the name). `Run` is the one that
   returns `*Info` (after Wait); Create doesn't wait.
4. ✅ **`RunOptions.WorkDir` stays `string`** (copy-mode implied). Full `DirSpec`
   (mode / mount-path / `:rw` / `:overlay`) lives only in Tier-2 `CreateOptions`.
5. ✅ **Bundle F4 — and resolve the F4/F21 collision.** `Options.Backend == "" →
   *UsageError` lands here. **Impl finding (2026-05-28):** F4 directly conflicts
   with F21's empty-Backend routing (`Options.Isolation`/`OS` →
   `resolveBackendFromConfig`) — the same `NewWithOptions` line. Resolution
   (owner): **F4 wins.** Require `Backend`; **delete `Options.Isolation`/`OS`**
   (dead — no in-tree caller sets them; the CLI resolves the backend at its own
   boundary via `ResolveBackend`/`SelectBackend` and passes a concrete `Backend`).
   Backend selection is inherently ambient (probes installed daemons), so it
   belongs at the boundary (§12), not silently inside construction. F21's core
   (the `SelectBackend` routing function) stays. To preserve the auto-detect
   convenience for external embedders *explicitly*, add a public
   `yoloai.SelectBackend(ctx, preferred, isolation, os) (BackendName, warning)`
   they call and pass the result — explicit, not an implicit construction default.
6. ✅ **`Create(ctx, CreateOptions)`** — keep the name; F1/F3 only swap its
   parameter from the internal struct to the public one. No `RunRaw`.

Design fully signed off. Implementation is the shared re-rooting PR with the F2
mapping (`f2-subhandle-mapping.md`) — both reshape the root `Run`/`Create` entry
points, so they land together.
