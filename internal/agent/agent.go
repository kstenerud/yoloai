// ABOUTME: Agent definitions (Claude, Gemini, Codex, Aider, OpenCode, etc.)
// ABOUTME: Consumed by sandbox/create.go to launch, seed, and configure agents.
// Package agent defines built-in agent definitions for yoloAI.
package agent

import (
	_ "embed"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// opencodeStatusPlugin is the OpenCode plugin (seeded into the sandbox) that
// mirrors turn state into agent-status.json, making OpenCode hook-authoritative.
//
//go:embed opencode_plugin.js
var opencodeStatusPlugin string

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
	HostPath   string // e.g., "~/.claude/settings.json"
	TargetPath string // relative to StateDir, e.g., "settings.json"
	// Content, when non-nil, is a yoloai-provided fallback: written when HostPath
	// is empty (a file with no host source, e.g. the OpenCode status plugin) or
	// when the host file is absent (an agent default, e.g. aider's empty "{}"
	// conf). A present host file still wins.
	Content         []byte
	AuthOnly        bool   // if true, only required when no API key is set
	HomeDir         bool   // if true, TargetPath is relative to /home/yoloai/ instead of StateDir
	KeychainService string // macOS Keychain service name; used as fallback when HostPath is missing
	// OwnerAPIKeys lists env vars whose presence means this seed file's auth
	// is unnecessary. When non-nil, copySeedFiles checks these instead of
	// the agent-level hasAPIKey.
	OwnerAPIKeys []string
	// Executable seeds the file mode 0700 (owner rwx) instead of the default
	// 0600, for scripts the agent execs by path (e.g. Claude Code's statusLine
	// script). Credentials/config stay 0600.
	Executable bool
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
	Type              AgentType
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

	// ResumeFlag is the agent's native conversation-resume flag, appended to the
	// interactive command to continue the prior conversation (e.g. Claude
	// "--continue"). "" means the agent has no native resume — the fall-to-shell
	// yoloai-resume script then relaunches a FRESH session and says so (honest
	// characterization, D96/agent-detection.md DD4). Resolved into resume_cmd for
	// the fall-to-shell wrapper.
	ResumeFlag string

	// ApplySettings patches the agent's JSON config map before it is written to
	// disk. Called with the parsed config map; mutates it in place. Nil means no
	// patches are needed.
	ApplySettings func(settings map[string]any)

	// SettingsFileName is the JSON config file ApplySettings targets in the
	// agent's state dir. "" defaults to "settings.json" (Claude, Gemini); Codex
	// uses "hooks.json" (its dedicated lifecycle-hooks file).
	SettingsFileName string

	// ShortLivedOAuthWarning, if true, warns users when an OAuth credential file
	// is copied into the sandbox (used by Claude Code which uses short-lived tokens).
	ShortLivedOAuthWarning bool

	// SeedsAllAgents, if true, means this agent seeds home configs for all real
	// agents rather than just itself (used by the shell agent).
	SeedsAllAgents bool
}

// agentsMu guards the agents map for concurrent reads and file-agent registration.
// init() runs single-threaded before user goroutines start, so init() and
// buildShellAgent() read/write agents without holding this lock.
var agentsMu sync.RWMutex

