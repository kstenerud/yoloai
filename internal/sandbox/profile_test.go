package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupProfileDir(t *testing.T, name, content string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".yoloai", "profiles", name)
	if err := os.MkdirAll(dir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profile.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestValidateProfileName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr string
	}{
		{"go-dev", ""},
		{"node.project", ""},
		{"my_profile", ""},
		{"A1", ""},
		{"base", "reserved"},
		{"", "required"},
		{strings.Repeat("a", 57), "at most 56"},
		{strings.Repeat("a", 56), ""},
		{"/bad", "looks like a path"},
		{"bad name", "must start with"},
		{"-starts-with-dash", "must start with"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProfileName(tt.name)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("ValidateProfileName(%q) = %v, want nil", tt.name, err)
				}
			} else {
				if err == nil {
					t.Errorf("ValidateProfileName(%q) = nil, want error containing %q", tt.name, tt.wantErr)
				} else if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("ValidateProfileName(%q) = %v, want error containing %q", tt.name, err, tt.wantErr)
				}
			}
		})
	}
}

func TestLoadProfile_BasicFields(t *testing.T) {
	yaml := `
agent: gemini
model: flash
backend: docker
ports:
  - "8080:8080"
  - "3000:3000"
env:
  GO111MODULE: "on"
  GOPATH: /home/yoloai/go
`
	setupProfileDir(t, "test-profile", yaml)

	cfg, err := LoadProfile("test-profile")
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Extends != "base" {
		t.Errorf("Extends = %q, want %q", cfg.Extends, "base")
	}
	if cfg.Agent != "gemini" {
		t.Errorf("Agent = %q, want %q", cfg.Agent, "gemini")
	}
	if cfg.Model != "flash" {
		t.Errorf("Model = %q, want %q", cfg.Model, "flash")
	}
	if cfg.Backend != "docker" {
		t.Errorf("Backend = %q, want %q", cfg.Backend, "docker")
	}
	if len(cfg.Ports) != 2 || cfg.Ports[0] != "8080:8080" {
		t.Errorf("Ports = %v, want [8080:8080, 3000:3000]", cfg.Ports)
	}
	if cfg.Env["GO111MODULE"] != "on" {
		t.Errorf("Env[GO111MODULE] = %q, want %q", cfg.Env["GO111MODULE"], "on")
	}
	if cfg.Env["GOPATH"] != "/home/yoloai/go" {
		t.Errorf("Env[GOPATH] = %q, want %q", cfg.Env["GOPATH"], "/home/yoloai/go")
	}
}

func TestLoadProfile_Extends(t *testing.T) {
	yaml := `extends: go-dev
agent: claude
`
	setupProfileDir(t, "go-web", yaml)

	cfg, err := LoadProfile("go-web")
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Extends != "go-dev" {
		t.Errorf("Extends = %q, want %q", cfg.Extends, "go-dev")
	}
}

func TestLoadProfile_Workdir(t *testing.T) {
	yaml := `
workdir:
  path: /home/user/my-app
  mode: copy
  mount: /opt/myapp
`
	setupProfileDir(t, "wd-profile", yaml)

	cfg, err := LoadProfile("wd-profile")
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Workdir == nil {
		t.Fatal("Workdir is nil")
	}
	if cfg.Workdir.Path != "/home/user/my-app" {
		t.Errorf("Workdir.Path = %q, want %q", cfg.Workdir.Path, "/home/user/my-app")
	}
	if cfg.Workdir.Mode != "copy" {
		t.Errorf("Workdir.Mode = %q, want %q", cfg.Workdir.Mode, "copy")
	}
	if cfg.Workdir.Mount != "/opt/myapp" {
		t.Errorf("Workdir.Mount = %q, want %q", cfg.Workdir.Mount, "/opt/myapp")
	}
}

