//go:build integration

// ABOUTME: Backend-agnostic behavioral conformance suite. Exercises the
// ABOUTME: runtime.Backend contract through interface methods only, so every
// ABOUTME: backend (docker, podman, containerd, tart, seatbelt, apple) verifies
// ABOUTME: one shared table. Sections a backend cannot support are declared
// ABOUTME: skipped (with a reason) rather than forced, keeping results legible.
package runtimetest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/runtime"
)

// Sleeper creates (but does not start) a long-running instance with cfg applied,
// registers its teardown via t.Cleanup, and returns the instance name. How a
// backend keeps a container/VM alive for exec tests is genuinely backend-
// specific — an OCI "sleep infinity" entrypoint override (docker/podman), a
// sleep image (apple/containerd), or a VM/host process (tart/seatbelt) — so each
// backend supplies its own. The suite owns naming (cfg.Name is pre-set) and the
// behavioral knobs it varies (mounts); the Sleeper fills backend defaults
// (image, entrypoint) and handles any pre-create eviction of stale leftovers.
type Sleeper func(t *testing.T, cfg runtime.InstanceConfig) string

// InterfaceBackend is the per-test fixture a backend hands the conformance suite.
type InterfaceBackend struct {
	Runtime    runtime.Backend
	Ctx        context.Context
	NewSleeper Sleeper

	// SkipMounts and SkipStdio each name a behavioral section this backend
	// cannot honor, with a one-line reason. Non-empty → the suite reports a
	// SKIP for that section (legible result) instead of forcing an inapplicable
	// assertion. Empty → the section runs. Examples: a backend whose state lives
	// directly on the host filesystem has no bind-mount semantics to verify; a
	// backend that does not implement runtime.StdioExecer skips the stdio
	// section (the suite also detects that case automatically).
	SkipMounts string
	SkipStdio  string
}

// InterfaceSetupFunc connects to a backend and returns a fresh per-test fixture
// with cleanup already registered (e.g. rt.Close via t.Cleanup).
type InterfaceSetupFunc func(t *testing.T) InterfaceBackend

// conformanceInstanceName flattens the subtest name into a legal instance name.
// Subtest names carry a "/" (e.g. "TestAppleConformance/ExecSimple"), illegal in
// a container/VM name. Kept in the shared suite so every backend names instances
// identically.
func conformanceInstanceName(t *testing.T) string {
	t.Helper()
	return "yoloai-test-" + strings.ReplaceAll(t.Name(), "/", "-")
}

