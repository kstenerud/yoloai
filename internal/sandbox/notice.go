// ABOUTME: Notice is a user-facing advisory message returned by orchestration
// ABOUTME: methods instead of being written to a coupled output Writer (F8). The
// ABOUTME: caller (CLI / embedder) decides how to render it.

package sandbox

import "fmt"

// NoticeLevel classifies a Notice for rendering — informational status vs. a
// warning the user should heed.
type NoticeLevel string

const (
	// NoticeInfo is an informational status message ("Sandbox X resumed").
	NoticeInfo NoticeLevel = "info"
	// NoticeWarn is a warning the user should notice ("could not fully remove …").
	NoticeWarn NoticeLevel = "warn"
)

// Notice is a single user-facing message produced by an orchestration method.
// The library formats the message text but returns it on the method's result
// rather than writing to an output Writer, so embedders receive it as data and
// the CLI owns presentation (F8 / Q-F: library returns data, caller renders).
type Notice struct {
	Level   NoticeLevel
	Message string
}

// notices accumulates Notices across an orchestration call and its helpers. A
// per-call value (not stored on the shared Manager) threaded through helpers
// that would otherwise have written to m.output.
type notices struct {
	list []Notice
}

// infof appends an informational notice.
func (n *notices) infof(format string, args ...any) {
	n.list = append(n.list, Notice{Level: NoticeInfo, Message: fmt.Sprintf(format, args...)})
}

// warnf appends a warning notice.
func (n *notices) warnf(format string, args ...any) {
	n.list = append(n.list, Notice{Level: NoticeWarn, Message: fmt.Sprintf(format, args...)})
}

// DestroyResult reports the outcome of a Destroy: any advisory notices emitted
// (e.g. a directory that couldn't be fully removed).
type DestroyResult struct {
	Notices []Notice
}

// StartResult reports the outcome of a Start: the advisory/status notices
// emitted (e.g. "Sandbox X started", "VS Code tunnel enabled").
type StartResult struct {
	Notices []Notice
}

// ResetResult reports the outcome of a Reset: the advisory/status notices
// emitted (e.g. "upgrading to restart", plus the restart's own start notices).
type ResetResult struct {
	Notices []Notice
}
