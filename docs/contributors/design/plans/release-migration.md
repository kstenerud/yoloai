<!-- ABOUTME: Release-gated cross-version concerns for the layering-refactor branch: -->
<!-- ABOUTME: the prerelease sandbox-compat test, the on-disk format gates, and W1b. -->

# Release migration — `layering-refactor` → `main` → release tag

## Why this doc exists

This branch introduces on-disk and Go↔Python boundary format changes that affect
sandboxes **across yoloai versions**. Nothing here has shipped in a release yet, so
there are currently **zero affected sandboxes in the wild** — the version gates below are
dormant. They go live the moment this branch merges to `main` and gets a release tag.

This is the single place to look up what must be verified or finished **before / at that
release** so nothing is silently missed. The branch may keep changing before merge; keep
this doc current as gates are added or retired.

## 1. Prerelease cross-version verification

The version gates exist so that a sandbox created by one yoloai binary keeps working when
a *different* binary drives it later. Verify that contract before merge by exercising a
real old→new transition (W1b's own precondition is now handled by the §3 migration backfill,
but this test still proves the §2 enforced-file fallbacks against real old-binary sandboxes):

1. Build yoloai from `main` (the last released-shape binary).
2. Create sandboxes on every backend you can test locally (`docker`, and where available
   `tart`, `seatbelt`).
3. Exercise each: launch, detach/attach, restart/respawn, `status`, `diff`/`apply`.
4. Rebuild yoloai from this branch (or the prospective merge commit).
5. With the **new** binary, keep using the **same** sandboxes created by the old binary.
   Verify all of step 3 still works — especially **agent relaunch / respawn** (until W1b
   ships, the launch-prefix fallback path; after, the migration-backfilled prefix) and
   **status monitoring** (`schema_version=0` tolerance).

This proves the backward-compat fallbacks (§2) actually work against real
old-binary sandboxes. Record the outcome here when run:

> _Cross-version test results: (not yet run)_

## 2. Cross-version format / version gates on this branch

| File | Field(s) | Enforced on read? | Cross-version behavior | Code |
|---|---|---|---|---|
| `runtime-config.json` | `agent_launch_prefix`, `use_launch_prefix` (W1a); `schema_version` (W2) | **No** — version written but not validated | Field-level tolerance only: old sandbox missing `agent_launch_prefix` → Python falls back to `prepare_launch_command` (the path W1b deletes, after `system migrate` backfills the field — see §3); unknown fields ignored. `schema_version` is a reserved field for a future loud-fail. | `runtimeconfig.go:17,40,44,45`; `create.go:721,725,726`; `sandbox-setup.py:818-825` |
| `agent-status.json` | `schema_version` (omitempty, W2) | **Yes** | `=0` (pre-W2) tolerated; any other mismatch rejected (loud-fail) | `status.go:182,190,241` |
| `meta.json` | `version` (pre-existing) | **Yes** | `=0` legacy tolerated; `version > 1` (future/newer) rejected (loud-fail) | `meta.go:20,24,79` |

**Downgrade rule (the two enforced files).** For `agent-status.json` and `meta.json`, an
older binary reading files written by a newer binary **hard-fails** with a specific
message rather than risk silent misinterpretation. The practical fix is "use the newer
binary"; re-creating the sandbox loses agent session state. This is deliberate (public
beta → breaking changes are explicit). `runtime-config.json` does **not** participate —
its `schema_version` is written but not yet checked, so a version skew there is silently
tolerated at field level. If runtime-config ever needs a hard incompatibility surfaced,
wiring its `schema_version` into a loud-fail reader is the place to do it.

## 3. Pending removal — W1b (retire the launch-prefix legacy path via migration)

Carried over from `../archive/plans/architecture-remediation.md` (now archived); this is its
only live remnant. W1a stores the backend-specific wrap prefix once at sandbox creation
(`agent_launch_prefix` in `runtime-config.json`) as the single source of truth; W1b removes
the old per-backend Python fallback (`prepare_launch_command`) that reconstructs the prefix
at runtime for sandboxes created before W1a.

**This is now a schema migration, not a hard cutover.** The earlier framing — "delete the
fallback and accept that in-flight pre-W1a sandboxes need re-creation" — is superseded. The
launch prefix is a *per-backend constant*, fully derivable host-side from the sandbox's
stored backend type alone (Tart → a `PATH=…` prepend; Seatbelt → `source ~/.swift-wrapper.sh
&& `; docker/podman/containerd → `""`). The Python fallback never read anything we lack
host-side — it was pure recomputation. So `system migrate` can backfill `agent_launch_prefix`
into any sandbox missing it, after which the fallback is dead code that strands nothing.

