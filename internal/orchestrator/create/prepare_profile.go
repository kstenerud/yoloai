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
		pr.imageRef = config.BaseImage
		return pr, nil
	}

	if err := config.ValidateProfileName(opts.Profile); err != nil {
		return nil, err
	}
	chain, err := config.ResolveProfileChain(d.Layout, opts.Profile)
	if err != nil {
		return nil, err
	}
	// A profile is self-contained (config.md: "Profiles are self-contained
	// ... Personal defaults do not carry into profiles — no exceptions").
	// The merge base is therefore the baked-in defaults, never the user's
	// defaults/config.yaml — that would carry personal env, cap_add,
	// isolation, network.allow, and everything else in ycfg into a profile
	// that is supposed to behave identically for everyone (DF207).
	bakedIn, err := config.LoadBakedInDefaults()
	if err != nil {
		return nil, fmt.Errorf("load baked-in defaults: %w", err)
	}
	merged, err := config.MergeProfileChain(d.Layout, bakedIn, chain)
	if err != nil {
		return nil, fmt.Errorf("merge profile chain: %w", err)
	}
	backend := d.Runtime.Descriptor().Type
	if err := config.ValidateProfileBackend(merged.Backend, string(backend)); err != nil {
		return nil, err
	}

	// baseAgent is "what the agent would be if the user passed no --agent", and
	// it must be resolved from the SAME layers the CLI resolved opts.Agent from,
	// or the comparison in applyMergedProfileToOpts compares two different
	// questions. It did until 2026-08-15 (DF213): this passed ycfg.Agent, and
	// ycfg comes from config.LoadConfig — the user's file ONLY, with no baked-in
	// merge — while the CLI's ResolveAgentFromConfig uses LoadDefaultsConfig,
	// which does merge. A fresh install's defaults/config.yaml has every line
	// commented out (GenerateScaffoldConfig), so ycfg.Agent was "" while
	// opts.Agent was "claude", the guard never fired, and a profile's agent: key
	// silently did nothing for anyone who had not hand-edited that file.
	//
	// Residual, and it is why D143's provenance is the real fix: an explicit
	// --agent that happens to equal the resolved default is still
	// indistinguishable from no flag at all, so the profile wins there. That is
	// a comparison trick standing in for provenance the pipeline does not carry.
	baseAgent := ycfg.Agent
	if baseAgent == "" {
		baseAgent = bakedIn.Agent
	}
	// Same mismatch, same fix, for --model (DF209): the CLI resolves
	// opts.Model as flag-else-config, so a personal model: made opts.Model
	// non-empty and the profile's model never applied.
	baseModel := ycfg.Model
	if baseModel == "" {
		baseModel = bakedIn.Model
	}

	homeDir := d.Layout.HomeDir
	if err := applyMergedProfileToOpts(opts, agentDef, merged, pr, baseAgent, baseModel, homeDir, d.Layout.Env().EnvForConfigInterpolation()); err != nil {
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
func applyMergedProfileToOpts(opts *Options, agentDef **agent.Definition, merged *config.MergedConfig, pr *profileResult, baseAgent, baseModel string, homeDir string, env map[string]string) error {
	// Apply merged values where CLI didn't override
	if opts.Agent == baseAgent && merged.Agent != "" {
		opts.Agent = merged.Agent
		def := agent.GetAgent(opts.Agent)
		if def == nil {
			return yoerrors.NewUsageError("unknown agent from profile: %s", opts.Agent)
		}
		*agentDef = def
	}
	// The profile layer decides the model whenever the CLI did not: assign
	// merged.Model even when it is empty, so a personal `model:` is *cleared*
	// rather than surviving into a profile the user expects to be
	// self-contained (DF209). An explicit --model differs from baseModel and
	// is kept.
	if opts.Model == "" || opts.Model == baseModel {
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

	// Network: apply merged config as defaults (CLI flags override later).
	// The allowlist merge is unconditional — only the mode *promotion* to
	// isolated depends on the mode still being unset. Gating the whole block
	// on opts.Network == NetworkModeDefault silently discarded a profile's
	// network.allow whenever the mode had already been set, including when
	// --network-allow itself set it (DF206).
	if merged.Network != nil {
		if opts.Network == NetworkModeDefault && merged.Network.Isolated {
			opts.Network = NetworkModeIsolated
		}
		opts.NetworkAllow = append(merged.Network.Allow, opts.NetworkAllow...)
	}

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
// set them, and applies CLI overrides for resources. homeDir and env are used
// for ~ and ${VAR} expansion in base-config directories: paths; pass
// layout.HomeDir and layout.Env().EnvForConfigInterpolation().
func applyConfigDefaults(opts *Options, ycfg *config.YoloaiConfig, pr *profileResult, homeDir string, env map[string]string) error {
	if opts.Profile == "" {
		if err := applyBaseConfigDefaults(opts, ycfg, pr, homeDir, env); err != nil {
			return err
		}
	}
	applyBaseResourceDefaults(ycfg, pr)

	// baseIsolation is what --isolation would resolve to from config alone.
	// The CLI coalesces flag-else-config into one string (DF209), so without
	// this an `isolation:` in the user's personal config is indistinguishable
	// from an explicit --isolation and overrides the profile's — against
	// config.md's "personal defaults do not carry into profiles".
	bakedIn, err := config.LoadBakedInDefaults()
	if err != nil {
		return fmt.Errorf("load baked-in defaults: %w", err)
	}
	baseIsolation := ycfg.Isolation
	if baseIsolation == "" {
		baseIsolation = bakedIn.Isolation
	}
	return applyCLIOverrides(opts, pr, baseIsolation)
}

// applyBaseConfigDefaults applies ports, caps, network, and directories from
// base config when no profile is active. Base-config directories get the
// same expansion and prepend-before-CLI-dirs treatment as a profile's
// directories: (D142) — prependProfileDirs is the shared path.
func applyBaseConfigDefaults(opts *Options, ycfg *config.YoloaiConfig, pr *profileResult, homeDir string, env map[string]string) error {
	if len(ycfg.Ports) > 0 {
		opts.Ports = append(ycfg.Ports, opts.Ports...)
	}
	pr.capAdd = ycfg.CapAdd
	pr.devices = ycfg.Devices
	pr.setup = ycfg.Setup
	pr.isolation = runtime.IsolationMode(ycfg.Isolation)

	// Same split as the profile-merge site (DF206): the allowlist merge is
	// unconditional, only the mode promotion depends on the mode being unset.
	if ycfg.Network != nil {
		if opts.Network == NetworkModeDefault && ycfg.Network.Isolated {
			opts.Network = NetworkModeIsolated
		}
		opts.NetworkAllow = append(ycfg.Network.Allow, opts.NetworkAllow...)
	}

	return prependProfileDirs(opts, ycfg.Directories, homeDir, env)
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
func applyCLIOverrides(opts *Options, pr *profileResult, baseIsolation string) error {
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
		// With a profile active, an isolation that merely equals what config
		// resolves to is a personal default wearing a CLI flag's clothes, and
		// must not override the profile (DF209). Scoped to the profile path on
		// purpose: on the no-profile path pr.isolation already came from the
		// same config, so suppressing the assignment there would silently
		// change which mode an unconfigured sandbox gets on backends whose base
		// mode is not `container` — a user-visible macOS change that belongs to
		// D143's layer work, with its own breaking-change entry, not here.
		personalDefaultInProfile := opts.Profile != "" && string(opts.Isolation) == baseIsolation
		if !personalDefaultInProfile {
			pr.isolation = opts.Isolation
		}
	}

	if len(opts.Env) > 0 {
		if pr.env == nil {
			pr.env = make(map[string]string)
		}
		maps.Copy(pr.env, opts.Env)
	}

	return nil
}
