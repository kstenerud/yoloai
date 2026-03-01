package sandbox

// ABOUTME: One-time migration from old flat ~/.yoloai/ layout to profiles/base/.
// ABOUTME: Moves resource files, transforms config.yaml, extracts state.yaml.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// MigrateIfNeeded detects the old ~/.yoloai/ layout and transforms it to
// the new profiles/base/ structure. Idempotent — does nothing if already
// migrated or if ~/.yoloai/ doesn't exist yet.
func MigrateIfNeeded(yoloaiDir string) error {
	if !needsMigration(yoloaiDir) {
		return nil
	}

	baseDir := filepath.Join(yoloaiDir, "profiles", "base")
	if err := os.MkdirAll(baseDir, 0750); err != nil {
		return fmt.Errorf("create profiles/base: %w", err)
	}

	// Move resource files
	resourceMoves := []struct {
		oldName string
		newName string
	}{
		{"Dockerfile.base", "Dockerfile"},
		{"entrypoint.sh", "entrypoint.sh"},
		{"tmux.conf", "tmux.conf"},
		{".last-build-checksum", ".last-build-checksum"},
		{".tart-provisioned", ".tart-provisioned"},
	}

	for _, rm := range resourceMoves {
		if err := moveIfExists(filepath.Join(yoloaiDir, rm.oldName), filepath.Join(baseDir, rm.newName)); err != nil {
			return fmt.Errorf("move %s: %w", rm.oldName, err)
		}
		// Also move .new conflict files
		if err := moveIfExists(filepath.Join(yoloaiDir, rm.oldName+".new"), filepath.Join(baseDir, rm.newName+".new")); err != nil {
			return fmt.Errorf("move %s.new: %w", rm.oldName, err)
		}
	}

	// Move and transform .resource-checksums (rename Dockerfile.base key to Dockerfile)
	if err := migrateChecksums(yoloaiDir, baseDir); err != nil {
		return fmt.Errorf("migrate checksums: %w", err)
	}

	// Transform config.yaml: extract state, flatten defaults
	if err := migrateConfig(yoloaiDir, baseDir); err != nil {
		return fmt.Errorf("migrate config: %w", err)
	}

	return nil
}

// needsMigration returns true if the old layout is detected.
func needsMigration(yoloaiDir string) bool {
	// Check for old-style Dockerfile.base at top level
	if _, err := os.Stat(filepath.Join(yoloaiDir, "Dockerfile.base")); err == nil {
		return true
	}

	// Check for old-style config.yaml with setup_complete or defaults key
	configPath := filepath.Join(yoloaiDir, "config.yaml")
	data, err := os.ReadFile(configPath) //nolint:gosec // G304: yoloaiDir is ~/.yoloai/
	if err != nil {
		return false
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return false
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return false
	}

	for i := 0; i < len(root.Content)-1; i += 2 {
		key := root.Content[i].Value
		if key == "setup_complete" || key == "defaults" {
			return true
		}
	}
	return false
}

// moveIfExists moves src to dst if src exists.
func moveIfExists(src, dst string) error {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil
	}
	return os.Rename(src, dst)
}

// migrateChecksums moves .resource-checksums and renames the Dockerfile.base
// key to Dockerfile inside the JSON.
func migrateChecksums(yoloaiDir, baseDir string) error {
	oldPath := filepath.Join(yoloaiDir, ".resource-checksums")
	newPath := filepath.Join(baseDir, ".resource-checksums")

	data, err := os.ReadFile(oldPath) //nolint:gosec // G304: yoloaiDir is ~/.yoloai/
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var checksums map[string]string
	if err := json.Unmarshal(data, &checksums); err != nil {
		// Corrupt file — just move it as-is
		return os.Rename(oldPath, newPath)
	}

	// Rename key
	if val, ok := checksums["Dockerfile.base"]; ok {
		checksums["Dockerfile"] = val
		delete(checksums, "Dockerfile.base")
	}

	out, err := json.MarshalIndent(checksums, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(newPath, out, 0600); err != nil {
		return err
	}
	return os.Remove(oldPath)
}

// migrateConfig reads old config.yaml, extracts setup_complete into state.yaml,
// flattens the defaults mapping to root level, and writes the new config to
// profiles/base/config.yaml. Deletes old config.yaml on success.
func migrateConfig(yoloaiDir, baseDir string) error {
	oldPath := filepath.Join(yoloaiDir, "config.yaml")
	data, err := os.ReadFile(oldPath) //nolint:gosec // G304: yoloaiDir is ~/.yoloai/
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		// Unparseable — move as-is to new location
		newPath := filepath.Join(baseDir, "config.yaml")
		if err := os.WriteFile(newPath, data, 0600); err != nil {
			return err
		}
		return os.Remove(oldPath)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return os.Remove(oldPath)
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return os.Remove(oldPath)
	}

	// Build new config root with flattened keys
	newRoot := &yaml.Node{Kind: yaml.MappingNode}
	setupComplete := false

	for i := 0; i < len(root.Content)-1; i += 2 {
		key := root.Content[i]
		val := root.Content[i+1]

		switch key.Value {
		case "setup_complete":
			setupComplete = val.Value == "true"
		case "defaults":
			// Flatten: promote children to root level
			if val.Kind == yaml.MappingNode {
				newRoot.Content = append(newRoot.Content, val.Content...)
			}
		default:
			// Preserve any extra root-level keys
			newRoot.Content = append(newRoot.Content, key, val)
		}
	}

	// Write state.yaml
	state := &State{SetupComplete: setupComplete}
	stateData, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	if err := os.WriteFile(filepath.Join(yoloaiDir, "state.yaml"), stateData, 0600); err != nil {
		return fmt.Errorf("write state.yaml: %w", err)
	}

	// Write new config.yaml
	newDoc := yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{newRoot}}
	newData, err := yaml.Marshal(&newDoc)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	newConfigPath := filepath.Join(baseDir, "config.yaml")
	if err := os.WriteFile(newConfigPath, newData, 0600); err != nil {
		return fmt.Errorf("write new config: %w", err)
	}

	// Remove old config
	return os.Remove(oldPath)
}
