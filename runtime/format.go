// ABOUTME: Shared human-readable byte formatting for backend prune/disk reporting,
// ABOUTME: so every backend renders reclaim/usage figures in identical units.
package runtime

import "fmt"

// FormatBytes renders a byte count as a human-readable string: GB (2 decimals)
// at >= 1 GiB, MB (1 decimal) at >= 1 MiB, otherwise raw bytes. The units are
// binary (1024-based) despite the GB/MB labels, matching what `docker system df`
// and the disk reporting have always shown.
func FormatBytes(b int64) string {
	const (
		mb = 1024 * 1024
		gb = 1024 * 1024 * 1024
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
