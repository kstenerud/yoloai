package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/agent"
	"github.com/kstenerud/yoloai/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopyAgentFiles_NilConfig(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	require.NoError(t, copyAgentFiles(agentDef, t.TempDir(), nil))
}

func TestCopyAgentFiles_StringForm_Normal(t *testing.T) {
	agentDef := agent.GetAgent("claude")

	// Create source directory structure mimicking ~/.claude/
	baseDir := t.TempDir()
	claudeDir := filepath.Join(baseDir, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(`{"key":"value"}`), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(claudeDir, "subdir"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, "subdir", "file.txt"), []byte("hello"), 0600))

	// Create sandbox directory
	sandboxDir := t.TempDir()
	agentStateDir := filepath.Join(sandboxDir, AgentRuntimeDir)
	require.NoError(t, os.MkdirAll(agentStateDir, 0750))

	af := &config.AgentFilesConfig{BaseDir: baseDir}
	require.NoError(t, copyAgentFiles(agentDef, sandboxDir, af))

	// Verify files were copied
	settingsPath := filepath.Join(agentStateDir, "settings.json")
	data, err := os.ReadFile(settingsPath) //nolint:gosec // test code
	require.NoError(t, err)
	assert.Equal(t, `{"key":"value"}`, string(data))

	subfilePath := filepath.Join(agentStateDir, "subdir", "file.txt")
	data, err = os.ReadFile(subfilePath) //nolint:gosec // test code
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))
}

func TestCopyAgentFiles_StringForm_MissingSrc(t *testing.T) {
	agentDef := agent.GetAgent("claude")

	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, AgentRuntimeDir), 0750))

	// Base dir exists but has no .claude/ subdir
	af := &config.AgentFilesConfig{BaseDir: t.TempDir()}
	require.NoError(t, copyAgentFiles(agentDef, sandboxDir, af))
}

func TestCopyAgentFiles_StringForm_Exclusions(t *testing.T) {
	agentDef := agent.GetAgent("claude")

	// Create source with files that should be excluded
	baseDir := t.TempDir()
	claudeDir := filepath.Join(baseDir, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte("ok"), 0600))
	// These should be excluded
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte("secret"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, "agent.log"), []byte("log data"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(claudeDir, "projects", "myproject"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, "projects", "myproject", "data.json"), []byte("proj"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(claudeDir, "statsig"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, "statsig", "cache.json"), []byte("stats"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(claudeDir, "todos"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, "todos", "list.json"), []byte("todos"), 0600))

	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, AgentRuntimeDir), 0750))

	af := &config.AgentFilesConfig{BaseDir: baseDir}
	require.NoError(t, copyAgentFiles(agentDef, sandboxDir, af))

	agentStateDir := filepath.Join(sandboxDir, AgentRuntimeDir)

	// settings.json should be copied
	assert.FileExists(t, filepath.Join(agentStateDir, "settings.json"))

	// Excluded files/dirs should not exist
	assert.NoFileExists(t, filepath.Join(agentStateDir, ".credentials.json"))
	assert.NoFileExists(t, filepath.Join(agentStateDir, "agent.log"))
	assert.NoDirExists(t, filepath.Join(agentStateDir, "projects"))
	assert.NoDirExists(t, filepath.Join(agentStateDir, "statsig"))
	assert.NoDirExists(t, filepath.Join(agentStateDir, "todos"))
}

func TestCopyAgentFiles_StringForm_NoOverwrite(t *testing.T) {
	agentDef := agent.GetAgent("claude")

	baseDir := t.TempDir()
	claudeDir := filepath.Join(baseDir, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte("from-agent-files"), 0600))

	sandboxDir := t.TempDir()
	agentStateDir := filepath.Join(sandboxDir, AgentRuntimeDir)
	require.NoError(t, os.MkdirAll(agentStateDir, 0750))
	// Pre-existing file (from SeedFiles) should not be overwritten
	require.NoError(t, os.WriteFile(filepath.Join(agentStateDir, "settings.json"), []byte("from-seed"), 0600))

	af := &config.AgentFilesConfig{BaseDir: baseDir}
	require.NoError(t, copyAgentFiles(agentDef, sandboxDir, af))

	p := filepath.Join(agentStateDir, "settings.json")
	data, err := os.ReadFile(p) //nolint:gosec // test code
	require.NoError(t, err)
	assert.Equal(t, "from-seed", string(data), "SeedFiles should not be overwritten")
}

