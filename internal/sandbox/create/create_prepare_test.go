// ABOUTME: Tests for the create_prepare pipeline: network config, copy/overlay
// ABOUTME: mount collection, mount validation, config defaults, and workdir setup.
package create

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/provision"
	"github.com/kstenerud/yoloai/internal/sandbox/state"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storeMeta writes a minimal valid meta.json into sandboxDir for name.
func storeMeta(sandboxDir, name string) error {
	return store.SaveMeta(sandboxDir, &store.Meta{
		Name:  name,
		Agent: "test",
	})
}

// git test helpers (thin wrappers over testutil so test code stays readable)
func initGitRepo(t *testing.T, dir string)    { t.Helper(); testutil.InitGitRepo(t, dir) }
func gitAdd(t *testing.T, dir, path string)   { t.Helper(); testutil.GitAdd(t, dir, path) }
func gitCommit(t *testing.T, dir, msg string) { t.Helper(); testutil.GitCommit(t, dir, msg) }
func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	testutil.WriteFile(t, dir, name, content)
}

// --- buildNetworkConfig ---

func TestBuildNetworkConfig_Default(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	mode, allow := buildNetworkConfig(Options{}, agentDef)
	assert.Equal(t, "", mode)
	assert.Nil(t, allow)
}

func TestBuildNetworkConfig_None(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	mode, allow := buildNetworkConfig(Options{Network: NetworkModeNone}, agentDef)
	assert.Equal(t, "none", mode)
	assert.Nil(t, allow)
}

func TestBuildNetworkConfig_Isolated(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	mode, allow := buildNetworkConfig(Options{Network: NetworkModeIsolated}, agentDef)
	assert.Equal(t, "isolated", mode)
	// Should include agent's allowlist
	assert.NotEmpty(t, allow)
	assert.Contains(t, allow, "api.anthropic.com")
}

func TestBuildNetworkConfig_IsolatedWithUserAllow(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	opts := Options{
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
	opts := Options{
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
	result, err := validateAndExpandMounts([]string{"/tmp/src:/container/dst"}, "/home/user", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"/tmp/src:/container/dst"}, result)
}

func TestValidateAndExpandMounts_ReadOnly(t *testing.T) {
	result, err := validateAndExpandMounts([]string{"/tmp/src:/container/dst:ro"}, "/home/user", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"/tmp/src:/container/dst:ro"}, result)
}

func TestValidateAndExpandMounts_Invalid(t *testing.T) {
	_, err := validateAndExpandMounts([]string{"no-colon"}, "/home/user", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid mount")
}

func TestValidateAndExpandMounts_Multiple(t *testing.T) {
	result, err := validateAndExpandMounts([]string{
		"/a:/b",
		"/c:/d:ro",
	}, "/home/user", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"/a:/b", "/c:/d:ro"}, result)
}

func TestValidateAndExpandMounts_TildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	result, err := validateAndExpandMounts([]string{"~/.gitconfig:/home/yoloai/.gitconfig:ro"}, home, nil)
	require.NoError(t, err)
	// Should not start with ~ after expansion
	assert.NotContains(t, result[0], "~")
	assert.Contains(t, result[0], ".gitconfig")
}

func TestValidateAndExpandMounts_Empty(t *testing.T) {
	result, err := validateAndExpandMounts(nil, "/home/user", nil)
	require.NoError(t, err)
	assert.Empty(t, result)
}

// --- applyConfigDefaults ---

func TestApplyConfigDefaults_ResourcesFromConfig(t *testing.T) {
	opts := &Options{}
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
	opts := &Options{}
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
	opts := &Options{CPUs: "8", Memory: "16g"}
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
	opts := &Options{CPUs: "8"}
	ycfg := &config.YoloaiConfig{}
	pr := &profileResult{
		resources: &config.ResourceLimits{CPUs: "2", Memory: "4g"},
	}

	require.NoError(t, applyConfigDefaults(opts, ycfg, pr))
	assert.Equal(t, "8", pr.resources.CPUs)
	assert.Equal(t, "4g", pr.resources.Memory) // unchanged
}

