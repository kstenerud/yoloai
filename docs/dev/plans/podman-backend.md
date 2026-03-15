# Podman Backend Implementation Plan

Based on verified research in `docs/dev/research/podman.md`. All API compatibility claims
have been verified against Podman, Docker, and Buildah source code.

## Strategy

Embed `docker.Runtime` and override only what differs. The Docker SDK client works over
Podman's Docker-compatible socket — most operations need zero new code.

**Overrides needed (3 methods + build):**

| Method | Why |
|--------|-----|
| `Name()` | Return `"podman"` instead of `"docker"` |
| `InteractiveExec()` | Shells out to `docker exec` — must use `podman exec` |
| `BuildProfileImage()` | CLI path shells out to `docker build` — must use `podman build` |
| `DiagHint()` | Should say `podman logs` not `docker logs` |

Everything else (Create, Start, Stop, Remove, Inspect, Exec, EnsureImage, ImageExists,
Prune, Close) goes through the Docker SDK client pointed at the Podman socket — identical
behavior verified.

**One new capability:** `--userns=keep-id` injection for rootless mode. Override `Create()`
to add this to the container config.

---

## Implementation Steps

### Step 1: Export Docker client constructor

**File:** `runtime/docker/docker.go`

Refactor `New()` to extract a `NewWithSocket()` that accepts a custom endpoint:

```go
// NewWithSocket creates a Runtime connected to a specific Docker-compatible socket.
func NewWithSocket(ctx context.Context, host string, binaryName string) (*Runtime, error) {
    opts := []dockerclient.Opt{
        dockerclient.WithAPIVersionNegotiation(),
    }
    if host != "" {
        opts = append(opts, dockerclient.WithHost(host))
    } else {
        opts = append(opts, dockerclient.FromEnv)
    }

    cli, err := dockerclient.NewClientWithOpts(opts...)
    // ... ping, etc.
    return &Runtime{client: cli, binaryName: binaryName}, nil
}

func New(ctx context.Context) (*Runtime, error) {
    if _, err := exec.LookPath("docker"); err != nil {
        return nil, fmt.Errorf(...)
    }
    return NewWithSocket(ctx, "", "docker")
}
```

Also add a `binaryName` field to `Runtime` and use it in `InteractiveExec` and
`buildProfileImageCLI` instead of hardcoded `"docker"`. This way the Podman backend
inherits correct behavior without overriding these methods.

**Changes:**
- Add `binaryName string` to `Runtime` struct
- Refactor `New()` → calls `NewWithSocket(ctx, "", "docker")`
- Export `NewWithSocket(ctx, host, binaryName)`
- Replace hardcoded `"docker"` in `InteractiveExec` (line 266) and `buildProfileImageCLI`
  (line 322) with `r.binaryName`
- Replace hardcoded `"docker"` in `DiagHint` (line 279) with `r.binaryName`
- Update error message in `buildProfileImageCLI` (line 331-333) to use `r.binaryName`

This eliminates the need to override `InteractiveExec`, `BuildProfileImage`, and `DiagHint`
in the Podman backend — they inherit correct behavior via the `binaryName` field.

### Step 2: Create `runtime/podman/` package

**New file:** `runtime/podman/podman.go` (~100-120 lines)

```go
package podman

type Runtime struct {
    *docker.Runtime
}

func New(ctx context.Context) (*Runtime, error) {
    if _, err := exec.LookPath("podman"); err != nil {
        return nil, fmt.Errorf("podman is not installed")
    }

    sock, err := discoverSocket()
    if err != nil {
        return nil, fmt.Errorf("podman socket not found: %w", err)
    }

    dockerRT, err := docker.NewWithSocket(ctx, sock, "podman")
    if err != nil {
        return nil, fmt.Errorf("connect to podman: %w", err)
    }

    return &Runtime{Runtime: dockerRT}, nil
}

func (r *Runtime) Name() string { return "podman" }

// Create wraps the Docker Create to inject --userns=keep-id for rootless mode.
func (r *Runtime) Create(ctx context.Context, cfg runtime.InstanceConfig) error {
    if isRootless() {
        cfg.UsernsMode = "keep-id"
    }
    return r.Runtime.Create(ctx, cfg)
}
```

**Socket discovery function** (`discoverSocket`):
1. Check `$CONTAINER_HOST` env var
2. Check `$DOCKER_HOST` env var
3. Check `$XDG_RUNTIME_DIR/podman/podman.sock` (rootless)
4. Check `/run/podman/podman.sock` (system-wide)
5. On macOS: fall back to `podman machine inspect --format '{{.ConnectionInfo.PodmanSocket.Path}}'`

