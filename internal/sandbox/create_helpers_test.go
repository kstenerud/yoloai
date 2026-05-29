package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/runtime/caps"
	dockerrt "github.com/kstenerud/yoloai/internal/runtime/docker"
	_ "github.com/kstenerud/yoloai/internal/runtime/seatbelt" // backend init() registers the descriptor for tests
	tartrt "github.com/kstenerud/yoloai/internal/runtime/tart"
	provision "github.com/kstenerud/yoloai/internal/sandbox/provision"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// layoutForTmpDir builds a Layout rooted at tmpDir/.yoloai for tests
// that exercise Engine methods which use m.layout. Mirrors what the
// CLI does at startup so tests don't depend on ambient HOME.
func layoutForTmpDir(tmpDir string) config.Layout {
	return config.NewLayout(filepath.Join(tmpDir, ".yoloai"))
}

// prepareSandboxState validation tests

func TestPrepareSandboxState_MissingName(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	mgr := NewEngine(&mockRuntime{}, slog.Default(), strings.NewReader(""), WithLayout(config.NewLayout(t.TempDir())))

	_, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:    "",
		Workdir: DirSpec{Path: tmpDir},
		Agent:   "test",
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name is required")
}

func TestPrepareSandboxState_UnknownAgent(t *testing.T) {
	tmpDir := t.TempDir()

	mgr := NewEngine(&mockRuntime{}, slog.Default(), strings.NewReader(""), WithLayout(config.NewLayout(t.TempDir())))

	_, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:    "test",
		Workdir: DirSpec{Path: tmpDir},
		Agent:   "nonexistent-agent",
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown agent")
}

func TestPrepareSandboxState_WorkdirMissing(t *testing.T) {
	tmpDir := t.TempDir()

	mgr := NewEngine(&mockRuntime{}, slog.Default(), strings.NewReader(""), WithLayout(layoutForTmpDir(tmpDir)))

	_, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:    "test",
		Workdir: DirSpec{Path: "/nonexistent/path"},
		Agent:   "test",
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workdir does not exist")
}

func TestPrepareSandboxState_SandboxExists(t *testing.T) {
	tmpDir := t.TempDir()

	// Create existing sandbox dir with valid environment.json
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", "existing")
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))
	require.NoError(t, store.SaveMeta(sandboxDir, &store.Meta{
		Name:  "existing",
		Agent: "test",
	}))

	mgr := NewEngine(&mockRuntime{}, slog.Default(), strings.NewReader(""), WithLayout(layoutForTmpDir(tmpDir)))

	_, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:    "existing",
		Workdir: DirSpec{Path: tmpDir},
		Agent:   "test",
	}, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSandboxExists)
}

func TestPrepareSandboxState_ConflictingPromptFlags(t *testing.T) {
	tmpDir := t.TempDir()

	mgr := NewEngine(&mockRuntime{}, slog.Default(), strings.NewReader(""), WithLayout(layoutForTmpDir(tmpDir)))

	_, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:       "test",
		Workdir:    DirSpec{Path: tmpDir},
		Agent:      "test",
		Prompt:     "hello",
		PromptFile: "/some/file",
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestPrepareSandboxState_MissingAPIKey(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

	mgr := NewEngine(&mockRuntime{}, slog.Default(), strings.NewReader(""), WithLayout(layoutForTmpDir(tmpDir)))

	_, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:    "test",
		Workdir: DirSpec{Path: tmpDir},
		Agent:   "claude",
	}, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingAPIKey)
}

func TestPrepareSandboxState_DangerousDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	mgr := NewEngine(&mockRuntime{}, slog.Default(), strings.NewReader(""), WithLayout(layoutForTmpDir(tmpDir)))

	_, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:    "test",
		Workdir: DirSpec{Path: "/"},
		Agent:   "claude",
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dangerous directory")
}

func TestPrepareSandboxState_DangerousDirForce(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	// HOME is classified as dangerous. Use :rw:force to avoid copying.
	var buf bytes.Buffer
	mgr := NewEngine(&mockRuntime{}, slog.Default(), strings.NewReader("y\n"), WithLayout(layoutForTmpDir(tmpDir)))

	_, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:    "test",
		Workdir: DirSpec{Path: tmpDir, Mode: DirModeRW, AllowDangerousPath: true},
		Agent:   "claude",
		Output:  &buf,
	}, nil)
	// Should NOT fail on "dangerous directory" — :force bypasses it.
	if err != nil {
		assert.NotContains(t, err.Error(), "dangerous directory")
	}
	assert.Contains(t, buf.String(), "WARNING: mounting dangerous directory")
}

// Error message tests

