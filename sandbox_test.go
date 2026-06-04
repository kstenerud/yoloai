// ABOUTME: Unit tests for the *Sandbox handle — its option types
// ABOUTME: (Clone/Reset→internal mapping), the name-binding accessor, the
// ABOUTME: runtime-free path getters, Unlock, and VscodeAttach resolution.

package yoloai

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

func TestCloneOptions_toInternal(t *testing.T) {
	in := SandboxCloneOptions{Source: "src", Dest: "dst", Overwrite: true}.toInternal()
	assert.Equal(t, sandbox.CloneOptions{Source: "src", Dest: "dst"}, in,
		"Overwrite is an orchestration-layer concern, not carried into the Engine clone")
}

// destroyForOverwrite must short-circuit (and never touch the runtime) when the
// destination doesn't exist — Overwrite on a fresh name is a plain clone.
func TestClient_destroyForOverwrite_MissingDestIsNoop(t *testing.T) {
	c, _ := clientWithSandbox(t) // nil runtime; the no-op path must not reach it
	require.NoError(t, c.destroyForOverwrite(context.Background(), "ghost"))
}

func TestResetOptions_toInternal(t *testing.T) {
	in := SandboxResetOptions{
		RestartContainer: true,
		ClearState:       true,
		KeepCache:        true,
		KeepFiles:        true,
		NoPrompt:         true,
		Debug:            true,
	}.toInternal("mybox")

	assert.Equal(t, "mybox", in.Name, "handle name is folded in")
	assert.True(t, in.Restart, "RestartContainer maps to internal Restart")
	assert.True(t, in.ClearState)
	assert.True(t, in.KeepCache)
	assert.True(t, in.KeepFiles)
	assert.True(t, in.NoPrompt)
	assert.True(t, in.Debug)
}

func TestResetOptions_toInternal_Defaults(t *testing.T) {
	in := SandboxResetOptions{}.toInternal("mybox")
	assert.Equal(t, "mybox", in.Name)
	assert.False(t, in.Restart)
	assert.False(t, in.ClearState)
}

func TestClient_Sandbox_BindsName(t *testing.T) {
	c, sys := clientWithSandbox(t)
	require.NoError(t, os.MkdirAll(sys.layout.SandboxDir("mybox"), 0750))
	sb, err := c.Sandbox("mybox")
	require.NoError(t, err)
	assert.Equal(t, "mybox", sb.Name())
}

// TestClient_Sandbox_NotFound verifies the handle constructor itself refuses an
// unknown name (F22): obtaining the handle IS the existence check, so the error
// surfaces eagerly here, not lazily inside a later operation.
func TestClient_Sandbox_NotFound(t *testing.T) {
	c, _ := clientWithSandbox(t)
	_, err := c.Sandbox("ghost")
	assert.ErrorIs(t, err, ErrSandboxNotFound)
}

func TestSandbox_ExchangePaths(t *testing.T) {
	dir := t.TempDir()
	c, err := NewClient(context.Background(), ClientCreateOptions{DataDir: dir, HomeDir: dir})
	require.NoError(t, err)
	defer c.Close() //nolint:errcheck

	state := c.layout.SandboxDir("box")
	require.NoError(t, os.MkdirAll(state, 0750))
	sb, err := c.Sandbox("box")
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(state, "files"), sb.Files().Path())
	assert.Equal(t, filepath.Join(state, "cache"), sb.CacheDir())
	assert.Equal(t, filepath.Join(state, "runtime-config.json"), sb.RuntimeConfigPath())
	assert.Equal(t, filepath.Join(state, "environment.json"), sb.EnvironmentPath())
}

