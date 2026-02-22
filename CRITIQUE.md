# Critique

Design-vs-research audit. Checked every claim in DESIGN.md against RESEARCH.md for backing evidence.

## Applied

- **C1.** "Fence" phantom reference — removed from DESIGN.md. Could not be verified.
- **C2.** `--append-system-prompt` — VERIFIED as a real Claude CLI flag. No fix needed.
- **C3.** `/work` directory collision claims — ALL VERIFIED (Google Cloud Build, Kaniko, Tekton use `/workspace`; VS Code Dev Containers and GitHub Codespaces use `/workspaces`; `/work` is not in the FHS and not used by any major tool). No fix needed.
- **C4.** `--storage-opt size=` — confirmed broken on macOS Docker Desktop (requires overlay2 + XFS + pquota). Commented out in DESIGN.md config with limitation documented.
- **C5.** Port forwarding — added `--port` CLI flag and `ports` config option to DESIGN.md.
- **C6.** Network isolation — rewrote to use internal Docker network + proxy sidecar + iptables + DNS control, based on new RESEARCH.md "Network Isolation Research" section.
- **C7.** Credential management — rewrote to use file-based injection via bind mount to `/run/secrets/`, based on new RESEARCH.md "Credential Management for Docker Containers" section.
- **C9.** Multi-agent scope — added explicit v1 scope note: Claude Code only, multi-agent deferred to v2.
- **C10.** Resource defaults — added rationale comment (Claude CLI ~2GB RSS + build tooling).
- **C11.** Docker-in-LXC — added `keyctl=1` requirement for unprivileged containers, noted runc 1.3.x issues.
- **C12.** UID/GID matching — specified mechanism: `usermod`/`groupmod` in entrypoint, handle exit code 12, `gosu` for privilege drop, `tini` as PID 1.
- **C13.** Overlay detection — specified: check `/proc/filesystems`, check `CAP_SYS_ADMIN` via `CapEff`, test mount, cache result. Follows containers/storage pattern.

## Deferred

- **C8.** Path consistency — design mounts at `/work/<dirname>/` which contradicts the Docker Sandbox research lesson ("Path consistency matters — mount at same paths to avoid confusion"). Trade-offs need discussion before resolving. **NEEDS DISCUSSION.**
