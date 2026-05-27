package sandbox

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
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
	workdir := &DirSpec{Path: "/home/user/project", Mode: DirMode("rw")}
	result := collectCopyDirs(workdir, nil)
	assert.Empty(t, result)
}

func TestCollectCopyDirs_WorkdirCopy(t *testing.T) {
	workdir := &DirSpec{Path: "/home/user/project", Mode: DirMode("copy")}
	result := collectCopyDirs(workdir, nil)
	assert.Equal(t, []string{"/home/user/project"}, result)
}

// Q-U: aux dirs can no longer be :copy or :overlay, so aux entries
// passed through here are silently ignored by collectCopyDirs (the
// parameter is kept for callsite stability). The mixed-modes case
// reduces to "the workdir's mode decides; aux entries don't count".
func TestCollectCopyDirs_MixedModes_IgnoresAux(t *testing.T) {
	workdir := &DirSpec{Path: "/home/user/project", Mode: DirMode("copy")}
	auxDirs := []*DirSpec{
		{Path: "/home/user/lib", Mode: DirMode("rw")},
		{Path: "/home/user/data", Mode: "ro"},
	}
	result := collectCopyDirs(workdir, auxDirs)
	assert.Equal(t, []string{"/home/user/project"}, result)
}

func TestCollectCopyDirs_CustomMountPath(t *testing.T) {
	workdir := &DirSpec{Path: "/home/user/project", Mode: DirMode("copy"), MountPath: "/app"}
	result := collectCopyDirs(workdir, nil)
	assert.Equal(t, []string{"/app"}, result)
}

// --- collectOverlayMounts ---

func TestCollectOverlayMounts_NoOverlay(t *testing.T) {
	workdir := &DirSpec{Path: "/home/user/project", Mode: DirMode("copy")}
	result := collectOverlayMounts(workdir, nil)
	assert.Empty(t, result)
}

func TestCollectOverlayMounts_WorkdirOverlay(t *testing.T) {
	workdir := &DirSpec{Path: "/home/user/project", Mode: DirMode("overlay")}
	result := collectOverlayMounts(workdir, nil)
	require.Len(t, result, 1)
	assert.Contains(t, result[0].Merged, "merged")
	assert.Contains(t, result[0].Lower, "lower")
	assert.Contains(t, result[0].Upper, "upper")
	assert.Contains(t, result[0].Work, "ovlwork")
}

// Q-U: aux dirs can no longer be :overlay. collectOverlayMounts
// ignores aux entries entirely; only the workdir's mode matters.
// Regress-guards both the "aux :overlay no longer participates" and
// "workdir-only result has length ≤ 1" invariants.
func TestCollectOverlayMounts_IgnoresAux(t *testing.T) {
	workdir := &DirSpec{Path: "/home/user/project", Mode: DirMode("copy")}
	// aux entries here would have been overlay under the old code path;
	// after Q-U they're rejected at parse time, but defending against a
	// stale DirSpec constructed in-process is cheap.
	auxDirs := []*DirSpec{{Path: "/home/user/lib", Mode: DirMode("rw")}}
	result := collectOverlayMounts(workdir, auxDirs)
	assert.Empty(t, result)
}

func TestCollectOverlayMounts_WorkdirOnly(t *testing.T) {
	workdir := &DirSpec{Path: "/a", Mode: DirMode("overlay")}
	auxDirs := []*DirSpec{{Path: "/b", Mode: DirMode("rw")}}
	result := collectOverlayMounts(workdir, auxDirs)
	require.Len(t, result, 1)
	assert.Contains(t, result[0].Merged, "merged")
	assert.Contains(t, result[0].Lower, "lower")
	assert.Contains(t, result[0].Upper, "upper")
	assert.Contains(t, result[0].Work, "ovlwork")
}

func TestCollectOverlayMounts_CustomMountPath(t *testing.T) {
	workdir := &DirSpec{Path: "/home/user/project", Mode: DirMode("overlay"), MountPath: "/app"}
	result := collectOverlayMounts(workdir, nil)
	require.Len(t, result, 1)
	assert.Contains(t, result[0].Merged, "merged")
}

// --- validateAndExpandMounts ---

func TestValidateAndExpandMounts_Valid(t *testing.T) {
	result, err := validateAndExpandMounts([]string{"/tmp/src:/container/dst"}, "/home/user")
	require.NoError(t, err)
	assert.Equal(t, []string{"/tmp/src:/container/dst"}, result)
}

func TestValidateAndExpandMounts_ReadOnly(t *testing.T) {
	result, err := validateAndExpandMounts([]string{"/tmp/src:/container/dst:ro"}, "/home/user")
	require.NoError(t, err)
	assert.Equal(t, []string{"/tmp/src:/container/dst:ro"}, result)
}

func TestValidateAndExpandMounts_Invalid(t *testing.T) {
	_, err := validateAndExpandMounts([]string{"no-colon"}, "/home/user")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid mount")
}

