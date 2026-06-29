// ABOUTME: Happy-path and key error-branch tests for MCP tool handlers using
// ABOUTME: fakeService — no live backend required.
package mcpsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── sandbox_create ────────────────────────────────────────────────────────────

func TestHandleSandboxCreate_Success(t *testing.T) {
	var ensureCalled, createCalled, startCalled bool
	var capturedOpts yoloai.SandboxCreateOptions

	svc := &fakeService{
		EnsureSetupFn: func(_ context.Context) error {
			ensureCalled = true
			return nil
		},
		CreateSandboxFn: func(_ context.Context, opts yoloai.SandboxCreateOptions) error {
			createCalled = true
			capturedOpts = opts
			return nil
		},
		StartFn: func(_ context.Context, name string) error {
			startCalled = true
			assert.Equal(t, "mybox", name)
			return nil
		},
	}
	s := &Server{svc: svc}
	req := newRunRequest(map[string]any{
		"name":    "mybox",
		"workdir": "/tmp/project",
		"prompt":  "add tests",
	})

	result, err := s.handleSandboxCreate(context.Background(), req)
	require.NoError(t, err)

	assert.True(t, ensureCalled, "EnsureSetup must be called")
	assert.True(t, createCalled, "CreateSandbox must be called")
	assert.True(t, startCalled, "Start must be called")
	assert.Equal(t, "mybox", capturedOpts.Name)
	assert.Equal(t, "/tmp/project", capturedOpts.Workdir.Path)
	assert.Equal(t, "add tests", capturedOpts.Prompt)
	assert.False(t, capturedOpts.Headless)

	text := resultText(t, result)
	assert.Contains(t, text, "mybox")
	assert.NotContains(t, text, "[ERROR]")
}

// ── sandbox_run ───────────────────────────────────────────────────────────────

func TestHandleSandboxRun_Success(t *testing.T) {
	var capturedOpts yoloai.SandboxCreateOptions

	svc := &fakeService{
		CreateSandboxFn: func(_ context.Context, opts yoloai.SandboxCreateOptions) error {
			capturedOpts = opts
			return nil
		},
	}
	s := &Server{svc: svc}
	req := newRunRequest(map[string]any{
		"name":    "runbox",
		"workdir": "/tmp/project",
		"prompt":  "fix bug",
	})

	result, err := s.handleSandboxRun(context.Background(), req)
	require.NoError(t, err)

	assert.True(t, capturedOpts.Headless, "sandbox_run must set Headless=true")
	assert.Equal(t, "runbox", capturedOpts.Name)
	assert.Equal(t, "fix bug", capturedOpts.Prompt)

	text := resultText(t, result)
	assert.Contains(t, text, "runbox")
	assert.NotContains(t, text, "[ERROR]")
}

// ── sandbox_status ────────────────────────────────────────────────────────────

func TestHandleSandboxStatus_Success(t *testing.T) {
	svc := &fakeService{
		InspectFn: func(_ context.Context, name string) (*yoloai.SandboxInfo, error) {
			return &yoloai.SandboxInfo{
				Environment: &yoloai.Environment{Name: name},
				Status:      yoloai.StatusIdle,
				AgentStatus: yoloai.AgentStatusIdle,
			}, nil
		},
	}
	s := &Server{svc: svc}
	req := newRunRequest(map[string]any{"name": "mybox"})

	result, err := s.handleSandboxStatus(context.Background(), req)
	require.NoError(t, err)

	text := resultText(t, result)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &out))
	assert.Equal(t, "mybox", out["name"])
	assert.Equal(t, string(yoloai.StatusIdle), out["status"])
}

// ── sandbox_wait ──────────────────────────────────────────────────────────────

