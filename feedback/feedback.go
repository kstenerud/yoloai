// ABOUTME: Notice — the one record type for advisory feedback addressed to the
// ABOUTME: caller of an API, carrying a semantic event ID, a chosen level, a
// ABOUTME: rendered message and optional fields. Emission is uniform; how a
// ABOUTME: notice is exposed is each API surface's own contract (D145).

// Package feedback carries advisory records from library code to whoever
// called it — the CLI, the MCP server, an embedder, a future daemon.
//
// The distinction that decides whether something belongs here is *who is
// addressed*. A message for the caller of an API is feedback: it is per-call
// and per-principal, so it must be threaded or returned, because a process
// global cannot say which caller a line belongs to and a multi-principal
// daemon would merge them. A message for the operator of the process is a
// diagnostic, and diagnostics are process-scoped by nature — they stay on the
// logger, whose handler the entrypoint installs. The rule is "no undeclared
// destination", not "no globals" (D145).
//
// What this package deliberately does not have:
//
//   - No universal record type. Results are per-API and typed; the only thing
//     common across APIs is the advisory, which is what Notice is. A record
//     shape wide enough to be every API's return value would be a shape no
//     API's caller can use without a type switch.
//   - No error level. Errors are returned, not emitted. A failure that a
//     caller must handle is not something to be found by reading a stream.
//   - No progress. Progress is only meaningful live and is not worth
//     accumulating into a result, so it stays a byte stream on io.Writer.
//
// This package is stdlib-only and depends on nothing else in yoloAI, so the
// bottom layers (runtime/, store/, copyflow/) can emit without importing
// anything above them.
package feedback

import "fmt"

// Level classifies a Notice for rendering. There are deliberately two: a
// notice the user should heed, and one they may ignore. A third would need a
// consumer that treats it differently, and none exists.
type Level string

const (
	// LevelInfo is an informational status message ("Sandbox X resumed").
	LevelInfo Level = "info"
	// LevelWarn is a warning the user should heed ("could not fully remove …").
	LevelWarn Level = "warn"
)

// Notice is a single advisory record addressed to the caller of an API.
//
// Event names what happened; it is the field that makes a renderer a lookup
// rather than a match on message text. Message is the already-rendered human
// form, so a consumer that only wants to print something never has to know the
// event vocabulary. Fields carry the values the message interpolated, for a
// consumer that wants them separately — a JSON log, a structured API response.
//
// Message is what the CLI prints today and is not optional. Fields are.
type Notice struct {
	// Event is the semantic event ID: dotted, lowercase, and self-describing —
	// "ports.unavailable", not "warn3" or "createPipeline". It names what
	// happened, not which function emitted it: two sites reporting the same
	// occurrence share an ID, and one site reporting two occurrences uses two.
	//
	// The form is "<subject>.<what_happened>", underscores within a segment.
	// The subject is the thing the notice is about — "devcontainer", "ports",
	// "profile" — not the package it was emitted from, because code moves and
	// an event should not move with it.
	//
	// The name is what a consumer switches on, so it is part of the contract
	// with that consumer even though this field is not visible in rendered
	// output. Renaming one is a behaviour change for any non-CLI consumer.
	Event string

	// Level is the notice's severity, chosen at the emission site.
	Level Level

	// Message is the rendered human-readable text, without a level prefix —
	// the renderer adds "Warning: " or its equivalent, because what a level
	// looks like is the consumer's decision.
	Message string

	// Fields carries the structured values behind Message, or nil. Keys are
	// scoped to the Event, not global.
	Fields map[string]any
}

// Emit sends a fully-formed notice. It is the general form; Infof and Warnf
// are the conveniences for the common case of a message with no fields.
//
// Prefer it over calling s.Notice directly, so that every emission in the
// codebase goes through the same nil check and a site carrying fields is not
// the one place that skips it.
func Emit(s Sink, n Notice) {
	emit(s, n)
}

// Infof emits an informational notice with a printf-formatted message.
//
// The event ID is a required argument rather than an optional field because an
// unnamed notice is one a non-CLI consumer can only match by its text, which
// is the coupling this package exists to remove.
func Infof(s Sink, event, format string, args ...any) {
	emit(s, Notice{Event: event, Level: LevelInfo, Message: fmt.Sprintf(format, args...)})
}

// Warnf emits a warning notice with a printf-formatted message.
func Warnf(s Sink, event, format string, args ...any) {
	emit(s, Notice{Event: event, Level: LevelWarn, Message: fmt.Sprintf(format, args...)})
}

// emit delivers a notice, rejecting a nil Sink loudly.
//
// A nil Sink is a wiring mistake, and the two ways to absorb one are both
// worse than a panic: dropping the notice silently hides output the caller
// asked for, and the runtime's own nil-interface panic names a memory address
// rather than the fix. Discarding notices is a legitimate thing to want, so it
// has a name — Discard — and stating it is the difference between a caller who
// chose silence and one who forgot a field.
func emit(s Sink, n Notice) {
	if s == nil {
		panic("feedback: nil Sink; pass feedback.Discard to drop notices deliberately")
	}
	s.Notice(n)
}
