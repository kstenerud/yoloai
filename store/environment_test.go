package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
	// Blank imports register backend descriptors so migrate() can look up
	// HostFilesystem from runtime.Descriptor(name). The actual factories
	// are not invoked here — only the static descriptors are needed.
	_ "github.com/kstenerud/yoloai/runtime/docker"
	_ "github.com/kstenerud/yoloai/runtime/seatbelt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMeta_SaveLoadRoundTrip is the comprehensive serialization round-trip: it
// populates every persisted field (including the pointer-typed Resources, the
// typed-segment Principal, and the slice fields Ports) and asserts the loaded
// value equals the original byte-for-byte. A full-struct assert.Equal subsumes
// every per-field round-trip, so there are deliberately no per-field round-trip
// variants — only tests for distinct logic (omitempty, version stamping,
// migration, version-too-new) live alongside. Agent/model are no longer substrate
// fields (Q104); network_mode/network_allow are no longer substrate fields (D90)
// — they live in sibling agent.json / netpolicy.json.
func TestMeta_SaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()

	original := &Environment{
		YoloaiVersion: "1.0.0",
		Name:          "fix-build",
		Principal:     "acme",
		CreatedAt:     time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
		Dirs: []DirEnvironment{{
			HostPath:    "/home/user/projects/my-app",
			MountPath:   "/home/user/projects/my-app",
			Mode:        "copy",
			BaselineSHA: "a1b2c3d4e5f6",
		}},
		HasPrompt: true,
		Ports:     []string{"3000:3000", "8080:8080"},
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
		Dirs: []DirEnvironment{{
			HostPath:  "/tmp/project",
			MountPath: "/tmp/project",
			Mode:      "copy",
		}},
	}

	err := SaveEnvironment(dir, meta)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, EnvironmentFile)) //nolint:gosec // test file in temp dir
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.NotContains(t, raw, "network_mode")
	assert.NotContains(t, raw, "ports")
	// agent/model are no longer substrate fields — the slimmed record never
	// writes them (they live in the sibling agent.json, Q104).
	assert.NotContains(t, raw, "agent")
	assert.NotContains(t, raw, "model")
	// Default (no-principal) sandboxes omit the field, so existing
	// environment.json files load as the default principal unchanged.
	assert.NotContains(t, raw, "principal")
}

func TestMeta_VersionSetOnSave(t *testing.T) {
	dir := t.TempDir()

	meta := &Environment{
		Name: "test-version",
		Dirs: []DirEnvironment{{
			HostPath:  "/tmp/project",
			MountPath: "/tmp/project",
			Mode:      "copy",
		}},
	}

	require.NoError(t, SaveEnvironment(dir, meta))
	assert.Equal(t, metaVersion, meta.Version)

	loaded, err := LoadEnvironment(dir)
	require.NoError(t, err)
	assert.Equal(t, metaVersion, loaded.Version)
}

