package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSandboxesDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	want := filepath.Join(tmpDir, ".yoloai", "sandboxes")
	assert.Equal(t, want, SandboxesDir())
}

func TestProfilesDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	want := filepath.Join(tmpDir, ".yoloai", "profiles")
	assert.Equal(t, want, ProfilesDir())
}

func TestCacheDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	want := filepath.Join(tmpDir, ".yoloai", "cache")
	assert.Equal(t, want, CacheDir())
}

func TestExtensionsDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	want := filepath.Join(tmpDir, ".yoloai", "extensions")
	assert.Equal(t, want, ExtensionsDir())
}

func TestYoloaiDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	want := filepath.Join(tmpDir, ".yoloai")
	assert.Equal(t, want, YoloaiDir())
}