// RunInterfaceConformance exercises the universal runtime.Backend contract every
// backend must honor, plus capability-gated sections a backend opts into via the
// InterfaceBackend skip fields. Each subtest calls setup for its own isolated
// fixture, matching the per-test isolation the backend-specific suites had before
// unification.
func RunInterfaceConformance(t *testing.T, setup InterfaceSetupFunc) {
	// sleeper creates a started, long-running instance and returns its name.
	sleeper := func(t *testing.T, b InterfaceBackend, cfg runtime.InstanceConfig) string {
		t.Helper()
		if cfg.Name == "" {
			cfg.Name = conformanceInstanceName(t)
		}
		name := b.NewSleeper(t, cfg)
		require.NoError(t, b.Runtime.Start(b.Ctx, name))
		return name
	}

	// --- Property invariants (no VM needed) ---

	// A backend that runs the work-copy git in confinement (Tart's VM, or a
	// container backend — audit C1) MUST implement GitExecer; git.NewSandbox
	// dispatches the work-copy git through it for exactly that set of backends.
	t.Run("ConfinementImpliesGitExecer", func(t *testing.T) {
		b := setup(t)
		if !runtime.GitRunsInConfinement(b.Runtime) {
			t.Skip("host-side git backend: no in-confinement git execution")
		}
		_, isGitExecer := b.Runtime.(runtime.GitExecer)
		assert.True(t, isGitExecer, "GitRunsInConfinement backend must implement runtime.GitExecer (git runs in the sandbox)")
	})

	// A SandboxSide backend additionally keeps its work copy inside the sandbox,
	// so baseline creation is deferred to the sandbox (WorkDirSetup). The
	// property-based dispatch in ExecuteVMWorkDirSetup assumes this invariant.
	t.Run("SandboxSideImplementsLocalityOps", func(t *testing.T) {
		b := setup(t)
		if b.Runtime.Descriptor().Capabilities.FilesystemLocality != runtime.LocalitySandboxSide {
			t.Skip("HostSide backend: no in-sandbox locality operations required")
		}
		_, isWorkDirSetup := b.Runtime.(runtime.WorkDirSetup)
		assert.True(t, isWorkDirSetup, "SandboxSide backend must implement runtime.WorkDirSetup (baseline deferred to sandbox)")
	})

	// --- Universal lifecycle ---

	t.Run("CreateStartStopRemove", func(t *testing.T) {
		b := setup(t)
		name := conformanceInstanceName(t)
		created := b.NewSleeper(t, runtime.InstanceConfig{Name: name})

		require.NoError(t, b.Runtime.Start(b.Ctx, created))
		info, err := b.Runtime.Inspect(b.Ctx, created)
		require.NoError(t, err)
		assert.True(t, info.Running)

		require.NoError(t, b.Runtime.Stop(b.Ctx, created))
		info, err = b.Runtime.Inspect(b.Ctx, created)
		require.NoError(t, err)
		assert.False(t, info.Running)

		require.NoError(t, b.Runtime.Remove(b.Ctx, created))
		_, err = b.Runtime.Inspect(b.Ctx, created)
		assert.ErrorIs(t, err, runtime.ErrNotFound)
	})

	t.Run("InspectRunning", func(t *testing.T) {
		b := setup(t)
		name := sleeper(t, b, runtime.InstanceConfig{})
		info, err := b.Runtime.Inspect(b.Ctx, name)
		require.NoError(t, err)
		assert.True(t, info.Running)
	})

	t.Run("InspectStopped", func(t *testing.T) {
		b := setup(t)
		name := sleeper(t, b, runtime.InstanceConfig{})
		require.NoError(t, b.Runtime.Stop(b.Ctx, name))
		info, err := b.Runtime.Inspect(b.Ctx, name)
		require.NoError(t, err)
		assert.False(t, info.Running)
	})

	t.Run("InspectNotFound", func(t *testing.T) {
		b := setup(t)
		_, err := b.Runtime.Inspect(b.Ctx, "yoloai-nonexistent-instance-xyz")
		assert.ErrorIs(t, err, runtime.ErrNotFound)
	})

	t.Run("StopIdempotent", func(t *testing.T) {
		b := setup(t)
		name := sleeper(t, b, runtime.InstanceConfig{})
		require.NoError(t, b.Runtime.Stop(b.Ctx, name))
		assert.NoError(t, b.Runtime.Stop(b.Ctx, name), "second Stop is a no-op")
	})

	t.Run("RemoveIdempotent", func(t *testing.T) {
		b := setup(t)
		name := b.NewSleeper(t, runtime.InstanceConfig{Name: conformanceInstanceName(t)})
		require.NoError(t, b.Runtime.Remove(b.Ctx, name))
		assert.NoError(t, b.Runtime.Remove(b.Ctx, name), "second Remove is a no-op")
	})

	t.Run("IsReady", func(t *testing.T) {
		b := setup(t)
		ready, err := b.Runtime.IsReady(b.Ctx)
		require.NoError(t, err)
		assert.True(t, ready)
	})

	// --- Universal exec ---

	t.Run("ExecSimple", func(t *testing.T) {
		b := setup(t)
		name := sleeper(t, b, runtime.InstanceConfig{})
		res, err := b.Runtime.Exec(b.Ctx, name, []string{"echo", "hello"}, "")
		require.NoError(t, err)
		assert.Equal(t, "hello", res.Stdout)
		assert.Equal(t, 0, res.ExitCode)
	})

	t.Run("ExecNonZeroExit", func(t *testing.T) {
		b := setup(t)
		name := sleeper(t, b, runtime.InstanceConfig{})
		res, err := b.Runtime.Exec(b.Ctx, name, []string{"sh", "-c", "exit 42"}, "")
		assert.Error(t, err)
		assert.Equal(t, 42, res.ExitCode)
	})

	// ExecOnStopped is the DF18 "exec into a stopped instance" error path: a
	// created-then-stopped instance must reject exec rather than hang or panic.
	t.Run("ExecOnStopped", func(t *testing.T) {
		b := setup(t)
		name := sleeper(t, b, runtime.InstanceConfig{})
		require.NoError(t, b.Runtime.Stop(b.Ctx, name))
		_, err := b.Runtime.Exec(b.Ctx, name, []string{"echo", "hello"}, "")
		assert.Error(t, err, "exec into a stopped instance must error")
	})

	t.Run("InteractiveExecZeroExit", func(t *testing.T) {
		b := setup(t)
		name := sleeper(t, b, runtime.InstanceConfig{})
		var out strings.Builder
		err := b.Runtime.InteractiveExec(b.Ctx, name, []string{"true"}, "", "",
			runtime.IOStreams{Out: &out, TTY: true})
		assert.NoError(t, err, "exit 0 stays nil")
	})

	t.Run("InteractiveExecNonZeroExit", func(t *testing.T) {
		b := setup(t)
		name := sleeper(t, b, runtime.InstanceConfig{})
		var out strings.Builder
		err := b.Runtime.InteractiveExec(b.Ctx, name, []string{"sh", "-c", "exit 9"}, "", "",
			runtime.IOStreams{Out: &out, TTY: true})
		var execErr *runtime.ExecError
		require.ErrorAs(t, err, &execErr, "TTY exec non-zero exit must surface as *runtime.ExecError")
		assert.Equal(t, 9, execErr.ExitCode)
	})

	// --- Stdio section (gated: SkipStdio or no StdioExecer) ---

	t.Run("Stdio", func(t *testing.T) {
		b := setup(t)
		if b.SkipStdio != "" {
			t.Skip(b.SkipStdio)
		}
		execer, ok := b.Runtime.(runtime.StdioExecer)
		if !ok {
			t.Skip("backend does not implement runtime.StdioExecer")
		}

		t.Run("PipesOutput", func(t *testing.T) {
			name := sleeper(t, b, runtime.InstanceConfig{})
			var stdout, stderr strings.Builder
			err := execer.StdioExec(b.Ctx, name, []string{"echo", "hello"}, nil, &stdout, &stderr)
			require.NoError(t, err)
			assert.Equal(t, "hello", strings.TrimSpace(stdout.String()))
		})

		t.Run("NonZeroExit", func(t *testing.T) {
			name := sleeper(t, b, runtime.InstanceConfig{})
			err := execer.StdioExec(b.Ctx, name, []string{"sh", "-c", "exit 7"}, nil, nil, nil)
			var execErr *runtime.ExecError
			require.ErrorAs(t, err, &execErr, "non-zero exit must surface as *runtime.ExecError")
			assert.Equal(t, 7, execErr.ExitCode)
		})
	})

	// --- Bind-mount section (gated: SkipMounts) ---

	t.Run("Mounts", func(t *testing.T) {
		b := setup(t)
		if b.SkipMounts != "" {
			t.Skip(b.SkipMounts)
		}

		t.Run("ReadWrite", func(t *testing.T) {
			hostDir := t.TempDir()
			name := sleeper(t, b, runtime.InstanceConfig{
				Mounts: []runtime.MountSpec{{HostPath: hostDir, ContainerPath: "/mnt/test", ReadOnly: false}},
			})
			_, err := b.Runtime.Exec(b.Ctx, name, []string{"sh", "-c", "echo hello > /mnt/test/output.txt"}, "")
			require.NoError(t, err)
			content, err := os.ReadFile(filepath.Join(hostDir, "output.txt"))
			require.NoError(t, err)
			assert.Contains(t, string(content), "hello")
		})

		t.Run("ReadOnly", func(t *testing.T) {
			hostDir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(hostDir, "readonly.txt"), []byte("original"), 0o600))
			name := sleeper(t, b, runtime.InstanceConfig{
				Mounts: []runtime.MountSpec{{HostPath: hostDir, ContainerPath: "/mnt/test", ReadOnly: true}},
			})
			res, err := b.Runtime.Exec(b.Ctx, name, []string{"cat", "/mnt/test/readonly.txt"}, "")
			require.NoError(t, err)
			assert.Equal(t, "original", res.Stdout)

			_, err = b.Runtime.Exec(b.Ctx, name, []string{"sh", "-c", "echo modified > /mnt/test/readonly.txt"}, "")
			assert.Error(t, err, "write to a read-only mount must fail")
		})
	})
}
