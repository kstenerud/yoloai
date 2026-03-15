# Implementation Plan: Bug Report and Structured Logging

Design spec: `docs/design/bugreport.md`

This plan is ordered by dependency. Each phase must be complete before the next starts.
Run `make check` after each phase.

---

## Phase 1: CLI Logger Rewrite

**Files touched:** `internal/cli/logger.go` (new), `internal/cli/root.go`, `internal/cli/commands.go`, `internal/cli/reset.go`

### 1.1 New file: `internal/cli/logger.go`

Implement a multi-sink `slog.Handler` that fans log records out to N sinks, each with its own minimum level filter.

```go
// multiSinkHandler sends each record to all registered sinks whose minLevel <= record.Level.
type multiSinkHandler struct {
    mu    sync.Mutex
    sinks []logSink
}

type logSink struct {
    handler  slog.Handler
    minLevel slog.Level
}

func (h *multiSinkHandler) addSink(handler slog.Handler, minLevel slog.Level)
func (h *multiSinkHandler) Enabled(ctx context.Context, level slog.Level) bool   // true if any sink is enabled
func (h *multiSinkHandler) Handle(ctx context.Context, r slog.Record) error       // fan out to all enabled sinks
func (h *multiSinkHandler) WithAttrs(attrs []slog.Attr) slog.Handler
func (h *multiSinkHandler) WithGroup(name string) slog.Handler
```

Also define:

```go
// LogEntry is a buffered log entry for the bugreport live-log sink.
type LogEntry struct {
    Time    time.Time
    Level   slog.Level
    Event   string
    Message string
    Attrs   []slog.Attr
}

// bufferSink accumulates LogEntry values for the bugreport live log (section 13).
type bufferSink struct {
    mu      sync.Mutex
    entries []LogEntry
}
```

Expose package-level state:
```go
var (
    globalHandler *multiSinkHandler   // set in PersistentPreRunE; nil before
    liveLogBuffer *bufferSink         // non-nil only when --bugreport is active
)

// AddLogSink adds a sink to the global handler. Called by sandbox subcommands after
// they know the sandbox name and can open logs/cli.jsonl.
func AddLogSink(w io.Writer, minLevel slog.Level)
```

The JSONL sink formats each record as:
```json
{"ts":"2026-03-15T14:23:01.123Z","level":"info","event":"sandbox.create","msg":"...","backend":"docker"}
```
`event` comes from an `event` slog attribute on the record (added by callers). If absent, omit the field.
`ts` is RFC3339 with milliseconds, UTC.

### 1.2 Remove `--debug` local flags

- `internal/cli/commands.go` line ~299: remove `newCmd.Flags().BoolVar(&debug, "debug", false, ...)`
- `internal/cli/reset.go` line ~87: remove `resetCmd.Flags().BoolVar(&debug, "debug", false, ...)`

### 1.3 Add global persistent `--debug` flag in `root.go`

```go
rootCmd.PersistentFlags().BoolVar(&globalDebug, "debug", false, "enable debug-level log entries in cli.jsonl")
```

### 1.4 Add `PersistentPreRunE` in `root.go`

The existing `PersistentPreRun` (non-E) handles `--verbose`/`--quiet`. Convert it or chain a new `PersistentPreRunE` that:

1. Initialises `globalHandler = &multiSinkHandler{}`
2. Adds the stderr sink at `info` level (or `debug` if `--debug` is set; suppressed if `--quiet`).
3. Calls `slog.SetDefault(slog.New(globalHandler))`.
4. If `--bugreport` is set (Phase 6), adds the bugreport temp file sink.

After `slog.SetDefault()`, all existing `slog.Default()` call sites — including `sandbox.NewManager(rt, slog.Default(), ...)` in `helpers.go` — automatically use the multi-sink logger.

---

## Phase 2: Sandbox Log Path Constants

**Files touched:** `sandbox/paths.go`, `sandbox/create.go`, plus any callers of the old constants.

### 2.1 Update `sandbox/paths.go`

Add or replace constants:

```go
const (
    LogsDirName       = "logs"
    CLIJSONLFile      = "logs/cli.jsonl"
    SandboxJSONLFile  = "logs/sandbox.jsonl"
    MonitorJSONLFile  = "logs/monitor.jsonl"
    HooksJSONLFile    = "logs/agent-hooks.jsonl"
    AgentLogFile      = "logs/agent.log"
)
```

Remove the old `LogFile = "log.txt"` and `MonitorLog = "monitor.log"` constants (or keep as deprecated aliases until callers are updated in Phase 5).

### 2.2 Create `logs/` directory on host before container start

