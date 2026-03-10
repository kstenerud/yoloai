package sandbox

// ABOUTME: Per-sandbox persistent state tracking (sandbox-state.json).
// ABOUTME: Tracks initialization flags like agent_files seeding across container restarts.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SandboxState tracks per-sandbox persistent state across container restarts.
// Stored as sandbox-state.json in the sandbox directory alongside environment.json.
type SandboxState struct {
	AgentFilesInitialized bool `json:"agent_files_initialized"`
}

// SaveSandboxState writes sandbox-state.json to the given sandbox directory.
func SaveSandboxState(sandboxDir string, state *SandboxState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", SandboxStateFile, err)
	}

	path := filepath.Join(sandboxDir, SandboxStateFile)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write %s: %w", SandboxStateFile, err)
	}

	return nil
}

// LoadSandboxState reads sandbox-state.json from the given sandbox directory.
// Falls back to legacy state.json, then returns a zero-value SandboxState if
// neither file exists (backward compatibility).
func LoadSandboxState(sandboxDir string) (*SandboxState, error) {
	path := filepath.Join(sandboxDir, SandboxStateFile)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		path = filepath.Join(sandboxDir, legacySandboxState) // legacy fallback
	}

	data, err := os.ReadFile(path) //nolint:gosec // path is constructed from sandbox dir
	if err != nil {
		if os.IsNotExist(err) {
			return &SandboxState{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}

	var state SandboxState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}

	return &state, nil
}
