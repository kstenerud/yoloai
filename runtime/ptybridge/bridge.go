// ABOUTME: Exec runs a child command under a locally-allocated PTY and copies it
// ABOUTME: to the caller's IOStreams — the seatbelt/tart/apple bridge model.
//
// This lives in its own package (not runtime) so the runtime core's
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

	"github.com/kstenerud/yoloai/runtime"
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
func Exec(cmd *exec.Cmd, streams runtime.IOStreams, opts ...Option) error {
	var cfg options
	for _, opt := range opts {
		opt(&cfg)
	}

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
	// The apple backend runs the interactive app under a second, in-guest PTY
	// (`container exec -t`) that the app owns and sets raw. The app's bare-LF
	// same-column cursor-down (cud1) then crosses this host-local PTY, whose slave
	// `container exec` forces to ONLCR — rewriting every \n to \r\n, so a cud1
	// becomes carriage-return-to-col-0 and the app's output shifts out of column
	// alignment. The exec CLI re-asserts ONLCR even if we clear it on the slave,
	// so we undo it in the stream: strip the one CR the ONLCR injected before each
	// LF. Only apple opts in: seatbelt is single-PTY (the app owns and raws the
	// only PTY) and tart is double-PTY but its `tart exec -t` does not force ONLCR
	// (tested) — for either, stripping would instead corrupt legitimate CRLFs.
	if cfg.remotePTY {
		s := &crStripper{w: out}
		_, _ = io.Copy(s, ptmx)
		_ = s.flush()
	} else {
		_, _ = io.Copy(out, ptmx)
	}

	return runtime.InteractiveExitError(cmd.Wait())
}

// Option configures Exec.
type Option func(*options)

type options struct {
	remotePTY bool
}

// WithRemotePTY makes the bridge strip the redundant CR that a remote-PTY exec
// CLI injects by forcing ONLCR on this host-local slave. The interactive app's
// real terminal lives in the guest and is set raw by the app, but the app cannot
// reach this slave, so its bare-LF cursor moves (cud1) arrive as \r\n and shift
// output out of column alignment; stripping the injected CR restores verbatim
// output. Enable only where the exec CLI actually does this: apple
// (`container exec -t`) does; tart (`tart exec -t`, tested) and single-PTY
// seatbelt do not — enabling it there would corrupt their legitimate CRLFs.
// See backend-idiosyncrasies.md.
func WithRemotePTY() Option { return func(o *options) { o.remotePTY = true } }

// crStripper removes exactly one CR immediately preceding each LF, undoing the
// single ONLCR the remote-PTY exec CLI applies to this bridge's slave: the app's
// line advance (nel = \r\n) becomes \r\r\n and is restored to \r\n, while its
// same-column cursor-down (cud1 = \n) becomes \r\n and is restored to \n. A lone
// CR (no following LF, e.g. a carriage return to column 0) is left untouched. It
// buffers a CR that lands on a read boundary so the strip stays correct across
// chunk boundaries; flush emits any trailing held CR at EOF.
type crStripper struct {
	w         io.Writer
	pendingCR bool
}

func (s *crStripper) Write(p []byte) (int, error) {
	buf := make([]byte, 0, len(p)+1)
	if s.pendingCR {
		if len(p) == 0 || p[0] != '\n' {
			buf = append(buf, '\r') // held CR was not before a LF; keep it
		}
		s.pendingCR = false
	}
	for i := 0; i < len(p); i++ {
		if p[i] == '\r' {
			if i+1 == len(p) {
				s.pendingCR = true // decide once the next chunk arrives
				break
			}
			if p[i+1] == '\n' {
				continue // drop the CR the ONLCR injected before this LF
			}
		}
		buf = append(buf, p[i])
	}
	if _, err := s.w.Write(buf); err != nil {
		return 0, err
	}
	return len(p), nil
}

// flush emits a CR held from a chunk that ended on \r (with no LF to strip it
// against). Call once after the copy loop drains.
func (s *crStripper) flush() error {
	if !s.pendingCR {
		return nil
	}
	s.pendingCR = false
	_, err := s.w.Write([]byte{'\r'})
	return err
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
