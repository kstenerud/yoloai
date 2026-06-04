package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	// Blank imports register backend descriptors so migrate() can look up
	// HostFilesystem from runtime.Descriptor(name). The actual factories
	// are not invoked here — only the static descriptors are needed.
	_ "github.com/kstenerud/yoloai/internal/runtime/docker"
	_ "github.com/kstenerud/yoloai/internal/runtime/seatbelt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMeta_SaveLoadRoundTrip is the comprehensive serialization round-trip: it
// populates every persisted field (including the pointer-typed Resources, the
// typed-segment Principal, and the slice fields Ports/NetworkAllow) and asserts
// the loaded value equals the original byte-for-byte. A full-struct assert.Equal
// subsumes every per-field round-trip, so there are deliberately no per-field
// round-trip variants — only tests for distinct logic (omitempty, version
// stamping, migration, version-too-new) live alongside.
func TestMeta_SaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()

	original := &Environment{
		YoloaiVersion: "1.0.0",
		Name:          "fix-build",
		Principal:     "acme",
		CreatedAt:     time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
		AgentType:     "claude",
		Model:         "claude-sonnet-4-latest",
		Workdir: WorkdirEnvironment{
			HostPath:    "/home/user/projects/my-app",
			MountPath:   "/home/user/projects/my-app",
			Mode:        "copy",
			BaselineSHA: "a1b2c3d4e5f6",
		},
		HasPrompt:    true,
		NetworkMode:  "isolated",
		NetworkAllow: []string{"api.anthropic.com", "sentry.io"},
		Ports:        []string{"3000:3000", "8080:8080"},
		Resources: &config.ResourceLimits{
			CPUs:   "4",
			Memory: "8g",
		},
	}

	err := SaveEnvironment(dir, original)
	require.NoError(t, err)

	loaded, err := LoadEnvironment(dir)
	require.NoError(t, err)

	assert.Equal(t, original, loaded)
}

func TestMeta_OmitEmptyFields(t *testing.T) {
	dir := t.TempDir()

	meta := &Environment{
		YoloaiVersion: "1.0.0",
		Name:          "test-sandbox",
		CreatedAt:     time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
		AgentType:     "claude",
		Workdir: WorkdirEnvironment{
			HostPath:  "/tmp/project",
			MountPath: "/tmp/project",
			Mode:      "copy",
		},
	}

	err := SaveEnvironment(dir, meta)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, EnvironmentFile)) //nolint:gosec // test file in temp dir
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.NotContains(t, raw, "model")
	assert.NotContains(t, raw, "network_mode")
	assert.NotContains(t, raw, "ports")
	// Default (no-principal) sandboxes omit the field, so existing
	// environment.json files load as the default principal unchanged.
	assert.NotContains(t, raw, "principal")
}

func TestMeta_VersionSetOnSave(t *testing.T) {
	dir := t.TempDir()

	meta := &Environment{
		Name:      "test-version",
		AgentType: "claude",
		Workdir: WorkdirEnvironment{
			HostPath:  "/tmp/project",
			MountPath: "/tmp/project",
			Mode:      "copy",
		},
	}

	require.NoError(t, SaveEnvironment(dir, meta))
	assert.Equal(t, metaVersion, meta.Version)

	loaded, err := LoadEnvironment(dir)
	require.NoError(t, err)
	assert.Equal(t, metaVersion, loaded.Version)
}

func TestMeta_MigrateV0ToV1_Docker(t *testing.T) {
	dir := t.TempDir()

	// Write a legacy environment.json without a version field (simulates pre-versioning sandboxes).
	legacyJSON := `{
		"yoloai_version": "0.1.0",
		"name": "old-sandbox",
		"created_at": "2025-01-01T00:00:00Z",
		"backend": "docker",
		"agent": "claude",
		"workdir": {"host_path": "/tmp/proj", "mount_path": "/tmp/proj", "mode": "copy"}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, EnvironmentFile), []byte(legacyJSON), 0600))

	loaded, err := LoadEnvironment(dir)
	require.NoError(t, err)
	assert.Equal(t, 1, loaded.Version, "v0 should be migrated to v1")
	assert.False(t, loaded.HostFilesystem, "docker backend should have HostFilesystem=false")
}

func TestMeta_MigrateV0ToV1_Seatbelt(t *testing.T) {
	dir := t.TempDir()

	legacyJSON := `{
		"yoloai_version": "0.1.0",
		"name": "old-seatbelt",
		"created_at": "2025-01-01T00:00:00Z",
		"backend": "seatbelt",
		"agent": "claude",
		"workdir": {"host_path": "/tmp/proj", "mount_path": "/tmp/proj", "mode": "copy"}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, EnvironmentFile), []byte(legacyJSON), 0600))

	loaded, err := LoadEnvironment(dir)
	require.NoError(t, err)
	assert.Equal(t, 1, loaded.Version, "v0 should be migrated to v1")
	assert.True(t, loaded.HostFilesystem, "seatbelt backend should have HostFilesystem=true")
}

func TestMeta_MigrateV0ToV1_UnknownBackendDefaultsToFalse(t *testing.T) {
	// If a meta file names a backend that isn't registered on this platform
	// (or doesn't exist at all), migration should default HostFilesystem to
	// false rather than panicking or rejecting the load. The conservative
	// answer keeps the upgrade path forward-compatible.
	dir := t.TempDir()

	legacyJSON := `{
		"yoloai_version": "0.1.0",
		"name": "old-mystery",
		"created_at": "2025-01-01T00:00:00Z",
		"backend": "not-a-real-backend",
		"agent": "claude",
		"workdir": {"host_path": "/tmp/proj", "mount_path": "/tmp/proj", "mode": "copy"}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, EnvironmentFile), []byte(legacyJSON), 0600))

	loaded, err := LoadEnvironment(dir)
	require.NoError(t, err)
	assert.Equal(t, 1, loaded.Version, "v0 should be migrated to v1 even with unknown backend")
	assert.False(t, loaded.HostFilesystem, "unknown backend should default to HostFilesystem=false")
}

func TestMeta_FutureVersionReturnsError(t *testing.T) {
	dir := t.TempDir()

	futureJSON := `{"version": 9999, "name": "future", "agent": "claude",
		"workdir": {"host_path": "/tmp", "mount_path": "/tmp", "mode": "copy"}}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, EnvironmentFile), []byte(futureJSON), 0600))

	_, err := LoadEnvironment(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "newer version")
}

func TestMeta_ResourcesOmittedWhenNil(t *testing.T) {
	dir := t.TempDir()

	meta := &Environment{
		YoloaiVersion: "1.0.0",
		Name:          "no-resources",
		CreatedAt:     time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC),
		AgentType:     "claude",
		Workdir: WorkdirEnvironment{
			HostPath:  "/tmp/project",
			MountPath: "/tmp/project",
			Mode:      "copy",
		},
	}

	err := SaveEnvironment(dir, meta)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, EnvironmentFile)) //nolint:gosec
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.NotContains(t, raw, "resources")
}
