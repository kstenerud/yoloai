package cliutil_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/internal/testutil"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ResolveBackend ---

func TestResolveBackend_FlagSet(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("backend", "", "")
	require.NoError(t, cmd.Flags().Set("backend", "tart"))

	assert.Equal(t, runtime.BackendType("tart"), cliutil.ResolveBackend(cmd))
}

func TestResolveBackend_IsolationVM(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("backend", "", "")
	cmd.Flags().String("isolation", "", "")
	cmd.Flags().String("os", "", "")
	require.NoError(t, cmd.Flags().Set("isolation", "vm"))

	result := cliutil.ResolveBackend(cmd)
	// Result depends on platform and config. Just verify it returns an available backend.
	assert.True(t, runtime.IsAvailable(result), "ResolveBackend returned unavailable backend: %s (available: %v)", result, runtime.Available())
}

func TestResolveBackend_IsolationVMEnhanced(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("backend", "", "")
	cmd.Flags().String("isolation", "", "")
	cmd.Flags().String("os", "", "")
	require.NoError(t, cmd.Flags().Set("isolation", "vm-enhanced"))

	result := cliutil.ResolveBackend(cmd)
	// Result depends on platform and config. Just verify it returns an available backend.
	assert.True(t, runtime.IsAvailable(result), "ResolveBackend returned unavailable backend: %s (available: %v)", result, runtime.Available())
}

func TestResolveBackend_OsMac(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("backend", "", "")
	cmd.Flags().String("isolation", "", "")
	cmd.Flags().String("os", "", "")
	require.NoError(t, cmd.Flags().Set("os", "mac"))

	assert.Equal(t, runtime.BackendType("seatbelt"), cliutil.ResolveBackend(cmd))
}

func TestResolveBackend_OsMacIsolationVM(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("backend", "", "")
	cmd.Flags().String("isolation", "", "")
	cmd.Flags().String("os", "", "")
	require.NoError(t, cmd.Flags().Set("os", "mac"))
	require.NoError(t, cmd.Flags().Set("isolation", "vm"))

	assert.Equal(t, runtime.BackendType("tart"), cliutil.ResolveBackend(cmd))
}

func TestResolveBackend_ConfigOsMacFlagIsolationVM(t *testing.T) {
	dir := testutil.CLIConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("os: mac\n"), 0600))

	cmd := &cobra.Command{}
	cmd.Flags().String("backend", "", "")
	cmd.Flags().String("isolation", "", "")
	cmd.Flags().String("os", "", "")
	require.NoError(t, cmd.Flags().Set("isolation", "vm"))

	assert.Equal(t, runtime.BackendType("tart"), cliutil.ResolveBackend(cmd))
}

func TestResolveBackend_FlagEmptyNoConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cmd := &cobra.Command{}
	cmd.Flags().String("backend", "", "")

	assert.Equal(t, runtime.BackendType("docker"), cliutil.ResolveBackend(cmd))
}

// --- ResolveContainerBackendConfig ---

func TestResolveContainerBackendConfig_HasBackend(t *testing.T) {
	dir := testutil.CLIConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("container_backend: tart\n"), 0600))

	assert.Equal(t, runtime.BackendType("tart"), cliutil.ResolveContainerBackendConfig())
}

func TestResolveContainerBackendConfig_Empty(t *testing.T) {
	dir := testutil.CLIConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("agent: claude\n"), 0600))

	assert.Equal(t, runtime.BackendType(""), cliutil.ResolveContainerBackendConfig())
}

func TestResolveContainerBackendConfig_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	assert.Equal(t, runtime.BackendType(""), cliutil.ResolveContainerBackendConfig())
}

// --- ResolveBackendForSandbox ---

func TestResolveBackendForSandbox_MetaHasBackend(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "test-backend"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "library", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Environment{
		Name:      name,
		Backend:   "tart",
		Agent:     "claude",
		CreatedAt: time.Now(),
		Workdir:   store.WorkdirEnvironment{HostPath: "/tmp/test", Mode: "copy"},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	assert.Equal(t, runtime.BackendType("tart"), cliutil.ResolveBackendForSandbox(name))
}

func TestResolveBackendForSandbox_MetaMissing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// No sandbox dir exists → falls back to config default
	assert.Equal(t, runtime.BackendType("docker"), cliutil.ResolveBackendForSandbox("nonexistent"))
}

