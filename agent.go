// ABOUTME: Agent is the per-sandbox sub-handle for agent-interaction verbs —
// ABOUTME: prompt/log reads, terminal capture, input injection, and attach.

package yoloai

import (
	"context"
	"fmt"
	"time"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/yoerrors"
)

// Agent is the agent-interaction sub-handle for a sandbox, reached via
// Sandbox.Agent(). It groups the verbs that read or drive the AI agent
// running inside the sandbox — its configured prompt, its log streams, the
// rendered terminal, typed input, and an interactive attach — under the D53
// "agent" noun (the third noun beside the sandbox itself and Workdir's
// "changes"). Like Workdir/Network it is pure namespace expansion off a
// validated *Sandbox: no IO, no error.
type Agent struct {
	client *Client
	name   string
}

// Prompt returns the prompt text persisted for the sandbox. The bool reports
// whether a prompt was configured: a sandbox with no prompt yields
// ("", false, nil); a present-but-empty prompt yields ("", true, nil). This is
// a host-filesystem read and does not require a running backend.
func (a *Agent) Prompt() (string, bool, error) {
	return sandbox.ReadStoredPrompt(a.client.layout, a.name)
}

// TerminalLog returns the raw agent terminal output for the sandbox. tailLines <= 0
// returns the full log; otherwise the last tailLines lines. A missing log is
// not an error — it returns ("", nil). ANSI escape sequences are left intact;
// the caller decides whether to strip them. This is a host-filesystem read and
// does not require a running backend. It is the recorded counterpart to
// CaptureTerminal's live snapshot.
func (a *Agent) TerminalLog(tailLines int) (string, error) {
	return sandbox.ReadAgentLog(a.client.layout, a.name, tailLines)
}

// LogEvent is one structured-log line surfaced by Logs: the verbatim JSONL byte
// slice (Raw) plus the two fields the library parsed to order and filter the
// stream (Time, Level). Raw is the canonical payload — yoloAI does not decompose
// it into event/message/field parts; a consumer that wants a richer view parses
// Raw itself (the line is JSON), keeping the wire representation unchanged until
// the consumer decides it must change.
type LogEvent struct {
	// Source is which JSONL stream the line came from.
	Source LogSource
	// Time is the frame's "ts", parsed for ordering/filtering. Falls back to
	// the read time when the line carries no parseable timestamp.
	Time time.Time
	// Level is the frame's "level" string, as written.
	Level string
	// Raw is the original JSONL line, verbatim (no trailing newline).
	Raw []byte
}

// AgentLogsOptions selects and filters the LogEvents that Logs emits.
type AgentLogsOptions struct {
	// Sources limits the streamed sources; empty (nil) means all of them.
	Sources []LogSource
	// MinLevel drops events below this level ("debug" < "info" < "warn" <
	// "error"). Empty means no level filter. An unknown value returns a
	// *UsageError from Logs.
	MinLevel string
	// Since drops events older than this instant. Zero means no time filter.
	Since time.Time
	// Follow keeps the stream open after the backlog, delivering new lines as
	// they are written until the agent reaches a terminal state or ctx is
	// cancelled.
	Follow bool
}

// toInternal maps the public AgentLogsOptions onto sandbox.LogStreamOptions (IC7:
// one internal counterpart, so a value→value method rather than inline mapping).
func (o AgentLogsOptions) toInternal() sandbox.LogStreamOptions {
	return sandbox.LogStreamOptions{
		Sources:  o.Sources,
		MinLevel: o.MinLevel,
		Since:    o.Since,
		Follow:   o.Follow,
	}
}

