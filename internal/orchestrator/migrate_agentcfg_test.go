// ABOUTME: Tests for the Q104/D90 per-sandbox migration that relocates agent/model
// ABOUTME: from environment.json into agent.json and network policy into netpolicy.json.
package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/netpolicycfg"
	"github.com/kstenerud/yoloai/internal/orchestrator/agentcfg"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/store"
)

// Every path in this file is a literal, and that is deliberate (DF164). This
// migrator's subject is the FLAT, pre-tier layout, so a fixture built with the
// live path builders would follow the builders wherever they move — agreeing
// with the migrator while both disagree with every sandbox on disk. That is
// exactly how the v6 tier move broke this migrator with the suite still green.
// Do not "tidy" these into store.EnvironmentFilePath / config.AgentConfigPath.
const (
	flatEnvFile       = "environment.json"
	flatAgentCfgFile  = "agent.json"
	flatNetpolicyFile = "netpolicy.json"
)

// writeRawEnv writes a raw environment.json into a fresh sandbox dir under the
// layout, at the flat pre-tier location, and returns that sandbox dir.
func writeRawEnv(t *testing.T, layout config.Layout, name, rawJSON string) string {
	t.Helper()
	sandboxDir := layout.SandboxDir(name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0o750))
	testutil.WriteSandboxRecord(t, filepath.Join(sandboxDir, flatEnvFile), []byte(rawJSON))
	return sandboxDir
}

func rawEnvKeys(t *testing.T, sandboxDir string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sandboxDir, flatEnvFile)) //nolint:gosec // G304: trusted sandbox subpath
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

// readFlatRecord decodes a sibling record from its flat pre-tier path. Reading
// it back through agentcfg.Load / netpolicycfg.Load would resolve into host/,
// which is where these files do NOT belong until the v6 tier move runs.
func readFlatRecord(t *testing.T, sandboxDir, file string, into any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sandboxDir, file)) //nolint:gosec // G304: trusted sandbox subpath
	require.NoError(t, err, "%s must be written at the flat pre-tier path", file)
	require.NoError(t, json.Unmarshal(data, into))
}

// assertNoHostTier is the negative half: the pre-tier migrator must not create
// the v6 host/ directory, because everything it writes belongs at the root
// until the tier move runs.
func assertNoHostTier(t *testing.T, sandboxDir string) {
	t.Helper()
	_, err := os.Stat(filepath.Join(sandboxDir, "host"))
	assert.True(t, os.IsNotExist(err), "a pre-v6 migrator must not create the host/ tier")
}

// TestMigrateAgentConfigs_RelocatesAndStamps is the core C3 case: a pre-Q104 v2
// record carrying agent/model and network fields migrates to a v3 record without
// those keys, and the values land in agent.json and netpolicy.json respectively.
// After migration LoadEnvironment (which balks below v3) succeeds.
func TestMigrateAgentConfigs_RelocatesAndStamps(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	sandboxDir := writeRawEnv(t, layout, "box", `{
		"version": 2,
		"name": "box",
		"backend": "docker",
		"agent": "claude",
		"model": "opus",
		"network_mode": "isolated",
		"network_allow": ["api.anthropic.com"],
		"dirs": [{"host_path": "/proj", "mount_path": "/proj", "mode": "copy"}]
	}`)

	require.NoError(t, MigrateAgentConfigs(layout))

	// agent.json now holds the inside-process config, flat.
	var acfg agentcfg.AgentConfig
	readFlatRecord(t, sandboxDir, flatAgentCfgFile, &acfg)
	assert.Equal(t, "claude", acfg.AgentType)
	assert.Equal(t, "opus", acfg.Model)

	// netpolicy.json now holds the network policy, flat.
	var np netpolicycfg.Netpolicy
	readFlatRecord(t, sandboxDir, flatNetpolicyFile, &np)
	assert.Equal(t, "isolated", np.Mode)
	assert.Equal(t, []string{"api.anthropic.com"}, np.Allow)

	// Earlier rungs preserve v3; only the DNS snapshot migrator advances it.
	keys := rawEnvKeys(t, sandboxDir)
	assert.NotContains(t, keys, "agent")
	assert.NotContains(t, keys, "model")
	assert.NotContains(t, keys, "network_mode")
	assert.NotContains(t, keys, "network_allow")
	assert.JSONEq(t, "3", string(keys["version"]))
	_, err := store.LoadEnvironmentFrom(filepath.Join(sandboxDir, flatEnvFile))
	require.ErrorIs(t, err, store.ErrNeedsMigration)

	assertNoHostTier(t, sandboxDir)
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
	firstEnv, err := os.ReadFile(filepath.Join(sandboxDir, flatEnvFile)) //nolint:gosec // G304: trusted sandbox subpath
	require.NoError(t, err)

	require.NoError(t, MigrateAgentConfigs(layout))
	secondEnv, err := os.ReadFile(filepath.Join(sandboxDir, flatEnvFile)) //nolint:gosec // G304: trusted sandbox subpath
	require.NoError(t, err)

	assert.Equal(t, string(firstEnv), string(secondEnv), "a second migration must not rewrite a v3 record")
	var acfg agentcfg.AgentConfig
	readFlatRecord(t, sandboxDir, flatAgentCfgFile, &acfg)
	assert.Equal(t, "claude", acfg.AgentType)
}

