// ABOUTME: Typed enum for agent names. Open-set: agent registry is the source
// ABOUTME: of truth; the constants document the agents shipped with yoloai.

package agent

// AgentType names a coding agent. Open-set typed string — the
// constants document the agents that ship with yoloai; user-defined
// or future agents supply their own name via the agent registry.
//
// This type exists so the public Client API surface (added in
// W-L8b/c/d) can take a typed parameter rather than `string`. The
// existing agent registry continues to use string keys internally;
// callers convert at the public boundary as they migrate.
//
// Established by W-L8a Q-Y.
type AgentType string

const (
	AgentClaude   AgentType = "claude"
	AgentCodex    AgentType = "codex"
	AgentGemini   AgentType = "gemini"
	AgentOpenCode AgentType = "opencode"
	AgentAider    AgentType = "aider"
	AgentTest     AgentType = "test" // dev/test helper agent
)