func TestLoadProfile_Directories(t *testing.T) {
	yaml := `
directories:
  - path: /home/user/shared-lib
    mode: rw
    mount: /usr/local/lib/shared
  - path: /home/user/types
`
	setupProfileDir(t, "dirs-profile", yaml)

	cfg, err := LoadProfile("dirs-profile")
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Directories) != 2 {
		t.Fatalf("len(Directories) = %d, want 2", len(cfg.Directories))
	}
	if cfg.Directories[0].Path != "/home/user/shared-lib" {
		t.Errorf("Dir[0].Path = %q", cfg.Directories[0].Path)
	}
	if cfg.Directories[0].Mode != "rw" {
		t.Errorf("Dir[0].Mode = %q", cfg.Directories[0].Mode)
	}
	if cfg.Directories[0].Mount != "/usr/local/lib/shared" {
		t.Errorf("Dir[0].Mount = %q", cfg.Directories[0].Mount)
	}
	if cfg.Directories[1].Path != "/home/user/types" {
		t.Errorf("Dir[1].Path = %q", cfg.Directories[1].Path)
	}
}

func TestLoadProfile_TartImage(t *testing.T) {
	yaml := `
tart:
  image: my-custom-vm
`
	setupProfileDir(t, "tart-profile", yaml)

	cfg, err := LoadProfile("tart-profile")
	if err != nil {
		t.Fatal(err)
	}

	if cfg.TartImage != "my-custom-vm" {
		t.Errorf("TartImage = %q, want %q", cfg.TartImage, "my-custom-vm")
	}
}

func TestLoadProfile_EnvExpansion(t *testing.T) {
	t.Setenv("TEST_VAR", "expanded_value")
	yaml := `
env:
  MY_VAR: "${TEST_VAR}"
`
	setupProfileDir(t, "env-profile", yaml)

	cfg, err := LoadProfile("env-profile")
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Env["MY_VAR"] != "expanded_value" {
		t.Errorf("Env[MY_VAR] = %q, want %q", cfg.Env["MY_VAR"], "expanded_value")
	}
}

func TestLoadProfile_MissingFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	_, err := LoadProfile("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing profile")
	}
}

func TestLoadProfile_EmptyFile(t *testing.T) {
	setupProfileDir(t, "empty-profile", "")

	cfg, err := LoadProfile("empty-profile")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Extends != "base" {
		t.Errorf("Extends = %q, want %q", cfg.Extends, "base")
	}
}

func TestLoadProfile_UnknownFieldsIgnored(t *testing.T) {
	yaml := `
agent: claude
future_field: some_value
another_unknown:
  nested: true
`
	setupProfileDir(t, "unknown-profile", yaml)

	cfg, err := LoadProfile("unknown-profile")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent != "claude" {
		t.Errorf("Agent = %q, want %q", cfg.Agent, "claude")
	}
}

func TestResolveProfileChain_SingleProfile(t *testing.T) {
	setupProfileDir(t, "go-dev", "agent: claude\n")

	chain, err := ResolveProfileChain("go-dev")
	if err != nil {
		t.Fatal(err)
	}

	if len(chain) != 2 || chain[0] != "base" || chain[1] != "go-dev" {
		t.Errorf("chain = %v, want [base, go-dev]", chain)
	}
}

func TestResolveProfileChain_TwoLevelChain(t *testing.T) {
	home := setupProfileDir(t, "go-dev", "agent: claude\n")

	// Create go-web extending go-dev
	goWebDir := filepath.Join(home, ".yoloai", "profiles", "go-web")
	if err := os.MkdirAll(goWebDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goWebDir, "profile.yaml"), []byte("extends: go-dev\n"), 0600); err != nil {
		t.Fatal(err)
	}

	chain, err := ResolveProfileChain("go-web")
	if err != nil {
		t.Fatal(err)
	}

	if len(chain) != 3 || chain[0] != "base" || chain[1] != "go-dev" || chain[2] != "go-web" {
		t.Errorf("chain = %v, want [base, go-dev, go-web]", chain)
	}
}

