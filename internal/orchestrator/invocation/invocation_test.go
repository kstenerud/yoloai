package invocation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/agent"
)

func TestResolveModel_Alias(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	result := ResolveModel(agentDef, "sonnet", nil)
	assert.Equal(t, "claude-sonnet-4-6", result)
}

func TestResolveModel_FullName(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	result := ResolveModel(agentDef, "claude-sonnet-4-5-20250929", nil)
	assert.Equal(t, "claude-sonnet-4-5-20250929", result)
}

func TestResolveModel_Empty(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	result := ResolveModel(agentDef, "", nil)
	assert.Equal(t, "", result)
}

func TestResolveModel_UserAliasOverridesBuiltin(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	userAliases := map[string]string{"sonnet": "claude-sonnet-4-20250514"}
	result := ResolveModel(agentDef, "sonnet", userAliases)
	assert.Equal(t, "claude-sonnet-4-20250514", result)
}

func TestResolveModel_UserAliasCustomKey(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	userAliases := map[string]string{"fast": "claude-haiku-4-latest"}
	result := ResolveModel(agentDef, "fast", userAliases)
	assert.Equal(t, "claude-haiku-4-latest", result)
}

func TestResolveModel_NilUserAliasesFallsBack(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	result := ResolveModel(agentDef, "sonnet", nil)
	assert.Equal(t, "claude-sonnet-4-6", result)
}

func TestBuildAgentCommand_InteractiveWithModel(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	result := BuildAgentCommand(agentDef, "claude-opus-4-latest", "", "", nil)
	assert.Equal(t, "claude --dangerously-skip-permissions --model claude-opus-4-latest", result)
}

func TestBuildAgentCommand_InteractiveWithPassthrough(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	result := BuildAgentCommand(agentDef, "claude-sonnet-4-latest", "", "", []string{"--max-turns", "5"})
	assert.Equal(t, "claude --dangerously-skip-permissions --model claude-sonnet-4-latest --max-turns 5", result)
}

func TestBuildAgentCommand_HeadlessWithPrompt(t *testing.T) {
	agentDef := agent.GetAgent("test")
	result := BuildAgentCommand(agentDef, "", "echo hello", "", nil)
	assert.Equal(t, `sh -c "echo hello"`, result)
}

func TestBuildAgentCommand_InteractiveFallback(t *testing.T) {
	agentDef := agent.GetAgent("test")
	result := BuildAgentCommand(agentDef, "", "", "", nil)
	assert.Equal(t, "bash", result)
}

func TestBuildAgentCommand_WithAgentArgs(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	result := BuildAgentCommand(agentDef, "claude-sonnet-4-latest", "", "--allowedTools '*'", []string{"--max-turns", "5"})
	assert.Equal(t, "claude --dangerously-skip-permissions --model claude-sonnet-4-latest --allowedTools '*' --max-turns 5", result)
}

func TestBuildAgentCommand_AgentArgsOnly(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	result := BuildAgentCommand(agentDef, "", "", "--verbose", nil)
	assert.Equal(t, "claude --dangerously-skip-permissions --verbose", result)
}

func TestReadPrompt_DirectText(t *testing.T) {
	result, err := ReadPrompt("hello", "", "/home/user", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "hello", result)
}

func TestReadPrompt_File(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "prompt.txt")
	require.NoError(t, os.WriteFile(path, []byte("prompt from file\n"), 0600))

	result, err := ReadPrompt("", path, "/home/user", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "prompt from file", result)
}

