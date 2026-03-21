# Exit Code System Design

## Status: Draft

---

## Background

The current exit code table covers the basics:

| Code | Meaning |
|------|---------|
| 0    | Success |
| 1    | General error |
| 2    | Usage error (bad arguments) |
| 3    | Configuration error (bad config file, missing required config) |
| 128+N | Terminated by signal N |
| 130  | SIGINT / Ctrl+C |

This is insufficient for scripted integration. Several categories of failure are currently collapsed into exit 1, making it impossible for callers to distinguish recoverable from fatal conditions, or to take targeted action (e.g., "install docker", "switch to a supported platform", "re-authenticate").

The critique identified the most glaring gap: `yoloai destroy` with pending changes returns exit 1 (generic), not a distinct code that a CI script could detect and handle gracefully.

---

## Proposed Exit Code Table

| Code | Type | Condition |
|------|------|-----------|
| 0    | — | Success |
| 1    | — | General error (unexpected) |
| 2    | `UsageError` | Bad arguments or missing required args |
| 3    | `ConfigError` | Bad config file or missing required config |
| 4    | `ActiveWorkError` | Sandbox has unapplied changes or a running agent that would be lost |
| 5    | `DependencyError` | Required software is not installed or not configured |
| 6    | `PlatformError` | Operation is fundamentally impossible on this OS/arch |
| 7    | `AuthError` | Credentials are completely absent (hard no-auth case) |
| 8    | `PermissionError` | System has the software and supports the platform, but access is denied by policy |
| 128+N | — | Terminated by signal N (POSIX) |
| 130  | — | SIGINT / Ctrl+C (128+2) |

**Resource exhaustion** (disk full, OOM, container quota exceeded) is discussed separately below.

---

## Type Definitions

All new error types follow the same pattern as existing `UsageError` and `ConfigError`: defined in `config/errors.go`, re-exported in `sandbox/errors.go`, and dispatched in `internal/cli/root.go`.

### Exit 4 — `ActiveWorkError`

**Condition:** A destructive operation (destroy, stop with `--force`, prune) would discard either:
- Unapplied changes in a `:copy` or `:overlay` directory, or
- A running agent whose output has not been reviewed.

**Where it fires:**
- `sandbox/lifecycle.go`: `Manager.NeedsConfirmation()` — returns `(bool, string)` today. The string reason ("unapplied changes exist", "agent is running") feeds the confirmation prompt. The `ActiveWorkError` is not returned here; instead, the CLI layer (destroy, stop) decides whether to prompt or error. See Phase 3 for the wiring.
- `internal/cli/destroy.go`: the non-TTY confirmation path (stdin is not a TTY, `--yes` was not passed) — currently exits 0 silently; should return `*ActiveWorkError` with a message like "sandbox 'X' has active work; use --yes to force or run 'yoloai apply X' first".
- `internal/cli/stop.go`: same non-TTY path for stops that check for running agents.

