// ABOUTME: Tests that each tool-definition function returns a tool with the
// ABOUTME: correct name — covers the tool constructor boilerplate.
package mcpsrv

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSandboxToolNames(t *testing.T) {
	cases := []struct {
		name string
		tool func() string
	}{
		{"sandbox_create", func() string { return sandboxCreateTool().Name }},
		{"sandbox_run", func() string { return sandboxRunTool().Name }},
		{"sandbox_status", func() string { return sandboxStatusTool().Name }},
		{"sandbox_wait", func() string { return sandboxWaitTool().Name }},
		{"sandbox_list", func() string { return sandboxListTool().Name }},
		{"sandbox_destroy", func() string { return sandboxDestroyTool().Name }},
		{"sandbox_diff", func() string { return sandboxDiffTool().Name }},
		{"sandbox_diff_file", func() string { return sandboxDiffFileTool().Name }},
		{"sandbox_log", func() string { return sandboxLogTool().Name }},
		{"sandbox_input", func() string { return sandboxInputTool().Name }},
		{"sandbox_reset", func() string { return sandboxResetTool().Name }},
		{"sandbox_files_list", func() string { return sandboxFilesListTool().Name }},
		{"sandbox_files_read", func() string { return sandboxFilesReadTool().Name }},
		{"sandbox_files_write", func() string { return sandboxFilesWriteTool().Name }},
		{"yoloai_help", func() string { return yoloaiHelpTool().Name }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.name, tc.tool())
		})
	}
}
