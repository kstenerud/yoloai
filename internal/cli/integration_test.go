//go:build integration

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/internal/testutil"
	dockerrt "github.com/kstenerud/yoloai/runtime/docker"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cliSetup creates an isolated HOME, a project dir, and ensures EnsureSetup
// has run (base image built). Returns a cleanup-enabled *testing.T context.
func cliSetup(t *testing.T) (projectDir string) {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	projectDir = filepath.Join(tmpHome, "project")
	require.NoError(t, os.MkdirAll(projectDir, 0750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectDir, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"),
		0600,
	))

	// Run EnsureSetup via a quick `new --no-start` then destroy, or just
	// invoke setup by creating a throwaway sandbox. We use the root command
	// to trigger EnsureSetup via the Manager.
	root := newRootCmd("test", "test", "test")
	root.SetArgs([]string{"new", "--agent", "test", "--no-start", "cli-setup", projectDir})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	require.NoError(t, root.ExecuteContext(context.Background()))

	// Clean up the setup sandbox
	root = newRootCmd("test", "test", "test")
	root.SetArgs([]string{"destroy", "--yes", "cli-setup"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	_ = root.ExecuteContext(context.Background())

	return projectDir
}

// runCLI executes a command through the root Cobra command and returns
// stdout, stderr, and any error.
func runCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newRootCmd("test", "test", "test")
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	root.SetOut(outBuf)
	root.SetErr(errBuf)
	root.SetArgs(args)
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

// destroySandbox is a cleanup helper that destroys a sandbox, ignoring errors.
func destroySandbox(t *testing.T, name string) {
	t.Helper()
	runCLI(t, "destroy", "--yes", name) //nolint:errcheck // best-effort cleanup
}

func TestCLI_NewAndDestroy(t *testing.T) {
	projectDir := cliSetup(t)

	_, stderr, err := runCLI(t, "new", "--agent", "test", "--no-start", "cli-new", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-new") })

	assert.DirExists(t, sandbox.Dir("cli-new"))
	assert.Contains(t, stderr, "cli-new") // Manager output goes to stderr

	stdout, _, err := runCLI(t, "destroy", "--yes", "cli-new")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Destroyed")
	assert.NoDirExists(t, sandbox.Dir("cli-new"))
}

func TestCLI_NewWithPrompt(t *testing.T) {
	projectDir := cliSetup(t)

	_, _, err := runCLI(t, "new", "--agent", "test", "--no-start", "--prompt", "echo hi", "cli-prompt", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-prompt") })

	sandboxDir := sandbox.Dir("cli-prompt")
	prompt, err := os.ReadFile(filepath.Join(sandboxDir, "prompt.txt")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Equal(t, "echo hi", string(prompt))
}

func TestCLI_Ls(t *testing.T) {
	projectDir := cliSetup(t)

	_, _, err := runCLI(t, "new", "--agent", "test", "--no-start", "cli-ls", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-ls") })

	stdout, _, err := runCLI(t, "ls")
	require.NoError(t, err)
	assert.Contains(t, stdout, "cli-ls")
}

func TestCLI_LsJSON(t *testing.T) {
	projectDir := cliSetup(t)

	_, _, err := runCLI(t, "new", "--agent", "test", "--no-start", "cli-lsjson", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-lsjson") })

	stdout, _, err := runCLI(t, "--json", "ls")
	require.NoError(t, err)

	var result struct {
		Sandboxes           []json.RawMessage `json:"sandboxes"`
		UnavailableBackends []string          `json:"unavailable_backends"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &result))
	assert.GreaterOrEqual(t, len(result.Sandboxes), 1)
}

func TestCLI_Diff(t *testing.T) {
	projectDir := cliSetup(t)

	_, _, err := runCLI(t, "new", "--agent", "test", "--no-start", "cli-diff", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-diff") })

	// Modify work copy
	meta, err := sandbox.LoadMeta(sandbox.Dir("cli-diff"))
	require.NoError(t, err)
	workDir := sandbox.WorkDir("cli-diff", meta.Workdir.HostPath)
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "main.go"),
		[]byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"diff-test\") }\n"),
		0600,
	))

	stdout, _, err := runCLI(t, "diff", "cli-diff")
	require.NoError(t, err)
	assert.Contains(t, stdout, "fmt")
}

func TestCLI_StartStop(t *testing.T) {
	projectDir := cliSetup(t)

	// Create and start in one step (avoids separate start which recreates container)
	_, _, err := runCLI(t, "new", "--agent", "test", "cli-startstop", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-startstop") })

	// Wait for container to become active
	rt, err := dockerrt.New(context.Background())
	require.NoError(t, err)
	defer rt.Close() //nolint:errcheck // test cleanup
	testutil.WaitForActive(context.Background(), t, rt, sandbox.InstanceName("cli-startstop"), 15*time.Second)

	_, _, err = runCLI(t, "stop", "cli-startstop")
	require.NoError(t, err)
}

func TestCLI_Log(t *testing.T) {
	projectDir := cliSetup(t)

	_, _, err := runCLI(t, "new", "--agent", "test", "--no-start", "cli-log", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-log") })

	// Write a fake JSONL log entry for testing
	sandboxDir := sandbox.Dir("cli-log")
	logsDir := filepath.Join(sandboxDir, sandbox.LogsDir)
	require.NoError(t, os.MkdirAll(logsDir, 0700))
	entry := `{"ts":"2026-03-16T10:00:00.000Z","level":"info","event":"test.event","msg":"test log output"}` + "\n"
	require.NoError(t, os.WriteFile(sandbox.CLIJSONLPath("cli-log"), []byte(entry), 0600))

	stdout, _, err := runCLI(t, "log", "cli-log")
	require.NoError(t, err)
	assert.Contains(t, stdout, "test log output")
}

func TestCLI_DestroyNonExistent(t *testing.T) {
	_ = cliSetup(t)

	_, _, err := runCLI(t, "destroy", "--yes", "nonexistent-sandbox-xyz")
	assert.Error(t, err)
}

func TestCLI_NewDuplicate(t *testing.T) {
	projectDir := cliSetup(t)

	_, _, err := runCLI(t, "new", "--agent", "test", "--no-start", "cli-dup", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-dup") })

	_, _, err = runCLI(t, "new", "--agent", "test", "--no-start", "cli-dup", projectDir)
	assert.Error(t, err, "creating duplicate sandbox should fail")
}

func TestCLI_NewReplace(t *testing.T) {
	projectDir := cliSetup(t)

	_, _, err := runCLI(t, "new", "--agent", "test", "--no-start", "cli-replace", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-replace") })

	_, _, err = runCLI(t, "new", "--agent", "test", "--no-start", "--force", "cli-replace", projectDir)
	require.NoError(t, err)

	assert.DirExists(t, sandbox.Dir("cli-replace"))
}

func TestCLI_NetworkLifecycle(t *testing.T) {
	projectDir := cliSetup(t)

	// Create network-isolated sandbox
	_, _, err := runCLI(t, "new", "--agent", "test", "--no-start", "--network-isolated", "cli-net", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-net") })

	// Verify meta has network isolation
	meta, err := sandbox.LoadMeta(sandbox.Dir("cli-net"))
	require.NoError(t, err)
	assert.Equal(t, "isolated", meta.NetworkMode)
	initialDomains := len(meta.NetworkAllow)

	// List domains (allowed)
	stdout, _, err := runCLI(t, "sandbox", "cli-net", "allowed")
	require.NoError(t, err)
	if initialDomains == 0 {
		assert.Contains(t, stdout, "No domains allowed")
	}

	// Add domains (allow)
	stdout, _, err = runCLI(t, "sandbox", "cli-net", "allow", "extra.example.com", "api.test.com")
	require.NoError(t, err)
	assert.Contains(t, stdout, "extra.example.com")

	// Verify persisted
	meta, err = sandbox.LoadMeta(sandbox.Dir("cli-net"))
	require.NoError(t, err)
	assert.Contains(t, meta.NetworkAllow, "extra.example.com")
	assert.Contains(t, meta.NetworkAllow, "api.test.com")

	// List again — should show added domains
	stdout, _, err = runCLI(t, "sandbox", "cli-net", "allowed")
	require.NoError(t, err)
	assert.Contains(t, stdout, "extra.example.com")
	assert.Contains(t, stdout, "api.test.com")

	// List with --json
	stdout, _, err = runCLI(t, "--json", "sandbox", "cli-net", "allowed")
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &result))
	assert.Equal(t, "cli-net", result["name"])
	assert.Equal(t, "isolated", result["network_mode"])

	// Remove a domain (deny)
	stdout, _, err = runCLI(t, "sandbox", "cli-net", "deny", "api.test.com")
	require.NoError(t, err)
	assert.Contains(t, stdout, "api.test.com")

	// Verify removal persisted
	meta, err = sandbox.LoadMeta(sandbox.Dir("cli-net"))
	require.NoError(t, err)
	assert.Contains(t, meta.NetworkAllow, "extra.example.com")
	assert.NotContains(t, meta.NetworkAllow, "api.test.com")

	// Remove nonexistent domain — should error
	_, _, err = runCLI(t, "sandbox", "cli-net", "deny", "nope.com")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not in the allowlist")

	// Add duplicate — should be idempotent
	stdout, _, err = runCLI(t, "sandbox", "cli-net", "allow", "extra.example.com")
	require.NoError(t, err)
	assert.Contains(t, stdout, "All domains already allowed")
}

func TestCLI_BugreportCommand_Unsafe(t *testing.T) {
	projectDir := cliSetup(t)

	_, _, err := runCLI(t, "new", "--agent", "test", "--no-start", "--prompt", "secret task", "cli-br-unsafe", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-br-unsafe") })

	// Write fake JSONL to all 4 log files and agent.log in the sandbox
	sandboxDir := sandbox.Dir("cli-br-unsafe")
	logsDir := filepath.Join(sandboxDir, sandbox.LogsDir)
	require.NoError(t, os.MkdirAll(logsDir, 0700))
	entry := `{"ts":"2026-03-16T10:00:00.000Z","level":"info","event":"test.event","msg":"test log message"}` + "\n"
	require.NoError(t, os.WriteFile(sandbox.CLIJSONLPath("cli-br-unsafe"), []byte(entry), 0600))
	require.NoError(t, os.WriteFile(sandbox.SandboxJSONLPath("cli-br-unsafe"), []byte(entry), 0600))
	require.NoError(t, os.WriteFile(sandbox.MonitorJSONLPath("cli-br-unsafe"), []byte(entry), 0600))
	require.NoError(t, os.WriteFile(sandbox.HooksJSONLPath("cli-br-unsafe"), []byte(entry), 0600))
	require.NoError(t, os.WriteFile(sandbox.AgentLogPath("cli-br-unsafe"), []byte("agent output line\n"), 0600))

	origDir, err := os.Getwd()
	require.NoError(t, err)
	reportDir := t.TempDir()
	require.NoError(t, os.Chdir(reportDir))
	t.Cleanup(func() { _ = os.Chdir(origDir) }) //nolint:gosec // G104: chdir in test cleanup

	_, _, err = runCLI(t, "sandbox", "cli-br-unsafe", "bugreport", "unsafe")
	require.NoError(t, err)

	matches, err := filepath.Glob(filepath.Join(reportDir, "yoloai-bugreport-*.md"))
	require.NoError(t, err)
	require.Len(t, matches, 1)

	content, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	out := string(content)

	assert.Contains(t, out, "Sandbox detail")
	assert.Contains(t, out, "logs/cli.jsonl")
	assert.Contains(t, out, "logs/sandbox.jsonl")
	assert.Contains(t, out, "logs/monitor.jsonl")
	assert.Contains(t, out, "logs/agent-hooks.jsonl")
	assert.Contains(t, out, "Agent output")
	assert.Contains(t, out, "secret task") // prompt included in unsafe

	// Flag-only sections not present in command path
	assert.NotContains(t, out, "Live log")
	assert.NotContains(t, out, "Exit code")
}

func TestCLI_BugreportCommand_Safe(t *testing.T) {
	projectDir := cliSetup(t)

	_, _, err := runCLI(t, "new", "--agent", "test", "--no-start", "--prompt", "secret task", "cli-br-safe", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-br-safe") })

	// Write fake JSONL to all 4 log files
	sandboxDir := sandbox.Dir("cli-br-safe")
	logsDir := filepath.Join(sandboxDir, sandbox.LogsDir)
	require.NoError(t, os.MkdirAll(logsDir, 0700))
	entry := `{"ts":"2026-03-16T10:00:00.000Z","level":"info","event":"test.event","msg":"test log message"}` + "\n"
	require.NoError(t, os.WriteFile(sandbox.CLIJSONLPath("cli-br-safe"), []byte(entry), 0600))
	require.NoError(t, os.WriteFile(sandbox.SandboxJSONLPath("cli-br-safe"), []byte(entry), 0600))
	require.NoError(t, os.WriteFile(sandbox.MonitorJSONLPath("cli-br-safe"), []byte(entry), 0600))
	require.NoError(t, os.WriteFile(sandbox.HooksJSONLPath("cli-br-safe"), []byte(entry), 0600))

	origDir, err := os.Getwd()
	require.NoError(t, err)
	reportDir := t.TempDir()
	require.NoError(t, os.Chdir(reportDir))
	t.Cleanup(func() { _ = os.Chdir(origDir) }) //nolint:gosec // G104: chdir in test cleanup

	_, _, err = runCLI(t, "sandbox", "cli-br-safe", "bugreport", "safe")
	require.NoError(t, err)

	matches, err := filepath.Glob(filepath.Join(reportDir, "yoloai-bugreport-*.md"))
	require.NoError(t, err)
	require.Len(t, matches, 1)

	content, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	out := string(content)

	assert.Contains(t, out, "Sandbox detail")
	assert.Contains(t, out, "logs/cli.jsonl")
	assert.Contains(t, out, "logs/sandbox.jsonl")
	assert.Contains(t, out, "logs/monitor.jsonl")
	assert.Contains(t, out, "logs/agent-hooks.jsonl")

	// Safe mode omits these
	assert.NotContains(t, out, "Agent output")
	assert.NotContains(t, out, "prompt.txt") // prompt.txt section omitted in safe mode
}

func TestCLI_StartAfterDone(t *testing.T) {
	projectDir := cliSetup(t)

	// Shell agent exits after sleep, reaching StatusDone
	_, _, err := runCLI(t, "new", "--agent", "shell", "--prompt", "sleep 5", "cli-startdone", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() {
		if t.Failed() {
			// Dump diagnostic logs to help debug flaky failures.
			sdir := sandbox.Dir("cli-startdone")
			for _, rel := range []string{"agent-status.json", "logs/monitor.jsonl", "logs/sandbox.jsonl"} {
				if data, readErr := os.ReadFile(filepath.Join(sdir, rel)); readErr == nil {
					t.Logf("=== %s ===\n%s", rel, data)
				}
			}
		}
		destroySandbox(t, "cli-startdone")
	})

	rt, err := dockerrt.New(context.Background())
	require.NoError(t, err)
	defer rt.Close() //nolint:errcheck // test cleanup

	testutil.WaitForStatus(context.Background(), t, func(ctx context.Context) (string, error) {
		s, err := sandbox.DetectStatus(ctx, rt, sandbox.InstanceName("cli-startdone"), sandbox.Dir("cli-startdone"))
		return string(s), err
	}, string(sandbox.StatusDone), 60*time.Second)

	// start must succeed on a done sandbox (regression test for baef847)
	_, _, err = runCLI(t, "start", "cli-startdone")
	require.NoError(t, err)
}

func TestCLI_FilesExchange(t *testing.T) {
	projectDir := cliSetup(t)

	_, _, err := runCLI(t, "new", "--agent", "test", "--no-start", "cli-files", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-files") })

	// Put
	srcDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "somefile.txt"), []byte("hello files"), 0600))
	_, _, err = runCLI(t, "files", "cli-files", "put", filepath.Join(srcDir, "somefile.txt"))
	require.NoError(t, err)

	// Ls
	stdout, _, err := runCLI(t, "files", "cli-files", "ls")
	require.NoError(t, err)
	assert.Contains(t, stdout, "somefile.txt")

	// Get
	outDir := t.TempDir()
	_, _, err = runCLI(t, "files", "cli-files", "get", "somefile.txt", "-o", outDir)
	require.NoError(t, err)
	content, err := os.ReadFile(filepath.Join(outDir, "somefile.txt")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Equal(t, "hello files", string(content))
}

func TestCLI_Apply(t *testing.T) {
	projectDir := cliSetup(t)

	_, _, err := runCLI(t, "new", "--agent", "test", "--no-start", "cli-apply", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-apply") })

	// Seed work copy with a distinctive change
	meta, err := sandbox.LoadMeta(sandbox.Dir("cli-apply"))
	require.NoError(t, err)
	workDir := sandbox.WorkDir("cli-apply", meta.Workdir.HostPath)
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "main.go"),
		[]byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"apply-test\") }\n"),
		0600,
	))

	_, _, err = runCLI(t, "apply", "cli-apply", "--yes")
	require.NoError(t, err)

	applied, err := os.ReadFile(filepath.Join(projectDir, "main.go")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Contains(t, string(applied), "apply-test")
}
