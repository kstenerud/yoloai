// ABOUTME: Tests for the Q104 per-sandbox migration that relocates agent/model
// ABOUTME: from environment.json into agent.json (orchestrator.MigrateAgentConfigs).
package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/orchestrator/agentcfg"
	"github.com/kstenerud/yoloai/store"
)

// writeRawEnv writes a raw environment.json into a fresh sandbox dir under the
// layout and returns that sandbox dir.
func writeRawEnv(t *testing.T, layout config.Layout, name, rawJSON string) string {
	t.Helper()
	sandboxDir := layout.SandboxDir(name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, store.EnvironmentFile), []byte(rawJSON), 0o600))
	return sandboxDir
}

func rawEnvKeys(t *testing.T, sandboxDir string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sandboxDir, store.EnvironmentFile)) //nolint:gosec // G304: test file in a temp dir
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

// TestMigrateAgentConfigs_RelocatesAndStamps is the core C3 case: a pre-Q104 v2
// record carrying agent/model and no agent.json migrates to a v3 record without
// those keys, and the values land in agent.json. After migration LoadEnvironment
// (which balks below v3) succeeds.
func TestMigrateAgentConfigs_RelocatesAndStamps(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	sandboxDir := writeRawEnv(t, layout, "box", `{
		"version": 2,
		"name": "box",
		"backend": "docker",
		"agent": "claude",
		"model": "opus",
		"dirs": [{"host_path": "/proj", "mount_path": "/proj", "mode": "copy"}]
	}`)

	require.NoError(t, MigrateAgentConfigs(layout))

	// agent.json now holds the inside-process config.
	acfg, err := agentcfg.Load(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, "claude", acfg.AgentType)
	assert.Equal(t, "opus", acfg.Model)

	// environment.json is stamped v3 and no longer carries agent/model.
	keys := rawEnvKeys(t, sandboxDir)
	assert.NotContains(t, keys, "agent")
	assert.NotContains(t, keys, "model")
	assert.JSONEq(t, "3", string(keys["version"]))

	// The slimmed record now loads without balking.
	meta, err := store.LoadEnvironment(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, "box", meta.Name)
}

// TestMigrateAgentConfigs_Idempotent asserts a second run is a no-op: the
// already-migrated v3 record (and its agent.json) are left untouched.
func TestMigrateAgentConfigs_Idempotent(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	sandboxDir := writeRawEnv(t, layout, "box", `{
		"version": 2,
		"name": "box",
		"backend": "docker",
		"agent": "claude",
		"model": "opus",
		"dirs": [{"host_path": "/proj", "mount_path": "/proj", "mode": "copy"}]
	}`)

	require.NoError(t, MigrateAgentConfigs(layout))
	firstEnv, err := os.ReadFile(filepath.Join(sandboxDir, store.EnvironmentFile)) //nolint:gosec // G304: test file in a temp dir
	require.NoError(t, err)

	require.NoError(t, MigrateAgentConfigs(layout))
	secondEnv, err := os.ReadFile(filepath.Join(sandboxDir, store.EnvironmentFile)) //nolint:gosec // G304: test file in a temp dir
	require.NoError(t, err)

	assert.Equal(t, string(firstEnv), string(secondEnv), "a second migration must not rewrite a v3 record")
	acfg, err := agentcfg.Load(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, "claude", acfg.AgentType)
}

// TestMigrateAgentConfigs_ResumesAfterCrash simulates a crash between the
// agent.json write and the environment.json strip: agent.json already exists but
// the record is still v2 with the keys present. A re-run must complete the
// migration (strip keys, stamp v3) without losing data.
func TestMigrateAgentConfigs_ResumesAfterCrash(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	sandboxDir := writeRawEnv(t, layout, "box", `{
		"version": 2,
		"name": "box",
		"backend": "docker",
		"agent": "claude",
		"model": "opus",
		"dirs": [{"host_path": "/proj", "mount_path": "/proj", "mode": "copy"}]
	}`)
	// Pre-existing agent.json (the durable write that survived the "crash").
	require.NoError(t, agentcfg.Save(sandboxDir, &agentcfg.AgentConfig{AgentType: "claude", Model: "opus"}))

	require.NoError(t, MigrateAgentConfigs(layout))

	keys := rawEnvKeys(t, sandboxDir)
	assert.NotContains(t, keys, "agent")
	assert.JSONEq(t, "3", string(keys["version"]))
	acfg, err := agentcfg.Load(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, "claude", acfg.AgentType)
	assert.Equal(t, "opus", acfg.Model)
}

// TestMigrateAgentConfigs_MigratesV0Record covers the oldest records: a
// pre-versioning v0 environment.json (no version field, legacy single-workdir
// shape) gets its agent/model relocated AND its substrate fields brought current
// by the in-struct ladder (backend/image backfill, dirs collapse).
func TestMigrateAgentConfigs_MigratesV0Record(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	sandboxDir := writeRawEnv(t, layout, "old", `{
		"name": "old",
		"agent": "claude",
		"workdir": {"host_path": "/proj", "mount_path": "/proj", "mode": "copy"}
	}`)

	require.NoError(t, MigrateAgentConfigs(layout))

	acfg, err := agentcfg.Load(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, "claude", acfg.AgentType)

	meta, err := store.LoadEnvironment(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, "yoloai-base", meta.ImageRef, "v0 image backfill")
	require.Len(t, meta.Dirs, 1, "legacy workdir collapsed into Dirs[0]")
	assert.Equal(t, "/proj", meta.Dirs[0].HostPath)
}

// TestMigrateAgentConfigs_NoSandboxesDir is a fresh install: the sandboxes dir
// does not exist yet, which is not an error.
func TestMigrateAgentConfigs_NoSandboxesDir(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	require.NoError(t, MigrateAgentConfigs(layout))
}
