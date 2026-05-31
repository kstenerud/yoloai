// ABOUTME: Public types for the per-sandbox handle (F2): Info/Status re-exports
// ABOUTME: plus the hand-written option structs (Reset/Destroy/Exec) that map
// ABOUTME: onto the internal sandbox package.

package yoloai

import "github.com/kstenerud/yoloai/internal/sandbox"

// Info is the combined metadata + live state returned by Sandbox.Inspect /
// Client.List. Hand-written (not a type alias) so its Meta field is the public
// Environment read-model rather than the internal store.Meta — embedders can
// hold the full result without naming any internal type. Built from the
// internal status.Info at the library boundary via infoFromStatus.
type Info struct {
	Meta           *Environment `json:"meta"`
	Status         Status       `json:"status"`
	AgentStatus    AgentStatus  `json:"agent_status,omitempty"`
	HasChanges     string       `json:"has_changes"`
	DiskUsageBytes int64        `json:"disk_usage_bytes"`
}

// Status is a sandbox's lifecycle state. Re-exported (type alias) from
// internal/sandbox; the constants below are the closed set of values.
type Status = sandbox.Status

const (
	StatusActive      Status = sandbox.StatusActive      // container running, agent working
	StatusIdle        Status = sandbox.StatusIdle        // container running, agent awaiting input
	StatusDone        Status = sandbox.StatusDone        // agent exited cleanly (exit 0)
	StatusFailed      Status = sandbox.StatusFailed      // agent exited non-zero
	StatusStopped     Status = sandbox.StatusStopped     // container stopped
	StatusSuspended   Status = sandbox.StatusSuspended   // VM suspended (Tart only)
	StatusRemoved     Status = sandbox.StatusRemoved     // container removed, sandbox dir remains
	StatusBroken      Status = sandbox.StatusBroken      // sandbox dir exists but meta.json missing/invalid
	StatusUnavailable Status = sandbox.StatusUnavailable // backend not running
)

// AgentStatus is the agent's activity state inside a running sandbox, carried
// on Info.AgentStatus. Re-exported (type alias) from internal/sandbox; the
// constants below are the closed set of values. Distinct from Status, which is
// the sandbox/container lifecycle state.
type AgentStatus = sandbox.AgentStatus

const (
	AgentStatusUnknown AgentStatus = sandbox.AgentStatusUnknown // not yet determined
	AgentStatusActive  AgentStatus = sandbox.AgentStatusActive  // actively working
	AgentStatusIdle    AgentStatus = sandbox.AgentStatusIdle    // awaiting input
	AgentStatusDone    AgentStatus = sandbox.AgentStatusDone    // completed its task
	AgentStatusFailed  AgentStatus = sandbox.AgentStatusFailed  // exited with an error
)

// TagInfo identifies a git tag in a sandbox's workdir (its Name and commit
// SHA). Re-exported (type alias) from internal/sandbox so embedders can hold
// the tag-listing results without importing internal packages. The operations
// that return it move onto the Sandbox/Workdir handles in a later step.
type TagInfo = sandbox.TagInfo

// StartOptions configures Sandbox.Start (and Restart). Re-exported (type alias)
// from internal/sandbox — its fields (Resume, Prompt, PromptFile, Isolation,
// VscodeTunnel) are all legitimate start-time knobs, so no field cleanup is
// needed.
type StartOptions = sandbox.StartOptions

// ResetOptions configures Sandbox.Reset. Hand-written rather than aliased: the
// internal struct carries a Name field that the handle now supplies, so it's
// dropped here.
type ResetOptions struct {
	RestartContainer bool // also stop+start the container after resetting (in-place by default)
	ClearState       bool // wipe the agent-runtime directory
	KeepCache        bool // preserve the cache directory
	KeepFiles        bool // preserve the files directory
	NoPrompt         bool // skip re-sending the prompt after reset
	Debug            bool // enable entrypoint debug logging
}

func (o ResetOptions) toInternal(name string) sandbox.ResetOptions {
	return sandbox.ResetOptions{
		Name:       name,
		Restart:    o.RestartContainer,
		ClearState: o.ClearState,
		KeepCache:  o.KeepCache,
		KeepFiles:  o.KeepFiles,
		NoPrompt:   o.NoPrompt,
		Debug:      o.Debug,
	}
}

// DestroyOptions configures Sandbox.Destroy.
type DestroyOptions struct {
	// AbandonUnappliedWork proceeds even when the sandbox holds work that was
	// never applied to the host — a running agent, a dirty workdir, or unapplied
	// commits. With it false, Destroy refuses such a sandbox with a typed
	// *ActiveWorkError carrying the reason, so the caller can prompt and retry.
	// (The CLI's --force flag maps onto this field at the boundary.)
	AbandonUnappliedWork bool
}

// CloneOptions configures Client.Clone. Hand-written rather than aliased so the
// public surface doesn't expose internal/sandbox.CloneOptions. Overwrite (not
// "Force") is the concern-specific name per the Q-J field audit — "Force" stays
// a CLI flag only.
type CloneOptions struct {
	Source    string // existing sandbox name to copy from; required
	Dest      string // new sandbox name; required
	Overwrite bool   // destroy Dest first if it already exists
}

func (o CloneOptions) toInternal() sandbox.CloneOptions {
	return sandbox.CloneOptions{Source: o.Source, Dest: o.Dest, Force: o.Overwrite}
}

// ExecOptions configures Sandbox.Exec. PTY selects between an interactive
// terminal session (PTY true — allocates a remote pty) and raw stdio piping
// (PTY false — line-oriented, the shape the MCP proxy bridges JSON-RPC over).
type ExecOptions struct {
	Command []string // command + args to run inside the container; required
	PTY     bool     // allocate a terminal (true) vs pipe raw stdio (false)
}
