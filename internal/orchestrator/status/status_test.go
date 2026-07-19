// ABOUTME: Sandbox status detection: pane-death/status.json/staleness-driven
// ABOUTME: state classification, workdir-data presence probing (copy and
// ABOUTME: overlay), and InspectSandbox/ListSandboxes including net-health
// ABOUTME: probes.
package status

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/internal/orchestrator/agentcfg"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
)

// InspectSandbox tests

func TestInspectSandbox_NotFound(t *testing.T) {
	tmpDir := t.TempDir()

	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai")).WithPrincipal(config.CLIPrincipal)
	mock := &fakeRuntime{}
	_, err := InspectSandbox(context.Background(), layout, mock, "nonexistent")
	assert.ErrorIs(t, err, store.ErrSandboxNotFound)
}

func TestInspectSandbox_Removed(t *testing.T) {
	tmpDir := t.TempDir()

	// Create sandbox dir with environment.json
	name := "test-removed"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))
	meta := &store.Environment{
		Name:      name,
		Principal: config.CLIPrincipal,
		Dirs: []store.DirEnvironment{{
			HostPath: "/tmp/test",
			Mode:     "copy",
		}},
		CreatedAt: time.Now(),
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))
	require.NoError(t, agentcfg.Save(sandboxDir, &agentcfg.AgentConfig{AgentType: "claude"}))

	mock := &fakeRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{}, fmt.Errorf("not found: %w", runtime.ErrNotFound)
		},
	}

	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai")).WithPrincipal(config.CLIPrincipal)
	info, err := InspectSandbox(context.Background(), layout, mock, name)
	require.NoError(t, err)
	assert.Equal(t, StatusRemoved, info.Status)
}

// ListSandboxes tests

func TestListSandboxes_Empty(t *testing.T) {
	tmpDir := t.TempDir()

	// Create sandboxes dir but leave it empty
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".yoloai", "sandboxes"), 0750))

	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai")).WithPrincipal(config.CLIPrincipal)
	mock := &fakeRuntime{}
	result, err := ListSandboxes(context.Background(), layout, mock)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestListSandboxes_IncludesBroken(t *testing.T) {
	tmpDir := t.TempDir()

	sandboxesDir := filepath.Join(tmpDir, ".yoloai", "sandboxes")
	require.NoError(t, os.MkdirAll(sandboxesDir, 0750))

	// Create a valid sandbox
	validDir := filepath.Join(sandboxesDir, "valid")
	require.NoError(t, os.MkdirAll(validDir, 0750))
	meta := &store.Environment{
		Name:      "valid",
		Principal: config.CLIPrincipal,
		Dirs: []store.DirEnvironment{{
			HostPath: "/tmp/test",
			Mode:     "copy",
		}},
		CreatedAt: time.Now(),
	}
	require.NoError(t, store.SaveEnvironment(validDir, meta))
	require.NoError(t, agentcfg.Save(validDir, &agentcfg.AgentConfig{AgentType: "claude"}))

	// Create a broken sandbox (dir exists but no environment.json)
	brokenDir := filepath.Join(sandboxesDir, "broken")
	require.NoError(t, os.MkdirAll(brokenDir, 0750))

	mock := &fakeRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{}, fmt.Errorf("not found: %w", runtime.ErrNotFound)
		},
	}

	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai")).WithPrincipal(config.CLIPrincipal)
	result, err := ListSandboxes(context.Background(), layout, mock)
	require.NoError(t, err)
	require.Len(t, result, 2)

	// Find valid and broken sandboxes (order depends on ReadDir)
	var validInfo, brokenInfo *Info
	for _, info := range result {
		switch info.Environment.Name {
		case "valid":
			validInfo = info
		case "broken":
			brokenInfo = info
		}
	}

	require.NotNil(t, validInfo)
	assert.Equal(t, StatusRemoved, validInfo.Status)

	require.NotNil(t, brokenInfo)
	assert.Equal(t, StatusBroken, brokenInfo.Status)
	assert.Equal(t, "-", brokenInfo.HasChanges)
	assert.Equal(t, int64(-1), brokenInfo.DiskUsageBytes)
}

