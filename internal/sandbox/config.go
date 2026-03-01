package sandbox

// ABOUTME: Config loading for profile (~/.yoloai/profiles/base/config.yaml) and
// ABOUTME: global (~/.yoloai/config.yaml) configs. Provides dotted-path get/set.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// YoloaiConfig holds the subset of config.yaml fields that the Go code reads.
type YoloaiConfig struct {
	Backend   string            `yaml:"backend"`    // backend
	TartImage string            `yaml:"tart_image"` // tart.image — custom base VM image for tart backend
	Agent     string            `yaml:"agent"`      // agent
	Model     string            `yaml:"model"`      // model
	Env       map[string]string `yaml:"env"`        // env — environment variables passed to container
	Resources *ResourceLimits   `yaml:"resources"`  // resources — container resource limits
	Network   *NetworkConfig    `yaml:"network"`    // network — network isolation settings
	Mounts    []string          `yaml:"mounts"`     // mounts — extra bind mounts (host:container[:ro])
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
	{"backend", "docker"},
	{"tart.image", ""},
	{"agent", "claude"},
	{"model", ""},
	{"resources.cpus", ""},
	{"resources.memory", ""},
	{"network.isolated", "false"},
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
	{"env", yaml.MappingNode},
	{"mounts", yaml.SequenceNode},
	{"network.allow", yaml.SequenceNode},
}

// globalKnownSettings lists scalar config keys belonging to the global config.
var globalKnownSettings = []knownSetting{
	{"tmux_conf", ""},
}

// globalKnownCollectionSettings lists non-scalar config keys belonging to global config.
var globalKnownCollectionSettings = []knownCollectionSetting{
	{"model_aliases", yaml.MappingNode},
}

// ConfigPath returns the path to ~/.yoloai/profiles/base/config.yaml.
func ConfigPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home directory: %w", err)
	}
	return filepath.Join(homeDir, ".yoloai", "profiles", "base", "config.yaml"), nil
}

// GlobalConfigPath returns the path to ~/.yoloai/config.yaml.
func GlobalConfigPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home directory: %w", err)
	}
	return filepath.Join(homeDir, ".yoloai", "config.yaml"), nil
}

// LoadConfig reads ~/.yoloai/profiles/base/config.yaml and extracts known fields.
func LoadConfig() (*YoloaiConfig, error) {
	configPath, err := ConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/profiles/base/config.yaml
	if err != nil {
		if os.IsNotExist(err) {
			return &YoloaiConfig{}, nil
		}
		return nil, fmt.Errorf("read config.yaml: %w", err)
	}

	// Parse into a yaml.Node tree to extract fields without losing structure.
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse config.yaml: %w", err)
	}

	cfg := &YoloaiConfig{}

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
		case "backend":
			expanded, err := expandEnvBraced(val.Value)
			if err != nil {
				return nil, fmt.Errorf("backend: %w", err)
			}
			cfg.Backend = expanded
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
		}
	}

	return cfg, nil
}

// LoadGlobalConfig reads ~/.yoloai/config.yaml and extracts global settings.
func LoadGlobalConfig() (*GlobalConfig, error) {
	configPath, err := GlobalConfigPath()
	if err != nil {
		return nil, err
	}

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
	configPath, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/profiles/base/config.yaml
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
	configPath, err := GlobalConfigPath()
	if err != nil {
		return nil, err
	}
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

	// Overlay values from the profile config file.
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
// ~/.yoloai/config.yaml; profile keys from ~/.yoloai/profiles/base/config.yaml.
// Returns the raw string value for scalars, or marshaled YAML for
// mappings/sequences. Falls back to the default for known settings.
// The bool return indicates whether the key was found (in file or defaults).
func GetConfigValue(path string) (string, bool, error) {
	var configPath string
	var err error
	var defaults []knownSetting

	if isGlobalKey(path) {
		configPath, err = GlobalConfigPath()
		defaults = globalKnownSettings
	} else {
		configPath, err = ConfigPath()
		defaults = knownSettings
	}
	if err != nil {
		return "", false, err
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
	configPath, err := ConfigPath()
	if err != nil {
		return err
	}

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/profiles/base/config.yaml
	if err != nil {
		return fmt.Errorf("read config.yaml: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse config.yaml: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return fmt.Errorf("config.yaml has unexpected structure")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("config.yaml root is not a mapping")
	}

	for fieldPath, value := range fields {
		setYAMLField(root, fieldPath, value)
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("marshal config.yaml: %w", err)
	}

	if err := os.WriteFile(configPath, out, 0600); err != nil {
		return fmt.Errorf("write config.yaml: %w", err)
	}

	return nil
}

// DeleteConfigField removes a key at a dotted path from config.yaml.
// Returns nil if the file doesn't exist or the key is already absent.
func DeleteConfigField(path string) error {
	configPath, err := ConfigPath()
	if err != nil {
		return err
	}

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/profiles/base/config.yaml
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

	if err := os.WriteFile(configPath, out, 0600); err != nil {
		return fmt.Errorf("write config.yaml: %w", err)
	}

	return nil
}

// UpdateGlobalConfigFields updates specific fields in the global config.yaml
// using yaml.Node manipulation to preserve comments and formatting.
func UpdateGlobalConfigFields(fields map[string]string) error {
	configPath, err := GlobalConfigPath()
	if err != nil {
		return err
	}

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

	if err := os.WriteFile(configPath, out, 0600); err != nil {
		return fmt.Errorf("write global config.yaml: %w", err)
	}

	return nil
}

// DeleteGlobalConfigField removes a key at a dotted path from the global config.yaml.
// Returns nil if the file doesn't exist or the key is already absent.
func DeleteGlobalConfigField(path string) error {
	configPath, err := GlobalConfigPath()
	if err != nil {
		return err
	}

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

	if err := os.WriteFile(configPath, out, 0600); err != nil {
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
