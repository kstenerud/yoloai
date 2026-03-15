# Bug Report and Structured Logging Design

## Structured Logging

### Log files

Each sandbox maintains log files named by writer, under `~/.yoloai/sandboxes/<name>/logs/`:

| File | Writer | Format | Content |
|------|--------|--------|---------|
| `cli.jsonl` | yoloai CLI (host Go binary) | JSONL | Sandbox lifecycle, mount operations, backend calls, config resolution |
| `sandbox.jsonl` | entrypoint.sh → sandbox-setup.py (sequential, single writer) | JSONL | Container setup, UID remapping, network isolation, overlay mounts, prompt delivery |
| `monitor.jsonl` | status-monitor.py | JSONL | Detector decisions, status transitions, idle/active polling |
| `agent-hooks.jsonl` | Agent hook scripts | JSONL | Hook-reported state changes (idle/active events from Claude Code hooks etc.) |
| `agent.log` | tmux pipe-pane | Raw terminal stream | Verbatim agent process output (ANSI codes, cursor positioning, etc.) |

`agent-status.json` is retained as the IPC mechanism between hook scripts and status-monitor.py — it is not a log file. `agent-hooks.jsonl` is a separate append-only log of hook events for diagnostic purposes.

Agent output is a raw terminal recording — not loggable alongside structured events. It is treated as a separate artifact, not a log source.

Each file has a single writer, so no file locking is required. POSIX append semantics are sufficient for `agent.log`.

### JSONL schema

Each line in the JSONL log files is a JSON object:

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
| `--debug` | Enables `debug`-level entries in the sandbox's JSONL log files. Silently ignored for non-sandbox commands (no log location to write to). |
| `--bugreport <type>` | Implies `--debug`. See below. |

---

## `sandbox <name> log` command

The log command is redesigned around the structured log files. Agent output is separate and accessed via dedicated flags.

### Default view

Pretty-printed, interleaved stream of `logs/cli.jsonl`, `logs/sandbox.jsonl`, `logs/monitor.jsonl`, and `logs/agent-hooks.jsonl`, ordered by timestamp. Default level: `info+`.

```
14:23:01 [cli]     info   sandbox.start    starting sandbox (backend=docker)
14:23:02 [sandbox] info   entrypoint.uid   remapped uid 0 → 1000
14:23:03 [cli]     info   mount.bind       bound /home/karl/Projects/foo
14:23:05 [sandbox] info   agent.launch     agent process started
14:23:06 [hooks]   info   agent.active     hook reported active
14:23:09 [monitor] warn   agent.idle       no output for 30s
```

### Flags

| Flag | Effect |
|------|--------|
| `--source <sources>` | Show only the specified sources instead of all four interleaved. Comma-separated list of: `cli`, `sandbox`, `monitor`, `hooks`. E.g. `--source cli,monitor` |
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

### Report types

Both mechanisms accept a required `<type>` argument:

| Type | Description |
|------|-------------|
| `safe` | Privacy-conscious report. Sensitive sections omitted or redacted. Suitable for sharing in a public GitHub issue. Includes a "Review before sharing" notice. |
| `full` | Author/developer report. No omissions, no redaction. Includes a prominent "**Do not share publicly**" warning banner. |

### Output filename

Reports are written to the current directory with an auto-generated name:

- Sandbox commands: `yoloai-bugreport-<name>-<timestamp>.md`
- Non-sandbox commands: `yoloai-bugreport-<timestamp>.md`

The filename is printed to stderr after writing:
```
Bug report written: yoloai-bugreport-x-20260315-142301.md
```

The temp file during writing is `<filename>.tmp`. On SIGKILL, the temp file survives with whatever was captured up to that point.

Report files are created with mode **0600** (owner read/write only).

### 1. Global `--bugreport` flag (flight recorder)

```
yoloai --bugreport safe new x .
yoloai --bugreport full new x .
```

Can be used with any yoloai command. When active:

