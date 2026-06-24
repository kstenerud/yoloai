// ABOUTME: File-defined agent loader — parses ~/.yoloai/agents/*.yaml into
// ABOUTME: Definition values and registers them in the global agent registry.
package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

// validAgentTypeName is the pattern an AgentType value must match.
// Lowercase letters, digits, and hyphens; must start with a letter.
var validAgentTypeName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// builtInAgentNames is the frozen set of agent names registered by init().
// Populated once (in init) before any goroutines start. File agents may
// not shadow these names; file agents may replace each other.
var builtInAgentNames map[string]bool

func init() {
	// Capture the built-in set after agent.go's init() has finished registering
	// all built-in agents (including the shell agent). File agents may not shadow
	// these names. No lock is needed: init() is single-threaded.
	builtInAgentNames = make(map[string]bool, len(agents))
	for name := range agents {
		builtInAgentNames[name] = true
	}
}

// FileAgentSeedFile mirrors SeedFile for YAML deserialization.
// Only data-expressible fields are included; code-only fields
// (OwnerAPIKeys, KeychainService, Executable) require Go code and are
// omitted — they remain zero-valued in the resulting SeedFile.
type FileAgentSeedFile struct {
	HostPath   string `yaml:"host_path"`
	TargetPath string `yaml:"target_path"`
	AuthOnly   bool   `yaml:"auth_only"`
	HomeDir    bool   `yaml:"home_dir"`
}

// FileAgentIdleSupport mirrors IdleSupport for YAML deserialization.
// Hook is intentionally excluded: wiring a hook requires agent-specific
// setup code that cannot be expressed in a data-only YAML file.
type FileAgentIdleSupport struct {
	ReadyPattern    string `yaml:"ready_pattern"`
	ContextSignal   bool   `yaml:"context_signal"`
	WchanApplicable bool   `yaml:"wchan_applicable"`
}

// FileAgentSpec is the YAML schema for a file-defined agent.
// Drop a file at ~/.yoloai/agents/<name>.yaml to register a new agent
// without writing Go code.
//
// File-defined agents are DATA-ONLY by construction: a YAML file cannot carry
// a Go func, so ApplySettings, ShortLivedOAuthWarning, and SeedsAllAgents
// (which require code or code-level reasoning about other agents) are not
// expressible here. They remain nil/false in the resulting Definition.
type FileAgentSpec struct {
	// Type is the agent's unique identifier (required, lowercase kebab-case,
	// must not collide with a built-in name).
	Type string `yaml:"type"`

	// Description is a short human-readable description of the agent.
	Description string `yaml:"description"`

	// InteractiveCmd is the shell command that launches the agent interactively.
	// At least one of InteractiveCmd or HeadlessCmd is required.
	InteractiveCmd string `yaml:"interactive_cmd"`

	// HeadlessCmd is the shell command that launches the agent with an initial
	// prompt injected. The literal string "PROMPT" is replaced at runtime.
	HeadlessCmd string `yaml:"headless_cmd"`

	// PromptMode determines how the agent receives its initial prompt.
	// Valid values: "interactive" (default) or "headless".
	PromptMode string `yaml:"prompt_mode"`

	// APIKeyEnvVars lists env var names that carry the agent's API key.
	APIKeyEnvVars []string `yaml:"api_key_env_vars"`

	// AuthHintEnvVars lists env vars indicating auth is configured without a
	// cloud API key (e.g. local model servers).
	AuthHintEnvVars []string `yaml:"auth_hint_env_vars"`

	// AuthOptional, if true, treats missing auth as a warning rather than an error.
	AuthOptional bool `yaml:"auth_optional"`

	// SeedFiles lists host files to copy into the agent's state directory.
	SeedFiles []FileAgentSeedFile `yaml:"seed_files"`

	// StateDir is the absolute in-container path to the agent's state directory
	// (e.g. "/home/yoloai/.myagent/"). Leave empty if the agent has no state dir.
	StateDir string `yaml:"state_dir"`

	// SubmitSequence is the tmux key sequence used to submit a prompt
	// (e.g. "Enter", "Enter Enter").
	SubmitSequence string `yaml:"submit_sequence"`

	// StartupDelayMS is the startup delay in milliseconds (e.g. 3000 for 3s).
	StartupDelayMS int `yaml:"startup_delay_ms"`

	// Idle describes idle-detection capabilities the agent can emit.
	Idle FileAgentIdleSupport `yaml:"idle"`

	// ModelFlag is the CLI flag used to pass the model name (e.g. "--model").
	ModelFlag string `yaml:"model_flag"`

	// ModelAliases maps short alias names to full model identifiers.
	ModelAliases map[string]string `yaml:"model_aliases"`

	// ModelPrefixes maps env var names to model name prefixes.
	ModelPrefixes map[string]string `yaml:"model_prefixes"`

	// NetworkAllowlist lists domains allowed when the sandbox runs in isolated mode.
	NetworkAllowlist []string `yaml:"network_allowlist"`

	// ContextFile is the filename in StateDir used for sandbox context injection
	// (e.g. "AGENTS.md"). Leave empty if the agent does not support context injection.
	ContextFile string `yaml:"context_file"`

	// AgentFilesExclude lists glob patterns to skip when copying agent_files.
	AgentFilesExclude []string `yaml:"agent_files_exclude"`
}