func TestHandleSandboxWait_ConditionMet(t *testing.T) {
	var capturedOpts yoloai.SandboxWaitOptions
	svc := &fakeService{
		WaitFn: func(_ context.Context, name string, opts yoloai.SandboxWaitOptions) (*yoloai.SandboxInfo, error) {
			capturedOpts = opts
			return &yoloai.SandboxInfo{
				Environment: &yoloai.Environment{Name: name},
				Status:      yoloai.StatusDone,
			}, nil
		},
	}
	s := &Server{svc: svc}
	req := newRunRequest(map[string]any{
		"name":            "mybox",
		"for":             "exit",
		"timeout_seconds": float64(30),
	})

	result, err := s.handleSandboxWait(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, yoloai.WaitForExit, capturedOpts.For)
	assert.NotZero(t, capturedOpts.Timeout, "timeout must be passed through")

	text := resultText(t, result)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &out))
	assert.Equal(t, "mybox", out["name"])
}

func TestHandleSandboxWait_Timeout(t *testing.T) {
	svc := &fakeService{
		WaitFn: func(_ context.Context, name string, opts yoloai.SandboxWaitOptions) (*yoloai.SandboxInfo, error) {
			info := &yoloai.SandboxInfo{
				Environment: &yoloai.Environment{Name: name},
				Status:      yoloai.StatusActive,
			}
			return info, yoloai.ErrWaitTimeout
		},
	}
	s := &Server{svc: svc}
	req := newRunRequest(map[string]any{"name": "mybox"})

	result, err := s.handleSandboxWait(context.Background(), req)
	require.NoError(t, err)

	text := resultText(t, result)
	assert.True(t, strings.HasPrefix(text, "[ERROR]"), "timeout must produce [ERROR] prefix")
	assert.Contains(t, text, "wait timed out")
	assert.Contains(t, text, "mybox")
}

// ── sandbox_list ──────────────────────────────────────────────────────────────

func TestHandleSandboxList_Empty(t *testing.T) {
	svc := &fakeService{
		ListSandboxesFn: func(_ context.Context) ([]*yoloai.SandboxInfo, error) {
			return nil, nil
		},
	}
	s := &Server{svc: svc}

	result, err := s.handleSandboxList(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.Equal(t, "No sandboxes found.", resultText(t, result))
}

func TestHandleSandboxList_NonEmpty(t *testing.T) {
	svc := &fakeService{
		ListSandboxesFn: func(_ context.Context) ([]*yoloai.SandboxInfo, error) {
			return []*yoloai.SandboxInfo{
				{
					Environment: &yoloai.Environment{Name: "box1"},
					Status:      yoloai.StatusActive,
				},
				{
					Environment: &yoloai.Environment{Name: "box2"},
					Status:      yoloai.StatusDone,
				},
			}, nil
		},
	}
	s := &Server{svc: svc}

	result, err := s.handleSandboxList(context.Background(), newRunRequest(nil))
	require.NoError(t, err)

	text := resultText(t, result)
	var entries []map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &entries))
	require.Len(t, entries, 2)
	assert.Equal(t, "box1", entries[0]["name"])
	assert.Equal(t, "box2", entries[1]["name"])
}

// ── sandbox_destroy ───────────────────────────────────────────────────────────

func TestHandleSandboxDestroy_ForceFalse_HasActiveWork_Refused(t *testing.T) {
	var destroyCalled bool
	svc := &fakeService{
		HasActiveWorkFn: func(_ context.Context, name string) (bool, string, error) {
			return true, "uncommitted changes", nil
		},
		DestroyFn: func(_ context.Context, name string, opts yoloai.SandboxDestroyOptions) error {
			destroyCalled = true
			return nil
		},
	}
	s := &Server{svc: svc}
	req := newRunRequest(map[string]any{"name": "mybox", "force": false})

	result, err := s.handleSandboxDestroy(context.Background(), req)
	require.NoError(t, err)

	assert.False(t, destroyCalled, "Destroy must NOT be called when active+force=false")
	text := resultText(t, result)
	assert.True(t, strings.HasPrefix(text, "[ERROR]"), "refusal must produce [ERROR] prefix")
	assert.Contains(t, text, "unapplied changes")
}

