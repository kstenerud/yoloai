# Critique — Round 3

Third design-vs-research audit. Focused on internal consistency, formatting, and design principle violations.

## Applied

- **C22.** CLI usage examples — fixed 4 missing spaces (`yoloai newmy-task` → `yoloai new my-task`).
- **C23.** Stale "needs dedicated research" — rewritten to focus on the remaining valid concern (`setup`/`cap_add`/`devices` privilege escalation risks) now that credential and network research is complete.
- **C24.** `mounts` merge behavior — specified as additive (merged with defaults, no deduplication).
- **C25.** Network isolation config equivalents — added `network_isolated` and `network_allow` to config schema (defaults + profiles). CLI flags override/add to config values.
- **C26.** Recipe field documentation — added merge behavior (additive), availability (defaults + profiles), and execution order (defaults first, then profile) for `cap_add`, `devices`, `setup`.
- **C27.** `yoloai destroy` proxy cleanup — added "(and proxy sidecar if `--network-isolated`)" to destroy description.
- **C28.** `--port` option placement — moved above the allowlist table to keep all `--` options grouped together.

## Deferred

(none)
