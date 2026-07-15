// ABOUTME: Tests for LogWriter's line buffering — the part with real logic, since a
// ABOUTME: dropped or shredded line defeats the point of not discarding the output.
package testutil

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// captureT collects what a logWriter would hand to t.Log. It stands in for the
// *testing.T so the assertions can inspect the lines rather than the test's own
// output.
type captureT struct{ lines []string }

func (c *captureT) Log(args ...any) { c.lines = append(c.lines, fmt.Sprint(args...)) }

// newCapturing builds a logWriter that logs into c. It mirrors LogWriter minus
// the t.Cleanup registration, which the tests drive explicitly via flush.
func newCapturing(c *captureT) *logWriter { return &logWriter{logf: c.Log} }

func TestLogWriterSplitsOnNewlines(t *testing.T) {
	c := &captureT{}
	w := newCapturing(c)

	n, err := fmt.Fprint(w, "first\nsecond\n")
	assert.NoError(t, err)
	assert.Equal(t, len("first\nsecond\n"), n, "Write must report every byte consumed")
	assert.Equal(t, []string{"first", "second"}, c.lines)
}

// A pull's progress output arrives in arbitrary chunks, not whole lines — the
// reason this writer buffers at all.
func TestLogWriterJoinsLineSplitAcrossWrites(t *testing.T) {
	c := &captureT{}
	w := newCapturing(c)

	fmt.Fprint(w, "one-")   //nolint:errcheck // in-memory writer never fails
	fmt.Fprint(w, "line\n") //nolint:errcheck // in-memory writer never fails

	assert.Equal(t, []string{"one-line"}, c.lines, "a line split across writes must log once, whole")
}

func TestLogWriterHoldsFragmentUntilFlush(t *testing.T) {
	c := &captureT{}
	w := newCapturing(c)

	fmt.Fprint(w, "no trailing newline") //nolint:errcheck // in-memory writer never fails
	assert.Empty(t, c.lines, "an unterminated fragment must not log early")

	// The end-of-test flush is what keeps a final unterminated line — e.g. a
	// progress bar, or the last thing printed before a hang — from being lost.
	w.flush()
	assert.Equal(t, []string{"no trailing newline"}, c.lines)
	c.lines = nil
	w.flush()
	assert.Empty(t, c.lines, "flush must not re-log an already-flushed fragment")
}

func TestLogWriterFlushIsNoOpWhenDrained(t *testing.T) {
	c := &captureT{}
	w := newCapturing(c)

	fmt.Fprint(w, "done\n") //nolint:errcheck // in-memory writer never fails
	w.flush()

	assert.Equal(t, []string{"done"}, c.lines, "flush must not emit a spurious empty line")
}
