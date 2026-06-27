package cliutil_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/internal/cli/clitest"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai/internal/orchestrator"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
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
	_ = clitest.Home(t)
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
	_ = clitest.Home(t)
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
	_ = clitest.Home(t)
	cmd := &cobra.Command{}
	cmd.Flags().String("backend", "", "")
	cmd.Flags().String("isolation", "", "")
	cmd.Flags().String("os", "", "")
	require.NoError(t, cmd.Flags().Set("os", "mac"))

	assert.Equal(t, runtime.BackendType("seatbelt"), cliutil.ResolveBackend(cmd))
}

func TestResolveBackend_OsMacIsolationVM(t *testing.T) {
	_ = clitest.Home(t)
	cmd := &cobra.Command{}
	cmd.Flags().String("backend", "", "")
	cmd.Flags().String("isolation", "", "")
	cmd.Flags().String("os", "", "")
	require.NoError(t, cmd.Flags().Set("os", "mac"))
	require.NoError(t, cmd.Flags().Set("isolation", "vm"))

	assert.Equal(t, runtime.BackendType("tart"), cliutil.ResolveBackend(cmd))
}

func TestResolveBackend_ConfigOsMacFlagIsolationVM(t *testing.T) {
	dir := clitest.ConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("os: mac\n"), 0600))

	cmd := &cobra.Command{}
	cmd.Flags().String("backend", "", "")
	cmd.Flags().String("isolation", "", "")
	cmd.Flags().String("os", "", "")
	require.NoError(t, cmd.Flags().Set("isolation", "vm"))

	assert.Equal(t, runtime.BackendType("tart"), cliutil.ResolveBackend(cmd))
}

func TestResolveBackend_FlagEmptyNoConfig(t *testing.T) {
	_ = clitest.Home(t)

	cmd := &cobra.Command{}
	cmd.Flags().String("backend", "", "")

	assert.Equal(t, runtime.BackendType("docker"), cliutil.ResolveBackend(cmd))
}

// --- container-system aliases (orbstack / docker-desktop) ---

func TestResolveBackend_OrbstackAliasResolvesToDocker(t *testing.T) {
	_ = clitest.Home(t)
	cmd := &cobra.Command{}
	cmd.Flags().String("backend", "", "")
	require.NoError(t, cmd.Flags().Set("backend", "orbstack"))

	assert.Equal(t, runtime.BackendType("docker"), cliutil.ResolveBackend(cmd),
		"an orbstack pick must route to the docker backend")
}

func TestResolveBackend_DockerDesktopAliasResolvesToDocker(t *testing.T) {
	_ = clitest.Home(t)
	cmd := &cobra.Command{}
	cmd.Flags().String("backend", "", "")
	require.NoError(t, cmd.Flags().Set("backend", "docker-desktop"))

	assert.Equal(t, runtime.BackendType("docker"), cliutil.ResolveBackend(cmd))
}

func TestResolveBackend_ConfigAliasResolvesToDocker(t *testing.T) {
	dir := clitest.ConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("container_backend: orbstack\n"), 0600))

	cmd := &cobra.Command{}
	cmd.Flags().String("backend", "", "")
	cmd.Flags().String("isolation", "", "")
	cmd.Flags().String("os", "", "")

	assert.Equal(t, runtime.BackendType("docker"), cliutil.ResolveBackend(cmd),
		"container_backend: orbstack must route to the docker backend")
}

func TestBackendEnv_PinsDockerHostForAlias(t *testing.T) {
	home := clitest.Home(t)
	cmd := &cobra.Command{}
	cmd.Flags().String("backend", "", "")
	require.NoError(t, cmd.Flags().Set("backend", "orbstack"))

	env := cliutil.BackendEnv(cmd)
	want := "unix://" + filepath.Join(home, ".orbstack/run/docker.sock")
	assert.Equal(t, want, env["DOCKER_HOST"], "an alias pick must pin DOCKER_HOST to that provider's socket")
}

func TestBackendEnv_NoPinForPlainBackend(t *testing.T) {
	_ = clitest.Home(t)
	cmd := &cobra.Command{}
	cmd.Flags().String("backend", "", "")
	require.NoError(t, cmd.Flags().Set("backend", "docker"))

	// A non-alias pick must leave any ambient DOCKER_HOST exactly as the edge
	// captured it — BackendEnv must not synthesize a pin.
	assert.Equal(t, cliutil.EdgeEnv()["DOCKER_HOST"], cliutil.BackendEnv(cmd)["DOCKER_HOST"])
}

// --- ResolveContainerBackendConfig ---

