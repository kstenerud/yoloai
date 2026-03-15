# Bug Report and Structured Logging Design

## Structured Logging

### Log files

Each sandbox maintains three log files in its state directory (`~/.yoloai/sandboxes/<name>/`):

| File | Format | Content |
|------|--------|---------|
| `events.jsonl` | JSONL | yoloai internal events: sandbox lifecycle, mount operations, backend calls, config resolution |
| `monitor.jsonl` | JSONL | Monitor process events: idle detection, agent status polling, health checks |
| `agent.log` | Raw terminal stream | Verbatim output from the agent process (ANSI codes, cursor positioning, etc.) |

Agent output is a raw terminal recording — not loggable alongside structured events. It is treated as a separate artifact, not a log source.

### JSONL schema

Each line in `events.jsonl` and `monitor.jsonl` is a JSON object:

```json
{"ts": "2026-03-15T14:23:01.123Z", "seq": 1, "level": "info", "event": "sandbox.start", "msg": "starting sandbox", "backend": "docker", "sandbox": "x"}
```

| Field | Type | Description |
|-------|------|-------------|
| `ts` | string | RFC3339 timestamp with milliseconds |
| `seq` | int | Monotonic sequence number — ordering guarantee when timestamps collide |
| `level` | string | `debug`, `info`, `warn`, `error` |
| `event` | string | Dot-separated event type: `sandbox.start`, `mount.bind`, `agent.launch`, `backend.exec`, etc. |
| `msg` | string | Human-readable summary |
| *(additional fields)* | | Per-event structured data (e.g. `backend`, `sandbox`, `path`) |

### `--debug` and `--verbose`

| Flag | Effect |
|------|--------|
| `--verbose` | More output printed to the terminal. No effect on log files. |
| `--debug` | Enables `debug`-level entries in `events.jsonl` and `monitor.jsonl`. Silently ignored for non-sandbox commands (no log location to write to). |
| `--bugreport <file>` | Implies `--debug`. Writes debug-level log entries to the report's temp file only — not to the sandbox's JSONL files. The report is the authoritative record for that run. |

---

## `sandbox <name> log` command

The log command is redesigned around the structured log files. Agent output is separate and accessed via dedicated flags.

### Default view

Pretty-printed, interleaved stream of `events.jsonl` and `monitor.jsonl`, ordered by timestamp. Default level: `info+`.

```
14:23:01 [yoloai]  info   sandbox.start    starting sandbox (backend=docker)
14:23:03 [yoloai]  info   mount.bind       bound /home/karl/Projects/foo
14:23:05 [monitor] info   agent.launch     agent process started
14:23:09 [monitor] warn   agent.idle       no output for 30s
```

### Flags

| Flag | Effect |
|------|--------|
| `--source events\|monitor` | Show only one structured source instead of interleaved |
| `--level debug\|info\|warn\|error` | Filter by minimum log level (default: `info`) |
| `--debug` | Shorthand for `--level debug` |
| `--follow` / `-f` | Tail the log live |
| `--since <duration\|timestamp>` | e.g. `--since 5m` or `--since 14:20:00` |
| `--agent` | Show agent output with ANSI stripped — readable text |
| `--agent-raw` | Show raw agent terminal stream (for replay or escape sequence inspection) |
| `--raw` | Emit structured log lines as raw JSONL (no formatting) |

`--follow` applies to all modes including `--agent` and `--agent-raw`.

---

## Two Bug Report Mechanisms

### 1. Global `--bugreport` flag (flight recorder)

```
yoloai --bugreport <file> <command> [args...]
```

Can be used with any yoloai command. When active:

- A temp file (`<file>.tmp`) is opened immediately, before any subcommand logic runs.
- Static sections (header, command invocation, system info, backends, config) are written to the temp file right at launch.
- `--debug` is implicitly enabled: debug-level log entries are written to the temp file only. The sandbox's `events.jsonl` and `monitor.jsonl` are not written during a `--bugreport` run — the report is the authoritative record for that command.
- A deferred finalizer (with `recover()` to catch panics) writes the exit code and any error, then renames `<file>.tmp` → `<file>`.
- For sandbox commands, the existing `events.jsonl` and `monitor.jsonl` are included in the report — prior `--debug` runs will have contributed debug-level entries.
- The report is **always written** regardless of outcome: success, error, panic, or signal. On SIGKILL the temp file survives with whatever was captured up to that point.