func TestHandleSandboxDestroy_ForceTrue_DestroysCalled(t *testing.T) {
	var destroyCalled bool
	var capturedOpts yoloai.SandboxDestroyOptions
	svc := &fakeService{
		DestroyFn: func(_ context.Context, name string, opts yoloai.SandboxDestroyOptions) error {
			destroyCalled = true
			capturedOpts = opts
			return nil
		},
	}
	s := &Server{svc: svc}
	req := newRunRequest(map[string]any{"name": "mybox", "force": true})

	result, err := s.handleSandboxDestroy(context.Background(), req)
	require.NoError(t, err)

	assert.True(t, destroyCalled, "Destroy must be called with force=true")
	assert.True(t, capturedOpts.AbandonUnappliedWork, "AbandonUnappliedWork must be set")
	text := resultText(t, result)
	assert.Contains(t, text, "mybox")
	assert.NotContains(t, text, "[ERROR]")
}

// ── sandbox_diff ──────────────────────────────────────────────────────────────

func TestHandleSandboxDiff_Empty(t *testing.T) {
	svc := &fakeService{
		DiffFn: func(_ context.Context, name string, opts yoloai.WorkdirDiffOptions) (string, error) {
			return "", nil
		},
	}
	s := &Server{svc: svc}
	req := newRunRequest(map[string]any{"name": "mybox"})

	result, err := s.handleSandboxDiff(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "No changes to diff", resultText(t, result))
}

func TestHandleSandboxDiff_NonEmpty(t *testing.T) {
	const fakeDiff = "--- a/foo.go\n+++ b/foo.go\n@@ -1 +1 @@\n-old\n+new\n"
	svc := &fakeService{
		DiffFn: func(_ context.Context, name string, opts yoloai.WorkdirDiffOptions) (string, error) {
			return fakeDiff, nil
		},
	}
	s := &Server{svc: svc}
	req := newRunRequest(map[string]any{"name": "mybox"})

	result, err := s.handleSandboxDiff(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, fakeDiff, resultText(t, result))
}

// ── sandbox_diff_file ─────────────────────────────────────────────────────────

func TestHandleSandboxDiffFile_Empty(t *testing.T) {
	svc := &fakeService{
		DiffFn: func(_ context.Context, name string, opts yoloai.WorkdirDiffOptions) (string, error) {
			return "", nil
		},
	}
	s := &Server{svc: svc}
	req := newRunRequest(map[string]any{"name": "mybox", "path": "foo/bar.go"})

	result, err := s.handleSandboxDiffFile(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "No changes in foo/bar.go", resultText(t, result))
}

// ── sandbox_log ───────────────────────────────────────────────────────────────

func TestHandleSandboxLog_LinesPassthrough(t *testing.T) {
	var capturedLines int
	svc := &fakeService{
		TerminalLogFn: func(_ context.Context, name string, lines int) (string, error) {
			capturedLines = lines
			return "line1\nline2\n", nil
		},
	}
	s := &Server{svc: svc}
	req := newRunRequest(map[string]any{"name": "mybox", "lines": float64(50)})

	result, err := s.handleSandboxLog(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, 50, capturedLines)
	assert.Equal(t, "line1\nline2\n", resultText(t, result))
}

func TestHandleSandboxLog_DefaultLines(t *testing.T) {
	var capturedLines int
	svc := &fakeService{
		TerminalLogFn: func(_ context.Context, name string, lines int) (string, error) {
			capturedLines = lines
			return "", nil
		},
	}
	s := &Server{svc: svc}
	// No "lines" arg → default 100
	req := newRunRequest(map[string]any{"name": "mybox"})

	result, err := s.handleSandboxLog(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, 100, capturedLines)
	assert.Equal(t, "(no log output)", resultText(t, result))
}

// ── sandbox_input ─────────────────────────────────────────────────────────────

