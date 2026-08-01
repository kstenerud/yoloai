//go:build windows

// ABOUTME: Windows stub for the free-space preflight; migration is not a Windows
// ABOUTME: concern (yoloai runs under WSL = linux), so the check reports no limit.
package migrate

import "math"

// FreeBytes reports an unbounded amount on Windows, which is not a migration
// target (yoloai's Windows story is WSL, i.e. linux). Provided only so the
// package compiles under a windows cross-check.
//
// Reporting "unlimited" rather than an error keeps the caller's shape simple,
// and is the same posture SameFilesystem takes here. The real implementation is
// GetDiskFreeSpaceEx, and it is a prerequisite for ever calling Windows
// supported — see DF167, which registers exactly this class of silent gap.
func FreeBytes(_ string) (uint64, error) { return math.MaxUint64, nil }
