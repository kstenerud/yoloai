// Package monitor embeds the Python status monitor script shared across
// all runtime backends (Docker, Tart, Seatbelt).
package monitor

import _ "embed"

//go:embed status-monitor.py
var embeddedStatusMonitor []byte

// Script returns the embedded status-monitor.py content.
func Script() []byte {
	return embeddedStatusMonitor
}
