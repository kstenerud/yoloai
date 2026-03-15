# Bug Report and Structured Logging Design

## Structured Logging

### Log files

Each sandbox maintains log files named by writer, under `~/.yoloai/sandboxes/<name>/logs/`:

| File | Writer | Format | Content |
|------|--------|--------|---------|
| `cli.jsonl` | yoloai CLI (host Go binary) | JSONL | Sandbox lifecycle, mount operations, backend calls, config resolution |
| `sandbox.jsonl` | entrypoint.sh → entrypoint.py → sandbox-setup.py (sequential, single writer at a time) | JSONL | Container setup, UID remapping, network isolation, overlay mounts, prompt delivery |
| `monitor.jsonl` | status-monitor.py | JSONL | Detector decisions, status transitions, idle/active polling |
| `agent-hooks.jsonl` | Agent hook scripts | JSONL | Hook-reported state changes (idle/active events from Claude Code hooks etc.) |
| `agent.log` | tmux pipe-pane | Raw terminal stream | Verbatim agent process output (ANSI codes, cursor positioning, etc.) |

`agent-status.json` is retained as the IPC mechanism between hook scripts and status-monitor.py — it is not a log file. `agent-hooks.jsonl` is a separate append-only log of hook events for diagnostic purposes.

Hook commands are shell strings injected by yoloai into the agent's settings (e.g. Claude Code's `~/.claude/settings.json`) via `injectIdleHook()`. They have access to `$YOLOAI_DIR` (e.g. `/yoloai` in Docker containers), so the log path is `${YOLOAI_DIR}/logs/agent-hooks.jsonl`. Each hook command appends one JSONL entry to `agent-hooks.jsonl` **and** overwrites `agent-status.json` (the latter is still required by `HookDetector` in `status-monitor.py`). No agent-side changes are needed — hooks are fully yoloai-side.

Hook JSONL entries use the standard schema with an added `status` field:

```json
{"ts": "2026-03-15T14:23:01.123Z", "level": "info", "event": "hook.idle", "msg": "agent hook: idle", "status": "idle"}
{"ts": "2026-03-15T14:23:05.456Z", "level": "info", "event": "hook.active", "msg": "agent hook: active", "status": "active"}
```

The hook commands (built in `create.go`) append to the JSONL log then overwrite the status file:

```sh
printf '{"ts":"%s","level":"info","event":"hook.idle","msg":"agent hook: idle","status":"idle"}\n' \
  "$(date -u +%Y-%m-%dT%H:%M:%S.000Z)" >> "${YOLOAI_DIR:-/yoloai}/logs/agent-hooks.jsonl" && \
printf '{"status":"idle","exit_code":null,"timestamp":%d}\n' "$(date +%s)" \
  > "${YOLOAI_DIR:-/yoloai}/agent-status.json"
```

Agent output is a raw terminal recording — not loggable alongside structured events. It is treated as a separate artifact, not a log source.

Each file has a single writer, so no file locking is required. POSIX append semantics are sufficient for `agent.log`.

### JSONL schema

Each line in the JSONL log files is a JSON object:

```json
{"ts": "2026-03-15T14:23:01.123Z", "level": "info", "event": "sandbox.ready", "msg": "sandbox fully initialized", "backend": "docker", "sandbox": "x"}
```

| Field | Type | Description |
|-------|------|-------------|
| `ts` | string | RFC3339 timestamp with milliseconds |
| `level` | string | `debug`, `info`, `warn`, `error` |
| `event` | string | Dot-separated event type — see taxonomy below |
| `msg` | string | Human-readable summary |
| *(additional fields)* | | Per-event structured data (e.g. `backend`, `sandbox`, `path`) |

### Event type taxonomy

#### `sandbox.jsonl` and `monitor.jsonl`

`sandbox.jsonl` is written by three sequential processes: the shell trampoline (`entrypoint.sh`), the root Python entrypoint (`entrypoint.py`), and the unprivileged setup script (`sandbox-setup.py`). `monitor.jsonl` is written solely by `status-monitor.py`.

