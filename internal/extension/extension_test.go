package extension

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeExtension is a test helper that writes a YAML file to the given directory.
func writeExtension(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0600))
}

// --- Load ---

func TestLoad_AllFields(t *testing.T) {
	dir := t.TempDir()
	writeExtension(t, dir, "full.yaml", `
description: "Full example"
agent: claude
args:
  - name: issue
    description: "Issue number"
  - name: workdir
    description: "Working directory"
flags:
  - name: model
    short: m
    description: "Model to use"
    default: "sonnet"
  - name: dry-run
    description: "Dry run mode"
action: |
  echo "${issue} ${workdir} ${model}"
`)

	ext, err := Load(filepath.Join(dir, "full.yaml"))
	require.NoError(t, err)

	assert.Equal(t, "full", ext.Name)
	assert.Equal(t, "Full example", ext.Description)
	require.NotNil(t, ext.Agent)
	assert.Equal(t, []string{"claude"}, ext.Agent.Names)
	require.Len(t, ext.Args, 2)
	assert.Equal(t, "issue", ext.Args[0].Name)
	assert.Equal(t, "workdir", ext.Args[1].Name)
	require.Len(t, ext.Flags, 2)
	assert.Equal(t, "model", ext.Flags[0].Name)
	assert.Equal(t, "m", ext.Flags[0].Short)
	assert.Equal(t, "sonnet", ext.Flags[0].Default)
	assert.Equal(t, "dry-run", ext.Flags[1].Name)
	assert.Contains(t, ext.Action, "echo")
}

func TestLoad_AgentAsList(t *testing.T) {
	dir := t.TempDir()
	writeExtension(t, dir, "multi.yaml", `
description: "Multi-agent"
agent:
  - claude
  - codex
action: echo hi
`)

	ext, err := Load(filepath.Join(dir, "multi.yaml"))
	require.NoError(t, err)
	require.NotNil(t, ext.Agent)
	assert.Equal(t, []string{"claude", "codex"}, ext.Agent.Names)
}

func TestLoad_AgentAbsent(t *testing.T) {
	dir := t.TempDir()
	writeExtension(t, dir, "noagent.yaml", `
description: "No agent constraint"
action: echo hi
`)

	ext, err := Load(filepath.Join(dir, "noagent.yaml"))
	require.NoError(t, err)
	assert.Nil(t, ext.Agent)
}

func TestLoad_YmlExtension(t *testing.T) {
	dir := t.TempDir()
	writeExtension(t, dir, "short.yml", `
action: echo hi
`)

	ext, err := Load(filepath.Join(dir, "short.yml"))
	require.NoError(t, err)
	assert.Equal(t, "short", ext.Name)
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	writeExtension(t, dir, "bad.yaml", `
action: [invalid yaml
`)

	_, err := Load(filepath.Join(dir, "bad.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse extension")
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read extension")
}

// --- LoadAll ---

func TestLoadAll_MultipleSorted(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	extDir := filepath.Join(dir, ".yoloai", "extensions")
	require.NoError(t, os.MkdirAll(extDir, 0750))

	writeExtension(t, extDir, "beta.yaml", "action: echo beta")
	writeExtension(t, extDir, "alpha.yaml", "action: echo alpha")
	writeExtension(t, extDir, "gamma.yml", "action: echo gamma")

	exts, err := LoadAll()
	require.NoError(t, err)
	require.Len(t, exts, 3)
	assert.Equal(t, "alpha", exts[0].Name)
	assert.Equal(t, "beta", exts[1].Name)
	assert.Equal(t, "gamma", exts[2].Name)
}

func TestLoadAll_SkipsNonYAML(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	extDir := filepath.Join(dir, ".yoloai", "extensions")
	require.NoError(t, os.MkdirAll(extDir, 0750))

	writeExtension(t, extDir, "good.yaml", "action: echo good")
	writeExtension(t, extDir, "readme.md", "# not an extension")
	writeExtension(t, extDir, "notes.txt", "some notes")

	exts, err := LoadAll()
	require.NoError(t, err)
	require.Len(t, exts, 1)
	assert.Equal(t, "good", exts[0].Name)
}

func TestLoadAll_SkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	extDir := filepath.Join(dir, ".yoloai", "extensions")
	require.NoError(t, os.MkdirAll(filepath.Join(extDir, "subdir.yaml"), 0750))
	writeExtension(t, extDir, "real.yaml", "action: echo real")

	exts, err := LoadAll()
	require.NoError(t, err)
	require.Len(t, exts, 1)
	assert.Equal(t, "real", exts[0].Name)
}

func TestLoadAll_MissingDirReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	exts, err := LoadAll()
	require.NoError(t, err)
	assert.Nil(t, exts)
}

func TestLoadAll_ParseErrorReturned(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	extDir := filepath.Join(dir, ".yoloai", "extensions")
	require.NoError(t, os.MkdirAll(extDir, 0750))

	writeExtension(t, extDir, "bad.yaml", "action: [broken yaml")

	_, err := LoadAll()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse extension")
}