func TestPrepareSandboxState_MissingAPIKeyErrorNoEmptyParens(t *testing.T) {
	tmpDir := t.TempDir()

	agentDef := agent.GetAgent("aider")
	// Clear all aider API key env vars
	for _, key := range agentDef.APIKeyEnvVars {
		t.Setenv(key, "")
	}
	// Clear all aider auth hint env vars
	for _, key := range agentDef.AuthHintEnvVars {
		t.Setenv(key, "")
	}

	mgr := NewEngine(&mockRuntime{}, slog.Default(), strings.NewReader(""), WithLayout(layoutForTmpDir(tmpDir)))

	_, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:    "test",
		Workdir: DirSpec{Path: tmpDir},
		Agent:   "aider",
	}, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingAPIKey)
	errMsg := err.Error()
	assert.NotContains(t, errMsg, "()", "error message should not contain empty parens")
	assert.Contains(t, errMsg, "local models", "error should mention local models")
	assert.Contains(t, errMsg, "OLLAMA_API_BASE", "error should mention OLLAMA_API_BASE")
}

func TestPrepareSandboxState_MissingAPIKeyErrorWithAuthFiles(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

	// Override provision.KeychainReader to fail
	origReader := provision.KeychainReader
	provision.KeychainReader = func(_ string) ([]byte, error) {
		return nil, fmt.Errorf("not found")
	}
	defer func() { provision.KeychainReader = origReader }()

	mgr := NewEngine(&mockRuntime{}, slog.Default(), strings.NewReader(""), WithLayout(layoutForTmpDir(tmpDir)))

	_, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:    "test",
		Workdir: DirSpec{Path: tmpDir},
		Agent:   "claude",
	}, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingAPIKey)
	errMsg := err.Error()
	assert.Contains(t, errMsg, ".credentials.json", "error should mention .credentials.json from AuthOnly seed files")
	assert.NotContains(t, errMsg, "local models", "claude has no AuthHintEnvVars, should not mention local models")
}

func TestPrepareSandboxState_NetworkIsolatedSetsAllowlist(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	// Create a workdir subdirectory to avoid dangerous directory detection
	workDir := filepath.Join(tmpDir, "project")
	require.NoError(t, os.MkdirAll(workDir, 0750))

	mgr := NewEngine(&mockRuntime{}, slog.Default(), strings.NewReader("y\n"), WithLayout(layoutForTmpDir(tmpDir)))

	state, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:    "test",
		Workdir: DirSpec{Path: workDir},
		Agent:   "claude",
		Network: NetworkModeIsolated,
		Version: "test",
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, state)

	assert.Equal(t, "isolated", state.NetworkMode)
	assert.Contains(t, state.NetworkAllow, "api.anthropic.com")
	assert.Contains(t, state.NetworkAllow, "statsig.anthropic.com")
	assert.Contains(t, state.NetworkAllow, "sentry.io")
}

// containsLocalhost tests

func TestContainsLocalhost_WithLocalhost(t *testing.T) {
	assert.True(t, containsLocalhost("http://localhost:11434"))
}

func TestContainsLocalhost_With127(t *testing.T) {
	assert.True(t, containsLocalhost("http://127.0.0.1:8080/api"))
}

func TestContainsLocalhost_Neither(t *testing.T) {
	assert.False(t, containsLocalhost("http://api.example.com"))
}

func TestContainsLocalhost_Empty(t *testing.T) {
	assert.False(t, containsLocalhost(""))
}

func TestContainsLocalhost_ExternalURL(t *testing.T) {
	assert.False(t, containsLocalhost("http://example.com"))
}

func TestPrepareSandboxState_NetworkAllowAddsExtraDomains(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	// Create a workdir subdirectory to avoid dangerous directory detection
	workDir := filepath.Join(tmpDir, "project")
	require.NoError(t, os.MkdirAll(workDir, 0750))

	mgr := NewEngine(&mockRuntime{}, slog.Default(), strings.NewReader("y\n"), WithLayout(layoutForTmpDir(tmpDir)))

	state, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:         "test",
		Workdir:      DirSpec{Path: workDir},
		Agent:        "claude",
		Network:      NetworkModeIsolated,
		NetworkAllow: []string{"api.example.com"},
		Version:      "test",
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, state)

	assert.Equal(t, "isolated", state.NetworkMode)
	assert.Contains(t, state.NetworkAllow, "api.anthropic.com")
	assert.Contains(t, state.NetworkAllow, "api.example.com")
}

// IsolationSnapshotter tests

func TestIsolationSnapshotter_VmEnhanced(t *testing.T) {
	assert.Equal(t, "devmapper", runtime.IsolationSnapshotter("vm-enhanced"))
}

func TestIsolationSnapshotter_Other(t *testing.T) {
	assert.Equal(t, "", runtime.IsolationSnapshotter("vm"))
	assert.Equal(t, "", runtime.IsolationSnapshotter("container"))
	assert.Equal(t, "", runtime.IsolationSnapshotter("container-enhanced"))
	assert.Equal(t, "", runtime.IsolationSnapshotter(""))
}

// IsolationContainerRuntime tests

func TestIsolationContainerRuntime_Container(t *testing.T) {
	assert.Equal(t, "", runtime.IsolationContainerRuntime("container"))
	assert.Equal(t, "", runtime.IsolationContainerRuntime(""))
}

func TestIsolationContainerRuntime_ContainerEnhanced(t *testing.T) {
	assert.Equal(t, "runsc", runtime.IsolationContainerRuntime("container-enhanced"))
}

