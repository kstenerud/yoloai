<!-- ABOUTME: Mid-workstream discoveries that were not in the original audit. Critical -->
<!-- ABOUTME: findings escalate; everything else parks here until the next re-audit. -->

# Discovered Findings

Findings that turned up mid-workstream (architecture-remediation, layering-refactor, or any future plan) and were **not** in the originating audit. Per the discovered-findings policy:

- **Critical findings escalate immediately, do not park.** Critical = observable data loss, security issues, observable regressions in shipped behavior, or anything that would block the current release.
- **Everything else parks here** until the next re-audit checkpoint. Don't expand a workstream's scope to absorb new findings.
- The discoverer makes the severity call; when uncertain, escalate.

## Entry format

```
### DF<N> — <one-line title>

- **Discovered:** <YYYY-MM-DD> · **Workstream:** <W-L1 / W7 / etc>
- **Severity:** CRITICAL / MEDIUM / LOW
- **Disposition:** ESCALATED / PARKED / ADDRESSED-IN-PLACE
- **Description:** <2-4 sentences>
- **Pointer:** <file:line or commit hash>
```

## Findings

### DF12 — Tag pipeline runs host git on the work copy, not the backend-aware exec (Tart-incorrect for VM work copies)

- **Discovered:** 2026-05-31 · **Workstream:** W-L1 (G7 apply carve)
- **Severity:** LOW
- **Disposition:** PARKED
- **Description:** The entire git-tag read/transfer pipeline (`internal/sandbox/tags.go`: `ListTagsBeyondBaseline`, `ListUnappliedTags`, `GetTagMessage`; `internal/workspace/tags.go`: `BuildSHAMapByMatching`, `CreateTag`, `getCommitMeta`) shells out via `workspace.NewGitCmd` directly against the sandbox work-copy path on the host, rather than the backend-aware `runtime.GitExecFor`. For Docker/Seatbelt the work copy is a real host directory, so this is correct. For Tart the work copy lives inside the VM, so tag discovery/matching against that path reads the wrong (or empty) repo. This is a **pre-existing, pipeline-wide** gap surfaced (not introduced) while relocating tag transfer into the public `Workdir().TransferTags` verb — the new verb preserves the existing host-git behavior exactly. Not half-fixed in the carve to avoid an inconsistent pipeline; should be addressed wholesale when Tart work-copy support is hardened.
- **Pointer:** `internal/sandbox/tags.go`, `internal/workspace/tags.go`, `internal/sandbox/transfer_tags.go` (doc comment notes the gap)

## Policy origin

Established in [architecture-remediation.md](../archive/plans/architecture-remediation.md) and inherited by [layering-refactor.md](../archive/plans/layering-refactor.md).
