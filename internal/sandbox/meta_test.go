package sandbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

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

	data, err := os.ReadFile(filepath.Join(dir, "meta.json")) //nolint:gosec // test file in temp dir
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
