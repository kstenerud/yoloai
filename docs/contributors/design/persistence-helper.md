# Persistence helper — scoped versioned handles over per-domain single docs

**Status:** Design converged 2026-06-15 (design conversation), not yet implemented. Foundation
persistence for the [public-layering](plans/public-layering.md) program — used by the **library** and
by **tools** (CLI, MCP) alike. Generalizes [D85](../decisions/working-notes.md) ("each layer persists
its own facts"). Decision: D87. Backed by
[research/shared-state-concurrency.md](research/shared-state-concurrency.md).

**One-line definition.** A component is handed a **scoped, versioned persistence handle** — its
private slice of storage — and reads/writes through it, blind to everything else, *including its own
physical location*. The handle is the membrane that makes the physical representation swappable and
keeps migrations from rippling.

## The model

1. **Config follows the *instance tree*, not the component type.** Config is scoped to *this
   component, in this position in the composition* — there is no global "component A config." yoloAI
   has this concretely: a sandbox tracks **multiple dirs, each with its own copyflow state**, so
   copyflow is instanced per-dir (N copyflow slices per sandbox). The handle tree mirrors the instance
   tree; `Sub(name)` derives a child scope to hand to a child component.

2. **The handle is the membrane — it decouples the component from the physical layout *and* prevents
   migration ripple.** A component never knows whether its slice is a separate file or a section of a
   big one. So (a) we can change the physical representation without touching any component, and (b)
   the two kinds of migration never reach a component that doesn't own them: a **content** change
   stays in the component; a **structural** (layout) change stays in the root, behind the handle. The
   component only ever sees its own content version.

3. **Single doc per ownership *domain*.** Components that ship in one binary and version/migrate
   *together* share one document; domains that version *independently* must be separate files
   (you cannot transactionally co-migrate schemas owned by different binaries on different cadences).
   - The **library** is one domain: substrate + copyflow + agent are all library components → **one
     library doc per sandbox** (`~/.yoloai/library/sandboxes/<name>/…`), migrated by
     `yoloai system migrate`.
   - Each **tool** (CLI, MCP, a future daemon) is its own domain → its own doc(s) in its own subtree,
     independently owned, versioned, and migrated. This is the existing cli/library bifurcation (D60).
   - Granularity is therefore **one doc per (domain × sandbox)** — "single doc per sandbox" was always
     "per sandbox *per domain*."

4. **Migration: one monotonic version + an append-only ordered registry of raw-JSON steps.** A
   multi-version span (v2→v7) must **replay steps in their true historical order**, because a
   structural/redistribution step is frozen against the exact prior shape — so independent per-tier
   counters can't express the order; they collapse to **one doc version** with a registered, ordered
   step list (DB-migrations style; `system migrate` replays `current+1 … latest`). Authorship stays
   local (each step touches its own section, authored by its owner) but **sequencing is global**. The
   two "tiers" survive only as two *kinds* of step — a cross-section **redistribution** (owned by the
   root; the *only* thing that sees across components; runs first because it must *place* data before
   components reshape it) vs a within-section **content** change. Hard rules:
   - **No auto-migration; balk and fail fast** — `Load` *checks* the version and refuses; only the
     explicit `yoloai system migrate` advances (the established philosophy — D61). This prevents the
     silent ratchet (a newer binary quietly upgrading data so the user's older binary then refuses it).
   - **Steps are immutable once shipped** — append-only; the binary carries every historical step; a
     floor/squash policy relieves accretion when wanted.
   - **Migration runs on *raw* JSON, not typed structs** — intermediate shapes have no Go types; only
     the head (current) version is typed. (This resolves the typed-vs-untyped question: typed `Load`
     at the head, raw-JSON for the chain.)
   - **The chain is doc-only and therefore transactional for free** — replay in memory, write once
     atomically; a crash leaves the pristine prior version, re-run from scratch. Any sandbox
     *filesystem* restructuring is a *separate* out-of-band migration with its own crash-safety, never
     smuggled into the config chain.

5. **The version field is sacred.** Fixed top-level location, **plain int**, never relocated by any
   migration — it's the bootstrap every binary (old or new) reads *first*, without understanding the
   rest, to decide operate / balk / reject. The version is the **sole compatibility arbiter**: exact
   match → operate; `< current` → balk (needs migration); `> current` → reject (`ErrTooNew`, the
   ratchet protection); absent → fresh; unreadable → hard error. **Therefore every schema change bumps
   it, including additive fields** — because Go's `json.Unmarshal` silently drops unknown fields, so a
   lagging binary that round-tripped a newer doc would silently *lose* the new fields; rejecting on
   "newer" is the only defense.

6. **Concurrency: `flock` + atomic-rename; mutate only through `Update`.** `flock` (not `fcntl` — the
   close-any-fd-drops-the-lock footgun) self-cleans on crash. Atomic-rename (`fsync(temp) → rename →
   fsync(dir)`) makes **`Load` lock-free** (never a torn read). Mutation **must** go through
   **`Update`** (acquire lock → re-read fresh under the lock → apply → write → release), never
   `Load`-then-`Save` (lost-update race). `Update`'s fresh re-read is where both checks live: the
   caller's CAS condition (copyflow's "baseline still == X?") *and* a **version re-check** (a
   concurrent `system migrate` changed the world → abort). `system migrate` takes the same per-sandbox
   lock, so it serializes against `Update`.

7. **Library vs tool ownership — the single-source-of-truth boundary.** The library doc is the single
   source of truth all tools must agree on; tools own private views/caches/UX/bookkeeping. Two tests:
   - **(1) Would another tool need to agree on this value to operate correctly on the sandbox?** Yes →
     **library** (substrate config, copyflow baselines — cli `diff` and mcp `apply` *must* agree —,
     **agent config** [`agent.json`], the resolved prompt). Only-meaningful-to-one-tool → **tool** (cli's
     last-command/list-view state, mcp's client-session mapping).
   - **The runtime status sidecar (`agent-status.json`) is *outside* this Handle/`flock`+`Update` contract**
     *(clarified 2026-06-24, D92)*. It is written **in-container by the monitor** (which cannot take the host
     `flock`), single-writer, and **host-polled** — a different concurrency class than the versioned,
     host-authored library docs. Two distinct "sidecars" not to conflate: **`agent.json`** = versioned agent
     *config*, host-authored, under the Handle; **`agent-status.json`** = turn-cursor + active/idle *status*,
     monitor-authored in-container, read-only to the host, *not* version-guarded or `flock`-mediated. The
     Handle governs library config docs; the status file is a lock-free single-producer/single-consumer channel.
   - **(2) Does it need *atomic coupling* with the sandbox's reality?** Yes → it **is** library state
     (promote it). Because file locks give no cross-domain transaction, **tool state must be
     independently recoverable from library state, never atomically co-updated** — a tool that crashes
     mid-op re-derives or re-records. Derived caches (cli's cached diff) are tool-owned (recomputable
     from the library truth).
   - This boundary is also the **concurrency-soundness boundary**: contention concentrates on the one
     library doc (low, serialized) and tool writes never contend. Misclassifying tool state into the
     library doc manufactures spurious cross-tool contention; misclassifying shared truth into a tool
     desyncs the tools.

8. **Notification is reactivity, not correctness.** Locks give correctness (we have it); the thing
   file locks can't do is *notify*. But correctness never needs it: stale-cache is caught lazily by
   the version re-check on `Update`; and our dominant change-source (agent status) is *already*
   file-polling by container-boundary necessity (the in-container monitor writes files, the host
   reads — no daemon changes that). Proactive reactivity is served by polling, or optimized via
   **`inotify` on the atomic-rename** (the filesystem notifies even though the lock can't), locally —
   no library API, no daemon. A daemon earns its place only for **low-latency push at scale**, cleanly
   deferred by library-first.

9. **Cross-tool version coupling.** Tool-domain versions evolve freely and independently; but all
   tools sharing a data dir are **coupled on the *library* doc's one version** — a newer tool that
   migrates it makes older tools balk (`ErrTooNew`, safe-not-corrupt). So tool versions may differ,
   but *embedded library versions* must stay compatible, and a library migration is a coordinated,
   conscious act. The price of a shared source of truth, paid in the safe currency.

## Surface sketch (shape, not final signatures)

```go
// A scoped, versioned slice. A component sees ONLY this.
type Handle interface {
    Load(v Record) (found bool, err error)        // check version: ==cur→fill; <cur→ErrNeedsMigration; >cur→ErrTooNew; absent→found=false. Lock-free, never migrates.
    Save(v Record) error                          // initial write (create)
    Update(v Record, mutate func() error) error   // lock → re-read fresh (version + CAS re-checked) → mutate → atomic write → unlock
    Migrate(v Record) error                        // EXPLICIT, system-migrate only: replay raw-JSON steps current+1..latest, write forward
    Sub(name string) Handle                        // a named child scope → handed to a child component
}

type Record interface {
    SchemaVersion() int                            // what THIS binary writes (the sacred plain-int, fixed top-level)
    // migration steps are registered append-only and run on raw JSON, not on this typed struct
}

// Root: maps the logical tree → physical storage (one doc per domain), owns layout/redistribution steps.
OpenDomain(dir string /* narrowed paths, per Q105 */) (Handle, error)   // balks if out of date; never auto-migrates
```

`store`'s existing per-sandbox `flock` (`AcquireLock`) is already the shared lock this rides on; the
helper is largely a **reframe of `store`**: the generic atomic-sudo-safe IO + the version guard + the
filename/domain registry become the shared mechanism, and the substrate's `Environment`/`SandboxState`
become *its* records (moving out of a substrate-private package).

## Deliberately NOT in the helper

- **Auto-migration** (balk + explicit `system migrate` only).
- **Multi-file atomic transactions** (single doc per domain → never needed within a domain; domains
  are independent → never needed across them).
- **A notification/watch API** (the atomic-rename *is* the surface; tools poll or `inotify` it).
- **A query language / DB engine** (whole-doc read/write of config-sized data; JSON, not SQLite —
  swappable behind the handle if the envelope is ever left).
- **Cross-domain atomic coupling** (tool state is independently recoverable, never co-updated with
  library state).

## Open items / findings

- **DF36** — detect the data dir's filesystem and **warn/refuse on a network FS** (advisory locking is
  unreliable on NFS/SMB → corruption; SQLite is *worse* there, so this isn't a JSON-vs-SQLite escape).
- **DF37** — file-locking hardening: confirm `store/lock_unix.go` uses **`flock`, not `fcntl`** (the
  silent-corruption footgun), and add the **`fsync(temp) → rename → fsync(dir)`** durability dance to
  atomic writes.
- **Home/name of the helper** — reframe `store` (the shared lock already lives there) vs a new
  `internal/record`/`internal/persist`. Now low-stakes (it's behind the `Handle` interface). Decide at
  Shape time.
- **The `Update`/CAS expected-value plumbing** and the exact `Record` migration-registration mechanism
  are Shape-time details; the model is fixed.