// ListSandboxesMultiBackend groups sandboxes by their recorded backend and
// inspects each group with a runtime opened for that backend. It is the read
// sibling of the DF138 destroy fix (which routes teardown through each
// sandbox's own backend) and had no unit test. This exercises all three fates:
// a sandbox on an available backend is inspected, one on an unavailable backend
// reports StatusUnavailable (with the backend named once, however many
// sandboxes it holds), and one with unreadable metadata reports StatusBroken.
func TestListSandboxesMultiBackend_GroupsAvailableUnavailableAndBroken(t *testing.T) {
	tmpDir := t.TempDir()
	sandboxesDir := filepath.Join(tmpDir, ".yoloai", "sandboxes")
	require.NoError(t, os.MkdirAll(sandboxesDir, 0750))

	saveOn := func(name string, backend runtime.BackendType) {
		dir := filepath.Join(sandboxesDir, name)
		require.NoError(t, os.MkdirAll(dir, 0750))
		require.NoError(t, store.SaveEnvironment(dir, &store.Environment{
			Name:        name,
			Principal:   config.CLIPrincipal,
			BackendType: backend,
			Dirs:        []store.DirEnvironment{{HostPath: "/tmp/test", Mode: "copy"}},
			CreatedAt:   time.Now(),
		}))
	}
	saveOn("alpha", "backend-a")    // available
	saveOn("gamma", "backend-gone") // unavailable
	saveOn("delta", "backend-gone") // same unavailable backend — dedup
	brokenDir := filepath.Join(sandboxesDir, "broken")
	require.NoError(t, os.MkdirAll(brokenDir, 0750)) // no environment.json

	requested := map[runtime.BackendType]int{}
	newRT := func(_ context.Context, bt runtime.BackendType) (runtime.Backend, error) {
		requested[bt]++
		if bt == "backend-gone" {
			return nil, fmt.Errorf("backend %s is not running", bt)
		}
		return &fakeRuntime{
			inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
				return runtime.InstanceInfo{}, fmt.Errorf("not found: %w", runtime.ErrNotFound)
			},
		}, nil
	}

	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai")).WithPrincipal(config.CLIPrincipal)
	result, unavailable, err := ListSandboxesMultiBackend(context.Background(), layout, newRT)
	require.NoError(t, err)

	byName := map[string]*Info{}
	for _, info := range result {
		byName[info.Environment.Name] = info
	}
	require.Len(t, result, 4)

	// Available backend: opened and inspected → not broken, not unavailable.
	require.NotNil(t, byName["alpha"])
	assert.Equal(t, StatusRemoved, byName["alpha"].Status)

	// Unavailable backend: both sandboxes reported unavailable, the backend
	// opened once for the group and named exactly once.
	require.NotNil(t, byName["gamma"])
	require.NotNil(t, byName["delta"])
	assert.Equal(t, StatusUnavailable, byName["gamma"].Status)
	assert.Equal(t, StatusUnavailable, byName["delta"].Status)
	assert.Equal(t, []string{"backend-gone"}, unavailable, "an unavailable backend must be named once, not per sandbox")
	assert.Equal(t, 1, requested["backend-gone"], "a backend group opens its runtime once")

	// Unreadable metadata: broken, and no runtime opened for the empty backend.
	require.NotNil(t, byName["broken"])
	assert.Equal(t, StatusBroken, byName["broken"].Status)
	assert.Equal(t, 0, requested[""], "the broken group must not open a runtime")
}

// DetectStatus tests (exec fallback — empty sandboxDir)

func TestDetectStatus_Running(t *testing.T) {
	mock := &fakeRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
		execFn: func(_ context.Context, _ string, _ []string, _ string) (runtime.ExecResult, error) {
			return runtime.ExecResult{Stdout: "0|0"}, nil
		},
	}
	status, err := DetectStatus(context.Background(), mock, "test", "")
	require.NoError(t, err)
	assert.Equal(t, StatusActive, status)
}

func TestDetectStatus_Done(t *testing.T) {
	dir := t.TempDir()
	exitCode := 0
	statusData := fmt.Sprintf(`{"status":"done","exit_code":%d,"timestamp":%d}`, exitCode, time.Now().Unix())
	require.NoError(t, os.WriteFile(filepath.Join(dir, store.AgentStatusFile), []byte(statusData), 0600))

	mock := &fakeRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
	}
	status, err := DetectStatus(context.Background(), mock, "test", dir)
	require.NoError(t, err)
	assert.Equal(t, StatusDone, status)
}

func TestDetectStatus_Failed(t *testing.T) {
	dir := t.TempDir()
	exitCode := 1
	statusData := fmt.Sprintf(`{"status":"done","exit_code":%d,"timestamp":%d}`, exitCode, time.Now().Unix())
	require.NoError(t, os.WriteFile(filepath.Join(dir, store.AgentStatusFile), []byte(statusData), 0600))

	mock := &fakeRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
	}
	status, err := DetectStatus(context.Background(), mock, "test", dir)
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, status)
}

