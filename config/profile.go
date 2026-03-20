package config

// ABOUTME: Profile data model, loading, and config merging.
// ABOUTME: Profiles are self-contained environment definitions in ~/.yoloai/profiles/<name>/.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"
)

// ProfileConfig holds the parsed fields from a profile's config.yaml file.
type ProfileConfig struct {
	Agent              string            // agent override
	Model              string            // model override
	OS                 string            // os override
	Backend            string            // optional backend constraint (different from container_backend)
	ContainerBackend   string            // container_backend override
	TartImage          string            // from tart.image nested key
	Env                map[string]string // environment variables
	Ports              []string          // port mappings
	Workdir            *ProfileWorkdir   // nil if not specified
	Directories        []ProfileDir      // empty if not specified
	Resources          *ResourceLimits   // resource limits (cpus, memory)
	Network            *NetworkConfig    // network isolation settings
	Mounts             []string          // extra bind mounts (host:container[:ro])
	AgentArgs          map[string]string // per-agent default CLI args
	AgentFiles         *AgentFilesConfig // agent_files — extra files to seed into agent-state
	CapAdd             []string          // cap_add — Linux capabilities to add (Docker only)
	Devices            []string          // devices — host devices to expose (Docker only)
	Setup              []string          // setup — commands to run before agent launch (Docker only)
	AutoCommitInterval int               // auto_commit_interval — seconds between auto-commits in :copy dirs; 0 = disabled
	Isolation          string            // isolation — sandbox isolation mode: container, container-enhanced, vm, vm-enhanced
}

// ProfileWorkdir defines a workdir from a profile.
type ProfileWorkdir struct {
	Path  string `json:"path"`            // host path
	Mode  string `json:"mode,omitempty"`  // "copy" or "rw"
	Mount string `json:"mount,omitempty"` // optional custom mount point
}

// ProfileDir defines an auxiliary directory from a profile.
type ProfileDir struct {
	Path  string `json:"path"`            // host path
	Mode  string `json:"mode,omitempty"`  // "rw", "copy", or "" (read-only)
	Mount string `json:"mount,omitempty"` // optional custom mount point
}

// MergedConfig holds the result of merging baked-in defaults with a profile.
type MergedConfig struct {
	Agent              string            `json:"agent,omitempty"`                // from nearest profile that specifies one
	Model              string            `json:"model,omitempty"`                // from nearest profile that specifies one
	OS                 string            `json:"os,omitempty"`                   // guest OS
	Backend            string            `json:"backend,omitempty"`              // last non-empty backend constraint
	ContainerBackend   string            `json:"container_backend,omitempty"`    // last non-empty container backend
	TartImage          string            `json:"tart_image,omitempty"`           // from nearest profile that specifies one
	Env                map[string]string `json:"env,omitempty"`                  // merged across chain
	Ports              []string          `json:"ports,omitempty"`                // additive across chain
	Workdir            *ProfileWorkdir   `json:"workdir,omitempty"`              // from nearest profile that specifies one (child wins)
	Directories        []ProfileDir      `json:"directories,omitempty"`          // additive across chain
	Resources          *ResourceLimits   `json:"resources,omitempty"`            // from per-field merge across chain
	Network            *NetworkConfig    `json:"network,omitempty"`              // isolated overrides (last wins), allow additive
	Mounts             []string          `json:"mounts,omitempty"`               // additive across chain (host:container[:ro])
	AgentArgs          map[string]string `json:"agent_args,omitempty"`           // merged across chain (map merge, later wins)
	AgentFiles         *AgentFilesConfig `json:"agent_files,omitempty"`          // replacement semantics (child replaces parent)
	CapAdd             []string          `json:"cap_add,omitempty"`              // additive across chain (Docker only)
	Devices            []string          `json:"devices,omitempty"`              // additive across chain (Docker only)
	Setup              []string          `json:"setup,omitempty"`                // additive across chain (Docker only)
	AutoCommitInterval int               `json:"auto_commit_interval,omitempty"` // profile overrides default
	Isolation          string            `json:"isolation,omitempty"`            // last non-empty wins across chain
}

