// ABOUTME: Tests the non-terminal branch of WithTerminal — the path CI can
// ABOUTME: exercise (test stdin is not a tty), pinning the opaque-stream contract.
package cliutil

import (
	"errors"
	"os"
	"testing"

	"github.com/creack/pty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/term"

	yoloai "github.com/kstenerud/yoloai"
)

// When stdin is not a terminal (the usual `go test` case), WithTerminal must
// skip all terminal management — no raw mode, no resize pump — and hand fn the
// process streams with TTY=false (claiming TTY over non-tty stdin makes the
// backend's `… -it` exec fail) and no Resize channel. It must invoke fn exactly
// once and propagate its error verbatim.
func TestWithTerminal_NonTTY(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) { //nolint:gosec // G115: fd is a small int
		t.Skip("stdin is a terminal; this test pins the non-tty branch")
	}

	sentinel := errors.New("boom")
	calls := 0
	var got yoloai.IOStreams

	err := WithTerminal(func(io yoloai.IOStreams) error {
		calls++
		got = io
		return sentinel
	})

	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, 1, calls)
	assert.Equal(t, os.Stdin, got.In)
	assert.Equal(t, os.Stdout, got.Out)
	assert.False(t, got.TTY, "TTY must be false when stdin is not a terminal")
	assert.Nil(t, got.Resize, "no resize pump when stdin is not a terminal")
}

// When stdin is a terminal but stdout is captured/redirected (a non-interactive
// exec — e.g. control-eval running `yoloai exec` with output piped),
// WithTerminal must treat the session as non-interactive: TTY=false, no raw
// mode, no resize pump. Gating on stdin alone would put the shared controlling
// terminal in raw mode, and concurrent invocations then race on MakeRaw/Restore
// and can leave it raw (ONLCR/ISIG off → staircased output, dead Ctrl-C).
func TestWithTerminal_TTYStdinButCapturedStdout(t *testing.T) {
	ptmx, tty, err := pty.Open()
	require.NoError(t, err)
	t.Cleanup(func() { _ = ptmx.Close(); _ = tty.Close() })

	pr, pw, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = pr.Close(); _ = pw.Close() })

	require.True(t, term.IsTerminal(int(tty.Fd())), "pty slave must be a terminal") //nolint:gosec // G115: fd is a small int
	require.False(t, term.IsTerminal(int(pw.Fd())), "pipe must not be a terminal")  //nolint:gosec // G115: fd is a small int

	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = tty, pw
	t.Cleanup(func() { os.Stdin, os.Stdout = oldIn, oldOut })

	var got yoloai.IOStreams
	err = WithTerminal(func(io yoloai.IOStreams) error {
		got = io
		return nil
	})

	require.NoError(t, err)
	assert.False(t, got.TTY, "captured stdout must make the session non-interactive (no raw mode)")
	assert.Nil(t, got.Resize, "no resize pump when the session is non-interactive")
}
