// ABOUTME: Agent command + model resolution helpers: resolving/validating models,
// ABOUTME: building agent command strings, and reading prompts from various sources.
package invocation

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/yoerrors"
)

// ResolveModel expands a model alias. User-configured aliases (from
// config.yaml model_aliases) take priority over agent built-in aliases.
func ResolveModel(agentDef *agent.Definition, model string, userAliases map[string]string) string {
	if model == "" {
		return ""
	}
	if userAliases != nil {
		if resolved, ok := userAliases[model]; ok {
			return resolved
		}
	}
	if agentDef.ModelAliases != nil {
		if resolved, ok := agentDef.ModelAliases[model]; ok {
			return resolved
		}
	}
	return model
}

// ApplyModelPrefix adds a provider prefix to the model name when needed.
// For example, when using aider with OLLAMA_API_BASE, the model must be
// prefixed with "ollama_chat/" for litellm to route it correctly. The trigger
// env var is looked up in hostEnv (the caller's host-environment snapshot) and
// in configEnv (the profile env); the library never reads os.Environ (§12).
func ApplyModelPrefix(agentDef *agent.Definition, model string, configEnv map[string]string, hostEnv config.Layout) string {
	if model == "" || strings.Contains(model, "/") {
		return model
	}
	if agentDef.ModelPrefixes == nil {
		return model
	}
	triggerKeys := make([]string, 0, len(agentDef.ModelPrefixes))
	for envVar := range agentDef.ModelPrefixes {
		triggerKeys = append(triggerKeys, envVar)
	}
	hostTriggers := hostEnv.Env().EnvForAgentCredentials(triggerKeys)
	for envVar, prefix := range agentDef.ModelPrefixes {
		if hostTriggers[envVar] != "" || configEnv[envVar] != "" {
			return prefix + model
		}
	}
	return model
}

// ValidateModel checks agent-specific model format requirements.
// Returns an error if the model format is invalid for the given agent.
func ValidateModel(agentDef *agent.Definition, resolvedModel string, originalModel string) error {
	// Skip validation if no model specified
	if resolvedModel == "" {
		return nil
	}

	// OpenCode requires provider/model format (e.g., "openai/gpt-4o", "anthropic/claude-sonnet-4-20250514")
	if agentDef.Type == "opencode" {
		if !strings.Contains(resolvedModel, "/") {
			return fmt.Errorf(
				"opencode requires models in provider/model format (e.g., \"openai/gpt-4o\", \"anthropic/claude-sonnet-4-20250514\")\n\n"+
					"You specified: %q\n"+
					"Resolved to: %q\n\n"+
					"To fix this:\n"+
					"  1. Configure providers on your HOST (install opencode, run /connect)\n"+
					"     OR set API key env vars: export OPENAI_API_KEY=sk-...\n"+
					"  2. Use --model with provider prefix: --model openai/gpt-4o\n\n"+
					"Valid examples:\n"+
					"  openai/gpt-4o\n"+
					"  openai/gpt-4o-mini\n"+
					"  anthropic/claude-sonnet-4-20250514\n"+
					"  opencode/gpt-5.1-codex (OpenCode Zen)\n\n"+
					"Note: OpenCode config must be set up on your host machine.\n"+
					"yoloAI will automatically seed it into containers",
				originalModel,
				resolvedModel,
			)
		}
	}

	return nil
}

// BuildAgentCommand constructs the full agent command string for config.json.
// Arg priority (left to right, last flag wins): base cmd → model flag → agentArgs → passthrough.
func BuildAgentCommand(agentDef *agent.Definition, model string, prompt string, agentArgs string, passthrough []string) string {
	var cmd string

	if agentDef.PromptMode == agent.PromptModeHeadless && prompt != "" {
		escaped := shellEscapeForDoubleQuotes(prompt)
		cmd = strings.ReplaceAll(agentDef.HeadlessCmd, "PROMPT", escaped)
	} else {
		cmd = agentDef.InteractiveCmd
		if model != "" && agentDef.ModelFlag != "" {
			cmd += " " + agentDef.ModelFlag + " " + model
		}
	}

	if agentArgs != "" {
		cmd += " " + agentArgs
	}

	for _, arg := range passthrough {
		cmd += " " + arg
	}

	return cmd
}

