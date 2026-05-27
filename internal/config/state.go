package config

// ABOUTME: Operational state persistence via DataDir/state.yaml.
// ABOUTME: Separates mutable runtime state from user configuration.

import (
	"fmt"
	"os"

	"github.com/kstenerud/yoloai/internal/fileutil"
	"gopkg.in/yaml.v3"
)

// State holds operational state that is not user configuration.
type State struct {
	SetupComplete bool `yaml:"setup_complete"`
}

// LoadState reads layout.StatePath() (DataDir/state.yaml).
// Returns zero-value State if the file is missing.
func LoadState(layout Layout) (*State, error) {
	statePath := layout.StatePath()

	data, err := os.ReadFile(statePath) //nolint:gosec // G304: path is DataDir/state.yaml
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

// SaveState writes state to layout.StatePath() (DataDir/state.yaml).
func SaveState(layout Layout, s *State) error {
	statePath := layout.StatePath()

	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal state.yaml: %w", err)
	}

	if err := fileutil.WriteFile(statePath, data, 0600); err != nil {
		return fmt.Errorf("write state.yaml: %w", err)
	}
	return nil
}
