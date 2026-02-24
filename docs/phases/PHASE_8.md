# Phase 8: Polish and Integration

## Goal

Complete the MVP with shell completion, `YOLOAI_SANDBOX` env var support, and an end-to-end integration test. Version command and ldflags are already implemented — this phase wires the remaining stubs and adds the convenience features that make the CLI feel finished.

## Prerequisites

- Phase 7 complete (lifecycle commands)
- Docker daemon running for integration test
- All existing tests passing

## Files to Create

| File | Description |
|------|-------------|
| `internal/cli/envname.go` | `resolveName(cmd, args)` helper for `YOLOAI_SANDBOX` env var fallback |
| `internal/cli/envname_test.go` | Tests for `resolveName` (explicit arg, env fallback, neither) |
| `internal/sandbox/integration_test.go` | End-to-end integration test (`//go:build integration`) |

## Files to Modify

| File | Change |
|------|--------|
| `internal/cli/commands.go` | Replace `newCompletionCmd` stub with Cobra shell completion generators |
| `internal/cli/attach.go` | Use `resolveName` for `YOLOAI_SANDBOX` support |
| `internal/cli/show.go` | Use `resolveName` for `YOLOAI_SANDBOX` support |
| `internal/cli/log.go` | Use `resolveName` for `YOLOAI_SANDBOX` support |
| `internal/cli/diff.go` | Use `resolveName` for `YOLOAI_SANDBOX` support |
| `internal/cli/apply.go` | Use `resolveName` for `YOLOAI_SANDBOX` support |
| `internal/cli/exec.go` | Use `resolveName` for `YOLOAI_SANDBOX` support |
| `internal/cli/start.go` | Use `resolveName` for `YOLOAI_SANDBOX` support |
| `internal/cli/stop.go` | Use `resolveName` for `YOLOAI_SANDBOX` support |
| `internal/cli/destroy.go` | Use `resolveName` for `YOLOAI_SANDBOX` support |
| `internal/cli/reset.go` | Use `resolveName` for `YOLOAI_SANDBOX` support |
| `Makefile` | Add `integration` target for `go test -tags=integration` |

## Types and Signatures

### `internal/cli/envname.go`

```go
package cli

import "github.com/spf13/cobra"

// EnvSandboxName is the environment variable used as default sandbox name.
const EnvSandboxName = "YOLOAI_SANDBOX"

// resolveName extracts the sandbox name from positional args, falling back
// to YOLOAI_SANDBOX if no name argument was provided.
// Returns the name and the remaining args (excluding the name).
// Returns a UsageError if no name is available from either source.
func resolveName(cmd *cobra.Command, args []string) (name string, rest []string, err error)
```

### `internal/cli/envname_test.go`

```go
package cli

func TestResolveName_ExplicitArg(t *testing.T)
// Args=["my-sandbox"], env unset → returns "my-sandbox", rest=[]

func TestResolveName_EnvFallback(t *testing.T)
// Args=[], env=YOLOAI_SANDBOX="env-sandbox" → returns "env-sandbox", rest=[]

func TestResolveName_ExplicitOverridesEnv(t *testing.T)
// Args=["explicit"], env=YOLOAI_SANDBOX="env-sandbox" → returns "explicit", rest=[]

func TestResolveName_NeitherSet(t *testing.T)
// Args=[], env unset → returns UsageError

func TestResolveName_ExtraArgs(t *testing.T)
// Args=["my-sandbox", "extra1", "extra2"] → returns "my-sandbox", rest=["extra1", "extra2"]
```

### `internal/cli/commands.go` — Completion

```go
// newCompletionCmd returns the completion command using Cobra's built-in generators.
func newCompletionCmd() *cobra.Command
```

### `internal/sandbox/integration_test.go`

```go
//go:build integration

package sandbox

func TestIntegration_FullLifecycle(t *testing.T)
// Create temp dir → create sandbox → wait for agent → diff → apply → destroy → verify
```

## Design Decisions

### 1. Centralized `resolveName` helper

Rather than duplicating `YOLOAI_SANDBOX` logic in each command, a single `resolveName(cmd, args)` function handles the precedence rule: explicit arg > env var > error. Each command calls this once at the top of its `RunE`. This eliminates 10 copies of the same logic and makes the precedence rule testable in one place.

### 2. `resolveName` returns remaining args

Commands like `diff`, `apply`, and `exec` have args beyond the sandbox name. `resolveName` returns `(name, rest, err)` where `rest` is everything after the name. This lets commands with `-- <path>...` or `<command> [args...]` patterns continue to work naturally.

### 3. Args validation changes to `ArbitraryArgs`

