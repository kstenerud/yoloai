# Go Coding Standard

Reference for consistent Go code style and practices across yoloAI.

Based on Effective Go, Go Code Review Comments, Google Go Style Guide, and Uber Go Style Guide — filtered for a modern Go CLI project where navigability, clarity, and maintainability matter most.

See also: `../principles/development-principles.md` (the *why* behind these rules — boundary discipline, validate-at-every-layer, parse-don't-validate, warnings-are-signal), `../principles/testing-principles.md` (testing discipline), `CLI.md` (CLI-specific concerns), and global `~/.claude/CLAUDE.md` for language-agnostic naming and comment principles.

## Language and Toolchain

- **Go 1.26+** — pinned by the `go` directive in `go.mod` (currently `go 1.26.1`). The Makefile passes `GOTOOLCHAIN=$(shell go env GOVERSION)` to the linter run, so the toolchain in use is the one Go itself picks for the directive.
- `gofmt` mandatory — non-negotiable, runs on save
- `goimports` for import grouping and cleanup
- Module path: `github.com/kstenerud/yoloai` (update when repo is created)

## Formatting and Linting

- **Formatter:** `gofmt` (mandatory, zero configuration)
- **Linter:** `golangci-lint` with curated linter list

### golangci-lint Configuration

Enable these linters in `.golangci.yml`:

| Linter      | Purpose                                  |
|-------------|------------------------------------------|
| errcheck    | Unchecked error returns                  |
| govet       | Suspicious constructs (`go vet`)         |
| staticcheck | Advanced static analysis                 |
| unused      | Unused code                              |
| ineffassign | Ineffectual assignments                  |
| gosec       | Security issues                          |
| errorlint   | Error wrapping/comparison mistakes       |
| revive      | Extensible linter (replaces golint)      |
| gocritic    | Opinionated style and performance checks |
| sloglint    | Consistent `log/slog` usage              |

Linters to skip: `exhaustruct` (forces filling every struct field — noisy, fights zero values), `varnamelen` (conflicts with Go's short-variable conventions).

## Project Structure

```
yoloai/
├── go.mod
├── go.sum
├── cmd/yoloai/main.go        # Entry point (thin — parse flags, call run)
├── internal/                  # All private packages
│   ├── agent/                 # Agent definitions (Aider, Claude, Codex, Gemini, OpenCode, etc.)
│   ├── cli/                   # Cobra command definitions
│   ├── runtime/               # Pluggable runtime interface
│   │   ├── docker/            # Docker implementation of runtime.Runtime
│   │   ├── tart/              # Tart (macOS VM) implementation
│   │   └── seatbelt/          # Seatbelt (macOS sandbox-exec) implementation
│   └── sandbox/               # Core logic: create, lifecycle, diff, apply, config, inspect
└── docs/                      # Documentation
```

Everything under `internal/` is private to this module — prevents accidental external imports.

## CLI Framework

- **Cobra** for command definitions
- One file per command under `internal/cli/`
- Use `RunE` (not `Run`) so commands return errors for proper propagation
- Config handled via CLI flags + go-yaml with `yaml.Node` for comment-preserving edits. Viper was evaluated but not adopted.
- Commands are thin — parse args, call into domain packages, format output

## File Organization

- **One primary responsibility per file.** A file groups a cohesive responsibility — a struct, its methods, and related helpers. File name matches the primary concept: type `SandboxManager` → `sandbox_manager.go`.
- **File names:** `snake_case.go` (Go convention).
- **`doc.go`** for package-level documentation in larger packages.
- **Files over 400 lines:** consider whether there are multiple responsibilities that should be split.
- **Functions over 40 lines:** consider whether it can be decomposed into named steps.
- These aren't hard limits — a long file with one coherent responsibility is fine. A short file with three unrelated concerns is not.

## Naming

Follow Go conventions — [Effective Go](https://go.dev/doc/effective_go) and [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments) are authoritative.

| Thing             | Convention                                | Example                                       |
|-------------------|-------------------------------------------|-----------------------------------------------|
| Packages          | short, lowercase, no underscores          | `sandbox`, `config`                           |
| Exported types    | `MixedCaps`                               | `SandboxManager`                              |
| Unexported types  | `mixedCaps`                               | `overlayState`                                |
| Functions/methods | `MixedCaps` / `mixedCaps`                 | `CreateSandbox()`, `buildArgs()`              |
| Constants         | `MixedCaps`                               | `DefaultDiskLimit` (not `DEFAULT_DISK_LIMIT`) |
| Interfaces        | name by method + `-er` when single-method | `Reader`, `ContainerRunner`                   |
| Receivers         | descriptive, like any other variable      | `sandbox` for `*Sandbox`, `mgr` for `*Manager` |
| Acronyms          | all-caps                                  | `URL`, `HTTP`, `ID`, `API`                    |

Never use underscores in Go names (except in test functions: `Test_xxx`). Receivers are named descriptively like any other variable — not 1-2 letter abbreviations, and not `self` or `this`.

### Clarity over brevity

Names must be understandable to someone unfamiliar with the codebase. When you encounter a variable mid-function, its role should be obvious without scrolling to its declaration.

- **Spell words out:** `containerName` not `ctrNm`, `directory` not `dir`
- **Accepted short forms** (universal enough to need no explanation): `i`, `j`, `k` (loop indices), `args` (arguments), `ctx` (context), `src`/`dst` (source/destination), `tmp` (temporary), `err` (error), `fmt` (format), `fn` (function), `idx` (index), `msg` (message), `cmd` (command), `cfg` (config)
- **A name that needs a comment to explain it is too short or too vague** — rename it instead of adding the comment
- **Parameters are part of the public interface** — a function signature should read almost like documentation: `CreateSandbox(name string, agent AgentPreset, directories []string)` over `CreateSandbox(n string, a AgentPreset, d []string)`

### Field comments: keep the ones doing real work; rename when the comment is the name's job

The same "name carries the meaning" rule applies at the struct-field level. For every commented field, ask: *is this comment doing the name's job?* If yes, the name is wrong — rename it. If no, the comment is doing real work and should stay.

**Comments that earn their keep** (document things the type system can't):
- **Invariants and constraints.** `ExportDir string // required when Mode == ApplyExport`
- **Side effects.** `Replace bool // destroy any existing sandbox with the same name first`
- **Zero-value semantics.** `Timeout time.Duration // 0 = no timeout`
- **Conditional population.** `Patch string // populated only when Status == ApplyStatusDryRun`
- **Cross-references.** `NewPrompt string // distinct from RunOptions.Prompt (the original)`
- **Format reminders for opaque scalars.** `Date string // ISO-8601 build timestamp`

**Comments that are doing the name's job** (rename instead):
- `Note string // probe failure reason` → `UnavailableReason string`
- `Output string // host destination dir or file path` → `OutputPath string`
- `Per []*Result // per-directory results` → `PerDir []*Result`
- `Cache bool // also prune backend caches` → `PruneCache bool`
- `Changes bool // filter to sandboxes with unapplied changes` → `OnlyWithChanges bool`
- `MissingCaps []string // failed capability checks` → `MissingCapabilities []string`

The heuristic: read the comment and ask *"could a future-self with no context guess what this field is for from the name alone?"* If yes, the comment is restating; rename. If the comment encodes a rule the name can't carry (a constraint, a side effect, a zero-value semantic), keep it.

A worked example of this audit pass: the W-L8a layering-refactor design checkpoint applied this to the entire proposed `yoloai.Client` surface (`api_surface.go`'s Q-K resolution), producing ~20 renames where the field comment was doing the name's job. The principle generalises: **API field names should be self-describing nouns or verb-objects; comments document things the type system can't encode.**

## Imports

Three groups, separated by blank lines (enforced by `goimports`):

1. Standard library
2. Third-party packages
3. Local packages (`github.com/kstenerud/yoloai/...`)

```go
import (
	"context"
	"fmt"

	"github.com/docker/docker/client"
	"github.com/spf13/cobra"

	"github.com/kstenerud/yoloai/internal/sandbox"
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

### CLI Error Handling

Domain packages return typed errors (e.g., `ConfigError`, `SandboxNotFoundError`) and sentinel errors. The root Cobra command handler inspects errors with `errors.As` / `errors.Is` and maps them to exit codes:

| Error Type                               | Exit Code   |
|------------------------------------------|-------------|
| (none)                                   | 0 — success |
| General / unknown                        | 1           |
| Usage error (bad args, missing required) | 2           |
| Configuration error                      | 3           |

Cobra customization required: set `SilenceErrors: true` and `SilenceUsage: true` on the root command, then handle error formatting and exit codes in a custom `RunE` wrapper or post-run error handler.

## Error Types and Exit Codes

Return typed errors from `config/errors.go` (or their aliases in `sandbox/errors.go`)
when the error has a clear exit-code category:

- Bad flags / argument validation → `NewUsageError` (exit 2)
- Config file malformed or missing required key → `NewConfigError` (exit 3)
- Feature not available on this platform/backend → `NewPlatformError` (exit 6)
- Missing API key / credential → `NewAuthError` (exit 7)
- Permission denied → `NewPermissionError` (exit 8)

Use plain `fmt.Errorf` for operational failures (I/O, network, unexpected errors).
Wrap upstream errors with `%w`. Do not wrap user-facing validation messages — they
have no upstream cause.

**UsageError vs ConfigError:** UsageError = bad argument or flag passed by the user;
ConfigError = a config file is malformed or missing a required value. "Profile does
not exist" is UsageError (bad argument); "profile config.yaml is missing `agent` key"
is ConfigError.

**Do not change errors that wrap an upstream error with `%w`** — those are operational
errors and should stay as `fmt.Errorf`. Only convert errors that are purely
user-facing messages with no upstream cause to wrap.

## Testing

- **Framework:** `testing` stdlib + `testify/assert` — reduces assertion boilerplate; stdlib `testing` is also acceptable, the key is consistency within the project
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
- Logger per component as a struct field, injected via constructor: `slog.New(handler).With("component", "sandbox")`
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
- Config parsing isolated in `internal/sandbox/config.go`; rest of code receives typed structs
- Default values defined in one place, not scattered
- Environment variable names prefixed with `YOLOAI_` for yoloai-specific vars

## Dependencies

- **Minimal** — Go culture favors the standard library. Justify each dependency.
- `go.mod` managed by `go mod tidy`. Respect major version path convention (`/v2`). Reproducible builds via `go.sum`
- **Core deps** (always needed): Cobra (CLI), Docker SDK
- **Config dep:** `go-yaml` v3 with `yaml.Node` for comment-preserving config edits. Viper was evaluated but not adopted — go-yaml + thin config struct is sufficient.
- **Dev deps:** golangci-lint, testify
- No vendoring unless required for reproducible builds in air-gapped environments

### Third-Party Caching and State Mechanisms

Treat any non-system library's caching, lazy-initialization, or persistent-state mechanism as potentially broken for your use case. You will hit the edge cases the library author didn't handle.

This is not hypothetical — it has burned us repeatedly (containerd bolt metadata sharing, CNI IPAM lease cleanup, snapshot chain management). The patterns that bite most often:

- **Cache assumes single-namespace use** — shared metadata structures have subtle cross-namespace invalidation bugs
- **Lazy cleanup skips error paths** — stale entries accumulate when a previous run failed partway through
- **GC reference tracing is incomplete** — the library marks some roots but not all, so entries are collected while still needed
- **"Already exists" is silently swallowed** — treated as success even when the existing entry is wrong or stale

Mitigation:
- **Verify, don't trust.** After any write through a library's cache layer, read it back and confirm the data is actually accessible.
- **Clean up before you write.** Remove stale entries before inserting new ones, not after.
- **Understand GC roots.** If a library has a garbage collector, trace its root-marking logic before relying on it to protect your objects.
- **Own the lifecycle.** If the library's cleanup is best-effort or async, drive it explicitly at the points in your code where you know cleanup is safe.

## Build and Release

- Version injection via `ldflags`: `go build -ldflags "-X main.version=..."`
- `CGO_ENABLED=0` for static binaries (no libc dependency)
- Cross-compilation targets: `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`
- Standard `go build` invocation — no custom build tools required

## API Design

### Two-Layer API (80/20 Rule)

Public packages expose two layers:

**High-level API** — simple, opinionated, covers ~80% of use cases. Most callers never need to go deeper. Built directly on top of the low-level API.

**Low-level API** — flexible, explicit, handles the remaining ~20% of use cases that the high-level API can't serve without sacrificing simplicity. Worth the added complexity for power users who need it.

The ratio isn't literal — it's a design target. The goal is that the common path stays simple and the escape hatch exists for when it doesn't.

In practice:
- High-level functions take compact, ergonomic parameters and apply sensible defaults.
- Low-level functions accept `*Options` / `*Config` structs with full control over every knob.
- High-level functions are thin wrappers: they fill in defaults and delegate to the low-level function. No duplicated logic.
- Callers who need more control graduate to the low-level API; they don't fight the high-level one.

Example pattern:

```go
// High-level: zero-config entry point for the common case.
func (mgr *Manager) Start(ctx context.Context, name string) error {
    return mgr.StartWithOptions(ctx, name, StartOptions{})
}

// Low-level: full control for callers that need it.
func (mgr *Manager) StartWithOptions(ctx context.Context, name string, opts StartOptions) error {
    // implementation
}
```

Keep both layers in the same package. Don't split them into separate packages — that forces callers to import two packages for one concept.

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

## Concurrency and Resource Cleanup

### Goroutine Discipline

- Always pass `context.Context` for cancellation — no goroutine should outlive its parent's context
- Use `errgroup.Group` for managed goroutine lifecycles with error propagation
- No fire-and-forget goroutines — every goroutine must have a shutdown path
- Goroutines that outlive a function call must be documented at the call site

### Signal Bridging

- `main()` sets up `signal.NotifyContext` to bridge OS signals to context cancellation
- The context flows down through all layers — CLI → domain → Docker operations
- All long-running operations respect `ctx.Done()` for clean shutdown
- This connects the CLI standard's SIGINT/SIGTERM behavior (exit 130/143) to the coding standard's `context.Context` requirement

### Resource Cleanup

- `defer` cleanup in the function that acquires the resource
- Types that hold resources (connections, file handles, mounts) implement `io.Closer`
- Be aware of LIFO ordering — defers run in reverse order, which matters when resources depend on each other

### Persistent Resource Idempotency

Assume any resource you create that lives outside process memory — files, directories, network namespaces, IPAM leases, containers, VMs, named pipes, lock files — will eventually be left stale. Crashes, `^C`, OOM kills, power loss, and partial failures happen in production. The next run must not fail because a previous run left something behind.

**Rule: always delete before you create.** Before allocating any persistent, named resource, unconditionally remove any stale instance with the same name. Do not gate this on whether you expect a stale entry to exist.

```go
// Bad: assumes previous run cleaned up
netnsPath, err := createNetNS(name)

// Good: idempotent regardless of prior state
_ = deleteNetNS(name)
netnsPath, err := createNetNS(name)
```

This applies even when a higher-level `--replace` or `Destroy` call should have cleaned up: that call may have been skipped (e.g. sandbox dir missing so replace logic short-circuits), failed partway through, or the resource may have been created before the gating check. The creation site is the last line of defense.

The same principle extends to partial failures mid-setup: if step 3 of a 5-step setup fails, steps 1 and 2 have already allocated resources. Clean all of them up on the failure path — not just the resource that failed.

This is not hypothetical — it has burned us repeatedly: stale netns `file exists`, duplicate IPAM lease, `already exists` on container create. Every one was a missing pre-clear.

## Documentation

- **godoc conventions:** comments start with the name being documented
  ```go
  // SandboxManager handles sandbox lifecycle operations.
  type SandboxManager struct { ... }

  // Create creates a new sandbox with the given agent preset.
  func (manager *SandboxManager) Create(ctx context.Context, name string) (*Sandbox, error) {
  ```
- Package comments in `doc.go` or above the `package` declaration
- **`// ABOUTME:`** project convention for quick scanning (supplemental to godoc, not a replacement)
- Document unexported functions only when the name and type signature don't make intent obvious. No documentation on test functions or obvious one-liners
- **No commented-out code.** Use version control.

## Runtime Backend Extension

**Backend-specific params in `New()`, not `InstanceConfig`.** Construction-time params specific to one backend (SSH host/key, Kubernetes namespace/kubeconfig, AWS region/AMI) belong in `New()`, not in `InstanceConfig`. Per-invocation params that are universal or translatable across backends belong in `InstanceConfig`. If a new backend needs per-invocation params with no `InstanceConfig` analog, introduce an optional interface (precedent: `IsolationValidator`, `UsernsProvider`) rather than widening `InstanceConfig`.

## What to Avoid

- **`init()` abuse** — avoid `init()` except for truly global setup (e.g., registering a driver). Prefer explicit initialization in `main()` or constructors.
- **Package-level mutable variables** — hidden global state. Pass dependencies through constructors or function parameters.
- **Over-engineering with interfaces** — don't create an interface until you have a consumer that needs the abstraction (testing counts as a consumer). One concrete type is simpler than one interface + one implementation.
- **Premature abstraction** — three similar lines of code are better than a premature helper function. Build what's needed now.
- **`self`/`this` receivers** — name receivers descriptively like any other variable
- **1-2 letter receivers** — `s`, `m`, `c` are cryptic; use `sandbox`, `mgr`, `client`
- **`SCREAMING_SNAKE` constants** — Go uses `MixedCaps` for everything
- **Hungarian notation** — Go doesn't use type prefixes/suffixes in names
- **Bare `panic`** in library code — always return errors
