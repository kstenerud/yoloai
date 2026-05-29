// ABOUTME: Deps bundles the cross-cutting dependencies (runtime backend + path
// ABOUTME: layout) that the launch/lifecycle free functions need, so callers in
// ABOUTME: create/ and lifecycle/ can build one handle instead of threading args.
package state

import (
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
)

// Deps holds the runtime backend and path layout shared by the sandbox
// launch free functions. It is constructed by the Engine (and other callers)
// and passed by value. New fields may be added as later carve phases dissolve
// more Engine methods.
type Deps struct {
	Runtime runtime.Runtime
	Layout  config.Layout
}
