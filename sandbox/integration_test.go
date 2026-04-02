//go:build integration

package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegration_FullLifecycle(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	// Create sandbox (starts container)
	sandboxName := "integ-test"
	_, err := mgr.Create(ctx, CreateOptions{
		Name:    sandboxName,
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)

	// Wait for container to become active
	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName(sandboxName), 15*time.Second)

	// Verify directory structure
	sandboxDir := Dir(sandboxName)
	assert.DirExists(t, sandboxDir)

	meta, err := LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, sandboxName, meta.Name)
	assert.Equal(t, "test", meta.Agent)
	assert.Equal(t, "copy", meta.Workdir.Mode)
	assert.NotEmpty(t, meta.Workdir.BaselineSHA)

	workDir := WorkDir(sandboxName, meta.Workdir.HostPath)
	assert.FileExists(t, filepath.Join(workDir, "main.go"))

	// Verify container is running
	status, err := DetectStatus(ctx, mgr.runtime, InstanceName(sandboxName), Dir(sandboxName))
	require.NoError(t, err)
	assert.Equal(t, StatusActive, status)

	// Exec inside running container
	result, err := mgr.runtime.Exec(ctx, InstanceName(sandboxName), []string{"echo", "lifecycle-test"}, "yoloai")
	require.NoError(t, err)
	assert.Equal(t, "lifecycle-test", result.Stdout)
	assert.Equal(t, 0, result.ExitCode)

	// Modify work copy and verify diff
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "main.go"),
		[]byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hello\") }\n"),
		0600,
	))

	diffResult, err := GenerateDiff(ctx, DiffOptions{Name: sandboxName, Runtime: mgr.runtime})
	require.NoError(t, err)
	assert.False(t, diffResult.Empty)
	assert.Contains(t, diffResult.Output, "fmt")

	// Stop container and verify
	require.NoError(t, mgr.Stop(ctx, sandboxName))

	status, err = DetectStatus(ctx, mgr.runtime, InstanceName(sandboxName), Dir(sandboxName))
	require.NoError(t, err)
	assert.Equal(t, StatusStopped, status)

	// Restart container and verify
	require.NoError(t, mgr.Start(ctx, sandboxName, StartOptions{}))
	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName(sandboxName), 15*time.Second)

	status, err = DetectStatus(ctx, mgr.runtime, InstanceName(sandboxName), Dir(sandboxName))
	require.NoError(t, err)
	assert.Equal(t, StatusActive, status)

	// Exec again after restart
	result, err = mgr.runtime.Exec(ctx, InstanceName(sandboxName), []string{"echo", "after-restart"}, "yoloai")
	require.NoError(t, err)
	assert.Equal(t, "after-restart", result.Stdout)
	assert.Equal(t, 0, result.ExitCode)

	// Generate patch and apply to a target directory
	patch, stat, err := GeneratePatch(ctx, mgr.runtime, sandboxName, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, patch)
	assert.Contains(t, stat, "main.go")

	targetDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(targetDir, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"),
		0600,
	))

	require.NoError(t, workspace.ApplyPatch(patch, targetDir, false))

	applied, err := os.ReadFile(filepath.Join(targetDir, "main.go")) //nolint:gosec // G304: test file path
	require.NoError(t, err)
	assert.Contains(t, string(applied), "fmt.Println")

	// Destroy
	require.NoError(t, mgr.Destroy(ctx, sandboxName))
	assert.NoDirExists(t, sandboxDir)

	// Container should be gone
	status, err = DetectStatus(ctx, mgr.runtime, InstanceName(sandboxName), Dir(sandboxName))
	require.NoError(t, err)
	assert.Equal(t, StatusRemoved, status)
}

func TestIntegration_CreateNoStart(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "nostart",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "nostart") }) //nolint:errcheck // test cleanup

	sandboxDir := Dir("nostart")
	assert.DirExists(t, sandboxDir)

	meta, err := LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, "nostart", meta.Name)
	assert.Equal(t, "test", meta.Agent)
	assert.Equal(t, "copy", meta.Workdir.Mode)
	assert.NotEmpty(t, meta.Workdir.BaselineSHA)

	// Verify work copy contains our file
	workDir := WorkDir("nostart", meta.Workdir.HostPath)
	assert.FileExists(t, filepath.Join(workDir, "main.go"))

	// Verify standard subdirs
	assert.DirExists(t, filepath.Join(sandboxDir, AgentRuntimeDir))
	assert.FileExists(t, filepath.Join(sandboxDir, EnvironmentFile))
	assert.FileExists(t, filepath.Join(sandboxDir, RuntimeConfigFile))
}

