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

## Public surface — the clean end-state (Move-time)

The split has a public dimension too: today agent/model leak onto the public read-model
(`yoloai.Environment` view, `environment.go:27-28`). The clean/consistent surface mirrors the
internal taxonomy — **agent identity (type/model) lives on `sb.Agent()`** (the agent noun, which
already surfaces the agent's prompt/terminal/attach; D67/agent-layer.md "`sb.Agent()` = identity
(type/model) + …"); the substrate view and public `store.Environment` carry **substrate facts
only**. So at the Move, `sb.Agent()` gains `Type()`/`Model()` and the `Environment` view sheds them.

## Resequencing — lands NOW, straight to final shape (revised 2026-06-26)

**Constraint lifted (user, 2026-06-26):** an inconsistent public API mid-branch is fine
([[feedback-inconsistent-public-api-ok-midbranch]]) — the public reshape need NOT be bundled into a
single Move-time pass to keep the API stable. The original "defer to Move-prep" framing existed only
to avoid a *throwaway* public-`Environment`-stable shim; with that no longer required, Q104 lands
**now, all at once, straight to its final shape** (slim + public reshape together) — no shim, so the
[[feedback-feature-branch-no-transitional-scaffolding]] concern is satisfied directly. The migration
bridges old on-disk records, so there is no runtime fallback either. `git mv` (the Move) stays a
pure path change with Q104 already done.

- **Step 1 — DONE (`70e7b11f`):** `internal/orchestrator/agentcfg` + `create` dual-writes
  `agent.json` (pre-stages the data; additive, behavior-preserving).
- **Now (one coherent pass, a few commits):** redirect internal readers
  (`restart`/`start`/`requireAgent`; `network` via a new `Engine.LoadAgentConfig`) to read
  `agent.json` directly — **no `meta`-fallback** (the migration guarantees `agent.json` exists by
  the time a reader runs); **slim `store.Environment`** (drop the fields, `metaVersion`→3); the
  **v2→v3 raw-JSON relocation + balk + `system migrate` per-sandbox pass** (M2); surface agent/model
  on **`sb.Agent().Type()/Model()`** and shed them from the public `Environment` view; tests +
  BREAKING-CHANGES.

## Build breakdown (C1 done; C2 = the cutover; C3 = migration test + docs)

**Status: C1 + C2 + C3 all DONE (2026-06-26, `substrate-move`).** The cutover landed
straight to final shape; `make check` + tagged vet green. Settled as **D102**.


Surface confirmed by audit (2026-06-26): `agentcfg.Load` is soft on a missing file (zero-value);
`create` dual-writes `agent.json` (`create.go:736`); per-sandbox `environment.json` migrates
**transparently on load** via the typed `migrate()` ladder (`store/environment.go`), `metaVersion=2`;
`system migrate` (→ `System.MigrateDataDir` → `config.MigrateLibrary`) does **realm**-level work only
and does NOT iterate sandboxes for env-record version bumps. Readers of `meta.AgentType`/`meta.Model`:
`restart.go` (10 refs incl. `requireAgent`, 3×`resolveAgentArgs`, 3×`BuildAgentCommand`, `state.Model`),
`network.go` (×2 `agentNetworkFloor`), `bugreport.go` (×2 display). Public view: root
`environment.go:27-28` + `environmentFromStore`.

- **C1 — DONE (additive groundwork, committed):** `Engine.LoadAgentConfig` + `sb.Agent().Type()/Model()`
  read `agent.json`. No behavior change; public `Environment` still carries agent/model (coexist).

### C2 build — two refinements settled during implementation (2026-06-26)

1. **Public read-model: agent/model ride on `SandboxInfo` top-level, not only `sb.Agent()`.**
   The plan said "consumers move to `sb.Agent().Type()/Model()`". Refined: the
   `Environment` *substrate view* sheds them (correct), but the *aggregated*
   read-model `SandboxInfo`/`status.Info` keeps them as **top-level fields**
   (`info.AgentType`, `info.Model`) — joining `AgentStatus`, which is already
   top-level on `SandboxInfo`, not buried in `Environment`. Rationale: `list`
   stays a single batched inspection (no per-row `sb.Agent()` handle read), and
   the taxonomy holds — `Environment` = substrate facts; the aggregated Info
   surfaces agent identity because that is its job. `sb.Agent().Type()/Model()`
   remains the targeted per-handle accessor. Populated from `agent.json` in
   `InspectSandbox`/`InspectSandboxWithBackend` (soft: missing → "").

2. **Realm schema bumped v2 → v3 to drive the existing D61 startup gate.**
   Rather than relying solely on the per-sandbox load-balk to surface "needs
   migration", `LibrarySchemaVersion` bumps to 3 with a **no-op**
   `migrateLibraryStep` case (internal/config cannot import store/agentcfg, so
   it does no per-sandbox work). The real per-sandbox relocation runs in
   `System.MigrateDataDir` **after** `MigrateLibrary`, as
   `orchestrator.MigrateAgentConfigs`. The realm bump means `RealmStatus`
   reports the data dir stale → the startup gate prompts `yoloai system migrate`
   up front, instead of the user first hitting a per-sandbox balk. The load-time
   balk (`LoadEnvironment` returns `ErrNeedsMigration` for version<3) stays as
   the data-safety net (it never rewrites-on-read, never drops the agent/model
   keys before relocation).

- **C2 — the cutover (one coherent commit; compiles + `make check` green; old sandboxes balk till migrate):**
  - **Slim** `store.Environment`: drop `AgentType`/`Model` fields; `metaVersion`→**3**.
  - **Balk, don't write-on-read (M2/D61):** `LoadEnvironment` returns `ErrNeedsMigration` when the raw
    `environment.json` still carries `agent`/`model` keys (equivalently version<3). It must check the
    RAW bytes BEFORE unmarshalling into the slimmed struct (the struct no longer has the fields, so
    unmarshal would silently drop them — the data must be read raw first). No file writes in Load.
  - **Migration (raw-JSON, explicit):** a per-sandbox `migrateEnvironmentRecord(sandboxDir)` that:
    read `environment.json` → `map[string]json.RawMessage`; if it has `agent`/`model`: write
    `agent.json` via `agentcfg.Save{AgentType,Model}` (idempotent — skip if agent.json already valid),
    delete the keys, then run the **existing** typed `migrate()` for any v0→v2 in-struct steps, set
    version 3, atomic-write. MUST be data-safe: never delete the keys before agent.json is durably
    written. Wire it into `system migrate` as a new **per-sandbox pass** (iterate `SandboxesDir()`,
    idempotent) — `MigrateDataDir` calls it after `MigrateLibrary`. Surface `ErrNeedsMigration` at the
    CLI boundary with "run `yoloai system migrate`".
  - **Redirect readers** to `agentcfg`/`Engine.LoadAgentConfig` (NO meta-fallback — the balk guarantees
    agent.json exists by read time): `requireAgent`/`resolveAgentArgs`/`BuildAgentCommand` in restart;
    `agentNetworkFloor` in network; `bugreport` display. `create` already has the values (writes both).
  - **Public reshape:** drop `AgentType`/`Model` from the root `Environment` view + `environmentFromStore`;
    consumers (bugreport, any CLI/MCP showing agent/model from `Environment`) move to `sb.Agent().Type()/Model()`.
  - *Compiling order note:* slimming the struct breaks every reader at compile time, so the reader
    redirects + public-view edit land in the SAME commit as the slim.

- **C3 — migration test + BREAKING-CHANGES:** a test that writes a v2 `environment.json` fixture WITH
  agent/model (no agent.json), runs the per-sandbox migration, asserts: `agent.json` now has the right
  type/model, `environment.json` is v3 without the keys, and a second run is a no-op (idempotent). Plus
  a data-loss guard (kill between agent.json-write and key-strip → re-run still recovers). `BREAKING-CHANGES.md`
  entry: existing sandboxes need `yoloai system migrate`; `Environment.AgentType/Model` move to `sb.Agent()`.

## Cross-references

[D97](../../decisions/working-notes.md) (the surface-cleanup that surfaced Q104),
[persistence-helper.md](../persistence-helper.md) (D87 Handle + raw-JSON migration model),
[public-layering.md](public-layering.md) Stage 3b. Decision earns a D-number when M1/M2 settles.