// TestMigrateAgentConfigs_ResumesAfterCrash simulates a crash between the
// sibling file writes and the environment.json strip: both sibling files already
// exist but the record is still v2 with the keys present. A re-run must complete
// the migration (strip keys, stamp v3) without losing data.
func TestMigrateAgentConfigs_ResumesAfterCrash(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	sandboxDir := writeRawEnv(t, layout, "box", `{
		"version": 2,
		"name": "box",
		"backend": "docker",
		"agent": "claude",
		"model": "opus",
		"network_mode": "isolated",
		"network_allow": ["a.example"],
		"dirs": [{"host_path": "/proj", "mount_path": "/proj", "mode": "copy"}]
	}`)
	// Pre-seed both sibling files (the durable writes that survived the "crash").
	require.NoError(t, agentcfg.SaveTo(filepath.Join(sandboxDir, flatAgentCfgFile),
		&agentcfg.AgentConfig{AgentType: "claude", Model: "opus"}))
	require.NoError(t, netpolicycfg.SaveTo(filepath.Join(sandboxDir, flatNetpolicyFile),
		&netpolicycfg.Netpolicy{Mode: "isolated", Allow: []string{"a.example"}}))

	require.NoError(t, MigrateAgentConfigs(layout))

	keys := rawEnvKeys(t, sandboxDir)
	assert.NotContains(t, keys, "agent")
	assert.NotContains(t, keys, "network_mode")
	assert.NotContains(t, keys, "network_allow")
	assert.JSONEq(t, "3", string(keys["version"]))
	var acfg agentcfg.AgentConfig
	readFlatRecord(t, sandboxDir, flatAgentCfgFile, &acfg)
	assert.Equal(t, "claude", acfg.AgentType)
	assert.Equal(t, "opus", acfg.Model)
	var np netpolicycfg.Netpolicy
	readFlatRecord(t, sandboxDir, flatNetpolicyFile, &np)
	assert.Equal(t, "isolated", np.Mode)
	assert.Equal(t, []string{"a.example"}, np.Allow)
	assertNoHostTier(t, sandboxDir)
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

	var acfg agentcfg.AgentConfig
	readFlatRecord(t, sandboxDir, flatAgentCfgFile, &acfg)
	assert.Equal(t, "claude", acfg.AgentType)

	var meta store.Environment
	readFlatRecord(t, sandboxDir, flatEnvFile, &meta)
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

// TestMigrateAgentConfigs_RelocatesNetworkFields covers the D90 relocation specifically:
// a v2 record with agent/model AND network_mode/network_allow — after migration both
// agent.json and netpolicy.json carry the data, environment.json carries neither.
func TestMigrateAgentConfigs_RelocatesNetworkFields(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	sandboxDir := writeRawEnv(t, layout, "netbox", `{
		"version": 2,
		"name": "netbox",
		"backend": "docker",
		"agent": "gemini",
		"model": "pro",
		"network_mode": "isolated",
		"network_allow": ["generativelanguage.googleapis.com", "sentry.io"],
		"dirs": [{"host_path": "/repo", "mount_path": "/repo", "mode": "copy"}]
	}`)

	require.NoError(t, MigrateAgentConfigs(layout))

	// agent.json gets agent/model.
	var acfg agentcfg.AgentConfig
	readFlatRecord(t, sandboxDir, flatAgentCfgFile, &acfg)
	assert.Equal(t, "gemini", acfg.AgentType)
	assert.Equal(t, "pro", acfg.Model)

	// netpolicy.json gets the network policy.
	var np netpolicycfg.Netpolicy
	readFlatRecord(t, sandboxDir, flatNetpolicyFile, &np)
	assert.Equal(t, "isolated", np.Mode)
	assert.Equal(t, []string{"generativelanguage.googleapis.com", "sentry.io"}, np.Allow)

	// environment.json carries none of the relocated keys.
	keys := rawEnvKeys(t, sandboxDir)
	assert.NotContains(t, keys, "agent", "agent must be absent from environment.json")
	assert.NotContains(t, keys, "model", "model must be absent from environment.json")
	assert.NotContains(t, keys, "network_mode", "network_mode must be absent from environment.json")
	assert.NotContains(t, keys, "network_allow", "network_allow must be absent from environment.json")
	assert.JSONEq(t, "3", string(keys["version"]))
}