func TestIntegration_CopyMode(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "copymode",
		Workdir: DirSpec{Path: projectDir, Mode: DirModeCopy},
		Agent:   "test",
		NoStart: true,
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "copymode") }) //nolint:errcheck // test cleanup

	meta, err := LoadMeta(Dir("copymode"))
	require.NoError(t, err)
	assert.Equal(t, "copy", meta.Workdir.Mode)

	workDir := WorkDir("copymode", meta.Workdir.HostPath)

	// Modify work copy
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "main.go"),
		[]byte("package main\n\n// modified\nfunc main() {}\n"),
		0600,
	))

	// Original should be unchanged
	original, err := os.ReadFile(filepath.Join(projectDir, "main.go")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.NotContains(t, string(original), "modified")
}

func TestIntegration_RWMode(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "rwmode",
		Workdir: DirSpec{Path: projectDir, Mode: DirModeRW},
		Agent:   "test",
		NoStart: true,
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "rwmode") }) //nolint:errcheck // test cleanup

	meta, err := LoadMeta(Dir("rwmode"))
	require.NoError(t, err)
	assert.Equal(t, "rw", meta.Workdir.Mode)
}

func TestIntegration_AuxDirCopy(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)
	auxDir := createAuxDir(t, "libs")

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "auxcopy",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		AuxDirs: []DirSpec{{Path: auxDir, Mode: DirModeCopy}},
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "auxcopy") }) //nolint:errcheck // test cleanup

	meta, err := LoadMeta(Dir("auxcopy"))
	require.NoError(t, err)
	require.Len(t, meta.Directories, 1)
	assert.Equal(t, "copy", meta.Directories[0].Mode)
	assert.NotEmpty(t, meta.Directories[0].BaselineSHA)

	// Verify aux work copy has the file
	auxWorkDir := WorkDir("auxcopy", meta.Directories[0].HostPath)
	assert.FileExists(t, filepath.Join(auxWorkDir, "data.txt"))
}

func TestIntegration_AuxDirRO(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)
	auxDir := createAuxDir(t, "readonly-lib")

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "auxro",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		AuxDirs: []DirSpec{{Path: auxDir}},
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "auxro") }) //nolint:errcheck // test cleanup

	meta, err := LoadMeta(Dir("auxro"))
	require.NoError(t, err)
	require.Len(t, meta.Directories, 1)
	assert.Equal(t, "ro", meta.Directories[0].Mode)
}

func TestIntegration_Replace(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	// Create first sandbox
	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "replaceme",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "replaceme") }) //nolint:errcheck // test cleanup

	// Replace with new sandbox
	_, err = mgr.Create(ctx, CreateOptions{
		Name:    "replaceme",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		Replace: true,
		Version: "test",
	})
	require.NoError(t, err)

	// Should still exist with valid meta
	meta, err := LoadMeta(Dir("replaceme"))
	require.NoError(t, err)
	assert.Equal(t, "replaceme", meta.Name)
}

func TestIntegration_Reset(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	// Create and start the sandbox (Reset requires a restart cycle)
	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "resettest",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "resettest") }) //nolint:errcheck // test cleanup

	// Wait for container to become active
	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName("resettest"), 15*time.Second)

	meta, err := LoadMeta(Dir("resettest"))
	require.NoError(t, err)
	workDir := WorkDir("resettest", meta.Workdir.HostPath)

	// Modify work copy
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "new_file.txt"),
		[]byte("agent wrote this\n"),
		0600,
	))

	// Reset
	require.NoError(t, mgr.Reset(ctx, ResetOptions{Name: "resettest"}))

	// Reset is synchronous (stop+restore+start completes before returning), so
	// just wait for the container to be active again.
	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName("resettest"), 15*time.Second)

	// new_file.txt should be gone after reset
	assert.NoFileExists(t, filepath.Join(workDir, "new_file.txt"))

	// Original file should be restored
	assert.FileExists(t, filepath.Join(workDir, "main.go"))
}

func TestIntegration_Exec(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	// Create and start the sandbox
	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "exectest",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "exectest") }) //nolint:errcheck // test cleanup

	// Wait for container to become active
	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName("exectest"), 15*time.Second)

	// Exec a command
	result, err := mgr.runtime.Exec(ctx, InstanceName("exectest"), []string{"echo", "integration-test"}, "yoloai")
	require.NoError(t, err)
	assert.Equal(t, "integration-test", result.Stdout)
	assert.Equal(t, 0, result.ExitCode)
}

