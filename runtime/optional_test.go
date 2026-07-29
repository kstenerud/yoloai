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

// presenceRuntime answers the image-presence probe; errPresenceRuntime fails it.
type presenceRuntime struct {
	Backend
	exists bool
}

func (p presenceRuntime) ImageExists(context.Context, string) (bool, error) { return p.exists, nil }

type errPresenceRuntime struct{ Backend }

func (errPresenceRuntime) ImageExists(context.Context, string) (bool, error) {
	return false, assert.AnError
}

// TestImagePresentFor_DistinguishesAbsentFromUnknown pins the asymmetry the
// caller depends on (DF152). A probe like this is check-then-act and cannot be a
// correctness guarantee, so it is only safe pointed one way: callers use it to
// build MORE, never to skip. That requires "absent" and "cannot say" to be
// distinguishable, which a bare bool could not express.
func TestImagePresentFor_DistinguishesAbsentFromUnknown(t *testing.T) {
	ctx := context.Background()

	present, known := ImagePresentFor(ctx, presenceRuntime{exists: true}, "yoloai-cli-dev")
	assert.True(t, known)
	assert.True(t, present)

	present, known = ImagePresentFor(ctx, presenceRuntime{exists: false}, "yoloai-cli-dev")
	assert.True(t, known, "the backend answered; absent is a real answer")
	assert.False(t, present)

	// A backend that cannot answer must be "no opinion", never "absent" —
	// reading it as absent would rebuild on every call for every such backend.
	present, known = ImagePresentFor(ctx, bareRuntime{}, "yoloai-cli-dev")
	assert.False(t, known, "a backend without the capability has no opinion")
	assert.False(t, present)

	// A failed probe is also no opinion, not absence: a daemon hiccup must not
	// silently turn into a forced rebuild.
	present, known = ImagePresentFor(ctx, errPresenceRuntime{}, "yoloai-cli-dev")
	assert.False(t, known, "a probe error is unknown, not absent")
	assert.False(t, present)
}
