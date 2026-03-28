package sandbox

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/agent"
	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/caps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- buildNetworkConfig ---

func TestBuildNetworkConfig_Default(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	mode, allow := buildNetworkConfig(CreateOptions{}, agentDef)
	assert.Equal(t, "", mode)
	assert.Nil(t, allow)
}

func TestBuildNetworkConfig_None(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	mode, allow := buildNetworkConfig(CreateOptions{Network: NetworkModeNone}, agentDef)
	assert.Equal(t, "none", mode)
	assert.Nil(t, allow)
}

func TestBuildNetworkConfig_Isolated(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	mode, allow := buildNetworkConfig(CreateOptions{Network: NetworkModeIsolated}, agentDef)
	assert.Equal(t, "isolated", mode)
	// Should include agent's allowlist
	assert.NotEmpty(t, allow)
	assert.Contains(t, allow, "api.anthropic.com")
}

func TestBuildNetworkConfig_IsolatedWithUserAllow(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	opts := CreateOptions{
		Network:      NetworkModeIsolated,
		NetworkAllow: []string{"example.com"},
	}
	mode, allow := buildNetworkConfig(opts, agentDef)
	assert.Equal(t, "isolated", mode)
	// Agent allowlist + user allowlist
	assert.Contains(t, allow, "api.anthropic.com")
	assert.Contains(t, allow, "example.com")
}

func TestBuildNetworkConfig_NoneTakesPriority(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	opts := CreateOptions{
		Network:      NetworkModeNone,
		NetworkAllow: []string{"example.com"},
	}
	mode, allow := buildNetworkConfig(opts, agentDef)
	assert.Equal(t, "none", mode)
	assert.Nil(t, allow)
}

// --- collectCopyDirs ---

func TestCollectCopyDirs_NoCopy(t *testing.T) {
	workdir := &DirArg{Path: "/home/user/project", Mode: "rw"}
	result := collectCopyDirs(workdir, nil)
	assert.Empty(t, result)
}

func TestCollectCopyDirs_WorkdirCopy(t *testing.T) {
	workdir := &DirArg{Path: "/home/user/project", Mode: "copy"}
	result := collectCopyDirs(workdir, nil)
	assert.Equal(t, []string{"/home/user/project"}, result)
}

func TestCollectCopyDirs_MixedModes(t *testing.T) {
	workdir := &DirArg{Path: "/home/user/project", Mode: "copy"}
	auxDirs := []*DirArg{
		{Path: "/home/user/lib", Mode: "rw"},
		{Path: "/home/user/data", Mode: "copy"},
		{Path: "/home/user/overlay", Mode: "overlay"},
	}
	result := collectCopyDirs(workdir, auxDirs)
	assert.Equal(t, []string{"/home/user/project", "/home/user/data"}, result)
}

func TestCollectCopyDirs_CustomMountPath(t *testing.T) {
	workdir := &DirArg{Path: "/home/user/project", Mode: "copy", MountPath: "/app"}
	result := collectCopyDirs(workdir, nil)
	assert.Equal(t, []string{"/app"}, result)
}

// --- collectOverlayMounts ---

func TestCollectOverlayMounts_NoOverlay(t *testing.T) {
	workdir := &DirArg{Path: "/home/user/project", Mode: "copy"}
	result := collectOverlayMounts(workdir, nil)
	assert.Empty(t, result)
}

func TestCollectOverlayMounts_WorkdirOverlay(t *testing.T) {
	workdir := &DirArg{Path: "/home/user/project", Mode: "overlay"}
	result := collectOverlayMounts(workdir, nil)
	require.Len(t, result, 1)
	assert.Contains(t, result[0].Merged, "merged")
	assert.Contains(t, result[0].Lower, "lower")
	assert.Contains(t, result[0].Upper, "upper")
	assert.Contains(t, result[0].Work, "ovlwork")
}

func TestCollectOverlayMounts_AuxOverlay(t *testing.T) {
	workdir := &DirArg{Path: "/home/user/project", Mode: "copy"}
	auxDirs := []*DirArg{
		{Path: "/home/user/lib", Mode: "overlay"},
	}
	result := collectOverlayMounts(workdir, auxDirs)
	require.Len(t, result, 1)
	assert.Contains(t, result[0].Merged, "merged")
}

func TestCollectOverlayMounts_Multiple(t *testing.T) {
	workdir := &DirArg{Path: "/a", Mode: "overlay"}
	auxDirs := []*DirArg{
		{Path: "/b", Mode: "overlay"},
		{Path: "/c", Mode: "copy"}, // not overlay
	}
	result := collectOverlayMounts(workdir, auxDirs)
	assert.Len(t, result, 2)
}