func TestResolveProfileChain_ThreeLevelChain(t *testing.T) {
	home := setupProfileDir(t, "level1", "agent: claude\n")

	for _, pair := range []struct{ name, extends string }{
		{"level2", "level1"},
		{"level3", "level2"},
	} {
		dir := filepath.Join(home, ".yoloai", "profiles", pair.name)
		if err := os.MkdirAll(dir, 0750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "profile.yaml"), []byte("extends: "+pair.extends+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	chain, err := ResolveProfileChain("level3")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"base", "level1", "level2", "level3"}
	if len(chain) != len(want) {
		t.Fatalf("chain = %v, want %v", chain, want)
	}
	for i, name := range want {
		if chain[i] != name {
			t.Errorf("chain[%d] = %q, want %q", i, chain[i], name)
		}
	}
}

func TestResolveProfileChain_CycleDetection(t *testing.T) {
	home := setupProfileDir(t, "a", "extends: b\n")
	bDir := filepath.Join(home, ".yoloai", "profiles", "b")
	if err := os.MkdirAll(bDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bDir, "profile.yaml"), []byte("extends: a\n"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := ResolveProfileChain("a")
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error = %v, want to contain 'cycle'", err)
	}
}

func TestResolveProfileChain_MissingProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create profiles dir
	if err := os.MkdirAll(filepath.Join(home, ".yoloai", "profiles"), 0750); err != nil {
		t.Fatal(err)
	}

	_, err := ResolveProfileChain("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing profile")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %v, want to contain 'does not exist'", err)
	}
}

func TestResolveProfileChain_MissingIntermediate(t *testing.T) {
	// Create profile that extends a non-existent parent
	setupProfileDir(t, "child", "extends: nonexistent\n")

	_, err := ResolveProfileChain("child")
	if err == nil {
		t.Fatal("expected error for missing intermediate profile")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %v, want to contain 'does not exist'", err)
	}
}

func TestListProfiles(t *testing.T) {
	home := setupProfileDir(t, "go-dev", "agent: claude\n")

	// Add another profile
	dir := filepath.Join(home, ".yoloai", "profiles", "node-dev")
	if err := os.MkdirAll(dir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profile.yaml"), []byte("agent: gemini\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// Add base dir (should be excluded)
	baseDir := filepath.Join(home, ".yoloai", "profiles", "base")
	if err := os.MkdirAll(baseDir, 0750); err != nil {
		t.Fatal(err)
	}

	names, err := ListProfiles()
	if err != nil {
		t.Fatal(err)
	}

	if len(names) != 2 || names[0] != "go-dev" || names[1] != "node-dev" {
		t.Errorf("names = %v, want [go-dev, node-dev]", names)
	}
}

func TestListProfiles_Empty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create profiles dir with only base
	if err := os.MkdirAll(filepath.Join(home, ".yoloai", "profiles", "base"), 0750); err != nil {
		t.Fatal(err)
	}

	names, err := ListProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 0 {
		t.Errorf("names = %v, want empty", names)
	}
}

func TestProfileExists(t *testing.T) {
	setupProfileDir(t, "exists-profile", "agent: claude\n")

	if !ProfileExists("exists-profile") {
		t.Error("ProfileExists returned false for existing profile")
	}
	if ProfileExists("nonexistent") {
		t.Error("ProfileExists returned true for nonexistent profile")
	}
}

func TestProfileHasDockerfile(t *testing.T) {
	home := setupProfileDir(t, "with-docker", "agent: claude\n")
	dir := filepath.Join(home, ".yoloai", "profiles", "with-docker")
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM yoloai-base\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if !ProfileHasDockerfile("with-docker") {
		t.Error("ProfileHasDockerfile returned false for profile with Dockerfile")
	}

	setupProfileDir(t, "without-docker", "agent: claude\n")
	if ProfileHasDockerfile("without-docker") {
		t.Error("ProfileHasDockerfile returned true for profile without Dockerfile")
	}
}

func TestMergeProfileChain_SingleProfile(t *testing.T) {
	setupProfileDir(t, "simple", "agent: gemini\nmodel: flash\n")

	base := &YoloaiConfig{
		Agent:    "claude",
		TmuxConf: "default",
	}

	chain := []string{"base", "simple"}
	merged, err := MergeProfileChain(base, chain)
	if err != nil {
		t.Fatal(err)
	}

	if merged.Agent != "gemini" {
		t.Errorf("Agent = %q, want %q", merged.Agent, "gemini")
	}
	if merged.Model != "flash" {
		t.Errorf("Model = %q, want %q", merged.Model, "flash")
	}
	if merged.TmuxConf != "default" {
		t.Errorf("TmuxConf = %q, want %q", merged.TmuxConf, "default")
	}
}

