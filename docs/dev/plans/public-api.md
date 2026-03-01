# Public Go API for yoloai

**Status: Future — implement after all internal features are complete.**

## Context

yoloai is currently internal-only — all Go packages live in `internal/`. The CLI binary is the sole consumer. We want a public Go API so external Go programs can create, manage, inspect, diff, and apply sandboxes programmatically. The API is consumer-focused (hide backend/implementation details) with room for future extensibility (custom backends).

Design docs (`docs/design/commands.md`, `docs/design/config.md`) and `docs/dev/plans/TODO.md` describe planned features that the API surface must accommodate even if their internals aren't implemented yet.

## Package Location

Root of the module (`package yoloai` in files at the repo root). Import as:

```go
import "github.com/kstenerud/yoloai"
```

Used as `yoloai.New(...)`, `yoloai.StatusRunning`, etc. No `pkg/` subdirectory — idiomatic modern Go. The `cmd/yoloai/` binary continues using `internal/` packages directly (CLI migration is a future effort).

## Client

```go
// Client manages sandbox lifecycles. Thread-safe after construction.
type Client struct { /* runtime, manager, logger, output */ }

func New(opts ...Option) (*Client, error)
func (c *Client) Close() error
func (c *Client) EnsureSetup(ctx context.Context) error  // non-interactive; idempotent
```

**Options:**
```go
func WithBackend(name string) Option     // "docker", "tart", "seatbelt" (default: from config)
func WithLogger(l *slog.Logger) Option   // default: slog.Default()
func WithOutput(w io.Writer) Option      // progress/status output (default: io.Discard)
```

`New` reads config, creates the appropriate runtime backend. No public `Runtime` interface in v1 — just `WithBackend(name)`. Future: `WithRuntime(rt Runtime)` for custom backends.

`EnsureSetup` builds base image if needed. `Create` does NOT auto-call it — consumers must call explicitly. This is intentional: setup is slow on first run, and the consumer should control when it happens.

## Lifecycle

```go
func (c *Client) Create(ctx context.Context, opts CreateOptions) (*Sandbox, error)
func (c *Client) Start(ctx context.Context, name string, opts ...StartOption) error
func (c *Client) Stop(ctx context.Context, name string) error
func (c *Client) Destroy(ctx context.Context, name string) error
func (c *Client) Reset(ctx context.Context, name string, opts ...ResetOption) error
func (c *Client) Restart(ctx context.Context, name string) error
```

No interactive confirmations. `Destroy` destroys. `Create` with duplicate name returns `ErrAlreadyExists` (use `CreateOptions.Replace`).

**CreateOptions** — parsed, consumer-friendly (not raw CLI strings):

```go
type CreateOptions struct {
    Name        string          // required
    Workdir     string          // host path (required unless profile provides one)
    Mode        string          // "copy" (default) or "rw"
    MountPath   string          // custom mount point (default: mirrors host path)
    Agent       string          // "claude", "gemini", "codex", "aider", "opencode", etc.
    Model       string          // model name or alias
    Profile     string          // profile name
    Prompt      string          // prompt text
    PromptFile  string          // path to prompt file (mutually exclusive with Prompt)
    Network     NetworkOptions
    Ports       []string        // "host:container" pairs
    AuxDirs     []AuxDir        // additional directories
    Mounts      []string        // extra bind mounts ("host:container[:ro]")
    Replace     bool            // destroy existing sandbox with same name
    NoStart     bool            // create without starting
    Passthrough []string        // args passed to agent CLI after --
    CPUs        string          // CPU limit
    Memory      string          // memory limit
    Debug       bool            // enable entrypoint debug logging
    Version     string          // yoloai version for meta.json (optional)
}

type AuxDir struct {
    HostPath  string
    MountPath string  // custom mount point (default: mirrors host path)
    Mode      string  // "ro" (default), "copy", "rw"
    Force     bool    // override dangerous directory detection
}

type NetworkOptions struct {
    Mode  string   // "" (default/host), "none", "isolated"
    Allow []string // domains to allow in isolated mode
}
```

**StartOption** — functional options for planned `--resume`:
```go
func WithResume() StartOption  // re-feed original prompt with continuation preamble
```

**ResetOption:**
```go
func WithClean() ResetOption      // also wipe agent-state directory
func WithNoPrompt() ResetOption   // skip re-sending prompt after reset
func WithNoRestart() ResetOption  // keep agent running, reset workspace in-place
```

## Inspection

```go
func (c *Client) Get(ctx context.Context, name string) (*SandboxInfo, error)
func (c *Client) List(ctx context.Context, opts ...ListOption) ([]*SandboxInfo, error)
```

**ListOption** — for planned `--running`/`--stopped` filters:
```go
func WithStatusFilter(statuses ...Status) ListOption
```