func TestValidateAndExpandMounts_Multiple(t *testing.T) {
	result, err := validateAndExpandMounts([]string{
		"/a:/b",
		"/c:/d:ro",
	}, "/home/user")
	require.NoError(t, err)
	assert.Equal(t, []string{"/a:/b", "/c:/d:ro"}, result)
}

func TestValidateAndExpandMounts_TildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	result, err := validateAndExpandMounts([]string{"~/.gitconfig:/home/yoloai/.gitconfig:ro"}, home)
	require.NoError(t, err)
	// Should not start with ~ after expansion
	assert.NotContains(t, result[0], "~")
	assert.Contains(t, result[0], ".gitconfig")
}

func TestValidateAndExpandMounts_Empty(t *testing.T) {
	result, err := validateAndExpandMounts(nil, "/home/user")
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

func (m *mockDockerRuntime) Setup(ctx context.Context, layout config.Layout, sourceDir string, output io.Writer, logger *slog.Logger, force bool) error {
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
func (m *mockDockerRuntime) GitExec(ctx context.Context, name, workDir string, args ...string) (string, error) {
	return "", nil
}
func (m *mockDockerRuntime) InteractiveExec(ctx context.Context, name string, cmd []string, user string, workdir string, io runtime.IOStreams) error {
	return nil
}
func (m *mockDockerRuntime) Prune(ctx context.Context, knownInstances []string, dryRun bool, output io.Writer) (runtime.PruneResult, error) {
	return runtime.PruneResult{}, nil
}
func (m *mockDockerRuntime) Close() error { return nil }
func (m *mockDockerRuntime) Logs(ctx context.Context, name string, lines int) string {
	return ""
}
func (m *mockDockerRuntime) DiagHint(name string) string           { return "" }
func (m *mockDockerRuntime) PrepareAgentCommand(cmd string) string { return cmd }
func (m *mockDockerRuntime) TmuxSocket(sandboxDir string) string   { return "" }
func (m *mockDockerRuntime) AttachCommand(tmuxSocket string, rows, cols int, term string) []string {
	return nil
}
func (m *mockDockerRuntime) Descriptor() runtime.BackendDescriptor {
	return runtime.BackendDescriptor{
		Name:         "mock",
		BaseModeName: "container",
	}
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

	tempDir := t.TempDir()
	sandboxDir := filepath.Join(tempDir, "test-sandbox") // setupWorkdir's first arg is sandboxDir post Q-W.4b
	sourceDir := filepath.Join(tempDir, "source")
	require.NoError(t, os.MkdirAll(sourceDir, 0755))                                             //nolint:gosec // G301: test directory
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "file.txt"), []byte("test"), 0644)) //nolint:gosec // G306: test file

	workdir := &DirSpec{
		Path: sourceDir,
		Mode: DirMode("copy"),
	}

	rt := &mockTartRuntime{}

	// setupWorkdir should return empty SHA for WorkDirSetup backends
	_, baselineSHA, err := setupWorkdir(sandboxDir, workdir, rt)
	require.NoError(t, err)
	assert.Empty(t, baselineSHA, "baseline SHA should be empty for WorkDirSetup backends (baseline deferred to VM)")
}

// TestSetupWorkdir_CreatesBaselineForDockerBackends tests that non-WorkDirSetup
// backends (Docker) get immediate baseline creation with non-empty SHA.
func TestSetupWorkdir_CreatesBaselineForDockerBackends(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping filesystem test in short mode")
	}

	tempDir := t.TempDir()
	sandboxDir := filepath.Join(tempDir, "test-sandbox") // setupWorkdir's first arg is sandboxDir post Q-W.4b
	sourceDir := filepath.Join(tempDir, "source")
	require.NoError(t, os.MkdirAll(sourceDir, 0755))                                             //nolint:gosec // G301: test directory
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "file.txt"), []byte("test"), 0644)) //nolint:gosec // G306: test file

	workdir := &DirSpec{
		Path: sourceDir,
		Mode: DirMode("copy"),
	}

	rt := &mockDockerRuntime{}

	// setupWorkdir should create baseline and return non-empty SHA for Docker
	_, baselineSHA, err := setupWorkdir(sandboxDir, workdir, rt)
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

	tempDir := t.TempDir()
	sandboxDir := filepath.Join(tempDir, "test-sandbox") // setupWorkdir's first arg is sandboxDir post Q-W.4b
	sourceDir := filepath.Join(tempDir, "source")
	require.NoError(t, os.MkdirAll(sourceDir, 0755)) //nolint:gosec // G301: test directory

	workdir := &DirSpec{
		Path: sourceDir,
		Mode: DirMode("overlay"),
	}

	// Test with both runtime types
	runtimes := []runtime.Runtime{
		&mockDockerRuntime{},
		&mockTartRuntime{},
	}

	for _, rt := range runtimes {
		_, baselineSHA, err := setupWorkdir(sandboxDir, workdir, rt)
		require.NoError(t, err)
		assert.Empty(t, baselineSHA, "overlay mode should defer baseline for all backends")
	}
}