In `sandbox/create.go` (or wherever the sandbox state directory is set up at creation time), add:

```go
logsDir := filepath.Join(stateDir, sandbox.LogsDirName)
if err := os.MkdirAll(logsDir, 0700); err != nil {
    return fmt.Errorf("create logs dir: %w", err)
}
```

This must happen before the container starts, because `entrypoint.sh` writes to it immediately.

### 2.3 Update hook commands in `create.go`

The hook commands built by `injectIdleHook()` must append to `logs/agent-hooks.jsonl` in addition to overwriting `agent-status.json`. Update `statusIdleCommand` and `statusActiveCommand` strings:

```sh
# idle hook (built as a Go string in create.go):
printf '{"ts":"%s","level":"info","event":"hook.idle","msg":"agent hook: idle","status":"idle"}\n' \
  "$(date -u +%Y-%m-%dT%H:%M:%S.000Z)" >> "${YOLOAI_DIR:-/yoloai}/logs/agent-hooks.jsonl" && \
printf '{"status":"idle","exit_code":null,"timestamp":%d}\n' "$(date +%s)" \
  > "${YOLOAI_DIR:-/yoloai}/agent-status.json"

# active hook:
printf '{"ts":"%s","level":"info","event":"hook.active","msg":"agent hook: active","status":"active"}\n' \
  "$(date -u +%Y-%m-%dT%H:%M:%S.000Z)" >> "${YOLOAI_DIR:-/yoloai}/logs/agent-hooks.jsonl" && \
printf '{"status":"active","exit_code":null,"timestamp":%d}\n' "$(date +%s)" \
  > "${YOLOAI_DIR:-/yoloai}/agent-status.json"
```

These are injected into `${YOLOAI_DIR}` by `injectIdleHook()` — `$YOLOAI_DIR` is already in scope for hook scripts.

---

## Phase 3: Python-side Structured Logging

**Files touched:** `sandbox/entrypoint.sh`, `sandbox/entrypoint.py` (new), `sandbox/sandbox-setup.py`, `sandbox/status-monitor.py`

All Python JSONL writers use:
```python
import json, datetime, sys

def log(level, event, msg, **fields):
    entry = {"ts": datetime.datetime.utcnow().strftime("%Y-%m-%dT%H:%M:%S.") +
             f"{datetime.datetime.utcnow().microsecond // 1000:03d}Z",
             "level": level, "event": event, "msg": msg, **fields}
    sys.stdout.write(json.dumps(entry) + "\n")
    sys.stdout.flush()
```

For file writers (writing to `logs/sandbox.jsonl` or `logs/monitor.jsonl`), open the file in append mode and flush after each write.

### 3.1 Refactor `entrypoint.sh` → thin trampoline

`entrypoint.sh` must remain minimal. Its only job is to write one canned JSONL line proving the container booted, then exec into Python:

```sh
#!/bin/sh
set -e
printf '{"ts":"%s","level":"info","event":"entrypoint.start","msg":"entrypoint.sh started"}\n' \
  "$(date -u +%Y-%m-%dT%H:%M:%S.000Z)" >> /yoloai/logs/sandbox.jsonl
exec python3 /yoloai/entrypoint.py "$@"
```

Remove all existing logic from `entrypoint.sh` — UID remapping, network setup, etc. — and move it to `entrypoint.py`.

### 3.2 New file: `sandbox/entrypoint.py` (runs as root)

Replaces the logic currently in `entrypoint.sh`. Appends to `/yoloai/logs/sandbox.jsonl`. Sequence:

1. Log `entrypoint.python_start`
2. UID/GID remapping → log `uid.remap` or `uid.remap_skip`
3. Write credential files → log `secrets.write` per file, or `secrets.skip`
4. Apply iptables default-deny → log `network.isolate`
5. Add allowlist entries → log `network.allow` per domain
6. Apply overlayfs mounts → log `overlay.mount` per path, or `overlay.skip`
7. Run setup commands → log `setup_cmd.start`, then `setup_cmd.done` or `setup_cmd.error` per command
8. `exec gosu yoloai python3 /yoloai/sandbox-setup.py`

Read runtime-config.json for all parameters (same as current entrypoint.sh reads environment variables).

### 3.3 Update `sandbox-setup.py`

- Remove the `sys.stdout`/`sys.stderr` redirect to `log.txt` — no longer needed.
- Open `/yoloai/logs/sandbox.jsonl` in append mode and write structured JSONL for each step:
  - `sandbox.backend_setup`
  - `overlay.git_baseline`
  - `sandbox.tmux_start`
  - `sandbox.agent_launch` (include `agent`, `model` fields)
  - `sandbox.prompt_deliver` (include `method` field) or `sandbox.prompt_skip`
  - `sandbox.monitor_launch`
  - `sandbox.ready`
  - `sandbox.agent_exit` (include `exit_code` field)