func TestCollectOverlayMounts_CustomMountPath(t *testing.T) {
	workdir := &DirArg{Path: "/home/user/project", Mode: "overlay", MountPath: "/app"}
	result := collectOverlayMounts(workdir, nil)
	require.Len(t, result, 1)
	assert.Contains(t, result[0].Merged, "merged")
}

// --- validateAndExpandMounts ---

func TestValidateAndExpandMounts_Valid(t *testing.T) {
	result, err := validateAndExpandMounts([]string{"/tmp/src:/container/dst"})
	require.NoError(t, err)
	assert.Equal(t, []string{"/tmp/src:/container/dst"}, result)
}

func TestValidateAndExpandMounts_ReadOnly(t *testing.T) {
	result, err := validateAndExpandMounts([]string{"/tmp/src:/container/dst:ro"})
	require.NoError(t, err)
	assert.Equal(t, []string{"/tmp/src:/container/dst:ro"}, result)
}

func TestValidateAndExpandMounts_Invalid(t *testing.T) {
	_, err := validateAndExpandMounts([]string{"no-colon"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid mount")
}

func TestValidateAndExpandMounts_Multiple(t *testing.T) {
	result, err := validateAndExpandMounts([]string{
		"/a:/b",
		"/c:/d:ro",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"/a:/b", "/c:/d:ro"}, result)
}

func TestValidateAndExpandMounts_TildeExpansion(t *testing.T) {
	result, err := validateAndExpandMounts([]string{"~/.gitconfig:/home/yoloai/.gitconfig:ro"})
	require.NoError(t, err)
	// Should not start with ~ after expansion
	assert.NotContains(t, result[0], "~")
	assert.Contains(t, result[0], ".gitconfig")
}

func TestValidateAndExpandMounts_Empty(t *testing.T) {
	result, err := validateAndExpandMounts(nil)
	require.NoError(t, err)
	assert.Empty(t, result)
}

// --- applyConfigDefaults ---

func TestApplyConfigDefaults_ResourcesFromConfig(t *testing.T) {
	opts := &CreateOptions{}
	ycfg := &config.YoloaiConfig{
		Resources: &config.ResourceLimits{CPUs: "4", Memory: "8g"},
	}
	pr := &profileResult{}

	require.NoError(t, applyConfigDefaults(opts, ycfg, pr))
	require.NotNil(t, pr.resources)
	assert.Equal(t, "4", pr.resources.CPUs)
	assert.Equal(t, "8g", pr.resources.Memory)
}

func TestApplyConfigDefaults_ProfileResourcesTakePriority(t *testing.T) {
	opts := &CreateOptions{}
	ycfg := &config.YoloaiConfig{
		Resources: &config.ResourceLimits{CPUs: "4", Memory: "8g"},
	}
	pr := &profileResult{
		resources: &config.ResourceLimits{CPUs: "2", Memory: "4g"},
	}

	require.NoError(t, applyConfigDefaults(opts, ycfg, pr))
	// Profile resources should not be overwritten by config
	assert.Equal(t, "2", pr.resources.CPUs)
	assert.Equal(t, "4g", pr.resources.Memory)
}

func TestApplyConfigDefaults_CLIOverridesResources(t *testing.T) {
	opts := &CreateOptions{CPUs: "8", Memory: "16g"}
	ycfg := &config.YoloaiConfig{
		Resources: &config.ResourceLimits{CPUs: "4", Memory: "8g"},
	}
	pr := &profileResult{}

	require.NoError(t, applyConfigDefaults(opts, ycfg, pr))
	require.NotNil(t, pr.resources)
	assert.Equal(t, "8", pr.resources.CPUs)
	assert.Equal(t, "16g", pr.resources.Memory)
}

func TestApplyConfigDefaults_CLIOverridesProfileResources(t *testing.T) {
	opts := &CreateOptions{CPUs: "8"}
	ycfg := &config.YoloaiConfig{}
	pr := &profileResult{
		resources: &config.ResourceLimits{CPUs: "2", Memory: "4g"},
	}

	require.NoError(t, applyConfigDefaults(opts, ycfg, pr))
	assert.Equal(t, "8", pr.resources.CPUs)
	assert.Equal(t, "4g", pr.resources.Memory) // unchanged
}

func TestApplyConfigDefaults_MountsFromConfigWhenNoProfile(t *testing.T) {
	opts := &CreateOptions{} // no profile
	ycfg := &config.YoloaiConfig{
		Mounts: []string{"/a:/b"},
	}
	pr := &profileResult{}

	require.NoError(t, applyConfigDefaults(opts, ycfg, pr))
	assert.Equal(t, []string{"/a:/b"}, pr.mounts)
}

func TestApplyConfigDefaults_MountsSkippedWithProfile(t *testing.T) {
	opts := &CreateOptions{Profile: "dev"}
	ycfg := &config.YoloaiConfig{
		Mounts: []string{"/a:/b"},
	}
	pr := &profileResult{mounts: []string{"/c:/d"}}

	require.NoError(t, applyConfigDefaults(opts, ycfg, pr))
	// Profile mounts should not be overwritten
	assert.Equal(t, []string{"/c:/d"}, pr.mounts)
}

func TestApplyConfigDefaults_PortsFromConfigWhenNoProfile(t *testing.T) {
	opts := &CreateOptions{Ports: []string{"9090:9090"}}
	ycfg := &config.YoloaiConfig{
		Ports: []string{"8080:8080"},
	}
	pr := &profileResult{}

	require.NoError(t, applyConfigDefaults(opts, ycfg, pr))
	// Config ports prepended to CLI ports
	assert.Equal(t, []string{"8080:8080", "9090:9090"}, opts.Ports)
}

func TestApplyConfigDefaults_NetworkFromConfigWhenNoProfile(t *testing.T) {
	opts := &CreateOptions{}
	ycfg := &config.YoloaiConfig{
		Network: &config.NetworkConfig{
			Isolated: true,
			Allow:    []string{"example.com"},
		},
	}
	pr := &profileResult{}

	require.NoError(t, applyConfigDefaults(opts, ycfg, pr))
	assert.Equal(t, NetworkModeIsolated, opts.Network)
	assert.Equal(t, []string{"example.com"}, opts.NetworkAllow)
}

func TestApplyConfigDefaults_NetworkSkippedWhenCLIOverrides(t *testing.T) {
	opts := &CreateOptions{Network: NetworkModeNone}
	ycfg := &config.YoloaiConfig{
		Network: &config.NetworkConfig{
			Isolated: true,
			Allow:    []string{"example.com"},
		},
	}
	pr := &profileResult{}

	require.NoError(t, applyConfigDefaults(opts, ycfg, pr))
	// NetworkNone takes priority; config network should not apply
	assert.Equal(t, NetworkModeNone, opts.Network)
	assert.Empty(t, opts.NetworkAllow)
}

func TestApplyConfigDefaults_RecipesFromConfigWhenNoProfile(t *testing.T) {
	opts := &CreateOptions{}
	ycfg := &config.YoloaiConfig{
		CapAdd:  []string{"SYS_ADMIN"},
		Devices: []string{"/dev/fuse"},
		Setup:   []string{"apt-get install -y vim"},
	}
	pr := &profileResult{}

	require.NoError(t, applyConfigDefaults(opts, ycfg, pr))
	assert.Equal(t, []string{"SYS_ADMIN"}, pr.capAdd)
	assert.Equal(t, []string{"/dev/fuse"}, pr.devices)
	assert.Equal(t, []string{"apt-get install -y vim"}, pr.setup)
}

// --- setupWorkdir baseline deferral ---

// mockDockerRuntime implements runtime.Runtime without WorkDirSetup (Docker-like behavior).
type mockDockerRuntime struct{}

func (m *mockDockerRuntime) Capabilities() runtime.BackendCaps {
	return runtime.BackendCaps{}
}
func (m *mockDockerRuntime) AgentProvisionedByBackend() bool { return false }
func (m *mockDockerRuntime) Setup(ctx context.Context, sourceDir string, output io.Writer, logger *slog.Logger, force bool) error {
	return nil
}
func (m *mockDockerRuntime) IsReady(ctx context.Context) (bool, error) { return true, nil }
func (m *mockDockerRuntime) Create(ctx context.Context, cfg runtime.InstanceConfig) error {
	return nil
}
func (m *mockDockerRuntime) Start(ctx context.Context, name string) error  { return nil }
func (m *mockDockerRuntime) Stop(ctx context.Context, name string) error   { return nil }
func (m *mockDockerRuntime) Remove(ctx context.Context, name string) error { return nil }
func (m *mockDockerRuntime) Inspect(ctx context.Context, name string) (runtime.InstanceInfo, error) {
	return runtime.InstanceInfo{}, nil
}
func (m *mockDockerRuntime) Exec(ctx context.Context, name string, cmd []string, user string) (runtime.ExecResult, error) {
	return runtime.ExecResult{}, nil
}
func (m *mockDockerRuntime) InteractiveExec(ctx context.Context, name string, cmd []string, user string, workdir string) error {
	return nil
}
func (m *mockDockerRuntime) Prune(ctx context.Context, knownInstances []string, dryRun bool, output io.Writer) (runtime.PruneResult, error) {
	return runtime.PruneResult{}, nil
}
func (m *mockDockerRuntime) Close() error { return nil }
func (m *mockDockerRuntime) Logs(ctx context.Context, name string, lines int) string {
	return ""
}
func (m *mockDockerRuntime) DiagHint(name string) string { return "" }
func (m *mockDockerRuntime) ResolveCopyMount(sandboxName, hostPath string) string {
	return hostPath
}
func (m *mockDockerRuntime) BaseModeName() string              { return "container" }
func (m *mockDockerRuntime) SupportedIsolationModes() []string { return nil }
func (m *mockDockerRuntime) RequiredCapabilities(isolation string) []caps.HostCapability {
	return nil
}
func (m *mockDockerRuntime) Name() string                        { return "mock" }
func (m *mockDockerRuntime) TmuxSocket(sandboxDir string) string { return "" }
func (m *mockDockerRuntime) AttachCommand(tmuxSocket string, rows, cols int, term string) []string {
	return nil
}

// mockTartRuntime implements both runtime.Runtime and runtime.WorkDirSetup (Tart-like).
type mockTartRuntime struct {
	mockDockerRuntime
}

func (m *mockTartRuntime) SetupWorkDirInVM(virtiofsStagingPath, vmLocalPath string) []string {
	return []string{
		"mkdir -p /parent",
		"rsync -a /staging/ /local/",
		"cd /local && git init && git add -A && git commit -m 'baseline'",
	}
}

// TestSetupWorkdir_DefersBaselineForWorkDirSetupBackends tests that backends
// implementing WorkDirSetup (Tart) return empty SHA, deferring baseline creation.
func TestSetupWorkdir_DefersBaselineForWorkDirSetupBackends(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping filesystem test in short mode")
	}

	sandboxName := "test-sandbox"
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "source")
	require.NoError(t, os.MkdirAll(sourceDir, 0755))                                             //nolint:gosec // G301: test directory
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "file.txt"), []byte("test"), 0644)) //nolint:gosec // G306: test file

	workdir := &DirArg{
		Path: sourceDir,
		Mode: "copy",
	}

	rt := &mockTartRuntime{}

	// setupWorkdir should return empty SHA for WorkDirSetup backends
	_, baselineSHA, err := setupWorkdir(sandboxName, workdir, rt)
	require.NoError(t, err)
	assert.Empty(t, baselineSHA, "baseline SHA should be empty for WorkDirSetup backends (baseline deferred to VM)")
}

