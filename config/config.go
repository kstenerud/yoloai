package config

// ABOUTME: Config loading for defaults (~/.yoloai/defaults/config.yaml) and
// ABOUTME: global (~/.yoloai/config.yaml) configs. Provides dotted-path get/set.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/kstenerud/yoloai/internal/fileutil"
	"gopkg.in/yaml.v3"
)

// AgentFilesConfig holds the parsed agent_files config option.
// nil means not specified (inherit from parent in merge).
type AgentFilesConfig struct {
	BaseDir string   // non-empty for string form (base directory path)
	Files   []string // non-nil for list form (explicit file/dir paths)
}

// IsStringForm returns true if the config uses the string (base directory) form.
func (c *AgentFilesConfig) IsStringForm() bool {
	return c != nil && c.BaseDir != ""
}

// YoloaiConfig holds the subset of config.yaml fields that the Go code reads.
type YoloaiConfig struct {
	OS                 string            `yaml:"os"`                   // os — guest OS: linux, mac
	ContainerBackend   string            `yaml:"container_backend"`    // container_backend — runtime backend: docker, podman, containerd
	TartImage          string            `yaml:"tart_image"`           // tart.image — custom base VM image for tart backend
	Agent              string            `yaml:"agent"`                // agent
	Model              string            `yaml:"model"`                // model
	Env                map[string]string `yaml:"env"`                  // env — environment variables passed to container
	Resources          *ResourceLimits   `yaml:"resources"`            // resources — container resource limits
	Network            *NetworkConfig    `yaml:"network"`              // network — network isolation settings
	Mounts             []string          `yaml:"mounts"`               // mounts — extra bind mounts (host:container[:ro])
	Ports              []string          `yaml:"ports"`                // ports — default port mappings (host:container)
	AgentArgs          map[string]string `yaml:"agent_args"`           // agent_args — per-agent default CLI args
	AgentFiles         *AgentFilesConfig `yaml:"-"`                    // agent_files — extra files to seed into agent-state
	CapAdd             []string          `yaml:"cap_add"`              // cap_add — Linux capabilities to add (Docker only)
	Devices            []string          `yaml:"devices"`              // devices — host devices to expose (Docker only)
	Setup              []string          `yaml:"setup"`                // setup — commands to run before agent launch (Docker only)
	AutoCommitInterval int               `yaml:"auto_commit_interval"` // auto_commit_interval — seconds between auto-commits in :copy dirs; 0 = disabled
	Isolation          string            `yaml:"isolation"`            // isolation — sandbox isolation mode: container, container-enhanced, vm, vm-enhanced
}

// ResourceLimits holds container resource constraints (CPU, memory).
type ResourceLimits struct {
	CPUs   string `yaml:"cpus" json:"cpus,omitempty"`
	Memory string `yaml:"memory" json:"memory,omitempty"`
}

// NetworkConfig holds network isolation settings.
type NetworkConfig struct {
	Isolated bool     `yaml:"isolated" json:"isolated,omitempty"`
	Allow    []string `yaml:"allow" json:"allow,omitempty"`
}

// GlobalConfig holds user preferences from ~/.yoloai/config.yaml.
// These settings apply to all sandboxes regardless of profile.
type GlobalConfig struct {
	TmuxConf     string            `yaml:"tmux_conf"`
	ModelAliases map[string]string `yaml:"model_aliases"`
}

// knownSetting defines a config key with its default value.
type knownSetting struct {
	Path    string
	Default string
}

// knownSettings lists every scalar config key the code recognizes, with defaults.
// Used by GetEffectiveConfig and GetConfigValue to fill in unset values.
var knownSettings = []knownSetting{
	{"os", "linux"},
	{"container_backend", ""},
	{"tart.image", ""},
	{"agent", "claude"},
	{"model", ""},
	{"resources.cpus", ""},
	{"resources.memory", ""},
	{"network.isolated", "false"},
	{"auto_commit_interval", "0"},
	{"isolation", ""},
}

