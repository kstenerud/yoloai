// ABOUTME: Public sandbox option types (F1/F3): SandboxCreateOptions (the advanced
// ABOUTME: surface Client.CreateSandbox takes) plus the mapping to the internal struct.

package yoloai

import (
	"fmt"
	"io"

	"github.com/kstenerud/yoloai/internal/sandbox"
)

// Option-mapping convention (IC7).
//
// Public *Options structs translate to internal calls in one of two forms;
// which one applies is decided by a single rule:
//
//   - toInternal(): use it whenever the public struct maps onto exactly one
//     internal counterpart struct. The mapping is a pure value→value method
//     (e.g. SandboxCreateOptions→sandbox.CreateOptions, AgentLogsOptions→
//     sandbox.LogStreamOptions, WorkdirExportOptions→patch.ExportOptions).
//   - inline field-by-field at the call site: only when there is NO single
//     internal struct to map to — either because the verb fans out to several
//     internal structs chosen by runtime state (WorkdirApplyOptions →
//     ApplySeries/ApplyOverlay/ApplyAll; WorkdirDiffOptions → Diff/CommitDiff),
//     or because the fields spread across distinct internal calls
//     (BuildImageOptions).

// SandboxCreateOptions is the public creation surface for Client.CreateSandbox —
// the entry point mirroring every creation capability the CLI exposes,
// built from public re-exported types so external embedders can construct it
// without reaching into internal packages. F1/F3.
//
// Confirmation is never interactive here: Create does not prompt. A dirty
// workdir yields *DirtyWorkdirError unless acked via AllowDirtyWorkdir (or the
// per-directory Workdir.AllowDirty); the CLI catches that, prompts, and retries.
type SandboxCreateOptions struct {
	// Name is the sandbox identifier. Required (no auto-generation).
	Name string

	// Workdir is the primary working directory. Path is required; empty Mode
	// defaults to DirModeCopy (the original is protected via copy/diff/apply).
	Workdir DirSpec

	// AuxDirs are additional directories mounted alongside the workdir.
	AuxDirs []DirSpec

	// AgentType selects the AI agent and is required: an empty AgentType is
	// rejected, not defaulted. An unset agent is a missing required input (not
	// an unsafe one), so the library never picks a default — embedders choose
	// their own. The CLI resolves --agent / config / "claude" at its own edge.
	AgentType AgentType

	// Model selects the model/alias. Empty resolves from config, then the
	// agent default.
	Model string

	// Profile applies a named profile (image, env, settings). Empty = none.
	Profile string

	// Prompt is the task description sent to the agent. Empty = interactive.
	Prompt string

	// PromptFile reads the prompt from a host file instead of Prompt.
	PromptFile string

	// Network sets the network access policy. Default = full access.
	Network NetworkMode

	// NetworkAllow lists allowlisted domains when Network is NetworkModeIsolated.
	NetworkAllow []string

	// Ports forwards host→container ports. Protocol is tcp (the only mode the
	// backend pipeline supports today).
	Ports []PortMapping

	// Replace destroys an existing same-named sandbox first; it must have no
	// unapplied changes (set AbandonUnappliedWork to override that safety check).
	Replace bool

	// AbandonUnappliedWork lets Replace destroy the existing sandbox even when it
	// holds work never applied to the host — a running agent, a dirty workdir, or
	// unapplied commits — skipping that safety check. Mirrors
	// SandboxDestroyOptions.AbandonUnappliedWork. (The CLI's --force flag maps here.)
	AbandonUnappliedWork bool

	// Passthrough are arguments passed to the agent after "--".
	Passthrough []string

	// Debug enables entrypoint debug logging in the container.
	Debug bool

	// CPUs / Memory cap container resources (e.g. "4", "8g").
	CPUs   string
	Memory string

	// Env injects KEY=VAL environment variables into the sandbox.
	Env map[string]string

	// Isolation selects the isolation mode (empty = backend default).
	Isolation IsolationMode

	// Runtimes lists Apple simulator runtimes (e.g. "ios", "tvos:26.1").
	Runtimes []string

	// VscodeTunnel starts a VS Code tunnel in the sandbox.
	VscodeTunnel bool

	// Archetype forces a project archetype (empty = auto-detect).
	Archetype string

	// AllowDirtyWorkdir proceeds even when the workdir has uncommitted git
	// changes, overriding *DirtyWorkdirError for the workdir. OR'd with
	// Workdir.AllowDirty. Aux directories are acked individually via their own
	// DirSpec.AllowDirty.
	AllowDirtyWorkdir bool

	// Output receives the create pipeline's human-readable progress (profile
	// image build stream, advisory warnings). Per-call so concurrent Creates on
	// one Client don't interleave on a shared writer. Nil falls back to the
	// Client's ClientCreateOptions.Output.
	Output io.Writer
}

// toInternal maps the public SandboxCreateOptions onto the internal sandbox struct.
// It folds AllowDirtyWorkdir into the workdir's per-directory AllowDirty and
// defaults an unset workdir Mode to copy. Version and the interactive flags are
// not caller inputs — Client.Create stamps Version from the Client.
func (o SandboxCreateOptions) toInternal() sandbox.CreateOptions {
	workdir := o.Workdir
	if workdir.Mode == "" {
		workdir.Mode = DirModeCopy
	}
	if o.AllowDirtyWorkdir {
		workdir.AllowDirty = true
	}
	return sandbox.CreateOptions{
		Name:                 o.Name,
		Workdir:              workdir,
		AuxDirs:              o.AuxDirs,
		Agent:                string(o.AgentType),
		Model:                o.Model,
		Profile:              o.Profile,
		Prompt:               o.Prompt,
		PromptFile:           o.PromptFile,
		Network:              o.Network,
		NetworkAllow:         o.NetworkAllow,
		Ports:                formatPorts(o.Ports),
		Replace:              o.Replace,
		AbandonUnappliedWork: o.AbandonUnappliedWork,
		Passthrough:          o.Passthrough,
		Debug:                o.Debug,
		CPUs:                 o.CPUs,
		Memory:               o.Memory,
		Env:                  o.Env,
		Isolation:            o.Isolation,
		Runtimes:             o.Runtimes,
		VscodeTunnel:         o.VscodeTunnel,
		Archetype:            o.Archetype,
		Output:               o.Output,
	}
}

// SandboxCloneOptions configures Sandbox.Clone. Source (the receiver sandbox)
// and Dest (the Clone argument) are not fields here — only the optional
// behavior knob is. Overwrite (not "Force") is the concern-specific name per
// the Q-J field audit — "Force" stays a CLI flag only.
type SandboxCloneOptions struct {
	Overwrite bool // destroy the destination first if it already exists
}

// formatPorts renders public PortMappings into the "host:container" strings the
// internal create path parses (parsePortBindings; tcp-only).
func formatPorts(ports []PortMapping) []string {
	if len(ports) == 0 {
		return nil
	}
	out := make([]string, 0, len(ports))
	for _, p := range ports {
		out = append(out, fmt.Sprintf("%d:%d", p.HostPort, p.ContainerPort))
	}
	return out
}