func TestMergeProfileChain_ScalarOverrideCascading(t *testing.T) {
	home := setupProfileDir(t, "parent", "agent: gemini\nmodel: flash\n")

	childDir := filepath.Join(home, ".yoloai", "profiles", "child")
	if err := os.MkdirAll(childDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "profile.yaml"), []byte("extends: parent\nmodel: pro\n"), 0600); err != nil {
		t.Fatal(err)
	}

	base := &YoloaiConfig{Agent: "claude", Model: "sonnet"}

	chain := []string{"base", "parent", "child"}
	merged, err := MergeProfileChain(base, chain)
	if err != nil {
		t.Fatal(err)
	}

	// agent from parent (child doesn't override)
	if merged.Agent != "gemini" {
		t.Errorf("Agent = %q, want %q", merged.Agent, "gemini")
	}
	// model overridden by child
	if merged.Model != "pro" {
		t.Errorf("Model = %q, want %q", merged.Model, "pro")
	}
}

func TestMergeProfileChain_EnvMerge(t *testing.T) {
	home := setupProfileDir(t, "env-parent", "env:\n  GO: \"1\"\n  SHARED: parent\n")

	childDir := filepath.Join(home, ".yoloai", "profiles", "env-child")
	if err := os.MkdirAll(childDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "profile.yaml"),
		[]byte("extends: env-parent\nenv:\n  NODE: \"1\"\n  SHARED: child\n"), 0600); err != nil {
		t.Fatal(err)
	}

	base := &YoloaiConfig{Env: map[string]string{"BASE_VAR": "base"}}

	chain := []string{"base", "env-parent", "env-child"}
	merged, err := MergeProfileChain(base, chain)
	if err != nil {
		t.Fatal(err)
	}

	if merged.Env["BASE_VAR"] != "base" {
		t.Errorf("Env[BASE_VAR] = %q, want %q", merged.Env["BASE_VAR"], "base")
	}
	if merged.Env["GO"] != "1" {
		t.Errorf("Env[GO] = %q, want %q", merged.Env["GO"], "1")
	}
	if merged.Env["NODE"] != "1" {
		t.Errorf("Env[NODE] = %q, want %q", merged.Env["NODE"], "1")
	}
	if merged.Env["SHARED"] != "child" {
		t.Errorf("Env[SHARED] = %q, want %q (child should win)", merged.Env["SHARED"], "child")
	}
}

func TestMergeProfileChain_PortsAdditive(t *testing.T) {
	home := setupProfileDir(t, "ports-parent", "ports:\n  - \"8080:8080\"\n")

	childDir := filepath.Join(home, ".yoloai", "profiles", "ports-child")
	if err := os.MkdirAll(childDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "profile.yaml"),
		[]byte("extends: ports-parent\nports:\n  - \"3000:3000\"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	base := &YoloaiConfig{}

	chain := []string{"base", "ports-parent", "ports-child"}
	merged, err := MergeProfileChain(base, chain)
	if err != nil {
		t.Fatal(err)
	}

	if len(merged.Ports) != 2 {
		t.Fatalf("len(Ports) = %d, want 2", len(merged.Ports))
	}
	if merged.Ports[0] != "8080:8080" || merged.Ports[1] != "3000:3000" {
		t.Errorf("Ports = %v, want [8080:8080, 3000:3000]", merged.Ports)
	}
}

func TestMergeProfileChain_WorkdirChildWins(t *testing.T) {
	home := setupProfileDir(t, "wd-parent", "workdir:\n  path: /parent/dir\n  mode: copy\n")

	childDir := filepath.Join(home, ".yoloai", "profiles", "wd-child")
	if err := os.MkdirAll(childDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "profile.yaml"),
		[]byte("extends: wd-parent\nworkdir:\n  path: /child/dir\n  mode: rw\n"), 0600); err != nil {
		t.Fatal(err)
	}

	base := &YoloaiConfig{}

	chain := []string{"base", "wd-parent", "wd-child"}
	merged, err := MergeProfileChain(base, chain)
	if err != nil {
		t.Fatal(err)
	}

	if merged.Workdir == nil {
		t.Fatal("Workdir is nil")
	}
	if merged.Workdir.Path != "/child/dir" {
		t.Errorf("Workdir.Path = %q, want %q", merged.Workdir.Path, "/child/dir")
	}
	if merged.Workdir.Mode != "rw" {
		t.Errorf("Workdir.Mode = %q, want %q", merged.Workdir.Mode, "rw")
	}
}

