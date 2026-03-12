package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- resolveBackend ---

func TestResolveBackend_FlagSet(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("backend", "", "")
	require.NoError(t, cmd.Flags().Set("backend", "tart"))

	assert.Equal(t, "tart", resolveBackend(cmd))
}

func TestResolveBackend_FlagEmptyWithConfig(t *testing.T) {
	dir := cliConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("backend: seatbelt\n"), 0600))

	cmd := &cobra.Command{}
	cmd.Flags().String("backend", "", "")

	assert.Equal(t, "seatbelt", resolveBackend(cmd))
}

func TestResolveBackend_FlagEmptyNoConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cmd := &cobra.Command{}
	cmd.Flags().String("backend", "", "")

	assert.Equal(t, "docker", resolveBackend(cmd))
}

// --- resolveBackendFromConfig ---

func TestResolveBackendFromConfig_HasBackend(t *testing.T) {
	dir := cliConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("backend: tart\n"), 0600))

	assert.Equal(t, "tart", resolveBackendFromConfig())
}

func TestResolveBackendFromConfig_Empty(t *testing.T) {
	dir := cliConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("agent: claude\n"), 0600))

	assert.Equal(t, "docker", resolveBackendFromConfig())
}

func TestResolveBackendFromConfig_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	assert.Equal(t, "docker", resolveBackendFromConfig())
}

// --- resolveBackendForSandbox ---

func TestResolveBackendForSandbox_MetaHasBackend(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "test-backend"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &sandbox.Meta{
		Name:      name,
		Backend:   "tart",
		Agent:     "claude",
		CreatedAt: time.Now(),
		Workdir:   sandbox.WorkdirMeta{HostPath: "/tmp/test", Mode: "copy"},
	}
	require.NoError(t, sandbox.SaveMeta(sandboxDir, meta))

	assert.Equal(t, "tart", resolveBackendForSandbox(name))
}

func TestResolveBackendForSandbox_MetaMissing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// No sandbox dir exists → falls back to config default
	assert.Equal(t, "docker", resolveBackendForSandbox("nonexistent"))
}

func TestResolveBackendForSandbox_MetaEmptyBackend(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "test-empty-backend"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &sandbox.Meta{
		Name:      name,
		Agent:     "claude",
		CreatedAt: time.Now(),
		Workdir:   sandbox.WorkdirMeta{HostPath: "/tmp/test", Mode: "copy"},
	}
	require.NoError(t, sandbox.SaveMeta(sandboxDir, meta))

	assert.Equal(t, "docker", resolveBackendForSandbox(name))
}

// --- resolveAgent ---

func TestResolveAgent_FlagSet(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("agent", "", "")
	require.NoError(t, cmd.Flags().Set("agent", "gemini"))

	assert.Equal(t, "gemini", resolveAgent(cmd))
}

func TestResolveAgent_FlagEmptyWithConfig(t *testing.T) {
	dir := cliConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("agent: aider\n"), 0600))

	cmd := &cobra.Command{}
	cmd.Flags().String("agent", "", "")

	assert.Equal(t, "aider", resolveAgent(cmd))
}

func TestResolveAgent_FlagEmptyNoConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cmd := &cobra.Command{}
	cmd.Flags().String("agent", "", "")

	assert.Equal(t, "claude", resolveAgent(cmd))
}

// --- resolveAgentFromConfig ---

func TestResolveAgentFromConfig_HasAgent(t *testing.T) {
	dir := cliConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("agent: gemini\n"), 0600))

	assert.Equal(t, "gemini", resolveAgentFromConfig())
}

func TestResolveAgentFromConfig_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	assert.Equal(t, "claude", resolveAgentFromConfig())
}

// --- resolveModel ---

func TestResolveModel_FlagSet(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("model", "", "")
	require.NoError(t, cmd.Flags().Set("model", "gpt-4o"))

	assert.Equal(t, "gpt-4o", resolveModel(cmd))
}

func TestResolveModel_FlagEmptyWithConfig(t *testing.T) {
	dir := cliConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("model: sonnet\n"), 0600))

	cmd := &cobra.Command{}
	cmd.Flags().String("model", "", "")

	assert.Equal(t, "sonnet", resolveModel(cmd))
}

func TestResolveModel_FlagEmptyNoConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cmd := &cobra.Command{}
	cmd.Flags().String("model", "", "")

	assert.Equal(t, "", resolveModel(cmd))
}

// --- resolveModelFromConfig ---

func TestResolveModelFromConfig_HasModel(t *testing.T) {
	dir := cliConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("model: opus\n"), 0600))

	assert.Equal(t, "opus", resolveModelFromConfig())
}

func TestResolveModelFromConfig_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	assert.Equal(t, "", resolveModelFromConfig())
}

// --- resolveProfile ---

func TestResolveProfile_FlagSet(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("profile", "", "")
	cmd.Flags().Bool("no-profile", false, "")
	require.NoError(t, cmd.Flags().Set("profile", "custom"))

	assert.Equal(t, "custom", resolveProfile(cmd))
}

func TestResolveProfile_NoProfileBypass(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("profile", "", "")
	cmd.Flags().Bool("no-profile", false, "")
	require.NoError(t, cmd.Flags().Set("no-profile", "true"))
	require.NoError(t, cmd.Flags().Set("profile", "custom"))

	assert.Equal(t, "", resolveProfile(cmd))
}

func TestResolveProfile_FlagEmptyWithConfig(t *testing.T) {
	dir := cliConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("profile: myprofile\n"), 0600))

	cmd := &cobra.Command{}
	cmd.Flags().String("profile", "", "")
	cmd.Flags().Bool("no-profile", false, "")

	assert.Equal(t, "myprofile", resolveProfile(cmd))
}

func TestResolveProfile_FlagEmptyNoConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cmd := &cobra.Command{}
	cmd.Flags().String("profile", "", "")
	cmd.Flags().Bool("no-profile", false, "")

	assert.Equal(t, "", resolveProfile(cmd))
}

// --- resolveProfileFromConfig ---

func TestResolveProfileFromConfig_HasProfile(t *testing.T) {
	dir := cliConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("profile: dev\n"), 0600))

	assert.Equal(t, "dev", resolveProfileFromConfig())
}

func TestResolveProfileFromConfig_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	assert.Equal(t, "", resolveProfileFromConfig())
}

// --- sandboxErrorHint ---

func TestSandboxErrorHint_NilErr(t *testing.T) {
	assert.NoError(t, sandboxErrorHint("test", nil))
}

func TestSandboxErrorHint_ErrSandboxNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	err := sandboxErrorHint("test", sandbox.ErrSandboxNotFound)
	assert.ErrorIs(t, err, sandbox.ErrSandboxNotFound)
	// Should NOT contain the hint (no directory to point at)
	assert.NotContains(t, err.Error(), "to remove:")
}

func TestSandboxErrorHint_GenericError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	origErr := errors.New("something went wrong")
	err := sandboxErrorHint("test", origErr)
	assert.ErrorIs(t, err, origErr)
	assert.Contains(t, err.Error(), "sandbox dir:")
	assert.Contains(t, err.Error(), "to remove: yoloai destroy test")
}
