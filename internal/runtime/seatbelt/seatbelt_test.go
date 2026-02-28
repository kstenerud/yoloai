package seatbelt

// ABOUTME: Unit tests for seatbelt backend — profile generation, platform, tmux socket.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/runtime"
)

func TestGenerateProfile_DenyDefault(t *testing.T) {
	cfg := runtime.InstanceConfig{Name: "test"}
	profile := GenerateProfile(cfg, "/tmp/sandbox", "/Users/testuser")

	if !strings.Contains(profile, "(deny default)") {
		t.Error("profile should start with (deny default)")
	}
	if !strings.Contains(profile, "(version 1)") {
		t.Error("profile should have (version 1)")
	}
}

func TestGenerateProfile_ReadOnlyMount(t *testing.T) {
	cfg := runtime.InstanceConfig{
		Name: "test",
		Mounts: []runtime.MountSpec{
			{Source: "/path/to/src", Target: "/path/to/src", ReadOnly: true},
		},
	}
	profile := GenerateProfile(cfg, "/tmp/sandbox", "/Users/testuser")

	if !strings.Contains(profile, `(allow file-read* (subpath "/path/to/src"))`) {
		t.Error("read-only mount should produce file-read* rule")
	}
	if strings.Contains(profile, `file-write* (subpath "/path/to/src")`) {
		t.Error("read-only mount should NOT produce file-write* rule")
	}
}

func TestGenerateProfile_ReadWriteMount(t *testing.T) {
	cfg := runtime.InstanceConfig{
		Name: "test",
		Mounts: []runtime.MountSpec{
			{Source: "/path/to/work", Target: "/path/to/work", ReadOnly: false},
		},
	}
	profile := GenerateProfile(cfg, "/tmp/sandbox", "/Users/testuser")

	if !strings.Contains(profile, `(allow file-read* file-write* (subpath "/path/to/work"))`) {
		t.Error("read-write mount should produce file-read* file-write* rule")
	}
}

func TestGenerateProfile_MultipleMounts(t *testing.T) {
	cfg := runtime.InstanceConfig{
		Name: "test",
		Mounts: []runtime.MountSpec{
			{Source: "/src/project", Target: "/src/project", ReadOnly: false},
			{Source: "/etc/config", Target: "/etc/config", ReadOnly: true},
			{Source: "/data/db", Target: "/data/db", ReadOnly: false},
		},
	}
	profile := GenerateProfile(cfg, "/tmp/sandbox", "/Users/testuser")

	if !strings.Contains(profile, `"/src/project"`) {
		t.Error("should contain project mount")
	}
	if !strings.Contains(profile, `"/etc/config"`) {
		t.Error("should contain config mount")
	}
	if !strings.Contains(profile, `"/data/db"`) {
		t.Error("should contain db mount")
	}
}

func TestGenerateProfile_NetworkDefault(t *testing.T) {
	cfg := runtime.InstanceConfig{Name: "test", NetworkMode: ""}
	profile := GenerateProfile(cfg, "/tmp/sandbox", "/Users/testuser")

	if !strings.Contains(profile, "(allow network*)") {
		t.Error("default network mode should allow all network")
	}
}

func TestGenerateProfile_NetworkNone(t *testing.T) {
	cfg := runtime.InstanceConfig{Name: "test", NetworkMode: "none"}
	profile := GenerateProfile(cfg, "/tmp/sandbox", "/Users/testuser")

	if strings.Contains(profile, "(allow network*)") {
		t.Error("network none should NOT allow network")
	}
}

func TestGenerateProfile_SandboxDirAlwaysWritable(t *testing.T) {
	cfg := runtime.InstanceConfig{Name: "test"}
	sandboxDir := "/Users/test/.yoloai/sandboxes/mybox"
	profile := GenerateProfile(cfg, sandboxDir, "/Users/test")

	if !strings.Contains(profile, fmt.Sprintf(`(allow file-read* file-write* (subpath %q))`, sandboxDir)) {
		t.Errorf("sandbox dir should be writable, profile:\n%s", profile)
	}
}

