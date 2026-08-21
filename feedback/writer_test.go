// ABOUTME: The byte contract WriterSink has to honour: one line per notice,
// ABOUTME: WarningPrefix on warnings and nothing on info. This is the safety
// ABOUTME: net that makes converting the emission sites mechanical — a rendered
// ABOUTME: record must be indistinguishable from the Fprintf it replaces.

package feedback_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/feedback"
)

// TestWriterSink_RendersTheBytesItReplaces pins the exact output, because the
// conversion's whole safety argument is that a caller holding an io.Writer
// cannot tell the difference. A test on "contains the message" would pass
// while a stray prefix or a missing newline changed what the user sees.
func TestWriterSink_RendersTheBytesItReplaces(t *testing.T) {
	var buf bytes.Buffer
	sink := feedback.WriterSink(&buf)

	feedback.Infof(sink, "image.pulling", "Pulling image %s", "alpine")
	feedback.Warnf(sink, "ports.unavailable", "skipping port %d", 8080)

	want := "Pulling image alpine\nWarning: skipping port 8080\n"
	if buf.String() != want {
		t.Errorf("rendered %q, want %q", buf.String(), want)
	}
}

// TestWriterSink_NilWriterDiscards checks the documented-optional public Output
// field stays optional. A nil Sink panics — that is a wiring mistake — but a
// nil writer is a caller's stated choice ("Default: io.Discard"), so adapting
// one must not turn a supported configuration into a crash.
func TestWriterSink_NilWriterDiscards(t *testing.T) {
	feedback.Infof(feedback.WriterSink(nil), "some.event", "goes nowhere")
}

// TestWriterSink_DropsWriteErrors checks that a failing destination cannot fail
// the operation being reported on. Every site this replaces ignores its write
// error for the same reason: losing a progress line is strictly better than
// aborting the work that produced it.
func TestWriterSink_DropsWriteErrors(t *testing.T) {
	feedback.Warnf(feedback.WriterSink(failingWriter{}), "some.event", "unwritable")
}

// TestWriterSink_LosesTheRecord is a documented limitation rather than a bug,
// and pinning it keeps the reason visible: a byte stream is where the event ID
// and fields die, which is the loss that motivated records in the first place.
// A consumer that wants them implements Sink instead of handing over a writer.
func TestWriterSink_LosesTheRecord(t *testing.T) {
	var buf bytes.Buffer
	feedback.Infof(feedback.WriterSink(&buf), "distinctive.event.id", "message")

	if strings.Contains(buf.String(), "distinctive.event.id") {
		t.Errorf("rendered %q; the event ID is not part of the byte contract", buf.String())
	}
}

// failingWriter rejects every write.
type failingWriter struct{}

// Write always fails.
func (failingWriter) Write([]byte) (int, error) {
	return 0, errWriteRefused
}

// errWriteRefused is failingWriter's error.
var errWriteRefused = errRefused("write refused")

// errRefused is a string error, kept local so this package stays stdlib-only.
type errRefused string

// Error returns the message.
func (e errRefused) Error() string { return string(e) }
