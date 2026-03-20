package sandbox

// ABOUTME: Per-sandbox persistent state tracking (sandbox-state.json).
// ABOUTME: Tracks initialization flags like agent_files seeding across container restarts.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/fileutil"
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
	if err := fileutil.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write %s: %w", SandboxStateFile, err)
	}

	return nil
}

// LoadSandboxState reads sandbox-state.json from the given sandbox directory.
// Returns a zero-value SandboxState if the file does not exist.
func LoadSandboxState(sandboxDir string) (*SandboxState, error) {
	path := filepath.Join(sandboxDir, SandboxStateFile)

	data, err := os.ReadFile(path) //nolint:gosec // path is constructed from sandbox dir
	if err != nil {
		if os.IsNotExist(err) {
			return &SandboxState{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", SandboxStateFile, err)
	}

	var state SandboxState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse %s: %w", SandboxStateFile, err)
	}

	return &state, nil
}
