package sandbox

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

	"github.com/kstenerud/yoloai/runtime"
)

// inspectMockRuntime extends mockRuntime to support Inspect and Exec for inspect tests.
type inspectMockRuntime struct {
	mockRuntime
	inspectFn func(ctx context.Context, name string) (runtime.InstanceInfo, error)
	execFn    func(ctx context.Context, name string, cmd []string, user string) (runtime.ExecResult, error)
}

func (m *inspectMockRuntime) Inspect(ctx context.Context, name string) (runtime.InstanceInfo, error) {
	if m.inspectFn != nil {
		return m.inspectFn(ctx, name)
	}
	return runtime.InstanceInfo{}, errMockNotImplemented
}

func (m *inspectMockRuntime) Exec(ctx context.Context, name string, cmd []string, user string) (runtime.ExecResult, error) {
	if m.execFn != nil {
		return m.execFn(ctx, name, cmd, user)
	}
	return m.mockRuntime.Exec(ctx, name, cmd, user)
}

// FormatAge tests

func TestFormatAge_Seconds(t *testing.T) {
	created := time.Now().Add(-30 * time.Second)
	assert.Equal(t, "30s", FormatAge(created))
}

func TestFormatAge_Minutes(t *testing.T) {
	created := time.Now().Add(-5 * time.Minute)
	assert.Equal(t, "5m", FormatAge(created))
}

func TestFormatAge_Hours(t *testing.T) {
	created := time.Now().Add(-2 * time.Hour)
	assert.Equal(t, "2h", FormatAge(created))
}

func TestFormatAge_Days(t *testing.T) {
	created := time.Now().Add(-3 * 24 * time.Hour)
	assert.Equal(t, "3d", FormatAge(created))
}

// detectChanges tests

func TestDetectChanges_NoWorkDir(t *testing.T) {
	assert.Equal(t, "-", detectChanges("/nonexistent/path"))
}

func TestDetectChanges_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	assert.Equal(t, "-", detectChanges(dir))
}

func TestDetectChanges_CleanRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	assert.Equal(t, "no", detectChanges(dir))
}

func TestDetectChanges_DirtyRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, "file.txt", "modified")
	assert.Equal(t, "yes", detectChanges(dir))
}

func TestDetectChanges_UntrackedFiles(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, "new.txt", "untracked")
	assert.Equal(t, "yes", detectChanges(dir))
}

// hasUnappliedWork tests

func TestHasUnappliedWork_NoWorkDir(t *testing.T) {
	assert.False(t, hasUnappliedWork("/nonexistent/path", "abc123"))
}

func TestHasUnappliedWork_CleanAtBaseline(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha := gitRevParse(t, dir)
	assert.False(t, hasUnappliedWork(dir, sha))
}

func TestHasUnappliedWork_DirtyWorkingTree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha := gitRevParse(t, dir)
	writeTestFile(t, dir, "file.txt", "modified")
	assert.True(t, hasUnappliedWork(dir, sha))
}

func TestHasUnappliedWork_CommitsBeyondBaseline(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	baselineSHA := gitRevParse(t, dir)

	// Make a new commit beyond baseline
	writeTestFile(t, dir, "file.txt", "modified")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "agent work")

	assert.True(t, hasUnappliedWork(dir, baselineSHA))
}

func TestHasUnappliedWork_EmptyBaseline(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	// Empty baseline — can't check commits, only dirty tree
	assert.False(t, hasUnappliedWork(dir, ""))
}

// InspectSandbox tests

func TestInspectSandbox_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mock := &inspectMockRuntime{}
	_, err := InspectSandbox(context.Background(), mock, "nonexistent")
	assert.ErrorIs(t, err, ErrSandboxNotFound)
}

func TestInspectSandbox_Removed(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create sandbox dir with environment.json
	name := "test-removed"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))
	meta := &Meta{
		Name:  name,
		Agent: "claude",
		Workdir: WorkdirMeta{
			HostPath: "/tmp/test",
			Mode:     "copy",
		},
		CreatedAt: time.Now(),
	}
	require.NoError(t, SaveMeta(sandboxDir, meta))

	mock := &inspectMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{}, fmt.Errorf("not found: %w", runtime.ErrNotFound)
		},
	}

	info, err := InspectSandbox(context.Background(), mock, name)
	require.NoError(t, err)
	assert.Equal(t, StatusRemoved, info.Status)
}