// SanitizeTunnelName converts a sandbox name to a valid VS Code tunnel name.
// VS Code tunnel names are limited to 20 characters, lowercase alphanumeric
// and hyphens, with no leading or trailing hyphens.
func SanitizeTunnelName(name string) string {
	name = strings.ToLower(name)
	// Replace underscores and dots with hyphens (sandbox names allow both)
	name = strings.NewReplacer("_", "-", ".", "-").Replace(name)
	// Truncate to 20 chars
	if len(name) > 20 {
		name = name[:20]
	}
	// Strip trailing hyphens introduced by truncation
	name = strings.TrimRight(name, "-")
	// Ensure minimum 3 chars (pad with 'x' if needed)
	for len(name) < 3 {
		name += "x"
	}
	return name
}

// ResolveDetectors computes the ordered detector stack based on the agent's
// idle support capabilities. The returned list is stored in config.json and
// used by the in-container status monitor to determine which detection
// strategies to run (in priority order).
func ResolveDetectors(idle agent.IdleSupport) []string {
	var detectors []string

	// Hook detector: highest priority, agent writes status.json directly.
	if idle.Hook {
		detectors = append(detectors, "hook")
	}

	// Wchan detector: high confidence, works for any process-based agent.
	if idle.WchanApplicable {
		detectors = append(detectors, "wchan")
	}

	// Ready pattern: medium confidence, checks tmux pane for prompt text.
	if idle.ReadyPattern != "" {
		detectors = append(detectors, "ready_pattern")
	}

	// Context signal: medium confidence, checks for agent-emitted markers.
	if idle.ContextSignal {
		detectors = append(detectors, "context_signal")
	}

	// Output stability: low confidence fallback, always added when any
	// other detector exists (provides a last-resort signal).
	if len(detectors) > 0 {
		detectors = append(detectors, "output_stability")
	}

	return detectors
}

// Idle-detection modes (the per-agent mode selector — session-layer.md §Tier-2).
// The status monitor reads idle_mode from runtime-config.json and branches.
const (
	// IdleModeHookAuthoritative: idle/active is owned EXCLUSIVELY by the agent's
	// turn hook (it writes agent-status.json directly); the monitor runs no
	// heuristic detectors for active/idle (only pane-death → done + a respawn
	// idle seed). This removes the startup blip, which is a heuristic artifact.
	IdleModeHookAuthoritative = "hook-authoritative"
	// IdleModeHeuristicOnly: the agent-agnostic heuristic detector stack (wchan,
	// ready-pattern, …). For agents without a turn hook.
	IdleModeHeuristicOnly = "heuristic-only"
	// A future IdleModeHookAssisted (hook + heuristic backstop for a missed hook)
	// slots in here without reworking the selector — the design-now seam.
)

// ResolveIdleMode selects the per-agent idle-detection mode. Hook-capable agents
// trust the authoritative hook exclusively (heuristics would only re-introduce
// the startup blip and add nothing a hook-capable agent needs — crash/hang is
// covered by liveness, not heuristics); others fall back to the heuristic stack.
func ResolveIdleMode(idle agent.IdleSupport) string {
	if idle.Hook {
		return IdleModeHookAuthoritative
	}
	return IdleModeHeuristicOnly
}

// ReadPrompt reads the prompt from --prompt, --prompt-file, or stdin ("-").
// homeDir is used to expand leading "~" in the promptFile path. stdin is the
// reader the "-" sentinel pulls from — threaded from the Engine's input
// (the CLI wires os.Stdin there; embedders supply their own), so the library
// never reaches for the process's stdin directly (§12).
// env is the curated interpolation map for ${VAR} expansion; pass
// layout.Env().EnvForConfigInterpolation().
func ReadPrompt(prompt, promptFile, homeDir string, env map[string]string, stdin io.Reader) (string, error) {
	if prompt != "" && promptFile != "" {
		return "", yoerrors.NewUsageError("--prompt and --prompt-file are mutually exclusive")
	}

	if prompt == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read prompt from stdin: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}

	if prompt != "" {
		return prompt, nil
	}

	if promptFile == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read prompt from stdin: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}

	if promptFile != "" {
		promptFile, err := config.ExpandPath(promptFile, homeDir, env)
		if err != nil {
			return "", fmt.Errorf("expand prompt file path: %w", err)
		}
		data, err := os.ReadFile(promptFile) //nolint:gosec // G304: path is from user-provided --prompt-file flag
		if err != nil {
			return "", fmt.Errorf("read prompt file: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}

	return "", nil
}

// shellEscapeForDoubleQuotes escapes a string for embedding inside
// double quotes in a shell command.
func shellEscapeForDoubleQuotes(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"`", "\\`",
		`$`, `\$`,
	)
	return r.Replace(s)
}
