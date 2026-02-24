# Phase 2: Docker Client Wrapper

## Goal

Thin interface wrapping the Docker SDK methods yoloai needs. Enables mocking for unit tests in later phases. No functionality beyond verifying Docker connectivity.

## Prerequisites

- Phase 1 complete (domain types)
- Docker SDK dependency: `go get github.com/docker/docker@latest`
- Docker daemon running (for integration test only)

## Files to Create

| File | Description |
|------|-------------|
| `internal/docker/client.go` | Client interface, real implementation, constructor |
| `internal/docker/client_integration_test.go` | Build-tagged integration test for Docker connectivity |

## Types and Signatures

### `internal/docker/client.go`

```go
package docker

import (
	"context"
	"io"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Client wraps the subset of Docker SDK methods used by yoloai.
// Defined as an interface for testability — later phases mock this
// in unit tests without requiring a real Docker daemon.
type Client interface {
	// Image operations
	ImageBuild(ctx context.Context, buildContext io.Reader, options types.ImageBuildOptions) (types.ImageBuildResponse, error)
	ImageInspectWithRaw(ctx context.Context, imageID string) (types.ImageInspect, []byte, error)

	// Container lifecycle
	ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error)
	ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error
	ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error
	ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error
	ContainerList(ctx context.Context, options container.ListOptions) ([]types.Container, error)
	ContainerInspect(ctx context.Context, containerID string) (types.ContainerJSON, error)

	// Exec (run commands inside containers)
	ContainerExecCreate(ctx context.Context, containerID string, options container.ExecOptions) (types.IDResponse, error)
	ContainerExecAttach(ctx context.Context, execID string, options container.ExecStartOptions) (types.HijackedResponse, error)
	ContainerExecInspect(ctx context.Context, execID string) (container.ExecInspect, error)

	// Connection
	Ping(ctx context.Context) (types.Ping, error)
	Close() error
}

// NewClient creates a Docker client and verifies the daemon is reachable.
// Returns a clear error if Docker is unavailable.
func NewClient(ctx context.Context) (Client, error)
```

**Design notes:**

- The `Client` interface uses Docker SDK types directly — no wrapper types. This avoids a translation layer that adds complexity without value. Consumers import Docker types alongside this package.
- Method signatures match `dockerclient.Client` exactly, so the real client satisfies the interface without an adapter struct.
- `NewClient` calls `client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())` then `Ping(ctx)`. If ping fails, return a wrapped error with actionable guidance (e.g., "is Docker running?").
- The `*dockerclient.Client` from the SDK directly satisfies this interface — no wrapper struct needed for the real implementation.

**Note on SDK types:** The exact type names (e.g., `types.ImageBuildResponse` vs `build.ImageBuildResponse`, `types.ContainerJSON` vs `container.InspectResponse`) vary between Docker SDK versions. The implementation phase should verify the exact types against the installed SDK version and adjust the interface accordingly. The method names and semantics are stable.

### Constructor implementation sketch

```go
func NewClient(ctx context.Context) (Client, error) {
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.FromEnv,
		dockerclient.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("create Docker client: %w", err)
	}

	_, err = cli.Ping(ctx)
	if err != nil {
		cli.Close()
		return nil, fmt.Errorf("connect to Docker: %w (is Docker running?)", err)
	}

	return cli, nil
}
```

The `*dockerclient.Client` returned by `NewClientWithOpts` already implements all the methods in our `Client` interface, so it can be returned directly without wrapping.

## Implementation Steps

1. **Add Docker SDK dependency:**
   ```
   go get github.com/docker/docker@latest
   go mod tidy
   ```
   This will pull in transitive dependencies. The `+incompatible` suffix in go.sum is expected and not a stability concern (see PLAN.md dependencies section).

2. **Create `internal/docker/client.go`:**
   - Define the `Client` interface with all methods listed above
   - Implement `NewClient` — create client with `FromEnv` + `WithAPIVersionNegotiation`, ping, return
   - Verify that `*dockerclient.Client` satisfies the interface with a compile-time check:
     ```go
     var _ Client = (*dockerclient.Client)(nil)
     ```
   - If the SDK types don't match exactly (version differences), adjust the interface signatures to match the installed version. The method names are stable; the type packaging may differ.

3. **Create `internal/docker/client_integration_test.go`:**
   - Build tag: `//go:build integration`
   - Test `NewClient` succeeds when Docker is running
   - Test that `Ping` returns a non-empty API version
   - No container creation — just connectivity verification

4. **Run `go mod tidy`.**

## Tests

### `internal/docker/client_integration_test.go`

```go
//go:build integration

package docker

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient_ConnectsToDocker(t *testing.T) {
	ctx := context.Background()
	client, err := NewClient(ctx)
	require.NoError(t, err, "Docker must be running for integration tests")
	defer client.Close()

	ping, err := client.Ping(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, ping.APIVersion)
}
```

No unit tests — the package is a pure wrapper with no logic beyond the constructor. The compile-time interface check (`var _ Client = ...`) catches type mismatches at build time. Real testing happens via the integration test.

## Verification

```bash
# Must compile (verifies interface satisfaction)
go build ./...

# Linter must pass
make lint

# Unit tests pass (no docker package unit tests, but existing tests still work)
make test

# Integration test (requires Docker running)
go test -tags=integration ./internal/docker/...
```

## Concerns

None. This is a thin interface layer — the main risk is SDK type mismatches between versions, which the compile-time check catches immediately. The interface may need minor signature adjustments when the dependency is actually added, depending on the exact SDK version resolved.
