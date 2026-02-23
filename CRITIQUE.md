# Critique — Round 6

Implementor's perspective audit. Could an implementor build this system without requiring clarification?

## Applied

- **C49.** `meta.json` schema — added full schema with field notes to DESIGN.md. No status field (queried live from Docker). Stores resolved creation-time config for sandbox lifecycle.
- **C50.** Overlayfs workdir parameter — specified `upper/` and `work/` subdirectories under `work/<encoded-path>/` for overlayfs `upperdir` and `workdir` mount options.
- **C51.** `work/<dirname>/` collision risk — resolved with caret encoding of absolute host paths (e.g., `/home/user/my-app` → `^2Fhome^2Fuser^2Fmy-app`). Fully reversible, no collisions.
- **C52.** Git baseline timing — overlay: entrypoint runs git init after mounting overlayfs. Full copy: host-side yoloai runs git init before container start.
- **C53.** Context file timing — generated on host, bind-mounted read-only at `/yoloai/context.md` (same pattern as log.txt and prompt.txt).
- **C54.** `--resume` without `prompt.txt` — error.
- **C56.** Base image distro — Debian slim. Smaller than Ubuntu, avoids Alpine musl incompatibilities with Node.js/npm.
- **C57.** `copy_strategy: overlay` when unavailable — error (don't silently degrade an explicit choice).
- **C58.** `~` expansion scope — all path-valued fields in both config files. Bare relative paths are an error everywhere.
- **C59.** Agent field in merge rules — added. Profile overrides default, CLI `--agent` overrides both.
- **C60.** Custom mount for profile workdir — added optional `mount` field to workdir in profile.yaml.
- **C61.** Resume prompt delivery — always interactive mode (user may want to follow up or redirect).
- **C62.** `yoloai apply` execution context — full copy: host-side (reads `work/` directly, no container needed). Overlay: inside container via `docker exec` (merged view requires overlay mount).
- **C63.** `yoloai diff` for `:rw` — requires running container (live bind mount), runs via `docker exec`.
- **C64.** Copy tool — `cp -a` (preserves permissions, symlinks, metadata, including `.git/`).
- **C65.** `yoloai version` — added to command listing.
- **C66.** Duplicate sandbox name — error before creating any state.
- **C67.** Missing API key — error before creating any state.
- **C68.** `~/.yoloai/cache/` — added to Directory Layout.
- **C69.** Startup delay — poll for agent ready indicator with timeout, not fixed sleep.
- **C70.** Debug/verbose mode — added `--verbose` / `-v` global flag and `YOLOAI_VERBOSE=1`.

## Deferred

- **C55.** Proxy implementation — prefer tinyproxy if it supports HTTPS CONNECT with domain allowlist, custom Go binary as fallback. Needs dedicated research.
