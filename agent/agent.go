// Package agent defines built-in agent definitions for yoloAI.
package agent

import (
	_ "embed"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed pi-yoloai-status.ts
var piYoloaiStatusExtension []byte

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

// IdleSupport describes what idle detection signals an agent can produce.
// These are agent capabilities, not configuration — the framework decides
// which detectors to activate based on these capabilities plus the platform.
type IdleSupport struct {
	// Hook: agent has a native hook system that yoloAI can wire up to
	// write status.json on state transitions. Requires agent-specific
	// setup code in sandbox/create.go.
	Hook bool

	// ReadyPattern: terminal prompt text visible when agent is waiting
	// for input. Used by the ready_pattern detector.
	ReadyPattern string

	// ContextSignal: agent reads a context file where we can inject
	// instructions to emit idle/working markers.
	ContextSignal bool

	// WchanApplicable: wchan-based detection is meaningful for this agent.
	// False for test/shell where the process is always waiting on stdin.
	WchanApplicable bool
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
	Idle              IdleSupport
	ModelFlag         string
	ModelAliases      map[string]string
	ModelPrefixes     map[string]string // env var → model prefix (e.g. OLLAMA_API_BASE → "ollama_chat/")
	NetworkAllowlist  []string          // domains allowed when network-isolated
	ContextFile       string            // filename in StateDir for sandbox context reference (e.g., "CLAUDE.md")
	AgentFilesExclude []string          // glob patterns to skip when copying agent_files (string form)

	// EmbeddedFiles maps target paths (relative to the agent's StateDir / the
	// per-sandbox AgentRuntimeDir) to file contents that yoloAI writes at
	// sandbox creation. Used to inject yoloAI-managed agent extensions such
	// as pi's status hook.
	EmbeddedFiles map[string][]byte

	// ApplySettings patches the agent's settings map before it is written to disk.
	// Called with the parsed settings map; mutates it in place.
	// Nil means no patches are needed.
	ApplySettings func(settings map[string]any)

	// ShortLivedOAuthWarning, if true, warns users when an OAuth credential file
	// is copied into the sandbox (used by Claude Code which uses short-lived tokens).
	ShortLivedOAuthWarning bool

	// SeedsAllAgents, if true, means this agent seeds home configs for all real
	// agents rather than just itself (used by the shell agent).
	SeedsAllAgents bool
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
		Idle: IdleSupport{
			ReadyPattern:    "> $",
			ContextSignal:   true,
			WchanApplicable: true,
		},
		ModelFlag: "--model",
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
		APIKeyEnvVars:  []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"},
		SeedFiles: []SeedFile{
			{HostPath: "~/.claude/.credentials.json", TargetPath: ".credentials.json", AuthOnly: true, KeychainService: "Claude Code-credentials"},
			{HostPath: "~/.claude/settings.json", TargetPath: "settings.json"},
			{HostPath: "~/.claude.json", TargetPath: ".claude.json", HomeDir: true},
		},
		StateDir:       "/home/yoloai/.claude/",
		SubmitSequence: "Enter Enter",
		StartupDelay:   3 * time.Second,
		Idle: IdleSupport{
			Hook:            true,
			ReadyPattern:    "❯",
			ContextSignal:   true,
			WchanApplicable: true,
		},
		ModelFlag: "--model",
		ModelAliases: map[string]string{
			"sonnet": "claude-sonnet-4-6",
			"opus":   "claude-opus-4-6",
			"haiku":  "claude-haiku-4-5-20251001",
		},
		NetworkAllowlist:  []string{"api.anthropic.com", "claude.ai", "platform.claude.com", "statsig.anthropic.com", "sentry.io"},
		ContextFile:       "CLAUDE.md",
		AgentFilesExclude: []string{"projects/", "statsig/", "todos/", ".credentials.json", "*.log"},
		ApplySettings: func(s map[string]any) {
			s["skipDangerousModePermissionPrompt"] = true
			// Disable Claude Code's built-in sandbox-exec to prevent nesting failures.
			// sandbox-exec cannot be nested — an inner sandbox-exec inherits the outer
			// profile's restrictions and typically fails.
			s["sandbox"] = map[string]any{"enabled": false}
			// Ensure Claude Code emits BEL for tmux tab highlighting.
			s["preferredNotifChannel"] = "terminal_bell"
			// Inject hooks for status tracking. Claude Code's own hook system is
			// far more reliable than polling tmux capture-pane for a ready pattern.
			injectIdleHook(s)
		},
		ShortLivedOAuthWarning: true,
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
		Idle: IdleSupport{
			ReadyPattern:    "Type your message",
			ContextSignal:   true,
			WchanApplicable: true,
		},
		ModelFlag: "--model",
		ModelAliases: map[string]string{
			"pro":           "gemini-2.5-pro",
			"flash":         "gemini-2.5-flash",
			"preview-pro":   "gemini-3.1-pro-preview",
			"preview-flash": "gemini-3-flash-preview",
		},
		NetworkAllowlist:  []string{"generativelanguage.googleapis.com", "cloudcode-pa.googleapis.com", "oauth2.googleapis.com"},
		ContextFile:       "GEMINI.md",
		AgentFilesExclude: []string{"logs/", "oauth_creds.json", "google_accounts.json"},
		ApplySettings: func(s map[string]any) {
			// Preserve existing security settings (e.g. auth.selectedType) while
			// disabling folder trust — the container is already sandboxed.
			security, _ := s["security"].(map[string]any)
			if security == nil {
				security = map[string]any{}
			}
			security["folderTrust"] = map[string]any{"enabled": false}
			s["security"] = security
		},
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
		Idle: IdleSupport{
			WchanApplicable: true,
		},
		ModelFlag: "--model",
		ModelAliases: map[string]string{
			// OpenCode requires provider/model format. Providers must be configured
			// first via /connect in OpenCode. Use /models to see available models.
			"sonnet": "anthropic/claude-sonnet-4-5-latest",
			"opus":   "anthropic/claude-opus-4-latest",
			"haiku":  "anthropic/claude-haiku-4-5-latest",
			"gpt4o":  "openai/gpt-4o",
			"mini":   "openai/gpt-4o-mini",
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
		Idle: IdleSupport{
			ReadyPattern:    "›",
			ContextSignal:   true,
			WchanApplicable: true,
		},
		ModelFlag: "--model",
		ModelAliases: map[string]string{
			"default": "gpt-5.3-codex",
			"spark":   "gpt-5.3-codex-spark",
			"mini":    "codex-mini-latest",
		},
		NetworkAllowlist:  []string{"api.openai.com"},
		AgentFilesExclude: []string{"auth.json", "sessions/"},
	},
	"pi": {
		Name:           "pi",
		Description:    "pi coding agent",
		InteractiveCmd: "pi",
		HeadlessCmd:    `pi "$PROMPT"`,
		PromptMode:     PromptModeInteractive,
		APIKeyEnvVars: []string{
			"ANTHROPIC_API_KEY",                // Anthropic Claude API key
			"ANTHROPIC_OAUTH_TOKEN",            // Anthropic OAuth token (alternative to API key)
			"OPENAI_API_KEY",                   // OpenAI GPT API key
			"AZURE_OPENAI_API_KEY",             // Azure OpenAI API key
			"AZURE_OPENAI_BASE_URL",            // Azure OpenAI/Cognitive Services base URL (e.g. https://{resource}.openai.azure.com)
			"AZURE_OPENAI_RESOURCE_NAME",       // Azure OpenAI resource name (alternative to base URL)
			"AZURE_OPENAI_API_VERSION",         // Azure OpenAI API version (default: v1)
			"AZURE_OPENAI_DEPLOYMENT_NAME_MAP", // Azure OpenAI model=deployment map (comma-separated)
			"DEEPSEEK_API_KEY",                 // DeepSeek API key
			"GEMINI_API_KEY",                   // Google Gemini API key
			"GROQ_API_KEY",                     // Groq API key
			"CEREBRAS_API_KEY",                 // Cerebras API key
			"XAI_API_KEY",                      // XAI Grok API key
			"FIREWORKS_API_KEY",                // Fireworks API key
			"OPENROUTER_API_KEY",               // OpenRouter API key
			"AI_GATEWAY_API_KEY",               // Vercel AI Gateway API key
			"ZAI_API_KEY",                      // ZAI API key
			"MISTRAL_API_KEY",                  // Mistral API key
			"MINIMAX_API_KEY",                  // MiniMax API key
			"OPENCODE_API_KEY",                 // OpenCode Zen/OpenCode Go API key
			"KIMI_API_KEY",                     // Kimi For Coding API key
			"CLOUDFLARE_API_KEY",               // Cloudflare API token (Workers AI)
			"CLOUDFLARE_ACCOUNT_ID",            // Cloudflare account id (required for Workers AI)
			"AWS_PROFILE",                      // AWS profile for Amazon Bedrock
			"AWS_ACCESS_KEY_ID",                // AWS access key for Amazon Bedrock
			"AWS_SECRET_ACCESS_KEY",            // AWS secret key for Amazon Bedrock
			"AWS_BEARER_TOKEN_BEDROCK",         // Bedrock API key (bearer token)
			"AWS_REGION",                       // AWS region for Amazon Bedrock (e.g., us-east-1)
			"PI_CODING_AGENT_DIR",              // Session storage directory (default: ~/.pi/agent)
			"PI_PACKAGE_DIR",                   // Override package directory (for Nix/Guix store paths)
			"PI_OFFLINE",                       // Disable startup network operations when set to 1/true/yes
			"PI_TELEMETRY",                     // Override install telemetry when set to 1/true/yes or 0/false/no
			"PI_SHARE_VIEWER_URL",              // Base URL for /share command (default: https://pi.dev/session/)
			"PI_AI_ANTIGRAVITY_VERSION",        // Override Antigravity User-Agent version (e.g., 1.23.0)
		},
		SeedFiles: []SeedFile{
			{HostPath: "~/.pi/agent/auth.json", TargetPath: "agent/auth.json", AuthOnly: true},
			{HostPath: "~/.pi/agent/models.json", TargetPath: "agent/models.json"},
			{HostPath: "~/.pi/agent/settings.json", TargetPath: "agent/settings.json"},
		},
		StateDir:       "/home/yoloai/.pi/",
		SubmitSequence: "Enter",
		StartupDelay:   3 * time.Second,
		Idle: IdleSupport{
			Hook:            true,
			WchanApplicable: true,
		},
		ModelFlag:         "--model",
		NetworkAllowlist:  []string{},
		ContextFile:       "agent/AGENTS.md",
		AgentFilesExclude: []string{"auth.json", "models.json", "settings.json", "AGENTS.md", "sessions/", "extensions/yoloai-status.ts"},
		EmbeddedFiles: map[string][]byte{
			"agent/extensions/yoloai-status.ts": piYoloaiStatusExtension,
		},
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
	"idle": {
		Name:           "idle",
		Description:    "No-op container — keeps the sandbox running without an AI agent. Default for mcp-proxy.",
		InteractiveCmd: "sleep infinity",
		HeadlessCmd:    "sleep infinity",
		PromptMode:     PromptModeInteractive,
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
		if name == "test" || name == "shell" || name == "idle" {
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
		InteractiveCmd:   `bash -c 'printf "\n  yoloai shell — launch any agent with yolo-<name>\n  Available: yolo-aider  yolo-claude  yolo-codex  yolo-gemini  yolo-opencode  yolo-pi\n\n"; exec bash'`,
		HeadlessCmd:      `sh -c "PROMPT"`,
		PromptMode:       PromptModeHeadless,
		APIKeyEnvVars:    apiKeys,
		SeedFiles:        seedFiles,
		StateDir:         "",
		SubmitSequence:   "Enter",
		StartupDelay:     0,
		ModelFlag:        "",
		ModelAliases:     nil,
		NetworkAllowlist: networkAllowlist,
		SeedsAllAgents:   true,
	}
}

func init() {
	agents["shell"] = buildShellAgent()
}

// statusIdleCommand writes idle status to agent-status.json and appends a
// structured JSONL entry to logs/agent-hooks.jsonl when Claude finishes a
// response (Stop hook). Uses $YOLOAI_DIR for portability across
// backends (Docker=/yoloai, seatbelt=sandbox dir).
const statusIdleCommand = `printf '{"ts":"%s","level":"info","event":"hook.idle","msg":"agent hook: idle","status":"idle"}\n' "$(date -u +%Y-%m-%dT%H:%M:%S.000Z)" >> "${YOLOAI_DIR:-/yoloai}/logs/agent-hooks.jsonl" && printf '{"status":"idle","exit_code":null,"timestamp":%d}\n' "$(date +%s)" > "${YOLOAI_DIR:-/yoloai}/agent-status.json"`

// statusActiveCommand writes active status to agent-status.json and appends a
// structured JSONL entry to logs/agent-hooks.jsonl when Claude starts working
// (PreToolUse and UserPromptSubmit hooks). This ensures the title updates from
// "> name" back to "name" as soon as a new prompt is submitted or a tool is called.
const statusActiveCommand = `printf '{"ts":"%s","level":"info","event":"hook.active","msg":"agent hook: active","status":"active"}\n' "$(date -u +%Y-%m-%dT%H:%M:%S.000Z)" >> "${YOLOAI_DIR:-/yoloai}/logs/agent-hooks.jsonl" && printf '{"status":"active","exit_code":null,"timestamp":%d}\n' "$(date +%s)" > "${YOLOAI_DIR:-/yoloai}/agent-status.json"`

// injectIdleHook merges hooks into Claude Code's settings map for status tracking.
// Stop → idle (turn complete), PreToolUse + UserPromptSubmit → running (work started).
// Preserves any existing hooks the user may have configured.
//
// We use the Stop hook (not Notification) because Stop fires immediately at the
// end of every turn. The Notification hook's idle_prompt type fires only after
// messageIdleNotifThresholdMs of inactivity (default: 60 seconds), making it
// useless as a completion signal. Notification also fires on permission_prompt
// and other non-idle events that would incorrectly mark the agent as idle.
//
// UserPromptSubmit is added alongside PreToolUse so the active signal fires as
// soon as the user submits a new prompt, before any tool calls begin.
func injectIdleHook(settings map[string]any) {
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}

	// Stop hook: mark idle when Claude concludes its response.
	idleHook := map[string]any{
		"type":    "command",
		"command": statusIdleCommand,
	}
	idleGroup := map[string]any{
		"hooks": []any{idleHook},
	}
	existingStop, _ := hooks["Stop"].([]any)
	hooks["Stop"] = append(existingStop, idleGroup)

	// PreToolUse hook: mark active when Claude starts using tools.
	activeHook := map[string]any{
		"type":    "command",
		"command": statusActiveCommand,
	}
	activeGroup := map[string]any{
		"hooks": []any{activeHook},
	}
	existingPre, _ := hooks["PreToolUse"].([]any)
	hooks["PreToolUse"] = append(existingPre, activeGroup)

	// UserPromptSubmit hook: mark active as soon as a new prompt is submitted,
	// before PreToolUse fires. This closes the window where the agent appears
	// idle between prompt submission and the first tool call.
	existingSubmit, _ := hooks["UserPromptSubmit"].([]any)
	hooks["UserPromptSubmit"] = append(existingSubmit, activeGroup)

	settings["hooks"] = hooks
}