// ValidateIsolationMode returns an error if mode is not a known isolation mode.
// Empty string is allowed (means "use default").
func ValidateIsolationMode(mode string) error {
	switch mode {
	case "", "container", "container-enhanced", "vm", "vm-enhanced":
		return nil
	default:
		return NewUsageError("unknown isolation mode %q: valid values are container, container-enhanced, vm, vm-enhanced", mode)
	}
}

// knownCollectionSetting defines a non-scalar config key (map or list)
// with its default YAML node kind.
type knownCollectionSetting struct {
	Path string
	Kind yaml.Kind // yaml.MappingNode or yaml.SequenceNode
}

// knownCollectionSettings lists non-scalar config keys shown in effective config.
// Each appears as an empty mapping ({}) or sequence ([]) when not set by the user.
var knownCollectionSettings = []knownCollectionSetting{
	{"agent_args", yaml.MappingNode},
	{"env", yaml.MappingNode},
	{"mounts", yaml.SequenceNode},
	{"ports", yaml.SequenceNode},
	{"network.allow", yaml.SequenceNode},
	{"cap_add", yaml.SequenceNode},
	{"devices", yaml.SequenceNode},
	{"setup", yaml.SequenceNode},
}

// globalKnownSettings lists scalar config keys belonging to the global config.
var globalKnownSettings = []knownSetting{
	{"tmux_conf", ""},
}

// globalKnownCollectionSettings lists non-scalar config keys belonging to global config.
var globalKnownCollectionSettings = []knownCollectionSetting{
	{"model_aliases", yaml.MappingNode},
}

// knownDefaultsKeys: valid top-level keys in defaults/config.yaml.
var knownDefaultsKeys = map[string]bool{
	"os": true, "agent": true, "model": true, "container_backend": true,
	"isolation": true, "tart": true, "network": true, "agent_files": true,
	"mounts": true, "ports": true, "resources": true, "agent_args": true,
	"env": true, "auto_commit_interval": true, "cap_add": true,
	"devices": true, "setup": true,
}

// knownProfileKeys: valid top-level keys in profiles/<name>/config.yaml.
// Superset of defaults keys — adds workdir and directories.
var knownProfileKeys = map[string]bool{
	"os": true, "agent": true, "model": true, "container_backend": true,
	"isolation": true, "tart": true, "network": true, "agent_files": true,
	"mounts": true, "ports": true, "resources": true, "agent_args": true,
	"env": true, "auto_commit_interval": true, "cap_add": true,
	"devices": true, "setup": true,
	"workdir": true, "directories": true, // profile-only
	// backend is kept for profile backend constraint (different from container_backend)
	"backend": true,
}

// ConfigPath returns the path to ~/.yoloai/defaults/config.yaml.
// Used by config get/set/reset for non-global settings.
func ConfigPath() string {
	return DefaultsConfigPath()
}

// GlobalConfigPath returns the path to ~/.yoloai/config.yaml.
func GlobalConfigPath() string {
	return filepath.Join(YoloaiDir(), "config.yaml")
}