**SandboxInfo** — clean public type:
```go
type SandboxInfo struct {
    Name         string
    Status       Status
    ContainerID  string
    CreatedAt    time.Time
    Backend      string
    Profile      string
    Agent        string
    Model        string
    Workdir      DirInfo
    Directories  []DirInfo
    NetworkMode  string
    NetworkAllow []string
    Ports        []string
    HasChanges   *bool    // nil = unknown, true = yes, false = no
    DiskUsage    int64    // bytes
}

type DirInfo struct {
    HostPath  string
    MountPath string
    Mode      string  // "ro", "copy", "rw"
}

type Status string
const (
    StatusRunning Status = "running"
    StatusDone    Status = "done"
    StatusFailed  Status = "failed"
    StatusStopped Status = "stopped"
    StatusRemoved Status = "removed"
    StatusBroken  Status = "broken"
)
```

Note: `HasChanges` is `*bool` (tri-state) because the internal type uses `"yes"/"no"/"-"` where `"-"` means "unknown/not applicable" (e.g., `:rw` dirs, or when detection fails). `DiskUsage` is raw bytes — consumers format as needed.

## Changes

```go
func (c *Client) Diff(ctx context.Context, name string, opts ...DiffOption) ([]*DiffResult, error)
func (c *Client) Commits(ctx context.Context, name string) ([]CommitInfo, error)
func (c *Client) CommitsWithStats(ctx context.Context, name string) ([]CommitInfoWithStat, error)
```

**DiffOption:**
```go
func WithStat() DiffOption              // summary only
func WithPaths(paths ...string) DiffOption  // filter to paths
func WithRef(ref string) DiffOption     // specific commit or range
```

`Diff` returns a slice (one per `:copy`/`:rw` directory). Single-workdir sandboxes return a 1-element slice.

```go
type DiffResult struct {
    Output  string  // diff text or stat summary
    WorkDir string  // directory that was diffed
    Mode    string  // "copy" or "rw"
    Empty   bool    // true if no changes
}

type CommitInfo struct {
    SHA     string
    Subject string
}

type CommitInfoWithStat struct {
    CommitInfo
    Stat string  // per-commit file change summary
}
```

## Apply

```go
func (c *Client) Apply(ctx context.Context, name string, opts ...ApplyOption) (*ApplyResult, error)
```

**ApplyOption:**
```go
func WithSquash() ApplyOption                // flatten to single patch
func WithRefs(refs ...string) ApplyOption    // cherry-pick specific commits
func WithNoWIP() ApplyOption                 // skip uncommitted changes
func WithForce() ApplyOption                 // proceed despite dirty host repo
func WithPatchesDir(dir string) ApplyOption  // export patches instead of applying
func WithApplyPaths(paths ...string) ApplyOption  // filter to paths
```

```go
type ApplyResult struct {
    Target         string  // host directory changes were applied to
    CommitsApplied int
    WIPApplied     bool
    Method         string  // "format-patch", "squash", "selective", "patches-export"
}
```

Returns result with zero counts when there are no changes (not an error).

## Operations

```go
func (c *Client) Exec(ctx context.Context, name string, cmd []string) (*ExecResult, error)
func (c *Client) NetworkAllow(ctx context.Context, name string, domains ...string) (*NetworkAllowResult, error)
func (c *Client) Log(ctx context.Context, name string) (string, error)
```

```go
type ExecResult struct {
    Stdout   string
    ExitCode int
}

type NetworkAllowResult struct {
    Name         string
    DomainsAdded []string
    Live         bool  // true if rules were live-patched into running container
}
```

## System

```go
func (c *Client) Build(ctx context.Context, opts ...BuildOption) error
func (c *Client) Prune(ctx context.Context, opts ...PruneOption) (*PruneResult, error)
```

**BuildOption:**
```go
func WithProfile(name string) BuildOption  // build specific profile image
func WithAllProfiles() BuildOption         // build base + all profiles
// default (no options): build base image only
```

**PruneOption:**
```go
func WithDryRun() PruneOption
```

```go
type PruneResult struct {
    Items  []PruneItem
    DryRun bool
}

type PruneItem struct {
    Kind string  // "container", "vm", "temp_dir"
    Name string
}
```

## Config & Profiles

```go
func (c *Client) GetConfig(ctx context.Context) (map[string]any, error)
func (c *Client) GetConfigValue(ctx context.Context, key string) (string, error)
func (c *Client) SetConfig(ctx context.Context, key, value string) error
func (c *Client) ResetConfig(ctx context.Context, key string) error

func (c *Client) ListProfiles(ctx context.Context) ([]ProfileSummary, error)
func (c *Client) CreateProfile(ctx context.Context, name string) (string, error)  // returns path
func (c *Client) DeleteProfile(ctx context.Context, name string) error
```

```go
type ProfileSummary struct {
    Name    string
    Extends string
    Image   string
    Agent   string
}
```

## Errors

```go
var (
    ErrNotFound      = errors.New("sandbox not found")
    ErrAlreadyExists = errors.New("sandbox already exists")
)
```

Sentinel errors for `errors.Is()`. Other errors are returned as-is from internal packages (e.g., backend connection failures). Future: add more sentinels as patterns emerge.

## File Layout

New files at repo root:

| File | Contents |
|------|----------|
| `doc.go` | Package documentation |
| `client.go` | `Client`, `New`, `Close`, `EnsureSetup`, `Option` types |
| `types.go` | All public types: `SandboxInfo`, `DirInfo`, `Status`, `CreateOptions`, `AuxDir`, `NetworkOptions`, `DiffResult`, `CommitInfo`, `ApplyResult`, `ExecResult`, `NetworkAllowResult`, `PruneResult`, `ProfileSummary`, errors |
| `lifecycle.go` | `Create`, `Start`, `Stop`, `Destroy`, `Reset`, `Restart` + option types |
| `inspect.go` | `Get`, `List` + `ListOption` |
| `diff.go` | `Diff`, `Commits`, `CommitsWithStats` + `DiffOption` |
| `apply.go` | `Apply` + `ApplyOption` |
| `operations.go` | `Exec`, `NetworkAllow`, `Log` |
| `system.go` | `Build`, `Prune` + option types |
| `config.go` | `GetConfig`, `GetConfigValue`, `SetConfig`, `ResetConfig`, `ListProfiles`, `CreateProfile`, `DeleteProfile` |

Each file maps types between public API and `internal/sandbox`, `internal/runtime`, `internal/agent`.

## Internal Wiring

`Client` internally holds:
- `runtime.Runtime` (created by `New` based on backend)
- `*sandbox.Manager` (created with `io.Discard` for input, `opts.Output` for progress)
- `backend` string, `logger`, `output` writer

Methods delegate to `internal/sandbox` functions:
- `Create` → maps `CreateOptions` → `sandbox.CreateOptions` (sets `Yes: true` to skip confirmations), calls `manager.Create()`
- `Get` → calls `sandbox.InspectSandbox()`, maps `*sandbox.Info` → `*SandboxInfo`
- `Diff` → calls `sandbox.GenerateMultiDiff()` or `sandbox.GenerateDiff()`, maps results
- `Apply` → calls the same functions as `internal/cli/apply.go` but without human-readable output
- `Exec` → calls `runtime.Exec()`, maps `runtime.ExecResult` → `ExecResult`
- Config/Profile → delegates to `sandbox.GetEffectiveConfig()`, `sandbox.LoadProfile()`, etc.

## Design Decisions

**No public `Runtime` interface in v1.** The internal `runtime.Runtime` has 12 methods including CLI-specific ones (`InteractiveExec`, `DiagHint`). Stabilizing it as public API is premature. Extensibility hook deferred to v2: expose a simplified interface + `WithRuntime()` option.

**No CLI migration.** The CLI continues using `internal/` directly. Migrating the CLI to use the public API is a large refactor with no user-facing benefit. The public API and CLI can coexist.

**Non-interactive by default.** The library never prompts stdin. Confirmations are the consumer's responsibility. Manager created with `io.Discard` input, `Yes: true` on operations.

**Functional options for future compatibility.** Methods that may gain options use `...Option` pattern (`Start`, `Reset`, `List`, `Diff`, `Apply`, `Build`, `Prune`). Methods unlikely to change use positional args (`Stop`, `Destroy`, `Log`).

**Tri-state `HasChanges`.** `*bool` instead of `bool` because the internal system has three states: yes, no, and unknown. Consumers check `info.HasChanges != nil && *info.HasChanges`.

**`Diff` returns `[]*DiffResult`** (not single result). Multi-directory sandboxes produce one result per `:copy`/`:rw` dir. Single-workdir sandboxes return a 1-element slice. Consistent API regardless of sandbox configuration.

**Planned features accounted for:**
- `--resume` on `Start` → `WithResume()` StartOption
- List filters → `WithStatusFilter()` ListOption
- `system build` with profile/`--all` → `WithProfile()`, `WithAllProfiles()` BuildOption
- Overlayfs, `auto_commit_interval`, `agent_files`, recipes → config/profile concerns, not API surface changes. They work through `CreateOptions.Profile` or config settings.
- Extensions → CLI-only feature. Library consumers write Go code instead.

## Implementation Order

1. **Foundation**: `doc.go`, `client.go`, `types.go`, errors
2. **Lifecycle**: `lifecycle.go` — Create, Start, Stop, Destroy, Reset, Restart
3. **Inspection**: `inspect.go` — Get, List
4. **Changes**: `diff.go` — Diff, Commits, CommitsWithStats
5. **Apply**: `apply.go`
6. **Operations**: `operations.go` — Exec, NetworkAllow, Log
7. **System**: `system.go` — Build, Prune
8. **Config**: `config.go` — config get/set, profile management
9. **Tests**: Unit tests for type mapping; integration test examples
10. **Docs**: Update ARCHITECTURE.md, add API section to GUIDE.md

Each step: implement, `make check`, commit.

## Verification

- `make check` passes at each step
- Unit tests cover type mapping (internal → public)
- Example program: create sandbox, inspect, diff, destroy — compiles and type-checks
- All public types have json tags for serialization
- Existing CLI continues working (no internal changes)
