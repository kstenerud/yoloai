// ABOUTME: Tests for AgentName typed-enum constants — values match the
// ABOUTME: strings used by the agent registry today.

package agent

import "testing"

func TestAgentNameConstants(t *testing.T) {
	cases := []struct {
		got  AgentName
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
			t.Errorf("AgentName(%v) = %q, want %q", c.got, string(c.got), c.want)
		}
	}
}

// TestAgentNameMatchesRegistry verifies each named constant corresponds to
// a real agent definition registered today — guards against the constant
// list drifting from the actual shipped agents.
func TestAgentNameMatchesRegistry(t *testing.T) {
	for _, name := range []AgentName{
		AgentClaude, AgentCodex, AgentGemini, AgentOpenCode, AgentAider, AgentTest,
	} {
		if def := GetAgent(string(name)); def == nil {
			t.Errorf("AgentName constant %q has no matching agent definition", name)
		}
	}
}