func TestDetectStatus_NoStatusFile(t *testing.T) {
	mock := &fakeRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
	}
	// No sandboxDir or status file — assumes active
	status, err := DetectStatus(context.Background(), mock, "test", "")
	require.NoError(t, err)
	assert.Equal(t, StatusActive, status)
}

func TestDetectStatus_Removed(t *testing.T) {
	mock := &fakeRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{}, fmt.Errorf("not found: %w", runtime.ErrNotFound)
		},
	}
	status, err := DetectStatus(context.Background(), mock, "test", "")
	require.NoError(t, err)
	assert.Equal(t, StatusRemoved, status)
}

func TestDetectStatus_Stopped(t *testing.T) {
	mock := &fakeRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: false}, nil
		},
	}
	status, err := DetectStatus(context.Background(), mock, "test", "")
	require.NoError(t, err)
	assert.Equal(t, StatusStopped, status)
}

// DetectStatus tests (agent-status.json)

func statusJSONBytes(status string, exitCode *int, ts int64) []byte {
	type sj struct {
		Status    string `json:"status"`
		ExitCode  *int   `json:"exit_code,omitempty"`
		Timestamp int64  `json:"timestamp"`
	}
	data, _ := json.Marshal(sj{Status: status, ExitCode: exitCode, Timestamp: ts})
	return data
}

func TestDetectStatus_StatusJSON_Active(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, store.AgentStatusFile),
		statusJSONBytes("active", nil, time.Now().Unix()), 0600))

	mock := &fakeRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
	}
	status, err := DetectStatus(context.Background(), mock, "test", dir)
	require.NoError(t, err)
	assert.Equal(t, StatusActive, status)
}

func TestDetectStatus_StatusJSON_Idle(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, store.AgentStatusFile),
		statusJSONBytes("idle", nil, time.Now().Unix()), 0600))

	mock := &fakeRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
	}
	status, err := DetectStatus(context.Background(), mock, "test", dir)
	require.NoError(t, err)
	assert.Equal(t, StatusIdle, status)
}

func TestDetectStatus_StatusJSON_Done(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, store.AgentStatusFile),
		statusJSONBytes("done", new(0), time.Now().Unix()), 0600))

	mock := &fakeRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
	}
	status, err := DetectStatus(context.Background(), mock, "test", dir)
	require.NoError(t, err)
	assert.Equal(t, StatusDone, status)
}

func TestDetectStatus_StatusJSON_Failed(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, store.AgentStatusFile),
		statusJSONBytes("done", new(1), time.Now().Unix()), 0600))

	mock := &fakeRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
	}
	status, err := DetectStatus(context.Background(), mock, "test", dir)
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, status)
}

func TestDetectStatus_StatusJSON_Stale(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, store.AgentStatusFile),
		statusJSONBytes("active", nil, time.Now().Add(-30*time.Second).Unix()), 0600))

	mock := &fakeRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
		execFn: func(_ context.Context, _ string, _ []string, _ string) (runtime.ExecResult, error) {
			return runtime.ExecResult{Stdout: "0|0"}, nil
		},
	}
	status, err := DetectStatus(context.Background(), mock, "test", dir)
	require.NoError(t, err)
	assert.Equal(t, StatusActive, status) // falls back to exec
}

func TestDetectStatus_StatusJSON_StaleDone(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, store.AgentStatusFile),
		statusJSONBytes("done", new(0), time.Now().Add(-30*time.Second).Unix()), 0600))

	mock := &fakeRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
	}
	status, err := DetectStatus(context.Background(), mock, "test", dir)
	require.NoError(t, err)
	assert.Equal(t, StatusDone, status) // "done" is terminal — trusted even if stale
}

// parseStatusJSON tests

