// ABOUTME: Unit tests for handleSandboxRun's input-validation guards — they
// ABOUTME: short-circuit before touching svc, so svc may be nil.

package mcpsrv

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newRunRequest builds a CallToolRequest for sandbox_run with the given
// arguments, mirroring how the mcp-go library populates the Params.Arguments.
func newRunRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: args,
		},
	}
}

// resultText extracts the text from the first content item, matching
// the shape textResult produces (mcp.NewToolResultText → single TextContent).
func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.NotNil(t, result)
	require.NotEmpty(t, result.Content)
	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok, "expected TextContent, got %T", result.Content[0])
	return tc.Text
}

func TestHandleSandboxRun_NameRequired(t *testing.T) {
	// Guard returns before calling s.createAndStart — nil svc is safe.
	s := &Server{}
	req := newRunRequest(map[string]any{
		"workdir": "/some/dir",
		"prompt":  "do something",
	})
	result, err := s.handleSandboxRun(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "name is required"), "expected 'name is required' in response")
}

func TestHandleSandboxRun_WorkdirRequired(t *testing.T) {
	s := &Server{}
	req := newRunRequest(map[string]any{
		"name":   "mybox",
		"prompt": "do something",
	})
	result, err := s.handleSandboxRun(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "workdir is required"), "expected 'workdir is required' in response")
}

func TestHandleSandboxRun_PromptRequired(t *testing.T) {
	s := &Server{}
	req := newRunRequest(map[string]any{
		"name":    "mybox",
		"workdir": "/some/dir",
	})
	result, err := s.handleSandboxRun(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, strings.Contains(resultText(t, result), "prompt is required for sandbox_run"), "expected 'prompt is required' in response")
}