**Behavioral edge cases:**
- `--yes` explicitly bypasses this check (that's its purpose). `ActiveWorkError` is only returned when confirmation would be needed and cannot be obtained.
- In `--json` mode: `effectiveYes()` already returns `true` when `--json` is set, so `destroy --json` never reaches the confirmation block at all. `ActiveWorkError` is irrelevant in this path — the user is implicitly opting out of the guard.
- `yoloai stop` without active work should not fire this code.

### Exit 5 — `DependencyError`

**Condition:** A required external program is not installed, not running, or not configured — but the operation would be valid on this platform if the dependency were present.

**Examples:**
- Docker daemon is not running or not installed (currently `ErrDockerUnavailable` in `sandbox/errors.go`).
- Podman socket is not found when `backend = podman`.
- Containerd socket path does not exist.
- `/dev/kvm` not present when using a VM-mode backend that requires hardware virtualization.
- A CNI plugin required for network isolation is missing.
- Tart is not installed (on macOS — the platform supports it, the binary is just missing).

**Where it fires:**
- `internal/cli/helpers.go`: `withRuntime()` / runtime initialization path.
- `runtime/docker/docker.go`: connection checks.
- `runtime/tart/tart.go`: binary presence check.
- `runtime/containerd/containerd.go`: socket/connection checks.
- `runtime/seatbelt/seatbelt.go`: binary presence check.

**Currently:** `ErrDockerUnavailable` is a plain sentinel that hits the `return 1` fallthrough. It should be wrapped in or replaced by `DependencyError`.

**Relationship to `PlatformError`:** If the operation is only possible on a different OS, that is exit 6. If the operation is possible on this OS but the required software is just not installed, that is exit 5.

**Example message:** `yoloai: docker is not available: cannot connect to Docker daemon. Install Docker or start the daemon.`

### Exit 6 — `PlatformError`

**Condition:** The requested operation is fundamentally impossible on the current OS or architecture. No amount of software installation can fix it.

**Examples:**
- `--backend seatbelt` on Linux (Seatbelt is macOS-only).
- `--backend tart` on non-Apple-Silicon (Tart requires Apple Silicon; M-series CPU).
- `--backend tart` on Linux.
- VM-mode sandboxing that requires KVM on macOS where HVF is unavailable.
- Any feature that requires a Linux-specific kernel interface (overlayfs, namespaces) being run on macOS or Windows/WSL without the required kernel support.

**Where it fires:**
- Backend selection in `internal/cli/helpers.go`: `newRuntime()` / `resolveBackendForSandbox()`.
- Runtime constructors in `runtime/*/` when they can detect the impossibility early.
- `:overlay` mode on a platform where overlayfs is not available (currently not typed — this should become `PlatformError`).

**Example message:** `yoloai: seatbelt backend is only supported on macOS`

**Note on `:overlay` on macOS Docker Desktop:** macOS Docker Desktop runs Linux containers via a VM. Overlayfs itself is supported. The failure mode there is a missing capability (`CAP_SYS_ADMIN`), not a platform impossibility — that is exit 8 (`PermissionError`).

### Exit 7 — `AuthError`

**Condition:** An API key or credential required to proceed is completely absent — not expired, not soft-failing OAuth, but genuinely not set at all.

**Scope:** Hard "no credentials at all" only. Specifically:
- Required agent API key env var is not set and not in any configured secrets path.
- No token at all in the credential store for a service that requires one.

**Out of scope (exit 1 or soft warning):**
- OAuth token that needs renewal — user may have a working credential that needs a re-auth flow; this is not a hard error.
- Missing API key that the user is aware of and working around (e.g., local model with no key needed).

**Where it fires:**
- `sandbox/create.go` or `sandbox/lifecycle.go`: the API key check before launching an agent (currently `ErrMissingAPIKey`). `ErrMissingAPIKey` should be wrapped in or replaced by `AuthError`.

**Example message:** `yoloai: ANTHROPIC_API_KEY is not set. Set it in your environment or run 'yoloai config set api_key ...'`

### Exit 8 — `PermissionError`

**Condition:** The platform supports the operation and the software is installed, but access is denied by an admin policy, security module, or OS-level capability restriction.

This is distinct from:
- Exit 5: software missing.
- Exit 6: platform incapable.

The distinction: the system *has* what is needed, but *access* is blocked.

**Examples:**
- `CAP_SYS_ADMIN` is denied by a seccomp profile or AppArmor/SELinux policy (required for `:overlay` mode).
- `CAP_NET_ADMIN` is denied (required for custom network namespaces).
- `/dev/kvm` exists but the user is not in the `kvm` group.
- Docker socket exists but the user does not have permission to connect to it.
- Rootless Podman fails due to `/proc/sys/kernel/unprivileged_userns_clone` being set to 0 (Linux sysctl lockdown).

**Where it fires:**
- `:overlay` capability validation in `sandbox/create.go` (`IsolationValidator` check).
- Runtime socket permission checks in `runtime/docker/`, `runtime/containerd/`.
- `runtime/seatbelt/` if sandbox entitlement is denied.

**Example message:** `yoloai: CAP_SYS_ADMIN is required for :overlay mode but was denied. Check your container security policy or use :copy mode instead.`

---

## Resource Exhaustion — Evaluation

**Question:** Should resource exhaustion get its own exit code?

**Arguments for:**
- Disk full, OOM, and container quota exceeded are all recoverable (free space, increase quota) and distinguishable from logic errors.
- CI pipelines benefit from knowing "this failed because disk is full" vs "this failed because of a bug."
- Other tools (systemd, borg backup) use distinct codes for resource failures.

**Arguments against:**
- Resource exhaustion surfaces in many places (file writes, container creates, image pulls) and is hard to consistently type at the error origin — most errors come from the OS as `ENOSPC` or from Docker as a container creation error, buried in error chains.
- Wrapping every `os.Write` call with resource exhaustion detection would be invasive and error-prone.
- A distinct code would only fire reliably for errors that pass through a central resource check, which doesn't exist today.

**Conclusion:** Defer. The infrastructure (typed errors, dispatch in root.go) will make it straightforward to add `ResourceExhaustionError` (exit 9) later if callers ask for it. Do not add it in this pass.

---

## Implementation

### Phase 1: Define types and dispatch

**`config/errors.go`** — add five new error types following the existing pattern:

```go
// ActiveWorkError indicates a sandbox has unapplied changes or a running agent (exit code 4).
type ActiveWorkError struct{ Err error }
func (e *ActiveWorkError) Error() string { return e.Err.Error() }
func (e *ActiveWorkError) Unwrap() error { return e.Err }
func NewActiveWorkError(format string, args ...any) *ActiveWorkError { ... }

// DependencyError indicates required software is not installed or not running (exit code 5).
type DependencyError struct{ Err error }
// ... same pattern

// PlatformError indicates the operation is impossible on this OS/arch (exit code 6).
type PlatformError struct{ Err error }
// ... same pattern

// AuthError indicates credentials are completely absent (exit code 7).
type AuthError struct{ Err error }
// ... same pattern

// PermissionError indicates access is denied by policy (exit code 8).
type PermissionError struct{ Err error }
// ... same pattern
```

**`sandbox/errors.go`** — re-export all five new types (type aliases + constructor vars).

**`internal/cli/root.go`** — extend the `errors.As` dispatch chain:

```go
var activeWorkErr *config.ActiveWorkError
if errors.As(runErr, &activeWorkErr) { return 4 }

var depErr *config.DependencyError
if errors.As(runErr, &depErr) { return 5 }

var platErr *config.PlatformError
if errors.As(runErr, &platErr) { return 6 }

var authErr *config.AuthError
if errors.As(runErr, &authErr) { return 7 }

var permErr *config.PermissionError
if errors.As(runErr, &permErr) { return 8 }
```

### Phase 2: Wire existing errors to new types

1. **`ErrDockerUnavailable`** → this sentinel is currently declared in `sandbox/errors.go` but never actually returned anywhere in the codebase — it is dead code. The real Docker unavailability errors come from the Docker client (connection refused, daemon not running) inside `runtime/docker/`. The wiring must happen at those actual error sites in `runtime/docker/docker.go`, not by wrapping a sentinel that nobody returns. `ErrDockerUnavailable` can either be deleted or repurposed as the inner error wrapped by `DependencyError`.
2. **`ErrMissingAPIKey`** → wrap with `AuthError` at the API key check in `sandbox/create.go`.
3. **Overlay capability denial** → wrap with `PermissionError` in the `IsolationValidator` path.
4. **Backend OS/arch checks** → return `PlatformError` from `runtime/seatbelt/` (non-macOS), `runtime/tart/` (non-Apple-Silicon).

### Phase 3: Fix `ActiveWorkError` paths

1. **`sandbox/lifecycle.go`**: `NeedsConfirmation()` stays as-is (it returns `(bool, string)` for use in prompts). No change needed here.
2. **`internal/cli/destroy.go`**: Add a non-TTY guard after the warnings loop, before the `sandbox.Confirm()` call. The codebase does not currently have an `isTerminal` helper — this requires either adding one using `golang.org/x/term` (check if already a transitive dependency) or using the simpler `os.Stdin.Stat()` approach. The guard:
   ```go
   // If stdin is not a TTY, we cannot prompt — treat active work as an error.
   if fi, err := os.Stdin.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
       return sandbox.NewActiveWorkError(
           "%d sandbox(es) have active work; use --yes to force or run 'yoloai apply' first",
           len(warnings),
       )
   }
   ```
   Place this immediately before the `sandbox.Confirm(...)` call, inside the `len(warnings) > 0` block.
3. **`internal/cli/stop.go`**: Same pattern if applicable.

### Phase 4: Update documentation

- **`docs/dev/CLI-STANDARD.md`**: Expand the exit codes table with codes 4–8.
- **`docs/GUIDE.md`**: Add a "Exit codes" section under "Scripting / CI" if one doesn't exist.

---

## CLI-STANDARD.md Table (updated)

| Code | Meaning |
|------|---------|
| 0    | Success |
| 1    | General error |
| 2    | Usage error (bad arguments, missing required args) |
| 3    | Configuration error (bad config file, missing required config) |
| 4    | Active work — sandbox has unapplied changes or a running agent |
| 5    | Dependency error — required software not installed or not running |
| 6    | Platform error — operation not possible on this OS/arch |
| 7    | Auth error — credentials completely absent |
| 8    | Permission error — access denied by policy (capability, seccomp, ACL) |
| 128+N | Terminated by signal N (POSIX convention) |
| 130  | Interrupted by SIGINT / Ctrl+C (128+2) |

---

## Open Questions

1. **Wrapping vs. replacing sentinels**: `ErrDockerUnavailable` and `ErrMissingAPIKey` are plain sentinel errors today. Should they be replaced by typed errors, or should callers wrap them? Proposal: replace at the origin so all callers get the typed code automatically without per-callsite wrapping.

2. **`--json` error envelope for new codes**: The JSON error output is currently `{"error": "message"}`. Should it include the exit code? e.g., `{"error": "message", "code": 5}`. This would make machine parsing unambiguous without requiring the caller to inspect the process exit code. Low priority but worth considering.

3. **`golang.org/x/term` dependency**: The non-TTY guard in Phase 3 uses `os.Stdin.Stat()` + `ModeCharDevice` instead of `term.IsTerminal()`. The latter is cleaner but requires `golang.org/x/term`. Check whether it is already a transitive dependency before deciding which approach to use.
