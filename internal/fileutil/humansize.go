// ABOUTME: Formats a byte count for humans, for messages where the magnitude is
// ABOUTME: the decision ("this will leave 40 GB in trash/").
package fileutil

import "fmt"

// HumanSize renders bytes with a unit chosen so the number stays readable.
//
// Takes uint64 because every producer of a size is one — statfs and a tree walk
// both count upward from zero — and routing those through a signed type buys
// nothing but a conversion the linter is right to flag.
//
// Deliberately coarse: one decimal place, binary units, no padding. Every caller
// is prose in a message a person reads once while deciding whether to proceed,
// where "1.4 GB" is the whole point and "1503238553 bytes" is not.
func HumanSize(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := uint64(unit), 0
	for n := bytes / unit; n >= unit && exp < 3; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGT"[exp])
}