Typical workflow for a hard-to-reproduce bug:
```
yoloai --debug new x .              # run leading-up commands with debug logging
yoloai --debug start x              # more debug-logged commands
yoloai --bugreport out.md start x   # captures live debug + prior events.jsonl from sandbox
```

### 2. `sandbox <name> bugreport` command (forensic tool)

```
yoloai sandbox <name> bugreport <file>
```

Used when a bug occurred in a past run and the user still has the sandbox — no need to reproduce the issue. Collects static diagnostic information from system state and the named sandbox, including `events.jsonl` and `monitor.jsonl` which will contain debug-level entries if prior commands were run with `--debug`. Always writes to a file; no stdout mode.

Placed under `sandbox` alongside `sandbox <name> log` and `sandbox <name> info`. Non-sandbox commands don't accumulate persistent state, so there is nothing to perform forensics on without a sandbox.

---

## Output Format

A single GitHub-Flavored Markdown document, structured for direct pasting into a GitHub issue.

GitHub issue bodies are capped at **65,536 characters**. After writing the report, yoloai checks the file size and prints a warning to stderr if this limit is exceeded:

```
Warning: report exceeds GitHub's issue body limit (65,536 characters).
Upload as a Gist instead: gh gist create out.md
```

Verbose sections (system info, backends, config, logs, sandbox detail) are wrapped in `<details>`/`<summary>` collapsible blocks. Only the header, command, and exit status are visible by default; machine-readable content is folded away.

```markdown
## yoloai Bug Report — 2026-03-15T14:23:01Z

**Version:** 0.9.1 (abc1234, 2026-03-10)
**Command:** `yoloai --bugreport out.md new x .`
**Exit code:** 1 — sandbox creation failed

<details>
<summary>System</summary>

...

</details>

<details>
<summary>events.jsonl</summary>

...

</details>
```

Note: `<details>`/`<summary>` is GitHub-Flavored Markdown and may not render correctly in other environments, but the content remains readable as plain text.

---

## Report Sections

Both mechanisms share the same section format. The flag-based report includes additional sections (command invocation, live log) that the forensic command omits.

### 1. Header

- Timestamp (UTC, RFC3339)
- yoloai version, commit, build date

### 2. Command Invocation *(flag only)*

- Full `os.Args` as a fenced code block

### 3. System

- OS and architecture (`GOOS/GOARCH` from compile-time constants)
- Kernel string (`uname -a` on Linux/macOS; omitted on Windows)
- Relevant environment variables: `DOCKER_HOST`, `CONTAINER_HOST`, `XDG_RUNTIME_DIR`, `YOLOAI_SANDBOX`, `HOME`, `TMUX` (only those that are set)
- yoloai data directory path and disk usage

### 4. Backends

For each known backend (docker, podman, tart, seatbelt):

- Availability status
- Version string from backend CLI if available:
  - docker: `docker version --format 'Client: {{.Client.Version}} / Server: {{.Server.Version}}'`
  - podman: `podman version --format '{{.Client.Version}}'`
  - tart: `tart --version`
  - seatbelt: "built-in"

In `--bugreport` mode, all probes run regardless of known availability — a failed probe output is useful diagnostic information. In `sandbox <name> bugreport`, probes are skipped for backends already known unavailable.

### 5. Configuration

Global config (`~/.yoloai/config.yaml`) and active profile config, each in a fenced YAML code block, sanitized (see Sanitization below). Missing files noted as "(not found)".

### 6. Sandbox Detail *(sandbox commands only)*

**Summary** — status, agent, model, backend, created, disk usage, has-changes.

**`environment.json`** — full contents (no API keys stored here).

**`agent-status.json`** — full contents.

**`runtime-config.json`** — full contents. Useful for diagnosing entrypoint and mount configuration issues.