func TestHandleSandboxInput_Success(t *testing.T) {
	var capturedName, capturedText string
	svc := &fakeService{
		SendInputFn: func(_ context.Context, name, text string) error {
			capturedName = name
			capturedText = text
			return nil
		},
	}
	s := &Server{svc: svc}
	req := newRunRequest(map[string]any{"name": "mybox", "text": "hello agent"})

	result, err := s.handleSandboxInput(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "mybox", capturedName)
	assert.Equal(t, "hello agent", capturedText)
	text := resultText(t, result)
	assert.Contains(t, text, "mybox")
	assert.NotContains(t, text, "[ERROR]")
}

// ── sandbox_reset ─────────────────────────────────────────────────────────────

func TestHandleSandboxReset_Success(t *testing.T) {
	var capturedOpts yoloai.SandboxResetOptions
	svc := &fakeService{
		ResetFn: func(_ context.Context, name string, opts yoloai.SandboxResetOptions) error {
			capturedOpts = opts
			return nil
		},
	}
	s := &Server{svc: svc}
	req := newRunRequest(map[string]any{"name": "mybox", "prompt": "try again"})

	result, err := s.handleSandboxReset(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, capturedOpts.RestartContainer)
	assert.Equal(t, "try again", capturedOpts.Prompt)
	text := resultText(t, result)
	assert.Contains(t, text, "mybox")
	assert.NotContains(t, text, "[ERROR]")
}

// ── sandbox_files_list ────────────────────────────────────────────────────────

func TestHandleSandboxFilesList_Empty(t *testing.T) {
	svc := &fakeService{
		ListFilesFn: func(_ context.Context, name string) ([]string, error) {
			return nil, nil
		},
	}
	s := &Server{svc: svc}
	req := newRunRequest(map[string]any{"name": "mybox"})

	result, err := s.handleSandboxFilesList(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "(no files)", resultText(t, result))
}

func TestHandleSandboxFilesList_NonEmpty(t *testing.T) {
	svc := &fakeService{
		ListFilesFn: func(_ context.Context, name string) ([]string, error) {
			return []string{"question.json", "answer.json"}, nil
		},
	}
	s := &Server{svc: svc}
	req := newRunRequest(map[string]any{"name": "mybox"})

	result, err := s.handleSandboxFilesList(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "question.json\nanswer.json", resultText(t, result))
}

// ── sandbox_files_read ────────────────────────────────────────────────────────

func TestHandleSandboxFilesRead_Success(t *testing.T) {
	svc := &fakeService{
		ReadFileFn: func(_ context.Context, name, rel string) ([]byte, error) {
			assert.Equal(t, "mybox", name)
			assert.Equal(t, "question.json", rel)
			return []byte(`{"question":"what color?"}`), nil
		},
	}
	s := &Server{svc: svc}
	req := newRunRequest(map[string]any{"name": "mybox", "filename": "question.json"})

	result, err := s.handleSandboxFilesRead(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, `{"question":"what color?"}`, resultText(t, result))
}

// ── sandbox_files_write ───────────────────────────────────────────────────────

func TestHandleSandboxFilesWrite_Success(t *testing.T) {
	var capturedRel string
	var capturedData []byte
	svc := &fakeService{
		WriteFileFn: func(_ context.Context, name, rel string, data []byte) error {
			capturedRel = rel
			capturedData = data
			return nil
		},
	}
	s := &Server{svc: svc}
	req := newRunRequest(map[string]any{
		"name":     "mybox",
		"filename": "answer.json",
		"content":  `{"answer":"blue"}`,
	})

	result, err := s.handleSandboxFilesWrite(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "answer.json", capturedRel)
	assert.Equal(t, `{"answer":"blue"}`, string(capturedData))
	text := resultText(t, result)
	assert.Contains(t, text, "Written")
	assert.Contains(t, text, "answer.json")
}

// ── error branches ────────────────────────────────────────────────────────────
// These cover the svc-error paths that happy-path tests don't reach.