| Event | Writer | Level | Fields | Description |
|-------|--------|-------|--------|-------------|
| `entrypoint.start` | entrypoint.sh | info | — | Shell trampoline started (canned entry) |
| `entrypoint.python_start` | entrypoint.py | info | — | Root Python entrypoint started |
| `uid.remap` | entrypoint.py | info | `host_uid`, `host_gid` | UID/GID remapping applied |
| `uid.remap_skip` | entrypoint.py | debug | — | UID already correct, remapping skipped |
| `secrets.write` | entrypoint.py | info | `path`, `kind` | Credential file written |
| `secrets.skip` | entrypoint.py | debug | — | No credentials to inject |
| `network.isolate` | entrypoint.py | info | — | Default-deny iptables rule applied |
| `network.allow` | entrypoint.py | info | `domain`, `ip` | Domain added to allowlist |
| `overlay.mount` | entrypoint.py | info | `path` | Overlayfs mount applied |
| `overlay.skip` | entrypoint.py | debug | — | No overlay mounts configured |
| `setup_cmd.start` | entrypoint.py | info | `cmd`, `index`, `total` | Setup command starting |
| `setup_cmd.done` | entrypoint.py | info | `duration_ms` | Setup command succeeded (exit_code=0) |
| `setup_cmd.error` | entrypoint.py | error | `exit_code`, `duration_ms` | Setup command failed (non-zero exit); mutually exclusive with `setup_cmd.done` |
| `sandbox.backend_setup` | sandbox-setup.py | info | `backend` | Backend-specific setup (seatbelt symlinks, tart mounts) |
| `overlay.git_baseline` | sandbox-setup.py | info | `path` | Git baseline commit on overlay merged directory (Docker only) |
| `sandbox.tmux_start` | sandbox-setup.py | info | — | tmux session created |
| `sandbox.agent_launch` | sandbox-setup.py | info | `agent`, `model` | Agent process started |
| `sandbox.prompt_deliver` | sandbox-setup.py | info | `method` | Prompt delivered to agent |
| `sandbox.prompt_skip` | sandbox-setup.py | info | — | No prompt.txt; agent started without prompt |
| `sandbox.monitor_launch` | sandbox-setup.py | info | — | status-monitor.py spawned |
| `sandbox.ready` | sandbox-setup.py | info | — | Sandbox fully initialized |
| `sandbox.agent_exit` | sandbox-setup.py | info | `exit_code` | Agent process exited |
| `monitor.start` | status-monitor.py | info | `detectors` | Monitor started |
| `detector.result` | status-monitor.py | **debug** | `detector`, `confidence`, `status` | Per-poll detector verdict (very frequent) |
| `status.transition` | status-monitor.py | info | `from`, `to`, `detector` | Status changed (includes `to=done` on pane death) |
| `monitor.exit` | status-monitor.py | info | `reason` | Monitor exiting |

#### `agent-hooks.jsonl`

| Event | Level | Fields | Description |
|-------|-------|--------|-------------|
| `hook.idle` | info | `status` | Notification hook fired (agent idle) |
| `hook.active` | info | `status` | PreToolUse hook fired (agent active) |

#### `cli.jsonl`

Events are defined by the Go CLI implementation. Event names follow the same `component.action` convention (e.g. `sandbox.create`, `mount.bind`, `backend.exec`). Exact taxonomy defined during implementation.

#### Safe-mode filter

The following event types are omitted from `sandbox.jsonl` in `safe` mode — they may reveal internal infrastructure:

- `setup_cmd.*` — setup commands may contain internal hostnames or credentials
- `network.allow` — allowed domains reveal internal network topology

All other events pass through. Field-level pattern scanning applies to all string-valued fields (not just `msg`) — see Sanitization section.

### `--debug` and `--verbose`

| Flag | Effect |
|------|--------|
| `--verbose` | More output printed to the terminal. No effect on log files. |
| `--debug` | Enables `debug`-level entries in `cli.jsonl`. For non-sandbox commands, silently ignored unless `--bugreport` is also active (in which case debug entries go to the bugreport temp file). Container processes (`sandbox.jsonl`, `monitor.jsonl`) read their debug flag from `runtime-config.json` at container creation time and cannot be reconfigured by `--debug` on subsequent commands like `start` — pass `--debug` to `new` to get container-side debug logs. |
| `--bugreport <type>` | Implies `--debug`. See below. |