**Container log** — full output from the container runtime:
- docker: `docker logs yoloai-<name> 2>&1`
- podman: `podman logs yoloai-<name> 2>&1`
- tart/seatbelt: noted as "not available for this backend"

Errors from a non-running container are included rather than silently omitted.

### 7. `events.jsonl` *(sandbox commands only)*

Full contents of the sandbox's `events.jsonl`. Contains debug-level entries if prior commands were run with `--debug`.

### 8. `monitor.jsonl` *(sandbox commands only)*

Full contents of the sandbox's `monitor.jsonl`.

### 9. Agent output *(sandbox commands only)*

ANSI-stripped contents of `agent.log`. The raw terminal stream is not included — it is not meaningful outside a terminal renderer.

### 10. Live log *(flag only)*

All debug-level log entries captured during the run, written incrementally to the temp file as the command progresses.

### 11. Exit *(flag only)*

- Exit code
- Error message (if any)
- Whether the process exited via panic (caught by `recover()`)

---

## Sanitization

Config file values are redacted for keys whose names (case-insensitive) contain any of: `key`, `token`, `secret`, `password`, `credential`, `passwd`.

```yaml
# input
anthropic_api_key: sk-ant-abc123
# output
anthropic_api_key: [REDACTED]
```

Line-by-line on raw YAML text (no parser dependency). Handles indented keys (e.g. inside `env:` maps).

---

## Deliberate Omissions

| Item | Reason omitted |
|------|----------------|
| `prompt.txt` | May contain sensitive task descriptions |
| Full environment dump | Too noisy; may expose unrelated secrets |
| Credential/key files | Never appropriate for a bug report |
| `home-seed/` directory | Large; agent-internal; not useful for diagnosis |
| Raw `agent.log` | Terminal stream; not meaningful outside a renderer |

---

## Implementation Plan

### Structured logging (prerequisite)

1. Replace existing `log.txt` / `monitor.log` with `events.jsonl` and `monitor.jsonl` in the sandbox state directory.
2. Update all internal logging calls to emit structured JSONL entries with `ts`, `seq`, `level`, `event`, `msg` fields.
3. Update `sandbox <name> log` to the new design (see above).
4. Agent output capture continues writing to `agent.log` unchanged — it is a terminal recording, not a structured log.

### Flag (`--bugreport`)

1. Add `--bugreport <file>` as a persistent flag on the root Cobra command in `internal/cli/root.go`.
2. In `PersistentPreRunE`, if the flag is set:
   - Open `<file>.tmp` immediately.
   - Write static sections (header, command invocation, system, backends, config).
   - Install a debug log handler that appends to the temp file.
   - Register a `defer` with `recover()` to write exit/error info and rename temp → target.
   - After rename, check file size and warn to stderr if > 65,536 characters.
3. New file: `internal/cli/bugreport_writer.go` — shared section-writing logic.

### Command (`sandbox <name> bugreport`)

1. New file: `internal/cli/sandbox_bugreport.go`
2. Wire into `newSandboxNameCmd()` in `internal/cli/sandbox.go`
3. Pass `version`, `commit`, `date` strings into the constructor (same pattern as `newSystemInfoCmd`)
4. No new external dependencies

### Key functions

| Function | Purpose |
|----------|---------|
| `newSandboxBugReportCmd(version, commit, date)` | Cobra command constructor |
| `writeBugReportHeader(w, version, commit, date)` | Section 1 |
| `writeBugReportCommandInvocation(w)` | Section 2 (flag only) |
| `writeBugReportSystem(w)` | Section 3 |
| `writeBugReportBackends(ctx, w)` | Section 4 |
| `writeBugReportConfig(w)` | Section 5 |
| `writeBugReportSandboxDetail(ctx, w, rt, name)` | Section 6 |
| `writeBugReportEventsLog(w, name)` | Section 7 |
| `writeBugReportMonitorLog(w, name)` | Section 8 |
| `writeBugReportAgentOutput(w, name)` | Section 9 |
| `backendVersion(backend)` | Returns version string from backend CLI, or "" |
| `sanitizeYAMLConfig(content)` | Redacts sensitive key values |
