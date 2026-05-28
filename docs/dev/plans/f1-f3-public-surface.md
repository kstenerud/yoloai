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
| `Yes` | | | ✗ | CLI-UX only — the library never prompts (api_surface: confirmation is the caller's concern) |
| `Attach` | | | ✗ | CLI-UX — attach is a separate `Sandbox(name).Attach`; not a creation param |
| `Version` | | | ✗ | library fills it from build info; not a caller input |
| (`Backend`) | — | — | — | stays on `yoloai.Options` (Client construction), not CreateOptions; F4 makes empty → `*UsageError` |
| `Wait` / `OnProgress` | ✓ | — | | run-flow only; not creation params |

Net: public `CreateOptions` ≈ 21 creation fields; 3 internal fields
(`Yes`/`Attach`/`Version`) never reach the public surface.

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

1. ✅ **Drop `Yes`, `Attach`, `Version`** from the public creation surface. The
   library never prompts; create-then-attach is two calls; version is build-info.
2. ✅ **`CreateOptions.Ports` is `[]PortMapping`** (typed, per Q-Y). The CLI
   parses its `--port` flag into `PortMapping` at the boundary.
3. ✅ **`Create` returns `(string, error)`** (the name). `Run` is the one that
   returns `*Info` (after Wait); Create doesn't wait.
4. ✅ **`RunOptions.WorkDir` stays `string`** (copy-mode implied). Full `DirSpec`
   (mode / mount-path / `:rw` / `:overlay`) lives only in Tier-2 `CreateOptions`.
5. ✅ **Bundle F4** — `Options.Backend == "" → *UsageError` lands in this pass
   (same `NewWithOptions` construction surface; one guard + a test).
6. ✅ **`Create(ctx, CreateOptions)`** — keep the name; F1/F3 only swap its
   parameter from the internal struct to the public one. No `RunRaw`.

Design fully signed off. Implementation is the shared re-rooting PR with the F2
mapping (`f2-subhandle-mapping.md`) — both reshape the root `Run`/`Create` entry
points, so they land together.
