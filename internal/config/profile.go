package config

// ABOUTME: Profile data model, loading, and config merging.
// ABOUTME: Profiles are self-contained environment definitions in ~/.yoloai/profiles/<name>/.

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kstenerud/yoloai/internal/yoerrors"
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

// ValidateProfileName validates a profile name.
// Rejects empty names and names that look like paths.
func ValidateProfileName(name string) error {
	if name == "" {
		return yoerrors.NewUsageError("profile name is required")
	}
	if len(name) > MaxNameLength {
		return yoerrors.NewUsageError("invalid profile name: must be at most %d characters (got %d)", MaxNameLength, len(name))
	}
	if name[0] == '/' || name[0] == '\\' {
		return yoerrors.NewUsageError("invalid profile name %q: looks like a path", name)
	}
	if !ValidNameRe.MatchString(name) {
		return yoerrors.NewUsageError("invalid profile name %q: must start with a letter or digit and contain only letters, digits, underscores, dots, or hyphens", name)
	}
	return nil
}

// ProfileExists checks whether a profile directory with a config.yaml exists.
func ProfileExists(layout Layout, name string) bool {
	dir := layout.ProfileDir(name)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, "config.yaml"))
	return err == nil
}

// ProfileHasDockerfile checks whether a profile has a Dockerfile.
func ProfileHasDockerfile(layout Layout, name string) bool {
	_, err := os.Stat(filepath.Join(layout.ProfileDir(name), "Dockerfile"))
	return err == nil
}