func TestApplyConfigDefaults_MountsFromConfigWhenNoProfile(t *testing.T) {
	opts := &Options{} // no profile
	ycfg := &config.YoloaiConfig{
		Mounts: []string{"/a:/b"},
	}
	pr := &profileResult{}

	require.NoError(t, applyConfigDefaults(opts, ycfg, pr))
	assert.Equal(t, []string{"/a:/b"}, pr.mounts)
}

func TestApplyConfigDefaults_MountsSkippedWithProfile(t *testing.T) {
	opts := &Options{Profile: "dev"}
	ycfg := &config.YoloaiConfig{
		Mounts: []string{"/a:/b"},
	}
	pr := &profileResult{mounts: []string{"/c:/d"}}

	require.NoError(t, applyConfigDefaults(opts, ycfg, pr))
	// Profile mounts should not be overwritten
	assert.Equal(t, []string{"/c:/d"}, pr.mounts)
}

func TestApplyConfigDefaults_PortsFromConfigWhenNoProfile(t *testing.T) {
	opts := &Options{Ports: []string{"9090:9090"}}
	ycfg := &config.YoloaiConfig{
		Ports: []string{"8080:8080"},
	}
	pr := &profileResult{}

	require.NoError(t, applyConfigDefaults(opts, ycfg, pr))
	// Config ports prepended to CLI ports
	assert.Equal(t, []string{"8080:8080", "9090:9090"}, opts.Ports)
}

func TestApplyConfigDefaults_NetworkFromConfigWhenNoProfile(t *testing.T) {
	opts := &Options{}
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
	opts := &Options{Network: NetworkModeNone}
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
	opts := &Options{}
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
func (m *mockDockerRuntime) AttachCommand(tmuxSocket string, rows, cols int, term runtime.IsolationMode) []string {
	return nil
}
func (m *mockDockerRuntime) Descriptor() runtime.BackendDescriptor {
	return runtime.BackendDescriptor{
		Name:         "mock",
		BaseModeName: runtime.IsolationModeContainer,
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
	sandboxDir := filepath.Join(tempDir, "test-sandbox")
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
	sandboxDir := filepath.Join(tempDir, "test-sandbox")
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
	sandboxDir := filepath.Join(tempDir, "test-sandbox")
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

// --- checkDirtyRepos (typed refusal, never prompts) ---

func TestCheckDirtyRepos_RefusesUntilAcked(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "tracked.txt", "hi")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "init")
	writeTestFile(t, dir, "wip.txt", "uncommitted") // now dirty (untracked)

	wd := &DirSpec{Path: dir, Mode: DirModeCopy}

	// Default: refuse with a typed *DirtyWorkdirError naming the dir — no prompt.
	err := checkDirtyRepos(wd, nil)
	var dwe *yoerrors.DirtyWorkdirError
	require.ErrorAs(t, err, &dwe)
	require.Len(t, dwe.Dirs, 1)
	assert.Equal(t, dir, dwe.Dirs[0].Path)
	assert.NotEmpty(t, dwe.Dirs[0].Status)

	// AllowDirty acks the specific directory → proceeds.
	wd.AllowDirty = true
	require.NoError(t, checkDirtyRepos(wd, nil))
}

func TestCheckDirtyRepos_CleanRepoPasses(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "tracked.txt", "hi")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "init")

	require.NoError(t, checkDirtyRepos(&DirSpec{Path: dir, Mode: DirModeCopy}, nil))
}

// prepareSandboxState validation tests (via state.Deps, not Engine)