// parseConfigYAML parses a config YAML document into a YoloaiConfig.
// source is used in error messages. knownKeys is the set of allowed top-level keys;
// if nil, no unknown-key validation is performed.
func parseConfigYAML(data []byte, source string, knownKeys map[string]bool) (*YoloaiConfig, error) {
	// Parse into a yaml.Node tree to extract fields without losing structure.
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", source, err)
	}

	cfg := &YoloaiConfig{}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return cfg, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return cfg, nil
	}

	// Validate unknown keys if knownKeys is provided.
	if knownKeys != nil {
		var unknown []string
		for i := 0; i < len(root.Content)-1; i += 2 {
			key := root.Content[i].Value
			if !knownKeys[key] {
				unknown = append(unknown, key)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return nil, fmt.Errorf("%s: unknown config field(s): %s", source, strings.Join(unknown, ", "))
		}
	}

	for i := 0; i < len(root.Content)-1; i += 2 {
		key := root.Content[i]
		val := root.Content[i+1]

		switch key.Value {
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
		case "ports":
			if val.Kind == yaml.SequenceNode {
				for _, item := range val.Content {
					cfg.Ports = append(cfg.Ports, item.Value)
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
		case "container_backend":
			expanded, err := expandEnvBraced(val.Value)
			if err != nil {
				return nil, fmt.Errorf("container_backend: %w", err)
			}
			cfg.ContainerBackend = expanded
		case "agent":
			expanded, err := expandEnvBraced(val.Value)
			if err != nil {
				return nil, fmt.Errorf("agent: %w", err)
			}
			cfg.Agent = expanded
		case "model":
			expanded, err := expandEnvBraced(val.Value)
			if err != nil {
				return nil, fmt.Errorf("model: %w", err)
			}
			cfg.Model = expanded
		case "os":
			expanded, err := expandEnvBraced(val.Value)
			if err != nil {
				return nil, fmt.Errorf("os: %w", err)
			}
			cfg.OS = expanded
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
			expanded, err := expandEnvBraced(val.Value)
			if err != nil {
				return nil, fmt.Errorf("isolation: %w", err)
			}
			if err := ValidateIsolationMode(expanded); err != nil {
				return nil, fmt.Errorf("isolation: %w", err)
			}
			cfg.Isolation = expanded
		}
	}

	return cfg, nil
}

// LoadBakedInDefaults parses the embedded defaults YAML into a YoloaiConfig.
// Returns a fully-populated config with every field at its baked-in default.
func LoadBakedInDefaults() (*YoloaiConfig, error) {
	return parseConfigYAML([]byte(DefaultConfigYAML), "<baked-in>", knownDefaultsKeys)
}

// LoadDefaultsConfig loads the effective config for the no-profile path:
// baked-in defaults merged with ~/.yoloai/defaults/config.yaml.
// Used by sandbox.Create() when no --profile is given.
func LoadDefaultsConfig() (*YoloaiConfig, error) {
	base, err := LoadBakedInDefaults()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(DefaultsConfigPath()) //nolint:gosec // G304: path is ~/.yoloai/defaults/config.yaml
	if err != nil {
		if os.IsNotExist(err) {
			return base, nil
		}
		return nil, fmt.Errorf("read defaults/config.yaml: %w", err)
	}

	override, err := parseConfigYAML(data, DefaultsConfigPath(), knownDefaultsKeys)
	if err != nil {
		return nil, err
	}

	return mergeConfigs(base, override), nil
}

// mergeConfigs merges override into base, returning a new YoloaiConfig.
// Merge semantics:
//   - Scalars (OS, Agent, Model, ContainerBackend, TartImage, Isolation): non-empty overrides
//   - Maps (Env, AgentArgs): map merge, override wins on conflict
//   - Lists (Mounts, Ports, CapAdd, Devices, Setup): additive
//   - Resources: per-field override (non-empty override wins)
//   - Network: Isolated overrides (last wins), Allow is additive
//   - AgentFiles: replacement semantics (non-nil replaces)
//   - AutoCommitInterval: non-zero override wins
func mergeConfigs(base, override *YoloaiConfig) *YoloaiConfig {
	result := &YoloaiConfig{
		OS:                 base.OS,
		ContainerBackend:   base.ContainerBackend,
		TartImage:          base.TartImage,
		Agent:              base.Agent,
		Model:              base.Model,
		Isolation:          base.Isolation,
		AutoCommitInterval: base.AutoCommitInterval,
		AgentFiles:         base.AgentFiles,
	}

	// Scalars: non-empty override wins
	if override.OS != "" {
		result.OS = override.OS
	}
	if override.ContainerBackend != "" {
		result.ContainerBackend = override.ContainerBackend
	}
	if override.TartImage != "" {
		result.TartImage = override.TartImage
	}
	if override.Agent != "" {
		result.Agent = override.Agent
	}
	if override.Model != "" {
		result.Model = override.Model
	}
	if override.Isolation != "" {
		result.Isolation = override.Isolation
	}

	// AutoCommitInterval: non-zero override wins
	if override.AutoCommitInterval > 0 {
		result.AutoCommitInterval = override.AutoCommitInterval
	}

	// AgentFiles: replacement semantics
	if override.AgentFiles != nil {
		result.AgentFiles = override.AgentFiles
	}

	// Env: map merge, override wins on conflict
	if len(base.Env) > 0 || len(override.Env) > 0 {
		result.Env = make(map[string]string)
		for k, v := range base.Env {
			result.Env[k] = v
		}
		for k, v := range override.Env {
			result.Env[k] = v
		}
	}

	// AgentArgs: map merge, override wins on conflict
	if len(base.AgentArgs) > 0 || len(override.AgentArgs) > 0 {
		result.AgentArgs = make(map[string]string)
		for k, v := range base.AgentArgs {
			result.AgentArgs[k] = v
		}
		for k, v := range override.AgentArgs {
			result.AgentArgs[k] = v
		}
	}

	// Lists: additive
	result.Mounts = append(append([]string{}, base.Mounts...), override.Mounts...)
	result.Ports = append(append([]string{}, base.Ports...), override.Ports...)
	result.CapAdd = append(append([]string{}, base.CapAdd...), override.CapAdd...)
	result.Devices = append(append([]string{}, base.Devices...), override.Devices...)
	result.Setup = append(append([]string{}, base.Setup...), override.Setup...)

	// Normalize empty slices to nil
	if len(result.Mounts) == 0 {
		result.Mounts = nil
	}
	if len(result.Ports) == 0 {
		result.Ports = nil
	}
	if len(result.CapAdd) == 0 {
		result.CapAdd = nil
	}
	if len(result.Devices) == 0 {
		result.Devices = nil
	}
	if len(result.Setup) == 0 {
		result.Setup = nil
	}
	if len(result.Env) == 0 {
		result.Env = nil
	}
	if len(result.AgentArgs) == 0 {
		result.AgentArgs = nil
	}

	// Resources: per-field override
	if base.Resources != nil || override.Resources != nil {
		result.Resources = &ResourceLimits{}
		if base.Resources != nil {
			result.Resources.CPUs = base.Resources.CPUs
			result.Resources.Memory = base.Resources.Memory
		}
		if override.Resources != nil {
			if override.Resources.CPUs != "" {
				result.Resources.CPUs = override.Resources.CPUs
			}
			if override.Resources.Memory != "" {
				result.Resources.Memory = override.Resources.Memory
			}
		}
	}

	// Network: Isolated overrides (last wins), Allow is additive
	if base.Network != nil || override.Network != nil {
		result.Network = &NetworkConfig{}
		if base.Network != nil {
			result.Network.Isolated = base.Network.Isolated
			result.Network.Allow = append(result.Network.Allow, base.Network.Allow...)
		}
		if override.Network != nil {
			result.Network.Isolated = override.Network.Isolated
			result.Network.Allow = append(result.Network.Allow, override.Network.Allow...)
		}
		if len(result.Network.Allow) == 0 {
			result.Network.Allow = nil
		}
	}

	return result
}

// LoadConfig reads ~/.yoloai/defaults/config.yaml and extracts known fields.
// Kept for backwards compatibility; new code should prefer LoadDefaultsConfig.
func LoadConfig() (*YoloaiConfig, error) {
	configPath := ConfigPath()

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/defaults/config.yaml
	if err != nil {
		if os.IsNotExist(err) {
			return &YoloaiConfig{}, nil
		}
		return nil, fmt.Errorf("read config.yaml: %w", err)
	}

	return parseConfigYAML(data, configPath, nil)
}

// LoadGlobalConfig reads ~/.yoloai/config.yaml and extracts global settings.
func LoadGlobalConfig() (*GlobalConfig, error) {
	configPath := GlobalConfigPath()

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/config.yaml
	if err != nil {
		if os.IsNotExist(err) {
			return &GlobalConfig{}, nil
		}
		return nil, fmt.Errorf("read global config.yaml: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse global config.yaml: %w", err)
	}

	cfg := &GlobalConfig{}

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
		case "tmux_conf":
			expanded, expandErr := expandEnvBraced(val.Value)
			if expandErr != nil {
				return nil, fmt.Errorf("tmux_conf: %w", expandErr)
			}
			cfg.TmuxConf = expanded
		case "model_aliases":
			if val.Kind == yaml.MappingNode {
				cfg.ModelAliases = make(map[string]string, len(val.Content)/2)
				for k := 0; k < len(val.Content)-1; k += 2 {
					aliasKey := val.Content[k].Value
					aliasExpanded, aliasErr := expandEnvBraced(val.Content[k+1].Value)
					if aliasErr != nil {
						return nil, fmt.Errorf("model_aliases.%s: %w", aliasKey, aliasErr)
					}
					cfg.ModelAliases[aliasKey] = aliasExpanded
				}
			}
		}
	}

	return cfg, nil
}

