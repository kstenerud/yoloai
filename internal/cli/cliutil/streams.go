// ABOUTME: IOStreams() — the calling process's stdin/stdout/stderr bound to a
// ABOUTME: yoloai.IOStreams. Used by Attach and any other CLI surface that
// ABOUTME: pipes user-terminal I/O into the library.

package cliutil

import (
	"os"

	"github.com/creack/pty"
	yoloai "github.com/kstenerud/yoloai"
)

// IOStreams returns a yoloai.IOStreams bound to the calling process's
// terminal, sized from os.Stdin's PTY. Used by every CLI command that
// invokes Client.Attach (or other interactive surfaces).
func IOStreams() yoloai.IOStreams {
	rows, cols, _ := pty.Getsize(os.Stdin)
	return yoloai.IOStreams{
		In:   os.Stdin,
		Out:  os.Stdout,
		Err:  os.Stderr,
		TTY:  true,
		Rows: rows,
		Cols: cols,
	}
}
