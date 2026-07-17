// ABOUTME: Per-sandbox inside-process config (agent.json), owned by the orchestration layer,
// ABOUTME: kept separate from the substrate record store.Environment (D98 / Q104 split).
package agentcfg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/fileutil"
)

// AgentConfigFile is the filename for the per-sandbox inside-process config.
const AgentConfigFile = "agent.json"

const schemaVersion = 1

// AgentConfig holds the inside-process config for a sandbox's tenant agent: which agent
// type runs inside and its model. Persisted per-sandbox separately from the substrate
// record store.Environment (D98 / Q104 split).
type AgentConfig struct {
	Version   int    `json:"version"`
	AgentType string `json:"agent"`
	Model     string `json:"model,omitempty"`
}

// Save writes agent.json to the given sandbox directory.
func Save(sandboxDir string, cfg *AgentConfig) error {
	cfg.Version = schemaVersion

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", AgentConfigFile, err)
	}

	path := filepath.Join(sandboxDir, AgentConfigFile)
	if err := fileutil.AtomicWriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write %s: %w", AgentConfigFile, err)
	}

	return nil
}

// Load reads agent.json from the given sandbox directory.
// Returns a zero-value AgentConfig if the file does not exist.
func Load(sandboxDir string) (*AgentConfig, error) {
	path := filepath.Join(sandboxDir, AgentConfigFile)

	data, err := os.ReadFile(path) //nolint:gosec // path is constructed from sandbox dir
	if err != nil {
		if os.IsNotExist(err) {
			return &AgentConfig{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", AgentConfigFile, err)
	}

	var cfg AgentConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", AgentConfigFile, err)
	}

	return &cfg, nil
}
