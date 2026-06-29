// ABOUTME: Unit tests for validateFilename (pure function) and input-guard coverage
// ABOUTME: for all tool handlers whose guards fire before client access (client may be nil).

package mcpsrv

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── validateFilename ───────────────────────────────────────────────────────────

func TestValidateFilename(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		wantErr  bool
	}{
		// Path-traversal attacks — must be rejected.
		{name: "dot_dot", filename: "..", wantErr: true},
		{name: "absolute_path", filename: "/etc/passwd", wantErr: true},
		{name: "slash_embedded", filename: "foo/bar", wantErr: true},
		{name: "backslash_embedded", filename: `foo\bar`, wantErr: true},
		{name: "dot_dot_embedded", filename: "foo..bar", wantErr: true},
		{name: "bare_dot", filename: ".", wantErr: true},
		// Clean filenames — must be accepted.
		{name: "plain_txt", filename: "out.txt", wantErr: false},
		{name: "json_file", filename: "result.json", wantErr: false},
		{name: "no_extension", filename: "answer", wantErr: false},
		// Empty string: validateFilename does NOT reject it; the calling handler
		// rejects empty filename before reaching validateFilename.
		{name: "empty_string", filename: "", wantErr: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFilename(tc.filename)
			if tc.wantErr {
				assert.Error(t, err, "expected error for filename %q", tc.filename)
			} else {
				assert.NoError(t, err, "expected no error for filename %q", tc.filename)
			}
		})
	}
}

// ── handleSandboxCreate guards ────────────────────────────────────────────────

func TestHandleSandboxCreate_NameRequired(t *testing.T) {
	s := &Server{client: nil}
	req := newRunRequest(map[string]any{"workdir": "/tmp/project"})
	result, err := s.handleSandboxCreate(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

func TestHandleSandboxCreate_WorkdirRequired(t *testing.T) {
	s := &Server{client: nil}
	req := newRunRequest(map[string]any{"name": "mybox"})
	result, err := s.handleSandboxCreate(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "workdir is required"))
}

// ── handleSandboxStatus guards ────────────────────────────────────────────────

func TestHandleSandboxStatus_NameRequired(t *testing.T) {
	s := &Server{client: nil}
	result, err := s.handleSandboxStatus(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

// ── handleSandboxWait guards ──────────────────────────────────────────────────

func TestHandleSandboxWait_NameRequired(t *testing.T) {
	s := &Server{client: nil}
	result, err := s.handleSandboxWait(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

func TestHandleSandboxWait_InvalidForValue(t *testing.T) {
	// Guard fires before s.client.Sandbox() — nil client is safe.
	s := &Server{client: nil}
	req := newRunRequest(map[string]any{"name": "mybox", "for": "badvalue"})
	result, err := s.handleSandboxWait(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "invalid 'for' value"))
}

// ── handleSandboxDestroy guards ───────────────────────────────────────────────

func TestHandleSandboxDestroy_NameRequired(t *testing.T) {
	s := &Server{client: nil}
	result, err := s.handleSandboxDestroy(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

// ── handleSandboxDiff guards ──────────────────────────────────────────────────

func TestHandleSandboxDiff_NameRequired(t *testing.T) {
	s := &Server{client: nil}
	result, err := s.handleSandboxDiff(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

// ── handleSandboxDiffFile guards ──────────────────────────────────────────────

func TestHandleSandboxDiffFile_NameRequired(t *testing.T) {
	s := &Server{client: nil}
	result, err := s.handleSandboxDiffFile(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

func TestHandleSandboxDiffFile_PathRequired(t *testing.T) {
	s := &Server{client: nil}
	req := newRunRequest(map[string]any{"name": "mybox"})
	result, err := s.handleSandboxDiffFile(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "path is required"))
}

// ── handleSandboxLog guards ───────────────────────────────────────────────────

func TestHandleSandboxLog_NameRequired(t *testing.T) {
	s := &Server{client: nil}
	result, err := s.handleSandboxLog(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

// ── handleSandboxInput guards ─────────────────────────────────────────────────

func TestHandleSandboxInput_NameRequired(t *testing.T) {
	s := &Server{client: nil}
	result, err := s.handleSandboxInput(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

func TestHandleSandboxInput_TextRequired(t *testing.T) {
	s := &Server{client: nil}
	req := newRunRequest(map[string]any{"name": "mybox"})
	result, err := s.handleSandboxInput(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "text is required"))
}

// ── handleSandboxReset guards ─────────────────────────────────────────────────

func TestHandleSandboxReset_NameRequired(t *testing.T) {
	s := &Server{client: nil}
	result, err := s.handleSandboxReset(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

// ── handleSandboxFilesList guards ─────────────────────────────────────────────

func TestHandleSandboxFilesList_NameRequired(t *testing.T) {
	s := &Server{client: nil}
	result, err := s.handleSandboxFilesList(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

// ── handleSandboxFilesRead guards ─────────────────────────────────────────────

func TestHandleSandboxFilesRead_NameRequired(t *testing.T) {
	s := &Server{client: nil}
	result, err := s.handleSandboxFilesRead(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

func TestHandleSandboxFilesRead_FilenameRequired(t *testing.T) {
	s := &Server{client: nil}
	req := newRunRequest(map[string]any{"name": "mybox"})
	result, err := s.handleSandboxFilesRead(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "filename is required"))
}

func TestHandleSandboxFilesRead_TraversalRejected(t *testing.T) {
	s := &Server{client: nil}
	req := newRunRequest(map[string]any{"name": "mybox", "filename": "../../etc/passwd"})
	result, err := s.handleSandboxFilesRead(context.Background(), req)
	require.NoError(t, err)
	text := resultText(t, result)
	assert.True(t, strings.HasPrefix(text, "[ERROR]"), "expected [ERROR] prefix for traversal filename, got: %s", text)
}

// ── handleSandboxFilesWrite guards ────────────────────────────────────────────

func TestHandleSandboxFilesWrite_NameRequired(t *testing.T) {
	s := &Server{client: nil}
	result, err := s.handleSandboxFilesWrite(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

func TestHandleSandboxFilesWrite_FilenameRequired(t *testing.T) {
	s := &Server{client: nil}
	req := newRunRequest(map[string]any{"name": "mybox", "content": "data"})
	result, err := s.handleSandboxFilesWrite(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "filename is required"))
}

func TestHandleSandboxFilesWrite_TraversalRejected(t *testing.T) {
	s := &Server{client: nil}
	req := newRunRequest(map[string]any{"name": "mybox", "filename": "../escape", "content": "x"})
	result, err := s.handleSandboxFilesWrite(context.Background(), req)
	require.NoError(t, err)
	text := resultText(t, result)
	assert.True(t, strings.HasPrefix(text, "[ERROR]"), "expected [ERROR] prefix for traversal filename, got: %s", text)
}

// ── handleYoloaiHelp ──────────────────────────────────────────────────────────

func TestHandleYoloaiHelp_ReturnsHelpText(t *testing.T) {
	result, err := handleYoloaiHelp(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	text := resultText(t, result)
	assert.NotEmpty(t, text)
	// The help text must describe the core workflow so the outer agent can orient itself.
	assert.True(t, strings.Contains(text, "sandbox_create"), "expected sandbox_create in help text")
	assert.True(t, strings.Contains(text, "sandbox_diff"), "expected sandbox_diff in help text")
}
