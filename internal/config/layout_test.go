// ABOUTME: Tests for Layout's path methods. Each verifies the path is
// ABOUTME: composed under the given DataDir with the expected leaf.

package config

import (
	"path/filepath"
	"testing"
)

func TestLayout_PathsRootUnderDataDir(t *testing.T) {
	const dataDir = "/tmp/yoloai-test-layout"
	l := NewLayout(dataDir)

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"YoloaiDir", l.YoloaiDir(), dataDir},
		{"SandboxesDir", l.SandboxesDir(), filepath.Join(dataDir, "sandboxes")},
		{"ProfilesDir", l.ProfilesDir(), filepath.Join(dataDir, "profiles")},
		{"CacheDir", l.CacheDir(), filepath.Join(dataDir, "cache")},
		{"ExtensionsDir", l.ExtensionsDir(), filepath.Join(dataDir, "extensions")},
		{"DefaultsDir", l.DefaultsDir(), filepath.Join(dataDir, "defaults")},
		{"DefaultsConfigPath", l.DefaultsConfigPath(), filepath.Join(dataDir, "defaults", "config.yaml")},
		{"TartBaseMetadataDir", l.TartBaseMetadataDir(), filepath.Join(dataDir, "tart-base-metadata")},
		{"TartBaseLocksDir", l.TartBaseLocksDir(), filepath.Join(dataDir, "tart-base-locks")},
		{"DockerBaseLocksDir", l.DockerBaseLocksDir(), filepath.Join(dataDir, "docker-base-locks")},
		{"VscodeCLIDir", l.VscodeCLIDir(), filepath.Join(dataDir, "vscode-cli")},
		{"SandboxDir(myname)", l.SandboxDir("myname"), filepath.Join(dataDir, "sandboxes", "myname")},
		{"SandboxLockPath(myname)", l.SandboxLockPath("myname"), filepath.Join(dataDir, "sandboxes", "myname.lock")},
		{"TartBaseLockPath(macos14)", l.TartBaseLockPath("macos14"), filepath.Join(dataDir, "tart-base-locks", "macos14.lock")},
		{"DockerBaseLockPath(yoloai-base)", l.DockerBaseLockPath("yoloai-base"), filepath.Join(dataDir, "docker-base-locks", "yoloai-base.lock")},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
}

// TestLayout_EmptyDataDirReturnsRelativePaths documents that an empty
// DataDir is not rejected — the resulting paths are simply unrooted
// (relative to ""). Embedders are expected to validate DataDir before
// constructing the Layout; the api_surface.go contract is "DataDir
// REQUIRED; empty rejected at yoloai.NewWithOptions construction."
func TestLayout_EmptyDataDirReturnsRelativePaths(t *testing.T) {
	l := NewLayout("")
	if l.YoloaiDir() != "" {
		t.Errorf("YoloaiDir() with empty DataDir: got %q, want %q", l.YoloaiDir(), "")
	}
	if got, want := l.SandboxesDir(), "sandboxes"; got != want {
		t.Errorf("SandboxesDir() with empty DataDir: got %q, want %q", got, want)
	}
}
