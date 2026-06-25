// ABOUTME: Boundary compiler that converts an agent.Definition into an
// ABOUTME: envsetup.EnvSpec; the only place internal/agent meets envsetup.
package envspec

import (
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/envsetup"
	"github.com/kstenerud/yoloai/internal/store"
)

// BuildEnvSpec compiles an agent.Definition into an agent-agnostic
// envsetup.EnvSpec. This is the orchestrator boundary where agent declarations
// become staging data; internal/envsetup never imports internal/agent. The
// design (D91) names this "the agent layer compiles its declarations"; placing
// the compiler here (not as a method on agent.Definition) keeps internal/agent a
// clean leaf and avoids a cycle through the shared sandbox-state/perms types.
func BuildEnvSpec(def *agent.Definition) envsetup.EnvSpec {
	return envsetup.EnvSpec{
		APIKeyEnvVars:          def.APIKeyEnvVars,
		AuthHintEnvVars:        def.AuthHintEnvVars,
		SeedFiles:              toSeedFiles(def.SeedFiles),
		StateRelPath:           def.StateRelPath(),
		HasStateDir:            def.StateDir != "",
		AgentFilesExclude:      def.AgentFilesExclude,
		ContextFile:            def.ContextFile,
		SettingsPatches:        settingsPatches(def),
		ShortLivedOAuthWarning: def.ShortLivedOAuthWarning,
	}
}

func toSeedFiles(in []agent.SeedFile) []envsetup.SeedFile {
	if in == nil {
		return nil
	}
	out := make([]envsetup.SeedFile, len(in))
	for i, sf := range in {
		out[i] = envsetup.SeedFile{
			HostPath:        sf.HostPath,
			TargetPath:      sf.TargetPath,
			AuthOnly:        sf.AuthOnly,
			HomeDir:         sf.HomeDir,
			KeychainService: sf.KeychainService,
			OwnerAPIKeys:    sf.OwnerAPIKeys,
			Executable:      sf.Executable,
		}
	}
	return out
}

// settingsPatches resolves the agent's settings.json mutations. Mirrors the old
// EnsureContainerSettings/ensureShellContainerSettings split exactly: a normal
// agent yields one patch into the agent-runtime dir; the shell agent
// (SeedsAllAgents) yields one patch per real agent into its home-seed subdir.
func settingsPatches(def *agent.Definition) []envsetup.SettingsPatch {
	if def.SeedsAllAgents {
		var ps []envsetup.SettingsPatch
		for _, name := range agent.RealAgents() {
			d := agent.GetAgent(name)
			if d.StateDir == "" || d.ApplySettings == nil {
				continue
			}
			ps = append(ps, envsetup.SettingsPatch{
				RelDir:  filepath.Join("home-seed", filepath.Base(d.StateDir)),
				DirPerm: 0o750,
				Apply:   d.ApplySettings,
			})
		}
		return ps
	}
	if def.StateDir == "" || def.ApplySettings == nil {
		return nil
	}
	return []envsetup.SettingsPatch{{
		RelDir:  store.AgentRuntimeDir,
		DirPerm: store.Perms().Dir,
		Apply:   def.ApplySettings,
	}}
}
