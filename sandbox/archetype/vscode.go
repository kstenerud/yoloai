// ABOUTME: VS Code workspace file injection for the devcontainer archetype.
// ABOUTME: Writes .vscode/extensions.json and .vscode/settings.json into the workdir copy.

package archetype

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/fileutil"
)

// InjectVSCodeWorkspace writes VS Code workspace files into the workdir copy
// based on devcontainer.json customizations. Called only when vscode-tunnel
// is active and the workdir mode supports writes (:copy or :overlay).
// Existing keys win — project-checked-in settings are preserved.
func InjectVSCodeWorkspace(workdirCopyPath string, dc *DevcontainerConfig) error {
	if dc == nil {
		return nil
	}
	extensions := dc.Customizations.VSCode.Extensions
	settings := dc.Customizations.VSCode.Settings
	if len(extensions) == 0 && len(settings) == 0 {
		return nil
	}

	vscodeDir := filepath.Join(workdirCopyPath, ".vscode")
	if err := fileutil.MkdirAll(vscodeDir, 0750); err != nil {
		return fmt.Errorf("create .vscode dir: %w", err)
	}

	if len(extensions) > 0 {
		if err := mergeExtensionsJSON(vscodeDir, extensions); err != nil {
			return fmt.Errorf("merge extensions.json: %w", err)
		}
	}

	if len(settings) > 0 {
		if err := mergeSettingsJSON(vscodeDir, settings); err != nil {
			return fmt.Errorf("merge settings.json: %w", err)
		}
	}

	return nil
}

// mergeExtensionsJSON merges extensions into .vscode/extensions.json.
// Existing recommendations are preserved; new ones are appended (dedup).
func mergeExtensionsJSON(vscodeDir string, newExtensions []string) error {
	path := filepath.Join(vscodeDir, "extensions.json")

	var existing struct {
		Recommendations []string `json:"recommendations"`
	}

	data, err := os.ReadFile(path) //nolint:gosec // G304: path is in workdir copy
	if err == nil {
		// File exists — unmarshal it; if unparseable, preserve as-is and skip injection
		if jsonErr := json.Unmarshal(data, &existing); jsonErr != nil {
			return nil //nolint:nilerr // intentional: preserve unparseable file
		}
	}

	// Dedup: existing extensions take precedence; append new ones not already present
	seen := make(map[string]bool)
	for _, e := range existing.Recommendations {
		seen[e] = true
	}
	for _, e := range newExtensions {
		if !seen[e] {
			existing.Recommendations = append(existing.Recommendations, e)
			seen[e] = true
		}
	}

	out, err := json.MarshalIndent(map[string]any{
		"recommendations": existing.Recommendations,
	}, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFile(path, out, 0644) //nolint:gosec // G306: .vscode/extensions.json is not a secret
}

// mergeSettingsJSON merges settings into .vscode/settings.json.
// Existing keys win — project-checked-in settings are preserved.
func mergeSettingsJSON(vscodeDir string, newSettings map[string]any) error {
	path := filepath.Join(vscodeDir, "settings.json")

	merged := make(map[string]any)

	// Start with devcontainer settings as base
	maps.Copy(merged, newSettings)

	// Overlay with existing file (existing keys win)
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is in workdir copy
	if err == nil {
		var existing map[string]any
		if jsonErr := json.Unmarshal(data, &existing); jsonErr == nil {
			// existing always wins
			maps.Copy(merged, existing)
		}
	}

	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFile(path, out, 0644) //nolint:gosec // G306: .vscode/settings.json is not a secret
}
