// ABOUTME: Public type surface for the yoloai root package: re-exports of the
// ABOUTME: internal enums (BackendType, AgentType, …), spec types (DirSpec,
// ABOUTME: MountSpec, PortMapping), and orchestration result types (Notice,
// ABOUTME: DestroyResult, …) so embedders never reach into internal/ packages.

package yoloai

import (
	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/store"
)

// BackendType names a runtime backend. Open-set typed string —
// the constants document the backends that ship with yoloai;
// custom builds can register additional names via runtime.Register.
//
// Re-exported (type alias) from internal/runtime so embedders can use
// the type without importing internal packages. Q-Y resolution
// (2026-05-25): public API fields like ClientCreateOptions.BackendType and
// SystemCheckOptions.BackendType take this typed name rather than plain string,
// catching typo-style bugs at the call site rather than at the
// "unknown backend" error.
type BackendType = runtime.BackendType

// Shipped backends. Constants are re-exported as typed-string consts
// so they can be used in case statements and compared cleanly.
//
// New backends registered at runtime via runtime.Register will not
// appear here — that's by design; the constant list documents the
// stable set, not the dynamic registry.
const (
	BackendDocker     BackendType = runtime.BackendDocker
	BackendPodman     BackendType = runtime.BackendPodman
	BackendTart       BackendType = runtime.BackendTart
	BackendSeatbelt   BackendType = runtime.BackendSeatbelt
	BackendContainerd BackendType = runtime.BackendContainerd
)

// Reserved backend selectors. These are not real backends and can never be
// registered as one; they are only meaningful for BuildImageOptions.BackendType,
// where they select a set rather than a single backend.
const (
	// BackendDefault selects the config-resolved container backend.
	BackendDefault BackendType = "default"
	// BackendsAll selects every registered backend.
	BackendsAll BackendType = "all"
)

// AgentType names a coding agent. Open-set typed string — the
// constants document the agents that ship with yoloai; user-defined
// extension agents supply their own name via the agent registry.
//
// Re-exported from internal/agent. Same Q-Y rationale as BackendType:
// public fields like SandboxCreateOptions.AgentType and SystemCheckOptions.AgentType take this
// typed name.
type AgentType = agent.AgentType

const (
	AgentClaude   AgentType = agent.AgentClaude
	AgentCodex    AgentType = agent.AgentCodex
	AgentGemini   AgentType = agent.AgentGemini
	AgentOpenCode AgentType = agent.AgentOpenCode
	AgentAider    AgentType = agent.AgentAider
	AgentTest     AgentType = agent.AgentTest // dev/test helper agent
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
	PruneKindContainer  PruneItemKind = "container"   // docker / podman / containerd
	PruneKindImage      PruneItemKind = "image"       // docker / podman / containerd
	PruneKindVM         PruneItemKind = "vm"          // tart
	PruneKindTempDir    PruneItemKind = "temp_dir"    // yoloai-side: stale ~/.yoloai temp dirs
	PruneKindSandboxDir PruneItemKind = "sandbox_dir" // yoloai-side: never-initialized sandbox dir (no recoverable work)
	PruneKindLockFile   PruneItemKind = "lock_file"   // yoloai-side: orphaned per-sandbox .lock file
	PruneKindStaleBase  PruneItemKind = "stale_base"  // tart: superseded base image (prune --stale-bases)
)

// LogSource names a structured-log stream emitted by one of yoloai's
// components. Closed set — adding a new source requires both a
// constant here and a producer in the implementation.
//
// Re-exported (type alias) from internal/store. Q-Y design
// promised this exposure at the yoloai root so future AgentLogsOptions.Sources
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
// sandbox. HostPath is the path on the host, ContainerPath is the path
// inside the sandbox, ReadOnly controls write access. Re-exported
// (type alias) from internal/runtime so embedders constructing
// SandboxCreateOptions mount specs (when that field lands) don't need to reach
// into internal packages. Q-Y.
//
// The "Path" suffix matches PortMapping's "Port" suffix: Go doesn't
// surface types at the call site, so `m.HostPath` is self-documenting
// in a way that bare `m.Host` is not (a reader of `m.Host` has to look
// up the type to know it isn't a hostname / IP / port). Direction is
// in the prefix (Host vs Container); kind is in the suffix (Path vs
// Port). Same convention as PortMapping below.
type MountSpec = runtime.MountSpec