func TestGenerateProfile_HomeDirReadable(t *testing.T) {
	cfg := runtime.InstanceConfig{Name: "test"}
	homeDir := "/Users/testuser"
	profile := GenerateProfile(cfg, "/tmp/sandbox", homeDir)

	if !strings.Contains(profile, fmt.Sprintf(`(allow file-read* (subpath %q))`, homeDir)) {
		t.Error("home dir should be readable")
	}
}

func TestGenerateProfile_SystemPaths(t *testing.T) {
	cfg := runtime.InstanceConfig{Name: "test"}
	profile := GenerateProfile(cfg, "/tmp/sandbox", "/Users/testuser")

	requiredPaths := []string{"/usr/lib", "/usr/bin", "/System", "/opt/homebrew"}
	for _, p := range requiredPaths {
		if !strings.Contains(profile, fmt.Sprintf(`(subpath %q)`, p)) {
			t.Errorf("profile should allow read access to %s", p)
		}
	}
}

func TestGenerateProfile_EmptyMountSourceSkipped(t *testing.T) {
	cfg := runtime.InstanceConfig{
		Name: "test",
		Mounts: []runtime.MountSpec{
			{Source: "", Target: "/some/path", ReadOnly: false},
		},
	}
	profile := GenerateProfile(cfg, "/tmp/sandbox", "/Users/testuser")

	// Should not have a rule for empty source
	if strings.Contains(profile, `(subpath "")`) {
		t.Error("empty source mount should be skipped")
	}
}

func TestIsMacOS(t *testing.T) {
	// Save and restore
	original := goos
	defer func() { goos = original }()

	goos = func() string { return "darwin" }
	if !isMacOS() {
		t.Error("should be macOS when goos=darwin")
	}

	goos = func() string { return "linux" }
	if isMacOS() {
		t.Error("should not be macOS when goos=linux")
	}
}

