// ABOUTME: Profile/config resolution pipeline — resolves the profile chain,
// ABOUTME: merges config values, and applies CLI overrides into a profileResult
// ABOUTME: ready for sandbox creation.
package create

import (
	"context"
	"fmt"
	"log/slog"
	"maps"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/orchestrator/profiles"
	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/yoerrors"
)

// profileResult holds resolved profile configuration after chain resolution
// and config merging.
type profileResult struct {
	name               string
	imageRef           string
	env                map[string]string
	agentArgs          map[string]string
	agentFiles         *config.AgentFilesConfig
	resources          *config.ResourceLimits
	mounts             []string
	capAdd             []string
	devices            []string
	setup              []string
	autoCommitInterval int
	isolation          runtime.IsolationMode
	isolationExplicit  bool // true when isolation was set via --isolation flag (not config/profile default)
	userAliases        map[string]string
	// Archetype-specific resolved fields
	archetypeDockerDRequired bool // true when archetype requires dockerd auto-start
}

// resolveProfileConfig resolves the profile chain, merges config, and builds
// the profile image if needed. Returns a profileResult with all merged values.
func resolveProfileConfig(ctx context.Context, d state.Deps, opts *Options, agentDef **agent.Definition, ycfg *config.YoloaiConfig, gcfg *config.GlobalConfig) (*profileResult, error) {
	pr := &profileResult{
		env:                ycfg.Env,
		agentArgs:          ycfg.AgentArgs,
		agentFiles:         ycfg.AgentFiles,
		autoCommitInterval: ycfg.AutoCommitInterval,
		userAliases:        gcfg.ModelAliases,
	}

	if opts.Profile == "" {
		// No profile specified: use base image
		pr.imageRef = "yoloai-base"
		return pr, nil
	}

	if err := config.ValidateProfileName(opts.Profile); err != nil {
		return nil, err
	}
	chain, err := config.ResolveProfileChain(d.Layout, opts.Profile)
	if err != nil {
		return nil, err
	}
	merged, err := config.MergeProfileChain(d.Layout, ycfg, chain)
	if err != nil {
		return nil, fmt.Errorf("merge profile chain: %w", err)
	}
	backend := d.Runtime.Descriptor().Type
	if err := config.ValidateProfileBackend(merged.Backend, string(backend)); err != nil {
		return nil, err
	}

	homeDir := d.Layout.HomeDir
	if err := applyMergedProfileToOpts(opts, agentDef, merged, pr, ycfg.Agent, homeDir, d.Layout.Env().EnvForConfigInterpolation()); err != nil {
		return nil, err
	}

	pr.name = opts.Profile
	pr.imageRef = config.ResolveProfileImage(d.Layout, opts.Profile, chain)

	// Build profile image if needed (Docker only)
	logger := slog.Default()
	if err := profiles.EnsureProfileImage(ctx, d.Runtime, d.Layout, opts.Profile, profiles.AutoBuildSecrets(d.Layout.HomeDir), outputFor(opts.Output), logger, false); err != nil {
		return nil, fmt.Errorf("build profile image: %w", err)
	}

	return pr, nil
}

// applyMergedProfileToOpts applies merged profile values to opts and pr.
// homeDir is used for ~ expansion in profile workdir and directory paths.
// env is the curated interpolation map for ${VAR} expansion; pass
// layout.Env().EnvForConfigInterpolation().
// baseAgent is the agent name from the base config (ycfg.Agent), used to
// detect whether the CLI override has been applied.
func applyMergedProfileToOpts(opts *Options, agentDef **agent.Definition, merged *config.MergedConfig, pr *profileResult, baseAgent string, homeDir string, env map[string]string) error {
	// Apply merged values where CLI didn't override
	if opts.Agent == baseAgent && merged.Agent != "" {
		opts.Agent = merged.Agent
		def := agent.GetAgent(opts.Agent)
		if def == nil {
			return yoerrors.NewUsageError("unknown agent from profile: %s", opts.Agent)
		}
		*agentDef = def
	}
	if opts.Model == "" && merged.Model != "" {
		opts.Model = merged.Model
	}

	pr.env = merged.Env
	pr.agentArgs = merged.AgentArgs
	pr.agentFiles = merged.AgentFiles

	if merged.Resources != nil {
		r := *merged.Resources
		pr.resources = &r
	}

	// Profile workdir: use if CLI didn't provide one
	if opts.Workdir.Path == "" && merged.Workdir != nil {
		wdPath, err := config.ExpandPath(merged.Workdir.Path, homeDir, env)
		if err != nil {
			return fmt.Errorf("expand profile workdir path: %w", err)
		}
		opts.Workdir = DirSpec{
			Path:      wdPath,
			Mode:      DirMode(merged.Workdir.Mode),
			MountPath: merged.Workdir.Mount,
			// --copy-strict (already on opts.Workdir) OR the profile's copy_strict.
			StripHistory: opts.Workdir.StripHistory || merged.Workdir.CopyStrict,
		}
	}

	// Profile directories: prepend before CLI aux dirs
	if err := prependProfileDirs(opts, merged.Directories, homeDir, env); err != nil {
		return err
	}

	// Profile ports: additive
	opts.Ports = append(merged.Ports, opts.Ports...)

	// Network: apply merged config as defaults (CLI flags override later)
	if merged.Network != nil && opts.Network == NetworkModeDefault {
		if merged.Network.Isolated {
			opts.Network = NetworkModeIsolated
		}
		opts.NetworkAllow = append(merged.Network.Allow, opts.NetworkAllow...)
	}

	pr.mounts = merged.Mounts
	pr.capAdd = merged.CapAdd
	pr.devices = merged.Devices
	pr.setup = merged.Setup
	pr.autoCommitInterval = merged.AutoCommitInterval
	pr.isolation = runtime.IsolationMode(merged.Isolation)

	return nil
}

