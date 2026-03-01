package sandbox

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/runtime"
)

func TestResolveModel_Alias(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	result := resolveModel(agentDef, "sonnet")
	assert.Equal(t, "claude-sonnet-4-latest", result)
}

func TestResolveModel_FullName(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	result := resolveModel(agentDef, "claude-sonnet-4-5-20250929")
	assert.Equal(t, "claude-sonnet-4-5-20250929", result)
}

func TestResolveModel_Empty(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	result := resolveModel(agentDef, "")
	assert.Equal(t, "", result)
}

func TestBuildAgentCommand_InteractiveWithModel(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	result := buildAgentCommand(agentDef, "claude-opus-4-latest", "", nil)
	assert.Equal(t, "claude --dangerously-skip-permissions --model claude-opus-4-latest", result)
}

func TestBuildAgentCommand_InteractiveWithPassthrough(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	result := buildAgentCommand(agentDef, "claude-sonnet-4-latest", "", []string{"--max-turns", "5"})
	assert.Equal(t, "claude --dangerously-skip-permissions --model claude-sonnet-4-latest --max-turns 5", result)
}

func TestBuildAgentCommand_HeadlessWithPrompt(t *testing.T) {
	agentDef := agent.GetAgent("test")
	result := buildAgentCommand(agentDef, "", "echo hello", nil)
	assert.Equal(t, `sh -c "echo hello"`, result)
}

func TestBuildAgentCommand_InteractiveFallback(t *testing.T) {
	agentDef := agent.GetAgent("test")
	result := buildAgentCommand(agentDef, "", "", nil)
	assert.Equal(t, "bash", result)
}

func TestBuildContainerConfig_ValidJSON(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	data, err := buildContainerConfig(agentDef, "claude --dangerously-skip-permissions", "default+host", "/Users/test/project", false, false, nil, nil)
	require.NoError(t, err)

	var cfg containerConfig
	err = json.Unmarshal(data, &cfg)
	require.NoError(t, err)

	assert.Equal(t, "claude --dangerously-skip-permissions", cfg.AgentCommand)
	assert.Equal(t, 3000, cfg.StartupDelay)
	assert.Equal(t, "Enter Enter", cfg.SubmitSequence)
	assert.Equal(t, os.Getuid(), cfg.HostUID)
	assert.Equal(t, os.Getgid(), cfg.HostGID)
	assert.Equal(t, "default+host", cfg.TmuxConf)
	assert.Equal(t, ".claude", cfg.StateDirName)
	assert.False(t, cfg.NetworkIsolated)
	assert.Empty(t, cfg.AllowedDomains)
}

func TestBuildContainerConfig_StateDirName(t *testing.T) {
	tests := []struct {
		agent    string
		expected string
	}{
		{"claude", ".claude"},
		{"gemini", ".gemini"},
		{"test", ""},
	}
	for _, tt := range tests {
		t.Run(tt.agent, func(t *testing.T) {
			agentDef := agent.GetAgent(tt.agent)
			data, err := buildContainerConfig(agentDef, "cmd", "default", "/tmp", false, false, nil, nil)
			require.NoError(t, err)
			var cfg containerConfig
			require.NoError(t, json.Unmarshal(data, &cfg))
			assert.Equal(t, tt.expected, cfg.StateDirName)
		})
	}
}

func TestReadPrompt_DirectText(t *testing.T) {
	result, err := readPrompt("hello", "")
	require.NoError(t, err)
	assert.Equal(t, "hello", result)
}

func TestReadPrompt_File(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "prompt.txt")
	require.NoError(t, os.WriteFile(path, []byte("prompt from file\n"), 0600))

	result, err := readPrompt("", path)
	require.NoError(t, err)
	assert.Equal(t, "prompt from file", result)
}

func TestReadPrompt_MutualExclusion(t *testing.T) {
	_, err := readPrompt("hello", "/some/file")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestReadPrompt_StdinDash(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)

	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	_, err = w.WriteString("hello from stdin\n")
	require.NoError(t, err)
	require.NoError(t, w.Close())

	result, err := readPrompt("-", "")
	require.NoError(t, err)
	assert.Equal(t, "hello from stdin", result)
}

func TestParsePortBindings_Valid(t *testing.T) {
	mappings, err := parsePortBindings([]string{"3000:3000", "8080:80"})
	require.NoError(t, err)
	require.Len(t, mappings, 2)

	assert.Equal(t, runtime.PortMapping{HostPort: "3000", InstancePort: "3000", Protocol: "tcp"}, mappings[0])
	assert.Equal(t, runtime.PortMapping{HostPort: "8080", InstancePort: "80", Protocol: "tcp"}, mappings[1])
}

func TestParsePortBindings_Invalid(t *testing.T) {
	_, err := parsePortBindings([]string{"invalid"})
	require.Error(t, err)
}

func TestParsePortBindings_Empty(t *testing.T) {
	mappings, err := parsePortBindings(nil)
	require.NoError(t, err)
	assert.Nil(t, mappings)
}

func TestGitBaseline_FreshInit(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "file.txt", "hello")

	sha, err := gitBaseline(dir)
	require.NoError(t, err)
	assert.Len(t, sha, 40)

	// Verify git repo was created with the file tracked
	_, err = os.Stat(filepath.Join(dir, ".git"))
	assert.NoError(t, err)
}

func TestGitBaseline_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	sha, err := gitBaseline(dir)
	require.NoError(t, err)
	assert.Len(t, sha, 40, "allow-empty should produce a valid commit")
}

// removeGitDirs tests

func TestRemoveGitDirs_TopLevel(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	require.NoError(t, os.MkdirAll(filepath.Join(gitDir, "objects"), 0750))
	writeTestFile(t, dir, "file.txt", "hello")

	require.NoError(t, removeGitDirs(dir))

	assert.NoDirExists(t, gitDir)
	assert.FileExists(t, filepath.Join(dir, "file.txt"))
}

