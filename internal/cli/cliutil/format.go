// ABOUTME: Human-readable formatting of sizes and ages for CLI presentation.
// ABOUTME: Rendering lives at the CLI tier; the domain returns structured data.

package cliutil

import (
	"fmt"
	"time"
)

// FormatAge returns a human-readable duration string (e.g., "2h", "3d", "5m")
// for the time elapsed since created.
func FormatAge(created time.Time) string {
	d := time.Since(created)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// FormatDiskUsage renders a sandbox's DiskUsageBytes for display: "-" when the
// size is unknown (a negative sentinel from the domain), otherwise the
// human-readable size.
func FormatDiskUsage(bytes int64) string {
	if bytes < 0 {
		return "-"
	}
	return FormatSize(bytes)
}

// FormatSize returns a human-readable size string (e.g., "1.2GB", "340KB").
func FormatSize(bytes int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.1fGB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1fMB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%dKB", bytes/kb)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
