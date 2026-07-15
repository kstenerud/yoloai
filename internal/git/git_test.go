// ABOUTME: git.NewSandbox picks the in-container vs host-side git executer by
// ABOUTME: confinement mode, and HostExec runs git directly on the host.

package git

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
)

// gitExecerRuntime embeds runtime.Backend (nil) so it satisfies the interface
// without implementing every method. caps drives Descriptor() so a single mock
// covers both SandboxSide and the container-cap (GitExecInConfinement) cases;
// it implements GitExec (the operation the routing uses) and records its args.
// The embedded nil is never called — the sandbox execer only reads Descriptor()
// and GitExec.
type gitExecerRuntime struct {
	runtime.Backend
	caps         runtime.BackendCaps
	lastInstance string
	lastUser     string
	lastWorkDir  string
	lastArgs     []string
}

func (g *gitExecerRuntime) GitExec(_ context.Context, instance, user, workDir string, args ...string) (string, error) {
	g.lastInstance = instance
	g.lastUser = user
	g.lastWorkDir = workDir
	g.lastArgs = args
	return "dispatched", nil
}

func (g *gitExecerRuntime) Descriptor() runtime.BackendDescriptor {
	return runtime.BackendDescriptor{Capabilities: g.caps}
}

// hostSideExecerRuntime implements GitExec yet declares neither SandboxSide nor
// GitExecInConfinement — used to prove the injection decision is
// GitRunsInConfinement, not the mere presence of GitExecer.
type hostSideExecerRuntime struct {
	runtime.Backend
}

func (g *hostSideExecerRuntime) GitExec(_ context.Context, _, _, _ string, _ ...string) (string, error) {
	return "dispatched", nil
}

func (g *hostSideExecerRuntime) Descriptor() runtime.BackendDescriptor {
	return runtime.BackendDescriptor{
		Capabilities: runtime.BackendCaps{FilesystemLocality: runtime.LocalityHostSide},
	}
}

// TestNewSandbox_DispatchesToGitExecer verifies the sandbox scope routes the
// work-copy git through a backend's GitExecer (in-VM for Tart, in-container for
// docker/podman/containerd — audit C1), mapping the host work-copy path to the
// dir's in-sandbox mount path and resolving the instance + container user.
func TestNewSandbox_DispatchesToGitExecer(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	sandboxDir := layout.SandboxDir("box")
	require.NoError(t, os.MkdirAll(sandboxDir, 0o750))
	require.NoError(t, store.SaveEnvironment(sandboxDir, &store.Environment{
		Version: 3,
		Name:    "box",
		Dirs:    []store.DirEnvironment{{HostPath: "/proj", MountPath: "/proj", Mode: store.DirModeCopy}},
	}))

	rt := &gitExecerRuntime{caps: runtime.BackendCaps{GitExecInConfinement: true}}
	g := NewSandbox(layout, rt, "box")

	out, err := g.Run(context.Background(), store.WorkDir(sandboxDir, "/proj"), "status", "--porcelain")
	require.NoError(t, err)
	assert.Equal(t, "dispatched", out)
	assert.Equal(t, "yoloai-box", rt.lastInstance)
	assert.Equal(t, "yoloai", rt.lastUser)
	assert.Equal(t, "/proj", rt.lastWorkDir, "host work-copy path maps to the dir's in-sandbox mount path")
	assert.Equal(t, []string{"-c", "safe.directory=/proj", "status", "--porcelain"}, rt.lastArgs,
		"the work path is trusted against git's dubious-ownership guard (uid may not match across a shared mount)")
}

// TestNewSandbox_InjectsExecerByConfinement verifies the factory injects the
// executor by runtime.GitRunsInConfinement (decided once), not by GitExecer
// presence: a SandboxSide backend and a container-cap backend both get the
// dispatching executor; a backend that merely implements GitExec without the
// cap, and a nil runtime, get the host executor.
func TestNewSandbox_InjectsExecerByConfinement(t *testing.T) {
	layout := config.NewLayout("/home/u/.yoloai")

	vm := NewSandbox(layout, &gitExecerRuntime{caps: runtime.BackendCaps{FilesystemLocality: runtime.LocalitySandboxSide}}, "box")
	_, ok := vm.e.(sandboxExec)
	assert.True(t, ok, "SandboxSide backend must get the sandbox (dispatching) executor")

	ctr := NewSandbox(layout, &gitExecerRuntime{caps: runtime.BackendCaps{GitExecInConfinement: true}}, "box")
	_, ok = ctr.e.(sandboxExec)
	assert.True(t, ok, "container backend (GitExecInConfinement) must get the sandbox (dispatching) executor")

	hs := NewSandbox(layout, &hostSideExecerRuntime{}, "box")
	_, ok = hs.e.(hostExec)
	assert.True(t, ok, "host-side backend gets the host executor even though it implements GitExec")

	nilrt := NewSandbox(layout, nil, "box")
	_, ok = nilrt.e.(hostExec)
	assert.True(t, ok, "nil runtime gets the host executor")
}

// TestHostExec_RunsHostGit verifies the host executor (what NewSandbox injects
// for a HostSide backend) runs real host git.
func TestHostExec_RunsHostGit(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "f", "v1")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "init")

	g := &Git{e: hostExec{env: testEnv()}}
	sha, err := g.HeadSHA(context.Background(), dir)
	require.NoError(t, err)
	assert.Len(t, sha, 40)
}
