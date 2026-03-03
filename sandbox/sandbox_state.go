package sandbox

// ABOUTME: Per-sandbox persistent state tracking (state.json).
// ABOUTME: Tracks initialization flags like agent_files seeding across container restarts.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SandboxState tracks per-sandbox persistent state across container restarts.
// Stored as state.json in the sandbox directory alongside meta.json.
type SandboxState struct {
	AgentFilesInitialized bool `json:"agent_files_initialized"`
}

// SaveSandboxState writes state.json to the given sandbox directory.
func SaveSandboxState(sandboxDir string, state *SandboxState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state.json: %w", err)
	}

	path := filepath.Join(sandboxDir, "state.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write state.json: %w", err)
	}

	return nil
}

// LoadSandboxState reads state.json from the given sandbox directory.
// Returns a zero-value SandboxState if the file doesn't exist (backward
// compatibility with sandboxes created before state.json was introduced).
func LoadSandboxState(sandboxDir string) (*SandboxState, error) {
	path := filepath.Join(sandboxDir, "state.json")

	data, err := os.ReadFile(path) //nolint:gosec // path is constructed from sandbox dir
	if err != nil {
		if os.IsNotExist(err) {
			return &SandboxState{}, nil
		}
		return nil, fmt.Errorf("read state.json: %w", err)
	}

	var state SandboxState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse state.json: %w", err)
	}

	return &state, nil
}
