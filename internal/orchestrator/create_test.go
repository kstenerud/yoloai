// ABOUTME: Tests the create pipeline's cleanup behavior: a leftover incomplete
// ABOUTME: sandbox dir doesn't trip ErrSandboxExists, and a failed prepare step
// ABOUTME: removes both the sandbox dir and its lock file.
package orchestrator

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/orchestrator/create"
	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/kstenerud/yoloai/runtime"
)

func TestBackendCaps(t *testing.T) {
	assert.True(t, mustDescriptor(t, "docker").Capabilities.CapAdd)
	assert.False(t, mustDescriptor(t, "tart").Capabilities.CapAdd)
	assert.False(t, mustDescriptor(t, "seatbelt").Capabilities.CapAdd)
}

func TestBackendCaps_CustomDNSIsAppleOnly(t *testing.T) {
	for _, backend := range []runtime.BackendType{"docker", "podman", "tart", "seatbelt"} {
		desc, ok := runtime.Descriptor(backend)
		if !ok {
			continue // platform-specific backend; its package's tests assert the same contract
		}
		assert.False(t, desc.Capabilities.CustomDNS, "%s must reject custom DNS", backend)
	}
	apple, ok := runtime.Descriptor(runtime.BackendApple)
	if ok {
		assert.True(t, apple.Capabilities.CustomDNS)
	}
	containerd, ok := runtime.Descriptor("containerd")
	if ok {
		assert.False(t, containerd.Capabilities.CustomDNS)
	}
}

func TestCreate_CleansUpIncompleteOnNew(t *testing.T) {
	tmpDir := t.TempDir()

	// Create sandbox dir without environment.json (incomplete from prior failure)
	name := "incomplete"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	// create.Run should auto-clean the incomplete dir.
	// It will fail later (no agent, etc.) but the key assertion is
	// that it does NOT return ErrSandboxExists.
	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai"))
	d := state.Deps{
		Runtime: &mockRuntime{},
		Layout:  layout,
		Input:   strings.NewReader(""),
	}
	// EnsureSetup is not called here (it's a unit test of the create pipeline)
	_, err := create.Run(context.Background(), d, create.Options{
		Name:    name,
		Workdir: create.DirSpec{Path: tmpDir},
		Agent:   "test",
		Version: "test",
	}, func(context.Context, io.Writer) error { return nil })
	// Will fail for other reasons (no API key etc.), but must NOT be ErrSandboxExists
	assert.NotErrorIs(t, err, ErrSandboxExists)
}

func TestCreate_CleansUpOnPrepareFail(t *testing.T) {
	tmpDir := t.TempDir()

	name := "cleanup-test"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)

	// Use test agent which needs no API key, but provide a nonexistent workdir
	// so preparation fails after directory creation.
	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai"))
	d := state.Deps{
		Runtime: &mockRuntime{},
		Layout:  layout,
		Input:   strings.NewReader(""),
	}
	_, err := create.Run(context.Background(), d, create.Options{
		Name:    name,
		Workdir: create.DirSpec{Path: filepath.Join(tmpDir, "nonexistent")},
		Agent:   "test",
		Version: "test",
	}, func(context.Context, io.Writer) error { return nil })
	require.Error(t, err)

	// Sandbox directory should not exist (cleaned up on failure)
	assert.NoDirExists(t, sandboxDir)
	// The lock file created at acquire-time must also be cleaned up so it
	// doesn't accumulate after a rolled-back Create.
	assert.NoFileExists(t, layout.SandboxLockPath(name))
}
