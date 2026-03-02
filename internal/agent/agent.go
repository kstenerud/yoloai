// Package agent defines built-in agent definitions for yoloAI.
package agent

import (
	"path/filepath"
	"sort"
	"strings"
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
	Name              string
	Description       string
	InteractiveCmd    string
	HeadlessCmd       string
	PromptMode        PromptMode
	APIKeyEnvVars     []string
	AuthHintEnvVars   []string // env vars indicating auth is configured without a cloud API key (e.g. local model servers)
	AuthOptional      bool     // when true, missing auth is a warning not an error (for agents with many auth paths)
	SeedFiles         []SeedFile
	StateDir          string
	SubmitSequence    string
	StartupDelay      time.Duration
	ReadyPattern      string // grep pattern in tmux output that signals agent is ready for input
	ModelFlag         string
	ModelAliases      map[string]string
	ModelPrefixes     map[string]string // env var → model prefix (e.g. OLLAMA_API_BASE → "ollama_chat/")
	NetworkAllowlist  []string          // domains allowed when network-isolated
	ContextFile       string            // filename in StateDir for sandbox context reference (e.g., "CLAUDE.md")
	AgentFilesExclude []string          // glob patterns to skip when copying agent_files (string form)
}

var agents = map[string]*Definition{
	"aider": {
		Name:            "aider",
		Description:     "Aider — AI pair programming in your terminal",
		InteractiveCmd:  "aider --yes-always",
		HeadlessCmd:     `aider --message "PROMPT" --yes-always --no-pretty --no-fancy-input`,
		PromptMode:      PromptModeInteractive,
		APIKeyEnvVars:   []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "DEEPSEEK_API_KEY", "OPENROUTER_API_KEY"},
		AuthHintEnvVars: []string{"OLLAMA_API_BASE", "OPENAI_API_BASE"},
		SeedFiles: []SeedFile{
			{HostPath: "~/.aider.conf.yml", TargetPath: ".aider.conf.yml", HomeDir: true},
		},
		StateDir:       "",
		SubmitSequence: "Enter",
		StartupDelay:   3 * time.Second,
		ReadyPattern:   "> $",
		ModelFlag:      "--model",
		ModelAliases: map[string]string{
			"sonnet":   "sonnet",
			"opus":     "opus",
			"haiku":    "haiku",
			"deepseek": "deepseek",
			"flash":    "flash",
		},
		ModelPrefixes: map[string]string{
			"OLLAMA_API_BASE": "ollama_chat/",
			"OPENAI_API_BASE": "openai/",
		},
	},
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
		NetworkAllowlist:  []string{"api.anthropic.com", "claude.ai", "platform.claude.com", "statsig.anthropic.com", "sentry.io"},
		ContextFile:       "CLAUDE.md",
		AgentFilesExclude: []string{"projects/", "statsig/", "todos/", ".credentials.json", "*.log"},
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
		ReadyPattern:   "Type your message",
		ModelFlag:      "--model",
		ModelAliases: map[string]string{
			"pro":           "gemini-2.5-pro",
			"flash":         "gemini-2.5-flash",
			"preview-pro":   "gemini-3.1-pro-preview",
			"preview-flash": "gemini-3-flash-preview",
		},
		NetworkAllowlist:  []string{"generativelanguage.googleapis.com", "cloudcode-pa.googleapis.com", "oauth2.googleapis.com"},
		ContextFile:       "GEMINI.md",
		AgentFilesExclude: []string{"logs/", "oauth_creds.json", "google_accounts.json"},
	},
	"opencode": {
		Name:            "opencode",
		Description:     "OpenCode — open-source AI coding agent",
		InteractiveCmd:  "opencode",
		HeadlessCmd:     `opencode run "PROMPT"`,
		PromptMode:      PromptModeHeadless,
		APIKeyEnvVars:   []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GROQ_API_KEY", "OPENROUTER_API_KEY", "XAI_API_KEY"},
		AuthHintEnvVars: []string{"GITHUB_TOKEN", "LOCAL_ENDPOINT", "AZURE_OPENAI_ENDPOINT", "AWS_ACCESS_KEY_ID", "AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_REGION", "AWS_DEFAULT_REGION", "VERTEXAI_PROJECT"},
		AuthOptional:    true,
		SeedFiles: []SeedFile{
			{HostPath: "~/.local/share/opencode/auth.json", TargetPath: "auth.json", AuthOnly: true},
			{HostPath: "~/.opencode.json", TargetPath: ".opencode.json", AuthOnly: true, HomeDir: true},
			{HostPath: "~/.config/github-copilot/hosts.json", TargetPath: ".config/github-copilot/hosts.json", AuthOnly: true, HomeDir: true},
			{HostPath: "~/.config/github-copilot/apps.json", TargetPath: ".config/github-copilot/apps.json", AuthOnly: true, HomeDir: true},
			{HostPath: "~/.config/opencode/.opencode.json", TargetPath: ".config/opencode/.opencode.json", HomeDir: true},
		},
		StateDir:       "/home/yoloai/.local/share/opencode/",
		SubmitSequence: "Enter",
		StartupDelay:   3 * time.Second,
		ReadyPattern:   "",
		ModelFlag:      "--model",
		ModelAliases: map[string]string{
			"sonnet": "anthropic/claude-sonnet-4-5-latest",
			"opus":   "anthropic/claude-opus-4-latest",
			"haiku":  "anthropic/claude-haiku-4-5-latest",
		},
		NetworkAllowlist:  []string{"api.anthropic.com", "api.openai.com", "generativelanguage.googleapis.com", "api.github.com", "api.githubcopilot.com"},
		AgentFilesExclude: []string{"auth.json", "sessions/"},
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
		ModelAliases: map[string]string{
			"default": "gpt-5.3-codex",
			"spark":   "gpt-5.3-codex-spark",
			"mini":    "codex-mini-latest",
		},
		NetworkAllowlist:  []string{"api.openai.com"},
		AgentFilesExclude: []string{"auth.json", "sessions/"},
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

// StateRelPath returns the relative path from /home/yoloai/ to the agent's
// state directory. For example, ".claude" for Claude, ".gemini" for Gemini,
// ".local/share/opencode" for OpenCode. Returns "" for agents without a StateDir.
func (d *Definition) StateRelPath() string {
	if d.StateDir == "" {
		return ""
	}
	const prefix = "/home/yoloai/"
	path := strings.TrimSuffix(d.StateDir, "/")
	if strings.HasPrefix(path, prefix) {
		return path[len(prefix):]
	}
	return ""
}

// buildShellAgent constructs a shell agent whose SeedFiles and APIKeyEnvVars
// are the union of all real agents.
func buildShellAgent() *Definition {
	var seedFiles []SeedFile
	seen := map[string]bool{}
	var apiKeys []string
	seenDomains := map[string]bool{}
	var networkAllowlist []string

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
		for _, domain := range ag.NetworkAllowlist {
			if !seenDomains[domain] {
				seenDomains[domain] = true
				networkAllowlist = append(networkAllowlist, domain)
			}
		}
	}

	return &Definition{
		Name:             "shell",
		Description:      "Bash shell with all agents' credentials seeded",
		InteractiveCmd:   `bash -c 'printf "\n  yoloai shell — launch any agent with yolo-<name>\n  Available: yolo-aider  yolo-claude  yolo-codex  yolo-gemini  yolo-opencode\n\n"; exec bash'`,
		HeadlessCmd:      `sh -c "PROMPT"`,
		PromptMode:       PromptModeHeadless,
		APIKeyEnvVars:    apiKeys,
		SeedFiles:        seedFiles,
		StateDir:         "",
		SubmitSequence:   "Enter",
		StartupDelay:     0,
		ReadyPattern:     "",
		ModelFlag:        "",
		ModelAliases:     nil,
		NetworkAllowlist: networkAllowlist,
	}
}

func init() {
	agents["shell"] = buildShellAgent()
}
