// ABOUTME: Tests for noticeWriter, the adapter that turns the launch path's
// ABOUTME: progress stream into structured Notices on the restart path (DF157).

package lifecycle

import (
	"fmt"
	"testing"

	"github.com/kstenerud/yoloai/feedback"
	"github.com/kstenerud/yoloai/internal/orchestrator/launch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoticeWriter_ReadsLevelFromEachLine pins DF157. state.State.Output is one
// stream carrying two kinds of line — a port warning from filterAvailablePorts
// and a streamed image build from ensureImageLineage — so a writer configured
// with a single level necessarily mislabels one of them. It mislabelled the
// larger: every line of BuildKit progress arrived as a NoticeWarn, and
// RenderNotices sends those to stderr and does not suppress them under --json.
//
// The level therefore has to be read per line, off the prefix the create path
// already uses to say the same thing to a terminal.
func TestNoticeWriter_ReadsLevelFromEachLine(t *testing.T) {
	// Verbatim shapes from the two real producers, so a change to either that
	// drops or renames the prefix convention shows up here.
	const buildLine = "#35 exporting layers 75.9s done"
	const portWarning = launch.WarningPrefix + "skipping port 8080:80 — host port 8080 is already in use"

	n := &notices{}
	w := &noticeWriter{notices: n}
	_, err := fmt.Fprintf(w, "%s\n%s\n", buildLine, portWarning)
	require.NoError(t, err)
	require.Len(t, n.list, 2)

	assert.Equal(t, NoticeInfo, n.list[0].Level,
		"build progress is not a warning: as NoticeWarn it goes to stderr and survives --json")
	assert.Equal(t, buildLine, n.list[0].Message)

	assert.Equal(t, NoticeWarn, n.list[1].Level,
		"a line that says it is a warning must stay one")
	assert.Equal(t, "skipping port 8080:80 — host port 8080 is already in use", n.list[1].Message,
		"the prefix must be stripped: RenderNotices re-adds it for a NoticeWarn, so keeping it renders 'Warning: Warning: …'")
}

// TestNoticeWriter_ClassifiesAcrossSplitWrites covers the buffering seam. A
// build streams in arbitrary chunks, so the prefix can arrive split from the
// rest of its line; classification happens when the line completes, not when
// the bytes land.
func TestNoticeWriter_ClassifiesAcrossSplitWrites(t *testing.T) {
	n := &notices{}
	w := &noticeWriter{notices: n}

	for _, chunk := range []string{"Warn", "ing: ", "half a line", " and the rest\nprogress", " line\n"} {
		_, err := w.Write([]byte(chunk))
		require.NoError(t, err)
	}

	require.Len(t, n.list, 2)
	assert.Equal(t, NoticeWarn, n.list[0].Level)
	assert.Equal(t, "half a line and the rest", n.list[0].Message)
	assert.Equal(t, NoticeInfo, n.list[1].Level)
	assert.Equal(t, "progress line", n.list[1].Message)
}

// TestNoticeWriter_RoundTripsWhatWriterSinkRenders is the safety net for the
// D145 conversion. The create path and the restart path share every helper but
// give it a different destination: create hands over a terminal, restart hands
// over a noticeWriter. Converting a helper to emit records is only safe if the
// two destinations still agree about what it said — so a notice rendered to
// bytes by feedback.WriterSink and read back by noticeWriter must come out the
// same notice.
//
// Without this, a converted site could silently change level or wording on one
// path only, and the paths are exercised by different commands.
func TestNoticeWriter_RoundTripsWhatWriterSinkRenders(t *testing.T) {
	emitted := []Notice{
		{Event: "image.building", Level: NoticeInfo, Message: "#35 exporting layers 75.9s done"},
		{Event: "ports.unavailable", Level: NoticeWarn, Message: "skipping port 8080:80 — host port 8080 is already in use"},
	}

	n := &notices{}
	w := &noticeWriter{notices: n}
	rendered := feedback.WriterSink(w)
	for _, want := range emitted {
		rendered.Notice(want)
	}

	require.Len(t, n.list, len(emitted))
	for i, want := range emitted {
		assert.Equal(t, want.Level, n.list[i].Level,
			"level survived rendering to bytes and back for %q", want.Event)
		assert.Equal(t, want.Message, n.list[i].Message,
			"message survived rendering to bytes and back for %q", want.Event)
	}
}
