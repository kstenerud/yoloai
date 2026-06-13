// ABOUTME: WithTerminal binds the calling process's stdin/stdout/stderr to a
// ABOUTME: yoloai.IOStreams and owns host-terminal management (raw mode, resize)
// ABOUTME: at the CLI boundary, keeping the library a pure byte-stream consumer.

package cliutil

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/term"

	yoloai "github.com/kstenerud/yoloai"
)

// WithTerminal binds the calling process's terminal to a yoloai.IOStreams and
// runs fn with it. When stdin is a real terminal it puts it in raw mode (so
// escape sequences reach the remote PTY rather than the host line discipline),
// seeds the initial geometry, and pumps SIGWINCH-driven resizes into
// IOStreams.Resize — restoring the terminal and stopping the pump when fn
// returns. Every backend bridges through a PTY (a remote one over its API
// socket, or a local one via ptybridge.Exec for tart/seatbelt), so the raw mode
// set here applies uniformly. When stdin is not a terminal (piped input, tests)
// it skips all terminal management and hands fn plain streams.
//
// This is where the §12 ambient-terminal reads live: the library never
// inspects a stream's FD, sets raw mode, or installs signal handlers — that is
// the embedder's job, and for the CLI the embedder is right here.
func WithTerminal(fn func(yoloai.IOStreams) error) error {
	in := os.Stdin
	fd := int(in.Fd()) //nolint:gosec // G115: a file descriptor is a small non-negative int
	isTTY := term.IsTerminal(fd)
	streams := yoloai.IOStreams{
		In:   in,
		Out:  os.Stdout,
		Err:  os.Stderr,
		TTY:  isTTY,
		Term: os.Getenv("TERM"), //nolint:forbidigo // §12: CLI boundary captures the user's terminal type; library never reads it
	}

	// TTY=true is a contract that the streams ARE a tty (the backend runs the
	// inner exec with `-it`); claiming it over piped/redirected stdin makes
	// `docker exec -it` fail with "the input device is not a TTY". When stdin
	// is not a terminal, hand fn plain streams (TTY=false) and skip raw mode.
	if !isTTY {
		return fn(streams)
	}

	if rows, cols, err := pty.Getsize(in); err == nil {
		streams.Rows, streams.Cols = rows, cols
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("set terminal raw mode: %w", err)
	}
	defer term.Restore(fd, oldState) //nolint:errcheck // best-effort restore on exit

	resize := make(chan yoloai.TermSize, 1)
	streams.Resize = resize
	stop := make(chan struct{})
	defer close(stop)
	go pumpResize(in, resize, stop)

	return fn(streams)
}

// pumpResize forwards window-size changes to the resize channel until stop is
// closed. The send is non-blocking (buffered, with a default drop) so a
// backend that ignores Resize never wedges the pump.
func pumpResize(in *os.File, resize chan<- yoloai.TermSize, stop <-chan struct{}) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)
	for {
		select {
		case <-stop:
			return
		case <-sigCh:
			rows, cols, err := pty.Getsize(in)
			if err != nil {
				continue
			}
			select {
			case resize <- yoloai.TermSize{Rows: rows, Cols: cols}:
			default:
			}
		}
	}
}
