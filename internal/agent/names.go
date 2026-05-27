// ABOUTME: Typed enum for agent names. Open-set: agent registry is the source
// ABOUTME: of truth; the constants document the agents shipped with yoloai.

package agent

// AgentName names a coding agent. Open-set typed string — the
// constants document the agents that ship with yoloai; user-defined
// or future agents supply their own name via the agent registry.
//
// This type exists so the public Client API surface (added in
// W-L8b/c/d) can take a typed parameter rather than `string`. The
// existing agent registry continues to use string keys internally;
// callers convert at the public boundary as they migrate.
//
// Established by W-L8a Q-Y.
type AgentName string

const (
	AgentClaude   AgentName = "claude"
	AgentCodex    AgentName = "codex"
	AgentGemini   AgentName = "gemini"
	AgentOpenCode AgentName = "opencode"
	AgentAider    AgentName = "aider"
	AgentTest     AgentName = "test" // dev/test helper agent
)