---

## `sandbox <name> log` command

The log command is redesigned around the structured log files. Agent output is separate and accessed via dedicated flags.

### Default view

Pretty-printed, interleaved stream of `logs/cli.jsonl`, `logs/sandbox.jsonl`, `logs/monitor.jsonl`, and `logs/agent-hooks.jsonl`, ordered by timestamp. Default level: `info+`.

**Interleaving algorithm:**
- **Static (no `--follow`):** Read all four files fully, merge-sort by `ts`, emit. Ordering is exact.
- **`--follow`:** One goroutine per file tails its file and sends lines to a merge channel; lines are emitted as they arrive. Ordering is approximate — sub-second reordering between files is possible but inconsequential since all lines carry timestamps.

**Pretty-print format:**

```
HH:MM:SS src     LEVL  event-name               message  key=val key=val...
```

- **Time:** local timezone, `HH:MM:SS`
- **Source:** 7 chars, right-padded: `cli    `, `sandbox`, `monitor`, `hooks  `
- **Level:** 4 chars uppercase: `INFO`, `WARN`, `ERRO`, `DBUG`
- **Event:** 24 chars, right-padded (fits all taxonomy events without abbreviation)
- **Message + extra fields:** remaining columns to terminal edge (from `$COLUMNS` or `os.Stdout` size, fallback 120). Extra fields shown as `key=val` pairs after the message, in order of diagnostic importance (e.g. `exit_code`, `duration_ms`, `agent`, `model`, `path`). The entire line is hard-truncated at terminal width — no wrapping.

```
14:23:01 cli     INFO  sandbox.create           creating sandbox  backend=docker name=x
14:23:02 sandbox INFO  entrypoint.python_start  root entrypoint started
14:23:02 sandbox INFO  uid.remap                UID/GID remapped  host_uid=1000 host_gid=1000
14:23:03 sandbox INFO  overlay.mount            overlay mounted   path=/home/karl/Projects/foo
14:23:05 sandbox INFO  sandbox.agent_launch     agent started     agent=claude model=claude-opus-4-6
14:23:06 hooks   INFO  hook.active              agent hook: active
14:23:09 monitor INFO  status.transition        status changed    from=active to=idle detector=hook
```

### Flags

The command operates in one of three mutually exclusive modes selected by flag. `--follow` is the only flag that applies across all modes.

**Structured log mode** (default):

| Flag | Effect |
|------|--------|
| `--source <sources>` | Show only the specified sources instead of all four interleaved. Comma-separated list of: `cli`, `sandbox`, `monitor`, `hooks`. E.g. `--source cli,monitor` |
| `--level debug\|info\|warn\|error` | Filter by minimum log level (default: `info`) |
| `--since <duration\|timestamp>` | e.g. `--since 5m` or `--since 14:20:00` (local timezone). Filters by `ts` field after converting to local time. |
| `--raw` | Emit lines as raw JSONL (no formatting) |

**Agent output modes** (mutually exclusive with each other and with all structured log flags):

| Flag | Effect |
|------|--------|
| `--agent` | Show agent output with ANSI stripped — readable text |
| `--agent-raw` | Show raw agent terminal stream (for replay or escape sequence inspection) |

**Applies to all modes:**

| Flag | Effect |
|------|--------|
| `--follow` / `-f` | Tail the log live. Auto-exits when all sources stop producing output and sandbox status is `done`. |

---

## Two Bug Report Mechanisms

### Report types

Both mechanisms accept a required `<type>` argument:

| Type | Description |
|------|-------------|
| `safe` | Privacy-conscious report. Sensitive sections omitted or redacted. Suitable for sharing in a public GitHub issue. Includes a "Review before sharing" notice. |
| `unsafe` | Author/developer report. No omissions, no redaction. Includes a prominent "**Do not share publicly**" warning banner. |

### Output filename

Reports are written to the current directory with an auto-generated name:

```
yoloai-bugreport-<timestamp>.md
```