func TestHandleSandboxStatus_InspectError(t *testing.T) {
	s := &Server{svc: &fakeService{
		InspectFn: func(_ context.Context, name string) (*yoloai.SandboxInfo, error) {
			return nil, fmt.Errorf("backend down")
		},
	}}
	result, err := s.handleSandboxStatus(context.Background(), newRunRequest(map[string]any{"name": "mybox"}))
	require.NoError(t, err)
	assert.Contains(t, resultText(t, result), "[ERROR]")
}

func TestHandleSandboxWait_OtherError(t *testing.T) {
	s := &Server{svc: &fakeService{
		WaitFn: func(_ context.Context, name string, opts yoloai.SandboxWaitOptions) (*yoloai.SandboxInfo, error) {
			return nil, fmt.Errorf("wait failed")
		},
	}}
	result, err := s.handleSandboxWait(context.Background(), newRunRequest(map[string]any{"name": "mybox"}))
	require.NoError(t, err)
	assert.Contains(t, resultText(t, result), "[ERROR]")
}

func TestHandleSandboxList_Error(t *testing.T) {
	s := &Server{svc: &fakeService{
		ListSandboxesFn: func(_ context.Context) ([]*yoloai.SandboxInfo, error) {
			return nil, fmt.Errorf("list failed")
		},
	}}
	result, err := s.handleSandboxList(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.Contains(t, resultText(t, result), "[ERROR]")
}

func TestHandleSandboxDestroy_DestroyError(t *testing.T) {
	s := &Server{svc: &fakeService{
		DestroyFn: func(_ context.Context, name string, opts yoloai.SandboxDestroyOptions) error {
			return fmt.Errorf("destroy failed")
		},
	}}
	result, err := s.handleSandboxDestroy(context.Background(), newRunRequest(map[string]any{"name": "mybox", "force": true}))
	require.NoError(t, err)
	assert.Contains(t, resultText(t, result), "[ERROR]")
}

func TestHandleSandboxLog_Error(t *testing.T) {
	s := &Server{svc: &fakeService{
		TerminalLogFn: func(_ context.Context, name string, lines int) (string, error) {
			return "", fmt.Errorf("log unavailable")
		},
	}}
	result, err := s.handleSandboxLog(context.Background(), newRunRequest(map[string]any{"name": "mybox"}))
	require.NoError(t, err)
	assert.Contains(t, resultText(t, result), "[ERROR]")
}

func TestHandleSandboxReset_Error(t *testing.T) {
	s := &Server{svc: &fakeService{
		ResetFn: func(_ context.Context, name string, opts yoloai.SandboxResetOptions) error {
			return fmt.Errorf("reset failed")
		},
	}}
	result, err := s.handleSandboxReset(context.Background(), newRunRequest(map[string]any{"name": "mybox"}))
	require.NoError(t, err)
	assert.Contains(t, resultText(t, result), "[ERROR]")
}

func TestHandleSandboxInput_Error(t *testing.T) {
	s := &Server{svc: &fakeService{
		SendInputFn: func(_ context.Context, name, text string) error {
			return fmt.Errorf("tmux not running")
		},
	}}
	result, err := s.handleSandboxInput(context.Background(), newRunRequest(map[string]any{"name": "mybox", "text": "hi"}))
	require.NoError(t, err)
	assert.Contains(t, resultText(t, result), "[ERROR]")
}

func TestHandleSandboxDiffFile_Error(t *testing.T) {
	s := &Server{svc: &fakeService{
		DiffFn: func(_ context.Context, name string, opts yoloai.WorkdirDiffOptions) (string, error) {
			return "", fmt.Errorf("diff error")
		},
	}}
	result, err := s.handleSandboxDiffFile(context.Background(), newRunRequest(map[string]any{"name": "mybox", "path": "foo.go"}))
	require.NoError(t, err)
	assert.Contains(t, resultText(t, result), "[ERROR]")
}

func TestHandleSandboxFilesList_Error(t *testing.T) {
	s := &Server{svc: &fakeService{
		ListFilesFn: func(_ context.Context, name string) ([]string, error) {
			return nil, fmt.Errorf("permission denied")
		},
	}}
	result, err := s.handleSandboxFilesList(context.Background(), newRunRequest(map[string]any{"name": "mybox"}))
	require.NoError(t, err)
	assert.Contains(t, resultText(t, result), "[ERROR]")
}

func TestHandleSandboxFilesRead_Error(t *testing.T) {
	s := &Server{svc: &fakeService{
		ReadFileFn: func(_ context.Context, name, rel string) ([]byte, error) {
			return nil, fmt.Errorf("file not found")
		},
	}}
	result, err := s.handleSandboxFilesRead(context.Background(), newRunRequest(map[string]any{"name": "mybox", "filename": "missing.json"}))
	require.NoError(t, err)
	assert.Contains(t, resultText(t, result), "[ERROR]")
}

func TestHandleSandboxCreate_CreateError(t *testing.T) {
	s := &Server{svc: &fakeService{
		CreateSandboxFn: func(_ context.Context, opts yoloai.SandboxCreateOptions) error {
			return fmt.Errorf("image pull failed")
		},
	}}
	req := newRunRequest(map[string]any{"name": "mybox", "workdir": "/tmp/work"})
	result, err := s.handleSandboxCreate(context.Background(), req)
	require.NoError(t, err)
	assert.Contains(t, resultText(t, result), "[ERROR]")
}

func TestHandleSandboxRun_CreateError(t *testing.T) {
	s := &Server{svc: &fakeService{
		CreateSandboxFn: func(_ context.Context, opts yoloai.SandboxCreateOptions) error {
			return fmt.Errorf("quota exceeded")
		},
	}}
	req := newRunRequest(map[string]any{"name": "mybox", "workdir": "/tmp/work", "prompt": "do it"})
	result, err := s.handleSandboxRun(context.Background(), req)
	require.NoError(t, err)
	assert.Contains(t, resultText(t, result), "[ERROR]")
}

func TestHandleSandboxDestroy_HasActiveWorkError(t *testing.T) {
	s := &Server{svc: &fakeService{
		HasActiveWorkFn: func(_ context.Context, name string) (bool, string, error) {
			return false, "", fmt.Errorf("sandbox unreachable")
		},
	}}
	req := newRunRequest(map[string]any{"name": "mybox", "force": false})
	result, err := s.handleSandboxDestroy(context.Background(), req)
	require.NoError(t, err)
	assert.Contains(t, resultText(t, result), "[ERROR]")
}

func TestHandleSandboxDiffFile_NonEmpty(t *testing.T) {
	const patch = "--- a/foo.go\n+++ b/foo.go\n"
	s := &Server{svc: &fakeService{
		DiffFn: func(_ context.Context, name string, opts yoloai.WorkdirDiffOptions) (string, error) {
			return patch, nil
		},
	}}
	req := newRunRequest(map[string]any{"name": "mybox", "path": "foo.go"})
	result, err := s.handleSandboxDiffFile(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, patch, resultText(t, result))
}

func TestHandleSandboxLog_ZeroLines_DefaultsTo100(t *testing.T) {
	var capturedLines int
	s := &Server{svc: &fakeService{
		TerminalLogFn: func(_ context.Context, name string, lines int) (string, error) {
			capturedLines = lines
			return "", nil
		},
	}}
	req := newRunRequest(map[string]any{"name": "mybox", "lines": float64(0)})
	result, err := s.handleSandboxLog(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, 100, capturedLines, "lines=0 must default to 100")
	assert.Equal(t, "(no log output)", resultText(t, result))
}

func TestHandleSandboxFilesWrite_Error(t *testing.T) {
	s := &Server{svc: &fakeService{
		WriteFileFn: func(_ context.Context, name, rel string, data []byte) error {
			return fmt.Errorf("disk full")
		},
	}}
	result, err := s.handleSandboxFilesWrite(context.Background(), newRunRequest(map[string]any{
		"name":     "mybox",
		"filename": "answer.json",
		"content":  "x",
	}))
	require.NoError(t, err)
	assert.Contains(t, resultText(t, result), "[ERROR]")
}
