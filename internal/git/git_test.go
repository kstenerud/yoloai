package git

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
)

// gitExecerRuntime embeds runtime.Backend (nil) so it satisfies the interface
// without implementing every method. It declares SandboxSide locality (so the
// sandbox execer routes git to the backend) and implements GitExec (the
// operation the routing uses). The embedded nil is never called — the sandbox
// execer only reads Descriptor() and GitExec.
type gitExecerRuntime struct {
	runtime.Backend
	lastWorkDir string
	lastArgs    []string
}

func (g *gitExecerRuntime) GitExec(_ context.Context, _, workDir string, args ...string) (string, error) {
	g.lastWorkDir = workDir
	g.lastArgs = args
	return "dispatched", nil
}

func (g *gitExecerRuntime) Descriptor() runtime.BackendDescriptor {
	return runtime.BackendDescriptor{
		Capabilities: runtime.BackendCaps{FilesystemLocality: runtime.LocalitySandboxSide},
	}
}

// hostSideExecerRuntime implements GitExec yet declares HostSide locality —
// used to prove the injection decision is the FilesystemLocality property, not
// the mere presence of GitExecer.
type hostSideExecerRuntime struct {
	runtime.Backend
}

func (g *hostSideExecerRuntime) GitExec(_ context.Context, _, _ string, _ ...string) (string, error) {
	return "dispatched", nil
}

func (g *hostSideExecerRuntime) Descriptor() runtime.BackendDescriptor {
	return runtime.BackendDescriptor{
		Capabilities: runtime.BackendCaps{FilesystemLocality: runtime.LocalityHostSide},
	}
}

// TestNewSandbox_DispatchesToGitExecer verifies the sandbox scope routes git
// through a backend's GitExecer (e.g. Tart runs git in-VM) instead of host git.
func TestNewSandbox_DispatchesToGitExecer(t *testing.T) {
	rt := &gitExecerRuntime{}
	g := NewSandbox(config.NewLayout("/home/u/.yoloai"), rt, "box")

	out, err := g.Run(context.Background(), "/work", "status", "--porcelain")
	require.NoError(t, err)
	assert.Equal(t, "dispatched", out)
	assert.Equal(t, "/work", rt.lastWorkDir)
	assert.Equal(t, []string{"status", "--porcelain"}, rt.lastArgs)
}

// TestNewSandbox_InjectsExecerByLocality verifies the factory injects the
// executor by FilesystemLocality (decided once), not by GitExecer presence: a
// SandboxSide backend gets the dispatching executor; a HostSide backend (even
// one that implements GitExec) and a nil runtime get the host executor.
func TestNewSandbox_InjectsExecerByLocality(t *testing.T) {
	layout := config.NewLayout("/home/u/.yoloai")

	sb := NewSandbox(layout, &gitExecerRuntime{}, "box")
	_, ok := sb.e.(sandboxExec)
	assert.True(t, ok, "SandboxSide backend must get the sandbox (dispatching) executor")

	hs := NewSandbox(layout, &hostSideExecerRuntime{}, "box")
	_, ok = hs.e.(hostExec)
	assert.True(t, ok, "HostSide backend gets the host executor even though it implements GitExec")

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

	g := &Git{hostExec{env: testEnv()}}
	sha, err := g.HeadSHA(context.Background(), dir)
	require.NoError(t, err)
	assert.Len(t, sha, 40)
}
