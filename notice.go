// ABOUTME: Re-exports of the structured-notice types (F8) so embedders receive
// ABOUTME: orchestration messages as data on result types instead of the library
// ABOUTME: writing them to a coupled output Writer.

package yoloai

import "github.com/kstenerud/yoloai/internal/sandbox"

// Notice is a user-facing advisory message returned on an orchestration
// result. Re-exported (type alias) from internal/sandbox.
type Notice = sandbox.Notice

// NoticeLevel classifies a Notice (info vs warning) for rendering.
// Re-exported (type alias) from internal/sandbox.
type NoticeLevel = sandbox.NoticeLevel

const (
	// NoticeInfo is an informational status message.
	NoticeInfo NoticeLevel = sandbox.NoticeInfo
	// NoticeWarn is a warning the user should heed.
	NoticeWarn NoticeLevel = sandbox.NoticeWarn
)

// DestroyResult reports the outcome of a Destroy — any advisory notices emitted
// (e.g. a directory that couldn't be fully removed). Re-exported (type alias)
// from internal/sandbox.
type DestroyResult = sandbox.DestroyResult

// StartResult reports the outcome of a Start/Restart — the advisory/status
// notices emitted (e.g. "Sandbox X started"). Re-exported (type alias) from
// internal/sandbox.
type StartResult = sandbox.StartResult

// ResetResult reports the outcome of a Reset — the advisory/status notices
// emitted. Re-exported (type alias) from internal/sandbox.
type ResetResult = sandbox.ResetResult