func TestRemoveGitDirs_Submodule(t *testing.T) {
	dir := t.TempDir()
	submod := filepath.Join(dir, "submod")
	require.NoError(t, os.MkdirAll(submod, 0750))
	writeTestFile(t, submod, "code.go", "package main")
	// Submodule .git is a file pointing to parent repo
	writeTestFile(t, submod, ".git", "gitdir: ../../.git/modules/submod")

	require.NoError(t, removeGitDirs(dir))

	assert.NoFileExists(t, filepath.Join(submod, ".git"))
	assert.FileExists(t, filepath.Join(submod, "code.go"))
}

func TestRemoveGitDirs_Nested(t *testing.T) {
	dir := t.TempDir()

	// Top-level .git dir
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0750))

	// Nested submodule with .git file
	nested := filepath.Join(dir, "a", "b")
	require.NoError(t, os.MkdirAll(nested, 0750))
	writeTestFile(t, nested, ".git", "gitdir: ../../../.git/modules/a/b")
	writeTestFile(t, nested, "main.go", "package main")

	require.NoError(t, removeGitDirs(dir))

	assert.NoDirExists(t, filepath.Join(dir, ".git"))
	assert.NoFileExists(t, filepath.Join(nested, ".git"))
	assert.FileExists(t, filepath.Join(nested, "main.go"))
}

func TestRemoveGitDirs_NoGit(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "file.txt", "hello")

	require.NoError(t, removeGitDirs(dir))
	assert.FileExists(t, filepath.Join(dir, "file.txt"))
}

func TestCreate_CleansUpIncompleteOnNew(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create sandbox dir without meta.json (incomplete from prior failure)
	name := "incomplete"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	// prepareSandboxState should auto-clean the incomplete dir.
	// It will fail later (no agent, etc.) but the key assertion is
	// that it does NOT return ErrSandboxExists.
	mgr := NewManager(&mockRuntime{}, "docker", slog.Default(), strings.NewReader(""), io.Discard)
	_, err := mgr.Create(context.Background(), CreateOptions{
		Name:       name,
		WorkdirArg: tmpDir,
		Agent:      "test",
		Version:    "test",
	})
	// Will fail for other reasons (no API key etc.), but must NOT be ErrSandboxExists
	assert.NotErrorIs(t, err, ErrSandboxExists)
}

func TestCreate_CleansUpOnPrepareFail(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "cleanup-test"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)

	// Use test agent which needs no API key, but provide a nonexistent workdir
	// so preparation fails after directory creation.
	mgr := NewManager(&mockRuntime{}, "docker", slog.Default(), strings.NewReader(""), io.Discard)
	_, err := mgr.Create(context.Background(), CreateOptions{
		Name:       name,
		WorkdirArg: filepath.Join(tmpDir, "nonexistent"),
		Agent:      "test",
		Version:    "test",
	})
	require.Error(t, err)

	// Sandbox directory should not exist (cleaned up on failure)
	assert.NoDirExists(t, sandboxDir)
}

func TestBuildContainerConfig_NetworkIsolated(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	domains := []string{"api.anthropic.com", "sentry.io"}
	data, err := buildContainerConfig(agentDef, "claude", "default", "/tmp", false, true, domains, nil)
	require.NoError(t, err)

	var cfg containerConfig
	require.NoError(t, json.Unmarshal(data, &cfg))

	assert.True(t, cfg.NetworkIsolated)
	assert.Equal(t, domains, cfg.AllowedDomains)
}

func TestParseResourceLimits(t *testing.T) {
	tests := []struct {
		name    string
		input   *ResourceLimits
		wantCPU int64
		wantMem int64
		wantNil bool
		wantErr bool
	}{
		{
			name:    "both set",
			input:   &ResourceLimits{CPUs: "4", Memory: "8g"},
			wantCPU: 4_000_000_000,
			wantMem: 8 * 1024 * 1024 * 1024,
		},
		{
			name:    "cpus only",
			input:   &ResourceLimits{CPUs: "2.5"},
			wantCPU: 2_500_000_000,
		},
		{
			name:    "memory only",
			input:   &ResourceLimits{Memory: "512m"},
			wantMem: 512 * 1024 * 1024,
		},
		{
			name:    "neither set",
			input:   &ResourceLimits{},
			wantNil: true,
		},
		{
			name:    "invalid cpus",
			input:   &ResourceLimits{CPUs: "abc"},
			wantErr: true,
		},
		{
			name:    "invalid memory",
			input:   &ResourceLimits{Memory: "xyz"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseResourceLimits(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantNil {
				if result != nil {
					t.Errorf("expected nil result, got %+v", result)
				}
				return
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if result.NanoCPUs != tt.wantCPU {
				t.Errorf("NanoCPUs = %d, want %d", result.NanoCPUs, tt.wantCPU)
			}
			if result.Memory != tt.wantMem {
				t.Errorf("Memory = %d, want %d", result.Memory, tt.wantMem)
			}
		})
	}
}

func TestParseMemoryString(t *testing.T) {
	tests := []struct {
		input   string
		want    int64
		wantErr bool
	}{
		{"1g", 1024 * 1024 * 1024, false},
		{"512m", 512 * 1024 * 1024, false},
		{"1024k", 1024 * 1024, false},
		{"1048576b", 1048576, false},
		{"1048576", 1048576, false},        // no suffix = bytes
		{"0.5g", 512 * 1024 * 1024, false}, // fractional
		{"", 0, false},
		{"abc", 0, true},
		{"-1g", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseMemoryString(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("parseMemoryString(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
