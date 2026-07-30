// ABOUTME: Notice is a user-facing advisory message returned by orchestration
// ABOUTME: methods instead of being written to a coupled output Writer (F8). The
// ABOUTME: caller (CLI / embedder) decides how to render it.

package lifecycle

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai/internal/orchestrator/launch"
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
// per-call value (not stored on the shared Engine) threaded through helpers
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
// terminated line written becomes one Notice. It lets a streaming helper that
// only knows how to Fprintf to a writer feed the structured-notice channel
// instead of a raw stream — so the restart path shares one implementation of
// each such helper with Create (which passes a real io.Writer) while still
// surfacing the result through the Start/Reset result's Notices. F8.
//
// The level is read from the line, not configured on the writer, because a
// single writer carries both kinds: state.State.Output is the launch path's
// general progress stream, and of its consumers some warn (filterAvailablePorts)
// while others stream an image build. Configuring one level for the whole stream
// therefore mislabels one kind or the other, and it mislabelled the larger:
// every line of BuildKit progress arrived as a warning, which RenderNotices
// sends to stderr and does not suppress under --json (DF157).
//
// Keying on launch.WarningPrefix rather than adding a second sink keeps one
// source of truth for the level — the message text — and it is already the
// source the create path uses, so a helper that says "Warning:" is correct on
// both paths and one that does not is informational on both. The cost is that
// the level is stringly-typed; the benefit is that there is no second channel
// for a future helper to be wired into wrongly, and no way for the two paths to
// disagree about a line they both carry.
type noticeWriter struct {
	notices *notices
	buf     []byte
}

// Write splits the incoming bytes on newlines and appends each complete,
// non-blank line as a Notice. A trailing partial line (no newline yet) is held
// in buf until the next Write completes it; the helpers that write here always
// newline-terminate, so nothing is lost in practice.
//
// The prefix is stripped when it classifies, because RenderNotices re-adds it
// for a NoticeWarn — retaining it here rendered as "Warning: Warning: …".
func (w *noticeWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		if line := strings.TrimSpace(string(w.buf[:i])); line != "" {
			level := NoticeInfo
			if after, found := strings.CutPrefix(line, launch.WarningPrefix); found {
				level, line = NoticeWarn, after
			}
			w.notices.list = append(w.notices.list, Notice{Level: level, Message: line})
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