- The output file is determined and the temp file opened immediately, before any subcommand logic runs.
- Static sections (header, command invocation, system info, backends, config) are written to the temp file right at launch.
- `--debug` is implicitly enabled: debug-level entries from the yoloai CLI are written to the temp file only, not to `cli.jsonl`. Container processes (`sandbox.jsonl`, `monitor.jsonl`, `agent-hooks.jsonl`) are independent and continue writing to their own files as normal — `--bugreport` cannot redirect them.
- A deferred finalizer (with `recover()` to catch panics) writes the exit code and any error, then renames the temp file to the final filename.
- For sandbox commands, the existing JSONL log files are included in the report — prior `--debug` runs will have contributed debug-level entries to `cli.jsonl`.
- The report is **always written** regardless of outcome: success, error, panic, or signal.

Typical workflow for a hard-to-reproduce bug:
```
yoloai --debug new x .           # run leading-up commands with debug logging
yoloai --debug start x           # more debug-logged commands
yoloai --bugreport safe start x  # captures live debug + prior logs from sandbox
```

### 2. `sandbox <name> bugreport` command (forensic tool)

```
yoloai sandbox <name> bugreport safe
yoloai sandbox <name> bugreport full
```

Used when a bug occurred in a past run and the user still has the sandbox — no need to reproduce the issue. Collects static diagnostic information from system state and the named sandbox, including all files under `logs/`, which will contain debug-level entries if prior commands were run with `--debug`.

Placed under `sandbox` alongside `sandbox <name> log` and `sandbox <name> info`. Non-sandbox commands don't accumulate persistent state, so there is nothing to perform forensics on without a sandbox.

---

## Output Format

A single GitHub-Flavored Markdown document, structured for direct pasting into a GitHub issue.

GitHub issue bodies are capped at **65,536 characters**. After writing the report, yoloai checks the file size and prints a warning to stderr if this limit is exceeded:

```
Warning: report exceeds GitHub's issue body limit (65,536 characters).
Upload as a Gist instead: gh gist create yoloai-bugreport-x-20260315-142301.md
```

Verbose sections (system info, backends, config, logs, sandbox detail) are wrapped in `<details>`/`<summary>` collapsible blocks. Only the header, type, command, and exit status are visible by default; machine-readable content is folded away.

```markdown
## yoloai Bug Report — 2026-03-15T14:23:01Z

> ⚠️ Review before sharing: this report may contain proprietary code,
> task descriptions, file paths, and internal configuration.

**Version:** 0.9.1 (abc1234, 2026-03-10)
**Type:** safe
**Command:** `yoloai --bugreport safe new x .`
**Exit code:** 1 — sandbox creation failed

<details>
<summary>System</summary>

...

</details>

<details>
<summary>logs/cli.jsonl</summary>

...

</details>
```

`full` reports include a stronger banner:

```markdown
> ⛔ FULL REPORT — unsanitized, contains all logs and agent output.
> Do not share publicly.
```

Note: `<details>`/`<summary>` is GitHub-Flavored Markdown and may not render correctly in other environments, but the content remains readable as plain text.

---

## Report Sections

Columns indicate whether a section is included in `safe` and `full` reports. *(flag)* and *(sandbox)* indicate which mechanism provides the section.

| Section | safe | full |
|---------|------|------|
| 1. Header | ✓ | ✓ |
| 2. Command invocation *(flag)* | ✓ redacted | ✓ |
| 3. System | ✓ | ✓ |
| 4. Backends | ✓ | ✓ |
| 5. Configuration | ✓ sanitized | ✓ |
| 6. Sandbox detail *(sandbox)* | ✓ partial | ✓ |
| 7. `cli.jsonl` *(sandbox)* | ✓ sanitized | ✓ |
| 8. `sandbox.jsonl` *(sandbox)* | ✓ partial | ✓ |
| 9. `monitor.jsonl` *(sandbox)* | ✓ | ✓ |
| 10. `agent-hooks.jsonl` *(sandbox)* | ✓ | ✓ |
| 11. Agent output *(sandbox)* | ✗ omitted | ✓ |
| 12. Live log *(flag)* | ✓ sanitized | ✓ |
| 13. Exit *(flag)* | ✓ | ✓ |

### 1. Header

