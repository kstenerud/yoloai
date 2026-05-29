// ABOUTME: Type and constant aliases that re-export the create/ leaf's public
// ABOUTME: symbols into package sandbox so external callers and staying façade
// ABOUTME: files (engine.go, setup.go, lifecycle.go, inspect.go) keep compiling
// ABOUTME: without change after the create/ carve (F5.2d).
package sandbox

import (
	"github.com/kstenerud/yoloai/internal/sandbox/create"
	"github.com/kstenerud/yoloai/internal/sandbox/state"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// CreateOptions re-exports create.Options so external callers that reference
// sandbox.CreateOptions continue to compile without change.
type CreateOptions = create.Options

// NetworkMode re-exports create.NetworkMode so external callers that reference
// sandbox.NetworkMode continue to compile without change.
type NetworkMode = create.NetworkMode

// Network access policy constants, aliased from the create leaf.
const (
	NetworkModeDefault  NetworkMode = create.NetworkModeDefault  // full network access
	NetworkModeNone     NetworkMode = create.NetworkModeNone     // no network access
	NetworkModeIsolated NetworkMode = create.NetworkModeIsolated // allowlist only
)

// State re-exports state.State so staying façade files (engine.go, setup.go,
// lifecycle.go) that previously got the alias from create.go keep compiling.
type State = state.State

// DirSpec re-exports state.DirSpec for the same reason.
type DirSpec = state.DirSpec

// DirMode re-exports store.DirMode for the same reason.
type DirMode = store.DirMode

// Re-exported DirMode constants. Canonical definitions in
// internal/sandbox/store/dirmode.go.
const (
	DirModeCopy    = store.DirModeCopy
	DirModeOverlay = store.DirModeOverlay
	DirModeRW      = store.DirModeRW
	DirModeRO      = store.DirModeRO
)
