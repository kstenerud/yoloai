// ABOUTME: Notice is a user-facing advisory message returned by orchestration
// ABOUTME: methods instead of being written to a coupled output Writer (F8). The
// ABOUTME: caller (CLI / embedder) decides how to render it. The type itself now
// ABOUTME: lives in feedback/, below every layer that emits one (D145).

package lifecycle

import (
	"github.com/kstenerud/yoloai/feedback"
)

// NoticeLevel classifies a Notice for rendering — informational status vs. a
// warning the user should heed. See feedback.Level.
type NoticeLevel = feedback.Level

const (
	// NoticeInfo is an informational status message ("Sandbox X resumed").
	NoticeInfo = feedback.LevelInfo
	// NoticeWarn is a warning the user should notice ("could not fully remove …").
	NoticeWarn = feedback.LevelWarn
)

// Notice is a single user-facing message produced by an orchestration method.
// The library formats the message text but returns it on the method's result
// rather than writing to an output Writer, so embedders receive it as data and
// the CLI owns presentation (F8 / Q-F: library returns data, caller renders).
//
// The definition moved to feedback/ so that the layers doing most of the
// emitting — runtime/, store/, copyflow/ — can reach it without importing an
// orchestration package that sits above them (D145). These names stay as
// aliases: they are what the public yoloai surface re-exports.
type Notice = feedback.Notice

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
