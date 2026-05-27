// ABOUTME: Public typed-name aliases (Q-Y): BackendName, AgentName, PruneItemKind.
// ABOUTME: Re-export the internal enum types so embedders importing yoloai don't
// ABOUTME: need to (and cannot) reach into internal/ packages.

package yoloai

import (
	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/runtime"
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
