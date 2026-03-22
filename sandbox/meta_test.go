package sandbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMeta_SaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()

	original := &Meta{
		YoloaiVersion: "1.0.0",
		Name:          "fix-build",
		CreatedAt:     time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
		Agent:         "claude",
		Model:         "claude-sonnet-4-latest",
		Workdir: WorkdirMeta{
			HostPath:    "/home/user/projects/my-app",
			MountPath:   "/home/user/projects/my-app",
			Mode:        "copy",
			BaselineSHA: "a1b2c3d4e5f6",
		},
		HasPrompt:   true,
		NetworkMode: "none",
		Ports:       []string{"3000:3000"},
	}

	err := SaveMeta(dir, original)
	require.NoError(t, err)

	loaded, err := LoadMeta(dir)
	require.NoError(t, err)

	assert.Equal(t, original, loaded)
}

func TestMeta_OmitEmptyFields(t *testing.T) {
	dir := t.TempDir()

	meta := &Meta{
		YoloaiVersion: "1.0.0",
		Name:          "test-sandbox",
		CreatedAt:     time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
		Agent:         "claude",
		Workdir: WorkdirMeta{
			HostPath:  "/tmp/project",
			MountPath: "/tmp/project",
			Mode:      "copy",
		},
	}

	err := SaveMeta(dir, meta)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, EnvironmentFile)) //nolint:gosec // test file in temp dir
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.NotContains(t, raw, "model")
	assert.NotContains(t, raw, "network_mode")
	assert.NotContains(t, raw, "ports")
}

func TestMeta_WithPortsAndNetwork(t *testing.T) {
	dir := t.TempDir()

	original := &Meta{
		YoloaiVersion: "1.0.0",
		Name:          "web-dev",
		CreatedAt:     time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC),
		Agent:         "claude",
		Workdir: WorkdirMeta{
			HostPath:    "/home/user/web-app",
			MountPath:   "/home/user/web-app",
			Mode:        "copy",
			BaselineSHA: "deadbeef",
		},
		NetworkMode: "none",
		Ports:       []string{"3000:3000", "8080:8080"},
	}

	err := SaveMeta(dir, original)
	require.NoError(t, err)

	loaded, err := LoadMeta(dir)
	require.NoError(t, err)

	assert.Equal(t, "none", loaded.NetworkMode)
	assert.Equal(t, []string{"3000:3000", "8080:8080"}, loaded.Ports)
}

func TestMeta_NetworkAllowRoundTrip(t *testing.T) {
	dir := t.TempDir()

	original := &Meta{
		YoloaiVersion: "1.0.0",
		Name:          "iso-test",
		CreatedAt:     time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC),
		Agent:         "claude",
		Workdir: WorkdirMeta{
			HostPath:  "/tmp/project",
			MountPath: "/tmp/project",
			Mode:      "copy",
		},
		NetworkMode:  "isolated",
		NetworkAllow: []string{"api.anthropic.com", "sentry.io"},
	}

	err := SaveMeta(dir, original)
	require.NoError(t, err)

	loaded, err := LoadMeta(dir)
	require.NoError(t, err)

	assert.Equal(t, "isolated", loaded.NetworkMode)
	assert.Equal(t, []string{"api.anthropic.com", "sentry.io"}, loaded.NetworkAllow)
}

func TestMeta_ResourcesRoundTrip(t *testing.T) {
	dir := t.TempDir()

	original := &Meta{
		YoloaiVersion: "1.0.0",
		Name:          "resource-test",
		CreatedAt:     time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC),
		Agent:         "claude",
		Workdir: WorkdirMeta{
			HostPath:  "/tmp/project",
			MountPath: "/tmp/project",
			Mode:      "copy",
		},
		Resources: &config.ResourceLimits{
			CPUs:   "4",
			Memory: "8g",
		},
	}

	err := SaveMeta(dir, original)
	require.NoError(t, err)

	loaded, err := LoadMeta(dir)
	require.NoError(t, err)

	require.NotNil(t, loaded.Resources)
	assert.Equal(t, "4", loaded.Resources.CPUs)
	assert.Equal(t, "8g", loaded.Resources.Memory)
}

func TestMeta_VersionSetOnSave(t *testing.T) {
	dir := t.TempDir()

	meta := &Meta{
		Name:  "test-version",
		Agent: "claude",
		Workdir: WorkdirMeta{
			HostPath:  "/tmp/project",
			MountPath: "/tmp/project",
			Mode:      "copy",
		},
	}

	require.NoError(t, SaveMeta(dir, meta))
	assert.Equal(t, metaVersion, meta.Version)

	loaded, err := LoadMeta(dir)
	require.NoError(t, err)
	assert.Equal(t, metaVersion, loaded.Version)
}

func TestMeta_MigrateV0ToV1_Docker(t *testing.T) {
	dir := t.TempDir()

	// Write a legacy meta.json without a version field (simulates pre-versioning sandboxes).
	legacyJSON := `{
		"yoloai_version": "0.1.0",
		"name": "old-sandbox",
		"created_at": "2025-01-01T00:00:00Z",
		"backend": "docker",
		"agent": "claude",
		"workdir": {"host_path": "/tmp/proj", "mount_path": "/tmp/proj", "mode": "copy"}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, EnvironmentFile), []byte(legacyJSON), 0600))

	loaded, err := LoadMeta(dir)
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

	loaded, err := LoadMeta(dir)
	require.NoError(t, err)
	assert.Equal(t, 1, loaded.Version, "v0 should be migrated to v1")
	assert.True(t, loaded.HostFilesystem, "seatbelt backend should have HostFilesystem=true")
}

func TestMeta_FutureVersionReturnsError(t *testing.T) {
	dir := t.TempDir()

	futureJSON := `{"version": 9999, "name": "future", "agent": "claude",
		"workdir": {"host_path": "/tmp", "mount_path": "/tmp", "mode": "copy"}}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, EnvironmentFile), []byte(futureJSON), 0600))

	_, err := LoadMeta(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "newer version")
}

func TestMeta_ResourcesOmittedWhenNil(t *testing.T) {
	dir := t.TempDir()

	meta := &Meta{
		YoloaiVersion: "1.0.0",
		Name:          "no-resources",
		CreatedAt:     time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC),
		Agent:         "claude",
		Workdir: WorkdirMeta{
			HostPath:  "/tmp/project",
			MountPath: "/tmp/project",
			Mode:      "copy",
		},
	}

	err := SaveMeta(dir, meta)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, EnvironmentFile)) //nolint:gosec
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.NotContains(t, raw, "resources")
}