Commands that currently use `cobra.ExactArgs(1)` must switch to `cobra.ArbitraryArgs` (or `cobra.MinimumNArgs(0)`) to allow the env var fallback case where no positional args are provided. Validation moves into `resolveName` which returns a `UsageError` when neither arg nor env var is set.

### 4. Completion uses Cobra's built-in generators

Cobra provides `GenBashCompletionV2`, `GenZshCompletion`, `GenFishCompletion`, and `GenPowerShellCompletion` out of the box. The completion command dispatches to the correct generator based on the shell argument. No custom completion logic is needed for MVP — Cobra auto-completes subcommands and flags from the command tree.

### 5. Integration test uses `//go:build integration` tag

The integration test requires Docker and takes significantly longer than unit tests. The `//go:build integration` tag (same pattern as `internal/docker/client_integration_test.go`) keeps it out of `go test ./...` and `make test`. Run explicitly with `make integration` or `go test -tags=integration ./internal/sandbox/`.

### 6. Integration test uses a minimal prompt

The integration test cannot rely on real AI agent credentials. Instead, it creates a sandbox with `--agent claude --no-start`, verifies the sandbox directory structure was created, starts it, checks the container is running, then stops, diffs (against a modified work copy), and destroys. This exercises the full lifecycle without needing API keys.

### 7. `new` command excluded from `YOLOAI_SANDBOX`

The `new` command requires an explicit name because it *creates* a sandbox — using an env var default would be confusing and error-prone. The `list`, `build`, `completion`, and `version` commands also don't take sandbox names.

## Detailed Implementation

### `internal/cli/envname.go` — Name resolution helper

```go
func resolveName(cmd *cobra.Command, args []string) (string, []string, error)
```

1. If `len(args) >= 1`, return `(args[0], args[1:], nil)`. Explicit arg always wins.
2. If `os.Getenv(EnvSandboxName) != ""`, return `(envValue, nil, nil)`. Env var fallback.
3. Return `("", nil, UsageError("sandbox name required (or set YOLOAI_SANDBOX)"))`.

The `cmd` parameter is currently unused but reserved for future use (e.g., reading command-specific defaults).

### `internal/cli/commands.go` — Completion command

Replace the `newCompletionCmd` stub:

```go
func newCompletionCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "completion [bash|zsh|fish|powershell]",
        Short: "Generate shell completion script",
        Long: `Generate shell completion script for the specified shell.

To load completions:

Bash:
  source <(yoloai completion bash)

Zsh:
  source <(yoloai completion zsh)

Fish:
  yoloai completion fish | source

PowerShell:
  yoloai completion powershell | Out-String | Invoke-Expression`,
        Args:      cobra.ExactArgs(1),
        ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
        RunE: func(cmd *cobra.Command, args []string) error {
            switch args[0] {
            case "bash":
                return cmd.Root().GenBashCompletionV2(cmd.OutOrStdout(), true)
            case "zsh":
                return cmd.Root().GenZshCompletion(cmd.OutOrStdout())
            case "fish":
                return cmd.Root().GenFishCompletion(cmd.OutOrStdout(), true)
            case "powershell":
                return cmd.Root().GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
            default:
                return sandbox.NewUsageError("unsupported shell: " + args[0] + " (valid: bash, zsh, fish, powershell)")
            }
        },
    }
}
```

Remove the `errNotImplemented` variable if it's no longer used.

### CLI command modifications for `YOLOAI_SANDBOX`

Each modified command follows the same pattern. The specific changes per command:

#### Single-name commands (`attach`, `show`, `log`, `start`, `reset`)

Current pattern:
```go
Args: cobra.ExactArgs(1),
RunE: func(cmd *cobra.Command, args []string) error {
    name := args[0]
    ...
```

New pattern:
```go
Args: cobra.ArbitraryArgs,
RunE: func(cmd *cobra.Command, args []string) error {
    name, _, err := resolveName(cmd, args)
    if err != nil {
        return err
    }
    ...
```

#### `diff` and `apply` (name + `-- <path>...`)

Current pattern:
```go
dashIdx := cmd.ArgsLenAtDash()
var positional, paths []string
if dashIdx < 0 {
    positional = args
} else {
    positional = args[:dashIdx]
    paths = args[dashIdx:]
}
if len(positional) != 1 {
    return sandbox.NewUsageError("expected exactly one sandbox name")
}
name := positional[0]
```

New pattern:
```go
dashIdx := cmd.ArgsLenAtDash()
var positional, paths []string
if dashIdx < 0 {
    positional = args
} else {
    positional = args[:dashIdx]
    paths = args[dashIdx:]
}
name, _, err := resolveName(cmd, positional)
if err != nil {
    return err
}
```

