// ABOUTME: The progress contract: records carry fields a bar can be built
// ABOUTME: from, a nil sink panics rather than dropping, and ProgressWriter
// ABOUTME: turns a subprocess's byte stream into one record per line without
// ABOUTME: losing the unterminated last one.

package feedback_test

import (
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/feedback"
)

// progressCollector accumulates progress records for assertions.
type progressCollector struct {
	list []feedback.Progress
}

// Progress appends p.
func (c *progressCollector) Progress(p feedback.Progress) { c.list = append(c.list, p) }

// TestProgressf_NamesTheEventAndRendersTheMessage is the basic emission
// contract, matching Infof/Warnf so a converted site cannot invent a variant.
func TestProgressf_NamesTheEventAndRendersTheMessage(t *testing.T) {
	var got progressCollector
	feedback.Progressf(&got, "image.pulling", "Pulling %s", "macos-tahoe-base")

	if len(got.list) != 1 {
		t.Fatalf("emitted %d records, want 1", len(got.list))
	}
	if got.list[0].Event != "image.pulling" {
		t.Errorf("Event = %q, want %q", got.list[0].Event, "image.pulling")
	}
	if got.list[0].Message != "Pulling macos-tahoe-base" {
		t.Errorf("Message = %q", got.list[0].Message)
	}
}

// TestEmitProgress_CarriesTheFieldsABarNeeds is the reason progress is a record
// at all. A step counter rendered into a sentence can only be recovered by
// parsing English; as fields it is the thing a progress bar reads directly.
func TestEmitProgress_CarriesTheFieldsABarNeeds(t *testing.T) {
	var got progressCollector
	feedback.EmitProgress(&got, feedback.Progress{
		Event:   "vm.provisioning",
		Message: "Provisioning step 3/7...",
		Fields:  map[string]any{"step": 3, "of": 7},
	})

	if len(got.list) != 1 {
		t.Fatalf("emitted %d records, want 1", len(got.list))
	}
	if got.list[0].Fields["step"] != 3 || got.list[0].Fields["of"] != 7 {
		t.Errorf("Fields = %v, want step=3 of=7", got.list[0].Fields)
	}
}

// TestEmitProgress_NilSinkPanicsNamingDiscardProgress mirrors the notice side:
// dropping progress silently would make a caller who forgot a field
// indistinguishable from one who wanted quiet.
func TestEmitProgress_NilSinkPanicsNamingDiscardProgress(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("emitting to a nil ProgressSink did not panic")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "feedback.DiscardProgress") {
			t.Errorf("panic = %v, want a message naming feedback.DiscardProgress", r)
		}
	}()
	feedback.Progressf(nil, "some.event", "dropped")
}

// TestDiscardProgress_AcceptsRecords checks the deliberate-silence path works.
func TestDiscardProgress_AcceptsRecords(t *testing.T) {
	feedback.Progressf(feedback.DiscardProgress, "some.event", "ignored")
}

// TestProgressWriter_OneRecordPerLine is the subprocess seam's contract. A
// child writes bytes in arbitrary chunks; what comes out must be one record
// per line regardless of how the chunks fell.
func TestProgressWriter_OneRecordPerLine(t *testing.T) {
	var got progressCollector
	w := feedback.NewProgressWriter(&got, "build.output")

	for _, chunk := range []string{"#1 resolv", "ing image\n#2 don", "e\n"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	if len(got.list) != 2 {
		t.Fatalf("emitted %d records, want 2: %v", len(got.list), got.list)
	}
	if got.list[0].Message != "#1 resolving image" {
		t.Errorf("record 0 = %q", got.list[0].Message)
	}
	if got.list[1].Message != "#2 done" {
		t.Errorf("record 1 = %q", got.list[1].Message)
	}
	if got.list[0].Event != "build.output" {
		t.Errorf("Event = %q, want the event the writer was built with", got.list[0].Event)
	}
}

// TestProgressWriter_FlushEmitsTheUnterminatedLastLine is the one that earns
// Flush. A subprocess's final line frequently has no trailing newline, and it
// is disproportionately the one that matters — the error text. Without Flush
// it would be the single line silently dropped from every failed build.
func TestProgressWriter_FlushEmitsTheUnterminatedLastLine(t *testing.T) {
	var got progressCollector
	w := feedback.NewProgressWriter(&got, "build.output")

	if _, err := w.Write([]byte("ERROR: no space left on device")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(got.list) != 0 {
		t.Fatalf("an unterminated line must be held, not guessed at; got %v", got.list)
	}

	w.Flush()
	if len(got.list) != 1 || got.list[0].Message != "ERROR: no space left on device" {
		t.Errorf("after Flush: %v", got.list)
	}
}

// TestProgressWriter_FlushIsIdempotent checks a second Flush emits nothing, so
// a defer plus an explicit call cannot double-report.
func TestProgressWriter_FlushIsIdempotent(t *testing.T) {
	var got progressCollector
	w := feedback.NewProgressWriter(&got, "build.output")

	if _, err := w.Write([]byte("tail")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	w.Flush()
	w.Flush()

	if len(got.list) != 1 {
		t.Errorf("emitted %d records, want 1", len(got.list))
	}
}

// TestProgressWriter_SkipsBlankLinesAndStripsCR keeps a stream's spacing from
// becoming a run of empty records, and handles the CRLF a Windows-built tool
// emits — otherwise every message would carry a trailing \r into whatever
// renders it.
func TestProgressWriter_SkipsBlankLinesAndStripsCR(t *testing.T) {
	var got progressCollector
	w := feedback.NewProgressWriter(&got, "build.output")

	if _, err := w.Write([]byte("first\r\n\n   \nsecond\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if len(got.list) != 2 {
		t.Fatalf("emitted %d records, want 2: %v", len(got.list), got.list)
	}
	if got.list[0].Message != "first" {
		t.Errorf("record 0 = %q, want the CR stripped", got.list[0].Message)
	}
	if got.list[1].Message != "second" {
		t.Errorf("record 1 = %q", got.list[1].Message)
	}
}
