package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupLogTest creates a sandbox dir and returns the name and sandbox dir path.
func setupLogTest(t *testing.T, name string) string {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "logs"), 0750))

	meta := &sandbox.Meta{
		Name:      name,
		Agent:     "claude",
		CreatedAt: time.Now(),
		Workdir:   sandbox.WorkdirMeta{HostPath: "/tmp/test", Mode: "copy"},
	}
	require.NoError(t, sandbox.SaveMeta(sandboxDir, meta))
	return sandboxDir
}

func TestRunLog_NoLogFiles(t *testing.T) {
	setupLogTest(t, "logtest-empty")

	cmd := newLogAliasCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"logtest-empty"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "No log entries found.")
}

func TestRunLog_AgentFlag(t *testing.T) {
	sandboxDir := setupLogTest(t, "logtest-agent")
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "logs", "agent.log"), []byte("hello world\n"), 0600))

	cmd := newLogAliasCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"logtest-agent", "--agent"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "hello world")
}

func TestRunLog_AgentMissing(t *testing.T) {
	setupLogTest(t, "logtest-agent-missing")

	cmd := newLogAliasCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"logtest-agent-missing", "--agent"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "No agent output yet")
}

func TestRunLog_AgentRawPreservesANSI(t *testing.T) {
	sandboxDir := setupLogTest(t, "logtest-agent-raw")
	ansiContent := "\x1b[31mred text\x1b[0m\n"
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "logs", "agent.log"), []byte(ansiContent), 0600))

	cmd := newLogAliasCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"logtest-agent-raw", "--agent-raw"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "\x1b[31m")
}

func TestRunLog_AgentStripsANSI(t *testing.T) {
	sandboxDir := setupLogTest(t, "logtest-agent-strip")
	ansiContent := "\x1b[31mred text\x1b[0m\n"
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "logs", "agent.log"), []byte(ansiContent), 0600))

	cmd := newLogAliasCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"logtest-agent-strip", "--agent"})
	require.NoError(t, cmd.Execute())

	assert.NotContains(t, buf.String(), "\x1b[31m")
	assert.Contains(t, buf.String(), "red text")
}

func TestRunLog_StructuredJSONL(t *testing.T) {
	sandboxDir := setupLogTest(t, "logtest-jsonl")
	entry := `{"ts":"2026-03-15T14:23:01.000Z","level":"info","event":"sandbox.attach","msg":"attaching to session"}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "logs", "cli.jsonl"), []byte(entry), 0600))

	cmd := newLogAliasCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"logtest-jsonl"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "attaching to session")
	assert.Contains(t, buf.String(), "INFO")
	assert.Contains(t, buf.String(), "sandbox.attach")
}

func TestRunLog_RawEmitsJSONL(t *testing.T) {
	sandboxDir := setupLogTest(t, "logtest-raw-jsonl")
	line := `{"ts":"2026-03-15T14:23:01.000Z","level":"info","event":"test.event","msg":"hello"}`
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "logs", "cli.jsonl"), []byte(line+"\n"), 0600))

	cmd := newLogAliasCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"logtest-raw-jsonl", "--raw"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), `"event":"test.event"`)
}

func TestRunLog_LevelFilter(t *testing.T) {
	sandboxDir := setupLogTest(t, "logtest-level")
	entries := `{"ts":"2026-03-15T14:23:01.000Z","level":"debug","event":"a","msg":"debug msg"}` + "\n" +
		`{"ts":"2026-03-15T14:23:02.000Z","level":"info","event":"b","msg":"info msg"}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "logs", "cli.jsonl"), []byte(entries), 0600))

	cmd := newLogAliasCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"logtest-level"}) // default level is info
	require.NoError(t, cmd.Execute())

	assert.NotContains(t, buf.String(), "debug msg")
	assert.Contains(t, buf.String(), "info msg")
}

func TestRunLog_DebugLevel(t *testing.T) {
	sandboxDir := setupLogTest(t, "logtest-debug")
	entries := `{"ts":"2026-03-15T14:23:01.000Z","level":"debug","event":"a","msg":"debug msg"}` + "\n" +
		`{"ts":"2026-03-15T14:23:02.000Z","level":"info","event":"b","msg":"info msg"}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "logs", "cli.jsonl"), []byte(entries), 0600))

	cmd := newLogAliasCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"logtest-debug", "--level", "debug"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "debug msg")
	assert.Contains(t, buf.String(), "info msg")
}

func TestRunLog_SourceFilter(t *testing.T) {
	sandboxDir := setupLogTest(t, "logtest-source")
	cliEntry := `{"ts":"2026-03-15T14:23:01.000Z","level":"info","event":"cli.event","msg":"cli message"}` + "\n"
	sandboxEntry := `{"ts":"2026-03-15T14:23:02.000Z","level":"info","event":"sb.event","msg":"sandbox message"}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "logs", "cli.jsonl"), []byte(cliEntry), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "logs", "sandbox.jsonl"), []byte(sandboxEntry), 0600))

	cmd := newLogAliasCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"logtest-source", "--source", "cli"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "cli message")
	assert.NotContains(t, buf.String(), "sandbox message")
}