// ListProfiles returns the names of all user profiles.
func ListProfiles(layout Layout) ([]string, error) {
	profilesDir := layout.ProfilesDir()

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

// profileConfigHandler is a function that handles a single YAML key in a ProfileConfig.
type profileConfigHandler func(cfg *ProfileConfig, val *yaml.Node, env map[string]string) error

// profileConfigHandlers maps top-level YAML keys to their handler functions.
var profileConfigHandlers = map[string]profileConfigHandler{
	"agent":                profileScalarHandler(func(c *ProfileConfig) *string { return &c.Agent }),
	"model":                profileScalarHandler(func(c *ProfileConfig) *string { return &c.Model }),
	"os":                   profileScalarHandler(func(c *ProfileConfig) *string { return &c.OS }),
	"backend":              profileScalarHandler(func(c *ProfileConfig) *string { return &c.Backend }),
	"container_backend":    profileScalarHandler(func(c *ProfileConfig) *string { return &c.ContainerBackend }),
	"mounts":               profileExpandedSeqHandler(func(c *ProfileConfig) *[]string { return &c.Mounts }, "mounts[]"),
	"ports":                profileRawSeqHandler(func(c *ProfileConfig) *[]string { return &c.Ports }),
	"cap_add":              profileExpandedSeqHandler(func(c *ProfileConfig) *[]string { return &c.CapAdd }, "cap_add[]"),
	"devices":              profileExpandedSeqHandler(func(c *ProfileConfig) *[]string { return &c.Devices }, "devices[]"),
	"setup":                profileExpandedSeqHandler(func(c *ProfileConfig) *[]string { return &c.Setup }, "setup[]"),
	"env":                  profileStringMapHandler(func(c *ProfileConfig) *map[string]string { return &c.Env }, "env"),
	"agent_args":           profileStringMapHandler(func(c *ProfileConfig) *map[string]string { return &c.AgentArgs }, "agent_args"),
	"tart":                 handleProfileTart,
	"resources":            handleProfileResources,
	"network":              handleProfileNetwork,
	"workdir":              handleProfileWorkdir,
	"directories":          handleProfileDirectories,
	"agent_files":          handleProfileAgentFiles,
	"auto_commit_interval": handleProfileAutoCommitInterval,
	"isolation":            handleProfileIsolation,
}

// profileScalarHandler returns a handler that expands env vars and stores the result in the field pointed to by ptr.
func profileScalarHandler(ptr func(*ProfileConfig) *string) profileConfigHandler {
	return func(cfg *ProfileConfig, val *yaml.Node, env map[string]string) error {
		expanded, err := expandEnvBraced(val.Value, env)
		if err != nil {
			return err
		}
		*ptr(cfg) = expanded
		return nil
	}
}

// profileExpandedSeqHandler returns a handler that appends expanded sequence items to the slice pointed to by ptr.
func profileExpandedSeqHandler(ptr func(*ProfileConfig) *[]string, label string) profileConfigHandler {
	return func(cfg *ProfileConfig, val *yaml.Node, env map[string]string) error {
		if val.Kind != yaml.SequenceNode {
			return nil
		}
		for _, item := range val.Content {
			expanded, err := expandEnvBraced(item.Value, env)
			if err != nil {
				return fmt.Errorf("%s: %w", label, err)
			}
			*ptr(cfg) = append(*ptr(cfg), expanded)
		}
		return nil
	}
}

// profileRawSeqHandler returns a handler that appends raw (unexpanded) sequence items to the slice pointed to by ptr.
func profileRawSeqHandler(ptr func(*ProfileConfig) *[]string) profileConfigHandler {
	return func(cfg *ProfileConfig, val *yaml.Node, _ map[string]string) error {
		if val.Kind != yaml.SequenceNode {
			return nil
		}
		for _, item := range val.Content {
			*ptr(cfg) = append(*ptr(cfg), item.Value)
		}
		return nil
	}
}

// profileStringMapHandler returns a handler that populates a map[string]string field with expanded values.
func profileStringMapHandler(ptr func(*ProfileConfig) *map[string]string, prefix string) profileConfigHandler {
	return func(cfg *ProfileConfig, val *yaml.Node, env map[string]string) error {
		if val.Kind != yaml.MappingNode {
			return nil
		}
		m := make(map[string]string, len(val.Content)/2)
		for k := 0; k < len(val.Content)-1; k += 2 {
			key := val.Content[k].Value
			expanded, err := expandEnvBraced(val.Content[k+1].Value, env)
			if err != nil {
				return fmt.Errorf("%s.%s: %w", prefix, key, err)
			}
			m[key] = expanded
		}
		*ptr(cfg) = m
		return nil
	}
}

func handleProfileTart(cfg *ProfileConfig, val *yaml.Node, env map[string]string) error {
	if val.Kind != yaml.MappingNode {
		return nil
	}
	for k := 0; k < len(val.Content)-1; k += 2 {
		subKey := val.Content[k].Value
		subExpanded, err := expandEnvBraced(val.Content[k+1].Value, env)
		if err != nil {
			return fmt.Errorf("tart.%s: %w", subKey, err)
		}
		if subKey == "image" {
			cfg.TartImage = subExpanded
		}
	}
	return nil
}

func handleProfileResources(cfg *ProfileConfig, val *yaml.Node, env map[string]string) error {
	if val.Kind != yaml.MappingNode {
		return nil
	}
	cfg.Resources = &ResourceLimits{}
	for k := 0; k < len(val.Content)-1; k += 2 {
		subKey := val.Content[k].Value
		subExpanded, err := expandEnvBraced(val.Content[k+1].Value, env)
		if err != nil {
			return fmt.Errorf("resources.%s: %w", subKey, err)
		}
		switch subKey {
		case "cpus":
			cfg.Resources.CPUs = subExpanded
		case "memory":
			cfg.Resources.Memory = subExpanded
		}
	}
	return nil
}

func handleProfileNetwork(cfg *ProfileConfig, val *yaml.Node, _ map[string]string) error {
	if val.Kind != yaml.MappingNode {
		return nil
	}
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
	return nil
}

func handleProfileWorkdir(cfg *ProfileConfig, val *yaml.Node, env map[string]string) error {
	if val.Kind != yaml.MappingNode {
		return nil
	}
	w := &ProfileWorkdir{}
	for k := 0; k < len(val.Content)-1; k += 2 {
		wKey := val.Content[k].Value
		expanded, err := expandEnvBraced(val.Content[k+1].Value, env)
		if err != nil {
			return fmt.Errorf("workdir.%s: %w", wKey, err)
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
	return nil
}

func handleProfileDirectories(cfg *ProfileConfig, val *yaml.Node, env map[string]string) error {
	if val.Kind != yaml.SequenceNode {
		return nil
	}
	for _, item := range val.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		d := ProfileDir{}
		for k := 0; k < len(item.Content)-1; k += 2 {
			dKey := item.Content[k].Value
			expanded, err := expandEnvBraced(item.Content[k+1].Value, env)
			if err != nil {
				return fmt.Errorf("directories[].%s: %w", dKey, err)
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
	return nil
}

func handleProfileAgentFiles(cfg *ProfileConfig, val *yaml.Node, _ map[string]string) error {
	af, err := parseAgentFilesNode(val)
	if err != nil {
		return fmt.Errorf("agent_files: %w", err)
	}
	cfg.AgentFiles = af
	return nil
}

func handleProfileAutoCommitInterval(cfg *ProfileConfig, val *yaml.Node, _ map[string]string) error {
	n, err := strconv.Atoi(val.Value)
	if err != nil {
		return fmt.Errorf("auto_commit_interval: %w", err)
	}
	cfg.AutoCommitInterval = n
	return nil
}

func handleProfileIsolation(cfg *ProfileConfig, val *yaml.Node, env map[string]string) error {
	expanded, err := expandEnvBraced(val.Value, env)
	if err != nil {
		return fmt.Errorf("isolation: %w", err)
	}
	if err := ValidateIsolationMode(expanded); err != nil {
		return fmt.Errorf("isolation: %w", err)
	}
	cfg.Isolation = expanded
	return nil
}

// LoadProfile reads and parses a profile's config.yaml file.
// layout.Env is used for ${VAR} expansion in config values.
func LoadProfile(layout Layout, name string) (*ProfileConfig, error) {
	dir := layout.ProfileDir(name)
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
		key := root.Content[i].Value
		val := root.Content[i+1]
		handler, ok := profileConfigHandlers[key]
		if !ok {
			continue // unknown fields are silently ignored
		}
		if err := handler(cfg, val, layout.Env); err != nil {
			return nil, err
		}
	}

	return cfg, nil
}

// LoadProfileConfig loads the effective config for the with-profile path:
// baked-in defaults merged with DataDir/profiles/<name>/config.yaml.
// defaults/config.yaml is NOT consulted — profiles are self-contained.
func LoadProfileConfig(layout Layout, name string) (*YoloaiConfig, error) {
	base, err := LoadBakedInDefaults()
	if err != nil {
		return nil, err
	}

	profileConfigPath := filepath.Join(layout.ProfileDir(name), "config.yaml")
	data, err := os.ReadFile(profileConfigPath) //nolint:gosec // G304: path is from profile directory
	if err != nil {
		if os.IsNotExist(err) {
			return base, nil
		}
		return nil, fmt.Errorf("read profile config: %w", err)
	}

	override, err := parseConfigYAML(data, profileConfigPath, knownProfileKeys, layout.Env)
	if err != nil {
		return nil, err
	}

	return mergeConfigs(base, override), nil
}

// ResolveProfileImage returns the Docker image tag for a sandbox using the
// given profile. Walks the chain from child to root, returning "yoloai-<P>"
// where P is the most-derived profile that has a Dockerfile. Falls back to
// "yoloai-base" if none has a Dockerfile.
func ResolveProfileImage(layout Layout, profileName string, chain []string) string {
	// Walk from most-derived (last) to root (first), skip "base"
	for _, name := range slices.Backward(chain) {

		if name == "base" {
			continue
		}
		if ProfileHasDockerfile(layout, name) {
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
func ResolveProfileChain(layout Layout, name string) ([]string, error) {
	var chain []string
	visited := map[string]bool{}
	current := name

	for current != "base" {
		if visited[current] {
			return nil, fmt.Errorf("profile inheritance cycle: %s", formatCycle(chain, current))
		}
		visited[current] = true

		if !ProfileExists(layout, current) {
			return nil, fmt.Errorf("profile %q does not exist", current)
		}

		chain = append(chain, current)

		// Load profile to check for extends field (legacy support)
		cfg, err := loadProfileLegacy(layout, current)
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
	for _, c := range slices.Backward(chain) {
		result = append(result, c)
	}
	return result, nil
}

// legacyProfileConfig holds a profile with the legacy extends field.
type legacyProfileConfig struct {
	extends string
}

// loadProfileLegacy reads the extends field from a profile config.yaml (for legacy chain support).
// Errors are treated as "no extends field" — the profile defaults to extending "base".
func loadProfileLegacy(layout Layout, name string) (legacyProfileConfig, error) {
	path := filepath.Join(layout.ProfileDir(name), "config.yaml")
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
			expanded, _ := expandEnvBraced(root.Content[i+1].Value, layout.Env)
			return legacyProfileConfig{extends: expanded}, nil
		}
	}
	return legacyProfileConfig{extends: "base"}, nil
}

// formatCycle formats a cycle for error messages: "A → B → A"
func formatCycle(chain []string, repeated string) string {
	var s strings.Builder
	for _, name := range chain {
		s.WriteString(name + " → ")
	}
	s.WriteString(repeated)
	return s.String()
}

// mergedConfigFromBase initialises a MergedConfig from the baked-in base YoloaiConfig.
func mergedConfigFromBase(base *YoloaiConfig) *MergedConfig {
	merged := &MergedConfig{
		Agent:              base.Agent,
		Model:              base.Model,
		OS:                 base.OS,
		ContainerBackend:   base.ContainerBackend,
		TartImage:          base.TartImage,
		Isolation:          base.Isolation,
		AgentFiles:         base.AgentFiles,
		AutoCommitInterval: base.AutoCommitInterval,
	}
	if len(base.Env) > 0 {
		merged.Env = make(map[string]string, len(base.Env))
		maps.Copy(merged.Env, base.Env)
	}
	if len(base.AgentArgs) > 0 {
		merged.AgentArgs = make(map[string]string, len(base.AgentArgs))
		maps.Copy(merged.AgentArgs, base.AgentArgs)
	}
	if base.Resources != nil {
		merged.Resources = &ResourceLimits{
			CPUs:   base.Resources.CPUs,
			Memory: base.Resources.Memory,
		}
	}
	if base.Network != nil {
		merged.Network = &NetworkConfig{Isolated: base.Network.Isolated}
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
	return merged
}

// applyProfileToMerged applies a single ProfileConfig on top of merged in place.
func applyProfileToMerged(merged *MergedConfig, profile *ProfileConfig) {
	// Scalars: non-empty overrides previous
	merged.Agent = mergeStringField(merged.Agent, profile.Agent)
	merged.Model = mergeStringField(merged.Model, profile.Model)
	merged.OS = mergeStringField(merged.OS, profile.OS)
	merged.Backend = mergeStringField(merged.Backend, profile.Backend)
	merged.ContainerBackend = mergeStringField(merged.ContainerBackend, profile.ContainerBackend)
	merged.TartImage = mergeStringField(merged.TartImage, profile.TartImage)
	merged.Isolation = mergeStringField(merged.Isolation, profile.Isolation)

	// AgentFiles: replacement semantics
	if profile.AgentFiles != nil {
		merged.AgentFiles = profile.AgentFiles
	}
	// AutoCommitInterval: non-zero wins
	if profile.AutoCommitInterval > 0 {
		merged.AutoCommitInterval = profile.AutoCommitInterval
	}
	// Workdir: child wins over parent
	if profile.Workdir != nil {
		merged.Workdir = profile.Workdir
	}

	// Maps: merge, later wins on conflict
	applyProfileMaps(merged, profile)

	// Additive fields
	merged.Ports = append(merged.Ports, profile.Ports...)
	merged.Mounts = append(merged.Mounts, profile.Mounts...)
	merged.CapAdd = append(merged.CapAdd, profile.CapAdd...)
	merged.Devices = append(merged.Devices, profile.Devices...)
	merged.Setup = append(merged.Setup, profile.Setup...)
	merged.Directories = append(merged.Directories, profile.Directories...)

	// Resources: per-field override
	if profile.Resources != nil {
		if merged.Resources == nil {
			merged.Resources = &ResourceLimits{}
		}
		merged.Resources.CPUs = mergeStringField(merged.Resources.CPUs, profile.Resources.CPUs)
		merged.Resources.Memory = mergeStringField(merged.Resources.Memory, profile.Resources.Memory)
	}

	// Network: isolated overrides (last wins), allow is additive
	if profile.Network != nil {
		if merged.Network == nil {
			merged.Network = &NetworkConfig{}
		}
		merged.Network.Isolated = profile.Network.Isolated
		merged.Network.Allow = append(merged.Network.Allow, profile.Network.Allow...)
	}
}

// applyProfileMaps merges profile map fields (Env, AgentArgs) into merged.
func applyProfileMaps(merged *MergedConfig, profile *ProfileConfig) {
	if len(profile.Env) > 0 {
		if merged.Env == nil {
			merged.Env = make(map[string]string)
		}
		maps.Copy(merged.Env, profile.Env)
	}
	if len(profile.AgentArgs) > 0 {
		if merged.AgentArgs == nil {
			merged.AgentArgs = make(map[string]string)
		}
		maps.Copy(merged.AgentArgs, profile.AgentArgs)
	}
}

// MergeProfileChain merges base config with each profile in the chain.
// chain is root-first, e.g. ["base", "go-dev", "go-web"].
func MergeProfileChain(layout Layout, base *YoloaiConfig, chain []string) (*MergedConfig, error) {
	merged := mergedConfigFromBase(base)

	for _, name := range chain {
		if name == "base" {
			continue
		}
		profile, err := LoadProfile(layout, name)
		if err != nil {
			return nil, err
		}
		applyProfileToMerged(merged, profile)
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
func LoadMergedConfig(layout Layout, profileName string) (*MergedConfig, error) {
	if profileName == "" {
		base, err := LoadDefaultsConfig(layout)
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
	chain, err := ResolveProfileChain(layout, profileName)
	if err != nil {
		return nil, err
	}
	return MergeProfileChain(layout, base, chain)
}
