package cli

// ABOUTME: Tests for the extensions CLI commands (yoloai x).

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/extension"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupExtTest creates a fake ~/.yoloai/extensions/ directory.
// Returns the extensions directory path.
func setupExtTest(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	extDir := filepath.Join(tmpDir, ".yoloai", "extensions")
	require.NoError(t, os.MkdirAll(extDir, 0750))

	return extDir
}

// writeExt writes a YAML extension file to the extensions directory.
func writeExt(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0600))
}

// --- List ---

func TestXList_NoExtensions(t *testing.T) {
	_ = setupExtTest(t)

	cmd := newXCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "No extensions found")
}

func TestXList_NoExtensionsDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	// Don't create extensions dir

	cmd := newXCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "No extensions found")
}

func TestXList_WithExtensions(t *testing.T) {
	extDir := setupExtTest(t)
	writeExt(t, extDir, "hello.yaml", `
description: "Say hello"
action: echo hello
`)
	writeExt(t, extDir, "bye.yaml", `
description: "Say goodbye"
agent: claude
action: echo bye
`)

	cmd := newXCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	out := buf.String()
	assert.Contains(t, out, "NAME")
	assert.Contains(t, out, "bye")
	assert.Contains(t, out, "hello")
	assert.Contains(t, out, "Say hello")
	assert.Contains(t, out, "claude")
	assert.Contains(t, out, "any")
}

func TestXList_JSON(t *testing.T) {
	extDir := setupExtTest(t)
	writeExt(t, extDir, "hello.yaml", `
description: "Say hello"
action: echo hello
`)

	// Need a root command with --json persistent flag
	root := newRootCmd("test", "test", "test")
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"--json", "x"})
	require.NoError(t, root.Execute())

	var result []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	require.Len(t, result, 1)
	assert.Equal(t, "hello", result[0]["name"])
	assert.Equal(t, "Say hello", result[0]["description"])
}

func TestXList_JSONEmpty(t *testing.T) {
	_ = setupExtTest(t)

	root := newRootCmd("test", "test", "test")
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"--json", "x"})
	require.NoError(t, root.Execute())

	var result []any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Empty(t, result)
}

// --- Run ---

func TestXRun_SimpleAction(t *testing.T) {
	extDir := setupExtTest(t)
	writeExt(t, extDir, "greet.yaml", `
description: "Greet someone"
args:
  - name: name
    description: "Who to greet"
action: echo "Hello ${name}"
`)

	cmd := newXCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"greet", "World"})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, "Hello World\n", buf.String())
}

func TestXRun_MissingArgs(t *testing.T) {
	extDir := setupExtTest(t)
	writeExt(t, extDir, "greet.yaml", `
description: "Greet"
args:
  - name: name
    description: "Who"
action: echo "${name}"
`)

	cmd := newXCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"greet"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected 1 argument")
}

func TestXRun_TooManyArgs(t *testing.T) {
	extDir := setupExtTest(t)
	writeExt(t, extDir, "greet.yaml", `
description: "Greet"
args:
  - name: name
    description: "Who"
action: echo "${name}"
`)

	cmd := newXCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"greet", "one", "two"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected 1 argument(s) but got 2")
}

func TestXRun_FlagDefaults(t *testing.T) {
	extDir := setupExtTest(t)
	writeExt(t, extDir, "flagtest.yaml", `
description: "Flag test"
flags:
  - name: model
    short: m
    description: "Model"
    default: "sonnet"
action: echo "${model}"
`)

	cmd := newXCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"flagtest"})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, "sonnet\n", buf.String())
}

func TestXRun_FlagOverride(t *testing.T) {
	extDir := setupExtTest(t)
	writeExt(t, extDir, "flagtest.yaml", `
description: "Flag test"
flags:
  - name: model
    short: m
    description: "Model"
    default: "sonnet"
action: echo "${model}"
`)

	cmd := newXCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"flagtest", "-m", "opus"})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, "opus\n", buf.String())
}

func TestXRun_HyphenatedFlagToUnderscore(t *testing.T) {
	extDir := setupExtTest(t)
	writeExt(t, extDir, "hflag.yaml", `
description: "Hyphen flag test"
flags:
  - name: max-turns
    description: "Max turns"
    default: "10"
action: echo "${max_turns}"
`)

	cmd := newXCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"hflag", "--max-turns", "5"})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, "5\n", buf.String())
}

func TestXRun_AgentEnvVar(t *testing.T) {
	extDir := setupExtTest(t)
	writeExt(t, extDir, "agentvar.yaml", `
description: "Agent var test"
action: echo "${agent}"
`)

	cmd := newXCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"agentvar"})
	require.NoError(t, cmd.Execute())

	// Default agent is "claude"
	assert.Contains(t, buf.String(), "claude")
}

func TestXRun_AgentConstraintFails(t *testing.T) {
	extDir := setupExtTest(t)
	writeExt(t, extDir, "codexonly.yaml", `
description: "Codex only"
agent: codex
action: echo hi
`)

	cmd := newXCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"codexonly"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support agent")
}

func TestXRun_ExitCodePropagation(t *testing.T) {
	extDir := setupExtTest(t)
	writeExt(t, extDir, "fail.yaml", `
description: "Exit with code"
action: exit 42
`)

	cmd := newXCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"fail"})
	err := cmd.Execute()
	require.Error(t, err)

	var exitErr *extension.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 42, exitErr.Code)
}