// toDefinition converts a validated FileAgentSpec into a *Definition.
// ApplySettings, ShortLivedOAuthWarning, and SeedsAllAgents are not set
// (they require Go code; file agents are data-only by construction).
func (s *FileAgentSpec) toDefinition() *Definition {
	seedFiles := make([]SeedFile, len(s.SeedFiles))
	for i, sf := range s.SeedFiles {
		seedFiles[i] = SeedFile{
			HostPath:   sf.HostPath,
			TargetPath: sf.TargetPath,
			AuthOnly:   sf.AuthOnly,
			HomeDir:    sf.HomeDir,
		}
	}

	pm := PromptMode(s.PromptMode)
	if pm == "" {
		pm = PromptModeInteractive
	}

	return &Definition{
		Type:            AgentType(s.Type),
		Description:     s.Description,
		InteractiveCmd:  s.InteractiveCmd,
		HeadlessCmd:     s.HeadlessCmd,
		PromptMode:      pm,
		APIKeyEnvVars:   s.APIKeyEnvVars,
		AuthHintEnvVars: s.AuthHintEnvVars,
		AuthOptional:    s.AuthOptional,
		SeedFiles:       seedFiles,
		StateDir:        s.StateDir,
		SubmitSequence:  s.SubmitSequence,
		StartupDelay:    time.Duration(s.StartupDelayMS) * time.Millisecond,
		Idle: IdleSupport{
			ReadyPattern:    s.Idle.ReadyPattern,
			ContextSignal:   s.Idle.ContextSignal,
			WchanApplicable: s.Idle.WchanApplicable,
		},
		ModelFlag:         s.ModelFlag,
		ModelAliases:      s.ModelAliases,
		ModelPrefixes:     s.ModelPrefixes,
		NetworkAllowlist:  s.NetworkAllowlist,
		ContextFile:       s.ContextFile,
		AgentFilesExclude: s.AgentFilesExclude,
	}
}

