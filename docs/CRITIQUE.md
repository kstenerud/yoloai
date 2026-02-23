# Critique — Round 7

Post-PLAN.md refresh audit. Focus: contradictions between documents, stale references, and gaps that would block or confuse an implementor.

## Applied

- **C71.** DESIGN.md non-root user section — replaced `$HOST_UID`/`$HOST_GID` env vars with config.json/jq reference.
- **C72.** DESIGN.md `auto_commit_interval` — replaced `YOLOAI_AUTO_COMMIT_INTERVAL` env var reference with `config.json`.
- **C73.** DESIGN.md `baseline_sha` — changed from "null for non-git dirs" to "always present" (synthetic commit SHA stored). Simplifies diff logic.
- **C74.** OPEN_QUESTIONS #5 — replaced `YOLOAI_STARTUP_DELAY` env var with `config.json startup_delay`.
- **C75.** DESIGN.md `yoloai destroy` — removed duplicate "Smart confirmation" paragraph.
- **C76.** CODING-STANDARD.md — marked Viper as post-MVP dep, updated core deps list to Cobra + Docker SDK only.
- **C77.** CODING-STANDARD.md — fixed godoc example receiver from `m` to `manager`.
- **C78.** DESIGN.md directory layout — added `config.json` to sandbox state directory listing.
- **C79.** DESIGN.md `:force` on workdir — `:force` no longer suppresses the default `:copy` mode. `./my-app:force` means `./my-app:copy:force`.
- **C80.** `yoloai tail` — removed from MVP. Users pipe `yoloai log` to `tail` or use `tail -f` on the log file directly (path from `yoloai show`). Moved to deferred.
- **C81.** DESIGN.md `yoloai diff` — removed redundant `git status` (everything is captured by `git add -A` + `git diff`).

## Deferred

(none)