func TestReadPrompt_MutualExclusion(t *testing.T) {
	_, err := ReadPrompt("hello", "/some/file", "/home/user", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestReadPrompt_StdinDash(t *testing.T) {
	// stdin is now an explicit reader threaded from the Engine's input,
	// so the test supplies one directly instead of swapping os.Stdin.
	result, err := ReadPrompt("-", "", "/home/user", nil, strings.NewReader("hello from stdin\n"))
	require.NoError(t, err)
	assert.Equal(t, "hello from stdin", result)
}

func TestResolveDetectors_HookAgent(t *testing.T) {
	idle := agent.IdleSupport{
		Hook:            true,
		ReadyPattern:    "❯",
		ContextSignal:   true,
		WchanApplicable: true,
	}
	detectors := ResolveDetectors(idle)
	assert.Equal(t, []string{"hook", "wchan", "ready_pattern", "context_signal", "output_stability"}, detectors)
}

func TestResolveDetectors_NonHookAgent(t *testing.T) {
	idle := agent.IdleSupport{
		ReadyPattern:    "> $",
		ContextSignal:   true,
		WchanApplicable: true,
	}
	detectors := ResolveDetectors(idle)
	assert.Equal(t, []string{"wchan", "ready_pattern", "context_signal", "output_stability"}, detectors)
}

func TestResolveDetectors_WchanOnly(t *testing.T) {
	idle := agent.IdleSupport{
		WchanApplicable: true,
	}
	detectors := ResolveDetectors(idle)
	assert.Equal(t, []string{"wchan", "output_stability"}, detectors)
}

func TestResolveDetectors_NoCapabilities(t *testing.T) {
	idle := agent.IdleSupport{}
	detectors := ResolveDetectors(idle)
	assert.Nil(t, detectors)
}

func TestResolveFallToShell_HookAgentEnabled(t *testing.T) {
	// Hook-authoritative agents launch under the fall-to-shell wrapper: the
	// monitor runs no heuristics while the pane lives, so a wrapper-written
	// `done` survives untouched (D96 Phase 1).
	assert.True(t, ResolveFallToShell(agent.IdleSupport{Hook: true}))
}

func TestResolveFallToShell_HeuristicAgentDisabled(t *testing.T) {
	// Heuristic agents stay on the exec-the-agent path until the runner honors a
	// wrapper-written `done` (Phase 3) — otherwise the monitor would read the
	// idle fall-to-shell shell as `idle` and clobber `done`.
	assert.False(t, ResolveFallToShell(agent.IdleSupport{ReadyPattern: "> $", WchanApplicable: true}))
}

func TestValidateModel_OpenCodeWithProviderPrefix(t *testing.T) {
	agentDef := agent.GetAgent("opencode")
	err := ValidateModel(agentDef, "openai/gpt-4o", "openai/gpt-4o")
	assert.NoError(t, err)
}

func TestValidateModel_OpenCodeWithoutProviderPrefix(t *testing.T) {
	agentDef := agent.GetAgent("opencode")
	err := ValidateModel(agentDef, "gpt-4o", "gpt-4o")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "provider/model format")
	assert.Contains(t, err.Error(), "openai/gpt-4o")
}

func TestValidateModel_OpenCodeResolvedWithoutPrefix(t *testing.T) {
	agentDef := agent.GetAgent("opencode")
	err := ValidateModel(agentDef, "claude-sonnet-4-5", "sonnet")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "You specified: \"sonnet\"")
	assert.Contains(t, err.Error(), "Resolved to: \"claude-sonnet-4-5\"")
}

func TestValidateModel_OpenCodeEmptyModel(t *testing.T) {
	agentDef := agent.GetAgent("opencode")
	err := ValidateModel(agentDef, "", "")
	assert.NoError(t, err)
}

func TestValidateModel_OtherAgentNoValidation(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	err := ValidateModel(agentDef, "claude-sonnet-4-6", "sonnet")
	assert.NoError(t, err)
}

func TestValidateModel_AiderNoValidation(t *testing.T) {
	agentDef := agent.GetAgent("aider")
	err := ValidateModel(agentDef, "sonnet", "sonnet")
	assert.NoError(t, err)
}

func TestShellEscapeForDoubleQuotes(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`hello`, `hello`},
		{`say "hello"`, `say \"hello\"`},
		{"use `backticks`", "use \\`backticks\\`"},
		{`$HOME`, `\$HOME`},
		{`back\slash`, `back\\slash`},
		{`all "special" $chars` + " `here`", `all \"special\" \$chars` + " \\`here\\`"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, shellEscapeForDoubleQuotes(tt.input))
	}
}
