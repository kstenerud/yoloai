// ABOUTME: Loads and validates .yoloai.yaml project configuration files.
// ABOUTME: Provides archetype declaration, extra mounts, and version requirements for projects.

package archetype

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/config"
	"gopkg.in/yaml.v3"
)

// YoloAIProjectConfig holds the parsed contents of a project's .yoloai.yaml file.
// This file is checked into the project repo and expresses project-level environment requirements.
type YoloAIProjectConfig struct {
	Archetype string            `yaml:"archetype,omitempty"`
	Mounts    []string          `yaml:"mounts,omitempty"`
	Requires  map[string]string `yaml:"requires,omitempty"`
}

// LoadYoloAIYaml looks for .yoloai.yaml in workdir.
// Returns (nil, false, nil) if not found.
// Returns (nil, false, error) on parse failures or unknown archetype.
// Expands tilde in each Mounts entry via config.ExpandPath.
// homeDir is used for ~ expansion; callers derive it from layout.HomeDir.
func LoadYoloAIYaml(workdir, homeDir string) (*YoloAIProjectConfig, bool, error) {
	path := filepath.Join(workdir, ".yoloai.yaml")
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is workdir + fixed filename
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read .yoloai.yaml: %w", err)
	}

	var cfg YoloAIProjectConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, false, fmt.Errorf("parse .yoloai.yaml: %w", err)
	}

	// Validate archetype if specified
	if cfg.Archetype != "" {
		if _, err := ParseArchetype(cfg.Archetype); err != nil {
			return nil, false, fmt.Errorf(".yoloai.yaml: %w", err)
		}
	}

	// Expand tilde in mounts
	for i, m := range cfg.Mounts {
		// Only expand the host part (before the first colon that isn't part of a Windows path)
		expanded, err := config.ExpandPath(m, homeDir)
		if err != nil {
			return nil, false, fmt.Errorf(".yoloai.yaml: expand mount path %q: %w", m, err)
		}
		cfg.Mounts[i] = expanded
	}

	return &cfg, true, nil
}
