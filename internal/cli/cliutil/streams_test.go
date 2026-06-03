// ABOUTME: Tests the non-terminal branch of WithTerminal — the path CI can
// ABOUTME: exercise (test stdin is not a tty), pinning the opaque-stream contract.
package cliutil

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/term"

	yoloai "github.com/kstenerud/yoloai"
)

// When stdin is not a terminal (the usual `go test` case), WithTerminal must
// skip all terminal management — no raw mode, no resize pump — and hand fn the
// process streams with no Resize channel. It must invoke fn exactly once and
// propagate its error verbatim.
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
	assert.True(t, got.TTY)
	assert.Nil(t, got.Resize, "no resize pump when stdin is not a terminal")
}
