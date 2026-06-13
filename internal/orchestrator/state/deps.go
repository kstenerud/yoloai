// ABOUTME: Deps bundles the cross-cutting dependencies (runtime backend + path
// ABOUTME: layout) that the launch/lifecycle free functions need, so callers in
// ABOUTME: create/ and lifecycle/ can build one handle instead of threading args.
package state

import (
	"io"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
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
}