func TestRunLog_SinceFilter(t *testing.T) {
	sandboxDir := setupLogTest(t, "logtest-since")
	// Two entries: one old (2020), one recent (2026).
	entries := `{"ts":"2020-01-01T00:00:00.000Z","level":"info","event":"old","msg":"old message"}` + "\n" +
		`{"ts":"2026-03-15T14:23:02.000Z","level":"info","event":"new","msg":"new message"}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "logs", "cli.jsonl"), []byte(entries), 0600))

	cmd := newLogAliasCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	// --since 1y filters out the 2020 entry; use a very long duration to capture 2026.
	cmd.SetArgs([]string{"logtest-since", "--since", "8760h"}) // 1 year back — includes 2026-03-15, excludes 2020
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "new message")
	assert.NotContains(t, buf.String(), "old message")
}

func TestRunLog_MultipleSourcesFilter(t *testing.T) {
	sandboxDir := setupLogTest(t, "logtest-multi-source")
	cliEntry := `{"ts":"2026-03-15T14:23:01.000Z","level":"info","event":"cli.event","msg":"cli msg"}` + "\n"
	sbEntry := `{"ts":"2026-03-15T14:23:02.000Z","level":"info","event":"sb.event","msg":"sandbox msg"}` + "\n"
	hooksEntry := `{"ts":"2026-03-15T14:23:03.000Z","level":"info","event":"hooks.event","msg":"hooks msg"}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "logs", "cli.jsonl"), []byte(cliEntry), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "logs", "sandbox.jsonl"), []byte(sbEntry), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "logs", "agent-hooks.jsonl"), []byte(hooksEntry), 0600))

	cmd := newLogAliasCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"logtest-multi-source", "--source", "cli,hooks"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "cli msg")
	assert.Contains(t, buf.String(), "hooks msg")
	assert.NotContains(t, buf.String(), "sandbox msg")
}

func TestRunLog_ExtraFieldsDisplayed(t *testing.T) {
	sandboxDir := setupLogTest(t, "logtest-extra")
	entry := `{"ts":"2026-03-15T14:23:01.000Z","level":"info","event":"sandbox.create","msg":"creating","sandbox":"my-box","agent":"claude"}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "logs", "cli.jsonl"), []byte(entry), 0600))

	cmd := newLogAliasCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"logtest-extra"})
	require.NoError(t, cmd.Execute())

	out := buf.String()
	assert.Contains(t, out, "sandbox=my-box")
	assert.Contains(t, out, "agent=claude")
}

func TestRunLog_MalformedLinesSkipped(t *testing.T) {
	sandboxDir := setupLogTest(t, "logtest-malformed")
	content := "not json\n" +
		`{"ts":"2026-03-15T14:23:01.000Z","level":"info","event":"e","msg":"valid msg"}` + "\n" +
		"{incomplete\n"
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "logs", "cli.jsonl"), []byte(content), 0600))

	cmd := newLogAliasCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"logtest-malformed"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "valid msg")
	assert.NotContains(t, buf.String(), "not json")
}

func TestRunLog_MergeSort(t *testing.T) {
	sandboxDir := setupLogTest(t, "logtest-merge")
	// cli entry is later but in cli.jsonl; sandbox entry is earlier but in sandbox.jsonl
	cliEntry := `{"ts":"2026-03-15T14:23:03.000Z","level":"info","event":"a","msg":"third"}` + "\n"
	sandboxEntry := `{"ts":"2026-03-15T14:23:01.000Z","level":"info","event":"b","msg":"first"}` + "\n" +
		`{"ts":"2026-03-15T14:23:02.000Z","level":"info","event":"c","msg":"second"}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "logs", "cli.jsonl"), []byte(cliEntry), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "logs", "sandbox.jsonl"), []byte(sandboxEntry), 0600))

	cmd := newLogAliasCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"logtest-merge"})
	require.NoError(t, cmd.Execute())

	out := buf.String()
	firstIdx := strings.Index(out, "first")
	secondIdx := strings.Index(out, "second")
	thirdIdx := strings.Index(out, "third")
	assert.Less(t, firstIdx, secondIdx)
	assert.Less(t, secondIdx, thirdIdx)
}