- Change tmux `pipe-pane` target from `log.txt` to `/yoloai/logs/agent.log`.

### 3.4 Update `status-monitor.py`

- Write structured JSONL to `/yoloai/logs/monitor.jsonl` (append mode).
- Replace existing log statements with structured entries:
  - `monitor.start` (include `detectors` list field)
  - `detector.result` at `debug` level (include `detector`, `confidence`, `status`)
  - `status.transition` (include `from`, `to`, `detector`)
  - `monitor.exit` (include `reason`)
- Read debug flag from `runtime-config.json` to decide whether to emit `debug`-level entries.

---

## Phase 4: Go CLI Structured Log Calls + `cli.jsonl` Sink

**Files touched:** all `internal/cli/*.go` files that call `slog.*`

### 4.1 Add `event` attribute to slog calls

Every `slog.Info(...)`, `slog.Debug(...)`, etc. call that should appear in `cli.jsonl` needs an `event` attribute added. Follow the `component.action` convention from the taxonomy (e.g. `sandbox.create`, `mount.bind`, `backend.exec`). The event taxonomy for `cli.jsonl` is defined during implementation — use judgment.

Example:
```go
// before
slog.Info("creating sandbox", "name", name, "backend", backend)
// after
slog.Info("creating sandbox", "event", "sandbox.create", "name", name, "backend", backend)
```

### 4.2 Open `cli.jsonl` sink in sandbox subcommands

In each sandbox subcommand's `RunE` (e.g. `runNew`, `runStart`, etc.), after resolving the sandbox name, add:

```go
cliLog, err := openCLIJSONLSink(stateDir)  // opens logs/cli.jsonl for append
if err != nil {
    return err
}
defer cliLog.Close()
cli.AddLogSink(cliLog, slog.LevelDebug)  // all levels, filtered by global --debug flag
```

`openCLIJSONLSink` is a small helper in `logger.go` that opens the file with `O_APPEND|O_CREATE|O_WRONLY`, mode 0600.

---

## Phase 5: `sandbox <name> log` Command Rewrite

**Files touched:** `internal/cli/log.go`

Full rewrite. Read the design spec section "sandbox <name> log command" carefully.

### Key implementation notes

- **Merge-sort (static):** Read all four JSONL files into memory, parse `ts` field, sort by timestamp, emit pretty-printed.
- **Follow mode:** Launch one goroutine per file that tails the file (poll with a ticker or `inotify`/`kqueue`). Send parsed entries to a merge channel. Emit as they arrive (ordering approximate). Auto-exit when all sources are idle and sandbox status transitions to `done`.
- **Pretty-print format:**
  ```
  HH:MM:SS src     LEVL  event-name               message  key=val...
  ```
  - Time: local timezone, `HH:MM:SS`
  - Source: 7 chars, right-padded: `cli    `, `sandbox`, `monitor`, `hooks  `
  - Level: 4 chars uppercase: `INFO`, `WARN`, `ERRO`, `DBUG`
  - Event: 24 chars, right-padded
  - Rest: message then key=val pairs to terminal width (fallback 120). Hard-truncate, no wrap.
- **`--level`:** `debug|info|warn|error` (default `info`). Use `--level` not `--debug`.
- **`--since`:** Accept `5m` (duration) or `14:20:00` (local time today). Convert to UTC for comparison against `ts` field.
- **`--agent` / `--agent-raw`:** Mutually exclusive with structured log flags. `--agent` runs `stripANSI()` over `logs/agent.log`. `--agent-raw` cats it directly.
- **`--raw`:** Emit JSONL lines verbatim, no formatting.
- **`--source`:** Comma-separated subset of `cli,sandbox,monitor,hooks`.

### Flag registration

```go
logCmd.Flags().StringVar(&source, "source", "", "comma-separated sources: cli,sandbox,monitor,hooks")
logCmd.Flags().StringVar(&level, "level", "info", "minimum log level: debug|info|warn|error")
logCmd.Flags().StringVar(&since, "since", "", "show entries since duration or timestamp")
logCmd.Flags().BoolVar(&raw, "raw", false, "emit raw JSONL")
logCmd.Flags().BoolVar(&agentOut, "agent", false, "show agent output (ANSI stripped)")
logCmd.Flags().BoolVar(&agentRaw, "agent-raw", false, "show raw agent terminal stream")
logCmd.Flags().BoolVarP(&follow, "follow", "f", false, "tail log live; auto-exits when sandbox is done")
```

