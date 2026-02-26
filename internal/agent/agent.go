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

// SeedFile describes a host file to copy into the agent's state directory.
// HostPath supports ~ for the user's home directory, expanded at runtime.
// TargetPath is relative to the agent's StateDir.
type SeedFile struct {
	HostPath        string // e.g., "~/.claude/settings.json"
	TargetPath      string // relative to StateDir, e.g., "settings.json"
	AuthOnly        bool   // if true, only required when no API key is set
	HomeDir         bool   // if true, TargetPath is relative to /home/yoloai/ instead of StateDir
	KeychainService string // macOS Keychain service name; used as fallback when HostPath is missing
}

// Definition describes an agent's install, launch, and behavioral characteristics.
type Definition struct {
	Name           string
	InteractiveCmd string
	HeadlessCmd    string
	PromptMode     PromptMode
	APIKeyEnvVars  []string
	SeedFiles      []SeedFile
	StateDir       string
	SubmitSequence string
	StartupDelay   time.Duration
	ReadyPattern   string // grep pattern in tmux output that signals agent is ready for input
	ModelFlag      string
	ModelAliases   map[string]string
}

var agents = map[string]*Definition{
	"claude": {
		Name:           "claude",
		InteractiveCmd: "claude --dangerously-skip-permissions",
		HeadlessCmd:    `claude -p "PROMPT" --dangerously-skip-permissions`,
		PromptMode:     PromptModeInteractive,
		APIKeyEnvVars:  []string{"ANTHROPIC_API_KEY"},
		SeedFiles: []SeedFile{
			{HostPath: "~/.claude/.credentials.json", TargetPath: ".credentials.json", AuthOnly: true, KeychainService: "Claude Code-credentials"},
			{HostPath: "~/.claude/settings.json", TargetPath: "settings.json"},
			{HostPath: "~/.claude.json", TargetPath: ".claude.json", HomeDir: true},
		},
		StateDir:       "/home/yoloai/.claude/",
		SubmitSequence: "Enter Enter",
		StartupDelay:   3 * time.Second,
		ReadyPattern:   "❯",
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