- Timestamp (UTC, RFC3339)
- yoloai version, commit, build date
- Report type (`safe` or `full`)

### 2. Command Invocation *(flag only)*

Full `os.Args` as a fenced code block. In `safe` mode, values for `--prompt` / `-p` flags are redacted: `--prompt [REDACTED]`.

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

Global config (`~/.yoloai/config.yaml`) and active profile config, each in a fenced YAML code block. In `safe` mode, sanitized (see Sanitization). Missing files noted as "(not found)".

### 6. Sandbox Detail *(sandbox commands only)*

**Summary** — status, agent, model, backend, created, disk usage, has-changes.

**`environment.json`** — full contents (no API keys stored here).

**`agent-status.json`** — full contents.

**`runtime-config.json`** — In `safe` mode, `setup_commands` and `allowed_domains` fields are omitted (may reveal internal infrastructure). In `full` mode, full contents.

**Container log** — full output from the container runtime:
- docker: `docker logs yoloai-<name> 2>&1`
- podman: `podman logs yoloai-<name> 2>&1`
- tart/seatbelt: noted as "not available for this backend"

Errors from a non-running container are included rather than silently omitted.

### 7. `cli.jsonl` *(sandbox commands only)*

Full contents. In `safe` mode, `msg` fields are scanned for known-sensitive patterns and redacted. Contains debug-level entries if prior commands were run with `--debug`.

### 8. `sandbox.jsonl` *(sandbox commands only)*

In `safe` mode, entries with `event` types that log setup commands or network configuration (`entrypoint.setup_cmd`, `entrypoint.network.*`) are omitted — these may reveal internal infrastructure. In `full` mode, full contents.

### 9. `monitor.jsonl` *(sandbox commands only)*

Full contents in both modes. Detector decisions and status transitions contain no user data.

### 10. `agent-hooks.jsonl` *(sandbox commands only)*

Full contents in both modes. Hook events are idle/active state changes with no user content.

### 11. Agent output *(sandbox commands only)*

**Omitted in `safe` mode.** The agent's terminal output is the highest-risk section — it contains the full conversation, processed code, and any secrets the agent encountered in files.

In `full` mode: ANSI-stripped contents of `agent.log`. Raw terminal stream is never included (not meaningful outside a renderer).

### 12. Live log *(flag only)*

All debug-level log entries captured during the run. In `safe` mode, `msg` fields are scanned and sanitized as with `cli.jsonl`.

### 13. Exit *(flag only)*

- Exit code
- Error message (if any)
- Whether the process exited via panic (caught by `recover()`)

---

## Sanitization

All sanitization is best-effort. `safe` mode reduces the risk of accidental exposure; it is not a security guarantee. Do not include credential-related bugs in `safe` reports intended for public sharing.

### YAML keyword matching (`safe` mode)

Values are redacted for keys whose names (case-insensitive) contain any of:

```
key, token, secret, password, credential, passwd, pwd, auth, jwt, bearer,
cert, private, access, encryption, saml, oauth, sso, connection
```

```yaml
# input
anthropic_api_key: sk-ant-abc123
db_connection: postgres://user:pass@host/db
# output
anthropic_api_key: [REDACTED]
db_connection: [REDACTED]
```

Line-by-line on raw YAML text (no parser dependency). Handles indented keys (e.g. inside `env:` maps).

### Pattern scanning (`safe` mode)

Applied to JSONL `msg` fields, container log output, and any other free-text content included in the report. Patterns are applied in order; first match wins.

| Pattern | Matches | Example |
|---------|---------|---------|
| PEM blocks | Private keys, certificates | `-----BEGIN RSA PRIVATE KEY-----` |
| Known service prefixes | API keys with distinctive prefixes | `sk-ant-`, `sk-proj-`, `ghp_`, `ghu_`, `gha_`, `sk_live_`, `sk_test_`, `AIzaSy`, `pplx-`, `gsk_` |
| AWS access keys | AWS IAM key IDs | `AKIA[A-Z0-9]{16}` |
| Connection strings | Database URLs with embedded credentials | `\w+://[^:@\s]+:[^@\s]+@\S+` |
| JWT tokens | Three-part base64url tokens | `eyJ[A-Za-z0-9\-_]{10,}\.[A-Za-z0-9\-_]{10,}\.[A-Za-z0-9\-_]{10,}` |
| Long hex strings | Hashes, raw keys | `[a-fA-F0-9]{32,}` |
| Base64 strings | Encoded secrets, tokens | `[A-Za-z0-9+/\-_]{40,}={0,2}` |