// ReadConfigRaw reads the raw bytes of config.yaml. Returns nil, nil if the
// file does not exist.
func ReadConfigRaw() ([]byte, error) {
	configPath := ConfigPath()
	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/defaults/config.yaml
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read config.yaml: %w", err)
	}
	return data, nil
}

// ReadGlobalConfigRaw reads the raw bytes of the global config.yaml.
// Returns nil, nil if the file does not exist.
func ReadGlobalConfigRaw() ([]byte, error) {
	configPath := GlobalConfigPath()
	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/config.yaml
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read global config.yaml: %w", err)
	}
	return data, nil
}

// isGlobalKey returns true if the top-level key belongs to global config
// rather than profile config.
func isGlobalKey(path string) bool {
	top := splitDottedPath(path)[0]
	for _, s := range globalKnownSettings {
		if splitDottedPath(s.Path)[0] == top {
			return true
		}
	}
	for _, cs := range globalKnownCollectionSettings {
		if splitDottedPath(cs.Path)[0] == top {
			return true
		}
	}
	return false
}

// IsGlobalKey is the exported version for use by CLI commands.
func IsGlobalKey(path string) bool {
	return isGlobalKey(path)
}

// GetEffectiveConfig returns YAML showing all known settings with their
// effective values (file overrides + defaults), plus any extra keys from the
// files that aren't in the known settings list.
func GetEffectiveConfig() (string, error) {
	// Build node tree with all known defaults (both global and profile).
	root := &yaml.Node{Kind: yaml.MappingNode}
	for _, s := range globalKnownSettings {
		setYAMLField(root, s.Path, s.Default)
	}
	for _, s := range knownSettings {
		setYAMLField(root, s.Path, s.Default)
	}

	// Add non-scalar defaults (maps/lists) as empty containers.
	for _, cs := range globalKnownCollectionSettings {
		parts := splitDottedPath(cs.Path)
		parent := root
		for _, p := range parts[:len(parts)-1] {
			parent = getOrCreateMapping(parent, p)
		}
		setNodeValue(parent, parts[len(parts)-1], &yaml.Node{Kind: cs.Kind})
	}
	for _, cs := range knownCollectionSettings {
		parts := splitDottedPath(cs.Path)
		parent := root
		for _, p := range parts[:len(parts)-1] {
			parent = getOrCreateMapping(parent, p)
		}
		setNodeValue(parent, parts[len(parts)-1], &yaml.Node{Kind: cs.Kind})
	}

	// Overlay values from the global config file.
	globalData, err := ReadGlobalConfigRaw()
	if err != nil {
		return "", err
	}
	if globalData != nil {
		var doc yaml.Node
		if err := yaml.Unmarshal(globalData, &doc); err == nil {
			if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
				if src := doc.Content[0]; src.Kind == yaml.MappingNode {
					mergeNodes(root, src)
				}
			}
		}
	}

	// Overlay values from the defaults config file.
	data, err := ReadConfigRaw()
	if err != nil {
		return "", err
	}
	if data != nil {
		var doc yaml.Node
		if err := yaml.Unmarshal(data, &doc); err == nil {
			if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
				if src := doc.Content[0]; src.Kind == yaml.MappingNode {
					mergeNodes(root, src)
				}
			}
		}
	}

	// Sort all mappings alphabetically for predictable, scannable output.
	sortMappingNodesRecursive(root)

	doc := yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return "", fmt.Errorf("marshal effective config: %w", err)
	}
	return string(out), nil
}