func TestResolveContainerBackendConfig_HasBackend(t *testing.T) {
	dir := clitest.ConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("container_backend: tart\n"), 0600))

	assert.Equal(t, runtime.BackendType("tart"), cliutil.ResolveContainerBackendConfig())
}

func TestResolveContainerBackendConfig_Empty(t *testing.T) {
	dir := clitest.ConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("agent: claude\n"), 0600))

	assert.Equal(t, runtime.BackendType(""), cliutil.ResolveContainerBackendConfig())
}

func TestResolveContainerBackendConfig_NoFile(t *testing.T) {
	_ = clitest.Home(t)

	assert.Equal(t, runtime.BackendType(""), cliutil.ResolveContainerBackendConfig())
}

// --- ResolveBackendForSandbox ---

func TestResolveBackendForSandbox_MetaHasBackend(t *testing.T) {
	tmpDir := clitest.Home(t)

	name := "test-backend"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "library", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Environment{
		Name:        name,
		BackendType: "tart",
		CreatedAt:   time.Now(),
		Dirs:        []store.DirEnvironment{{HostPath: "/tmp/test", Mode: "copy"}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	assert.Equal(t, runtime.BackendType("tart"), cliutil.ResolveBackendForSandbox(name))
}

func TestResolveBackendForSandbox_MetaMissing(t *testing.T) {
	_ = clitest.Home(t)

	// No sandbox dir exists → falls back to config default
	assert.Equal(t, runtime.BackendType("docker"), cliutil.ResolveBackendForSandbox("nonexistent"))
}

func TestResolveBackendForSandbox_MetaEmptyBackend(t *testing.T) {
	tmpDir := clitest.Home(t)

	name := "test-empty-backend"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "library", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Environment{
		Name:      name,
		CreatedAt: time.Now(),
		Dirs:      []store.DirEnvironment{{HostPath: "/tmp/test", Mode: "copy"}},
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
	dir := clitest.ConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("agent: aider\n"), 0600))

	cmd := &cobra.Command{}
	cmd.Flags().String("agent", "", "")

	assert.Equal(t, "aider", cliutil.ResolveAgent(cmd))
}

func TestResolveAgent_FlagEmptyNoConfig(t *testing.T) {
	_ = clitest.Home(t)

	cmd := &cobra.Command{}
	cmd.Flags().String("agent", "", "")

	assert.Equal(t, "claude", cliutil.ResolveAgent(cmd))
}

// --- ResolveAgentFromConfig ---

func TestResolveAgentFromConfig_HasAgent(t *testing.T) {
	dir := clitest.ConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("agent: gemini\n"), 0600))

	assert.Equal(t, "gemini", cliutil.ResolveAgentFromConfig())
}

func TestResolveAgentFromConfig_NoFile(t *testing.T) {
	_ = clitest.Home(t)

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
	dir := clitest.ConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("model: sonnet\n"), 0600))

	cmd := &cobra.Command{}
	cmd.Flags().String("model", "", "")

	assert.Equal(t, "sonnet", cliutil.ResolveModel(cmd))
}

func TestResolveModel_FlagEmptyNoConfig(t *testing.T) {
	_ = clitest.Home(t)

	cmd := &cobra.Command{}
	cmd.Flags().String("model", "", "")

	assert.Equal(t, "", cliutil.ResolveModel(cmd))
}

// --- ResolveModelFromConfig ---

func TestResolveModelFromConfig_HasModel(t *testing.T) {
	dir := clitest.ConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("model: opus\n"), 0600))

	assert.Equal(t, "opus", cliutil.ResolveModelFromConfig())
}

func TestResolveModelFromConfig_NoFile(t *testing.T) {
	_ = clitest.Home(t)

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
	_ = clitest.Home(t)

	cmd := &cobra.Command{}
	cmd.Flags().String("profile", "", "")
	cmd.Flags().Bool("no-profile", false, "")

	assert.Equal(t, "", cliutil.ResolveProfile(cmd))
}

func TestResolveProfile_FlagEmptyNoConfig(t *testing.T) {
	_ = clitest.Home(t)

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
	_ = clitest.Home(t)

	err := cliutil.SandboxErrorHint("test", orchestrator.ErrSandboxNotFound)
	assert.ErrorIs(t, err, orchestrator.ErrSandboxNotFound)
	// Should NOT contain the hint (no directory to point at)
	assert.NotContains(t, err.Error(), "to remove:")
}

func TestSandboxErrorHint_GenericError(t *testing.T) {
	_ = clitest.Home(t)

	origErr := errors.New("something went wrong")
	err := cliutil.SandboxErrorHint("test", origErr)
	assert.ErrorIs(t, err, origErr)
	assert.Contains(t, err.Error(), "sandbox dir:")
	assert.Contains(t, err.Error(), "to remove: yoloai destroy test")
}