func TestXRun_MultipleArgs(t *testing.T) {
	extDir := setupExtTest(t)
	writeExt(t, extDir, "multi.yaml", `
description: "Multi arg test"
args:
  - name: first
    description: "First arg"
  - name: second
    description: "Second arg"
action: echo "${first} ${second}"
`)

	cmd := newXCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"multi", "hello", "world"})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, "hello world\n", buf.String())
}

func TestXRun_NoArgs(t *testing.T) {
	extDir := setupExtTest(t)
	writeExt(t, extDir, "noargs.yaml", `
description: "No args"
action: echo "done"
`)

	cmd := newXCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"noargs"})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, "done\n", buf.String())
}

func TestXRun_ArgsAndFlags(t *testing.T) {
	extDir := setupExtTest(t)
	writeExt(t, extDir, "combo.yaml", `
description: "Args and flags"
args:
  - name: target
    description: "Target"
flags:
  - name: count
    short: c
    description: "Count"
    default: "1"
action: echo "${target} ${count}"
`)

	cmd := newXCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"combo", "foo", "-c", "3"})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, "foo 3\n", buf.String())
}

// --- Help ---

func TestXRun_Help(t *testing.T) {
	extDir := setupExtTest(t)
	writeExt(t, extDir, "documented.yaml", `
description: "A well documented extension"
args:
  - name: issue
    description: "Issue number"
flags:
  - name: model
    short: m
    description: "Model to use"
    default: "sonnet"
action: echo hi
`)

	cmd := newXCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"documented", "--help"})
	require.NoError(t, cmd.Execute())

	out := buf.String()
	assert.Contains(t, out, "documented")
	assert.Contains(t, out, "well documented")
	assert.Contains(t, out, "model")
}

// --- Invalid extensions skipped ---

func TestXRun_InvalidExtensionSkipped(t *testing.T) {
	extDir := setupExtTest(t)
	// This extension has no action — invalid
	writeExt(t, extDir, "bad.yaml", `
description: "No action"
`)
	// This one is valid
	writeExt(t, extDir, "good.yaml", `
description: "Good"
action: echo good
`)

	cmd := newXCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	// List should show the good one (invalid ones are skipped during subcommand registration
	// but LoadAll still returns them for listing)
	out := buf.String()
	assert.Contains(t, out, "good")
}

// --- Agent constraint with multiple agents ---

func TestXRun_MultiAgentConstraintPasses(t *testing.T) {
	extDir := setupExtTest(t)
	writeExt(t, extDir, "multi-agent.yaml", `
description: "Supports claude and codex"
agent:
  - claude
  - codex
action: echo "agent is ${agent}"
`)

	cmd := newXCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"multi-agent"})
	require.NoError(t, cmd.Execute())

	// Default agent is claude, which is in the list
	assert.Contains(t, buf.String(), "agent is claude")
}

// --- Edge cases ---

func TestXList_SkipsInvalidForSubcommands(t *testing.T) {
	extDir := setupExtTest(t)
	// Extension named "new" conflicts with built-in
	writeExt(t, extDir, "new.yaml", `
description: "Conflict"
action: echo hi
`)

	cmd := newXCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	// The conflicting extension is skipped during subcommand registration,
	// but we can still list. Check that listing doesn't crash.
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	// Should still list it (LoadAll returns all, Validate is separate)
	out := buf.String()
	assert.Contains(t, out, "new")
}

func TestXRun_FlagLongForm(t *testing.T) {
	extDir := setupExtTest(t)
	writeExt(t, extDir, "longflag.yaml", `
description: "Long flag form"
flags:
  - name: output
    short: o
    description: "Output format"
    default: "text"
action: echo "${output}"
`)

	cmd := newXCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"longflag", "--output", "json"})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, "json\n", buf.String())
}

func TestXRun_StderrPassthrough(t *testing.T) {
	extDir := setupExtTest(t)
	writeExt(t, extDir, "stderr.yaml", `
description: "Stderr test"
action: echo "err" >&2
`)

	cmd := newXCmd()
	stdoutBuf := new(bytes.Buffer)
	stderrBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(stderrBuf)
	cmd.SetArgs([]string{"stderr"})
	require.NoError(t, cmd.Execute())

	assert.Empty(t, stdoutBuf.String())
	assert.Equal(t, "err\n", stderrBuf.String())
}

func TestXList_JSONWithAgents(t *testing.T) {
	extDir := setupExtTest(t)
	writeExt(t, extDir, "restricted.yaml", `
description: "Agent-restricted"
agent:
  - claude
  - codex
action: echo hi
`)

	root := newRootCmd("test", "test", "test")
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"--json", "x"})
	require.NoError(t, root.Execute())

	var result []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	require.Len(t, result, 1)
	agents := result[0]["agents"].([]any)
	assert.Contains(t, agents, "claude")
	assert.Contains(t, agents, "codex")
}

func TestXRun_EnvVarsAllSet(t *testing.T) {
	extDir := setupExtTest(t)
	// Use env command to dump all extension-relevant vars
	writeExt(t, extDir, "envcheck.yaml", `
description: "Check env vars"
args:
  - name: myarg
    description: "An arg"
flags:
  - name: my-flag
    description: "A flag"
    default: "default_val"
action: |
  echo "arg=${myarg}"
  echo "flag=${my_flag}"
  echo "agent=${agent}"
`)

	cmd := newXCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"envcheck", "test_arg_value"})
	require.NoError(t, cmd.Execute())

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, "arg=test_arg_value", lines[0])
	assert.Equal(t, "flag=default_val", lines[1])
	assert.Contains(t, lines[2], "agent=")
}
