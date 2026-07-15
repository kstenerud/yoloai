// ABOUTME: LogWriter adapts *testing.T into an io.Writer, so a helper's progress
// ABOUTME: output lands in the test log instead of being discarded.
package testutil

import (
	"bytes"
	"io"
	"testing"
)

// LogWriter returns an io.Writer that forwards each line to t.Log.
//
// Prefer it over io.Discard for any long-running setup that reports progress.
// `go test` prints the log of a failed or verbose test, so a step that stalls
// explains itself; discarding the same output turns a slow step into an
// indistinguishable hang. The Tart suite is the cautionary case: EnsureSetup
// announces "This is a one-time download (~30 GB)" before pulling the base
// image, that went to io.Discard, and the resulting 10-minute test timeout read
// as a wedged VM for far longer than it should have (DF19).
//
// Writes are line-buffered because t.Log stamps each call with its own
// file:line prefix, so forwarding raw chunks would shred a multi-line message.
// A trailing unterminated fragment is flushed at test end.
func LogWriter(t *testing.T) io.Writer {
	t.Helper()
	w := &logWriter{logf: t.Log}
	t.Cleanup(w.flush)
	return w
}

// logWriter holds logf rather than a *testing.T so its buffering is testable
// without asserting on a real test's output.
type logWriter struct {
	logf func(args ...any)
	buf  bytes.Buffer
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	// Drain whole lines; anything after the last newline stays buffered for the
	// next Write or the end-of-test flush.
	for {
		i := bytes.IndexByte(w.buf.Bytes(), '\n')
		if i < 0 {
			return len(p), nil
		}
		w.logf(string(w.buf.Next(i)))
		w.buf.Next(1) // discard the newline itself
	}
}

// flush emits any unterminated trailing fragment. Draining the buffer keeps it
// idempotent — it runs from t.Cleanup, and a fragment logged twice would be as
// misleading as one dropped.
func (w *logWriter) flush() {
	if rest := w.buf.String(); rest != "" {
		w.buf.Reset()
		w.logf(rest)
	}
}