func TestSandboxName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"yoloai-mybox", "mybox"},
		{"yoloai-test-sandbox", "test-sandbox"},
		{"noprefix", "noprefix"},
	}

	for _, tt := range tests {
		got := sandboxName(tt.input)
		if got != tt.expected {
			t.Errorf("sandboxName(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestBuildTmuxCommand(t *testing.T) {
	r := &Runtime{sandboxExecBin: "/usr/bin/sandbox-exec"}
	sandboxPath := "/Users/test/.yoloai/sandboxes/mybox"

	cmd := r.buildTmuxCommand(sandboxPath, []string{"tmux", "attach", "-t", "main"})

	args := cmd.Args
	if args[0] != "tmux" {
		t.Errorf("first arg should be tmux, got %q", args[0])
	}
	if args[1] != "-S" {
		t.Errorf("second arg should be -S, got %q", args[1])
	}
	expectedSocket := sandboxPath + "/tmux.sock"
	if args[2] != expectedSocket {
		t.Errorf("third arg should be socket path %q, got %q", expectedSocket, args[2])
	}
	if args[3] != "attach" || args[4] != "-t" || args[5] != "main" {
		t.Errorf("remaining args should be [attach -t main], got %v", args[3:])
	}
}

func TestBuildExecCommand_TmuxDetection(t *testing.T) {
	r := &Runtime{sandboxExecBin: "/usr/bin/sandbox-exec"}
	sandboxPath := "/Users/test/.yoloai/sandboxes/mybox"

	// tmux command should use buildTmuxCommand
	tmuxCmd := r.buildExecCommand(sandboxPath, []string{"tmux", "list-sessions"})
	if tmuxCmd.Args[0] != "tmux" {
		t.Error("tmux command should be dispatched to tmux binary")
	}
	if tmuxCmd.Args[1] != "-S" {
		t.Error("tmux command should have socket injection")
	}

	// Non-tmux command should use sandbox-exec
	otherCmd := r.buildExecCommand(sandboxPath, []string{"bash", "-c", "echo hello"})
	if otherCmd.Args[0] != "/usr/bin/sandbox-exec" {
		t.Errorf("non-tmux command should use sandbox-exec, got %q", otherCmd.Args[0])
	}
	if otherCmd.Args[1] != "-f" {
		t.Error("sandbox-exec should have -f flag")
	}
}

func TestMountSymlinks(t *testing.T) {
	// Create a source directory
	srcDir := t.TempDir()

	// Create a target path in a temp area
	targetBase := t.TempDir()
	targetPath := filepath.Join(targetBase, "nested", "target")

	mounts := []runtime.MountSpec{
		{Source: srcDir, Target: targetPath, ReadOnly: true},
	}

	created, err := mountSymlinks(mounts)
	if err != nil {
		t.Fatalf("mountSymlinks failed: %v", err)
	}
	defer func() {
		for _, p := range created {
			_ = os.Remove(p)
		}
	}()

	if len(created) != 1 {
		t.Fatalf("expected 1 symlink, got %d", len(created))
	}
	if created[0] != targetPath {
		t.Errorf("expected symlink at %q, got %q", targetPath, created[0])
	}

	// Verify symlink points to source
	resolved, err := os.Readlink(targetPath)
	if err != nil {
		t.Fatalf("readlink failed: %v", err)
	}
	if resolved != srcDir {
		t.Errorf("symlink points to %q, want %q", resolved, srcDir)
	}
}

func TestMountSymlinks_SkipSecrets(t *testing.T) {
	srcDir := t.TempDir()

	mounts := []runtime.MountSpec{
		{Source: srcDir, Target: "/run/secrets/API_KEY", ReadOnly: true},
	}

	created, err := mountSymlinks(mounts)
	if err != nil {
		t.Fatalf("mountSymlinks failed: %v", err)
	}
	if len(created) != 0 {
		t.Errorf("expected no symlinks for secrets, got %d", len(created))
	}
}

func TestMountSymlinks_SkipSamePath(t *testing.T) {
	mounts := []runtime.MountSpec{
		{Source: "/same/path", Target: "/same/path", ReadOnly: true},
	}

	created, err := mountSymlinks(mounts)
	if err != nil {
		t.Fatalf("mountSymlinks failed: %v", err)
	}
	if len(created) != 0 {
		t.Errorf("expected no symlinks for same path, got %d", len(created))
	}
}

func TestMountSymlinks_SkipFiles(t *testing.T) {
	// Create a source file (not a directory)
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "file.txt")
	if err := os.WriteFile(srcFile, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}

	targetBase := t.TempDir()
	targetPath := filepath.Join(targetBase, "target-file")

	mounts := []runtime.MountSpec{
		{Source: srcFile, Target: targetPath, ReadOnly: true},
	}

	created, err := mountSymlinks(mounts)
	if err != nil {
		t.Fatalf("mountSymlinks failed: %v", err)
	}
	if len(created) != 0 {
		t.Errorf("expected no symlinks for file source, got %d", len(created))
	}
}

func TestMountSymlinks_SkipEmptySource(t *testing.T) {
	mounts := []runtime.MountSpec{
		{Source: "", Target: "/some/target", ReadOnly: true},
	}

	created, err := mountSymlinks(mounts)
	if err != nil {
		t.Fatalf("mountSymlinks failed: %v", err)
	}
	if len(created) != 0 {
		t.Errorf("expected no symlinks for empty source, got %d", len(created))
	}
}

func TestMountSymlinks_SkipExistingTarget(t *testing.T) {
	srcDir := t.TempDir()
	targetDir := t.TempDir() // target already exists as a real directory

	mounts := []runtime.MountSpec{
		{Source: srcDir, Target: targetDir},
	}

	created, err := mountSymlinks(mounts)
	if err != nil {
		t.Fatalf("mountSymlinks failed: %v", err)
	}
	if len(created) != 0 {
		t.Errorf("expected no symlinks for existing target, got %d", len(created))
	}
}