// PortMapping describes a host-to-sandbox port forwarding. HostPort and
// ContainerPort are integer port numbers; Protocol defaults to "tcp"
// when empty. Re-exported (type alias) from internal/runtime. Q-Y.
//
// Naming mirrors MountSpec above: direction in the prefix (Host /
// Container), kind in the suffix (Path / Port). Without the type-
// carrying suffix an int field named "Host" reads ambiguously
// (hostname? address?) — the suffix makes it self-documenting at any
// call site, regardless of whether Go has inferred the type into
// scope.
type PortMapping = runtime.PortMapping

// IsolationMode names a sandbox isolation mode. Closed set — the
// constants below are the only valid values. Empty value
// (IsolationModeDefault) means "use the backend's BaseMode".
//
// Re-exported (type alias) from internal/runtime. F11 (2026-05-27)
// established this typing so public fields like
// SystemCheckOptions.Isolation and SandboxCreateOptions.Isolation take a
// closed-set typed value, exhaustive-checked at every switch.
type IsolationMode = runtime.IsolationMode

const (
	IsolationModeDefault             IsolationMode = runtime.IsolationModeDefault
	IsolationModeContainer           IsolationMode = runtime.IsolationModeContainer
	IsolationModeContainerEnhanced   IsolationMode = runtime.IsolationModeContainerEnhanced
	IsolationModeContainerPrivileged IsolationMode = runtime.IsolationModeContainerPrivileged
	IsolationModeVM                  IsolationMode = runtime.IsolationModeVM
	IsolationModeVMEnhanced          IsolationMode = runtime.IsolationModeVMEnhanced
	IsolationModeProcess             IsolationMode = runtime.IsolationModeProcess
)

// DirSpec describes a directory to mount in the sandbox: host Path, mount
// Mode, optional container MountPath, and the per-directory safety acks
// (AllowDirty for uncommitted git changes, AllowDangerousPath for the :force
// dangerous-path override). Re-exported (type alias) from internal/sandbox so
// embedders populate SandboxCreateOptions.Workdir / AuxDirs without importing
// internal packages. F1.
type DirSpec = sandbox.DirSpec

// DirMode names how a directory is mounted into the sandbox. Closed set.
// Re-exported (type alias) from internal/sandbox.
type DirMode = sandbox.DirMode

const (
	DirModeCopy    DirMode = sandbox.DirModeCopy    // full copy; diff/apply workflow (default)
	DirModeOverlay DirMode = sandbox.DirModeOverlay // overlayfs upper layer; diff/apply (Docker-only)
	DirModeRW      DirMode = sandbox.DirModeRW      // live read-write bind mount
	DirModeRO      DirMode = sandbox.DirModeRO      // read-only bind mount
)

// NetworkMode names a sandbox's network access policy. Closed set.
// Re-exported (type alias) from internal/sandbox.
type NetworkMode = sandbox.NetworkMode

const (
	NetworkModeDefault  NetworkMode = sandbox.NetworkModeDefault  // full network access
	NetworkModeNone     NetworkMode = sandbox.NetworkModeNone     // no network access
	NetworkModeIsolated NetworkMode = sandbox.NetworkModeIsolated // allowlist only
)

// Notice is a user-facing advisory message returned on an orchestration
// result. Re-exported (type alias) from internal/sandbox.
type Notice = sandbox.Notice

// NoticeLevel classifies a Notice (info vs warning) for rendering.
// Re-exported (type alias) from internal/sandbox.
type NoticeLevel = sandbox.NoticeLevel

const (
	// NoticeInfo is an informational status message.
	NoticeInfo NoticeLevel = sandbox.NoticeInfo
	// NoticeWarn is a warning the user should heed.
	NoticeWarn NoticeLevel = sandbox.NoticeWarn
)

// DestroyResult reports the outcome of a Destroy — any advisory notices emitted
// (e.g. a directory that couldn't be fully removed). Re-exported (type alias)
// from internal/sandbox.
type DestroyResult = sandbox.DestroyResult

// StartResult reports the outcome of a Start/Restart — the advisory/status
// notices emitted (e.g. "Sandbox X started"). Re-exported (type alias) from
// internal/sandbox.
type StartResult = sandbox.StartResult

// ResetResult reports the outcome of a Reset — the advisory/status notices
// emitted. Re-exported (type alias) from internal/sandbox.
type ResetResult = sandbox.ResetResult
