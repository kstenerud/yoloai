// ABOUTME: Sandbox is the per-sandbox handle returned by Client.Sandbox(name).
// ABOUTME: Provides scoped sub-handles (currently Network; more to come).

package yoloai

import "context"

// Sandbox is a name-scoped handle for a single sandbox. Methods on
// the handle don't pre-validate that the sandbox exists — reads
// happen lazily when individual operations are invoked, so the
// caller gets a meaningful error from the operation that needs it.
//
// Q-G resolution (Shape B): name-bound handles group per-sandbox
// operations behind one accessor so the Client root stays
// uncluttered. Today only Network() is exposed; the design also
// reserves Workdir() and other sub-handles for future surface.
type Sandbox struct {
	c    *Client
	name string
}

// Sandbox returns a sandbox-scoped handle.
func (c *Client) Sandbox(name string) *Sandbox {
	return &Sandbox{c: c, name: name}
}

// Name returns the sandbox name this handle is bound to. Useful for
// embedders threading the handle through multiple call sites.
func (s *Sandbox) Name() string { return s.name }

// TerminalSnapshot is a captured view of the sandbox's agent tmux pane.
// Both fields are best-effort: if the runtime supplied plain output but
// the ANSI-preserving variant failed, Plain will be populated and ANSI
// will be nil — that's the documented degraded mode (see
// sandbox.Manager.CaptureTerminal).
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
// Backed by sandbox.Manager.CaptureTerminal which uses the runtime's
// non-interactive Exec to invoke `tmux capture-pane`; backend-specific
// socket dispatch is handled inside that primitive. DF3.
func (s *Sandbox) CaptureTerminal(ctx context.Context, scrollback int) (TerminalSnapshot, error) {
	plain, ansi, err := s.c.manager.CaptureTerminal(ctx, s.name, scrollback)
	return TerminalSnapshot{Plain: plain, ANSI: ansi}, err
}
