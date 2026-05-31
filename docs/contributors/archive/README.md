<!-- ABOUTME: Index of archived yoloAI dev docs — completed/superseded plans, research, -->
<!-- ABOUTME: investigations, and design specs kept for history, not as live references. -->

# Archive

Historical documents kept for the record. Nothing here is a live reference: these are
**completed**, **superseded**, or **point-in-time** docs. Live docs may still cite them as
history (links rewritten to point here); new work should not depend on them.

This is a tentative first cut — the broader greenfield doc reorg may move more in later.

## Layout

| Subdir | What's here |
| --- | --- |
| `plans/` | Completed or superseded implementation plans. |
| `research/` | Completed/superseded research (mostly the layering epic's spikes). |
| `investigations/` | Point-in-time investigations and audits (a snapshot, not a living doc). |
| `design/` | Superseded design specs. |
| `old/` | The original pre-archive `old/` pile (initial PLAN + phase notes + early flag/devcontainer designs). |

## Contents

### plans/
- The Layer-1 public-API epic: `f1-f3-public-surface`, `f2-f1f3-implementation`, `f2-subhandle-mapping`, `layer1-public-api`. (Live successor: `../plans/layer1-completion.md`.)
- The layering / separation-of-concerns refactor: `layering-refactor`, `soc-refactor`.
- Shipped feature plans: `config-revamp`, `capability-registry`, `mcp-server`, `bugreport`, `podman-backend`.
- The architecture-remediation program: `architecture-remediation` (its one release-gated remnant, W1b, lives in `../plans/release-migration.md`).
- Other completed: `cli-critique-deferred`, `critique-followup` (the 31-finding critique tracker), `vm-isolation-debug`, `smoke-test-redesign`.

### research/
- Layering-epic spikes: `layering-cli-surface`, `layering-comparators`, `layering-leak-audit`, `layering-open-questions`, `mcp-sdk-evaluation`.

### investigations/
- `architecture-audit-2026-05` (the audit the remediation plan addressed), `macos-disk-reporting-checklist`, `tart-regression`, `ios-testing-investigation`, `ios-testing-status`.

### design/
- `layering`, `layering-greenfield` (superseded layering design specs).

### old/
- `PLAN`, `devcontainer`, `json-flag`, and `phases/` (PHASE_0 … PHASE_8).
