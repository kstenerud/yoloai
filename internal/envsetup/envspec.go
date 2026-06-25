// ABOUTME: Agent-agnostic description of sandbox staging inputs; consumed by
// ABOUTME: the seed-path functions without importing internal/agent.
package envsetup

import "os"

// EnvSpec is the agent-agnostic description of a sandbox's agent-specific
// staging inputs. The orchestrator compiles it from an agent.Definition
// (see internal/orchestrator/envspec); envsetup itself never imports agent.
type EnvSpec struct {
	// Credentials — consumed by the secrets/auth-detection stage (not yet
	// migrated to EnvSpec; populated here for the upcoming step).
	APIKeyEnvVars   []string
	AuthHintEnvVars []string

	// SeedFiles are host files copied into the sandbox before launch.
	SeedFiles []SeedFile

	// StateRelPath is the agent state dir relative to /home/yoloai (e.g.
	// ".claude"); "" when the agent has no state dir under /home/yoloai.
	StateRelPath string

	// HasStateDir reports whether the agent declares a state dir at all
	// (agentDef.StateDir != ""); gates agent_files copying.
	HasStateDir bool

	// AgentFilesExclude are globs skipped when copying agent_files (string form).
	AgentFilesExclude []string

	// SettingsPatches are the resolved settings.json mutations to apply. For a
	// normal agent this is one entry targeting the agent-runtime dir; for the
	// shell agent the compiler expands it to one entry per real agent targeting
	// that agent's home-seed subdir. envsetup applies them blindly.
	SettingsPatches []SettingsPatch

	// ShortLivedOAuthWarning emits the OAuth-token warning when auth files were copied.
	ShortLivedOAuthWarning bool
}

// SeedFile mirrors agent.SeedFile as plain data (no agent import).
type SeedFile struct {
	HostPath        string
	TargetPath      string
	AuthOnly        bool
	HomeDir         bool
	KeychainService string
	OwnerAPIKeys    []string
	Executable      bool
}

// SettingsPatch is one resolved settings.json mutation.
type SettingsPatch struct {
	RelDir  string               // dir under sandboxDir holding settings.json
	DirPerm os.FileMode          // perms for MkdirAllPerm of RelDir
	Apply   func(map[string]any) // mutate settings.json in place
}