// --- Validate ---

func TestValidate_Valid(t *testing.T) {
	ext := &Extension{
		Name:   "test-ext",
		Action: "echo hi",
		Args: []Arg{
			{Name: "issue", Description: "Issue number"},
		},
		Flags: []Flag{
			{Name: "model", Short: "m", Description: "Model", Default: "sonnet"},
		},
	}
	assert.NoError(t, Validate(ext))
}

func TestValidate_MissingAction(t *testing.T) {
	ext := &Extension{Name: "noaction"}
	err := Validate(ext)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "action is required")
}

func TestValidate_NameCollision(t *testing.T) {
	ext := &Extension{Name: "new", Action: "echo hi"}
	err := Validate(ext)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conflicts with built-in command")
}

func TestValidate_EmptyArgName(t *testing.T) {
	ext := &Extension{
		Name:   "bad",
		Action: "echo hi",
		Args:   []Arg{{Name: ""}},
	}
	err := Validate(ext)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "arg name is required")
}

func TestValidate_InvalidArgName(t *testing.T) {
	ext := &Extension{
		Name:   "bad",
		Action: "echo hi",
		Args:   []Arg{{Name: "123invalid"}},
	}
	err := Validate(ext)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid arg name")
}

func TestValidate_DuplicateArgName(t *testing.T) {
	ext := &Extension{
		Name:   "bad",
		Action: "echo hi",
		Args:   []Arg{{Name: "foo"}, {Name: "foo"}},
	}
	err := Validate(ext)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate arg name")
}

func TestValidate_InvalidFlagName(t *testing.T) {
	ext := &Extension{
		Name:   "bad",
		Action: "echo hi",
		Flags:  []Flag{{Name: "123bad"}},
	}
	err := Validate(ext)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid flag name")
}

func TestValidate_DuplicateFlagName(t *testing.T) {
	ext := &Extension{
		Name:   "bad",
		Action: "echo hi",
		Flags:  []Flag{{Name: "model"}, {Name: "model"}},
	}
	err := Validate(ext)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate flag name")
}

func TestValidate_ReservedFlagName(t *testing.T) {
	ext := &Extension{
		Name:   "bad",
		Action: "echo hi",
		Flags:  []Flag{{Name: "verbose"}},
	}
	err := Validate(ext)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conflicts with reserved flag")
}

func TestValidate_FlagShortTooLong(t *testing.T) {
	ext := &Extension{
		Name:   "bad",
		Action: "echo hi",
		Flags:  []Flag{{Name: "model", Short: "mm"}},
	}
	err := Validate(ext)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "single character")
}

func TestValidate_DuplicateFlagShort(t *testing.T) {
	ext := &Extension{
		Name:   "bad",
		Action: "echo hi",
		Flags:  []Flag{{Name: "model", Short: "m"}, {Name: "mode", Short: "m"}},
	}
	err := Validate(ext)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate flag short")
}

func TestValidate_ReservedFlagShort(t *testing.T) {
	ext := &Extension{
		Name:   "bad",
		Action: "echo hi",
		Flags:  []Flag{{Name: "model", Short: "v"}},
	}
	err := Validate(ext)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conflicts with reserved flag")
}

func TestValidate_UnknownAgent(t *testing.T) {
	ext := &Extension{
		Name:   "bad",
		Action: "echo hi",
		Agent:  &AgentConstraint{Names: []string{"nonexistent"}},
	}
	err := Validate(ext)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown agent")
}

func TestValidate_ValidAgent(t *testing.T) {
	ext := &Extension{
		Name:   "good",
		Action: "echo hi",
		Agent:  &AgentConstraint{Names: []string{"claude"}},
	}
	assert.NoError(t, Validate(ext))
}

// --- SupportsAgent ---

func TestSupportsAgent_NoConstraint(t *testing.T) {
	ext := &Extension{Name: "any"}
	assert.True(t, ext.SupportsAgent("claude"))
	assert.True(t, ext.SupportsAgent("codex"))
}

func TestSupportsAgent_SingleMatch(t *testing.T) {
	ext := &Extension{
		Name:  "claude-only",
		Agent: &AgentConstraint{Names: []string{"claude"}},
	}
	assert.True(t, ext.SupportsAgent("claude"))
	assert.False(t, ext.SupportsAgent("codex"))
}

func TestSupportsAgent_MultipleMatch(t *testing.T) {
	ext := &Extension{
		Name:  "multi",
		Agent: &AgentConstraint{Names: []string{"claude", "codex"}},
	}
	assert.True(t, ext.SupportsAgent("claude"))
	assert.True(t, ext.SupportsAgent("codex"))
	assert.False(t, ext.SupportsAgent("gemini"))
}

// --- ExitError ---

func TestExitError_Message(t *testing.T) {
	err := &ExitError{Code: 42}
	assert.Equal(t, "extension exited with code 42", err.Error())
}