// Logs streams the sandbox's structured-log events in time order. The on-disk
// backlog is delivered first (merged across the requested sources); with
// Follow the channel then stays open, tailing each source until the agent
// reaches a terminal state or ctx is cancelled. The channel is closed when the
// stream ends, so a plain range over it terminates cleanly.
//
// This is a host-filesystem read: no backend connection is required, matching
// TerminalLog. Cancel ctx to stop a Follow stream early. A missing sandbox returns
// ErrSandboxNotFound; an invalid MinLevel returns a *UsageError.
func (a *Agent) Logs(ctx context.Context, opts AgentLogsOptions) (<-chan LogEvent, error) {
	frames, err := sandbox.StreamLogs(ctx, a.client.layout, a.name, opts.toInternal())
	if err != nil {
		return nil, err
	}

	out := make(chan LogEvent, 64)
	go func() {
		defer close(out)
		for f := range frames {
			select {
			case out <- LogEvent{Source: f.Source, Time: f.Time, Level: f.Level, Raw: f.Raw}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// TerminalSnapshot is a captured view of the sandbox's agent tmux pane.
// Both fields are best-effort: if the runtime supplied plain output but
// the ANSI-preserving variant failed, Plain will be populated and ANSI
// will be nil — that's the documented degraded mode (see
// sandbox.Engine.CaptureTerminal).
type TerminalSnapshot struct {
	Plain []byte // tmux capture-pane -p output, printable characters only
	ANSI  []byte // same capture with terminal-control escape sequences preserved (-e flag)
}

// CaptureTerminal grabs the rendered agent tmux pane for diagnostics —
// what `yoloai attach <name>` would show right now, frozen at one
// moment. scrollback is the number of history lines above the visible
// pane to include (DF3's diagnostic depth is 200; pass 0 for just the
// current screen). Returns an error when the sandbox isn't running
// (callers preserving bug reports should treat as best-effort skip,
// not fatal); a partial snapshot (Plain set, ANSI nil) on a successful
// plain capture with a failed ANSI capture.
//
// Backed by sandbox.Engine.CaptureTerminal which uses the runtime's
// non-interactive Exec to invoke `tmux capture-pane`; backend-specific
// socket dispatch is handled inside that primitive. DF3.
func (a *Agent) CaptureTerminal(ctx context.Context, scrollback int) (TerminalSnapshot, error) {
	if err := a.client.ensure(ctx); err != nil {
		return TerminalSnapshot{}, err
	}
	plain, ansi, err := a.client.engine.CaptureTerminal(ctx, a.name, scrollback)
	return TerminalSnapshot{Plain: plain, ANSI: ansi}, err
}

// SendInput appends text to the running sandbox's tmux session as if the user
// typed it. Returns ErrContainerNotRunning when the sandbox is stopped.
func (a *Agent) SendInput(ctx context.Context, text string) error {
	if err := a.client.ensure(ctx); err != nil {
		return err
	}
	return a.client.engine.SendInput(ctx, a.name, text)
}

// ContainerLogs returns the tail of the sandbox's raw container log (roughly
// tailLines lines). Returns "" when the container is gone or logs can't be
// fetched. This is backend container stdout/stderr for diagnostics — distinct
// from the structured agent log stream.
func (a *Agent) ContainerLogs(ctx context.Context, tailLines int) string {
	a.client.tryEnsure(ctx) // best-effort: no backend → no container logs to fetch
	if a.client.runtime == nil {
		return ""
	}
	return runtime.LogsFor(ctx, a.client.runtime, store.InstanceName(a.client.layout.Principal, a.name), tailLines)
}

// Attach connects the supplied IOStreams to the sandbox's tmux session.
// Blocks until the user detaches (Ctrl-B d) or the agent exits. The sandbox
// must be running (Active/Idle/Done/Failed); for stopped sandboxes call Start
// first. io.TTY=true is required; non-TTY attach returns a *UsageError.
func (a *Agent) Attach(ctx context.Context, io IOStreams) error {
	if !io.TTY {
		return yoerrors.NewUsageError("attach requires TTY=true")
	}
	if err := a.client.ensure(ctx); err != nil {
		return err
	}
	info, err := a.client.engine.Inspect(ctx, a.name)
	if err != nil {
		return err
	}
	if err := attachStatusOK(info.Status, a.name); err != nil {
		return err
	}
	containerName := store.InstanceName(a.client.layout.Principal, a.name)
	user := sandbox.ContainerUser(info.Environment, a.client.layout.HostUID)
	if err := sandbox.WaitForAttachReady(ctx, a.client.runtime, a.client.layout, a.name, user, 300*time.Second); err != nil {
		return fmt.Errorf("waiting for tmux session: %w", err)
	}
	sock := sandbox.ReadTmuxSocket(a.client.layout, a.name)
	cmd := a.client.runtime.AttachCommand(sock, io.Rows, io.Cols, info.Environment.Isolation)
	return execExitError(a.client.runtime.InteractiveExec(ctx, containerName, cmd, user, "", io))
}

// attachStatusOK returns nil if the sandbox status permits attach,
// otherwise a typed error suitable for the CLI exit-code mapping.
func attachStatusOK(status sandbox.Status, name string) error {
	switch status {
	case sandbox.StatusActive, sandbox.StatusIdle, sandbox.StatusDone, sandbox.StatusFailed:
		return nil
	default:
		// StatusStopped, StatusRemoved, StatusBroken, StatusUnavailable
		return fmt.Errorf("sandbox %q: %w", name, sandbox.ErrContainerNotRunning)
	}
}
