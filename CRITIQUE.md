# Critique — Round 4

Fourth design-vs-research audit. Focused on completeness gaps, command consistency, and unspecified behaviors.

## Applied

- **C29.** Resolved Design Decisions numbering — swapped items 8 and 9 to restore sequential order.
- **C30.** `yoloai init` missing from commands table — added to Commands section.
- **C31.** Proxy sidecar in `stop`/`start` — added proxy sidecar handling to both commands (`restart` inherits via stop+start).
- **C32.** `--network-allow` without `--network-isolated` — specified that `--network-allow` implies `--network-isolated` (both CLI and config). Added mutual exclusivity with `--network-none`.
- **C33.** `yoloai build` proxy image — added proxy image (`yoloai-proxy`) to build section; simplified duplicate description in proxy sidecar lifecycle to cross-reference.
- **C34.** `log.txt` and `prompt.txt` container access — added explicit bind-mounts at `/yoloai/log.txt` and `/yoloai/prompt.txt` to Container Startup. Updated `/yoloai/` description and tmux/prompt references to use the bind-mounted paths.
- **C35.** `--network-none` mutual exclusivity — added "Mutually exclusive with `--network-isolated` and `--network-allow`" to `--network-none` description.
- **C36.** `auto_commit_interval` delivery — specified `YOLOAI_AUTO_COMMIT_INTERVAL` environment variable as the delivery mechanism.
- **C37.** RESEARCH.md Docker Sandbox version discrepancy — clarified that 4.50+ is sandboxes GA, 4.58+ is network policy features.

## Deferred

(none)
