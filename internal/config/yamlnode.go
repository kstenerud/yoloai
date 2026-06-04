package config

// ABOUTME: Generic dotted-path YAML-node CRUD engine: effective-config assembly,
// ABOUTME: get/set/delete by dotted path, and low-level yaml.Node walkers.

import (
	"fmt"
	"os"
	"sort"

	"github.com/kstenerud/yoloai/internal/fileutil"
	"gopkg.in/yaml.v3"
)

// buildEffectiveConfigDefaults constructs a yaml.Node mapping tree with all
// known settings at their default values (scalars and empty collections).
func buildEffectiveConfigDefaults() *yaml.Node {
	root := &yaml.Node{Kind: yaml.MappingNode}
	for _, s := range globalKnownSettings {
		setYAMLField(root, s.Path, s.Default)
	}
	for _, s := range knownSettings {
		setYAMLField(root, s.Path, s.Default)
	}
	for _, cs := range globalKnownCollectionSettings {
		setCollectionDefault(root, cs)
	}
	for _, cs := range knownCollectionSettings {
		setCollectionDefault(root, cs)
	}
	return root
}

// setCollectionDefault adds an empty collection node at the path given by cs.
func setCollectionDefault(root *yaml.Node, cs knownCollectionSetting) {
	parts := splitDottedPath(cs.Path)
	parent := root
	for _, p := range parts[:len(parts)-1] {
		parent = getOrCreateMapping(parent, p)
	}
	setNodeValue(parent, parts[len(parts)-1], &yaml.Node{Kind: cs.Kind})
}

// overlayConfigFile reads raw YAML from readFn and merges it into root.
// Errors from readFn and YAML parse errors are both returned — a config file
// the user wrote but that fails to parse must surface, not be silently dropped
// from the effective view.
func overlayConfigFile(root *yaml.Node, readFn func() ([]byte, error)) error {
	data, err := readFn()
	if err != nil {
		return err
	}
	if data == nil {
		return nil
	}
	return mergeRawYAMLInto(root, data)
}

// mergeRawYAMLInto parses data as YAML and merges its top-level mapping into root.
// A parse error is returned so the caller can report the malformed file.
func mergeRawYAMLInto(root *yaml.Node, data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse config YAML: %w", err)
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		if src := doc.Content[0]; src.Kind == yaml.MappingNode {
			mergeNodes(root, src)
		}
	}
	return nil
}

// GetEffectiveConfig returns YAML showing all known settings with their
// effective values (file overrides + defaults), plus any extra keys from the
// files that aren't in the known settings list.
func GetEffectiveConfig(layout Layout) (string, error) {
	root := buildEffectiveConfigDefaults()

	if err := overlayConfigFile(root, func() ([]byte, error) { return ReadGlobalConfigRaw(layout) }); err != nil {
		return "", err
	}
	if err := overlayConfigFile(root, func() ([]byte, error) { return ReadConfigRaw(layout) }); err != nil {
		return "", err
	}

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
// layout.GlobalConfigPath(); profile keys from layout.DefaultsConfigPath().
// Returns the raw string value for scalars, or marshaled YAML for
// mappings/sequences. Falls back to the default for known settings.
// The bool return indicates whether the key was found (in file or defaults).
func GetConfigValue(layout Layout, path string) (string, bool, error) {
	var configPath string
	var defaults []knownSetting

	if isGlobalKey(path) {
		configPath = layout.GlobalConfigPath()
		defaults = globalKnownSettings
	} else {
		configPath = layout.DefaultsConfigPath()
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

// UpdateConfigFields updates specific fields in DataDir/defaults/config.yaml using yaml.Node
// manipulation to preserve comments and formatting.
func UpdateConfigFields(layout Layout, fields map[string]string) error {
	configPath := layout.DefaultsConfigPath()

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is DataDir/defaults/config.yaml
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

// DeleteConfigField removes a key at a dotted path from DataDir/defaults/config.yaml.
// Returns nil if the file doesn't exist or the key is already absent.
func DeleteConfigField(layout Layout, path string) error {
	configPath := layout.DefaultsConfigPath()

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is DataDir/defaults/config.yaml
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

// UpdateGlobalConfigFields updates specific fields in DataDir/config.yaml
// using yaml.Node manipulation to preserve comments and formatting.
func UpdateGlobalConfigFields(layout Layout, fields map[string]string) error {
	configPath := layout.GlobalConfigPath()

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is DataDir/config.yaml
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

// DeleteGlobalConfigField removes a key at a dotted path from DataDir/config.yaml.
// Returns nil if the file doesn't exist or the key is already absent.
func DeleteGlobalConfigField(layout Layout, path string) error {
	configPath := layout.GlobalConfigPath()

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is DataDir/config.yaml
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
	for i := range n {
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

// FindYAMLValue returns the value node for a top-level key in a mapping.
func FindYAMLValue(root *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(root.Content)-1; i += 2 {
		if root.Content[i].Value == key {
			return root.Content[i+1]
		}
	}
	return nil
}