func TestIntegration_DiffClean(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "diffclean",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "diffclean") }) //nolint:errcheck // test cleanup

	diffResult, err := GenerateDiff(ctx, DiffOptions{Name: "diffclean", Runtime: mgr.runtime})
	require.NoError(t, err)
	assert.True(t, diffResult.Empty)
}

func TestIntegration_DiffWithChanges(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "diffchanges",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "diffchanges") }) //nolint:errcheck // test cleanup

	meta, err := LoadMeta(Dir("diffchanges"))
	require.NoError(t, err)
	workDir := WorkDir("diffchanges", meta.Workdir.HostPath)

	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "main.go"),
		[]byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"changed\") }\n"),
		0600,
	))

	diffResult, err := GenerateDiff(ctx, DiffOptions{Name: "diffchanges", Runtime: mgr.runtime})
	require.NoError(t, err)
	assert.False(t, diffResult.Empty)
	assert.Contains(t, diffResult.Output, "fmt")
}

func TestIntegration_ApplyPatch(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "applypatch",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "applypatch") }) //nolint:errcheck // test cleanup

	meta, err := LoadMeta(Dir("applypatch"))
	require.NoError(t, err)
	workDir := WorkDir("applypatch", meta.Workdir.HostPath)

	// Make a change
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "main.go"),
		[]byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"patched\") }\n"),
		0600,
	))

	// Generate patch
	patch, stat, err := GeneratePatch(ctx, mgr.runtime, "applypatch", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, patch)
	assert.Contains(t, stat, "main.go")

	// Apply to a fresh copy of the original
	targetDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(targetDir, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"),
		0600,
	))

	require.NoError(t, workspace.ApplyPatch(patch, targetDir, false))

	applied, err := os.ReadFile(filepath.Join(targetDir, "main.go")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Contains(t, string(applied), "patched")
}

func TestIntegration_Prompt(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "prompttest",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		Prompt:  "echo hello world",
		NoStart: true,
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "prompttest") }) //nolint:errcheck // test cleanup

	sandboxDir := Dir("prompttest")
	meta, err := LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.True(t, meta.HasPrompt)

	// Verify prompt.txt was written
	prompt, err := os.ReadFile(filepath.Join(sandboxDir, "prompt.txt")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Equal(t, "echo hello world", string(prompt))
}

func TestIntegration_ResourceLimits(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "reslimits",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		CPUs:    "2",
		Memory:  "512m",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "reslimits") }) //nolint:errcheck // test cleanup

	meta, err := LoadMeta(Dir("reslimits"))
	require.NoError(t, err)
	require.NotNil(t, meta.Resources)
	assert.Equal(t, "2", meta.Resources.CPUs)
	assert.Equal(t, "512m", meta.Resources.Memory)
}

func TestIntegration_PortForwarding(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "portfwd",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		Ports:   []string{"3000:3000"},
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "portfwd") }) //nolint:errcheck // test cleanup

	meta, err := LoadMeta(Dir("portfwd"))
	require.NoError(t, err)
	assert.Contains(t, meta.Ports, "3000:3000")
}

func TestIntegration_MultiSandbox(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	for _, name := range []string{"multi-a", "multi-b"} {
		_, err := mgr.Create(ctx, CreateOptions{
			Name:    name,
			Workdir: DirSpec{Path: projectDir},
			Agent:   "test",
			NoStart: true,
			Version: "test",
		})
		require.NoError(t, err)
	}
	t.Cleanup(func() {
		mgr.Destroy(ctx, "multi-a") //nolint:errcheck // test cleanup
		mgr.Destroy(ctx, "multi-b") //nolint:errcheck // test cleanup
	})

	// Both should exist
	assert.DirExists(t, Dir("multi-a"))
	assert.DirExists(t, Dir("multi-b"))

	// Both should be in the listing
	infos, err := ListSandboxes(ctx, mgr.runtime)
	require.NoError(t, err)

	names := make(map[string]bool)
	for _, info := range infos {
		names[info.Meta.Name] = true
	}
	assert.True(t, names["multi-a"], "multi-a should be listed")
	assert.True(t, names["multi-b"], "multi-b should be listed")
}

