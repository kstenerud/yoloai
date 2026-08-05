// ABOUTME: Per-sandbox inside-process config (agent.json), owned by the orchestration layer,
// ABOUTME: kept separate from the substrate record store.Environment (D98 / Q104 split).
package agentcfg

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
)

// AgentConfigFile is the filename for the per-sandbox inside-process config.
const AgentConfigFile = config.AgentConfigFileName

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
	if err := config.EnsureHostTier(sandboxDir); err != nil {
		return fmt.Errorf("create host tier: %w", err)
	}
	return SaveTo(config.AgentConfigPath(sandboxDir), cfg)
}

// SaveTo writes the record to an explicit path, stamping and serializing it
// exactly as Save does but resolving nothing: the caller supplies the path and
// owns the directory's existence. It exists for migrators, which must address
// the layout of the era they are migrating FROM (internal/config/pretier,
// DF164). Everything else calls Save.
func SaveTo(path string, cfg *AgentConfig) error {
	cfg.Version = schemaVersion

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", AgentConfigFile, err)
	}
	if err := fileutil.AtomicWriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write %s: %w", AgentConfigFile, err)
	}
	return nil
}

// Load reads agent.json from the given sandbox directory.
// Returns a zero-value AgentConfig if the file does not exist.
func Load(sandboxDir string) (*AgentConfig, error) {
	path := config.AgentConfigPath(sandboxDir)

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
