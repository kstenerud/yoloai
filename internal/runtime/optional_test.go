// ABOUTME: Tests for the optional-interface dispatch helpers (LogsFor,
// ABOUTME: LauncherOf) — that they dispatch to a backend implementing the
// ABOUTME: optional interface, and fall back otherwise.

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

func TestLogsFor(t *testing.T) {
	assert.Equal(t, "", LogsFor(context.Background(), bareRuntime{}, "box", 10),
		"a backend without LogTailer returns empty logs")
	assert.Equal(t, "logs:box", LogsFor(context.Background(), logRuntime{}, "box", 10),
		"a LogTailer backend is dispatched to")
}

// launchRuntime is a minimal stub that implements ProcessLauncher but does
// nothing. Used only for the LauncherOf type-dispatch test.
type launchRuntime struct{ Backend }

func (launchRuntime) Ready(_ context.Context, _ string) (bool, error) {
	return true, nil
}

func (launchRuntime) Launch(_ context.Context, _ string, _ ProcSpec) (Process, error) {
	return nil, nil
}

func TestLauncherOf(t *testing.T) {
	_, ok := LauncherOf(bareRuntime{})
	assert.False(t, ok, "a backend without ProcessLauncher returns (nil, false)")

	l, ok := LauncherOf(launchRuntime{})
	assert.True(t, ok, "a ProcessLauncher backend is recognised")
	assert.NotNil(t, l, "LauncherOf returns the backend as a ProcessLauncher")
}