**Rootless detection** (`isRootless`):
- `os.Getuid() != 0` — if not running as root, Podman is in rootless mode.

### Step 3: Add `UsernsMode` to `InstanceConfig`

**File:** `runtime/runtime.go`

Add to `InstanceConfig`:
```go
UsernsMode string // "" = default, "keep-id" = rootless Podman
```

**File:** `runtime/docker/docker.go` — in `Create()`, apply it:
```go
if cfg.UsernsMode != "" {
    hostConfig.UsernsMode = container.UsernsMode(cfg.UsernsMode)
}
```

This keeps the field in the runtime-agnostic config. Docker ignores it (empty string).
Podman sets it in its `Create()` override.

### Step 4: Generalize backend checks in `sandbox/create.go`

**File:** `sandbox/create.go`

Add helper:
```go
func isContainerBackend(backend string) bool {
    return backend == "docker" || backend == "podman"
}
```

Replace four locations:
- **Line 525:** `m.backend != "docker"` → `!isContainerBackend(m.backend)`
- **Line 561:** `m.backend == "docker"` → `isContainerBackend(m.backend)`
- **Line 567:** `m.backend != "docker"` → `!isContainerBackend(m.backend)`
- **Line 574:** `m.backend != "docker"` → `!isContainerBackend(m.backend)`

### Step 5: Register backend

Three locations:

**`yoloai.go:newRuntime()`** — add case:
```go
case "podman":
    return podmanrt.New(ctx)
```

**`internal/cli/helpers.go:newRuntime()`** — same case.

**`internal/cli/info.go:knownBackends`** — add entry:
```go
{Name: "podman", Description: "Linux containers (Podman)", ...}
```

### Step 6: Add to `availableBackends()`

**File:** `sandbox/setup.go`

Add `"podman"` to Linux backends. Consider checking if `podman` binary exists
to decide availability (same pattern as other backends).

### Step 7: Tests

**Unit tests** (`runtime/podman/podman_test.go`):
- Socket discovery logic (mock env vars, mock file existence)
- Rootless detection
- `Name()` returns `"podman"`
- `Create()` injects `UsernsMode` when rootless

**Integration tests** (`runtime/podman/integration_test.go`):
- Same pattern as `runtime/docker/integration_test.go`
- Guarded by build tag or env var (`YOLOAI_TEST_PODMAN=1`)
- Requires running Podman with socket activated

**`sandbox/create.go` tests:**
- Verify `isContainerBackend()` returns true for both `"docker"` and `"podman"`
- Verify existing backend check tests still pass

---

## File Change Summary

| File | Change |
|------|--------|
| `runtime/docker/docker.go` | Add `binaryName` field, export `NewWithSocket()`, use `binaryName` in 3 places |
| `runtime/runtime.go` | Add `UsernsMode` to `InstanceConfig` |
| `runtime/podman/podman.go` | **New.** Socket discovery, constructor, `Name()`, `Create()` override |
| `runtime/podman/podman_test.go` | **New.** Unit tests |
| `sandbox/create.go` | Add `isContainerBackend()`, replace 4 hardcoded checks |
| `yoloai.go` | Add `"podman"` case in `newRuntime()` |
| `internal/cli/helpers.go` | Add `"podman"` case in `newRuntime()` |
| `internal/cli/info.go` | Add podman to `knownBackends` |
| `sandbox/setup.go` | Add `"podman"` to `availableBackends()` |

**Estimated total:** ~150 lines new code, ~30 lines modified.

---

## Commit Plan

1. **Refactor Docker backend for reuse** — export `NewWithSocket()`, add `binaryName` field.
   Pure refactor, no behavior change.
2. **Generalize backend checks** — add `isContainerBackend()`, update `sandbox/create.go`.
   No new backend yet, just removing the blocker.
3. **Add Podman backend** — new `runtime/podman/` package, backend registration,
   `availableBackends()` update.
4. **Add tests** — unit tests for socket discovery / rootless detection, integration tests
   guarded by env var.

---

## Manual Testing Required

These items cannot be verified from source code alone and require hands-on testing.

### Pre-implementation: Podman environment setup

```bash
# Install Podman (Ubuntu 24.04)
sudo apt install podman

# Start socket (rootless)
systemctl --user start podman.socket

# Verify socket exists
ls $XDG_RUNTIME_DIR/podman/podman.sock

# Verify Docker SDK can talk to Podman socket
curl --unix-socket $XDG_RUNTIME_DIR/podman/podman.sock http://localhost/v1.44/info
```

