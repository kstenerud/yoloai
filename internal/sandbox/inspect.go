// ABOUTME: Façade re-exports of the sandbox read-model. The implementation lives
// ABOUTME: in the status/ leaf; these aliases keep the public sandbox API stable.
package sandbox

import (
	"github.com/kstenerud/yoloai/internal/sandbox/state"
	"github.com/kstenerud/yoloai/internal/sandbox/status"
)

// Status represents the current state of a sandbox. See status.Status.
type Status = status.Status

// Status constants for sandbox lifecycle states. See status package.
const (
	StatusActive      = status.StatusActive      // container running, agent actively working
	StatusIdle        = status.StatusIdle        // container running, agent awaiting input
	StatusDone        = status.StatusDone        // agent exited cleanly (exit 0)
	StatusFailed      = status.StatusFailed      // agent exited with error (non-zero)
	StatusStopped     = status.StatusStopped     // container stopped
	StatusSuspended   = status.StatusSuspended   // VM suspended (Tart only)
	StatusRemoved     = status.StatusRemoved     // container removed but sandbox dir exists
	StatusBroken      = status.StatusBroken      // sandbox dir exists but environment.json missing/invalid
	StatusUnavailable = status.StatusUnavailable // backend not running
)

// AgentStatus represents the agent's activity state. See status.AgentStatus.
type AgentStatus = status.AgentStatus

// AgentStatus constants. See status package.
const (
	AgentStatusUnknown = status.AgentStatusUnknown
	AgentStatusActive  = status.AgentStatusActive
	AgentStatusIdle    = status.AgentStatusIdle
	AgentStatusDone    = status.AgentStatusDone
	AgentStatusFailed  = status.AgentStatusFailed
)

// Info holds the combined metadata and live state for a sandbox. See status.Info.
type Info = status.Info

// WorkDataState classifies what a sandbox directory holds. See status.WorkDataState.
type WorkDataState = status.WorkDataState

// WorkDataState constants. See status package.
const (
	WorkDataNone      = status.WorkDataNone
	WorkDataPresent   = status.WorkDataPresent
	WorkDataAmbiguous = status.WorkDataAmbiguous
)

// DirSize recursively calculates the total size under a path. See status.DirSize.
var DirSize = status.DirSize

// ProbeWorkData inspects a sandbox dir for recoverable data. See status.ProbeWorkData.
var ProbeWorkData = status.ProbeWorkData

// ContainerUser resolves the in-container user for a sandbox. See status.ContainerUser.
var ContainerUser = status.ContainerUser

// ExecInContainer runs a command inside a sandbox instance. See status.ExecInContainer.
var ExecInContainer = status.ExecInContainer

// DetectStatus queries runtime + agent-status.json for sandbox status. See status.DetectStatus.
var DetectStatus = status.DetectStatus

// InspectSandbox loads metadata and queries the runtime. See status.InspectSandbox.
var InspectSandbox = status.InspectSandbox

// InspectSandboxWithBackend loads metadata, optionally querying runtime. See status.InspectSandboxWithBackend.
var InspectSandboxWithBackend = status.InspectSandboxWithBackend

// ListSandboxes returns info for all sandboxes. See status.ListSandboxes.
var ListSandboxes = status.ListSandboxes

// ListSandboxesMultiBackend inspects sandboxes per their backends. See status.ListSandboxesMultiBackend.
var ListSandboxesMultiBackend = status.ListSandboxesMultiBackend

// IsolationPerms is re-exported from state. See state.IsolationPerms.
type IsolationPerms = state.IsolationPerms

// Perms is re-exported from state. See state.Perms.
var Perms = state.Perms