// TestSetupWorkdir_CreatesBaselineForDockerBackends tests that non-WorkDirSetup
// backends (Docker) get immediate baseline creation with non-empty SHA.
func TestSetupWorkdir_CreatesBaselineForDockerBackends(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping filesystem test in short mode")
	}

	sandboxName := "test-sandbox"
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "source")
	require.NoError(t, os.MkdirAll(sourceDir, 0755))                                             //nolint:gosec // G301: test directory
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "file.txt"), []byte("test"), 0644)) //nolint:gosec // G306: test file

	workdir := &DirArg{
		Path: sourceDir,
		Mode: "copy",
	}

	rt := &mockDockerRuntime{}

	// setupWorkdir should create baseline and return non-empty SHA for Docker
	_, baselineSHA, err := setupWorkdir(sandboxName, workdir, rt)
	require.NoError(t, err)
	assert.NotEmpty(t, baselineSHA, "baseline SHA should be non-empty for Docker backends (immediate baseline)")
	assert.Len(t, baselineSHA, 40, "SHA should be 40 characters (git SHA-1)")
}

// TestSetupWorkdir_OverlayModeDeferBaseline tests that overlay mode always
// defers baseline creation regardless of backend.
func TestSetupWorkdir_OverlayModeDeferBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping filesystem test in short mode")
	}

	sandboxName := "test-sandbox"
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "source")
	require.NoError(t, os.MkdirAll(sourceDir, 0755)) //nolint:gosec // G301: test directory

	workdir := &DirArg{
		Path: sourceDir,
		Mode: "overlay",
	}

	// Test with both runtime types
	runtimes := []runtime.Runtime{
		&mockDockerRuntime{},
		&mockTartRuntime{},
	}

	for _, rt := range runtimes {
		_, baselineSHA, err := setupWorkdir(sandboxName, workdir, rt)
		require.NoError(t, err)
		assert.Empty(t, baselineSHA, "overlay mode should defer baseline for all backends")
	}
}