Timestamp format: UTC, `YYYYMMDD-HHMMSS.mmm` (millisecond precision). Example: `yoloai-bugreport-20260315-142301.123.md`. The sandbox name is included inside the report, not in the filename. If a file with the same name already exists (same-millisecond collision), yoloai exits with an error rather than overwriting.

The filename is printed to stderr after writing:
```
Bug report written: yoloai-bugreport-20260315-142301.123.md
```

The temp file during writing is `<filename>.tmp`. On successful completion the temp file is renamed atomically to the final name. On SIGKILL the rename never happens, leaving the `.tmp` with whatever was captured up to that point — it is not cleaned up automatically.

Report files are created with mode **0600** (owner read/write only).

### 1. Global `--bugreport` flag (flight recorder)

```
yoloai --bugreport safe new x .
yoloai --bugreport unsafe new x .
```

Can be used with any yoloai command. When active:

- The output file is determined and the temp file opened immediately, before any subcommand logic runs.
- Static sections (header, command invocation, system info, backends, config) are written to the temp file right at launch.
- `--debug` is implicitly enabled: debug-level entries are written to both the bugreport temp file and to `cli.jsonl` (once it opens in the subcommand). Container processes (`sandbox.jsonl`, `monitor.jsonl`, `agent-hooks.jsonl`) are independent and continue writing to their own files as normal — `--bugreport` cannot redirect them.
- A deferred finalizer (with `recover()` to catch panics) writes the exit code and any error, then renames the temp file to the final filename.
- For sandbox commands, the existing JSONL log files are included in the report — prior `--debug` runs will have contributed debug-level entries to `cli.jsonl`.
- The report is **always written** regardless of outcome: success, error, or panic. On SIGKILL the rename never completes, leaving a partial `.tmp` file.

For non-sandbox commands (e.g. `yoloai ls`, `yoloai system info`), the report contains only sections 1–5, 13, and 14 — no sandbox detail or log files are included.

Typical workflow for a hard-to-reproduce bug:
```
yoloai --debug new x .           # run leading-up commands with debug logging
yoloai --debug start x           # more debug-logged commands
yoloai --bugreport safe start x  # captures live debug + prior logs from sandbox
```

### 2. `sandbox <name> bugreport` command (forensic tool)

```
yoloai sandbox <name> bugreport safe
yoloai sandbox <name> bugreport unsafe
```

Used when a bug occurred in a past run and the user still has the sandbox — no need to reproduce the issue. Collects static diagnostic information from system state and the named sandbox, including all files under `logs/`, which will contain debug-level entries if prior commands were run with `--debug`.

Placed under `sandbox` alongside `sandbox <name> log` and `sandbox <name> info`. Non-sandbox commands don't accumulate persistent state, so there is nothing to perform forensics on without a sandbox.

---

## Output Format

A single GitHub-Flavored Markdown document, structured for direct pasting into a GitHub issue.

GitHub issue bodies are capped at **65,536 characters**. After writing the report, yoloai checks the file size and prints a warning to stderr if this limit is exceeded:

