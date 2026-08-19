// ABOUTME: Progress — what an operation is doing right now. A record, not a
// ABOUTME: line of text, because progress carries the most structure of
// ABOUTME: anything the library emits (step 3 of 7, ~30 GB, ios 18.2) and
// ABOUTME: formatting it is what makes a progress bar impossible to build.

package feedback

import (
	"bytes"
	"fmt"
	"strings"
)

// Progress reports what a long-running operation is doing at this moment.
//
// It is a separate type from Notice, not a Notice with a third level, for two
// reasons. Progress has no severity — asking whether "Booting VM" is a warning
// is a category error — and making that a fact the compiler holds beats a
// comment saying to leave the field alone. And consumers route the two
// differently: a notice is a fact about the call and rides home on its result,
// while progress is only meaningful live and is never accumulated.
//
// It carries fields for the same reason Notice does, and more urgently. The
// values interpolated into a progress line are exactly what a consumer that is
// not a terminal wants: a step counter to drive a bar, a byte total to
// estimate, a platform and version to label. Rendering them into a sentence
// and handing over the sentence is what forces every such consumer to parse
// English back into the numbers we already had.
type Progress struct {
	// Event is the semantic event ID, under the same rules as Notice.Event:
	// "<subject>.<what_happened>", dotted, lowercase, naming what is happening
	// rather than which function said so.
	Event string

	// Message is the rendered human-readable line.
	Message string

	// Fields carries the structured values behind Message, or nil.
	Fields map[string]any
}

// ProgressSink is a destination for progress records.
//
// It is deliberately separate from Sink rather than a second method on it.
// Most functions emit one kind or the other — prune's helpers only ever have
// advisories, tart's build path only ever has progress — and a single fat
// interface would make every one of them accept a contract wider than it uses.
// Go's structural interfaces mean a caller still writes *one* type that
// satisfies both and passes it to either, so the split costs the caller
// nothing and tells the reader of any signature exactly what that function
// emits.
type ProgressSink interface {
	// Progress delivers one record. Implementations must not block
	// indefinitely: this is called from inside the operation being reported.
	Progress(p Progress)
}

// ProgressSinkFunc adapts a plain function to ProgressSink.
type ProgressSinkFunc func(Progress)

// Progress calls f.
func (f ProgressSinkFunc) Progress(p Progress) { f(p) }

// DiscardProgress is the sink that drops every progress record. Like Discard,
// it exists so that wanting silence is something a caller states rather than
// something a zero-valued field causes.
var DiscardProgress ProgressSink = discardProgress{}

// discardProgress implements ProgressSink by doing nothing.
type discardProgress struct{}

// Progress drops p.
func (discardProgress) Progress(Progress) {}

// EmitProgress sends a fully-formed progress record. It is the general form;
// Progressf is the convenience for a message with no fields.
func EmitProgress(s ProgressSink, p Progress) {
	emitProgress(s, p)
}

// Progressf emits a progress record with a printf-formatted message.
func Progressf(s ProgressSink, event, format string, args ...any) {
	emitProgress(s, Progress{Event: event, Message: fmt.Sprintf(format, args...)})
}

// emitProgress delivers a record, rejecting a nil sink loudly — same reasoning
// as emit: silence is legitimate and therefore has a name.
func emitProgress(s ProgressSink, p Progress) {
	if s == nil {
		panic("feedback: nil ProgressSink; pass feedback.DiscardProgress to drop progress deliberately")
	}
	s.Progress(p)
}

// ProgressWriter adapts a ProgressSink onto io.Writer: each newline-terminated
// line written becomes one Progress record carrying the line verbatim.
//
// This is the seam where a subprocess's output enters the record world.
// exec.Cmd.Stdout is an io.Writer and cannot be anything else, so a backend
// streaming `docker build` or `tart pull` writes here rather than at a public
// writer the caller had to supply. The line is passed through unparsed: it is
// the child's text, and inventing structure for it would be guessing.
//
// Splitting costs little in practice: os/exec hands the child a pipe whenever
// the writer is not an *os.File — which it is not here — so most tools already
// emit line-oriented output rather than redrawing. The two shapes that do
// redraw are handled rather than lost: a \r-updating tool is split on \r too
// (see Write), and the three sites that used to pass an *os.File straight
// through to a TTY are named in the D145 amendment, where flattening them was
// weighed against keeping a public writer and judged the better trade.
type ProgressWriter struct {
	sink  ProgressSink
	event string
	buf   []byte
}

// NewProgressWriter returns a ProgressWriter emitting to sink under event.
func NewProgressWriter(sink ProgressSink, event string) *ProgressWriter {
	return &ProgressWriter{sink: sink, event: event}
}

// Write splits p on either newline or carriage return and emits each complete,
// non-blank segment. A trailing partial segment is held until the next Write
// completes it, or until Flush.
//
// Splitting on \r as well as \n is what keeps a redrawing tool legible.
// xcodebuild reports download progress as repeated \r-terminated updates with
// no newline until the very end; splitting on \n alone would buffer the entire
// download into one enormous record delivered after it finished. Each update
// becomes its own record instead, which a terminal consumer can render in place
// and a log consumer can sample or drop.
func (w *ProgressWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexAny(w.buf, "\n\r")
		if i < 0 {
			break
		}
		w.emitLine(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

// Flush emits any held partial line. Call it after the subprocess exits: a
// child's last line often has no trailing newline, and without this it would
// be the one line silently dropped — typically the error message.
func (w *ProgressWriter) Flush() {
	if len(w.buf) == 0 {
		return
	}
	line := string(w.buf)
	w.buf = nil
	w.emitLine(line)
}

// emitLine emits one segment, skipping blanks so a stream's spacing — and the
// empty segment a \r\n pair leaves behind — does not become a run of empty
// records.
func (w *ProgressWriter) emitLine(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	emitProgress(w.sink, Progress{Event: w.event, Message: line})
}

// ProgressAsNotices returns a ProgressSink that forwards each record to sink as
// an informational Notice.
//
// It exists for a caller with no live stream to render to. The restart path is
// the case: it returns a result the CLI prints once the call is over, so a
// progress record with nowhere to go live would simply vanish — and the thing
// that vanishes is the several minutes of image-build output that explain why
// `start` took so long. Folding progress into the result keeps it visible.
//
// Info, not warn: DF157 is the record of what happens when build progress is
// filed as a warning — it goes to stderr and survives --json, which is exactly
// backwards for a transient line.
func ProgressAsNotices(sink Sink) ProgressSink {
	return ProgressSinkFunc(func(p Progress) {
		Emit(sink, Notice{
			Event:   p.Event,
			Level:   LevelInfo,
			Message: p.Message,
			Fields:  p.Fields,
		})
	})
}