func TestCopyAgentFiles_StringForm_NoStateDirAgent(t *testing.T) {
	agentDef := agent.GetAgent("aider") // no StateDir

	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, AgentRuntimeDir), 0750))

	af := &config.AgentFilesConfig{BaseDir: t.TempDir()}
	require.NoError(t, copyAgentFiles(agentDef, sandboxDir, af))
}

func TestCopyAgentFiles_ListForm_Files(t *testing.T) {
	// Create source files
	srcDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "custom.json"), []byte("custom"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "other.txt"), []byte("other"), 0600))

	sandboxDir := t.TempDir()
	agentStateDir := filepath.Join(sandboxDir, AgentRuntimeDir)
	require.NoError(t, os.MkdirAll(agentStateDir, 0750))

	agentDef := agent.GetAgent("claude")
	af := &config.AgentFilesConfig{
		Files: []string{
			filepath.Join(srcDir, "custom.json"),
			filepath.Join(srcDir, "other.txt"),
		},
	}
	require.NoError(t, copyAgentFiles(agentDef, sandboxDir, af))

	p1 := filepath.Join(agentStateDir, "custom.json")
	data, err := os.ReadFile(p1) //nolint:gosec // test code
	require.NoError(t, err)
	assert.Equal(t, "custom", string(data))

	p2 := filepath.Join(agentStateDir, "other.txt")
	data, err = os.ReadFile(p2) //nolint:gosec // test code
	require.NoError(t, err)
	assert.Equal(t, "other", string(data))
}

func TestCopyAgentFiles_ListForm_Directory(t *testing.T) {
	srcDir := t.TempDir()
	subDir := filepath.Join(srcDir, "myconfig")
	require.NoError(t, os.MkdirAll(subDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "file.txt"), []byte("in-dir"), 0600))

	sandboxDir := t.TempDir()
	agentStateDir := filepath.Join(sandboxDir, AgentRuntimeDir)
	require.NoError(t, os.MkdirAll(agentStateDir, 0750))

	agentDef := agent.GetAgent("claude")
	af := &config.AgentFilesConfig{
		Files: []string{subDir},
	}
	require.NoError(t, copyAgentFiles(agentDef, sandboxDir, af))

	p := filepath.Join(agentStateDir, "myconfig", "file.txt")
	data, err := os.ReadFile(p) //nolint:gosec // test code
	require.NoError(t, err)
	assert.Equal(t, "in-dir", string(data))
}

func TestCopyAgentFiles_ListForm_MissingEntry(t *testing.T) {
	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, AgentRuntimeDir), 0750))

	agentDef := agent.GetAgent("claude")
	af := &config.AgentFilesConfig{
		Files: []string{"/nonexistent/path/file.json"},
	}
	require.NoError(t, copyAgentFiles(agentDef, sandboxDir, af))
}

func TestCopyAgentFiles_ListForm_NoOverwrite(t *testing.T) {
	srcDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "settings.json"), []byte("from-list"), 0600))

	sandboxDir := t.TempDir()
	agentStateDir := filepath.Join(sandboxDir, AgentRuntimeDir)
	require.NoError(t, os.MkdirAll(agentStateDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(agentStateDir, "settings.json"), []byte("from-seed"), 0600))

	agentDef := agent.GetAgent("claude")
	af := &config.AgentFilesConfig{
		Files: []string{filepath.Join(srcDir, "settings.json")},
	}
	require.NoError(t, copyAgentFiles(agentDef, sandboxDir, af))

	p := filepath.Join(agentStateDir, "settings.json")
	data, err := os.ReadFile(p) //nolint:gosec // test code
	require.NoError(t, err)
	assert.Equal(t, "from-seed", string(data))
}

func TestShouldExclude(t *testing.T) {
	patterns := []string{"projects/", "statsig/", "todos/", ".credentials.json", "*.log"}

	tests := []struct {
		rel   string
		isDir bool
		want  bool
	}{
		{"projects", true, true},
		{"projects/foo", false, true},
		{"statsig", true, true},
		{"todos", true, true},
		{".credentials.json", false, true},
		{"agent.log", false, true},
		{"debug.log", false, true},
		{"settings.json", false, false},
		{"subdir/file.txt", false, false},
		{"myprojects", true, false}, // not exact match for "projects/"
	}

	for _, tt := range tests {
		t.Run(tt.rel, func(t *testing.T) {
			got := shouldExclude(tt.rel, tt.isDir, patterns)
			assert.Equal(t, tt.want, got)
		})
	}
}