// validateFileAgentSpec returns an error if the spec is missing required fields,
// uses an invalid format, or collides with a name in builtIns.
func validateFileAgentSpec(s *FileAgentSpec, filePath string, builtIns map[string]bool) error {
	base := filepath.Base(filePath)
	if s.Type == "" {
		return fmt.Errorf("file-defined agent %q: type is required", base)
	}
	if !validAgentTypeName.MatchString(s.Type) {
		return fmt.Errorf("file-defined agent %q: type %q must match [a-z][a-z0-9-]* (lowercase kebab-case)", base, s.Type)
	}
	if s.InteractiveCmd == "" && s.HeadlessCmd == "" {
		return fmt.Errorf("file-defined agent %q (type %q): at least one of interactive_cmd or headless_cmd is required", base, s.Type)
	}
	if s.PromptMode != "" &&
		s.PromptMode != string(PromptModeInteractive) &&
		s.PromptMode != string(PromptModeHeadless) {
		return fmt.Errorf("file-defined agent %q (type %q): prompt_mode must be %q or %q, got %q",
			base, s.Type, PromptModeInteractive, PromptModeHeadless, s.PromptMode)
	}
	if builtIns[s.Type] {
		return fmt.Errorf("file-defined agent %q: type %q is a reserved built-in name", base, s.Type)
	}
	return nil
}

// LoadFileAgents parses all *.yaml and *.yml files in dir and returns the
// resulting agent Definitions. A missing dir is not an error (returns nil, nil).
// Returns a descriptive error naming the file on any parse or validation failure.
//
// LoadFileAgents validates that no file agent shadows a built-in name. The
// built-in set is frozen after package init() and requires no lock to read.
func LoadFileAgents(dir string) ([]*Definition, error) {
	// Collect candidate files.
	var files []string
	for _, pat := range []string{
		filepath.Join(dir, "*.yaml"),
		filepath.Join(dir, "*.yml"),
	} {
		matches, err := filepath.Glob(pat)
		if err != nil {
			return nil, fmt.Errorf("loading file-defined agents from %q: %w", dir, err)
		}
		files = append(files, matches...)
	}

	// A missing dir returns no matches from Glob (nil, nil). Distinguish "dir
	// exists but empty" from "dir does not exist" only to give a clear return.
	if len(files) == 0 {
		if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		// Dir exists but contains no YAML files — not an error.
		return nil, nil
	}

	// builtInAgentNames is written once in init() before any goroutine starts;
	// it is safe to read without a lock.
	builtIns := builtInAgentNames

	var defs []*Definition
	for _, f := range files {
		data, err := os.ReadFile(f) //nolint:gosec // G304: f is a glob match under AgentsDir(), a trusted data dir (DataDir/agents/)
		if err != nil {
			return nil, fmt.Errorf("file-defined agent %q: %w", filepath.Base(f), err)
		}
		var spec FileAgentSpec
		if err := yaml.Unmarshal(data, &spec); err != nil {
			return nil, fmt.Errorf("file-defined agent %q: YAML parse error: %w", filepath.Base(f), err)
		}
		if err := validateFileAgentSpec(&spec, f, builtIns); err != nil {
			return nil, err
		}
		defs = append(defs, spec.toDefinition())
	}
	return defs, nil
}

// RegisterFileAgents loads file-defined agents from dir and registers them in
// the global agent registry. A missing dir is silently ignored.
// Returns an error if any file is malformed, fails validation, or uses a
// reserved built-in name.
//
// File-defined agents may replace a previously-registered file agent with the
// same type name (idempotent for repeated calls); overriding a built-in is
// always rejected.
//
// Single-process / single-data-dir assumption: RegisterFileAgents writes to a
// package-global registry. A future multi-data-dir daemon would need a
// per-Client registry rather than this shared global.
func RegisterFileAgents(dir string) error {
	// Load and validate outside the write lock (IO + YAML work should not hold
	// the write lock). Built-in collision is detected here against the frozen
	// builtInAgentNames set; no re-check needed inside the lock because that set
	// never changes after init().
	defs, err := LoadFileAgents(dir)
	if err != nil {
		return err
	}
	if len(defs) == 0 {
		return nil
	}

	agentsMu.Lock()
	defer agentsMu.Unlock()
	for _, def := range defs {
		agents[string(def.Type)] = def
	}
	return nil
}
