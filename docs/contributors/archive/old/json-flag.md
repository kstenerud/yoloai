# Global `--json` Flag for Machine-Readable Output

**Status: Implemented.**

## Context

yoloai needs machine-readable output for scripting, CI pipelines, and tool integration. The [CLI-STANDARD.md](../CLI-STANDARD.md) already mentions `--json` as a convention. This adds a global `--json` persistent flag that switches all command output from human-readable text to structured JSON.

## Design Decisions

**No envelope wrapper.** Commands output their domain object directly (like `gh`, `docker`, `kubectl`). Exit codes signal success/failure. Errors go to stderr as JSON when `--json` is active.

**All commands get JSON handling.** Data queries output structured JSON. Action commands output result objects. Interactive commands (`attach`, `exec`) reject `--json` with an error.

**Confirmations require `--yes`.** Commands with interactive prompts (`destroy`, `apply`, `system prune`) error if `--json` is set without `--yes`.

## New File

### `internal/cli/json.go`

Helper functions used by all commands:

- `jsonEnabled(cmd) bool` — reads `--json` persistent flag from root
- `writeJSON(w, v) error` — marshals v as indented JSON + newline to w
- `writeJSONError(w, err)` — writes `{"error": "message"}` to w (for stderr)
- `errJSONNotSupported(cmdName) error` — error for interactive commands
- `requireYesForJSON(cmd) error` — errors if `--json` without `--yes`

## Changes by File

### `internal/cli/root.go`

- Add `--json` persistent flag in `newRootCmd()`
- In `Execute()`, format errors as JSON to stderr when `--json` is active

### `internal/sandbox/inspect.go` — Add json tags to `Info`

```go
type Info struct {
    Meta        *Meta  `json:"meta"`
    Status      Status `json:"status"`
    ContainerID string `json:"container_id,omitempty"`
    HasChanges  string `json:"has_changes"`
    DiskUsage   string `json:"disk_usage"`
}
```

### `internal/sandbox/apply.go` — Add json tags to `CommitInfo`

```go
type CommitInfo struct {
    SHA     string `json:"sha"`
    Subject string `json:"subject"`
}
```

### `internal/sandbox/diff.go` — Add json tags to `DiffResult`, `CommitInfoWithStat`

```go
type DiffResult struct {
    Output  string `json:"output"`
    WorkDir string `json:"workdir"`
    Mode    string `json:"mode"`
    Empty   bool   `json:"empty"`
}
type CommitInfoWithStat struct {
    CommitInfo
    Stat string `json:"stat,omitempty"`
}
```

### Data Query Commands

Each gets an early `if jsonEnabled(cmd)` check that writes JSON and returns, leaving existing human-readable code untouched.

**`internal/cli/list.go`** — Output `[]*sandbox.Info` array. Empty list → `[]`.

**`internal/cli/sandbox_info.go`** (`sandbox info`) — Output `Info` object with added `prompt_preview` field.

**`internal/cli/diff.go`** — Output `DiffResult` for plain/stat diff, structured `{commits, has_uncommitted_changes}` for `--log`. Bypasses pager. Suppress `agentRunningWarning()` when JSON.

**`internal/cli/log.go`** — Read file, output `{"content": "..."}`. Bypasses pager.

**`internal/cli/commands.go`** (`version`) — Output `{"version", "commit", "date"}`.

**`internal/cli/system_info.go`** (`system info`) — Output structured JSON: `{"version", "commit", "date", "config_path", "data_dir", "sandboxes_dir", "disk_usage", "backends": [...]}`.

**`internal/cli/info.go`** (`system backends`, `system agents`) — List: array of `{name, description, available, note}`. Detail: single object with all fields.

**`internal/cli/config.go`** (`config get`) — Full config: parse YAML to `map[string]any`, output as JSON. Single key: `{"key": "...", "value": "..."}`.

**`internal/cli/profile.go`** (`profile list`) — Output JSON array of `{name, extends, image, agent}`.

### Action Commands

Each outputs a result object on success.

**`internal/cli/stop.go`** — Collect per-sandbox results into `[{"name", "action": "stopped"}]` or `[{"name", "error": "..."}]`. Write array at end.

