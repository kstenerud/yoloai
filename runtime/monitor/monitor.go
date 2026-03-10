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