func TestMergeProfileChain_DirectoriesAdditive(t *testing.T) {
	home := setupProfileDir(t, "dirs-parent", "directories:\n  - path: /parent/lib\n    mode: rw\n")

	childDir := filepath.Join(home, ".yoloai", "profiles", "dirs-child")
	if err := os.MkdirAll(childDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "profile.yaml"),
		[]byte("extends: dirs-parent\ndirectories:\n  - path: /child/lib\n    mode: copy\n"), 0600); err != nil {
		t.Fatal(err)
	}

	base := &YoloaiConfig{}

	chain := []string{"base", "dirs-parent", "dirs-child"}
	merged, err := MergeProfileChain(base, chain)
	if err != nil {
		t.Fatal(err)
	}

	if len(merged.Directories) != 2 {
		t.Fatalf("len(Directories) = %d, want 2", len(merged.Directories))
	}
	if merged.Directories[0].Path != "/parent/lib" {
		t.Errorf("Dir[0].Path = %q, want %q", merged.Directories[0].Path, "/parent/lib")
	}
	if merged.Directories[1].Path != "/child/lib" {
		t.Errorf("Dir[1].Path = %q, want %q", merged.Directories[1].Path, "/child/lib")
	}
}

func TestMergeProfileChain_NilEnv(t *testing.T) {
	setupProfileDir(t, "no-env", "agent: claude\n")

	base := &YoloaiConfig{}

	chain := []string{"base", "no-env"}
	merged, err := MergeProfileChain(base, chain)
	if err != nil {
		t.Fatal(err)
	}

	if merged.Env != nil {
		t.Errorf("Env = %v, want nil", merged.Env)
	}
}

func TestMergeProfileChain_BackendConstraint(t *testing.T) {
	setupProfileDir(t, "constrained", "backend: docker\nagent: claude\n")

	base := &YoloaiConfig{}

	chain := []string{"base", "constrained"}
	merged, err := MergeProfileChain(base, chain)
	if err != nil {
		t.Fatal(err)
	}

	if merged.Backend != "docker" {
		t.Errorf("Backend = %q, want %q", merged.Backend, "docker")
	}
}

func TestValidateProfileBackend(t *testing.T) {
	// No constraint
	if err := ValidateProfileBackend("", "docker"); err != nil {
		t.Errorf("empty constraint returned error: %v", err)
	}

	// Matching
	if err := ValidateProfileBackend("docker", "docker"); err != nil {
		t.Errorf("matching constraint returned error: %v", err)
	}

	// Mismatch
	if err := ValidateProfileBackend("docker", "tart"); err == nil {
		t.Error("mismatching constraint returned nil")
	}
}

func TestResolveProfileImage(t *testing.T) {
	home := setupProfileDir(t, "with-df", "agent: claude\n")
	dir := filepath.Join(home, ".yoloai", "profiles", "with-df")
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM yoloai-base\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// Profile with Dockerfile → yoloai-<name>
	img := ResolveProfileImage("with-df", []string{"base", "with-df"})
	if img != "yoloai-with-df" {
		t.Errorf("image = %q, want %q", img, "yoloai-with-df")
	}

	// Profile without Dockerfile inherits parent
	nodfDir := filepath.Join(home, ".yoloai", "profiles", "no-df")
	if err := os.MkdirAll(nodfDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodfDir, "profile.yaml"), []byte("extends: with-df\n"), 0600); err != nil {
		t.Fatal(err)
	}

	img = ResolveProfileImage("no-df", []string{"base", "with-df", "no-df"})
	if img != "yoloai-with-df" {
		t.Errorf("image = %q, want %q (should inherit parent's)", img, "yoloai-with-df")
	}

	// No Dockerfiles at all → base
	setupProfileDir(t, "plain", "agent: claude\n")
	img = ResolveProfileImage("plain", []string{"base", "plain"})
	if img != "yoloai-base" {
		t.Errorf("image = %q, want %q", img, "yoloai-base")
	}
}