func TestParseStatusJSON(t *testing.T) {
	now := time.Now().Unix()
	old := time.Now().Add(-30 * time.Second).Unix()

	tests := []struct {
		name     string
		data     []byte
		status   Status
		exitCode *int
		ok       bool
	}{
		{"empty", []byte("{}"), "", nil, false},
		{"invalid json", []byte("{bad"), "", nil, false},
		{"active fresh", statusJSONBytes("active", nil, now), StatusActive, nil, true},
		{"idle fresh", statusJSONBytes("idle", nil, now), StatusIdle, nil, true},
		{"active stale", statusJSONBytes("active", nil, old), "", nil, false},
		{"idle stale", statusJSONBytes("idle", nil, old), StatusIdle, nil, true},
		{"done success", statusJSONBytes("done", new(0), now), StatusDone, new(0), true},
		{"done failure", statusJSONBytes("done", new(1), now), StatusFailed, new(1), true},
		{"done stale success", statusJSONBytes("done", new(0), old), StatusDone, new(0), true},
		{"done stale failure", statusJSONBytes("done", new(1), old), StatusFailed, new(1), true},
		{"done no exit code", statusJSONBytes("done", nil, now), StatusFailed, new(1), true},
		{"done failure code 3", statusJSONBytes("done", new(3), now), StatusFailed, new(3), true},
		{"unknown status", statusJSONBytes("unknown", nil, now), "", nil, false},
		{"zero timestamp", statusJSONBytes("active", nil, 0), "", nil, false},
		// W2 schema versioning: missing schema_version (=0) is tolerated;
		// mismatched non-zero schema_version causes the reader to discard the
		// status as unusable rather than misinterpret it.
		{"schema_version match", []byte(`{"schema_version":1,"status":"active","timestamp":` + fmt.Sprint(now) + `}`), StatusActive, nil, true},
		{"schema_version mismatch", []byte(`{"schema_version":99,"status":"active","timestamp":` + fmt.Sprint(now) + `}`), "", nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, exitCode, ok := parseStatusJSON(tc.data)
			assert.Equal(t, tc.ok, ok)
			if ok {
				assert.Equal(t, tc.status, status)
				if tc.exitCode == nil {
					assert.Nil(t, exitCode)
				} else {
					require.NotNil(t, exitCode)
					assert.Equal(t, *tc.exitCode, *exitCode)
				}
			}
		})
	}
}

// ProbeWorkData tests

func TestProbeWorkData_NoWorkDir(t *testing.T) {
	st, _ := ProbeWorkData(context.Background(), git.NewTestHostWithEnv(testutil.GitEnv()), t.TempDir())
	assert.Equal(t, WorkDataNone, st)
}

func TestProbeWorkData_EmptyWorkDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "work"), 0o750))
	st, _ := ProbeWorkData(context.Background(), git.NewTestHostWithEnv(testutil.GitEnv()), dir)
	assert.Equal(t, WorkDataNone, st)
}

func TestProbeWorkData_CopyCleanIsAmbiguous(t *testing.T) {
	dir := t.TempDir()
	work := filepath.Join(dir, "work", store.EncodePath("/home/u/proj"))
	require.NoError(t, os.MkdirAll(work, 0o750))
	testutil.InitGitRepo(t, work)
	testutil.WriteFile(t, work, "file.txt", "hello")
	testutil.GitAdd(t, work, ".")
	testutil.GitCommit(t, work, "initial")

	// Clean tree, but baseline is unknown without meta — preserve it.
	st, _ := ProbeWorkData(context.Background(), git.NewTestHostWithEnv(testutil.GitEnv()), dir)
	assert.Equal(t, WorkDataAmbiguous, st)
}

func TestProbeWorkData_CopyDirtyIsPresent(t *testing.T) {
	dir := t.TempDir()
	work := filepath.Join(dir, "work", store.EncodePath("/home/u/proj"))
	require.NoError(t, os.MkdirAll(work, 0o750))
	testutil.InitGitRepo(t, work)
	testutil.WriteFile(t, work, "file.txt", "hello")
	testutil.GitAdd(t, work, ".")
	testutil.GitCommit(t, work, "initial")
	testutil.WriteFile(t, work, "file.txt", "modified")

	st, detail := ProbeWorkData(context.Background(), git.NewTestHostWithEnv(testutil.GitEnv()), dir)
	assert.Equal(t, WorkDataPresent, st)
	assert.NotEmpty(t, detail)
}

func TestProbeWorkData_OverlayUpperNonEmptyIsPresent(t *testing.T) {
	dir := t.TempDir()
	upper := filepath.Join(dir, "work", store.EncodePath("/home/u/proj"), "upper")
	require.NoError(t, os.MkdirAll(upper, 0o750))
	testutil.WriteFile(t, upper, "changed.txt", "diff")

	st, detail := ProbeWorkData(context.Background(), git.NewTestHostWithEnv(testutil.GitEnv()), dir)
	assert.Equal(t, WorkDataPresent, st)
	assert.NotEmpty(t, detail)
}

func TestProbeWorkData_OverlayUpperEmptyIsAmbiguous(t *testing.T) {
	dir := t.TempDir()
	// Overlay scaffolding present (upper/ exists) but no captured changes.
	upper := filepath.Join(dir, "work", store.EncodePath("/home/u/proj"), "upper")
	require.NoError(t, os.MkdirAll(upper, 0o750))

	st, _ := ProbeWorkData(context.Background(), git.NewTestHostWithEnv(testutil.GitEnv()), dir)
	assert.Equal(t, WorkDataAmbiguous, st)
}

// probeNetHealth / SandboxNetHealthProber tests

