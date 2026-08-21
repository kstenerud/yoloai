// ABOUTME: The emission contract: Infof/Warnf produce a named, levelled record;
// ABOUTME: a nil Sink panics with the fix in the message rather than dropping
// ABOUTME: notices; Discard drops deliberately; Collector preserves order and
// ABOUTME: survives concurrent emission; Tee delivers to each sink exactly once.

package feedback_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/kstenerud/yoloai/feedback"
)

// TestInfof_NamesTheEventAndRendersTheMessage checks that the printf helper
// fills every field a consumer switches on. The event ID is the field that
// makes a renderer a lookup instead of a text match, so a helper that lost it
// would leave the message as the only handle — the coupling D145 removes.
func TestInfof_NamesTheEventAndRendersTheMessage(t *testing.T) {
	var got feedback.Collector
	feedback.Infof(&got, "sandbox.resumed", "Sandbox %s resumed", "alpha")

	ns := got.Notices()
	if len(ns) != 1 {
		t.Fatalf("emitted %d notices, want 1", len(ns))
	}
	if ns[0].Event != "sandbox.resumed" {
		t.Errorf("Event = %q, want %q", ns[0].Event, "sandbox.resumed")
	}
	if ns[0].Level != feedback.LevelInfo {
		t.Errorf("Level = %q, want %q", ns[0].Level, feedback.LevelInfo)
	}
	if ns[0].Message != "Sandbox alpha resumed" {
		t.Errorf("Message = %q, want %q", ns[0].Message, "Sandbox alpha resumed")
	}
}

// TestWarnf_SetsWarnLevelAndOmitsThePrefix pins that the level is a field, not
// a string baked into the text. The old writer-based path classified by
// matching a "Warning: " prefix on the message and re-added it when rendering,
// which produced "Warning: Warning: …" whenever the two disagreed (DF157).
func TestWarnf_SetsWarnLevelAndOmitsThePrefix(t *testing.T) {
	var got feedback.Collector
	feedback.Warnf(&got, "ports.unavailable", "port %d is already in use", 8080)

	ns := got.Notices()
	if len(ns) != 1 {
		t.Fatalf("emitted %d notices, want 1", len(ns))
	}
	if ns[0].Level != feedback.LevelWarn {
		t.Errorf("Level = %q, want %q", ns[0].Level, feedback.LevelWarn)
	}
	if strings.Contains(ns[0].Message, "Warning") {
		t.Errorf("Message = %q, want no level prefix — the renderer adds it", ns[0].Message)
	}
}

// TestEmit_NilSinkPanicsNamingDiscard is the reason Discard exists. A nil sink
// is a wiring mistake, and both quiet alternatives are worse: silently
// dropping hides output the caller asked for, and the runtime's own
// nil-interface panic names an address rather than the fix.
func TestEmit_NilSinkPanicsNamingDiscard(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("emitting to a nil Sink did not panic; a dropped notice is invisible")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "feedback.Discard") {
			t.Errorf("panic = %v, want a message naming feedback.Discard as the fix", r)
		}
	}()
	feedback.Infof(nil, "some.event", "dropped")
}

// TestDiscard_AcceptsNoticesWithoutPanicking checks the deliberate-silence
// path is actually usable — Discard is only a defensible answer to the nil
// panic if emitting to it is a no-op rather than another failure.
func TestDiscard_AcceptsNoticesWithoutPanicking(t *testing.T) {
	feedback.Infof(feedback.Discard, "some.event", "ignored")
	feedback.Warnf(feedback.Discard, "some.warning", "also ignored")
}