// ProfileDirPath returns the host-side directory for a profile.
//
//	~/.yoloai/profiles/<name>/
func ProfileDirPath(name string) string {
	return filepath.Join(ProfilesDir(), name)
}

// ValidateProfileName validates a profile name.
// Rejects empty names and names that look like paths.
func ValidateProfileName(name string) error {
	if name == "" {
		return NewUsageError("profile name is required")
	}
	if len(name) > MaxNameLength {
		return NewUsageError("invalid profile name: must be at most %d characters (got %d)", MaxNameLength, len(name))
	}
	if name[0] == '/' || name[0] == '\\' {
		return NewUsageError("invalid profile name %q: looks like a path", name)
	}
	if !ValidNameRe.MatchString(name) {
		return NewUsageError("invalid profile name %q: must start with a letter or digit and contain only letters, digits, underscores, dots, or hyphens", name)
	}
	return nil
}

// ProfileExists checks whether a profile directory with a config.yaml exists.
func ProfileExists(name string) bool {
	dir := ProfileDirPath(name)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, "config.yaml"))
	return err == nil
}

// ProfileHasDockerfile checks whether a profile has a Dockerfile.
func ProfileHasDockerfile(name string) bool {
	_, err := os.Stat(filepath.Join(ProfileDirPath(name), "Dockerfile"))
	return err == nil
}