Base64 strings are redacted aggressively — any base64-looking string of 40+ characters is considered a potential encoded secret. The diagnostic value of base64 content in logs is low; the security risk is not.

Matched content is replaced with `[REDACTED]` inline, preserving surrounding context so the log line remains readable.

---

## Deliberate Omissions (`safe` mode)

| Item | Reason omitted |
|------|----------------|
| Agent output (`agent.log`) | Full conversation; may contain proprietary code and echoed secrets |
| `prompt.txt` | May contain sensitive task descriptions |
| `--prompt` flag value in `os.Args` | May contain sensitive task descriptions |
| `setup_commands` in `runtime-config.json` | May reveal internal infrastructure |
| `allowed_domains` in `runtime-config.json` | May reveal internal network topology |
| `sandbox.jsonl` network/setup entries | Same as above |
| Full environment dump | Too noisy; may expose unrelated secrets |
| Credential/key files | Never appropriate for a bug report |
| `home-seed/` directory | Large; agent-internal; not useful for diagnosis |
| Raw `agent.log` | Terminal stream; not meaningful outside a renderer |

---

## Implementation Plan

### Structured logging (prerequisite)

1. Create `logs/` subdirectory in the sandbox state directory.
2. Replace existing `log.txt` / `monitor.log` with `logs/cli.jsonl`, `logs/sandbox.jsonl`, `logs/monitor.jsonl`, `logs/agent-hooks.jsonl`, and `logs/agent.log`.
3. Update all internal logging calls to emit structured JSONL entries with `ts`, `seq`, `level`, `event`, `msg` fields.
4. Update `sandbox <name> log` to the new design (see above).
5. Agent output capture (tmux pipe-pane) redirects to `logs/agent.log` — format unchanged.

### Flag (`--bugreport`)

1. Add `--bugreport <type>` as a persistent flag on the root Cobra command in `internal/cli/root.go`. Valid values: `safe`, `full`.
2. In `PersistentPreRunE`, if the flag is set:
   - Determine output filename (`yoloai-bugreport-[<name>-]<timestamp>.md`).
   - Open `<filename>.tmp` with mode 0600 immediately.
   - Write static sections (header, command invocation, system, backends, config).
   - Install a debug log handler that appends to the temp file.
   - Register a `defer` with `recover()` to write exit/error info and rename temp → final.
   - Print filename to stderr after rename.
   - Check file size and warn to stderr if > 65,536 characters.
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
| `writeBugReportHeader(w, version, commit, date, reportType)` | Section 1 |
| `writeBugReportCommandInvocation(w, reportType)` | Section 2 (flag only) |
| `writeBugReportSystem(w)` | Section 3 |
| `writeBugReportBackends(ctx, w)` | Section 4 |
| `writeBugReportConfig(w, reportType)` | Section 5 |
| `writeBugReportSandboxDetail(ctx, w, rt, name, reportType)` | Section 6 |
| `writeBugReportCLILog(w, name, reportType)` | Section 7 |
| `writeBugReportSandboxLog(w, name, reportType)` | Section 8 |
| `writeBugReportMonitorLog(w, name)` | Section 9 |
| `writeBugReportHooksLog(w, name)` | Section 10 |
| `writeBugReportAgentOutput(w, name)` | Section 11 (full only) |
| `bugReportFilename(sandboxName, t)` | Generates output filename |
| `backendVersion(backend)` | Returns version string from backend CLI, or "" |
| `sanitizeYAMLConfig(content)` | Redacts values for sensitive key names |
| `sanitizeText(content)` | Applies pattern scanning to free-text content (JSONL msg fields, container logs, etc.) |
