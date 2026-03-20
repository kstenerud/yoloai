package config

// ABOUTME: Operational state persistence via ~/.yoloai/state.yaml.
// ABOUTME: Separates mutable runtime state from user configuration.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/fileutil"
	"gopkg.in/yaml.v3"
)

// State holds operational state that is not user configuration.
type State struct {
	SetupComplete bool `yaml:"setup_complete"`
}

// StatePath returns the path to ~/.yoloai/state.yaml.
func StatePath() string {
	return filepath.Join(YoloaiDir(), "state.yaml")
}

// LoadState reads ~/.yoloai/state.yaml. Returns zero-value State if missing.
func LoadState() (*State, error) {
	statePath := StatePath()

	data, err := os.ReadFile(statePath) //nolint:gosec // G304: path is ~/.yoloai/state.yaml
	if err != nil {
		if os.IsNotExist(err) {
			return &State{}, nil
		}
		return nil, fmt.Errorf("read state.yaml: %w", err)
	}

	var s State
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse state.yaml: %w", err)
	}
	return &s, nil
}

// SaveState writes state to ~/.yoloai/state.yaml.
func SaveState(s *State) error {
	statePath := StatePath()

	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal state.yaml: %w", err)
	}

	if err := fileutil.WriteFile(statePath, data, 0600); err != nil {
		return fmt.Errorf("write state.yaml: %w", err)
	}
	return nil
}
