// ABOUTME: WriterSink renders notices onto a single byte stream in the form
// ABOUTME: yoloAI has always written them — one line each, warnings prefixed.
// ABOUTME: It is what lets library code emit records while a caller that only
// ABOUTME: gave us an io.Writer keeps receiving exactly the bytes it did before.

package feedback

import (
	"fmt"
	"io"
)

// WarningPrefix is the conventional marker for a warning line on a byte
// stream. It is here, rather than at the emission sites, because it is a
// *rendering* decision: a consumer that receives records decides for itself
// what a warning looks like, and several already do — the CLI sends warnings
// to stderr and drops info lines under --json.
//
// It survives as a constant only because one stream-shaped consumer remains
// (the public Output writers). Emitting it by hand into a message is the
// defect it used to be: the level was recovered by matching this prefix on the
// text, and a site that also rendered it produced "Warning: Warning: …"
// (DF157). Emit a level; let a renderer add the prefix.
const WarningPrefix = "Warning: "

// WriterSink returns a Sink that renders each notice as one line on w:
// warnings carry WarningPrefix, informational notices do not.
//
// This is the adapter for a caller who handed us an io.Writer and expects
// text. It renders and discards — the event ID and fields do not survive it —
// which is precisely the loss that motivated records, so it is the fallback
// for the byte-stream contract rather than the shape to build new consumers
// on. A consumer that wants the record implements Sink.
//
// Write errors are dropped, as they are at every site this replaces: feedback
// is advisory, and failing an operation because its progress line could not be
// printed would be worse than losing the line.
//
// A nil w yields Discard rather than a panic — unlike a nil Sink. The
// difference is that the writer it adapts is a documented-optional public
// field ("Default: io.Discard" on ClientOptions.Output), so leaving it unset
// is a caller's stated choice and not a wiring mistake.
func WriterSink(w io.Writer) Sink {
	if w == nil {
		return Discard
	}
	return SinkFunc(func(n Notice) {
		prefix := ""
		if n.Level == LevelWarn {
			prefix = WarningPrefix
		}
		fmt.Fprintf(w, "%s%s\n", prefix, n.Message) //nolint:errcheck // best-effort: advisory output
	})
}
