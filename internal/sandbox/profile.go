package sandbox

// ABOUTME: Profile data model, loading, chain resolution, and config merging.
// ABOUTME: Profiles are reusable environment definitions with inheritance via extends.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// ProfileConfig holds the parsed fields from a profile.yaml file.
type ProfileConfig struct {
	Extends     string            // parent profile name; "" means "base" (default)
	Agent       string            // agent override
	Model       string            // model override
	Backend     string            // optional backend constraint
	TartImage   string            // from tart.image nested key
	Env         map[string]string // environment variables
	Ports       []string          // port mappings
	Workdir     *ProfileWorkdir   // nil if not specified
	Directories []ProfileDir      // empty if not specified
}

// ProfileWorkdir defines a workdir from a profile.
type ProfileWorkdir struct {
	Path  string // host path
	Mode  string // "copy" or "rw"
	Mount string // optional custom mount point
}

// ProfileDir defines an auxiliary directory from a profile.
type ProfileDir struct {
	Path  string // host path
	Mode  string // "rw", "copy", or "" (read-only)
	Mount string // optional custom mount point
}

// MergedConfig holds the result of merging base config with a profile chain.
type MergedConfig struct {
	Agent       string            // from nearest profile that specifies one
	Model       string            // from nearest profile that specifies one
	Backend     string            // last non-empty backend constraint
	TartImage   string            // from nearest profile that specifies one
	TmuxConf    string            // from base config only
	Env         map[string]string // merged across chain
	Ports       []string          // additive across chain
	Workdir     *ProfileWorkdir   // from nearest profile that specifies one (child wins)
	Directories []ProfileDir      // additive across chain
}

// ProfileDirPath returns the host-side directory for a profile.
//
//	~/.yoloai/profiles/<name>/
func ProfileDirPath(name string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".yoloai", "profiles", name)
}

// ValidateProfileName validates a profile name using the same rules as sandbox
// names, plus rejecting "base" which is reserved.
func ValidateProfileName(name string) error {
	if name == "" {
		return NewUsageError("profile name is required")
	}
	if name == "base" {
		return NewUsageError("profile name %q is reserved", name)
	}
	if len(name) > maxNameLength {
		return NewUsageError("invalid profile name: must be at most %d characters (got %d)", maxNameLength, len(name))
	}
	if name[0] == '/' || name[0] == '\\' {
		return NewUsageError("invalid profile name %q: looks like a path", name)
	}
	if !validNameRe.MatchString(name) {
		return NewUsageError("invalid profile name %q: must start with a letter or digit and contain only letters, digits, underscores, dots, or hyphens", name)
	}
	return nil
}

// ProfileExists checks whether a profile directory with a profile.yaml exists.
func ProfileExists(name string) bool {
	dir := ProfileDirPath(name)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, "profile.yaml"))
	return err == nil
}

// ProfileHasDockerfile checks whether a profile has a Dockerfile.
func ProfileHasDockerfile(name string) bool {
	_, err := os.Stat(filepath.Join(ProfileDirPath(name), "Dockerfile"))
	return err == nil
}

// ListProfiles returns the names of all user profiles (excludes "base").
func ListProfiles() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home directory: %w", err)
	}
	profilesDir := filepath.Join(home, ".yoloai", "profiles")

	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read profiles directory: %w", err)
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "base" {
			continue
		}
		// Only include directories that have a profile.yaml
		if _, statErr := os.Stat(filepath.Join(profilesDir, e.Name(), "profile.yaml")); statErr == nil {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// LoadProfile reads and parses a profile.yaml file.
func LoadProfile(name string) (*ProfileConfig, error) {
	dir := ProfileDirPath(name)
	path := filepath.Join(dir, "profile.yaml")

	data, err := os.ReadFile(path) //nolint:gosec // G304: path is from profile directory
	if err != nil {
		return nil, fmt.Errorf("read profile.yaml for %q: %w", name, err)
	}

	if len(data) == 0 {
		return &ProfileConfig{Extends: "base"}, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse profile.yaml for %q: %w", name, err)
	}

	cfg := &ProfileConfig{}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		cfg.Extends = "base"
		return cfg, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		cfg.Extends = "base"
		return cfg, nil
	}

	for i := 0; i < len(root.Content)-1; i += 2 {
		key := root.Content[i]
		val := root.Content[i+1]

		switch key.Value {
		case "extends":
			expanded, expandErr := expandEnvBraced(val.Value)
			if expandErr != nil {
				return nil, fmt.Errorf("extends: %w", expandErr)
			}
			cfg.Extends = expanded
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
		case "backend":
			expanded, expandErr := expandEnvBraced(val.Value)
			if expandErr != nil {
				return nil, fmt.Errorf("backend: %w", expandErr)
			}
			cfg.Backend = expanded
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
			// Unknown fields are silently ignored
		}
	}

	// Default extends to "base" if not specified
	if cfg.Extends == "" {
		cfg.Extends = "base"
	}

	return cfg, nil
}

// ResolveProfileChain walks the extends chain from the given profile back to
// base. Returns ordered list root-first, e.g. ["base", "go-dev", "go-web"].
// Detects cycles and validates that each profile exists.
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

		cfg, err := LoadProfile(current)
		if err != nil {
			return nil, err
		}
		current = cfg.Extends
	}

	// Prepend "base" and reverse the chain so it's root-first
	result := make([]string, 0, len(chain)+1)
	result = append(result, "base")
	for i := len(chain) - 1; i >= 0; i-- {
		result = append(result, chain[i])
	}
	return result, nil
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

// MergeProfileChain merges base config with each profile in the chain.
// chain is root-first, e.g. ["base", "go-dev", "go-web"].
func MergeProfileChain(base *YoloaiConfig, chain []string) (*MergedConfig, error) {
	merged := &MergedConfig{
		Agent:     base.Agent,
		Model:     base.Model,
		Backend:   base.Backend,
		TartImage: base.TartImage,
		TmuxConf:  base.TmuxConf,
	}
	if len(base.Env) > 0 {
		merged.Env = make(map[string]string, len(base.Env))
		for k, v := range base.Env {
			merged.Env[k] = v
		}
	}

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
		if profile.Backend != "" {
			merged.Backend = profile.Backend
		}
		if profile.TartImage != "" {
			merged.TartImage = profile.TartImage
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

		// Ports: additive
		merged.Ports = append(merged.Ports, profile.Ports...)

		// Directories: additive
		merged.Directories = append(merged.Directories, profile.Directories...)

		// Workdir: child wins over parent
		if profile.Workdir != nil {
			merged.Workdir = profile.Workdir
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
