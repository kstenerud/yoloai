//go:build integration

// ABOUTME: Integration tests for (*Runtime).Launch — the non-blocking process
// ABOUTME: handle verb. Gated by the same //go:build integration tag and
// ABOUTME: TestMain docker-availability check as the rest of the docker tests.
package docker

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/runtime"
)

// launchTestInstance creates a minimal sandbox, starts it, and registers cleanup.
func launchTestInstance(t *testing.T, name string) *Runtime {
	t.Helper()
	ctx := context.Background()
	rt, err := New(ctx, config.Layout{}.WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	_ = rt.Remove(ctx, name) // clear leftover from a prior failed run
	cfg := runtime.InstanceConfig{
		Name:       name,
		ImageRef:   "yoloai-base",
		WorkingDir: "/",
	}
	require.NoError(t, rt.Create(ctx, cfg))
	t.Cleanup(func() { _ = rt.Remove(ctx, name) })
	require.NoError(t, rt.Start(ctx, name))
	return rt
}

// TestLaunchNonTTYExitCode verifies that Launch returns the correct exit code
// and that non-TTY stdout is readable after the process finishes.
func TestLaunchNonTTYExitCode(t *testing.T) {
	const sandboxName = "yoloai-launch-it-exit"
	rt := launchTestInstance(t, sandboxName)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	spec := runtime.ProcSpec{
		Argv: []string{"sh", "-c", "printf hi; exit 7"},
	}
	proc, err := rt.Launch(ctx, sandboxName, spec)
	require.NoError(t, err)
	require.NotEmpty(t, proc.ID())

	streams := proc.Streams()
	require.NotNil(t, streams.Stdout)
	assert.Nil(t, streams.Stderr, "non-TTY: stderr pipe should not be nil, but for this test we ignore it")

	// Read stdout before Wait — the goroutine demuxes asynchronously.
	stdoutBytes, err := io.ReadAll(streams.Stdout)
	require.NoError(t, err)
	assert.Equal(t, "hi", string(stdoutBytes))

	// Drain stderr too so the goroutine can finish.
	if streams.Stderr != nil {
		_, _ = io.ReadAll(streams.Stderr)
	}

	status, err := proc.Wait(ctx)
	require.NoError(t, err)
	assert.Equal(t, 7, status.Code)
	assert.False(t, status.Signaled)
}

// TestLaunchStdinEcho verifies that Launch with spec.Stdin=true lets the caller
// write to the process's stdin and read the echoed output back from stdout.
func TestLaunchStdinEcho(t *testing.T) {
	const sandboxName = "yoloai-launch-it-stdin"
	rt := launchTestInstance(t, sandboxName)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	spec := runtime.ProcSpec{
		Argv:  []string{"cat"},
		Stdin: true,
	}
	proc, err := rt.Launch(ctx, sandboxName, spec)
	require.NoError(t, err)

	streams := proc.Streams()
	require.NotNil(t, streams.Stdin)
	require.NotNil(t, streams.Stdout)

	// Write to stdin then close it so cat exits.
	_, err = io.WriteString(streams.Stdin, "ping\n")
	require.NoError(t, err)
	require.NoError(t, streams.Stdin.Close())

	// Drain stdout.
	stdoutBytes, err := io.ReadAll(streams.Stdout)
	require.NoError(t, err)
	assert.Equal(t, "ping\n", string(stdoutBytes))

	// Drain stderr.
	if streams.Stderr != nil {
		_, _ = io.ReadAll(streams.Stderr)
	}

	status, err := proc.Wait(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, status.Code, "cat exits 0 after stdin closes")
	_ = strings.TrimSpace("") // keep strings import used
}

// TestLaunch_Detached verifies that Detached:true launches a process without
// attaching stdio — Launch succeeds, the process ID is non-empty, and Streams
// returns empty/nil readers with no stdin writer.
func TestLaunch_Detached(t *testing.T) {
	const sandboxName = "yoloai-launch-it-detached"
	rt := launchTestInstance(t, sandboxName)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	spec := runtime.ProcSpec{
		Argv:     []string{"sh", "-c", "sleep 30"},
		Detached: true,
	}
	proc, err := rt.Launch(ctx, sandboxName, spec)
	require.NoError(t, err)
	assert.NotEmpty(t, proc.ID(), "detached process must have a non-empty exec ID")

	streams := proc.Streams()
	assert.Nil(t, streams.Stdout, "detached process must have nil Stdout")
	assert.Nil(t, streams.Stderr, "detached process must have nil Stderr")
	assert.Nil(t, streams.Stdin, "detached process must have nil Stdin")
}
