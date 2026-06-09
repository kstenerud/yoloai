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
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/testutil"
)

// InspectSandbox tests

func TestInspectSandbox_NotFound(t *testing.T) {
	tmpDir := t.TempDir()

	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai"))
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
		AgentType: "claude",
		Workdir: store.WorkdirEnvironment{
			HostPath: "/tmp/test",
			Mode:     "copy",
		},
		CreatedAt: time.Now(),
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	mock := &fakeRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{}, fmt.Errorf("not found: %w", runtime.ErrNotFound)
		},
	}

	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai"))
	info, err := InspectSandbox(context.Background(), layout, mock, name)
	require.NoError(t, err)
	assert.Equal(t, StatusRemoved, info.Status)
}

// ListSandboxes tests

func TestListSandboxes_Empty(t *testing.T) {
	tmpDir := t.TempDir()

	// Create sandboxes dir but leave it empty
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".yoloai", "sandboxes"), 0750))

	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai"))
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
		AgentType: "claude",
		Workdir: store.WorkdirEnvironment{
			HostPath: "/tmp/test",
			Mode:     "copy",
		},
		CreatedAt: time.Now(),
	}
	require.NoError(t, store.SaveEnvironment(validDir, meta))

	// Create a broken sandbox (dir exists but no environment.json)
	brokenDir := filepath.Join(sandboxesDir, "broken")
	require.NoError(t, os.MkdirAll(brokenDir, 0750))

	mock := &fakeRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{}, fmt.Errorf("not found: %w", runtime.ErrNotFound)
		},
	}

	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai"))
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
		name   string
		data   []byte
		status Status
		ok     bool
	}{
		{"empty", []byte("{}"), "", false},
		{"invalid json", []byte("{bad"), "", false},
		{"active fresh", statusJSONBytes("active", nil, now), StatusActive, true},
		{"idle fresh", statusJSONBytes("idle", nil, now), StatusIdle, true},
		{"active stale", statusJSONBytes("active", nil, old), "", false},
		{"idle stale", statusJSONBytes("idle", nil, old), StatusIdle, true},
		{"done success", statusJSONBytes("done", new(0), now), StatusDone, true},
		{"done failure", statusJSONBytes("done", new(1), now), StatusFailed, true},
		{"done stale success", statusJSONBytes("done", new(0), old), StatusDone, true},
		{"done stale failure", statusJSONBytes("done", new(1), old), StatusFailed, true},
		{"done no exit code", statusJSONBytes("done", nil, now), StatusFailed, true},
		{"unknown status", statusJSONBytes("unknown", nil, now), "", false},
		{"zero timestamp", statusJSONBytes("active", nil, 0), "", false},
		// W2 schema versioning: missing schema_version (=0) is tolerated;
		// mismatched non-zero schema_version causes the reader to discard the
		// status as unusable rather than misinterpret it.
		{"schema_version match", []byte(`{"schema_version":1,"status":"active","timestamp":` + fmt.Sprint(now) + `}`), StatusActive, true},
		{"schema_version mismatch", []byte(`{"schema_version":99,"status":"active","timestamp":` + fmt.Sprint(now) + `}`), "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, ok := parseStatusJSON(tc.data)
			assert.Equal(t, tc.ok, ok)
			if ok {
				assert.Equal(t, tc.status, status)
			}
		})
	}
}

// ProbeWorkData tests

func TestProbeWorkData_NoWorkDir(t *testing.T) {
	st, _ := ProbeWorkData(context.Background(), git.NewHostWithEnv(testutil.GitEnv()), t.TempDir())
	assert.Equal(t, WorkDataNone, st)
}

func TestProbeWorkData_EmptyWorkDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "work"), 0o750))
	st, _ := ProbeWorkData(context.Background(), git.NewHostWithEnv(testutil.GitEnv()), dir)
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
	st, _ := ProbeWorkData(context.Background(), git.NewHostWithEnv(testutil.GitEnv()), dir)
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

	st, detail := ProbeWorkData(context.Background(), git.NewHostWithEnv(testutil.GitEnv()), dir)
	assert.Equal(t, WorkDataPresent, st)
	assert.NotEmpty(t, detail)
}

func TestProbeWorkData_OverlayUpperNonEmptyIsPresent(t *testing.T) {
	dir := t.TempDir()
	upper := filepath.Join(dir, "work", store.EncodePath("/home/u/proj"), "upper")
	require.NoError(t, os.MkdirAll(upper, 0o750))
	testutil.WriteFile(t, upper, "changed.txt", "diff")

	st, detail := ProbeWorkData(context.Background(), git.NewHostWithEnv(testutil.GitEnv()), dir)
	assert.Equal(t, WorkDataPresent, st)
	assert.NotEmpty(t, detail)
}

func TestProbeWorkData_OverlayUpperEmptyIsAmbiguous(t *testing.T) {
	dir := t.TempDir()
	// Overlay scaffolding present (upper/ exists) but no captured changes.
	upper := filepath.Join(dir, "work", store.EncodePath("/home/u/proj"), "upper")
	require.NoError(t, os.MkdirAll(upper, 0o750))

	st, _ := ProbeWorkData(context.Background(), git.NewHostWithEnv(testutil.GitEnv()), dir)
	assert.Equal(t, WorkDataAmbiguous, st)
}
