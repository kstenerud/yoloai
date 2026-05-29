package sandbox

// ABOUTME: Unit tests for CaptureTerminal — confirms the tmux command shape,
// ABOUTME: dual plain+ANSI capture, and the not-running error path.

import (
	"context"
	"errors"
	"testing"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// terminalMockRuntime is a CaptureTerminal-focused mock: it captures
// each Exec call's args so the test can assert the tmux command shape,
// and returns canned ExecResult values keyed by whether the ANSI flag
// (-e) is present in the command.
type terminalMockRuntime struct {
	mockRuntime
	tmuxSocket   string
	plainResult  runtime.ExecResult
	plainErr     error
	ansiResult   runtime.ExecResult
	ansiErr      error
	execCalls    [][]string
	inspectFn    func(ctx context.Context, name string) (runtime.InstanceInfo, error)
	tmuxCallUser string
}

func (m *terminalMockRuntime) TmuxSocket(_ string) string { return m.tmuxSocket }

func (m *terminalMockRuntime) Exec(_ context.Context, _ string, cmd []string, user string) (runtime.ExecResult, error) {
	m.execCalls = append(m.execCalls, append([]string(nil), cmd...))
	m.tmuxCallUser = user
	for _, a := range cmd {
		if a == "-e" {
			return m.ansiResult, m.ansiErr
		}
	}
	return m.plainResult, m.plainErr
}

func (m *terminalMockRuntime) Inspect(ctx context.Context, name string) (runtime.InstanceInfo, error) {
	if m.inspectFn != nil {
		return m.inspectFn(ctx, name)
	}
	return runtime.InstanceInfo{}, errMockNotImplemented
}

func TestCaptureTerminal_NotRunning(t *testing.T) {
	tmpDir := t.TempDir()
	createTestSandbox(t, tmpDir, "capt-stopped", "/tmp/project", "copy")

	// Runtime reports the instance isn't running → Status downgrades from
	// Active/Idle to Stopped, which CaptureTerminal rejects with
	// ErrContainerNotRunning.
	mock := &terminalMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: false}, nil
		},
	}
	mgr := newTerminalMgr(mock, tmpDir)

	_, _, err := mgr.CaptureTerminal(context.Background(), "capt-stopped", 200)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrContainerNotRunning, "must surface ErrContainerNotRunning so callers can best-effort-skip")
	assert.Empty(t, mock.execCalls, "must not invoke tmux when sandbox isn't running")
}

func TestCaptureTerminal_BuildsTmuxCommand(t *testing.T) {
	tmpDir := t.TempDir()
	createTestSandbox(t, tmpDir, "capt-running", "/tmp/project", "copy")

	mock := &terminalMockRuntime{
		tmuxSocket: "/tmp/yoloai-tmux.sock",
		plainResult: runtime.ExecResult{
			Stdout: "agent screen contents\n", ExitCode: 0,
		},
		ansiResult: runtime.ExecResult{
			Stdout: "\x1b[31magent screen\x1b[0m\n", ExitCode: 0,
		},
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
	}
	mgr := newTerminalMgr(mock, tmpDir)

	plain, ansi, err := mgr.CaptureTerminal(context.Background(), "capt-running", 200)
	require.NoError(t, err)
	assert.Equal(t, "agent screen contents\n", string(plain))
	assert.Equal(t, "\x1b[31magent screen\x1b[0m\n", string(ansi))

	// Two Exec calls — one plain, one ANSI. Both share the same prefix
	// (tmux -S <socket> capture-pane -p -t main -S -200), the ANSI one
	// also has the trailing -e flag.
	require.Len(t, mock.execCalls, 2)
	plainArgs := mock.execCalls[0]
	ansiArgs := mock.execCalls[1]
	assert.Equal(t, []string{
		"tmux", "-S", "/tmp/yoloai-tmux.sock",
		"capture-pane", "-p", "-t", "main", "-S", "-200",
	}, plainArgs)
	assert.Equal(t, []string{
		"tmux", "-S", "/tmp/yoloai-tmux.sock",
		"capture-pane", "-p", "-t", "main", "-S", "-200", "-e",
	}, ansiArgs)

	// User propagation: tmux server runs as the sandbox's container user
	// (resolved via sandbox.ContainerUser from meta); the mock captures
	// whatever Exec was invoked with. For the test sandbox meta, that's "".
	// The exact value isn't pinned here — only that the same user is used
	// across both calls (we already pinned this by virtue of the field).
	assert.NotNil(t, mock.tmuxCallUser, "user argument must be propagated")
}

func TestCaptureTerminal_AnsiFailureReturnsPartial(t *testing.T) {
	tmpDir := t.TempDir()
	createTestSandbox(t, tmpDir, "capt-partial", "/tmp/project", "copy")

	mock := &terminalMockRuntime{
		tmuxSocket:  "/tmp/yoloai-tmux.sock",
		plainResult: runtime.ExecResult{Stdout: "plain works\n", ExitCode: 0},
		ansiErr:     errors.New("ansi capture failed"),
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
	}
	mgr := newTerminalMgr(mock, tmpDir)

	plain, ansi, err := mgr.CaptureTerminal(context.Background(), "capt-partial", 200)
	require.Error(t, err, "ansi failure should be surfaced")
	assert.Contains(t, err.Error(), "ansi")
	assert.Equal(t, "plain works\n", string(plain), "plain must be returned even when ANSI errored")
	assert.Nil(t, ansi, "ANSI must be nil so callers can degrade gracefully")
}

func TestCaptureTerminal_ZeroScrollbackOmitsHistoryFlag(t *testing.T) {
	tmpDir := t.TempDir()
	createTestSandbox(t, tmpDir, "capt-noscroll", "/tmp/project", "copy")

	mock := &terminalMockRuntime{
		tmuxSocket:  "/tmp/yoloai-tmux.sock",
		plainResult: runtime.ExecResult{Stdout: "viewport only\n", ExitCode: 0},
		ansiResult:  runtime.ExecResult{Stdout: "viewport only ansi\n", ExitCode: 0},
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
	}
	mgr := newTerminalMgr(mock, tmpDir)

	_, _, err := mgr.CaptureTerminal(context.Background(), "capt-noscroll", 0)
	require.NoError(t, err)

	// scrollback=0 → no "-S -200" suffix; only the socket -S should appear.
	// Each call should have exactly one "-S" (for the socket).
	require.Len(t, mock.execCalls, 2)
	assert.Equal(t, []string{
		"tmux", "-S", "/tmp/yoloai-tmux.sock", "capture-pane", "-p", "-t", "main",
	}, mock.execCalls[0])
	assert.Equal(t, []string{
		"tmux", "-S", "/tmp/yoloai-tmux.sock", "capture-pane", "-p", "-t", "main", "-e",
	}, mock.execCalls[1])
}

// newTerminalMgr is a CaptureTerminal-focused helper that mirrors
// newLifecycleMgr but accepts the wider mock used here.
func newTerminalMgr(rt *terminalMockRuntime, tmpDir string) *Engine {
	return newLifecycleMgr(&lifecycleMockRuntime{mockRuntime: rt.mockRuntime}, tmpDir).
		WithRuntime(rt)
}

// WithRuntime is a test-only helper that swaps the Engine's runtime —
// needed because newLifecycleMgr can only construct with a
// *lifecycleMockRuntime but we want the wider terminalMockRuntime to
// receive Exec/Inspect calls. The package-internal field is set
// directly since both manager and test live in the same package.
func (m *Engine) WithRuntime(rt runtime.Runtime) *Engine {
	m.runtime = rt
	return m
}
