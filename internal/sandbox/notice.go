// ABOUTME: Notice is a user-facing advisory message returned by orchestration
// ABOUTME: methods instead of being written to a coupled output Writer (F8). The
// ABOUTME: caller (CLI / embedder) decides how to render it.

package sandbox

import (
	"bytes"
	"fmt"
	"strings"
)

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

// noticeWriter adapts the notices accumulator onto io.Writer: each newline-
// terminated line written becomes one Notice at the configured level. It lets a
// streaming helper that only knows how to Fprintf a warning (filterAvailablePorts
// on the restart path) feed the structured-notice channel instead of a raw
// stream — so the restart path shares one port-filter implementation with Create
// (which writes to a real io.Writer) while still surfacing warnings through the
// Start/Reset result's Notices. F8.
type noticeWriter struct {
	n     *notices
	level NoticeLevel
	buf   []byte
}

// Write splits the incoming bytes on newlines and appends each complete,
// non-blank line as a Notice. A trailing partial line (no newline yet) is held
// in buf until the next Write completes it; the helpers that write here always
// newline-terminate, so nothing is lost in practice.
func (w *noticeWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		if line := strings.TrimSpace(string(w.buf[:i])); line != "" {
			w.n.list = append(w.n.list, Notice{Level: w.level, Message: line})
		}
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
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
