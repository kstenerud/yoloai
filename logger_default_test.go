// ABOUTME: The library's default log destination is silence, not the process
// ABOUTME: global. A caller that declared no logger gets none — reaching for
// ABOUTME: slog.Default() would publish to wherever the runtime happens to
// ABOUTME: point, which is the undeclared destination D145 forbids.

package yoloai

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/feedback"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewClient_WithoutALoggerDiscards is the red-on-revert test for the
// default change. With slog.Default() as the fallback this passes only if the
// process happens to have a silent handler installed — so the assertion pins a
// capturing one first, and then requires the client's logger to ignore it.
func TestNewClient_WithoutALoggerDiscards(t *testing.T) {
	var captured bytes.Buffer
	restore := slog.Default() //nolint:forbidigo // this test's subject IS the global: it installs a capturing handler to prove the library ignores it (D145)
	slog.SetDefault(slog.New(slog.NewTextHandler(&captured, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(restore) })

	c := newClientForLoggerTest(t, nil)
	logger := c.engine.Logger()
	require.NotNil(t, logger, "the engine must always have a logger; nil would panic at the first use")

	ctx := context.Background()
	assert.False(t, logger.Enabled(ctx, slog.LevelError),
		"a client given no logger must discard, not inherit the process-global handler")

	logger.Error("this must not reach the process-global handler")
	assert.Empty(t, captured.String(),
		"an embedder who declared no destination must not have output published to one")
}

// TestNewClient_WithALoggerUsesIt is the other half: silence is the default,
// not the only option. Without this the discard change could be satisfied by
// ignoring the field entirely.
func TestNewClient_WithALoggerUsesIt(t *testing.T) {
	var declared bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&declared, &slog.HandlerOptions{Level: slog.LevelDebug}))

	c := newClientForLoggerTest(t, logger)
	c.engine.Logger().Error("this must reach the destination the caller named")

	assert.Contains(t, declared.String(), "must reach the destination")
}

// TestSystem_InheritsTheClientLogger covers the sibling handle. System's build
// path calls functions that take a logger, and it used to fabricate
// slog.Default() one frame before each — discarding what the caller declared.
func TestSystem_InheritsTheClientLogger(t *testing.T) {
	var declared bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&declared, &slog.HandlerOptions{Level: slog.LevelDebug}))

	c := newClientForLoggerTest(t, logger)
	c.System().loggerOr().Error("system must use the client's logger")

	assert.Contains(t, declared.String(), "system must use the client's logger")
}

// newClientForLoggerTest builds a Client with the given logger (nil to leave
// the field unset).
func newClientForLoggerTest(t *testing.T, logger *slog.Logger) *Client {
	t.Helper()
	root := t.TempDir()
	dataDir := filepath.Join(root, ".yoloai")
	require.NoError(t, os.MkdirAll(dataDir, 0750))
	c, err := NewClient(context.Background(), ClientCreateOptions{
		DataDir:   dataDir,
		HomeDir:   root,
		Principal: string(testutil.UniqueTestPrincipal(t)),
		Logger:    logger,
	})
	require.NoError(t, err)
	return c
}

// TestNewClient_WithoutSinksDiscards is the edge guarantee that lets the leaves
// panic on a nil sink.
//
// Leaf code refuses a nil destination on purpose: absorbing one makes "the
// caller wanted silence" and "the wiring is broken" indistinguishable. That is
// only safe if the edge always supplies something, so a library caller who
// declares nothing gets working discards rather than a crash somewhere deep in
// a create.
func TestNewClient_WithoutSinksDiscards(t *testing.T) {
	c := newClientForLoggerTest(t, nil)

	require.NotNil(t, c.notices, "a nil notices sink would panic at the first advisory")
	require.NotNil(t, c.progress, "a nil progress sink would panic at the first build line")

	assert.NotPanics(t, func() {
		feedback.Infof(c.notices, "test.event", "discarded")
		feedback.Progressf(c.progress, "test.event", "discarded")
	})
}