// mergeNodes recursively copies values from src into dst, overwriting
// existing values and adding keys that don't exist in dst.
func mergeNodes(dst, src *yaml.Node) {
	for i := 0; i < len(src.Content)-1; i += 2 {
		key := src.Content[i].Value
		val := src.Content[i+1]

		if val.Kind == yaml.MappingNode {
			dstChild := getOrCreateMapping(dst, key)
			mergeNodes(dstChild, val)
		} else {
			setNodeValue(dst, key, val)
		}
	}
}

// setNodeValue sets a key in a mapping node to the given value node,
// replacing any existing value. Works for all node kinds (scalar,
// sequence, mapping).
func setNodeValue(parent *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i < len(parent.Content)-1; i += 2 {
		if parent.Content[i].Value == key {
			parent.Content[i+1] = val
			return
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
	parent.Content = append(parent.Content, keyNode, val)
}

// GetConfigValue reads a value at the given dotted path from the appropriate
// config file. Global keys (tmux_conf, model_aliases) are read from
// ~/.yoloai/config.yaml; profile keys from ~/.yoloai/defaults/config.yaml.
// Returns the raw string value for scalars, or marshaled YAML for
// mappings/sequences. Falls back to the default for known settings.
// The bool return indicates whether the key was found (in file or defaults).
func GetConfigValue(path string) (string, bool, error) {
	var configPath string
	var defaults []knownSetting

	if isGlobalKey(path) {
		configPath = GlobalConfigPath()
		defaults = globalKnownSettings
	} else {
		configPath = ConfigPath()
		defaults = knownSettings
	}

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path from GlobalConfigPath/ConfigPath
	if err != nil {
		if os.IsNotExist(err) {
			return knownDefaultFrom(path, defaults)
		}
		return "", false, fmt.Errorf("read config: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", false, fmt.Errorf("parse config: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return knownDefaultFrom(path, defaults)
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return knownDefaultFrom(path, defaults)
	}

	parts := splitDottedPath(path)
	node := root
	for _, part := range parts {
		found := false
		for i := 0; i < len(node.Content)-1; i += 2 {
			if node.Content[i].Value == part {
				node = node.Content[i+1]
				found = true
				break
			}
		}
		if !found {
			return knownDefaultFrom(path, defaults)
		}
	}

	if node.Kind == yaml.ScalarNode {
		return node.Value, true, nil
	}

	// For mappings/sequences, sort and marshal the subtree.
	sortMappingNodesRecursive(node)
	out, err := yaml.Marshal(node)
	if err != nil {
		return "", false, fmt.Errorf("marshal subtree: %w", err)
	}
	return string(out), true, nil
}

// knownDefaultFrom returns the default value for a known setting path
// from the given defaults list.
func knownDefaultFrom(path string, defaults []knownSetting) (string, bool, error) {
	for _, s := range defaults {
		if s.Path == path {
			return s.Default, true, nil
		}
	}
	return "", false, nil
}

// UpdateConfigFields updates specific fields in config.yaml using yaml.Node
// manipulation to preserve comments and formatting.
func UpdateConfigFields(fields map[string]string) error {
	configPath := ConfigPath()

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/defaults/config.yaml
	if err != nil {
		return fmt.Errorf("read config.yaml: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse config.yaml: %w", err)
	}

	var root *yaml.Node
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 && doc.Content[0].Kind == yaml.MappingNode {
		root = doc.Content[0]
	} else {
		// Empty or all-comments file (e.g., scaffold): initialize a fresh mapping.
		root = &yaml.Node{Kind: yaml.MappingNode}
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{root}
	}

	for fieldPath, value := range fields {
		setYAMLField(root, fieldPath, value)
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("marshal config.yaml: %w", err)
	}

	if err := fileutil.WriteFile(configPath, out, 0600); err != nil {
		return fmt.Errorf("write config.yaml: %w", err)
	}

	return nil
}

// DeleteConfigField removes a key at a dotted path from config.yaml.
// Returns nil if the file doesn't exist or the key is already absent.
func DeleteConfigField(path string) error {
	configPath := ConfigPath()

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/defaults/config.yaml
	if err != nil {
		if os.IsNotExist(err) {
			return nil // nothing to delete
		}
		return fmt.Errorf("read config.yaml: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse config.yaml: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}

	deleteYAMLField(root, path)

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("marshal config.yaml: %w", err)
	}

	if err := fileutil.WriteFile(configPath, out, 0600); err != nil {
		return fmt.Errorf("write config.yaml: %w", err)
	}

	return nil
}

// UpdateGlobalConfigFields updates specific fields in the global config.yaml
// using yaml.Node manipulation to preserve comments and formatting.
func UpdateGlobalConfigFields(fields map[string]string) error {
	configPath := GlobalConfigPath()

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/config.yaml
	if err != nil {
		return fmt.Errorf("read global config.yaml: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse global config.yaml: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return fmt.Errorf("global config.yaml has unexpected structure")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("global config.yaml root is not a mapping")
	}

	for fieldPath, value := range fields {
		setYAMLField(root, fieldPath, value)
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("marshal global config.yaml: %w", err)
	}

	if err := fileutil.WriteFile(configPath, out, 0600); err != nil {
		return fmt.Errorf("write global config.yaml: %w", err)
	}

	return nil
}

// DeleteGlobalConfigField removes a key at a dotted path from the global config.yaml.
// Returns nil if the file doesn't exist or the key is already absent.
func DeleteGlobalConfigField(path string) error {
	configPath := GlobalConfigPath()

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/config.yaml
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read global config.yaml: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse global config.yaml: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}

	deleteYAMLField(root, path)

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("marshal global config.yaml: %w", err)
	}

	if err := fileutil.WriteFile(configPath, out, 0600); err != nil {
		return fmt.Errorf("write global config.yaml: %w", err)
	}

	return nil
}

// deleteYAMLField removes a key at a dotted path from a yaml.Node mapping tree.
func deleteYAMLField(root *yaml.Node, path string) {
	parts := splitDottedPath(path)
	node := root

	// Navigate to the parent mapping.
	for _, part := range parts[:len(parts)-1] {
		found := false
		for i := 0; i < len(node.Content)-1; i += 2 {
			if node.Content[i].Value == part && node.Content[i+1].Kind == yaml.MappingNode {
				node = node.Content[i+1]
				found = true
				break
			}
		}
		if !found {
			return // parent path doesn't exist, nothing to delete
		}
	}

	// Remove the leaf key-value pair.
	leafKey := parts[len(parts)-1]
	for i := 0; i < len(node.Content)-1; i += 2 {
		if node.Content[i].Value == leafKey {
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
			return
		}
	}
}

// setYAMLField sets a dotted path (e.g., "defaults.tmux_conf") to a value
// in a yaml.Node mapping tree. Creates intermediate mappings as needed.
func setYAMLField(root *yaml.Node, path string, value string) {
	parts := splitDottedPath(path)
	node := root

	// Navigate to the parent mapping, creating intermediate mappings as needed.
	for _, part := range parts[:len(parts)-1] {
		node = getOrCreateMapping(node, part)
	}

	// Set the leaf value.
	leafKey := parts[len(parts)-1]
	for i := 0; i < len(node.Content)-1; i += 2 {
		if node.Content[i].Value == leafKey {
			node.Content[i+1].Value = value
			node.Content[i+1].Tag = "!!str"
			if value == "true" || value == "false" {
				node.Content[i+1].Tag = "!!bool"
			}
			return
		}
	}

	// Key doesn't exist — append it.
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: leafKey}
	valNode := &yaml.Node{Kind: yaml.ScalarNode, Value: value, Tag: "!!str"}
	if value == "true" || value == "false" {
		valNode.Tag = "!!bool"
	}
	node.Content = append(node.Content, keyNode, valNode)
}

// getOrCreateMapping finds or creates a mapping node under the given key.
func getOrCreateMapping(parent *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(parent.Content)-1; i += 2 {
		if parent.Content[i].Value == key && parent.Content[i+1].Kind == yaml.MappingNode {
			return parent.Content[i+1]
		}
	}

	// Create new mapping.
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
	mapNode := &yaml.Node{Kind: yaml.MappingNode}
	parent.Content = append(parent.Content, keyNode, mapNode)
	return mapNode
}

// sortMappingNodesRecursive sorts all mapping nodes in the tree alphabetically.
func sortMappingNodesRecursive(node *yaml.Node) {
	if node.Kind == yaml.MappingNode {
		// Sort children first
		for i := 1; i < len(node.Content); i += 2 {
			sortMappingNodesRecursive(node.Content[i])
		}
		sortMappingNode(node)
	}
}

// sortMappingNode sorts a mapping node's key-value pairs alphabetically by key.
func sortMappingNode(node *yaml.Node) {
	n := len(node.Content) / 2
	if n < 2 {
		return
	}

	type kv struct {
		key *yaml.Node
		val *yaml.Node
	}
	pairs := make([]kv, n)
	for i := 0; i < n; i++ {
		pairs[i] = kv{node.Content[i*2], node.Content[i*2+1]}
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].key.Value < pairs[j].key.Value
	})
	for i, p := range pairs {
		node.Content[i*2] = p.key
		node.Content[i*2+1] = p.val
	}
}

// parseAgentFilesNode parses an agent_files yaml.Node into AgentFilesConfig.
// Supports two forms:
//   - String (scalar): base directory path, e.g. "~/.claude" or "${HOME}"
//   - List (sequence): explicit file/dir paths, e.g. ["~/.claude/settings.json"]
func parseAgentFilesNode(val *yaml.Node) (*AgentFilesConfig, error) {
	switch val.Kind {
	case yaml.ScalarNode:
		expanded, err := ExpandPath(val.Value)
		if err != nil {
			return nil, err
		}
		return &AgentFilesConfig{BaseDir: expanded}, nil
	case yaml.SequenceNode:
		files := make([]string, 0, len(val.Content))
		for _, item := range val.Content {
			expanded, err := ExpandPath(item.Value)
			if err != nil {
				return nil, err
			}
			files = append(files, expanded)
		}
		return &AgentFilesConfig{Files: files}, nil
	default:
		return nil, fmt.Errorf("expected string or list, got %v", val.Kind)
	}
}

// splitDottedPath splits "a.b.c" into ["a", "b", "c"].
func splitDottedPath(path string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(path); i++ {
		if path[i] == '.' {
			parts = append(parts, path[start:i])
			start = i + 1
		}
	}
	parts = append(parts, path[start:])
	return parts
}

// FindYAMLValue returns the value node for a top-level key in a mapping.
func FindYAMLValue(root *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(root.Content)-1; i += 2 {
		if root.Content[i].Value == key {
			return root.Content[i+1]
		}
	}
	return nil
}