func TestIntegration_DestroyCleanup(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "destroyme",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		Version: "test",
	})
	require.NoError(t, err)

	sandboxDir := Dir("destroyme")
	assert.DirExists(t, sandboxDir)

	require.NoError(t, mgr.Destroy(ctx, "destroyme"))
	assert.NoDirExists(t, sandboxDir)

	// Container should be removed
	status, err := DetectStatus(ctx, mgr.runtime, InstanceName("destroyme"), Dir("destroyme"))
	require.NoError(t, err)
	assert.Equal(t, StatusRemoved, status)
}

// TestIntegration_NetworkIsolation verifies that network-isolated sandboxes block
// outbound traffic. The isolation is enforced by iptables rules applied in
// entrypoint.py; this test confirms those rules are actually in effect.
func TestIntegration_NetworkIsolation(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "netisolated",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		Network: NetworkModeIsolated,
		// No NetworkAllow entries — all outbound should be blocked.
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "netisolated") }) //nolint:errcheck // test cleanup

	// Verify runtime-config.json has network_isolated: true so the test
	// can't pass vacuously (e.g., if the config field were never written).
	rcData, err := os.ReadFile(filepath.Join(Dir("netisolated"), RuntimeConfigFile)) //nolint:gosec // test path
	require.NoError(t, err)
	var rc map[string]any
	require.NoError(t, json.Unmarshal(rcData, &rc))
	assert.Equal(t, true, rc["network_isolated"], "runtime-config.json must have network_isolated: true")

	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName("netisolated"), 15*time.Second)

	// The entrypoint applies iptables rules before starting the agent, but
	// WaitForActive only confirms the container process is running, not that
	// the Python setup has completed. Give it a moment.
	time.Sleep(2 * time.Second)

	// curl to a well-known external IP should be blocked by the iptables REJECT rule.
	// Exec returns a non-nil error when the command exits non-zero, so we discard
	// the error and check ExitCode directly.
	external, _ := mgr.runtime.Exec(ctx, InstanceName("netisolated"),
		[]string{"curl", "-sf", "--max-time", "5", "https://1.1.1.1"}, "yoloai")
	assert.NotEqual(t, 0, external.ExitCode,
		"curl to external IP should be blocked by network isolation")

	// Loopback must still be reachable — isolation must not block intra-container traffic.
	// Nothing listens on 127.0.0.1:80, so curl gets "connection refused" (exit 7),
	// which is distinct from an iptables timeout (exit 28). The point is that loopback
	// traffic is not blocked by our rules.
	lo, _ := mgr.runtime.Exec(ctx, InstanceName("netisolated"),
		[]string{"curl", "-sf", "--max-time", "5", "http://127.0.0.1"}, "yoloai")
	assert.NotEqual(t, 28, lo.ExitCode, "loopback should not time out (iptables must allow lo)")
}

// TestIntegration_ReadOnlyMountVerified verifies that a read-only aux directory
// mount is actually enforced inside the container, not just recorded in meta.json.
// TestIntegration_AuxDirRO only checks the meta; this test proves the kernel
// enforces the mount flag.
func TestIntegration_ReadOnlyMountVerified(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)
	auxDir := createAuxDir(t, "readonly-verify")

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "romount",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		AuxDirs: []DirSpec{{Path: auxDir}}, // default mode = read-only
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "romount") }) //nolint:errcheck // test cleanup

	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName("romount"), 15*time.Second)

	// Attempt to write to the read-only aux dir from inside the container.
	// The mount target mirrors the host path by default.
	result, _ := mgr.runtime.Exec(ctx, InstanceName("romount"),
		[]string{"sh", "-c", "echo pwned > " + auxDir + "/attack.txt"}, "yoloai")
	assert.NotEqual(t, 0, result.ExitCode,
		"write to read-only aux dir mount should fail inside the container")

	// Verify the host-side aux dir is unmodified.
	assert.NoFileExists(t, filepath.Join(auxDir, "attack.txt"))
}

