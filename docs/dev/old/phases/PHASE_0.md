# Phase 0: Project Scaffold

## Goal

Compilable Go project with a Cobra CLI that prints help text for all MVP commands. No Docker, no functionality — just the skeleton.

## Prerequisites

- Go 1.22+ installed
- `golangci-lint` installed (for verification)

## Files to Create/Modify

| File | Description |
|------|-------------|
| `go.mod` | Module definition: `github.com/kstenerud/yoloai`, Go 1.22 |
| `Makefile` | `build`, `test`, `lint` targets |
| `.golangci.yml` | Linter config per [CODING-STANDARD.md](../../CODING-STANDARD.md) |
| `cmd/yoloai/main.go` | Thin entry point: signal handling, root command execution, exit code |
| `internal/cli/root.go` | Root Cobra command, global flags, error-to-exit-code mapping |
| `internal/cli/commands.go` | All stub subcommand registrations |

## Types and Signatures

### `cmd/yoloai/main.go`

```go
package main

// version, commit, date are set via ldflags at build time.
var (
    version = "dev"
    commit  = "none"
    date    = "unknown"
)

func main()
// Sets up signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM),
// calls cli.Execute(ctx, version, commit, date), and os.Exit with the returned code.
```

### `internal/cli/root.go`

```go
package cli

import "github.com/spf13/cobra"

// Execute runs the root command and returns the exit code.
func Execute(ctx context.Context, version, commit, date string) int

// newRootCmd creates the root Cobra command with all subcommands registered.
func newRootCmd(version, commit, date string) *cobra.Command
```

Root command configuration:
- `Use: "yoloai"`
- `Short: "Sandboxed AI coding agent runner"`
- `Long`: Multi-sentence description of what yoloAI does
- `SilenceErrors: true`
- `SilenceUsage: true`

Global flags (on root `PersistentFlags()`):
- `--verbose` / `-v`: `CountP` (not `BoolP`) — supports stacking (`-v`, `-vv`)
- `--quiet` / `-q`: `CountP` — supports stacking (`-q`, `-qq`)
- `--no-color`: `Bool`

Error-to-exit-code mapping in `Execute`:
- `nil` → 0
- `UsageError` → 2
- `ConfigError` → 3
- anything else → 1

Error types (defined inline in root.go for now, will move to `internal/sandbox/errors.go` in Phase 4a):

```go
// UsageError indicates bad arguments or missing required args (exit code 2).
type UsageError struct {
    Err error
}
func (err *UsageError) Error() string
func (err *UsageError) Unwrap() error

// ConfigError indicates a configuration problem (exit code 3).
type ConfigError struct {
    Err error
}
func (err *ConfigError) Error() string
func (err *ConfigError) Unwrap() error
```

Error message format: `yoloai: <lowercase message>` (printed to stderr).

### `internal/cli/commands.go`

```go
package cli

// registerCommands adds all subcommands to the root command.
func registerCommands(root *cobra.Command, version, commit, date string)
```

Each subcommand is created via a small factory function in `commands.go`. Each is a `&cobra.Command{...}` with:
- `Use` — command name with positional arg placeholders
- `Short` — one-line description
- `Args` — Cobra arg validation (e.g., `cobra.ExactArgs(1)`, `cobra.MinimumNArgs(1)`)
- `RunE` — returns `fmt.Errorf("not implemented")` (placeholder)

### Subcommand Definitions

