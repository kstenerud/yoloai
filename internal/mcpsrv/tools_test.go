// ABOUTME: Input-guard coverage for all tool handlers whose guards fire before
// ABOUTME: svc access (svc may be nil for these tests).

package mcpsrv

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── handleSandboxCreate guards ────────────────────────────────────────────────

func TestHandleSandboxCreate_NameRequired(t *testing.T) {
	s := &Server{}
	req := newRunRequest(map[string]any{"workdir": "/tmp/project"})
	result, err := s.handleSandboxCreate(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

func TestHandleSandboxCreate_WorkdirRequired(t *testing.T) {
	s := &Server{}
	req := newRunRequest(map[string]any{"name": "mybox"})
	result, err := s.handleSandboxCreate(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "workdir is required"))
}

// ── handleSandboxStatus guards ────────────────────────────────────────────────

func TestHandleSandboxStatus_NameRequired(t *testing.T) {
	s := &Server{}
	result, err := s.handleSandboxStatus(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

// ── handleSandboxWait guards ──────────────────────────────────────────────────

func TestHandleSandboxWait_NameRequired(t *testing.T) {
	s := &Server{}
	result, err := s.handleSandboxWait(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

func TestHandleSandboxWait_InvalidForValue(t *testing.T) {
	// Guard fires before s.svc call — nil svc is safe.
	s := &Server{}
	req := newRunRequest(map[string]any{"name": "mybox", "for": "badvalue"})
	result, err := s.handleSandboxWait(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "invalid 'for' value"))
}

// ── handleSandboxDestroy guards ───────────────────────────────────────────────

func TestHandleSandboxDestroy_NameRequired(t *testing.T) {
	s := &Server{}
	result, err := s.handleSandboxDestroy(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

// ── handleSandboxDiff guards ──────────────────────────────────────────────────

func TestHandleSandboxDiff_NameRequired(t *testing.T) {
	s := &Server{}
	result, err := s.handleSandboxDiff(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

// ── handleSandboxDiffFile guards ──────────────────────────────────────────────

func TestHandleSandboxDiffFile_NameRequired(t *testing.T) {
	s := &Server{}
	result, err := s.handleSandboxDiffFile(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

func TestHandleSandboxDiffFile_PathRequired(t *testing.T) {
	s := &Server{}
	req := newRunRequest(map[string]any{"name": "mybox"})
	result, err := s.handleSandboxDiffFile(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "path is required"))
}

// ── handleSandboxLog guards ───────────────────────────────────────────────────

func TestHandleSandboxLog_NameRequired(t *testing.T) {
	s := &Server{}
	result, err := s.handleSandboxLog(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

// ── handleSandboxInput guards ─────────────────────────────────────────────────

func TestHandleSandboxInput_NameRequired(t *testing.T) {
	s := &Server{}
	result, err := s.handleSandboxInput(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

func TestHandleSandboxInput_TextRequired(t *testing.T) {
	s := &Server{}
	req := newRunRequest(map[string]any{"name": "mybox"})
	result, err := s.handleSandboxInput(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "text is required"))
}

// ── handleSandboxReset guards ─────────────────────────────────────────────────

func TestHandleSandboxReset_NameRequired(t *testing.T) {
	s := &Server{}
	result, err := s.handleSandboxReset(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

// ── handleSandboxFilesList guards ─────────────────────────────────────────────

func TestHandleSandboxFilesList_NameRequired(t *testing.T) {
	s := &Server{}
	result, err := s.handleSandboxFilesList(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

// ── handleSandboxFilesRead guards ─────────────────────────────────────────────

func TestHandleSandboxFilesRead_NameRequired(t *testing.T) {
	s := &Server{}
	result, err := s.handleSandboxFilesRead(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

func TestHandleSandboxFilesRead_FilenameRequired(t *testing.T) {
	s := &Server{}
	req := newRunRequest(map[string]any{"name": "mybox"})
	result, err := s.handleSandboxFilesRead(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "filename is required"))
}

// ── handleSandboxFilesWrite guards ────────────────────────────────────────────

func TestHandleSandboxFilesWrite_NameRequired(t *testing.T) {
	s := &Server{}
	result, err := s.handleSandboxFilesWrite(context.Background(), newRunRequest(nil))
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"))
}

func TestHandleSandboxFilesWrite_FilenameRequired(t *testing.T) {
	s := &Server{}
	req := newRunRequest(map[string]any{"name": "mybox", "content": "data"})
	result, err := s.handleSandboxFilesWrite(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "filename is required"))
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