func TestPrepareSandboxState_MissingName(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	d := state.Deps{
		Runtime: &fakeRuntime{},
		Layout:  config.NewLayout(t.TempDir()),
		Input:   strings.NewReader(""),
	}

	_, err := prepareSandboxState(context.TODO(), d, Options{
		Name:    "",
		Workdir: DirSpec{Path: tmpDir},
		Agent:   "test",
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name is required")
}

func TestPrepareSandboxState_UnknownAgent(t *testing.T) {
	tmpDir := t.TempDir()

	d := state.Deps{
		Runtime: &fakeRuntime{},
		Layout:  config.NewLayout(t.TempDir()),
		Input:   strings.NewReader(""),
	}

	_, err := prepareSandboxState(context.TODO(), d, Options{
		Name:    "test",
		Workdir: DirSpec{Path: tmpDir},
		Agent:   "nonexistent-agent",
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown agent")
}

func TestPrepareSandboxState_WorkdirMissing(t *testing.T) {
	tmpDir := t.TempDir()

	d := state.Deps{
		Runtime: &fakeRuntime{},
		Layout:  layoutForTmpDir(tmpDir),
		Input:   strings.NewReader(""),
	}

	_, err := prepareSandboxState(context.TODO(), d, Options{
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
	require.NoError(t, storeMeta(sandboxDir, "existing"))

	d := state.Deps{
		Runtime: &fakeRuntime{},
		Layout:  layoutForTmpDir(tmpDir),
		Input:   strings.NewReader(""),
	}

	_, err := prepareSandboxState(context.TODO(), d, Options{
		Name:    "existing",
		Workdir: DirSpec{Path: tmpDir},
		Agent:   "test",
	}, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSandboxExists)
}

func TestPrepareSandboxState_ConflictingPromptFlags(t *testing.T) {
	tmpDir := t.TempDir()

	d := state.Deps{
		Runtime: &fakeRuntime{},
		Layout:  layoutForTmpDir(tmpDir),
		Input:   strings.NewReader(""),
	}

	_, err := prepareSandboxState(context.TODO(), d, Options{
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

	d := state.Deps{
		Runtime: &fakeRuntime{},
		Layout:  layoutForTmpDir(tmpDir),
		Input:   strings.NewReader(""),
	}

	_, err := prepareSandboxState(context.TODO(), d, Options{
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

	d := state.Deps{
		Runtime: &fakeRuntime{},
		Layout:  layoutForTmpDir(tmpDir),
		Input:   strings.NewReader(""),
	}

	_, err := prepareSandboxState(context.TODO(), d, Options{
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
	d := state.Deps{
		Runtime: &fakeRuntime{},
		Layout:  layoutForTmpDir(tmpDir),
		Input:   strings.NewReader("y\n"),
	}

	_, err := prepareSandboxState(context.TODO(), d, Options{
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

	d := state.Deps{
		Runtime: &fakeRuntime{},
		Layout:  layoutForTmpDir(tmpDir),
		Input:   strings.NewReader(""),
	}

	_, err := prepareSandboxState(context.TODO(), d, Options{
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

	d := state.Deps{
		Runtime: &fakeRuntime{},
		Layout:  layoutForTmpDir(tmpDir),
		Input:   strings.NewReader(""),
	}

	_, err := prepareSandboxState(context.TODO(), d, Options{
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

	d := state.Deps{
		Runtime: &fakeRuntime{},
		Layout:  layoutForTmpDir(tmpDir),
		Input:   strings.NewReader("y\n"),
	}

	st, err := prepareSandboxState(context.TODO(), d, Options{
		Name:    "test",
		Workdir: DirSpec{Path: workDir},
		Agent:   "claude",
		Network: NetworkModeIsolated,
		Version: "test",
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, st)

	assert.Equal(t, "isolated", st.NetworkMode)
	assert.Contains(t, st.NetworkAllow, "api.anthropic.com")
	assert.Contains(t, st.NetworkAllow, "statsig.anthropic.com")
	assert.Contains(t, st.NetworkAllow, "sentry.io")
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

	d := state.Deps{
		Runtime: &fakeRuntime{},
		Layout:  layoutForTmpDir(tmpDir),
		Input:   strings.NewReader("y\n"),
	}

	st, err := prepareSandboxState(context.TODO(), d, Options{
		Name:         "test",
		Workdir:      DirSpec{Path: workDir},
		Agent:        "claude",
		Network:      NetworkModeIsolated,
		NetworkAllow: []string{"api.example.com"},
		Version:      "test",
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, st)

	assert.Equal(t, "isolated", st.NetworkMode)
	assert.Contains(t, st.NetworkAllow, "api.anthropic.com")
	assert.Contains(t, st.NetworkAllow, "api.example.com")
}
