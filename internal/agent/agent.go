// Package agent defines built-in agent definitions for yoloAI.
package agent

import (
	"path/filepath"
	"sort"
	"time"
)

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
	// OwnerAPIKeys lists env vars whose presence means this seed file's auth
	// is unnecessary. When non-nil, copySeedFiles checks these instead of
	// the agent-level hasAPIKey.
	OwnerAPIKeys []string
}

// Definition describes an agent's install, launch, and behavioral characteristics.
type Definition struct {
	Name           string
	Description    string
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
		Description:    "Anthropic Claude Code — AI coding assistant",
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
	"gemini": {
		Name:           "gemini",
		Description:    "Google Gemini CLI — AI coding assistant",
		InteractiveCmd: "gemini --yolo",
		HeadlessCmd:    `gemini -p "PROMPT" --yolo`,
		PromptMode:     PromptModeInteractive,
		APIKeyEnvVars:  []string{"GEMINI_API_KEY"},
		SeedFiles: []SeedFile{
			{HostPath: "~/.gemini/oauth_creds.json", TargetPath: "oauth_creds.json", AuthOnly: true},
			{HostPath: "~/.gemini/google_accounts.json", TargetPath: "google_accounts.json", AuthOnly: true},
			{HostPath: "~/.gemini/settings.json", TargetPath: "settings.json"},
		},
		StateDir:       "/home/yoloai/.gemini/",
		SubmitSequence: "Enter",
		StartupDelay:   3 * time.Second,
		ReadyPattern:   "",
		ModelFlag:      "--model",
		ModelAliases: map[string]string{
			"pro":   "gemini-2.5-pro",
			"flash": "gemini-2.5-flash",
		},
	},
	"codex": {
		Name:           "codex",
		Description:    "OpenAI Codex — AI coding agent",
		InteractiveCmd: "codex --dangerously-bypass-approvals-and-sandbox",
		HeadlessCmd:    `codex exec --dangerously-bypass-approvals-and-sandbox "PROMPT"`,
		PromptMode:     PromptModeInteractive,
		APIKeyEnvVars:  []string{"CODEX_API_KEY", "OPENAI_API_KEY"},
		SeedFiles: []SeedFile{
			{HostPath: "~/.codex/auth.json", TargetPath: "auth.json", AuthOnly: true},
			{HostPath: "~/.codex/config.toml", TargetPath: "config.toml"},
		},
		StateDir:       "/home/yoloai/.codex/",
		SubmitSequence: "Enter",
		StartupDelay:   3 * time.Second,
		ReadyPattern:   "›",
		ModelFlag:      "--model",
		ModelAliases:   nil,
	},
	"test": {
		Name:           "test",
		Description:    "Bash shell for testing and development",
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

// AllAgentNames returns sorted agent names for stable iteration.
func AllAgentNames() []string {
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// RealAgents returns sorted names of agents that are real coding agents
// (excludes utility pseudo-agents like "test" and "shell").
func RealAgents() []string {
	var names []string
	for name := range agents {
		if name == "test" || name == "shell" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// buildShellAgent constructs a shell agent whose SeedFiles and APIKeyEnvVars
// are the union of all real agents.
func buildShellAgent() *Definition {
	var seedFiles []SeedFile
	seen := map[string]bool{}
	var apiKeys []string

	for _, name := range RealAgents() {
		ag := agents[name]
		for _, sf := range ag.SeedFiles {
			remapped := sf
			remapped.OwnerAPIKeys = ag.APIKeyEnvVars
			if !sf.HomeDir {
				remapped.HomeDir = true
				remapped.TargetPath = filepath.Base(ag.StateDir) + "/" + sf.TargetPath
			}
			seedFiles = append(seedFiles, remapped)
		}
		for _, key := range ag.APIKeyEnvVars {
			if !seen[key] {
				seen[key] = true
				apiKeys = append(apiKeys, key)
			}
		}
	}

	return &Definition{
		Name:           "shell",
		Description:    "Bash shell with all agents' credentials seeded",
		InteractiveCmd: "bash",
		HeadlessCmd:    `sh -c "PROMPT"`,
		PromptMode:     PromptModeHeadless,
		APIKeyEnvVars:  apiKeys,
		SeedFiles:      seedFiles,
		StateDir:       "",
		SubmitSequence: "Enter",
		StartupDelay:   0,
		ReadyPattern:   "",
		ModelFlag:      "",
		ModelAliases:   nil,
	}
}

func init() {
	agents["shell"] = buildShellAgent()
}