| Command | Use | Short | Args |
|---------|-----|-------|------|
| `build` | `build [profile]` | `Build or rebuild Docker image(s)` | `cobra.MaximumNArgs(1)` |
| `new` | `new [flags] <name> [<workdir>]` | `Create and start a sandbox` | `cobra.RangeArgs(1, 2)` |
| `attach` | `attach <name>` | `Attach to a sandbox's tmux session` | `cobra.ExactArgs(1)` |
| `show` | `show <name>` | `Show sandbox configuration and state` | `cobra.ExactArgs(1)` |
| `diff` | `diff <name>` | `Show changes the agent made` | `cobra.ExactArgs(1)` |
| `apply` | `apply <name>` | `Copy changes back to original directories` | `cobra.MinimumNArgs(1)` |
| `list` | `list` | `List sandboxes and their status` | `cobra.NoArgs` |
| `log` | `log <name>` | `Show sandbox session log` | `cobra.ExactArgs(1)` |
| `exec` | `exec <name> <command> [args...]` | `Run a command inside a sandbox` | `cobra.MinimumNArgs(2)` |
| `stop` | `stop <name>...` | `Stop sandboxes (preserving state)` | `cobra.MinimumNArgs(1)` |
| `start` | `start <name>` | `Start a stopped sandbox` | `cobra.ExactArgs(1)` |
| `destroy` | `destroy <name>...` | `Stop and remove sandboxes` | `cobra.MinimumNArgs(1)` |
| `reset` | `reset <name>` | `Re-copy workdir and reset git baseline` | `cobra.ExactArgs(1)` |
| `completion` | `completion [bash\|zsh\|fish\|powershell]` | `Generate shell completion script` | `cobra.ExactArgs(1)` |
| `version` | `version` | `Show version information` | `cobra.NoArgs` |

Note on `new` args validation: `cobra.RangeArgs(1, 2)` is a placeholder — Phase 4b will handle `--` passthrough via `ArgsLenAtDash()` which requires `cobra.ArbitraryArgs` with custom validation. For Phase 0 the basic range check is sufficient.

The `version` command prints: `yoloai version <version> (commit: <commit>, built: <date>)`.

## Implementation Steps

1. **Initialize Go module:**
   ```
   go mod init github.com/kstenerud/yoloai
   ```
   Edit `go.mod` to set Go version to 1.22.

2. **Add Cobra dependency:**
   ```
   go get github.com/spf13/cobra
   ```

3. **Create `cmd/yoloai/main.go`:**
   - `signal.NotifyContext` for SIGINT/SIGTERM
   - Call `cli.Execute(ctx, version, commit, date)`
   - `os.Exit` with returned code

4. **Create `internal/cli/root.go`:**
   - `UsageError` and `ConfigError` types
   - `newRootCmd` — creates root command, calls `registerCommands`, adds global flags
   - `Execute` — runs `rootCmd.ExecuteContext(ctx)`, maps errors to exit codes, prints errors to stderr in `yoloai: <message>` format

5. **Create `internal/cli/commands.go`:**
   - `registerCommands` — creates and adds all 15 stub subcommands to root
   - `version` command has a real implementation (prints version info)
   - All other commands return `fmt.Errorf("not implemented")`

6. **Create `Makefile`:**
   ```makefile
   BINARY := yoloai
   VERSION ?= dev
   COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
   DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
   LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

   .PHONY: build test lint clean

   build:
   	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/yoloai

   test:
   	go test ./...

   lint:
   	golangci-lint run ./...

   clean:
   	rm -f $(BINARY)
   ```

7. **Create `.golangci.yml`:**
   ```yaml
   linters:
     enable:
       - errcheck
       - govet
       - staticcheck
       - unused
       - ineffassign
       - gosec
       - errorlint
       - revive
       - gocritic
       - sloglint
     disable:
       - exhaustruct
       - varnamelen

   run:
     timeout: 2m
   ```

8. **Run `go mod tidy`** to clean up dependencies.

## Tests

No unit tests in Phase 0 — the codebase is pure boilerplate with no logic to test. The `version` command is the only real implementation, and it's trivially verified via the manual checks below.

Testing infrastructure (testify, table-driven patterns) will be established in Phase 1 alongside the first testable logic (caret encoding, meta.json serialization, dir arg parsing).

## Verification

Run these commands to confirm Phase 0 is complete:

```bash
# Must compile without errors
go build ./...

# Must produce a binary
make build

# Must show all 15 subcommands in help output
./yoloai --help

# Must show global flags (--verbose, --quiet, --no-color)
./yoloai --help | grep -E '(verbose|quiet|no-color)'

# Each subcommand must have help
./yoloai new --help
./yoloai build --help

# Version command must work
./yoloai version

# Linter must pass
make lint

# Tests must pass (no tests yet, but must not error)
make test

# Unimplemented commands must exit with code 1
./yoloai list; echo "exit code: $?"
# Expected: yoloai: not implemented
# Expected: exit code: 1
```