func TestResolveBackendForSandbox_MetaEmptyBackend(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "test-empty-backend"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "library", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Environment{
		Name:      name,
		Agent:     "claude",
		CreatedAt: time.Now(),
		Workdir:   store.WorkdirEnvironment{HostPath: "/tmp/test", Mode: "copy"},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	assert.Equal(t, runtime.BackendType("docker"), cliutil.ResolveBackendForSandbox(name))
}

// --- ResolveAgent ---

func TestResolveAgent_FlagSet(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("agent", "", "")
	require.NoError(t, cmd.Flags().Set("agent", "gemini"))

	assert.Equal(t, "gemini", cliutil.ResolveAgent(cmd))
}

func TestResolveAgent_FlagEmptyWithConfig(t *testing.T) {
	dir := testutil.CLIConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("agent: aider\n"), 0600))

	cmd := &cobra.Command{}
	cmd.Flags().String("agent", "", "")

	assert.Equal(t, "aider", cliutil.ResolveAgent(cmd))
}

func TestResolveAgent_FlagEmptyNoConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cmd := &cobra.Command{}
	cmd.Flags().String("agent", "", "")

	assert.Equal(t, "claude", cliutil.ResolveAgent(cmd))
}

// --- ResolveAgentFromConfig ---

func TestResolveAgentFromConfig_HasAgent(t *testing.T) {
	dir := testutil.CLIConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("agent: gemini\n"), 0600))

	assert.Equal(t, "gemini", cliutil.ResolveAgentFromConfig())
}

func TestResolveAgentFromConfig_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	assert.Equal(t, "claude", cliutil.ResolveAgentFromConfig())
}

// --- ResolveModel ---

func TestResolveModel_FlagSet(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("model", "", "")
	require.NoError(t, cmd.Flags().Set("model", "gpt-4o"))

	assert.Equal(t, "gpt-4o", cliutil.ResolveModel(cmd))
}

func TestResolveModel_FlagEmptyWithConfig(t *testing.T) {
	dir := testutil.CLIConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("model: sonnet\n"), 0600))

	cmd := &cobra.Command{}
	cmd.Flags().String("model", "", "")

	assert.Equal(t, "sonnet", cliutil.ResolveModel(cmd))
}

func TestResolveModel_FlagEmptyNoConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cmd := &cobra.Command{}
	cmd.Flags().String("model", "", "")

	assert.Equal(t, "", cliutil.ResolveModel(cmd))
}

// --- ResolveModelFromConfig ---

func TestResolveModelFromConfig_HasModel(t *testing.T) {
	dir := testutil.CLIConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("model: opus\n"), 0600))

	assert.Equal(t, "opus", cliutil.ResolveModelFromConfig())
}

func TestResolveModelFromConfig_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	assert.Equal(t, "", cliutil.ResolveModelFromConfig())
}

// --- ResolveProfile ---

func TestResolveProfile_FlagSet(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("profile", "", "")
	cmd.Flags().Bool("no-profile", false, "")
	require.NoError(t, cmd.Flags().Set("profile", "custom"))

	assert.Equal(t, "custom", cliutil.ResolveProfile(cmd))
}

func TestResolveProfile_NoProfileBypass(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("profile", "", "")
	cmd.Flags().Bool("no-profile", false, "")
	require.NoError(t, cmd.Flags().Set("no-profile", "true"))
	require.NoError(t, cmd.Flags().Set("profile", "custom"))

	assert.Equal(t, "", cliutil.ResolveProfile(cmd))
}

func TestResolveProfile_FlagEmptyWithConfig(t *testing.T) {
	// ResolveProfile no longer reads profile from config — flag only.
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cmd := &cobra.Command{}
	cmd.Flags().String("profile", "", "")
	cmd.Flags().Bool("no-profile", false, "")

	assert.Equal(t, "", cliutil.ResolveProfile(cmd))
}

func TestResolveProfile_FlagEmptyNoConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cmd := &cobra.Command{}
	cmd.Flags().String("profile", "", "")
	cmd.Flags().Bool("no-profile", false, "")

	assert.Equal(t, "", cliutil.ResolveProfile(cmd))
}

// --- SandboxErrorHint ---

func TestSandboxErrorHint_NilErr(t *testing.T) {
	assert.NoError(t, cliutil.SandboxErrorHint("test", nil))
}

func TestSandboxErrorHint_ErrSandboxNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	err := cliutil.SandboxErrorHint("test", sandbox.ErrSandboxNotFound)
	assert.ErrorIs(t, err, sandbox.ErrSandboxNotFound)
	// Should NOT contain the hint (no directory to point at)
	assert.NotContains(t, err.Error(), "to remove:")
}

func TestSandboxErrorHint_GenericError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	origErr := errors.New("something went wrong")
	err := cliutil.SandboxErrorHint("test", origErr)
	assert.ErrorIs(t, err, origErr)
	assert.Contains(t, err.Error(), "sandbox dir:")
	assert.Contains(t, err.Error(), "to remove: yoloai destroy test")
}
