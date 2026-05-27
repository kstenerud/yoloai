// ABOUTME: Public typed-name aliases (Q-Y): BackendName, AgentName, PruneItemKind,
// ABOUTME: LogSource. Re-export the internal enum types so embedders importing yoloai
// ABOUTME: don't need to (and cannot) reach into internal/ packages.

package yoloai

import (
	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// BackendName names a runtime backend. Open-set typed string —
// the constants document the backends that ship with yoloai;
// custom builds can register additional names via runtime.Register.
//
// Re-exported (type alias) from internal/runtime so embedders can use
// the type without importing internal packages. Q-Y resolution
// (2026-05-25): public API fields like Options.Backend and
// CheckOptions.Backend take this typed name rather than plain string,
// catching typo-style bugs at the call site rather than at the
// "unknown backend" error.
type BackendName = runtime.BackendName

// Shipped backends. Constants are re-exported as typed-string consts
// so they can be used in case statements and compared cleanly.
//
// New backends registered at runtime via runtime.Register will not
// appear here — that's by design; the constant list documents the
// stable set, not the dynamic registry.
const (
	BackendDocker     BackendName = runtime.BackendDocker
	BackendPodman     BackendName = runtime.BackendPodman
	BackendTart       BackendName = runtime.BackendTart
	BackendSeatbelt   BackendName = runtime.BackendSeatbelt
	BackendContainerd BackendName = runtime.BackendContainerd
)

// AgentName names a coding agent. Open-set typed string — the
// constants document the agents that ship with yoloai; user-defined
// extension agents supply their own name via the agent registry.
//
// Re-exported from internal/agent. Same Q-Y rationale as BackendName:
// public fields like RunOptions.Agent and CheckOptions.Agent take this
// typed name.
type AgentName = agent.AgentName

const (
	AgentClaude   AgentName = agent.AgentClaude
	AgentCodex    AgentName = agent.AgentCodex
	AgentGemini   AgentName = agent.AgentGemini
	AgentOpenCode AgentName = agent.AgentOpenCode
	AgentAider    AgentName = agent.AgentAider
	AgentTest     AgentName = agent.AgentTest // dev/test helper agent
)

// PruneItemKind labels the kind of resource a PruneItem describes
// (container, image, vm, temp_dir, etc.). Open-set typed string so
// backends can introduce new kinds without breaking the type.
//
// Q-Y resolution: PruneItem.Kind moves from plain string to this
// typed enum so embedders can switch over a closed set of cases and
// get a default branch for new kinds.
type PruneItemKind string

const (
	PruneKindContainer PruneItemKind = "container" // docker / podman / containerd
	PruneKindImage     PruneItemKind = "image"     // docker / podman / containerd
	PruneKindVM        PruneItemKind = "vm"        // tart
	PruneKindTempDir   PruneItemKind = "temp_dir"  // yoloai-side: stale ~/.yoloai temp dirs
)

// LogSource names a structured-log stream emitted by one of yoloai's
// components. Closed set — adding a new source requires both a
// constant here and a producer in the implementation.
//
// Re-exported (type alias) from internal/sandbox/store. Q-Y design
// promised this exposure at the yoloai root so future LogOptions.Sources
// callers don't need to reach into internal packages to construct or
// switch over a typed source list.
type LogSource = store.LogSource

const (
	LogSourceCLI     LogSource = store.LogSourceCLI     // CLI's own structured log
	LogSourceSandbox LogSource = store.LogSourceSandbox // sandbox lifecycle events from the in-container entrypoint
	LogSourceMonitor LogSource = store.LogSourceMonitor // agent idle/active detector emissions
	LogSourceHooks   LogSource = store.LogSourceHooks   // hook-emitted events (Claude Code hooks; future agents too)
)

// MountSpec describes a bind mount from the host filesystem into the
// sandbox. Host and Container are paths; ReadOnly controls write access.
// Re-exported (type alias) from internal/runtime so embedders constructing
// RunOptions.Mounts (when that field lands) don't need to reach into
// internal packages. Q-Y. Direction is explicit in the field names —
// Host is the path on the host, Container is the path inside the
// sandbox — so the call site reads clearly without consulting docs.
type MountSpec = runtime.MountSpec

// PortMapping describes a host-to-sandbox port forwarding. HostPort and
// ContainerPort are integer port numbers; Protocol defaults to "tcp"
// when empty. Re-exported (type alias) from internal/runtime. Q-Y.
// The `Port` suffix is deliberate — without it, an int field named
// "Host" would read ambiguously (hostname? address?) where "HostPort"
// is self-documenting.
type PortMapping = runtime.PortMapping
