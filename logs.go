// ABOUTME: SystemClient.Logs — subscribable activity stream of a sandbox's
// ABOUTME: structured JSONL log frames, time-ordered, with optional follow.
package yoloai

import (
	"context"
	"time"

	"github.com/kstenerud/yoloai/internal/sandbox"
)

// LogEvent is one structured-log line surfaced by Logs: the verbatim JSONL byte
// slice (Raw) plus the two fields the library parsed to order and filter the
// stream (Time, Level). Raw is the canonical payload — yoloAI does not decompose
// it into event/message/field parts; a consumer that wants a richer view parses
// Raw itself (the line is JSON), keeping the wire representation unchanged until
// the consumer decides it must change.
type LogEvent struct {
	// Source is which JSONL stream the line came from.
	Source LogSource
	// Time is the frame's "ts", parsed for ordering/filtering. Falls back to
	// the read time when the line carries no parseable timestamp.
	Time time.Time
	// Level is the frame's "level" string, as written.
	Level string
	// Raw is the original JSONL line, verbatim (no trailing newline).
	Raw []byte
}

// LogOptions selects and filters the LogEvents that Logs emits.
type LogOptions struct {
	// Sources limits the streamed sources; empty (nil) means all of them.
	Sources []LogSource
	// MinLevel drops events below this level ("debug" < "info" < "warn" <
	// "error"). Empty means no level filter. An unknown value returns a
	// *UsageError from Logs.
	MinLevel string
	// Since drops events older than this instant. Zero means no time filter.
	Since time.Time
	// Follow keeps the stream open after the backlog, delivering new lines as
	// they are written until the agent reaches a terminal state or ctx is
	// cancelled.
	Follow bool
}

// Logs streams a sandbox's structured-log events in time order. The on-disk
// backlog is delivered first (merged across the requested sources); with
// Follow the channel then stays open, tailing each source until the agent
// reaches a terminal state or ctx is cancelled. The channel is closed when the
// stream ends, so a plain range over it terminates cleanly.
//
// This is a host-filesystem read: no backend connection is required, matching
// AgentLog and LogPaths. Cancel ctx to stop a Follow stream early. A missing
// sandbox returns ErrSandboxNotFound; an invalid MinLevel returns a *UsageError.
func (s *SystemClient) Logs(ctx context.Context, name string, opts LogOptions) (<-chan LogEvent, error) {
	frames, err := sandbox.StreamLogs(ctx, s.layout, name, sandbox.LogStreamOptions{
		Sources:  opts.Sources,
		MinLevel: opts.MinLevel,
		Since:    opts.Since,
		Follow:   opts.Follow,
	})
	if err != nil {
		return nil, err
	}

	out := make(chan LogEvent, 64)
	go func() {
		defer close(out)
		for f := range frames {
			select {
			case out <- LogEvent{Source: f.Source, Time: f.Time, Level: f.Level, Raw: f.Raw}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
