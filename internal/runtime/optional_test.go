// ABOUTME: Tests for the optional-interface dispatch helpers (LogsFor,
// ABOUTME: PrepareAgentCommandFor, GitExecFor) — that they dispatch to a backend
// ABOUTME: implementing the optional interface, and fall back otherwise.

package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bareRuntime embeds the Runtime interface (nil) so it satisfies Runtime
// without implementing any optional interface. The helpers only type-assert;
// they never call the embedded (nil) methods.
type bareRuntime struct{ Runtime }

type logRuntime struct{ Runtime }

func (logRuntime) Logs(_ context.Context, name string, _ int) string { return "logs:" + name }

type prepRuntime struct{ Runtime }

func (prepRuntime) PrepareAgentCommand(cmd string) string { return "wrapped:" + cmd }

type gitRuntime struct{ Runtime }

func (gitRuntime) GitExec(_ context.Context, _, _ string, _ ...string) (string, error) {
	return "dispatched", nil
}

func TestLogsFor(t *testing.T) {
	assert.Equal(t, "", LogsFor(context.Background(), bareRuntime{}, "box", 10),
		"a backend without LogTailer returns empty logs")
	assert.Equal(t, "logs:box", LogsFor(context.Background(), logRuntime{}, "box", 10),
		"a LogTailer backend is dispatched to")
}

func TestPrepareAgentCommandFor(t *testing.T) {
	assert.Equal(t, "agent --foo", PrepareAgentCommandFor(bareRuntime{}, "agent --foo"),
		"a backend without AgentCommandPreparer returns the command unchanged")
	assert.Equal(t, "wrapped:agent --foo", PrepareAgentCommandFor(prepRuntime{}, "agent --foo"),
		"an AgentCommandPreparer backend is dispatched to")
}

func TestGitExecFor_DispatchesToGitExecer(t *testing.T) {
	out, err := GitExecFor(context.Background(), gitRuntime{}, "box", "/w", "status")
	require.NoError(t, err)
	assert.Equal(t, "dispatched", out,
		"a GitExecer backend (e.g. Tart) is dispatched to instead of host git")
	// The default host-git path (non-GitExecer backends) is exercised by
	// containerd's TestGitExec_ExitOneReturnsExecError.
}