func TestIsolationContainerRuntime_VM(t *testing.T) {
	assert.Equal(t, "io.containerd.kata.v2", runtime.IsolationContainerRuntime("vm"))
}

func TestIsolationContainerRuntime_VMEnhanced(t *testing.T) {
	assert.Equal(t, "io.containerd.kata-fc.v2", runtime.IsolationContainerRuntime("vm-enhanced"))
}

// BackendCaps tests — each backend declares its own capabilities.
// Read the static descriptor via the registry rather than instantiating the
// runtime; the backend packages register themselves at init().

// mustDescriptor returns the registered descriptor for name, failing the test
// if the backend isn't registered (e.g., test ran on an unsupported platform).
func mustDescriptor(t *testing.T, name runtime.BackendName) runtime.BackendDescriptor {
	t.Helper()
	desc, ok := runtime.Descriptor(name)
	require.True(t, ok, "backend %q not registered", name)
	return desc
}

func TestBackendCaps_Docker(t *testing.T) {
	caps := mustDescriptor(t, "docker").Capabilities
	assert.True(t, caps.NetworkIsolation)
	assert.True(t, caps.OverlayDirs)
	assert.True(t, caps.CapAdd)
}

func TestBackendCaps_Tart(t *testing.T) {
	caps := mustDescriptor(t, "tart").Capabilities
	assert.False(t, caps.NetworkIsolation)
	assert.False(t, caps.OverlayDirs)
	assert.False(t, caps.CapAdd)
}

func TestBackendCaps_Seatbelt(t *testing.T) {
	caps := mustDescriptor(t, "seatbelt").Capabilities
	assert.False(t, caps.NetworkIsolation)
	assert.False(t, caps.OverlayDirs)
	assert.False(t, caps.CapAdd)
}

// AgentProvisionedByBackend and ResolveCopyMount tests

func TestAgentProvisionedByBackend_Docker(t *testing.T) {
	assert.True(t, mustDescriptor(t, "docker").AgentProvisionedByBackend)
}

func TestAgentProvisionedByBackend_Tart(t *testing.T) {
	assert.True(t, mustDescriptor(t, "tart").AgentProvisionedByBackend)
}

func TestAgentProvisionedByBackend_Seatbelt(t *testing.T) {
	assert.False(t, mustDescriptor(t, "seatbelt").AgentProvisionedByBackend) // uses host native agent
}

func TestResolveCopyMount_Docker(t *testing.T) {
	// Docker doesn't implement CopyMountResolver — helper falls back to hostPath.
	rt := (*dockerrt.Runtime)(nil)
	assert.Equal(t, "/home/user/project", runtime.ResolveCopyMountFor(rt, "mysandbox", "/home/user/project"))
}

func TestResolveCopyMount_Tart(t *testing.T) {
	// Tart implements CopyMountResolver — returns local VM path.
	result := runtime.ResolveCopyMountFor((*tartrt.Runtime)(nil), "mysandbox", "/home/user/project")
	assert.Equal(t, "/Users/admin/yoloai-work/^shome^suser^sproject", result)
}

// checkIsolationPrerequisites tests

// capsRuntime wraps mockRuntime and overrides RequiredCapabilities for testing.
type capsRuntime struct {
	mockRuntime
	capList []caps.HostCapability
}

func (c *capsRuntime) RequiredCapabilities(_ runtime.IsolationMode) []caps.HostCapability {
	return c.capList
}

func TestCheckIsolationPrerequisites_NoCaps(t *testing.T) {
	// mockRuntime returns nil from RequiredCapabilities — should be a no-op.
	rt := &mockRuntime{}
	err := checkIsolationPrerequisites(context.Background(), rt, "container-enhanced")
	assert.NoError(t, err)
}

func TestCheckIsolationPrerequisites_AllCapsPass(t *testing.T) {
	rt := &capsRuntime{
		capList: []caps.HostCapability{
			{ID: "a", Summary: "Cap A", Check: func(_ context.Context) error { return nil }},
		},
	}
	err := checkIsolationPrerequisites(context.Background(), rt, "vm")
	assert.NoError(t, err)
}

func TestCheckIsolationPrerequisites_CapFails(t *testing.T) {
	rt := &capsRuntime{
		capList: []caps.HostCapability{
			{ID: "kata-shim", Summary: "kata shim", Check: func(_ context.Context) error { return fmt.Errorf("kata shim not found") }},
		},
	}
	err := checkIsolationPrerequisites(context.Background(), rt, "vm")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kata shim")
}

func TestCheckIsolationPrerequisites_IsolationModeForwarded(t *testing.T) {
	rt := &capsRuntime{}
	// For this test we use the base capsRuntime which returns nil caps.
	// Just verify that checkIsolationPrerequisites doesn't panic for any mode.
	for _, mode := range []runtime.IsolationMode{"container", "container-enhanced", "vm", "vm-enhanced", ""} {
		err := checkIsolationPrerequisites(context.Background(), rt, mode)
		assert.NoError(t, err, "mode %q should not fail with nil caps", mode)
	}
}
