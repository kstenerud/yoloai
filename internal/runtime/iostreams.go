// ABOUTME: IOStreams bundles caller-provided stdio for interactive runtime
// ABOUTME: methods (currently InteractiveExec). Modeled on kubectl.

package runtime

import "io"

// IOStreams names the stdio handles for interactive runtime methods. The
// caller controls where input comes from and where output / error go,
// rather than the backend hard-coding os.Stdin / os.Stdout / os.Stderr.
//
// The original `runtime.Runtime.InteractiveExec` signature took no IO
// parameters and reached for the calling process's stdio. That worked for
// the CLI (where the process stdio IS the user's terminal) but broke any
// non-CLI embedder — HTTP servers, MCP bridges, test harnesses bridging
// virtual terminals.
//
// **TTY semantics.** When TTY=true, In and Out must each be a terminal
// (typically *os.File whose underlying FD is a PTY). Backends that
// allocate a remote PTY (docker exec -t, containerd FIFO with
// terminal=true) require this for the PTY bridge to work end-to-end.
// When TTY=false the backend treats the streams as plain pipes; no
// PTY is allocated on the remote side.
//
// **Sizing.** Rows / Cols, when non-zero, are the terminal dimensions
// the caller wants the remote PTY to start at. A backend that supports
// resize can set the initial geometry without round-tripping through an
// ioctl on In's FD. Zero means "let the backend detect from In's FD or
// pick a default."
type IOStreams struct {
	In  io.Reader // stdin (must be a terminal when TTY=true)
	Out io.Writer // stdout
	Err io.Writer // stderr

	// TTY signals the streams are a terminal. Backends allocate a
	// remote PTY (docker -t, containerd terminal=true) when set.
	TTY bool

	// Rows and Cols are the initial PTY geometry when TTY=true. Zero
	// means "detect from In's FD if possible, else backend default."
	Rows, Cols int

	// Term is the terminal type ($TERM) the interactive exec should
	// advertise to ncurses/tmux inside the instance. The library never
	// reads the process's own $TERM (§12: that would be the embedding
	// daemon's terminal, not the principal's) — the caller supplies it.
	// Empty means "xterm-256color", a safe modern default.
	Term string
}