// ListProfiles returns the names of all user profiles.
func ListProfiles() ([]string, error) {
	profilesDir := ProfilesDir()

	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read profiles directory: %w", err)
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Only include directories that have a config.yaml
		if _, statErr := os.Stat(filepath.Join(profilesDir, e.Name(), "config.yaml")); statErr == nil {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// LoadProfile reads and parses a profile's config.yaml file.
func LoadProfile(name string) (*ProfileConfig, error) {
	dir := ProfileDirPath(name)
	path := filepath.Join(dir, "config.yaml")

	data, err := os.ReadFile(path) //nolint:gosec // G304: path is from profile directory
	if err != nil {
		return nil, fmt.Errorf("read config.yaml for %q: %w", name, err)
	}

	if len(data) == 0 {
		return &ProfileConfig{}, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse config.yaml for %q: %w", name, err)
	}

	cfg := &ProfileConfig{}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return cfg, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return cfg, nil
	}

	for i := 0; i < len(root.Content)-1; i += 2 {
		key := root.Content[i]
		val := root.Content[i+1]

		switch key.Value {
		case "agent":
			expanded, expandErr := expandEnvBraced(val.Value)
			if expandErr != nil {
				return nil, fmt.Errorf("agent: %w", expandErr)
			}
			cfg.Agent = expanded
		case "model":
			expanded, expandErr := expandEnvBraced(val.Value)
			if expandErr != nil {
				return nil, fmt.Errorf("model: %w", expandErr)
			}
			cfg.Model = expanded
		case "os":
			expanded, expandErr := expandEnvBraced(val.Value)
			if expandErr != nil {
				return nil, fmt.Errorf("os: %w", expandErr)
			}
			cfg.OS = expanded
		case "backend":
			expanded, expandErr := expandEnvBraced(val.Value)
			if expandErr != nil {
				return nil, fmt.Errorf("backend: %w", expandErr)
			}
			cfg.Backend = expanded
		case "container_backend":
			expanded, expandErr := expandEnvBraced(val.Value)
			if expandErr != nil {
				return nil, fmt.Errorf("container_backend: %w", expandErr)
			}
			cfg.ContainerBackend = expanded
		case "tart":
			if val.Kind == yaml.MappingNode {
				for k := 0; k < len(val.Content)-1; k += 2 {
					subKey := val.Content[k].Value
					subExpanded, subErr := expandEnvBraced(val.Content[k+1].Value)
					if subErr != nil {
						return nil, fmt.Errorf("tart.%s: %w", subKey, subErr)
					}
					if subKey == "image" {
						cfg.TartImage = subExpanded
					}
				}
			}
		case "agent_args":
			if val.Kind == yaml.MappingNode {
				cfg.AgentArgs = make(map[string]string, len(val.Content)/2)
				for k := 0; k < len(val.Content)-1; k += 2 {
					agentKey := val.Content[k].Value
					agentExpanded, agentErr := expandEnvBraced(val.Content[k+1].Value)
					if agentErr != nil {
						return nil, fmt.Errorf("agent_args.%s: %w", agentKey, agentErr)
					}
					cfg.AgentArgs[agentKey] = agentExpanded
				}
			}
		case "env":
			if val.Kind == yaml.MappingNode {
				cfg.Env = make(map[string]string, len(val.Content)/2)
				for k := 0; k < len(val.Content)-1; k += 2 {
					envKey := val.Content[k].Value
					envExpanded, envErr := expandEnvBraced(val.Content[k+1].Value)
					if envErr != nil {
						return nil, fmt.Errorf("env.%s: %w", envKey, envErr)
					}
					cfg.Env[envKey] = envExpanded
				}
			}
		case "ports":
			if val.Kind == yaml.SequenceNode {
				for _, item := range val.Content {
					cfg.Ports = append(cfg.Ports, item.Value)
				}
			}
		case "mounts":
			if val.Kind == yaml.SequenceNode {
				for _, item := range val.Content {
					expanded, expandErr := expandEnvBraced(item.Value)
					if expandErr != nil {
						return nil, fmt.Errorf("mounts[]: %w", expandErr)
					}
					cfg.Mounts = append(cfg.Mounts, expanded)
				}
			}
		case "cap_add":
			if val.Kind == yaml.SequenceNode {
				for _, item := range val.Content {
					expanded, expandErr := expandEnvBraced(item.Value)
					if expandErr != nil {
						return nil, fmt.Errorf("cap_add[]: %w", expandErr)
					}
					cfg.CapAdd = append(cfg.CapAdd, expanded)
				}
			}
		case "devices":
			if val.Kind == yaml.SequenceNode {
				for _, item := range val.Content {
					expanded, expandErr := expandEnvBraced(item.Value)
					if expandErr != nil {
						return nil, fmt.Errorf("devices[]: %w", expandErr)
					}
					cfg.Devices = append(cfg.Devices, expanded)
				}
			}
		case "setup":
			if val.Kind == yaml.SequenceNode {
				for _, item := range val.Content {
					expanded, expandErr := expandEnvBraced(item.Value)
					if expandErr != nil {
						return nil, fmt.Errorf("setup[]: %w", expandErr)
					}
					cfg.Setup = append(cfg.Setup, expanded)
				}
			}
		case "workdir":
			if val.Kind == yaml.MappingNode {
				w := &ProfileWorkdir{}
				for k := 0; k < len(val.Content)-1; k += 2 {
					wKey := val.Content[k].Value
					wVal := val.Content[k+1].Value
					expanded, wErr := expandEnvBraced(wVal)
					if wErr != nil {
						return nil, fmt.Errorf("workdir.%s: %w", wKey, wErr)
					}
					switch wKey {
					case "path":
						w.Path = expanded
					case "mode":
						w.Mode = expanded
					case "mount":
						w.Mount = expanded
					}
				}
				cfg.Workdir = w
			}
		case "directories":
			if val.Kind == yaml.SequenceNode {
				for _, item := range val.Content {
					if item.Kind == yaml.MappingNode {
						d := ProfileDir{}
						for k := 0; k < len(item.Content)-1; k += 2 {
							dKey := item.Content[k].Value
							dVal := item.Content[k+1].Value
							expanded, dErr := expandEnvBraced(dVal)
							if dErr != nil {
								return nil, fmt.Errorf("directories[].%s: %w", dKey, dErr)
							}
							switch dKey {
							case "path":
								d.Path = expanded
							case "mode":
								d.Mode = expanded
							case "mount":
								d.Mount = expanded
							}
						}
						cfg.Directories = append(cfg.Directories, d)
					}
				}
			}
		case "resources":
			if val.Kind == yaml.MappingNode {
				cfg.Resources = &ResourceLimits{}
				for k := 0; k < len(val.Content)-1; k += 2 {
					subKey := val.Content[k].Value
					subExpanded, subErr := expandEnvBraced(val.Content[k+1].Value)
					if subErr != nil {
						return nil, fmt.Errorf("resources.%s: %w", subKey, subErr)
					}
					switch subKey {
					case "cpus":
						cfg.Resources.CPUs = subExpanded
					case "memory":
						cfg.Resources.Memory = subExpanded
					}
				}
			}
		case "network":
			if val.Kind == yaml.MappingNode {
				cfg.Network = &NetworkConfig{}
				for k := 0; k < len(val.Content)-1; k += 2 {
					subKey := val.Content[k].Value
					switch subKey {
					case "isolated":
						cfg.Network.Isolated = val.Content[k+1].Value == "true"
					case "allow":
						if val.Content[k+1].Kind == yaml.SequenceNode {
							for _, item := range val.Content[k+1].Content {
								cfg.Network.Allow = append(cfg.Network.Allow, item.Value)
							}
						}
					}
				}
			}
		case "agent_files":
			af, afErr := parseAgentFilesNode(val)
			if afErr != nil {
				return nil, fmt.Errorf("agent_files: %w", afErr)
			}
			cfg.AgentFiles = af
		case "auto_commit_interval":
			n, aErr := strconv.Atoi(val.Value)
			if aErr != nil {
				return nil, fmt.Errorf("auto_commit_interval: %w", aErr)
			}
			cfg.AutoCommitInterval = n
		case "isolation":
			expanded, expandErr := expandEnvBraced(val.Value)
			if expandErr != nil {
				return nil, fmt.Errorf("isolation: %w", expandErr)
			}
			if err := ValidateIsolationMode(expanded); err != nil {
				return nil, fmt.Errorf("isolation: %w", err)
			}
			cfg.Isolation = expanded
			// Unknown fields are silently ignored
		}
	}

	return cfg, nil
}

// LoadProfileConfig loads the effective config for the with-profile path:
// baked-in defaults merged with ~/.yoloai/profiles/<name>/config.yaml.
// defaults/config.yaml is NOT consulted — profiles are self-contained.
func LoadProfileConfig(name string) (*YoloaiConfig, error) {
	base, err := LoadBakedInDefaults()
	if err != nil {
		return nil, err
	}

	profileConfigPath := filepath.Join(ProfilesDir(), name, "config.yaml")
	data, err := os.ReadFile(profileConfigPath) //nolint:gosec // G304: path is from profile directory
	if err != nil {
		if os.IsNotExist(err) {
			return base, nil
		}
		return nil, fmt.Errorf("read profile config: %w", err)
	}

	override, err := parseConfigYAML(data, profileConfigPath, knownProfileKeys)
	if err != nil {
		return nil, err
	}

	return mergeConfigs(base, override), nil
}

// ResolveProfileImage returns the Docker image tag for a sandbox using the
// given profile. Walks the chain from child to root, returning "yoloai-<P>"
// where P is the most-derived profile that has a Dockerfile. Falls back to
// "yoloai-base" if none has a Dockerfile.
func ResolveProfileImage(profileName string, chain []string) string {
	// Walk from most-derived (last) to root (first), skip "base"
	for i := len(chain) - 1; i >= 0; i-- {
		name := chain[i]
		if name == "base" {
			continue
		}
		if ProfileHasDockerfile(name) {
			return "yoloai-" + name
		}
	}
	return "yoloai-base"
}

// ResolveProfileChain walks the extends chain from the given profile back to
// base. Returns ordered list root-first, e.g. ["base", "go-dev", "go-web"].
// Detects cycles and validates that each profile exists.
// Note: profiles no longer support inheritance chains; extends fields in
// config.yaml are read for legacy compatibility only.
func ResolveProfileChain(name string) ([]string, error) {
	var chain []string
	visited := map[string]bool{}
	current := name

	for current != "base" {
		if visited[current] {
			return nil, fmt.Errorf("profile inheritance cycle: %s", formatCycle(chain, current))
		}
		visited[current] = true

		if !ProfileExists(current) {
			return nil, fmt.Errorf("profile %q does not exist", current)
		}

		chain = append(chain, current)

		// Load profile to check for extends field (legacy support)
		cfg, err := loadProfileLegacy(current)
		if err != nil {
			return nil, err
		}
		current = cfg.extends
		if current == "" {
			current = "base"
		}
	}

	// Prepend "base" and reverse the chain so it's root-first
	result := make([]string, 0, len(chain)+1)
	result = append(result, "base")
	for i := len(chain) - 1; i >= 0; i-- {
		result = append(result, chain[i])
	}
	return result, nil
}

// legacyProfileConfig holds a profile with the legacy extends field.
type legacyProfileConfig struct {
	extends string
}

// loadProfileLegacy reads the extends field from a profile config.yaml (for legacy chain support).
// Errors are treated as "no extends field" — the profile defaults to extending "base".
func loadProfileLegacy(name string) (legacyProfileConfig, error) {
	path := filepath.Join(ProfileDirPath(name), "config.yaml")
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is from profile directory
	if err != nil {
		return legacyProfileConfig{extends: "base"}, nil //nolint:nilerr // missing file = default
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return legacyProfileConfig{extends: "base"}, nil //nolint:nilerr // invalid YAML = default
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return legacyProfileConfig{extends: "base"}, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return legacyProfileConfig{extends: "base"}, nil
	}

	for i := 0; i < len(root.Content)-1; i += 2 {
		if root.Content[i].Value == "extends" {
			expanded, _ := expandEnvBraced(root.Content[i+1].Value)
			return legacyProfileConfig{extends: expanded}, nil
		}
	}
	return legacyProfileConfig{extends: "base"}, nil
}

// formatCycle formats a cycle for error messages: "A → B → A"
func formatCycle(chain []string, repeated string) string {
	s := ""
	for _, name := range chain {
		s += name + " → "
	}
	s += repeated
	return s
}

// MergeProfileChain merges base config with each profile in the chain.
// chain is root-first, e.g. ["base", "go-dev", "go-web"].
func MergeProfileChain(base *YoloaiConfig, chain []string) (*MergedConfig, error) {
	merged := &MergedConfig{
		Agent:            base.Agent,
		Model:            base.Model,
		OS:               base.OS,
		ContainerBackend: base.ContainerBackend,
		TartImage:        base.TartImage,
		Isolation:        base.Isolation,
	}
	if len(base.Env) > 0 {
		merged.Env = make(map[string]string, len(base.Env))
		for k, v := range base.Env {
			merged.Env[k] = v
		}
	}

	if base.Resources != nil {
		merged.Resources = &ResourceLimits{}
		if base.Resources.CPUs != "" {
			merged.Resources.CPUs = base.Resources.CPUs
		}
		if base.Resources.Memory != "" {
			merged.Resources.Memory = base.Resources.Memory
		}
	}

	if base.Network != nil {
		merged.Network = &NetworkConfig{
			Isolated: base.Network.Isolated,
		}
		if len(base.Network.Allow) > 0 {
			merged.Network.Allow = make([]string, len(base.Network.Allow))
			copy(merged.Network.Allow, base.Network.Allow)
		}
	}

	if len(base.Mounts) > 0 {
		merged.Mounts = make([]string, len(base.Mounts))
		copy(merged.Mounts, base.Mounts)
	}

	if len(base.Ports) > 0 {
		merged.Ports = make([]string, len(base.Ports))
		copy(merged.Ports, base.Ports)
	}

	if len(base.CapAdd) > 0 {
		merged.CapAdd = append([]string{}, base.CapAdd...)
	}
	if len(base.Devices) > 0 {
		merged.Devices = append([]string{}, base.Devices...)
	}
	if len(base.Setup) > 0 {
		merged.Setup = append([]string{}, base.Setup...)
	}

	if len(base.AgentArgs) > 0 {
		merged.AgentArgs = make(map[string]string, len(base.AgentArgs))
		for k, v := range base.AgentArgs {
			merged.AgentArgs[k] = v
		}
	}

	merged.AgentFiles = base.AgentFiles
	merged.AutoCommitInterval = base.AutoCommitInterval

	// Apply each non-base profile in order
	for _, name := range chain {
		if name == "base" {
			continue
		}

		profile, err := LoadProfile(name)
		if err != nil {
			return nil, err
		}

		// Scalars: non-empty overrides previous
		if profile.Agent != "" {
			merged.Agent = profile.Agent
		}
		if profile.Model != "" {
			merged.Model = profile.Model
		}
		if profile.OS != "" {
			merged.OS = profile.OS
		}
		if profile.Backend != "" {
			merged.Backend = profile.Backend
		}
		if profile.ContainerBackend != "" {
			merged.ContainerBackend = profile.ContainerBackend
		}
		if profile.TartImage != "" {
			merged.TartImage = profile.TartImage
		}
		if profile.Isolation != "" {
			merged.Isolation = profile.Isolation
		}

		// Env: map merge, later wins on conflict
		if len(profile.Env) > 0 {
			if merged.Env == nil {
				merged.Env = make(map[string]string)
			}
			for k, v := range profile.Env {
				merged.Env[k] = v
			}
		}

		// AgentArgs: map merge, later wins on conflict
		if len(profile.AgentArgs) > 0 {
			if merged.AgentArgs == nil {
				merged.AgentArgs = make(map[string]string)
			}
			for k, v := range profile.AgentArgs {
				merged.AgentArgs[k] = v
			}
		}

		// Ports: additive
		merged.Ports = append(merged.Ports, profile.Ports...)

		// Directories: additive
		merged.Directories = append(merged.Directories, profile.Directories...)

		// Workdir: child wins over parent
		if profile.Workdir != nil {
			merged.Workdir = profile.Workdir
		}

		// Resources: per-field override
		if profile.Resources != nil {
			if merged.Resources == nil {
				merged.Resources = &ResourceLimits{}
			}
			if profile.Resources.CPUs != "" {
				merged.Resources.CPUs = profile.Resources.CPUs
			}
			if profile.Resources.Memory != "" {
				merged.Resources.Memory = profile.Resources.Memory
			}
		}

		// Network: isolated overrides (last wins), allow is additive
		if profile.Network != nil {
			if merged.Network == nil {
				merged.Network = &NetworkConfig{}
			}
			merged.Network.Isolated = profile.Network.Isolated
			merged.Network.Allow = append(merged.Network.Allow, profile.Network.Allow...)
		}

		// Mounts: additive
		merged.Mounts = append(merged.Mounts, profile.Mounts...)

		// Recipes: additive
		merged.CapAdd = append(merged.CapAdd, profile.CapAdd...)
		merged.Devices = append(merged.Devices, profile.Devices...)
		merged.Setup = append(merged.Setup, profile.Setup...)

		// AgentFiles: replacement semantics (child replaces parent entirely)
		if profile.AgentFiles != nil {
			merged.AgentFiles = profile.AgentFiles
		}

		// AutoCommitInterval: scalar override (non-zero wins)
		if profile.AutoCommitInterval > 0 {
			merged.AutoCommitInterval = profile.AutoCommitInterval
		}

	}

	return merged, nil
}

// ValidateProfileBackend checks that a profile's backend constraint matches
// the resolved backend. Returns nil if the profile has no constraint.
func ValidateProfileBackend(profileBackend, resolvedBackend string) error {
	if profileBackend == "" {
		return nil
	}
	if profileBackend != resolvedBackend {
		return fmt.Errorf("profile requires backend %q but resolved backend is %q", profileBackend, resolvedBackend)
	}
	return nil
}

// LoadMergedConfig loads the baked-in defaults and merges the named profile
// in a single call. If profileName is empty, returns defaults with no profile.
func LoadMergedConfig(profileName string) (*MergedConfig, error) {
	if profileName == "" {
		base, err := LoadDefaultsConfig()
		if err != nil {
			return nil, err
		}
		return &MergedConfig{
			Agent:            base.Agent,
			Model:            base.Model,
			OS:               base.OS,
			ContainerBackend: base.ContainerBackend,
			Env:              base.Env,
		}, nil
	}

	base, err := LoadBakedInDefaults()
	if err != nil {
		return nil, err
	}

	// Use legacy chain resolution for backward compatibility
	chain, err := ResolveProfileChain(profileName)
	if err != nil {
		return nil, err
	}
	return MergeProfileChain(base, chain)
}
