// ABOUTME: Sandbox is the per-sandbox handle returned by Client.Sandbox(name).
// ABOUTME: Provides scoped sub-handles (currently Network; more to come).

package yoloai

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
