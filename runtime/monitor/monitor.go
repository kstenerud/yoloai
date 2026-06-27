// ABOUTME: Embeds Python scripts (status-monitor, sandbox-setup, setup_helpers,
// ABOUTME: tmux_io, diagnose-idle) and exposes them for all runtime backends to install.
// Package monitor embeds the Python status monitor script and the
// consolidated sandbox setup script shared across all runtime backends
// (Docker, Tart, Seatbelt).
package monitor

import _ "embed"

//go:embed status-monitor.py
var embeddedStatusMonitor []byte

//go:embed diagnose-idle.sh
var embeddedDiagnoseIdle []byte

//go:embed sandbox-setup.py
var embeddedSandboxSetup []byte

//go:embed setup_helpers.py
var embeddedSetupHelpers []byte

//go:embed tmux_io.py
var embeddedTmuxIO []byte

//go:embed agent-run.sh
var embeddedAgentRun []byte

//go:embed yoloai-resume.sh
var embeddedYoloaiResume []byte

// Script returns the embedded status-monitor.py content.
func Script() []byte {
	return embeddedStatusMonitor
}

// DiagnoseScript returns the embedded diagnose-idle.sh content.
func DiagnoseScript() []byte {
	return embeddedDiagnoseIdle
}

// SetupScript returns the embedded sandbox-setup.py content.
func SetupScript() []byte {
	return embeddedSandboxSetup
}

// SetupHelpers returns the embedded setup_helpers.py content. The helpers
// module is imported by sandbox-setup.py at runtime, so backends must write
// it alongside sandbox-setup.py in the sandbox bin dir.
func SetupHelpers() []byte {
	return embeddedSetupHelpers
}

// TmuxIO returns the embedded tmux_io.py content. The module is imported by
// sandbox-setup.py at runtime (and by status-monitor.py when wired in), so
// backends must write it alongside sandbox-setup.py in the sandbox bin dir.
func TmuxIO() []byte {
	return embeddedTmuxIO
}

// AgentRunScript returns the embedded agent-run.sh wrapper. It is the
// fall-to-shell launch wrapper (D96): backends must install it executable in
// the sandbox bin dir alongside status-monitor.py, which it invokes via
// `--write-status done` to record the agent's authoritative `done` on exit.
func AgentRunScript() []byte {
	return embeddedAgentRun
}

// YoloaiResumeScript returns the embedded yoloai-resume script. It is the
// in-sandbox resume command (D96 DD4): run from the fall-to-shell shell, it
// relaunches the agent (continuing the prior conversation where supported)
// through agent-run.sh. Backends install it executable in the sandbox bin dir
// as `yoloai-resume` (no extension).
func YoloaiResumeScript() []byte {
	return embeddedYoloaiResume
}
