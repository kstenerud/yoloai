// ABOUTME: The CLI's rendering point — the one layer that turns the library's
// ABOUTME: feedback records into text and decides where each kind goes. Every
// ABOUTME: destination decision the library used to bake into a formatted
// ABOUTME: string is made here instead, once (D145).

package cliutil

import (
	"fmt"
	"io"

	"github.com/kstenerud/yoloai/feedback"
	"github.com/spf13/cobra"
)

// renderer implements both feedback.Sink and feedback.ProgressSink, turning
// records into lines on a command's streams.
//
// One type for both, deliberately: the library's two narrow interfaces let each
// function declare what it emits, while a consumer that renders both wants a
// single thing to construct and hand over. That asymmetry is the point of
// keeping the interfaces separate rather than bundling them into a struct.
type renderer struct {
	out      io.Writer
	err      io.Writer
	jsonMode bool
}

// Feedback returns the sinks the CLI hands to the library for cmd.
//
// Routing, and why each choice: warnings go to stderr always, because they must
// survive `--json` without corrupting the document on stdout. Informational
// notices go to stdout in human mode and are suppressed under `--json` for the
// same reason. Progress goes to stderr always — it is transient, it is not part
// of any answer, and a caller piping stdout wants the answer, not the build log.
//
// This mirrors RenderNotices, which renders the notices that arrive on a
// *result* rather than on a stream. Two entry points, one policy; if they ever
// disagree the same notice would render differently depending on which API
// surface produced it.
func Feedback(cmd *cobra.Command) (feedback.Sink, feedback.ProgressSink) {
	r := &renderer{out: cmd.OutOrStdout(), err: cmd.ErrOrStderr(), jsonMode: JSONEnabled(cmd)}
	return r, r
}

// Notice renders one advisory.
func (r *renderer) Notice(n feedback.Notice) {
	switch n.Level {
	case feedback.LevelWarn:
		fmt.Fprintf(r.err, "%s%s\n", feedback.WarningPrefix, n.Message) //nolint:errcheck // best-effort
	case feedback.LevelInfo:
		if !r.jsonMode {
			fmt.Fprintln(r.out, n.Message) //nolint:errcheck // best-effort
		}
	}
}

// Progress renders one progress record.
//
// Unprefixed and on stderr: this is the build output and step counters a user
// watches while waiting, and dressing each line as a notice would bury the
// notices among them. Suppressed under --json — progress has no place in a
// document, and a consumer that wants it structured should not be using the
// CLI's rendering at all.
func (r *renderer) Progress(p feedback.Progress) {
	if r.jsonMode {
		return
	}
	fmt.Fprintln(r.err, p.Message) //nolint:errcheck // best-effort
}
