// ABOUTME: Tests for AgentType typed-enum constants — values match the
// ABOUTME: strings used by the agent registry today.

package agent

import "testing"

func TestAgentTypeConstants(t *testing.T) {
	cases := []struct {
		got  AgentType
		want string
	}{
		{AgentClaude, "claude"},
		{AgentCodex, "codex"},
		{AgentGemini, "gemini"},
		{AgentOpenCode, "opencode"},
		{AgentAider, "aider"},
		{AgentTest, "test"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("AgentType(%v) = %q, want %q", c.got, string(c.got), c.want)
		}
	}
}

// TestAgentTypeMatchesRegistry verifies each named constant corresponds to
// a real agent definition registered today — guards against the constant
// list drifting from the actual shipped agents.
func TestAgentTypeMatchesRegistry(t *testing.T) {
	for _, name := range []AgentType{
		AgentClaude, AgentCodex, AgentGemini, AgentOpenCode, AgentAider, AgentTest,
	} {
		if def := GetAgent(string(name)); def == nil {
			t.Errorf("AgentType constant %q has no matching agent definition", name)
		}
	}
}