// TestIntegration_CredentialInjection verifies the /run/secrets credential
// lifecycle: secrets are readable inside the agent's process tree, and the
// host-side temp directory is cleaned up after container start.
//
// Docker exec does NOT inherit the entrypoint's environment (it starts a new
// process chain), so we verify credentials by having the test agent's prompt
// write the env var to a file, then reading that file via exec.
func TestIntegration_CredentialInjection(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	meta, err := mgr.Create(ctx, CreateOptions{
		Name:    "credinject",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		Prompt:  "printenv TEST_CREDENTIAL > /tmp/cred-check; sleep 5",
		Env:     map[string]string{"TEST_CREDENTIAL": "secret-value-xyz"},
		Version: "test",
	})
	require.NoError(t, err)
	_ = meta
	t.Cleanup(func() { mgr.Destroy(ctx, "credinject") }) //nolint:errcheck // test cleanup

	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName("credinject"), 15*time.Second)

	// Poll until the prompt has written the credential file. The test agent
	// runs sh -c "PROMPT" via tmux; on slow CI runners a fixed sleep was
	// insufficient and caused flaky failures.
	var credOutput string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		r, execErr := mgr.runtime.Exec(ctx, InstanceName("credinject"),
			[]string{"cat", "/tmp/cred-check"}, "yoloai")
		if execErr == nil && r.ExitCode == 0 && r.Stdout != "" {
			credOutput = r.Stdout
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	require.NotEmpty(t, credOutput, "credential file was never written by prompt within 15s")
	assert.Equal(t, "secret-value-xyz", credOutput,
		"credential should be injected via /run/secrets and available in agent env")

	// The host-side temp secrets dir (yoloai-secrets-*) should have been
	// removed by the defer in launchContainer. Check that no matching dir
	// remains in the system temp directory.
	tmpParent := os.TempDir()
	entries, err := os.ReadDir(tmpParent)
	require.NoError(t, err)
	for _, e := range entries {
		assert.False(t, e.IsDir() && len(e.Name()) > 15 && e.Name()[:15] == "yoloai-secrets-",
			"host-side secrets temp dir should be cleaned up: %s", e.Name())
	}
}

// TestIntegration_AgentStubWorkflow tests the full agent-does-work → diff → apply pipeline.
// It uses the "test" agent (bash), starts the container, execs a command inside
// to simulate agent output, then verifies diff detects the change and apply lands
// the file in the original project directory.
func TestIntegration_AgentStubWorkflow(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "stubworkflow",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "stubworkflow") }) //nolint:errcheck // test cleanup

	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName("stubworkflow"), 15*time.Second)

	// Simulate agent output: create a new file inside the container.
	// Exec runs in the container's WorkingDir (= project bind-mount), so the
	// file appears in the work copy on the host side via the bind-mount.
	result, err := mgr.runtime.Exec(ctx, InstanceName("stubworkflow"), []string{"touch", "agent-output.txt"}, "yoloai")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)

	// Verify the file is visible in the work copy on the host
	meta, err := LoadMeta(Dir("stubworkflow"))
	require.NoError(t, err)
	workDir := WorkDir("stubworkflow", meta.Workdir.HostPath)
	assert.FileExists(t, filepath.Join(workDir, "agent-output.txt"))

	// Diff should detect the new file
	diffResult, err := GenerateDiff(ctx, DiffOptions{Name: "stubworkflow", Runtime: mgr.runtime})
	require.NoError(t, err)
	assert.False(t, diffResult.Empty, "diff should not be empty after agent created a file")
	assert.Contains(t, diffResult.Output, "agent-output.txt")

	// Generate patch and apply to a fresh copy of the original project
	patch, stat, err := GeneratePatch(ctx, mgr.runtime, "stubworkflow", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, patch)
	assert.Contains(t, stat, "agent-output.txt")

	targetDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(targetDir, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"),
		0600,
	))
	require.NoError(t, workspace.ApplyPatch(patch, targetDir, false))
	assert.FileExists(t, filepath.Join(targetDir, "agent-output.txt"))
}

func TestIntegration_Clone(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "clone-a",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		mgr.Destroy(ctx, "clone-a") //nolint:errcheck // test cleanup
		mgr.Destroy(ctx, "clone-b") //nolint:errcheck // test cleanup
	})

	// Seed a change in A's work copy
	meta, err := LoadMeta(Dir("clone-a"))
	require.NoError(t, err)
	workDir := WorkDir("clone-a", meta.Workdir.HostPath)
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "main.go"),
		[]byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"clone-test\") }\n"),
		0600,
	))

	// Clone A → B
	require.NoError(t, mgr.Clone(ctx, CloneOptions{Source: "clone-a", Dest: "clone-b"}))

	// Diff on clone should show the seeded change
	diffResult, err := GenerateDiff(ctx, DiffOptions{Name: "clone-b", Runtime: mgr.runtime})
	require.NoError(t, err)
	assert.False(t, diffResult.Empty, "cloned sandbox should have changes")
	assert.Contains(t, diffResult.Output, "clone-test")
}