The startup gate (`internal/cli/gate.go`) already forces `system migrate` before the new
binary will drive a stale data dir, so the backfill is *guaranteed* to run before any launch
can reach the deleted fallback. The old precondition (cross-version test + accept re-creation,
or wait one release) is therefore satisfied structurally — W1b no longer needs a release
go/no-go and can ship behind the migration.

**Blast radius:** docker/podman/containerd sandboxes need no backfill — their correct prefix
is `""`, which prepends to a no-op even unconditionally. Only Tart and Seatbelt sandboxes get
a string written. In practice the migration only touches macOS sandboxes.

**Steps:**
1. Schema bump (`internal/config/schema.go`): `LibrarySchemaVersion` 1 → 2; add a `case 1:`
   (v1→v2) transform to `migrateLibraryStep`. Migration is **stepwise** — `MigrateLibrary`
   loops one step per version (v0→v1→v2), so this case only ever handles v1→v2; a v0 dir
   reaches it through the existing no-op v0→v1 stamp.
   - **Recompute, don't backfill-if-empty.** The step walks `layout.SandboxesDir()`, reads
     each sandbox's backend type from `environment.json` (`"backend"`), and writes the
     matching constant prefix into `runtime-config.json` **unconditionally, for every
     sandbox**. Do *not* gate on an empty `agent_launch_prefix`: empty is also the correct
     value for container backends, so "never set" and "correctly empty" are
     indistinguishable. Because the prefix is a deterministic per-backend constant,
     overwriting is idempotent — v1-origin sandboxes that already carry the field (created by
     a W1a-capable build) get the identical value rewritten, a true no-op; v0-origin
     sandboxes get it filled. This is what accounts for **v1 users who already have the
     `agent_launch_prefix` fix.**
   - **Layering / host-independence.** `migrateLibraryStep` lives in `internal/config`, which
     must not import `internal/runtime`; inject the backend→prefix resolver from the migrate
     command (`internal/cli/system/migrate.go`, which already has runtime access). The
     resolver must be a pure `BackendType → prefix` lookup sourced from the backend
     descriptor, **not** a constructed `Runtime` — a Linux host must be able to migrate
     Tart/Seatbelt sandboxes without probing for the `tart` binary.
   - **Not the per-sandbox version.** This is the *realm* `.schema-version` counter that
     `system migrate` drives. It is separate from the per-sandbox `meta.Version` in
     `environment.json` (which does its own read-time v0→v1 backfill in `environment.go`).
     W1b belongs in the realm step, not the read-time path — don't conflate them.
2. `sandbox-setup.py`: collapse the launch branch (lines ~818-825) to the single
   prefix path; delete the four `prepare_launch_command` defs (ABC + Docker/Tart/Seatbelt
   at ~189/270/537/675) and the abstract method.
3. `restart.go:336-337` (the `if cfg.UseLaunchPrefix` guard): make the `AgentLaunchPrefix`
   prepend unconditional — post-migration every sandbox carries the field (empty for
   container backends, which is a no-op prepend).
4. `create.go:726` + `runtimeconfig.go:45`: drop the `UseLaunchPrefix` bool (keep
   `AgentLaunchPrefix`).
5. `backend-idiosyncrasies.md`: reword the two entries (Tart node@24, Seatbelt
   swift-wrapper) from "yoloAI failure mode" to "environmental fact + how yoloAI handles
   it via the stored final command." The environmental facts (Cirrus base image's
   `.zprofile`; macOS `sandbox-exec` doesn't nest) outlive W1 — only the failure mode goes.

**Acceptance:**
- `system migrate` brings a v1 data dir to v2, writing `agent_launch_prefix` into any
  Tart/Seatbelt sandbox that lacked it; container-backend sandboxes are left unchanged.
- `grep -r "prepare_launch_command" internal/runtime/monitor/` returns no production code.
- `restart.go` no longer references `use_launch_prefix` / `UseLaunchPrefix`.
- The two `backend-idiosyncrasies.md` entries describe the environmental fact + current
  handling, not a yoloAI failure mode.
- `make check` green.

## 4. Release checklist

- [ ] §1 prerelease cross-version test run and results recorded.
- [ ] `docs/BREAKING-CHANGES.md` covers this branch's public-API reshape (layer-1) and the
      `agent_files` inner json-tag change.
- [ ] W1b shipped as the v1→v2 migration step (§3): `LibrarySchemaVersion` bumped,
      `agent_launch_prefix` backfill verified, Python fallback deleted.
- [ ] Any new on-disk/boundary field added since this doc was written is added to the §2
      table with its compat behavior.
