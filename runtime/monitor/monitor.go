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
