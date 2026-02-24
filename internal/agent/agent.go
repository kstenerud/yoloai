// Package agent defines built-in agent definitions for yoloAI.
package agent

import "time"

// PromptMode determines how the agent receives its initial prompt.
type PromptMode string

const (
	// PromptModeInteractive feeds prompt via tmux send-keys after startup.
	PromptModeInteractive PromptMode = "interactive"
	// PromptModeHeadless passes prompt as a CLI argument in the launch command.
	PromptModeHeadless PromptMode = "headless"
)

// AuthFile describes a host file that provides authentication credentials.
// HostPath supports ~ for the user's home directory, expanded at runtime.
// TargetPath is relative to the agent's StateDir.
type AuthFile struct {
	HostPath   string // e.g., "~/.claude/.credentials.json"
	TargetPath string // relative to StateDir, e.g., ".credentials.json"
}

// Definition describes an agent's install, launch, and behavioral characteristics.
type Definition struct {
	Name           string
	InteractiveCmd string
	HeadlessCmd    string
	PromptMode     PromptMode
	APIKeyEnvVars  []string
	AuthFiles      []AuthFile
	StateDir       string
	SubmitSequence string
	StartupDelay   time.Duration
	ModelFlag      string
	ModelAliases   map[string]string
}

var agents = map[string]*Definition{
	"claude": {
		Name:           "claude",
		InteractiveCmd: "claude --dangerously-skip-permissions",
		HeadlessCmd:    `claude -p "PROMPT" --dangerously-skip-permissions`,
		PromptMode:     PromptModeInteractive,
		APIKeyEnvVars: []string{"ANTHROPIC_API_KEY"},
		AuthFiles: []AuthFile{
			{HostPath: "~/.claude/.credentials.json", TargetPath: ".credentials.json"},
		},
		StateDir:       "/home/yoloai/.claude/",
		SubmitSequence: "Enter Enter",
		StartupDelay:   3 * time.Second,
		ModelFlag:      "--model",
		ModelAliases: map[string]string{
			"sonnet": "claude-sonnet-4-latest",
			"opus":   "claude-opus-4-latest",
			"haiku":  "claude-haiku-4-latest",
		},
	},
	"test": {
		Name:           "test",
		InteractiveCmd: "bash",
		HeadlessCmd:    `sh -c "PROMPT"`,
		PromptMode:     PromptModeHeadless,
		APIKeyEnvVars:  []string{},
		StateDir:       "",
		SubmitSequence: "Enter",
		StartupDelay:   0,
		ModelFlag:      "",
		ModelAliases:   nil,
	},
}

// GetAgent returns the agent definition for the given name.
// Returns nil if the agent is not known.
func GetAgent(name string) *Definition {
	return agents[name]
}
