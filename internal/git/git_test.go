package git

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
)

// gitExecerRuntime embeds runtime.Runtime (nil) so it satisfies the interface
// without implementing every method, and adds GitExec so NewSandbox dispatches
// to it instead of falling back to host git. The embedded nil is never called —
// the sandbox execer only type-asserts for runtime.GitExecer.
type gitExecerRuntime struct {
	runtime.Runtime
	lastWorkDir string
	lastArgs    []string
}

func (g *gitExecerRuntime) GitExec(_ context.Context, _, workDir string, args ...string) (string, error) {
	g.lastWorkDir = workDir
	g.lastArgs = args
	return "dispatched", nil
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

// TestNewSandbox_FallsBackToHostGit verifies the sandbox scope runs host git
// for backends that don't implement GitExecer (Docker, containerd, Seatbelt).
func TestNewSandbox_FallsBackToHostGit(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "f", "v1")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "init")

	// A nil-runtime sandbox falls back to host git; build env from the test env
	// via the host execer the sandbox scope wraps.
	g := &Git{sandboxExec{env: testEnv(), rt: nil, name: "box"}}
	sha, err := g.HeadSHA(context.Background(), dir)
	require.NoError(t, err)
	assert.Len(t, sha, 40)
}
