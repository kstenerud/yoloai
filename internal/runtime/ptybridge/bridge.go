// ABOUTME: Exec runs a child command under a locally-allocated PTY and copies it
// ABOUTME: to the caller's IOStreams — the seatbelt/tart/apple bridge model.
//
// This lives in its own package (not internal/runtime) so the runtime core's
// dependency closure does not pull github.com/creack/pty: only backends that
// actually bridge a local PTY import ptybridge. PTY is a refinement of exec, not
// part of the core contract (which is already PTY-optional via IOStreams.TTY).
package ptybridge

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/creack/pty"

	"github.com/kstenerud/yoloai/internal/runtime"
)

// Exec runs cmd interactively, bridging the caller's IOStreams the same way the
// docker backend bridges its API-socket exec: when TTY is set the child runs
// under a locally-allocated PTY slave, and the library copies the PTY master to
// the caller's Out verbatim.
//
// Wrapping the child in a local PTY (rather than inheriting the host's stdio) is
// what keeps error output from stair-stepping. The PTY slave has OPOST on, so
// the child emits proper CRLF line endings; the host tty — already in raw mode
// at the CLI boundary — receives them verbatim. Inheriting os.Stdout directly
// meant an early child error (e.g. tmux failing to open its socket) printed bare
// LFs while the host's ONLCR was disabled, cascading each line down-and-right.
// Bridging also honors the IOStreams abstraction for non-CLI embedders, whose
// streams may not be real terminal *os.Files.
//
// When TTY is false the child's stdio is wired straight to the streams as plain
// pipes; no PTY is allocated.
func Exec(cmd *exec.Cmd, streams runtime.IOStreams) error {
	if !streams.TTY {
		cmd.Stdin = streams.In
		cmd.Stdout = streams.Out
		cmd.Stderr = streams.Err
		return runtime.InteractiveExitError(cmd.Run())
	}

	ptmx, err := pty.StartWithSize(cmd, winsize(streams.Rows, streams.Cols))
	if err != nil {
		return fmt.Errorf("allocate pty: %w", err)
	}
	defer func() { _ = ptmx.Close() }()

	if streams.Resize != nil {
		done := make(chan struct{})
		defer close(done)
		go forwardResizes(ptmx, streams.Resize, done)
	}

	if streams.In != nil {
		go func() { _, _ = io.Copy(ptmx, streams.In) }()
	}
	// Copy the child's PTY output until it exits and the master reports EOF.
	// A nil Out would make io.Copy deref a nil writer; drain to io.Discard
	// instead so we still empty the PTY master (a full buffer would wedge the
	// child) without requiring every caller to supply a sink.
	out := streams.Out
	if out == nil {
		out = io.Discard
	}
	_, _ = io.Copy(out, ptmx)

	return runtime.InteractiveExitError(cmd.Wait())
}

// forwardResizes applies caller-supplied geometry updates to the PTY until the
// channel closes or the exec returns (done closes). Mirrors the docker backend's
// forwardExecResizes, but drives the local PTY via pty.Setsize.
func forwardResizes(ptmx *os.File, resize <-chan runtime.TermSize, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case sz, ok := <-resize:
			if !ok {
				return
			}
			if sz.Rows > 0 && sz.Cols > 0 {
				_ = pty.Setsize(ptmx, winsize(sz.Rows, sz.Cols))
			}
		}
	}
}

// winsize builds a pty.Winsize from int dimensions; zero means "PTY default".
func winsize(rows, cols int) *pty.Winsize {
	return &pty.Winsize{
		Rows: uint16(rows), //nolint:gosec // G115: terminal dimensions fit uint16
		Cols: uint16(cols), //nolint:gosec // G115: terminal dimensions fit uint16
	}
}
