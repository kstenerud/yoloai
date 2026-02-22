# Coding Standard

Reference for consistent code style and practices across the yoloai codebase.

Based on Effective Go, Go Code Review Comments, Google Go Style Guide, and Uber Go Style Guide — filtered for a modern Go 1.22+ CLI project where navigability, clarity, and maintainability matter most.

See also: global `CLAUDE.md` for language-agnostic naming philosophy and comment principles.

## Language and Toolchain

- **Go 1.22+** (latest stable)
- `gofmt` mandatory — non-negotiable, runs on save
- `goimports` for import grouping and cleanup
- Module path: `github.com/<org>/yoloai` (update when repo is created)

## Formatting and Linting

- **Formatter:** `gofmt` (mandatory, zero configuration)
- **Linter:** `golangci-lint` with curated linter list

### golangci-lint Configuration

Enable these linters in `.golangci.yml`:

| Linter | Purpose |
|--------|---------|
| errcheck | Unchecked error returns |
| govet | Suspicious constructs (`go vet`) |
| staticcheck | Advanced static analysis |
| unused | Unused code |
| ineffassign | Ineffectual assignments |
| gosec | Security issues |
| errorlint | Error wrapping/comparison mistakes |
| revive | Extensible linter (replaces golint) |
| gocritic | Opinionated style and performance checks |
| sloglint | Consistent `log/slog` usage |

