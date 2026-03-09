package seatbelt

// ABOUTME: Unit tests for seatbelt backend — profile generation, platform, tmux socket.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/runtime"
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

func TestGenerateProfile_HomeDirMinimalAccess(t *testing.T) {
	cfg := runtime.InstanceConfig{Name: "test"}
	homeDir := "/Users/testuser"
	profile := GenerateProfile(cfg, "/tmp/sandbox", homeDir)

	// Should allow read access to ~/.local (agent binaries)
	localPath := filepath.Join(homeDir, ".local")
	if !strings.Contains(profile, fmt.Sprintf(`(allow file-read* (subpath %q))`, localPath)) {
		t.Error("~/.local should be readable for agent binaries")
	}

	// Should NOT grant blanket read access to the entire home directory.
	// The .local rule contains homeDir as a prefix, so check for the exact
	// standalone rule that would grant full home access.
	blanketRule := fmt.Sprintf("(allow file-read* (subpath %q))\n", homeDir)
	if strings.Contains(profile, blanketRule) {
		t.Error("home dir should NOT have blanket read access")
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

func TestMountSymlinks_SkipUnreachableParent(t *testing.T) {
	srcDir := t.TempDir()

	// Use a path under a read-only system directory that can't be created.
	// On macOS /home is managed by auto_master; on all platforms /dev is
	// not a normal writable directory.
	mounts := []runtime.MountSpec{
		{Source: srcDir, Target: "/dev/null/impossible/.state"},
	}

	created, err := mountSymlinks(mounts)
	if err != nil {
		t.Fatalf("mountSymlinks should skip unreachable paths, got error: %v", err)
	}
	if len(created) != 0 {
		t.Errorf("expected no symlinks for unreachable parent, got %d", len(created))
	}
}

func TestPatchConfigWorkingDir_CopyMode(t *testing.T) {
	sandboxPath := t.TempDir()
	workDir := filepath.Join(sandboxPath, "work", "encoded-dir")
	if err := os.MkdirAll(workDir, 0750); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(sandboxPath, "config.json")
	cfgData, err := json.Marshal(map[string]interface{}{
		"working_dir": "/original/path",
		"other_key":   "other_value",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, cfgData, 0600); err != nil {
		t.Fatal(err)
	}

	mounts := []runtime.MountSpec{
		{Source: workDir, Target: "/some/target", ReadOnly: false},
	}

	r := &Runtime{}
	if err := r.patchConfigWorkingDir(sandboxPath, mounts); err != nil {
		t.Fatalf("patchConfigWorkingDir failed: %v", err)
	}

	data, err := os.ReadFile(cfgPath) //nolint:gosec // G304: test file in temp dir
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["working_dir"] != workDir {
		t.Errorf("working_dir = %q, want %q", raw["working_dir"], workDir)
	}
	if raw["other_key"] != "other_value" {
		t.Error("other_key should be preserved")
	}
}

func TestPatchConfigWorkingDir_NotCopyMode(t *testing.T) {
	sandboxPath := t.TempDir()

	originalConfig := `{"working_dir": "/original/path"}`
	cfgPath := filepath.Join(sandboxPath, "config.json")
	if err := os.WriteFile(cfgPath, []byte(originalConfig), 0600); err != nil {
		t.Fatal(err)
	}

	// Mount source is NOT under <sandboxPath>/work/
	mounts := []runtime.MountSpec{
		{Source: "/some/other/path", Target: "/target", ReadOnly: false},
	}

	r := &Runtime{}
	if err := r.patchConfigWorkingDir(sandboxPath, mounts); err != nil {
		t.Fatalf("patchConfigWorkingDir failed: %v", err)
	}

	data, err := os.ReadFile(cfgPath) //nolint:gosec // G304: test file in temp dir
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != originalConfig {
		t.Errorf("config.json should be unchanged, got %s", string(data))
	}
}

func TestPatchConfigWorkingDir_NoMounts(t *testing.T) {
	sandboxPath := t.TempDir()

	originalConfig := `{"working_dir": "/original/path"}`
	cfgPath := filepath.Join(sandboxPath, "config.json")
	if err := os.WriteFile(cfgPath, []byte(originalConfig), 0600); err != nil {
		t.Fatal(err)
	}

	r := &Runtime{}
	if err := r.patchConfigWorkingDir(sandboxPath, nil); err != nil {
		t.Fatalf("patchConfigWorkingDir failed: %v", err)
	}

	data, err := os.ReadFile(cfgPath) //nolint:gosec // G304: test file in temp dir
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != originalConfig {
		t.Errorf("config.json should be unchanged, got %s", string(data))
	}
}

func TestPatchConfigWorkingDir_AlreadyCorrect(t *testing.T) {
	sandboxPath := t.TempDir()
	workDir := filepath.Join(sandboxPath, "work", "encoded-dir")
	if err := os.MkdirAll(workDir, 0750); err != nil {
		t.Fatal(err)
	}

	// working_dir already equals the copy source
	cfgData, err := json.Marshal(map[string]interface{}{
		"working_dir": workDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(sandboxPath, "config.json")
	if err := os.WriteFile(cfgPath, cfgData, 0600); err != nil {
		t.Fatal(err)
	}

	// Record the file's mod time before the call
	infoBefore, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	mounts := []runtime.MountSpec{
		{Source: workDir, Target: "/some/target", ReadOnly: false},
	}

	r := &Runtime{}
	if err := r.patchConfigWorkingDir(sandboxPath, mounts); err != nil {
		t.Fatalf("patchConfigWorkingDir failed: %v", err)
	}

	// File content should be unchanged (not rewritten)
	data, err := os.ReadFile(cfgPath) //nolint:gosec // G304: test file in temp dir
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["working_dir"] != workDir {
		t.Errorf("working_dir = %q, want %q", raw["working_dir"], workDir)
	}

	// Verify the file was not rewritten by checking mod time
	infoAfter, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if infoAfter.ModTime() != infoBefore.ModTime() {
		t.Error("config.json should not have been rewritten when working_dir already matches")
	}
}

func TestSandboxEnv_Whitelist(t *testing.T) {
	// Set some env vars that should be stripped
	t.Setenv("SSH_AUTH_SOCK", "/tmp/ssh-agent.sock")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "super-secret")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")

	env := sandboxEnv()
	envMap := make(map[string]string)
	for _, entry := range env {
		k, v, _ := strings.Cut(entry, "=")
		envMap[k] = v
	}

	// Sensitive vars must NOT be present
	for _, key := range []string{"SSH_AUTH_SOCK", "AWS_SECRET_ACCESS_KEY", "ANTHROPIC_API_KEY", "GIT_AUTHOR_EMAIL"} {
		if _, ok := envMap[key]; ok {
			t.Errorf("%s should be excluded from sandbox environment", key)
		}
	}
}

func TestSandboxEnv_PreservesWhitelisted(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/Users/testuser")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LANG", "en_US.UTF-8")
	t.Setenv("LC_CTYPE", "UTF-8")

	env := sandboxEnv()
	envMap := make(map[string]string)
	for _, entry := range env {
		k, v, _ := strings.Cut(entry, "=")
		envMap[k] = v
	}

	for _, key := range []string{"PATH", "HOME", "TERM", "LANG", "LC_CTYPE"} {
		if _, ok := envMap[key]; !ok {
			t.Errorf("%s should be preserved in sandbox environment", key)
		}
	}
}
