package sandbox

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// yoloaiConfig holds the subset of config.yaml fields that the Go code reads.
type yoloaiConfig struct {
	SetupComplete bool   `yaml:"setup_complete"`
	TmuxConf      string `yaml:"tmux_conf"` // from defaults.tmux_conf
}

// loadConfig reads ~/.yoloai/config.yaml and extracts known fields.
func loadConfig() (*yoloaiConfig, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home directory: %w", err)
	}
	configPath := filepath.Join(homeDir, ".yoloai", "config.yaml")

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/config.yaml
	if err != nil {
		if os.IsNotExist(err) {
			return &yoloaiConfig{}, nil
		}
		return nil, fmt.Errorf("read config.yaml: %w", err)
	}

	// Parse into a yaml.Node tree to extract fields without losing structure.
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse config.yaml: %w", err)
	}

	cfg := &yoloaiConfig{}

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
					if val.Content[j].Value == "tmux_conf" {
						cfg.TmuxConf = val.Content[j+1].Value
					}
				}
			}
		}
	}

	return cfg, nil
}

// updateConfigFields updates specific fields in config.yaml using yaml.Node
// manipulation to preserve comments and formatting.
func updateConfigFields(fields map[string]string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home directory: %w", err)
	}
	configPath := filepath.Join(homeDir, ".yoloai", "config.yaml")

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