// ListSandboxes tests

func TestListSandboxes_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create sandboxes dir but leave it empty
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".yoloai", "sandboxes"), 0750))

	mock := &inspectMockRuntime{}
	result, err := ListSandboxes(context.Background(), mock)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestListSandboxes_IncludesBroken(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	sandboxesDir := filepath.Join(tmpDir, ".yoloai", "sandboxes")
	require.NoError(t, os.MkdirAll(sandboxesDir, 0750))

	// Create a valid sandbox
	validDir := filepath.Join(sandboxesDir, "valid")
	require.NoError(t, os.MkdirAll(validDir, 0750))
	meta := &Meta{
		Name:  "valid",
		Agent: "claude",
		Workdir: WorkdirMeta{
			HostPath: "/tmp/test",
			Mode:     "copy",
		},
		CreatedAt: time.Now(),
	}
	require.NoError(t, SaveMeta(validDir, meta))

	// Create a broken sandbox (dir exists but no environment.json)
	brokenDir := filepath.Join(sandboxesDir, "broken")
	require.NoError(t, os.MkdirAll(brokenDir, 0750))

	mock := &inspectMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{}, fmt.Errorf("not found: %w", runtime.ErrNotFound)
		},
	}

	result, err := ListSandboxes(context.Background(), mock)
	require.NoError(t, err)
	require.Len(t, result, 2)

	// Find valid and broken sandboxes (order depends on ReadDir)
	var validInfo, brokenInfo *Info
	for _, info := range result {
		switch info.Meta.Name {
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
	assert.Equal(t, "-", brokenInfo.DiskUsage)
}

// DetectStatus tests (exec fallback — empty sandboxDir)

func TestDetectStatus_Running(t *testing.T) {
	mock := &inspectMockRuntime{
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, AgentStatusFile), []byte(statusData), 0600))

	mock := &inspectMockRuntime{
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, AgentStatusFile), []byte(statusData), 0600))

	mock := &inspectMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
	}
	status, err := DetectStatus(context.Background(), mock, "test", dir)
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, status)
}

func TestDetectStatus_NoStatusFile(t *testing.T) {
	mock := &inspectMockRuntime{
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
	mock := &inspectMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{}, fmt.Errorf("not found: %w", runtime.ErrNotFound)
		},
	}
	status, err := DetectStatus(context.Background(), mock, "test", "")
	require.NoError(t, err)
	assert.Equal(t, StatusRemoved, status)
}

func TestDetectStatus_Stopped(t *testing.T) {
	mock := &inspectMockRuntime{
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

func intPtr(v int) *int { return &v }

func TestDetectStatus_StatusJSON_Active(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, AgentStatusFile),
		statusJSONBytes("active", nil, time.Now().Unix()), 0600))

	mock := &inspectMockRuntime{
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, AgentStatusFile),
		statusJSONBytes("idle", nil, time.Now().Unix()), 0600))

	mock := &inspectMockRuntime{
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, AgentStatusFile),
		statusJSONBytes("done", intPtr(0), time.Now().Unix()), 0600))

	mock := &inspectMockRuntime{
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, AgentStatusFile),
		statusJSONBytes("done", intPtr(1), time.Now().Unix()), 0600))

	mock := &inspectMockRuntime{
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, AgentStatusFile),
		statusJSONBytes("active", nil, time.Now().Add(-30*time.Second).Unix()), 0600))

	mock := &inspectMockRuntime{
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, AgentStatusFile),
		statusJSONBytes("done", intPtr(0), time.Now().Add(-30*time.Second).Unix()), 0600))

	mock := &inspectMockRuntime{
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
		{"done success", statusJSONBytes("done", intPtr(0), now), StatusDone, true},
		{"done failure", statusJSONBytes("done", intPtr(1), now), StatusFailed, true},
		{"done stale success", statusJSONBytes("done", intPtr(0), old), StatusDone, true},
		{"done stale failure", statusJSONBytes("done", intPtr(1), old), StatusFailed, true},
		{"done no exit code", statusJSONBytes("done", nil, now), StatusFailed, true},
		{"unknown status", statusJSONBytes("unknown", nil, now), "", false},
		{"zero timestamp", statusJSONBytes("active", nil, 0), "", false},
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