// prependProfileDirs prepends profile directory specs before the CLI aux dirs.
// homeDir is used for ~ expansion in profile directory paths.
// env is the curated interpolation map for ${VAR} expansion; pass
// layout.Env().EnvForConfigInterpolation().
func prependProfileDirs(opts *Options, profileDirs []config.ProfileDir, homeDir string, env map[string]string) error {
	var dirs []DirSpec
	for _, pd := range profileDirs {
		dirPath, err := config.ExpandPath(pd.Path, homeDir, env)
		if err != nil {
			return fmt.Errorf("expand profile directory path: %w", err)
		}
		dirs = append(dirs, DirSpec{
			Path:      dirPath,
			Mode:      DirMode(pd.Mode),
			MountPath: pd.Mount,
		})
	}
	opts.AuxDirs = append(dirs, opts.AuxDirs...)
	return nil
}

// applyConfigDefaults fills in values from base config when the profile didn't
// set them, and applies CLI overrides for resources.
func applyConfigDefaults(opts *Options, ycfg *config.YoloaiConfig, pr *profileResult) error {
	if opts.Profile == "" {
		applyBaseConfigDefaults(opts, ycfg, pr)
	}
	applyBaseResourceDefaults(ycfg, pr)
	return applyCLIOverrides(opts, pr)
}

// applyBaseConfigDefaults applies mounts, ports, caps, and network from base
// config when no profile is active.
func applyBaseConfigDefaults(opts *Options, ycfg *config.YoloaiConfig, pr *profileResult) {
	if len(ycfg.Mounts) > 0 {
		pr.mounts = ycfg.Mounts
	}
	if len(ycfg.Ports) > 0 {
		opts.Ports = append(ycfg.Ports, opts.Ports...)
	}
	pr.capAdd = ycfg.CapAdd
	pr.devices = ycfg.Devices
	pr.setup = ycfg.Setup
	pr.isolation = runtime.IsolationMode(ycfg.Isolation)

	if ycfg.Network != nil && opts.Network == NetworkModeDefault {
		if ycfg.Network.Isolated {
			opts.Network = NetworkModeIsolated
		}
		opts.NetworkAllow = append(ycfg.Network.Allow, opts.NetworkAllow...)
	}
}

// applyBaseResourceDefaults applies resource limits from base config when the
// profile didn't set them.
func applyBaseResourceDefaults(ycfg *config.YoloaiConfig, pr *profileResult) {
	if pr.resources == nil && ycfg.Resources != nil {
		r := *ycfg.Resources
		pr.resources = &r
	}
}

// applyCLIOverrides applies CLI flag overrides for resources, isolation, and env.
func applyCLIOverrides(opts *Options, pr *profileResult) error {
	if opts.CPUs != "" {
		if pr.resources == nil {
			pr.resources = &config.ResourceLimits{}
		}
		pr.resources.CPUs = opts.CPUs
	}
	if opts.Memory != "" {
		if pr.resources == nil {
			pr.resources = &config.ResourceLimits{}
		}
		pr.resources.Memory = opts.Memory
	}

	if opts.Isolation != "" {
		if err := config.ValidateIsolationMode(string(opts.Isolation)); err != nil {
			return err
		}
		pr.isolation = opts.Isolation
		pr.isolationExplicit = true
	}

	if len(opts.Env) > 0 {
		if pr.env == nil {
			pr.env = make(map[string]string)
		}
		maps.Copy(pr.env, opts.Env)
	}

	return nil
}