Linters to skip: `exhaustruct` (forces filling every struct field — noisy, fights zero values), `varnamelen` (conflicts with Go's short-variable conventions).

## Project Structure

```
yoloai/
├── go.mod
├── go.sum
├── cmd/yoloai/main.go        # Entry point (thin — parse flags, call run)
├── internal/                  # All private packages
│   ├── cmd/                   # Cobra command definitions
│   ├── sandbox/               # Sandbox lifecycle
│   ├── docker/                # Docker client wrapper
│   ├── config/                # Config parsing (Viper)
│   └── agent/                 # Agent preset definitions
├── resources/                 # Dockerfiles, templates
└── testdata/                  # Test fixtures
```

Everything under `internal/` is private to this module — prevents accidental external imports.

## CLI Framework

- **Cobra** for command definitions, **Viper** for configuration
- One file per command under `internal/cmd/`
- Use `RunE` (not `Run`) so commands return errors for proper propagation
- Viper for config file + env var + flag binding with struct unmarshaling
- Commands are thin — parse args, call into domain packages, format output

## File Organization

- **One primary type per file.** Small helper types used only by that type can live in the same file. File name matches the primary type: type `SandboxManager` → `sandbox_manager.go`.
- **File names:** `snake_case.go` (Go convention).
- **`doc.go`** for package-level documentation in larger packages.
- **Files over 400 lines:** consider whether there are multiple responsibilities that should be split.
- **Functions over 40 lines:** consider whether it can be decomposed into named steps.
- These aren't hard limits — a long file with one coherent responsibility is fine. A short file with three unrelated concerns is not.

## Naming

Follow Go conventions — [Effective Go](https://go.dev/doc/effective_go) and [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments) are authoritative.

| Thing | Convention | Example |
|-------|-----------|---------|
| Packages | short, lowercase, no underscores | `sandbox`, `config` |
| Exported types | `MixedCaps` | `SandboxManager` |
| Unexported types | `mixedCaps` | `overlayState` |
| Functions/methods | `MixedCaps` / `mixedCaps` | `CreateSandbox()`, `buildArgs()` |
| Constants | `MixedCaps` | `DefaultDiskLimit` (not `DEFAULT_DISK_LIMIT`) |
| Interfaces | name by method + `-er` when single-method | `Reader`, `ContainerRunner` |
| Receivers | 1-2 letters, consistent within type | `s` for `*Sandbox`, `m` for `*Manager` |
| Acronyms | all-caps | `URL`, `HTTP`, `ID`, `API` |

Never use underscores in Go names (except in test functions: `Test_xxx`). Never use `self` or `this` for receivers.

### Clarity over brevity

Names must be understandable to someone unfamiliar with the codebase. When you encounter a variable mid-function, its role should be obvious without scrolling to its declaration.

- **Spell words out:** `containerName` not `ctrNm`, `directory` not `dir`
- **Accepted short forms** (universal enough to need no explanation): `i`, `j`, `k` (loop indices), `args` (arguments), `ctx` (context), `src`/`dst` (source/destination), `tmp` (temporary), `err` (error), `fmt` (format), `fn` (function), `idx` (index), `msg` (message), `cmd` (command), `cfg` (config)
- **A name that needs a comment to explain it is too short or too vague** — rename it instead of adding the comment
- **Parameters are part of the public interface** — a function signature should read almost like documentation: `CreateSandbox(name string, agent AgentPreset, directories []string)` over `CreateSandbox(n string, a AgentPreset, d []string)`

## Imports

Three groups, separated by blank lines (enforced by `goimports`):

1. Standard library
2. Third-party packages
3. Local packages (`github.com/<org>/yoloai/...`)

```go
import (
	"context"
	"fmt"

	"github.com/docker/docker/client"
	"github.com/spf13/cobra"

	"github.com/<org>/yoloai/internal/sandbox"
)
```

- **No dot imports** (`import . "pkg"`) — makes the namespace unpredictable
- **Blank imports** (`import _ "pkg"`) only in `main.go` or test files

## Error Handling

- Wrap errors with context: `fmt.Errorf("create sandbox %q: %w", name, err)`
- Inspect errors with `errors.Is` and `errors.As`
- **Sentinel errors** use `Err` prefix: `var ErrSandboxNotFound = errors.New("sandbox not found")`
- Custom error types for rich context when callers need structured information
- Never `panic` in library code — return errors
- Happy path at minimal indentation (early returns for errors)

```go
result, err := doSomething()
if err != nil {
	return fmt.Errorf("doing something: %w", err)
}
// happy path continues here, unindented
```

## Testing

- **Framework:** `testing` stdlib + `testify/assert` for readability
- **Table-driven tests** as the standard pattern for multiple cases
- `t.Helper()` on all test helper functions
- **Build tags** for slow tests: `//go:build integration` at the top of integration test files
- **Mocking:** define interfaces at the consumption site, not the implementation site. Mock via interface satisfaction.
- Test file naming: `xxx_test.go` alongside the code it tests
- Test function naming: `TestCreateSandbox_FailsWhenDockerUnavailable` — describe the scenario
- Run with: `go test ./...` (fast), `go test -tags=integration ./...` (needs Docker)
- All new functionality requires tests. Bug fixes require a regression test.

## Logging

- **`log/slog`** (stdlib, Go 1.21+) — not `log` or `fmt.Println`
- Logger per component via `slog.With("component", "sandbox")`
- `TextHandler` for development, `JSONHandler` for production
- CLI layer configures the handler based on `--verbose` / `--quiet`
- Log levels: `Debug` for tracing, `Info` for normal operations, `Warn` for recoverable issues, `Error` for failures

## Docker Interactions

- Use **`github.com/docker/docker/client`** (official SDK), not subprocess calls to `docker` CLI
- Wrap behind interfaces for testability
- `context.Context` on all Docker operations
- Handle Docker daemon not running with a clear error message
- All container names prefixed with `yoloai-`

## Configuration and Constants

- No magic strings — use constants or typed values
- Config parsing isolated in `internal/config/`; rest of code receives typed structs
- Default values defined in one place, not scattered
- Environment variable names prefixed with `YOLOAI_` for yoloai-specific vars

## Dependencies

- **Minimal** — Go culture favors the standard library. Justify each dependency.
- `go.mod` with compatible version ranges
- **Core deps** (always needed): Cobra (CLI), Viper (config), Docker SDK
- **Dev deps:** golangci-lint, testify
- No vendoring unless required for reproducible builds in air-gapped environments

## Code Organization Patterns

- **Accept interfaces, return structs** — define interfaces at the point of consumption, not alongside the implementation
- **Small interfaces** (1-2 methods) — the bigger the interface, the weaker the abstraction
- **Constructor injection** via `NewXxx` functions:
  ```go
  func NewSandboxManager(docker DockerClient, cfg Config) *SandboxManager {
      return &SandboxManager{docker: docker, cfg: cfg}
  }
  ```
- **`context.Context` as first parameter** for all I/O operations
- **No global mutable state** — pass dependencies through constructors or function parameters

## Documentation

- **godoc conventions:** comments start with the name being documented
  ```go
  // SandboxManager handles sandbox lifecycle operations.
  type SandboxManager struct { ... }

  // Create creates a new sandbox with the given agent preset.
  func (m *SandboxManager) Create(ctx context.Context, name string) (*Sandbox, error) {
  ```
- Package comments in `doc.go` or above the `package` declaration
- **`// ABOUTME:`** project convention for quick scanning (supplemental to godoc, not a replacement)
- No documentation on unexported helpers, test functions, or obvious one-liners
- **No commented-out code.** Use version control.

## What to Avoid

- **`init()` abuse** — avoid `init()` except for truly global setup (e.g., registering a driver). Prefer explicit initialization in `main()` or constructors.
- **Package-level mutable variables** — these are hidden global state. Pass dependencies through constructors.
- **Over-engineering with interfaces** — don't create an interface until you have a consumer that needs the abstraction (testing counts as a consumer). One concrete type is simpler than one interface + one implementation.
- **Premature abstraction** — three similar lines of code are better than a premature helper function. Build what's needed now.
- **`self`/`this` receivers** — use short, idiomatic names (`s`, `m`, `c`)
- **`SCREAMING_SNAKE` constants** — Go uses `MixedCaps` for everything
- **Hungarian notation** — Go doesn't use type prefixes/suffixes in names
- **Bare `panic`** in library code — always return errors
- **Global mutable state** — pass dependencies through constructors or function parameters