The `resolveName` call on `positional` (not `args`) ensures `--` path args are handled correctly.

#### `exec` (name + command + args)

Current pattern:
```go
Args: cobra.MinimumNArgs(2),
RunE: func(cmd *cobra.Command, args []string) error {
    name := args[0]
    cmdArgs := args[1:]
```

New pattern:
```go
Args: cobra.MinimumNArgs(1),
RunE: func(cmd *cobra.Command, args []string) error {
    name, rest, err := resolveName(cmd, args)
    if err != nil {
        return err
    }
    if len(rest) == 0 {
        return sandbox.NewUsageError("command is required")
    }
    cmdArgs := rest
```

When `YOLOAI_SANDBOX` is set, `exec <command> [args...]` works — the entire `args` becomes the command since the name comes from the env var.

#### `stop` and `destroy` (multi-name + `--all`)

These commands accept multiple names and have `--all` flags. The env var applies when no names and no `--all`:

Current pattern:
```go
if len(args) == 0 {
    return sandbox.NewUsageError("at least one sandbox name is required (or use --all)")
}
names = args
```

New pattern:
```go
if len(args) == 0 {
    envName := os.Getenv(EnvSandboxName)
    if envName == "" {
        return sandbox.NewUsageError("at least one sandbox name is required (or use --all or set YOLOAI_SANDBOX)")
    }
    names = []string{envName}
} else {
    names = args
}
```

For multi-name commands, the env var provides a single default name. This is intentional — `YOLOAI_SANDBOX` is documented as "useful for single-sandbox sessions."

### `internal/sandbox/integration_test.go` — End-to-end test

```go
//go:build integration

package sandbox
```

#### `TestIntegration_FullLifecycle`

1. **Setup:** `t.TempDir()` for a temp project directory. Write a simple `main.go` with known content.
2. **Build base image:** Create a Manager, call `EnsureSetup` to build/verify the base image.
3. **Create sandbox:** Call `mgr.Create(ctx, CreateOptions{Name: "integ-test", WorkdirArg: tempDir, Agent: "claude", NoStart: true})`. Use `--no-start` to avoid needing API keys.
4. **Verify directory structure:**
   - `Dir("integ-test")` exists.
   - `meta.json` is valid.
   - Work copy exists with the original file.
5. **Start and stop:**
   - `mgr.Start(ctx, "integ-test")` — container starts.
   - `DetectStatus(ctx, client, "yoloai-integ-test")` returns StatusRunning or StatusDone/Failed.
   - `mgr.Stop(ctx, "integ-test")` — container stops.
   - `DetectStatus` returns StatusStopped.
6. **Diff with modifications:**
   - Modify a file in the work copy directly.
   - `GenerateDiff(DiffOptions{Name: "integ-test"})` returns non-empty diff.
   - `GeneratePatch("integ-test", nil)` returns non-empty patch.
7. **Apply:**
   - Create a target directory with the original content.
   - `ApplyPatch(patch, targetDir, false)` applies cleanly.
   - Verify target file has modified content.
8. **Destroy:**
   - `mgr.Destroy(ctx, "integ-test", true)`.
   - `Dir("integ-test")` no longer exists.
   - Container is removed.

### `Makefile` — Integration target

Add after the `lint` target:

```makefile
integration:
	go test -tags=integration -v -count=1 ./internal/sandbox/ ./internal/docker/
```

## Implementation Steps

1. **Create `internal/cli/envname.go`:**
   - `EnvSandboxName` constant and `resolveName` function.

2. **Create `internal/cli/envname_test.go`:**
   - Tests for all precedence cases (explicit, env, both, neither, extra args).

3. **Update `internal/cli/commands.go`:**
   - Replace `newCompletionCmd` stub with Cobra generators.
   - Remove `errNotImplemented` if no longer used.

4. **Update 10 CLI command files for `YOLOAI_SANDBOX`:**
   - `attach.go`, `show.go`, `log.go`, `start.go`, `reset.go` — single-name pattern.
   - `diff.go`, `apply.go` — dash-separated path pattern.
   - `exec.go` — name + command pattern.
   - `stop.go`, `destroy.go` — multi-name pattern (inline env check).

5. **Create `internal/sandbox/integration_test.go`:**
   - Full lifecycle test with `//go:build integration` tag.

6. **Update `Makefile`:**
   - Add `integration` target.

7. **Run `go build ./...`, `make lint`, `make test`.**

## Tests

### `internal/cli/envname_test.go`