// TestCollector_PreservesEmissionOrder pins ordering, which the CLI's rendering
// depends on: notices read as a narrative of one operation.
func TestCollector_PreservesEmissionOrder(t *testing.T) {
	var got feedback.Collector
	feedback.Infof(&got, "step.one", "first")
	feedback.Warnf(&got, "step.two", "second")
	feedback.Infof(&got, "step.three", "third")

	var events []string
	for _, n := range got.Notices() {
		events = append(events, n.Event)
	}
	want := []string{"step.one", "step.two", "step.three"}
	if len(events) != len(want) {
		t.Fatalf("collected %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Errorf("notice %d = %q, want %q", i, events[i], want[i])
		}
	}
}

// TestCollector_NoticesReturnsACopy checks a caller can hold the result while
// the collector keeps receiving — the shape a streaming API has, where the
// result is read once the operation returns but emission ran throughout.
func TestCollector_NoticesReturnsACopy(t *testing.T) {
	var got feedback.Collector
	feedback.Infof(&got, "first.event", "one")

	held := got.Notices()
	feedback.Infof(&got, "second.event", "two")

	if len(held) != 1 {
		t.Errorf("held snapshot grew to %d notices; Notices must return a copy", len(held))
	}
	if len(got.Notices()) != 2 {
		t.Errorf("collector holds %d notices, want 2", len(got.Notices()))
	}
}

// TestCollector_SurvivesConcurrentEmission is the reason Collector is guarded.
// Library operations fan out — prune sweeps backends in parallel — and an
// unguarded append there loses or duplicates a line in a way that surfaces
// long after the fact. Meaningful under -race, which make check runs.
func TestCollector_SurvivesConcurrentEmission(t *testing.T) {
	const emitters = 8
	const each = 25

	var got feedback.Collector
	var wg sync.WaitGroup
	for i := range emitters {
		wg.Go(func() {
			for j := range each {
				feedback.Infof(&got, "concurrent.event", "emitter %d notice %d", i, j)
			}
		})
	}
	wg.Wait()

	if n := len(got.Notices()); n != emitters*each {
		t.Errorf("collected %d notices, want %d", n, emitters*each)
	}
}

// TestTee_DeliversToEverySinkExactlyOnce is what lets one emission both stream
// live and land on a result without the emitting site knowing which exposure
// its API chose.
func TestTee_DeliversToEverySinkExactlyOnce(t *testing.T) {
	var live, result feedback.Collector
	feedback.Warnf(feedback.Tee(&live, &result), "shared.event", "seen by both")

	for name, c := range map[string]*feedback.Collector{"live": &live, "result": &result} {
		ns := c.Notices()
		if len(ns) != 1 {
			t.Errorf("%s sink got %d notices, want exactly 1", name, len(ns))
			continue
		}
		if ns[0].Event != "shared.event" {
			t.Errorf("%s sink got event %q, want %q", name, ns[0].Event, "shared.event")
		}
	}
}

// TestTee_PreservesSinkOrder pins that a notice reaches sinks in the order
// given, so a consumer teeing a renderer after a collector sees them agree.
func TestTee_PreservesSinkOrder(t *testing.T) {
	var order []string
	first := feedback.SinkFunc(func(feedback.Notice) { order = append(order, "first") })
	second := feedback.SinkFunc(func(feedback.Notice) { order = append(order, "second") })

	feedback.Infof(feedback.Tee(first, second), "some.event", "message")

	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Errorf("delivery order = %v, want [first second]", order)
	}
}

// TestTee_OfNothingDiscards checks the degenerate case is silence rather than
// a nil Sink — otherwise building a Tee from an empty list of destinations
// would arm the nil-sink panic at the first emission instead of at the wiring.
func TestTee_OfNothingDiscards(t *testing.T) {
	feedback.Infof(feedback.Tee(), "some.event", "nowhere to go")
}

// TestTee_RejectsANilMember checks a nil slipped into a fan-out is caught at
// emission rather than skipped, so one missing destination cannot be mistaken
// for a working Tee.
func TestTee_RejectsANilMember(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a nil member of a Tee was silently skipped")
		}
	}()
	var present feedback.Collector
	feedback.Infof(feedback.Tee(&present, nil), "some.event", "message")
}
