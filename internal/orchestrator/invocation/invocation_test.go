// ABOUTME: Agent-launch invocation building: model alias resolution, shell
// ABOUTME: command assembly (interactive/headless/resume), prompt sourcing,
// ABOUTME: idle-detector selection, and fall-to-shell wrapper eligibility.
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
	result := BuildAgentCommand(agentDef, "claude-opus-4-latest", "", "", nil, false)
	assert.Equal(t, "claude --dangerously-skip-permissions --model claude-opus-4-latest", result)
}

func TestBuildAgentCommand_InteractiveWithPassthrough(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	result := BuildAgentCommand(agentDef, "claude-sonnet-4-latest", "", "", []string{"--max-turns", "5"}, false)
	assert.Equal(t, "claude --dangerously-skip-permissions --model claude-sonnet-4-latest --max-turns 5", result)
}

func TestBuildAgentCommand_HeadlessWithPrompt(t *testing.T) {
	agentDef := agent.GetAgent("test")
	result := BuildAgentCommand(agentDef, "", "echo hello", "", nil, false)
	assert.Equal(t, `sh -c "echo hello"`, result)
}

func TestBuildAgentCommand_InteractiveFallback(t *testing.T) {
	agentDef := agent.GetAgent("test")
	result := BuildAgentCommand(agentDef, "", "", "", nil, false)
	assert.Equal(t, "bash", result)
}

func TestBuildAgentCommand_WithAgentArgs(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	result := BuildAgentCommand(agentDef, "claude-sonnet-4-latest", "", "--allowedTools '*'", []string{"--max-turns", "5"}, false)
	assert.Equal(t, "claude --dangerously-skip-permissions --model claude-sonnet-4-latest --allowedTools '*' --max-turns 5", result)
}

func TestBuildAgentCommand_AgentArgsOnly(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	result := BuildAgentCommand(agentDef, "", "", "--verbose", nil, false)
	assert.Equal(t, "claude --dangerously-skip-permissions --verbose", result)
}

func TestBuildAgentCommand_HeadlessForcesHeadlessCmd(t *testing.T) {
	// `yoloai run` forces headless on an agent that defaults to interactive:
	// the prompt is baked into the launch command (HeadlessCmd), not injected.
	agentDef := agent.GetAgent("claude")
	result := BuildAgentCommand(agentDef, "", "do the thing", "", nil, true)
	assert.Equal(t, `claude -p "do the thing" --dangerously-skip-permissions`, result)
}

func TestBuildAgentCommand_HeadlessHonorsModel(t *testing.T) {
	// A forced-headless run still applies --model (e.g. `claude -p "…" --model opus`).
	agentDef := agent.GetAgent("claude")
	result := BuildAgentCommand(agentDef, "claude-opus-4-latest", "do the thing", "", nil, true)
	assert.Equal(t, `claude -p "do the thing" --dangerously-skip-permissions --model claude-opus-4-latest`, result)
}

func TestBuildAgentCommand_HeadlessEscapesPrompt(t *testing.T) {
	// The prompt is embedded in a double-quoted shell argument, so quotes and
	// expansions must be escaped.
	agentDef := agent.GetAgent("claude")
	result := BuildAgentCommand(agentDef, "", `say "hi" $USER`, "", nil, true)
	assert.Equal(t, `claude -p "say \"hi\" \$USER" --dangerously-skip-permissions`, result)
}

func TestBuildAgentCommand_HeadlessNoPromptFallsToInteractive(t *testing.T) {
	// Headless is only taken when there is a prompt to bake in; with none it
	// falls back to the interactive command (create rejects this combination
	// upstream, but the builder is defensive).
	agentDef := agent.GetAgent("claude")
	result := BuildAgentCommand(agentDef, "", "", "", nil, true)
	assert.Equal(t, "claude --dangerously-skip-permissions", result)
}

func TestResolveFallToShell_OffWhenHeadless(t *testing.T) {
	// Headless run: the pane must die on agent exit so the monitor's pane-death
	// detection records the authoritative done+exit-code (Tier-3, D100).
	assert.False(t, ResolveFallToShell(agent.IdleSupport{Hook: true}, true))
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
	assert.True(t, ResolveFallToShell(agent.IdleSupport{Hook: true}, false))
}

func TestResolveFallToShell_HeuristicAgentEnabled(t *testing.T) {
	// As of Phase 3 heuristic agents also get fall-to-shell: the monitor honors a
	// wrapper-written `done` (no longer clobbering it with the idle shell) and
	// get_agent_pid descends through the wrapper to the real agent.
	assert.True(t, ResolveFallToShell(agent.IdleSupport{ReadyPattern: "> $", WchanApplicable: true}, false))
}

func TestResolveResumeCommand_AppendsFlag(t *testing.T) {
	// Claude's resume command is the launch command + its native resume flag,
	// continuing the prior conversation with no fresh prompt.
	got := ResolveResumeCommand("claude --dangerously-skip-permissions", "--continue")
	assert.Equal(t, "claude --dangerously-skip-permissions --continue", got)
}

func TestResolveResumeCommand_NoFlagYieldsEmpty(t *testing.T) {
	// An agent with no native resume flag yields "" → yoloai-resume relaunches a
	// fresh session and says so (never claims a resume that didn't happen).
	assert.Equal(t, "", ResolveResumeCommand("aider --yes-always", ""))
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
