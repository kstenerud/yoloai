// ABOUTME: Public types for the per-sandbox handle (F2): Info/Status re-exports
// ABOUTME: plus the hand-written option structs (Reset/Destroy/Exec) that map
// ABOUTME: onto the internal sandbox package.

package yoloai

import "github.com/kstenerud/yoloai/internal/sandbox"

// Info is the combined metadata + live state returned by Sandbox.Inspect /
// Client.List. Re-exported (type alias) from internal/sandbox so embedders can
// hold the result without importing internal packages. A richer hand-written
// Info surface (re-exporting Meta / AgentStatus) is a separate follow-up.
type Info = sandbox.Info

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
	// Force proceeds even when the sandbox has active work — a running agent,
	// a dirty workdir, or unapplied commits. With Force false, Destroy refuses
	// such a sandbox with a typed *ActiveWorkError carrying the reason, so the
	// caller can prompt and retry with Force true.
	Force bool
}

// ExecOptions configures Sandbox.Exec. PTY selects between an interactive
// terminal session (PTY true — allocates a remote pty) and raw stdio piping
// (PTY false — line-oriented, the shape the MCP proxy bridges JSON-RPC over).
type ExecOptions struct {
	Command []string // command + args to run inside the container; required
	PTY     bool     // allocate a terminal (true) vs pipe raw stdio (false)
}
