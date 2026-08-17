// ABOUTME: Sink — where a Notice goes. A destination is always stated: nil is a
// ABOUTME: wiring mistake and Discard is the way to ask for silence. Collector
// ABOUTME: accumulates notices for a result; Tee fans one emission out to
// ABOUTME: several destinations, each receiving it exactly once.

package feedback

import "sync"

// Sink is a destination for notices.
//
// It is an interface taking one value, not a channel, because a channel makes
// the caller responsible for a lifecycle they did not ask for: something must
// drain it for the whole call, and an unbuffered channel nobody reads
// deadlocks the library mid-operation. Bridging a Sink to a channel is one
// line — SinkFunc(func(n Notice) { ch <- n }) — while bridging the other way
// forces a goroutine and a close protocol on every consumer, including the
// ones that just want to print. Whether notices are buffered, batched, or
// given job IDs is the consumer's decision, not something to fix at this
// boundary (D145).
type Sink interface {
	// Notice delivers one notice. Implementations must not block
	// indefinitely: library code emits while holding the operation open.
	Notice(n Notice)
}

// SinkFunc adapts a plain function to Sink.
type SinkFunc func(Notice)

// Notice calls f.
func (f SinkFunc) Notice(n Notice) { f(n) }

// Discard is the sink that drops everything. It exists so that wanting no
// notices is something a caller says, rather than something a zero-valued
// field does silently — see emit.
var Discard Sink = discardSink{}

// discardSink implements Sink by doing nothing.
type discardSink struct{}

// Notice drops n.
func (discardSink) Notice(Notice) {}

// Collector accumulates notices so a method can return them on its result.
// The zero value is ready to use.
//
// It is safe for concurrent use because library operations fan out — pruning
// sweeps backends in parallel, and a per-backend goroutine emitting into a
// shared collector is the natural shape. An unguarded slice append there is a
// data race that surfaces as a lost or duplicated line long after the fact.
type Collector struct {
	mu   sync.Mutex
	list []Notice
}

// Notice appends n.
func (c *Collector) Notice(n Notice) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.list = append(c.list, n)
}

// Notices returns the notices collected so far, in emission order. The result
// is a copy, so a caller may hold it while the collector keeps receiving.
func (c *Collector) Notices() []Notice {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.list) == 0 {
		return nil
	}
	out := make([]Notice, len(c.list))
	copy(out, c.list)
	return out
}

// Tee returns a Sink delivering each notice to every sink given, in order.
//
// This is what lets one emission both stream live and land on a result: the
// same notice reaches the caller's handler and the collector, once each,
// rather than the emitting site having to know which exposure its API chose.
// Tee of nothing is Discard; Tee of one is that sink.
func Tee(sinks ...Sink) Sink {
	switch len(sinks) {
	case 0:
		return Discard
	case 1:
		return sinks[0]
	}
	fanout := make([]Sink, len(sinks))
	copy(fanout, sinks)
	return SinkFunc(func(n Notice) {
		for _, s := range fanout {
			emit(s, n)
		}
	})
}