```
Warning: report exceeds GitHub's issue body limit (65,536 characters).
Upload as a Gist instead: gh gist create yoloai-bugreport-20260315-142301.123.md
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

`unsafe` reports include a stronger banner:

```markdown
> ⛔ UNSAFE REPORT — unsanitized, contains all logs and agent output.
> Do not share publicly.
```

Note: `<details>`/`<summary>` is GitHub-Flavored Markdown and may not render correctly in other environments, but the content remains readable as plain text.

---

## Report Sections

Columns indicate whether a section is included in `safe` and `unsafe` reports. *(flag)* and *(sandbox)* indicate which mechanism provides the section.

| Section | safe | unsafe |
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
| 12. tmux screen capture *(sandbox)* | ✗ omitted | ✓ |
| 13. Live log *(flag)* | ✓ sanitized | ✓ |
| 14. Exit *(flag)* | ✓ | ✓ |

### 1. Header

- Timestamp (UTC, RFC3339)
- yoloai version, commit, build date
- Report type (`safe` or `unsafe`)

### 2. Command Invocation *(flag only)*

Full `os.Args` as a fenced code block. In `safe` mode, values for `--prompt` / `-p` flags are redacted: `--prompt [REDACTED]`. `--prompt-file` / `-P` paths are not redacted (the path itself is not sensitive; file contents are never included).

### 3. System

- OS and architecture (`GOOS/GOARCH` from compile-time constants)
- Kernel string (`uname -a` on Linux/macOS; omitted on Windows)
- Relevant environment variables: `DOCKER_HOST`, `CONTAINER_HOST`, `XDG_RUNTIME_DIR`, `YOLOAI_SANDBOX`, `HOME`, `TMUX` (only those that are set)
- yoloai data directory path and disk usage

### 4. Backends

For each known backend (docker, podman, tart, seatbelt):

- Availability status: one of `available`, `unavailable`, or `unknown` (if the check itself failed)
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

**`environment.json`** — In `safe` mode, parsed with `encoding/json`, `network_allow` and `setup` fields deleted, then re-serialized (`network_allow` reveals allowed domains; `setup` reveals setup commands — same sensitivity as the corresponding fields in `runtime-config.json`). In `unsafe` mode, full contents.

**`agent-status.json`** — full contents.

**`runtime-config.json`** — In `safe` mode, parsed with `encoding/json`, `setup_commands` and `allowed_domains` fields deleted, then re-serialized (may reveal internal infrastructure). In `unsafe` mode, full contents included verbatim.

**Container log** — full output from the container runtime:
- docker: `docker logs yoloai-<name> 2>&1`
- podman: `podman logs yoloai-<name> 2>&1`
- tart/seatbelt: noted as "not available for this backend"

Errors from a non-running container are included rather than silently omitted.

### 7. `cli.jsonl` *(sandbox commands only)*

Full contents. In `safe` mode, all string-valued fields are scanned for known-sensitive patterns and redacted (via `sanitizeJSONLFile`). Contains debug-level entries if prior commands were run with `--debug`.

### 8. `sandbox.jsonl` *(sandbox commands only)*

In `safe` mode, `setup_cmd.*` and `network.allow` entries are omitted and all string-valued fields are pattern-scanned for sensitive values (see safe-mode filter in taxonomy above). In `unsafe` mode, full contents.

### 9. `monitor.jsonl` *(sandbox commands only)*

Full contents in both modes. Detector decisions and status transitions contain no user data.

### 10. `agent-hooks.jsonl` *(sandbox commands only)*

Full contents in both modes. Hook events are idle/active state changes with no user content.

### 11. Agent output *(sandbox commands only)*

**Omitted in `safe` mode.** The agent's terminal output is the highest-risk section — it contains the full conversation, processed code, and any secrets the agent encountered in files.

In `unsafe` mode: ANSI-stripped contents of `agent.log`. Raw terminal stream is never included (not meaningful outside a renderer).

**ANSI stripping:** `agent.log` contains the full VT100 stream — not just SGR color codes but cursor positioning, mode switches, terminal queries, OSC sequences, etc. The stripper uses a grammar-based regex covering all three sequence forms (no library dependency):

```
\x1b(?:
  \[[0-?]*[ -/]*[@-~]             CSI: param bytes + intermediate bytes + final byte
  |\][^\x07\x1b]*(?:\x07|\x1b\\)  OSC: BEL or ST terminated
  |[ -/][0-~]                      nF: intermediate + final (e.g. character set designation)
  |[0-~]                           2-char: Fp/Fe/Fs (ESC 7, ESC M, ESC c, etc.)
)
```

DCS/APC/SOS/PM sequences are left as best-effort (span multiple lines, vanishingly rare in agent output).

### 12. tmux screen capture *(sandbox commands only)*

**Omitted in `safe` mode** (screen contents may contain sensitive data).

In `unsafe` mode: output of `tmux capture-pane -p -t main`, which renders the current pane contents as plain text. Session name is always `main`. For seatbelt sandboxes, the non-default socket must be specified: `tmux -S $state_dir/tmux/tmux.sock capture-pane -p -t main`. For all other backends, the default socket is used. Silently omitted if the sandbox is not running or the tmux session is not found. Provides a snapshot of what was on screen at the time of the report — useful context even after the agent has exited, as long as the container is still up.

### 13. Live log *(flag only)*

All log entries (all levels) captured from the CLI logger during the run. In `safe` mode, all string-valued fields are scanned and sanitized as with `cli.jsonl`.

### 14. Exit *(flag only)*

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

Applied to: JSONL `msg` fields and all other string-valued JSONL fields (e.g. `path`, `cmd`, `backend`), container log output, and `environment.json` string values. Patterns are applied in order; first match wins.

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
| `setup` in `environment.json` | Same as setup_commands |
| `network_allow` in `environment.json` | Same as allowed_domains |
| `sandbox.jsonl` network/setup entries | Same as above |
| Full environment dump | Too noisy; may expose unrelated secrets |
| Credential/key files | Never appropriate for a bug report |
| `home-seed/` directory | Large; agent-internal; not useful for diagnosis |
| Raw `agent.log` | Terminal stream; not meaningful outside a renderer |

---

## Implementation Plan

### Structured logging (prerequisite)

1. Create `logs/` subdirectory in the sandbox state directory **on the host before container start** — `entrypoint.sh` writes to it as its first action inside the container, so the directory must be visible via the bind mount immediately.
2. Replace existing `log.txt` / `monitor.log` with `logs/cli.jsonl`, `logs/sandbox.jsonl`, `logs/monitor.jsonl`, `logs/agent-hooks.jsonl`, and `logs/agent.log`. Update the path constants in `sandbox/paths.go`.
3. Update all internal logging calls to emit structured JSONL entries with `ts`, `level`, `event`, `msg` fields.
4. Update `sandbox <name> log` to the new design (see above).
5. **Python-side change:** Update `sandbox-setup.py` to redirect `tmux pipe-pane` output to `logs/agent.log` instead of `log.txt`. Remove the existing `sys.stdout`/`sys.stderr` redirect to `log.txt` — sandbox-setup.py will write structured JSONL directly instead of relying on stdout capture.
6. **Python-side change:** Update `status-monitor.py` to write structured JSONL to `logs/monitor.jsonl` instead of `monitor.log`.

#### Container entrypoint refactor

`entrypoint.sh` is refactored into a minimal shell trampoline + a Python script:

- **`entrypoint.sh` (shell, stays thin):** Writes one canned JSONL entry to `logs/sandbox.jsonl` to record that the shell started (evidence of container boot even if Python fails), then `exec`s into `entrypoint.py`. Timestamp via `date -u +%Y-%m-%dT%H:%M:%S.000Z`.
  ```json
  {"ts":"...","level":"info","event":"entrypoint.start","msg":"entrypoint.sh started"}
  ```
- **`entrypoint.py` (new, runs as root):** Handles all real setup work — UID remapping, secrets, network isolation, overlay mounts, setup commands — and writes structured JSONL to `logs/sandbox.jsonl`. Then `exec`s to `gosu yoloai python3 sandbox-setup.py`.
- **`sandbox-setup.py`:** Continues as the unprivileged Python process; appends to `logs/sandbox.jsonl` for tmux setup, agent launch, and prompt delivery.

### CLI logger rewrite (prerequisite)

The existing CLI logger must be replaced with a multi-sink structured logger before bug reporting or `cli.jsonl` can be implemented. Requirements:

- **Multiple simultaneous sinks** — each sink is an `io.Writer` with its own minimum level filter
- **Sinks:** (1) stderr (for `--verbose` terminal output), (2) `cli.jsonl` file per sandbox, (3) bugreport temp file when `--bugreport` is active
- **Single initialization point** — construct the multi-sink logger in `PersistentPreRunE` and call `slog.SetDefault()`. All existing `slog.Default()` call sites in the CLI (e.g. `sandbox.NewManager(rt, slog.Default(), ...)`) automatically pick it up with no other changes.
- **`cli.jsonl` sink is added lazily** — the sandbox name is a subcommand positional argument, not known at `PersistentPreRunE` time. Each sandbox subcommand's `RunE` opens `logs/cli.jsonl` and registers it as a sink before doing any other work. Log entries emitted in `PersistentPreRunE` before the sink is registered go to whichever other sinks are already active (bugreport temp file, stderr).
- **`--debug` flag:** currently defined as a local flag on `new` and `reset` commands. Must be removed from those commands and replaced with the new global persistent `--debug` flag; behavior is unchanged.
- **Bugreport sink receives all levels** — not filtered to debug only; the temp file is the flight recorder.
- **Live log entries are buffered in memory** — the bugreport sink accumulates entries in a `[]LogEntry` slice during the run. `writeBugReportLiveLog` writes them all in the `defer` finalizer, preserving document section order. Entry count is bounded by CLI operation duration, which is short.

New file: `internal/cli/logger.go`.

### Flag (`--bugreport`)

1. Add `--bugreport <type>` as a persistent flag on the root Cobra command in `internal/cli/root.go`. Valid values: `safe`, `unsafe`.
2. In `PersistentPreRunE`, if the flag is set:
   - Determine output filename (`yoloai-bugreport-<timestamp>.md`).
   - Open `<filename>.tmp` with mode 0600 immediately.
   - Write static sections (header, command invocation, system, backends, config).
   - Register the temp file as an all-levels sink on the CLI logger.
   - Register a `defer` with `recover()` to write exit/error info and rename temp → final.
   - Print filename to stderr after rename.
   - Check file size and warn to stderr if > 65,536 characters.
   - Orphaned `.tmp` files from prior SIGKILLed runs are left in place — they contain partial diagnostic data and should not be silently deleted.
3. New file: `internal/cli/bugreport_writer.go` — shared section-writing logic.

### Command (`sandbox <name> bugreport`)

1. New file: `internal/cli/sandbox_bugreport.go` — implements `runSandboxBugReport()`
2. Add a `"bugreport"` case to `sandboxDispatch()` in `internal/cli/sandbox_cmd.go` dispatching to `runSandboxBugReport()`
3. Pass `version`, `commit`, `date` strings via a closure or package-level vars (same pattern as other commands in the dispatch switch)
4. No new external dependencies

### Key functions

| Function | Purpose |
|----------|---------|
| `runSandboxBugReport(cmd, name, reportType, version, commit, date)` | Entry point called from `sandboxDispatch()` |
| `writeBugReportHeader(w, version, commit, date, reportType)` | Section 1 |
| `writeBugReportCommandInvocation(w, reportType)` | Section 2 (flag only) |
| `writeBugReportSystem(w)` | Section 3 |
| `writeBugReportBackends(ctx, w)` | Section 4 |
| `writeBugReportConfig(w, reportType)` | Section 5 |
| `writeBugReportSandboxDetail(ctx, w, rt, name, reportType)` | Section 6 |
| `writeBugReportCLILog(w, name, reportType)` | Section 7 |
| `writeBugReportSandboxJSONL(w, name, reportType)` | Section 8 |
| `writeBugReportMonitorLog(w, name)` | Section 9 |
| `writeBugReportHooksLog(w, name)` | Section 10 |
| `writeBugReportAgentOutput(w, name)` | Section 11 (unsafe only) |
| `writeBugReportTmuxCapture(w, name, backend, stateDir)` | Section 12 (unsafe only); uses seatbelt socket when needed |
| `writeBugReportLiveLog(w, entries, reportType)` | Section 13 (flag only) |
| `writeBugReportExit(w, code, err, panicked)` | Section 14 (flag only) |
| `bugReportFilename(t)` | Generates output filename from UTC timestamp (millisecond precision); errors if file already exists |
| `backendVersion(backend)` | Returns version string from backend CLI, or "" |
| `sanitizeYAMLConfig(content)` | Redacts values for sensitive key names; operates on raw YAML text line-by-line |
| `sanitizeJSONLFile(path, reportType, omitEvents)` | Reads a JSONL file, omits specified event types, applies pattern scanning to all string-valued fields of each parsed object; passes through malformed lines unmodified. `omitEvents` entries are either exact (`"network.allow"`) or prefix patterns ending in `".*"` (`"setup_cmd.*"`). |
| `sanitizeText(content)` | Applies pattern scanning to raw free-text (container logs, environment.json values, etc.) |
