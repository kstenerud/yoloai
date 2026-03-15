# Bug Report Command Design

## Goal

Give users a single command that collects diagnostic information for filing a
GitHub issue. The output should be directly pasteable into an issue or passed
to an AI agent for diagnosis — no manual digging through log files or config
directories required.

## Command

```
yoloai system bugreport [sandbox-name] [--output <file>]
```

Placed under the `system` group alongside `system info`. The sandbox name is
optional; without it, only system-level information is collected.

## Output Format

A single Markdown document written to stdout by default. Markdown was chosen
over JSON because:

- Directly pasteable into GitHub issues without conversion
- Human-readable for manual inspection before filing
- Can be fed to an AI agent as-is
- Section headers make it easy to navigate large reports

With `--output <file>` (`-o`), the report is written to a file and a
confirmation is printed to stderr.

## Sections

### 1. Header

- Timestamp (UTC, RFC3339)
- yoloai version, commit, build date

### 2. System

- OS and architecture (`GOOS/GOARCH` from compile-time constants)
- Kernel string (`uname -a` on Linux/macOS; omitted on Windows)
- Relevant environment variables (name + value):
  - `DOCKER_HOST`, `CONTAINER_HOST`, `XDG_RUNTIME_DIR`
  - `YOLOAI_SANDBOX`, `HOME`, `TMUX`
  - Only variables that are actually set are shown
- yoloai data directory path and disk usage

### 3. Backends

For each known backend (docker, podman, tart, seatbelt):

- Availability status
- If available: a short version string obtained by running the backend CLI
  - docker: `docker version --format 'Client: {{.Client.Version}} / Server: {{.Server.Version}}'`
  - podman: `podman version --format '{{.Client.Version}}'`
  - tart: `tart --version`
  - seatbelt: listed as "built-in" (sandbox-exec has no version flag)

### 4. Configuration

Global config (`~/.yoloai/config.yaml`) and active profile config, each in a
fenced YAML code block. Values are sanitized before output (see Sanitization
below).

If a config file does not exist, the section notes it as "(not found)".

### 5. Sandbox List

A brief table of all sandboxes (name + status), produced by the same
`ListSandboxesMultiBackend` logic used by `yoloai ls`. This gives context on
what exists, even when no specific sandbox is being debugged.

### 6. Sandbox Detail (optional)

Only included when a sandbox name is provided. Subsections:

**Summary** — one-line fields: status, agent, model, backend, created,
disk usage, has-changes.

**`environment.json`** — full contents. No API keys are stored in this file
(credentials live in the keychain or environment at runtime), so it can be
included as-is.

**`agent-status.json`** — full contents. Small file; useful for diagnosing
agent exit states.

**Sandbox log** — last 100 lines of `log.txt`, ANSI escape sequences stripped.
Capped to avoid oversized reports.

**Monitor log** — last 50 lines of `monitor.log`, ANSI stripped.

**Container log** — last 50 lines from the container runtime:
- docker: `docker logs --tail 50 yoloai-<name> 2>&1`
- podman: `podman logs --tail 50 yoloai-<name> 2>&1`
- tart/seatbelt: noted as "not available for this backend"

If the container is not running, the log command may fail; the error is
included in the report rather than silently omitted.

## Sanitization

Config file values are redacted for keys whose names (case-insensitive) contain
any of: `key`, `token`, `secret`, `password`, `credential`, `passwd`.

Example input:
```yaml
agent: claude
anthropic_api_key: sk-ant-abc123
backend: docker
```

Example output:
```yaml
agent: claude
anthropic_api_key: [REDACTED]
backend: docker
```

Sanitization is line-by-line on raw YAML text (no YAML parser dependency).
It handles indented keys (e.g., inside `env:` maps).

## Deliberate Omissions

| Item | Reason omitted |
|------|----------------|
| `prompt.txt` | May contain sensitive task descriptions |
| `runtime-config.json` | Too internal; low diagnostic value |
| Full environment dump | Too noisy; may expose unrelated secrets |
| Credential/key files | Never appropriate for a bug report |
| `home-seed/` directory | Large; agent-internal; not useful for diagnosis |

## Implementation Plan

1. New file: `internal/cli/system_bugreport.go`
2. Wire into `newSystemCmd()` in `internal/cli/system.go`
3. Pass `version`, `commit`, `date` strings into the new constructor (same
   pattern as `newSystemInfoCmd`)
4. No new external dependencies — uses existing sandbox package helpers,
   `stripANSI` from `ansi.go`, and `os/exec` for CLI version probes

### Key functions

| Function | Purpose |
|----------|---------|
| `newSystemBugReportCmd(version, commit, date)` | Cobra command constructor |
| `runBugReport(cmd, version, commit, date, name, outputFile)` | Assembles report into a `bytes.Buffer`, writes to stdout or file |
| `writeBugReportHeader(w, version, commit, date)` | Section 1 |
| `writeBugReportSystem(w)` | Section 2 |
| `writeBugReportBackends(ctx, w)` | Section 3 |
| `writeBugReportConfig(w)` | Section 4 |
| `writeBugReportSandboxList(ctx, w)` | Section 5 |
| `writeBugReportSandboxDetail(ctx, w, rt, name)` | Section 6 |
| `backendVersion(backend)` | Returns version string from backend CLI, or "" |
| `sanitizeYAMLConfig(content)` | Redacts sensitive key values |
| `tailLines(content, n)` | Returns last n lines of a string |

## Open Questions

1. **Log line limits** — 100 lines for `log.txt` and 50 for monitor/container
   logs. Are these the right defaults? Should there be a `--log-lines` flag?

2. **Sandbox name argument vs. flag** — Currently positional: `bugreport
   mybox`. Should it be a `--sandbox` flag instead for consistency with other
   commands that use `resolveName`?

3. **Noisy backends** — Should the backend version probe (e.g., `docker
   version`) be skipped if the backend is already known unavailable, to avoid
   an extra failed subprocess call?

4. **`runtime-config.json`** — Listed as omitted above, but it could be useful
   for diagnosing entrypoint/mount configuration issues. Worth adding?

5. **Output file default name** — If the user runs `bugreport --output` without
   a value, should there be a default filename (e.g.,
   `yoloai-bugreport-<timestamp>.md`)? Or keep it strictly required when the
   flag is used?
