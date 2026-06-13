// ABOUTME: Tests for the optional-interface dispatch helpers (LogsFor,
// ABOUTME: PrepareAgentCommandFor) — that they dispatch to a backend implementing
// ABOUTME: the optional interface, and fall back otherwise.

package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// bareRuntime embeds the Runtime interface (nil) so it satisfies Runtime
// without implementing any optional interface. The helpers only type-assert;
// they never call the embedded (nil) methods.
type bareRuntime struct{ Backend }

type logRuntime struct{ Backend }

func (logRuntime) Logs(_ context.Context, name string, _ int) string { return "logs:" + name }

type prepRuntime struct{ Backend }

func (prepRuntime) PrepareAgentCommand(cmd string) string { return "wrapped:" + cmd }

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
