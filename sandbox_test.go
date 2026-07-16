// ABOUTME: Unit tests for the *Sandbox handle — its option types
// ABOUTME: (Reset→internal mapping), the name-binding accessor, the
// ABOUTME: runtime-free path getters, Unlock, and VscodeAttach resolution.

package yoloai

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
)

// execExitError is the Sandbox.Exec boundary that gives embedders one public
// type to match across all backends: a non-zero inner exit (internally a
// *runtime.ExecError, whatever the backend) must surface as the public
// *ExecExitError carrying the same code; every other error passes through.
func TestExecExitError_TranslatesRuntimeExecError(t *testing.T) {
	out := execExitError(&runtime.ExecError{ExitCode: 42, Stderr: "boom"})

	var public *ExecExitError
	require.ErrorAs(t, out, &public, "runtime.ExecError must become the public ExecExitError")
	assert.Equal(t, 42, public.Code)
	assert.Equal(t, 42, public.ExitCode(), "ExecExitError is an ExitCoder carrying the inner code")
}

func TestExecExitError_WrappedRuntimeExecError(t *testing.T) {
	wrapped := fmt.Errorf("attach failed: %w", &runtime.ExecError{ExitCode: 7})
	out := execExitError(wrapped)

	var public *ExecExitError
	require.ErrorAs(t, out, &public, "errors.As must unwrap to the runtime.ExecError")
	assert.Equal(t, 7, public.Code)
}

func TestExecExitError_PassesThroughOtherErrors(t *testing.T) {
	sentinel := errors.New("not an exit error")
	assert.Same(t, sentinel, execExitError(sentinel), "non-exec errors are returned unchanged")
	assert.NoError(t, execExitError(nil), "nil stays nil")
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
	c, err := NewClient(context.Background(), ClientCreateOptions{DataDir: dir, HomeDir: dir, Principal: "cli"})
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
	c, err := NewClient(context.Background(), ClientCreateOptions{DataDir: dir, HomeDir: dir, Principal: "cli"})
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
	c, err := NewClient(context.Background(), ClientCreateOptions{DataDir: dir, HomeDir: dir, Principal: "cli"})
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
	c, err := NewClient(context.Background(), ClientCreateOptions{DataDir: dir, HomeDir: dir, Principal: "cli"})
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
		Principal:   "cli",
		BackendType: BackendDocker,
		Dirs:        []store.DirEnvironment{{HostPath: "/proj", MountPath: "/proj", Mode: store.DirModeCopy}},
	})
	sb, err := c.Sandbox("box")
	require.NoError(t, err)

	attach, err := sb.VscodeAttach()
	require.NoError(t, err)
	assert.True(t, attach.Supported)
	assert.Equal(t, store.InstanceName("cli", "box"), attach.ContainerName)
	assert.Equal(t, "/proj", attach.WorkdirPath)
	assert.True(t, strings.HasPrefix(attach.FolderURI, "vscode-remote://attached-container+"))
	assert.True(t, strings.HasSuffix(attach.FolderURI, "/proj"))
}

func TestVscodeAttach_Unsupported(t *testing.T) {
	c := vscodeClient(t, &store.Environment{
		Name:        "box",
		BackendType: BackendSeatbelt,
		Dirs:        []store.DirEnvironment{{HostPath: "/proj", MountPath: "/proj", Mode: store.DirModeCopy}},
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

// TestSandbox_DestroyedHandle_RefusesEveryErrorReturningMethod pins the
// reuse-after-destroy contract: once a handle's destroyed flag is set, every
// error-returning method short-circuits with ErrSandboxDestroyed before
// touching disk or the backend. The guard runs first, so a backend-less Client
// suffices — none of these reach ensure/engine. Destroy is deliberately absent:
// it is the one method that treats a destroyed handle as benign success (see
// TestSandbox_DestroyedHandle_RepeatDestroyIsBenignSuccess).
func TestSandbox_DestroyedHandle_RefusesEveryErrorReturningMethod(t *testing.T) {
	c := vscodeClient(t, nil) // backend-less; the guard must fire before any IO
	ctx := context.Background()
	sb := &Sandbox{engine: c.engine, name: "box", destroyed: true}

	ops := map[string]func() error{
		"Metadata":     func() error { _, err := sb.Metadata(); return err },
		"Inspect":      func() error { _, err := sb.Inspect(ctx); return err },
		"Stop":         func() error { return sb.Stop(ctx) },
		"Clone":        func() error { _, err := sb.Clone(ctx, "dest", SandboxCloneOptions{}); return err },
		"Start":        func() error { _, err := sb.Start(ctx, SandboxStartOptions{}); return err },
		"Restart":      func() error { _, err := sb.Restart(ctx, SandboxStartOptions{}); return err },
		"Wait":         func() error { _, err := sb.Wait(ctx, SandboxWaitOptions{}); return err },
		"Reset":        func() error { _, err := sb.Reset(ctx, SandboxResetOptions{}); return err },
		"Exec":         func() error { return sb.Exec(ctx, SandboxExecOptions{Command: []string{"true"}}, IOStreams{}) },
		"Unlock":       func() error { _, err := sb.Unlock(); return err },
		"VscodeAttach": func() error { _, err := sb.VscodeAttach(); return err },
	}

	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			assert.ErrorIs(t, op(), ErrSandboxDestroyed)
		})
	}
}

// TestSandbox_DestroyedHandle_RepeatDestroyIsBenignSuccess pins the carve-out: a
// second Destroy on an already-destroyed handle returns an empty result and no
// error, so a defensive cleanup call by one handle holder can't be mistaken for a
// broken-runtime failure by another. The empty result (no notices) marks this
// call as the one that did NOT perform the teardown.
func TestSandbox_DestroyedHandle_RepeatDestroyIsBenignSuccess(t *testing.T) {
	c := vscodeClient(t, nil) // backend-less; must short-circuit before any IO
	ctx := context.Background()
	sb := &Sandbox{engine: c.engine, name: "box", destroyed: true}

	res, err := sb.Destroy(ctx, SandboxDestroyOptions{})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Empty(t, res.Notices, "a repeat Destroy did no teardown, so it carries no notices")
}

// A live handle (destroyed flag unset) must NOT be refused by the guard — the
// guard is the only thing the destroyed flag gates, so an unset flag lets the
// call proceed to its normal path (here, a host-only read that succeeds).
func TestSandbox_LiveHandle_PassesGuard(t *testing.T) {
	c := vscodeClient(t, &store.Environment{
		Name:        "box",
		BackendType: BackendDocker,
		Dirs:        []store.DirEnvironment{{HostPath: "/proj", MountPath: "/proj", Mode: store.DirModeCopy}},
	})
	sb, err := c.Sandbox("box")
	require.NoError(t, err)

	_, err = sb.Metadata()
	assert.NotErrorIs(t, err, ErrSandboxDestroyed, "a live handle must not trip the destroyed guard")
}
