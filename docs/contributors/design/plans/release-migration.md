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
real old→new transition (this is the test that actually retires the W1b precondition —
not a calendar gap, since nothing has been released):

1. Build yoloai from `main` (the last released-shape binary).
2. Create sandboxes on every backend you can test locally (`docker`, and where available
   `tart`, `seatbelt`).
3. Exercise each: launch, detach/attach, restart/respawn, `status`, `diff`/`apply`.
4. Rebuild yoloai from this branch (or the prospective merge commit).
5. With the **new** binary, keep using the **same** sandboxes created by the old binary.
   Verify all of step 3 still works — especially **agent relaunch / respawn** (the
   launch-prefix fallback path) and **status monitoring** (`schema_version=0` tolerance).

This proves the backward-compat fallbacks (§2) actually work against real
old-binary sandboxes. Record the outcome here when run:

> _Cross-version test results: (not yet run)_

## 2. Cross-version format / version gates on this branch

| File | Field(s) | Enforced on read? | Cross-version behavior | Code |
|---|---|---|---|---|
| `runtime-config.json` | `agent_launch_prefix`, `use_launch_prefix` (W1a); `schema_version` (W2) | **No** — version written but not validated | Field-level tolerance only: old sandbox missing `agent_launch_prefix` → Python falls back to `prepare_launch_command` (the path W1b deletes); unknown fields ignored. `schema_version` is a reserved field for a future loud-fail. | `runtimeconfig.go:17,40,44,45`; `create.go:721,725,726`; `sandbox-setup.py:818-825` |
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

## 3. Pending removal — W1b (retire the launch-prefix legacy path)

Carried over from `../archive/plans/architecture-remediation.md` (now archived); this is its
only live remnant. W1a stores the backend-specific wrap prefix once at sandbox creation as
the single source of truth; W1b removes the old per-backend Python fallback that served
sandboxes created before W1a.

**Precondition.** Either (a) the §1 cross-version test passes *and* we accept that any
in-flight pre-change sandbox would need re-creation after the fallback is gone, or
(b) one release after this branch ships, with a regression-surfacing gap. On an unreleased
branch (a) is the operative gate — the calendar gap in the original plan assumed W1a had
shipped.

**Steps:**
1. `sandbox-setup.py`: collapse the launch branch (lines ~818-825) to the single
   prefix path; delete the four `prepare_launch_command` defs (ABC + Docker/Tart/Seatbelt
   at ~189/270/537/675) and the abstract method.
2. `lifecycle.go:1095-1096`: make the `AgentLaunchPrefix` prepend unconditional (drop the
   `if cfg.UseLaunchPrefix` guard).
3. `create.go:726` + `runtimeconfig.go:45`: drop the `UseLaunchPrefix` bool (keep
   `AgentLaunchPrefix`).
4. `backend-idiosyncrasies.md`: reword the two entries (Tart node@24, Seatbelt
   swift-wrapper) from "yoloAI failure mode" to "environmental fact + how yoloAI handles
   it via the stored final command." The environmental facts (Cirrus base image's
   `.zprofile`; macOS `sandbox-exec` doesn't nest) outlive W1 — only the failure mode goes.

**Acceptance:**
- `grep -r "prepare_launch_command" internal/runtime/monitor/` returns no production code.
- `lifecycle.go` no longer references `use_launch_prefix` / `UseLaunchPrefix`.
- The two `backend-idiosyncrasies.md` entries describe the environmental fact + current
  handling, not a yoloAI failure mode.
- `make check` green.

## 4. Release checklist

- [ ] §1 prerelease cross-version test run and results recorded.
- [ ] `docs/BREAKING-CHANGES.md` covers this branch's public-API reshape (layer-1) and the
      `agent_files` inner json-tag change.
- [ ] W1b decision made: remove the fallback now (post-§1) or keep it one release.
- [ ] Any new on-disk/boundary field added since this doc was written is added to the §2
      table with its compat behavior.