**`internal/cli/destroy.go`** — Same multi-result pattern. Add `requireYesForJSON()` check.

**`internal/cli/start.go`** — Output `{"name", "action": "started"}`. Error if `--attach` + `--json`.

**`internal/cli/reset.go`** — Output `{"name", "action": "reset"}`.

**`internal/cli/restart.go`** — Output `{"name", "action": "restarted"}`. Error if `--attach` + `--json`.

**`internal/cli/commands.go`** (`new`) — Output sandbox meta JSON. Error if `--attach` + `--json`. Pass `io.Discard` as Manager output writer to suppress progress.

**`internal/cli/system.go`** (`system build`) — Output `{"action": "built"}`. Pass `io.Discard` for build output.

**`internal/cli/apply.go`** — `requireYesForJSON()`. Define result type `{"target", "commits_applied", "wip_applied"}`. Suppress human-readable progress. Most complex command — multiple code paths (default, squash, selective, patches export) each need JSON result.

**`internal/cli/config.go`** (`config set`) — Output `{"key", "value", "action": "set"}`.

**`internal/cli/config.go`** (`config reset`) — Output `{"key", "action": "reset"}`.

**`internal/cli/profile.go`** (`profile create`) — Output `{"name", "path", "action": "created"}`.

**`internal/cli/profile.go`** (`profile delete`) — Output `{"name", "action": "deleted"}`.

**`internal/cli/network_allow.go`** (`sandbox network-allow`) — Output `{"name", "domains_added": [...], "live": true/false}`.

**`internal/cli/system_prune.go`** (`system prune`) — `requireYesForJSON()`. Output `{"items": [...], "dry_run": bool}`. Fixed key regardless of mode; `dry_run` field distinguishes scan from removal.

### Interactive Command Guards

**`internal/cli/attach.go`**, **`internal/cli/exec.go`** — Add `if jsonEnabled(cmd) { return errJSONNotSupported("...") }` at top of RunE.

### Cross-Cutting

- `agentRunningWarning()` in `diff.go`: skip when `jsonEnabled(cmd)`
- Pager bypass: already handled — JSON paths write directly, never call `RunPager()`
- Manager progress suppression: when `--json`, pass `io.Discard` as output writer to `NewManager()` in relevant commands

## Documentation Updates

- `docs/contributors/CLI-STANDARD.md` — Expand `--json` line into a section documenting: flag behavior, error format, interactive command rejection, `--yes` requirement
- `docs/GUIDE.md` (at `../../GUIDE.md`) — Add `--json` to global flags table, add usage examples
- `docs/contributors/architecture/README.md` (at `../ARCHITECTURE.md`) — Add `json.go` to file index

## Implementation Order

1. Foundation: `json.go` helpers + `root.go` flag + json tags on sandbox types
2. Interactive guards: `attach`, `exec` (trivial, immediate safety)
3. Data queries: `list`, `sandbox info`, `version`, `system info`, `system backends/agents` (highest value)
4. More data queries: `diff`, `log`, `config get`, `profile list`
5. Simple actions: `stop`, `start`, `reset`, `restart`, `system build`, `config set`, `config reset`, `profile create`, `profile delete`
6. Complex actions: `new`, `destroy`, `apply`, `system prune`, `sandbox network-allow`
7. Docs + final `make check`

## Verification

- `make check` passes at each step
- `yoloai list --json` outputs valid JSON array
- `yoloai list --json | jq .` parses cleanly
- `yoloai sandbox info <name> --json | jq .status` extracts fields
- `yoloai attach --json` errors with "not supported for interactive command"
- `yoloai destroy <name> --json` errors requiring `--yes`
- `yoloai destroy <name> --json --yes` outputs JSON result
- `yoloai diff <name> --json` outputs JSON (no pager)
- Errors: `yoloai sandbox info nonexistent --json` outputs JSON error to stderr, exits 1
- `yoloai system prune --json` errors requiring `--yes`
- `yoloai system prune --json --yes` outputs JSON result
- `json_test.go` unit tests for all helpers