---

## Phase 6: `--bugreport` Global Flag

**Files touched:** `internal/cli/root.go`, `internal/cli/bugreport_writer.go` (new)

### 6.1 Add flag to `root.go`

```go
rootCmd.PersistentFlags().StringVar(&bugreportType, "bugreport", "", "write bug report: safe|unsafe")
```

Validate in `PersistentPreRunE`:
```go
if bugreportType != "" && bugreportType != "safe" && bugreportType != "unsafe" {
    return fmt.Errorf("--bugreport: must be safe or unsafe")
}
```

### 6.2 `PersistentPreRunE` bugreport initialization

When `--bugreport` is set:

1. Generate output filename via `bugReportFilename(time.Now().UTC())`.
2. Open `<filename>.tmp` with `os.OpenFile(..., O_CREATE|O_EXCL|O_WRONLY, 0600)`.
3. Write sections 1–5 immediately (header, command invocation, system, backends, config).
4. Create `liveLogBuffer = &bufferSink{}` and register it as a sink on `globalHandler` (all levels, no filter).
5. Register a top-level `defer` with `recover()` that:
   - Calls `writeBugReportLiveLog(tmp, liveLogBuffer.entries, bugreportType)` (section 13).
   - Calls `writeBugReportExit(tmp, exitCode, runErr, panicked)` (section 14).
   - Closes and renames `<filename>.tmp` → `<filename>`.
   - Prints `Bug report written: <filename>` to stderr.
   - Checks file size; if > 65536 bytes, prints Gist warning to stderr.
   - Re-panics if the original cause was a panic.

For non-sandbox commands, no additional sections are written. The report will contain only sections 1–5, 13, 14.

### 6.3 New file: `internal/cli/bugreport_writer.go`

Contains all section-writing functions shared between the flag mechanism and the `sandbox bugreport` command:

```
writeBugReportHeader(w, version, commit, date, reportType)
writeBugReportCommandInvocation(w, reportType)
writeBugReportSystem(w)
writeBugReportBackends(ctx, w)
writeBugReportConfig(w, reportType)
writeBugReportSandboxDetail(ctx, w, rt, name, reportType)
writeBugReportCLILog(w, name, reportType)
writeBugReportSandboxJSONL(w, name, reportType)
writeBugReportMonitorLog(w, name)
writeBugReportHooksLog(w, name)
writeBugReportAgentOutput(w, name)
writeBugReportTmuxCapture(w, name, backend, stateDir)
writeBugReportLiveLog(w, entries []LogEntry, reportType)
writeBugReportExit(w, code int, err error, panicked bool)
bugReportFilename(t time.Time) (string, error)
backendVersion(ctx context.Context, backend string) string
sanitizeYAMLConfig(content []byte) []byte
sanitizeJSONLFile(path, reportType string, omitEvents []string) ([]byte, error)
sanitizeText(content []byte) []byte
```

#### `sanitizeJSONLFile` details

- Read file line by line.
- Skip lines matching `omitEvents`. Exact match or prefix match if the pattern ends in `.*` (e.g. `"setup_cmd.*"` matches `"setup_cmd.start"`, `"setup_cmd.done"`, `"setup_cmd.error"`).
- For kept lines: parse as JSON, walk all string-valued fields (recursively if nested), apply `sanitizeText()` to each value, re-serialize.
- Malformed lines (not valid JSON): pass through unmodified (preserves partial entries).

#### `sanitizeYAMLConfig` details

- Operate line-by-line on raw YAML text. No parser dependency.
- For each line, if it matches `^\s*(<key>)\s*:` where `<key>` (case-insensitive) contains any of the keyword list, replace the value portion with `[REDACTED]`.
- Sensitive key names: `key`, `token`, `secret`, `password`, `credential`, `passwd`, `pwd`, `auth`, `jwt`, `bearer`, `cert`, `private`, `access`, `encryption`, `saml`, `oauth`, `sso`, `connection`.

#### `sanitizeText` details

Apply patterns in order (first match wins), replacing matched content with `[REDACTED]`:

| Pattern | Regex sketch |
|---------|-------------|
| PEM blocks | `-----BEGIN [A-Z ]+ KEY-----[\s\S]+?-----END [A-Z ]+ KEY-----` |
| Known API key prefixes | `(sk-ant-\|sk-proj-\|ghp_\|ghu_\|gha_\|sk_live_\|sk_test_\|AIzaSy\|pplx-\|gsk_)\S+` |
| AWS access keys | `AKIA[A-Z0-9]{16}` |
| Connection strings | `\w+://[^:@\s]+:[^@\s]+@\S+` |
| JWT tokens | `eyJ[A-Za-z0-9\-_]{10,}\.[A-Za-z0-9\-_]{10,}\.[A-Za-z0-9\-_]{10,}` |
| Long hex strings | `[a-fA-F0-9]{32,}` |
| Base64 strings | `[A-Za-z0-9+/\-_]{40,}={0,2}` |

