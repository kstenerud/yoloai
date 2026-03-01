package sandbox

// ABOUTME: Config loading, reading, and writing for ~/.yoloai/config.yaml.
// ABOUTME: Provides dotted-path get/set with YAML comment preservation.

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// YoloaiConfig holds the subset of config.yaml fields that the Go code reads.
type YoloaiConfig struct {
	SetupComplete bool              `yaml:"setup_complete"`
	TmuxConf      string            `yaml:"tmux_conf"`  // from defaults.tmux_conf
	Backend       string            `yaml:"backend"`    // from defaults.backend
	TartImage     string            `yaml:"tart_image"` // from defaults.tart_image — custom base VM image for tart backend
	Agent         string            `yaml:"agent"`      // from defaults.agent
	Model         string            `yaml:"model"`      // from defaults.model
	Env           map[string]string `yaml:"env"`        // from defaults.env — environment variables passed to container
}

// knownSetting defines a config key with its default value.
type knownSetting struct {
	Path    string
	Default string
}

// knownSettings lists every scalar config key the code recognizes, with defaults.
// Used by GetEffectiveConfig and GetConfigValue to fill in unset values.
var knownSettings = []knownSetting{
	{"setup_complete", "false"},
	{"defaults.backend", "docker"},
	{"defaults.tart_image", ""},
	{"defaults.tmux_conf", ""},
	{"defaults.agent", "claude"},
	{"defaults.model", ""},
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
	{"defaults.env", yaml.MappingNode},
}

// ConfigPath returns the path to ~/.yoloai/config.yaml.
func ConfigPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home directory: %w", err)
	}
	return filepath.Join(homeDir, ".yoloai", "config.yaml"), nil
}

// LoadConfig reads ~/.yoloai/config.yaml and extracts known fields.
func LoadConfig() (*YoloaiConfig, error) {
	configPath, err := ConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/config.yaml
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
		case "setup_complete":
			cfg.SetupComplete = val.Value == "true"
		case "defaults":
			if val.Kind == yaml.MappingNode {
				for j := 0; j < len(val.Content)-1; j += 2 {
					fieldName := val.Content[j].Value
					fieldVal := val.Content[j+1]

					// env is a mapping, not a scalar — handle separately
					if fieldName == "env" {
						if fieldVal.Kind == yaml.MappingNode {
							cfg.Env = make(map[string]string, len(fieldVal.Content)/2)
							for k := 0; k < len(fieldVal.Content)-1; k += 2 {
								envKey := fieldVal.Content[k].Value
								envExpanded, envErr := expandEnvBraced(fieldVal.Content[k+1].Value)
								if envErr != nil {
									return nil, fmt.Errorf("defaults.env.%s: %w", envKey, envErr)
								}
								cfg.Env[envKey] = envExpanded
							}
						}
						continue
					}

					expanded, err := expandEnvBraced(fieldVal.Value)
					if err != nil {
						return nil, fmt.Errorf("defaults.%s: %w", fieldName, err)
					}
					switch fieldName {
					case "tmux_conf":
						cfg.TmuxConf = expanded
					case "backend":
						cfg.Backend = expanded
					case "tart_image":
						cfg.TartImage = expanded
					case "agent":
						cfg.Agent = expanded
					case "model":
						cfg.Model = expanded
					}
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
	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/config.yaml
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read config.yaml: %w", err)
	}
	return data, nil
}

// GetEffectiveConfig returns YAML showing all known settings with their
// effective values (file overrides + defaults), plus any extra keys from the
// file that aren't in the known settings list.
func GetEffectiveConfig() (string, error) {
	// Build node tree with all known defaults.
	root := &yaml.Node{Kind: yaml.MappingNode}
	for _, s := range knownSettings {
		setYAMLField(root, s.Path, s.Default)
	}

	// Add non-scalar defaults (maps/lists) as empty containers.
	for _, cs := range knownCollectionSettings {
		parts := splitDottedPath(cs.Path)
		parent := root
		for _, p := range parts[:len(parts)-1] {
			parent = getOrCreateMapping(parent, p)
		}
		setNodeValue(parent, parts[len(parts)-1], &yaml.Node{Kind: cs.Kind})
	}

	// Overlay values from the actual config file.
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

// GetConfigValue reads a value at the given dotted path from config.yaml.
// Returns the raw string value for scalars, or marshaled YAML for
// mappings/sequences. Falls back to the default for known settings.
// The bool return indicates whether the key was found (in file or defaults).
func GetConfigValue(path string) (string, bool, error) {
	configPath, err := ConfigPath()
	if err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/config.yaml
	if err != nil {
		if os.IsNotExist(err) {
			return knownDefault(path)
		}
		return "", false, fmt.Errorf("read config.yaml: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", false, fmt.Errorf("parse config.yaml: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return knownDefault(path)
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return knownDefault(path)
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
			return knownDefault(path)
		}
	}

	if node.Kind == yaml.ScalarNode {
		return node.Value, true, nil
	}

	// For mappings/sequences, marshal the subtree.
	out, err := yaml.Marshal(node)
	if err != nil {
		return "", false, fmt.Errorf("marshal subtree: %w", err)
	}
	return string(out), true, nil
}

// knownDefault returns the default value for a known setting path.
func knownDefault(path string) (string, bool, error) {
	for _, s := range knownSettings {
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

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/config.yaml
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
