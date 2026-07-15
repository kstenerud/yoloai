// ABOUTME: Façade aliases re-exporting notice types from lifecycle/ so the
// ABOUTME: leaf carve stays invisible to package orchestrator's callers.
package orchestrator

import "github.com/kstenerud/yoloai/internal/orchestrator/lifecycle"

// NoticeLevel classifies a Notice for rendering. See lifecycle.NoticeLevel.
type NoticeLevel = lifecycle.NoticeLevel

// NoticeInfo is an informational status message. See lifecycle.NoticeInfo.
const NoticeInfo = lifecycle.NoticeInfo

// NoticeWarn is a warning the user should notice. See lifecycle.NoticeWarn.
const NoticeWarn = lifecycle.NoticeWarn

// Notice is a single user-facing message. See lifecycle.Notice.
type Notice = lifecycle.Notice

// DestroyResult reports the outcome of a Destroy. See lifecycle.DestroyResult.
type DestroyResult = lifecycle.DestroyResult

// StartResult reports the outcome of a Start. See lifecycle.StartResult.
type StartResult = lifecycle.StartResult

// ResetResult reports the outcome of a Reset. See lifecycle.ResetResult.
type ResetResult = lifecycle.ResetResult
