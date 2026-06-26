# Q104 — split the inside-process config out of the substrate record

**Status:** Scoped 2026-06-26 (Stage 3b of the public-layering Move). Resolves **Q104**.
Gated decision pending (migration style, below). Branch: `substrate-move`.

## Why

`store.Environment` (`environment.json`) is the sandbox's persisted record and will be
promoted to public `yoloai/store` by the Move. Today it holds `AgentType` + `Model`, which
are **configuration of a process (an agent) that runs *inside* the sandbox** — categorically
distinct from the record's other fields:

- *constitutive* — `BackendType`, `ImageRef` (the sandbox **is** a backend instance from an image)
- *policy on the sandbox* — `Dirs`, `NetworkAllow`
- *audit/provenance* — `CreatedAt`, `YoloaiVersion`, `Name`, `Principal`, `Profile` (the request, fanned out)
- *inside-process config* — `AgentType`, `Model` ← **these don't belong on a substrate record**

Promoting a substrate record that describes a tenant process's config would freeze the
conflation into public semver. So before the Move: move `{agent, model}` to their own
orchestration-owned doc.

## Target shape (decided)

- **`store.Environment`** keeps constitutive + policy + provenance; **sheds `AgentType`/`Model`**.
- **New per-sandbox doc — `agent.json`** (option (a), separate doc, not a section) holding the
  inside-process config `{AgentType, Model}`. **Owned by orchestration** (the layer that decides
  "what runs inside"), NOT the agent *catalog* (which stays zero-import standalone). `store`
  provides only the persistence mechanism.
  - Persistence: prefer the D87 `store.Handle` (`agent.json` would be its first real consumer,
    forward-aligned) if the `OpenDomain` API fits a per-sandbox doc cleanly; otherwise a plain
    `Load/Save` mirroring `environment.json`. Implementation detail, not a blocking decision.

## Read/write sites to redirect (inventory)

Writers (set at creation): `create.go` (builds `Environment` from resolved opts/profile).
Readers of `meta.AgentType` / `meta.Model` that must instead load `agent.json`:

- **`orchestrator/lifecycle/restart.go`** — the heavy consumer (~10 refs): `agent.GetAgent(meta.AgentType)`,
  `resolveAgentArgs(..., meta.AgentType, ...)`, `BuildAgentCommand(agentDef, meta.Model, ...)` across
  the relaunch/respawn paths. Each already loads `meta`/`cfg`, so loading a sibling `agent.json`
  is a parallel add, not new threading.
- **`network.go`** — `agentNetworkFloor(string(meta.AgentType))` (×2).
- **`create.go`** — `Model: meta.Model` into the container config.
- **`cli/sandboxcmd/bugreport.go`** — display of agent/model.
- Construction/options plumbing: `environment.go` (build from config Meta), `sandbox_options.go`,
  `profile.go` — these populate the value; they move to populating `agent.json`'s record.

## The migration (v2 → v3) — the wrinkle + the open decision

`environment.json` is at `metaVersion = 2`. The new version **3** removes `agent`/`model`. The
migration is **cross-file**: it must read the *old* keys (which the slimmed struct no longer
has) and relocate them into `agent.json`. This is the "append-only **raw-JSON** migration step"
the `environment.go` `MigrateRecord` TODO already anticipates (read raw map → pull `agent`/`model`
→ write `agent.json` → delete keys → set version 3).

Today per-sandbox metadata migrates **transparently on `LoadEnvironment`** (v0→v2, in-struct,
no user action). The data-dir realm migrates **explicitly** (`yoloai system migrate`, balk-on-stale,
D61). The cross-file v2→v3 step forces a choice — **this is the decision needed before building:**

- **(M1) Extend the transparent ladder.** A raw-JSON v2→v3 step runs at load: relocate to
  `agent.json`, strip, write v3. Pro: old sandboxes keep working with no user action; matches
  today's per-sandbox behavior. Con: `Load` gains a file-*write* side effect (it writes
  `agent.json` + rewrites `environment.json`), which is exactly what the explicit-migrate
  philosophy (D61, [[feedback-migration-versioning-philosophy]]) exists to avoid.
- **(M2) Balk + explicit `system migrate`.** A v2 `environment.json` → `ErrNeedsMigration`;
  `system migrate` gains a per-sandbox pass that does the relocation. Pro: no Load side effects;
  aligns the per-sandbox record with D61/D87 (the stated direction; the `environment.go` TODO).
  Con: existing sandboxes balk until the user runs `system migrate` — new per-sandbox UX, and
  `system migrate` doesn't iterate sandboxes today (new wiring).

**Recommendation: M2.** It matches the project's explicit-migrate philosophy and the D87
direction the code is already TODO'd toward, and it keeps `Load` pure (no write-on-read). The
cost (a one-time `system migrate` for existing sandboxes) is exactly the model D61 chose
deliberately. M1 is the lighter, less-surprising-for-now option if we'd rather not expand
`system migrate`'s scope in this step.

## Decomposition (once the M1/M2 decision is made)

1. **New `agent.json` record + orchestration ownership** — type, persistence, `create` writes it.
   Readers still read `meta.AgentType/Model` (dual-present), so this step is behavior-preserving.
2. **Redirect readers** to `agent.json` (restart, network, create-config, bugreport).
3. **Slim `store.Environment`** — drop the fields; `metaVersion` → 3; add the raw-JSON v2→v3
   relocation under the chosen migration style (M1 ladder step, or M2 `system migrate` pass +
   balk).
4. **Tests + BREAKING-CHANGES.md** — the on-disk sandbox-record format changes (existing
   sandboxes need migration); note it even though `store` is not yet public.

Each step is its own commit; 1–2 are behavior-preserving, 3 is the semver-relevant cut.

## Cross-references

[D97](../../decisions/working-notes.md) (the surface-cleanup that surfaced Q104),
[persistence-helper.md](../persistence-helper.md) (D87 Handle + raw-JSON migration model),
[public-layering.md](public-layering.md) Stage 3b. Decision earns a D-number when M1/M2 settles.