var agents = map[string]*Definition{
	"aider": {
		Type:        "aider",
		Description: "Aider — AI pair programming in your terminal",
		// --notifications-command runs on "LLM finished, waiting for input" (turn
		// complete → idle). It is Aider's only turn callback (no turn-start event),
		// so Aider is hook-authoritative for IDLE; the active signal comes from
		// yoloai's prompt-delivery (active-before-submit). Reuses the --write-status
		// CLI (schema single-sourced).
		InteractiveCmd:  "aider --yes-always --notifications --notifications-command 'python3 /yoloai/bin/status-monitor.py --write-status idle /yoloai/agent-status.json'",
		HeadlessCmd:     `aider --message "PROMPT" --yes-always --no-pretty --no-fancy-input`,
		PromptMode:      PromptModeInteractive,
		APIKeyEnvVars:   []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "DEEPSEEK_API_KEY", "OPENROUTER_API_KEY"},
		AuthHintEnvVars: []string{"OLLAMA_API_BASE", "OPENAI_API_BASE"},
		SeedFiles: []SeedFile{
			// Default to an empty YAML map when the host has no aider config: aider
			// errors on an empty/whitespace .aider.conf.yml ("NoneType, not a
			// dict"), and the file is always present in the sandbox. A real host
			// config still wins (Content is a fallback).
			{HostPath: "~/.aider.conf.yml", TargetPath: ".aider.conf.yml", Content: []byte("{}\n"), HomeDir: true},
		},
		StateDir:       "",
		SubmitSequence: "Enter",
		StartupDelay:   3 * time.Second,
		Idle: IdleSupport{
			// Hook-authoritative for idle via --notifications-command (above).
			// Stop-only: active relies on prompt-delivery's active-before-submit,
			// so a turn the user types directly (via attach) shows stale-idle until
			// it completes — a known gap a future hook-assisted mode would close.
			Hook:            true,
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
		Type:           "claude",
		Description:    "Anthropic Claude Code — AI coding assistant",
		InteractiveCmd: "claude --dangerously-skip-permissions",
		HeadlessCmd:    `claude -p "PROMPT" --dangerously-skip-permissions`,
		PromptMode:     PromptModeInteractive,
		ResumeFlag:     "--continue",
		APIKeyEnvVars:  []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"},
		SeedFiles: []SeedFile{
			{HostPath: "~/.claude/.credentials.json", TargetPath: ".credentials.json", AuthOnly: true, KeychainService: "Claude Code-credentials"},
			{HostPath: "~/.claude/settings.json", TargetPath: "settings.json"},
			// Default to an empty JSON object when the host has no ~/.claude.json:
			// Claude Code treats an empty/0-byte file as corrupted (logs a scary
			// "config corrupted" warning and backs it up). A real host file wins.
			{HostPath: "~/.claude.json", TargetPath: ".claude.json", Content: []byte("{}\n"), HomeDir: true},
			// statusLine script referenced by settings.json; Claude Code execs it
			// by path, so it must keep the exec bit (Executable → 0700).
			{HostPath: "~/.claude/statusline.sh", TargetPath: "statusline.sh", Executable: true},
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
		Type:           "gemini",
		Description:    "Google Gemini CLI — AI coding assistant",
		InteractiveCmd: "gemini --yolo",
		HeadlessCmd:    `gemini -p "PROMPT" --yolo`,
		PromptMode:     PromptModeInteractive,
		APIKeyEnvVars:  []string{"GEMINI_API_KEY"},
		SeedFiles: []SeedFile{
			// Gemini renamed its OAuth credential file oauth_creds.json →
			// gemini-credentials.json (current CLI, e.g. 0.47). Seed both: AuthOnly
			// files are skipped when absent, so this covers old and new CLIs.
			{HostPath: "~/.gemini/oauth_creds.json", TargetPath: "oauth_creds.json", AuthOnly: true},
			{HostPath: "~/.gemini/gemini-credentials.json", TargetPath: "gemini-credentials.json", AuthOnly: true},
			{HostPath: "~/.gemini/google_accounts.json", TargetPath: "google_accounts.json", AuthOnly: true},
			{HostPath: "~/.gemini/settings.json", TargetPath: "settings.json"},
		},
		StateDir:       "/home/yoloai/.gemini/",
		SubmitSequence: "Enter",
		StartupDelay:   3 * time.Second,
		Idle: IdleSupport{
			Hook:            true,
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
		AgentFilesExclude: []string{"logs/", "oauth_creds.json", "gemini-credentials.json", "google_accounts.json"},
		ApplySettings: func(s map[string]any) {
			// Preserve existing security settings (e.g. auth.selectedType) while
			// disabling folder trust — the container is already sandboxed.
			security, _ := s["security"].(map[string]any)
			if security == nil {
				security = map[string]any{}
			}
			security["folderTrust"] = map[string]any{"enabled": false}
			s["security"] = security
			// Native turn-completion detection: BeforeAgent → active, AfterAgent
			// → idle (Gemini CLI >= v0.26.0). Makes Gemini hook-authoritative.
			injectGeminiHook(s)
		},
	},
	"opencode": {
		Type:            "opencode",
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
			// Native turn-completion detection via an OpenCode plugin (a JS file
			// OpenCode auto-loads): session.idle → idle, message.updated → active.
			// yoloai-provided content (no host file). Makes OpenCode hook-authoritative.
			{TargetPath: ".config/opencode/plugins/yoloai-status.js", Content: []byte(opencodeStatusPlugin), HomeDir: true},
		},
		StateDir:       "/home/yoloai/.local/share/opencode/",
		SubmitSequence: "Enter",
		StartupDelay:   3 * time.Second,
		Idle: IdleSupport{
			Hook:            true,
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
		Type:        "codex",
		Description: "OpenAI Codex — AI coding agent",
		// --dangerously-bypass-hook-trust runs our status-tracking hooks without
		// Codex's interactive trust prompt; the sandbox is the trust boundary
		// (the agent-layer folder-trust principle), same rationale as the
		// approvals/sandbox bypass.
		InteractiveCmd: "codex --dangerously-bypass-approvals-and-sandbox --dangerously-bypass-hook-trust",
		HeadlessCmd:    `codex exec --dangerously-bypass-approvals-and-sandbox --dangerously-bypass-hook-trust "PROMPT"`,
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
			Hook:            true,
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
		AgentFilesExclude: []string{"auth.json", "sessions/", "hooks.json"},
		// Native turn-completion detection via Codex's lifecycle hooks, written to
		// its dedicated ~/.codex/hooks.json: UserPromptSubmit/PreToolUse → active,
		// Stop → idle. Makes Codex hook-authoritative.
		SettingsFileName: "hooks.json",
		ApplySettings:    injectCodexHooks,
	},
	"test": {
		Type:           "test",
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
		Type:        "idle",
		Description: "No-op container — keeps the sandbox running without an AI agent. Default for mcp-proxy.",
		// `tail -f /dev/null`, not `sleep infinity`: the latter is a GNU-coreutils
		// extension and the tart guest is macOS, whose BSD `sleep` rejects
		// "infinity" (exit 1 → sandbox fails). tail -f blocks event-driven
		// (kqueue/inotify) at ~0% CPU and is portable across BSD and GNU.
		InteractiveCmd: "tail -f /dev/null",
		HeadlessCmd:    "tail -f /dev/null",
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
	agentsMu.RLock()
	defer agentsMu.RUnlock()
	return agents[name]
}

// AllAgentTypes returns sorted agent names for stable iteration.
func AllAgentTypes() []string {
	agentsMu.RLock()
	defer agentsMu.RUnlock()
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
	agentsMu.RLock()
	defer agentsMu.RUnlock()
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

// realAgentNamesLocked returns sorted real agent names without acquiring agentsMu.
// Callers must hold agentsMu (or call only from init, before any goroutines start).
func realAgentNamesLocked() []string {
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

// buildShellAgent constructs a shell agent whose SeedFiles and APIKeyEnvVars
// are the union of all real agents. Called only from init(), which runs
// single-threaded before any goroutines start — no lock needed.
func buildShellAgent() *Definition {
	var seedFiles []SeedFile
	seen := map[string]bool{}
	var apiKeys []string
	seenDomains := map[string]bool{}
	var networkAllowlist []string

	for _, name := range realAgentNamesLocked() {
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
		Type:             "shell",
		Description:      "Bash shell with all agents' credentials seeded",
		InteractiveCmd:   `bash -c 'printf "\n  yoloai shell — launch any agent with yolo-<name>\n  Available: yolo-aider  yolo-claude  yolo-codex  yolo-gemini  yolo-opencode\n\n"; exec bash'`,
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
const statusIdleCommand = `printf '{"ts":"%s","level":"info","event":"hook.idle","msg":"agent hook: idle","status":"idle"}\n' "$(date -u +%Y-%m-%dT%H:%M:%S.000Z)" >> "${YOLOAI_DIR:-/yoloai}/logs/agent-hooks.jsonl" && printf '{"schema_version":1,"status":"idle","exit_code":null,"timestamp":%d}\n' "$(date +%s)" > "${YOLOAI_DIR:-/yoloai}/agent-status.json"`

// statusActiveCommand writes active status to agent-status.json and appends a
// structured JSONL entry to logs/agent-hooks.jsonl when Claude starts working
// (PreToolUse and UserPromptSubmit hooks). This ensures the title updates from
// "> name" back to "name" as soon as a new prompt is submitted or a tool is called.
const statusActiveCommand = `printf '{"ts":"%s","level":"info","event":"hook.active","msg":"agent hook: active","status":"active"}\n' "$(date -u +%Y-%m-%dT%H:%M:%S.000Z)" >> "${YOLOAI_DIR:-/yoloai}/logs/agent-hooks.jsonl" && printf '{"schema_version":1,"status":"active","exit_code":null,"timestamp":%d}\n' "$(date +%s)" > "${YOLOAI_DIR:-/yoloai}/agent-status.json"`

// geminiActiveCommand / geminiIdleCommand reuse the shared status writers but
// also emit an empty JSON object on stdout: Gemini parses a hook's stdout as JSON
// and mandates "no plain text" — "{}" means "continue, take no action", so the
// status write is the only side effect. The status-writer payload itself is
// completion's (D89); only the trailing stdout contract is Gemini-specific.
const geminiActiveCommand = statusActiveCommand + ` && printf '{}'`
const geminiIdleCommand = statusIdleCommand + ` && printf '{}'`

// injectGeminiHook merges Gemini CLI lifecycle hooks into the settings map for
// status tracking: BeforeAgent → active (a turn started), AfterAgent → idle
// (turn complete). Mirrors injectIdleHook but uses Gemini's hooks schema (each
// group carries matcher/sequential) and event names. Requires Gemini CLI
// >= v0.26.0; preserves any hooks the user configured.
func injectGeminiHook(settings map[string]any) {
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	// No "matcher": Gemini 0.47 validates it as a string and rejects null, and
	// BeforeAgent/AfterAgent are agent-level (nothing to match). The minimal
	// group — just the command hooks — mirrors Claude's shape.
	group := func(command string) map[string]any {
		return map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": command}},
		}
	}
	appendHookGroup(hooks, "BeforeAgent", geminiActiveCommand, group(geminiActiveCommand))
	appendHookGroup(hooks, "AfterAgent", geminiIdleCommand, group(geminiIdleCommand))
	settings["hooks"] = hooks
}

// hookEventHasCommand reports whether any group already registered under a hook
// event contains a command hook with the given command. ApplySettings is
// re-applied on every create+start and restart, so the injectors must be
// idempotent — without this check the hooks accumulate one duplicate per apply.
func hookEventHasCommand(groups []any, command string) bool {
	for _, g := range groups {
		gm, ok := g.(map[string]any)
		if !ok {
			continue
		}
		inner, _ := gm["hooks"].([]any)
		for _, h := range inner {
			hm, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if cmd, _ := hm["command"].(string); cmd == command {
				return true
			}
		}
	}
	return false
}

// appendHookGroup adds a hook group under an event, idempotently (skips if a
// group with the same command is already registered) and preserving any hooks
// already present.
func appendHookGroup(hooks map[string]any, event, command string, group map[string]any) {
	existing, _ := hooks[event].([]any)
	if hookEventHasCommand(existing, command) {
		return
	}
	hooks[event] = append(existing, group)
}

// injectCodexHooks writes Codex lifecycle hooks into its hooks.json map for
// status tracking: UserPromptSubmit + PreToolUse → active (a turn started),
// Stop → idle (turn complete). Codex runs each command via shell and treats
// "exit 0 with no output" as success, so the shared status writers are reused
// directly (no stdout contract, unlike Gemini). hooks.json nests the event→groups
// map under a top-level "hooks" key (mirroring config.toml's `[hooks]`); preserves
// any hooks already present.
func injectCodexHooks(root map[string]any) {
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	group := func(command string) map[string]any {
		return map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": command}},
		}
	}
	appendHookGroup(hooks, "UserPromptSubmit", statusActiveCommand, group(statusActiveCommand))
	appendHookGroup(hooks, "PreToolUse", statusActiveCommand, group(statusActiveCommand))
	appendHookGroup(hooks, "Stop", statusIdleCommand, group(statusIdleCommand))
	root["hooks"] = hooks
}

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

	idleGroup := map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": statusIdleCommand}},
	}
	activeGroup := map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": statusActiveCommand}},
	}
	// Stop → idle; PreToolUse + UserPromptSubmit → active (the latter closes the
	// window where the agent looks idle between prompt submit and the first tool
	// call). Idempotent so re-applies (create+start, restart) don't accumulate.
	appendHookGroup(hooks, "Stop", statusIdleCommand, idleGroup)
	appendHookGroup(hooks, "PreToolUse", statusActiveCommand, activeGroup)
	appendHookGroup(hooks, "UserPromptSubmit", statusActiveCommand, activeGroup)

	settings["hooks"] = hooks
}