```go
func TestResolveName_ExplicitArg(t *testing.T)
// args=["my-sandbox"] → ("my-sandbox", [], nil)

func TestResolveName_EnvFallback(t *testing.T)
// args=[], YOLOAI_SANDBOX="env-name" → ("env-name", [], nil)

func TestResolveName_ExplicitOverridesEnv(t *testing.T)
// args=["explicit"], YOLOAI_SANDBOX="env-name" → ("explicit", [], nil)

func TestResolveName_NeitherSet(t *testing.T)
// args=[], no env → UsageError

func TestResolveName_ExtraArgs(t *testing.T)
// args=["name", "extra1", "extra2"] → ("name", ["extra1", "extra2"], nil)
```

### `internal/sandbox/integration_test.go`

```go
func TestIntegration_FullLifecycle(t *testing.T)
// Setup → create → verify structure → start → stop → modify work copy →
// diff → patch → apply to target → destroy → verify cleanup.
// Build tag: //go:build integration
// Requires: Docker daemon, ~30-60s runtime.
```

## Verification

```bash
# Must compile
go build ./...

# Linter must pass
make lint

# Unit tests pass
make test

# Integration test (requires Docker)
make integration

# Manual verification:

# Completion
yoloai completion bash > /dev/null    # Should produce output, no error
yoloai completion zsh > /dev/null
yoloai completion fish > /dev/null
yoloai completion powershell > /dev/null

# Version (already works)
yoloai version
# yoloai version dev (commit: <sha>, built: <date>)

make build
./yoloai version
# yoloai version dev (commit: <sha>, built: <date>)

make VERSION=0.1.0 build
./yoloai version
# yoloai version 0.1.0 (commit: <sha>, built: <date>)

# YOLOAI_SANDBOX env var
mkdir -p /tmp/test-env
echo "hello" > /tmp/test-env/file.txt
yoloai new test-env /tmp/test-env
export YOLOAI_SANDBOX=test-env

yoloai show           # Should show test-env (no name arg needed)
yoloai diff           # Should work (no changes)
yoloai log            # Should work
yoloai stop           # Should stop test-env
yoloai start          # Should start test-env
yoloai reset          # Should reset test-env

# Explicit arg overrides env
yoloai show other-name  # Should try "other-name", not "test-env"

# Multi-name commands with env var
yoloai stop           # Should stop test-env (single from env)
yoloai stop a b       # Explicit names override env

# Missing name without env
unset YOLOAI_SANDBOX
yoloai show           # Should error: "sandbox name required (or set YOLOAI_SANDBOX)"

# Exec with env var
export YOLOAI_SANDBOX=test-env
yoloai start
yoloai exec ls -la    # Should run "ls -la" in test-env (name from env)

# Cleanup
yoloai destroy --yes
unset YOLOAI_SANDBOX
rm -rf /tmp/test-env

# Dogfood (final verification)
yoloai new fix-something --prompt "list the files" ~/Projects/yoloai:copy
yoloai attach fix-something
# Ctrl+B, D to detach
yoloai diff fix-something
yoloai destroy fix-something --yes
```

## Concerns

### 1. Args validation change for env var support

Switching from `cobra.ExactArgs(1)` to `cobra.ArbitraryArgs` means Cobra no longer validates argument count. Validation moves to `resolveName` which returns a `UsageError`. The error messages are equivalent but now include the env var hint. This is a net improvement in UX.

### 2. `exec` with env var ambiguity

When `YOLOAI_SANDBOX` is set, `yoloai exec ls -la` means "run `ls -la` in the env-var sandbox." Without the env var, `yoloai exec ls -la` would fail because `ls` would be interpreted as the sandbox name and `-la` as the command — which is probably wrong but is the user's explicit input. The `MinimumNArgs(1)` change ensures at least a command is provided when using the env var.

### 3. Integration test Docker dependency

The integration test requires a running Docker daemon and the yoloai-base image. `EnsureSetup` handles image building, but the test will fail in CI environments without Docker. The `//go:build integration` tag keeps it out of normal test runs. A CI pipeline should run `make integration` as a separate step with Docker available.

### 4. Integration test without API keys

The test uses `--no-start` to create the sandbox without starting the agent, then starts the container manually. The agent will fail to connect (no API key), but the container and tmux session are operational enough to test status detection, stop, start, diff, and destroy. The test modifies the work copy directly rather than relying on agent output.

### 5. Completion caching

Cobra's generated completion scripts are static — they reflect the command tree at generation time. If the user adds a new version of yoloAI with new commands, they need to regenerate the completion script. This is standard Cobra behavior and documented in the `Long` help text.

### 6. `stop` and `destroy` env var interaction with `--all`

When `--all` is set, the env var is irrelevant (all sandboxes are targeted). When neither `--all` nor explicit args are provided, the env var provides a single sandbox name. This is consistent with the "single-sandbox session" documentation for `YOLOAI_SANDBOX`.