### Test 1: Docker SDK over Podman socket (validates core approach)

Before writing any code, verify the Docker Go SDK works against Podman's socket:

```go
// Quick test program
cli, _ := client.NewClientWithOpts(
    client.WithHost("unix:///run/user/1000/podman/podman.sock"),
    client.WithAPIVersionNegotiation(),
)
info, _ := cli.Ping(context.Background())
fmt.Println(info.APIVersion) // Should print "1.44.0" or similar
```

**What to check:**
- Ping succeeds
- Container create/start/stop/remove cycle works
- `ContainerExec` works (non-interactive)
- `ImageBuild` (SDK path, no secrets) works
- Bind mounts work and file permissions are correct

### Test 2: Rootless file ownership with `--userns=keep-id`

```bash
# Create a file as your user
echo "test" > /tmp/podman-test-file

# Run container with keep-id and bind mount
podman run --userns=keep-id -v /tmp/podman-test-file:/mnt/test:ro \
    alpine stat -c '%u:%g' /mnt/test
# Should show your UID:GID, not 0:0

# Run container WITHOUT keep-id
podman run -v /tmp/podman-test-file:/mnt/test:ro \
    alpine stat -c '%u:%g' /mnt/test
# Compare — may show 0:0 or different mapping
```

**What to check:**
- With `keep-id`: container sees files owned by the mapped user
- `:copy` and `:rw` mounts both work correctly
- Files created inside the container have correct host ownership

### Test 3: `:overlay` mode on rootless Podman (open question)

This is the last open research question.

```bash
# Check kernel version (need 5.11+ for unprivileged overlayfs)
uname -r

# Run container with CAP_SYS_ADMIN and try overlayfs
podman run --cap-add SYS_ADMIN --rm -it alpine sh -c '
    mkdir -p /tmp/lower /tmp/upper /tmp/work /tmp/merged
    echo "base" > /tmp/lower/file.txt
    mount -t overlay overlay \
        -o lowerdir=/tmp/lower,upperdir=/tmp/upper,workdir=/tmp/work \
        /tmp/merged
    cat /tmp/merged/file.txt
    echo "modified" > /tmp/merged/file.txt
    cat /tmp/upper/file.txt
'
```

**What to check:**
- Does `mount -t overlay` succeed in a rootless container with `CAP_SYS_ADMIN`?
- Can files be read from the lower layer and written to the upper layer?
- If it fails, what error? (`EPERM`? `ENOSYS`?)
- Test on kernel 5.11+ and document the minimum kernel version

**If overlay fails:** Not a blocker. Add `"podman"` to the overlay rejection check instead
of `isContainerBackend()`, and document that `:overlay` requires the Docker backend.

### Test 4: Network isolation

```bash
# Verify --network=none works
podman run --network=none alpine ping -c1 8.8.8.8
# Should fail (no network)

# Verify NET_ADMIN capability works (needed for domain-based filtering)
podman run --cap-add NET_ADMIN --rm alpine sh -c 'ip link show'
# Should succeed
```

### Test 5: Full yoloAI lifecycle (post-implementation)

After implementation, run the full lifecycle on Podman:

```bash
# Set backend
export YOLOAI_BACKEND=podman  # or however backend selection works

# Create and enter a sandbox
yoloai create test-podman /path/to/project:copy
yoloai enter test-podman

# Verify inside sandbox:
# - Files are accessible and writable in :copy dir
# - Agent can run
# - File permissions are correct

# Test diff/apply workflow
yoloai diff test-podman
yoloai apply test-podman

# Cleanup
yoloai destroy test-podman
```

### Test 6: CI smoke test

Verify the GitHub Actions setup works:

```yaml
# Add to CI workflow as a separate job
test-podman:
  runs-on: ubuntu-24.04
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version-file: go.mod
    - name: Start Podman socket
      run: systemctl --user start podman.socket
    - name: Run Podman integration tests
      run: YOLOAI_TEST_PODMAN=1 go test ./runtime/podman/ -v -count=1
```

---

## Risk Assessment

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Docker SDK incompatible with Podman socket | Low | High | Pre-validated via source code; Test 1 confirms |
| `:overlay` fails on rootless Podman | Medium | Low | Not a blocker; can exclude Podman from overlay |
| `--userns=keep-id` breaks something | Low | Medium | Test 2 validates; well-documented Podman feature |
| Podman socket not started by default | Certain | Low | Good error message pointing to `systemctl --user start podman.socket` |
| `binaryName` refactor breaks Docker backend | Low | High | Pure refactor; existing Docker tests catch regressions |