// TestLoadEnvironment_BalksBelowCurrentVersion asserts the M2/D61 rule: a record
// below the current schema is not migrated on read (which would mean a write
// side-effect and, worse, would drop the agent/model keys the slimmed struct no
// longer has before they could be relocated). LoadEnvironment balks with
// ErrNeedsMigration; the explicit `yoloai system migrate` pass does the work.
func TestLoadEnvironment_BalksBelowCurrentVersion(t *testing.T) {
	dir := t.TempDir()

	v2JSON := `{
		"version": 2,
		"name": "pre-q104",
		"backend": "docker",
		"agent": "claude",
		"model": "opus",
		"dirs": [{"host_path": "/tmp/proj", "mount_path": "/tmp/proj", "mode": "copy"}]
	}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, EnvironmentFile), []byte(v2JSON), 0600))

	_, err := LoadEnvironment(dir)
	require.ErrorIs(t, err, ErrNeedsMigration)
}

// migrateLadder unmarshals a legacy record and runs the in-struct migration
// ladder (the v0->v2 backfills). It is the path the explicit per-sandbox
// migration uses after relocating agent/model into agent.json; LoadEnvironment
// no longer runs it on read (it balks instead).
func migrateLadder(t *testing.T, legacyJSON string) *Environment {
	t.Helper()
	var meta Environment
	require.NoError(t, json.Unmarshal([]byte(legacyJSON), &meta))
	require.NoError(t, MigrateEnvironment(&meta))
	return &meta
}

func TestMeta_MigrateV0ToV1_Docker(t *testing.T) {
	// Legacy environment.json without a version field (pre-versioning sandbox).
	legacyJSON := `{
		"yoloai_version": "0.1.0",
		"name": "old-sandbox",
		"created_at": "2025-01-01T00:00:00Z",
		"backend": "docker",
		"agent": "claude",
		"workdir": {"host_path": "/tmp/proj", "mount_path": "/tmp/proj", "mode": "copy"}
	}`
	loaded := migrateLadder(t, legacyJSON)
	assert.False(t, loaded.HostFilesystem, "docker backend should have HostFilesystem=false")
}

func TestMeta_MigrateV0ToV1_EmptyBackendBackfillsDocker(t *testing.T) {
	// The very oldest sandboxes predate the `backend` field entirely, so it
	// deserialises as "". Migration backfills Docker (the only backend that
	// existed then) explicitly, so downstream readers (e.g. status grouping)
	// can treat an empty BackendType as genuinely broken rather than coercing.
	legacyJSON := `{
		"yoloai_version": "0.1.0",
		"name": "oldest-sandbox",
		"created_at": "2025-01-01T00:00:00Z",
		"agent": "claude",
		"workdir": {"host_path": "/tmp/proj", "mount_path": "/tmp/proj", "mode": "copy"}
	}`
	loaded := migrateLadder(t, legacyJSON)
	assert.Equal(t, runtime.BackendDocker, loaded.BackendType, "empty legacy backend backfills to docker")
	assert.Equal(t, "yoloai-base", loaded.ImageRef, "empty legacy image_ref backfills to yoloai-base")
	assert.False(t, loaded.HostFilesystem, "docker backend should have HostFilesystem=false")
}

func TestMeta_MigrateV0ToV1_Seatbelt(t *testing.T) {
	legacyJSON := `{
		"yoloai_version": "0.1.0",
		"name": "old-seatbelt",
		"created_at": "2025-01-01T00:00:00Z",
		"backend": "seatbelt",
		"agent": "claude",
		"workdir": {"host_path": "/tmp/proj", "mount_path": "/tmp/proj", "mode": "copy"}
	}`
	loaded := migrateLadder(t, legacyJSON)
	assert.True(t, loaded.HostFilesystem, "seatbelt backend should have HostFilesystem=true")
}

func TestMeta_MigrateV0ToV1_UnknownBackendDefaultsToFalse(t *testing.T) {
	// If a meta file names a backend that isn't registered on this platform
	// (or doesn't exist at all), migration should default HostFilesystem to
	// false rather than panicking or rejecting the load. The conservative
	// answer keeps the upgrade path forward-compatible.
	legacyJSON := `{
		"yoloai_version": "0.1.0",
		"name": "old-mystery",
		"created_at": "2025-01-01T00:00:00Z",
		"backend": "not-a-real-backend",
		"agent": "claude",
		"workdir": {"host_path": "/tmp/proj", "mount_path": "/tmp/proj", "mode": "copy"}
	}`
	loaded := migrateLadder(t, legacyJSON)
	assert.False(t, loaded.HostFilesystem, "unknown backend should default to HostFilesystem=false")
}

func TestMeta_FutureVersionReturnsError(t *testing.T) {
	dir := t.TempDir()

	futureJSON := `{"version": 9999, "name": "future",
		"dirs": [{"host_path": "/tmp", "mount_path": "/tmp", "mode": "copy"}]}`
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
		Dirs: []DirEnvironment{{
			HostPath:  "/tmp/project",
			MountPath: "/tmp/project",
			Mode:      "copy",
		}},
	}

	err := SaveEnvironment(dir, meta)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, EnvironmentFile)) //nolint:gosec
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.NotContains(t, raw, "resources")
}

func TestEnvironmentDir(t *testing.T) {
	env := &Environment{
		Dirs: []DirEnvironment{
			{HostPath: "/a", Mode: DirModeCopy},
			{HostPath: "/b", Mode: DirModeRW},
		},
	}

	// "" selects Dirs[0]
	d := env.Dir("")
	require.NotNil(t, d)
	assert.Equal(t, "/a", d.HostPath)

	// exact match returns that dir
	d = env.Dir("/b")
	require.NotNil(t, d)
	assert.Equal(t, "/b", d.HostPath)

	// write through pointer propagates to slice
	d.Mode = DirModeOverlay
	assert.Equal(t, DirModeOverlay, env.Dirs[1].Mode)

	// non-existent path returns nil
	d = env.Dir("/nope")
	assert.Nil(t, d)
}

func TestMeta_MigrateV1ToV2(t *testing.T) {
	// A v1 record with the old two-field shape (workdir object + directories
	// array): the ladder repacks it into Dirs with the workdir at element 0 and
	// the aux dir at element 1, and re-saving drops the legacy keys.
	v1JSON := `{
		"version": 1,
		"name": "old-v1",
		"created_at": "2025-01-01T00:00:00Z",
		"backend": "docker",
		"agent": "claude",
		"image_ref": "yoloai-base",
		"workdir": {"host_path": "/tmp/proj", "mount_path": "/tmp/proj", "mode": "copy", "baseline_sha": "abc123"},
		"directories": [{"host_path": "/tmp/aux", "mount_path": "/tmp/aux", "mode": "ro"}]
	}`
	loaded := migrateLadder(t, v1JSON)
	require.Len(t, loaded.Dirs, 2, "Dirs should have 2 entries (workdir + 1 aux)")
	assert.Equal(t, "/tmp/proj", loaded.Dirs[0].HostPath)
	assert.Equal(t, DirMode("copy"), loaded.Dirs[0].Mode)
	assert.Equal(t, "abc123", loaded.Dirs[0].BaselineSHA)
	assert.Equal(t, "/tmp/aux", loaded.Dirs[1].HostPath)
	assert.Equal(t, DirMode("ro"), loaded.Dirs[1].Mode)

	// Re-save and verify no legacy keys, current version stamped.
	dir := t.TempDir()
	require.NoError(t, SaveEnvironment(dir, loaded))
	data, err := os.ReadFile(filepath.Join(dir, EnvironmentFile)) //nolint:gosec // test file in temp dir
	require.NoError(t, err)
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.NotContains(t, raw, "workdir", "legacy workdir key must not be written back")
	assert.NotContains(t, raw, "directories", "legacy directories key must not be written back")
	assert.Contains(t, raw, "dirs", "new dirs key must be present")
}