func TestSandbox_LogPaths(t *testing.T) {
	dir := t.TempDir()
	c, err := NewClient(context.Background(), ClientCreateOptions{DataDir: dir, HomeDir: dir})
	require.NoError(t, err)
	defer c.Close() //nolint:errcheck

	state := c.layout.SandboxDir("box")
	require.NoError(t, os.MkdirAll(state, 0750))
	sb, err := c.Sandbox("box")
	require.NoError(t, err)

	logs := sb.LogPaths()
	assert.Equal(t, filepath.Join(state, "logs", "cli.jsonl"), logs.CLI)
	assert.Equal(t, filepath.Join(state, "logs", "sandbox.jsonl"), logs.Sandbox)
	assert.Equal(t, filepath.Join(state, "logs", "monitor.jsonl"), logs.Monitor)
	assert.Equal(t, filepath.Join(state, "logs", "agent-hooks.jsonl"), logs.Hooks)
	assert.Equal(t, filepath.Join(state, "agent-status.json"), logs.AgentStatus)
}

// TestSandbox_Unlock_Noop verifies that unlocking a sandbox with no lock
// file present reports cleared=false without error. The stale-lock and
// live-holder paths are covered by store/lock_test.go.
func TestSandbox_Unlock_Noop(t *testing.T) {
	dir := t.TempDir()
	c, err := NewClient(context.Background(), ClientCreateOptions{DataDir: dir, HomeDir: dir})
	require.NoError(t, err)
	defer c.Close() //nolint:errcheck

	require.NoError(t, os.MkdirAll(c.layout.SandboxDir("box"), 0750))
	sb, err := c.Sandbox("box")
	require.NoError(t, err)

	cleared, err := sb.Unlock()
	require.NoError(t, err)
	assert.False(t, cleared)
}

// vscodeClient builds a backend-less Client; when meta is non-nil it also
// materializes the sandbox dir + environment.json so c.Sandbox resolves it.
func vscodeClient(t *testing.T, meta *store.Environment) *Client {
	t.Helper()
	dir := t.TempDir()
	c, err := NewClient(context.Background(), ClientCreateOptions{DataDir: dir, HomeDir: dir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	if meta != nil {
		sbDir := c.layout.SandboxDir(meta.Name)
		require.NoError(t, os.MkdirAll(sbDir, 0750))
		require.NoError(t, store.SaveEnvironment(sbDir, meta))
	}
	return c
}

func TestVscodeAttach_Supported(t *testing.T) {
	c := vscodeClient(t, &store.Environment{
		Name:        "box",
		AgentType:   "test",
		BackendType: BackendDocker,
		Workdir:     store.WorkdirEnvironment{HostPath: "/proj", MountPath: "/proj", Mode: store.DirModeCopy},
	})
	sb, err := c.Sandbox("box")
	require.NoError(t, err)

	attach, err := sb.VscodeAttach()
	require.NoError(t, err)
	assert.True(t, attach.Supported)
	assert.Equal(t, store.InstanceName("", "box"), attach.ContainerName)
	assert.Equal(t, "/proj", attach.WorkdirPath)
	assert.True(t, strings.HasPrefix(attach.FolderURI, "vscode-remote://attached-container+"))
	assert.True(t, strings.HasSuffix(attach.FolderURI, "/proj"))
}

func TestVscodeAttach_Unsupported(t *testing.T) {
	c := vscodeClient(t, &store.Environment{
		Name:        "box",
		AgentType:   "test",
		BackendType: BackendSeatbelt,
		Workdir:     store.WorkdirEnvironment{HostPath: "/proj", MountPath: "/proj", Mode: store.DirModeCopy},
	})
	sb, err := c.Sandbox("box")
	require.NoError(t, err)

	attach, err := sb.VscodeAttach()
	require.NoError(t, err)
	assert.False(t, attach.Supported)
	assert.Empty(t, attach.FolderURI)
	assert.Equal(t, BackendSeatbelt, attach.BackendType)
}

func TestVscodeAttach_NotFound(t *testing.T) {
	c := vscodeClient(t, nil)
	_, err := c.Sandbox("ghost")
	require.ErrorIs(t, err, ErrSandboxNotFound)
}