// writeNetHealthFixture creates a minimal sandbox directory for net-health
// tests and returns the layout to inspect it with.
func writeNetHealthFixture(t *testing.T, name string) config.Layout {
	t.Helper()
	tmpDir := t.TempDir()
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))
	meta := &store.Environment{
		Name:      name,
		Principal: config.CLIPrincipal,
		Dirs: []store.DirEnvironment{{
			HostPath: "/tmp/test",
			Mode:     "copy",
		}},
		CreatedAt: time.Now(),
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))
	require.NoError(t, agentcfg.Save(sandboxDir, &agentcfg.AgentConfig{AgentType: "claude"}))
	return config.NewLayout(filepath.Join(tmpDir, ".yoloai")).WithPrincipal(config.CLIPrincipal)
}

// runningInspectFn reports the instance as running, so DetectStatus (with no
// status file) yields StatusActive.
func runningInspectFn(_ context.Context, _ string) (runtime.InstanceInfo, error) {
	return runtime.InstanceInfo{Running: true}, nil
}

func TestInspectSandbox_NetHealth_ProbedWhenActive(t *testing.T) {
	name := "net-active"
	layout := writeNetHealthFixture(t, name)
	mock := &fakeProberRuntime{
		fakeRuntime: fakeRuntime{inspectFn: runningInspectFn},
		health: runtime.VMNetHealth{
			SandboxName: name,
			VMName:      "yoloai-" + name,
			State:       runtime.NetHealthWedged,
			Detail:      "169.254.93.37",
		},
	}

	info, err := InspectSandbox(context.Background(), layout, mock, name)
	require.NoError(t, err)
	assert.Equal(t, StatusActive, info.Status)
	assert.Equal(t, []string{name}, mock.probeCalls)
	assert.Equal(t, "wedged", info.NetHealth)
	assert.Equal(t, "169.254.93.37", info.NetHealthDetail)
}

func TestInspectSandbox_NetHealth_NotProbedWhenStopped(t *testing.T) {
	name := "net-stopped"
	layout := writeNetHealthFixture(t, name)
	mock := &fakeProberRuntime{
		fakeRuntime: fakeRuntime{
			inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
				return runtime.InstanceInfo{Running: false}, nil
			},
		},
	}

	info, err := InspectSandbox(context.Background(), layout, mock, name)
	require.NoError(t, err)
	assert.Equal(t, StatusStopped, info.Status)
	assert.Empty(t, mock.probeCalls, "a stopped sandbox must not be probed")
	assert.Empty(t, info.NetHealth)
	assert.Empty(t, info.NetHealthDetail)
}

func TestInspectSandbox_NetHealth_EmptyWithoutProber(t *testing.T) {
	name := "net-noprober"
	layout := writeNetHealthFixture(t, name)
	mock := &fakeRuntime{inspectFn: runningInspectFn}

	info, err := InspectSandbox(context.Background(), layout, mock, name)
	require.NoError(t, err)
	assert.Equal(t, StatusActive, info.Status)
	assert.Empty(t, info.NetHealth)
	assert.Empty(t, info.NetHealthDetail)
}

func TestInspectSandbox_NetHealth_ProbeErrorLeavesFieldsEmpty(t *testing.T) {
	name := "net-probeerr"
	layout := writeNetHealthFixture(t, name)
	mock := &fakeProberRuntime{
		fakeRuntime: fakeRuntime{inspectFn: runningInspectFn},
		probeErr:    fmt.Errorf("tart exec: connection refused"),
	}

	info, err := InspectSandbox(context.Background(), layout, mock, name)
	require.NoError(t, err, "a failed probe must never fail the inspect")
	assert.Equal(t, StatusActive, info.Status)
	assert.Empty(t, info.NetHealth)
	assert.Empty(t, info.NetHealthDetail)
}

func TestInspectSandboxWithBackend_NetHealth_ProbedWhenActive(t *testing.T) {
	name := "net-backend"
	layout := writeNetHealthFixture(t, name)
	mock := &fakeProberRuntime{
		fakeRuntime: fakeRuntime{inspectFn: runningInspectFn},
		health: runtime.VMNetHealth{
			SandboxName: name,
			VMName:      "yoloai-" + name,
			State:       runtime.NetHealthOK,
			Detail:      "192.168.64.12",
		},
	}

	info, err := InspectSandboxWithBackend(context.Background(), layout, mock, name)
	require.NoError(t, err)
	assert.Equal(t, StatusActive, info.Status)
	assert.Equal(t, []string{name}, mock.probeCalls)
	assert.Equal(t, "ok", info.NetHealth)
	assert.Equal(t, "192.168.64.12", info.NetHealthDetail)
}
