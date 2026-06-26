// ABOUTME: Q104 per-sandbox migration — relocate the inside-process config
// ABOUTME: (agent, model) out of environment.json into the sibling agent.json.
package orchestrator

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/orchestrator/agentcfg"
	"github.com/kstenerud/yoloai/internal/store"
)

// MigrateAgentConfigs relocates the agent/model fields of every sandbox's
// environment.json (schema < v3) into a sibling agent.json (Q104). It is the
// per-sandbox pass of `yoloai system migrate`, run after the realm-level
// MigrateLibrary stamps the data dir. It lives in the orchestrator (not
// internal/config, which drives the realm migration) because it needs the store
// and agentcfg types that internal/config cannot import.
//
// The pass is idempotent: a record already at v3 is skipped, and a re-run after
// a crash mid-migration resumes safely (agent.json is written durably before
// environment.json is rewritten, so the irreplaceable agent/model values are
// never lost). A missing sandboxes dir is not an error (a fresh install).
func MigrateAgentConfigs(layout config.Layout) error {
	entries, err := os.ReadDir(layout.SandboxesDir())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read sandboxes dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sandboxDir := filepath.Join(layout.SandboxesDir(), e.Name())
		if err := migrateAgentConfigRecord(sandboxDir); err != nil {
			return fmt.Errorf("sandbox %q: %w", e.Name(), err)
		}
	}
	return nil
}

// migrateAgentConfigRecord migrates one sandbox. It reads environment.json's raw
// bytes, and if the record is below the current schema version it: (1) extracts
// agent/model, (2) writes agent.json FIRST (durable copy of the only data that
// exists nowhere else), then (3) re-saves environment.json through the slimmed
// struct — which drops agent/model by construction and stamps the current
// version — after running the in-struct migration ladder for any older record.
//
// Step ordering is the data-safety guarantee: should the process die between
// steps 2 and 3, the agent/model values are already in agent.json, the record
// is still < v3, and a re-run repeats the (idempotent) steps to completion.
func migrateAgentConfigRecord(sandboxDir string) error {
	path := filepath.Join(sandboxDir, store.EnvironmentFile)
	data, err := os.ReadFile(path) //nolint:gosec // G304: trusted sandbox subpath
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // partial/dormant directory with no record to migrate
		}
		return fmt.Errorf("read %s: %w", store.EnvironmentFile, err)
	}

	var legacy struct {
		Version   int    `json:"version"`
		AgentType string `json:"agent"`
		Model     string `json:"model"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return fmt.Errorf("parse %s: %w", store.EnvironmentFile, err)
	}
	if legacy.Version >= (&store.Environment{}).SchemaVersion() {
		return nil // already migrated (agent.json was written by create or a prior run)
	}

	// (2) Durable copy of the irreplaceable values, before touching the record.
	if err := agentcfg.Save(sandboxDir, &agentcfg.AgentConfig{AgentType: legacy.AgentType, Model: legacy.Model}); err != nil {
		return fmt.Errorf("write %s: %w", agentcfg.AgentConfigFile, err)
	}

	// (3) Re-save environment.json through the slimmed struct: unmarshalling the
	// original bytes drops agent/model (the struct no longer has them), the
	// ladder backfills any pre-v2 substrate fields, and SaveEnvironment stamps
	// the current version.
	var meta store.Environment
	if err := json.Unmarshal(data, &meta); err != nil {
		return fmt.Errorf("parse %s: %w", store.EnvironmentFile, err)
	}
	if err := store.MigrateEnvironment(&meta); err != nil {
		return err
	}
	if err := store.SaveEnvironment(sandboxDir, &meta); err != nil {
		return err
	}
	return nil
}