#### `writeBugReportTmuxCapture` details

For seatbelt backend:
```sh
tmux -S <stateDir>/tmux/tmux.sock capture-pane -p -t main
```
For all other backends:
```sh
tmux capture-pane -p -t main
```
Run with `exec.CommandContext`. If the sandbox is not running or tmux session not found, silently omit the section (do not write an error).

#### `writeBugReportSandboxDetail` `environment.json` handling

In `safe` mode: parse with `encoding/json`, delete `network_allow` and `setup` keys, re-serialize with `json.MarshalIndent`.
In `unsafe` mode: include verbatim.

#### `writeBugReportSandboxDetail` `runtime-config.json` handling

In `safe` mode: parse with `encoding/json`, delete `setup_commands` and `allowed_domains` keys, re-serialize with `json.MarshalIndent`.
In `unsafe` mode: include verbatim.

#### `bugReportFilename` details

```go
func bugReportFilename(t time.Time) (string, error) {
    name := fmt.Sprintf("yoloai-bugreport-%s.md",
        t.UTC().Format("20060102-150405.000"))
    if _, err := os.Stat(name); err == nil {
        return "", fmt.Errorf("file already exists: %s", name)
    }
    return name, nil
}
```

---

## Phase 7: `sandbox <name> bugreport` Command

**Files touched:** `internal/cli/sandbox_bugreport.go` (new), `internal/cli/sandbox_cmd.go`

### 7.1 New file: `internal/cli/sandbox_bugreport.go`

```go
func runSandboxBugReport(cmd *cobra.Command, name string, reportType string,
    version, commit, date string) error {
    // 1. Resolve backend and state dir for the named sandbox.
    // 2. Generate output filename via bugReportFilename(time.Now().UTC()).
    // 3. Open <filename>.tmp with O_CREATE|O_EXCL|O_WRONLY, mode 0600.
    // 4. Write all applicable sections (1, 3-12).
    //    Sections 2, 13, 14 are flag-only.
    // 5. Close and rename temp → final.
    // 6. Print filename to stderr.
    // 7. Check size; warn if > 65536.
}
```

Section order for `sandbox bugreport`:
1. Header
3. System
4. Backends (skip backends already known unavailable — check meta.json)
5. Configuration (sanitized in safe mode)
6. Sandbox detail
7. `cli.jsonl`
8. `sandbox.jsonl`
9. `monitor.jsonl`
10. `agent-hooks.jsonl`
11. Agent output (unsafe only)
12. tmux screen capture (unsafe only)

(Sections 2, 13, 14 are flag-only — omitted here.)

### 7.2 Wire into `sandbox_cmd.go`

In `sandboxSubcmds` map:
```go
sandboxSubcmds["bugreport"] = true
```

In `sandboxDispatch()` switch:
```go
case "bugreport":
    if len(args) < 3 {
        return fmt.Errorf("usage: yoloai sandbox <name> bugreport safe|unsafe")
    }
    return runSandboxBugReport(cmd, name, args[2], version, commit, date)
```

`version`, `commit`, `date` are passed into `sandboxDispatch()` via a closure or package-level vars — check how other commands in the switch handle this.

---

## Testing

After each phase, run `make check`. Additional test notes:

- **Phase 1:** Unit tests for `multiSinkHandler` fan-out, level filtering, `slog.SetDefault` integration.
- **Phase 5:** Test `sanitizeYAMLConfig` and `sanitizeText` against all pattern types. Test `sanitizeJSONLFile` with `omitEvents` including `setup_cmd.*` prefix matching.
- **Phase 6/7:** Integration test: create a sandbox, run a command with `--bugreport safe`, verify the output file exists, is valid Markdown, and does not contain known-sensitive patterns.
- **Existing test:** `TestStripANSI` in `internal/cli/ansi_test.go` already covers the grammar-based ANSI stripper (Phase 3 prerequisite — already done).

---

## Already Done

- `internal/cli/ansi.go`: grammar-based VT100/ANSI stripper (CSI, OSC, nF, 2-char sequences).
- `internal/cli/ansi_test.go`: test cases including `>` params, ST terminator, ESC M/c, cursor positioning, bracketed paste.
- `docs/dev/plans/TODO.md`: added `yoloai apply should pull new git tags`.
