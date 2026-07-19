// ABOUTME: White-box tests for the Engine lifecycle/create verbs — the
// ABOUTME: DestroyForOverwrite missing-destination no-op short-circuit, and
// ABOUTME: depsForSandbox's three resolution branches (reuse, fallback, and the
// ABOUTME: cross-backend open that DF138 exists to fix).

package orchestrator

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
)

// crossBackendType is the registry name for a fake backend distinct from the
// mockRuntime's "mock", so a sandbox recorded under it forces depsForSandbox
// down its cross-backend-open branch. Registered process-wide (the registry
// panics on a double Register), so registration is guarded by a sync.Once. The
// factory returns a fresh *mockRuntime each call, which the test captures via
// deps.Runtime to assert the opened runtime — not the Engine's own — is closed.
const crossBackendType runtime.BackendType = "df138crossfake"

var crossBackendOnce sync.Once

func registerCrossBackendFake(t *testing.T) {
	t.Helper()
	crossBackendOnce.Do(func() {
		runtime.Register(
			func(context.Context, config.Layout) (runtime.Backend, error) {
				return &mockRuntime{}, nil
			},
			runtime.BackendDescriptor{Type: crossBackendType},
		)
	})
}

// DestroyForOverwrite must short-circuit (and never touch the runtime) when the
// destination doesn't exist — an Overwrite clone onto a fresh name is a plain
// clone. The injected nil-runtime Engine latches opened, so ensure is a no-op
// and the os.Stat miss returns before any runtime call.
func TestEngine_DestroyForOverwrite_MissingDestIsNoop(t *testing.T) {
	layout := config.NewLayout(filepath.Join(t.TempDir(), ".yoloai"))
	e := NewEngineWithRuntime(nil, slog.Default(), strings.NewReader(""), WithLayout(layout))
	require.NoError(t, e.DestroyForOverwrite(context.Background(), "ghost"))
}

// depsForSandbox must reuse the Engine's own runtime (never open a second one)
// when the sandbox's recorded backend already matches the Engine's — the
// common case, and the one every existing single-backend caller hits on every
// call (DF138 only changes behavior when the recorded backend differs).
func TestEngine_DepsForSandbox_ReusesEngineRuntimeWhenBackendMatches(t *testing.T) {
	layout := config.NewLayout(filepath.Join(t.TempDir(), ".yoloai"))
	mock := &mockRuntime{}
	e := NewEngineWithRuntime(mock, slog.Default(), strings.NewReader(""), WithLayout(layout))

	name := "same-backend"
	sandboxDir := layout.SandboxDir(name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))
	require.NoError(t, store.SaveEnvironment(sandboxDir, &store.Environment{
		Name:        name,
		BackendType: mock.Descriptor().Type, // "mock" — matches e.backend
		CreatedAt:   time.Now(),
	}))

	deps, cleanup, err := e.depsForSandbox(context.Background(), name)
	require.NoError(t, err)
	require.Same(t, mock, deps.Runtime, "same-backend sandbox must reuse the Engine's own runtime, not open a new one")

	cleanup()
	require.Equal(t, 0, mock.closeCalls, "cleanup for a reused runtime must not close it")
}

// depsForSandbox must fall back to the Engine's own deps, without error, when
// the sandbox's environment.json is missing or unreadable (e.g. a pre-D62
// record that never recorded a backend) — it must not treat unreadable
// metadata as a hard failure.
func TestEngine_DepsForSandbox_FallsBackWhenMetadataUnreadable(t *testing.T) {
	layout := config.NewLayout(filepath.Join(t.TempDir(), ".yoloai"))
	mock := &mockRuntime{}
	e := NewEngineWithRuntime(mock, slog.Default(), strings.NewReader(""), WithLayout(layout))

	// No environment.json written for "ghost" — LoadEnvironment fails to read it.
	deps, cleanup, err := e.depsForSandbox(context.Background(), "ghost")
	require.NoError(t, err)
	require.Same(t, mock, deps.Runtime, "unreadable metadata must fall back to the Engine's own runtime")

	cleanup()
	require.Equal(t, 0, mock.closeCalls)
}

// depsForSandbox must open a SEPARATE runtime for the sandbox's own recorded
// backend when it differs from the Engine's, and its cleanup must close that
// opened runtime while leaving the Engine's own untouched. This is the exact
// behavior DF138 exists to fix — a --all/wildcard batch tearing down a sandbox
// created on another backend — so it gets a regression guard rather than relying
// on the one-time real-hardware verification.
func TestEngine_DepsForSandbox_OpensAndClosesForDifferentBackend(t *testing.T) {
	registerCrossBackendFake(t)

	layout := config.NewLayout(filepath.Join(t.TempDir(), ".yoloai"))
	mock := &mockRuntime{}
	e := NewEngineWithRuntime(mock, slog.Default(), strings.NewReader(""), WithLayout(layout))

	name := "other-backend"
	sandboxDir := layout.SandboxDir(name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))
	require.NoError(t, store.SaveEnvironment(sandboxDir, &store.Environment{
		Name:        name,
		BackendType: crossBackendType, // differs from e.backend ("mock")
		CreatedAt:   time.Now(),
	}))

	deps, cleanup, err := e.depsForSandbox(context.Background(), name)
	require.NoError(t, err)

	opened, ok := deps.Runtime.(*mockRuntime)
	require.True(t, ok, "a differing backend must yield a runtime opened via the registry")
	require.NotSame(t, mock, opened, "the opened runtime must be the recorded backend's, not the Engine's own")
	require.Equal(t, 0, opened.closeCalls, "the opened runtime must not be closed before cleanup")

	cleanup()
	require.Equal(t, 1, opened.closeCalls, "cleanup must close the runtime it opened")
	require.Equal(t, 0, mock.closeCalls, "cleanup must not close the Engine's own runtime")
}
