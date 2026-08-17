// ABOUTME: Deps bundles the cross-cutting dependencies (runtime backend + path
// ABOUTME: layout) that the launch/lifecycle free functions need, so callers in
// ABOUTME: create/ and lifecycle/ can build one handle instead of threading args.
package state

import (
	"io"
	"log/slog"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
)

// Deps holds the runtime backend, path layout, and interactive input reader
// shared by the sandbox launch and create free functions. It is constructed by
// the Engine (and other callers) and passed by value. Input carries the
// interactive input reader used by create (prompt reading via invocation.ReadPrompt)
// and lifecycle (start prompt).
type Deps struct {
	Runtime runtime.Backend
	Layout  config.Layout
	Input   io.Reader
	// Logger is where diagnostics for this call go. It carries the caller's
	// declared destination down to the leaves, which is the whole reason it is
	// here: four sites used to fabricate slog.Default() one frame before
	// calling a function that takes a logger, silently discarding what the
	// caller asked for. A destination is stated, never guessed (D145).
	//
	// Nil is a wiring mistake, not "use the default" — use LoggerOr at the few
	// sites that build a partial Deps for a narrow purpose.
	Logger *slog.Logger
}

// LoggerOr returns d.Logger, or a logger that discards everything.
//
// The fallback is silence rather than slog.Default() on purpose: a library
// that reaches for the process-global handler when nothing was set publishes
// wherever the runtime happens to point, which is the undeclared destination
// D145 forbids. Silence is wrong visibly and only for the caller who forgot;
// the alternative is wrong invisibly and for everyone downstream.
func (d Deps) LoggerOr() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.New(slog.DiscardHandler)
}