func TestIntegration_Overlay(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("overlayfs lower-layer files are immutable on Docker Desktop/VirtioFS; test requires native Linux overlayfs")
	}
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "overlay-integ",
		Workdir: DirSpec{Path: projectDir, Mode: DirModeOverlay},
		Agent:   "test",
		Version: "test",
	})
	if err != nil {
		if strings.Contains(err.Error(), "overlay") || strings.Contains(err.Error(), "mount") ||
			strings.Contains(err.Error(), "CAP_SYS_ADMIN") || strings.Contains(err.Error(), "permission") {
			t.Skip("overlay not supported: " + err.Error())
		}
		require.NoError(t, err) // fail on unexpected errors
	}
	t.Cleanup(func() {
		// The overlayfs workdir (ovlwork/) contains root-owned kernel files that
		// cannot be removed by the test process. Clean them up via exec as root
		// inside the still-running container before destroying the sandbox.
		ovlEncoded := EncodePath(projectDir)
		mgr.runtime.Exec(ctx, InstanceName("overlay-integ"), //nolint:errcheck // best-effort
			[]string{"rm", "-rf", "/yoloai/overlay/" + ovlEncoded + "/ovlwork"}, "root")
		mgr.Destroy(ctx, "overlay-integ") //nolint:errcheck // test cleanup
	})

	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName("overlay-integ"), 15*time.Second)

	// For overlay mode, MountPath is /yoloai/overlay/<encoded>/merged — not the host path.
	meta, err := LoadMeta(Dir("overlay-integ"))
	require.NoError(t, err)
	containerPath := meta.Workdir.MountPath

	// Re-initialize git inside the container's overlay merged dir. The entrypoint's
	// chown -R can make overlayfs directories opaque, hiding the lower layer's git
	// objects. In production the agent (or auto-commit) creates a fresh repo; we
	// replicate that here.
	//
	// Poll until git init + commit succeeds. The overlay mount is done by the
	// entrypoint; on slow systems WaitForActive may return before the mount is
	// fully visible to subsequent exec calls.
	// Initialize git and create baseline commit inside the container, then
	// retrieve the SHA in a separate exec to avoid mixing git commit output
	// with the SHA.
	initCmd := fmt.Sprintf(
		"cd %s && rm -rf .git && git init -b main && git config user.email test@test && git config user.name test && git add -A && git commit -q -m baseline",
		containerPath,
	)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		initResult, initErr := mgr.runtime.Exec(ctx, InstanceName("overlay-integ"),
			[]string{"sh", "-c", initCmd}, "yoloai")
		if initErr == nil && initResult.ExitCode == 0 {
			break
		}
		if time.Now().Add(500 * time.Millisecond).After(deadline) {
			require.NoError(t, initErr, "git init inside overlay should succeed within 10s")
			require.Equal(t, 0, initResult.ExitCode, "git init exit code")
		}
		time.Sleep(500 * time.Millisecond)
	}

	shaResult, err := mgr.runtime.Exec(ctx, InstanceName("overlay-integ"),
		[]string{"git", "-C", containerPath, "rev-parse", "HEAD"}, "yoloai")
	require.NoError(t, err)
	baselineSHA := strings.TrimSpace(shaResult.Stdout)
	require.Len(t, baselineSHA, 40, "baseline SHA should be 40 hex chars")
	require.NoError(t, updateOverlayBaseline("overlay-integ", projectDir, baselineSHA))

	// Write a file inside the container (overlay captures it in upper layer)
	writeResult, err := mgr.runtime.Exec(ctx, InstanceName("overlay-integ"),
		[]string{"sh", "-c", fmt.Sprintf("echo overlay-test > %s/output.txt", containerPath)}, "yoloai")
	require.NoError(t, err)
	assert.Equal(t, 0, writeResult.ExitCode)

	// Diff: must use GenerateOverlayDiff (GenerateDiff returns a stub for overlay)
	diffResults, err := GenerateOverlayDiff(ctx, mgr.runtime, DiffOptions{Name: "overlay-integ"})
	require.NoError(t, err)
	require.NotEmpty(t, diffResults, "overlay diff should return results")
	assert.False(t, diffResults[0].Empty, "overlay should have changes after exec write")
	assert.Contains(t, diffResults[0].Output, "output.txt")

	// Apply: must use GenerateOverlayPatch (ApplyAll skips overlay dirs)
	patches, err := GenerateOverlayPatch(ctx, mgr.runtime, "overlay-integ", nil)
	require.NoError(t, err)
	require.NotEmpty(t, patches)
	require.NoError(t, workspace.ApplyPatch(patches[0].Patch, projectDir, false))

	applied, err := os.ReadFile(filepath.Join(projectDir, "output.txt")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Contains(t, string(applied), "overlay-test")
}
